package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func writeContextAMQRC(t *testing.T, project, root string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"root": root})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".amqrc"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveCanonicalContextPrecedenceMatrix(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	project, _ = os.Getwd()

	t.Run("documented defaults", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Profile != team.DefaultProfile || ctx.Sources["profile"] != contextSourceDefault {
			t.Fatalf("default profile = %q from %q", ctx.Profile, ctx.Sources["profile"])
		}
		if ctx.Sources["session"] != contextSourceDefault || ctx.PinMode != "sessionful" {
			t.Fatalf("default session source/pin = %q/%q", ctx.Sources["session"], ctx.PinMode)
		}
	})

	writeContextAMQRC(t, project, filepath.Join(".agent-mail", "configured"))
	t.Run("project amqrc", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Session != "configured" || ctx.Sources["session"] != contextSourceAMQRC {
			t.Fatalf("amqrc session = %q from %q", ctx.Session, ctx.Sources["session"])
		}
	})

	liveRoot := filepath.Join(project, ".agent-mail", "live", "live-s")
	contextPIDAlive = func(pid int) bool { return pid == 101 }
	contextProcessMatch = func(int, func(string) bool) bool { return true }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{{
			AgentDir: filepath.Join(liveRoot, "agents", "lead"),
			Record:   launch.Record{AgentPID: 101, Binary: "codex", TeamProfile: "live", Session: "live-s", Handle: "lead", Root: liveRoot, BaseRoot: liveRoot},
		}}, nil
	}
	t.Run("live launch", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Profile != "live" || ctx.Session != "live-s" || ctx.Sources["profile"] != contextSourceLaunch {
			t.Fatalf("launch context = profile %q session %q sources %#v", ctx.Profile, ctx.Session, ctx.Sources)
		}
	})

	envRoot := filepath.Join(project, ".agent-mail", "environment", "env-s")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")
	t.Run("injected environment", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Profile != "environment" || ctx.Session != "env-s" || ctx.Sources["profile"] != contextSourceEnv || ctx.PinMode != "exact_root" {
			t.Fatalf("env context = profile %q session %q pin %q sources %#v", ctx.Profile, ctx.Session, ctx.PinMode, ctx.Sources)
		}
	})

	t.Run("explicit flags", func(t *testing.T) {
		ctx, err := resolveCanonicalContext(contextResolveOptions{
			ProfileFlag: "flags", SessionFlag: "flag-s", HandleFlag: "flag-agent",
			ProfileExplicit: true, SessionExplicit: true, HandleExplicit: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		if ctx.Profile != "flags" || ctx.Session != "flag-s" || ctx.Handle != "flag-agent" {
			t.Fatalf("flag context = profile %q session %q handle %q", ctx.Profile, ctx.Session, ctx.Handle)
		}
		for _, field := range []string{"profile", "session", "handle"} {
			if ctx.Sources[field] != contextSourceFlags {
				t.Errorf("%s source = %q", field, ctx.Sources[field])
			}
		}
		diagnostic := strings.Join(contextDiagnosticLines(ctx), "\n")
		for _, want := range []string{contextSourceFlags, contextSourceEnv, contextSourceLaunch, contextSourceAMQRC, contextSourceDefault, "winner", "loser"} {
			if !strings.Contains(diagnostic, want) {
				t.Errorf("diagnostic missing %q:\n%s", want, diagnostic)
			}
		}
	})
}

func TestExplicitSessionSwitchCarriesProfileButNotOldTupleIdentity(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	envRoot := filepath.Join(project, ".agent-mail", "envprof", "envsession")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")

	resolve := func(handle string, explicit bool) contextResolution {
		t.Helper()
		ctx, err := resolveCanonicalContext(contextResolveOptions{
			SessionFlag: "flagsession", HandleFlag: handle,
			SessionExplicit: true, HandleExplicit: explicit,
		})
		if err != nil {
			t.Fatal(err)
		}
		return ctx
	}

	ctx := resolve("", false)
	wantRoot := filepath.Join(project, ".agent-mail", "envprof", "flagsession")
	if ctx.Profile != "envprof" || ctx.Sources["profile"] != contextSourceEnv || ctx.Session != "flagsession" || ctx.Sources["session"] != contextSourceFlags {
		t.Fatalf("same-profile switch anchors: %#v", ctx)
	}
	if ctx.Handle != "" || canonicalContextComparisonPath(ctx.Root) != canonicalContextComparisonPath(wantRoot) || canonicalContextComparisonPath(ctx.BaseRoot) != canonicalContextComparisonPath(wantRoot) || ctx.Sources["root"] != contextSourceDefault {
		t.Fatalf("old tuple identity leaked into switch: %#v", ctx)
	}
	for _, candidate := range ctx.Candidates {
		if candidate.Source == contextSourceEnv && candidate.Field != "profile" && candidate.Selected {
			t.Errorf("old env tuple field selected: %#v", candidate)
		}
	}

	explicitHandle := resolve("new-agent", true)
	if explicitHandle.Handle != "new-agent" || explicitHandle.Sources["handle"] != contextSourceFlags {
		t.Fatalf("explicit handle lost across switch: %#v", explicitHandle)
	}
	for _, candidate := range explicitHandle.Candidates {
		if candidate.Field == "handle" && candidate.Source == contextSourceEnv && candidate.Selected {
			t.Errorf("old environment handle selected over --me: %#v", candidate)
		}
	}
}

func TestMatchingLowerTupleContributesHandleAndExactRoot(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	envRoot := filepath.Join(project, ".agent-mail", "envprof", "envsession")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")

	ctx, err := resolveCanonicalContext(contextResolveOptions{ProfileFlag: "envprof", ProfileExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Profile != "envprof" || ctx.Session != "envsession" || ctx.Handle != "env-agent" || ctx.Root != envRoot || ctx.BaseRoot != envRoot {
		t.Fatalf("matching lower tuple not preserved: %#v", ctx)
	}
	for _, field := range []string{"session", "handle", "root", "base_root"} {
		if ctx.Sources[field] != contextSourceEnv {
			t.Errorf("%s source = %q, want %q", field, ctx.Sources[field], contextSourceEnv)
		}
	}
}

func TestResolveCanonicalContextAmbiguousLaunchesReportEveryProvenance(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	writeContextAMQRC(t, project, filepath.Join(".agent-mail", "configured"))
	contextPIDAlive = func(int) bool { return true }
	contextProcessMatch = func(int, func(string) bool) bool { return true }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{
			{AgentDir: filepath.Join(project, ".agent-mail", "alpha", "s", "agents", "a"), Record: launch.Record{AgentPID: 1, Binary: "codex", TeamProfile: "alpha", Session: "s", Handle: "a"}},
			{AgentDir: filepath.Join(project, ".agent-mail", "beta", "s", "agents", "b"), Record: launch.Record{AgentPID: 2, Binary: "codex", TeamProfile: "beta", Session: "s", Handle: "b"}},
		}, nil
	}
	_, err := resolveCanonicalContext(contextResolveOptions{})
	if err == nil {
		t.Fatal("expected same-rank live-launch ambiguity")
	}
	message := err.Error()
	for _, want := range []string{
		"ambiguous profile", "no winner", "every candidate", "alpha", "beta", "agents/a", "agents/b",
		contextSourceLaunch, contextSourceAMQRC, contextSourceDefault, "lower precedence",
		"amq-squad doctor --profile alpha --session s",
		"amq-squad doctor --profile beta --session s",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("ambiguity missing %q: %s", want, message)
		}
	}
}

// TestResolveCanonicalContextSkipSessionResolutionAvoidsRootAmbiguity is
// senior-dev's re-review finding on PR #723: SkipSessionResolution used to
// bypass only the "session" candidate selection, but root/base_root/handle
// selection ran right after with session=="" -- and
// contextCandidateTupleCompatible treats every session-bound candidate as
// compatible with an empty selected session, so two SAME-PROFILE
// runtime-live launch records at different sessions still tied on "root"
// and returned "ambiguous root" before 'resume --last' ever got to pick a
// session. SkipSessionResolution must now return before touching
// session/handle/root/base_root/namespace at all.
func TestResolveCanonicalContextSkipSessionResolutionAvoidsRootAmbiguity(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	contextPIDAlive = func(int) bool { return true }
	contextProcessMatch = func(int, func(string) bool) bool { return true }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{
			{AgentDir: filepath.Join(project, ".agent-mail", "alpha", "s1", "agents", "a"), Record: launch.Record{AgentPID: 1, Binary: "codex", TeamProfile: "alpha", Session: "s1", Handle: "a"}},
			{AgentDir: filepath.Join(project, ".agent-mail", "alpha", "s2", "agents", "a"), Record: launch.Record{AgentPID: 2, Binary: "codex", TeamProfile: "alpha", Session: "s2", Handle: "a"}},
		}, nil
	}
	ctx, err := resolveCanonicalContext(contextResolveOptions{SkipSessionResolution: true})
	if err != nil {
		t.Fatalf("project/profile-only resolution must not hit session-dependent root ambiguity: %v", err)
	}
	if ctx.Profile != "alpha" {
		t.Fatalf("ctx.Profile = %q, want %q", ctx.Profile, "alpha")
	}
	if ctx.Session != "" || ctx.Root != "" || ctx.Handle != "" {
		t.Fatalf("SkipSessionResolution must leave session-dependent fields empty, got %#v", ctx)
	}

	// The same fixture without SkipSessionResolution must still surface the
	// ambiguity: this proves the fix scopes the bypass, it doesn't disable
	// the underlying safety check.
	if _, err := resolveCanonicalContext(contextResolveOptions{}); err == nil {
		t.Fatal("expected the full resolution path to still report ambiguous root for this fixture")
	}
}

