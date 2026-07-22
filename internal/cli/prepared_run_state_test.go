package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

func nextPreparedRunManifestForTest(t *testing.T, manifest preparedRunManifest) preparedRunManifest {
	t.Helper()
	generation, err := newPreparedRunGeneration()
	if err != nil {
		t.Fatal(err)
	}
	manifest.Generation = generation
	manifest.PreparationRecord = preparedRunPreparationRecord{
		Generation: generation, Namespace: manifest.Namespace, LaunchShape: manifest.LaunchShape, Lead: manifest.Lead,
	}
	manifest.PreparedAt = manifest.PreparedAt.Add(time.Nanosecond)
	return manifest
}

func republishPreparedRunManifestForTest(t *testing.T, manifest preparedRunManifest) preparedRunManifest {
	t.Helper()
	manifest = nextPreparedRunManifestForTest(t, manifest)
	if err := writePreparedRunManifest(preparedRunPath(manifest.Project, manifest.Profile, manifest.Session), manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func preparedRunStateFixture(t *testing.T) (string, preparedRunManifest, preparedRunToken) {
	t.Helper()
	dir := prepareRunStartFixture(t, runwizard.LaunchShapeWorkingTeamTogether)
	manifest, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	return dir, manifest, preparedRunTokenFromSnapshot(manifest, digest)
}

func reservePreparedRunStateFixture(t *testing.T) (string, preparedRunManifest, preparedRunToken) {
	t.Helper()
	dir, manifest, token := preparedRunStateFixture(t)
	attempt, err := reservePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token)
	if err != nil {
		t.Fatal(err)
	}
	token.LaunchAttempt = attempt
	return dir, manifest, token
}

func reservedPreparedRunTokenForTest(t *testing.T, project, profile, session string) preparedRunToken {
	t.Helper()
	manifest, digest, err := readPreparedRunManifestSnapshot(project, profile, session)
	if err != nil {
		t.Fatal(err)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	attempt, err := reservePreparedRunLaunch(project, profile, session, token)
	if err != nil {
		t.Fatal(err)
	}
	token.LaunchAttempt = attempt
	return token
}

func preparedRunStagedStateFixture(t *testing.T) (string, preparedRunManifest, preparedRunToken) {
	t.Helper()
	dir := seedTeam(t, team.Team{
		Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: "prepared", CWD: ""},
			{Role: "qa", Handle: "qa", Binary: "claude", Session: "prepared", CWD: "", ToolProfile: team.ToolProfileFull},
		},
	})
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeLeadOnlyStaged, "--staged-roles", "qa",
			"--goal", "Execute the accepted staged fixture", "--visibility", "detached", "--prepare",
		}, "test")
	})
	if err != nil {
		t.Fatalf("prepare staged run fixture: %v", err)
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.StagedMembers["qa"].Binary != "claude" || manifest.BootstrapDigests["qa"] == "" || manifest.BootstrapBindings["qa"] == "" {
		t.Fatalf("staged immutable envelope is incomplete: identity=%+v digest=%q binding=%q", manifest.StagedMembers["qa"], manifest.BootstrapDigests["qa"], manifest.BootstrapBindings["qa"])
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	attempt, err := reservePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token)
	if err != nil {
		t.Fatal(err)
	}
	token.LaunchAttempt = attempt
	if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", token, "cto", "cto"); err != nil {
		t.Fatal(err)
	}
	if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "cto"); err != nil {
		t.Fatal(err)
	}
	if err := completePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token); err != nil {
		t.Fatal(err)
	}
	return dir, manifest, token.generationRef()
}

