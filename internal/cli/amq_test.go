package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func withAMQCommandSeams(t *testing.T, env amqEnv, output string) *[]amqCommandRequest {
	t.Helper()
	var calls []amqCommandRequest
	prevEnv := resolveAMQEnvForAMQCommand
	prevRun := runAMQCommand
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		got := env
		if strings.TrimSpace(rootFlag) != "" {
			got.Root = rootFlag
		} else {
			got.Root = strings.ReplaceAll(got.Root, "{session}", session)
		}
		got.SessionName = session
		got.Me = handle
		if got.BaseRoot == "" {
			got.BaseRoot = ".agent-mail"
		}
		return got, nil
	}
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		calls = append(calls, req)
		result := output
		if len(req.Arg) > 0 {
			switch req.Arg[0] {
			case "list":
				// Durable reply resolution intentionally uses the non-mutating
				// AMQ 0.42.1+ list --json shape, never `amq read`.
				if containsString(req.Arg, "--new") {
					result = `[{"id":"q1","from":"cto","thread":"p2p/cto__user","box":"new","path":"inbox/new/q1.md"},{"id":"msg1","from":"qa","thread":"p2p/lead__qa","box":"new","path":"inbox/new/msg1.md"}]` + "\n"
				} else if containsString(req.Arg, "--cur") {
					result = "[]\n"
				}
			case "send":
				if parseSentMessageID(result) == "" {
					result = "Sent fixture-msg to " + amqFlagValue(req.Arg, "to") + "\n"
				}
			case "reply":
				if parseSentMessageID(result) == "" {
					result = "Replied fixture-reply to fixture-recipient\n"
				}
			}
		}
		return []byte(result), nil
	}
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = prevEnv
		runAMQCommand = prevRun
	})
	return &calls
}

func TestDefaultRunAMQCommandDisablesChildUpdateCheckWithInheritedEnv(t *testing.T) {
	t.Setenv("AMQ_NO_UPDATE_CHECK", "0")
	setupFakeAMQScript(t, `#!/bin/sh
if [ "$AMQ_NO_UPDATE_CHECK" != "1" ]; then
  echo "update available" >&2
  exit 91
fi
printf '%s\n' '{"clean":true}'
`)

	out, err := defaultRunAMQCommand(amqCommandRequest{Dir: t.TempDir(), Arg: []string{"ops", "--json"}})
	if err != nil {
		t.Fatalf("defaultRunAMQCommand: %v", err)
	}
	if got, want := string(out), "{\"clean\":true}\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
	if got := os.Getenv("AMQ_NO_UPDATE_CHECK"); got != "0" {
		t.Fatalf("parent AMQ_NO_UPDATE_CHECK = %q, want unchanged 0", got)
	}
}

func TestAMQRouteBuildsRouteExplain(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, `{"routable":true}`+"\n")

	stdout, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"route", "--session", "issue-96", "--me", "cto", "--to", "fullstack", "--target-session", "review", "--json"})
	})
	if err != nil {
		t.Fatalf("amq route: %v", err)
	}
	if !strings.Contains(stdout, `"routable":true`) {
		t.Fatalf("route output = %q", stdout)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"route explain", "--from-root", ".agent-mail/issue-96", "--me cto", "--to fullstack", "--session review", "--json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("route args missing %q: %s", want, got)
		}
	}
}

func TestResolveAMQContextForProjectIsPrimaryResolver(t *testing.T) {
	dir := t.TempDir()
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail", AMQVersion: "0.38.0"}, "ok\n")
	ctx, err := resolveAMQContextForProject(dir, "issue-96", "cto")
	if err != nil {
		t.Fatalf("resolveAMQContextForProject: %v", err)
	}
	if !sameResolvedDir(ctx.ProjectDir, dir) {
		t.Fatalf("ProjectDir = %q, want %q", ctx.ProjectDir, dir)
	}
	if ctx.Me != "cto" || ctx.Env.SessionName != "issue-96" {
		t.Fatalf("identity/session not resolved: %+v", ctx)
	}
	if !strings.HasSuffix(ctx.Root, ".agent-mail/issue-96") {
		t.Fatalf("Root = %q, want session root", ctx.Root)
	}
	base, err := resolveAMQBaseRootForProject(dir, "issue-96", "cto")
	if err != nil {
		t.Fatalf("resolveAMQBaseRootForProject: %v", err)
	}
	if !strings.HasSuffix(base, ".agent-mail") {
		t.Fatalf("BaseRoot = %q, want .agent-mail container", base)
	}
}

