package cli

import (
	"testing"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// PR5 / #498: THE GATE-ORDER TABLE. Every row asserts WHICH gate fails AND the exact prefix of gates
// evaluated before it.
//
// THE PREFIX ASSERTION IS WHY THIS CAN CLAIM "ORDER". dev-2 caught that my first version could not:
// every row had exactly ONE failing gate with all others passing, so moving that gate earlier or later
// still left it the first and only failure and the row stayed green. It proved defect-to-gate identity
// and it detected fixture decay -- both real -- but the NAME claimed order, which it did not pin. Same
// false-name problem I have hit twice today, so the name now states what is proven.
//
// This exists because of a specific failure of mine, and the mechanism is cto's ratified fix for it.
// I wrote a policy-refusal row, then added the canonical-metadata gate UPSTREAM of it, and the row
// silently re-aimed: it still refused, still passed its "did it refuse" assertion for a while, and was
// no longer testing the policy boundary at all. Only the error-text assertion caught it, and only by
// accident of wording.
//
// SINGLE-DEFECT-PER-ROW IS NOT A PROPERTY A ROW HAS. It is a property that DECAYS every time a gate is
// added ahead of it. Asserting the gate NAME converts it from something I have to remember into
// something the suite enforces: add a gate upstream of any row and this table fails loudly, naming the
// row whose meaning just changed.
//
// Each row is otherwise-valid and carries EXACTLY ONE defect. A row that could fail for two reasons
// proves nothing about either.

// canonicalGateOrderLiteral is the expected evaluation order written out BY HAND.
//
// It deliberately does NOT reference supervisionPreMutationGateOrder. Comparing the evaluator against
// the package's own list would be a same-helper assertion: if someone reorders the evaluator and
// updates that list to match, both move together and every test stays green while the actual order --
// the thing that decides which fault an operator is told about -- has changed.
//
// This is the golden-vector argument from PR5's compatibility proof, applied to ordering rather than to
// a digest: a literal is the only expectation that cannot drift WITH the implementation. If this list
// and supervisionPreMutationGateOrder disagree, that disagreement is the finding.
var canonicalGateOrderLiteral = []supervisionGateName{
	"wiring",
	"supervisor-identity",
	"action-canonical-metadata",
	"action-identity-binding",
	"payload-authorization",
	"assessment-fresh",
	"wall-clock-freshness",
	"assessment-eligible",
	"operator-policy",
	"delivering-path-needs-no-confirmation",
	"claim-once-ledger",
}

// gateNamesOf extracts the reported gate names in order, so a row can assert the exact PREFIX the
// evaluation walked rather than only which gate failed last.
func gateNamesOf(results []supervisionGateResult) []supervisionGateName {
	out := make([]supervisionGateName, 0, len(results))
	for _, r := range results {
		out = append(out, r.Name)
	}
	return out
}

func gateNamesEqual(a, b []supervisionGateName) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// gateFixture builds an assessment/action/loader triple that passes EVERY gate. Rows then break one
// thing. Building "all valid" once and mutating from it is what makes one-defect-per-row mechanically
// true rather than aspirational -- hand-building each row invites two defects by omission.
func gateFixture(t *testing.T) (GoalSupervisionAssessment, GoalSupervisionAction, supervisionResumePayloadLoader, supervisionResumeClock) {
	t.Helper()
	const payload = "/goal resume attempt-abc"
	now := time.Now().UTC()
	project := t.TempDir()

	assessment := GoalSupervisionAssessment{
		Fresh:                  true,
		Eligible:               true,
		AutomaticResumeAllowed: true,
		Fingerprint:            "fingerprint-1",
		ObservedAt:             now,
		FreshUntil:             now.Add(time.Hour),
		// SAFE-AUTO explicitly: the constructor derives NeedsConfirmation from Policy.Mode, and the
		// all-valid fixture must reach the delivering-path gate, which requires false.
		Policy: team.GoalSupervisionPolicyStatus{
			Mode: team.GoalSupervisionSafeAuto, Revision: 1, Source: "test",
		},
		Binding: GoalSupervisionBinding{
			Project: project, Profile: "squad", Session: "v2-25-0",
			NamespaceID: squadnamespace.ID("squad", "v2-25-0"),
			LeadRole:    "cto", LeadHandle: "cto",
			Goal: GoalSupervisionGoalIdentity{
				AttemptID: "attempt-abc", BindingDigest: "digest-def",
				CommandDigest: digestGoalSupervisionString(payload),
			},
			PauseGeneration: "pause-gen-1",
			Pane:            GoalSupervisionPaneIdentity{PaneID: "%7", Managed: true},
		},
	}
	// FROM THE PRODUCTION CONSTRUCTOR. I hand-built this action first and it carried the identical
	// defect dev-2 caught in the policy row: a fixture that copies canonical fields is a second
	// definition of canon and drifts from goalSupervisionActions. Consuming the constructor means every
	// canonical field -- Kind, ID, ActionKind, Scope, NamespaceID, Command, Mutates, Available,
	// NeedsConfirmation, Reason -- plus Fingerprint/AttemptID/CommandDigest comes from production, so a
	// row can only fail the canonical gate by DELIBERATELY diverging.
	assessment.Actions = goalSupervisionActions(assessment)
	action := assessment.Actions.Resume

	loader := func(GoalSupervisionAssessment) (string, string, error) {
		return payload, digestGoalSupervisionString(payload), nil
	}
	clock := func() time.Time { return now }
	return assessment, action, loader, clock
}

func TestPreMutationGateOrderAndFirstFailureArePinned(t *testing.T) {
	// A scan that always reports a vacant ledger, so no row fails on claim-once unless it means to.
	vacantScan := func(string, string, string) (pauseLedgerScan, error) { return pauseLedgerScan{}, nil }

	for _, tc := range []struct {
		name string
		// mutate introduces EXACTLY ONE defect.
		mutate   func(*GoalSupervisionAssessment, *GoalSupervisionAction, *supervisionResumePayloadLoader, *supervisionResumeClock)
		wantGate supervisionGateName
	}{
		{
			name: "nil clock is a wiring fault, not an operator condition",
			mutate: func(_ *GoalSupervisionAssessment, _ *GoalSupervisionAction, _ *supervisionResumePayloadLoader, c *supervisionResumeClock) {
				*c = nil
			},
			wantGate: gateWiring,
		},
		{
			name: "blank supervisor",
			mutate: func(_ *GoalSupervisionAssessment, _ *GoalSupervisionAction, _ *supervisionResumePayloadLoader, _ *supervisionResumeClock) {
			},
			wantGate: gateSupervisorID,
		},
		{
			name: "invocation command diverges from canon",
			mutate: func(_ *GoalSupervisionAssessment, a *GoalSupervisionAction, _ *supervisionResumePayloadLoader, _ *supervisionResumeClock) {
				a.Command = "amq-squad goal supervise-resume --session SOMETHING-ELSE"
			},
			wantGate: gateActionCanonical,
		},
		{
			name: "action fingerprint diverges",
			mutate: func(_ *GoalSupervisionAssessment, a *GoalSupervisionAction, _ *supervisionResumePayloadLoader, _ *supervisionResumeClock) {
				a.Fingerprint = "fingerprint-STALE"
			},
			wantGate: gateActionBinding,
		},
		{
			name: "loader returns bytes that disagree with their own digest",
			mutate: func(_ *GoalSupervisionAssessment, _ *GoalSupervisionAction, l *supervisionResumePayloadLoader, _ *supervisionResumeClock) {
				*l = func(GoalSupervisionAssessment) (string, string, error) {
					return "/goal resume SOMETHING-ELSE", digestGoalSupervisionString("/goal resume attempt-abc"), nil
				}
			},
			wantGate: gatePayloadAuthorized,
		},
		{
			name: "assessment not fresh",
			mutate: func(as *GoalSupervisionAssessment, _ *GoalSupervisionAction, _ *supervisionResumePayloadLoader, _ *supervisionResumeClock) {
				as.Fresh = false
			},
			wantGate: gateAssessmentFresh,
		},
		{
			name: "clock is one tick past FreshUntil",
			mutate: func(as *GoalSupervisionAssessment, _ *GoalSupervisionAction, _ *supervisionResumePayloadLoader, c *supervisionResumeClock) {
				expired := as.FreshUntil.Add(time.Nanosecond)
				*c = func() time.Time { return expired }
			},
			wantGate: gateWallClock,
		},
		{
			name: "ineligible evidence is NOT a policy refusal",
			mutate: func(as *GoalSupervisionAssessment, _ *GoalSupervisionAction, _ *supervisionResumePayloadLoader, _ *supervisionResumeClock) {
				as.Eligible = false
				// AutomaticResumeAllowed stays true so ONLY eligibility is wrong. This is the row that
				// proves collapsing Eligible into the policy gate is a misdiagnosis rather than a
				// shortcut: with the gates collapsed, this input reported an operator-policy failure.
			},
			wantGate: gateAssessmentEligible,
		},
		{
			name: "policy forbids automatic resume",
			mutate: func(as *GoalSupervisionAssessment, a *GoalSupervisionAction, _ *supervisionResumePayloadLoader, _ *supervisionResumeClock) {
				as.AutomaticResumeAllowed = false
				as.Policy.Mode = team.GoalSupervisionNotifyOnly
				// Re-derive through the CONSTRUCTOR rather than patching Available by hand, so the
				// canonical action stays whatever production says it is for this policy. Patching
				// fields here would re-introduce the second-definition problem inside the very table
				// built to catch decay.
				as.Actions = goalSupervisionActions(*as)
				*a = as.Actions.Resume
			},
			wantGate: gateOperatorPolicy,
		},
		{
			// dev-2's breadth finding: my first nine rows covered the first nine gates and left the LAST
			// TWO with no failure identity at all. The all-valid companion walks through them, but
			// walking through a gate is not the same as proving what it refuses -- a gate that never
			// fails in any test is a gate whose refusal is unproven.
			name: "action requires confirmation, so the delivering path refuses",
			mutate: func(as *GoalSupervisionAssessment, a *GoalSupervisionAction, _ *supervisionResumePayloadLoader, _ *supervisionResumeClock) {
				// MECHANISM, stated accurately: this DIRECTLY PATCHES the invoked action and the
				// assessment-published canon together. It does NOT call goalSupervisionActions -- my
				// earlier comment claimed it was "re-derived through the constructor", which was simply
				// false, and dev-2 caught the comment describing a mechanism the code does not use.
				//
				// The patching is deliberate and it is what isolates this gate: setting
				// NeedsConfirmation on BOTH sides keeps action-canonical passing (the equality is
				// action-vs-canon, and both moved), while operator-policy still passes because safe-auto
				// leaves AutomaticResumeAllowed true. So the delivering-path guard is the SOLE refuser,
				// which is the only way to prove its refusal identity.
				//
				// LIMITATION, since a fixture that cannot occur in production proves less than one that
				// can: the production constructor derives NeedsConfirmation FROM Policy.Mode, so
				// safe-auto with NeedsConfirmation=true is a state production would never build. This is
				// a direct-evaluator isolation fixture, not a production-realistic assessment. It proves
				// what the gate refuses; it does not prove that state is reachable.
				a.NeedsConfirmation = true
				as.Actions.Resume = *a
			},
			wantGate: gateDeliverableNoConf,
		},
		{
			name: "an existing claim for this pause blocks",
			mutate: func(_ *GoalSupervisionAssessment, _ *GoalSupervisionAction, _ *supervisionResumePayloadLoader, _ *supervisionResumeClock) {
				// Signalled via the blockingScan sentinel below rather than by mutating inputs, because
				// the claim-once gate is the only one whose verdict comes from the SCAN rather than from
				// the assessment or action.
			},
			wantGate: gateClaimOnce,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assessment, action, loader, clock := gateFixture(t)
			supervisor := "supervisor-1"
			if tc.wantGate == gateSupervisorID {
				supervisor = "   "
			}
			tc.mutate(&assessment, &action, &loader, &clock)

			scan := vacantScan
			if tc.wantGate == gateClaimOnce {
				// A ledger that reports an existing reservation for this pause. The blocker carries a
				// path so describe()/recovery() produce a real operator-facing refusal rather than an
				// empty one.
				scan = func(dir, _, _ string) (pauseLedgerScan, error) {
					return pauseLedgerScan{Blockers: []recoveryTransitionBlocker{{
						Path:   dir + "/.recovery-native-goal-resume-existing.json",
						Reason: "a recovery transition for this pause is reserved and NOT consumed",
					}}}, nil
				}
			}
			eval := evaluateSupervisionPreMutationGates(assessment, action, supervisor, clock, loader, scan)

			failed := eval.firstFailure()
			if failed == nil {
				t.Fatalf("expected gate %q to fail, but every gate passed", tc.wantGate)
			}
			// THE ORDER ASSERTION: the exact sequence of gates evaluated, prefix and all. This is what
			// makes reordering the evaluator break a test -- checking only the last entry cannot.
			gotNames := gateNamesOf(eval.Results)
			wantPrefix := canonicalGateOrderLiteral[:indexOfGate(t, tc.wantGate)+1]
			if !gateNamesEqual(gotNames, wantPrefix) {
				t.Errorf("evaluated gate SEQUENCE = %v, want exactly %v.\nA row that only checked its "+
					"failing gate would stay green if the evaluator reordered, because a single failure is "+
					"the first failure wherever it sits.", gotNames, wantPrefix)
			}
			if failed.Name != tc.wantGate {
				t.Errorf("FIRST FAILING GATE = %q, want %q.\nThe row still refuses, so a "+
					"did-it-refuse assertion would have passed while this row silently stopped testing "+
					"what it is named for. If a gate was added upstream, re-derive this row's defect "+
					"rather than re-pointing the expectation.", failed.Name, tc.wantGate)
			}
			// A failed evaluation must expose NO validated payload: a partially-authorized payload
			// reaching a caller is worse than none, because its authorization state is unknowable.
			if eval.Payload != "" || eval.AuthorizedDigest != "" {
				t.Errorf("a failed evaluation leaked a payload/digest baseline (payload=%q digest=%q)",
					eval.Payload, eval.AuthorizedDigest)
			}
			// And the unreached suffix must be reported, not silently dropped.
			if len(eval.skippedGates()) == 0 && failed.Name != gateClaimOnce {
				t.Errorf("gate %q failed but no gates were reported as skipped; the report presents a "+
					"prefix as the whole set", failed.Name)
			}
		})
	}
}

