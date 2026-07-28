package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// PR5 / #498 U6: THE BEHAVIOURAL FALSIFIERS for post-delivery reporting.
//
// U6's sentence has two halves and they are independently falsifiable: failure never authorizes
// replay, AND failure evidence never disappears. Every row below attacks the second half, because
// the first half was already load-bearing in the claim-once rows while the second half was, until
// dev-2 named it, satisfied by code that silently discarded errors.
//
// WHY ORDER IS PROVEN BY ARTIFACT AND NOT BY A RECORDED LABEL. A recorded event log
// (append "consume", append "directive", append "poll") only proves the order in which my STUBS
// were called. It cannot prove that consume actually completed before the directive claimed
// delivery, because a stub that records a label is agnostic about what the code did. So ordering
// here is established from the LEDGER: consume publishes a `.consumed.json` companion beside the
// reservation, so the directive stub asserting that companion EXISTS proves consume completed
// first, and the same assertion inverted inside `deliver` proves it had not. That is the
// difference between observing a sequence and proving a happens-before.

// u6DeliveringFixture builds the one fixture that passes all eleven pre-mutation gates and reaches
// delivery, so post-delivery behaviour is reachable at all. It is deliberately the same shape as the
// U9 policy row's fixture with AutomaticResumeAllowed TRUE -- which here is production-consistent
// rather than forced, since Eligible && Mode==safe_auto is exactly how production computes it.
//
// It returns the reservation path the executor WILL create, computed from the canonical claim key
// rather than guessed, so the ordering assertions inspect the real artifact.
func u6DeliveringFixture(t *testing.T) (GoalSupervisionAssessment, GoalSupervisionAction, string, string, time.Time) {
	t.Helper()

	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	const pauseGeneration = "pause-gen-1"
	const payload = "/goal resume attempt-abc"

	now := time.Now().UTC()
	project := t.TempDir()
	namespaceID := squadnamespace.ID(profile, session)

	assessment := GoalSupervisionAssessment{
		Fresh:       true,
		Eligible:    true,
		ObservedAt:  now,
		FreshUntil:  now.Add(time.Hour),
		Policy:      team.GoalSupervisionPolicyStatus{Mode: team.GoalSupervisionSafeAuto, Revision: 1, Source: "test"},
		Fingerprint: "fingerprint-1",
		// THE BLOCKER EVIDENCE, which this fixture was missing entirely.
		//
		// It sets Eligible=true DIRECTLY rather than letting eligibility be computed, so the absent blocker
		// never showed up as an ineligible assessment -- the gates read the flag, not the fields behind it.
		// That is a fixture asserting a state production cannot reach: eligibility
		// (goal_supervision.go:341-342) requires Known + nonblank ID + Resolved + nonblank ResolutionDigest,
		// so no real eligible assessment has a blank blocker. The executor now persists these into the exact
		// binding, where the constructor requires them, and construction would refuse this fixture outright.
		Blocker: GoalSupervisionBlockerEvidence{
			ID: "blocker-1", Known: true, Resolved: true, ResolutionDigest: "resolution-digest-1",
		},
		Binding: GoalSupervisionBinding{
			Project: project, Profile: profile, Session: session,
			NamespaceID: namespaceID,
			LeadRole:    "cto", LeadHandle: "cto",
			LaunchID:            "launch-1",
			LaunchRecordDigest:  "launch-digest-1",
			LaunchRecordModTime: 1700000000,
			LaunchStartedAt:     now.Add(-time.Minute),
			Goal: GoalSupervisionGoalIdentity{
				// Mode was ABSENT here for the same reason the blocker was: nothing in the gate path read it.
				// The exact binding does, and it requires the value production actually emits.
				Mode:      fixtureNativeGoalMode,
				AttemptID: attemptID, BindingDigest: "digest-def",
				CommandDigest: digestGoalSupervisionString(payload),
			},
			PauseGeneration: pauseGeneration,
			Pane:            GoalSupervisionPaneIdentity{PaneID: "%7", Managed: true},
		},
	}
	assessment.AutomaticResumeAllowed = true
	assessment.Actions = goalSupervisionActions(assessment)

	dir := goalAttemptDir(project, profile, session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create ledger directory: %v", err)
	}

	// The reservation path is DERIVED from the canonical key, not hardcoded. A literal filename here
	// would silently stop matching if the naming changed, and every ordering assertion below would
	// then pass vacuously against a path that never exists -- the zero-effect-assertion trap.
	key, err := supervisionClaimKey(namespaceID, pauseGeneration, attemptID)
	if err != nil {
		t.Fatalf("canonical claim key: %v", err)
	}
	reservation := filepath.Join(dir, currentRecoveryTransitionName(recoveryTransitionKindNativeGoalResume, key))

	return assessment, assessment.Actions.Resume, payload, reservation, now
}

// u6StubSeams replaces the two package-global post-success seams and restores them. Restoration is
// not hygiene theatre: these are package globals, so a leaked stub would silently rewire the NEXT
// test in the same binary, and the production directive publisher would otherwise try to write into
// a real AMQ root during a unit test.
func u6StubSeams(t *testing.T, directive supervisionDirectivePublisher, poll func(GoalSupervisionAssessment) error) {
	t.Helper()
	previousDirective, previousPoll := publishDeliveredDirective, pollSupervisionStatusOnce
	t.Cleanup(func() {
		publishDeliveredDirective = previousDirective
		pollSupervisionStatusOnce = previousPoll
	})
	publishDeliveredDirective = directive
	pollSupervisionStatusOnce = poll
}