func TestResolveAMQContextNormalizesRelativeRootsToAbsoluteEnv(t *testing.T) {
	dir := t.TempDir()
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail", AMQVersion: "0.41.0"}, "ok\n")

	ctx, err := resolveAMQContextForProject(dir, "issue-96", "cto")
	if err != nil {
		t.Fatalf("resolveAMQContextForProject: %v", err)
	}
	wantRoot := filepath.Join(dir, ".agent-mail", "issue-96")
	wantBase := filepath.Join(dir, ".agent-mail")
	if ctx.Root != wantRoot {
		t.Fatalf("ctx.Root = %q, want absolute %q", ctx.Root, wantRoot)
	}
	env := amqCommandEnv(ctx)
	if !envHas(env, "AM_ROOT", wantRoot) {
		t.Fatalf("AM_ROOT not injected as absolute root: %v", env)
	}
	if !envHas(env, "AM_BASE_ROOT", wantBase) {
		t.Fatalf("AM_BASE_ROOT not injected as absolute base root: %v", env)
	}
}

func TestAMQCommandEnvBuildsCompleteIdentityTuples(t *testing.T) {
	t.Setenv("AM_ROOT", "/stale/root")
	t.Setenv("AM_BASE_ROOT", "/stale/base")
	t.Setenv("AM_SESSION", "stale")
	t.Setenv("AM_ME", "stale")
	t.Setenv("AMQ_GLOBAL_ROOT", "/stale/global")

	t.Run("sessionful default profile", func(t *testing.T) {
		ctx := amqContext{
			ProjectDir: "/project",
			Profile:    team.DefaultProfile,
			Env:        amqEnv{BaseRoot: "/project/.agent-mail"},
			Root:       "/project/.agent-mail/issue-96",
			Me:         "cto",
			Session:    "issue-96",
			PinMode:    amqPinSessionful,
		}
		env := amqCommandEnv(ctx)
		for key, want := range map[string]string{
			"AM_ROOT": "/project/.agent-mail/issue-96", "AM_BASE_ROOT": "/project/.agent-mail", "AM_SESSION": "issue-96", "AM_ME": "cto",
		} {
			if !envHas(env, key, want) {
				t.Fatalf("missing %s=%q in %#v", key, want, env)
			}
		}
		if envHasPrefix(env, "AMQ_GLOBAL_ROOT", "") {
			t.Fatalf("stale AMQ_GLOBAL_ROOT leaked: %#v", env)
		}
	})

	t.Run("exact root named profile", func(t *testing.T) {
		root := "/project/.agent-mail/review/issue-96"
		ctx := amqContext{ProjectDir: "/project", Profile: "review", Root: root, Me: "cto", Session: "issue-96", PinMode: amqPinExactRoot}
		env := amqCommandEnv(ctx)
		for key, want := range map[string]string{"AM_ROOT": root, "AM_BASE_ROOT": root, "AM_ME": "cto"} {
			if !envHas(env, key, want) {
				t.Fatalf("missing %s=%q in %#v", key, want, env)
			}
		}
		if envHasPrefix(env, "AM_SESSION", "") {
			t.Fatalf("exact-root tuple must omit AM_SESSION: %#v", env)
		}
	})
}

func TestResolveAMQContextInfersNamedProfileBeforeAMQResolution(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".agent-mail", "review", "issue-96")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	writeAMQBoundaryTeamProfile(t, dir, "review")
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_BASE_ROOT", root)
	if err := os.Unsetenv("AM_SESSION"); err != nil {
		t.Fatal(err)
	}

	previous := resolveAMQEnvForAMQCommand
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		if cwd != dir || rootFlag != root || session != "" {
			t.Fatalf("named root resolver args = cwd=%q root=%q session=%q, want exact root without --session", cwd, rootFlag, session)
		}
		return amqEnv{Root: root, BaseRoot: root, Me: handle, AMQVersion: "0.43.0"}, nil
	}
	t.Cleanup(func() { resolveAMQEnvForAMQCommand = previous })

	ctx, err := resolveAMQContext(dir, "", "issue-96", "cto", true)
	if err != nil {
		t.Fatalf("resolveAMQContext: %v", err)
	}
	if ctx.Profile != "review" || ctx.PinMode != amqPinExactRoot {
		t.Fatalf("context = %+v, want named exact-root context", ctx)
	}
	if env := amqCommandEnv(ctx); envHasPrefix(env, "AM_SESSION", "") || !envHas(env, "AM_BASE_ROOT", root) {
		t.Fatalf("named exact-root tuple = %#v", env)
	}
}

