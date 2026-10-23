package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestDispatchSendArgs(t *testing.T) {
	got := dispatchSendArgs("/repo/.agent-mail/issue-96", "cto", "qa", "p2p/cto__qa", "todo", "Do X", "details here", "urgent", "drained", 60*time.Second)
	want := []string{
		"send", "--root", "/repo/.agent-mail/issue-96", "--me", "cto", "--to", "qa",
		"--thread", "p2p/cto__qa", "--kind", "todo", "--subject", "Do X", "--body", "details here", "--priority", "urgent",
		"--wait-for", "drained", "--wait-timeout", "1m0s",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("dispatchSendArgs = %v\nwant %v", got, want)
	}

	// Optional fields omitted when empty; body always present.
	got = dispatchSendArgs("/r", "a", "b", "", "", "", "body", "", "", 0)
	for _, bad := range []string{"--thread", "--kind", "--subject", "--priority", "--wait-for", "--wait-timeout"} {
		if containsString(got, bad) {
			t.Fatalf("empty %s should be omitted: %v", bad, got)
		}
	}
	if !containsString(got, "--body") {
		t.Fatalf("body must always be sent: %v", got)
	}
}

func TestDispatchReceiptWaitForPolicy(t *testing.T) {
	if got := dispatchReceiptWaitFor("answer", ""); got != "drained" {
		t.Fatalf("answer default wait = %q, want drained", got)
	}
	if got := dispatchReceiptWaitFor("todo", ""); got != "" {
		t.Fatalf("todo default wait = %q, want none", got)
	}
	if got := dispatchReceiptWaitFor("todo", "dlq"); got != "dlq" {
		t.Fatalf("explicit wait = %q, want dlq", got)
	}
	if got := dispatchReceiptWaitFor("answer", "none"); got != "" {
		t.Fatalf("answer none opt-out = %q, want empty", got)
	}
}

func TestParseSentMessageID(t *testing.T) {
	out := "Sent 2026-06-21T09-12-42.415Z_pid63003_cc12d3ad to qa (session: , root: /x/.agent-mail/clitest)\n"
	if got := parseSentMessageID(out); got != "2026-06-21T09-12-42.415Z_pid63003_cc12d3ad" {
		t.Fatalf("parseSentMessageID = %q", got)
	}
	if got := parseSentMessageID("drained by lead\nnothing here"); got != "" {
		t.Fatalf("no Sent line should yield empty, got %q", got)
	}
}

func withDispatchAMQCommandErrorSeam(t *testing.T, env amqEnv, output string, sendErr error) *[]amqCommandRequest {
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
		return []byte(output), sendErr
	}
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = prevEnv
		runAMQCommand = prevRun
	})
	return &calls
}

func TestSimpleTaskDispatchFailureLeavesPlainClaimAndAllowsSecondSend(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withDispatchAMQCommandErrorSeam(t,
		amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "", nil,
	)
	attempt := 0
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		*calls = append(*calls, req)
		attempt++
		if attempt == 1 {
			return nil, errors.New("transport unavailable")
		}
		return []byte("Sent msg-second to qa\n"), nil
	}

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{
			"--session", "issue-96", "--role", "qa", "--subject", "plain task",
			"--body", "do the work", "--create-task",
		})
	})
	if err == nil || !strings.Contains(err.Error(), "task remains in_progress") {
		t.Fatalf("first dispatch error = %v, want visible in_progress recovery guidance", err)
	}
	tasks, err := taskstore.ListForProfile(dir, team.DefaultProfile, "issue-96")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 {
		t.Fatalf("tasks = %+v, want one created task", tasks)
	}
	created := tasks[0]
	if created.Status != taskstore.StatusInProgress || created.AssignedTo != "qa" {
		t.Fatalf("task after failed send = %+v, want qa/in_progress", created)
	}
	if len(created.Outbox) != 0 || created.Dispatch != nil || created.CompletionReconcile != nil || created.CompletionLifecycle != nil {
		t.Fatalf("failed simple dispatch persisted retry/delivery state: %+v", created)
	}

	_, _, err = captureOutput(t, func() error {
		return runDispatch([]string{
			"--session", "issue-96", "--role", "qa", "--subject", "plain task",
			"--body", "do the work", "--task", created.ID,
		})
	})
	if err != nil {
		t.Fatalf("second dispatch of already-claimed task: %v", err)
	}
	after, err := taskstore.ShowForProfile(dir, team.DefaultProfile, "issue-96", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != taskstore.StatusInProgress || after.AssignedTo != "qa" || len(after.Outbox) != 0 || after.Dispatch != nil {
		t.Fatalf("second dispatch changed plain task authority: %+v", after)
	}
	if attempt != 2 {
		t.Fatalf("AMQ send attempts = %d, want 2 explicit attempts", attempt)
	}
}