// FALSIFIER 1: a failed DELIVERED directive is REPORTED, not swallowed, and does not fail the command.
//
// THE FALSIFYING INPUT is a directive publisher that returns an error after a delivery that
// succeeded and a consume that succeeded. If the executor swallows the error -- its original
// behaviour -- DeliveredDirectiveError is empty and the operator never learns the audit record is
// missing for a resume that really happened. If the executor instead RETURNS the error, a completed
// irreversible delivery is reported as a failed command, which is the conflation dev-2 named.
//
// So this row asserts BOTH directions at once: evidence present AND exit success. A row asserting
// only one of them would be satisfied by the defect on the other side.
func TestFailedDeliveredDirectiveIsReportedWithoutFailingTheCommand(t *testing.T) {
	assessment, action, payload, reservation, now := u6DeliveringFixture(t)
	directiveErr := errors.New("audit root unwritable")

	consumedBeforeDirective := false
	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error {
			// ORDERING, proven from the ledger: consume publishes the .consumed.json companion, so its
			// existence here means consume completed BEFORE this directive claimed delivery. A directive
			// that says DELIVERED before the claim is consumed would be an audit record asserting
			// something not yet true.
			_, statErr := os.Stat(resumeGoalTransitionConsumedPath(reservation))
			consumedBeforeDirective = statErr == nil
			return directiveErr
		},
		func(GoalSupervisionAssessment) error { return nil },
	)

	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}
	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loader,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		readReservedRecoveryTransition,
		func(string) error { return nil })

	if err != nil {
		t.Fatalf("a directive failure after a completed delivery must NOT fail the command; got %v.\n"+
			"An ordinary error here makes a delivery that HAPPENED indistinguishable from one that did not.", err)
	}
	if !outcome.Delivered || !outcome.Consumed {
		t.Errorf("Delivered=%t Consumed=%t; both must be true -- the directive is bookkeeping ABOUT a "+
			"delivery, and its failure cannot retroactively unmake the delivery or the consume",
			outcome.Delivered, outcome.Consumed)
	}
	if outcome.DeliveredDirectivePublished {
		t.Error("DeliveredDirectivePublished=true after the publisher returned an error")
	}
	if !strings.Contains(outcome.DeliveredDirectiveError, directiveErr.Error()) {
		t.Errorf("DeliveredDirectiveError = %q, want it to carry %q.\nU6 permits failure to not authorize "+
			"replay; it does not permit failure evidence to disappear.",
			outcome.DeliveredDirectiveError, directiveErr.Error())
	}
	if !consumedBeforeDirective {
		t.Error("the .consumed.json companion did not exist when the DELIVERED directive ran, so the " +
			"directive claimed delivery before the consume that makes the claim true")
	}
	// The warning is the OPERATOR-VISIBLE half. A structured field nobody renders is evidence only for
	// machines, and this failure needs a human to notice a missing audit record.
	if got := outcome.warnings(); len(got) != 1 || !strings.Contains(got[0], directiveErr.Error()) {
		t.Errorf("warnings() = %v, want exactly one carrying the directive error", got)
	}
}

// FALSIFIER 2: a failed status poll is REPORTED, is observability-only, and does not fail the command.
//
// Distinct from falsifier 1 despite the identical shape, because the two failures have different
// MEANINGS and the outcome must not flatten them: a missing audit directive is a durable
// record-keeping hole, a failed poll costs only a refreshed view. Sharing one error field, or one
// warning phrasing, would tell an operator the wrong severity.
func TestFailedStatusPollIsReportedAsObservabilityOnly(t *testing.T) {
	assessment, action, payload, _, now := u6DeliveringFixture(t)
	pollErr := errors.New("status probe timed out")

	directiveRan := false
	pollRanAfterDirective := false
	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { directiveRan = true; return nil },
		func(GoalSupervisionAssessment) error {
			pollRanAfterDirective = directiveRan
			return pollErr
		},
	)

	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}
	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loader,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		readReservedRecoveryTransition,
		func(string) error { return nil })

	if err != nil {
		t.Fatalf("an observability failure must not fail a completed resume; got %v", err)
	}
	if !outcome.Delivered || !outcome.Consumed || !outcome.DeliveredDirectivePublished {
		t.Errorf("Delivered=%t Consumed=%t DirectivePublished=%t; a poll failure must leave every "+
			"preceding success intact", outcome.Delivered, outcome.Consumed, outcome.DeliveredDirectivePublished)
	}
	if outcome.StatusPolled {
		t.Error("StatusPolled=true after the poll returned an error")
	}
	if !strings.Contains(outcome.StatusPollError, pollErr.Error()) {
		t.Errorf("StatusPollError = %q, want it to carry %q", outcome.StatusPollError, pollErr.Error())
	}
	if !pollRanAfterDirective {
		t.Error("the poll ran BEFORE the DELIVERED directive; the contract is consume -> directive -> " +
			"poll, so the poll observes a world in which the directive already exists")
	}
	// SEVERITY, not merely presence. The poll warning must say the resume is unaffected; if it read like
	// the directive warning an operator would investigate a resume that is entirely fine.
	got := outcome.warnings()
	if len(got) != 1 || !strings.Contains(got[0], "observability only") {
		t.Errorf("warnings() = %v, want exactly one marked observability only -- a poll failure and a "+
			"lost audit record must not read as the same severity", got)
	}
}

// FALSIFIER 3: an UNKNOWN pane-input result is terminal-indeterminate, not a refusal, and reaches
// neither consume nor any post-success step.
//
// This is the row for the state dev-2 twice found collapsed. The zero outcome made an unknown
// delivery byte-identical to an ordinary pre-delivery refusal, so the command reported "refused" for
// the one case that leaves a live reservation and an operator decision behind.
//
// THE FALSIFYING INPUT is a deliver callback that returns an error AFTER the claim is reserved. The
// row then asserts the four things that must all hold together: the discriminator is set, Delivered
// is NOT set (success is unknown, and claiming it would be worse than claiming failure), the claim is
// NOT consumed, and no post-success step ran at all.
func TestUnknownPaneInputIsTerminalIndeterminateAndConsumesNothing(t *testing.T) {
	assessment, action, payload, reservation, now := u6DeliveringFixture(t)
	inputErr := errors.New("pane input could not be confirmed")

	directiveRan, pollRan := false, false
	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { directiveRan = true; return nil },
		func(GoalSupervisionAssessment) error { pollRan = true; return nil },
	)

	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}
	reservedAtDelivery := false
	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loader,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		readReservedRecoveryTransition,
		func(string) error {
			// RESERVE-BEFORE-DELIVER, proven rather than assumed: the reservation must already exist when
			// delivery is attempted, because that ordering is what makes a crash here read as
			// indeterminate instead of invisible.
			_, statErr := os.Stat(reservation)
			reservedAtDelivery = statErr == nil
			return inputErr
		})

	if err == nil {
		t.Error("an unconfirmed delivery must return an error; silence would let a caller treat an " +
			"indeterminate pause as a completed one")
	}
	if !outcome.DeliveryOutcomeUnknown {
		t.Error("DeliveryOutcomeUnknown=false after an unconfirmed pane input.\nWithout this " +
			"discriminator the outcome is byte-identical to an ordinary pre-delivery refusal, and the " +
			"command reports refused for a pause that may well have been resumed.")
	}
	if outcome.Delivered {
		t.Error("Delivered=true for an UNCONFIRMED input; Delivered must mean KNOWN-successful, " +
			"because a caller reading it as truth would skip the inspection this state requires")
	}
	if outcome.Consumed {
		t.Error("Consumed=true after an unconfirmed delivery; the claim must stay reserved so " +
			"claim-once refuses the next attempt rather than risking a second audited resume")
	}
	if !reservedAtDelivery {
		t.Error("the reservation did not exist when delivery was attempted, so a crash at delivery " +
			"would leave no evidence that a resume may have been sent")
	}
	if _, statErr := os.Stat(resumeGoalTransitionConsumedPath(reservation)); statErr == nil {
		t.Error("a .consumed.json companion exists after an unconfirmed delivery; consuming an " +
			"indeterminate claim would record a completion nobody established")
	}
	if directiveRan || pollRan {
		t.Errorf("post-success steps ran after an unconfirmed delivery (directive=%t poll=%t); both "+
			"assert a completed resume, and neither fact is available here", directiveRan, pollRan)
	}
}

