package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
)

const (
	taskLifecycleGenerationOne  = "11111111111111111111111111111111"
	taskLifecycleGenerationTwo  = "22222222222222222222222222222222"
	taskLifecycleGenerationNext = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	taskLifecycleGenerationFake = "ffffffffffffffffffffffffffffffff"
	taskLifecycleManifestDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	taskLifecycleGoalDigest     = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func taskLifecyclePreparedManifest(project, generation string) preparedRunManifest {
	manifest := preparedRunManifest{
		SchemaVersion: preparedRunSchema,
		Generation:    generation,
		Project:       project,
		Profile:       "review",
		Session:       "s",
		Namespace:     "review/s",
		GoalNamespace: "review/s",
		GoalDigest:    taskLifecycleGoalDigest,
	}
	manifest.PreparationRecord = preparedRunPreparationRecord{Generation: generation, Namespace: manifest.Namespace}
	manifest.ResumeAuthorization = preparedRunResumeAuthorization{Policy: "managed_launch_record", SingleUse: true, RecordBound: true, GenerationBound: true}
	return manifest
}

func TestLifecycleCorrelationUsesValueEqualityAndImmutableEvidence(t *testing.T) {
	project, seeded := seedEvidenceTask(t, true)
	project, _ = filepath.EvalSymlinks(project)
	now := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	generation := taskstore.GenerationRef{Generation: taskLifecycleGenerationOne, ManifestDigest: taskLifecycleManifestDigest, GoalNamespace: "review/s", GoalDigest: taskLifecycleGoalDigest}
	prepared, err := taskstore.PrepareDispatchForProfile(project, "review", "s", seeded.ID, taskstore.DispatchIntentOptions{From: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo", GenerationRef: &generation, Now: now})
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
		Actor: "worker", Evidence: "tests", Notify: true, GenerationRef: &generation, EvidenceRef: &evidence, Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	intent := done.Outbox[0]
	if _, err := taskstore.BeginOutboxDeliveryForProfile(project, "review", "s", seeded.ID, intent.ID, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.FinishOutboxDeliveryForProfile(project, "review", "s", seeded.ID, intent.ID, taskstore.DeliveryOutcome{State: taskstore.DeliveryDelivered, MessageID: "msg-done"}, now.Add(3*time.Second)); err != nil {
		t.Fatal(err)
	}
	selected, err := readTaskSelection(project, "review", "s", seeded.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Round-trip through JSON so EvidenceRef has distinct pointer identity from
	// the task's outbox envelope while retaining equal values.
	b, _ := json.Marshal(intent.Lifecycle)
	var decoded taskstore.LifecycleEnvelope
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatal(err)
	}
	message := state.Message{ID: "msg-done", From: "worker", To: []string{"cto"}, Owner: "cto", Path: filepath.Join(selected.Namespace.AMQRoot, "agents", "cto", "inbox", "new", "msg-done.md")}
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

func TestManagedLifecycleRejectsArbitraryOverrideStaleAndMissingActorRecord(t *testing.T) {
	project := t.TempDir()
	ns := squadnamespace.Resolve(project, "review", "s")
	manifest := taskLifecyclePreparedManifest(project, taskLifecycleGenerationOne)
	if err := publishPreparedRunGeneration(project, "review", "s", manifest); err != nil {
		t.Fatal(err)
	}
	_, manifestDigest, err := readPreparedRunManifestSnapshot(project, "review", "s")
	if err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(ns.AMQRoot, "agents", "worker")
	record := launch.Record{Schema: launch.SchemaVersion, CWD: project, Binary: "codex", Handle: "worker", Session: "s", Root: ns.AMQRoot,
		PreparedRunGeneration: taskLifecycleGenerationOne, PreparedRunDigest: manifestDigest, PreparedRunGoalNamespace: "review/s", PreparedRunGoalDigest: taskLifecycleGoalDigest}
	if err := launch.Write(agentDir, record); err != nil {
		t.Fatal(err)
	}
	want := taskstore.GenerationRef{Generation: taskLifecycleGenerationOne, ManifestDigest: manifestDigest, GoalNamespace: "review/s", GoalDigest: taskLifecycleGoalDigest}
	if got, err := taskLifecycleGenerationRef(ns, "worker", want); err != nil || got != want {
		t.Fatalf("matching managed generation rejected: got=%+v err=%v", got, err)
	}
	forged := want
	forged.Generation = taskLifecycleGenerationFake
	if _, err := taskLifecycleGenerationRef(ns, "worker", forged); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("arbitrary override accepted: %v", err)
	}
	record.PreparedRunGeneration = taskLifecycleGenerationTwo
	if err := launch.Write(agentDir, record); err != nil {
		t.Fatal(err)
	}
	if _, err := taskLifecycleGenerationRef(ns, "worker", want); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("stale generation accepted: %v", err)
	}
	record.PreparedRunGeneration = taskLifecycleGenerationOne
	if err := launch.Write(agentDir, record); err != nil {
		t.Fatal(err)
	}
	manifest.Generation = taskLifecycleGenerationNext
	if err := publishPreparedRunGeneration(project, "review", "s", manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := taskLifecycleGenerationRef(ns, "worker", taskstore.GenerationRef{}); err == nil || !strings.Contains(err.Error(), "current accepted") {
		t.Fatalf("pointer-advanced stale record accepted: %v", err)
	}
	if err := os.Remove(launch.ExistingPath(agentDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := taskLifecycleGenerationRef(ns, "worker", taskstore.GenerationRef{}); err == nil {
		t.Fatal("missing managed actor record accepted")
	}
}

func TestDispatchGenerationRequiresManagedPairAgreement(t *testing.T) {
	project := t.TempDir()
	ns := squadnamespace.Resolve(project, "review", "s")
	root := ns.AMQRoot
	manifest := taskLifecyclePreparedManifest(project, taskLifecycleGenerationOne)
	if err := publishPreparedRunGeneration(project, "review", "s", manifest); err != nil {
		t.Fatal(err)
	}
	_, digest, err := readPreparedRunManifestSnapshot(project, "review", "s")
	if err != nil {
		t.Fatal(err)
	}
	write := func(handle, generation string) {
		t.Helper()
		if err := launch.Write(filepath.Join(root, "agents", handle), launch.Record{Schema: launch.SchemaVersion, Handle: handle,
			PreparedRunGeneration: generation, PreparedRunDigest: digest, PreparedRunGoalNamespace: "review/s", PreparedRunGoalDigest: taskLifecycleGoalDigest}); err != nil {
			t.Fatal(err)
		}
	}
	write("cto", taskLifecycleGenerationOne)
	write("worker", taskLifecycleGenerationTwo)
	if _, err := dispatchGenerationRef(project, "review", "s", root, "cto", "worker"); err == nil || !strings.Contains(err.Error(), "disagree") {
		t.Fatalf("mismatched dispatch generation accepted: %v", err)
	}
	write("worker", taskLifecycleGenerationOne)
	if ref, err := dispatchGenerationRef(project, "review", "s", root, "cto", "worker"); err != nil || ref == nil || ref.Generation != taskLifecycleGenerationOne {
		t.Fatalf("matching dispatch generation rejected: ref=%+v err=%v", ref, err)
	}
	if err := os.Remove(launch.ExistingPath(filepath.Join(root, "agents", "worker"))); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatchGenerationRef(project, "review", "s", root, "cto", "worker"); err == nil || !strings.Contains(err.Error(), "requires launch record") {
		t.Fatalf("one-sided managed generation accepted: %v", err)
	}
	if err := os.Remove(launch.ExistingPath(filepath.Join(root, "agents", "cto"))); err != nil {
		t.Fatal(err)
	}
	if _, err := dispatchGenerationRef(project, "review", "s", root, "cto", "worker"); err == nil || !strings.Contains(err.Error(), "requires launch record") {
		t.Fatalf("deleted-both managed records downgraded to legacy: %v", err)
	}
}
