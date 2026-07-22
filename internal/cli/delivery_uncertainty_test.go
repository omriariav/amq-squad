package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestCommittedIndeterminateSendPreservesStableIDAndFinalPath(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".agent-mail", "s")
	finalPath := filepath.Join(root, "agents", "qa", "inbox", "new", "msg-045.md")
	output := "message msg-045 has a committed delivery; retrying may duplicate it: delivery to qa committed at " + finalPath + ", but durability is indeterminate: sync new dir: injected; do not retry blindly\n"
	receipt := newDeliveryReceipt(dir, team.DefaultProfile, "s", "qa", "qa", "project_lead", "dispatch")
	receipt.Recipients = []string{"qa"}
	receipt.Consumers = []deliveryConsumerState{{Consumer: "qa", State: deliveryStateAmbiguousUnknown}}
	receipt.Root = root
	receipt.AMQInvoked = true
	markDeliverySendResult(&receipt, []byte(output), errors.New("exit status 1: "+strings.TrimSpace(output)))

	if receipt.MessageID != "msg-045" || receipt.CommittedPath != finalPath || receipt.Status != deliveryStateCommittedIndeterminate || receipt.DeliveryState != deliveryStateCommittedIndeterminate {
		t.Fatalf("committed-indeterminate receipt = %+v", receipt)
	}
	if len(receipt.Consumers) != 1 || receipt.Consumers[0].State != deliveryStateCommittedIndeterminate || receipt.EvidenceSource != "amq_committed_delivery_error" {
		t.Fatalf("committed-indeterminate projection = %+v", receipt)
	}
	outcome := taskDeliveryOutcome(&receipt, errors.New("indeterminate durability"))
	if outcome.State != taskstore.DeliveryDelivered || outcome.MessageID != "msg-045" {
		t.Fatalf("committed delivery must never enter automatic retry state: %+v", outcome)
	}
}

func TestCommittedIndeterminateRequiresErrorAndExactReceiptBinding(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".agent-mail", "s")
	validPath := filepath.Join(root, "agents", "qa", "inbox", "new", "msg-045.md")
	line := func(id, path string) string {
		return "message " + id + " has a committed delivery; retrying may duplicate it: delivery to qa committed at " + path + ", but durability is indeterminate: sync new dir: injected; do not retry blindly\n"
	}
	baseReceipt := func() deliveryReceiptData {
		receipt := newDeliveryReceipt(dir, team.DefaultProfile, "s", "qa", "qa", "project_lead", "dispatch")
		receipt.Root = root
		receipt.Recipients = []string{"qa"}
		receipt.Consumers = []deliveryConsumerState{{Consumer: "qa", State: deliveryStateAmbiguousUnknown}}
		receipt.AMQInvoked = true
		return receipt
	}

	successLookalike := baseReceipt()
	markDeliverySendResult(&successLookalike, []byte(line("msg-045", validPath)), nil)
	if successLookalike.DeliveryState == deliveryStateCommittedIndeterminate || successLookalike.MessageID != "" {
		t.Fatalf("successful lookalike promoted: %+v", successLookalike)
	}

	tests := []struct {
		name   string
		mutate func(*deliveryReceiptData)
		id     string
		path   string
	}{
		{name: "sibling handle", path: filepath.Join(root, "agents", "other", "inbox", "new", "msg-045.md")},
		{name: "outside root", path: filepath.Join(dir, "outside", "msg-045.md")},
		{name: "id path mismatch", path: filepath.Join(root, "agents", "qa", "inbox", "new", "different.md")},
		{name: "traversal id", id: "..msg-045", path: filepath.Join(root, "agents", "qa", "inbox", "new", "..msg-045.md")},
		{name: "relative root", path: validPath, mutate: func(r *deliveryReceiptData) { r.Root = filepath.Join(".agent-mail", "s") }},
		{name: "target ambiguity", path: validPath, mutate: func(r *deliveryReceiptData) { r.Target.Handle = "other" }},
		{name: "duplicate consumer", path: validPath, mutate: func(r *deliveryReceiptData) { r.Consumers = append(r.Consumers, r.Consumers[0]) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			receipt := baseReceipt()
			if tt.mutate != nil {
				tt.mutate(&receipt)
			}
			id := tt.id
			if id == "" {
				id = "msg-045"
			}
			markDeliverySendResult(&receipt, []byte(line(id, tt.path)), errors.New("exit status 1"))
			if receipt.DeliveryState == deliveryStateCommittedIndeterminate || receipt.CommittedPath != "" {
				t.Fatalf("unbound evidence promoted: %+v", receipt)
			}
		})
	}
}