// FALSIFIER 4: a consume failure after a KNOWN delivery reports the delivery AND the failure.
//
// The honesty row. Delivered was originally set only after consume succeeded, so a successful
// delivery followed by a failed consume returned Delivered=false -- a report denying an
// irreversible action that demonstrably happened, which is the most dangerous shape available here
// because a caller may reasonably retry.
//
// INDUCTION METHOD, and it is worth naming because it is not obvious: consume is called directly
// rather than through a seam, so it cannot be stubbed. Instead the ledger directory is made
// read-only from INSIDE the deliver callback -- the one point that runs after reserve and before
// consume. Delivery therefore succeeds and the subsequent companion publish genuinely fails, which
// exercises the real consume code path rather than a fake standing in for it.
func TestConsumeFailureAfterKnownDeliveryReportsBothFacts(t *testing.T) {
	assessment, action, payload, reservation, now := u6DeliveringFixture(t)
	dir := filepath.Dir(reservation)

	directiveRan, pollRan := false, false
	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { directiveRan = true; return nil },
		func(GoalSupervisionAssessment) error { pollRan = true; return nil },
	)

	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}
	deliveredPayload := ""
	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loader,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		readReservedRecoveryTransition,
		func(got string) error {
			deliveredPayload = got
			// Read-only ledger directory: the delivery below is a genuine success and the consume that
			// follows genuinely cannot publish its companion.
			if chmodErr := os.Chmod(dir, 0o555); chmodErr != nil {
				t.Fatalf("make ledger directory read-only: %v", chmodErr)
			}
			// Restored so t.TempDir cleanup can remove the tree; without this the failure surfaces as an
			// unrelated cleanup error and obscures this row's result.
			t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
			// THE PREMISE IS PROVED, NOT ASSUMED. Mode 0555 does not stop a privileged user, so under root
			// the consume would SUCCEED and this row would fail while reporting nothing about the
			// return-value-versus-reality property it exists to guard. A probe turns that from a confusing
			// failure into an explicit skip, and it is the same reason the policy row probes writability
			// in the opposite direction.
			probe := filepath.Join(dir, ".consume-failure-premise-probe")
			if probeErr := os.WriteFile(probe, []byte("x"), 0o644); probeErr == nil {
				_ = os.Remove(probe)
				t.Skip("ledger directory is still writable at mode 0555 (privileged user?), so a consume " +
					"failure cannot be induced this way and this row would prove nothing")
			}
			return nil
		})

	if err == nil {
		t.Fatal("a failed consume must be reported as an error; the claim is left reserved and an " +
			"operator has to resolve it, so silence would strand an indeterminate pause")
	}
	if !outcome.Delivered {
		t.Error("Delivered=false after a KNOWN-successful delivery whose consume failed.\nThe " +
			"irreversible fact is established by the delivery, not by the bookkeeping after it, and a " +
			"caller reading Delivered=false may retry a resume that already landed.")
	}
	if outcome.Consumed {
		t.Error("Consumed=true although the companion publish failed")
	}
	if outcome.DeliveryOutcomeUnknown {
		t.Error("DeliveryOutcomeUnknown=true for a delivery that was KNOWN to succeed; this row and " +
			"falsifier 3 are different states and flattening them loses the distinction dev-2 required")
	}
	if outcome.ConsumeError == "" {
		t.Error("ConsumeError empty after a failed consume; the outcome must carry both facts, because " +
			"a bare error would erase the delivery and a bare outcome would hide the failure")
	}
	if deliveredPayload != payload {
		t.Errorf("delivered payload = %q, want the digest-authorized native payload %q", deliveredPayload, payload)
	}
	// Post-success steps must NOT run: both would claim a consumed claim that does not exist.
	if directiveRan || pollRan {
		t.Errorf("post-success steps ran after a failed consume (directive=%t poll=%t)", directiveRan, pollRan)
	}
	if got := outcome.warnings(); len(got) != 1 || !strings.Contains(got[0], "INDETERMINATE") {
		t.Errorf("warnings() = %v, want exactly one marking the pause INDETERMINATE", got)
	}
}

// #498 U7 SLICE / cross-kind mutex prerequisite: native reservations must persist a MATCHABLE
// attempt identity (cto ruling 2026-07-28T09:25, handed off from dev-2's Unit B).
//
// WHY THIS EXISTS AT ALL: the redelivery-side mutex tries to prove a current reservation belongs to a
// DIFFERENT pause before ignoring it. That escape keyed on record fields native reservations never
// populated, so it could never open -- the code read as conditional while behaving unconditionally,
// and every native pause blocked every redelivery forever. The fix is to persist what a reader can
// match on.
func TestNativeReservationPersistsTheResumedAttemptAsItsMatchableIdentity(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	project := t.TempDir()

	record, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                nativeTransitionBase(project, profile, session),
		Kind:                recoveryTransitionKindNativeGoalResume,
		Supervisor:          "supervisor-1",
		NamespaceID:         squadnamespace.ID(profile, session),
		AttemptID:           attemptID,
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
		NativeBinding:       validNativeBinding(squadnamespace.ID(profile, session), attemptID),
	})
	if err != nil {
		t.Fatalf("a complete native input must be accepted: %v", err)
	}
	if record.NewAttemptID != attemptID {
		t.Errorf("NewAttemptID = %q, want the resumed attempt %q.\nA native record with no matchable "+
			"attempt makes the redelivery mutex's proves-different escape unreachable, so every native "+
			"pause blocks every redelivery permanently.", record.NewAttemptID, attemptID)
	}
	if strings.TrimSpace(record.NewAttemptID) == "" {
		t.Error("NewAttemptID is blank; the ruling requires it NONBLANK, because a blank field is " +
			"indistinguishable from the unpopulated state this fixes")
	}
}

