package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

func writeNativeResumeMutexReservation(
	t *testing.T,
	tm team.Team,
	session string,
	plan runwizard.ResumeGoalPlan,
	pause string,
) (string, string) {
	t.Helper()
	return writeNativeResumeMutexReservationForAttempt(
		t,
		tm,
		session,
		plan,
		pause,
		plan.OriginalAttemptID,
	)
}

func writeNativeResumeMutexReservationForAttempt(
	t *testing.T,
	tm team.Team,
	session string,
	plan runwizard.ResumeGoalPlan,
	pause string,
	attemptID string,
) (string, string) {
	t.Helper()
	in := completeNativeTransitionInput(
		tm.Project,
		team.DefaultProfile,
		session,
		attemptID,
	)
	in.Base.Role = plan.LeadRole
	in.Base.Handle = plan.LeadHandle
	in.PauseGeneration = pause
	record, path, err := newRecoveryTransitionRecord(in)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, path, record)
	return path, record.TransitionID
}

func resumeGoalMutexOptions(
	tm team.Team,
	session string,
	plan runwizard.ResumeGoalPlan,
) goalDeliveryOptions {
	return goalDeliveryOptions{
		Project:            tm.Project,
		Profile:            team.DefaultProfile,
		Session:            session,
		Role:               plan.LeadRole,
		Goal:               plan.Goal,
		Team:               tm,
		Member:             tm.Members[0],
		Namespace:          squadnamespace.Resolve(tm.Project, team.DefaultProfile, session),
		Mode:               executionModeProjectLead,
		ResumeTransitionID: plan.TransitionID,
	}
}

// The old reader filtered only ".resume-redelivery-" and skipped consumed
// records. Either mutation makes this current native consumed reservation
// invisible and turns the expected refusal into nil.
func TestGoalDeliveryDiscoveryUsesTheSharedParserForCurrentConsumedTransitions(t *testing.T) {
	tm, session, _, plan, _ := freshResumeTransitionFixture(t)
	path, key := writeNativeResumeMutexReservation(t, tm, session, plan, "pause-current-consumed")
	writeTestJSON(t, resumeGoalTransitionConsumedPath(path), map[string]string{
		"transition_id": key,
	})

	opts := resumeGoalMutexOptions(tm, session, plan)
	opts.ResumeTransitionID = ""
	opts.AttemptID = plan.OriginalAttemptID
	_, err := validateResumeGoalTransitionForDelivery(opts, memberRuntime{})
	if err == nil || !strings.Contains(err.Error(), "CONSUMED") ||
		!strings.Contains(err.Error(), filepath.Base(resumeGoalTransitionConsumedPath(path))) {
		t.Fatalf("current consumed native transition was not discovered through the shared parser: %v", err)
	}
}

// Unconsumed recovery evidence blocks directory-wide because it may still
// authorize delivery. Once consumed, the same durable record is history and
// blocks only the exact legacy attempt+binding identity.
func TestRedeliveryScanUsesExactLegacyMatching(t *testing.T) {
	tm, session, _, plan, _ := freshResumeTransitionFixture(t)
	dir := goalAttemptDir(tm.Project, team.DefaultProfile, session)
	otherID := strings.Repeat("f", 64)
	otherPath, err := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, otherID)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, otherPath, resumeGoalTransitionRecord{
		SchemaVersion: resumeGoalTransitionSchemaVersion,
		TransitionID:  otherID,
		RecoveryKind:  string(recoveryTransitionKindRedeliver),
	})

	blocker, err := scanResumeGoalRecoveryTransitions(dir, resumeGoalRecoveryScanOptions{
		LegacyKey:       plan.TransitionID,
		TargetNamespace: squadnamespace.ID(team.DefaultProfile, session),
		TargetAttemptID: plan.OriginalAttemptID,
	})
	if err != nil || blocker == nil || blocker.Path != otherPath {
		t.Fatalf("different unconsumed legacy evidence did not block directory-wide: blocker=%+v err=%v", blocker, err)
	}
	writeTestJSON(t, resumeGoalTransitionConsumedPath(otherPath), map[string]string{
		"transition_id": otherID,
	})
	blocker, err = scanResumeGoalRecoveryTransitions(dir, resumeGoalRecoveryScanOptions{
		LegacyKey:       plan.TransitionID,
		TargetNamespace: squadnamespace.ID(team.DefaultProfile, session),
		TargetAttemptID: plan.OriginalAttemptID,
	})
	if err != nil || blocker != nil {
		t.Fatalf("different consumed legacy key poisoned this delivery: blocker=%+v err=%v", blocker, err)
	}

	ownPath, err := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, plan.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, ownPath, resumeGoalTransitionRecord{
		SchemaVersion: resumeGoalTransitionSchemaVersion,
		TransitionID:  plan.TransitionID,
		RecoveryKind:  string(recoveryTransitionKindRedeliver),
	})
	writeTestJSON(t, resumeGoalTransitionConsumedPath(ownPath), map[string]string{
		"transition_id": plan.TransitionID,
	})
	blocker, err = scanResumeGoalRecoveryTransitions(dir, resumeGoalRecoveryScanOptions{
		LegacyKey:       plan.TransitionID,
		TargetNamespace: squadnamespace.ID(team.DefaultProfile, session),
		TargetAttemptID: plan.OriginalAttemptID,
	})
	if err != nil || blocker == nil || blocker.Path != resumeGoalTransitionConsumedPath(ownPath) {
		t.Fatalf("matching consumed legacy key did not block: blocker=%+v err=%v", blocker, err)
	}
}

