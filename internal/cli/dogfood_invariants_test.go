package cli

// dogfood_invariants_test.go is the v2.11 orchestration regression baseline.
//
// Each test below corresponds to one invariant from docs/v2.12.0-plan.md
// section "Dogfood Regression Suite" (#263). The tests are named
// TestInvariant* so they are discoverable as a group.
//
// Where an invariant is already well-covered by focused unit tests, this file
// adds a thin named wrapper and a comment pointing to the primary coverage so
// maintainers have a single register of all v2.11 invariants.

import (
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// --- Invariant 1: manual agent up from current pane is not a valid visible lead ---
//
// Primary coverage: TestExecuteStatusJSONFlagsCurrentPaneCollapsedLead (status_test.go)
// This wrapper asserts the key fields directly and names the invariant.

func TestInvariantCurrentPaneCollapseNotOperatorVisible(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "lead", Binary: "codex", Handle: "lead", Session: "dogfood"}},
		Orchestrated:  true,
		Lead:          "lead",
		ExecutionMode: executionModeProjectLead,
	})
	// bare_agent_up + same launcher_pane_id == agent_pane_id => collapsed lead.
	seedAgentRecord(t, base, "dogfood", "lead", launch.Record{
		Binary: "codex", Handle: "lead", Role: "lead", AgentPID: 9001,
		AdoptionMode: "bare_agent_up", LauncherPaneID: "%10",
		Tmux: &launch.TmuxInfo{Session: "root", WindowID: "@1", PaneID: "%10"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%10"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "dogfood",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{9001: true}, map[int]bool{9001: true}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	lead := env.Data.Records[0]
	if lead.OperatorVisible {
		t.Error("invariant 1 violated: bare_agent_up current-pane lead must not be operator_visible")
	}
	if !lead.CurrentPaneConflict {
		t.Error("invariant 1 violated: bare_agent_up current-pane lead must have current_pane_conflict=true")
	}
	if env.Data.Execution.InvariantOK {
		t.Error("invariant 1 violated: execution invariant_ok must be false for collapsed lead")
	}
}

// --- Invariant 2: goal deliver refuses non-live/dead targets ---
//
// `goal deliver` (and the runtime pane-injection delivery it invariant-tested)
// was removed in v2.31.0 (gh#761) -- goal delivery is now launch-time
// InitialInput, not a runtime verb this invariant applies to. Successor
// assertion: TestGoalLegacySubcommandsAreGone (goal_test.go).

// --- Invariant 3: status --json exposes operator_visible, invariant_ok, invariant_errors ---
//
// Primary coverage: TestExecuteStatusJSONFlagsDetachedVisibleLeadInvariant (status_test.go)
//   and TestExecuteStatusJSONMarksOperatorVisibleLead (status_test.go).
// Thin wrapper confirming the fields are present for a known-bad state.

func TestInvariantStatusJSONExposesVisibleLeadFields(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "lead", Binary: "codex", Handle: "lead", Session: "dogfood"}},
		Orchestrated:  true,
		Lead:          "lead",
		ExecutionMode: executionModeProjectLead,
	})
	// Dead pane: pane %55 is not in the live set, so the lead is detached.
	seedAgentRecord(t, base, "dogfood", "lead", launch.Record{
		Binary: "codex", Handle: "lead", Role: "lead", AgentPID: 8001,
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@2", PaneID: "%55", Target: "new-window"},
	})
	swapStatusPaneLister(t, nil, nil) // no live panes

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "dogfood",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{}, map[int]bool{}, time.Now()),
		RuntimeVersion:   "2.11.0",
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	lead := env.Data.Records[0]
	if lead.OperatorVisible {
		t.Error("invariant 3: detached lead must have operator_visible=false")
	}
	exec := env.Data.Execution
	if exec.InvariantOK {
		t.Error("invariant 3: invariant_ok must be false when visible lead is absent")
	}
	if len(exec.InvariantErrors) == 0 {
		t.Error("invariant 3: invariant_errors must be non-empty when visible lead is absent")
	}
}

// --- Invariant 4: operator handle refused by lifecycle/control/task/goal/lead operations ---
//
// Primary coverage: TestRoleControlCommandsRefuseOperatorTarget (operator_guard_test.go)
//   and TestAgentUpRefusesOperatorRoleAndHandle (operator_guard_test.go)
//   and TestTaskOperatorGuardRefusesBareUserWithNoTeam (task_operator_guard_test.go).
// `goal deliver` was removed in v2.31.0 (gh#761); this file's thin wrapper for
// it (and operator_guard_test.go's own "goal_deliver" case in
// TestRoleControlCommandsRefuseOperatorTarget) were removed alongside it --
// the operator-handle guard itself is unchanged and still covered by the
// other role-control invariants above.

// --- Invariant 5: runtime action JSON marks direct child mutating actions unavailable
//     in project-lead and project-team modes ---
//
// Primary coverage: json_envelopes_helpers_test.go (applyMemberActionPolicy checks at lines ~777-788)
//   TestStatusBoardJSONMemberActionPolicyBlocksChildMutations (status_board_test.go if present).
// Thin wrapper calling the policy function directly so it's visible as a named invariant.

func TestInvariantRuntimeActionsBlockChildMutationsInProjectLeadMode(t *testing.T) {
	t.Parallel()
	leadTeam := team.Team{
		Members: []team.Member{
			{Role: "lead", Binary: "codex", Handle: "lead"},
			{Role: "worker", Binary: "claude", Handle: "worker"},
		},
		Orchestrated:  true,
		Lead:          "lead",
		ExecutionMode: executionModeProjectLead,
	}
	workerActions := policyAwareMemberActions(leadTeam, "default", "dogfood", "worker", true)
	byKind := actionsByKind(workerActions)

	for _, mutating := range []string{"send", "goal_deliver", "dispatch"} {
		a, ok := byKind[mutating]
		if !ok {
			t.Errorf("invariant 6: action %q missing from worker actions", mutating)
			continue
		}
		if a.Available {
			t.Errorf("invariant 6: action %q must be unavailable for worker in project_lead mode", mutating)
		}
		if a.Reason == "" {
			t.Errorf("invariant 6: action %q must carry a reason when unavailable", mutating)
		}
	}

	// Read-only actions must remain available for worker.
	for _, readonly := range []string{"focus", "status", "task_list"} {
		a, ok := byKind[readonly]
		if !ok {
			t.Errorf("invariant 6: read-only action %q missing from worker actions", readonly)
			continue
		}
		if !a.Available {
			t.Errorf("invariant 6: read-only action %q must remain available for worker", readonly)
		}
	}

	// Lead's own actions must be unaffected by the child-mutation policy.
	// goal_deliver is excluded: gh#761 t15 retired it unconditionally in
	// v2.31.0, so it is unavailable for lead and worker alike -- that is a
	// global retirement, not a project-lead-mode child-mutation block.
	leadActions := policyAwareMemberActions(leadTeam, "default", "dogfood", "lead", true)
	leadByKind := actionsByKind(leadActions)
	for _, mutating := range []string{"send", "dispatch"} {
		if a, ok := leadByKind[mutating]; !ok || !a.Available {
			t.Errorf("invariant 6: lead action %q must remain available for the lead itself", mutating)
		}
	}
	if a, ok := leadByKind["goal_deliver"]; !ok || a.Available {
		t.Errorf("invariant 6: goal_deliver must stay unconditionally retired, even for the lead: %+v", a)
	}
}
