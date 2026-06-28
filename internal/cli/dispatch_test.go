package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func TestDispatchSendArgs(t *testing.T) {
	got := dispatchSendArgs("/repo/.agent-mail/issue-96", "cto", "qa", "p2p/cto__qa", "todo", "Do X", "details here", "urgent")
	want := []string{
		"send", "--root", "/repo/.agent-mail/issue-96", "--me", "cto", "--to", "qa",
		"--thread", "p2p/cto__qa", "--kind", "todo", "--subject", "Do X", "--body", "details here", "--priority", "urgent",
	}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("dispatchSendArgs = %v\nwant %v", got, want)
	}

	// Optional fields omitted when empty; body always present.
	got = dispatchSendArgs("/r", "a", "b", "", "", "", "body", "")
	for _, bad := range []string{"--thread", "--kind", "--subject", "--priority"} {
		if containsString(got, bad) {
			t.Fatalf("empty %s should be omitted: %v", bad, got)
		}
	}
	if !containsString(got, "--body") {
		t.Fatalf("body must always be sent: %v", got)
	}
}

func TestDispatchNudgePromptCarriesNoBody(t *testing.T) {
	// The nudge must point the agent at `amq drain` and never embed task content
	// (that lives only in the durable message — the single source of truth).
	if !strings.Contains(dispatchNudgePrompt, "amq drain") {
		t.Fatalf("nudge must tell the agent to drain: %q", dispatchNudgePrompt)
	}
	if strings.Contains(strings.ToLower(dispatchNudgePrompt), "%s") {
		t.Fatalf("nudge must be a fixed string with no body interpolation: %q", dispatchNudgePrompt)
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

func TestRunDispatchPrintsSessionAwareSummary(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent 2026abc_msgid to qa (session: , root: /x/.agent-mail/issue-96)\n")
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

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
	if !strings.Contains(stdout, "Next: collect the child report with `amq-squad collect") ||
		!strings.Contains(stdout, "--session issue-96 --me cto --timeout 120s --include-body") {
		t.Fatalf("summary missing collect follow-up:\n%s", stdout)
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
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --json: %v", err)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "dispatch" || env.Data.Status != "queued_and_nudged" || env.Data.MessageID != "msg-123" || env.Data.Handle != "qa" {
		t.Fatalf("bad dispatch envelope: %+v", env)
	}
	if strings.Contains(stdout, "Dispatched todo") {
		t.Fatalf("--json must not include human output:\n%s", stdout)
	}
	if len(*nudges) != 1 {
		t.Fatalf("expected one nudge, got %v", *nudges)
	}
	var foundCollect bool
	for _, a := range env.Data.Actions {
		if a.Kind == "collect" {
			foundCollect = true
			if !strings.Contains(a.Command, "amq-squad collect") ||
				!strings.Contains(a.Command, "--session issue-96") ||
				!strings.Contains(a.Command, "--me cto") ||
				!strings.Contains(a.Command, "--timeout 120s --include-body") {
				t.Fatalf("bad collect action: %+v", a)
			}
		}
	}
	if !foundCollect {
		t.Fatalf("dispatch JSON actions missing collect: %+v", env.Data.Actions)
	}
}

func TestDispatchCollectCommandQuotesScope(t *testing.T) {
	got := dispatchCollectCommand("/Code/my app", "issue-96", "lead user")
	want := "amq-squad collect --project '/Code/my app' --session issue-96 --me 'lead user' --timeout 120s --include-body"
	if got != want {
		t.Fatalf("dispatchCollectCommand = %q, want %q", got, want)
	}
}

func TestClassifyNudgeResult(t *testing.T) {
	busy := func(string) (bool, error) { return true, nil }
	idle := func(string) (bool, error) { return false, nil }
	unconfirmed := &tmuxpane.SubmitUnconfirmedError{PaneID: "%7", Attempts: 3}

	// Clean send -> nudged.
	if o, err := classifyNudgeResult("%7", nil, idle); err != nil || o.PaneID != "%7" {
		t.Fatalf("clean send: got %+v, %v", o, err)
	}
	// Unconfirmed but the pane is now BUSY -> the agent was woken (sidecar) and is
	// working; count as delivered, NOT a failure.
	if o, err := classifyNudgeResult("%7", unconfirmed, busy); err != nil || o.PaneID != "%7" {
		t.Fatalf("unconfirmed+busy should be delivered: got %+v, %v", o, err)
	}
	// Unconfirmed and still idle -> soft skip (durable task queued), no error.
	o, err := classifyNudgeResult("%7", unconfirmed, idle)
	if err != nil {
		t.Fatalf("unconfirmed+idle must not be a hard error: %v", err)
	}
	if o.PaneID != "" || !strings.Contains(o.Skipped, "unconfirmed") {
		t.Fatalf("unconfirmed+idle should be a soft skip, got %+v", o)
	}
	// A real failure (dead pane) propagates as an error.
	dead := &tmuxpane.DeadPaneError{PaneID: "%7"}
	if _, err := classifyNudgeResult("%7", dead, idle); err == nil {
		t.Fatal("a dead-pane error must propagate as a failure")
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

// withDispatchWakeSeam records the nudge call and returns a canned outcome.
func withDispatchWakeSeam(t *testing.T, outcome dispatchOutcome, err error) *[]string {
	t.Helper()
	var calls []string
	prev := dispatchWakePane
	dispatchWakePane = func(projectDir, profile, session string, explicitSession bool, role string, force bool) (dispatchOutcome, error) {
		calls = append(calls, role)
		return outcome, err
	}
	t.Cleanup(func() { dispatchWakePane = prev })
	return &calls
}

func TestRunDispatchSendsDurablyThenNudges(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "sent msg abc123\n")
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

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
	if !strings.Contains(stdout, "sent msg abc123") {
		t.Fatalf("amq send output should pass through: %q", stdout)
	}
	if len(*nudges) != 1 || (*nudges)[0] != "qa" {
		t.Fatalf("expected one nudge for qa, got %v", *nudges)
	}
	if !strings.Contains(stderr, "Nudged qa pane %7") {
		t.Fatalf("expected nudged notice, got:\n%s", stderr)
	}
}

func TestRunDispatchNoWakeSkipsNudge(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "ok\n")
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	_, stderr, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--no-wake"})
	})
	if err != nil {
		t.Fatalf("dispatch --no-wake: %v", err)
	}
	if len(*nudges) != 0 {
		t.Fatalf("--no-wake must not nudge, got %v", *nudges)
	}
	if !strings.Contains(stderr, "Skipped pane nudge") {
		t.Fatalf("expected no-wake notice, got:\n%s", stderr)
	}
}