func TestRedeliveryConsumedNativeUsesRecomputedDeliveryIdentity(t *testing.T) {
	tm, session, _, plan, _ := freshResumeTransitionFixture(t)
	dir := goalAttemptDir(tm.Project, team.DefaultProfile, session)
	otherAttempt := "different-native-attempt"
	path, key := writeNativeResumeMutexReservationForAttempt(
		t,
		tm,
		session,
		plan,
		"pause-native-consumed-other",
		otherAttempt,
	)
	writeTestJSON(t, resumeGoalTransitionConsumedPath(path), map[string]string{
		"transition_id": key,
	})

	blocker, err := scanResumeGoalRecoveryTransitions(dir, resumeGoalRecoveryScanOptions{
		LegacyKey:       plan.TransitionID,
		TargetNamespace: squadnamespace.ID(team.DefaultProfile, session),
		TargetAttemptID: plan.OriginalAttemptID,
	})
	if err != nil || blocker != nil {
		t.Fatalf("different consumed native attempt poisoned this delivery: blocker=%+v err=%v", blocker, err)
	}
}

// The consumed filename and TransitionID still describe the original claim,
// but the body has been damaged after publication. Without read-side contract
// validation, the scanner uses the tampered nonblank PauseGeneration to derive
// a different key, ignores the prior-consumption evidence, and this production
// redelivery reaches one pane send.
func TestRedeliveryConsumedNativePauseGenerationTamperBlocksProductionDelivery(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixture(t)
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatal(err)
	}
	path, key := writeNativeResumeMutexReservation(
		t,
		tm,
		session,
		plan,
		"pause-native-consumed-original",
	)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record resumeGoalTransitionRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatal(err)
	}
	if err := validateRecoveryTransitionRecordContract(record); err != nil {
		t.Fatalf("native reservation was not valid before the one-field tamper: %v", err)
	}
	record.PauseGeneration = "pause-native-consumed-tampered"
	writeTestJSON(t, path, record)
	writeTestJSON(t, resumeGoalTransitionConsumedPath(path), map[string]string{
		"transition_id": key,
	})

	oldLister, oldSend := statusPaneLister, sendPromptToPane
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
		return []tmuxpane.TmuxPane{{
			PaneID:  "%447",
			CWD:     tm.Project,
			Command: tm.Members[0].Binary,
			Title:   paneTitleToken(session, plan.LeadRole),
		}}, nil
	}
	sends := 0
	sendPromptToPane = func(_ string, _ string) error {
		sends++
		return nil
	}
	t.Cleanup(func() {
		statusPaneLister, sendPromptToPane = oldLister, oldSend
	})

	_, err = executeGoalDelivery(resumeGoalMutexOptions(tm, session, plan))
	if err == nil || sends != 0 ||
		!strings.Contains(err.Error(), "cross-kind claim-once mutex") ||
		!strings.Contains(err.Error(), "not the claim key") {
		t.Fatalf("damaged consumed native identity did not fail closed before pane input: err=%v sends=%d", err, sends)
	}
}

