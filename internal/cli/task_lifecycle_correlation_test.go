package cli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
)

func TestLifecycleCorrelationUsesValueEqualityAndImmutableEvidence(t *testing.T) {
	project, seeded := seedEvidenceTask(t, true)
	project, _ = filepath.EvalSymlinks(project)
	now := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	// GenerationRef survives as opaque task-lifecycle correlation data. This
	// fixture supplies it directly and deliberately creates no prepared-run
	// manifest, token, admission, or launch record.
	generation := taskstore.GenerationRef{
		Generation: "run-1", ManifestDigest: "manifest-1",
		GoalNamespace: "review/s", GoalDigest: "goal-1",
	}
	prepared, err := taskstore.PrepareDispatchForProfile(project, "review", "s", seeded.ID, taskstore.DispatchIntentOptions{
		From: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo",
		GenerationRef: &generation, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.BeginOutboxDeliveryForProfile(project, "review", "s", seeded.ID, prepared.Intent.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := taskstore.FinishDispatchForProfile(project, "review", "s", seeded.ID, prepared.Intent.ID, taskstore.Dispatch{
		Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo",
	}, taskstore.DeliveryOutcome{State: taskstore.DeliveryDelivered, MessageID: "dispatch-1"}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runEvidence(evidenceRunArgs(t, project, seeded.ID, "attempt-done", "completion proof", true))
	}); err != nil {
		t.Fatal(err)
	}
	linked, err := taskstore.ShowForProfile(project, "review", "s", seeded.ID)
	if err != nil || len(linked.CommandEvidence) != 1 {
		t.Fatalf("linked evidence=%+v err=%v", linked.CommandEvidence, err)
	}
	evidence, err := taskstore.LifecycleCommandEvidenceRef(linked.CommandEvidence[0])
	if err != nil {
		t.Fatal(err)
	}
	done, err := taskstore.DoneAtomicForProfile(project, "review", "s", seeded.ID, taskstore.DoneOptions{
		Actor: "worker", Evidence: "tests", Notify: true,
		GenerationRef: &generation, EvidenceRef: &evidence, Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	intent := done.Outbox[0]
	if _, err := taskstore.BeginOutboxDeliveryForProfile(project, "review", "s", seeded.ID, intent.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.FinishOutboxDeliveryForProfile(project, "review", "s", seeded.ID, intent.ID, taskstore.DeliveryOutcome{
		State: taskstore.DeliveryDelivered, MessageID: "msg-done",
	}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	selected, err := readTaskSelection(project, "review", "s", seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip through JSON so EvidenceRef has distinct pointer identity from
	// the task's outbox envelope while retaining equal values.
	b, err := json.Marshal(intent.Lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	var decoded taskstore.LifecycleEnvelope
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	message := state.Message{
		ID: "msg-done", From: "worker", To: []string{"cto"}, Owner: "cto",
		Path: filepath.Join(selected.Namespace.AMQRoot, "agents", "cto", "inbox", "new", "msg-done.md"),
	}
	if blockers := lifecycleCorrelationBlockers(selected, message, decoded); len(blockers) != 0 {
		t.Fatalf("value-equal decoded envelope rejected: %v", blockers)
	}
	forged := decoded
	forged.Actor = "other"
	if blockers := lifecycleCorrelationBlockers(selected, message, forged); !containsTestString(blockers, "lifecycle_actor_mismatch") {
		t.Fatalf("forged actor accepted: %v", blockers)
	}
	wrongEvidence := decoded
	wrongRef := *wrongEvidence.EvidenceRef
	wrongRef.SHA256 = strings.Repeat("0", 64)
	wrongEvidence.EvidenceRef = &wrongRef
	if blockers := lifecycleCorrelationBlockers(selected, message, wrongEvidence); !containsTestString(blockers, "lifecycle_evidence_mismatch") {
		t.Fatalf("wrong evidence digest accepted: %v", blockers)
	}
	missingEvidence := decoded
	missingEvidence.EvidenceRef = nil
	if blockers := lifecycleCorrelationBlockers(selected, message, missingEvidence); !containsTestString(blockers, "lifecycle_evidence_missing") {
		t.Fatalf("missing evidence accepted: %v", blockers)
	}
}