func rewritePreparedRunEventForTest(t *testing.T, path string, mutate func(*preparedRunEvent)) {
	t.Helper()
	event, err := readPreparedRunEvent(path)
	if err != nil {
		t.Fatal(err)
	}
	mutate(&event)
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedRunPathIDsFailClosedBeforeArtifacts(t *testing.T) {
	dir, _, token := preparedRunStateFixture(t)
	generationRoot := preparedRunGenerationsPath(dir, team.DefaultProfile, "prepared")
	before, err := os.ReadDir(generationRoot)
	if err != nil {
		t.Fatal(err)
	}

	for _, generation := range []string{"../escape", strings.Repeat("A", 32), strings.Repeat("a", 15) + "/" + strings.Repeat("b", 16)} {
		t.Run("generation_"+strings.ReplaceAll(generation, "/", "_"), func(t *testing.T) {
			forged := token
			forged.Generation = generation
			if _, err := reservePreparedRunLaunch(dir, team.DefaultProfile, "prepared", forged); err == nil || !strings.Contains(err.Error(), "canonical prepared-run id") {
				t.Fatalf("invalid generation error = %v", err)
			}
		})
	}
	after, err := os.ReadDir(generationRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("invalid generation created artifacts: before=%d after=%d", len(before), len(after))
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(generationRoot), "escape")); !os.IsNotExist(err) {
		t.Fatalf("traversal created an artifact outside generation root: %v", err)
	}

	forgedAttempt := token
	forgedAttempt.LaunchAttempt = "../resume"
	lockPath := preparedRunStateLockPath(dir, team.DefaultProfile, "prepared", token.Generation)
	if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", forgedAttempt, "cto", "cto"); err == nil || !strings.Contains(err.Error(), "canonical prepared-run id") {
		t.Fatalf("invalid launch attempt error = %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("invalid launch attempt created a generation lock: %v", err)
	}

	validAttempt, err := newPreparedRunGeneration()
	if err != nil {
		t.Fatal(err)
	}
	forgedAttempt.LaunchAttempt = validAttempt
	rec := launch.Record{Role: "cto", Handle: "cto"}
	applyPreparedRunTokenToRecord(&rec, forgedAttempt)
	desc := preparedRestoreDescriptor{
		Token: forgedAttempt, AttemptID: "../../outside", RecordDigest: preparedRestoreRecordDigest(rec), SemanticDigest: preparedRestoreSemanticDigest(rec),
	}
	if err := consumePreparedRunResume(dir, team.DefaultProfile, "prepared", forgedAttempt, rec, desc); err == nil || !strings.Contains(err.Error(), "canonical prepared-run id") {
		t.Fatalf("invalid resume attempt error = %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("invalid resume attempt created a generation lock: %v", err)
	}
}

func TestCompletePreparedRunLaunchRejectsTamperedEventIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		path   func(string, preparedRunToken) string
		mutate func(*preparedRunEvent)
	}{
		{name: "member token", path: func(dir string, token preparedRunToken) string {
			return preparedRunMemberEventPath(dir, team.DefaultProfile, "prepared", token.Generation, "cto")
		}, mutate: func(event *preparedRunEvent) { event.Token.GoalDigest = "sha256:tampered" }},
		{name: "member role", path: func(dir string, token preparedRunToken) string {
			return preparedRunMemberEventPath(dir, team.DefaultProfile, "prepared", token.Generation, "cto")
		}, mutate: func(event *preparedRunEvent) { event.Role = "runtime-dev" }},
		{name: "member handle", path: func(dir string, token preparedRunToken) string {
			return preparedRunMemberEventPath(dir, team.DefaultProfile, "prepared", token.Generation, "cto")
		}, mutate: func(event *preparedRunEvent) { event.Handle = "CTO" }},
		{name: "goal role", path: func(dir string, token preparedRunToken) string {
			return preparedRunGoalEventPath(dir, team.DefaultProfile, "prepared", token.Generation)
		}, mutate: func(event *preparedRunEvent) { event.Role = "runtime-dev" }},
		{name: "goal token", path: func(dir string, token preparedRunToken) string {
			return preparedRunGoalEventPath(dir, team.DefaultProfile, "prepared", token.Generation)
		}, mutate: func(event *preparedRunEvent) { event.Token.ManifestDigest = strings.Repeat("0", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, token := reservePreparedRunStateFixture(t)
			if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", token, "cto", "cto"); err != nil {
				t.Fatal(err)
			}
			if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "cto"); err != nil {
				t.Fatal(err)
			}
			rewritePreparedRunEventForTest(t, tc.path(dir, token), tc.mutate)
			if err := completePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token); err == nil || !strings.Contains(err.Error(), "no exact consumption evidence") {
				t.Fatalf("tampered event completion error = %v", err)
			}
			if _, err := os.Stat(preparedRunTerminalEventPath(dir, team.DefaultProfile, "prepared", token.Generation)); !os.IsNotExist(err) {
				t.Fatalf("tampered evidence created terminal event: %v", err)
			}
		})
	}
}