func TestRedeliveryConsumedNativeWithBlankPauseGenerationBlocks(t *testing.T) {
	tm, session, _, plan, _ := freshResumeTransitionFixture(t)
	dir := goalAttemptDir(tm.Project, team.DefaultProfile, session)
	path, key := writeNativeResumeMutexReservation(
		t,
		tm,
		session,
		plan,
		"pause-native-consumed-blank",
	)
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record resumeGoalTransitionRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatal(err)
	}
	record.PauseGeneration = ""
	writeTestJSON(t, path, record)
	writeTestJSON(t, resumeGoalTransitionConsumedPath(path), map[string]string{
		"transition_id": key,
	})

	blocker, err := scanResumeGoalRecoveryTransitions(dir, resumeGoalRecoveryScanOptions{
		LegacyKey:       plan.TransitionID,
		TargetNamespace: squadnamespace.ID(team.DefaultProfile, session),
		TargetAttemptID: plan.OriginalAttemptID,
	})
	if err != nil || blocker == nil ||
		!strings.Contains(blocker.Reason, "requires a pause generation") ||
		blocker.Path != path {
		t.Fatalf("blank consumed pause generation did not fail closed with cause: blocker=%+v err=%v", blocker, err)
	}
}

// Removing reserveResumeGoalTransition's pre-scan makes this call publish the
// legacy redelivery path despite the existing native reservation.
func TestRedeliveryPreReserveScanBlocksNativeWithoutPublishing(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixture(t)
	nativePath, _ := writeNativeResumeMutexReservation(t, tm, session, plan, "pause-pre-reserve")

	err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan)
	if err == nil || !strings.Contains(err.Error(), "before reserve") ||
		!strings.Contains(err.Error(), filepath.Base(nativePath)) {
		t.Fatalf("native reservation did not block before redelivery reserve: %v", err)
	}
	redeliveryPath, pathErr := resumeGoalTransitionPath(
		tm.Project,
		team.DefaultProfile,
		session,
		plan.TransitionID,
	)
	if pathErr != nil {
		t.Fatal(pathErr)
	}
	if _, statErr := os.Stat(redeliveryPath); !os.IsNotExist(statErr) {
		t.Fatalf("pre-reserve refusal still published redelivery reservation: %v", statErr)
	}
}

// A pathname is not proof of ownership. The first scan excludes the exact
// path only because its body carries the same redelivery kind and transition
// id; changing the body at that path must turn it back into a blocker.
func TestRedeliverySelfExclusionRequiresPathAndBodyIdentity(t *testing.T) {
	tm, session, _, plan, verified := freshResumeTransitionFixture(t)
	if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
		t.Fatal(err)
	}
	path, err := resumeGoalTransitionPath(tm.Project, team.DefaultProfile, session, plan.TransitionID)
	if err != nil {
		t.Fatal(err)
	}
	opts := resumeGoalRecoveryScanOptions{
		LegacyKey:       plan.TransitionID,
		TargetNamespace: squadnamespace.ID(team.DefaultProfile, session),
		TargetAttemptID: plan.OriginalAttemptID,
		OwnPath:         path,
		OwnTransitionID: plan.TransitionID,
	}
	if blocker, scanErr := scanResumeGoalRecoveryTransitions(filepath.Dir(path), opts); scanErr != nil || blocker != nil {
		t.Fatalf("content-proven own reservation was not excluded: blocker=%+v err=%v", blocker, scanErr)
	}

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var record resumeGoalTransitionRecord
	if err := json.Unmarshal(payload, &record); err != nil {
		t.Fatal(err)
	}
	record.TransitionID = strings.Repeat("e", 64)
	writeTestJSON(t, path, record)
	blocker, scanErr := scanResumeGoalRecoveryTransitions(filepath.Dir(path), opts)
	if scanErr != nil || blocker == nil ||
		!strings.Contains(blocker.Reason, "disagrees with record transition id") {
		t.Fatalf("changed body at own path was self-excluded: blocker=%+v err=%v", blocker, scanErr)
	}
}

