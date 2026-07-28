package cli

import (
	"fmt"
	"strings"
)

// PR5 / #498 F5: ONE pre-mutation gate evaluator, consumed by BOTH the executor and --dry-run.
//
// WHY THIS IS ONE FUNCTION AND NOT TWO. The dry-run's entire purpose is to PREDICT the executor. If
// it evaluated its own copy of the gates, the two would drift, and the drift would be invisible
// precisely where it matters most: a dry-run reporting "would deliver" while the real path refuses,
// or worse, reporting a refusal the real path does not make. That is the two-deciders defect in the
// one place whose whole job is agreement. So there is one evaluator, one gate list, one set of
// verdicts, and the dry-run is nothing more than "run it and print instead of act".
//
// EVERY GATE HERE IS READ-ONLY. No reservation, no bind, no consume, no directive, no pane input.
// That is what makes the dry-run genuinely side-effect-free rather than side-effect-free by
// convention -- the property is structural, because the evaluator has nothing that writes.
//
// THE DELIVERY-TIME GATES ARE DELIBERATELY NOT HERE, and that absence is reported rather than
// hidden: the payload-drift recheck, the launch-generation comparison, and the live pane
// revalidation all require the world AT DELIVERY TIME, after a reservation exists. A dry-run cannot
// evaluate them without reserving, so it names them as NOT EVALUATED instead of implying a verdict
// it did not reach.

type supervisionGateName string

const (
	gateWiring             supervisionGateName = "wiring"
	gateSupervisorID       supervisionGateName = "supervisor-identity"
	gateActionCanonical    supervisionGateName = "action-canonical-metadata"
	gateActionBinding      supervisionGateName = "action-identity-binding"
	gatePayloadAuthorized  supervisionGateName = "payload-authorization"
	gateAssessmentFresh    supervisionGateName = "assessment-fresh"
	gateWallClock          supervisionGateName = "wall-clock-freshness"
	gateAssessmentEligible supervisionGateName = "assessment-eligible"
	gateOperatorPolicy     supervisionGateName = "operator-policy"
	gateDeliverableNoConf  supervisionGateName = "delivering-path-needs-no-confirmation"
	gateClaimOnce          supervisionGateName = "claim-once-ledger"
)

// supervisionDeliveryTimeGates are the gates a dry-run CANNOT evaluate, listed so its report can say
// so explicitly. Naming them is the difference between "these passed" and "these were not checked".
var supervisionDeliveryTimeGates = []string{
	"payload-drift (re-read the goal binding immediately before input)",
	"launch-generation drift (digest and modtime against the bound generation)",
	"live pane revalidation (exact managed, alive, idle pane with a nonzero PID)",
	// U4 requires a SECOND wall-clock FreshUntil check after reserve/bind/rescan and immediately
	// before pane input. The pre-mutation clock gate above does not satisfy it: time passes during the
	// ledger work, so an assessment fresh at evaluation can expire before the bytes are typed.
	"delivery-time wall-clock freshness (FreshUntil re-checked immediately before pane input)",
}

type supervisionGateResult struct {
	Name     supervisionGateName `json:"gate"`
	Passed   bool                `json:"passed"`
	Clause   string              `json:"clause,omitempty"`
	Detail   string              `json:"detail,omitempty"`
	Recovery string              `json:"recovery,omitempty"`
}

// supervisionPreMutationEvaluation is the whole verdict. Payload is populated ONLY when every gate
// passed: a partially-validated payload must not be reachable, because the next reader would have no
// way to know which validations it survived.
type supervisionPreMutationEvaluation struct {
	Results []supervisionGateResult `json:"gates"`
	Payload string                  `json:"-"`

	// AuthorizedDigest is the digest that PASSED the pre-reserve equalities -- the exact suppliedDigest
	// the loader returned, having been checked against the payload bytes, the binding's CommandDigest,
	// and the action's CommandDigest.
	//
	// IT EXISTS TO BE A TEMPORAL BASELINE, and that is why it must be carried rather than recomputed.
	// The delivery-time drift gate asks "is the binding still what it was when I authorized it", which
	// is a question about TWO MOMENTS. Recomputing a digest after the reserve -- or deriving one from
	// the re-read, or substituting the action's or binding's value -- would compare the present against
	// the present and always agree. That is not a weaker check, it is a tautology wearing a check's
	// shape, and it would silently delete the drift protection entirely.
	//
	// My F5 extraction lost this by returning Payload alone, which is how the drift gate ended up
	// referencing an out-of-scope local. The compiler caught it; a recomputing "fix" would have
	// compiled cleanly and been wrong.
	AuthorizedDigest string `json:"-"`

	ClaimKey  string `json:"claim_key,omitempty"`
	Dir       string `json:"-"`
	LegacyKey string `json:"-"`
}

