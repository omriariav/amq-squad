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
		t.Run("exact inject-via wake retirement", func(t *testing.T) {
			realAMQExactInjectViaWakeRetirement(t, binary)
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
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(project, ".agent-mail", "wake-retire")
	realAMQInitAgents(t, binary, project, root, "consumer")
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
	previous := runExactWakeRetire
	runExactWakeRetire = func(req amqCommandRequest) ([]byte, error) {
		out, err := realAMQTryCommand(binary, req.Dir, req.Env, req.Arg...)
		return []byte(out), err
	}
	t.Cleanup(func() { runExactWakeRetire = previous })
	result, err := retireWakeWithAMQ045(launch.Record{CWD: project, AMQVersion: "0.45.0", WakePID: wake.Process.Pid, WakeInjectVia: injector, WakeInjectArgs: []string{"fixed"}}, root, "consumer")
	if err != nil || result.Status != "retired" || result.PID != wake.Process.Pid {
		t.Fatalf("exact retirement result=%+v err=%v wake_log=%s", result, err, wakeLog.String())
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- wake.Wait() }()
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
	if _, err := os.Stat(filepath.Join(root, "agents", "consumer", ".wake.lock")); !os.IsNotExist(err) {
		t.Fatalf("exact retirement left wake lock: %v", err)
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
	if intent.State != taskstore.OutboxDelivered || intent.MessageID != dispatch.MessageID || intent.ReceiptAttemptID != dispatch.DeliveryReceipt.AttemptID || intent.ReceiptPath != dispatch.DeliveryReceipt.Path {
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