func TestPreparedRunResumeRejectsTamperedTerminalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*preparedRunEvent)
	}{
		{name: "token", mutate: func(event *preparedRunEvent) { event.Token.GoalNamespace = "tampered/namespace" }},
		{name: "launch attempt", mutate: func(event *preparedRunEvent) { event.LaunchAttempt = strings.Repeat("0", 32) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, token := reservePreparedRunStateFixture(t)
			if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", token, "cto", "cto"); err != nil {
				t.Fatal(err)
			}
			if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "cto"); err != nil {
				t.Fatal(err)
			}
			if err := completePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token); err != nil {
				t.Fatal(err)
			}
			rewritePreparedRunEventForTest(t, preparedRunTerminalEventPath(dir, team.DefaultProfile, "prepared", token.Generation), tc.mutate)

			rec := launch.Record{Role: "cto", Handle: "cto"}
			applyPreparedRunTokenToRecord(&rec, token)
			resumeAttempt, err := newPreparedRunGeneration()
			if err != nil {
				t.Fatal(err)
			}
			desc := preparedRestoreDescriptor{
				Token: token, AttemptID: resumeAttempt, RecordDigest: preparedRestoreRecordDigest(rec), SemanticDigest: preparedRestoreSemanticDigest(rec),
			}
			if err := consumePreparedRunResume(dir, team.DefaultProfile, "prepared", token, rec, desc); err == nil || !strings.Contains(err.Error(), "exact completed-launch evidence") {
				t.Fatalf("tampered terminal resume error = %v", err)
			}
			if _, err := os.Stat(preparedRunResumeEventPath(dir, team.DefaultProfile, "prepared", token.Generation, resumeAttempt)); !os.IsNotExist(err) {
				t.Fatalf("tampered terminal created resume event: %v", err)
			}
		})
	}
}

func TestPreparedRunStagedResumeRejectsTamperedConsumptionEvidence(t *testing.T) {
	dir, _, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, dir, token)
	claim, err := admitPreparedRunStagedClaim(dir, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err != nil {
		t.Fatal(err)
	}
	launchToken := token
	launchToken.LaunchAttempt = claim.ClaimID
	if err := consumePreparedRunStagedClaimLocked(dir, team.DefaultProfile, "prepared", launchToken, claim.Role, claim.Handle); err != nil {
		t.Fatal(err)
	}
	rewritePreparedRunEventForTest(t, preparedRunStagedConsumptionPath(dir, team.DefaultProfile, "prepared", token.Generation, claim.Role, claim.ClaimID), func(event *preparedRunEvent) {
		event.Detail = "tampered"
	})

	rec := launch.Record{Role: claim.Role, Handle: claim.Handle}
	applyPreparedRunTokenToRecord(&rec, launchToken)
	resumeAttempt, err := newPreparedRunGeneration()
	if err != nil {
		t.Fatal(err)
	}
	desc := preparedRestoreDescriptor{
		Token: launchToken, AttemptID: resumeAttempt, RecordDigest: preparedRestoreRecordDigest(rec), SemanticDigest: preparedRestoreSemanticDigest(rec),
	}
	if err := consumePreparedRunResume(dir, team.DefaultProfile, "prepared", launchToken, rec, desc); err == nil || !strings.Contains(err.Error(), "exact staged-spawn evidence") {
		t.Fatalf("tampered staged consumption resume error = %v", err)
	}
	if _, err := os.Stat(preparedRunResumeEventPath(dir, team.DefaultProfile, "prepared", token.Generation, resumeAttempt)); !os.IsNotExist(err) {
		t.Fatalf("tampered staged evidence created resume event: %v", err)
	}
}

func TestPreparedRunPublicationCrashKeepsAcceptedPointerAtomic(t *testing.T) {
	for _, stage := range []string{"manifest", "initial_state"} {
		t.Run(stage, func(t *testing.T) {
			dir, accepted, acceptedToken := preparedRunStateFixture(t)
			candidate := nextPreparedRunManifestForTest(t, accepted)
			oldHook := preparedRunPublishAfterArtifact
			preparedRunPublishAfterArtifact = func(got string) error {
				if got == stage {
					return errors.New("injected publication crash")
				}
				return nil
			}
			t.Cleanup(func() { preparedRunPublishAfterArtifact = oldHook })

			err := writePreparedRunManifest(preparedRunPath(dir, team.DefaultProfile, "prepared"), candidate)
			if err == nil || !strings.Contains(err.Error(), "injected publication crash") {
				t.Fatalf("publication crash error = %v", err)
			}
			current, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "prepared")
			if err != nil {
				t.Fatal(err)
			}
			if current.Generation != accepted.Generation || !samePreparedRunGeneration(acceptedToken, preparedRunTokenFromSnapshot(current, digest)) {
				t.Fatalf("partial publication advanced accepted pointer: accepted=%s current=%s", accepted.Generation, current.Generation)
			}
			if _, err := os.Stat(preparedRunGenerationManifestPath(dir, team.DefaultProfile, "prepared", candidate.Generation)); err != nil {
				t.Fatalf("orphan generation manifest is not inspectable: %v", err)
			}
			_, initialErr := os.Stat(preparedRunInitialStatePath(dir, team.DefaultProfile, "prepared", candidate.Generation))
			if stage == "manifest" && !os.IsNotExist(initialErr) {
				t.Fatalf("manifest-stage crash unexpectedly published initial state: %v", initialErr)
			}
			if stage == "initial_state" && initialErr != nil {
				t.Fatalf("initial-state-stage orphan is not inspectable: %v", initialErr)
			}
		})
	}
}