func TestDispatchDrainCommandQuotesScope(t *testing.T) {
	got := dispatchDrainCommand("/Code/my app/.agent-mail/issue-96", "lead user")
	want := "amq drain --include-body --root '/Code/my app/.agent-mail/issue-96' --me 'lead user'"
	if got != want {
		t.Fatalf("dispatchDrainCommand = %q, want %q", got, want)
	}
}

func TestResolveDispatchSender(t *testing.T) {
	orchestrated := team.Team{
		Orchestrated: true, Lead: "cto",
		Members: []team.Member{{Role: "cto", Handle: "cto"}, {Role: "qa", Handle: "qa"}},
	}
	flat := team.Team{Members: []team.Member{{Role: "qa", Handle: "qa"}}}

	// Explicit --from always wins.
	if got, err := resolveDispatchSender(orchestrated, "lead-x"); err != nil || got != "lead-x" {
		t.Fatalf("explicit from = %q, %v; want lead-x", got, err)
	}
	// Orchestrated team defaults to the lead handle.
	if got, err := resolveDispatchSender(orchestrated, ""); err != nil || got != "cto" {
		t.Fatalf("orchestrated default = %q, %v; want cto", got, err)
	}
	// Non-orchestrated, no AM_ME -> usage error guiding the operator to --from.
	t.Setenv("AM_ME", "")
	_, err := resolveDispatchSender(flat, "")
	if err == nil || !strings.Contains(err.Error(), "--from") {
		t.Fatalf("flat team without AM_ME should require --from, got %v", err)
	}
	// Non-orchestrated falls back to AM_ME when present.
	t.Setenv("AM_ME", "bootstrapped-lead")
	if got, err := resolveDispatchSender(flat, ""); err != nil || got != "bootstrapped-lead" {
		t.Fatalf("AM_ME fallback = %q, %v; want bootstrapped-lead", got, err)
	}
}

func writeDispatchTeam(t *testing.T, dir string) {
	t.Helper()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Project:      dir,
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunDispatchWorktreeMemberUsesCanonicalTeamRoot(t *testing.T) {
	teamHome := t.TempDir()
	worktree := t.TempDir()
	const (
		profile = "review"
		session = "issue-96"
	)
	if err := team.WriteProfile(teamHome, profile, team.Team{
		Project:      teamHome,
		Workstream:   session,
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: session},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: session, CWD: worktree},
		},
	}); err != nil {
		t.Fatal(err)
	}

	calls := withAMQCommandSeams(t, amqEnv{}, "Sent msg-worktree to qa\n")
	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{
			"--project", teamHome,
			"--profile", profile,
			"--session", session,
			"--role", "qa",
			"--subject", "worktree root regression",
			"--body", "run",
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("dispatch worktree member: %v\n%s", err, stdout)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq calls = %d, want 1", len(*calls))
	}
	canonicalRoot := filepath.Join(teamHome, ".agent-mail", profile, session)
	if got := amqFlagValue((*calls)[0].Arg, "root"); got != canonicalRoot {
		t.Fatalf("dispatch root = %q, want canonical team root %q (member worktree %q must not be root authority)", got, canonicalRoot, worktree)
	}
	if got := (*calls)[0].Dir; got != teamHome {
		t.Fatalf("amq send cwd = %q, want canonical team home %q", got, teamHome)
	}
	result := decodeJSONEnvelope[mutationResult](t, stdout)
	if result.Data.Root != canonicalRoot {
		t.Fatalf("dispatch envelope root = %q, want %q", result.Data.Root, canonicalRoot)
	}
	if _, statErr := os.Stat(filepath.Join(worktree, ".agent-mail")); !os.IsNotExist(statErr) {
		t.Fatalf("dispatch touched worktree-local .agent-mail: %v", statErr)
	}
}

