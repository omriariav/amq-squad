package cli

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// #498 U5: FALSIFIERS FOR THE THREE DELIVERY-TIME GATES.
//
// Each row supplies an input that is valid in EVERY dimension except the one under test. That is the
// r9 lesson applied: a row that is invalid in two dimensions cannot tell you which gate refused, so
// deleting the gate it names may change nothing and the row still passes.
//
// Every row also asserts NO DELIVERY OCCURRED. A refusal that still delivered would be the worst
// possible outcome here, and a row checking only the error text would not notice.

// wantFreshnessDetail is the fragment every delivery-time freshness assertion expects.
//
// IT EXISTS BECAUSE ITS ABSENCE COST A WINDOW. Two rows asserted this same refusal with two different
// expected wordings: when I renamed the detail from "expired during ledger work" to "expired before
// pane input" (accurate once the sample moved after the reads), I updated the code and the new row and
// left the older row asserting a string that no longer existed. It failed in the integration window --
// while the gate underneath it worked perfectly.
//
// HAND-WRITTEN, AND DELIBERATELY NOT REFERENCING THE PRODUCTION FORMAT STRING. A constant sourced from
// production would let expectation and implementation drift together in silence, which is the co-drift
// trap the gate-order literal avoids for the same reason. Shared among the TESTS only, a production
// wording change now fails every row that depends on it, loudly and together -- which is what I wanted
// the first time and did not build.
const wantFreshnessDetail = "expired before pane input"

// The three refusal CLAUSES, hand-written for the same reason as the detail fragment above: imported
// from production they would drift together in silence. A clause identifies WHICH #498 contract gate
// refused, so asserting it catches MISCLASSIFICATION -- a refusal that is correct about refusing and
// wrong about why, which reads as a pass to any test that only checks that an error occurred.
//
// CLAUSE AND DETAIL ARE NOT REDUNDANT, and I checked this against the rule that a gate no mutation can
// kill is decoration. They are two properties of one refusal with two INDEPENDENT falsifiers: mutate
// the clause mapping and the identity assertion fails while the detail still matches; garble the detail
// and the fragment fails while the clause still matches. Two assertions each killable by a distinct
// mutation are coverage, not decoration.
const (
	wantFreshnessClause  = "the assessment remains fresh at the moment of delivery"
	wantGenerationClause = "the same launch generation ... remains current"
	wantPaneClause       = "the same ... pane identity ... remains current"
)

// passingGenerationReader echoes the fixture's OWN bound generation back, so the drift gate passes.
// It deliberately reads from the assessment rather than returning constants: constants would drift
// from the fixture silently, and a reader whose "no drift" answer is wrong makes every OTHER row's
// green meaningless.
func passingGenerationReader(a GoalSupervisionAssessment) supervisionGenerationReader {
	return func(GoalSupervisionAssessment) (string, int64, error) {
		return a.Binding.LaunchRecordDigest, a.Binding.LaunchRecordModTime, nil
	}
}

// passingPaneReader reports the fixture's own pane as managed, present, idle, with a live PID.
func passingPaneReader(a GoalSupervisionAssessment) supervisionPaneReader {
	// Built from passingObservation so there is exactly ONE definition of "a healthy pane". Two
	// identical literals are two owners of one fact, and the rows below mutate a COPY of this one --
	// if the reader's version drifted, every row would fail for a reason unrelated to its own gate.
	return func(GoalSupervisionAssessment) (supervisionPaneObservation, error) {
		return passingObservation(a), nil
	}
}

