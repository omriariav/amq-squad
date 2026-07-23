package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRealAMQGateCloseReplyRefsCompatibility(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if binary == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to run disposable AMQ reply/refs compatibility proof")
	}
	info, err := os.Stat(binary)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		t.Skipf("AMQ_SQUAD_REAL_AMQ is unavailable or not executable: %v", err)
	}
	version := strings.TrimSpace(realAMQCommand(t, binary, t.TempDir(), nil, "version"))
	if !semverMeetsStableFloor(version, "0.43.1") {
		t.Skipf("AMQ %s predates reply refs compatibility floor 0.43.1", version)
	}

	project := t.TempDir()
	root := filepath.Join(project, ".agent-mail", "gate-close-compat")
	projectClean, rootClean := filepath.Clean(project), filepath.Clean(root)
	if rootClean == projectClean || !strings.HasPrefix(rootClean, projectClean+string(os.PathSeparator)) {
		t.Fatalf("refusing real AMQ compatibility root outside temp project: project=%q root=%q", projectClean, rootClean)
	}
	realAMQInitAgents(t, binary, project, root, "cto", team.DefaultOperatorHandle)
	cleanEnv := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
	const thread = "gate/real-amq-close-refs"
	questionOut := realAMQCommand(t, binary, project, cleanEnv,
		"send", "--root", root, "--me", "cto", "--to", team.DefaultOperatorHandle,
		"--thread", thread, "--kind", "question", "--subject", "APPROVAL: close refs compatibility", "--body", "approve?", "--json")
	questionID := parseSentMessageID(questionOut)
	if questionID == "" {
		t.Fatalf("real AMQ question omitted stable id: %s", questionOut)
	}
	contextJSON := `{"gate":{"actor":"cto","request_message_id":"` + questionID + `","requester":"cto","state":"closed","thread":"` + thread + `"}}`
	replyOut := realAMQCommand(t, binary, project, cleanEnv,
		"reply", "--root", root, "--me", "cto", "--id", questionID,
		"--kind", "status", "--subject", "CLOSED: real-amq-close-refs", "--body", "compatibility complete", "--context", contextJSON, "--json")
	replyID := parseSentMessageID(replyOut)
	if replyID == "" {
		t.Fatalf("real AMQ reply omitted stable id: %s", replyOut)
	}

	messages, warnings := state.ScanSessionMessages(root, time.Now)
	if len(warnings) != 0 {
		t.Fatalf("scan real AMQ reply warnings: %+v", warnings)
	}
	var terminal *state.Message
	var threadMessages []state.Message
	for i := range messages {
		if messages[i].Thread != thread {
			continue
		}
		threadMessages = append(threadMessages, messages[i])
		if messages[i].ID == replyID {
			copy := messages[i]
			terminal = &copy
		}
	}
	if terminal == nil {
		t.Fatalf("parsed real AMQ messages omitted reply %s: %+v", replyID, messages)
	}
	if terminal.ReplyTo != "" || !terminal.RefsPresent || !terminal.RefsValid || len(terminal.Refs) != 1 || terminal.Refs[0] != questionID {
		t.Fatalf("real AMQ reply linkage = reply_to:%q refs:%#v present:%t valid:%t", terminal.ReplyTo, terminal.Refs, terminal.RefsPresent, terminal.RefsValid)
	}
	if !terminal.ToPresent || !terminal.ToArrayValid || len(terminal.RawTo) != 1 || terminal.RawTo[0] != team.DefaultOperatorHandle {
		t.Fatalf("real AMQ reply recipient evidence = raw_to:%#v present:%t valid:%t raw:%q", terminal.RawTo, terminal.ToPresent, terminal.ToArrayValid, terminal.ToRaw)
	}
	gateState, signal := state.ResolveOperatorGate(threadMessages, team.DefaultOperatorHandle, time.Now())
	if gateState != state.OperatorGateStateClosed || signal != nil {
		t.Fatalf("real AMQ reply lifecycle = state:%q signal:%+v", gateState, signal)
	}
}