// The companion: the all-valid fixture must PASS every gate. Without it, every row above could be
// satisfied by an evaluator that refuses unconditionally.
func TestPreMutationGatesPassForAValidFixture(t *testing.T) {
	assessment, action, loader, clock := gateFixture(t)
	vacantScan := func(string, string, string) (pauseLedgerScan, error) { return pauseLedgerScan{}, nil }

	eval := evaluateSupervisionPreMutationGates(assessment, action, "supervisor-1", clock, loader, vacantScan)

	if f := eval.firstFailure(); f != nil {
		t.Fatalf("the all-valid fixture must pass every gate; %q failed: %s", f.Name, f.Detail)
	}
	// Full sequence against the LITERAL, not against the package's list -- see
	// canonicalGateOrderLiteral for why comparing to supervisionPreMutationGateOrder would permit
	// evaluator and list to drift together.
	if got := gateNamesOf(eval.Results); !gateNamesEqual(got, canonicalGateOrderLiteral) {
		t.Errorf("full-pass gate SEQUENCE = %v, want exactly %v", got, canonicalGateOrderLiteral)
	}
	// And the package's own ordering list must agree with the literal. If it does not, one of them is
	// wrong and this names the disagreement instead of letting the pair drift.
	if !gateNamesEqual(supervisionPreMutationGateOrder, canonicalGateOrderLiteral) {
		t.Errorf("supervisionPreMutationGateOrder %v disagrees with the hand-written canonical order %v",
			supervisionPreMutationGateOrder, canonicalGateOrderLiteral)
	}
	if eval.Payload == "" || eval.AuthorizedDigest == "" {
		t.Error("a full pass must carry the validated payload AND its pre-reserve digest baseline; " +
			"without the baseline the delivery-time drift gate has nothing to compare against")
	}
	if eval.AuthorizedDigest != digestGoalSupervisionString(eval.Payload) {
		t.Errorf("the carried baseline must be the digest of the carried payload")
	}
	if got := len(eval.skippedGates()); got != 0 {
		t.Errorf("a full pass skips nothing, got %d skipped", got)
	}
	if eval.ClaimKey == "" {
		t.Error("a full pass must carry the claim key: the dry-run reports it as the identity that " +
			"WOULD be reserved, and an empty one makes that report useless")
	}
}

// indexOfGate locates a gate in the literal order, failing loudly if the row names a gate the canonical
// list does not contain -- which would mean the row and the contract disagree about what gates exist.
func indexOfGate(t *testing.T, name supervisionGateName) int {
	t.Helper()
	for i, n := range canonicalGateOrderLiteral {
		if n == name {
			return i
		}
	}
	t.Fatalf("row names gate %q which is absent from the canonical order literal", name)
	return -1
}
