package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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
	if env.Data.DeliveryReceipt == nil ||
		env.Data.DeliveryReceipt.Kind != "dispatch" ||
		env.Data.DeliveryReceipt.Method != "durable_amq_plus_prompt_fallback" ||
		env.Data.DeliveryReceipt.MessageID != "msg-123" ||
		env.Data.DeliveryReceipt.Status != dispatchSubmitConfirmed ||
		env.Data.DeliveryReceipt.PaneID != "%7" ||
		!env.Data.DeliveryReceipt.Fallback {
		t.Fatalf("bad dispatch delivery receipt: %+v", env.Data.DeliveryReceipt)
	}
	if !receiptHasStage(env.Data.DeliveryReceipt, "queued_amq") ||
		!receiptHasStage(env.Data.DeliveryReceipt, "nudge_requested") ||
		!receiptHasStage(env.Data.DeliveryReceipt, "prompt_staged") ||
		!receiptHasStage(env.Data.DeliveryReceipt, "last_resort_pane_injection") ||
		!receiptHasStage(env.Data.DeliveryReceipt, "submit_attempted") ||
		!receiptHasStage(env.Data.DeliveryReceipt, dispatchSubmitConfirmed) {
		t.Fatalf("dispatch receipt stages = %+v, want explicit submit-confirmed state machine", env.Data.DeliveryReceipt.Stages)
	}
	if env.Data.DeliveryReceipt.Path == "" {
		t.Fatalf("dispatch receipt should be written to disk: %+v", env.Data.DeliveryReceipt)
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

func TestRunDispatchAnswerDefaultsToDrainedWait(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent msg-answer to qa (session: , root: /x/.agent-mail/issue-96); drained by qa\n")
	withDispatchWakeLiveSeam(t, true)

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
	r := decodeJSONEnvelope[mutationResult](t, stdout).Data.DeliveryReceipt
	if r == nil || !receiptHasStage(r, "amq_wait_drained") {
		t.Fatalf("answer receipt missing amq_wait_drained stage: %+v", r)
	}
}

func TestRunDispatchAnswerWaitCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-answer to qa\n")
	withDispatchWakeLiveSeam(t, true)

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
	withDispatchWakeLiveSeam(t, true)

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
	r := env.Data.DeliveryReceipt
	if r == nil || r.MessageID != "msg-timeout" || !receiptHasStage(r, "amq_wait_timeout") {
		t.Fatalf("timeout receipt = %+v, want queued receipt with amq_wait_timeout", r)
	}
	if receiptHasStage(r, "amq_wait_drained") || receiptHasStage(r, "failed") {
		t.Fatalf("timeout receipt must not claim drained or failed: %+v", r.Stages)
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
	withDispatchWakeLiveSeam(t, true)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--kind", "answer", "--subject", "A", "--body", "resolved"})
	})
	if err != nil {
		t.Fatalf("dispatch answer wait timeout should not fail: %v", err)
	}
	for _, want := range []string{"msg msg-timeout", "queued, drained receipt unconfirmed", "do NOT re-send", "nudge drain instead"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("timeout human output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunDispatchJSONEnvelopeReportsSubmitUnconfirmed(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent msg-456 to qa (session: , root: /x/.agent-mail/issue-96)\n")
	_ = withDispatchWakeSeam(t, dispatchOutcome{
		PaneID:      "%7",
		SubmitState: dispatchSubmitUnconfirmed,
		Detail:      "pane %7 was staged and Enter was attempted, but submission could not be confirmed after 3 attempts",
	}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --json: %v", err)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.Status != "queued_nudge_submit_unconfirmed" {
		t.Fatalf("dispatch status = %q, want queued_nudge_submit_unconfirmed", env.Data.Status)
	}
	r := env.Data.DeliveryReceipt
	if r == nil ||
		r.Status != dispatchSubmitUnconfirmed ||
		r.Method != "durable_amq_plus_prompt_fallback" ||
		r.PaneID != "%7" ||
		!r.Fallback ||
		!strings.Contains(r.Detail, "could not be confirmed") {
		t.Fatalf("dispatch receipt = %+v, want explicit submit_unconfirmed pane attempt", r)
	}
	if !receiptHasStage(r, "queued_amq") ||
		!receiptHasStage(r, "nudge_requested") ||
		!receiptHasStage(r, "prompt_staged") ||
		!receiptHasStage(r, "last_resort_pane_injection") ||
		!receiptHasStage(r, "submit_attempted") ||
		!receiptHasStage(r, dispatchSubmitUnconfirmed) {
		t.Fatalf("dispatch receipt stages = %+v, want explicit submit-unconfirmed state machine", r.Stages)
	}
}