func TestCommittedDeliveryEvidenceRejectsIncompleteOrRelativeShape(t *testing.T) {
	for _, text := range []string{
		"message msg has a committed delivery; retrying may duplicate it",
		"message msg has a committed delivery; delivery to qa committed at relative/msg.md, but durability is indeterminate: sync failed",
		"delivery to qa committed at /tmp/msg.md, but durability is indeterminate: sync failed",
	} {
		if got, ok := parseCommittedDeliveryEvidence(text, nil); ok {
			t.Fatalf("unexpected committed evidence %+v from %q", got, text)
		}
	}
}

func TestLinkedCompletionInvokedWithoutIDRequiresConfirmedRetry(t *testing.T) {
	cases := []struct {
		name     string
		first    string
		firstErr error
	}{
		{name: "exit zero malformed", first: "ok without id\n"},
		{name: "nonzero without id", firstErr: errors.New("amq transport exited 7")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			withFixedTaskNow(t)
			calls := installSequencedAMQSend(t, tc.first, tc.firstErr)
			p, _ := taskstore.Add(dir, "s", taskstore.AddInput{Title: "build", AssignTo: "dev"}, taskNow())
			_, _ = taskstore.Claim(dir, "s", p.ID, "dev", taskNow())
			_, _ = taskstore.LinkDispatch(dir, "s", p.ID, taskstore.Dispatch{Sender: "cto", Assignee: "dev", Thread: "p2p/cto__dev"}, taskNow())

			if _, _, err := captureOutput(t, func() error {
				return runTask([]string{"done", p.ID, "--me", "dev", "--session", "s", "--json"})
			}); err != nil {
				t.Fatalf("committed completion: %v", err)
			}
			persisted, _ := taskstore.Show(dir, "s", p.ID)
			intent := persisted.Outbox[0]
			if intent.State != taskstore.OutboxUncertain || intent.MessageID != "" || intent.ReceiptAttemptID == "" || len(intent.ReceiptAttempts) != 1 {
				t.Fatalf("uncertain linked intent=%+v", intent)
			}
			receipt, err := readDeliveryReceipt(intent.ReceiptPath)
			if err != nil || !receipt.AMQInvoked || receipt.DeliveryState != deliveryStateAmbiguousUnknown {
				t.Fatalf("uncertain receipt=%+v err=%v", receipt, err)
			}
			if _, _, err := captureOutput(t, func() error {
				return runTask([]string{"retry-delivery", p.ID, "--intent", intent.ID, "--me", "dev", "--reason", "blind", "--session", "s"})
			}); err == nil || !strings.Contains(err.Error(), "confirm-not-delivered") {
				t.Fatalf("blind retry err=%v", err)
			}
			if *calls != 1 {
				t.Fatalf("blind retry invoked AMQ: calls=%d", *calls)
			}
			if _, _, err := captureOutput(t, func() error {
				return runTask([]string{"retry-delivery", p.ID, "--intent", intent.ID, "--me", "dev", "--reason", "operator verified mailbox", "--confirm-not-delivered", "--session", "s", "--json"})
			}); err != nil {
				t.Fatalf("confirmed retry: %v", err)
			}
			persisted, _ = taskstore.Show(dir, "s", p.ID)
			intent = persisted.Outbox[0]
			if intent.State != taskstore.OutboxDelivered || intent.MessageID != "retry-msg" || len(intent.ReceiptAttempts) != 2 || len(intent.RetryAudits) != 1 || !intent.RetryAudits[0].ConfirmedNotDelivered {
				t.Fatalf("confirmed retry audit/linkage=%+v", intent)
			}
		})
	}
}