// deliveryGateRefusal drives the executor with one deliberately-broken reader and returns the refusal
// plus how many deliveries occurred. Sharing this makes every row below differ ONLY in the dimension
// it breaks, which is what makes each row attributable to one gate.
func deliveryGateRefusal(
	t *testing.T,
	clock supervisionResumeClock,
	readGeneration supervisionGenerationReader,
	readPane supervisionPaneReader,
) (int, error) {
	t.Helper()
	assessment, action, payload, _, now := u6DeliveringFixture(t)
	if clock == nil {
		clock = func() time.Time { return now }
	}
	if readGeneration == nil {
		readGeneration = passingGenerationReader(assessment)
	}
	if readPane == nil {
		readPane = passingPaneReader(assessment)
	}
	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { return nil },
		func(GoalSupervisionAssessment) error { return nil },
	)
	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}
	delivered := 0
	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		clock, loader, readGeneration, readPane,
		readReservedRecoveryTransition,
		func(string) error { delivered++; return nil })
	if outcome.Delivered {
		t.Error("outcome reports Delivered=true for a refused delivery")
	}
	return delivered, err
}

func requireDeliveryGateRefusal(t *testing.T, delivered int, err error, wantClause, wantDetail string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected a refusal with clause %q mentioning %q, got nil", wantClause, wantDetail)
	}
	if delivered != 0 {
		t.Errorf("delivered %d times despite a refusal; a gate that refuses AFTER delivering has "+
			"already caused the harm it exists to prevent", delivered)
	}
	var refusal supervisionResumeRefusal
	if !errors.As(err, &refusal) {
		t.Fatalf("refusal must be the typed supervisionResumeRefusal, got %T: %v", err, err)
	}
	// IDENTITY FIRST, by EXACT equality rather than substring. Which gate refused is a machine-facing
	// fact, and a substring could match another clause's text -- the same existence-for-identity
	// substitution that produced the arbitrary-payload hole earlier in this PR.
	if refusal.Clause != wantClause {
		t.Errorf("refusal clause = %q, want exactly %q -- a refusal that is right to refuse and wrong "+
			"about WHICH contract clause it enforces is a misclassification, and it reads as a pass to "+
			"any assertion that only checks an error occurred", refusal.Clause, wantClause)
	}
	if !strings.Contains(refusal.Detail, wantDetail) {
		t.Errorf("refusal detail = %q, want it to contain %q -- a refusal that does not name its "+
			"cause sends an operator to the wrong place", refusal.Detail, wantDetail)
	}
}

// GATE 1 FALSIFIER: the assessment expires DURING the ledger work.
//
// THE ISOLATION PROBLEM, and it is the reason this row needs a stepping clock rather than a constant
// expired time. A constantly-expired clock is refused by the PRE-MUTATION wall-clock gate, which runs
// first; the delivery-time gate would never execute, and deleting it would leave this row green. That
// is gate redundancy defeating the falsifier -- the same trap the policy row hit.
//
// So the clock reports FRESH until the reservation exists on disk, then EXPIRED. That is not a trick
// to dodge a gate: it is exactly the real scenario U4 names, where reserve/bind/rescan consume enough
// wall time for a fresh assessment to expire before the bytes are typed. Keying on the artifact rather
// than a call count also survives refactoring, whereas "expire on the 4th now() call" would silently
// stop isolating the moment anyone adds a clock read.
func TestAnAssessmentThatExpiresDuringLedgerWorkIsRefusedAtDeliveryTime(t *testing.T) {
	assessment, action, payload, reservation, now := u6DeliveringFixture(t)
	expired := assessment.FreshUntil.Add(time.Minute)

	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { return nil },
		func(GoalSupervisionAssessment) error { return nil },
	)
	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}
	clock := func() time.Time {
		if _, err := os.Stat(reservation); err == nil {
			return expired
		}
		return now
	}
	delivered := 0
	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		clock, loader,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		readReservedRecoveryTransition,
		func(string) error { delivered++; return nil })

	if outcome.Delivered || delivered != 0 {
		t.Errorf("delivered=%d Delivered=%t; an assessment that expired before input must not be "+
			"delivered, because the contract already declared that observation too old to act on",
			delivered, outcome.Delivered)
	}
	requireDeliveryGateRefusal(t, delivered, err, wantFreshnessClause, wantFreshnessDetail)
	// PROOF OF ISOLATION: the reservation must EXIST. If it does not, the run was refused before
	// reserve, which means the pre-mutation gate caught it and this row proved nothing about the
	// delivery-time gate it claims to test.
	if _, statErr := os.Stat(reservation); statErr != nil {
		t.Error("no reservation exists, so the refusal happened BEFORE reserve -- this row did not " +
			"reach the delivery-time clock gate and its green would be meaningless")
	}
}