func TestRunDispatchJSONEnvelopeReportsSubmitQueued(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"},
		"Sent msg-queued to qa (session: , root: /x/.agent-mail/issue-96)\n")
	_ = withDispatchWakeSeam(t, dispatchOutcome{
		PaneID:      "%7",
		SubmitState: dispatchSubmitQueued,
		Detail:      "pane %7 has the prompt queued in its input; it will submit when the agent goes idle",
	}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --json: %v", err)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.Status != "queued_nudge_submit_queued" {
		t.Fatalf("dispatch status = %q, want queued_nudge_submit_queued", env.Data.Status)
	}
	r := env.Data.DeliveryReceipt
	if r == nil || r.Status != dispatchSubmitQueued || !r.Fallback || !strings.Contains(r.Detail, "will submit when the agent goes idle") || !receiptHasStage(r, dispatchSubmitQueued) {
		t.Fatalf("dispatch receipt = %+v, want explicit submit_queued pane attempt", r)
	}
}

func TestRunDispatchReportsBracketedPastePreEnterFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		wakeErr    error
		wantStatus string
	}{
		{
			name:       "marker leak",
			wakeErr:    &tmuxpane.BracketedPasteLeakError{PaneID: "%7"},
			wantStatus: "bracketed_paste_leak",
		},
		{
			name: "inspection unavailable",
			wakeErr: &tmuxpane.BracketedPasteCheckUnavailableError{
				PaneID: "%7", Cause: errors.New("capture denied"), Detail: "post-paste pane capture failed",
			},
			wantStatus: "bracketed_paste_check_unavailable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			writeDispatchTeam(t, dir)
			_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-pre-enter to qa\n")
			_ = withDispatchWakeSeam(t, dispatchOutcome{}, tc.wakeErr)

			stdout, _, err := captureOutput(t, func() error {
				return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--json"})
			})
			if err != nil {
				t.Fatalf("dispatch must preserve durable success: %v", err)
			}
			env := decodeJSONEnvelope[mutationResult](t, stdout)
			if env.Data.Status != "queued_nudge_failed" || env.Data.MessageID != "msg-pre-enter" {
				t.Fatalf("dispatch result = %+v, want queued_nudge_failed with durable message", env.Data)
			}
			r := env.Data.DeliveryReceipt
			if r == nil || r.Status != tc.wantStatus || r.Method != "durable_amq_plus_prompt_fallback" || !r.Fallback || r.PaneID != "%7" || r.Acknowledged || r.MessageID != "msg-pre-enter" || r.DeliveryState != deliveryStateDeliveredNotDrained {
				t.Fatalf("dispatch receipt = %+v", r)
			}
			if !receiptHasStage(r, tc.wantStatus) || receiptHasStage(r, "submit_attempted") {
				t.Fatalf("dispatch receipt stages = %+v, want %s and no submit_attempted", r.Stages, tc.wantStatus)
			}
		})
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

