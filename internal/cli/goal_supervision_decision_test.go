package cli

import "testing"

// #498 U6: THE DECISION MAPPING, EXECUTED rather than read.
//
// This REPLACES TestTheTwoIndeterminateStatesRenderAsDistinctLiterals, which asserted that two string
// literals APPEAR in goal_supervise_resume.go. That is a fact about a file, not about behaviour: it
// survives SWAPPING the two switch cases, which is exactly the confusion the delivery_outcome_unknown
// rename was ruled to prevent. I disclosed it as the weakest thing in the U5/U6 work at the time; this is
// the promised replacement, made possible by extracting the mapping into a pure function.
func TestEveryOutcomeStateMapsToItsOwnDecisionLiteral(t *testing.T) {
	// HAND-WRITTEN expectations, deliberately NOT referencing whatever the mapping computes. Comparing the
	// function against its own constants would pass however the pair drifted -- including back into
	// near-identical names, the defect the rename fixed. Same reason the gate-order row compares against a
	// literal instead of against supervisionPreMutationGateOrder.
	rows := []struct {
		name    string
		outcome supervisionResumeOutcome
		want    string
	}{
		{
			// Unknown pane input. Delivered stays FALSE because success is not known; the discriminator is
			// the only thing separating this from an ordinary refusal.
			name:    "unconfirmed pane input",
			outcome: supervisionResumeOutcome{DeliveryOutcomeUnknown: true},
			want:    "delivery_outcome_unknown",
		},
		{
			// KNOWN delivery, consume failed. Materially different from the row above: here an
			// irreversible action demonstrably happened.
			name:    "known delivery with failed consume",
			outcome: supervisionResumeOutcome{Delivered: true, ConsumeError: "publish failed"},
			want:    "delivered_indeterminate",
		},
		{
			name:    "known delivery and consume, later error",
			outcome: supervisionResumeOutcome{Delivered: true, Consumed: true},
			want:    "delivered_with_error",
		},
		{
			// The zero outcome: refused before anything happened.
			name:    "ordinary pre-delivery refusal",
			outcome: supervisionResumeOutcome{},
			want:    "refused",
		},
	}

	seen := map[string]string{}
	for _, row := range rows {
		got := supervisionResumeDecision(row.outcome)
		if got != row.want {
			t.Errorf("%s: decision = %q, want %q", row.name, got, row.want)
		}
		// NO TWO STATES MAY SHARE A LITERAL. Four rows each asserting their own value would still pass if
		// two states collapsed onto one string, and a collapsed pair is exactly what U6 kept
		// rediscovering -- unknown delivery reported as "refused".
		if prev, dup := seen[got]; dup {
			t.Errorf("%s and %s both report %q; distinct outcome states must be distinguishable in "+
				"operator evidence", prev, row.name, got)
		}
		seen[got] = row.name
	}
	if len(seen) != len(rows) {
		t.Errorf("%d outcome states produced only %d distinct literals", len(rows), len(seen))
	}
}

// PRECEDENCE. DeliveryOutcomeUnknown must outrank Delivered, because a defensive caller that set both
// would otherwise be reported as a known delivery -- claiming an irreversible action that is NOT known to
// have happened. Over-reporting an irreversible action is worse than under-reporting it, so uncertainty
// wins.
func TestUnknownDeliveryOutranksDeliveredWhenBothAreSet(t *testing.T) {
	got := supervisionResumeDecision(supervisionResumeOutcome{DeliveryOutcomeUnknown: true, Delivered: true})
	if got != "delivery_outcome_unknown" {
		t.Errorf("decision = %q, want %q: uncertainty must outrank a claimed delivery", got, "delivery_outcome_unknown")
	}
}

// THE TWO INDETERMINATE LITERALS MUST STAY DISTINGUISHABLE AT A GLANCE. Technically-unequal strings that
// differ by two characters are practically identical in an operator's evidence, which is the defect the
// rename corrected. This keeps that property enforced now that the source-text row is gone.
func TestTheTwoIndeterminateLiteralsAreNotConfusable(t *testing.T) {
	unknownDelivery := supervisionResumeDecision(supervisionResumeOutcome{DeliveryOutcomeUnknown: true})
	knownUnconsumed := supervisionResumeDecision(supervisionResumeOutcome{Delivered: true})

	if unknownDelivery == knownUnconsumed {
		t.Fatalf("both states report %q", unknownDelivery)
	}
	shared := 0
	for shared < len(unknownDelivery) && shared < len(knownUnconsumed) &&
		unknownDelivery[shared] == knownUnconsumed[shared] {
		shared++
	}
	if shared > len("delivered") {
		t.Errorf("%q and %q share a %d-character prefix -- too long to tell apart in operator evidence",
			unknownDelivery, knownUnconsumed, shared)
	}
}
