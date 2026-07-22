package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type preparedDispatchNextFixture struct {
	preparedLifecycleRaceFixture
	successor taskstore.Task
}

func newPreparedDispatchNextFixture(t *testing.T, targetMode, intent string) preparedDispatchNextFixture {
	t.Helper()
	base := newPreparedLifecycleRaceFixture(t, true)
	tm, err := team.ReadProfile(base.project, "review")
	if err != nil {
		t.Fatal(err)
	}
	tm.Members = append(tm.Members, team.Member{
		Role: "implementer", Handle: "implementer", Binary: "codex", Session: "s", ActorMode: targetMode,
	})
	if err := team.WriteProfile(base.project, "review", tm); err != nil {
		t.Fatal(err)
	}
	input := taskstore.AddInput{
		Title: "successor", Description: "continue exact generation", AssignTo: "implementer", DependsOn: []string{base.task.ID},
	}
	if intent != "" {
		input.Intent = intent
		input.Artifact = "internal/cli/task.go"
		input.ExpectedBaseSHA = strings.Repeat("a", 40)
		input.Implementer = "implementer"
		input.Reviewer = "cto"
	}
	successor, err := taskstore.AddForProfile(base.project, "review", "s", input, time.Date(2026, 7, 21, 18, 1, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	writePreparedTaskRaceLaunch(t, base.project, "review", "s", "implementer", base.ref)
	return preparedDispatchNextFixture{preparedLifecycleRaceFixture: base, successor: successor}
}

func taskFileBytes(t *testing.T, project, profile, session, id string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(taskstore.DirForProfile(project, profile, session), id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func persistDispatchNextTaskFixture(t *testing.T, project, profile, session string, current taskstore.Task) {
	t.Helper()
	b, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	if err := os.WriteFile(filepath.Join(taskstore.DirForProfile(project, profile, session), current.ID+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func persistDispatchNextDONEFixture(t *testing.T, selected taskSelection, envelope taskstore.LifecycleEnvelope, messageID, body string, now time.Time) string {
	t.Helper()
	dispatch, ok := taskstore.CanonicalDispatch(selected.Task)
	if !ok {
		t.Fatal("DONE fixture requires canonical dispatch")
	}
	selected.Task.Outbox = append(selected.Task.Outbox, taskstore.OutboxIntent{
		ID: envelope.OutboxIntentID, TaskID: selected.Task.ID, Type: "lifecycle_event", State: taskstore.OutboxDelivered,
		From: envelope.Actor, To: dispatch.Sender, Thread: dispatch.Thread, Kind: string(state.KindStatus), Subject: "DONE: " + selected.Task.Title,
		Body: body, MessageID: messageID, CreatedAt: now, UpdatedAt: now, Lifecycle: &envelope,
	})
	persistDispatchNextTaskFixture(t, selected.ProjectDir, selected.Profile, selected.Session, selected.Task)
	contextJSON, err := taskstore.LifecycleContextJSON(envelope)
	if err != nil {
		t.Fatal(err)
	}
	var context map[string]any
	if err := json.Unmarshal([]byte(contextJSON), &context); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(selected.Namespace.AMQRoot, "agents", dispatch.Sender, "inbox", "new")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	header := map[string]any{
		"schema": 1, "id": messageID, "from": envelope.Actor, "to": []string{dispatch.Sender}, "thread": dispatch.Thread,
		"subject": "DONE: " + selected.Task.Title, "created": now.Format(time.RFC3339Nano), "kind": string(state.KindStatus), "context": context,
	}
	headerJSON, err := json.MarshalIndent(header, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, messageID+".md")
	if err := os.WriteFile(path, []byte(fmt.Sprintf("---json\n%s\n---\n%s\n", headerJSON, body)), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func preparedDispatchNextDoneArgs(f preparedDispatchNextFixture) []string {
	return []string{
		"done", f.task.ID, "--me", "worker", "--evidence-id", f.evidence.ID, "--dispatch-next", f.successor.ID,
		"--project", f.project, "--profile", "review", "--session", "s", "--json",
	}
}

func evidenceRunArgsForActor(t *testing.T, project, taskID, attemptID, subject, actor string) []string {
	t.Helper()
	args := evidenceRunArgs(t, project, taskID, attemptID, subject, true)
	for i := 1; i < len(args); i++ {
		if args[i-1] == "--me" {
			args[i] = actor
			return args
		}
	}
	t.Fatal("evidence helper args have no --me actor")
	return nil
}

func assertExactSuccessorLifecycle(t *testing.T, got taskstore.LifecycleEnvelope, f preparedDispatchNextFixture, event taskstore.LifecycleEvent, taskGeneration, dispatchMessageID, outboxID string) {
	t.Helper()
	if got.Event != event || got.TaskID != f.successor.ID || got.Actor != "implementer" || got.GenerationRef != f.ref ||
		got.RunGeneration != f.ref.Generation || got.TaskGeneration != taskGeneration || got.DispatchMessageID != dispatchMessageID || got.OutboxIntentID != outboxID {
		t.Fatalf("%s successor lifecycle identity=%+v", event, got)
	}
}

func TestPreparedDispatchNextCarriesExactIdentityThroughACKAndDONE(t *testing.T) {
	f := newPreparedDispatchNextFixture(t, team.ActorModeImplementation, "")
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent successor-exact to recipient\n")
	if _, _, err := captureOutput(t, func() error { return runTask(preparedDispatchNextDoneArgs(f)) }); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 {
		t.Fatalf("completion + successor sends=%d want=2", len(*calls))
	}
	successor, err := taskstore.ShowForProfile(f.project, "review", "s", f.successor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if successor.Status != taskstore.StatusInProgress || successor.LifecycleGenerationRef == nil || *successor.LifecycleGenerationRef != f.ref ||
		successor.LifecycleTaskGeneration == "" || len(successor.Outbox) != 1 || successor.Outbox[0].Type != "successor_dispatch" ||
		successor.Outbox[0].State != taskstore.OutboxDelivered || successor.Outbox[0].MessageID != "successor-exact" {
		t.Fatalf("prepared successor dispatch=%+v", successor)
	}
	taskGeneration := successor.LifecycleTaskGeneration
	dispatchMessageID := successor.Outbox[0].MessageID

	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"event", successor.ID, "--event", "ACK", "--me", "implementer", "--project", f.project, "--profile", "review", "--session", "s", "--json"})
	}); err != nil {
		t.Fatal(err)
	}
	successor, err = taskstore.ShowForProfile(f.project, "review", "s", successor.ID)
	if err != nil || len(successor.Outbox) != 2 || successor.Outbox[1].Lifecycle == nil {
		t.Fatalf("ACK successor=%+v err=%v", successor, err)
	}
	assertExactSuccessorLifecycle(t, *successor.Outbox[1].Lifecycle, f, taskstore.LifecycleACK, taskGeneration, dispatchMessageID, successor.Outbox[1].ID)

	if _, _, err := captureOutput(t, func() error {
		return runEvidence(evidenceRunArgsForActor(t, f.project, successor.ID, "attempt-successor", "successor proof", "implementer"))
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"done", successor.ID, "--me", "implementer", "--evidence-id", "attempt-successor", "--project", f.project, "--profile", "review", "--session", "s", "--json"})
	}); err != nil {
		t.Fatal(err)
	}
	successor, err = taskstore.ShowForProfile(f.project, "review", "s", successor.ID)
	if err != nil || successor.Status != taskstore.StatusCompleted || len(successor.Outbox) != 3 || successor.Outbox[2].Lifecycle == nil {
		t.Fatalf("DONE successor=%+v err=%v", successor, err)
	}
	assertExactSuccessorLifecycle(t, *successor.Outbox[2].Lifecycle, f, taskstore.LifecycleDone, taskGeneration, dispatchMessageID, successor.Outbox[2].ID)
}

func TestPreparedDispatchNextFreshDONEEvidencePreviewsAndReconcilesExactly(t *testing.T) {
	f := newPreparedDispatchNextFixture(t, team.ActorModeImplementation, taskstore.IntentImplement)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent successor-fresh to recipient\n")
	if _, _, err := captureOutput(t, func() error { return runTask(preparedDispatchNextDoneArgs(f)) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runEvidence(evidenceRunArgsForActor(t, f.project, f.successor.ID, "attempt-fresh-done", "fresh successor proof", "implementer"))
	}); err != nil {
		t.Fatal(err)
	}
	selected, err := readTaskSelection(f.project, "review", "s", f.successor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if selected.Task.Dispatch == nil || selected.Task.Dispatch.Sender != "worker" || selected.Task.Dispatch.Assignee != "implementer" ||
		selected.Task.Dispatch.Thread != "p2p/implementer__worker" || selected.Task.Dispatch.Kind != "todo" || selected.Task.Dispatch.Subject != "successor" ||
		selected.Task.Dispatch.OutboxIntentID != selected.Task.Outbox[0].ID || selected.Task.Dispatch.DeliveryState != taskstore.OutboxDelivered ||
		selected.Task.Dispatch.MessageID != "successor-fresh" || selected.Task.Dispatch.ReceiptAttemptID == "" || selected.Task.Dispatch.ReceiptPath == "" || len(selected.Task.CommandEvidence) != 1 {
		t.Fatalf("fresh successor authority=%+v evidence=%+v", selected.Task.Dispatch, selected.Task.CommandEvidence)
	}
	evidence, err := taskstore.LifecycleCommandEvidenceRef(selected.Task.CommandEvidence[0])
	if err != nil {
		t.Fatal(err)
	}
	now := taskNow().Add(time.Minute)
	envelope := taskstore.LifecycleEnvelope{
		SchemaVersion: taskstore.LifecycleSchemaVersion, EventID: "fresh-done-event", TaskID: selected.Task.ID, Event: taskstore.LifecycleDone,
		Actor: "implementer", Profile: "review", Session: "s", NamespaceID: "review/s", RunGeneration: f.ref.Generation,
		GenerationRef: f.ref, TaskGeneration: selected.Task.LifecycleTaskGeneration, DispatchMessageID: "successor-fresh",
		OutboxIntentID: "fresh-done-outbox", EvidenceRef: &evidence, OccurredAt: now,
	}
	persistDispatchNextDONEFixture(t, selected, envelope, "fresh-done", "fresh successor complete", now)
	selected, err = readTaskSelection(f.project, "review", "s", f.successor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(selected.Task.Outbox) != 2 || selected.Task.Outbox[1].Lifecycle == nil || selected.Task.Outbox[1].Lifecycle.DispatchMessageID != "successor-fresh" {
		t.Fatalf("persisted fresh DONE outbox=%+v", selected.Task.Outbox)
	}
	preview, err := assessTaskCompletionEvidence(selected, "fresh-done", now)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.Exact || len(preview.Blockers) != 0 || preview.Expected.Sender != "implementer" || preview.Expected.To != "worker" || preview.Expected.Thread != "p2p/implementer__worker" {
		t.Fatalf("fresh successor preview=%+v", preview)
	}
	applied, err := taskstore.ApplyCompletionEvidenceForProfile(f.project, "review", "s", selected.Task.ID, taskstore.CompletionEvidenceApply{
		ExpectedTaskSHA256: selected.FileSHA256, Evidence: completionEvidenceRecord(preview, "worker", now), Exact: true, Actor: "worker", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !applied.Changed || applied.Task.Status != taskstore.StatusCompleted || applied.Task.CompletionReconcile == nil || applied.Task.CompletionReconcile.CompletedEvidence == nil ||
		applied.Task.CompletionReconcile.CompletedEvidence.MessageID != "fresh-done" {
		t.Fatalf("fresh successor reconciliation=%+v", applied)
	}
}

func TestResetReDispatchPersistedDONERejectsOldAndReconcilesOnlyReplacementAuthority(t *testing.T) {
	f := newPreparedDispatchNextFixture(t, team.ActorModeImplementation, taskstore.IntentImplement)
	_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent dispatch-old to recipient\n")
	if _, _, err := captureOutput(t, func() error { return runTask(preparedDispatchNextDoneArgs(f)) }); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runEvidence(evidenceRunArgsForActor(t, f.project, f.successor.ID, "attempt-reset-done", "reset successor proof", "implementer"))
	}); err != nil {
		t.Fatal(err)
	}
	selected, err := readTaskSelection(f.project, "review", "s", f.successor.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldDispatch := *selected.Task.Dispatch
	if oldDispatch.MessageID != "dispatch-old" || oldDispatch.OutboxIntentID != selected.Task.Outbox[0].ID || oldDispatch.DeliveryState != taskstore.OutboxDelivered {
		t.Fatalf("old canonical dispatch=%+v", oldDispatch)
	}
	evidence, err := taskstore.LifecycleCommandEvidenceRef(selected.Task.CommandEvidence[0])
	if err != nil {
		t.Fatal(err)
	}
	oldGeneration := selected.Task.LifecycleTaskGeneration
	oldDone := taskstore.LifecycleEnvelope{
		SchemaVersion: taskstore.LifecycleSchemaVersion, EventID: "old-done-event", TaskID: selected.Task.ID, Event: taskstore.LifecycleDone,
		Actor: "implementer", Profile: "review", Session: "s", NamespaceID: "review/s", RunGeneration: f.ref.Generation,
		GenerationRef: f.ref, TaskGeneration: oldGeneration, DispatchMessageID: oldDispatch.MessageID,
		OutboxIntentID: "old-done-outbox", EvidenceRef: &evidence, OccurredAt: taskNow().Add(time.Minute),
	}
	persistDispatchNextDONEFixture(t, selected, oldDone, "old-done", "old completion", oldDone.OccurredAt)
	if _, err := taskstore.ResetForProfile(f.project, "review", "s", f.successor.ID, "implementer", "replacement dispatch", taskNow().Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	reset, err := taskstore.ShowForProfile(f.project, "review", "s", f.successor.ID)
	if err != nil || reset.Dispatch == nil || reset.Dispatch.DeliveryState != "reset" || reset.Dispatch.MessageID != "" {
		t.Fatalf("reset authority=%+v err=%v", reset.Dispatch, err)
	}
	replacement, err := taskstore.PrepareDispatchForProfile(f.project, "review", "s", f.successor.ID, taskstore.DispatchIntentOptions{
		From: "worker", Assignee: "implementer", Thread: "p2p/implementer__worker", Kind: "todo", Subject: "successor",
		ReceiptAttemptID: "replacement-attempt", ReceiptPath: "/receipts/replacement-attempt.json", GenerationRef: &f.ref, Now: taskNow().Add(3 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Task.Dispatch == nil || replacement.Task.Dispatch.MessageID != "" || replacement.Task.Dispatch.OutboxIntentID != replacement.Intent.ID || replacement.Task.Dispatch.DeliveryState != taskstore.OutboxPending {
		t.Fatalf("replacement placeholder=%+v", replacement.Task.Dispatch)
	}
	if _, err := taskstore.BeginOutboxDeliveryForProfile(f.project, "review", "s", f.successor.ID, replacement.Intent.ID, taskNow().Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.AttachOutboxReceiptForProfile(f.project, "review", "s", f.successor.ID, replacement.Intent.ID, "replacement-attempt", "/receipts/replacement-attempt.json", taskNow().Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.FinishOutboxDeliveryForProfile(f.project, "review", "s", f.successor.ID, replacement.Intent.ID, taskstore.DeliveryOutcome{State: taskstore.DeliveryDelivered, MessageID: "dispatch-replacement"}, taskNow().Add(6*time.Minute)); err != nil {
		t.Fatal(err)
	}
	selected, err = readTaskSelection(f.project, "review", "s", f.successor.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := selected.Task.Dispatch
	if current == nil || current.MessageID != "dispatch-replacement" || current.OutboxIntentID != replacement.Intent.ID || current.DeliveryState != taskstore.OutboxDelivered ||
		current.ReceiptAttemptID != "replacement-attempt" || current.ReceiptPath != "/receipts/replacement-attempt.json" || selected.Task.LifecycleTaskGeneration == oldGeneration {
		t.Fatalf("replacement canonical dispatch=%+v task_generation=%s old=%s", current, selected.Task.LifecycleTaskGeneration, oldGeneration)
	}
	beforeOld := taskFileBytes(t, f.project, "review", "s", f.successor.ID)
	oldPreview, err := assessTaskCompletionEvidence(selected, "old-done", taskNow().Add(7*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if oldPreview.Exact || !containsTestString(oldPreview.Blockers, "dispatch_message_mismatch") || !containsTestString(oldPreview.Blockers, "task_generation_mismatch") {
		t.Fatalf("old DONE preview=%+v", oldPreview)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"reconcile", f.successor.ID, "--evidence-id", "old-done", "--binding-digest", oldPreview.BindingSHA256, "--apply", "--me", "worker", "--project", f.project, "--profile", "review", "--session", "s"})
	}); err == nil {
		t.Fatal("old DONE unexpectedly reconciled")
	}
	if string(taskFileBytes(t, f.project, "review", "s", f.successor.ID)) != string(beforeOld) {
		t.Fatal("old DONE refusal changed persisted task bytes")
	}
	newDone := taskstore.LifecycleEnvelope{
		SchemaVersion: taskstore.LifecycleSchemaVersion, EventID: "replacement-done-event", TaskID: selected.Task.ID, Event: taskstore.LifecycleDone,
		Actor: "implementer", Profile: "review", Session: "s", NamespaceID: "review/s", RunGeneration: f.ref.Generation,
		GenerationRef: f.ref, TaskGeneration: selected.Task.LifecycleTaskGeneration, DispatchMessageID: current.MessageID,
		OutboxIntentID: "replacement-done-outbox", EvidenceRef: &evidence, OccurredAt: taskNow().Add(8 * time.Minute),
	}
	persistDispatchNextDONEFixture(t, selected, newDone, "replacement-done", "replacement completion", newDone.OccurredAt)
	previewOut, _, err := captureOutput(t, func() error {
		return runTask([]string{"reconcile", f.successor.ID, "--evidence-id", "replacement-done", "--project", f.project, "--profile", "review", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	previewEnv := decodeJSONEnvelope[taskReconcileEnvelopeData](t, previewOut)
	preview := previewEnv.Data.Completion
	if preview == nil || !preview.Exact || len(preview.Blockers) != 0 || preview.Expected.Sender != "implementer" || preview.Expected.To != "worker" || preview.Expected.Thread != "p2p/implementer__worker" {
		t.Fatalf("replacement DONE preview=%+v", preview)
	}
	applyOut, _, err := captureOutput(t, func() error {
		return runTask([]string{"reconcile", f.successor.ID, "--evidence-id", "replacement-done", "--binding-digest", preview.BindingSHA256, "--apply", "--me", "worker", "--project", f.project, "--profile", "review", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	applyEnv := decodeJSONEnvelope[taskReconcileEnvelopeData](t, applyOut)
	if applyEnv.Data.Applied == nil || !applyEnv.Data.Applied.Changed || applyEnv.Data.Applied.Task.Status != taskstore.StatusCompleted ||
		applyEnv.Data.Applied.Task.CompletionReconcile == nil || applyEnv.Data.Applied.Task.CompletionReconcile.CompletedEvidence == nil ||
		applyEnv.Data.Applied.Task.CompletionReconcile.CompletedEvidence.MessageID != "replacement-done" {
		t.Fatalf("replacement DONE apply=%+v", applyEnv.Data.Applied)
	}
}

func TestPreparedDispatchNextRejectsMissingStaleAndReviewTargetBeforeSideEffects(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   string
		intent string
		mutate func(t *testing.T, f preparedDispatchNextFixture)
		want   string
	}{
		{name: "missing sender record", mode: team.ActorModeImplementation, mutate: func(t *testing.T, f preparedDispatchNextFixture) {
			if err := os.Remove(launch.Path(filepath.Join(f.preparedLifecycleRaceFixture.project, ".agent-mail", "review", "s", "agents", "worker"))); err != nil {
				t.Fatal(err)
			}
		}, want: "launch record"},
		{name: "missing target record", mode: team.ActorModeImplementation, mutate: func(t *testing.T, f preparedDispatchNextFixture) {
			if err := os.Remove(launch.Path(filepath.Join(f.preparedLifecycleRaceFixture.project, ".agent-mail", "review", "s", "agents", "implementer"))); err != nil {
				t.Fatal(err)
			}
		}, want: "launch record"},
		{name: "stale sender record", mode: team.ActorModeImplementation, mutate: func(t *testing.T, f preparedDispatchNextFixture) {
			stale := f.ref
			stale.Generation = taskLifecycleGenerationTwo
			writePreparedTaskRaceLaunch(t, f.project, "review", "s", "worker", stale)
		}, want: "values disagree"},
		{name: "stale target record", mode: team.ActorModeImplementation, mutate: func(t *testing.T, f preparedDispatchNextFixture) {
			stale := f.ref
			stale.Generation = taskLifecycleGenerationTwo
			writePreparedTaskRaceLaunch(t, f.project, "review", "s", "implementer", stale)
		}, want: "values disagree"},
		{name: "review target implement refusal", mode: team.ActorModeReview, intent: taskstore.IntentImplement, want: "EffectiveActorMode=review"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newPreparedDispatchNextFixture(t, tc.mode, tc.intent)
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent forbidden to recipient\n")
			if tc.mutate != nil {
				tc.mutate(t, f)
			}
			beforePredecessor := taskFileBytes(t, f.project, "review", "s", f.task.ID)
			beforeSuccessor := taskFileBytes(t, f.project, "review", "s", f.successor.ID)
			_, _, err := captureOutput(t, func() error { return runTask(preparedDispatchNextDoneArgs(f)) })
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("dispatch-next refusal err=%v want %q", err, tc.want)
			}
			if string(taskFileBytes(t, f.project, "review", "s", f.task.ID)) != string(beforePredecessor) ||
				string(taskFileBytes(t, f.project, "review", "s", f.successor.ID)) != string(beforeSuccessor) {
				t.Fatal("refused dispatch-next mutated predecessor or successor task")
			}
			if len(*calls) != 0 {
				t.Fatalf("refused dispatch-next sent AMQ: %+v", *calls)
			}
			if _, statErr := os.Stat(deliveryReceiptDir(f.project, "review", "s")); !os.IsNotExist(statErr) {
				t.Fatalf("refused dispatch-next persisted receipt artifact: %v", statErr)
			}
		})
	}
}

func TestDispatchNextCleanNoTeamLegacyRemainsCompatible(t *testing.T) {
	project := t.TempDir()
	successor, err := taskstore.Add(project, "s", taskstore.AddInput{Title: "legacy successor", AssignTo: "worker"}, taskNow())
	if err != nil {
		t.Fatal(err)
	}
	ns := squadnamespace.Resolve(project, team.DefaultProfile, "s")
	for _, path := range []string{
		team.Path(project),
		preparedRunPath(project, team.DefaultProfile, "s"),
		launch.Path(filepath.Join(ns.AMQRoot, "agents", "lead")),
		launch.Path(filepath.Join(ns.AMQRoot, "agents", "worker")),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("clean legacy fixture unexpectedly contains %s: %v", path, statErr)
		}
	}
	before := taskFileBytes(t, project, team.DefaultProfile, "s", successor.ID)
	binding, err := taskDoneSuccessorDispatchBinding(project, team.DefaultProfile, "s", ns, "lead", successor.ID)
	if err != nil {
		t.Fatal(err)
	}
	if binding == nil || binding.Assignee != "worker" || binding.GenerationRef != nil || binding.Thread != "p2p/lead__worker" {
		t.Fatalf("clean legacy binding=%+v", binding)
	}
	if string(taskFileBytes(t, project, team.DefaultProfile, "s", successor.ID)) != string(before) {
		t.Fatal("clean legacy admission mutated successor")
	}
}

func TestDispatchNextNoCURRENTRefusesReviewActorsAndOrphanedPreparedIdentityWithoutSideEffects(t *testing.T) {
	ref := taskstore.GenerationRef{
		Generation: taskLifecycleGenerationOne, ManifestDigest: strings.Repeat("b", 64),
		GoalNamespace: "review/s", GoalDigest: "sha256:" + strings.Repeat("c", 64),
	}
	for _, tc := range []struct {
		name         string
		mode         string
		intent       string
		orphanHandle string
		want         string
	}{
		{name: "review implement", mode: team.ActorModeReview, intent: taskstore.IntentImplement, want: "EffectiveActorMode=review"},
		{name: "review lifecycle", mode: team.ActorModeReview, intent: taskstore.IntentLifecycle, want: "EffectiveActorMode=review"},
		{name: "orphan sender", mode: team.ActorModeImplementation, intent: taskstore.IntentImplement, orphanHandle: "worker", want: "no accepted prepared artifact"},
		{name: "orphan target", mode: team.ActorModeImplementation, intent: taskstore.IntentImplement, orphanHandle: "implementer", want: "no accepted prepared artifact"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project := t.TempDir()
			if err := team.WriteProfile(project, "review", team.Team{Project: project, Orchestrated: true, Lead: "cto", Members: []team.Member{
				{Role: "cto", Handle: "cto", Binary: "codex", Session: "s", ActorMode: team.ActorModeImplementation},
				{Role: "worker", Handle: "worker", Binary: "codex", Session: "s", ActorMode: team.ActorModeImplementation},
				{Role: "implementer", Handle: "implementer", Binary: "codex", Session: "s", ActorMode: tc.mode},
			}}); err != nil {
				t.Fatal(err)
			}
			predecessor, err := taskstore.AddForProfile(project, "review", "s", taskstore.AddInput{Title: "predecessor", AssignTo: "worker"}, taskNow())
			if err != nil {
				t.Fatal(err)
			}
			successor, err := taskstore.AddForProfile(project, "review", "s", taskstore.AddInput{
				Title: "successor", Intent: tc.intent, Artifact: "internal/cli/task.go", ExpectedBaseSHA: strings.Repeat("a", 40),
				Implementer: "implementer", Reviewer: "cto", AssignTo: "implementer", DependsOn: []string{predecessor.ID},
			}, taskNow())
			if err != nil {
				t.Fatal(err)
			}
			if _, err := taskstore.ClaimForProfile(project, "review", "s", predecessor.ID, "worker", taskNow()); err != nil {
				t.Fatal(err)
			}
			if _, err := taskstore.LinkDispatchForProfile(project, "review", "s", predecessor.ID, taskstore.Dispatch{
				Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker", MessageID: "dispatch-predecessor",
			}, taskNow()); err != nil {
				t.Fatal(err)
			}
			if tc.orphanHandle != "" {
				writePreparedTaskRaceLaunch(t, project, "review", "s", tc.orphanHandle, ref)
			}
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent forbidden to recipient\n")
			beforePredecessor := taskFileBytes(t, project, "review", "s", predecessor.ID)
			beforeSuccessor := taskFileBytes(t, project, "review", "s", successor.ID)
			_, _, err = captureOutput(t, func() error {
				return runTask([]string{"done", predecessor.ID, "--me", "worker", "--dispatch-next", successor.ID, "--project", project, "--profile", "review", "--session", "s"})
			})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("refusal err=%v want %q", err, tc.want)
			}
			if string(taskFileBytes(t, project, "review", "s", predecessor.ID)) != string(beforePredecessor) ||
				string(taskFileBytes(t, project, "review", "s", successor.ID)) != string(beforeSuccessor) {
				t.Fatal("refusal changed task bytes")
			}
			if len(*calls) != 0 {
				t.Fatalf("refusal crossed AMQ boundary: %+v", *calls)
			}
			if _, statErr := os.Stat(deliveryReceiptDir(project, "review", "s")); !os.IsNotExist(statErr) {
				t.Fatalf("refusal persisted receipt artifact: %v", statErr)
			}
		})
	}
}

func TestPreparedDispatchNextUncertainDeliveryRequiresConfirmedRetryBeforeLifecycle(t *testing.T) {
	f := newPreparedDispatchNextFixture(t, team.ActorModeImplementation, "")
	calls := withDispatchAMQCommandErrorSeam(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "", errors.New("AMQ unavailable"))
	stdout, _, err := captureOutput(t, func() error { return runTask(preparedDispatchNextDoneArgs(f)) })
	if err != nil {
		t.Fatalf("committed transition must remain visible: %v", err)
	}
	if env := decodeJSONEnvelope[mutationResult](t, stdout); env.Data.Status != "completed_delivery_attention" {
		t.Fatalf("uncertain transition=%+v", env.Data)
	}
	successor, err := taskstore.ShowForProfile(f.project, "review", "s", f.successor.ID)
	if err != nil || len(successor.Outbox) != 1 || successor.Outbox[0].State != taskstore.OutboxUncertain || successor.Outbox[0].MessageID != "" {
		t.Fatalf("uncertain prepared successor=%+v err=%v", successor, err)
	}
	intentID := successor.Outbox[0].ID
	beforeCalls := len(*calls)
	reconcileOut, _, err := captureOutput(t, func() error {
		return runTask([]string{"reconcile", "--project", f.project, "--profile", "review", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcile := decodeJSONEnvelope[taskReconcileEnvelopeData](t, reconcileOut)
	if !cliFinding(reconcile.Data.Result.Findings, "outbox_delivery_uncertain", successor.ID) {
		t.Fatalf("uncertain successor reconcile findings=%+v", reconcile.Data.Result.Findings)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"event", successor.ID, "--event", "ACK", "--me", "implementer", "--project", f.project, "--profile", "review", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "dispatch_message_id") {
		t.Fatalf("typed ACK before authoritative delivery err=%v", err)
	}
	if len(*calls) != beforeCalls {
		t.Fatal("failed typed ACK crossed AMQ boundary")
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"retry-delivery", successor.ID, "--intent", intentID, "--me", "implementer", "--reason", "retry", "--project", f.project, "--profile", "review", "--session", "s"})
	}); err == nil || !strings.Contains(err.Error(), "confirm-not-delivered") {
		t.Fatalf("blind uncertain retry err=%v", err)
	}
	if len(*calls) != beforeCalls {
		t.Fatal("uncertain successor was resent without confirmation")
	}

	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		*calls = append(*calls, req)
		return []byte("Sent successor-retry to implementer\n"), nil
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"retry-delivery", successor.ID, "--intent", intentID, "--me", "implementer", "--reason", "operator confirmed absent", "--confirm-not-delivered", "--project", f.project, "--profile", "review", "--session", "s", "--json"})
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTask([]string{"event", successor.ID, "--event", "ACK", "--me", "implementer", "--project", f.project, "--profile", "review", "--session", "s", "--json"})
	}); err != nil {
		t.Fatalf("ACK after authoritative retry: %v", err)
	}
	successor, err = taskstore.ShowForProfile(f.project, "review", "s", successor.ID)
	if err != nil || len(successor.Outbox) != 2 || successor.Outbox[0].State != taskstore.OutboxDelivered || successor.Outbox[0].MessageID != "successor-retry" ||
		successor.Outbox[1].Lifecycle == nil || successor.Outbox[1].Lifecycle.DispatchMessageID != "successor-retry" {
		t.Fatalf("retried successor lifecycle=%+v err=%v", successor, err)
	}
	reconcileOut, _, err = captureOutput(t, func() error {
		return runTask([]string{"reconcile", "--project", f.project, "--profile", "review", "--session", "s", "--json"})
	})
	if err != nil {
		t.Fatal(err)
	}
	reconcile = decodeJSONEnvelope[taskReconcileEnvelopeData](t, reconcileOut)
	if cliFinding(reconcile.Data.Result.Findings, "outbox_delivery_uncertain", successor.ID) {
		t.Fatalf("confirmed successor retry did not clear uncertain reconcile finding: %+v", reconcile.Data.Result.Findings)
	}
}

func TestPreparedDispatchNextGenerationRaceOrdersAtomicTransactionAndCURRENT(t *testing.T) {
	t.Run("dispatch-next mutation wins", func(t *testing.T) {
		f := newPreparedDispatchNextFixture(t, team.ActorModeImplementation, "")
		_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent dispatch-next-race to recipient\n")
		next := nextPreparedRunManifestForTest(t, f.manifest)
		attempted := make(chan struct{})
		var writerDone <-chan error
		oldHook := taskAfterDispatchNextGenerationRead
		taskAfterDispatchNextGenerationRead = func(project, profile, session, successorID string, binding *taskstore.SuccessorDispatchBinding) error {
			if successorID != f.successor.ID || binding == nil || binding.GenerationRef == nil || *binding.GenerationRef != f.ref {
				t.Fatalf("dispatch-next race binding=%+v successor=%s", binding, successorID)
			}
			writerDone = advancePreparedTaskRace(project, profile, session, next, attempted)
			<-attempted
			return nil
		}
		t.Cleanup(func() { taskAfterDispatchNextGenerationRead = oldHook })
		runErr := runTask(preparedDispatchNextDoneArgs(f))
		taskAfterDispatchNextGenerationRead = oldHook
		writerErr := <-writerDone
		if runErr != nil {
			t.Fatal(runErr)
		}
		if writerErr != nil {
			t.Fatal(writerErr)
		}
		successor, err := taskstore.ShowForProfile(f.project, "review", "s", f.successor.ID)
		if err != nil || successor.LifecycleGenerationRef == nil || *successor.LifecycleGenerationRef != f.ref || successor.Status != taskstore.StatusInProgress {
			t.Fatalf("dispatch-next winner successor=%+v err=%v", successor, err)
		}
		accepted, _, err := readPreparedRunManifestSnapshot(f.project, "review", "s")
		if err != nil || accepted.Generation != next.Generation {
			t.Fatalf("CURRENT=%s err=%v want=%s", accepted.Generation, err, next.Generation)
		}
	})

	t.Run("preparation wins", func(t *testing.T) {
		f := newPreparedDispatchNextFixture(t, team.ActorModeImplementation, "")
		calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent stale-dispatch-next to recipient\n")
		beforePredecessor := taskFileBytes(t, f.project, "review", "s", f.task.ID)
		beforeSuccessor := taskFileBytes(t, f.project, "review", "s", f.successor.ID)
		namespaceAdmission, err := acquireNamespaceWriterAdmission(f.project, "review", "s")
		if err != nil {
			t.Fatal(err)
		}
		manifestAdmission, err := acquirePreparedManifestWriterAdmission(f.project, "review", "s")
		if err != nil {
			namespaceAdmission.close()
			t.Fatal(err)
		}
		next := nextPreparedRunManifestForTest(t, f.manifest)
		readerAttempted := make(chan struct{})
		oldReaderHook := preparedManifestReaderBeforeAdmission
		preparedManifestReaderBeforeAdmission = func(string, string, string) error {
			close(readerAttempted)
			return nil
		}
		t.Cleanup(func() { preparedManifestReaderBeforeAdmission = oldReaderHook })
		errCh := make(chan error, 1)
		go func() { errCh <- runTask(preparedDispatchNextDoneArgs(f)) }()
		<-readerAttempted
		if err := publishPreparedRunGeneration(f.project, "review", "s", next); err != nil {
			t.Fatal(err)
		}
		manifestAdmission.close()
		namespaceAdmission.close()
		preparedManifestReaderBeforeAdmission = oldReaderHook
		if err := <-errCh; err == nil || (!strings.Contains(err.Error(), "current accepted prepared artifact") && !strings.Contains(err.Error(), ".prepared.lock")) {
			t.Fatalf("preparation-winning dispatch-next err=%v", err)
		}
		if string(taskFileBytes(t, f.project, "review", "s", f.task.ID)) != string(beforePredecessor) ||
			string(taskFileBytes(t, f.project, "review", "s", f.successor.ID)) != string(beforeSuccessor) {
			t.Fatal("preparation-winning dispatch-next mutated task store")
		}
		if len(*calls) != 0 {
			t.Fatalf("preparation-winning dispatch-next sent AMQ: %+v", *calls)
		}
	})
}