// FALSIFIER: a caller cannot smuggle a DIFFERENT attempt id through Base.
//
// The constructor owns identity. If Base could override it, a record could be filed under the claim
// key for one attempt while claiming another -- exactly the filename-versus-body disagreement that
// the scan treats as ambiguous evidence, manufactured at write time instead of by corruption.
func TestNativeReservationRefusesASmuggledDisagreeingAttemptID(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	smuggled := nativeTransitionBase(project, profile, session)
	// A DIFFERENT attempt than the one being resumed. Applied to the complete base rather than replacing
	// it, so the smuggled id is the row's ONLY defect and the launch generation stays valid.
	smuggled.NewAttemptID = "attempt-somebody-elses"

	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                smuggled,
		Kind:                recoveryTransitionKindNativeGoalResume,
		Supervisor:          "supervisor-1",
		NamespaceID:         squadnamespace.ID(profile, session),
		AttemptID:           "attempt-abc",
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
		NativeBinding:       validNativeBinding(squadnamespace.ID(profile, session), "attempt-abc"),
	})
	if err == nil {
		t.Fatal("a Base-supplied NewAttemptID disagreeing with the attempt being resumed must be " +
			"REFUSED; silently overwriting it would hide a caller bug, and honouring it would file the " +
			"record under one attempt while it claims another")
	}
	if !strings.Contains(err.Error(), "disagrees with the attempt being resumed") {
		t.Errorf("refusal must name the disagreement, got: %v", err)
	}
}

// REGRESSION GUARD for a change I made in SHARED code.
//
// The NewAttemptID derivation lives inside the native branch, but it is in the constructor both kinds
// use. If it ever leaks to the redeliver branch it would overwrite redelivery's NewAttemptID with the
// ORIGINAL attempt id -- and redelivery has two validators that REJECT exactly that ("transition
// reuses the original attempt id"). The field's meaning is inverted between kinds, so a leak here
// does not merely change a value, it violates the other kind's invariant.
func TestRedeliverReservationKeepsItsOwnNewAttemptIDUntouched(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const originalAttempt = "attempt-original"
	const freshAttempt = "attempt-freshly-created"
	project := t.TempDir()

	record, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: resumeGoalTransitionRecord{
			Project: project, Profile: profile, Session: session,
			OriginalAttemptID: originalAttempt, OriginalBindingDigest: "binding-digest-1",
			NewAttemptID: freshAttempt,
		},
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     originalAttempt,
		BindingDigest: "binding-digest-1",
	})
	if err != nil {
		t.Fatalf("a complete redeliver input must still be accepted: %v", err)
	}
	if record.NewAttemptID != freshAttempt {
		t.Errorf("redeliver NewAttemptID = %q, want the caller's fresh attempt %q -- the native "+
			"derivation must not reach this branch", record.NewAttemptID, freshAttempt)
	}
	if record.NewAttemptID == record.OriginalAttemptID {
		t.Error("redeliver NewAttemptID now equals OriginalAttemptID, which its own validators reject " +
			"as \"transition reuses the original attempt id\"")
	}
}

// #498 U7 REMAINDER: the SUPERVISOR is persisted on native reservations.
//
// WHY IT MATTERS: the pre-mutation gates already refuse a blank or placeholder supervisor, and the
// DELIVERED directive names it -- and then it was discarded. So a durable claim could not answer the one
// question an operator asks about an unexpected resume: who authorised this. Validating an identity and
// then not recording it is accountability that exists only while the process is alive.
func TestNativeReservationPersistsTheAuthorisingSupervisor(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	record, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                nativeTransitionBase(project, profile, session),
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         squadnamespace.ID(profile, session),
		AttemptID:           "attempt-abc",
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
		Supervisor:          "cto",
		NativeBinding:       validNativeBinding(squadnamespace.ID(profile, session), "attempt-abc"),
	})
	if err != nil {
		t.Fatalf("a complete native input must be accepted: %v", err)
	}
	if record.Supervisor != "cto" {
		t.Errorf("Supervisor = %q, want %q -- a claim nobody is recorded as authorising cannot be "+
			"audited after the fact", record.Supervisor, "cto")
	}
}

// A BLANK supervisor REFUSES at the constructor, not only at the gate.
//
// The gate protects the delivering path; this protects the RECORD. Any future caller that skips the gate
// would otherwise write an unattributable claim, and a durable identity field whose only guard lives in
// one caller is guarded by convention rather than by construction.
func TestNativeReservationRefusesABlankSupervisor(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                nativeTransitionBase(project, profile, session),
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         squadnamespace.ID(profile, session),
		AttemptID:           "attempt-abc",
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
		// EVERYTHING ELSE VALID, including the exact binding: the missing supervisor must be the only
		// defect. The constructor happens to check the supervisor before the binding, so this row would
		// still refuse for the right reason today -- but relying on check ORDER for the single-defect
		// property means a harmless reordering silently changes what this row proves.
		NativeBinding: validNativeBinding(squadnamespace.ID(profile, session), "attempt-abc"),
		// Supervisor deliberately absent.
	})
	if err == nil {
		t.Fatal("a native reservation without a supervisor must be REFUSED")
	}
	if !strings.Contains(err.Error(), "requires the supervisor identity") {
		t.Errorf("refusal must name the missing supervisor, got: %v", err)
	}
}

// THE PLACEHOLDER is refused too, and this is the sharper of the two: a blank field is visibly empty,
// whereas the literal "<your-identity>" LOOKS like an answer while attributing the resume to nobody.
func TestNativeReservationRefusesThePlaceholderSupervisor(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                nativeTransitionBase(project, profile, session),
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         squadnamespace.ID(profile, session),
		AttemptID:           "attempt-abc",
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
		Supervisor:          supervisorIdentityPlaceholder,
		NativeBinding:       validNativeBinding(squadnamespace.ID(profile, session), "attempt-abc"),
	})
	if err == nil {
		t.Fatal("the literal placeholder must be REFUSED as a supervisor identity")
	}
	if !strings.Contains(err.Error(), "still the literal placeholder") {
		t.Errorf("refusal must name the placeholder, got: %v", err)
	}
}

// REDELIVER MUST NOT CARRY A SUPERVISOR, the same asymmetry as PauseGeneration.
//
// That path holds no assessment and no supervising actor, so a value there was invented rather than
// observed. Refusing is what keeps "supervisor present" a reliable signal that a record came from the
// supervised path -- if redelivery could carry one, the field would stop distinguishing the two origins.
func TestRedeliverReservationRefusesASuppliedSupervisor(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: resumeGoalTransitionRecord{
			Project: project, Profile: profile, Session: session,
			OriginalAttemptID: "attempt-original", OriginalBindingDigest: "binding-digest-1",
			NewAttemptID: "attempt-fresh",
		},
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     "attempt-original",
		BindingDigest: "binding-digest-1",
		Supervisor:    "cto",
	})
	if err == nil {
		t.Fatal("a redeliver reservation carrying a supervisor must be REFUSED: that path has no " +
			"supervising actor, so the value was invented")
	}
	if !strings.Contains(err.Error(), "must NOT carry a supervisor") {
		t.Errorf("refusal must name the supervisor, got: %v", err)
	}
}

