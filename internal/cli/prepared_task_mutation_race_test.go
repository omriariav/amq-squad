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
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type preparedLifecycleRaceFixture struct {
	project  string
	manifest preparedRunManifest
	ref      taskstore.GenerationRef
	task     taskstore.Task
	evidence *taskstore.EvidenceRef
}

func preparedTaskRaceManifest(project, profile, session, generation string) preparedRunManifest {
	namespace := squadnamespace.ID(profile, session)
	manifest := taskLifecyclePreparedManifest(project, generation)
	manifest.Profile = squadnamespace.NormalizeProfile(profile)
	manifest.Session = session
	manifest.Namespace = namespace
	manifest.GoalNamespace = namespace
	manifest.PreparationRecord.Namespace = namespace
	return manifest
}

func writePreparedTaskRaceLaunch(t *testing.T, project, profile, session, handle string, ref taskstore.GenerationRef) {
	t.Helper()
	ns := squadnamespace.Resolve(project, profile, session)
	if err := launch.Write(filepath.Join(ns.AMQRoot, "agents", handle), launch.Record{
		Schema: launch.SchemaVersion, CWD: project, Binary: "codex", Handle: handle, Session: session, Root: ns.AMQRoot,
		PreparedRunGeneration: ref.Generation, PreparedRunDigest: ref.ManifestDigest,
		PreparedRunGoalNamespace: ref.GoalNamespace, PreparedRunGoalDigest: ref.GoalDigest,
	}); err != nil {
		t.Fatal(err)
	}
}

