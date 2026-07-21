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