func TestPreparedRunNewGenerationPreservesOldArtifactsAndRejectsStaleToken(t *testing.T) {
	dir, accepted, oldToken := preparedRunStateFixture(t)
	oldManifestPath := preparedRunGenerationManifestPath(dir, team.DefaultProfile, "prepared", accepted.Generation)
	oldData, err := os.ReadFile(oldManifestPath)
	if err != nil {
		t.Fatal(err)
	}

	candidate := nextPreparedRunManifestForTest(t, accepted)
	if err := writePreparedRunManifest(preparedRunPath(dir, team.DefaultProfile, "prepared"), candidate); err != nil {
		t.Fatal(err)
	}
	if current, _, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "prepared"); err != nil || current.Generation != candidate.Generation {
		t.Fatalf("current generation = %s err=%v want=%s", current.Generation, err, candidate.Generation)
	}
	if got, err := os.ReadFile(oldManifestPath); err != nil || string(got) != string(oldData) {
		t.Fatalf("old generation artifact changed: err=%v", err)
	}
	if _, err := reservePreparedRunLaunch(dir, team.DefaultProfile, "prepared", oldToken); err == nil || !strings.Contains(err.Error(), "no longer the current accepted generation") {
		t.Fatalf("stale generation reservation error = %v", err)
	}
}

func TestPreparedRunReservationAndClaimsAreClaimOnce(t *testing.T) {
	dir, _, token := preparedRunStateFixture(t)
	const callers = 8
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := reservePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("concurrent reservation successes=%d want=1", succeeded)
	}

	reservation, err := readPreparedRunEvent(preparedRunReservationPath(dir, team.DefaultProfile, "prepared", token.Generation))
	if err != nil {
		t.Fatal(err)
	}
	token.LaunchAttempt = reservation.LaunchAttempt
	if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", token, "cto", "cto"); err != nil {
		t.Fatal(err)
	}
	if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", token, "cto", "cto"); err == nil || !strings.Contains(err.Error(), "replay refused") {
		t.Fatalf("member replay error = %v", err)
	}
	if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "cto"); err != nil {
		t.Fatal(err)
	}
	if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "cto"); err == nil || !strings.Contains(err.Error(), "replay refused") {
		t.Fatalf("goal replay error = %v", err)
	}
}

func TestPreparedRunResumeAttemptIsSingleUseButFreshAttemptIsAllowed(t *testing.T) {
	dir, _, token := reservePreparedRunStateFixture(t)
	if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", token, "cto", "cto"); err != nil {
		t.Fatal(err)
	}
	if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "cto"); err != nil {
		t.Fatal(err)
	}
	if err := completePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token); err != nil {
		t.Fatal(err)
	}

	persisted := launch.Record{Role: "cto", Handle: "cto", AgentPID: 41, AgentTTY: "persisted", StartedAt: time.Unix(1, 0).UTC()}
	applyPreparedRunTokenToRecord(&persisted, token)
	candidate := persisted
	candidate.AgentPID = 42
	candidate.AgentTTY = "reconstructed"
	candidate.StartedAt = time.Unix(2, 0).UTC()
	if preparedRestoreRecordDigest(candidate) == preparedRestoreRecordDigest(persisted) || preparedRestoreSemanticDigest(candidate) != preparedRestoreSemanticDigest(persisted) {
		t.Fatalf("fixture does not isolate volatile reconstruction: persisted=%+v candidate=%+v", persisted, candidate)
	}
	newDescriptor := func() preparedRestoreDescriptor {
		attempt, err := newPreparedRunGeneration()
		if err != nil {
			t.Fatal(err)
		}
		return preparedRestoreDescriptor{Token: token, AttemptID: attempt, RecordDigest: preparedRestoreRecordDigest(persisted), SemanticDigest: preparedRestoreSemanticDigest(persisted)}
	}
	first := newDescriptor()
	if err := consumePreparedRunResume(dir, team.DefaultProfile, "prepared", token, candidate, first); err != nil {
		t.Fatal(err)
	}
	if err := consumePreparedRunResume(dir, team.DefaultProfile, "prepared", token, candidate, first); err == nil || !strings.Contains(err.Error(), "replay refused") {
		t.Fatalf("resume replay error = %v", err)
	}
	second := newDescriptor()
	if err := consumePreparedRunResume(dir, team.DefaultProfile, "prepared", token, candidate, second); err != nil {
		t.Fatalf("fresh resume attempt failed: %v", err)
	}
}