func TestLinkedDispatchInvokedWithoutIDRequiresConfirmedRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		first    string
		firstErr error
	}{
		{name: "exit zero malformed", first: "ok without id\n"},
		{name: "nonzero without id", firstErr: errors.New("amq transport exited 7")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			writeDispatchTeam(t, dir)
			calls := installSequencedAMQSend(t, tc.first, tc.firstErr)
			_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)
			if _, _, err := captureOutput(t, func() error {
				return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task"})
			}); err == nil {
				t.Fatal("invoked/no-ID dispatch must report uncertainty")
			}
			persisted, _ := taskstore.Show(dir, "issue-96", "t1")
			intent := persisted.Outbox[0]
			if intent.State != taskstore.OutboxUncertain || intent.ReceiptAttemptID == "" {
				t.Fatalf("dispatch intent=%+v", intent)
			}
			if _, _, err := captureOutput(t, func() error {
				return runTask([]string{"retry-delivery", "t1", "--intent", intent.ID, "--me", "qa", "--reason", "blind", "--session", "issue-96"})
			}); err == nil || !strings.Contains(err.Error(), "confirm-not-delivered") {
				t.Fatalf("blind dispatch retry err=%v", err)
			}
			if *calls != 1 {
				t.Fatalf("blind dispatch retry invoked AMQ: %d", *calls)
			}
			if _, _, err := captureOutput(t, func() error {
				return runTask([]string{"retry-delivery", "t1", "--intent", intent.ID, "--me", "qa", "--reason", "operator verified mailbox", "--confirm-not-delivered", "--session", "issue-96", "--json"})
			}); err != nil {
				t.Fatalf("confirmed dispatch retry: %v", err)
			}
			persisted, _ = taskstore.Show(dir, "issue-96", "t1")
			intent = persisted.Outbox[0]
			if intent.State != taskstore.OutboxDelivered || len(intent.ReceiptAttempts) != 2 || len(intent.RetryAudits) != 1 || !intent.RetryAudits[0].ConfirmedNotDelivered {
				t.Fatalf("confirmed dispatch retry=%+v", intent)
			}
		})
	}
}

func TestLinkedStableIDTimeoutIsDeliveredAndResolverFailureIsRetryable(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	_ = withDispatchAMQCommandErrorSeam(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, `{"id":"timeout-msg","wait":{"event":"timeout"}}`+"\n", errors.New("timed out waiting for drained receipt"))
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)
	if _, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task", "--wait-for", "drained"})
	}); err != nil {
		t.Fatalf("stable-ID timeout is durable delivery: %v", err)
	}
	persisted, _ := taskstore.Show(dir, "issue-96", "t1")
	if persisted.Outbox[0].State != taskstore.OutboxDelivered || persisted.Outbox[0].MessageID != "timeout-msg" {
		t.Fatalf("stable-ID timeout intent=%+v", persisted.Outbox[0])
	}

	// A resolver error occurs before any AMQ invocation and remains the narrow,
	// freely retryable failure state.
	p, _ := taskstore.Add(dir, "resolver", taskstore.AddInput{Title: "notify", AssignTo: "qa"}, taskNow())
	_, _ = taskstore.Claim(dir, "resolver", p.ID, "qa", taskNow())
	_, _ = taskstore.LinkDispatch(dir, "resolver", p.ID, taskstore.Dispatch{Sender: "cto", Assignee: "qa", Thread: "p2p/cto__qa"}, taskNow())
	done, _ := taskstore.DoneAtomicForProfile(dir, team.DefaultProfile, "resolver", p.ID, taskstore.DoneOptions{Actor: "qa", Notify: true, Now: taskNow()})
	intent := done.Outbox[0]
	previousResolver := resolveAMQEnvForAMQCommand
	resolveAMQEnvForAMQCommand = func(string, string, string, string) (amqEnv, error) {
		return amqEnv{}, errors.New("resolver rejected root")
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"deliver", p.ID, "--intent", intent.ID, "--me", "qa", "--session", "resolver", "--json"})
	}); err != nil {
		t.Fatalf("committed resolver failure: %v", err)
	}
	resolveAMQEnvForAMQCommand = previousResolver
	persistedResolver, _ := taskstore.Show(dir, "resolver", p.ID)
	if persistedResolver.Outbox[0].State != taskstore.OutboxFailed {
		t.Fatalf("pre-invocation resolver state=%+v", persistedResolver.Outbox[0])
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"retry-delivery", p.ID, "--intent", intent.ID, "--me", "qa", "--reason", "resolver repaired", "--session", "resolver", "--json"})
	}); err != nil {
		t.Fatalf("pre-invocation failure should retry without confirmation: %v", err)
	}
}