// TestRealAMQCompatibility is intentionally opt-in. Ordinary internal/cli
// tests must never discover or invoke a host AMQ binary; CI supplies this
// exact binary via AMQ_SQUAD_REAL_AMQ for the focused floor/current/canary
// compatibility matrix.
func TestRealAMQCompatibility(t *testing.T) {
	binary := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ"))
	if binary == "" {
		t.Skip("set AMQ_SQUAD_REAL_AMQ to run disposable real-AMQ compatibility checks")
	}
	info, err := os.Stat(binary)
	if err != nil {
		t.Fatalf("stat AMQ_SQUAD_REAL_AMQ %q: %v", binary, err)
	}
	if info.IsDir() || info.Mode()&0o111 == 0 {
		t.Fatalf("AMQ_SQUAD_REAL_AMQ %q is not an executable file", binary)
	}
	version := strings.TrimSpace(realAMQCommand(t, binary, t.TempDir(), nil, "version"))
	if !semverMeetsStableFloor(version, doctorMinAMQVersion) {
		t.Fatalf("real AMQ %q is below supported floor %s", version, doctorMinAMQVersion)
	}
	if expected := strings.TrimSpace(os.Getenv("AMQ_SQUAD_REAL_AMQ_VERSION")); expected != "" && expected != "latest" && strings.TrimPrefix(version, "v") != strings.TrimPrefix(expected, "v") {
		t.Fatalf("real AMQ version = %q, expected requested %q", version, expected)
	}
	t.Logf("real AMQ binary=%s version=%s requested=%s", binary, version, os.Getenv("AMQ_SQUAD_REAL_AMQ_VERSION"))
	t.Setenv("PATH", filepath.Dir(binary)+string(os.PathListSeparator)+os.Getenv("PATH"))

	t.Run("sessionful default profile", func(t *testing.T) {
		project := t.TempDir()
		root := filepath.Join(project, ".agent-mail", "issue-449")
		realAMQInit(t, binary, project, root)
		ctx := amqContext{
			ProjectDir: project,
			Profile:    team.DefaultProfile,
			Env:        amqEnv{BaseRoot: filepath.Dir(root)},
			Root:       root,
			Me:         "lead",
			Session:    "issue-449",
			PinMode:    amqPinSessionful,
		}
		realAMQRoundTrip(t, binary, ctx, root, "sessionful")
	})

	t.Run("exact root named profile", func(t *testing.T) {
		project := t.TempDir()
		root := filepath.Join(project, "root with spaces", ".agent-mail", "review", "issue-449")
		realAMQInit(t, binary, project, root)
		ctx := amqContext{
			ProjectDir: project,
			Profile:    "review",
			Root:       root,
			Me:         "lead",
			Session:    "issue-449",
			PinMode:    amqPinExactRoot,
		}
		realAMQRoundTrip(t, binary, ctx, root, "exact-root")
	})

	t.Run("three actor 100 message isolation", func(t *testing.T) {
		realAMQThreeActorHundredMessageIsolation(t, binary)
	})
	if semverMeetsStableFloor(version, "0.45.0") {
		t.Run("committed delivery wording contract", func(t *testing.T) {
			realAMQCommittedDeliveryWordingContract(t, binary)
		})
		t.Run("exact inject-via wake retirement", func(t *testing.T) {
			realAMQExactInjectViaWakeRetirement(t, binary)
		})
	}
	if amqSupportsBaselineExisting(version) {
		t.Run("coop exec drains preexisting goal with zero injection", func(t *testing.T) {
			realAMQCoopExecBaselineDrainContract(t, binary)
		})
		t.Run("external wake suppresses backlog and injects post-baseline resend", func(t *testing.T) {
			realAMQExternalWakeBaselineContract(t, binary)
		})
	}

	t.Run("post-coop child identity", func(t *testing.T) {
		project := t.TempDir()
		cleanEnv := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))

		defaultSession := "issue-481-default"
		defaultRoot := filepath.Join(project, ".agent-mail", defaultSession)
		realAMQInit(t, binary, project, defaultRoot)
		defaultOut := realAMQCommand(t, binary, project, cleanEnv,
			"coop", "exec", "--session", defaultSession, "--me", "lead", "--no-wake", "env")
		defaultChildEnv := strings.Split(defaultOut, "\n")
		if !envHas(defaultChildEnv, "AM_SESSION", defaultSession) {
			t.Fatalf("default-profile post-coop child omitted AM_SESSION=%q:\n%s", defaultSession, defaultOut)
		}

		namedRoot := filepath.Join(project, ".agent-mail", "review", "issue-481-named")
		realAMQInit(t, binary, project, namedRoot)
		namedOut := realAMQCommand(t, binary, project, cleanEnv,
			"coop", "exec", "--root", namedRoot, "--me", "lead", "--no-wake",
			"env", "--", "-u", "AM_SESSION", "env")
		namedChildEnv := strings.Split(namedOut, "\n")
		if envHasPrefix(namedChildEnv, "AM_SESSION", "") {
			t.Fatalf("named-profile post-coop child retained AM_SESSION:\n%s", namedOut)
		}
		for key, want := range map[string]string{"AM_ROOT": namedRoot, "AM_BASE_ROOT": namedRoot, "AM_ME": "lead"} {
			if !envHas(namedChildEnv, key, want) {
				t.Fatalf("named-profile post-coop child %s mismatch, want %q:\n%s", key, want, namedOut)
			}
		}
	})

	for _, profile := range []string{team.DefaultProfile, "review"} {
		profile := profile
		t.Run("orchestration contract "+profile, func(t *testing.T) {
			realAMQOrchestrationContract(t, binary, version, profile)
		})
	}

	// Issue #470: the supported floor and latest lanes must also prove that a
	// genuinely empty project reaches a live recording backend launch without
	// relying on ambient AMQ discovery. The fake registered under the real tmux
	// backend name records exactly one launch and never creates a user pane.
	for _, profile := range []string{team.DefaultProfile, "review"} {
		profile := profile
		t.Run("fresh live launch "+profile, func(t *testing.T) {
			project := t.TempDir()
			chdir(t, project)
			if err := team.WriteProfile(project, profile, issue470Team(project, profile)); err != nil {
				t.Fatal(err)
			}
			prepareIssue470Run(t, project, profile, "--visibility", visibilityDetached)
			backend := useFakeTmuxBackend(t)
			args := issue470RunArgs(project, profile, "--visibility", visibilityDetached, "--go")
			_, _, err := captureOutput(t, func() error { return runRunStart(args, "test") })
			if err != nil {
				t.Fatalf("fresh %s live launch with real AMQ %s: %v", profile, version, err)
			}
			if len(backend.dryRuns) != 0 || len(backend.launches) != 1 || len(backend.teams) != 1 {
				t.Fatalf("recording backend dryRuns=%d launches=%d", len(backend.dryRuns), len(backend.launches))
			}
			launch := backend.launches[0]
			if launch.DryRun || launch.Workstream != issue470Session || launch.Profile != profile {
				t.Fatalf("recorded launch = %+v", launch)
			}
			if len(backend.teams[0].Members) != 1 || backend.teams[0].Members[0].Handle != "cto" {
				t.Fatalf("recording backend launched unexpected user panes: %+v", backend.teams[0].Members)
			}
			ctx := realAMQProfileContext(project, profile, issue470Session, "cto")
			preflights, err := buildTeamPreflights(backend.teams[0], launch)
			if err != nil || len(preflights) != 1 {
				t.Fatalf("fresh %s launch preflights=%+v err=%v", profile, preflights, err)
			}
			resolvedPreflightRoot, preflightRootErr := canonicalPathForReceipt(preflights[0].Root)
			resolvedExpectedRoot, expectedRootErr := canonicalPathForReceipt(ctx.Root)
			if preflightRootErr != nil || expectedRootErr != nil || resolvedPreflightRoot != resolvedExpectedRoot || preflights[0].Workstream != issue470Session || preflights[0].Handle != "cto" {
				t.Fatalf("fresh %s launch identity = %+v, want root=%q session=%q handle=cto", profile, preflights[0], ctx.Root, issue470Session)
			}
			panes := buildTeamLaunchPanes(backend.teams[0], launch)
			if len(panes) != 1 {
				t.Fatalf("fresh %s launch panes = %+v", profile, panes)
			}
			for _, want := range []string{"agent up codex", "--role cto", "--session " + issue470Session, "--team-workstream", "--me cto"} {
				if !strings.Contains(panes[0].Command, want) {
					t.Fatalf("fresh %s launch argv %q missing %q", profile, panes[0].Command, want)
				}
			}
			if profile == team.DefaultProfile {
				if _, err := os.Stat(filepath.Join(project, ".agent-mail")); err != nil {
					t.Fatalf("fresh default launch did not initialize sessionful base: %v", err)
				}
				if !envHas(amqCommandEnv(ctx), "AM_SESSION", issue470Session) {
					t.Fatalf("fresh default launch omitted sessionful tuple: %#v", amqCommandEnv(ctx))
				}
				if strings.Contains(panes[0].Command, "--team-profile") {
					t.Fatalf("fresh default launch argv carried a named profile: %s", panes[0].Command)
				}
				if strings.Contains(panes[0].Command, "--root") {
					t.Fatalf("fresh default sessionful launch argv forced an exact root: %s", panes[0].Command)
				}
			} else {
				if _, err := os.Stat(ctx.Root); err != nil {
					t.Fatalf("fresh named selected root %q: %v", ctx.Root, err)
				}
				if envHasPrefix(amqCommandEnv(ctx), "AM_SESSION", "") {
					t.Fatalf("fresh named launch leaked AM_SESSION: %#v", amqCommandEnv(ctx))
				}
				if !strings.Contains(panes[0].Command, "--team-profile "+profile) {
					t.Fatalf("fresh named launch argv omitted profile: %s", panes[0].Command)
				}
				if !strings.Contains(panes[0].Command, "--root "+shellQuote(preflights[0].Root)) {
					t.Fatalf("fresh named launch argv omitted exact root: %s", panes[0].Command)
				}
				if _, err := os.Stat(filepath.Join(project, ".agent-mail", issue470Session)); !os.IsNotExist(err) {
					t.Fatalf("fresh named launch created legacy default root: %v", err)
				}
			}
		})
	}
}

