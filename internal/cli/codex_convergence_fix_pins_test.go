package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// PINS FOR THE CODEX CONVERGENCE FIXES 2, 3, 4 and 5.
//
// Each row exists because a REAL defect passed the previous suite. Where the old code was structurally
// unable to fail a check, that is stated at the row, so a reader can see which assertion is load-bearing
// rather than inferring it from the name.

// ============================================================================================
// FINDING 4: the durable contract accepted a NewAttemptID its own delivery reader rejects.
// ============================================================================================

// validRedeliverBaseForAttempt returns the redeliver base the constructor accepts, with NewAttemptID as the
// ONE varied field.
//
// Copied in shape from recovery_transition_roundtrip_test.go's base rather than trimmed down: a minimal
// record would refuse on some OTHER missing field and the row would pass without ever reaching the attempt-id
// check, which is the void-proof failure this whole milestone keeps catching.
func validRedeliverBaseForAttempt(project, profile, session, newAttemptID string) resumeGoalTransitionRecord {
	const goalText = "ship it"
	return resumeGoalTransitionRecord{
		SchemaVersion:         resumeGoalTransitionSchemaVersion,
		Project:               project,
		Profile:               profile,
		Session:               session,
		Role:                  "cto",
		Handle:                "cto",
		MemberSession:         session,
		MemberCWD:             project,
		MemberBinary:          "codex",
		GoalDigest:            digestBytes([]byte(goalText)),
		OriginalAttemptID:     "attempt-abc",
		OriginalBindingDigest: "digest-def",
		OriginalAttemptDigest: "attempt-digest",
		OriginalClaimDigest:   "claim-digest",
		NewAttemptID:          newAttemptID,
		LaunchID:              "launch-1",
		LaunchStartedAt:       time.Now().UTC(),
		TeamRecordDigest:      "team-digest",
		TeamRecordModTime:     1,
		LaunchRecordDigest:    "launch-digest",
		LaunchRecordModTime:   1,
		CreatedAt:             time.Now().UTC(),
	}
}

// TestContractRefusesANewAttemptIDItsDeliveryReaderRejects is the writer/reader agreement pin.
//
// WHAT PASSED BEFORE: "../new" is nonblank, and for redeliver it also differs from the original attempt, so
// every writer-side check accepted it and the record PUBLISHED -- while resume_goal.go:1176 refuses it via
// goalAttemptPath at delivery. The result was durable evidence nothing could consume: the claim blocks at the
// claim-once gate forever, and no code path deletes reservations.
//
// BOTH KINDS, because the finding was reported against redeliver only and native shared the hole through a
// different derivation: native hashes NewAttemptID into its claim key, and a hash accepts any string.
func TestContractRefusesANewAttemptIDItsDeliveryReaderRejects(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"

	// Every value here is one the READER refuses. The loop below proves that claim rather than assuming it,
	// so this table cannot drift away from goalAttemptPath's actual rule.
	for _, unsafe := range []string{"../new", "sub/dir", "..", ".", `back\slash`} {
		t.Run("refuses "+unsafe, func(t *testing.T) {
			project := t.TempDir()

			// THE READER'S VERDICT FIRST. If goalAttemptPath ever started accepting one of these, the row below
			// would be testing a stricter writer than reader -- the opposite defect, and equally worth failing.
			if _, err := goalAttemptPath(project, profile, session, unsafe); err == nil {
				t.Fatalf("fixture is void: the delivery reader ACCEPTS %q, so the writer refusing it would be a "+
					"writer/reader disagreement in the other direction", unsafe)
			}

			t.Run("native", func(t *testing.T) {
				in := completeNativeTransitionInput(project, profile, session, unsafe)
				if _, _, err := newRecoveryTransitionRecord(in); err == nil {
					t.Fatalf("the constructor PUBLISHED a native claim whose attempt id %q the delivery path "+
						"refuses. That record is durable evidence nothing can consume: the pause blocks at the "+
						"claim-once gate permanently and nothing deletes reservations.", unsafe)
				} else if !strings.Contains(err.Error(), "delivery reader accepts") {
					t.Errorf("refusal must name the writer/reader disagreement, got: %v", err)
				}
			})

			t.Run("redeliver", func(t *testing.T) {
				_, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
					Base:          validRedeliverBaseForAttempt(project, profile, session, unsafe),
					Kind:          recoveryTransitionKindRedeliver,
					AttemptID:     "attempt-abc",
					BindingDigest: "digest-def",
				})
				if err == nil {
					t.Fatalf("the constructor PUBLISHED a redeliver record whose new attempt id %q the delivery "+
						"path refuses at resume_goal.go:1176", unsafe)
				}
				if !strings.Contains(err.Error(), "delivery reader accepts") {
					t.Errorf("refusal must name the writer/reader disagreement, got: %v", err)
				}
			})
		})
	}
}