// ANTI-VACUITY CONTROL for the row above. Without it, that row would pass even if redeliver refused
// EVERYTHING -- including the supervisor-less form production actually writes.
func TestRedeliverReservationStillSucceedsWithoutASupervisor(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	record, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: resumeGoalTransitionRecord{
			Project: project, Profile: profile, Session: session,
			OriginalAttemptID: "attempt-original", OriginalBindingDigest: "binding-digest-1",
			NewAttemptID: "attempt-fresh",
		},
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     "attempt-original",
		BindingDigest: "binding-digest-1",
	})
	if err != nil {
		t.Fatalf("the supervisor-less redeliver form production writes must still be accepted: %v", err)
	}
	if record.Supervisor != "" {
		t.Errorf("Supervisor = %q on a redeliver record, want empty -- omitempty keeps it absent from "+
			"the round trip so a reader cannot mistake it for a deliberate value", record.Supervisor)
	}
}

// FALSIFIER for the route my first version left open: a supervisor smuggled through BASE on a redeliver.
//
// DISTINCT FROM TestRedeliverReservationRefusesASuppliedSupervisor, and the distinction is the whole
// point: that row supplies `in.Supervisor` and my original guard caught it. This one leaves in.Supervisor
// EMPTY and puts the value on Base, which `record := in.Base` copies straight into the returned record. My
// first version persisted it -- so the asymmetry was defeated through the door I left open, and a
// redeliver record would have carried a supervisor, making "supervisor present" an unreliable signal that
// a record came from the supervised path.
func TestRedeliverReservationRefusesASupervisorSmuggledThroughBase(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: resumeGoalTransitionRecord{
			Project: project, Profile: profile, Session: session,
			OriginalAttemptID: "attempt-original", OriginalBindingDigest: "binding-digest-1",
			NewAttemptID: "attempt-fresh",
			// The smuggled value. in.Supervisor is deliberately left EMPTY.
			Supervisor: "smuggled-supervisor",
		},
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     "attempt-original",
		BindingDigest: "binding-digest-1",
	})
	if err == nil {
		t.Fatal("a supervisor arriving via Base must be REFUSED on redeliver; guarding only the explicit " +
			"input leaves the Base route open and persists an invented supervisor")
	}
	if !strings.Contains(err.Error(), "must NOT carry a supervisor") {
		t.Errorf("refusal must name the supervisor, got: %v", err)
	}
	// The refusal must NAME THE ROUTE, so a caller can tell which of the two doors it came through.
	if !strings.Contains(err.Error(), "Base record") {
		t.Errorf("refusal must identify the Base route so the caller knows where the value came from, got: %v", err)
	}
	// NOTHING PERSISTED. A refusal that still returned a usable record and path would let a caller write
	// it anyway, which is the return-value-versus-reality class one layer down.
	if record.Supervisor != "" || path != "" {
		t.Errorf("a refused construction must return no usable record or path; got supervisor=%q path=%q",
			record.Supervisor, path)
	}
}

// WHITESPACE-ONLY is refused too, which is dev-2's sharper catch: `omitempty` does NOT omit a
// whitespace-only string, because the raw value is nonempty. So a trimmed guard would let it through and
// the record would serialise a present-but-meaningless supervisor -- an answer-shaped non-answer, the same
// defect class as the placeholder.
func TestRedeliverReservationRefusesAWhitespaceOnlySupervisor(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: resumeGoalTransitionRecord{
			Project: project, Profile: profile, Session: session,
			OriginalAttemptID: "attempt-original", OriginalBindingDigest: "binding-digest-1",
			NewAttemptID: "attempt-fresh",
			Supervisor:   "   ",
		},
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     "attempt-original",
		BindingDigest: "binding-digest-1",
	})
	if err == nil {
		t.Fatal("a whitespace-only supervisor must be REFUSED: omitempty keeps it in the JSON because the " +
			"raw string is nonempty, so a trimmed guard would persist a meaningless value")
	}
}

// NATIVE: a DISAGREEING Base.Supervisor is refused rather than silently overwritten.
//
// The overwrite would have been SAFE -- the validated input wins, so the persisted attribution is correct
// and nothing breaks. That is precisely why it is worth refusing: a caller passing a different supervisor
// through Base believes something false about what this record will say, and silently correcting them hides
// a caller bug that surfaces somewhere less observable. Same rule as the attempt id one file over.
//
// AGREEING values are fine, and the second half of this row proves the guard is not simply "refuse any
// Base value" -- which would be a stricter rule that breaks a legitimate caller.
func TestNativeReservationRefusesADisagreeingBaseSupervisor(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	base := func(supervisor string) resumeGoalTransitionRecord {
		b := nativeTransitionBase(project, profile, session)
		b.Supervisor = supervisor
		return b
	}
	input := func(b resumeGoalTransitionRecord) recoveryTransitionInput {
		return recoveryTransitionInput{
			Base:                b,
			Kind:                recoveryTransitionKindNativeGoalResume,
			NamespaceID:         squadnamespace.ID(profile, session),
			AttemptID:           "attempt-abc",
			PauseGeneration:     "pause-gen-1",
			PreclaimFingerprint: "fingerprint-1",
			Supervisor:          "cto",
			NativeBinding:       validNativeBinding(squadnamespace.ID(profile, session), "attempt-abc"),
		}
	}

	_, _, err := newRecoveryTransitionRecord(input(base("someone-else")))
	if err == nil {
		t.Fatal("a Base.Supervisor disagreeing with the validated supervisor must be REFUSED, not " +
			"silently overwritten -- the overwrite is safe, which is what makes it hide a caller bug")
	}
	if !strings.Contains(err.Error(), "disagrees with the Base-supplied") {
		t.Errorf("refusal must name the disagreement, got: %v", err)
	}

	// AGREEING is accepted: the guard must not degenerate into "refuse any Base value", which would
	// reject a legitimate caller that populated Base consistently.
	record, _, agreeErr := newRecoveryTransitionRecord(input(base("cto")))
	if agreeErr != nil {
		t.Fatalf("an AGREEING Base.Supervisor must be accepted: %v", agreeErr)
	}
	if record.Supervisor != "cto" {
		t.Errorf("Supervisor = %q, want %q", record.Supervisor, "cto")
	}
}