func realAMQCoopExecBaselineDrainContract(t *testing.T, binary string) {
	t.Helper()
	project := realAMQSafeInjectViaFixtureProject(t)
	root := filepath.Join(project, ".agent-mail", "baseline-drain")
	realAMQInitAgents(t, binary, project, root, "sender", "member")
	cleanEnv := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
	const goalBody = "pre-existing launch goal must be engaged by bootstrap drain"
	sendOut := realAMQCommand(t, binary, project, cleanEnv,
		"send", "--root", root, "--me", "sender", "--to", "member",
		"--kind", "todo", "--subject", "pre-existing launch goal", "--body", goalBody, "--json")
	messageID := parseSentMessageID(sendOut)
	if messageID == "" {
		t.Fatalf("pre-existing goal send omitted stable id: %s", sendOut)
	}

	injectionLog := filepath.Join(project, "injections.log")
	drainLog := filepath.Join(project, "bootstrap-drain.log")
	retireErrLog := filepath.Join(project, "retire-err.log")
	injector := filepath.Join(project, "injector.sh")
	member := filepath.Join(project, "member.sh")
	if err := os.WriteFile(injector, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AMQ_TEST_INJECTION_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	// require-wake guarantees coop exec arms the wake consumer before this
	// process starts. The delay leaves more than AMQ's debounce window for an
	// un-suppressed backlog injection to become observable before the drain.
	//
	// This subtest's contract is zero injection plus drain engagement, not
	// wake retirement mechanics (exact_inject-via_wake_retirement covers
	// that, and passes on every tested AMQ version). Dropping the retire call
	// entirely was tried and rejected: coop exec's spawned wake helper
	// inherits the SAME stdout/stderr pipe as this test's own subprocess
	// capture, and once coop exec execs into this script the wake helper is
	// deliberately orphaned (left running) rather than killed; an orphan
	// that never exits holds that pipe open, so the overall command blocks
	// until it eventually stops on its own (~30s against AMQ 0.46, every
	// run) instead of returning promptly. So retire is kept, but strictly
	// best-effort ("|| true"): AMQ 0.46's own OS/timing-dependent internal
	// wake lifecycle (avivsinai/agent-message-queue#267) can refuse it via
	// two different races (lock busy pre-prepared, lock already gone) that
	// this subtest does not need to win — a successful retire just also
	// reaps the wake helper promptly in the common case, and a refused one
	// leaves cleanup to coop exec/the OS without failing this contract.
	// Diagnostics land in a log instead of being discarded, in case a retire
	// failure is ever worth inspecting.
	memberScript := `#!/bin/sh
sleep 1
amq drain --include-body > "$AMQ_TEST_DRAIN_LOG"
amq wake retire --root "$AM_ROOT" --me "$AM_ME" --inject-via "$AMQ_TEST_INJECTOR" >/dev/null 2>"$AMQ_TEST_RETIRE_ERR_LOG" || true
`
	if err := os.WriteFile(member, []byte(memberScript), 0o700); err != nil {
		t.Fatal(err)
	}
	env := append([]string(nil), cleanEnv...)
	env = append(env, "AMQ_TEST_INJECTION_LOG="+injectionLog, "AMQ_TEST_DRAIN_LOG="+drainLog, "AMQ_TEST_INJECTOR="+injector, "AMQ_TEST_RETIRE_ERR_LOG="+retireErrLog)
	realAMQCommand(t, binary, project, env,
		"coop", "exec", "--root", root, "--me", "member", "--require-wake",
		"--wake-inject-via", injector, member)

	drained, err := os.ReadFile(drainLog)
	if err != nil {
		t.Fatalf("read bootstrap drain: %v", err)
	}
	if !bytes.Contains(drained, []byte(messageID)) || !bytes.Contains(drained, []byte(goalBody)) {
		t.Fatalf("bootstrap drain did not engage pre-existing goal %s:\n%s", messageID, drained)
	}
	if injected, err := os.ReadFile(injectionLog); err == nil {
		if strings.TrimSpace(string(injected)) != "" {
			t.Fatalf("coop exec injected pre-existing backlog instead of relying on bootstrap drain:\n%s", injected)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read injection log: %v", err)
	}
}

func realAMQExternalWakeBaselineContract(t *testing.T, binary string) {
	t.Helper()
	project := realAMQSafeInjectViaFixtureProject(t)
	root := filepath.Join(project, ".agent-mail", "external-baseline")
	realAMQInitAgents(t, binary, project, root, "sender", "lead")
	cleanEnv := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))

	const oldGoal = "durable original goal copy"
	oldOut := realAMQCommand(t, binary, project, cleanEnv,
		"send", "--root", root, "--me", "sender", "--to", "lead",
		"--kind", "todo", "--subject", "original goal", "--body", oldGoal, "--json")
	oldID := parseSentMessageID(oldOut)
	if oldID == "" {
		t.Fatalf("original goal send omitted stable id: %s", oldOut)
	}

	injectionLog := filepath.Join(project, "external-injections.log")
	injector := filepath.Join(project, "injector.sh")
	if err := os.WriteFile(injector, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AMQ_TEST_INJECTION_LOG\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(project, "wake.ready")
	wake := exec.Command(binary, "wake", "--root", root, "--me", "lead",
		"--baseline-existing", "--inject-via", injector, "--ready-file", ready)
	wake.Dir = project
	wake.Env = append(cleanEnv, "AMQ_TEST_INJECTION_LOG="+injectionLog)
	var wakeLog bytes.Buffer
	wake.Stdout, wake.Stderr = &wakeLog, &wakeLog
	if err := wake.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited && wake.Process != nil {
			_ = wake.Process.Kill()
			_, _ = wake.Process.Wait()
		}
	})
	waitForRealWakeFile(t, ready, "external baseline wake readiness")
	time.Sleep(750 * time.Millisecond)
	if injected, err := os.ReadFile(injectionLog); err == nil {
		if strings.TrimSpace(string(injected)) != "" {
			t.Fatalf("external wake injected pre-existing backlog:\n%s", injected)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("read pre-resend injection log: %v", err)
	}

	const resentGoal = "post-baseline claim-once goal resend"
	newOut := realAMQCommand(t, binary, project, cleanEnv,
		"send", "--root", root, "--me", "sender", "--to", "lead",
		"--kind", "todo", "--subject", "post-baseline goal resend", "--body", resentGoal, "--json")
	newID := parseSentMessageID(newOut)
	if newID == "" {
		t.Fatalf("post-baseline resend omitted stable id: %s", newOut)
	}
	waitForRealWakeCondition(t, "post-baseline goal injection", func() bool {
		info, err := os.Stat(injectionLog)
		return err == nil && info.Size() > 0
	})
	injected, err := os.ReadFile(injectionLog)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(injected, []byte("post-baseline goal resend")) || bytes.Contains(injected, []byte("original goal")) {
		t.Fatalf("external wake injection did not isolate the post-baseline resend:\n%s", injected)
	}

	_ = wake.Process.Kill()
	_, _ = wake.Process.Wait()
	waited = true
	drained := realAMQCommand(t, binary, project, cleanEnv,
		"drain", "--root", root, "--me", "lead", "--include-body")
	for _, want := range []string{oldID, oldGoal, newID, resentGoal} {
		if !strings.Contains(drained, want) {
			t.Fatalf("durable inbox omitted %q after baseline/post-baseline delivery:\n%s", want, drained)
		}
	}
}