// ANTI-VACUITY FOR THE ROW ABOVE: the same fixtures with a READER-ACCEPTED attempt id must still be accepted.
//
// Without this, a contract that refused every attempt id would satisfy the refusal rows completely, and the
// fix would read as correct while making all recovery impossible.
func TestContractStillAcceptsAReaderValidNewAttemptID(t *testing.T) {
	const profile = "squad"
	const session = "v2-25-0"
	const safe = "attempt-xyz"
	project := t.TempDir()

	if _, err := goalAttemptPath(project, profile, session, safe); err != nil {
		t.Fatalf("fixture is void: the reader refuses %q, so acceptance below would prove nothing: %v", safe, err)
	}
	if _, _, err := newRecoveryTransitionRecord(
		completeNativeTransitionInput(project, profile, session, safe)); err != nil {
		t.Errorf("native claim with a valid attempt id was refused: %v", err)
	}
	if _, _, err := newRecoveryTransitionRecord(recoveryTransitionInput{
		Base:          validRedeliverBaseForAttempt(project, profile, session, safe),
		Kind:          recoveryTransitionKindRedeliver,
		AttemptID:     "attempt-abc",
		BindingDigest: "digest-def",
	}); err != nil {
		t.Errorf("redeliver record with a valid new attempt id was refused: %v", err)
	}
}

// ============================================================================================
// FINDING 2: the migration guard ignored native CONSUMED companions.
// ============================================================================================