func newPreparedLifecycleRaceFixture(t *testing.T, withEvidence bool) preparedLifecycleRaceFixture {
	t.Helper()
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	if err := team.WriteProfile(project, "review", team.Team{Project: project, Orchestrated: true, Lead: "cto", Members: []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"},
		{Role: "worker", Handle: "worker", Binary: "codex", Session: "s"},
	}}); err != nil {
		t.Fatal(err)
	}
	manifest := preparedTaskRaceManifest(project, "review", "s", taskLifecycleGenerationOne)
	if err := publishPreparedRunGeneration(project, "review", "s", manifest); err != nil {
		t.Fatal(err)
	}
	_, digest, err := readPreparedRunManifestSnapshot(project, "review", "s")
	if err != nil {
		t.Fatal(err)
	}
	ref := taskstore.GenerationRef{Generation: manifest.Generation, ManifestDigest: digest, GoalNamespace: manifest.GoalNamespace, GoalDigest: manifest.GoalDigest}
	writePreparedTaskRaceLaunch(t, project, "review", "s", "worker", ref)

	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	created, err := taskstore.AddForProfile(project, "review", "s", taskstore.AddInput{Title: "race", AssignTo: "worker"}, now)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := taskstore.PrepareDispatchForProfile(project, "review", "s", created.ID, taskstore.DispatchIntentOptions{
		From: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo", Subject: "race", Body: "race", GenerationRef: &ref, Now: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := taskstore.LinkDispatchForProfile(project, "review", "s", prepared.Task.ID, taskstore.Dispatch{
		Sender: "cto", Assignee: "worker", Thread: "p2p/cto__worker", Kind: "todo", Subject: "race", MessageID: "dispatch-race",
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	fixture := preparedLifecycleRaceFixture{project: project, manifest: manifest, ref: ref, task: current}
	if withEvidence {
		if _, _, err := captureOutput(t, func() error {
			return runEvidence(evidenceRunArgs(t, project, current.ID, "attempt-race", "race proof", true))
		}); err != nil {
			t.Fatal(err)
		}
		current, err = taskstore.ShowForProfile(project, "review", "s", current.ID)
		if err != nil || len(current.CommandEvidence) != 1 {
			t.Fatalf("command evidence=%+v err=%v", current.CommandEvidence, err)
		}
		evidence, err := taskstore.LifecycleCommandEvidenceRef(current.CommandEvidence[0])
		if err != nil {
			t.Fatal(err)
		}
		fixture.task, fixture.evidence = current, &evidence
	}
	return fixture
}

func lifecycleRaceCommand(f preparedLifecycleRaceFixture, event taskstore.LifecycleEvent) []string {
	base := []string{"--project", f.project, "--profile", "review", "--session", "s"}
	switch event {
	case taskstore.LifecycleACK, taskstore.LifecycleProgress, taskstore.LifecycleCheckpoint:
		return append([]string{"event", f.task.ID, "--event", string(event), "--me", "worker"}, base...)
	case taskstore.LifecycleReview:
		return append([]string{"event", f.task.ID, "--event", string(event), "--me", "worker", "--evidence-kind", f.evidence.Kind, "--evidence-id", f.evidence.ID, "--evidence-sha256", f.evidence.SHA256}, base...)
	case taskstore.LifecycleDone:
		return append([]string{"done", f.task.ID, "--me", "worker", "--evidence-id", f.evidence.ID}, base...)
	case taskstore.LifecycleBlock:
		return append([]string{"block", f.task.ID, "--me", "worker", "--reason", "blocked by race", "--evidence-id", f.evidence.ID}, base...)
	case taskstore.LifecycleCancel:
		return append([]string{"cancel", f.task.ID, "--me", "worker", "--reason", "cancelled by race", "--evidence-id", f.evidence.ID}, base...)
	default:
		panic("unsupported lifecycle race event " + event)
	}
}

func advancePreparedTaskRace(project, profile, session string, manifest preparedRunManifest, attempted chan<- struct{}) <-chan error {
	done := make(chan error, 1)
	go func() {
		namespaceAdmission, err := acquireNamespaceWriterAdmission(project, profile, session)
		if err != nil {
			done <- err
			return
		}
		defer namespaceAdmission.close()
		close(attempted)
		manifestAdmission, err := acquirePreparedManifestWriterAdmission(project, profile, session)
		if err != nil {
			done <- err
			return
		}
		defer manifestAdmission.close()
		done <- publishPreparedRunGeneration(project, profile, session, manifest)
	}()
	return done
}

func TestPreparationWinsBeforeDispatchAndEveryLifecycleMutation(t *testing.T) {
	events := []taskstore.LifecycleEvent{
		taskstore.LifecycleACK, taskstore.LifecycleProgress, taskstore.LifecycleCheckpoint, taskstore.LifecycleReview,
		taskstore.LifecycleDone, taskstore.LifecycleBlock, taskstore.LifecycleCancel,
	}
	for _, event := range events {
		t.Run(string(event), func(t *testing.T) {
			fixture := newPreparedLifecycleRaceFixture(t, event.RequiresEvidence())
			_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent stale-event to cto\n")
			before, err := os.ReadFile(filepath.Join(taskstore.DirForProfile(fixture.project, "review", "s"), fixture.task.ID+".json"))
			if err != nil {
				t.Fatal(err)
			}

			namespaceAdmission, err := acquireNamespaceWriterAdmission(fixture.project, "review", "s")
			if err != nil {
				t.Fatal(err)
			}
			manifestAdmission, err := acquirePreparedManifestWriterAdmission(fixture.project, "review", "s")
			if err != nil {
				namespaceAdmission.close()
				t.Fatal(err)
			}
			next := nextPreparedRunManifestForTest(t, fixture.manifest)
			readerAttempted := make(chan struct{})
			oldReaderHook := preparedManifestReaderBeforeAdmission
			preparedManifestReaderBeforeAdmission = func(string, string, string) error {
				close(readerAttempted)
				return nil
			}
			t.Cleanup(func() { preparedManifestReaderBeforeAdmission = oldReaderHook })
			errCh := make(chan error, 1)
			go func() { errCh <- runTask(lifecycleRaceCommand(fixture, event)) }()
			<-readerAttempted
			if err := publishPreparedRunGeneration(fixture.project, "review", "s", next); err != nil {
				t.Fatal(err)
			}
			manifestAdmission.close()
			namespaceAdmission.close()
			preparedManifestReaderBeforeAdmission = oldReaderHook
			if err := <-errCh; err == nil || (!strings.Contains(err.Error(), "current accepted prepared artifact") && !strings.Contains(err.Error(), ".prepared.lock")) {
				t.Fatalf("stale %s mutation err=%v", event, err)
			}
			after, err := os.ReadFile(filepath.Join(taskstore.DirForProfile(fixture.project, "review", "s"), fixture.task.ID+".json"))
			if err != nil || string(after) != string(before) {
				t.Fatalf("preparation-winning %s race mutated task: err=%v", event, err)
			}
		})
	}

	t.Run("DISPATCH", func(t *testing.T) {
		project := t.TempDir()
		chdir(t, project)
		writeDispatchTeam(t, project)
		calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent stale-dispatch to qa\n")
		manifest := preparedTaskRaceManifest(project, team.DefaultProfile, "issue-96", taskLifecycleGenerationOne)
		if err := publishPreparedRunGeneration(project, team.DefaultProfile, "issue-96", manifest); err != nil {
			t.Fatal(err)
		}
		_, digest, err := readPreparedRunManifestSnapshot(project, team.DefaultProfile, "issue-96")
		if err != nil {
			t.Fatal(err)
		}
		ref := taskstore.GenerationRef{Generation: manifest.Generation, ManifestDigest: digest, GoalNamespace: manifest.GoalNamespace, GoalDigest: manifest.GoalDigest}
		writePreparedTaskRaceLaunch(t, project, team.DefaultProfile, "issue-96", "cto", ref)
		writePreparedTaskRaceLaunch(t, project, team.DefaultProfile, "issue-96", "qa", ref)
		namespaceAdmission, err := acquireNamespaceWriterAdmission(project, team.DefaultProfile, "issue-96")
		if err != nil {
			t.Fatal(err)
		}
		manifestAdmission, err := acquirePreparedManifestWriterAdmission(project, team.DefaultProfile, "issue-96")
		if err != nil {
			namespaceAdmission.close()
			t.Fatal(err)
		}
		next := nextPreparedRunManifestForTest(t, manifest)
		readerAttempted := make(chan struct{})
		oldReaderHook := preparedManifestReaderBeforeAdmission
		preparedManifestReaderBeforeAdmission = func(string, string, string) error { close(readerAttempted); return nil }
		t.Cleanup(func() { preparedManifestReaderBeforeAdmission = oldReaderHook })
		errCh := make(chan error, 1)
		go func() {
			errCh <- runDispatch([]string{"--project", project, "--session", "issue-96", "--role", "qa", "--subject", "stale", "--body", "stale", "--create-task", "--no-wake"})
		}()
		<-readerAttempted
		if err := publishPreparedRunGeneration(project, team.DefaultProfile, "issue-96", next); err != nil {
			t.Fatal(err)
		}
		manifestAdmission.close()
		namespaceAdmission.close()
		preparedManifestReaderBeforeAdmission = oldReaderHook
		if err := <-errCh; err == nil || (!strings.Contains(err.Error(), "current accepted prepared artifact") && !strings.Contains(err.Error(), ".prepared.lock")) {
			t.Fatalf("stale dispatch err=%v", err)
		}
		if len(*calls) != 0 {
			t.Fatalf("stale dispatch sent AMQ: %+v", *calls)
		}
		if _, err := os.Stat(filepath.Join(taskstore.DirForProfile(project, team.DefaultProfile, "issue-96"), "t1.json")); !os.IsNotExist(err) {
			t.Fatalf("stale dispatch created task: %v", err)
		}
	})
}

func TestLifecycleMutationWinsBeforePreparationAcrossEventCatalog(t *testing.T) {
	events := []taskstore.LifecycleEvent{
		taskstore.LifecycleACK, taskstore.LifecycleProgress, taskstore.LifecycleCheckpoint, taskstore.LifecycleReview,
		taskstore.LifecycleDone, taskstore.LifecycleBlock, taskstore.LifecycleCancel,
	}
	for _, event := range events {
		t.Run(string(event), func(t *testing.T) {
			fixture := newPreparedLifecycleRaceFixture(t, event.RequiresEvidence())
			_ = withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent lifecycle-winner to cto\n")
			next := nextPreparedRunManifestForTest(t, fixture.manifest)
			attempted := make(chan struct{})
			var writerDone <-chan error
			oldHook := taskAfterLifecycleGenerationRead
			taskAfterLifecycleGenerationRead = func(project, profile, session string, got taskstore.LifecycleEvent, ref taskstore.GenerationRef) error {
				if got != event || ref != fixture.ref {
					t.Fatalf("race hook event/ref=%s/%+v", got, ref)
				}
				writerDone = advancePreparedTaskRace(project, profile, session, next, attempted)
				<-attempted
				return nil
			}
			t.Cleanup(func() { taskAfterLifecycleGenerationRead = oldHook })
			runErr := runTask(lifecycleRaceCommand(fixture, event))
			taskAfterLifecycleGenerationRead = oldHook
			writerErr := <-writerDone
			if runErr != nil {
				t.Fatalf("lifecycle-first %s: %v", event, runErr)
			}
			if writerErr != nil {
				t.Fatal(writerErr)
			}
			current, err := taskstore.ShowForProfile(fixture.project, "review", "s", fixture.task.ID)
			if err != nil || len(current.LifecycleEvents) == 0 {
				t.Fatalf("committed %s lifecycle=%+v err=%v", event, current.LifecycleEvents, err)
			}
			last := current.LifecycleEvents[len(current.LifecycleEvents)-1].Envelope
			if last.Event != event || last.GenerationRef != fixture.ref {
				t.Fatalf("committed lifecycle=%+v", last)
			}
			accepted, _, err := readPreparedRunManifestSnapshot(fixture.project, "review", "s")
			if err != nil || accepted.Generation != next.Generation {
				t.Fatalf("successor CURRENT=%s err=%v want=%s", accepted.Generation, err, next.Generation)
			}
		})
	}
}

func TestDispatchMutationWinsBeforePreparation(t *testing.T) {
	project := t.TempDir()
	project, _ = filepath.EvalSymlinks(project)
	chdir(t, project)
	writeDispatchTeam(t, project)
	calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent dispatch-winner to qa\n")
	manifest := preparedTaskRaceManifest(project, team.DefaultProfile, "issue-96", taskLifecycleGenerationOne)
	if err := publishPreparedRunGeneration(project, team.DefaultProfile, "issue-96", manifest); err != nil {
		t.Fatal(err)
	}
	_, digest, err := readPreparedRunManifestSnapshot(project, team.DefaultProfile, "issue-96")
	if err != nil {
		t.Fatal(err)
	}
	ref := taskstore.GenerationRef{Generation: manifest.Generation, ManifestDigest: digest, GoalNamespace: manifest.GoalNamespace, GoalDigest: manifest.GoalDigest}
	writePreparedTaskRaceLaunch(t, project, team.DefaultProfile, "issue-96", "cto", ref)
	writePreparedTaskRaceLaunch(t, project, team.DefaultProfile, "issue-96", "qa", ref)
	next := nextPreparedRunManifestForTest(t, manifest)
	attempted := make(chan struct{})
	var writerDone <-chan error
	oldHook := dispatchAfterGenerationRead
	dispatchAfterGenerationRead = func(gotProject, profile, session string, got *taskstore.GenerationRef) error {
		if got == nil || *got != ref {
			t.Fatalf("dispatch race generation_ref=%+v want=%+v", got, ref)
		}
		writerDone = advancePreparedTaskRace(gotProject, profile, session, next, attempted)
		<-attempted
		return nil
	}
	t.Cleanup(func() { dispatchAfterGenerationRead = oldHook })
	runErr := runDispatch([]string{"--project", project, "--session", "issue-96", "--role", "qa", "--subject", "winner", "--body", "winner", "--create-task", "--no-wake"})
	dispatchAfterGenerationRead = oldHook
	writerErr := <-writerDone
	if runErr != nil {
		t.Fatal(runErr)
	}
	if writerErr != nil {
		t.Fatal(writerErr)
	}
	if len(*calls) != 1 {
		t.Fatalf("dispatch AMQ calls=%d want=1", len(*calls))
	}
	created, err := taskstore.ShowForProfile(project, team.DefaultProfile, "issue-96", "t1")
	if err != nil || created.LifecycleGenerationRef == nil || *created.LifecycleGenerationRef != ref {
		t.Fatalf("dispatch task generation=%+v err=%v", created.LifecycleGenerationRef, err)
	}
	accepted, _, err := readPreparedRunManifestSnapshot(project, team.DefaultProfile, "issue-96")
	if err != nil || accepted.Generation != next.Generation {
		t.Fatalf("successor CURRENT=%s err=%v want=%s", accepted.Generation, err, next.Generation)
	}
}

func TestAdvancedCurrentRejectsDelayedOutboxAndCompletionReconcile(t *testing.T) {
	for _, mode := range []string{"deliver", "retry-delivery", "completion-reconcile"} {
		t.Run(mode, func(t *testing.T) {
			fixture := newPreparedLifecycleRaceFixture(t, false)
			calls := withAMQCommandSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, "Sent stale-recovery to cto\n")
			now := time.Date(2026, 7, 21, 18, 30, 0, 0, time.UTC)
			event, err := taskstore.RecordLifecycleEventForProfile(fixture.project, "review", "s", fixture.task.ID, taskstore.LifecycleEventOptions{
				Event: taskstore.LifecycleACK, Actor: "worker", GenerationRef: fixture.ref, Body: "ack", Now: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "retry-delivery" {
				if _, err := taskstore.BeginOutboxDeliveryForProfile(fixture.project, "review", "s", fixture.task.ID, event.Intent.ID, now.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if _, err := taskstore.FinishOutboxDeliveryForProfile(fixture.project, "review", "s", fixture.task.ID, event.Intent.ID, taskstore.DeliveryOutcome{
					State: taskstore.DeliveryFailedBeforeInvoke, Error: "offline",
				}, now.Add(2*time.Second)); err != nil {
					t.Fatal(err)
				}
			}
			next := nextPreparedRunManifestForTest(t, fixture.manifest)
			if err := publishPreparedRunGeneration(fixture.project, "review", "s", next); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(taskstore.DirForProfile(fixture.project, "review", "s"), fixture.task.ID+".json")
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			base := []string{"--project", fixture.project, "--profile", "review", "--session", "s", "--me", "worker"}
			var args []string
			switch mode {
			case "deliver":
				args = append([]string{"deliver", fixture.task.ID, "--intent", event.Intent.ID}, base...)
			case "retry-delivery":
				args = append([]string{"retry-delivery", fixture.task.ID, "--intent", event.Intent.ID, "--reason", "confirmed offline"}, base...)
			case "completion-reconcile":
				args = []string{"reconcile", fixture.task.ID, "--evidence-id", "stale-done", "--project", fixture.project, "--profile", "review", "--session", "s"}
			}
			err = runTask(args)
			if err == nil || !strings.Contains(err.Error(), "task lifecycle generation_ref does not match the current accepted prepared artifact") {
				t.Fatalf("stale %s err=%v", mode, err)
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(before) {
				t.Fatalf("stale %s mutated task: err=%v", mode, err)
			}
			if len(*calls) != 0 {
				t.Fatalf("stale %s sent AMQ: %+v", mode, *calls)
			}
		})
	}
}

func TestCompletionEvidenceRechecksCurrentPreparedGeneration(t *testing.T) {
	fixture := newPreparedLifecycleRaceFixture(t, true)
	now := time.Date(2026, 7, 21, 19, 0, 0, 0, time.UTC)
	done, err := taskstore.DoneAtomicForProfile(fixture.project, "review", "s", fixture.task.ID, taskstore.DoneOptions{
		Actor: "worker", Notify: true, GenerationRef: &fixture.ref, EvidenceRef: fixture.evidence, Now: now,
	})
	if err != nil || len(done.Outbox) != 1 || done.Outbox[0].Lifecycle == nil {
		t.Fatalf("structured DONE=%+v err=%v", done, err)
	}
	intent := done.Outbox[0]
	if _, err := taskstore.BeginOutboxDeliveryForProfile(fixture.project, "review", "s", fixture.task.ID, intent.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.FinishOutboxDeliveryForProfile(fixture.project, "review", "s", fixture.task.ID, intent.ID, taskstore.DeliveryOutcome{
		State: taskstore.DeliveryDelivered, MessageID: "stale-done",
	}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	contextJSON, err := taskstore.LifecycleContextJSON(*intent.Lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	var context map[string]any
	if err := json.Unmarshal([]byte(contextJSON), &context); err != nil {
		t.Fatal(err)
	}
	root := squadnamespace.AMQRoot(fixture.project, "review", "s")
	seedEvidenceMessage(t, root, "cto", "stale-done", "DONE: race", "done", now.Add(2*time.Second), context)
	selected, err := readTaskSelection(fixture.project, "review", "s", fixture.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	next := nextPreparedRunManifestForTest(t, fixture.manifest)
	if err := publishPreparedRunGeneration(fixture.project, "review", "s", next); err != nil {
		t.Fatal(err)
	}
	preview, err := assessTaskCompletionEvidence(selected, "stale-done", now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if preview.Exact || !containsTestString(preview.Blockers, "current_prepared_generation_mismatch") {
		t.Fatalf("stale completion evidence preview=%+v", preview)
	}
}