func realAMQCommittedDeliveryWordingContract(t *testing.T, binary string) {
	t.Helper()
	raw, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, phrase := range [][]byte{
		[]byte("message %s has a committed delivery; retrying may duplicate it"),
		[]byte("delivery to %s committed at %s, but durability is indeterminate"),
		[]byte("do not retry blindly"),
	} {
		if !bytes.Contains(raw, phrase) {
			t.Fatalf("real AMQ binary no longer carries committed-delivery contract phrase %q", phrase)
		}
	}
	root := filepath.Join(t.TempDir(), ".agent-mail", "committed-contract")
	id := "real-amq-committed-contract"
	path := filepath.Join(root, "agents", "qa", "inbox", "new", id+".md")
	line := fmt.Sprintf("message %s has a committed delivery; retrying may duplicate it: delivery to qa committed at %s, but durability is indeterminate: injected sync failure; do not retry blindly", id, path)
	evidence, ok := parseCommittedDeliveryEvidence(line, fmt.Errorf("%s", line))
	if !ok || evidence.MessageID != id || evidence.FinalPath != path {
		t.Fatalf("parser rejected the real-AMQ committed wording contract: evidence=%+v ok=%t", evidence, ok)
	}
}

func realAMQThreeActorHundredMessageIsolation(t *testing.T, binary string) {
	t.Helper()
	project := t.TempDir()
	root := filepath.Join(project, ".agent-mail", "three-actor")
	realAMQInitAgents(t, binary, project, root, "sender", "consumer", "sibling")
	cleanEnv := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
	ids := make([]string, 0, 100)
	for i := 0; i < 100; i++ {
		out := realAMQCommand(t, binary, project, cleanEnv,
			"send", "--root", root, "--me", "sender", "--to", "consumer", "--thread", "p2p/sender__consumer",
			"--kind", "todo", "--subject", fmt.Sprintf("isolation-%03d", i), "--body", fmt.Sprintf("payload-%03d", i), "--json")
		id := parseSentMessageID(out)
		if id == "" {
			t.Fatalf("send %d omitted stable id: %s", i, out)
		}
		ids = append(ids, id)
	}
	sibling := realAMQCommand(t, binary, project, cleanEnv, "drain", "--root", root, "--me", "sibling", "--include-body")
	if strings.TrimSpace(sibling) != "" {
		t.Fatalf("wrong actor drained consumer deliveries: %s", sibling)
	}
	drained := realAMQCommand(t, binary, project, cleanEnv, "drain", "--root", root, "--me", "consumer", "--include-body", "--limit", "0")
	for i, id := range ids {
		if !strings.Contains(drained, id) || !strings.Contains(drained, fmt.Sprintf("payload-%03d", i)) {
			t.Fatalf("consumer drain omitted delivery %d id=%s", i, id)
		}
	}
	if again := strings.TrimSpace(realAMQCommand(t, binary, project, cleanEnv, "drain", "--root", root, "--me", "consumer", "--include-body")); again != "" {
		t.Fatalf("consumer second drain = %q, want empty", again)
	}
}