// TestPlannerBlocksMigrationWhenTheSourceHoldsAnOrphanConsumedNativeCompanion is the WIRING row for finding 2.
//
// WHY THROUGH THE PLANNER: a helper-direct row would stay green if the production call site were deleted, and
// that exact gap is why M-M1 exists. The through-the-caller rule applies per guard, and this is a new guard.
//
// THE OLD CODE COULD NOT FAIL THIS. recognizeRecoveryTransitionName classifies ".consumed.json" as
// NotATransition, so the guard's single-step recognition ignored the file entirely, the adapter copied it
// content-unchanged with its basename preserved, and in the destination the OLD claim key no longer matches
// the NEW namespace -- so neither the consumed-state blocker nor the orphan-companion blocker can fire. Prior
// consumption becomes invisible to claim-once, and invisible prior delivery is a SECOND delivery.
//
// ORPHAN (no reservation beside it) is the shape that reaches the gap: a present native reservation blocks on
// its own. It is reachable in practice -- the guard's own remediation text used to tell operators to "resolve"
// the claim, and deleting the reservation is how an operator does that.
func TestPlannerBlocksMigrationWhenTheSourceHoldsAnOrphanConsumedNativeCompanion(t *testing.T) {
	fx := newNamespaceMigrationFixture(t)

	goalDir := goalAttemptDir(fx.project, fx.source.Profile, fx.source.Session)
	if err := os.MkdirAll(goalDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// The reservation path comes from the REAL constructor, so the companion is named exactly as production
	// would name it -- then the reservation itself is deliberately NOT written.
	_, claimPath, err := newRecoveryTransitionRecord(
		completeNativeTransitionInput(fx.project, fx.source.Profile, fx.source.Session, "attempt-abc"))
	if err != nil {
		t.Fatalf("the complete native fixture must be accepted: %v", err)
	}
	companionPath := resumeGoalTransitionConsumedPath(claimPath)
	if err := os.WriteFile(companionPath, []byte(`{"transition_id":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// ANTI-VACUITY 1: the reservation must be ABSENT, or this row would be blocked by the pre-existing
	// reservation blocker and would prove nothing about companions.
	if _, statErr := os.Stat(claimPath); statErr == nil {
		t.Fatalf("fixture is void: the reservation %q exists, so the existing blocker would fire instead", claimPath)
	}
	// ANTI-VACUITY 2: the single-step recognition the guard used to rely on must classify this companion as
	// NotATransition. That is the precise reason the old guard ignored it, and asserting it here is what makes
	// this row a regression pin rather than a restatement.
	if _, recognition := recognizeRecoveryTransitionName(filepath.Base(companionPath)); recognition != recoveryNameNotATransition {
		t.Fatalf("fixture is void: %q is no longer classified NotATransition by the name parser, so it does not "+
			"exercise the two-step recognition this fix added", filepath.Base(companionPath))
	}
	// ANTI-VACUITY 3: the companion must land in the directory the planner inspects.
	if filepath.Dir(companionPath) != goalDir {
		t.Fatalf("fixture is void: the companion landed at %q but the planner inspects %q", companionPath, goalDir)
	}

	plan, err := planNamespaceMigration(namespaceMigrationPlannerOptions{
		ProjectDir: fx.project, Source: fx.source, Target: fx.target,
		DryRun: true, Now: time.Now().UTC(), Probe: livenessProbe(nil, nil, time.Now()),
	})
	if err != nil {
		t.Fatalf("planning must succeed and REPORT a blocker rather than erroring: %v", err)
	}

	joined := strings.Join(plan.Blockers, "\n")
	if !strings.Contains(joined, "cannot be migrated safely") {
		t.Fatalf("the PLAN did not block a namespace holding an orphan CONSUMED native companion.\n"+
			"The adapter copies it unchanged with its basename, carrying the old namespace-derived claim key "+
			"into the new namespace, where nothing matches it -- so the prior consumption disappears from the "+
			"claim-once decision and a second delivery is permitted.\nblockers:\n%s", joined)
	}
	if !strings.Contains(joined, "COMPANION") {
		t.Errorf("the blocker must say it is companion evidence, so an operator knows a reservation is NOT "+
			"what they are looking for; got:\n%s", joined)
	}
	if !strings.Contains(joined, filepath.Base(companionPath)) {
		t.Errorf("the blocker must NAME the offending companion; got:\n%s", joined)
	}
}

// A transition-LIKE companion that cannot be classified must block too: unknown is not absent applies to
// companion names for the same reason it applies to reservations.
func TestMigrationRefusesAMalformedTransitionLikeCompanion(t *testing.T) {
	dir := t.TempDir()
	companion := filepath.Join(dir, currentRecoveryTransitionPrefix+"not-a-valid-body.consumed.json")
	if err := os.WriteFile(companion, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The fixture must actually reach the malformed branch THROUGH the companion step: base+".json" is what
	// the guard now recognises, so that is what this assertion checks.
	base, ok := companionReservationBase(filepath.Base(companion))
	if !ok {
		t.Fatalf("fixture is void: %q is not recognised as a companion", filepath.Base(companion))
	}
	if _, recognition := recognizeRecoveryTransitionName(base + ".json"); recognition != recoveryNameMalformed {
		t.Fatalf("fixture is void: companion base %q does not classify as Malformed", base)
	}

	var blockers []string
	inspectNamespaceNativeRecoveryClaims(dir, &blockers)
	if len(blockers) == 0 {
		t.Fatal("an unclassifiable transition-like COMPANION raised no blocker. It might be consumption " +
			"evidence for a native claim, and migrating it corrupts exactly the record we failed to read.")
	}
	joined := strings.Join(blockers, "\n")
	if !strings.Contains(joined, "unknown is not absent") {
		t.Errorf("blocker must give the unknown-is-not-absent reason, got: %s", joined)
	}
	if !strings.Contains(joined, "COMPANION") {
		t.Errorf("blocker must identify it as a companion, got: %s", joined)
	}
}

// THE NON-OVERBLOCKING HALF, and it is not optional: a LEGACY companion must NOT block.
//
// Legacy identities are not namespace-derived, so the adapter's rewrite is safe for them. A guard that
// blocked these would make every namespace that has ever run a redelivery unmigratable -- strictly worse than
// the corruption the guard was added to prevent, and the failure mode a refusal-only test cannot see.
func TestMigrationAllowsALegacyConsumedCompanion(t *testing.T) {
	dir := t.TempDir()
	legacyBase := legacyRecoveryTransitionPrefix + strings.Repeat("c", 64)
	companion := filepath.Join(dir, legacyBase+".consumed.json")
	if err := os.WriteFile(companion, []byte(`{"transition_id":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// Anti-vacuity: prove the guard's own two-step recognition sees this as a recognized LEGACY claim rather
	// than ignoring the name outright, or the pass below would prove nothing about legacy tolerance.
	base, ok := companionReservationBase(filepath.Base(companion))
	if !ok {
		t.Fatalf("fixture is void: %q is not recognised as a companion", filepath.Base(companion))
	}
	parsed, recognition := recognizeRecoveryTransitionName(base + ".json")
	if recognition != recoveryNameRecognized || !parsed.Legacy {
		t.Fatalf("fixture is void: companion base %q is not a recognized LEGACY claim (recognition=%v legacy=%t)",
			base, recognition, parsed.Legacy)
	}

	var blockers []string
	inspectNamespaceNativeRecoveryClaims(dir, &blockers)
	if len(blockers) != 0 {
		t.Fatalf("a LEGACY consumed companion was BLOCKED: %s\nLegacy identities are not namespace-keyed, so "+
			"the adapter rewrite is safe for them; blocking here is an outage for every namespace that ever "+
			"ran a redelivery.", strings.Join(blockers, "\n"))
	}
}

// The remediation text must never tell an operator to delete a reservation, because doing so MANUFACTURES the
// orphan-companion state this guard exists to catch. My original wording said "Resolve or consume the claim",
// and "resolve" is what an operator reads as "delete the file".
func TestTheNativeClaimBlockerNeverAdvisesDeletingAReservation(t *testing.T) {
	dir := t.TempDir()
	project := t.TempDir()
	_, claimPath, err := newRecoveryTransitionRecord(
		completeNativeTransitionInput(project, "squad", "v2-25-0", "attempt-abc"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.Base(claimPath)), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	var blockers []string
	inspectNamespaceNativeRecoveryClaims(dir, &blockers)
	if len(blockers) == 0 {
		t.Fatal("fixture is void: no blocker was raised, so there is no remediation text to check")
	}
	joined := strings.Join(blockers, "\n")
	if !strings.Contains(joined, "do NOT delete the reservation") {
		t.Errorf("the blocker must warn against deleting the reservation; got:\n%s", joined)
	}
	if !strings.Contains(joined, "CONSUME the claim") {
		t.Errorf("the blocker must name consuming as the remedy; got:\n%s", joined)
	}
}

// ============================================================================================
// FINDING 3: the gates did not run immediately before pane input.
// FINDING 5: definite pre-input failures were reported as UNKNOWN.
// ============================================================================================

// TestDeliveryBoundaryRegatesAfterTheDirectiveAndImmediatelyBeforeInput is the ORDER pin, and the order is
// the whole finding.
//
// Ratified text requires both: acceptance line 64 and line 352 say the gates run immediately before PANE
// INPUT, and line 89 says the durable directive comes BEFORE input. The only sequence satisfying both is
// directive -> gates -> input. The old code ran the gates before the closure and then published an `amq send`
// subprocess plus a receipt persist -- unbounded duration -- with no re-check, so FreshUntil could pass, the
// launch generation could change and the pane could go busy between the last check and the bytes.
func TestDeliveryBoundaryRegatesAfterTheDirectiveAndImmediatelyBeforeInput(t *testing.T) {
	assessment, _, payload, _, _ := u6DeliveringFixture(t)

	var order []string
	oldSend := sendPromptToPane
	t.Cleanup(func() { sendPromptToPane = oldSend })
	sendPromptToPane = func(string, string) error {
		order = append(order, "input")
		return nil
	}

	deliver := newSupervisionResumeDelivery(
		func(GoalSupervisionAssessment, string, string) error {
			order = append(order, "directive")
			return nil
		},
		assessment, "supervisor-1",
		func() *supervisionResumeRefusal {
			order = append(order, "gate")
			return nil
		})

	if err := deliver(payload); err != nil {
		t.Fatalf("a delivery whose gate passes must succeed: %v", err)
	}
	want := "directive,gate,input"
	if got := strings.Join(order, ","); got != want {
		t.Fatalf("delivery order was %q, want %q.\nThe gate must sit AFTER the durable directive (U6 line 89) "+
			"and IMMEDIATELY BEFORE pane input (U4.2 line 64, line 352). Any other order leaves an unbounded "+
			"durable send between the last world-check and the bytes.", got, want)
	}
}

// A gate refusal AFTER the directive types nothing, and says so definitively.
func TestGateRefusalAfterTheDirectiveTypesNothingAndIsDefiniteNonDelivery(t *testing.T) {
	assessment, _, payload, _, _ := u6DeliveringFixture(t)

	directivePublished, sends := false, 0
	oldSend := sendPromptToPane
	t.Cleanup(func() { sendPromptToPane = oldSend })
	sendPromptToPane = func(string, string) error { sends++; return nil }

	refusal := &supervisionResumeRefusal{
		Clause:   "the same launch generation ... remains current",
		Detail:   "launch generation changed between the directive and pane input",
		Recovery: "inspect the launch record",
	}
	deliver := newSupervisionResumeDelivery(
		func(GoalSupervisionAssessment, string, string) error { directivePublished = true; return nil },
		assessment, "supervisor-1",
		func() *supervisionResumeRefusal { return refusal },
	)

	err := deliver(payload)
	if err == nil {
		t.Fatal("a post-directive gate refusal must return an error")
	}
	if sends != 0 {
		t.Errorf("pane input happened %d time(s) after the gate refused; the refusal exists precisely to stop "+
			"the bytes", sends)
	}
	if !directivePublished {
		t.Error("fixture is void: the directive did not publish, so this row does not prove the gate runs AFTER it")
	}
	var definite *supervisionDefiniteNonDeliveryError
	if !errors.As(err, &definite) {
		t.Fatalf("a pre-input gate refusal must be typed as DEFINITE non-delivery, not left ambiguous: %v", err)
	}
	if definite.Refusal == nil || definite.Refusal.Clause != refusal.Clause {
		t.Errorf("the gate's own verdict must travel with the error so the operator sees its clause and "+
			"recovery rather than a paraphrase; got %+v", definite.Refusal)
	}
}

// A MISSING GATE IS NOT A PASSING GATE. Without this, the whole re-check could be removed by dropping one
// argument, and every test that supplies a gate would stay green while production validated nothing.
func TestDeliveryBoundaryRefusesWhenNoGateIsSupplied(t *testing.T) {
	assessment, _, payload, _, _ := u6DeliveringFixture(t)

	directivePublished, sends := false, 0
	oldSend := sendPromptToPane
	t.Cleanup(func() { sendPromptToPane = oldSend })
	sendPromptToPane = func(string, string) error { sends++; return nil }

	deliver := newSupervisionResumeDelivery(
		func(GoalSupervisionAssessment, string, string) error { directivePublished = true; return nil },
		assessment, "supervisor-1", nil)

	err := deliver(payload)
	if err == nil {
		t.Fatal("a nil delivery-time gate must REFUSE, never skip: a missing check that silently becomes a " +
			"passing check is the authorization-seam defect")
	}
	if sends != 0 || directivePublished {
		t.Errorf("nothing may happen without a gate (sends=%d directivePublished=%t): the wiring fault is "+
			"detected before any durable or irreversible step", sends, directivePublished)
	}
	var definite *supervisionDefiniteNonDeliveryError
	if !errors.As(err, &definite) {
		t.Errorf("a wiring refusal is proven non-delivery and must be typed as such: %v", err)
	}
}

// FINDING 5, PRODUCER AND CONSUMER IN ONE PRODUCTION-SHAPED ROW: a directive failure is PROVEN
// non-delivery, so the BOUNDARY must emit the typed marker and the EXECUTOR must not report unknown.
//
// WHAT PASSED BEFORE: every error out of the boundary set DeliveryOutcomeUnknown, including this one -- while
// the field's own documentation said it is set "ONLY on the pane-input error path". The operator was sent to
// inspect a pane about a question with no ambiguity in it.
//
// THIS ROW WAS ITSELF DEFECTIVE ON FIRST WRITE, and dev-2's cross-gate caught it. It constructed
// supervisionDefiniteNonDeliveryError directly inside a fake deliver callback, so it pinned only the
// executor's branch: reverting the boundary's publisher-failure path to a plain fmt.Errorf would have left
// this test GREEN. I tested the consumer and ASSERTED the producer -- the wiring-versus-guard defect I had
// named twice today in other people's code, reproduced in my own falsifier for the finding about untested
// distinctions. The deliver argument is now the REAL newSupervisionResumeDelivery, with a pass-through spy
// that forwards the boundary's error verbatim so both halves can be asserted from one run.
func TestDirectiveFailureIsReportedAsProvenNonDeliveryNotUnknown(t *testing.T) {
	assessment, action, payload, reservation, now := u6DeliveringFixture(t)

	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { return nil },
		func(GoalSupervisionAssessment) error { return nil },
	)
	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}

	// The pane-input seam must stay at ZERO: a failed directive means no bytes, and that is the property.
	sends := 0
	oldSend := sendPromptToPane
	t.Cleanup(func() { sendPromptToPane = oldSend })
	sendPromptToPane = func(string, string) error { sends++; return nil }

	directiveFailure := errors.New("audit directive receipt did not persist")
	gateRan := false
	boundary := newSupervisionResumeDelivery(
		func(GoalSupervisionAssessment, string, string) error { return directiveFailure },
		assessment, "supervisor-1",
		func() *supervisionResumeRefusal { gateRan = true; return nil },
	)
	var boundaryErr error
	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loader,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		readReservedRecoveryTransition,
		func(p string) error {
			boundaryErr = boundary(p)
			return boundaryErr
		})

	if err == nil {
		t.Fatal("a failed directive must refuse the delivery")
	}
	// THE PRODUCER HALF. Without this, deleting the definite wrapper in the boundary's publisher-failure
	// branch is invisible to the suite.
	var producedDefinite *supervisionDefiniteNonDeliveryError
	if !errors.As(boundaryErr, &producedDefinite) {
		t.Fatalf("the BOUNDARY did not emit a typed definite-non-delivery error for a failed directive; it "+
			"produced %#v. The executor's branch is then unreachable in production no matter how well it is "+
			"tested.", boundaryErr)
	}
	if !errors.Is(boundaryErr, directiveFailure) {
		t.Errorf("the publisher's own error must remain in the chain so an operator can see WHY the audit "+
			"record failed; got: %v", boundaryErr)
	}
	if sends != 0 {
		t.Errorf("pane input happened %d time(s) despite a failed audit directive; property (1) of the "+
			"boundary is that a directive failure produces ZERO input", sends)
	}
	if gateRan {
		t.Error("the delivery-time gate ran after the directive had already failed; the directive failure " +
			"short-circuits before any further work")
	}
	if outcome.DeliveryOutcomeUnknown {
		t.Error("DeliveryOutcomeUnknown=true for a failure that provably produced ZERO pane input.\n" +
			"The directive is published BEFORE any input, so there are no bytes to be ambiguous about, and " +
			"reporting ambiguity strands the pause in an operator decision that has no question in it.")
	}
	if outcome.Delivered || outcome.Consumed {
		t.Errorf("nothing was delivered or consumed, got Delivered=%t Consumed=%t", outcome.Delivered, outcome.Consumed)
	}
	if !strings.Contains(err.Error(), "BEFORE any pane input") {
		t.Errorf("the refusal must tell the operator no delivery occurred, got: %v", err)
	}
	// THE CLAIM IS NOT RELEASED. Proven non-delivery changes the REPORT only: a self-deleting claim would
	// manufacture the orphan-companion state of finding 2, and nothing in this system deletes reservations.
	if _, statErr := os.Stat(reservation); statErr != nil {
		t.Errorf("the reservation must remain after a proven non-delivery: %v", statErr)
	}
}

// The gate refusal reaches the operator with the gate's OWN clause plus the proven-no-input fact prepended.
func TestPostDirectiveGateRefusalReportsBothTheProofAndTheGateClause(t *testing.T) {
	assessment, action, payload, _, now := u6DeliveringFixture(t)

	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { return nil },
		func(GoalSupervisionAssessment) error { return nil },
	)
	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}

	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loader,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		readReservedRecoveryTransition,
		func(string) error {
			return &supervisionDefiniteNonDeliveryError{
				Detail: "the world changed between the audit directive and pane input, so nothing was typed",
				Refusal: &supervisionResumeRefusal{
					Clause:   "the same launch generation ... remains current",
					Detail:   "launch generation changed between the directive and pane input",
					Recovery: "inspect the launch record before any manual resume",
				},
			}
		})

	if err == nil {
		t.Fatal("a post-directive gate refusal must refuse the delivery")
	}
	if outcome.DeliveryOutcomeUnknown {
		t.Error("a gate refusal happens before any pane input, so it is not an unknown outcome")
	}
	if !strings.Contains(err.Error(), "NO PANE INPUT OCCURRED") {
		t.Errorf("the operator must learn the proven fact first, got: %v", err)
	}
	if !strings.Contains(err.Error(), "launch generation changed") {
		t.Errorf("the gate's own detail must survive, got: %v", err)
	}
	if !strings.Contains(err.Error(), "inspect the launch record") {
		t.Errorf("the gate's own recovery must survive rather than being replaced by a paraphrase, got: %v", err)
	}
}

