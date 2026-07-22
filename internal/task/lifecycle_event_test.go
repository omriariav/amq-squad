package task

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStructuredDoneBindsImmutableCommandEvidenceAndGeneration(t *testing.T) {
	previousVerify := verifyCommandEvidenceLinkRecord
	verifyCommandEvidenceLinkRecord = func(string, string, string, string, CommandEvidenceLink) error { return nil }
	t.Cleanup(func() { verifyCommandEvidenceLinkRecord = previousVerify })
	project := t.TempDir()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	created, err := AddForProfile(project, "review", "s", AddInput{Title: "structured", AssignTo: "worker"}, now)
	if err != nil {
		t.Fatal(err)
	}
	ref := GenerationRef{Generation: "run-1", ManifestDigest: "manifest-1", GoalNamespace: "review/s", GoalDigest: "goal-1"}
	prepared, err := PrepareDispatchForProfile(project, "review", "s", created.ID, DispatchIntentOptions{From: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo", GenerationRef: &ref, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = BeginOutboxDeliveryForProfile(project, "review", "s", created.ID, prepared.Intent.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err = FinishDispatchForProfile(project, "review", "s", created.ID, prepared.Intent.ID, Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker"}, DeliveryOutcome{State: DeliveryDelivered, MessageID: "dispatch-1"}, now); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(project, ".amq-squad", "evidence", "commands", "review", "s", "tasks", created.ID, "attempts", "attempt-1")
	sha := "sha256:" + strings.Repeat("a", 64)
	link := CommandEvidenceLink{AttemptID: "attempt-1", Actor: "worker", Subject: "tests", ProcessState: "succeeded", FinalizationState: "complete",
		ManifestPath: filepath.Join(base, "manifest.json"), ManifestSHA256: sha, OutcomePath: filepath.Join(base, "outcome.json"), OutcomeSHA256: sha, SummaryPath: filepath.Join(base, "summary.json"), SummarySHA256: sha}
	linked, _, err := LinkCommandEvidenceForProfile(project, "review", "s", created.ID, taskFileDigest(t, project, "review", "s", created.ID), "worker", link, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := LifecycleCommandEvidenceRef(linked.CommandEvidence[0])
	if err != nil {
		t.Fatal(err)
	}
	wrong := evidence
	wrong.SHA256 = strings.Repeat("0", 64)
	if _, err := DoneAtomicForProfile(project, "review", "s", created.ID, DoneOptions{Actor: "worker", Notify: true, GenerationRef: &ref, EvidenceRef: &wrong, Now: now.Add(2 * time.Second)}); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("wrong evidence digest accepted: %v", err)
	}
	result, err := DoneAtomicForProfile(project, "review", "s", created.ID, DoneOptions{Actor: "worker", Notify: true, GenerationRef: &ref, EvidenceRef: &evidence, Now: now.Add(3 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Outbox) != 1 || result.Outbox[0].Lifecycle == nil || len(result.Task.LifecycleEvents) != 1 {
		t.Fatalf("structured completion not committed atomically: %+v", result)
	}
	envelope := *result.Outbox[0].Lifecycle
	if envelope.GenerationRef != ref || envelope.EvidenceRef == nil || *envelope.EvidenceRef != evidence || envelope.DispatchMessageID != "dispatch-1" || envelope.TaskGeneration == "" {
		t.Fatalf("wrong completion envelope: %+v", envelope)
	}
}

func TestLifecycleJournalUsesCommittedAppendOrderNotOccurredAt(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	created, _ := AddForProfile(project, "review", "s", AddInput{Title: "ordered", AssignTo: "worker"}, now)
	ref := GenerationRef{Generation: "run-1", ManifestDigest: "manifest-1", GoalNamespace: "review/s", GoalDigest: "goal-1"}
	prepared, _ := PrepareDispatchForProfile(project, "review", "s", created.ID, DispatchIntentOptions{From: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo", GenerationRef: &ref, Now: now})
	_, _ = BeginOutboxDeliveryForProfile(project, "review", "s", created.ID, prepared.Intent.ID, now)
	_, _, _ = FinishDispatchForProfile(project, "review", "s", created.ID, prepared.Intent.ID, Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker"}, DeliveryOutcome{State: DeliveryDelivered, MessageID: "dispatch-1"}, now)
	first, err := RecordLifecycleEventForProfile(project, "review", "s", created.ID, LifecycleEventOptions{Event: LifecycleACK, Actor: "worker", GenerationRef: ref, Now: now.Add(2 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := RecordLifecycleEventForProfile(project, "review", "s", created.ID, LifecycleEventOptions{Event: LifecycleProgress, Actor: "worker", GenerationRef: ref, Now: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Task.LifecycleEvents) != 2 || second.Task.LifecycleEvents[0].Envelope.EventID != first.Intent.Lifecycle.EventID || second.Task.LifecycleEvents[1].Envelope.Event != LifecycleProgress {
		t.Fatalf("journal reordered by occurred_at: %+v", second.Task.LifecycleEvents)
	}
}

func TestLifecycleRejectsDelayedGenerationDuplicateConflictAndCrossNamespace(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	created, _ := AddForProfile(project, "review", "s", AddInput{Title: "generation", AssignTo: "worker"}, now)
	ref := GenerationRef{Generation: "run-1", ManifestDigest: "manifest-1", GoalNamespace: "review/s", GoalDigest: "goal-1"}
	prepared, err := PrepareDispatchForProfile(project, "review", "s", created.ID, DispatchIntentOptions{From: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo", GenerationRef: &ref, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := prepared.Task.LifecycleTaskGeneration
	if _, err := ResetForProfile(project, "review", "s", created.ID, "worker", "retry", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := ClaimForProfile(project, "review", "s", created.ID, "worker", now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if reclaimed.LifecycleTaskGeneration == "" || reclaimed.LifecycleTaskGeneration == oldGeneration {
		t.Fatalf("reset/reclaim did not rotate task generation: old=%s new=%s", oldGeneration, reclaimed.LifecycleTaskGeneration)
	}
	envelope := LifecycleEnvelope{SchemaVersion: LifecycleSchemaVersion, EventID: "event-1", TaskID: created.ID, Event: LifecycleACK, Actor: "worker",
		Profile: "review", Session: "s", NamespaceID: "review/s", RunGeneration: ref.Generation, GenerationRef: ref,
		TaskGeneration: oldGeneration, DispatchMessageID: "dispatch-1", OutboxIntentID: "intent-1", OccurredAt: now}
	if err := ValidateLifecycleEnvelope(envelope); err != nil {
		t.Fatal(err)
	}
	taskImage := reclaimed
	if err := appendLifecycleEvent(&taskImage, envelope); err == nil || !strings.Contains(err.Error(), "task_generation") {
		t.Fatalf("delayed old task generation accepted: %v", err)
	}
	envelope.TaskGeneration = reclaimed.LifecycleTaskGeneration
	if err := appendLifecycleEvent(&taskImage, envelope); err != nil || len(taskImage.LifecycleEvents) != 1 {
		t.Fatalf("current event rejected: events=%d err=%v", len(taskImage.LifecycleEvents), err)
	}
	if err := appendLifecycleEvent(&taskImage, envelope); err != nil || len(taskImage.LifecycleEvents) != 1 {
		t.Fatalf("identical duplicate was not idempotent: events=%d err=%v", len(taskImage.LifecycleEvents), err)
	}
	conflict := envelope
	conflict.Actor = "other"
	if err := appendLifecycleEvent(&taskImage, conflict); err == nil || !strings.Contains(err.Error(), "different content") {
		t.Fatalf("conflicting duplicate accepted: %v", err)
	}
	cross := envelope
	cross.Session, cross.NamespaceID = "other", "review/other"
	if _, err := LifecycleEnvelopeSHA256(cross); err == nil || !strings.Contains(err.Error(), "goal_namespace") {
		t.Fatalf("cross-session generation accepted: %v", err)
	}
}

func TestStructuredBlockAndCancelCommitTerminalStateEventAndOutboxTogether(t *testing.T) {
	previousVerify := verifyCommandEvidenceLinkRecord
	verifyCommandEvidenceLinkRecord = func(string, string, string, string, CommandEvidenceLink) error { return nil }
	t.Cleanup(func() { verifyCommandEvidenceLinkRecord = previousVerify })
	for _, event := range []LifecycleEvent{LifecycleBlock, LifecycleCancel} {
		t.Run(string(event), func(t *testing.T) {
			project := t.TempDir()
			now := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
			created, _ := AddForProfile(project, "review", "s", AddInput{Title: "terminal", AssignTo: "worker"}, now)
			ref := GenerationRef{Generation: "run-1", ManifestDigest: "manifest-1", GoalNamespace: "review/s", GoalDigest: "goal-1"}
			prepared, _ := PrepareDispatchForProfile(project, "review", "s", created.ID, DispatchIntentOptions{From: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo", GenerationRef: &ref, Now: now})
			_, _ = BeginOutboxDeliveryForProfile(project, "review", "s", created.ID, prepared.Intent.ID, now)
			_, _, _ = FinishDispatchForProfile(project, "review", "s", created.ID, prepared.Intent.ID, Dispatch{Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker"}, DeliveryOutcome{State: DeliveryDelivered, MessageID: "dispatch-1"}, now)
			base := filepath.Join(project, ".amq-squad", "evidence", "commands", "review", "s", "tasks", created.ID, "attempts", "attempt-terminal")
			sha := "sha256:" + strings.Repeat("b", 64)
			link := CommandEvidenceLink{AttemptID: "attempt-terminal", Actor: "worker", Subject: "terminal proof", ProcessState: "succeeded", FinalizationState: "complete",
				ManifestPath: filepath.Join(base, "manifest.json"), ManifestSHA256: sha, OutcomePath: filepath.Join(base, "outcome.json"), OutcomeSHA256: sha, SummaryPath: filepath.Join(base, "summary.json"), SummarySHA256: sha}
			linked, _, err := LinkCommandEvidenceForProfile(project, "review", "s", created.ID, taskFileDigest(t, project, "review", "s", created.ID), "worker", link, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			evidence, _ := LifecycleCommandEvidenceRef(linked.CommandEvidence[0])
			opts := TerminalLifecycleOptions{Actor: "worker", Reason: "bounded terminal reason", GenerationRef: ref, EvidenceRef: &evidence, Now: now.Add(2 * time.Second)}
			var result TerminalLifecycleResult
			if event == LifecycleBlock {
				result, err = BlockAtomicLifecycleForProfile(project, "review", "s", created.ID, opts)
			} else {
				result, err = CancelAtomicLifecycleForProfile(project, "review", "s", created.ID, opts)
			}
			if err != nil {
				t.Fatal(err)
			}
			wantStatus := StatusBlocked
			if event == LifecycleCancel {
				wantStatus = StatusCancelled
			}
			if result.Task.Status != wantStatus || len(result.Outbox) != 1 || result.Outbox[0].Lifecycle == nil || result.Outbox[0].Lifecycle.Event != event || len(result.Task.LifecycleEvents) != 1 {
				t.Fatalf("terminal transition was not atomic: %+v", result)
			}
		})
	}
}