func TestRunDispatchRejectsLegacyLeadershipEpochBeforeArtifacts(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-epoch to qa\n")
	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--leadership-epoch", "1", "--subject", "legacy", "--body", "run", "--create-task"})
	})
	if err == nil || !strings.Contains(err.Error(), "--leadership-epoch is unavailable in simple task mode") {
		t.Fatalf("legacy leadership flag err=%v", err)
	}
	if tasks, err := taskstore.ListForProfile(dir, team.DefaultProfile, "issue-96"); err != nil || len(tasks) != 0 {
		t.Fatalf("legacy flag refusal persisted tasks: %+v err=%v", tasks, err)
	}
	if len(*calls) != 0 {
		t.Fatalf("legacy flag refusal invoked AMQ: %d", len(*calls))
	}
	if _, err := os.Stat(filepath.Join(dir, team.DirName, "receipts", "issue-96")); !os.IsNotExist(err) {
		t.Fatalf("legacy flag refusal persisted receipt artifact: %v", err)
	}
}

func writeDispatchActorContractTeam(t *testing.T, dir string) {
	t.Helper()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Schema: team.SchemaVersion, Project: dir, Orchestrated: true, Lead: "cto", LeadMode: team.LeadModePlanner, ExecutionMode: executionModeProjectLead,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "actor-contract", ActorMode: team.ActorModeReview},
			{Role: "dev", Binary: "codex", Handle: "dev", Session: "actor-contract", ActorMode: team.ActorModeImplementation},
			{Role: "reviewer", Binary: "codex", Handle: "reviewer", Session: "actor-contract", ActorMode: team.ActorModeReview},
		},
	}); err != nil {
		t.Fatal(err)
	}
}

func addClaimedDispatchTask(t *testing.T) {
	t.Helper()
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "existing", "--assign", "qa", "--session", "issue-96"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t1", "--me", "qa", "--session", "issue-96"})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunDispatchValidations(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"missing role", []string{"--session", "s", "--subject", "x", "--body", "y"}, "requires --role"},
		{"missing subject", []string{"--session", "s", "--role", "qa", "--body", "y"}, "requires --subject"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureOutput(t, func() error { return runDispatch(tc.args) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want contains %q", err, tc.want)
			}
		})
	}
}