// THE FIRST-EVER COVERAGE OF supervisionUnknownDeliveryError, through the REAL boundary.
//
// WHY THIS ROW HAD TO EXIST BEFORE THE BATCH COULD PASS: my adjudication of finding 5 said "a typed
// distinction with zero readers and zero tests is a decorative field", and cto's ruling required the type's
// first-ever tests as part of the fix. My initial batch shipped without them -- `rg
// supervisionUnknownDeliveryError internal/cli/*_test.go` still returned nothing -- so the batch left intact
// the precise defect it was adjudicating. dev-2's cross-gate caught that, and this row closes it.
//
// It drives the PRODUCER: a real newSupervisionResumeDelivery, a successful directive, a passing gate, and a
// pane-input seam that returns tmuxpane's two genuinely ambiguous errors. Both must arrive as
// supervisionUnknownDeliveryError with the original error still in the chain, and the executor must report
// UNKNOWN rather than either success or definite failure -- treated as failure a retry could deliver twice,
// treated as success a lost resume looks completed.
func TestBothAmbiguousPaneInputResultsProduceTheTypedUnknownAndReportUnknown(t *testing.T) {
	for _, row := range []struct {
		name       string
		fail       func(paneID string) error
		wantDetail string
	}{
		{
			name:       "queued input, arrival unconfirmed",
			fail:       func(paneID string) error { return &tmuxpane.QueuedInputError{PaneID: paneID} },
			wantDetail: "queued but arrival is unconfirmed",
		},
		{
			// tmuxpane documents this one as EXPLICITLY ambiguous: the text was typed and the submit could not
			// be confirmed, so the resume may well be running. I originally mapped it as an ordinary failure,
			// which invites the retry that delivers twice.
			name:       "typed input, submission unconfirmed",
			fail:       func(paneID string) error { return &tmuxpane.SubmitUnconfirmedError{PaneID: paneID, Attempts: 3} },
			wantDetail: "submission could not be confirmed",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			assessment, action, payload, reservation, now := u6DeliveringFixture(t)
			u6StubSeams(t,
				func(GoalSupervisionAssessment, string, string) error { return nil },
				func(GoalSupervisionAssessment) error { return nil },
			)
			loader := func(GoalSupervisionAssessment) (string, string, error) {
				return payload, digestGoalSupervisionString(payload), nil
			}

			// The seam returns the ambiguous error for the pane the ASSESSMENT names, so the fixture cannot pass
			// by reporting ambiguity about some other pane.
			var raised error
			sends := 0
			oldSend := sendPromptToPane
			t.Cleanup(func() { sendPromptToPane = oldSend })
			sendPromptToPane = func(paneID string, _ string) error {
				sends++
				raised = row.fail(paneID)
				return raised
			}

			directivePublished, gateRan := false, false
			boundary := newSupervisionResumeDelivery(
				func(GoalSupervisionAssessment, string, string) error { directivePublished = true; return nil },
				assessment, "supervisor-1",
				func() *supervisionResumeRefusal { gateRan = true; return nil },
			)
			var boundaryErr error
			outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
				func() time.Time { return now }, loader,
				passingGenerationReader(assessment), passingPaneReader(assessment),
				readReservedRecoveryTransition,
				func(p string) error {
					boundaryErr = boundary(p)
					return boundaryErr
				})

			if err == nil {
				t.Fatal("an unconfirmed delivery must return an error; silence would let a caller treat an " +
					"indeterminate pause as a completed one")
			}
			// FIXTURE INTEGRITY: the ambiguous error must have been reached through the real ordering, or this
			// row proves nothing about the producer.
			if !directivePublished || !gateRan || sends != 1 {
				t.Fatalf("fixture is void: the ambiguous result must be reached AFTER a published directive and a "+
					"passing gate, with exactly one input attempt (directive=%t gate=%t sends=%d)",
					directivePublished, gateRan, sends)
			}

			// THE PRODUCER HALF -- the type's first coverage.
			var unknown *supervisionUnknownDeliveryError
			if !errors.As(boundaryErr, &unknown) {
				t.Fatalf("the BOUNDARY did not map an ambiguous pane-input result to supervisionUnknownDeliveryError; "+
					"it produced %#v. Collapsing ambiguity into failure invites a retry that delivers twice, and "+
					"into success hides a lost resume.", boundaryErr)
			}
			if !strings.Contains(unknown.Detail, row.wantDetail) {
				t.Errorf("the operator must be told WHICH ambiguity occurred; detail %q does not mention %q",
					unknown.Detail, row.wantDetail)
			}
			if !errors.Is(boundaryErr, raised) {
				t.Errorf("tmuxpane's own error must stay in the chain rather than being replaced by a summary; got: %v",
					boundaryErr)
			}

			// THE CONSUMER HALF: unknown is neither delivered nor definite, and nothing is consumed.
			if !outcome.DeliveryOutcomeUnknown {
				t.Error("an ambiguous pane-input result was not reported as UNKNOWN. This is the one state that " +
					"leaves a live reservation and an operator decision, and it must not be collapsed into a " +
					"refusal or a delivery.")
			}
			if outcome.Delivered {
				t.Error("Delivered=true for an UNCONFIRMED input; Delivered must mean KNOWN-successful")
			}
			if outcome.Consumed {
				t.Error("Consumed=true after an unconfirmed delivery; the claim must stay reserved so claim-once " +
					"refuses the next attempt rather than risking a second audited resume")
			}
			if _, statErr := os.Stat(reservation); statErr != nil {
				t.Errorf("the reservation must survive an unknown outcome: %v", statErr)
			}
		})
	}
}