func TestRunDispatchCreateTaskLinksMessage(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-abc to qa\n")
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task"})
	})
	if err != nil {
		t.Fatalf("dispatch --create-task: %v", err)
	}
	if !strings.Contains(stdout, "task t1") {
		t.Fatalf("dispatch output should include task id:\n%s", stdout)
	}
	show, _, err := captureOutput(t, func() error {
		return runTask([]string{"show", "t1", "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("task show: %v", err)
	}
	for _, want := range []string{"Assigned: qa", "Dispatch Assignee: qa", "Dispatch Message: msg-abc"} {
		if !strings.Contains(show, want) {
			t.Fatalf("task show missing %q:\n%s", want, show)
		}
	}
}

func TestRunDispatchCreateTaskAMQSendFailureLeavesTaskAuditTrail(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	var calls []amqCommandRequest
	prevEnv := resolveAMQEnvForAMQCommand
	prevRun := runAMQCommand
	resolveAMQEnvForAMQCommand = func(cwd, rootFlag, session, handle string) (amqEnv, error) {
		return amqEnv{Root: ".agent-mail/" + session, BaseRoot: ".agent-mail", SessionName: session, Me: handle}, nil
	}
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		calls = append(calls, req)
		return nil, errors.New("amq send failed")
	}
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = prevEnv
		runAMQCommand = prevRun
	})
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task"})
	})
	if err == nil || !strings.Contains(err.Error(), "dispatch send to qa") {
		t.Fatalf("dispatch should report AMQ send failure, got %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected one attempted AMQ send, got %d", len(calls))
	}
	if len(*nudges) != 0 {
		t.Fatalf("failed durable send must not nudge, got %v", *nudges)
	}
	show, _, showErr := captureOutput(t, func() error {
		return runTask([]string{"show", "t1", "--session", "issue-96"})
	})
	if showErr != nil {
		t.Fatalf("created task should remain inspectable after AMQ failure: %v", showErr)
	}
	if !strings.Contains(show, "ID: t1") || strings.Contains(show, "Dispatch Message:") {
		t.Fatalf("task should remain without dispatch metadata after AMQ failure:\n%s", show)
	}
}

func TestRunDispatchCreateTaskLinkFailureLeavesQueuedMessageAndTask(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-link to qa\n")
	prevLink := dispatchLinkTask
	dispatchLinkTask = func(projectDir, profile, session, id string, dispatch taskstore.Dispatch, now time.Time) (taskstore.Task, error) {
		return taskstore.Task{}, errors.New("link failed")
	}
	t.Cleanup(func() { dispatchLinkTask = prevLink })
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task"})
	})
	if err == nil || !strings.Contains(err.Error(), "link native task t1 to dispatch") {
		t.Fatalf("dispatch should report link failure, got %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected one successful AMQ send before link failure, got %d", len(*calls))
	}
	if len(*nudges) != 0 {
		t.Fatalf("link failure must not nudge because metadata linkage failed, got %v", *nudges)
	}
	show, _, showErr := captureOutput(t, func() error {
		return runTask([]string{"show", "t1", "--session", "issue-96"})
	})
	if showErr != nil {
		t.Fatalf("created task should remain inspectable after link failure: %v", showErr)
	}
	if !strings.Contains(show, "ID: t1") || strings.Contains(show, "Dispatch Message:") {
		t.Fatalf("task should remain without dispatch metadata after link failure:\n%s", show)
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
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

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
}

func TestRunDispatchWakeFailureStillSucceeds(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}"}, "ok\n")
	// The durable send succeeded; a wake error must NOT fail the dispatch.
	_ = withDispatchWakeSeam(t, dispatchOutcome{}, errTmuxAccessDenied())

	_, stderr, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y"})
	})
	if err != nil {
		t.Fatalf("wake failure must not fail dispatch, got %v", err)
	}
	if !strings.Contains(stderr, "pane nudge failed") {
		t.Fatalf("expected nudge-failed warning, got:\n%s", stderr)
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
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

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
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

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
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

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