// firstFailure returns the gate that refused, or nil. Order matters and is the contract: gates are
// evaluated in the order a caller would want them diagnosed, so a wiring fault is never reported as
// a policy problem.
func (e supervisionPreMutationEvaluation) firstFailure() *supervisionGateResult {
	for i := range e.Results {
		if !e.Results[i].Passed {
			return &e.Results[i]
		}
	}
	return nil
}

func (e supervisionPreMutationEvaluation) allPassed() bool { return e.firstFailure() == nil }

// refusal converts the failed gate into the executor's refusal type, so the executor and the dry-run
// report the SAME clause, detail and recovery for the same condition. Two renderings of one verdict
// is a smaller version of two deciders.
func (e supervisionPreMutationEvaluation) refusal() error {
	f := e.firstFailure()
	if f == nil {
		return nil
	}
	return supervisionResumeRefusal{Clause: f.Clause, Detail: f.Detail, Recovery: f.Recovery}
}

// evaluateSupervisionPreMutationGates runs every read-only gate and STOPS AT THE FIRST FAILURE.
//
// Short-circuiting is deliberate rather than an optimisation: later gates read inputs earlier gates
// validate, so evaluating past a failure would either produce cascading noise or, worse, execute a
// read against inputs already known to be untrustworthy.
func evaluateSupervisionPreMutationGates(
	assessment GoalSupervisionAssessment,
	action GoalSupervisionAction,
	supervisor string,
	now supervisionResumeClock,
	loadPayload supervisionResumePayloadLoader,
	scan func(dir, claimKey, legacyKey string) (pauseLedgerScan, error),
) supervisionPreMutationEvaluation {
	var out supervisionPreMutationEvaluation
	pass := func(n supervisionGateName) {
		out.Results = append(out.Results, supervisionGateResult{Name: n, Passed: true})
	}
	fail := func(n supervisionGateName, clause, detail, recovery string) supervisionPreMutationEvaluation {
		out.Results = append(out.Results, supervisionGateResult{
			Name: n, Passed: false, Clause: clause, Detail: detail, Recovery: recovery,
		})
		return out
	}

	// WIRING first: a nil seam is a programming fault in the caller, and diagnosing it as anything
	// else sends the wrong person to the wrong place.
	if now == nil {
		return fail(gateWiring, "the executor requires an explicit time source",
			"no clock supplied", "this is a wiring bug, not an operator condition")
	}
	if loadPayload == nil {
		return fail(gateWiring, "the executor requires a payload loader",
			"no payload loader supplied", "this is a wiring bug; bytes that cannot be authorized are never delivered")
	}
	if scan == nil {
		return fail(gateWiring, "the executor requires a ledger scanner",
			"no ledger scanner supplied", "this is a wiring bug; an unscanned ledger is never treated as vacant")
	}
	pass(gateWiring)

	if strings.TrimSpace(supervisor) == "" {
		return fail(gateSupervisorID, "supervisor identity is explicit, nonblank, and never inferred",
			"no supervisor identity supplied",
			"supply the supervising actor; it is persisted in the claim and is not derivable from the lead")
	}
	if strings.TrimSpace(supervisor) == supervisorIdentityPlaceholder {
		return fail(gateSupervisorID, "the placeholder is not an identity and is never persisted as one",
			fmt.Sprintf("supervisor identity is still the literal placeholder %s", supervisorIdentityPlaceholder),
			"supply the real supervising actor; a claim recording the placeholder cannot answer who authorised it")
	}
	pass(gateSupervisorID)

	// CANONICAL METADATA, derived ONCE from the assessment. One field per check so a single-field
	// mutation fails a NAMED gate rather than a compound one.
	expected := action.canonicalExpectationFrom(assessment)
	if detail := action.canonicalMismatch(expected, assessment); detail != "" {
		return fail(gateActionCanonical, "the invoked action must be the canonical resume action this assessment published",
			detail, "re-read the assessment and invoke its published resume action verbatim")
	}
	pass(gateActionCanonical)

	if strings.TrimSpace(action.Fingerprint) != strings.TrimSpace(assessment.Fingerprint) {
		return fail(gateActionBinding, "the invoked action must be the one this assessment authorised",
			fmt.Sprintf("action fingerprint %q does not match assessment fingerprint %q", action.Fingerprint, assessment.Fingerprint),
			"re-read the assessment; a stale action is never delivered")
	}
	if strings.TrimSpace(action.AttemptID) != strings.TrimSpace(assessment.Binding.Goal.AttemptID) {
		return fail(gateActionBinding, "the invoked action must target the attempt this assessment is about",
			fmt.Sprintf("action attempt %q does not match assessment attempt %q", action.AttemptID, assessment.Binding.Goal.AttemptID),
			"re-read the assessment; delivering to a different attempt is a wrong-target delivery")
	}
	pass(gateActionBinding)

	// PAYLOAD AUTHORIZATION. Four independent equalities; see the executor for why independence
	// matters and why they are not collapsed.
	payload, suppliedDigest, payloadErr := loadPayload(assessment)
	switch {
	case payloadErr != nil:
		return fail(gatePayloadAuthorized, "the delivered payload is the assessment-authorized native resume payload",
			"cannot load the native resume payload: "+payloadErr.Error(),
			"inspect the goal binding; an unreadable payload is never delivered and nothing was reserved")
	case strings.TrimSpace(payload) == "":
		return fail(gatePayloadAuthorized, "the delivered payload is the assessment-authorized native resume payload",
			"the native resume payload is empty",
			"inspect the goal binding; delivering nothing would consume the claim without resuming anything")
	case digestGoalSupervisionString(payload) != strings.TrimSpace(suppliedDigest):
		return fail(gatePayloadAuthorized, "the payload bytes must match the digest that accompanies them",
			fmt.Sprintf("payload digest %q does not describe the supplied bytes (their digest is %q)",
				suppliedDigest, digestGoalSupervisionString(payload)),
			"bytes and digest disagree; this is corruption or substitution, not a retryable condition")
	case strings.TrimSpace(suppliedDigest) == "" ||
		strings.TrimSpace(suppliedDigest) != strings.TrimSpace(assessment.Binding.Goal.CommandDigest):
		// RESTORED. My refactor dropped this and left the comment claiming four equalities over three
		// -- transitively implied, and I had argued against exactly that reasoning when I added the
		// direct payload<->action check. Reintroducing transitivity in a refactor of the fix that
		// removed it is worth naming: a consolidation is a place where assertions quietly merge, and
		// "the comment says four" was the only surviving evidence that one had gone.
		return fail(gatePayloadAuthorized, "the loaded payload's digest must be the one the binding authorizes",
			fmt.Sprintf("loaded payload digest %q does not match the assessment's bound digest %q",
				suppliedDigest, assessment.Binding.Goal.CommandDigest),
			"the loader returned a payload the durable binding does not authorize; nothing was reserved")
	case strings.TrimSpace(action.CommandDigest) == "" ||
		strings.TrimSpace(action.CommandDigest) != strings.TrimSpace(assessment.Binding.Goal.CommandDigest):
		return fail(gatePayloadAuthorized, "the delivered payload is the assessment-authorized native resume payload",
			fmt.Sprintf("action command digest %q does not match the assessment's bound digest %q",
				action.CommandDigest, assessment.Binding.Goal.CommandDigest),
			"re-read the assessment; a payload the binding does not authorize is never delivered")
	case digestGoalSupervisionString(payload) != strings.TrimSpace(action.CommandDigest):
		return fail(gatePayloadAuthorized, "the payload bytes must be the ones the invoked action authorizes",
			fmt.Sprintf("digest of the loaded payload %q does not match the action's authorized digest %q",
				digestGoalSupervisionString(payload), action.CommandDigest),
			"the action and the payload describe different bytes; nothing was reserved")
	}
	pass(gatePayloadAuthorized)

	if !assessment.Fresh {
		return fail(gateAssessmentFresh, "one fresh assessment",
			fmt.Sprintf("assessment observed at %s is no longer fresh (valid until %s)", assessment.ObservedAt, assessment.FreshUntil),
			"re-run the supervision sweep; a stale assessment is never delivered from")
	}
	pass(gateAssessmentFresh)

	// WALL CLOCK. assessment.Fresh is a static bool from when the assessment was BUILT; freshness must
	// hold at the moment of delivery, and only a clock read can express that.
	if !assessment.FreshUntil.IsZero() && !now().Before(assessment.FreshUntil) {
		return fail(gateWallClock, "one fresh assessment",
			fmt.Sprintf("assessment expired at %s and it is now %s", assessment.FreshUntil, now()),
			"re-run the supervision sweep; delivering from an expired observation is the stale-evidence action #498 forbids")
	}
	pass(gateWallClock)

	// ELIGIBILITY IS ITS OWN GATE AND ITS OWN DIAGNOSIS. I collapsed this into the policy gate on the
	// grounds that AutomaticResumeAllowed == Eligible && safe_auto, so a policy refusal "covered" it.
	// It does not: an actor with contradictory or unknown evidence would be reported as an
	// OPERATOR-POLICY refusal, sending the operator to inspect their policy configuration when the
	// real problem is the evidence. A lossy substitute for a gate is not the gate.
	// It also contradicted this file's own header, which states assessment.Eligible is AUTHORITATIVE.
	//
	// CONSUMED, never re-derived: the assessment owns the eligibility conjunction and this reports its
	// deterministic reasons rather than forming a second opinion.
	if !assessment.Eligible {
		return fail(gateAssessmentEligible,
			"unknown or contradictory evidence is ineligible, not a best-effort resume",
			"assessment reports ineligible: "+firstFailedGoalSupervisionReason(assessment.Reasons),
			assessment.Actions.Inspect.Command)
	}
	pass(gateAssessmentEligible)

	if !assessment.AutomaticResumeAllowed {
		return fail(gateOperatorPolicy, "operator policy: automatic resume permitted",
			fmt.Sprintf("policy mode %s does not permit automatic native resume", assessment.Policy.Mode),
			assessment.Actions.Inspect.Command)
	}
	pass(gateOperatorPolicy)

	if action.NeedsConfirmation {
		return fail(gateDeliverableNoConf, "the delivering path requires an action that needs no confirmation",
			"the resume action requires confirmation, so this profile's policy is not safe_auto",
			"under manual or notify-only policy a human performs the resume; automatic delivery is refused")
	}
	pass(gateDeliverableNoConf)

	// CLAIM-ONCE, a READ. Both derivations are scanned: a live redelivery reservation is an
	// authoritative claim on this pause even though it lives under a different filename derivation.
	key, keyErr := supervisionClaimKey(assessment.Binding.NamespaceID, assessment.Binding.PauseGeneration,
		assessment.Binding.Goal.AttemptID)
	if keyErr != nil {
		return fail(gateClaimOnce, "claim-once",
			"cannot derive the claim key for this pause: "+keyErr.Error(),
			assessment.Actions.Inspect.Command)
	}
	dir := goalAttemptDir(assessment.Binding.Project, assessment.Binding.Profile, assessment.Binding.Session)
	legacyKey := resumeGoalTransitionID(assessment.Binding.Goal.AttemptID, assessment.Binding.Goal.BindingDigest)
	existing, scanErr := scan(dir, key, legacyKey)
	if scanErr != nil {
		return fail(gateClaimOnce, "no earlier resume claim ... delivered or remains indeterminate",
			"cannot read the recovery transition ledger for this pause: "+scanErr.Error(),
			"inspect the transition directory; an unreadable ledger is never treated as vacant")
	}
	if blocker := existing.blocking(); blocker != nil {
		return fail(gateClaimOnce, "no earlier resume claim ... delivered or remains indeterminate",
			blocker.describe(), blocker.recovery())
	}
	pass(gateClaimOnce)

	// Populated ONLY here, after every gate passed, alongside Payload. A partially-validated baseline
	// would be worse than none: the drift gate would compare against a value whose authorization is
	// unknown.
	out.Payload = payload
	out.AuthorizedDigest = strings.TrimSpace(suppliedDigest)
	out.ClaimKey = key
	out.Dir = dir
	out.LegacyKey = legacyKey
	return out
}

