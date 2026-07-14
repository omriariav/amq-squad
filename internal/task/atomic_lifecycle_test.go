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

	result, err := DoneAtomicForProfile(dir, "default", "s", predecessor.ID, DoneOptions{
		Actor: "dev", Evidence: "head abc", FinalHead: "abc", DispatchNextID: successor.ID,
		Notify: true, LeaseDuration: time.Hour, Now: now.Add(time.Minute),
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

func hasFinding(findings []ReconcileFinding, kind, taskID string) bool {
	for _, f := range findings {
		if f.Kind == kind && f.TaskID == taskID {
			return true
		}
	}
	return false
}