func TestNamedProfileFromInheritedAMQRootFailsClosed(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".agent-mail")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Run("matching default session remains non-inference", func(t *testing.T) {
		root := filepath.Join(base, "issue-96")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AM_ROOT", root)
		profile, ok, err := namedProfileFromInheritedAMQRoot(dir, "issue-96")
		if err != nil || ok || profile != "" {
			t.Fatalf("default-session inference = profile %q, ok %t, err %v; want non-inference", profile, ok, err)
		}
	})
	t.Run("symlinked default session rewrites identity inside selected base", func(t *testing.T) {
		actual := filepath.Join(base, "actual-default-session")
		if err := os.MkdirAll(actual, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(actual, filepath.Join(base, "issue-default-alias")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Setenv("AM_ROOT", filepath.Join(base, "issue-default-alias"))
		if _, _, err := namedProfileFromInheritedAMQRoot(dir, "issue-default-alias"); err == nil || !strings.Contains(err.Error(), "does not preserve selected .agent-mail identity") {
			t.Fatalf("rewritten default inherited root error = %v, want identity-preservation failure", err)
		}
	})
	t.Run("outside project", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), ".agent-mail", "review", "issue-96")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AM_ROOT", root)
		if _, _, err := namedProfileFromInheritedAMQRoot(dir, "issue-96"); err == nil {
			t.Fatal("outside inherited root accepted")
		}
	})
	t.Run("session mismatch", func(t *testing.T) {
		root := filepath.Join(base, "review", "other")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AM_ROOT", root)
		if _, _, err := namedProfileFromInheritedAMQRoot(dir, "issue-96"); err == nil {
			t.Fatal("mismatched inherited session accepted")
		}
	})
	t.Run("default profile path collision", func(t *testing.T) {
		root := filepath.Join(base, team.DefaultProfile, "issue-96")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("AM_ROOT", root)
		if _, _, err := namedProfileFromInheritedAMQRoot(dir, "issue-96"); err == nil {
			t.Fatal("colliding default-profile path accepted")
		}
	})
	t.Run("unresolvable inherited root", func(t *testing.T) {
		t.Setenv("AM_ROOT", filepath.Join(base, "review", "missing"))
		if _, _, err := namedProfileFromInheritedAMQRoot(dir, "issue-96"); err == nil || !strings.Contains(err.Error(), "cannot resolve inherited AM_ROOT") {
			t.Fatalf("missing inherited root error = %v, want resolution failure", err)
		}
	})
	t.Run("symlinked profile escapes selected base", func(t *testing.T) {
		outside := t.TempDir()
		if err := os.MkdirAll(filepath.Join(outside, "issue-96"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(base, "review-escape")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		root := filepath.Join(base, "review-escape", "issue-96")
		t.Setenv("AM_ROOT", root)
		if _, _, err := namedProfileFromInheritedAMQRoot(dir, "issue-96"); err == nil || !strings.Contains(err.Error(), "resolves outside selected .agent-mail base") {
			t.Fatalf("escaped inherited root error = %v, want containment failure", err)
		}
	})
	t.Run("symlinked profile rewrites identity inside selected base", func(t *testing.T) {
		actual := filepath.Join(base, "actual", "issue-96")
		if err := os.MkdirAll(actual, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(base, "actual"), filepath.Join(base, "review-alias")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		root := filepath.Join(base, "review-alias", "issue-96")
		t.Setenv("AM_ROOT", root)
		if _, _, err := namedProfileFromInheritedAMQRoot(dir, "issue-96"); err == nil || !strings.Contains(err.Error(), "does not preserve selected .agent-mail identity") {
			t.Fatalf("rewritten inherited root error = %v, want identity-preservation failure", err)
		}
	})
	t.Run("symlinked named session rewrites identity inside selected base", func(t *testing.T) {
		actual := filepath.Join(base, "review-session", "actual")
		if err := os.MkdirAll(actual, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(actual, filepath.Join(base, "review-session", "issue-96")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		root := filepath.Join(base, "review-session", "issue-96")
		t.Setenv("AM_ROOT", root)
		if _, _, err := namedProfileFromInheritedAMQRoot(dir, "issue-96"); err == nil || !strings.Contains(err.Error(), "does not preserve selected .agent-mail identity") {
			t.Fatalf("rewritten named-session root error = %v, want identity-preservation failure", err)
		}
	})
	t.Run("selected base symlink is canonical namespace", func(t *testing.T) {
		project := t.TempDir()
		resolvedBase := t.TempDir()
		root := filepath.Join(resolvedBase, "review", "issue-96")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(resolvedBase, filepath.Join(project, ".agent-mail")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		t.Setenv("AM_ROOT", filepath.Join(project, ".agent-mail", "review", "issue-96"))
		profile, ok, err := namedProfileFromInheritedAMQRoot(project, "issue-96")
		if err != nil || !ok || profile != "review" {
			t.Fatalf("base symlink inference = profile %q, ok %t, err %v", profile, ok, err)
		}
	})
}

func TestResolveAMQContextRefusesInheritedNamedProfileSymlinkEscape(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".agent-mail")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "issue-96"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(base, "review")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("AM_ROOT", filepath.Join(base, "review", "issue-96"))
	t.Setenv("AM_BASE_ROOT", filepath.Join(base, "review", "issue-96"))

	previous := resolveAMQEnvForAMQCommand
	resolveAMQEnvForAMQCommand = func(string, string, string, string) (amqEnv, error) {
		t.Fatal("AMQ resolver must not run after inherited root escapes selected base")
		return amqEnv{}, nil
	}
	t.Cleanup(func() { resolveAMQEnvForAMQCommand = previous })

	if _, err := resolveAMQContext(dir, "", "issue-96", "qa", true); err == nil || !strings.Contains(err.Error(), "resolves outside selected .agent-mail base") {
		t.Fatalf("resolveAMQContext symlink escape error = %v, want containment failure", err)
	}
}

func TestResolveAMQContextRefusesInheritedNamedProfileSymlinkIdentityRewrite(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".agent-mail")
	actual := filepath.Join(base, "actual", "issue-96")
	if err := os.MkdirAll(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(base, "actual"), filepath.Join(base, "review")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("AM_ROOT", filepath.Join(base, "review", "issue-96"))
	t.Setenv("AM_BASE_ROOT", filepath.Join(base, "review", "issue-96"))

	previous := resolveAMQEnvForAMQCommand
	resolveAMQEnvForAMQCommand = func(string, string, string, string) (amqEnv, error) {
		t.Fatal("AMQ resolver must not run after inherited root rewrites namespace identity")
		return amqEnv{}, nil
	}
	t.Cleanup(func() { resolveAMQEnvForAMQCommand = previous })

	if _, err := resolveAMQContext(dir, "", "issue-96", "qa", true); err == nil || !strings.Contains(err.Error(), "does not preserve selected .agent-mail identity") {
		t.Fatalf("resolveAMQContext symlink identity rewrite error = %v, want identity-preservation failure", err)
	}
}

func TestResolveAMQContextRefusesInheritedDefaultSessionSymlinkIdentityRewrite(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, ".agent-mail")
	actual := filepath.Join(base, "actual")
	if err := os.MkdirAll(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(actual, filepath.Join(base, "issue-96")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Setenv("AM_ROOT", filepath.Join(base, "issue-96"))
	t.Setenv("AM_BASE_ROOT", base)
	t.Setenv("AM_SESSION", "issue-96")

	previous := resolveAMQEnvForAMQCommand
	resolveAMQEnvForAMQCommand = func(string, string, string, string) (amqEnv, error) {
		t.Fatal("AMQ resolver must not run after inherited default session rewrites namespace identity")
		return amqEnv{}, nil
	}
	t.Cleanup(func() { resolveAMQEnvForAMQCommand = previous })

	if _, err := resolveAMQContext(dir, "", "issue-96", "qa", true); err == nil || !strings.Contains(err.Error(), "does not preserve selected .agent-mail identity") {
		t.Fatalf("resolveAMQContext default-session symlink identity rewrite error = %v, want identity-preservation failure", err)
	}
}

func TestAMQRouteAddsJSONByDefault(t *testing.T) {
	chdir(t, t.TempDir())
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, `{"routable":true}`+"\n")

	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"route", "--session", "issue-96", "--me", "cto", "--to", "fullstack"})
	})
	if err != nil {
		t.Fatalf("amq route: %v", err)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"route explain", "--to fullstack", "--json"} {
		if !strings.Contains(got, want) {
			t.Fatalf("route args missing %q: %s", want, got)
		}
	}
}