// supervisionPreMutationGateOrder is the COMPLETE pre-mutation gate list, in evaluation order.
//
// It exists because the evaluator SHORT-CIRCUITS: Results holds the passed prefix plus the first
// failure, and nothing after it. A report printing only Results would imply that prefix is the whole
// set -- the same overclaim-by-omission as a mutation walk that silently drops an item. With this
// list, a caller can name every gate it did NOT reach as skipped, so "passed", "failed" and
// "not reached" are three distinct states in the output rather than two states and a gap.
var supervisionPreMutationGateOrder = []supervisionGateName{
	gateWiring,
	gateSupervisorID,
	gateActionCanonical,
	gateActionBinding,
	gatePayloadAuthorized,
	gateAssessmentFresh,
	gateWallClock,
	gateAssessmentEligible,
	gateOperatorPolicy,
	gateDeliverableNoConf,
	gateClaimOnce,
}

// skippedGates returns the gates the evaluation never reached, in order. Empty when everything ran.
func (e supervisionPreMutationEvaluation) skippedGates() []supervisionGateName {
	reached := map[supervisionGateName]bool{}
	for _, r := range e.Results {
		reached[r.Name] = true
	}
	var skipped []supervisionGateName
	for _, n := range supervisionPreMutationGateOrder {
		if !reached[n] {
			skipped = append(skipped, n)
		}
	}
	return skipped
}