func realAMQExactInjectViaWakeRetirement(t *testing.T, binary string) {
	t.Helper()
	project := realAMQSafeInjectViaFixtureProject(t)
	root := filepath.Join(project, ".agent-mail", "wake-retire")
	realAMQInitAgents(t, binary, project, root, "sender", "consumer", "sibling")
	injector := filepath.Join(project, "injector.sh")
	if err := os.WriteFile(injector, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	ready := filepath.Join(project, "wake.ready")
	cleanEnv := amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
	wake := exec.Command(binary, "wake", "--root", root, "--me", "consumer", "--inject-via", injector, "--inject-arg", "fixed", "--ready-file", ready)
	wake.Dir, wake.Env = project, cleanEnv
	var wakeLog bytes.Buffer
	wake.Stdout, wake.Stderr = &wakeLog, &wakeLog
	if err := wake.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited && wake.Process != nil {
			_ = wake.Process.Kill()
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("wake did not become ready: %s", wakeLog.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	siblingReady := filepath.Join(project, "sibling-wake.ready")
	siblingWake := exec.Command(binary, "wake", "--root", root, "--me", "sibling", "--inject-via", injector, "--inject-arg", "fixed", "--ready-file", siblingReady)
	siblingWake.Dir, siblingWake.Env = project, cleanEnv
	var siblingLog bytes.Buffer
	siblingWake.Stdout, siblingWake.Stderr = &siblingLog, &siblingLog
	if err := siblingWake.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if siblingWake.Process != nil {
			_ = siblingWake.Process.Kill()
			_, _ = siblingWake.Process.Wait()
		}
	})
	deadline = time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(siblingReady); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sibling wake did not become ready: %s", siblingLog.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	unrelated := exec.Command("sleep", "60")
	if err := unrelated.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if unrelated.Process != nil {
			_ = unrelated.Process.Kill()
			_, _ = unrelated.Process.Wait()
		}
	})
	mailOut := realAMQCommand(t, binary, project, cleanEnv, "send", "--root", root, "--me", "sender", "--to", "consumer", "--subject", "preserve mailbox", "--body", "survives exact retire", "--json")
	mailID := parseSentMessageID(mailOut)
	if mailID == "" {
		t.Fatalf("pre-retirement mailbox send omitted stable id: %s", mailOut)
	}
	previous := runExactWakeRetire
	runExactWakeRetire = func(req amqCommandRequest) ([]byte, error) {
		out, err := realAMQTryCommand(binary, req.Dir, req.Env, req.Arg...)
		return []byte(out), err
	}
	t.Cleanup(func() { runExactWakeRetire = previous })
	waitErr := make(chan error, 1)
	go func() { waitErr <- wake.Wait() }()
	result := reapStaleArtifacts(filepath.Join(root, "agents", "consumer"), "consumer", root, false, launch.Record{CWD: project, AMQVersion: "0.45.0", WakePID: wake.Process.Pid, WakeInjectVia: injector, WakeInjectArgs: []string{"fixed"}}, &recordingTerminator{}, defaultDuplicateLaunchProbe)
	if result.failed() || (result.WakeRetirement != "amq_0_45_exact" && result.WakeRetirement != nativeWakeRetireSelfCleaned) || result.WakeKilled != wake.Process.Pid {
		t.Fatalf("exact retirement result=%+v wake_log=%s", result, wakeLog.String())
	}
	select {
	case err := <-waitErr:
		waited = true
		// Linux retirement exits by signal while Darwin's cooperative control
		// path exits cleanly. The successful exact retire result and absent lock
		// below are the cross-platform authority; either process exit is final.
		_ = err
	case <-time.After(10 * time.Second):
		t.Fatal("retired wake did not exit")
	}
	if defaultDuplicateLaunchProbe.PIDAlive(wake.Process.Pid) {
		t.Fatalf("exact retirement left wake pid %d alive", wake.Process.Pid)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "consumer", ".wake.lock")); !os.IsNotExist(err) {
		t.Fatalf("exact retirement left wake lock: %v", err)
	}
	if !defaultDuplicateLaunchProbe.PIDAlive(siblingWake.Process.Pid) {
		t.Fatalf("exact retirement killed sibling wake %d", siblingWake.Process.Pid)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "sibling", ".wake.lock")); err != nil {
		t.Fatalf("exact retirement removed sibling wake lock: %v", err)
	}
	if !defaultDuplicateLaunchProbe.PIDAlive(unrelated.Process.Pid) {
		t.Fatalf("exact retirement killed unrelated process %d", unrelated.Process.Pid)
	}
	drained := realAMQCommand(t, binary, project, cleanEnv, "drain", "--root", root, "--me", "consumer", "--include-body")
	if !strings.Contains(drained, mailID) || !strings.Contains(drained, "survives exact retire") {
		t.Fatalf("exact retirement did not preserve consumer mailbox:\n%s", drained)
	}
}

