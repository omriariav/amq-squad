package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type namespaceMigrationFixture struct {
	project string
	source  squadnamespace.Ref
	target  squadnamespace.Ref
	message string
	task    string
	brief   string
}

func newNamespaceMigrationFixture(t *testing.T) namespaceMigrationFixture {
	return newNamespaceMigrationFixtureProfiles(t, team.DefaultProfile, "recovery")
}

func newNamespaceMigrationFixtureProfiles(t *testing.T, sourceProfile, targetProfile string) namespaceMigrationFixture {
	t.Helper()
	project := t.TempDir()
	var err error
	project, err = filepath.EvalSymlinks(project)
	if err != nil {
		t.Fatal(err)
	}
	source := squadnamespace.Resolve(project, sourceProfile, "source-run")
	target := squadnamespace.Resolve(project, targetProfile, "target-run")
	member := team.Member{Role: "qa", Handle: "qa", Binary: "codex"}
	sourceTeam := team.Team{Members: []team.Member{member}}
	sourceTeam.Members[0].Session = source.Session
	targetTeam := team.Team{Members: []team.Member{member}}
	targetTeam.Members[0].Session = target.Session
	if err := team.WriteProfile(project, source.Profile, sourceTeam); err != nil {
		t.Fatal(err)
	}
	if err := team.WriteProfile(project, target.Profile, targetTeam); err != nil {
		t.Fatal(err)
	}
	messagePath := filepath.Join(source.AMQRoot, "agents", "qa", "inbox", "cur", "message.md")
	if err := os.MkdirAll(filepath.Dir(messagePath), 0o700); err != nil {
		t.Fatal(err)
	}
	message := "From: cto\nTo: qa\n\nexact durable message\n"
	if err := os.WriteFile(messagePath, []byte(message), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"new", "tmp"} {
		if err := os.MkdirAll(filepath.Join(source.AMQRoot, "agents", "qa", "inbox", dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	taskPath := filepath.Join(source.Paths.Tasks, "t1.json")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	task := `{"id":"t1","status":"in_progress","assigned_to":"qa"}` + "\n"
	if err := os.WriteFile(taskPath, []byte(task), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(source.Paths.Brief), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := "# Durable brief\n"
	if err := os.WriteFile(source.Paths.Brief, []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(filepath.Join(source.AMQRoot, "agents", "qa"), launch.Record{
		CWD: project, Binary: "codex", Argv: []string{"codex"}, Session: source.Session,
		SharedWorkstream: true, Handle: "qa", Role: "qa", Root: source.AMQRoot,
		BaseRoot: namespaceMigrationBaseRoot(source), RootSource: "test", StartedAt: time.Unix(1, 0).UTC(),
		TeamProfile: source.Profile, TeamHome: project,
	}); err != nil {
		t.Fatal(err)
	}
	stateKey := source.Profile + "/" + source.Session + "\x00gate\x00gate/release"
	if err := writeNotifyState(defaultNotifyStatePath(project), notifyStateFile{Schema: notifyStateSchema, Items: map[string]notifyStateRecord{
		stateKey: {LatestID: "gate-1", Active: true},
	}}); err != nil {
		t.Fatal(err)
	}
	return namespaceMigrationFixture{project: project, source: source, target: target, message: message, task: task, brief: brief}
}

func (f namespaceMigrationFixture) plan(t *testing.T) namespaceMigrationPlan {
	t.Helper()
	plan, err := planNamespaceMigration(namespaceMigrationPlannerOptions{ProjectDir: f.project, Source: f.source, Target: f.target, DryRun: true, Now: time.Now().UTC(), Probe: livenessProbe(nil, nil, time.Now())})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Blockers) > 0 {
		t.Fatalf("unexpected blockers: %v", plan.Blockers)
	}
	return plan
}

func TestNamespaceMigrationPreviewIsReadOnlyAndStable(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan1 := fx.plan(t)
	if _, err := os.Lstat(namespaceMigrationRoot(fx.project)); !os.IsNotExist(err) {
		t.Fatalf("preview created migration state: %v", err)
	}
	plan2 := fx.plan(t)
	if plan1.ID != plan2.ID || plan1.Fingerprint != plan2.Fingerprint {
		t.Fatalf("stable plan identity changed: %s/%s vs %s/%s", plan1.ID, plan1.Fingerprint, plan2.ID, plan2.Fingerprint)
	}
}

func TestNamespaceMigrationCommitAndReverseRollbackPreservePayloads(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false
	result, err := executeNamespaceMigration(plan)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "committed" || result.Phase != migrationPhaseCommitted {
		t.Fatalf("result = %+v", result)
	}
	assertMigrationPayload(t, filepath.Join(fx.target.AMQRoot, "agents", "qa", "inbox", "cur", "message.md"), fx.message)
	assertMigrationPayload(t, filepath.Join(fx.target.Paths.Tasks, "t1.json"), fx.task)
	assertMigrationPayload(t, fx.target.Paths.Brief, fx.brief)
	if _, err := os.Lstat(fx.source.AMQRoot); !os.IsNotExist(err) {
		t.Fatalf("source AMQ root remains after commit: %v", err)
	}
	rolled, err := rollbackNamespaceMigration(fx.project, plan.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if rolled.Status != "rolled_back" {
		t.Fatalf("rollback result = %+v", rolled)
	}
	assertMigrationPayload(t, filepath.Join(fx.source.AMQRoot, "agents", "qa", "inbox", "cur", "message.md"), fx.message)
	assertMigrationPayload(t, filepath.Join(fx.source.Paths.Tasks, "t1.json"), fx.task)
	assertMigrationPayload(t, fx.source.Paths.Brief, fx.brief)
}

func TestNamespaceMigrationCrashAfterFirstPublishRecoversSource(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false
	original := namespaceMigrationFailpoint
	namespaceMigrationFailpoint = func(boundary string) error {
		if boundary == "published:amq_root" {
			return errors.New("injected crash")
		}
		return nil
	}
	_, err := executeNamespaceMigration(plan)
	namespaceMigrationFailpoint = original
	if err == nil || !strings.Contains(err.Error(), "injected crash") {
		t.Fatalf("execute error = %v", err)
	}
	journal, readErr := readNamespaceMigrationJournal(fx.project, plan.ID)
	if readErr != nil || journal.Phase != migrationPhaseRecoveryRequired {
		t.Fatalf("journal after crash = %+v err=%v", journal, readErr)
	}
	result, err := recoverNamespaceMigration(fx.project, plan.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "rolled_back" {
		t.Fatalf("recovery result = %+v", result)
	}
	assertMigrationPayload(t, filepath.Join(fx.source.AMQRoot, "agents", "qa", "inbox", "cur", "message.md"), fx.message)
	assertMigrationPayload(t, filepath.Join(fx.source.Paths.Tasks, "t1.json"), fx.task)
	if _, err := os.Lstat(fx.target.AMQRoot); !os.IsNotExist(err) {
		t.Fatalf("partial target still published: %v", err)
	}
}

func TestNamespaceMigrationCrashRecoveryEveryJournalPhase(t *testing.T) {
	tests := []struct {
		boundary string
		status   string
	}{
		{boundary: "planned", status: "rolled_back"},
		{boundary: "staged", status: "rolled_back"},
		{boundary: "source_backed_up", status: "rolled_back"},
		{boundary: "targets_published", status: "committed"},
		{boundary: "shared_state_published", status: "committed"},
		{boundary: "committed", status: "committed"},
	}
	for _, tc := range tests {
		t.Run(tc.boundary, func(t *testing.T) {
			fx := newNamespaceMigrationFixture(t)
			plan := fx.plan(t)
			plan.DryRun = false
			original := namespaceMigrationFailpoint
			namespaceMigrationFailpoint = func(boundary string) error {
				if boundary == tc.boundary {
					return errors.New("crash at " + boundary)
				}
				return nil
			}
			t.Cleanup(func() { namespaceMigrationFailpoint = original })
			result, err := executeNamespaceMigration(plan)
			if tc.boundary == "committed" {
				if err != nil || result.Status != "committed" {
					t.Fatalf("committed boundary result=%+v err=%v", result, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "crash at "+tc.boundary) {
				t.Fatalf("execute error = %v", err)
			}
			recovered, err := recoverNamespaceMigration(fx.project, plan.ID, false)
			if err != nil {
				t.Fatal(err)
			}
			if recovered.Status != tc.status {
				t.Fatalf("recovery = %+v, want status %s", recovered, tc.status)
			}
			if tc.status == "rolled_back" {
				assertMigrationPayload(t, filepath.Join(fx.source.AMQRoot, "agents", "qa", "inbox", "cur", "message.md"), fx.message)
			} else {
				assertMigrationPayload(t, filepath.Join(fx.target.AMQRoot, "agents", "qa", "inbox", "cur", "message.md"), fx.message)
				state, stateErr := readNotifyState(defaultNotifyStatePath(fx.project))
				if stateErr != nil {
					t.Fatal(stateErr)
				}
				targetKey := fx.target.Profile + "/" + fx.target.Session + "\x00gate\x00gate/release"
				if _, ok := state.Items[targetKey]; !ok {
					t.Fatalf("recovery did not publish target notify-state key %q", targetKey)
				}
			}
		})
	}
}

func TestNamespaceMigrationLaunchBaseRootDirectionMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, sourceProfile, targetProfile string
	}{
		{name: "default_to_named", sourceProfile: team.DefaultProfile, targetProfile: "recovery"},
		{name: "named_to_default", sourceProfile: "source-profile", targetProfile: team.DefaultProfile},
		{name: "named_to_named", sourceProfile: "source-profile", targetProfile: "target-profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newNamespaceMigrationFixtureProfiles(t, tc.sourceProfile, tc.targetProfile)
			plan := fx.plan(t)
			plan.DryRun = false
			if _, err := executeNamespaceMigration(plan); err != nil {
				t.Fatal(err)
			}
			rec, err := launch.Read(filepath.Join(fx.target.AMQRoot, "agents", "qa"))
			if err != nil {
				t.Fatal(err)
			}
			if rec.Root != fx.target.AMQRoot || rec.BaseRoot != namespaceMigrationBaseRoot(fx.target) || rec.TeamProfile != fx.target.Profile || rec.Session != fx.target.Session {
				t.Fatalf("migrated launch tuple = root %q base %q profile %q session %q", rec.Root, rec.BaseRoot, rec.TeamProfile, rec.Session)
			}
			if rec.AgentPID != 0 || rec.WakePID != 0 || rec.Tmux != nil || rec.Terminal != nil || rec.LauncherPaneID != "" {
				t.Fatalf("migrated launch retained runtime ownership: %+v", rec)
			}
		})
	}
}

func TestNamespaceMigrationBoundTransitionUsesPairedHandle(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	for _, handle := range []string{"aaa-first", "zzz-paired"} {
		if err := launch.Write(filepath.Join(fx.source.AMQRoot, "agents", handle), launch.Record{
			CWD: fx.project, Binary: "codex", Argv: []string{"codex"}, Session: fx.source.Session,
			Handle: handle, Role: handle, Root: fx.source.AMQRoot, BaseRoot: namespaceMigrationBaseRoot(fx.source),
			TeamProfile: fx.source.Profile, TeamHome: fx.project, Model: handle, StartedAt: time.Unix(2, 0).UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	goalDir := filepath.Join(fx.project, team.DirName, "goal-attempts", fx.source.Session)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	transition := resumeGoalTransitionRecord{SchemaVersion: resumeGoalTransitionSchemaVersion, TransitionID: "transition-1", Handle: "zzz-paired"}
	transitionBytes, _ := json.Marshal(transition)
	mainName := ".resume-redelivery-transition-1.json"
	if err := os.WriteFile(filepath.Join(goalDir, mainName), append(transitionBytes, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	bound := resumeGoalTransitionBound{SchemaVersion: resumeGoalTransitionSchemaVersion, TransitionID: transition.TransitionID, NewAttemptID: "attempt-1"}
	boundBytes, _ := json.Marshal(bound)
	plan := namespaceMigrationPlan{ProjectDir: fx.project, Source: fx.source, Target: fx.target, Artifacts: []namespaceMigrationArtifact{{Name: "goal_attempts", Source: goalDir}}}
	rewritten, _, err := rewriteMigrationGoalArtifact(plan, ".resume-redelivery-transition-1.bound.json", boundBytes)
	if err != nil {
		t.Fatal(err)
	}
	var got resumeGoalTransitionBound
	if err := json.Unmarshal(rewritten, &got); err != nil {
		t.Fatal(err)
	}
	wantDigest, wantMod, err := migratedLaunchGeneration(plan, "zzz-paired")
	if err != nil {
		t.Fatal(err)
	}
	firstDigest, _, err := migratedLaunchGeneration(plan, "aaa-first")
	if err != nil {
		t.Fatal(err)
	}
	if got.LaunchRecordDigest != wantDigest || got.LaunchRecordModTime != wantMod || got.LaunchRecordDigest == firstDigest {
		t.Fatalf("bound launch generation = %s/%d, paired=%s/%d first=%s", got.LaunchRecordDigest, got.LaunchRecordModTime, wantDigest, wantMod, firstDigest)
	}
}

func TestNamespaceMigrationPlannerExcludesSelfOwnedProfileLocks(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	locks, err := acquireNamespaceMigrationLocks(fx.project, fx.source.Profile, fx.target.Profile)
	if err != nil {
		t.Fatal(err)
	}
	defer locks.close()
	blocked, err := planNamespaceMigration(namespaceMigrationPlannerOptions{ProjectDir: fx.project, Source: fx.source, Target: fx.target})
	if err != nil {
		t.Fatal(err)
	}
	if !migrationStringsContain(blocked.Blockers, "lock is held") {
		t.Fatalf("ordinary planner did not observe owned endpoint locks: %v", blocked.Blockers)
	}
	owned, err := planNamespaceMigration(namespaceMigrationPlannerOptions{ProjectDir: fx.project, Source: fx.source, Target: fx.target, OwnsEndpointLocks: true})
	if err != nil {
		t.Fatal(err)
	}
	if migrationStringsContain(owned.Blockers, "lock is held") {
		t.Fatalf("locked replan blocked on self-owned locks: %v", owned.Blockers)
	}
}

func TestNamespaceMigrationBackupDigestCheckpointPrecedesPublication(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false
	artifact := migrationPlanArtifact(plan, "amq_root")
	var checkpointErr error
	seen := false
	original := namespaceMigrationFailpoint
	namespaceMigrationFailpoint = func(boundary string) error {
		if boundary != "backed_up:amq_root" {
			return nil
		}
		seen = true
		backup := filepath.Join(filepath.Dir(namespaceMigrationJournalPath(plan.ProjectDir, plan.ID)), "backup", "amq_root")
		checkpointErr = ensureMigrationPathDigest(backup, artifact.SHA256)
		if _, err := os.Lstat(plan.Target.AMQRoot); err == nil || !os.IsNotExist(err) {
			checkpointErr = errors.New("target was published before backup checkpoint")
		}
		return errors.New("stop after verified backup")
	}
	t.Cleanup(func() { namespaceMigrationFailpoint = original })
	_, err := executeNamespaceMigration(plan)
	if err == nil || !strings.Contains(err.Error(), "stop after verified backup") {
		t.Fatalf("execute error = %v", err)
	}
	if !seen || checkpointErr != nil {
		t.Fatalf("backup checkpoint seen=%t err=%v", seen, checkpointErr)
	}
}

func TestNamespaceMigrationReservationFreezesPreAdmittedWriterBeforeLockedReplan(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false
	writer, err := acquireNamespaceWriterAdmission(fx.project, fx.source.Profile, fx.source.Session)
	if err != nil {
		t.Fatal(err)
	}
	beforeAdmission := make(chan struct{})
	beforeReservation := make(chan struct{})
	originalAdmission, originalReservation := namespaceMigrationBeforeAdmission, namespaceMigrationBeforeReservation
	namespaceMigrationBeforeAdmission = func(namespaceMigrationPlan) error {
		close(beforeAdmission)
		return nil
	}
	namespaceMigrationBeforeReservation = func(namespaceMigrationPlan) error {
		close(beforeReservation)
		return nil
	}
	t.Cleanup(func() {
		namespaceMigrationBeforeAdmission = originalAdmission
		namespaceMigrationBeforeReservation = originalReservation
		writer.close()
	})
	type outcome struct {
		result namespaceMigrationResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := executeNamespaceMigration(plan)
		done <- outcome{result: result, err: err}
	}()
	<-beforeAdmission
	select {
	case <-beforeReservation:
		t.Fatal("migration crossed reservation while a pre-admitted writer was paused")
	case <-time.After(100 * time.Millisecond):
	}
	if err := os.WriteFile(fx.source.Paths.Brief, []byte("writer completed before reservation\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writer.close()
	select {
	case <-beforeReservation:
	case <-time.After(2 * time.Second):
		t.Fatal("migration did not resume after the admitted writer released")
	}
	got := <-done
	if got.err == nil || !strings.Contains(got.err.Error(), "preflight changed after reservation") {
		t.Fatalf("execute result=%+v err=%v", got.result, got.err)
	}
	journal, err := readNamespaceMigrationJournal(fx.project, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Phase != migrationPhaseAborted {
		t.Fatalf("journal phase = %s", journal.Phase)
	}
	if _, err := os.Lstat(fx.target.AMQRoot); !os.IsNotExist(err) {
		t.Fatalf("target published despite pre-admitted writer change: %v", err)
	}
	assertMigrationPayload(t, fx.source.Paths.Brief, "writer completed before reservation\n")
}

func TestNamespaceMigrationHoldsWriterAdmissionsThroughCommittedManifest(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false
	verified, releaseCommit := make(chan struct{}), make(chan struct{})
	original := namespaceMigrationFailpoint
	namespaceMigrationFailpoint = func(boundary string) error {
		if boundary == "verified" {
			close(verified)
			<-releaseCommit
		}
		return nil
	}
	t.Cleanup(func() { namespaceMigrationFailpoint = original })
	type outcome struct {
		result namespaceMigrationResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := executeNamespaceMigration(plan)
		done <- outcome{result: result, err: err}
	}()
	<-verified
	if lock, err := acquireNamespaceWriterAdmission(fx.project, fx.source.Profile, fx.source.Session); err == nil {
		lock.close()
		t.Fatal("writer crossed final verification before the committed manifest")
	} else if !strings.Contains(err.Error(), "migration owns") {
		t.Fatalf("writer admission refusal = %v", err)
	}
	close(releaseCommit)
	got := <-done
	if got.err != nil || got.result.Phase != migrationPhaseCommitted {
		t.Fatalf("execute result=%+v err=%v", got.result, got.err)
	}
	lock, err := acquireNamespaceWriterAdmission(fx.project, fx.source.Profile, fx.source.Session)
	if err != nil {
		t.Fatalf("writer admission failed after committed manifest: %v", err)
	}
	lock.close()
}

func TestNamespaceRecoveryHoldsWriterAdmissionsThroughCommittedManifest(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false
	original := namespaceMigrationFailpoint
	namespaceMigrationFailpoint = func(boundary string) error {
		if boundary == "targets_published" {
			return errors.New("crash before shared publication")
		}
		return nil
	}
	if _, err := executeNamespaceMigration(plan); err == nil || !strings.Contains(err.Error(), "crash before shared publication") {
		t.Fatalf("crash setup error = %v", err)
	}
	verified, releaseCommit := make(chan struct{}), make(chan struct{})
	namespaceMigrationFailpoint = func(boundary string) error {
		if boundary == "recovery_verified" {
			close(verified)
			<-releaseCommit
		}
		return nil
	}
	t.Cleanup(func() { namespaceMigrationFailpoint = original })
	type outcome struct {
		result namespaceMigrationResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := recoverNamespaceMigration(fx.project, plan.ID, false)
		done <- outcome{result: result, err: err}
	}()
	<-verified
	if lock, err := acquireNamespaceWriterAdmission(fx.project, fx.target.Profile, fx.target.Session); err == nil {
		lock.close()
		t.Fatal("writer crossed recovery verification before the committed manifest")
	} else if !strings.Contains(err.Error(), "migration owns") {
		t.Fatalf("writer admission refusal = %v", err)
	}
	close(releaseCommit)
	got := <-done
	if got.err != nil || got.result.Phase != migrationPhaseCommitted {
		t.Fatalf("recover result=%+v err=%v", got.result, got.err)
	}
	lock, err := acquireNamespaceWriterAdmission(fx.project, fx.target.Profile, fx.target.Session)
	if err != nil {
		t.Fatalf("writer admission failed after recovery committed manifest: %v", err)
	}
	lock.close()
}

func TestNamespaceMigrationLockRootSymlinkRefusedBeforeCreation(t *testing.T) {
	project, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, team.DirName), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, namespaceMigrationRoot(project)); err != nil {
		t.Fatal(err)
	}
	locks, err := acquireNamespaceMigrationLocks(project, team.DefaultProfile)
	if locks != nil {
		locks.close()
	}
	if err == nil {
		t.Fatalf("symlinked migration root was not refused: %v", err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("lock creation escaped through symlink: %v", entries)
	}
}

func TestNamespaceAdmissionContainedOpenRejectsAncestorSwapWithoutOutsideSideEffects(t *testing.T) {
	project := t.TempDir()
	controlDir := filepath.Join(project, team.DirName)
	if err := os.MkdirAll(controlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("unchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	originalHook := namespaceLockBeforeContainedCreate
	var once sync.Once
	namespaceLockBeforeContainedCreate = func(gotProject, _ string) error {
		var hookErr error
		once.Do(func() {
			if filepath.Clean(gotProject) != filepath.Clean(project) {
				hookErr = fmt.Errorf("unexpected project %s", gotProject)
				return
			}
			if err := os.Rename(controlDir, controlDir+".original"); err != nil {
				hookErr = err
				return
			}
			hookErr = os.Symlink(outside, controlDir)
		})
		return hookErr
	}
	t.Cleanup(func() { namespaceLockBeforeContainedCreate = originalHook })

	lock, err := acquireNamespaceWriterAdmission(project, team.DefaultProfile, "source-run")
	if lock != nil {
		lock.close()
	}
	if err == nil {
		t.Fatal("ancestor swap unexpectedly admitted writer")
	}
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !reflect.DeepEqual(names, []string{"sentinel"}) {
		t.Fatalf("contained lock creation changed outside tree: %v", names)
	}
	assertMigrationPayload(t, sentinel, "unchanged\n")
}

func TestNamespaceWriterResolvedBeforeMigrationCommitRefusesWithoutExternalAMQ(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false

	env := amqEnv{Root: fx.source.AMQRoot, BaseRoot: namespaceMigrationBaseRoot(fx.source), SessionName: fx.source.Session, Me: "qa"}
	calls := withAMQCommandSeams(t, env, "[]\n")
	resolved, release := make(chan struct{}), make(chan struct{})
	originalHook := namespaceWriterBeforeAdmission
	var once sync.Once
	namespaceWriterBeforeAdmission = func(project, profile, session string) error {
		if filepath.Clean(project) == filepath.Clean(fx.project) && squadnamespace.ProfilesEqual(profile, fx.source.Profile) && session == fx.source.Session {
			once.Do(func() {
				close(resolved)
				<-release
			})
		}
		return nil
	}
	t.Cleanup(func() { namespaceWriterBeforeAdmission = originalHook })

	writerDone := make(chan error, 1)
	go func() {
		writerDone <- runAMQPassthrough("drain", []string{
			"--project", fx.project, "--profile", fx.source.Profile,
			"--session", fx.source.Session, "--me", "qa",
		})
	}()
	<-resolved
	result, err := executeNamespaceMigration(plan)
	if err != nil || result.Phase != migrationPhaseCommitted {
		t.Fatalf("migration result=%+v err=%v", result, err)
	}
	close(release)
	if err := <-writerDone; err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("stale AMQ writer error = %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("stale AMQ writer executed external command: %+v", *calls)
	}
	if _, err := os.Lstat(fx.source.AMQRoot); !os.IsNotExist(err) {
		t.Fatalf("stale AMQ writer recreated source root: %v", err)
	}
}

func TestNamespaceWriterResolvedBeforeForwardRecoveryCommitRefusesWithoutSourceRecreation(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false
	originalFailpoint := namespaceMigrationFailpoint
	namespaceMigrationFailpoint = func(boundary string) error {
		if boundary == "targets_published" {
			return errors.New("crash before forward recovery")
		}
		return nil
	}
	if _, err := executeNamespaceMigration(plan); err == nil || !strings.Contains(err.Error(), "crash before forward recovery") {
		t.Fatalf("crash setup error = %v", err)
	}
	namespaceMigrationFailpoint = originalFailpoint
	t.Cleanup(func() { namespaceMigrationFailpoint = originalFailpoint })

	resolved, release := make(chan struct{}), make(chan struct{})
	originalHook := namespaceWriterBeforeAdmission
	var once sync.Once
	namespaceWriterBeforeAdmission = func(project, profile, session string) error {
		if filepath.Clean(project) == filepath.Clean(fx.project) && squadnamespace.ProfilesEqual(profile, fx.source.Profile) && session == fx.source.Session {
			once.Do(func() {
				close(resolved)
				<-release
			})
		}
		return nil
	}
	t.Cleanup(func() { namespaceWriterBeforeAdmission = originalHook })

	writerDone := make(chan error, 1)
	go func() {
		writerDone <- runTaskAdd([]string{
			"--project", fx.project, "--profile", fx.source.Profile,
			"--session", fx.source.Session, "--title", "must not be stranded",
		})
	}()
	<-resolved
	result, err := recoverNamespaceMigration(fx.project, plan.ID, false)
	if err != nil || result.Phase != migrationPhaseCommitted {
		t.Fatalf("recovery result=%+v err=%v", result, err)
	}
	close(release)
	if err := <-writerDone; err == nil || !strings.Contains(err.Error(), "refused") {
		t.Fatalf("stale task writer error = %v", err)
	}
	if _, err := os.Lstat(fx.source.Paths.Tasks); !os.IsNotExist(err) {
		t.Fatalf("stale task writer recreated source tasks: %v", err)
	}
}

func TestNamespaceWriterUnchangedContextProceedsAfterPreAdmissionPause(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	resolved, release := make(chan struct{}), make(chan struct{})
	originalHook := namespaceWriterBeforeAdmission
	var once sync.Once
	namespaceWriterBeforeAdmission = func(project, profile, session string) error {
		if filepath.Clean(project) == filepath.Clean(fx.project) && squadnamespace.ProfilesEqual(profile, fx.source.Profile) && session == fx.source.Session {
			once.Do(func() {
				close(resolved)
				<-release
			})
		}
		return nil
	}
	t.Cleanup(func() { namespaceWriterBeforeAdmission = originalHook })

	done := make(chan error, 1)
	go func() {
		done <- runTaskAdd([]string{
			"--project", fx.project, "--profile", fx.source.Profile,
			"--session", fx.source.Session, "--title", "unchanged context",
		})
	}()
	<-resolved
	close(release)
	if err := <-done; err != nil {
		t.Fatalf("unchanged namespace writer refused: %v", err)
	}
}

func TestNamespaceMigrationLockedReplanSeesPostReservationLockAndAborts(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false
	original := namespaceMigrationFailpoint
	var held *flock.Exclusive
	namespaceMigrationFailpoint = func(boundary string) error {
		if boundary != "reserved" {
			return nil
		}
		path := launch.RecordLockPath(filepath.Join(fx.source.AMQRoot, "agents", "qa"))
		lock, acquired, err := flock.TryExclusive(path, false)
		if err != nil {
			return err
		}
		if !acquired {
			return errors.New("test could not acquire post-reservation lock")
		}
		held = lock
		return nil
	}
	t.Cleanup(func() {
		namespaceMigrationFailpoint = original
		if held != nil {
			_ = held.Close()
		}
	})
	result, err := executeNamespaceMigration(plan)
	if held != nil {
		_ = held.Close()
		held = nil
	}
	if err == nil || !strings.Contains(err.Error(), "lock is held") {
		t.Fatalf("execute result=%+v err=%v", result, err)
	}
	journal, err := readNamespaceMigrationJournal(fx.project, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Phase != migrationPhaseAborted || !journal.Phase.terminal() || result.Status != string(migrationPhaseAborted) {
		t.Fatalf("harmless reserved refusal was not terminalized: result=%+v journal=%s", result, journal.Phase)
	}
	if err := ensureNoNamespaceMigration("after aborted reservation", fx.project, fx.source.Profile, fx.source.Session); err != nil {
		t.Fatalf("aborted reservation still freezes writers: %v", err)
	}
	assertMigrationPayload(t, filepath.Join(fx.source.AMQRoot, "agents", "qa", "inbox", "cur", "message.md"), fx.message)
}

func TestNamespaceMigrationSourceRecreationBeforeCommitForcesRecovery(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	plan.DryRun = false
	original := namespaceMigrationFailpoint
	namespaceMigrationFailpoint = func(boundary string) error {
		if boundary != "shared_state_published" {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(fx.source.Paths.Brief), 0o755); err != nil {
			return err
		}
		return os.WriteFile(fx.source.Paths.Brief, []byte("fresh stranded brief\n"), 0o644)
	}
	t.Cleanup(func() { namespaceMigrationFailpoint = original })
	result, err := executeNamespaceMigration(plan)
	if err == nil || !strings.Contains(err.Error(), "source path reappeared after backup") {
		t.Fatalf("execute result=%+v err=%v", result, err)
	}
	journal, err := readNamespaceMigrationJournal(fx.project, plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if journal.Phase != migrationPhaseRecoveryRequired {
		t.Fatalf("mixed transaction committed: phase=%s", journal.Phase)
	}
	assertMigrationPayload(t, fx.source.Paths.Brief, "fresh stranded brief\n")
	assertMigrationPayload(t, fx.target.Paths.Brief, fx.brief)
	_, recoverErr := recoverNamespaceMigration(fx.project, plan.ID, false)
	if recoverErr == nil || !strings.Contains(recoverErr.Error(), "manual recovery required") {
		t.Fatalf("source recreation recovery = %v", recoverErr)
	}
	assertMigrationPayload(t, fx.source.Paths.Brief, "fresh stranded brief\n")
}

func TestNamespaceMigrationExpiredWatcherLeaseWithLiveMatchingPIDBlocks(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	now := time.Now().UTC()
	host, _ := os.Hostname()
	rec := notificationWatcherRecord{
		SchemaVersion: notificationWatcherSchema, ProjectDir: fx.project, Profile: fx.source.Profile,
		Session: fx.source.Session, NamespaceID: fx.source.ID, PID: 4242, Host: host,
		OwnerToken: "watcher-token", Expected: true, Health: "healthy", LeaseExpiresAt: now.Add(-time.Minute),
	}
	if err := writeNotificationWatcherRecord(notificationWatcherRuntimePath(fx.project, fx.source.Profile, fx.source.Session), rec); err != nil {
		t.Fatal(err)
	}
	oldAlive, oldMatch := notificationWatcherPIDAlive, notificationWatcherProcessMatch
	notificationWatcherPIDAlive = func(pid int) bool { return pid == 4242 }
	notificationWatcherProcessMatch = func(pid int, _ func(string) bool) bool { return pid == 4242 }
	t.Cleanup(func() { notificationWatcherPIDAlive, notificationWatcherProcessMatch = oldAlive, oldMatch })
	plan, err := planNamespaceMigration(namespaceMigrationPlannerOptions{ProjectDir: fx.project, Source: fx.source, Target: fx.target, Now: now, Probe: livenessProbe(nil, nil, now)})
	if err != nil {
		t.Fatal(err)
	}
	if !migrationStringsContain(plan.Blockers, "notification watcher process is live for pid 4242") {
		t.Fatalf("expired but live watcher did not block: %v", plan.Blockers)
	}
}

func TestNamespaceMigrationNonterminalJournalFreezesBothEndpoints(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	root := filepath.Dir(namespaceMigrationJournalPath(fx.project, plan.ID))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := namespaceMigrationJournal{SchemaVersion: namespaceMigrationSchema, ID: plan.ID, ProjectDir: fx.project, Source: fx.source, Target: fx.target, Phase: migrationPhaseStaged, Plan: plan, Recovery: plan.Recovery}
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []squadnamespace.Ref{fx.source, fx.target} {
		err := ensureNoNamespaceMigration("task claim", fx.project, endpoint.Profile, endpoint.Session)
		if err == nil || !strings.Contains(err.Error(), plan.Recovery) {
			t.Fatalf("endpoint %s was not frozen with recovery: %v", endpoint.ID, err)
		}
	}
}

func TestNamespaceMigrationFreezesPreviouslyUnguardedWriters(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	root := filepath.Dir(namespaceMigrationJournalPath(fx.project, plan.ID))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := namespaceMigrationJournal{SchemaVersion: namespaceMigrationSchema, ID: plan.ID, ProjectDir: plan.ProjectDir, Source: fx.source, Target: fx.target, Phase: migrationPhaseStaged, Plan: plan, Recovery: plan.Recovery}
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		t.Fatal(err)
	}

	rec, err := launch.Read(filepath.Join(fx.source.AMQRoot, "agents", "qa"))
	if err != nil {
		t.Fatal(err)
	}
	rec.BootstrapExpectation = &bootstrapack.Expectation{Required: true, LaunchID: "launch-1", PromptVersion: bootstrapack.PromptVersion, IssuedAt: time.Now().UTC()}
	if err := launch.Write(filepath.Join(fx.source.AMQRoot, "agents", "qa"), rec); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", fx.source.AMQRoot)
	t.Setenv("AM_BASE_ROOT", namespaceMigrationBaseRoot(fx.source))
	t.Setenv("AM_SESSION", fx.source.Session)
	t.Setenv("AM_ME", "qa")

	seedPath := filepath.Join(fx.project, "seed.md")
	if err := os.WriteFile(seedPath, []byte("# seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tm, err := team.ReadProfile(fx.project, fx.source.Profile)
	if err != nil {
		t.Fatal(err)
	}
	ctx := amqContext{ProjectDir: fx.project, Profile: fx.source.Profile, Session: fx.source.Session, Root: fx.source.AMQRoot, Me: "qa"}

	writers := []struct {
		name string
		run  func() error
	}{
		{name: "amq read", run: func() error { return guardAMQPassthrough("read", ctx, nil, amqPassthroughOptions{}) }},
		{name: "collect", run: func() error {
			return runCollect([]string{"--project", fx.project, "--profile", fx.source.Profile, "--session", fx.source.Session, "--me", "qa"})
		}},
		{name: "bootstrap ack", run: func() error {
			return runBootstrapAck([]string{"--skill-version", "2.20.1", "--steps", "startup-files,initial-drain,context-review"})
		}},
		{name: "goal claim", run: func() error {
			return runGoalClaim([]string{"--project", fx.project, "--profile", fx.source.Profile, "--session", fx.source.Session, "--attempt-id", "attempt-1", "--route", "native"})
		}},
		{name: "goal retry-attempt", run: func() error {
			return runGoalRetryAttempt([]string{"--project", fx.project, "--profile", fx.source.Profile, "--session", fx.source.Session, "--role", "qa", "--attempt-id", "attempt-1", "--yes"})
		}},
		{name: "brief seed", run: func() error {
			return runBriefSeed([]string{"--project", fx.project, "--profile", fx.source.Profile, "--session", fx.source.Session, "--seed-from", "file:" + seedPath})
		}},
		{name: "brief decision", run: func() error {
			return runBriefDecision([]string{"--project", fx.project, "--profile", fx.source.Profile, "--session", fx.source.Session, "--body", "blocked"})
		}},
		{name: "lead register", run: func() error {
			return runLeadRegister([]string{"--project", fx.project, "--profile", fx.source.Profile, "--session", fx.source.Session, "--role", "qa", "--no-wake"})
		}},
		{name: "notify scoped", run: func() error {
			return runNotify([]string{"--project", fx.project, "--profile", fx.source.Profile, "--session", fx.source.Session})
		}},
		{name: "notify profile-wide", run: func() error {
			return runNotify([]string{"--project", fx.project, "--profile", fx.source.Profile})
		}},
		{name: "notification watcher start", run: func() error {
			return reconcileNotificationWatcherStarted(tm, fx.source.Profile, fx.source.Session, namespaceMigrationBaseRoot(fx.source))
		}},
		{name: "notification watcher stop", run: func() error {
			return stopNotificationWatcher(fx.project, fx.source.Profile, fx.source.Session)
		}},
	}
	for _, writer := range writers {
		t.Run(writer.name, func(t *testing.T) {
			err := writer.run()
			if err == nil || !strings.Contains(err.Error(), "namespace migration") || !strings.Contains(err.Error(), plan.ID) {
				t.Fatalf("writer was not frozen by %s: %v", plan.ID, err)
			}
		})
	}
}

func TestNamespaceMigrationFreezesDirectAgentUpBeforeLaunchMutation(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)
	plan := fx.plan(t)
	root := filepath.Dir(namespaceMigrationJournalPath(fx.project, plan.ID))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	journal := namespaceMigrationJournal{SchemaVersion: namespaceMigrationSchema, ID: plan.ID, ProjectDir: plan.ProjectDir, Source: fx.source, Target: fx.target, Phase: migrationPhaseStaged, Plan: plan, Recovery: plan.Recovery}
	if err := writeNamespaceMigrationJournal(&journal); err != nil {
		t.Fatal(err)
	}
	setupFakeAMQ(t)
	execCalled := false
	originalExec := amqSyscallExec
	amqSyscallExec = func(string, []string, []string) error {
		execCalled = true
		return errors.New("unexpected exec")
	}
	t.Cleanup(func() { amqSyscallExec = originalExec })
	err := runLaunch([]string{
		"--no-bootstrap", "--team-home", fx.project, "--team-profile", fx.source.Profile,
		"--session", fx.source.Session, "--me", "qa", "--role", "qa", "--force-duplicate", "codex",
	})
	if err == nil || !strings.Contains(err.Error(), "namespace migration") || !strings.Contains(err.Error(), plan.ID) {
		t.Fatalf("direct agent up was not frozen by %s: %v", plan.ID, err)
	}
	if execCalled {
		t.Fatal("direct agent up crossed the migration guard into external exec")
	}
}

func assertMigrationPayload(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("payload %s = %q, want %q", path, b, want)
	}
}

func migrationStringsContain(values []string, fragment string) bool {
	for _, value := range values {
		if strings.Contains(value, fragment) {
			return true
		}
	}
	return false
}
