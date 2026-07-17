package task

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompletionEvidencePendingExactAndIdempotentStateMatrix(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)
	primary, err := AddForProfile(project, "default", "s", AddInput{Title: "build", AssignTo: "worker"}, now)
	if err != nil {
		t.Fatal(err)
	}
	dependent, err := AddForProfile(project, "default", "s", AddInput{Title: "review", DependsOn: []string{primary.ID}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ClaimForProfile(project, "default", "s", primary.ID, "worker", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := LinkDispatchForProfile(project, "default", "s", primary.ID, Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker"}, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	dir := DirForProfile(project, "default", "s")
	acceptedDigest, err := taskFileSHA256(dir, primary.ID)
	if err != nil {
		t.Fatal(err)
	}
	mismatch := testCompletionEvidence("msg-wrong", "binding-wrong", []string{"sender_assignee_mismatch"}, "lead")
	pending, err := ApplyCompletionEvidenceForProfile(project, "default", "s", primary.ID, CompletionEvidenceApply{
		ExpectedTaskSHA256: acceptedDigest, Evidence: mismatch, Actor: "lead", Now: now.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if pending.Task.Status != StatusCompletedPendingReconcile || pending.Task.AssignedTo != "worker" || pending.Task.Lease == nil || !pending.Changed {
		t.Fatalf("pending state lost ownership: %+v", pending)
	}
	if got, _ := ShowForProfile(project, "default", "s", dependent.ID); got.ReadyAt != nil {
		t.Fatalf("pending reconcile satisfied dependency: %+v", got)
	}
	// Repeating the accepted apply is idempotent even though its task digest is
	// now stale; it must not churn UpdatedAt or append attempts.
	repeat, err := ApplyCompletionEvidenceForProfile(project, "default", "s", primary.ID, CompletionEvidenceApply{
		ExpectedTaskSHA256: acceptedDigest, Evidence: mismatch, Actor: "lead", Now: now.Add(3 * time.Minute),
	})
	if err != nil || repeat.Changed || !repeat.Task.UpdatedAt.Equal(pending.Task.UpdatedAt) || len(repeat.Task.CompletionReconcile.Attempts) != 1 {
		t.Fatalf("pending repeat not idempotent: result=%+v err=%v", repeat, err)
	}
	if _, err := RenewLeaseForProfile(project, "default", "s", primary.ID, "worker", time.Hour, now.Add(4*time.Minute)); err != nil {
		t.Fatalf("pending assignee could not renew: %v", err)
	}
	for name, mutate := range map[string]func() error{
		"reset": func() error {
			_, err := ResetForProfile(project, "default", "s", primary.ID, "worker", "no", now)
			return err
		},
		"release": func() error {
			_, err := ReleaseForProfile(project, "default", "s", primary.ID, "worker", "no", now)
			return err
		},
		"cancel": func() error {
			_, err := CancelForProfile(project, "default", "s", primary.ID, "worker", "no", "", now)
			return err
		},
	} {
		if err := mutate(); err == nil {
			t.Fatalf("%s silently reopened/reassigned pending task", name)
		}
	}

	currentDigest, _ := taskFileSHA256(dir, primary.ID)
	exact := testCompletionEvidence("msg-exact", "binding-exact", nil, "lead")
	completed, err := ApplyCompletionEvidenceForProfile(project, "default", "s", primary.ID, CompletionEvidenceApply{
		ExpectedTaskSHA256: currentDigest, Evidence: exact, Exact: true, Actor: "lead", Now: now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if completed.Task.Status != StatusCompleted || completed.Task.Lease != nil || len(completed.ReleasedTaskIDs) != 1 || len(completed.Task.Outbox) != 0 {
		t.Fatalf("exact completion result=%+v", completed)
	}
	if got, _ := ShowForProfile(project, "default", "s", dependent.ID); got.ReadyAt == nil {
		t.Fatalf("exact reconciliation did not release dependent: %+v", got)
	}
	repeatExact, err := ApplyCompletionEvidenceForProfile(project, "default", "s", primary.ID, CompletionEvidenceApply{
		ExpectedTaskSHA256: currentDigest, Evidence: exact, Exact: true, Actor: "lead", Now: now.Add(6 * time.Minute),
	})
	if err != nil || repeatExact.Changed || !repeatExact.Task.UpdatedAt.Equal(completed.Task.UpdatedAt) {
		t.Fatalf("exact repeat not idempotent: result=%+v err=%v", repeatExact, err)
	}
}

func TestCompletionEvidenceLockedDigestCASRejectsConcurrentMutation(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)
	task, _ := AddForProfile(project, "default", "s", AddInput{Title: "build", AssignTo: "worker"}, now)
	_, _ = ClaimForProfile(project, "default", "s", task.ID, "worker", now)
	dir := DirForProfile(project, "default", "s")
	acceptedDigest, _ := taskFileSHA256(dir, task.ID)
	completionEvidenceMutationSeam = func(dir, id string) error {
		completionEvidenceMutationSeam = nil
		path := filepath.Join(dir, id+".json")
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var current Task
		if err := json.Unmarshal(b, &current); err != nil {
			return err
		}
		current.UpdatedAt = now.Add(time.Minute)
		b, _ = json.MarshalIndent(current, "", "  ")
		return os.WriteFile(path, append(b, '\n'), 0o644)
	}
	t.Cleanup(func() { completionEvidenceMutationSeam = nil })
	_, err := ApplyCompletionEvidenceForProfile(project, "default", "s", task.ID, CompletionEvidenceApply{
		ExpectedTaskSHA256: acceptedDigest, Evidence: testCompletionEvidence("msg", "binding", nil, "lead"), Exact: true, Actor: "lead", Now: now.Add(2 * time.Minute),
	})
	if err == nil || !strings.Contains(err.Error(), "changed before completion evidence commit") {
		t.Fatalf("locked CAS err=%v", err)
	}
	got, _ := ShowForProfile(project, "default", "s", task.ID)
	if got.Status != StatusInProgress || !got.UpdatedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("CAS overwrote concurrent mutation: %+v", got)
	}
}

func TestCompletionEvidencePackageRejectsUnsafeIDAndActorBeforeMutation(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)
	called := false
	completionEvidenceMutationSeam = func(_, _ string) error { called = true; return nil }
	t.Cleanup(func() { completionEvidenceMutationSeam = nil })
	evidence := testCompletionEvidence("msg", "binding", nil, "lead")
	for _, id := range []string{"", "../t1", "/tmp/t1", "t01", "t1/other"} {
		if _, err := ApplyCompletionEvidenceForProfile(project, "default", "s", id, CompletionEvidenceApply{
			ExpectedTaskSHA256: "digest", Evidence: evidence, Exact: true, Actor: "lead", Now: now,
		}); err == nil || !strings.Contains(err.Error(), "invalid task id") {
			t.Fatalf("unsafe id %q err=%v", id, err)
		}
	}
	if called {
		t.Fatal("unsafe id reached mutation seam")
	}
	if _, err := ApplyCompletionEvidenceForProfile(project, "default", "s", "t1", CompletionEvidenceApply{
		ExpectedTaskSHA256: "digest", Evidence: evidence, Exact: true, Actor: "other", Now: now,
	}); err == nil || !strings.Contains(err.Error(), "does not match durable evidence actor") {
		t.Fatalf("actor mismatch err=%v", err)
	}
	if called {
		t.Fatal("actor mismatch reached mutation seam")
	}
}

func TestPendingReconcileLeaseFindingsNeverSuggestForbiddenTransitions(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 15, 18, 0, 0, 0, time.UTC)
	task, _ := AddForProfile(project, "default", "s", AddInput{Title: "build", AssignTo: "worker"}, now)
	_, _ = ClaimForProfile(project, "default", "s", task.ID, "worker", now)
	dir := DirForProfile(project, "default", "s")
	digest, _ := taskFileSHA256(dir, task.ID)
	_, err := ApplyCompletionEvidenceForProfile(project, "default", "s", task.ID, CompletionEvidenceApply{
		ExpectedTaskSHA256: digest, Evidence: testCompletionEvidence("msg", "binding", []string{"sender_assignee_mismatch"}, "lead"), Actor: "lead", Now: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := ReconcileForProfile(project, "default", "s", ReconcileOptions{Now: now.Add(3 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	stale := completionLeaseFinding(result.Findings, "stale_lease", task.ID)
	if stale == nil || !strings.Contains(stale.Guidance, "task renew t1 --me worker") || containsForbiddenPendingGuidance(stale.Guidance) {
		t.Fatalf("stale pending guidance=%+v", stale)
	}
	if _, err := mutateForProfile(project, "default", "s", task.ID, func(t *Task, _ map[string]*Task) error {
		t.Lease = nil
		t.UpdatedAt = now.Add(4 * time.Hour)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	result, err = ReconcileForProfile(project, "default", "s", ReconcileOptions{Now: now.Add(5 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	legacy := completionLeaseFinding(result.Findings, "legacy_unleased", task.ID)
	if legacy == nil || !strings.Contains(legacy.Guidance, "task renew t1 --me worker") || containsForbiddenPendingGuidance(legacy.Guidance) {
		t.Fatalf("legacy pending guidance=%+v", legacy)
	}
}

func completionLeaseFinding(findings []ReconcileFinding, kind, taskID string) *ReconcileFinding {
	for i := range findings {
		if findings[i].Kind == kind && findings[i].TaskID == taskID {
			return &findings[i]
		}
	}
	return nil
}

func containsForbiddenPendingGuidance(guidance string) bool {
	for _, forbidden := range []string{"task release", "task reset", "task cancel", "task fail", "task block", "task done"} {
		if strings.Contains(guidance, forbidden) {
			return true
		}
	}
	return false
}

func testCompletionEvidence(id, binding string, blockers []string, actor string) CompletionEvidence {
	now := time.Date(2026, 7, 15, 21, 0, 0, 0, time.UTC)
	return CompletionEvidence{
		MessageID: id, FirstPath: "/mail/new/" + id, CurrentPath: "/mail/new/" + id,
		ContentSHA256: "content-" + id, BindingSHA256: binding, From: "worker", To: []string{"cto"}, Owner: "cto",
		CanonicalThread: "p2p/cto__worker", ExpectedAssignee: "worker", ExpectedAMQSender: "worker",
		ExpectedRecipient: "cto", ExpectedThread: "p2p/cto__worker", Blockers: blockers,
		ObservedAt: now, AppliedBy: actor, AppliedAt: now,
	}
}