func realAMQSafeInjectViaFixtureProject(t *testing.T) string {
	t.Helper()
	defaultProject, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve default inject-via fixture: %v", err)
	}
	defaultErr := realAMQValidateSafeInjectViaAncestors(defaultProject)
	if defaultErr == nil {
		return defaultProject
	}

	type candidate struct {
		name string
		base string
	}
	var candidates []candidate
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		candidates = append(candidates, candidate{name: "user home", base: home})
	}
	if runnerTemp := strings.TrimSpace(os.Getenv("RUNNER_TEMP")); runnerTemp != "" {
		candidates = append(candidates, candidate{name: "RUNNER_TEMP", base: runnerTemp})
	}

	failures := []string{fmt.Sprintf("default temp %q: %v", defaultProject, defaultErr)}
	for _, candidate := range candidates {
		base, err := filepath.EvalSymlinks(candidate.base)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s %q: resolve: %v", candidate.name, candidate.base, err))
			continue
		}
		if err := realAMQValidateSafeInjectViaAncestors(base); err != nil {
			failures = append(failures, fmt.Sprintf("%s %q: %v", candidate.name, base, err))
			continue
		}
		project, err := os.MkdirTemp(base, ".amq-squad-inject-via-*")
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s %q: create fixture: %v", candidate.name, base, err))
			continue
		}
		if err := os.Chmod(project, 0o700); err != nil {
			_ = os.RemoveAll(project)
			failures = append(failures, fmt.Sprintf("%s %q: secure fixture: %v", candidate.name, project, err))
			continue
		}
		resolvedProject, err := filepath.EvalSymlinks(project)
		if err != nil {
			_ = os.RemoveAll(project)
			failures = append(failures, fmt.Sprintf("%s %q: resolve fixture: %v", candidate.name, project, err))
			continue
		}
		if err := realAMQValidateSafeInjectViaAncestors(resolvedProject); err != nil {
			_ = os.RemoveAll(project)
			failures = append(failures, fmt.Sprintf("%s %q: %v", candidate.name, resolvedProject, err))
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(resolvedProject) })
		return resolvedProject
	}

	t.Fatalf("no safe inject-via fixture location: %s", strings.Join(failures, "; "))
	return ""
}

func realAMQValidateSafeInjectViaAncestors(path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("resolve: %w", err)
	}
	for dir := resolved; ; dir = filepath.Dir(dir) {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("stat ancestor %q: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("ancestor %q is not a directory", dir)
		}
		if info.Mode().Perm()&0o022 != 0 {
			return fmt.Errorf("ancestor %q is group/world-writable", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil
		}
	}
}