func TestAMQRejectsUnknownSubcommandWithCompleteCanonicalSurface(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runAMQ([]string{"frobnicate"})
	})
	if err == nil {
		t.Fatal("runAMQ() error = nil, want unknown subcommand error")
	}
	const want = "unknown 'amq' subcommand: \"frobnicate\". Try 'env' or 'route'."
	if got := err.Error(); got != want {
		t.Fatalf("runAMQ() error = %q, want %q", got, want)
	}
}

func installAMQWaitPostureRuntime(t *testing.T, dir, session, gate string) string {
	t.Helper()
	t.Setenv("AM_ME", "cto")
	t.Setenv("TMUX_PANE", "%7")
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionLeadPane
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectTeam, Operator: &op,
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: session}, {Role: "qa", Binary: "claude", Handle: "qa", Session: session}},
	}); err != nil {
		t.Fatal(err)
	}
	projectRoot, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(projectRoot, ".agent-mail", session)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	previousActor := waitPostureResolveCurrentActor
	waitPostureResolveCurrentActor = func(projectDir, profile, requestedSession string, _ team.Team) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: "cto", Handle: "cto", Profile: profile, Session: requestedSession, Root: filepath.Join(projectDir, ".agent-mail", requestedSession), PaneID: "%7"}, nil
	}
	t.Cleanup(func() { waitPostureResolveCurrentActor = previousActor })
	threads := []state.ThreadSummary{}
	if gate != "" {
		threads = append(threads, state.ThreadSummary{ID: gate, OperatorGate: &state.OperatorGateSignal{From: "cto", Since: time.Now()}})
	}
	previousSnapshot := waitPostureLoadSnapshot
	waitPostureLoadSnapshot = func(projectDir, _ string) (state.Snapshot, error) {
		selectedRoot := filepath.Join(projectDir, ".agent-mail", session)
		return state.Snapshot{Sessions: []state.Session{{Name: session, TeamProfile: team.DefaultProfile, NamespaceID: team.DefaultProfile + "/" + session, Root: selectedRoot, Coordination: state.Coordination{Threads: threads}}}}, nil
	}
	t.Cleanup(func() { waitPostureLoadSnapshot = previousSnapshot })
	return root
}