func TestRunDispatchPrintsSessionAwareSummary(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent 2026abc_msgid to qa (session: , root: /x/.agent-mail/issue-96)\n")

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y"})
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// Our own summary: names the session, carries the msg id, NO empty "session:".
	for _, want := range []string{"Dispatched todo to qa", "on session issue-96", "msg 2026abc_msgid"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("summary missing %q in:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stdout, "Next: drain the child report with `amq drain --include-body --root") ||
		!strings.Contains(stdout, "--me cto") {
		t.Fatalf("summary missing rooted drain follow-up:\n%s", stdout)
	}
	if strings.Contains(stdout, "session: ,") {
		t.Fatalf("must not echo amq's empty-session line:\n%s", stdout)
	}
}

func TestRunDispatchJSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent msg-123 to qa (session: , root: /x/.agent-mail/issue-96)\n")

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --json: %v", err)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "dispatch" || env.Data.Status != "queued" || env.Data.MessageID != "msg-123" || env.Data.Handle != "qa" {
		t.Fatalf("bad dispatch envelope: %+v", env)
	}
	if strings.Contains(stdout, "Dispatched todo") {
		t.Fatalf("--json must not include human output:\n%s", stdout)
	}
	var foundDrain bool
	for _, a := range env.Data.Actions {
		if a.Kind == "drain" {
			foundDrain = true
			if !strings.Contains(a.Command, "amq drain --include-body --root") ||
				!strings.Contains(a.Command, "--me cto") {
				t.Fatalf("bad drain action: %+v", a)
			}
		}
	}
	if !foundDrain {
		t.Fatalf("dispatch JSON actions missing drain: %+v", env.Data.Actions)
	}
}

func TestRunDispatchAnswerDefaultsToDrainedWait(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent msg-answer to qa (session: , root: /x/.agent-mail/issue-96); drained by qa\n")

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--kind", "answer", "--subject", "A", "--body", "resolved", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch answer --json: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"--kind answer", "--wait-for drained", "--wait-timeout 1m0s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("answer dispatch missing %q: %s", want, got)
		}
	}
	if got := decodeJSONEnvelope[mutationResult](t, stdout).Data.MessageID; got != "msg-answer" {
		t.Fatalf("answer message id = %q, want msg-answer", got)
	}
}

func TestRunDispatchAnswerWaitCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-answer to qa\n")

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--kind", "answer", "--subject", "A", "--body", "resolved", "--wait-for", "none", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch answer --wait-for none: %v", err)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	if strings.Contains(got, "--wait-for") || strings.Contains(got, "--wait-timeout") {
		t.Fatalf("answer wait opt-out still passed wait flags: %s", got)
	}
}

func TestRunDispatchAnswerWaitTimeoutIsQueuedUnconfirmed(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withDispatchAMQCommandErrorSeam(t,
		amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent msg-timeout to qa (session: , root: /x/.agent-mail/issue-96)\ntimed out waiting for drained\n",
		errors.New("exit status 4: timed out waiting for drained"),
	)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--kind", "answer", "--subject", "A", "--body", "resolved", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch answer wait timeout should not fail: %v", err)
	}
	got := strings.Join((*calls)[0].Arg, " ")
	for _, want := range []string{"--kind answer", "--wait-for drained", "--wait-timeout 1m0s"} {
		if !strings.Contains(got, want) {
			t.Fatalf("answer timeout dispatch missing %q: %s", want, got)
		}
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.MessageID != "msg-timeout" {
		t.Fatalf("message id = %q, want msg-timeout", env.Data.MessageID)
	}
}

func TestRunDispatchAnswerWaitTimeoutHumanOutputWarnsDoNotResend(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withDispatchAMQCommandErrorSeam(t,
		amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent msg-timeout to qa (session: , root: /x/.agent-mail/issue-96)\ntimed out waiting for drained\n",
		errors.New("exit status 4: timed out waiting for drained"),
	)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--kind", "answer", "--subject", "A", "--body", "resolved"})
	})
	if err != nil {
		t.Fatalf("dispatch answer wait timeout should not fail: %v", err)
	}
	for _, want := range []string{"msg msg-timeout", "queued, drained receipt unconfirmed", "do NOT re-send"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("timeout human output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunDispatchCreateTaskClaimsBeforeMessageWithoutTaskDeliveryState(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-abc to qa\n")
	previousRun := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		committed, err := taskstore.ShowForProfile(dir, team.DefaultProfile, "issue-96", "t1")
		if err != nil {
			t.Fatalf("task must exist before AMQ send: %v", err)
		}
		if committed.Status != taskstore.StatusInProgress || committed.AssignedTo != "qa" || committed.Lease == nil {
			t.Fatalf("AMQ send ran before the atomic task claim: %+v", committed)
		}
		if len(committed.Outbox) != 0 || committed.Dispatch != nil {
			t.Fatalf("simple claim persisted prepared delivery state before AMQ send: %+v", committed)
		}
		return previousRun(req)
	}
	t.Cleanup(func() { runAMQCommand = previousRun })

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --create-task: %v", err)
	}
	result := decodeJSONEnvelope[mutationResult](t, stdout).Data
	if result.TaskID != "t1" || result.MessageID != "msg-abc" {
		t.Fatalf("simple dispatch result = %+v", result)
	}
	persisted, err := taskstore.ShowForProfile(dir, team.DefaultProfile, "issue-96", "t1")
	if err != nil || persisted.Status != taskstore.StatusInProgress || persisted.AssignedTo != "qa" || len(persisted.Outbox) != 0 || persisted.Dispatch != nil {
		t.Fatalf("simple dispatch task state = %+v err=%v", persisted, err)
	}
}

func TestRunDispatchTaskRejectsMismatchedAssigneeBeforeSend(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "existing", "--assign", "cto", "--session", "issue-96"})
	}); err != nil {
		t.Fatal(err)
	}
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-json to qa\n")

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--task", "t1"})
	})
	if err == nil || !strings.Contains(err.Error(), "task t1 is assigned to cto") {
		t.Fatalf("dispatch should reject mismatched task owner before send, got %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("mismatched task owner must not send AMQ, got %d calls", len(*calls))
	}
}

func TestRunDispatchTaskRejectsBlockedAndTerminalStatusesBeforeSend(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T) string
		want  string
	}{
		{
			name: "dependency blocked",
			setup: func(t *testing.T) string {
				t.Helper()
				if _, _, err := captureOutput(t, func() error {
					return runTask([]string{"add", "--title", "dependency", "--assign", "cto", "--session", "issue-96"})
				}); err != nil {
					t.Fatal(err)
				}
				if _, _, err := captureOutput(t, func() error {
					return runTask([]string{"add", "--title", "blocked child", "--assign", "qa", "--depends-on", "t1", "--session", "issue-96"})
				}); err != nil {
					t.Fatal(err)
				}
				return "t2"
			},
			want: "task t2 is blocked on t1 (pending); complete it before dispatch",
		},
		{
			name: "completed terminal",
			setup: func(t *testing.T) string {
				t.Helper()
				addClaimedDispatchTask(t)
				if _, _, err := captureOutput(t, func() error {
					return runTask([]string{"done", "t1", "--me", "qa", "--evidence", "accepted", "--session", "issue-96"})
				}); err != nil {
					t.Fatal(err)
				}
				return "t1"
			},
			want: "task t1 is completed; dispatch requires pending or in_progress",
		},
		{
			name: "failed terminal",
			setup: func(t *testing.T) string {
				t.Helper()
				addClaimedDispatchTask(t)
				if _, _, err := captureOutput(t, func() error {
					return runTask([]string{"fail", "t1", "--me", "qa", "--reason", "failed", "--session", "issue-96"})
				}); err != nil {
					t.Fatal(err)
				}
				return "t1"
			},
			want: "task t1 is failed; dispatch requires pending or in_progress",
		},
		{
			name: "blocked terminal",
			setup: func(t *testing.T) string {
				t.Helper()
				addClaimedDispatchTask(t)
				if _, _, err := captureOutput(t, func() error {
					return runTask([]string{"block", "t1", "--me", "qa", "--reason", "blocked", "--session", "issue-96"})
				}); err != nil {
					t.Fatal(err)
				}
				return "t1"
			},
			want: "task t1 is blocked; dispatch requires pending or in_progress",
		},
		{
			name: "cancelled terminal",
			setup: func(t *testing.T) string {
				t.Helper()
				addClaimedDispatchTask(t)
				if _, _, err := captureOutput(t, func() error {
					return runTask([]string{"cancel", "t1", "--me", "qa", "--reason", "superseded", "--session", "issue-96"})
				}); err != nil {
					t.Fatal(err)
				}
				return "t1"
			},
			want: "task t1 is cancelled; dispatch requires pending or in_progress",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			writeDispatchTeam(t, dir)
			taskID := tc.setup(t)
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-json to qa\n")

			_, _, err := captureOutput(t, func() error {
				return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--task", taskID})
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("dispatch error = %v, want %q", err, tc.want)
			}
			if len(*calls) != 0 {
				t.Fatalf("blocked/terminal task must not send AMQ, got %d calls", len(*calls))
			}
		})
	}
}

func TestRunDispatchTaskJSONEnvelope(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "existing", "--assign", "qa", "--session", "issue-96"})
	}); err != nil {
		t.Fatal(err)
	}
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-json to qa\n")

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--task", "t1", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --task --json: %v", err)
	}
	for _, want := range []string{"\"kind\": \"dispatch\"", "\"task_id\": \"t1\"", "\"message_id\": \"msg-json\"", "\"assignee\": \"qa\""} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("dispatch json missing %q:\n%s", want, stdout)
		}
	}
	persisted, err := taskstore.ShowForProfile(dir, team.DefaultProfile, "issue-96", "t1")
	if err != nil || persisted.Status != taskstore.StatusInProgress || persisted.AssignedTo != "qa" || len(persisted.Outbox) != 0 || persisted.Dispatch != nil {
		t.Fatalf("dispatch --task should leave one plain claimed task: %+v err=%v", persisted, err)
	}
}

// TestDispatchMismatchedSenderSetsFromInTask is the production-path test for
// #176: when dispatch --from orchestrator is used with a team whose lead is cto,
// the AMQ message is sent with --me orchestrator so the task's From field is
// "orchestrator". The worker drains it, sees From=orchestrator, and bootstrap
// tells it to reply there — not to the team.json lead.
func TestDispatchMismatchedSenderSetsFromInTask(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir) // team: orchestrated, lead=cto handle=cto
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-abc to qa\n")

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa",
			"--from", "orchestrator", "--subject", "X", "--body", "y"})
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// The AMQ message must carry --me orchestrator so its From field is the
	// dispatcher, not the team.json lead. Workers draining it reply to From.
	if len(*calls) == 0 {
		t.Fatal("no amq calls made")
	}
	sendArgs := (*calls)[0].Arg
	meIdx := -1
	for i, a := range sendArgs {
		if a == "--me" {
			meIdx = i
			break
		}
	}
	if meIdx < 0 || meIdx+1 >= len(sendArgs) || sendArgs[meIdx+1] != "orchestrator" {
		t.Fatalf("amq send --me should be orchestrator (dispatcher); args: %v", sendArgs)
	}
}

// TestRunDispatchWarnsMismatchedSender covers option 3 of #176: when
// dispatch --from differs from the team.json configured lead, a notice is
// printed to stderr so the operator knows children will report to the
// dispatcher, not the configured lead mailbox.
func TestRunDispatchWarnsMismatchedSender(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir) // team: orchestrated, lead=cto handle=cto
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-abc to qa\n")

	_, stderr, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa",
			"--from", "orchestrator", "--subject", "X", "--body", "y"})
	})
	if err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", err, stderr)
	}
	// notice must name both the dispatcher and the configured lead
	for _, want := range []string{"orchestrator", "cto"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("mismatch notice missing %q; stderr:\n%s", want, stderr)
		}
	}
}

func TestRunDispatchNoWarnWhenSenderMatchesLead(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir) // team: orchestrated, lead=cto handle=cto
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-abc to qa\n")

	_, stderr, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa",
			"--from", "cto", "--subject", "X", "--body", "y"})
	})
	if err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stderr, "notice:") {
		t.Fatalf("no mismatch notice expected when sender == configured lead; stderr:\n%s", stderr)
	}
}

func TestRunDispatchSendsDurably(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent abc123 to qa\n")

	stdout, stderr, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run the suite"})
	})
	if err != nil {
		t.Fatalf("dispatch: %v\nstderr:\n%s", err, stderr)
	}
	if len(*calls) != 1 {
		t.Fatalf("amq send calls = %d, want 1", len(*calls))
	}
	got := strings.Join((*calls)[0].Arg, " ")
	// Durable send to the resolved root, from the orchestration lead, to qa.
	for _, want := range []string{"send", "--root", ".agent-mail/issue-96", "--me cto", "--to qa", "--kind todo", "--subject Validate", "--body run the suite"} {
		if !strings.Contains(got, want) {
			t.Fatalf("send args missing %q: %s", want, got)
		}
	}
	if !strings.Contains(stdout, "msg abc123") {
		t.Fatalf("amq send output should pass through: %q", stdout)
	}
	if strings.Contains(stderr, "Nudged") || strings.Contains(stderr, "pane") {
		t.Fatalf("3.0 dispatch never nudges a pane, got:\n%s", stderr)
	}
}

func TestRunDispatchRejectsReviewActorImplementAndLifecycleBeforeSideEffects(t *testing.T) {
	for _, intent := range []string{taskstore.IntentImplement, taskstore.IntentLifecycle} {
		for _, path := range []string{"create-task", "existing-task"} {
			t.Run(intent+"_"+path, func(t *testing.T) {
				dir := t.TempDir()
				chdir(t, dir)
				writeDispatchActorContractTeam(t, dir)
				calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent forbidden to reviewer\n")
				args := []string{
					"--session", "actor-contract", "--role", "reviewer", "--subject", "Forbidden", "--body", "do not run",
				}
				var before taskstore.Task
				if path == "create-task" {
					args = append(args,
						"--create-task", "--task-intent", intent, "--task-artifact", "artifact",
						"--task-expected-base", strings.Repeat("a", 40), "--task-implementer", "reviewer", "--task-reviewer", "cto",
					)
				} else {
					created, err := taskstore.AddForProfile(dir, team.DefaultProfile, "actor-contract", taskstore.AddInput{
						Title: "existing forbidden", Intent: intent, Artifact: "artifact", ExpectedBaseSHA: strings.Repeat("a", 40),
						Implementer: "reviewer", Reviewer: "cto", AssignTo: "reviewer",
					}, taskNow())
					if err != nil {
						t.Fatalf("seed existing task: %v", err)
					}
					before = created
					args = append(args, "--task", created.ID)
				}
				_, _, err := captureOutput(t, func() error { return runDispatch(args) })
				if err == nil || !strings.Contains(err.Error(), "EffectiveActorMode=review") || !strings.Contains(err.Error(), "current_actor_implementation_allowed=false") {
					t.Fatalf("review actor %s %s refusal = %v", intent, path, err)
				}
				if len(*calls) != 0 {
					t.Fatalf("review actor refusal crossed send/pane boundary: amq=%v", *calls)
				}
				tasks, listErr := taskstore.ListForProfile(dir, team.DefaultProfile, "actor-contract")
				if listErr != nil {
					t.Fatal(listErr)
				}
				if path == "create-task" && len(tasks) != 0 {
					t.Fatalf("create-task refusal persisted task/outbox: %+v", tasks)
				}
				if path == "existing-task" {
					after, showErr := taskstore.ShowForProfile(dir, team.DefaultProfile, "actor-contract", before.ID)
					if showErr != nil || !reflect.DeepEqual(after, before) {
						t.Fatalf("existing-task refusal mutated task/outbox: before=%+v after=%+v err=%v", before, after, showErr)
					}
				}
				if _, statErr := os.Stat(filepath.Join(dir, team.DirName, "receipts", "actor-contract")); !os.IsNotExist(statErr) {
					t.Fatalf("review actor refusal persisted receipt artifact: %v", statErr)
				}
			})
		}
	}
}

func TestDispatchWaitPostureGuardsOwnedSendAndPreservesNoWait(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	installAMQWaitPostureRuntime(t, dir, "issue-96", "gate/release")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-dispatch to qa\n")

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--kind", "answer", "--subject", "answer", "--body", "body"})
	})
	if err == nil || !strings.Contains(err.Error(), "gate/release") {
		t.Fatalf("dispatch wait posture error = %v", err)
	}

	_, _, err = captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--kind", "todo", "--subject", "todo", "--body", "body", "--wait-for", "none", "--wait-timeout", "-1s"})
	})
	if err != nil || len(*calls) != 1 {
		t.Fatalf("dispatch no-wait must preserve behavior: calls=%d err=%v", len(*calls), err)
	}
}
