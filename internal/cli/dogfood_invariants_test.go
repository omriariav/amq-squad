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
	"strings"
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
// Not previously tested in isolation. This is the only invariant in this file
// without primary coverage elsewhere.

func TestInvariantGoalDeliverRefusesDeadLeadPane(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "dogfood"},
		},
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "dogfood", "cto", launch.Record{
		CWD:     dir,
		Binary:  "codex",
		Handle:  "cto",
		Role:    "cto",
		Session: "dogfood",
		Tmux:    &launch.TmuxInfo{PaneID: "%99"},
	})
	// Pane %99 is not live: lister returns empty.
	oldLister := statusPaneLister
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) { return nil, nil }
	t.Cleanup(func() { statusPaneLister = oldLister })

	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--session", "dogfood", "--role", "cto", "--goal", "ship it"})
	})
	if err == nil {
		t.Fatal("invariant 2 violated: goal deliver must fail when lead pane is dead")
	}
	if !strings.Contains(err.Error(), "no live tmux pane") {
		t.Errorf("invariant 2: goal deliver error should mention 'no live tmux pane', got: %v", err)
	}
}

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
// Thin wrapper confirming goal deliver is covered by the guard.

func TestInvariantOperatorHandleRefusedByGoalDeliver(t *testing.T) {
	seedOperatorGuardTeam(t)
	setupFakeAMQSessionRoots(t)
	withOutputPolicy(t, outputPolicy{})

	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--session", "issue-96", "--role", "user", "--goal", "ship"})
	})
	assertOperatorMailboxOnlyError(t, err)
}

// --- Invariant 5: lead-owned mailbox boundary blocks global/orchestrator drain ---
//
// Primary coverage: TestRunCollectBlocksNonOwnerMailboxInProjectTeam (collect_test.go)
//   TestRunCollectBlocksNonOwnerMailboxInNamedProfile, TestRunCollectOverrideRequiresReason,
//   TestRunCollectOverrideWritesAuditAndExecutes.
// Thin wrapper confirming the error message is stable for regression detection.

func TestInvariantMailboxBoundaryBlocksOrchestrator(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	writeAMQBoundaryTeam(t, dir)
	t.Setenv("AM_ME", "cto")
	withCollectAMQSeams(t, amqEnv{Root: ".agent-mail/{session}", BaseRoot: ".agent-mail"}, []string{"msg\n"})

	_, _, err := captureOutput(t, func() error {
		return runCollect([]string{"--session", "issue-96", "--me", "qa", "--include-body"})
	})
	if err == nil {
		t.Fatal("invariant 5 violated: cross-boundary collect must be refused")
	}
	if !strings.Contains(err.Error(), "lead-owned mailbox") {
		t.Errorf("invariant 5: boundary error should mention 'lead-owned mailbox', got: %v", err)
	}
	if !strings.Contains(err.Error(), "--override-boundary") {
		t.Errorf("invariant 5: boundary error should mention '--override-boundary', got: %v", err)
	}
}

// --- Invariant 6: runtime action JSON marks direct child mutating actions unavailable
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

	// Lead's own actions must be unaffected.
	leadActions := policyAwareMemberActions(leadTeam, "default", "dogfood", "lead", true)
	leadByKind := actionsByKind(leadActions)
	for _, mutating := range []string{"send", "goal_deliver", "dispatch"} {
		if a, ok := leadByKind[mutating]; !ok || !a.Available {
			t.Errorf("invariant 6: lead action %q must remain available for the lead itself", mutating)
		}
	}
}