// dev-2's THIRD instance of the Base-route class: a PauseGeneration smuggled through Base on a redeliver.
//
// DISTINGUISHABLE FROM TestRedeliverInputCarryingAPauseGenerationIsRefused (recovery_transition_roundtrip_test.go),
// which supplies the EXPLICIT input and was always caught. This row leaves in.PauseGeneration EMPTY and puts
// the value on Base, which `record := in.Base` copies straight into the returned record.
//
// THE UNCOMFORTABLE PART: that older guard is the one I used as the TEMPLATE when adding the Supervisor
// field, so it propagated its own defect into the new field. The Supervisor bug was inherited, not
// independently invented -- which is why the fix is the whole class plus a stated posture rule, not a third
// one-off patch.
func TestRedeliverReservationRefusesAPauseGenerationSmuggledThroughBase(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	record, path, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: resumeGoalTransitionRecord{
			Project: project, Profile: profile, Session: session,
			OriginalAttemptID: "attempt-original", OriginalBindingDigest: "binding-digest-1",
			NewAttemptID:    "attempt-fresh",
			PauseGeneration: "smuggled-pause",
		},
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     "attempt-original",
		BindingDigest: "binding-digest-1",
		// in.PauseGeneration intentionally empty.
	})
	if err == nil {
		t.Fatal("a pause generation arriving via Base must be REFUSED on redeliver: that path holds no " +
			"assessment, so the value was recomputed or invented")
	}
	if !strings.Contains(err.Error(), "must NOT carry a pause generation") {
		t.Errorf("refusal must name the pause generation, got: %v", err)
	}
	if !strings.Contains(err.Error(), "Base record") {
		t.Errorf("refusal must identify the Base route, got: %v", err)
	}
	if record.PauseGeneration != "" || path != "" {
		t.Errorf("a refused construction must return no usable record or path; got pause=%q path=%q",
			record.PauseGeneration, path)
	}
}

// WHITESPACE-ONLY through Base, the omitempty case: a raw nonempty string still serialises.
func TestRedeliverReservationRefusesAWhitespaceOnlyPauseGeneration(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: resumeGoalTransitionRecord{
			Project: project, Profile: profile, Session: session,
			OriginalAttemptID: "attempt-original", OriginalBindingDigest: "binding-digest-1",
			NewAttemptID:    "attempt-fresh",
			PauseGeneration: "  ",
		},
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     "attempt-original",
		BindingDigest: "binding-digest-1",
	})
	if err == nil {
		t.Fatal("a whitespace-only pause generation must be REFUSED: omitempty keeps it in the JSON " +
			"because the raw string is nonempty")
	}
}

// NATIVE: the two remaining derived fields refuse a DISAGREEING Base value, completing the class.
//
// Both were silent overwrites until this fix. Each row also asserts the AGREEING case is accepted, so
// neither guard can degenerate into "refuse any Base value" and reject a consistent caller.
func TestNativeReservationRefusesDisagreeingBaseDerivedFields(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()

	build := func(mutate func(*recoveryTransitionInput)) (resumeGoalTransitionRecord, error) {
		in := recoveryTransitionInput{
			Base:                nativeTransitionBase(project, profile, session),
			Kind:                recoveryTransitionKindNativeGoalResume,
			NamespaceID:         squadnamespace.ID(profile, session),
			AttemptID:           "attempt-abc",
			PauseGeneration:     "pause-gen-1",
			PreclaimFingerprint: "fingerprint-1",
			Supervisor:          "cto",
			NativeBinding:       validNativeBinding(squadnamespace.ID(profile, session), "attempt-abc"),
		}
		mutate(&in)
		rec, _, err := newRecoveryTransitionRecord(in)
		return rec, err
	}

	for _, row := range []struct {
		name    string
		mutate  func(*recoveryTransitionInput)
		wantErr string
	}{
		{"disagreeing Base pause generation",
			func(in *recoveryTransitionInput) { in.Base.PauseGeneration = "someone-elses-pause" },
			"pause generation"},
		{"disagreeing Base preclaim fingerprint",
			func(in *recoveryTransitionInput) { in.Base.PreclaimFingerprint = "someone-elses-fingerprint" },
			"preclaim fingerprint"},
	} {
		_, err := build(row.mutate)
		if err == nil {
			t.Errorf("%s: must be REFUSED rather than silently overwritten", row.name)
			continue
		}
		if !strings.Contains(err.Error(), row.wantErr) || !strings.Contains(err.Error(), "disagrees with the Base-supplied") {
			t.Errorf("%s: refusal must name the field and the disagreement, got: %v", row.name, err)
		}
	}

	// AGREEING values are accepted, for both fields at once.
	rec, err := build(func(in *recoveryTransitionInput) {
		in.Base.PauseGeneration = "pause-gen-1"
		in.Base.PreclaimFingerprint = "fingerprint-1"
	})
	if err != nil {
		t.Fatalf("AGREEING Base values must be accepted, else the guard rejects a consistent caller: %v", err)
	}
	if rec.PauseGeneration != "pause-gen-1" || rec.PreclaimFingerprint != "fingerprint-1" {
		t.Errorf("agreeing values must persist; got pause=%q fingerprint=%q",
			rec.PauseGeneration, rec.PreclaimFingerprint)
	}
}

// THE FLAT LAUNCH GENERATION IS REQUIRED FOR NATIVE, one row per field.
//
// These exist because the requirement and its falsifiers must land together. I shipped the publication
// validator with no falsifiers at all and dev-2 found a hole in it by reading; a required field whose
// absence nothing tests is the same thing one layer down -- a guard that is only as good as my reading of
// it. Each row removes EXACTLY ONE field from a complete input, so a row can only pass because that
// specific requirement fired.
//
// WHY THE FIELDS MATTER: the record is the recovery contract. A reserved native claim that does not say
// which launch generation it was made against cannot be revalidated after a crash, so a relaunched runtime
// looks identical to the one the claim authorized -- and the pane can be perfectly live, managed and idle
// while belonging to a different process entirely. That is the one drift the U5 liveness gates cannot see.
func TestNativeReservationRequiresTheFlatLaunchGeneration(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	project := t.TempDir()

	for _, row := range []struct {
		name    string
		break1  func(*recoveryTransitionInput)
		wantErr string
	}{
		{"missing launch id", func(in *recoveryTransitionInput) { in.Base.LaunchID = "" }, "launch id"},
		{"missing launch record digest", func(in *recoveryTransitionInput) { in.Base.LaunchRecordDigest = "" },
			"launch record digest"},
		// ZERO is the absent value, and it is the dangerous one: it is what an unset field looks like, so a
		// lenient writer would persist a generation no stat call could ever reproduce.
		{"zero launch record mod time", func(in *recoveryTransitionInput) { in.Base.LaunchRecordModTime = 0 },
			"positive launch record mod time"},
		// A NEGATIVE modtime is not a plausible observation either. Without this row a guard written as
		// `== 0` would pass while accepting a value no filesystem produces.
		{"negative launch record mod time", func(in *recoveryTransitionInput) { in.Base.LaunchRecordModTime = -1 },
			"positive launch record mod time"},
	} {
		in := completeNativeTransitionInput(project, profile, session, attemptID)
		row.break1(&in)
		_, _, err := newRecoveryTransitionRecord(in)
		if err == nil {
			t.Errorf("%s: a native reservation missing its launch generation must REFUSE -- the claim it "+
				"would write cannot be revalidated against the runtime it was made against", row.name)
			continue
		}
		if !strings.Contains(err.Error(), row.wantErr) {
			t.Errorf("%s: refusal must name the field, got: %v", row.name, err)
		}
	}
}