func realAMQOrchestrationContract(t *testing.T, binary, version, profile string) {
	t.Helper()
	const session = "issue-471"
	project := t.TempDir()
	ctx := realAMQProfileContext(project, profile, session, "cto")
	realAMQInitAgents(t, binary, project, ctx.Root, "cto", "qa", team.DefaultOperatorHandle)
	op := team.DefaultOperator()
	cfg := team.Team{
		Project:      project,
		Operator:     &op,
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: session},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: session},
		},
	}
	if err := team.WriteProfile(project, profile, cfg); err != nil {
		t.Fatal(err)
	}
	recordBase := filepath.Dir(ctx.Root)
	if profile != team.DefaultProfile {
		recordBase = ctx.Root
	}
	seedAgentRecord(t, filepath.Dir(ctx.Root), session, "cto", launch.Record{
		CWD: project, Binary: "codex", Role: "cto", Handle: "cto", Session: session,
		Root: ctx.Root, BaseRoot: recordBase, TeamProfile: profile, External: true,
	})

	realAMQConcurrentDrainedSend(t, binary, ctx)

	args := []string{
		"--project", project, "--profile", profile, "--session", session,
		"--role", "qa", "--from", "cto", "--thread", "p2p/cto__qa",
		"--subject", "real dispatch contract", "--body", "real task outbox receipt body",
		"--create-task", "--no-wake", "--json",
	}
	stdout, stderr, err := captureOutput(t, func() error { return runDispatch(args) })
	if err != nil {
		t.Fatalf("real dispatch: %v\nstderr:\n%s", err, stderr)
	}
	dispatch := decodeJSONEnvelope[mutationResult](t, stdout).Data
	if dispatch.TaskID == "" || dispatch.MessageID == "" || dispatch.DeliveryReceipt == nil {
		t.Fatalf("real dispatch omitted task/message/receipt linkage: %+v", dispatch)
	}
	if !sameResolvedDir(dispatch.Root, ctx.Root) || !sameResolvedDir(dispatch.DeliveryReceipt.Root, ctx.Root) {
		t.Fatalf("real dispatch roots = %q / %q, want %q", dispatch.Root, dispatch.DeliveryReceipt.Root, ctx.Root)
	}
	qa := ctx
	qa.Me = "qa"
	drained := realAMQCommand(t, binary, project, amqCommandEnv(qa), "drain", "--include-body")
	if !strings.Contains(drained, dispatch.MessageID) || !strings.Contains(drained, "real task outbox receipt body") {
		t.Fatalf("real dispatch drain missing message evidence:\n%s", drained)
	}
	if err := refreshDeliveryReceipt(dispatch.DeliveryReceipt, project, profile, session); err != nil {
		t.Fatalf("refresh real dispatch receipt: %v", err)
	}
	if dispatch.DeliveryReceipt.DeliveryState != deliveryStateDrained || dispatch.DeliveryReceipt.NativeStage != "drained" {
		t.Fatalf("refreshed dispatch receipt = %+v", dispatch.DeliveryReceipt)
	}
	if err := writeDeliveryReceipt(project, profile, session, dispatch.DeliveryReceipt); err != nil {
		t.Fatalf("persist refreshed dispatch receipt: %v", err)
	}
	persistedReceipt, err := readDeliveryReceipt(dispatch.DeliveryReceipt.Path)
	if err != nil {
		t.Fatalf("read persisted dispatch receipt: %v", err)
	}
	if persistedReceipt.MessageID != dispatch.MessageID || persistedReceipt.TaskID != dispatch.TaskID || persistedReceipt.OutboxIntentID == "" || persistedReceipt.DeliveryState != deliveryStateDrained {
		t.Fatalf("persisted dispatch receipt linkage = %+v", persistedReceipt)
	}
	task, err := taskstore.ShowForProfile(project, profile, session, dispatch.TaskID)
	if err != nil {
		t.Fatalf("show real dispatched task: %v", err)
	}
	if task.Status != taskstore.StatusInProgress || task.AssignedTo != "qa" || task.Dispatch == nil || task.Dispatch.MessageID != dispatch.MessageID || len(task.Outbox) != 1 {
		t.Fatalf("real dispatched task linkage = %+v", task)
	}
	intent := task.Outbox[0]
	receiptPathMatches := intent.ReceiptPath == dispatch.DeliveryReceipt.Path ||
		filepath.Base(intent.ReceiptPath) == filepath.Base(dispatch.DeliveryReceipt.Path) && sameResolvedDir(filepath.Dir(intent.ReceiptPath), filepath.Dir(dispatch.DeliveryReceipt.Path))
	if intent.State != taskstore.OutboxDelivered || intent.MessageID != dispatch.MessageID || intent.ReceiptAttemptID != dispatch.DeliveryReceipt.AttemptID || !receiptPathMatches {
		t.Fatalf("real dispatched outbox linkage = %+v", intent)
	}

	const gate = "gate/real-amq-471"
	const action = "external_send"
	const target = "real AMQ compatibility canary"
	const note = "real AMQ typed envelope"
	questionStdout, questionStderr, err := captureOutput(t, func() error {
		return runGateRaise([]string{
			"--project", project, "--profile", profile, "--session", session, "--me", "cto",
			"--gate", gate, "--kind", action, "--action", action, "--target", target, "--note", note, "--json",
		})
	})
	if err != nil {
		t.Fatalf("real typed gate raise: %v\nstderr:\n%s", err, questionStderr)
	}
	questionMutation := decodeJSONEnvelope[mutationResult](t, questionStdout).Data
	questionID := questionMutation.MessageID
	if questionID == "" || questionMutation.Thread != gate || questionMutation.DeliveryReceipt == nil {
		t.Fatalf("real typed gate question omitted durable identity: %+v", questionMutation)
	}
	question, err := humanApprovalQuestion(project, profile, session, gate, action, action, target)
	if err != nil || question.ID != questionID || question.AuthorizationRequest == nil || question.AuthorizationRequest.Note != note {
		t.Fatalf("real strict gate question id=%q want=%q err=%v", question.ID, questionID, err)
	}
	answerStdout, answerStderr, err := captureOutput(t, func() error {
		return runOperatorAnswer([]string{
			"--project", project, "--profile", profile, "--session", session,
			"--gate", gate, "--to", "cto", "--approved",
			"--kind", action, "--action", action, "--target", target, "--json",
		})
	})
	if err != nil {
		t.Fatalf("real structured operator answer: %v\nstderr:\n%s", err, answerStderr)
	}
	answer := decodeJSONEnvelope[mutationResult](t, answerStdout).Data
	if answer.MessageID == "" || answer.DeliveryReceipt == nil || answer.Thread != gate {
		t.Fatalf("real structured operator answer = %+v", answer)
	}
	if answer.DeliveryReceipt.MessageID != answer.MessageID || !sameResolvedDir(answer.DeliveryReceipt.Root, ctx.Root) || answer.DeliveryReceipt.DeliveryState != deliveryStateDeliveredNotDrained || answer.DeliveryReceipt.NativeStage != "" {
		t.Fatalf("real structured operator answer receipt = %+v; message=%s root=%s", answer.DeliveryReceipt, answer.MessageID, ctx.Root)
	}
	approvalReceiptPath := selfApprovalReceiptPath(project, profile, session, gate, questionID, answer.MessageID)
	approvalReceiptBytes, err := os.ReadFile(approvalReceiptPath)
	if err != nil {
		t.Fatalf("read persisted human approval receipt: %v", err)
	}
	approvalReceipt, err := operatorauth.DecodeReceipt(approvalReceiptBytes)
	if err != nil {
		t.Fatalf("decode persisted human approval receipt: %v", err)
	}
	if approvalReceipt.SchemaVersion != operatorauth.ReceiptSchemaVersion || approvalReceipt.TaxonomyVersion != operatorauth.ActionTaxonomyVersion || approvalReceipt.QuestionMessageID != questionID || approvalReceipt.AnswerMessageID != answer.MessageID || approvalReceipt.Gate != gate || approvalReceipt.GateKind != action || approvalReceipt.Action != action || approvalReceipt.Target != target || approvalReceipt.Note != note || approvalReceipt.Decision != actionDecisionApproved || approvalReceipt.AnsweredBy != team.DefaultOperatorHandle || approvalReceipt.ApprovalSource != "human" || approvalReceipt.SelfApproved {
		t.Fatalf("persisted human approval receipt tuple = %+v; question=%s answer=%s", approvalReceipt, questionID, answer.MessageID)
	}
	verifyStdout, verifyStderr, err := captureOutput(t, func() error {
		return runVerifyAction([]string{
			"--project", project, "--profile", profile, "--session", session,
			"--gate", gate, "--action", action, "--target", target, "--to", "cto", "--json",
		})
	})
	if err != nil {
		t.Fatalf("real verify action: %v\nstderr:\n%s", err, verifyStderr)
	}
	verified := decodeJSONEnvelope[verifyActionResult](t, verifyStdout).Data
	if verified.Decision != actionDecisionApproved || verified.AnsweredBy != team.DefaultOperatorHandle || verified.MessageID != answer.MessageID || verified.Action != action || verified.Target != target || verified.Gate != gate {
		t.Fatalf("real verify action tuple = %+v; question=%s answer=%s", verified, questionID, answer.MessageID)
	}

	doctor := defaultDoctorExecution(project)
	doctor.Profile = profile
	doctor.ResolveAMQEnv = func(string) (amqEnv, error) {
		var got amqEnv
		raw, err := realAMQTryCommand(binary, project, amqCommandEnv(ctx), "env", "--json")
		if err != nil {
			return amqEnv{}, err
		}
		if err := json.Unmarshal([]byte(raw), &got); err != nil {
			return amqEnv{}, err
		}
		return got, nil
	}
	check := doctorCheckAMQVersion(doctor)
	if check.Status != doctorOK || !strings.Contains(check.Detail, strings.TrimPrefix(version, "v")) {
		t.Fatalf("real doctor version check = %+v", check)
	}
}

