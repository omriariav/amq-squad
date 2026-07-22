package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestTaskDoneCommitsBeforeCanonicalNotificationAndSuccessorDispatch(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent msg-task-lifecycle to recipient\n")
	p, _ := taskstore.Add(dir, "s", taskstore.AddInput{Title: "build", AssignTo: "dev"}, taskNow())
	s, _ := taskstore.Add(dir, "s", taskstore.AddInput{Title: "review", Description: "inspect", AssignTo: "qa", DependsOn: []string{p.ID}}, taskNow())
	_, _ = taskstore.Claim(dir, "s", p.ID, "dev", taskNow())
	_, _ = taskstore.LinkDispatch(dir, "s", p.ID, taskstore.Dispatch{Sender: "cto", Assignee: "dev", Thread: "p2p/cto__dev", Kind: "todo", Subject: "build", MessageID: "original"}, taskNow())
	var legacyBinding *taskstore.SuccessorDispatchBinding
	oldDispatchNextHook := taskAfterDispatchNextGenerationRead
	taskAfterDispatchNextGenerationRead = func(_, _, _, successorID string, binding *taskstore.SuccessorDispatchBinding) error {
		if successorID == s.ID && binding != nil {
			copy := *binding
			legacyBinding = &copy
		}
		return nil
	}
	t.Cleanup(func() { taskAfterDispatchNextGenerationRead = oldDispatchNextHook })
	previousRun := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		committedP, err := taskstore.Show(dir, "s", p.ID)
		if err != nil {
			t.Fatal(err)
		}
		committedS, err := taskstore.Show(dir, "s", s.ID)
		if err != nil {
			t.Fatal(err)
		}
		if committedP.Status != taskstore.StatusCompleted || committedS.Status != taskstore.StatusInProgress {
			t.Fatalf("AMQ send ran before transaction commit: predecessor=%+v successor=%+v", committedP, committedS)
		}
		return previousRun(req)
	}
	t.Cleanup(func() { runAMQCommand = previousRun })

	stdout, _, err := captureOutput(t, func() error {
		return runTask([]string{"done", p.ID, "--me", "dev", "--evidence", "head abc", "--final-head", "abc", "--dispatch-next", s.ID, "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Kind != "task_done" || env.Data.Status != taskstore.StatusCompleted || env.Data.SuccessorTaskID != s.ID || len(env.Data.ReleasedTaskIDs) != 1 || len(env.Data.Outbox) != 2 {
		t.Fatalf("done envelope=%+v", env)
	}
	for _, intent := range env.Data.Outbox {
		if intent.State != taskstore.OutboxDelivered || intent.MessageID != "msg-task-lifecycle" {
			t.Fatalf("outbox=%+v", env.Data.Outbox)
		}
	}
	if len(*calls) != 2 {
		t.Fatalf("AMQ calls=%d, want completion + successor", len(*calls))
	}
	if legacyBinding == nil || legacyBinding.Assignee != "qa" || legacyBinding.GenerationRef != nil {
		t.Fatalf("legacy no-CURRENT dispatch-next binding=%+v", legacyBinding)
	}
	first, second := strings.Join((*calls)[0].Arg, " "), strings.Join((*calls)[1].Arg, " ")
	for _, want := range []string{"--kind status", "--subject DONE: build", "--to cto", "--thread p2p/cto__dev"} {
		if !strings.Contains(first, want) {
			t.Fatalf("completion send missing %q: %s", want, first)
		}
	}
	for _, want := range []string{"--kind todo", "--subject review", "--to qa"} {
		if !strings.Contains(second, want) {
			t.Fatalf("successor send missing %q: %s", want, second)
		}
	}
	persistedP, _ := taskstore.Show(dir, "s", p.ID)
	persistedS, _ := taskstore.Show(dir, "s", s.ID)
	if persistedP.Status != taskstore.StatusCompleted || persistedP.FinalHead != "abc" || persistedS.Status != taskstore.StatusInProgress || persistedS.Lease == nil {
		t.Fatalf("persisted predecessor=%+v successor=%+v", persistedP, persistedS)
	}
	showOut, _, err := captureOutput(t, func() error { return runTask([]string{"show", p.ID, "--session", "s", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	show := decodeJSONEnvelope[taskEnvelopeData](t, showOut)
	if show.Data.Task.FinalHead != "abc" || len(show.Data.Task.Outbox) != 1 || show.Data.Task.Outbox[0].State != taskstore.OutboxDelivered {
		t.Fatalf("show JSON final head/outbox=%+v", show.Data.Task)
	}

	// The successor's committed dispatch outbox is also its durable completion
	// route; it must not depend on a separate legacy dispatch-link write.
	runAMQCommand = previousRun
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"done", s.ID, "--me", "qa", "--session", "s", "--json"})
	}); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 3 {
		t.Fatalf("AMQ calls=%d, want successor completion", len(*calls))
	}
	third := strings.Join((*calls)[2].Arg, " ")
	for _, want := range []string{"--kind status", "--subject DONE: review", "--to dev"} {
		if !strings.Contains(third, want) {
			t.Fatalf("successor completion send missing %q: %s", want, third)
		}
	}
}

func TestTaskDoneNoNotifyIsExplicitAndVisible(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent should-not-send\n")
	p, _ := taskstore.Add(dir, "s", taskstore.AddInput{Title: "x", AssignTo: "dev"}, taskNow())
	_, _ = taskstore.Claim(dir, "s", p.ID, "dev", taskNow())
	_, _ = taskstore.LinkDispatch(dir, "s", p.ID, taskstore.Dispatch{Sender: "cto", Thread: "p2p/cto__dev"}, taskNow())
	if _, _, err := captureOutput(t, func() error { return runTask([]string{"done", p.ID, "--me", "dev", "--no-notify", "--session", "s"}) }); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 0 {
		t.Fatalf("suppressed completion sent AMQ: %+v", *calls)
	}
	got, _ := taskstore.Show(dir, "s", p.ID)
	if got.NotificationSuppression == nil || got.NotificationSuppression.Actor != "dev" || got.NotificationSuppression.Reason != "explicit --no-notify" {
		t.Fatalf("suppression=%+v", got.NotificationSuppression)
	}
	out, _, _ := captureOutput(t, func() error { return runTask([]string{"show", p.ID, "--session", "s"}) })
	if !strings.Contains(out, "Completion Notification: suppressed by dev") {
		t.Fatalf("suppression not visible:\n%s", out)
	}
}

func TestTaskDoneDeliveryFailureLeavesVisibleClaimAndRecovery(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	_ = withDispatchAMQCommandErrorSeam(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "", errors.New("AMQ unavailable"))
	p, _ := taskstore.Add(dir, "s", taskstore.AddInput{Title: "p", AssignTo: "dev"}, taskNow())
	s, _ := taskstore.Add(dir, "s", taskstore.AddInput{Title: "s", AssignTo: "qa", DependsOn: []string{p.ID}}, taskNow())
	_, _ = taskstore.Claim(dir, "s", p.ID, "dev", taskNow())
	stdout, stderr, err := captureOutput(t, func() error {
		return runTask([]string{"done", p.ID, "--me", "dev", "--dispatch-next", s.ID, "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatalf("committed transition must not look rolled back: %v", err)
	}
	env := decodeJSONEnvelope[mutationResult](t, stdout)
	if env.Data.Status != "completed_delivery_attention" || !strings.Contains(stderr, "delivery needs reconciliation") {
		t.Fatalf("env=%+v stderr=%s", env, stderr)
	}
	successor, _ := taskstore.Show(dir, "s", s.ID)
	if successor.Status != taskstore.StatusInProgress || len(successor.Outbox) != 1 || successor.Outbox[0].State != taskstore.OutboxUncertain || !strings.Contains(successor.Outbox[0].LastError, "AMQ unavailable") {
		t.Fatalf("successor delivery state=%+v", successor)
	}
	reconcileOut, _, err := captureOutput(t, func() error { return runTask([]string{"reconcile", "--session", "s", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	reconcile := decodeJSONEnvelope[taskReconcileEnvelopeData](t, reconcileOut)
	if !cliFinding(reconcile.Data.Result.Findings, "outbox_delivery_uncertain", s.ID) {
		t.Fatalf("findings=%+v", reconcile.Data.Result.Findings)
	}
}

func TestTaskPendingAndUncertainRecoveryCommandsMatchState(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent recovered-msg to cto\n")
	p, _ := taskstore.Add(dir, "s", taskstore.AddInput{Title: "p", AssignTo: "dev"}, taskNow())
	_, _ = taskstore.Claim(dir, "s", p.ID, "dev", taskNow())
	_, _ = taskstore.LinkDispatch(dir, "s", p.ID, taskstore.Dispatch{Sender: "cto", Thread: "p2p/cto__dev"}, taskNow())
	done, err := taskstore.DoneAtomicForProfile(dir, team.DefaultProfile, "s", p.ID, taskstore.DoneOptions{Actor: "dev", Notify: true, Now: taskNow()})
	if err != nil || len(done.Outbox) != 1 {
		t.Fatalf("done=%+v err=%v", done, err)
	}
	intent := done.Outbox[0]

	stdout, _, err := captureOutput(t, func() error { return runTask([]string{"reconcile", "--session", "s", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	reconcile := decodeJSONEnvelope[taskReconcileEnvelopeData](t, stdout)
	pending := findCLIFinding(reconcile.Data.Result.Findings, "outbox_pending", p.ID)
	if pending == nil || !strings.Contains(pending.Guidance, "amq-squad task deliver "+p.ID) || !strings.Contains(pending.Guidance, "--session s") {
		t.Fatalf("pending guidance=%+v", pending)
	}
	deliverOut, _, err := captureOutput(t, func() error {
		return runTask([]string{"deliver", p.ID, "--intent", intent.ID, "--me", "dev", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatalf("pending delivery command: %v", err)
	}
	if env := decodeJSONEnvelope[mutationResult](t, deliverOut); env.Kind != "task_deliver" || len(env.Data.Outbox) != 1 || env.Data.Outbox[0].State != taskstore.OutboxDelivered {
		t.Fatalf("deliver JSON=%+v", env)
	}
	if len(*calls) != 1 {
		t.Fatalf("pending delivery calls=%d", len(*calls))
	}

	pFailed, _ := taskstore.Add(dir, "s", taskstore.AddInput{Title: "failed", AssignTo: "dev"}, taskNow().Add(time.Second))
	_, _ = taskstore.Claim(dir, "s", pFailed.ID, "dev", taskNow().Add(time.Second))
	_, _ = taskstore.LinkDispatch(dir, "s", pFailed.ID, taskstore.Dispatch{Sender: "cto", Thread: "p2p/cto__dev"}, taskNow().Add(time.Second))
	failedDone, _ := taskstore.DoneAtomicForProfile(dir, team.DefaultProfile, "s", pFailed.ID, taskstore.DoneOptions{Actor: "dev", Notify: true, Now: taskNow().Add(time.Second)})
	failedIntent := failedDone.Outbox[0]
	_, _ = taskstore.BeginOutboxDeliveryForProfile(dir, team.DefaultProfile, "s", pFailed.ID, failedIntent.ID, taskNow().Add(2*time.Second))
	_, _ = taskstore.FinishOutboxDeliveryForProfile(dir, team.DefaultProfile, "s", pFailed.ID, failedIntent.ID, taskstore.DeliveryOutcome{State: taskstore.DeliveryFailedBeforeInvoke, Error: "offline"}, taskNow().Add(3*time.Second))
	failedReconcileOut, _, err := captureOutput(t, func() error { return runTask([]string{"reconcile", "--session", "s", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	failedReconcile := decodeJSONEnvelope[taskReconcileEnvelopeData](t, failedReconcileOut)
	failedFinding := findCLIFinding(failedReconcile.Data.Result.Findings, "outbox_failed", pFailed.ID)
	if failedFinding == nil || !strings.Contains(failedFinding.Guidance, "amq-squad task retry-delivery "+pFailed.ID) {
		t.Fatalf("failed guidance=%+v", failedFinding)
	}
	beforeFailedRetry := len(*calls)
	failedRetryOut, _, err := captureOutput(t, func() error {
		return runTask([]string{"retry-delivery", pFailed.ID, "--intent", failedIntent.ID, "--me", "dev", "--reason", "network restored", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatalf("failed retry command: %v", err)
	}
	if env := decodeJSONEnvelope[mutationResult](t, failedRetryOut); env.Kind != "task_retry-delivery" || len(env.Data.Outbox) != 1 || env.Data.Outbox[0].State != taskstore.OutboxDelivered {
		t.Fatalf("failed retry JSON=%+v", env)
	}
	if len(*calls) != beforeFailedRetry+1 {
		t.Fatalf("failed retry calls=%d want=%d", len(*calls), beforeFailedRetry+1)
	}

	p2, _ := taskstore.Add(dir, "s", taskstore.AddInput{Title: "p2", AssignTo: "dev"}, taskNow().Add(time.Second))
	_, _ = taskstore.Claim(dir, "s", p2.ID, "dev", taskNow().Add(time.Second))
	_, _ = taskstore.LinkDispatch(dir, "s", p2.ID, taskstore.Dispatch{Sender: "cto", Thread: "p2p/cto__dev"}, taskNow().Add(time.Second))
	done2, _ := taskstore.DoneAtomicForProfile(dir, team.DefaultProfile, "s", p2.ID, taskstore.DoneOptions{Actor: "dev", Notify: true, Now: taskNow().Add(time.Second)})
	uncertain := done2.Outbox[0]
	if _, err := taskstore.BeginOutboxDeliveryForProfile(dir, team.DefaultProfile, "s", p2.ID, uncertain.ID, taskNow().Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	beforeCalls := len(*calls)
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"retry-delivery", p2.ID, "--intent", uncertain.ID, "--me", "dev", "--reason", "retry", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "confirm-not-delivered") {
		t.Fatalf("uncertain retry err=%v", err)
	}
	if len(*calls) != beforeCalls {
		t.Fatal("uncertain delivery was resent without confirmation")
	}
	retryOut, _, err := captureOutput(t, func() error {
		return runTask([]string{"retry-delivery", p2.ID, "--intent", uncertain.ID, "--me", "dev", "--reason", "operator confirmed", "--confirm-not-delivered", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatalf("confirmed retry: %v", err)
	}
	if env := decodeJSONEnvelope[mutationResult](t, retryOut); env.Kind != "task_retry-delivery" || len(env.Data.Outbox) != 1 || env.Data.Outbox[0].State != taskstore.OutboxDelivered {
		t.Fatalf("uncertain retry JSON=%+v", env)
	}
	if len(*calls) != beforeCalls+1 {
		t.Fatalf("confirmed retry calls=%d want=%d", len(*calls), beforeCalls+1)
	}
}

func TestTaskReconcileMissingSessionIsReadOnlyAndHelpIsCanonical(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	stdout, _, err := captureOutput(t, func() error { return runTask([]string{"reconcile", "--session", "missing", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	env := decodeJSONEnvelope[taskReconcileEnvelopeData](t, stdout)
	if len(env.Data.Result.Findings) != 0 {
		t.Fatalf("missing findings=%+v", env.Data.Result.Findings)
	}
	if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "tasks", "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing reconcile created task dir: %v", err)
	}
	_, help, err := captureOutput(t, func() error { return runTask([]string{"--help"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"task claim", "--lease 2h", "--override-dependencies --reason WHY", "task renew", "task cancel",
		"task release", "task deliver", "task retry-delivery", "--confirm-not-delivered", "task reconcile",
		"--final-head SHA", "--dispatch-next ID", "DONE: <task title>", "there is no AMQ kind named done",
		"completed_pending_reconcile", "--evidence-id ID", "--binding-digest SHA", "--me H",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("help missing %q:\n%s", want, help)
		}
	}
}

func TestTaskClaimLeaseOverrideRenewReleaseCancelAndReviewJSON(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	withFixedTaskNow(t)
	if _, _, err := captureOutput(t, func() error { return runTask([]string{"add", "--title", "dep", "--session", "s"}) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "gated", "--depends-on", "t1", "--assign", "worker", "--session", "s"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t2", "--me", "worker", "--override-dependencies", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "reason") {
		t.Fatalf("override without reason err=%v", err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runTask([]string{"claim", "t2", "--me", "worker", "--override-dependencies", "--reason", "incident", "--lease", "30m", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	claimedEnv := decodeJSONEnvelope[mutationResult](t, stdout)
	if claimedEnv.Kind != "task_claim" || claimedEnv.Data.Status != taskstore.StatusInProgress {
		t.Fatalf("claim env=%+v", claimedEnv)
	}
	claimed, _ := taskstore.Show(dir, "s", "t2")
	if claimed.Lease == nil || claimed.Lease.ExpiresAt.Sub(claimed.Lease.IssuedAt) != 30*time.Minute || len(claimed.DependencyOverrides) != 1 || claimed.DependencyOverrides[0].Reason != "incident" {
		t.Fatalf("claimed=%+v", claimed)
	}

	stdout, _, err = captureOutput(t, func() error {
		return runTask([]string{"renew", "t2", "--me", "worker", "--lease", "1h", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if env := decodeJSONEnvelope[mutationResult](t, stdout); env.Kind != "task_renew" || env.Data.Status != taskstore.StatusInProgress {
		t.Fatalf("renew env=%+v", env)
	}
	renewed, _ := taskstore.Show(dir, "s", "t2")
	if renewed.Lease == nil || renewed.Lease.ExpiresAt.Sub(renewed.Lease.RenewedAt) != time.Hour {
		t.Fatalf("renewed=%+v", renewed)
	}

	stdout, _, err = captureOutput(t, func() error {
		return runTask([]string{"release", "t2", "--me", "lead", "--reason", "stale worker confirmed", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if env := decodeJSONEnvelope[mutationResult](t, stdout); env.Kind != "task_release" || env.Data.Status != taskstore.StatusPending {
		t.Fatalf("release env=%+v", env)
	}
	released, _ := taskstore.Show(dir, "s", "t2")
	if released.AssignedTo != "" || released.Lease != nil || len(released.Releases) != 1 {
		t.Fatalf("released=%+v", released)
	}

	if _, _, err := captureOutput(t, func() error { return runTask([]string{"add", "--title", "replacement", "--session", "s"}) }); err != nil {
		t.Fatal(err)
	}
	stdout, _, err = captureOutput(t, func() error {
		return runTask([]string{"cancel", "t2", "--me", "lead", "--reason", "superseded", "--replacement", "t3", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if env := decodeJSONEnvelope[mutationResult](t, stdout); env.Kind != "task_cancel" || env.Data.Status != taskstore.StatusCancelled {
		t.Fatalf("cancel env=%+v", env)
	}
	cancelled, _ := taskstore.Show(dir, "s", "t2")
	replacement, _ := taskstore.Show(dir, "s", "t3")
	if cancelled.ReplacedBy != "t3" || replacement.Replaces != "t2" {
		t.Fatalf("cancelled=%+v replacement=%+v", cancelled, replacement)
	}

	stdout, _, err = captureOutput(t, func() error {
		return runTask([]string{"add", "--title", "review", "--review-of", "t3", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if env := decodeJSONEnvelope[mutationResult](t, stdout); env.Kind != "task_add" || env.Data.ID != "t4" {
		t.Fatalf("review add env=%+v", env)
	}
	review, _ := taskstore.Show(dir, "s", "t4")
	replacement, _ = taskstore.Show(dir, "s", "t3")
	if review.ReviewOf != "t3" || len(replacement.ReviewTasks) != 1 || replacement.ReviewTasks[0] != "t4" {
		t.Fatalf("review=%+v replacement=%+v", review, replacement)
	}
}

func cliFinding(findings []taskstore.ReconcileFinding, kind, taskID string) bool {
	return findCLIFinding(findings, kind, taskID) != nil
}
func findCLIFinding(findings []taskstore.ReconcileFinding, kind, taskID string) *taskstore.ReconcileFinding {
	for i := range findings {
		if findings[i].Kind == kind && findings[i].TaskID == taskID {
			return &findings[i]
		}
	}
	return nil
}