// THE ANTI-VACUITY CONTROL for the rows above: the complete fixture must be ACCEPTED.
//
// Without it, all four rows would be satisfied by a constructor that refused every native input, which is
// precisely the failure mode that makes a wall of refusal tests worthless.
func TestTheCompleteNativeFixtureIsAccepted(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	project := t.TempDir()

	record, path, err := newRecoveryTransitionRecord(
		completeNativeTransitionInput(project, profile, session, attemptID))
	if err != nil {
		t.Fatalf("the shared complete native fixture must be accepted: %v\nEvery single-defect row in this "+
			"package derives from this fixture, so if it is refused, all of them pass for the wrong reason.", err)
	}
	// The persisted launch generation must be the one supplied, not a default.
	if record.LaunchID != fixtureLaunchID || record.LaunchRecordDigest != fixtureLaunchDigest ||
		record.LaunchRecordModTime != fixtureLaunchModTime {
		t.Errorf("the launch generation was not persisted as supplied: got %q/%q/%d",
			record.LaunchID, record.LaunchRecordDigest, record.LaunchRecordModTime)
	}
	if path == "" {
		t.Error("constructor returned an empty path for an accepted native input")
	}
}

// ===== U7 PHASE 1: the exact-binding block =====
//
// validNativeBinding is the all-valid block, shared so every refusal row below differs from it in exactly
// ONE field. A row invalid in two dimensions cannot tell you which validation refused.
func validNativeBinding(namespaceID, attemptID string) *resumeGoalNativeBinding {
	return &resumeGoalNativeBinding{
		NamespaceID: namespaceID,
		PaneID:      "%7",
		// THE MODE PRODUCTION ACTUALLY EMITS. This said "native", which no path produces: eligibility
		// (goal_supervision.go:334) requires exactly "native_goal_blocked". The constructor only checks
		// GoalMode for NONBLANKNESS, so the wrong value passed every guard -- the SAME defect class as the
		// "safe_auto" lookalike noted four lines below, in the same helper, one field apart. Two instances
		// of one class in one struct is why fixtures now come from production values rather than being
		// invented next to the assertion that needs them.
		GoalMode:                fixtureNativeGoalMode,
		GoalAttemptID:           attemptID,
		GoalBindingDigest:       "binding-digest-1",
		GoalCommandDigest:       "command-digest-1",
		BlockerID:               "blocker-1",
		BlockerResolutionDigest: "resolution-digest-1",
		// The REAL constant, not a lookalike literal. I first hardcoded "safe_auto" with an underscore
		// while production uses "safe-auto" with a hyphen. The constructor only checks nonblank here so it
		// passed -- a fixture carrying a value production never emits, which is the unreachable-state
		// problem in milder form: green, and describing a system that does not exist.
		PolicyMode:     team.GoalSupervisionSafeAuto,
		PolicyRevision: 1,
	}
}

// THE ANTI-VACUITY CONTROL, and it is the row that replaces the deleted blocker-less-passes row.
//
// That deleted row asserted a state the system CANNOT PRODUCE -- eligibility requires nonblank Blocker.ID
// and ResolutionDigest -- so it would have pinned support for an impossibility and gone red when someone
// correctly tightened the constructor. This control proves the guards do not refuse everything using a
// state production actually produces.
func TestNativeReservationAcceptsTheCompleteExactBinding(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	project := t.TempDir()
	ns := squadnamespace.ID(profile, session)

	record, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                nativeTransitionBase(project, profile, session),
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         ns,
		AttemptID:           attemptID,
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
		Supervisor:          "cto",
		NativeBinding:       validNativeBinding(ns, attemptID),
	})
	if err != nil {
		t.Fatalf("the complete ratified binding must be accepted: %v", err)
	}
	if record.NativeBinding == nil {
		t.Fatal("record.NativeBinding is nil after an accepted native construction")
	}
	if record.SchemaVersion != resumeGoalTransitionSchemaVersion {
		t.Errorf("SchemaVersion = %d, want %d -- native records persisted ZERO before this change while "+
			"four readers validate nonzero", record.SchemaVersion, resumeGoalTransitionSchemaVersion)
	}
	// FRESH VALUE, NOT AN ALIAS: mutating the caller's struct afterwards must not reach the record.
	// A pointer assignment would leave the record aliasing memory the caller still holds, so the
	// validation would expire the moment the caller touched it again.
	supplied := validNativeBinding(ns, attemptID)
	rec2, _, err2 := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                nativeTransitionBase(project, profile, session),
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         ns,
		AttemptID:           attemptID,
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
		Supervisor:          "cto",
		NativeBinding:       supplied,
	})
	if err2 != nil {
		t.Fatalf("second construction: %v", err2)
	}
	supplied.PaneID = "%999"
	if rec2.NativeBinding.PaneID == "%999" {
		t.Error("the record ALIASES the caller's binding: a caller mutation reached a validated record, " +
			"so the validation expired the moment the caller touched its own struct again")
	}
}

// EVERY REQUIRED FIELD REFUSES WHEN BLANK, one dimension at a time.
//
// The blocker rows are the ones I originally proposed as OPTIONAL. dev-2 disproved that premise from the
// executable contract (goal_supervision.go:341-342 make both nonblank a condition of ELIGIBILITY, and the
// executor refuses ineligible assessments before reserve), so accepting empty would let a gate-skipping
// caller write a record the executor could never authorize.
func TestNativeExactBindingRefusesEachMissingField(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	project := t.TempDir()
	ns := squadnamespace.ID(profile, session)

	for _, row := range []struct {
		name    string
		blank   func(*resumeGoalNativeBinding)
		wantErr string
	}{
		{"pane id", func(b *resumeGoalNativeBinding) { b.PaneID = "" }, "pane id"},
		{"goal mode", func(b *resumeGoalNativeBinding) { b.GoalMode = "" }, "goal mode"},
		{"goal binding digest", func(b *resumeGoalNativeBinding) { b.GoalBindingDigest = "" }, "goal binding digest"},
		{"goal command digest", func(b *resumeGoalNativeBinding) { b.GoalCommandDigest = "" }, "goal command digest"},
		{"blocker id", func(b *resumeGoalNativeBinding) { b.BlockerID = "" }, "blocker id"},
		{"blocker resolution digest", func(b *resumeGoalNativeBinding) { b.BlockerResolutionDigest = "" }, "blocker resolution digest"},
		{"policy mode", func(b *resumeGoalNativeBinding) { b.PolicyMode = "" }, "policy mode"},
		// The expected substring follows the SHARED CONTRACT's wording, which is deliberately more specific
		// than the constructor's old message: interior refusals are prefixed "native binding ..." so an
		// operator can tell an interior failure from a flat one. Ruled to change the TEST rather than weaken
		// the message -- trading diagnostic value for a green substring is backwards.
		{"policy revision zero", func(b *resumeGoalNativeBinding) { b.PolicyRevision = 0 },
			"positive native binding policy revision"},
	} {
		nb := validNativeBinding(ns, attemptID)
		row.blank(nb)
		_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
			Base:                nativeTransitionBase(project, profile, session),
			Kind:                recoveryTransitionKindNativeGoalResume,
			NamespaceID:         ns,
			AttemptID:           attemptID,
			PauseGeneration:     "pause-gen-1",
			PreclaimFingerprint: "fingerprint-1",
			Supervisor:          "cto",
			NativeBinding:       nb,
		})
		if err == nil {
			t.Errorf("%s: a blank required binding field must REFUSE", row.name)
			continue
		}
		if !strings.Contains(err.Error(), row.wantErr) {
			t.Errorf("%s: refusal must name the field, got: %v", row.name, err)
		}
	}
}

