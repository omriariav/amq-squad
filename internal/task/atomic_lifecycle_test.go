package task

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDoneAtomicClosesReleasesClaimsAndQueuesBeforeDelivery(t *testing.T) {
	dir := t.TempDir()
	now := fixedNow
	predecessor, err := Add(dir, "s", AddInput{Title: "build"}, now)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := Add(dir, "s", AddInput{Title: "review", Description: "inspect head", DependsOn: []string{predecessor.ID}, AssignTo: "qa"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, "s", predecessor.ID, "dev", now); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkDispatch(dir, "s", predecessor.ID, Dispatch{Sender: "cto", Assignee: "dev", Thread: "p2p/cto__dev", Kind: "todo", Subject: "build", MessageID: "m1"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := DoneAtomicForProfile(dir, "default", "s", predecessor.ID, DoneOptions{
		Actor: "dev", DispatchNextID: successor.ID, Notify: true, Now: now.Add(time.Minute),
	}); err == nil || !strings.Contains(err.Error(), "requires an admitted successor binding") {
		t.Fatalf("dispatch-next without admitted binding err=%v", err)
	}
	unchangedPredecessor, _ := Show(dir, "s", predecessor.ID)
	unchangedSuccessor, _ := Show(dir, "s", successor.ID)
	if unchangedPredecessor.Status != StatusInProgress || unchangedSuccessor.Status != StatusPending || len(unchangedPredecessor.Outbox) != 0 || len(unchangedSuccessor.Outbox) != 0 {
		t.Fatalf("refused dispatch-next mutated atomic store: predecessor=%+v successor=%+v", unchangedPredecessor, unchangedSuccessor)
	}

	result, err := DoneAtomicForProfile(dir, "default", "s", predecessor.ID, DoneOptions{
		Actor: "dev", Evidence: "head abc", FinalHead: "abc", DispatchNextID: successor.ID,
		SuccessorDispatch: &SuccessorDispatchBinding{Assignee: "qa"},
		Notify:            true, LeaseDuration: time.Hour, Now: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Task.Status != StatusCompleted || result.Task.FinalHead != "abc" {
		t.Fatalf("completed = %+v", result.Task)
	}
	if len(result.ReleasedTaskIDs) != 1 || result.ReleasedTaskIDs[0] != successor.ID {
		t.Fatalf("released = %v", result.ReleasedTaskIDs)
	}
	if result.Successor == nil || result.Successor.Status != StatusInProgress || result.Successor.Lease == nil || result.Successor.Lease.Owner != "qa" {
		t.Fatalf("successor = %+v", result.Successor)
	}
	if len(result.Outbox) != 2 || result.Outbox[0].Type != "completion" || result.Outbox[0].Subject != "DONE: build" || result.Outbox[1].Type != "successor_dispatch" {
		t.Fatalf("outbox = %+v", result.Outbox)
	}
	tasks, err := List(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	if tasks[0].Status != StatusCompleted || tasks[1].Status != StatusInProgress || tasks[1].ReadyAt == nil {
		t.Fatalf("persisted tasks = %+v", tasks)
	}
	if _, err := os.Stat(filepath.Join(Dir(dir, "s"), transactionJournalName)); !os.IsNotExist(err) {
		t.Fatalf("journal left after success: %v", err)
	}
}

func TestDoneAtomicBindsCompletionGenerationAndPreservesUnresolvedGate(t *testing.T) {
	dir := t.TempDir()
	now := fixedNow
	tk, err := Add(dir, "s", AddInput{Title: "build"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, "s", tk.ID, "dev", now); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkDispatch(dir, "s", tk.ID, Dispatch{Sender: "cto", Assignee: "dev", Thread: "p2p/cto__dev", Kind: "todo", MessageID: "dispatch"}, now); err != nil {
		t.Fatal(err)
	}
	correlation := &CompletionGateCorrelation{
		TaskID: tk.ID, Profile: "default", Session: "s", NamespaceID: "default/s", NamespaceGeneration: "none",
		Thread: "gate/release", RequestMessageID: "request-1", RequestSHA256: strings.Repeat("a", 64),
		State: "open_preserved", Suppressed: false, Reason: "unresolved human decision preserved", ObservedAt: now,
	}
	result, err := DoneAtomicForProfile(dir, "default", "s", tk.ID, DoneOptions{
		Actor: "dev", Evidence: "head abc", CompletionGeneration: "completion-1", GateCorrelation: correlation,
		Notify: true, Now: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := result.Task.CompletionLifecycle
	if got == nil || got.Generation != "completion-1" || got.ReportIntentID == "" || got.Gate == nil || got.Gate.Suppressed || got.Gate.State != "open_preserved" || len(got.GateHistory) != 1 {
		t.Fatalf("completion lifecycle = %+v", got)
	}
	if len(result.Outbox) != 1 || result.Outbox[0].ID != got.ReportIntentID {
		t.Fatalf("report and lifecycle were not committed as one generation: outbox=%+v lifecycle=%+v", result.Outbox, got)
	}

	repeat := *correlation
	repeat.ObservedAt = now.Add(2 * time.Minute)
	repeat.Reason = "same exact request observed again"
	again, err := DoneAtomicForProfile(dir, "default", "s", tk.ID, DoneOptions{
		Actor: "dev", CompletionGeneration: "completion-1", GateCorrelation: &repeat, Notify: true, Now: now.Add(2 * time.Minute),
	})
	if err != nil || again.Task.CompletionLifecycle == nil || again.Task.CompletionLifecycle.Generation != "completion-1" || len(again.Outbox) != 0 {
		t.Fatalf("exact repeat must be idempotent: result=%+v err=%v", again, err)
	}
	if len(again.Task.CompletionLifecycle.GateHistory) != 1 {
		t.Fatalf("idempotent observation duplicated audit history: %+v", again.Task.CompletionLifecycle.GateHistory)
	}

	terminal := repeat
	terminal.State = "closed"
	terminal.Suppressed = true
	terminal.Reason = "exact request durably closed"
	terminal.ObservedAt = now.Add(3 * time.Minute)
	reconciled, err := DoneAtomicForProfile(dir, "default", "s", tk.ID, DoneOptions{
		Actor: "dev", CompletionGeneration: "completion-1", GateCorrelation: &terminal, Notify: true, Now: now.Add(3 * time.Minute),
	})
	if err != nil || reconciled.Task.CompletionLifecycle == nil || reconciled.Task.CompletionLifecycle.Gate == nil ||
		!reconciled.Task.CompletionLifecycle.Gate.Suppressed || reconciled.Task.CompletionLifecycle.Gate.State != "closed" || len(reconciled.Outbox) != 0 {
		t.Fatalf("same request terminal reconciliation failed: result=%+v err=%v", reconciled, err)
	}
	history := reconciled.Task.CompletionLifecycle.GateHistory
	if len(history) != 2 || history[0].State != "open_preserved" || history[0].Suppressed || history[1].State != "closed" || !history[1].Suppressed {
		t.Fatalf("gate audit history = %+v", history)
	}
	repeatedTerminal, err := DoneAtomicForProfile(dir, "default", "s", tk.ID, DoneOptions{
		Actor: "dev", CompletionGeneration: "completion-1", GateCorrelation: &terminal, Notify: true, Now: now.Add(4 * time.Minute),
	})
	if err != nil || len(repeatedTerminal.Task.CompletionLifecycle.GateHistory) != 2 || len(repeatedTerminal.Outbox) != 0 {
		t.Fatalf("terminal repeat must be idempotent: result=%+v err=%v", repeatedTerminal, err)
	}
	if _, err := DoneAtomicForProfile(dir, "default", "s", tk.ID, DoneOptions{
		Actor: "dev", CompletionGeneration: "completion-1", GateCorrelation: correlation, Notify: true, Now: now.Add(5 * time.Minute),
	}); err == nil || !strings.Contains(err.Error(), "cannot reopen terminal request") {
		t.Fatalf("terminal request was reopened: %v", err)
	}

	different := repeat
	different.RequestMessageID = "request-2"
	if _, err := DoneAtomicForProfile(dir, "default", "s", tk.ID, DoneOptions{
		Actor: "dev", CompletionGeneration: "completion-1", GateCorrelation: &different, Notify: true, Now: now.Add(6 * time.Minute),
	}); err == nil {
		t.Fatal("same completion generation accepted a different gate request identity")
	}
}

func TestDoneAtomicRejectsSuppressionOfUnresolvedGate(t *testing.T) {
	dir := t.TempDir()
	tk, _ := Add(dir, "s", AddInput{Title: "build"}, fixedNow)
	_, _ = Claim(dir, "s", tk.ID, "dev", fixedNow)
	correlation := &CompletionGateCorrelation{
		TaskID: tk.ID, Profile: "default", Session: "s", NamespaceID: "default/s", NamespaceGeneration: "none",
		Thread: "gate/release", RequestMessageID: "request-1", RequestSHA256: strings.Repeat("a", 64),
		State: "open_preserved", Suppressed: true, Reason: "invalid", ObservedAt: fixedNow,
	}
	if _, err := DoneAtomicForProfile(dir, "default", "s", tk.ID, DoneOptions{Actor: "dev", CompletionGeneration: "completion-1", GateCorrelation: correlation, Now: fixedNow.Add(time.Minute)}); err == nil || !strings.Contains(err.Error(), "must remain unsuppressed") {
		t.Fatalf("unresolved gate suppression must fail closed, got %v", err)
	}
	stored, _ := Show(dir, "s", tk.ID)
	if stored.Status != StatusInProgress || stored.CompletionLifecycle != nil {
		t.Fatalf("invalid correlation mutated task: %+v", stored)
	}
}

func TestDoneAtomicRejectsNoncanonicalGateCorrelationWithoutMutation(t *testing.T) {
	tests := []struct {
		name string
		edit func(*CompletionGateCorrelation)
	}{
		{name: "short digest", edit: func(c *CompletionGateCorrelation) { c.RequestSHA256 = "abc" }},
		{name: "uppercase digest", edit: func(c *CompletionGateCorrelation) { c.RequestSHA256 = strings.Repeat("A", 64) }},
		{name: "nonhex digest", edit: func(c *CompletionGateCorrelation) { c.RequestSHA256 = strings.Repeat("g", 64) }},
		{name: "multiline reason", edit: func(c *CompletionGateCorrelation) { c.Reason = "closed\nforged" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tk, _ := Add(dir, "s", AddInput{Title: "build"}, fixedNow)
			_, _ = Claim(dir, "s", tk.ID, "dev", fixedNow)
			correlation := CompletionGateCorrelation{
				TaskID: tk.ID, Profile: "default", Session: "s", NamespaceID: "default/s", NamespaceGeneration: "none",
				Thread: "gate/release", RequestMessageID: "request-1", RequestSHA256: strings.Repeat("a", 64),
				State: "closed", Suppressed: true, Reason: "exact request closed", ObservedAt: fixedNow,
			}
			tc.edit(&correlation)
			if _, err := DoneAtomicForProfile(dir, "default", "s", tk.ID, DoneOptions{
				Actor: "dev", CompletionGeneration: "completion-1", GateCorrelation: &correlation, Now: fixedNow.Add(time.Minute),
			}); err == nil {
				t.Fatal("noncanonical correlation accepted")
			}
			stored, err := Show(dir, "s", tk.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != StatusInProgress || stored.CompletionLifecycle != nil {
				t.Fatalf("rejected correlation mutated task: %+v", stored)
			}
		})
	}
}

func TestTransactionCrashBoundariesRecoverAtomically(t *testing.T) {
	phases := []string{
		transactionPhaseBeforeJournalRename,
		transactionPhaseAfterJournalCommit,
		transactionPhaseMidApply,
		transactionPhaseAfterAllApply,
		transactionPhaseBeforeJournalRemove,
		transactionPhaseAfterJournalRemove,
	}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			dir := t.TempDir()
			p, _ := Add(dir, "s", AddInput{Title: "p"}, fixedNow)
			_, _ = Add(dir, "s", AddInput{Title: "d", DependsOn: []string{p.ID}}, fixedNow)
			_, _ = Claim(dir, "s", p.ID, "w", fixedNow)
			transactionFault = func(got string, _ int) error {
				if got == phase {
					return errors.New("crash")
				}
				return nil
			}
			t.Cleanup(func() { transactionFault = nil })
			_, err := DoneAtomicForProfile(dir, "default", "s", p.ID, DoneOptions{Actor: "w", Now: fixedNow.Add(time.Minute)})
			if err == nil || !strings.Contains(err.Error(), phase) {
				t.Fatalf("phase %s err=%v", phase, err)
			}
			transactionFault = nil
			tasks, err := List(dir, "s")
			if err != nil {
				t.Fatal(err)
			}
			if phase == transactionPhaseBeforeJournalRename {
				if tasks[0].Status != StatusInProgress || tasks[1].ReadyAt != nil {
					t.Fatalf("pre-commit state changed: %+v", tasks)
				}
				if _, err := os.Stat(filepath.Join(Dir(dir, "s"), transactionJournalTmp)); err != nil {
					t.Fatalf("expected abandoned temp: %v", err)
				}
				return
			}
			if tasks[0].Status != StatusCompleted || tasks[1].ReadyAt == nil {
				t.Fatalf("committed state not recovered: %+v", tasks)
			}
			if _, err := os.Stat(filepath.Join(Dir(dir, "s"), transactionJournalName)); !os.IsNotExist(err) {
				t.Fatalf("journal not cleared: %v", err)
			}
		})
	}
}

func TestStructuredDispatchNextJournalReplayRestoresBoundSuccessorAtomically(t *testing.T) {
	dir := t.TempDir()
	now := fixedNow
	predecessor, err := Add(dir, "s", AddInput{Title: "predecessor", AssignTo: "dev"}, now)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := Add(dir, "s", AddInput{
		Title: "structured successor", Intent: IntentImplement, Artifact: "internal/cli/task.go",
		ExpectedBaseSHA: strings.Repeat("a", 40), Implementer: "qa", Reviewer: "cto",
		AssignTo: "qa", DependsOn: []string{predecessor.ID},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, "s", predecessor.ID, "dev", now); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkDispatch(dir, "s", predecessor.ID, Dispatch{Sender: "cto", Assignee: "dev", Thread: "p2p/cto__dev", MessageID: "dispatch-p"}, now); err != nil {
		t.Fatal(err)
	}
	ref := GenerationRef{
		Generation: strings.Repeat("1", 32), ManifestDigest: strings.Repeat("b", 64),
		GoalNamespace: "default/s", GoalDigest: "sha256:" + strings.Repeat("c", 64),
	}
	transactionFault = func(phase string, _ int) error {
		if phase == transactionPhaseMidApply {
			return errors.New("crash after predecessor image")
		}
		return nil
	}
	t.Cleanup(func() { transactionFault = nil })
	_, err = DoneAtomicForProfile(dir, "default", "s", predecessor.ID, DoneOptions{
		Actor: "dev", DispatchNextID: successor.ID, Notify: true, Now: now.Add(time.Minute),
		SuccessorDispatch: &SuccessorDispatchBinding{Assignee: "qa", Intent: IntentImplement, GenerationRef: &ref},
	})
	if err == nil || !strings.Contains(err.Error(), "committed but needs recovery") {
		t.Fatalf("mid-apply crash err=%v", err)
	}
	transactionFault = nil
	tasks, err := List(dir, "s")
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].Status != StatusCompleted || len(tasks[0].Outbox) != 1 || tasks[0].Outbox[0].Type != "completion" {
		t.Fatalf("replayed predecessor=%+v", tasks)
	}
	replayed := tasks[1]
	if replayed.Status != StatusInProgress || replayed.Lease == nil || replayed.LifecycleGenerationRef == nil || *replayed.LifecycleGenerationRef != ref ||
		replayed.LifecycleTaskGeneration == "" || len(replayed.Outbox) != 1 || replayed.Outbox[0].Type != "successor_dispatch" || replayed.Outbox[0].State != OutboxPending ||
		replayed.Dispatch == nil || replayed.Dispatch.OutboxIntentID != replayed.Outbox[0].ID || replayed.Dispatch.DeliveryState != OutboxPending ||
		replayed.Dispatch.MessageID != "" || replayed.Dispatch.Sender != "dev" || replayed.Dispatch.Assignee != "qa" || replayed.Dispatch.Thread != "p2p/dev__qa" {
		t.Fatalf("replayed bound successor=%+v", replayed)
	}
	if _, err := os.Stat(filepath.Join(Dir(dir, "s"), transactionJournalName)); !os.IsNotExist(err) {
		t.Fatalf("replayed journal not cleared: %v", err)
	}
}

func TestSuccessorDispatchCanonicalAuthorityTracksUncertainConfirmedRetryAndDelivery(t *testing.T) {
	dir := t.TempDir()
	now := fixedNow
	predecessor, _ := Add(dir, "s", AddInput{Title: "predecessor", AssignTo: "dev"}, now)
	successor, _ := Add(dir, "s", AddInput{
		Title: "successor", Intent: IntentImplement, Artifact: "internal/task/lifecycle.go", ExpectedBaseSHA: strings.Repeat("a", 40),
		Implementer: "qa", Reviewer: "cto", AssignTo: "qa", DependsOn: []string{predecessor.ID},
	}, now)
	_, _ = Claim(dir, "s", predecessor.ID, "dev", now)
	_, _ = LinkDispatch(dir, "s", predecessor.ID, Dispatch{Sender: "cto", Assignee: "dev", Thread: "p2p/cto__dev", MessageID: "dispatch-predecessor"}, now)
	ref := GenerationRef{Generation: strings.Repeat("1", 32), ManifestDigest: strings.Repeat("b", 64), GoalNamespace: "default/s", GoalDigest: "sha256:" + strings.Repeat("c", 64)}
	result, err := DoneAtomicForProfile(dir, "default", "s", predecessor.ID, DoneOptions{
		Actor: "dev", DispatchNextID: successor.ID, SuccessorDispatch: &SuccessorDispatchBinding{Assignee: "qa", Intent: IntentImplement, GenerationRef: &ref}, Notify: true, Now: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	current := *result.Successor
	if current.Dispatch == nil || current.Dispatch.OutboxIntentID == "" || current.Dispatch.DeliveryState != OutboxPending || current.Dispatch.MessageID != "" || current.Dispatch.Thread != "p2p/dev__qa" {
		t.Fatalf("pending canonical dispatch=%+v", current.Dispatch)
	}
	intentID := current.Dispatch.OutboxIntentID
	beforeLifecycle, _ := os.ReadFile(filepath.Join(Dir(dir, "s"), successor.ID+".json"))
	if _, err := RecordLifecycleEventForProfile(dir, "default", "s", successor.ID, LifecycleEventOptions{Event: LifecycleACK, Actor: "qa", GenerationRef: ref, Now: now.Add(2 * time.Minute)}); err == nil || !strings.Contains(err.Error(), "dispatch_message_id") {
		t.Fatalf("pre-delivery ACK err=%v", err)
	}
	afterLifecycle, _ := os.ReadFile(filepath.Join(Dir(dir, "s"), successor.ID+".json"))
	if string(beforeLifecycle) != string(afterLifecycle) {
		t.Fatal("pre-delivery lifecycle refusal changed task bytes")
	}
	if _, err := BeginOutboxDeliveryForProfile(dir, "default", "s", successor.ID, intentID, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachOutboxReceiptForProfile(dir, "default", "s", successor.ID, intentID, "attempt-1", "/receipts/attempt-1.json", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := FinishOutboxDeliveryForProfile(dir, "default", "s", successor.ID, intentID, DeliveryOutcome{State: DeliveryUncertain, Error: "no stable id"}, now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, _ = Show(dir, "s", successor.ID)
	if current.Dispatch == nil || current.Dispatch.DeliveryState != OutboxUncertain || current.Dispatch.MessageID != "" || current.Dispatch.LastError != "no stable id" || current.Dispatch.ReceiptAttemptID != "attempt-1" {
		t.Fatalf("uncertain canonical dispatch=%+v", current.Dispatch)
	}
	beforeBlind, _ := os.ReadFile(filepath.Join(Dir(dir, "s"), successor.ID+".json"))
	if _, err := PrepareOutboxRetryForProfile(dir, "default", "s", successor.ID, intentID, "qa", "blind", false, now.Add(6*time.Minute)); err == nil || !strings.Contains(err.Error(), "confirm-not-delivered") {
		t.Fatalf("blind retry err=%v", err)
	}
	afterBlind, _ := os.ReadFile(filepath.Join(Dir(dir, "s"), successor.ID+".json"))
	if string(beforeBlind) != string(afterBlind) {
		t.Fatal("blind retry changed task bytes")
	}
	if _, err := PrepareOutboxRetryForProfile(dir, "default", "s", successor.ID, intentID, "qa", "mailbox checked", true, now.Add(7*time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, _ = Show(dir, "s", successor.ID)
	if current.Dispatch == nil || current.Dispatch.DeliveryState != OutboxPending || current.Dispatch.MessageID != "" || current.Dispatch.LastError != "" || current.Dispatch.ReceiptAttemptID != "" || current.Dispatch.ReceiptPath != "" {
		t.Fatalf("confirmed retry placeholder=%+v", current.Dispatch)
	}
	if current.Outbox[0].ReceiptAttemptID != "attempt-1" {
		t.Fatalf("retry discarded historical receipt pointer: %+v", current.Outbox[0])
	}
	if _, err := BeginOutboxDeliveryForProfile(dir, "default", "s", successor.ID, intentID, now.Add(8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachOutboxReceiptForProfile(dir, "default", "s", successor.ID, intentID, "attempt-2", "/receipts/attempt-2.json", now.Add(9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := FinishOutboxDeliveryForProfile(dir, "default", "s", successor.ID, intentID, DeliveryOutcome{State: DeliveryDelivered, MessageID: "dispatch-new"}, now.Add(10*time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, _ = Show(dir, "s", successor.ID)
	if current.Dispatch == nil || current.Dispatch.DeliveryState != OutboxDelivered || current.Dispatch.MessageID != "dispatch-new" || current.Dispatch.ReceiptAttemptID != "attempt-2" || current.Dispatch.OutboxIntentID != intentID {
		t.Fatalf("delivered canonical dispatch=%+v", current.Dispatch)
	}
	event, err := RecordLifecycleEventForProfile(dir, "default", "s", successor.ID, LifecycleEventOptions{Event: LifecycleACK, Actor: "qa", GenerationRef: ref, Now: now.Add(11 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if event.Intent.Lifecycle == nil || event.Intent.Lifecycle.DispatchMessageID != "dispatch-new" {
		t.Fatalf("ACK did not bind canonical dispatch: %+v", event.Intent.Lifecycle)
	}
}

func TestResetReDispatchTombstoneAndPlaceholderRejectStaleSuccessorIntent(t *testing.T) {
	dir := t.TempDir()
	now := fixedNow
	predecessor, _ := Add(dir, "s", AddInput{Title: "predecessor", AssignTo: "dev"}, now)
	successor, _ := Add(dir, "s", AddInput{
		Title: "successor", Intent: IntentImplement, Artifact: "internal/task/lifecycle.go", ExpectedBaseSHA: strings.Repeat("a", 40),
		Implementer: "qa", Reviewer: "cto", AssignTo: "qa", DependsOn: []string{predecessor.ID},
	}, now)
	_, _ = Claim(dir, "s", predecessor.ID, "dev", now)
	_, _ = LinkDispatch(dir, "s", predecessor.ID, Dispatch{Sender: "cto", Assignee: "dev", Thread: "p2p/cto__dev", MessageID: "dispatch-predecessor"}, now)
	ref := GenerationRef{Generation: strings.Repeat("1", 32), ManifestDigest: strings.Repeat("b", 64), GoalNamespace: "default/s", GoalDigest: "sha256:" + strings.Repeat("c", 64)}
	first, err := DoneAtomicForProfile(dir, "default", "s", predecessor.ID, DoneOptions{
		Actor: "dev", DispatchNextID: successor.ID, SuccessorDispatch: &SuccessorDispatchBinding{Assignee: "qa", Intent: IntentImplement, GenerationRef: &ref}, Notify: true, Now: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	oldIntentID := first.Successor.Dispatch.OutboxIntentID
	_, _ = BeginOutboxDeliveryForProfile(dir, "default", "s", successor.ID, oldIntentID, now.Add(2*time.Minute))
	_, _ = FinishOutboxDeliveryForProfile(dir, "default", "s", successor.ID, oldIntentID, DeliveryOutcome{State: DeliveryFailedBeforeInvoke, Error: "offline"}, now.Add(3*time.Minute))
	if _, err := Reset(dir, "s", successor.ID, "qa", "retry successor", now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	resetSuccessor, _ := Show(dir, "s", successor.ID)
	if resetSuccessor.Dispatch == nil || resetSuccessor.Dispatch.DeliveryState != "reset" || resetSuccessor.Dispatch.MessageID != "" {
		t.Fatalf("reset tombstone=%+v", resetSuccessor.Dispatch)
	}
	if _, ok := CanonicalDispatch(resetSuccessor); !ok {
		t.Fatal("reset tombstone must suppress nil-Dispatch migration fallback")
	}
	if _, err := Reset(dir, "s", predecessor.ID, "dev", "retry predecessor", now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := Claim(dir, "s", predecessor.ID, "dev", now.Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := DoneAtomicForProfile(dir, "default", "s", predecessor.ID, DoneOptions{Actor: "dev", Notify: true, Now: now.Add(7 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	second, err := PrepareDispatchForProfile(dir, "default", "s", successor.ID, DispatchIntentOptions{
		From: "dev", Assignee: "qa", Thread: "p2p/dev__qa", Kind: "todo", Subject: "successor", GenerationRef: &ref, Now: now.Add(8 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	newIntentID := second.Task.Dispatch.OutboxIntentID
	if newIntentID == oldIntentID || second.Task.Dispatch.MessageID != "" || second.Task.Dispatch.DeliveryState != OutboxPending {
		t.Fatalf("new dispatch placeholder=%+v old=%s", second.Task.Dispatch, oldIntentID)
	}
	beforeStale, _ := os.ReadFile(filepath.Join(Dir(dir, "s"), successor.ID+".json"))
	if _, err := PrepareOutboxRetryForProfile(dir, "default", "s", successor.ID, oldIntentID, "qa", "stale retry", false, now.Add(9*time.Minute)); err == nil || !strings.Contains(err.Error(), "not current Task.Dispatch authority") {
		t.Fatalf("stale retry err=%v", err)
	}
	afterStale, _ := os.ReadFile(filepath.Join(Dir(dir, "s"), successor.ID+".json"))
	if string(beforeStale) != string(afterStale) {
		t.Fatal("stale retry altered current task bytes")
	}
	if _, err := RecordLifecycleEventForProfile(dir, "default", "s", successor.ID, LifecycleEventOptions{Event: LifecycleACK, Actor: "qa", GenerationRef: ref, Now: now.Add(10 * time.Minute)}); err == nil || !strings.Contains(err.Error(), "dispatch_message_id") {
		t.Fatalf("pre-delivery ACK err=%v", err)
	}
	_, _ = BeginOutboxDeliveryForProfile(dir, "default", "s", successor.ID, newIntentID, now.Add(11*time.Minute))
	_, _ = FinishOutboxDeliveryForProfile(dir, "default", "s", successor.ID, newIntentID, DeliveryOutcome{State: DeliveryDelivered, MessageID: "dispatch-replacement"}, now.Add(12*time.Minute))
	event, err := RecordLifecycleEventForProfile(dir, "default", "s", successor.ID, LifecycleEventOptions{Event: LifecycleACK, Actor: "qa", GenerationRef: ref, Now: now.Add(13 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if event.Intent.Lifecycle == nil || event.Intent.Lifecycle.DispatchMessageID != "dispatch-replacement" {
		t.Fatalf("replacement ACK=%+v", event.Intent.Lifecycle)
	}
}

func TestCanonicalDispatchMigratesOnlyDeliveredNilDispatchSuccessorRecords(t *testing.T) {
	task := Task{Outbox: []OutboxIntent{
		{ID: "old-task", Type: "task_dispatch", State: OutboxDelivered, From: "old", To: "worker", MessageID: "old-task-id"},
		{ID: "legacy-successor", Type: "successor_dispatch", State: OutboxDelivered, From: "cto", To: "worker", Thread: "p2p/cto__worker", MessageID: "legacy-id"},
	}}
	dispatch, ok := CanonicalDispatch(task)
	if !ok || dispatch.OutboxIntentID != "legacy-successor" || dispatch.MessageID != "legacy-id" {
		t.Fatalf("legacy migration dispatch=%+v ok=%v", dispatch, ok)
	}
	task.Dispatch = &Dispatch{DeliveryState: OutboxPending}
	dispatch, ok = CanonicalDispatch(task)
	if !ok || dispatch.MessageID != "" || dispatch.DeliveryState != OutboxPending {
		t.Fatalf("current placeholder did not suppress history: dispatch=%+v ok=%v", dispatch, ok)
	}
}

func TestConcurrentReaderCannotObservePartialAfterImages(t *testing.T) {
	dir := t.TempDir()
	p, _ := Add(dir, "s", AddInput{Title: "p"}, fixedNow)
	_, _ = Add(dir, "s", AddInput{Title: "d", DependsOn: []string{p.ID}}, fixedNow)
	_, _ = Claim(dir, "s", p.ID, "w", fixedNow)
	reached := make(chan struct{})
	release := make(chan struct{})
	transactionFault = func(phase string, _ int) error {
		if phase == transactionPhaseMidApply {
			close(reached)
			<-release
			return errors.New("crash mid apply")
		}
		return nil
	}
	t.Cleanup(func() { transactionFault = nil })
	writerDone := make(chan error, 1)
	go func() {
		_, err := DoneAtomicForProfile(dir, "default", "s", p.ID, DoneOptions{Actor: "w", Now: fixedNow.Add(time.Minute)})
		writerDone <- err
	}()
	<-reached
	readerDone := make(chan []Task, 1)
	readerErr := make(chan error, 1)
	go func() {
		tasks, err := List(dir, "s")
		if err != nil {
			readerErr <- err
			return
		}
		readerDone <- tasks
	}()
	select {
	case tasks := <-readerDone:
		t.Fatalf("reader bypassed transaction lock and saw %+v", tasks)
	case err := <-readerErr:
		t.Fatalf("reader failed early: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-writerDone; err == nil {
		t.Fatal("writer should report injected crash")
	}
	transactionFault = nil
	select {
	case err := <-readerErr:
		t.Fatal(err)
	case tasks := <-readerDone:
		if tasks[0].Status != StatusCompleted || tasks[1].ReadyAt == nil {
			t.Fatalf("reader observed partial state: %+v", tasks)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reader did not complete after writer released lock")
	}
}

func TestConcurrentClaimOnlyOneWins(t *testing.T) {
	dir := t.TempDir()
	task, _ := Add(dir, "s", AddInput{Title: "x"}, fixedNow)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, actor := range []string{"a", "b"} {
		wg.Add(1)
		go func(actor string) { defer wg.Done(); _, err := Claim(dir, "s", task.ID, actor, fixedNow); errs <- err }(actor)
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful claims=%d, want 1", successes)
	}
}

func TestLeaseOverrideAndReconcileNeverSilentlyUnclaim(t *testing.T) {
	dir := t.TempDir()
	dep, _ := Add(dir, "s", AddInput{Title: "dep"}, fixedNow)
	gated, _ := Add(dir, "s", AddInput{Title: "gated", DependsOn: []string{dep.ID}}, fixedNow)
	claimed, err := ClaimWithOptionsForProfile(dir, "default", "s", gated.ID, ClaimOptions{Actor: "w", LeaseDuration: time.Minute, OverrideReason: "incident recovery", Now: fixedNow})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Lease == nil || len(claimed.DependencyOverrides) != 1 || claimed.DependencyOverrides[0].Unmet[0].TaskID != dep.ID {
		t.Fatalf("audited claim = %+v", claimed)
	}
	result, err := ReconcileForProfile(dir, "default", "s", ReconcileOptions{Now: fixedNow.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(result.Findings, "stale_lease", gated.ID) {
		t.Fatalf("findings=%+v", result.Findings)
	}
	stillOwned, _ := Show(dir, "s", gated.ID)
	if stillOwned.Status != StatusInProgress || stillOwned.AssignedTo != "w" {
		t.Fatalf("stale reconcile unclaimed task: %+v", stillOwned)
	}
	if _, err := RenewLeaseForProfile(dir, "default", "s", gated.ID, "w", 3*time.Hour, fixedNow.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyTaskMigrationAndReadOnlyMissingReconcile(t *testing.T) {
	project := t.TempDir()
	missing := Dir(project, "missing")
	result, err := ReconcileForProfile(project, "default", "missing", ReconcileOptions{})
	if err != nil || len(result.Findings) != 0 {
		t.Fatalf("missing reconcile=%+v err=%v", result, err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("read-only reconcile created missing dir: %v", err)
	}
	if _, err := ReconcileForProfile(project, "default", "missing", ReconcileOptions{Apply: true}); err == nil || !strings.Contains(err.Error(), "non-zero timestamp") {
		t.Fatalf("zero-time apply err=%v", err)
	}

	dir := Dir(project, "legacy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{"id": "t1", "title": "legacy", "status": StatusInProgress, "assigned_to": "old-worker", "created_at": fixedNow, "updated_at": fixedNow}
	b, _ := json.Marshal(legacy)
	if err := os.WriteFile(filepath.Join(dir, "t1.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	legacyResult, err := ReconcileForProfile(project, "default", "legacy", ReconcileOptions{Now: fixedNow.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(legacyResult.Findings, "legacy_unleased", "t1") {
		t.Fatalf("legacy findings=%+v", legacyResult.Findings)
	}
	task, err := Show(project, "legacy", "t1")
	if err != nil || task.AssignedTo != "old-worker" || task.Status != StatusInProgress {
		t.Fatalf("legacy task=%+v err=%v", task, err)
	}
}

func TestLifecycleLinksAndFinalHead(t *testing.T) {
	dir := t.TempDir()
	original, _ := Add(dir, "s", AddInput{Title: "original"}, fixedNow)
	review, err := Add(dir, "s", AddInput{Title: "review", ReviewOf: original.ID}, fixedNow.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	originalAfterReview, _ := Show(dir, "s", original.ID)
	if review.ReviewOf != original.ID || len(originalAfterReview.ReviewTasks) != 1 || originalAfterReview.ReviewTasks[0] != review.ID {
		t.Fatalf("review links original=%+v review=%+v", originalAfterReview, review)
	}
	replacement, _ := Add(dir, "s", AddInput{Title: "replacement"}, fixedNow.Add(2*time.Second))
	cancelled, err := CancelForProfile(dir, "default", "s", original.ID, "lead", "superseded", replacement.ID, fixedNow.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	replacement, _ = Show(dir, "s", replacement.ID)
	if cancelled.Status != StatusCancelled || cancelled.ReplacedBy != replacement.ID || replacement.Replaces != original.ID {
		t.Fatalf("replacement links cancelled=%+v replacement=%+v", cancelled, replacement)
	}
	if _, err := CancelForProfile(dir, "default", "s", replacement.ID, "lead", "cycle", original.ID, fixedNow.Add(4*time.Second)); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle err=%v", err)
	}

	final, _ := Add(dir, "s", AddInput{Title: "final"}, fixedNow.Add(5*time.Second))
	_, _ = Claim(dir, "s", final.ID, "dev", fixedNow.Add(6*time.Second))
	done, err := DoneAtomicForProfile(dir, "default", "s", final.ID, DoneOptions{Actor: "dev", FinalHead: "deadbeef", Notify: true, Now: fixedNow.Add(7 * time.Second)})
	if err != nil || done.Task.FinalHead != "deadbeef" {
		t.Fatalf("final=%+v err=%v", done, err)
	}
}

func TestReconcileReportsReviewCyclesDeterministically(t *testing.T) {
	dir := t.TempDir()
	a, _ := Add(dir, "s", AddInput{Title: "a"}, fixedNow)
	b, _ := Add(dir, "s", AddInput{Title: "b"}, fixedNow.Add(time.Second))
	a.ReviewOf, b.ReviewOf = b.ID, a.ID
	for _, task := range []Task{a, b} {
		body, err := json.Marshal(task)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(Dir(dir, "s"), task.ID+".json"), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	first, err := ReconcileForProfile(dir, "default", "s", ReconcileOptions{Now: fixedNow.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ReconcileForProfile(dir, "default", "s", ReconcileOptions{Now: fixedNow.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first.Findings)
	secondJSON, _ := json.Marshal(second.Findings)
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("reconcile ordering changed:\n%s\n%s", firstJSON, secondJSON)
	}
	if !hasFinding(first.Findings, "review_cycle", a.ID) || !hasFinding(first.Findings, "review_cycle", b.ID) {
		t.Fatalf("review cycle findings=%+v", first.Findings)
	}
}

func TestReceiptReplacementRequiresAndConsumesOrderedRetryAudits(t *testing.T) {
	dir := t.TempDir()
	task, _ := Add(dir, "s", AddInput{Title: "dispatch", AssignTo: "qa"}, fixedNow)
	prepared, err := PrepareDispatchForProfile(dir, "default", "s", task.ID, DispatchIntentOptions{
		From: "cto", Assignee: "qa", Kind: "todo", Subject: "dispatch",
		ReceiptAttemptID: "attempt-1", ReceiptPath: "/receipts/attempt-1.json", Now: fixedNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := BeginOutboxDeliveryForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, fixedNow.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachOutboxReceiptForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, "", "", fixedNow.Add(2*time.Second)); err == nil {
		t.Fatal("empty receipt link accepted")
	}
	if _, err := AttachOutboxReceiptForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, "attempt-2", "/receipts/attempt-2.json", fixedNow.Add(2*time.Second)); err == nil || !strings.Contains(err.Error(), "pending audited retry") {
		t.Fatalf("unaudited replacement err=%v", err)
	}
	if _, _, err := FinishDispatchForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, Dispatch{}, DeliveryOutcome{State: DeliveryFailedBeforeInvoke, Error: "offline"}, fixedNow.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareOutboxRetryForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, "qa", "network repaired", false, fixedNow.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginOutboxDeliveryForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, fixedNow.Add(5*time.Second)); err != nil {
		t.Fatal(err)
	}
	second, err := AttachOutboxReceiptForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, "attempt-2", "/receipts/attempt-2.json", fixedNow.Add(6*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(second.ReceiptAttempts) != 2 || second.ReceiptAttempts[0].AttemptID != "attempt-1" || second.ReceiptAttempts[1].AttemptID != "attempt-2" || len(second.RetryAudits) != 1 || second.RetryAudits[0].PreviousState != OutboxFailed || second.RetryAudits[0].ConfirmedNotDelivered || second.RetryAudits[0].ReceiptAttemptID != "attempt-2" {
		t.Fatalf("first retry ordering=%+v", second)
	}
	if _, err := AttachOutboxReceiptForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, "attempt-unaudited", "/receipts/attempt-unaudited.json", fixedNow.Add(6*time.Second)); err == nil || !strings.Contains(err.Error(), "already links") {
		t.Fatalf("one retry audit authorized two replacements: %v", err)
	}
	if _, err := FinishOutboxDeliveryForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, DeliveryOutcome{State: DeliveryUncertain, Error: "no id"}, fixedNow.Add(7*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareOutboxRetryForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, "qa", "blind", false, fixedNow.Add(8*time.Second)); err == nil {
		t.Fatal("uncertain retry accepted without confirmation")
	}
	if _, err := PrepareOutboxRetryForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, "qa", "mailbox checked", true, fixedNow.Add(9*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginOutboxDeliveryForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, fixedNow.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := AttachOutboxReceiptForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, "attempt-1", "/receipts/attempt-1.json", fixedNow.Add(11*time.Second)); err == nil || !strings.Contains(err.Error(), "replay historical") {
		t.Fatalf("historical receipt replay err=%v", err)
	}
	afterReplay, err := ShowForProfile(dir, "default", "s", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterReplayIntent, err := taskOutboxIntent(&afterReplay, prepared.Intent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterReplayIntent.State != OutboxSending || afterReplayIntent.ReceiptAttemptID != "attempt-2" || len(afterReplayIntent.ReceiptAttempts) != 2 || len(afterReplayIntent.RetryAudits) != 2 || afterReplayIntent.RetryAudits[1].ReceiptAttemptID != "" || afterReplayIntent.RetryAudits[1].ReceiptPath != "" {
		t.Fatalf("historical replay mutated transaction or consumed retry audit: %+v", afterReplayIntent)
	}
	third, err := AttachOutboxReceiptForProfile(dir, "default", "s", task.ID, prepared.Intent.ID, "attempt-3", "/receipts/attempt-3.json", fixedNow.Add(11*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(third.ReceiptAttempts) != 3 || len(third.RetryAudits) != 2 || third.ReceiptAttempts[2].AttemptID != "attempt-3" || third.RetryAudits[1].PreviousState != OutboxUncertain || !third.RetryAudits[1].ConfirmedNotDelivered || third.RetryAudits[1].ReceiptAttemptID != "attempt-3" {
		t.Fatalf("repeated retry ordering=%+v", third)
	}
}

func hasFinding(findings []ReconcileFinding, kind, taskID string) bool {
	for _, f := range findings {
		if f.Kind == kind && f.TaskID == taskID {
			return true
		}
	}
	return false
}