func TestPreparedResumeRecordCASPrecedesGenerationClaim(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*testing.T, string, launch.Record) *launch.Record
		needle  string
	}{
		{name: "missing", prepare: func(t *testing.T, agentDir string, _ launch.Record) *launch.Record {
			return nil
		}, needle: "persisted launch record is missing"},
		{name: "replaced", prepare: func(t *testing.T, agentDir string, persisted launch.Record) *launch.Record {
			replaced := persisted
			replaced.AgentPID++
			if err := launch.Write(agentDir, replaced); err != nil {
				t.Fatal(err)
			}
			return &replaced
		}, needle: "persisted launch record changed before CAS"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, _, token := reservePreparedRunStateFixture(t)
			if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", token, "cto", "cto"); err != nil {
				t.Fatal(err)
			}
			if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "cto"); err != nil {
				t.Fatal(err)
			}
			if err := completePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token); err != nil {
				t.Fatal(err)
			}

			agentDir := t.TempDir()
			persisted := launch.Record{CWD: dir, Role: "cto", Handle: "cto", AgentPID: 41, StartedAt: time.Unix(1, 0).UTC()}
			applyPreparedRunTokenToRecord(&persisted, token)
			desc := preparedRestoreDescriptor{Token: token, AttemptID: strings.Repeat("e", 32), RecordDigest: preparedRestoreRecordDigest(persisted), SemanticDigest: preparedRestoreSemanticDigest(persisted)}
			candidate := persisted
			candidate.AgentPID = 42
			candidate.StartedAt = time.Unix(2, 0).UTC()
			wantStored := tc.prepare(t, agentDir, persisted)
			if wantStored != nil {
				normalized, err := launch.Read(agentDir)
				if err != nil {
					t.Fatal(err)
				}
				wantStored = &normalized
			}
			claimCalls := 0
			done := make(chan error, 1)
			go func() {
				_, err := writeLaunchRecordWithSnapshot(agentDir, candidate, desc.RecordDigest, func() error {
					claimCalls++
					return consumePreparedRunResume(dir, team.DefaultProfile, "prepared", token, candidate, desc)
				})
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), tc.needle) {
					t.Fatalf("record CAS error=%v want %q", err, tc.needle)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("record-to-generation lock order deadlocked")
			}
			if claimCalls != 0 {
				t.Fatalf("raw %s record created %d generation claim(s)", tc.name, claimCalls)
			}
			if _, err := os.Stat(preparedRunResumeEventPath(dir, team.DefaultProfile, "prepared", token.Generation, desc.AttemptID)); !os.IsNotExist(err) {
				t.Fatalf("raw %s record created resume event: %v", tc.name, err)
			}
			if wantStored != nil {
				stored, err := launch.Read(agentDir)
				if err != nil || preparedRestoreRecordDigest(stored) != preparedRestoreRecordDigest(*wantStored) {
					t.Fatalf("raw replacement changed: stored=%+v err=%v", stored, err)
				}
			}
		})
	}
}