func TestRedeliveryScanBlocksMalformedAndOrphanCurrentEvidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		write func(*testing.T, string)
	}{
		{
			name: "malformed current reservation name",
			write: func(t *testing.T, dir string) {
				t.Helper()
				writeTestJSON(t, filepath.Join(dir, currentRecoveryTransitionPrefix+"bad-key.json"), map[string]string{})
			},
		},
		{
			name: "orphan current bound companion",
			write: func(t *testing.T, dir string) {
				t.Helper()
				key := strings.Repeat("a", 64)
				reservation := filepath.Join(
					dir,
					currentRecoveryTransitionName(recoveryTransitionKindNativeGoalResume, key),
				)
				writeTestJSON(t, resumeGoalTransitionBoundPath(reservation), map[string]string{
					"transition_id": key,
				})
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tm, session, _, plan, _ := freshResumeTransitionFixture(t)
			dir := goalAttemptDir(tm.Project, team.DefaultProfile, session)
			tc.write(t, dir)
			blocker, err := scanResumeGoalRecoveryTransitions(dir, resumeGoalRecoveryScanOptions{
				LegacyKey:       plan.TransitionID,
				TargetNamespace: squadnamespace.ID(team.DefaultProfile, session),
				TargetAttemptID: plan.OriginalAttemptID,
			})
			if err != nil || blocker == nil {
				t.Fatalf("ambiguous current evidence did not block: blocker=%+v err=%v", blocker, err)
			}
		})
	}
}

// This companion prevents an always-refuse implementation from satisfying the
// race row: without a native competitor the production redelivery must reach
// one pane send; with both reservations present its final validation must send
// zero. Removing the redelivery-side rescan changes the second count to one.
func TestRedeliveryNativeRaceChangesProductionDeliveryFromOneToZero(t *testing.T) {
	for _, competitor := range []bool{false, true} {
		name := "without native competitor"
		if competitor {
			name = "with native competitor"
		}
		t.Run(name, func(t *testing.T) {
			tm, session, _, plan, verified := freshResumeTransitionFixture(t)
			if err := reserveResumeGoalTransition(tm, team.DefaultProfile, session, verified, plan); err != nil {
				t.Fatal(err)
			}
			var nativePath string
			if competitor {
				nativePath, _ = writeNativeResumeMutexReservation(t, tm, session, plan, "pause-race")
			}

			oldLister, oldSend := statusPaneLister, sendPromptToPane
			statusPaneLister = func() ([]tmuxpane.TmuxPane, error) {
				return []tmuxpane.TmuxPane{{
					PaneID:  "%447",
					CWD:     tm.Project,
					Command: tm.Members[0].Binary,
					Title:   paneTitleToken(session, plan.LeadRole),
				}}, nil
			}
			sends := 0
			sendPromptToPane = func(_ string, _ string) error {
				sends++
				return nil
			}
			t.Cleanup(func() {
				statusPaneLister, sendPromptToPane = oldLister, oldSend
			})

			_, err := executeGoalDelivery(resumeGoalMutexOptions(tm, session, plan))
			if !competitor {
				if err != nil || sends != 1 {
					t.Fatalf("anti-vacuity delivery failed: err=%v sends=%d", err, sends)
				}
				return
			}
			if err == nil || sends != 0 ||
				!strings.Contains(err.Error(), "cross-kind claim-once mutex") {
				t.Fatalf("both reservations did not suppress redelivery: err=%v sends=%d", err, sends)
			}

			nativeKey := strings.TrimSuffix(
				strings.TrimPrefix(filepath.Base(nativePath), currentRecoveryTransitionPrefix+string(recoveryTransitionKindNativeGoalResume)+"-"),
				".json",
			)
			nativeScan, scanErr := scanRecoveryTransitionsForPause(
				goalAttemptDir(tm.Project, team.DefaultProfile, session),
				nativeKey,
				plan.TransitionID,
			)
			if scanErr != nil {
				t.Fatal(scanErr)
			}
			if other := nativeScan.competingWith(nativePath); other == nil {
				t.Fatal("native side did not refuse the concurrently reserved redelivery")
			}
			redeliveryPath, pathErr := resumeGoalTransitionPath(
				tm.Project,
				team.DefaultProfile,
				session,
				plan.TransitionID,
			)
			if pathErr != nil {
				t.Fatal(pathErr)
			}
			for _, path := range []string{nativePath, redeliveryPath} {
				if _, statErr := os.Stat(path); statErr != nil {
					t.Fatalf("race refusal removed reservation %s: %v", path, statErr)
				}
			}
		})
	}
}