// GATE 2 FALSIFIERS: generation drift, one row per compared component.
//
// TWO ROWS, not one. Comparing only the digest would leave the modtime comparison untested, and an
// untested half of a conjunction is where the omitempty defect lived earlier in this PR. The
// same-digest-different-modtime case is the one a digest-only gate silently accepts: a relaunch that
// reproduces identical bytes, behind which the pane may be a different process entirely.
func TestLaunchGenerationDigestDriftIsRefusedAtDeliveryTime(t *testing.T) {
	delivered, err := deliveryGateRefusal(t, nil,
		func(a GoalSupervisionAssessment) (string, int64, error) {
			return "a-different-digest", a.Binding.LaunchRecordModTime, nil
		}, nil)
	requireDeliveryGateRefusal(t, delivered, err, wantGenerationClause, "launch generation changed")
}

func TestLaunchGenerationModTimeDriftIsRefusedEvenWithAnIdenticalDigest(t *testing.T) {
	delivered, err := deliveryGateRefusal(t, nil,
		func(a GoalSupervisionAssessment) (string, int64, error) {
			// IDENTICAL digest, different modtime. A digest-only comparison passes this.
			return a.Binding.LaunchRecordDigest, a.Binding.LaunchRecordModTime + 1, nil
		}, nil)
	requireDeliveryGateRefusal(t, delivered, err, wantGenerationClause, "launch generation changed")
}

// An UNREADABLE generation must refuse. Unknown is not unchanged, and this gate exists precisely
// because the record may have moved.
func TestAnUnreadableLaunchGenerationIsRefusedRatherThanAssumedUnchanged(t *testing.T) {
	delivered, err := deliveryGateRefusal(t, nil,
		func(GoalSupervisionAssessment) (string, int64, error) {
			return "", 0, errors.New("launch record vanished")
		}, nil)
	requireDeliveryGateRefusal(t, delivered, err, wantGenerationClause, "cannot re-read the launch generation")
}

// GATE 3 FALSIFIERS: one row per pane conjunct, each valid in every other dimension.
func TestUnmanagedPaneIsRefusedAtDeliveryTime(t *testing.T) {
	delivered, err := deliveryGateRefusal(t, nil, nil,
		func(a GoalSupervisionAssessment) (supervisionPaneObservation, error) {
			o := passingObservation(a)
			o.Managed = false
			return o, nil
		})
	requireDeliveryGateRefusal(t, delivered, err, wantPaneClause, "is not managed at delivery time")
}

func TestAPaneThatIsNotAffirmativelyPresentIsRefused(t *testing.T) {
	delivered, err := deliveryGateRefusal(t, nil, nil,
		func(a GoalSupervisionAssessment) (supervisionPaneObservation, error) {
			o := passingObservation(a)
			o.Found = false
			o.State = "unavailable"
			return o, nil
		})
	requireDeliveryGateRefusal(t, delivered, err, wantPaneClause, "not affirmatively present")
}

// THE PANE-OUTLIVES-ITS-PROCESS ROW. A pane-exists check passes this input; bytes typed into a pane
// with no live process are silently discarded, which is a delivery that reports success and did
// nothing. That is why PID is a separate conjunct rather than folded into presence.
func TestAPresentPaneWithNoLiveProcessIsRefused(t *testing.T) {
	delivered, err := deliveryGateRefusal(t, nil, nil,
		func(a GoalSupervisionAssessment) (supervisionPaneObservation, error) {
			o := passingObservation(a)
			o.PID = 0
			return o, nil
		})
	requireDeliveryGateRefusal(t, delivered, err, wantPaneClause, "no live foreground process")
}