func TestPreparedRunResumeSemanticDriftRejectsBeforeClaim(t *testing.T) {
	dir, _, token := reservePreparedRunStateFixture(t)
	if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", token, "cto", "cto"); err != nil {
		t.Fatal(err)
	}
	if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "cto"); err != nil {
		t.Fatal(err)
	}
	if err := completePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token); err != nil {
		t.Fatal(err)
	}
	persisted := launch.Record{Role: "cto", Handle: "cto"}
	applyPreparedRunTokenToRecord(&persisted, token)
	desc := preparedRestoreDescriptor{Token: token, AttemptID: strings.Repeat("f", 32), RecordDigest: preparedRestoreRecordDigest(persisted), SemanticDigest: preparedRestoreSemanticDigest(persisted)}
	candidate := persisted
	candidate.GoalBinding = &launch.GoalBinding{Goal: "semantic drift"}
	if err := consumePreparedRunResume(dir, team.DefaultProfile, "prepared", token, candidate, desc); err == nil || !strings.Contains(err.Error(), "descriptor changed") {
		t.Fatalf("semantic drift error=%v", err)
	}
	if _, err := os.Stat(preparedRunResumeEventPath(dir, team.DefaultProfile, "prepared", token.Generation, desc.AttemptID)); !os.IsNotExist(err) {
		t.Fatalf("semantic drift created resume event: %v", err)
	}
}

func TestPreparedRunGenerationClaimsRejectRoleAndHandleCaseDrift(t *testing.T) {
	t.Run("member-handle", func(t *testing.T) {
		dir, _, token := reservePreparedRunStateFixture(t)
		if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", token, "cto", "CTO"); err == nil || !strings.Contains(err.Error(), "no exact initial actor identity") {
			t.Fatalf("case-drifted member handle error=%v", err)
		}
		if _, err := os.Stat(preparedRunMemberEventPath(dir, team.DefaultProfile, "prepared", token.Generation, "cto")); !os.IsNotExist(err) {
			t.Fatalf("case-drifted member handle created claim: %v", err)
		}
	})
	t.Run("goal-role", func(t *testing.T) {
		dir, _, token := reservePreparedRunStateFixture(t)
		if err := consumePreparedRunGoal(dir, team.DefaultProfile, "prepared", token, "CTO"); err == nil || !strings.Contains(err.Error(), "accepted goal belongs") {
			t.Fatalf("case-drifted goal role error=%v", err)
		}
		if _, err := os.Stat(preparedRunGoalEventPath(dir, team.DefaultProfile, "prepared", token.Generation)); !os.IsNotExist(err) {
			t.Fatalf("case-drifted goal role created claim: %v", err)
		}
	})
}

// Staged consumption has a single system of record: the immutable claim
// system in prepared_run_staged_claim.go (admit -> active.json pointer ->
// consume). consumePreparedRunMember's staged branch is a second entry point
// into that same system (any caller that reaches it with token.LaunchAttempt
// bound to an admitted claim ID, not only the parent-owned staged launch
// transaction in team_member_staged_launch.go) and must remain
// resume-recognizable via validateConsumedPreparedRunStagedClaim. See #508
// review finding B2.
func TestPreparedRunMemberConsumesStagedClaimAndStaysResumeRecognizable(t *testing.T) {
	dir, manifest, generation := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, dir, generation)
	claim, err := admitPreparedRunStagedClaim(dir, team.DefaultProfile, "prepared", generation, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err != nil {
		t.Fatal(err)
	}
	staged := generation
	staged.LaunchAttempt = claim.ClaimID

	// This is the staged-token-without---staged-spawn branch: a caller that
	// reaches consumePreparedRunMember directly (token bound to an admitted
	// claim, not routed through the parent staged-launch transaction).
	if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", staged, "qa", "qa"); err != nil {
		t.Fatal(err)
	}
	if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", staged, "qa", "qa"); err == nil || !strings.Contains(err.Error(), "replay refused") {
		t.Fatalf("staged claim replay error = %v", err)
	}
	rec := launch.Record{Role: "qa", Handle: "qa"}
	applyPreparedRunTokenToRecord(&rec, staged)
	if rec.PreparedRunGeneration != manifest.Generation || rec.PreparedRunLaunchAttempt != claim.ClaimID {
		t.Fatalf("staged record lost original generation/attempt: %+v", rec)
	}

	// The consumption must be recognizable by resume through the same claim
	// system it was written to - not the retired legacy single-file path.
	if err := validateConsumedPreparedRunStagedClaim(dir, team.DefaultProfile, "prepared", staged, "qa", "qa"); err != nil {
		t.Fatalf("resume did not recognize staged consumption: %v", err)
	}
	if _, err := os.Stat(preparedRunStagedClaimPath(dir, team.DefaultProfile, "prepared", generation.Generation, "qa")); !os.IsNotExist(err) {
		t.Fatalf("retired legacy staged claim path must never be (re)created: %v", err)
	}
}