func realAMQProfileContext(project, profile, session, me string) amqContext {
	if profile == team.DefaultProfile {
		root := filepath.Join(project, ".agent-mail", session)
		return amqContext{ProjectDir: project, Profile: profile, Env: amqEnv{BaseRoot: filepath.Dir(root)}, Root: root, Me: me, Session: session, PinMode: amqPinSessionful}
	}
	root := filepath.Join(project, ".agent-mail", profile, session)
	return amqContext{ProjectDir: project, Profile: profile, Root: root, Me: me, Session: session, PinMode: amqPinExactRoot}
}

func realAMQConcurrentDrainedSend(t *testing.T, binary string, cto amqContext) {
	t.Helper()
	const subject = "concurrent drained receipt"
	const body = "real concurrent wait-for drained body"
	type commandResult struct {
		out string
		err error
	}
	done := make(chan commandResult, 1)
	go func() {
		out, err := realAMQTryCommand(binary, cto.ProjectDir, amqCommandEnv(cto), "send", "--to", "qa", "--thread", "p2p/cto__qa", "--kind", "todo", "--subject", subject, "--body", body, "--json", "--wait-for", "drained", "--wait-timeout", "10s")
		done <- commandResult{out: out, err: err}
	}()

	qa := cto
	qa.Me = "qa"
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case result := <-done:
			t.Fatalf("real concurrent send completed before drain: out=%s err=%v", result.out, result.err)
		default:
		}
		listed, err := realAMQTryCommand(binary, qa.ProjectDir, amqCommandEnv(qa), "list", "--new", "--json")
		if err == nil && strings.Contains(listed, subject) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("real concurrent message did not reach qa inbox: last list=%q err=%v", listed, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	drained := realAMQCommand(t, binary, qa.ProjectDir, amqCommandEnv(qa), "drain", "--include-body")
	if !strings.Contains(drained, body) {
		t.Fatalf("real concurrent drain missing body:\n%s", drained)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("real concurrent send wait: %v", result.err)
		}
		msgID := parseSentMessageID(result.out)
		if msgID == "" || !strings.Contains(drained, msgID) {
			t.Fatalf("real concurrent receipt/drain id mismatch: out=%s drain=%s", result.out, drained)
		}
		receipt, ok := nativeReceiptFromSendOutput([]byte(result.out), msgID, "qa")
		if !ok || receipt.Stage != "drained" || receipt.MsgID != msgID || receipt.Consumer != "qa" {
			t.Fatalf("real concurrent native receipt = %+v ok=%v", receipt, ok)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("real concurrent send did not observe drained receipt")
	}
	if again := strings.TrimSpace(realAMQCommand(t, binary, qa.ProjectDir, amqCommandEnv(qa), "drain", "--include-body")); again != "" {
		t.Fatalf("real concurrent second drain = %q, want empty", again)
	}
}

func realAMQInit(t *testing.T, binary, project, root string) {
	t.Helper()
	realAMQInitAgents(t, binary, project, root, "lead", "worker")
}

func realAMQInitAgents(t *testing.T, binary, project, root string, agents ...string) {
	t.Helper()
	realAMQCommand(t, binary, project, amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ())), "init", "--root", root, "--agents", strings.Join(agents, ","))
}

func realAMQRoundTrip(t *testing.T, binary string, lead amqContext, root, label string) {
	t.Helper()
	outside := filepath.Join(t.TempDir(), "must-not-be-used")
	t.Setenv("AMQ_GLOBAL_ROOT", outside)
	leadEnv := amqCommandEnv(lead)
	if lead.PinMode == amqPinExactRoot && envHasPrefix(leadEnv, "AM_SESSION", "") {
		t.Fatalf("%s exact-root tuple leaked AM_SESSION: %#v", label, leadEnv)
	}
	if lead.PinMode == amqPinSessionful && !envHas(leadEnv, "AM_SESSION", lead.Session) {
		t.Fatalf("%s sessionful tuple omitted AM_SESSION=%q: %#v", label, lead.Session, leadEnv)
	}
	if envHasPrefix(leadEnv, "AMQ_GLOBAL_ROOT", "") {
		t.Fatalf("%s tuple leaked stale AMQ_GLOBAL_ROOT: %#v", label, leadEnv)
	}

	var got amqEnv
	if err := json.Unmarshal([]byte(realAMQCommand(t, binary, lead.ProjectDir, leadEnv, "env", "--json")), &got); err != nil {
		t.Fatalf("%s bare amq env JSON: %v", label, err)
	}
	if !sameResolvedDir(got.Root, root) {
		t.Fatalf("%s bare amq env root = %q, want %q", label, got.Root, root)
	}

	body := "real AMQ " + label + " round trip"
	realAMQCommand(t, binary, lead.ProjectDir, leadEnv, "send", "--to", "worker", "--subject", "compatibility", "--body", body, "--kind", "todo")
	worker := lead
	worker.Me = "worker"
	drained := realAMQCommand(t, binary, worker.ProjectDir, amqCommandEnv(worker), "drain", "--include-body")
	if !strings.Contains(drained, body) {
		t.Fatalf("%s bare amq drain did not contain delivered body:\n%s", label, drained)
	}
	if again := strings.TrimSpace(realAMQCommand(t, binary, worker.ProjectDir, amqCommandEnv(worker), "drain", "--include-body")); again != "" {
		t.Fatalf("%s second bare amq drain = %q, want empty", label, again)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("%s command touched stale global root %q: %v", label, outside, err)
	}
}

func realAMQCommand(t *testing.T, binary, dir string, env []string, args ...string) string {
	t.Helper()
	out, err := realAMQTryCommand(binary, dir, env, args...)
	if err != nil {
		t.Fatalf("real amq %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func realAMQTryCommand(binary, dir string, env []string, args ...string) (string, error) {
	cmd := exec.Command(binary, args...)
	cmd.Dir = dir
	if env == nil {
		env = amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ()))
	}
	cmd.Env = amqexec.NoUpdateCheckEnv(env)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%w\nstderr:\n%s", err, stderr.String())
	}
	return string(out), nil
}