func writeAMQBoundaryTeamProfile(t *testing.T, dir, profile string) {
	t.Helper()
	if err := team.WriteProfile(dir, profile, team.Team{
		Project:       dir,
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectTeam,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func envHas(env []string, key, val string) bool {
	return containsString(env, key+"="+val)
}

func envHasPrefix(env []string, key, valSubstr string) bool {
	for _, e := range env {
		if strings.HasPrefix(e, key+"=") && strings.Contains(e, valSubstr) {
			return true
		}
	}
	return false
}

func TestDurableSendWaitPostureUsesLastWaitFlagsAndPreservesNoWait(t *testing.T) {
	ctx := amqContext{ProjectDir: t.TempDir(), Session: "issue-96", Root: "/tmp/root"}
	req, err := durableSendWaitPosture("amq send", ctx, []string{"--to", "qa", "--subject", "review", "--wait-for", "none", "--wait-for", "drained", "--wait-timeout", "30s", "--wait-timeout", "10m"}, false, "")
	if err != nil || !req.Blocking || req.Timeout != 10*time.Minute {
		t.Fatalf("last blocking send flags: req=%+v err=%v", req, err)
	}
	req, err = durableSendWaitPosture("amq send", ctx, []string{"--to", "qa", "--subject", "review", "--wait-for", "drained", "--wait-for", "none", "--wait-timeout", "-1s"}, false, "")
	if err != nil || req.Blocking {
		t.Fatalf("last no-wait send must not block: req=%+v err=%v", req, err)
	}
	if _, err := durableSendWaitPosture("amq send", ctx, []string{"--subject", "--wait-for", "--wait-for", "drained", "--wait-timeout", "-1s"}, false, ""); err == nil {
		t.Fatal("negative blocking timeout accepted")
	}
}