func TestPreparedRunMemberRejectsUnadmittedOrStaleStagedClaim(t *testing.T) {
	t.Run("never admitted", func(t *testing.T) {
		dir, _, generation := preparedRunStagedStateFixture(t)
		ungated := generation
		ungated.LaunchAttempt = strings.Repeat("a", 32)
		if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", ungated, "qa", "qa"); err == nil {
			t.Fatalf("unadmitted staged claim unexpectedly succeeded")
		}
		if _, err := os.Stat(preparedRunStagedClaimPath(dir, team.DefaultProfile, "prepared", generation.Generation, "qa")); !os.IsNotExist(err) {
			t.Fatalf("unadmitted staged claim created legacy evidence: %v", err)
		}
	})

	t.Run("superseded", func(t *testing.T) {
		dir, _, generation := preparedRunStagedStateFixture(t)
		seedPreparedStagedAuthorizer(t, dir, generation)
		first, err := admitPreparedRunStagedClaim(dir, team.DefaultProfile, "prepared", generation, preparedRunStagedAdmissionRequest{
			Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := admitPreparedRunStagedClaim(dir, team.DefaultProfile, "prepared", generation, preparedRunStagedAdmissionRequest{
			Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
			SupersedesClaimID: first.ClaimID,
		}); err != nil {
			t.Fatal(err)
		}
		stale := generation
		stale.LaunchAttempt = first.ClaimID
		if err := consumePreparedRunMember(dir, team.DefaultProfile, "prepared", stale, "qa", "qa"); err == nil || !strings.Contains(err.Error(), "stale, inactive, or belongs to different launch evidence") {
			t.Fatalf("superseded staged claim error = %v", err)
		}
	})
}

func TestPreparedRunPreparationRejectsRoleOnlyStagedIdentity(t *testing.T) {
	dir := t.TempDir()
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--session", "prepared", "--roles", "cto", "--binary", "cto=codex", "--lead", "cto",
			"--launch-shape", runwizard.LaunchShapeLeadOnlyStaged, "--staged-roles", "qa",
			"--goal", "Reject incomplete staged identity", "--visibility", "detached", "--prepare",
		}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "no complete profile member definition") {
		t.Fatalf("role-only staged preparation error = %v", err)
	}
}