func TestStoppedAndPIDReusedLaunchRecordsDoNotWinContext(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	stoppedAt := time.Now().Add(-time.Minute).UTC()
	contextPIDAlive = func(int) bool { return true }
	contextProcessMatch = func(pid int, _ func(string) bool) bool { return pid == 2 || pid == 3 }
	contextProcessTTY = func(pid int) (string, bool) {
		if pid == 2 {
			return "/dev/ttys999", true
		}
		return "/dev/ttys003", true
	}
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{
			{
				AgentDir: filepath.Join(project, ".agent-mail", "alpha", "s", "agents", "stopped"),
				Record: launch.Record{
					AgentPID: 1, Binary: "codex", AgentTTY: "/dev/ttys001", TeamProfile: "alpha",
					Session: "s", Handle: "stopped", TeamHome: project, StoppedAt: &stoppedAt,
				},
			},
			{
				AgentDir: filepath.Join(project, ".agent-mail", "beta", "s", "agents", "reused"),
				Record: launch.Record{
					AgentPID: 2, Binary: "codex", AgentTTY: "/dev/ttys002", TeamProfile: "beta",
					Session: "s", Handle: "reused", TeamHome: project,
				},
			},
		}, nil
	}
	ctx, err := resolveCanonicalContext(contextResolveOptions{ProjectFlag: project, ProjectExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Profile != team.DefaultProfile || ctx.Sources["profile"] == contextSourceLaunch {
		t.Fatalf("stopped/PID-reused records won context: %#v", ctx)
	}

	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{{
			AgentDir: filepath.Join(project, ".agent-mail", "gamma", "s", "agents", "live"),
			Record: launch.Record{
				AgentPID: 3, Binary: "codex", AgentTTY: "/dev/ttys003", TeamProfile: "gamma",
				Session: "s", Handle: "live", TeamHome: project,
			},
		}}, nil
	}
	ctx, err = resolveCanonicalContext(contextResolveOptions{ProjectFlag: project, ProjectExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Profile != "gamma" || ctx.Sources["profile"] != contextSourceLaunch {
		t.Fatalf("verified live record did not win context: %#v", ctx)
	}
}

func TestLegacyLaunchRecordsWithoutStoppedAtUseLivenessProbe(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	deadDir := filepath.Join(project, ".agent-mail", "legacy-a", "s", "agents", "dead")
	reusedDir := filepath.Join(project, ".agent-mail", "legacy-b", "s", "agents", "reused")
	for agentDir, rec := range map[string]launch.Record{
		deadDir: {
			TeamHome: project, TeamProfile: "legacy-a", Session: "s", Handle: "dead",
			Binary: "codex", AgentPID: 30, AgentTTY: "/dev/ttys030",
		},
		reusedDir: {
			TeamHome: project, TeamProfile: "legacy-b", Session: "s", Handle: "reused",
			Binary: "codex", AgentPID: 31, AgentTTY: "/dev/ttys031",
		},
	} {
		if err := launch.Write(agentDir, rec); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(launch.Path(agentDir))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "stopped_at") {
			t.Fatalf("legacy fixture unexpectedly carries stopped_at:\n%s", raw)
		}
	}
	contextScanLaunchEntries = launch.ScanEntries
	contextPIDAlive = func(pid int) bool { return pid == 31 }
	contextProcessMatch = func(pid int, _ func(string) bool) bool { return pid == 31 }
	contextProcessTTY = func(pid int) (string, bool) { return "/dev/ttys999", pid == 31 }

	ctx, err := resolveCanonicalContext(contextResolveOptions{ProjectFlag: project, ProjectExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Profile != team.DefaultProfile || ctx.Sources["profile"] == contextSourceLaunch {
		t.Fatalf("legacy dead/reused records poisoned context: %#v", ctx)
	}
}

func TestContextRejectsSameBinaryPIDReuseWithSameOrUnknownTTY(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	recordedAt := time.Now().Add(-time.Minute).UTC()
	contextPIDAlive = func(int) bool { return true }
	contextProcessMatch = func(int, func(string) bool) bool { return true }
	contextProcessStartTime = func(int) (time.Time, bool) {
		return recordedAt.Add(launchProcessStartSkewEpsilon + time.Nanosecond), true
	}
	contextProcessTTY = func(int) (string, bool) { return "/dev/ttys007", true }

	for _, agentTTY := range []string{"/dev/ttys007", "", "unknown"} {
		rec := launch.Record{
			AgentPID: 42, Binary: "codex", AgentTTY: agentTTY,
			StartedAt: recordedAt, TeamProfile: "reused", Session: "s",
			Role: "worker", Handle: "worker", TeamHome: project,
		}
		if contextLaunchRecordRuntimeLive(rec, "") {
			t.Fatalf("process born after launch record won context with recorded tty %q", agentTTY)
		}
	}
}

func TestContextRejectsReusedPaneIDForCurrentAndExternalRecords(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	rec := launch.Record{
		External: true, TeamHome: project, Session: "s", Role: "worker", Handle: "worker",
		Tmux: &launch.TmuxInfo{PaneID: "%7"},
	}
	contextPaneTitle = func(string) (string, bool) { return "amq:s:someone-else", true }
	if contextLaunchRecordRuntimeLive(rec, "%7") {
		t.Fatal("current-pane shortcut accepted a reused pane id with the wrong title")
	}
	if contextLaunchRecordRuntimeLive(rec, "") {
		t.Fatal("external-pane path accepted a reused pane id with the wrong title")
	}
	contextPaneTitle = func(string) (string, bool) { return "amq:s:worker", true }
	if !contextLaunchRecordRuntimeLive(rec, "%7") {
		t.Fatal("exact current pane identity was not accepted")
	}
	if !contextLaunchRecordRuntimeLive(rec, "") {
		t.Fatal("exact external pane identity was not accepted")
	}
}

func TestResolveCanonicalContextSharedTupleDoesNotRequireHandle(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	root := filepath.Join(project, ".agent-mail", "release", "shared")
	contextPIDAlive = func(int) bool { return true }
	contextProcessMatch = func(int, func(string) bool) bool { return true }
	contextPaneTitle = func(paneID string) (string, bool) {
		return "amq:shared:cto", paneID == "%7"
	}
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{
			{AgentDir: filepath.Join(root, "agents", "cto"), Record: launch.Record{AgentPID: 1, Binary: "codex", TeamProfile: "release", Session: "shared", Handle: "cto", Root: root, BaseRoot: root, Tmux: &launch.TmuxInfo{PaneID: "%7"}}},
			{AgentDir: filepath.Join(root, "agents", "qa"), Record: launch.Record{AgentPID: 2, Binary: "codex", TeamProfile: "release", Session: "shared", Handle: "qa", Root: root, BaseRoot: root}},
		}, nil
	}
	if err := team.WriteProfile(project, "release", team.Team{Members: []team.Member{
		{Role: "cto", Binary: "codex", Handle: "cto", Session: "shared"},
		{Role: "qa", Binary: "codex", Handle: "qa", Session: "shared"},
	}}); err != nil {
		t.Fatal(err)
	}

	ctx, err := resolveCanonicalContext(contextResolveOptions{ProjectFlag: project, ProjectExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Profile != "release" || ctx.Session != "shared" || ctx.Root != root || ctx.Handle != "" {
		t.Fatalf("shared tuple = profile %q session %q root %q handle %q", ctx.Profile, ctx.Session, ctx.Root, ctx.Handle)
	}
	var handles []string
	for _, candidate := range ctx.Candidates {
		if candidate.Field == "handle" && candidate.Source == contextSourceLaunch {
			handles = append(handles, candidate.Value)
		}
	}
	sort.Strings(handles)
	if strings.Join(handles, ",") != "cto,qa" {
		t.Fatalf("handle candidates = %v", handles)
	}
	t.Setenv("TMUX_PANE", "%7")
	paneCtx, err := resolveCanonicalContext(contextResolveOptions{ProjectFlag: project, ProjectExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if paneCtx.Handle != "cto" || paneCtx.Sources["handle"] != contextSourceLaunch {
		t.Fatalf("pane-matched handle = %q from %q", paneCtx.Handle, paneCtx.Sources["handle"])
	}
	if err := os.Unsetenv("TMUX_PANE"); err != nil {
		t.Fatal(err)
	}

	for name, run := range map[string]func() error{
		"status": func() error { return runStatus([]string{"--project", project, "--session", "shared", "--json"}) },
		"task":   func() error { return runTask([]string{"list", "--project", project, "--session", "shared", "--json"}) },
	} {
		t.Run(name, func(t *testing.T) {
			_, stderr, err := captureOutput(t, run)
			if err != nil {
				t.Fatalf("%s rejected shared tuple: %v\n%s", name, err, stderr)
			}
		})
	}
}

func TestExplicitRootOverridesMalformedInjectedIdentityWithWarning(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	t.Setenv("AM_ROOT", filepath.Join(project, ".agent-mail", "stale", "old"))
	if err := os.Unsetenv("AM_BASE_ROOT"); err != nil {
		t.Fatal(err)
	}
	explicitRoot := filepath.Join(project, ".agent-mail", "review", "issue-463")
	ctx, err := resolveCanonicalContext(contextResolveOptions{RootFlag: explicitRoot, RootExplicit: true})
	if err != nil {
		t.Fatalf("complete explicit root should outrank malformed env: %v", err)
	}
	if ctx.Profile != "review" || ctx.Session != "issue-463" || ctx.Root != explicitRoot || ctx.BaseRoot != explicitRoot || ctx.PinMode != "exact_root" {
		t.Fatalf("explicit-root context: %#v", ctx)
	}
	warnings := strings.Join(ctx.Warnings, "\n")
	if !strings.Contains(warnings, "injected AMQ identity is incomplete") {
		t.Fatalf("malformed env warning missing: %v", ctx.Warnings)
	}
	diagnostic := strings.Join(contextDiagnosticLines(ctx), "\n")
	for _, want := range []string{contextSourceFlags, contextSourceEnv, "losing candidate", "incomplete"} {
		if !strings.Contains(diagnostic, want) {
			t.Errorf("diagnostic missing %q:\n%s", want, diagnostic)
		}
	}

	_, err = resolveCanonicalContext(contextResolveOptions{ProfileFlag: "review", ProfileExplicit: true})
	if err == nil || !strings.Contains(err.Error(), "injected AMQ identity is incomplete") {
		t.Fatalf("incomplete explicit tuple should not suppress malformed env: %v", err)
	}
}

func TestExplicitExternalRootWinsForNamedProfile(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	externalRoot := filepath.Join(t.TempDir(), "review-mail")

	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProfileFlag: "review", SessionFlag: "issue-463", RootFlag: externalRoot,
		ProfileExplicit: true, SessionExplicit: true, RootExplicit: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Profile != "review" || ctx.Session != "issue-463" || ctx.Root != externalRoot || ctx.BaseRoot != externalRoot || ctx.PinMode != "exact_root" {
		t.Fatalf("explicit external-root context: %#v", ctx)
	}
	if ctx.Sources["root"] != contextSourceFlags || ctx.Sources["base_root"] != contextSourceFlags {
		t.Fatalf("explicit external root sources: %#v", ctx.Sources)
	}
}

func TestExplicitProjectOutranksForeignInjectedRoot(t *testing.T) {
	currentProject := t.TempDir()
	selectedProject := t.TempDir()
	isolateCanonicalContextTest(t, currentProject)
	foreignRoot := filepath.Join(currentProject, ".agent-mail", "foreign", "old")
	t.Setenv("AM_ROOT", foreignRoot)
	t.Setenv("AM_BASE_ROOT", foreignRoot)

	ctx, err := resolveCanonicalContext(contextResolveOptions{ProjectFlag: selectedProject, ProjectExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.ProjectDir != selectedProject {
		t.Fatalf("project = %q, want explicit %q", ctx.ProjectDir, selectedProject)
	}
	if ctx.Root == foreignRoot || !strings.HasPrefix(ctx.Root, filepath.Join(selectedProject, ".agent-mail")) {
		t.Fatalf("foreign injected root won explicit project: %#v", ctx)
	}
	diagnostic := strings.Join(contextDiagnosticLines(ctx), "\n")
	if !strings.Contains(diagnostic, contextSourceEnv) || !strings.Contains(diagnostic, "losing candidate") {
		t.Fatalf("foreign env provenance missing: %s", diagnostic)
	}
}

func TestResolveAMQContextUsesCanonicalCustomRoot(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	customBase := filepath.Join(project, "custom-mail")
	writeContextAMQRC(t, project, customBase)

	previous := resolveAMQEnvForAMQCommand
	var selectedRoot, selectedSession string
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		selectedRoot, selectedSession = rootFlag, session
		return amqEnv{Root: rootFlag, BaseRoot: rootFlag, Me: handle}, nil
	}
	t.Cleanup(func() { resolveAMQEnvForAMQCommand = previous })

	ctx, err := resolveAMQContext(project, "", "issue-463", "cto", true)
	if err != nil {
		t.Fatal(err)
	}
	wantRoot := filepath.Join(customBase, "issue-463")
	if selectedRoot != wantRoot || selectedSession != "" {
		t.Fatalf("AMQ resolver called with root/session %q/%q, want %q/exact-root", selectedRoot, selectedSession, wantRoot)
	}
	if ctx.Root != wantRoot || ctx.Env.Root != wantRoot || ctx.Env.BaseRoot != customBase || ctx.Session != "issue-463" {
		t.Fatalf("resolved custom context: %#v", ctx)
	}
}

func TestOrdinaryCommandsDoNotSpliceConflictingTuples(t *testing.T) {
	project := t.TempDir()
	isolateCanonicalContextTest(t, project)
	if err := team.WriteProfile(project, "flagprof", team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "flagsession"}}}); err != nil {
		t.Fatal(err)
	}
	writeContextAMQRC(t, project, filepath.Join(".agent-mail", "configprof", "configsession"))
	envRoot := filepath.Join(project, ".agent-mail", "envprof", "envsession")
	t.Setenv("AM_ROOT", envRoot)
	t.Setenv("AM_BASE_ROOT", envRoot)
	t.Setenv("AM_ME", "env-agent")
	liveRoot := filepath.Join(project, ".agent-mail", "liveprof", "livesession")
	contextPIDAlive = func(int) bool { return true }
	contextProcessMatch = func(int, func(string) bool) bool { return true }
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) {
		return []launch.Entry{{
			AgentDir: filepath.Join(liveRoot, "agents", "live-agent"),
			Record: launch.Record{
				AgentPID: 1, Binary: "codex", TeamProfile: "liveprof", Session: "livesession", Handle: "live-agent",
				Root: liveRoot, BaseRoot: liveRoot,
			},
		}}, nil
	}

	type amqSelection struct{ root, session, handle string }
	var selections []amqSelection
	previousResolve, previousRun := resolveAMQEnvForAMQCommand, runAMQCommand
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		selections = append(selections, amqSelection{root: rootFlag, session: session, handle: handle})
		return amqEnv{Root: rootFlag, BaseRoot: rootFlag, Me: handle}, nil
	}
	runAMQCommand = func(amqCommandRequest) ([]byte, error) { return []byte("[]\n"), nil }
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = previousResolve
		runAMQCommand = previousRun
	})

	commands := []struct {
		name string
		run  func() error
	}{
		{"status", func() error {
			return runStatus([]string{"--project", project, "--profile", "flagprof", "--session", "flagsession", "--json"})
		}},
		{"task", func() error {
			return runTask([]string{"list", "--project", project, "--profile", "flagprof", "--session", "flagsession", "--json"})
		}},
		{"wrapped-amq", func() error {
			return runAMQPassthrough("list", []string{"--project", project, "--profile", "flagprof"})
		}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			_, stderr, err := captureOutput(t, command.run)
			if err != nil {
				t.Fatalf("%s failed: %v\n%s", command.name, err, stderr)
			}
			for _, want := range []string{contextSourceEnv, contextSourceLaunch, contextSourceAMQRC, "loser"} {
				if !strings.Contains(stderr, want) {
					t.Errorf("%s diagnostics missing %q:\n%s", command.name, want, stderr)
				}
			}
		})
	}
	if len(selections) != 1 {
		t.Fatalf("wrapped AMQ selections = %v", selections)
	}
	wantFlagRoot := filepath.Join(project, ".agent-mail", "flagprof", "flagsession")
	if canonicalContextComparisonPath(selections[0].root) != canonicalContextComparisonPath(wantFlagRoot) || selections[0].session != "" || selections[0].handle != "" {
		t.Fatalf("explicit-profile AMQ selection spliced old tuple: %#v", selections[0])
	}

	_, stderr, err := captureOutput(t, func() error {
		return runAMQPassthrough("list", []string{"--project", project, "--session", "switched"})
	})
	if err != nil {
		t.Fatalf("symmetric explicit-session AMQ failed: %v\n%s", err, stderr)
	}
	if len(selections) != 2 {
		t.Fatalf("wrapped AMQ selections = %v", selections)
	}
	wantSwitchedRoot := filepath.Join(project, ".agent-mail", "envprof", "switched")
	if canonicalContextComparisonPath(selections[1].root) != canonicalContextComparisonPath(wantSwitchedRoot) || selections[1].session != "" || selections[1].handle != "" {
		t.Fatalf("explicit-session AMQ selection spliced old tuple: %#v", selections[1])
	}
}