// POLARITY PIN: an ordinary error from inside pane input STAYS unknown.
//
// This is the row that makes the fail-closed default real. Inverting the branch -- default definite, mark
// unknown -- reads as equivalent code and is a fail-open at the one seam where being wrong costs a second
// audited resume. TestUnknownPaneInputIsTerminalIndeterminateAndConsumesNothing covers the same polarity from
// the outcome side; this row states it as the explicit default.
func TestAnUntypedDeliveryErrorRemainsUnknownRatherThanDefinite(t *testing.T) {
	assessment, action, payload, _, now := u6DeliveringFixture(t)

	u6StubSeams(t,
		func(GoalSupervisionAssessment, string, string) error { return nil },
		func(GoalSupervisionAssessment) error { return nil },
	)
	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}

	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loader,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		readReservedRecoveryTransition,
		func(string) error { return errors.New("tmux refused the send for an unclassified reason") })

	if err == nil {
		t.Fatal("an unclassified delivery error must still refuse")
	}
	if !outcome.DeliveryOutcomeUnknown {
		t.Error("an UNTYPED delivery error was downgraded to definite non-delivery.\nOnly the refusals that " +
			"happen provably before sendPromptToPane may be definite; once control has entered pane input we " +
			"cannot prove no bytes landed, and assuming so is the fail-open direction.")
	}
}