func TestAgentUpStagedSpawnProductPathBindsActiveClaimWithoutConsuming(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir, manifest, token := preparedRunStagedStateFixture(t)
	seedPreparedStagedAuthorizer(t, dir, token)
	claim, err := admitPreparedRunStagedClaim(dir, team.DefaultProfile, "prepared", token, preparedRunStagedAdmissionRequest{
		Role: "qa", Handle: "qa", AuthorizingRole: "cto", AuthorizingHandle: "cto", ActorMode: team.ActorModeReview,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(envTmuxTarget, "new-window")
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%43")
	oldPane := launchCurrentPaneIdentity
	launchCurrentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "managed", WindowID: "@2", WindowName: "qa", PaneID: "%43"}, nil
	}
	t.Cleanup(func() { launchCurrentPaneIdentity = oldPane })
	oldTerminal := launchStdinIsTerminal
	launchStdinIsTerminal = func() bool { return true }
	t.Cleanup(func() { launchStdinIsTerminal = oldTerminal })
	execCalls := 0
	oldExec := amqSyscallExec
	amqSyscallExec = func(string, []string, []string) error { execCalls++; return nil }
	t.Cleanup(func() { amqSyscallExec = oldExec })
	args := []string{
		"--project", dir, "--team-home", dir, "--team-profile", team.DefaultProfile,
		"--role", "qa", "--me", "qa", "--session", "prepared", "--trust", trustModeApproveForMe, "claude",
	}
	if _, _, err := captureOutput(t, func() error { return runLaunch(args) }); err == nil || !strings.Contains(err.Error(), "requires an exact single-use spawn reservation") {
		t.Fatalf("ungated direct staged agent up error = %v", err)
	}
	if execCalls != 0 {
		t.Fatalf("ungated direct staged agent up executed %d time(s)", execCalls)
	}

	stagedArgs := append([]string{"--staged-spawn", "--staged-claim", claim.ClaimID}, args...)
	if _, _, err := captureOutput(t, func() error { return runLaunch(stagedArgs) }); err != nil {
		t.Fatalf("staged-spawn agent up: %v", err)
	}
	if execCalls != 1 {
		t.Fatalf("staged-spawn exec calls=%d want=1", execCalls)
	}
	env, err := resolveAMQEnvForTeamLaunchProfile(dir, team.DefaultProfile, "prepared", "qa")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := launch.Read(filepath.Join(env.Root, "agents", "qa"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.PreparedRunGeneration != manifest.Generation || rec.PreparedRunLaunchAttempt != claim.ClaimID {
		t.Fatalf("staged launch record lost generation/attempt: %+v", rec)
	}
	pointer, err := readPreparedRunStagedClaimPointer(preparedRunStagedClaimActivePath(dir, team.DefaultProfile, "prepared", manifest.Generation, "qa"))
	if err != nil || pointer.ClaimID != claim.ClaimID || pointer.LifecycleState != stagedClaimStateAdmitted || pointer.Consumption != nil {
		t.Fatalf("staged pointer=%+v err=%v record_attempt=%s", pointer, err, rec.PreparedRunLaunchAttempt)
	}
	if _, err := os.Stat(preparedRunStagedClaimPath(dir, team.DefaultProfile, "prepared", manifest.Generation, "qa")); !os.IsNotExist(err) {
		t.Fatalf("legacy staged event must not be recreated or consumed by child launch: %v", err)
	}
}

func TestDurableCreateExclusivePublishesCompleteNoReplaceArtifacts(t *testing.T) {
	t.Run("failure before install recovers without authoritative file", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "generation", "manifest.json")
		oldHook := preparedRunBeforeExclusiveInstall
		calls := 0
		preparedRunBeforeExclusiveInstall = func(got string) error {
			if got != path {
				t.Fatalf("install hook path=%q want=%q", got, path)
			}
			calls++
			if calls == 1 {
				return errors.New("injected crash before exclusive install")
			}
			return nil
		}
		t.Cleanup(func() { preparedRunBeforeExclusiveInstall = oldHook })
		want := []byte(strings.Repeat("immutable-manifest\n", 128))
		if err := durableCreateExclusive(path, want); err == nil || !strings.Contains(err.Error(), "injected crash") {
			t.Fatalf("injected install error = %v", err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("pre-install failure exposed authoritative path: %v", err)
		}
		if err := durableCreateExclusive(path, want); err != nil {
			t.Fatalf("retry after pre-install failure: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(want) {
			t.Fatalf("recovered artifact bytes=%d err=%v want=%d", len(got), err, len(want))
		}
	})

	t.Run("valid final is never replaced", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "events", "terminal.json")
		first := []byte("complete-first-event\n")
		if err := durableCreateExclusive(path, first); err != nil {
			t.Fatal(err)
		}
		if err := durableCreateExclusive(path, []byte("different-second-event\n")); err == nil || !errors.Is(err, os.ErrExist) {
			t.Fatalf("duplicate exclusive create error = %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil || string(got) != string(first) {
			t.Fatalf("valid final changed: %q err=%v", got, err)
		}
	})

	t.Run("manifest and event failures expose no truncated final", func(t *testing.T) {
		dir := t.TempDir()
		paths := []string{
			filepath.Join(dir, "generations", strings.Repeat("a", 32), "manifest.json"),
			filepath.Join(dir, "generations", strings.Repeat("a", 32), "events", "members", "cto.json"),
		}
		oldHook := preparedRunBeforeExclusiveInstall
		preparedRunBeforeExclusiveInstall = func(string) error { return errors.New("stop before install") }
		t.Cleanup(func() { preparedRunBeforeExclusiveInstall = oldHook })
		for _, path := range paths {
			if err := durableCreateExclusive(path, []byte(strings.Repeat("complete-bytes", 64))); err == nil {
				t.Fatalf("path %s unexpectedly installed", path)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("path %s became authoritative after pre-install failure: %v", path, err)
			}
		}
	})

	t.Run("race has one complete winner", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "events", "claim.json")
		const callers = 12
		type result struct {
			payload []byte
			err     error
		}
		results := make(chan result, callers)
		var wg sync.WaitGroup
		for i := range callers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				payload := []byte(strings.Repeat(string(rune('a'+i)), 4096))
				results <- result{payload: payload, err: durableCreateExclusive(path, payload)}
			}()
		}
		wg.Wait()
		close(results)
		var winner []byte
		successes := 0
		for result := range results {
			if result.err == nil {
				successes++
				winner = result.payload
			} else if !errors.Is(result.err, os.ErrExist) {
				t.Fatalf("race loser error = %v", result.err)
			}
		}
		got, err := os.ReadFile(path)
		if err != nil || successes != 1 || string(got) != string(winner) || len(got) != 4096 {
			t.Fatalf("exclusive race successes=%d bytes=%d err=%v", successes, len(got), err)
		}
	})
}