func receiptHasStage(r *deliveryReceiptData, state string) bool {
	if r == nil {
		return false
	}
	for _, stage := range r.Stages {
		if stage.State == state {
			return true
		}
	}
	return false
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
	queued := &tmuxpane.QueuedInputError{PaneID: "%7"}

	// Clean send -> nudged.
	if o, err := classifyNudgeResult("%7", nil, idle); err != nil || o.PaneID != "%7" || o.SubmitState != dispatchSubmitConfirmed {
		t.Fatalf("clean send: got %+v, %v", o, err)
	}
	// Unconfirmed but the pane is now BUSY remains an explicit unconfirmed
	// receipt state; busy is useful detail, not proof of submit confirmation.
	if o, err := classifyNudgeResult("%7", unconfirmed, busy); err != nil || o.PaneID != "%7" || o.SubmitState != dispatchSubmitUnconfirmed || !strings.Contains(o.Detail, "busy") {
		t.Fatalf("unconfirmed+busy should be delivered: got %+v, %v", o, err)
	}
	// Unconfirmed and still idle -> explicit unconfirmed pane attempt, no error.
	o, err := classifyNudgeResult("%7", unconfirmed, idle)
	if err != nil {
		t.Fatalf("unconfirmed+idle must not be a hard error: %v", err)
	}
	if o.PaneID != "%7" || o.SubmitState != dispatchSubmitUnconfirmed || !strings.Contains(o.Detail, "manual Enter") {
		t.Fatalf("unconfirmed+idle should be an explicit unconfirmed attempt, got %+v", o)
	}
	// Codex's explicit queued-input footer is stronger evidence than an
	// unchanged input region and should never be described as a dropped Enter.
	if o, err := classifyNudgeResult("%7", queued, idle); err != nil || o.SubmitState != dispatchSubmitQueued || !strings.Contains(o.Detail, "will submit when the agent goes idle") {
		t.Fatalf("queued input should be a soft queued outcome: got %+v, %v", o, err)
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

func withDispatchWakeLiveSeam(t *testing.T, live bool) {
	t.Helper()
	prev := dispatchRecipientWakeLive
	dispatchRecipientWakeLive = func(projectDir, profile, session string, explicitSession bool, role string) bool {
		return live
	}
	t.Cleanup(func() { dispatchRecipientWakeLive = prev })
}

// TestRunDispatchWakeLiveSkipsPaneInjection proves the #289 default: a wake-live
// recipient is delivered via durable AMQ + wake with NO pane injection and no
// nudge_requested stage.
func TestRunDispatchWakeLiveSkipsPaneInjection(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-789 to qa\n")
	withDispatchWakeLiveSeam(t, true)
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --json: %v", err)
	}
	if len(*nudges) != 0 {
		t.Fatalf("wake-live recipient must NOT be pane-injected, got nudges %v", *nudges)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.Status != "queued_wake_delivered" {
		t.Fatalf("status = %q, want queued_wake_delivered", env.Data.Status)
	}
	r := env.Data.DeliveryReceipt
	if r == nil || r.Method != "durable_amq+wake" || r.Fallback || r.PaneID != "" {
		t.Fatalf("wake-live receipt = %+v, want durable_amq+wake no fallback", r)
	}
	if !receiptHasStage(r, "wake_delivered") {
		t.Fatalf("missing wake_delivered stage: %+v", r.Stages)
	}
	if receiptHasStage(r, "nudge_requested") || receiptHasStage(r, "last_resort_pane_injection") {
		t.Fatalf("wake-live must not have nudge/last-resort stages: %+v", r.Stages)
	}
}

// TestRunDispatchNotWakeLiveUsesLastResortPane proves a recipient that is not
// wake-live falls back to the explicit, clearly-marked last-resort pane nudge.
func TestRunDispatchNotWakeLiveUsesLastResortPane(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-790 to qa\n")
	withDispatchWakeLiveSeam(t, false)
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --json: %v", err)
	}
	if len(*nudges) != 1 {
		t.Fatalf("not-wake-live recipient should get one last-resort pane nudge, got %v", *nudges)
	}
	r := decodeJSONEnvelope[mutationResult](t, stdout).Data.DeliveryReceipt
	if r == nil || r.Method != "durable_amq_plus_prompt_fallback" || !r.Fallback {
		t.Fatalf("not-wake-live receipt = %+v, want last-resort pane injection", r)
	}
	if !receiptHasStage(r, "last_resort_pane_injection") {
		t.Fatalf("missing last_resort_pane_injection stage: %+v", r.Stages)
	}
}

func TestRunDispatchNoneModeSkipsLastResortPane(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	if err := launch.Write(filepath.Join(dir, ".agent-mail", "issue-96", "agents", "qa"), launch.Record{
		Handle: "qa", Session: "issue-96", WakeInjectMode: "none",
	}); err != nil {
		t.Fatal(err)
	}
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-zero to qa\n")
	withDispatchWakeLiveSeam(t, false)
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch none mode: %v", err)
	}
	if len(*nudges) != 0 {
		t.Fatalf("none-mode recipient must not receive pane input, got %v", *nudges)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.Status != "queued_zero_input" {
		t.Fatalf("status = %q, want queued_zero_input", env.Data.Status)
	}
	r := env.Data.DeliveryReceipt
	if r == nil || r.Method != "durable_amq_only" || !receiptHasStage(r, "wake_skipped_zero_input") || r.Fallback || r.PaneID != "" {
		t.Fatalf("zero-input receipt = %+v", r)
	}
}

func TestRunDispatchWakeLiveNoneModeReportsNoticeNotDrain(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	if err := launch.Write(filepath.Join(dir, ".agent-mail", "issue-96", "agents", "qa"), launch.Record{
		Handle: "qa", Session: "issue-96", WakeInjectMode: "none",
	}); err != nil {
		t.Fatal(err)
	}
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-zero-live to qa\n")
	withDispatchWakeLiveSeam(t, true)
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch live none mode: %v", err)
	}
	if len(*nudges) != 0 {
		t.Fatalf("wake-live none recipient must not receive pane input, got %v", *nudges)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	r := env.Data.DeliveryReceipt
	if env.Data.Status != "queued_zero_input" || r == nil || r.Method != "durable_amq+wake_notice" || !receiptHasStage(r, "wake_notice_zero_input") {
		t.Fatalf("wake-live none outcome = status %q receipt %+v", env.Data.Status, r)
	}
	if receiptHasStage(r, "wake_delivered") || strings.Contains(r.Detail, "drain") {
		t.Fatalf("wake-live none outcome must not claim auto-drain: %+v", r)
	}
}

func TestRunDispatchForceCannotOverrideNoneMode(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	if err := launch.Write(filepath.Join(dir, ".agent-mail", "issue-96", "agents", "qa"), launch.Record{
		Handle: "qa", Session: "issue-96", WakeInjectMode: "none",
	}); err != nil {
		t.Fatal(err)
	}
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-zero-force to qa\n")
	withDispatchWakeLiveSeam(t, true)
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--create-task", "--force", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "refusing --force") || !strings.Contains(err.Error(), "re-run without --force") {
		t.Fatalf("force none-mode error = %v", err)
	}
	if stdout != "" {
		t.Fatalf("force refusal must not emit a JSON success envelope: %q", stdout)
	}
	if len(*calls) != 0 {
		t.Fatalf("force refusal must not send AMQ, got calls %+v", *calls)
	}
	if len(*nudges) != 0 {
		t.Fatalf("--force must not override none mode, got nudges %v", *nudges)
	}
	if _, statErr := os.Stat(deliveryReceiptDir(dir, team.DefaultProfile, "issue-96")); !os.IsNotExist(statErr) {
		t.Fatalf("force refusal wrote a receipt directory: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, team.DirName, "tasks", "issue-96")); !os.IsNotExist(statErr) {
		t.Fatalf("force refusal created a native task: %v", statErr)
	}
}

// TestRunDispatchForceOverridesWakeFirst proves --force is an explicit pane
// override even when the recipient is wake-live.
func TestRunDispatchForceOverridesWakeFirst(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-791 to qa\n")
	withDispatchWakeLiveSeam(t, true)
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--force", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --force --json: %v", err)
	}
	if len(*nudges) != 1 {
		t.Fatalf("--force must override wake-first and pane-nudge, got %v", *nudges)
	}
	r := decodeJSONEnvelope[mutationResult](t, stdout).Data.DeliveryReceipt
	if r == nil || r.Method != "durable_amq_plus_prompt_fallback" || !receiptHasStage(r, "forced_pane_injection") {
		t.Fatalf("--force receipt = %+v, want legacy method + additive forced_pane_injection stage", r)
	}
}

