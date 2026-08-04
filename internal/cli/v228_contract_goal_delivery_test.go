package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
)

// AC9: a goal typed in the `start` wizard is delivered to the lead ONLY after
// every agent verifies live — the last act of a successful launch, never a
// precondition. A skipped goal can be assigned later with `goal`. A rolled-
// forward rerun of `start` never re-sends one.
//
// Delivery is a single human-initiated send: no dedup machinery, no delivery
// states, no supervision gates. That is what makes "never re-sends" a property
// of when the send is issued rather than of a suppression record.
//
// The three behavioral bodies live in v228_contract_goal_delivery_step3_test.go,
// written against the step-3 DeliverGoal seam and gated behind the v228step3
// build tag. This file keeps the half that is checkable on any base.

// TestV228ContractGoalIsNotALaunchPrecondition is the half of AC9 that is
// checkable today: whatever the goal step becomes, a launch must not gate on
// goal delivery. On this base `start` takes no goal at all, so the pin is that
// a successful start reports success and records every role without any goal
// input existing.
func TestV228ContractGoalIsNotALaunchPrecondition(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)

	const session = "ac9"
	roles := []string{"cto", "dev"}
	fixture := v228NewStartFixture(t, session, v228StartMembers(session, roles...))
	pids := map[string]int{"cto": 5101, "dev": 5102}
	alive := map[int]bool{}

	run := v228RunStart(t, fixture, alive, func(role string) int { return pids[role] })
	if run.Err != nil {
		t.Fatalf("start without any goal failed: %v\n%s", run.Err, run.Output)
	}
	if !strings.Contains(run.Output, "started "+session) {
		t.Errorf("start did not report success without a goal:\n%s", run.Output)
	}
	for _, role := range roles {
		rec, err := launch.Read(filepath.Join(fixture.Root, "agents", role))
		if err != nil {
			t.Fatalf("%s has no launch record after a goal-less start: %v", role, err)
		}
		// Goal binding is launch-time evidence of the deleted goal-in-launch
		// machinery. A goal-less simple start must not synthesize one.
		if rec.GoalBinding != nil {
			t.Errorf("%s recorded a goal binding for a goal-less start: %+v", role, rec.GoalBinding)
		}
	}
}