// UNKNOWN busyness and BUSY are separate rows with separate messages. Collapsing them would report a
// busy pane when the truth is a failed probe, sending an operator to wait for a pane that is idle.
func TestUnknownPaneBusynessIsRefusedSeparatelyFromBusy(t *testing.T) {
	delivered, err := deliveryGateRefusal(t, nil, nil,
		func(a GoalSupervisionAssessment) (supervisionPaneObservation, error) {
			o := passingObservation(a)
			o.IdleKnown = false
			o.Idle = false
			return o, nil
		})
	requireDeliveryGateRefusal(t, delivered, err, wantPaneClause, "busy state is UNKNOWN")
}

func TestABusyPaneIsRefusedAtDeliveryTime(t *testing.T) {
	delivered, err := deliveryGateRefusal(t, nil, nil,
		func(a GoalSupervisionAssessment) (supervisionPaneObservation, error) {
			o := passingObservation(a)
			o.IdleKnown = true
			o.Idle = false
			return o, nil
		})
	requireDeliveryGateRefusal(t, delivered, err, wantPaneClause, "is BUSY at delivery time")
}

// A reader that inspected the WRONG pane must refuse. Its facts may all look healthy; they are simply
// about something else, and healthy facts about the wrong target passing a gate is worse than the
// gate failing.
func TestAnObservationForADifferentPaneIsRefused(t *testing.T) {
	delivered, err := deliveryGateRefusal(t, nil, nil,
		func(a GoalSupervisionAssessment) (supervisionPaneObservation, error) {
			o := passingObservation(a)
			o.PaneID = "%999"
			return o, nil
		})
	requireDeliveryGateRefusal(t, delivered, err, wantPaneClause, "but the assessment binds pane")
}

// NIL READERS REFUSE. A missing check is not a passing check, and these are authorization seams: if a
// nil reader skipped, deleting the injection at the call site would silently disable the gate while
// every test that supplies a reader stayed green.
func TestNilDeliveryTimeReadersRefuseRatherThanSkip(t *testing.T) {
	assessment, _, _, _, now := u6DeliveringFixture(t)

	if refusal := evaluateSupervisionDeliveryTimeGates(assessment, func() time.Time { return now }, nil, passingPaneReader(assessment)); refusal == nil {
		t.Error("a nil generation reader must REFUSE; skipping would make an absent check " +
			"indistinguishable from a passing one")
	} else if refusal.Clause != wantGenerationClause || !strings.Contains(refusal.Detail, "no launch-generation reader") {
		t.Errorf("nil generation reader refusal must carry clause %q and name the wiring fault; got clause %q detail %q",
			wantGenerationClause, refusal.Clause, refusal.Detail)
	}
	if refusal := evaluateSupervisionDeliveryTimeGates(assessment, func() time.Time { return now }, passingGenerationReader(assessment), nil); refusal == nil {
		t.Error("a nil pane reader must REFUSE")
	} else if refusal.Clause != wantPaneClause || !strings.Contains(refusal.Detail, "no pane reader") {
		t.Errorf("nil pane reader refusal must carry clause %q and name the wiring fault; got clause %q detail %q",
			wantPaneClause, refusal.Clause, refusal.Detail)
	}
}

// THE ALL-VALID CONTROL. Without it, every row above could be passing because the fixture refuses for
// some unrelated reason, and the suite would look thorough while proving nothing.
func TestAllDeliveryTimeGatesPassForAValidObservation(t *testing.T) {
	assessment, _, _, _, now := u6DeliveringFixture(t)
	if refusal := evaluateSupervisionDeliveryTimeGates(assessment, func() time.Time { return now },
		passingGenerationReader(assessment), passingPaneReader(assessment)); refusal != nil {
		t.Fatalf("the all-valid delivery-time fixture must pass every gate; refused with: %s", refusal.Detail)
	}
}

func passingObservation(a GoalSupervisionAssessment) supervisionPaneObservation {
	return supervisionPaneObservation{
		PaneID:    a.Binding.Pane.PaneID,
		Managed:   true,
		Found:     true,
		State:     "found",
		PID:       4242,
		IdleKnown: true,
		Idle:      true,
	}
}