// TestRunDispatchNoWakeSkipsEverything proves --no-wake queues only, with a
// wake_skipped stage and no wake-first / pane injection.
func TestRunDispatchNoWakeSkipsEverything(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-792 to qa\n")
	withDispatchWakeLiveSeam(t, true)
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "X", "--body", "y", "--no-wake", "--json"})
	})
	if err != nil {
		t.Fatalf("dispatch --no-wake --json: %v", err)
	}
	if len(*nudges) != 0 {
		t.Fatalf("--no-wake must not pane-inject, got %v", *nudges)
	}
	r := decodeJSONEnvelope[mutationResult](t, stdout).Data.DeliveryReceipt
	if r == nil || r.Method != "durable_amq_only" || !receiptHasStage(r, "wake_skipped") {
		t.Fatalf("--no-wake receipt = %+v, want durable_amq_only + wake_skipped", r)
	}
}

func TestRunDispatchSendsDurablyThenNudges(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent abc123 to qa\n")
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
	if !strings.Contains(stdout, "msg abc123") {
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
	previousRun := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		committed, err := taskstore.Show(dir, "issue-96", "t1")
		if err != nil {
			t.Fatalf("task must exist before AMQ send: %v", err)
		}
		if committed.Status != taskstore.StatusInProgress || committed.Lease == nil || len(committed.Outbox) != 1 || committed.Outbox[0].State != taskstore.OutboxSending {
			t.Fatalf("AMQ send ran before task-backed dispatch commit: %+v", committed)
		}
		return previousRun(req)
	}
	t.Cleanup(func() { runAMQCommand = previousRun })

	stdout, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task"})
	})
	if err != nil {
		t.Fatalf("dispatch --create-task: %v", err)
	}
	if !strings.Contains(stdout, "task t1") {
		t.Fatalf("dispatch output should include task id:\n%s", stdout)
	}
	persisted, err := taskstore.Show(dir, "issue-96", "t1")
	if err != nil || len(persisted.Outbox) != 1 || persisted.Outbox[0].ReceiptAttemptID == "" || persisted.Outbox[0].ReceiptPath == "" || persisted.Dispatch == nil || persisted.Dispatch.ReceiptAttemptID != persisted.Outbox[0].ReceiptAttemptID {
		t.Fatalf("task/outbox must link the canonical receipt projection: task=%+v err=%v", persisted, err)
	}
	linkedReceipt, err := readDeliveryReceipt(persisted.Outbox[0].ReceiptPath)
	if err != nil || linkedReceipt.MessageID != "msg-abc" || linkedReceipt.TaskID != "t1" || linkedReceipt.OutboxIntentID != persisted.Outbox[0].ID {
		t.Fatalf("linked receipt=%+v err=%v", linkedReceipt, err)
	}
	show, _, err := captureOutput(t, func() error {
		return runTask([]string{"show", "t1", "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("task show: %v", err)
	}
	for _, want := range []string{"Status: in_progress", "Assigned: qa", "Dispatch Assignee: qa", "Dispatch Message: msg-abc"} {
		if !strings.Contains(show, want) {
			t.Fatalf("task show missing %q:\n%s", want, show)
		}
	}
}

func TestRunDispatchCreateTaskAMQSendFailureLeavesUncertainTaskAuditTrail(t *testing.T) {
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
	for _, want := range []string{"ID: t1", "Status: in_progress", "Outbox:", taskstore.OutboxUncertain, "error=amq send failed", "Dispatch Assignee: qa"} {
		if !strings.Contains(show, want) {
			t.Fatalf("failed task-backed dispatch missing %q:\n%s", want, show)
		}
	}
}

func TestRunDispatchCreateTaskFinalizeFailureLeavesUncertainClaimedTask(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-link to qa\n")
	prevFinish := dispatchFinishTask
	dispatchFinishTask = func(projectDir, profile, session, taskID, intentID string, dispatch taskstore.Dispatch, outcome taskstore.DeliveryOutcome, now time.Time) (taskstore.Task, taskstore.OutboxIntent, error) {
		return taskstore.Task{}, taskstore.OutboxIntent{}, errors.New("finish failed")
	}
	t.Cleanup(func() { dispatchFinishTask = prevFinish })
	nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

	_, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task"})
	})
	if err == nil || !strings.Contains(err.Error(), "delivery may be uncertain") {
		t.Fatalf("dispatch should report uncertain finalize failure, got %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected one successful AMQ send after committed intent, got %d", len(*calls))
	}
	if len(*nudges) != 0 {
		t.Fatalf("finalize failure must not nudge because outcome is uncertain, got %v", *nudges)
	}
	show, _, showErr := captureOutput(t, func() error {
		return runTask([]string{"show", "t1", "--session", "issue-96"})
	})
	if showErr != nil {
		t.Fatalf("created task should remain inspectable after finalize failure: %v", showErr)
	}
	for _, want := range []string{"ID: t1", "Status: in_progress", "Outbox:", "sending"} {
		if !strings.Contains(show, want) {
			t.Fatalf("uncertain task missing %q:\n%s", want, show)
		}
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
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

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
			nudges := withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)

			_, _, err := captureOutput(t, func() error {
				return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--task", taskID})
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("dispatch error = %v, want %q", err, tc.want)
			}
			if len(*calls) != 0 {
				t.Fatalf("blocked/terminal task must not send AMQ, got %d calls", len(*calls))
			}
			if len(*nudges) != 0 {
				t.Fatalf("blocked/terminal task must not nudge, got %v", *nudges)
			}
		})
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
	show, _, err := captureOutput(t, func() error {
		return runTask([]string{"show", "t1", "--session", "issue-96"})
	})
	if err != nil {
		t.Fatalf("task show: %v", err)
	}
	if !strings.Contains(show, "Status: in_progress") || !strings.Contains(show, "Dispatch Message: msg-json") {
		t.Fatalf("dispatch --task should auto-claim and link task:\n%s", show)
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