func TestInvocationBoundaryPersistenceFailureIsPreInvokeForLinkedRoutes(t *testing.T) {
	for _, route := range []string{"dispatch", "task_outbox"} {
		t.Run(route, func(t *testing.T) {
			dir := t.TempDir()
			chdir(t, dir)
			writeDispatchTeam(t, dir)
			calls := installSequencedAMQSend(t, "Sent must-not-run to qa\n", nil)
			previousPersist := persistDeliveryReceipt
			persistDeliveryReceipt = func(projectDir, profile, session string, receipt *deliveryReceiptData) error {
				if receipt.AMQInvoked {
					return errors.New("injected invocation-boundary persistence failure")
				}
				return writeDeliveryReceipt(projectDir, profile, session, receipt)
			}
			t.Cleanup(func() { persistDeliveryReceipt = previousPersist })

			var taskID string
			if route == "dispatch" {
				_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)
				if _, _, err := captureOutput(t, func() error {
					return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task"})
				}); err == nil || !strings.Contains(err.Error(), "invocation-boundary") {
					t.Fatalf("dispatch boundary error=%v", err)
				}
				taskID = "t1"
			} else {
				p, _ := taskstore.Add(dir, "issue-96", taskstore.AddInput{Title: "notify", AssignTo: "qa"}, taskNow())
				_, _ = taskstore.Claim(dir, "issue-96", p.ID, "qa", taskNow())
				_, _ = taskstore.LinkDispatch(dir, "issue-96", p.ID, taskstore.Dispatch{Sender: "cto", Assignee: "qa", Thread: "p2p/cto__qa"}, taskNow())
				if _, _, err := captureOutput(t, func() error {
					return runTask([]string{"done", p.ID, "--me", "qa", "--session", "issue-96", "--json"})
				}); err != nil {
					t.Fatalf("committed task transition: %v", err)
				}
				taskID = p.ID
			}
			if *calls != 0 {
				t.Fatalf("AMQ invoked after boundary persistence failure: calls=%d", *calls)
			}
			persisted, _ := taskstore.Show(dir, "issue-96", taskID)
			intent := persisted.Outbox[0]
			if intent.State != taskstore.OutboxFailed || intent.ReceiptAttemptID == "" {
				t.Fatalf("pre-invoke intent=%+v", intent)
			}
			receipt, err := readDeliveryReceipt(intent.ReceiptPath)
			if err != nil || receipt.AMQInvoked || receipt.DeliveryState != deliveryStateFailed {
				t.Fatalf("pre-invoke receipt=%+v err=%v", receipt, err)
			}
			persistDeliveryReceipt = previousPersist
			if _, _, err := captureOutput(t, func() error {
				return runTask([]string{"retry-delivery", taskID, "--intent", intent.ID, "--me", "qa", "--reason", "storage repaired", "--session", "issue-96", "--json"})
			}); err != nil {
				t.Fatalf("safe pre-invoke retry: %v", err)
			}
		})
	}
}

func TestDispatchPostBeginReceiptLinkWriteFailureFinalizesPreInvoke(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeDispatchTeam(t, dir)
	calls := installSequencedAMQSend(t, "Sent must-not-run to qa\n", nil)
	previousPersist := persistDeliveryReceipt
	persistDeliveryReceipt = func(projectDir, profile, session string, receipt *deliveryReceiptData) error {
		if receipt.OutboxIntentID != "" && !receipt.AMQInvoked {
			return errors.New("injected post-begin receipt link failure")
		}
		return writeDeliveryReceipt(projectDir, profile, session, receipt)
	}
	t.Cleanup(func() { persistDeliveryReceipt = previousPersist })
	_ = withDispatchWakeSeam(t, dispatchOutcome{PaneID: "%7"}, nil)
	if _, _, err := captureOutput(t, func() error {
		return runDispatch([]string{"--session", "issue-96", "--role", "qa", "--subject", "Validate", "--body", "run", "--create-task"})
	}); err == nil || !strings.Contains(err.Error(), "post-begin") {
		t.Fatalf("post-begin link error=%v", err)
	}
	if *calls != 0 {
		t.Fatalf("post-begin receipt failure invoked AMQ: %d", *calls)
	}
	persisted, _ := taskstore.Show(dir, "issue-96", "t1")
	if persisted.Outbox[0].State != taskstore.OutboxFailed || persisted.Outbox[0].ReceiptAttemptID == "" {
		t.Fatalf("post-begin intent not finalized=%+v", persisted.Outbox[0])
	}
}

func installSequencedAMQSend(t *testing.T, first string, firstErr error) *int {
	t.Helper()
	previousResolver := resolveAMQEnvForAMQCommand
	previousRun := runAMQCommand
	resolveAMQEnvForAMQCommand = func(_ string, rootFlag, session, handle string) (amqEnv, error) {
		root := rootFlag
		if root == "" {
			root = filepath.Join(".agent-mail", session)
		}
		return amqEnv{Root: root, BaseRoot: ".agent-mail", SessionName: session, Me: handle}, nil
	}
	calls := 0
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte(first), firstErr
		}
		return []byte("Sent retry-msg to recipient\n"), nil
	}
	t.Cleanup(func() {
		resolveAMQEnvForAMQCommand = previousResolver
		runAMQCommand = previousRun
	})
	return &calls
}