// GATE 3 FALSIFIER, dev-2's finding: the assessment is FRESH at evaluator entry and expires DURING the
// generation/pane reads.
//
// WHY THE EARLIER ROW DID NOT COVER THIS, which is the part worth keeping. The artifact-keyed row makes
// the clock return expired as soon as the reservation exists, so with freshness checked FIRST it
// refused at evaluator entry. It therefore proved that an already-expired assessment is refused -- and
// said nothing about one that expires between entry and input. My gate order made that gap invisible:
// a check placed before the two slowest reads cannot observe time passing during them.
//
// THE FALSIFYING INPUT advances the clock from INSIDE a reader. That is exactly the real scenario --
// the pane read performs bounded tmux retries and can take real time -- and it is only catchable if
// the freshness sample happens after the readers. Move the sample back before them, or delete it, and
// this row fails.
func TestAnAssessmentExpiringDuringTheDeliveryTimeReadsIsRefused(t *testing.T) {
	assessment, action, payload, _, base := u6DeliveringFixture(t)

	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { return nil },
		func(GoalSupervisionAssessment) error { return nil },
	)
	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}

	// The clock is FRESH until a reader advances it. Nothing before the readers can see the expiry.
	current := base
	expiredAtEntry := false
	clock := func() time.Time { return current }
	readGeneration := func(a GoalSupervisionAssessment) (string, int64, error) {
		// Record whether the assessment was already expired when the reads began. If it was, this row
		// is not testing what it claims -- it would have degenerated into the earlier row.
		expiredAtEntry = !current.Before(a.FreshUntil)
		return a.Binding.LaunchRecordDigest, a.Binding.LaunchRecordModTime, nil
	}
	readPane := func(a GoalSupervisionAssessment) (supervisionPaneObservation, error) {
		// TIME PASSES HERE, standing in for the bounded tmux retries a real pane read performs.
		current = a.FreshUntil.Add(time.Second)
		return passingObservation(a), nil
	}

	delivered := 0
	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		clock, loader, readGeneration, readPane,
		readReservedRecoveryTransition,
		func(string) error { delivered++; return nil })

	if expiredAtEntry {
		t.Fatal("the assessment was already expired when the delivery-time reads began, so this row " +
			"degenerated into the already-expired case and proves nothing about expiry DURING the reads")
	}
	if delivered != 0 || outcome.Delivered {
		t.Errorf("delivered=%d Delivered=%t; an assessment that expired during the delivery-time reads "+
			"must not reach the pane", delivered, outcome.Delivered)
	}
	requireDeliveryGateRefusal(t, delivered, err, wantFreshnessClause, wantFreshnessDetail)
}

// THE BOUNDARY ROW. At EXACT equality with FreshUntil the assessment is EXPIRED, because the
// pre-mutation gate defines it that way with !now().Before(FreshUntil). Two gates reading one field
// must not disagree at any value; my first version used After(), which called this instant fresh.
func TestExactFreshUntilEqualityIsExpiredAtDeliveryTime(t *testing.T) {
	assessment, _, _, _, _ := u6DeliveringFixture(t)
	atBoundary := assessment.FreshUntil
	refusal := evaluateSupervisionDeliveryTimeGates(assessment, func() time.Time { return atBoundary },
		passingGenerationReader(assessment), passingPaneReader(assessment))
	if refusal == nil {
		t.Fatal("an instant exactly equal to FreshUntil must be EXPIRED here, matching " +
			"goal_supervision_gates.go's !now().Before(FreshUntil); treating it as fresh makes the " +
			"dry-run and the delivering path disagree at that instant")
	}
	// CLAUSE AND DETAIL, same as the helper enforces. Without the clause this row would pass if the
	// boundary instant were refused by a DIFFERENT gate that happened to mention the same fragment.
	if refusal.Clause != wantFreshnessClause {
		t.Errorf("boundary refusal clause = %q, want exactly %q", refusal.Clause, wantFreshnessClause)
	}
	if !strings.Contains(refusal.Detail, wantFreshnessDetail) {
		t.Errorf("boundary refusal must be the freshness one, got %q", refusal.Detail)
	}
}
