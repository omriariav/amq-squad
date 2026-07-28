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

// PR5 / #498 U9: THE POLICY-GATE MUTATION ROW, driving the EXECUTOR.
//
// MY FIRST VERSION DID NOT SATISFY THIS AND dev-2's REASON IS THE SHARPEST FINDING ON THIS ROW SO FAR.
// It called evaluateSupervisionPreMutationGates only. That evaluator is structurally read-only and has
// no reservation or delivery capability whatsoever, so:
//
//   - its zero-ledger-write assertion was GUARANTEED regardless of my writability probe. I had built the
//     probe to make an absence meaningful and then asserted the absence against code that cannot write,
//     which is the vacuity I was trying to close, one level up.
//   - deleting the policy gate would merely make the evaluation return success, and the test would die
//     at `failed == nil` BEFORE any executor, reserve, bind, rescan, delivery or consume ran. It would
//     have "failed under mutation" for the wrong reason -- which is worse than surviving, because a kill
//     for the wrong reason reads as proof that the mutation was caught.
//
// So this drives executeSupervisionResume, and every downstream field is valid specifically so that
// REMOVING the policy guard can reach reserve -> bind -> rescan -> deliver -> consume. That reachability
// is the whole point: a mutation can only be killed by a test that would otherwise get further.
//
// NO PRE-EXECUTOR FATAL. dev-2's point 5, and it is subtle: an evaluator assertion placed ahead of
// execution would kill the test under the policy mutation before the behaviour being falsified occurs.
// Diagnostics about gate skipping belong AFTER the executor assertions, where they cannot pre-empt them.
//
// LIMITATION, stated not buried: safe-auto with AutomaticResumeAllowed forced false is NOT
// production-reachable -- production computes that field as Eligible && Mode==safe-auto. Forcing it is
// the only way to make operator-policy the SOLE refuser, which is the only condition under which
// deleting it is falsifiable. This is a direct executor isolation input.
func TestPolicyGateBlocksTheExecutorFromReservingOrDelivering(t *testing.T) {
	const payload = "/goal resume attempt-abc"
	const profile = "squad"
	const session = "v2-25-0"
	now := time.Now().UTC()
	project := t.TempDir()

	assessment := GoalSupervisionAssessment{
		Fresh:      true,
		Eligible:   true,
		ObservedAt: now,
		FreshUntil: now.Add(time.Hour),
		// SAFE-AUTO so the canonical action carries NeedsConfirmation=false and the delivering-path
		// guard PASSES instead of co-refusing. Without this, deleting the policy gate changes nothing
		// observable and the mutation is masked by redundancy.
		Policy:      team.GoalSupervisionPolicyStatus{Mode: team.GoalSupervisionSafeAuto, Revision: 1, Source: "test"},
		Fingerprint: "fingerprint-1",
		// THE BLOCKER EVIDENCE, by exactly the reasoning the launch-generation comment below already gives.
		// The exact binding requires a nonblank blocker id and resolution digest, so without these the
		// MUTATED run dies at construction instead of reaching delivery -- and a mutation that dies before
		// the behaviour it targets proves nothing about the policy gate. Same failure dev-2's point 3
		// identified for bind, one step earlier in the pipeline.
		Blocker: GoalSupervisionBlockerEvidence{
			ID: "blocker-1", Known: true, Resolved: true, ResolutionDigest: "resolution-digest-1",
		},
		Binding: GoalSupervisionBinding{
			Project: project, Profile: profile, Session: session,
			NamespaceID: squadnamespace.ID(profile, session),
			LeadRole:    "cto", LeadHandle: "cto",
			LaunchID: "launch-1",
			// NONBLANK launch generation, per dev-2's point 3: bind consumes these, so blank values
			// would stop the mutated run at bind instead of letting it reach delivery -- and a mutation
			// that dies at bind proves nothing about the policy gate.
			LaunchRecordDigest:  "launch-digest-1",
			LaunchRecordModTime: 1700000000,
			LaunchStartedAt:     now.Add(-time.Minute),
			Goal: GoalSupervisionGoalIdentity{
				// The mode production emits for a blocked native goal. Required by the exact binding, and
				// absent here for the same reason it was absent everywhere else: no gate on this path read it.
				Mode:      fixtureNativeGoalMode,
				AttemptID: "attempt-abc", BindingDigest: "digest-def",
				CommandDigest: digestGoalSupervisionString(payload),
			},
			PauseGeneration: "pause-gen-1",
			Pane:            GoalSupervisionPaneIdentity{PaneID: "%7", Managed: true},
		},
	}
	// FORCED after the policy is set: this is what makes operator-policy the sole refuser.
	assessment.AutomaticResumeAllowed = false
	assessment.Actions = goalSupervisionActions(assessment)
	action := assessment.Actions.Resume

	// The ledger directory exists and is PROVED writable, so "no transition artifact" distinguishes
	// "declined to write" from "could not write". Under the mutation the executor really can write here,
	// which is what makes the absence evidence rather than a guarantee.
	dir := goalAttemptDir(project, profile, session)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create ledger directory: %v", err)
	}
	probe := filepath.Join(dir, ".writability-probe")
	if err := os.WriteFile(probe, []byte("x"), 0o644); err != nil {
		t.Fatalf("ledger directory not writable, so a zero-artifact assertion would prove nothing: %v", err)
	}
	if err := os.Remove(probe); err != nil {
		t.Fatalf("remove writability probe: %v", err)
	}

	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}

	// THE DELIVERY CALLBACK asserts the EXACT native payload and counts invocations. Counting alone
	// would not catch a delivery of the wrong bytes, and asserting bytes without counting would not
	// catch a double delivery.
	delivered := 0
	var deliveredPayload string
	deliver := func(got string) error {
		delivered++
		deliveredPayload = got
		if got != payload {
			t.Errorf("delivered payload = %q, want the digest-authorized native payload %q", got, payload)
		}
		return nil
	}

	// The outcome is captured so a policy refusal can be shown to carry NO delivered=true -- a refusal
	// that reported delivery would be the worst possible disagreement between return value and reality.
	outcome, err := executeSupervisionResume(assessment, action, "supervisor-1",
		func() time.Time { return now }, loader,
		passingGenerationReader(assessment), passingPaneReader(assessment),
		readReservedRecoveryTransition, deliver)

	// 1. THE REFUSAL MUST BE THE POLICY ONE, BY TYPE AND EXACT CLAUSE.
	//
	// My previous version labelled itself "by identity and not merely by existence" and then used
	// strings.Contains(err.Error(), "automatic resume") -- a SUBSTRING, which is existence. dev-2 caught
	// the label and the check disagreeing. A substring can match another refusal's Detail or Recovery
	// text, so it cannot establish WHICH gate refused; and this is the same existence-for-identity
	// substitution that produced the arbitrary-payload hole earlier in this PR.
	//
	// NOT t.Fatal on a nil error, per dev-2's point 2: under the policy-guard mutation the executor
	// returns nil, and stopping here would suppress the delivered-count and artifact evidence below.
	// The mutation artifact must SHOW that one exact-payload delivery occurred and that reservation
	// artifacts appeared -- "expected refusal, got nil" does not demonstrate the behaviour U9 names.
	if err == nil {
		t.Error("the executor must refuse when policy forbids automatic resume; continuing so the " +
			"delivered-count and ledger-artifact evidence below is still reported")
	} else {
		var refusal supervisionResumeRefusal
		if !errors.As(err, &refusal) {
			t.Errorf("refusal must be a typed supervisionResumeRefusal, got %T: %v", err, err)
		} else if refusal.Clause != "operator policy: automatic resume permitted" {
			t.Errorf("refusal Clause = %q, want exactly %q.\nAny other clause means the fixture stopped "+
				"isolating the policy gate and the mutation cannot be falsified through this input.",
				refusal.Clause, "operator policy: automatic resume permitted")
		}
	}

	// 2a. THE OUTCOME MUST NOT CLAIM DELIVERY. A refusal whose return value said delivered=true would be
	// a disagreement between what happened and what was reported -- and the reported value is what a
	// wrapper acts on.
	if outcome.Delivered {
		t.Error("a policy refusal returned an outcome claiming Delivered=true")
	}

	// 2. ZERO DELIVERIES, and no payload reached the callback at all.
	if delivered != 0 {
		t.Errorf("policy refusal delivered %d time(s) with payload %q; the operator's policy forbids it",
			delivered, deliveredPayload)
	}

	// 3. NO TRANSITION ARTIFACT in a directory we proved writable.
	entries, readErr := os.ReadDir(dir)
	if readErr != nil {
		t.Fatalf("read ledger directory: %v", readErr)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), currentRecoveryTransitionPrefix) ||
			strings.HasPrefix(e.Name(), legacyRecoveryTransitionPrefix) {
			t.Errorf("a policy refusal left %s behind in a writable ledger directory", e.Name())
		}
	}

	// 4. DIAGNOSTIC COMPANION, placed AFTER the executor assertions on purpose (dev-2's point 5). Run
	// ahead of them it would abort the test under the policy mutation before the behaviour above could
	// be observed, so a mutation kill would not mean what it appears to mean.
	eval := evaluateSupervisionPreMutationGates(assessment, action, "supervisor-1",
		func() time.Time { return now }, loader, scanRecoveryTransitionsForPause)
	if f := eval.firstFailure(); f != nil && f.Name != gateOperatorPolicy {
		t.Errorf("diagnostic: first failing gate = %q, want %q -- the fixture is no longer isolating the "+
			"policy gate", f.Name, gateOperatorPolicy)
	}
}