// THE ONE COLLAPSED SIDE DOOR: a Base-supplied binding block is refused on BOTH kinds, even when the
// values would have been valid. Nesting turned fifteen potential Base doors into one; this proves the one
// is locked, which is the difference between concentrating risk and removing it.
func TestBaseSuppliedNativeBindingIsRefusedOnBothKinds(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const attemptID = "attempt-abc"
	project := t.TempDir()
	ns := squadnamespace.ID(profile, session)

	for _, row := range []struct {
		name string
		in   recoveryTransitionInput
	}{
		{"native", recoveryTransitionInput{
			Base: resumeGoalTransitionRecord{
				Project: project, Profile: profile, Session: session,
				NativeBinding: validNativeBinding(ns, attemptID),
			},
			Kind: recoveryTransitionKindNativeGoalResume, NamespaceID: ns, AttemptID: attemptID,
			PauseGeneration: "pause-gen-1", PreclaimFingerprint: "fingerprint-1", Supervisor: "cto",
			NativeBinding: validNativeBinding(ns, attemptID),
		}},
		{"redeliver", recoveryTransitionInput{
			Base: resumeGoalTransitionRecord{
				Project: project, Profile: profile, Session: session,
				OriginalAttemptID: "attempt-original", OriginalBindingDigest: "binding-digest-1", NewAttemptID: "attempt-fresh",
				NativeBinding: validNativeBinding(ns, attemptID),
			},
			Kind: recoveryTransitionKindRedeliver, AttemptID: "attempt-original", BindingDigest: "bd-1",
		}},
	} {
		record, path, err := newRecoveryTransitionRecord(row.in)
		if err == nil {
			t.Errorf("%s: a Base-supplied native binding must be REFUSED even when its values are valid, "+
				"because there is no legitimate Base source for a block the constructor alone populates", row.name)
			continue
		}
		if !strings.Contains(err.Error(), "must not carry a Base-supplied native binding") {
			t.Errorf("%s: refusal must name the Base binding, got: %v", row.name, err)
		}
		if record.NativeBinding != nil || path != "" {
			t.Errorf("%s: refused construction must return no usable record or path", row.name)
		}
	}
}

// REDELIVER MUST NOT CARRY THE BLOCK through the explicit input either, and the anti-vacuity half proves
// redeliver still succeeds without it -- the form production actually writes.
func TestRedeliverRefusesAnExplicitNativeBindingButSucceedsWithout(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()
	// THE DIGEST MUST MATCH THE INPUT'S ("bd-1", not the "binding-digest-1" used elsewhere in this file).
	// The shared contract RECOMPUTES the redeliver identity from the record's own OriginalAttemptID and
	// OriginalBindingDigest, so a Base digest that disagreed with the input's would refuse on the identity
	// check and this row's anti-vacuity half would fail for a reason unrelated to native bindings. I hit
	// exactly that by bulk-adding one digest value across the file -- the mechanical edit that fixed seven
	// sites broke the eighth, which is why the check below is a script and not a reread.
	base := resumeGoalTransitionRecord{
		Project: project, Profile: profile, Session: session,
		OriginalAttemptID: "attempt-original", OriginalBindingDigest: "bd-1", NewAttemptID: "attempt-fresh",
	}

	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: base, Kind: recoveryTransitionKindRedeliver,
		AttemptID: "attempt-original", BindingDigest: "bd-1",
		NativeBinding: validNativeBinding(squadnamespace.ID(profile, session), "attempt-original"),
	})
	if err == nil {
		t.Fatal("redeliver must REFUSE an explicit native binding: that block is the assessment's exact " +
			"binding and this path holds no assessment")
	}
	if !strings.Contains(err.Error(), "must NOT carry a native binding") {
		t.Errorf("refusal must name the native binding, got: %v", err)
	}

	rec, _, okErr := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base: base, Kind: recoveryTransitionKindRedeliver,
		AttemptID: "attempt-original", BindingDigest: "bd-1",
	})
	if okErr != nil {
		t.Fatalf("the binding-less redeliver form production writes must still be accepted: %v", okErr)
	}
	if rec.NativeBinding != nil {
		t.Error("redeliver record carries a native binding; the pointer must stay nil so omitempty omits it")
	}
}

// THE INTENTIONAL DUAL'S INVARIANT. GoalAttemptID and the flat NewAttemptID legitimately carry one value
// ONLY because equality is enforced; without the check they would be two of the five accidental duplicates
// dev-2 rejected from my draft.
func TestNativeBindingAttemptIdentityMustEqualTheReservationAttempt(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	project := t.TempDir()
	ns := squadnamespace.ID(profile, session)

	nb := validNativeBinding(ns, "attempt-from-somewhere-else")
	_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:                nativeTransitionBase(project, profile, session),
		Kind:                recoveryTransitionKindNativeGoalResume,
		NamespaceID:         ns,
		AttemptID:           "attempt-abc",
		PauseGeneration:     "pause-gen-1",
		PreclaimFingerprint: "fingerprint-1",
		Supervisor:          "cto",
		NativeBinding:       nb,
	})
	if err == nil {
		t.Fatal("a native binding whose goal attempt id differs from the reservation attempt must REFUSE: " +
			"the authorized identity and the lifecycle identity must be the same attempt")
	}
	if !strings.Contains(err.Error(), "disagrees with the reservation attempt") {
		t.Errorf("refusal must name the disagreement, got: %v", err)
	}
}
