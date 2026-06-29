package cli

// repair_fault_test.go covers the #265 repair-first UX: structured fault
// objects with canonical remedy actions on the no-visible-lead invariant path.

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// --- Unit tests for remedy helpers ---

func TestFaultRemedyRelaunchBuildsCommand(t *testing.T) {
	r := faultRemedyRelaunch(faultRepairScope{Session: "issue-96"})
	if r.Kind != r.ID || r.ID != "up" {
		t.Errorf("kind/id = %q/%q, want up/up", r.Kind, r.ID)
	}
	if r.ActionKind != "repair" {
		t.Errorf("action_kind = %q, want repair", r.ActionKind)
	}
	if !r.Available {
		t.Errorf("remedy must be available when session is known")
	}
	if !strings.Contains(r.Command, "amq-squad up") || !strings.Contains(r.Command, "issue-96") {
		t.Errorf("remedy command %q does not reference 'up' and session 'issue-96'", r.Command)
	}
}

func TestFaultRemedyRelaunchUnavailableWithoutSession(t *testing.T) {
	r := faultRemedyRelaunch(faultRepairScope{})
	if r.Available {
		t.Error("remedy must be unavailable when session is unknown")
	}
	if r.UnavailableReason == "" || r.Reason != r.UnavailableReason {
		t.Error("reason and unavailable_reason must be set and mirrored when remedy is unavailable")
	}
}

func TestFaultRemedyResumeBuildsCommand(t *testing.T) {
	r := faultRemedyResume("cto", faultRepairScope{Session: "issue-96"})
	if r.Kind != r.ID || r.ID != "resume" {
		t.Errorf("kind/id = %q/%q, want resume/resume", r.Kind, r.ID)
	}
	if r.ActionKind != "repair" {
		t.Errorf("action_kind = %q, want repair", r.ActionKind)
	}
	if !r.Available {
		t.Errorf("remedy must be available when role and session are known")
	}
	if !strings.Contains(r.Command, "amq-squad resume") ||
		!strings.Contains(r.Command, "cto") ||
		!strings.Contains(r.Command, "issue-96") {
		t.Errorf("remedy command %q missing expected tokens", r.Command)
	}
}

func TestFaultRemedyResumeUnavailableWithoutRole(t *testing.T) {
	r := faultRemedyResume("", faultRepairScope{Session: "issue-96"})
	if r.Available {
		t.Error("remedy must be unavailable when role is missing")
	}
}

func TestFaultRemedyCommandsIncludeProjectAndNamedProfile(t *testing.T) {
	scope := faultRepairScope{Project: "/repo", Profile: "review", Session: "issue-96"}
	relaunch := faultRemedyRelaunch(scope)
	for _, want := range []string{"--project /repo", "--profile review", "--session issue-96"} {
		if !strings.Contains(relaunch.Command, want) {
			t.Fatalf("relaunch command missing %q: %q", want, relaunch.Command)
		}
	}
	resume := faultRemedyResume("cto", scope)
	for _, want := range []string{"--role cto", "--project /repo", "--profile review", "--session issue-96"} {
		if !strings.Contains(resume.Command, want) {
			t.Fatalf("resume command missing %q: %q", want, resume.Command)
		}
	}
}

// --- Fault object field tests via invariantErrorForVisibilityProblem ---

func TestFaultObjectLeadPaneCollapsedHasRemedyAndDocRef(t *testing.T) {
	row := statusRecord{Role: "cto"}
	err := invariantErrorForVisibilityProblem(row, "current_pane_collapse", faultRepairScope{Session: "issue-96"})
	if err.Code != "lead_pane_collapsed" {
		t.Errorf("code = %q, want lead_pane_collapsed", err.Code)
	}
	if err.DocRef == "" {
		t.Error("doc_ref must be non-empty")
	}
	if err.Remedy == nil {
		t.Fatal("remedy must be present for lead_pane_collapsed")
	}
	if err.Remedy.ActionKind != "repair" {
		t.Errorf("remedy.action_kind = %q, want repair", err.Remedy.ActionKind)
	}
	if !err.Remedy.Available {
		t.Errorf("remedy.available = false, want true when session is known")
	}
	if !strings.Contains(err.Remedy.Command, "amq-squad up") {
		t.Errorf("collapsed lead remedy should suggest 'amq-squad up', got %q", err.Remedy.Command)
	}
}

func TestFaultObjectLeadPaneDeadHasResumeRemedy(t *testing.T) {
	row := statusRecord{Role: "cto"}
	err := invariantErrorForVisibilityProblem(row, "lead_pane_dead", faultRepairScope{Session: "issue-96"})
	if err.Code != "lead_pane_dead" {
		t.Errorf("code = %q, want lead_pane_dead", err.Code)
	}
	if err.Remedy == nil {
		t.Fatal("remedy must be present for lead_pane_dead")
	}
	if !strings.Contains(err.Remedy.Command, "amq-squad resume") {
		t.Errorf("dead-pane remedy should suggest 'amq-squad resume', got %q", err.Remedy.Command)
	}
	if err.Remedy.ID != "resume" {
		t.Errorf("remedy id = %q, want resume", err.Remedy.ID)
	}
}

func TestFaultObjectDetachedSessionHasResumeRemedy(t *testing.T) {
	row := statusRecord{Role: "cto"}
	err := invariantErrorForVisibilityProblem(row, "detached_session", faultRepairScope{Session: "issue-96"})
	if err.Remedy == nil || !strings.Contains(err.Remedy.Command, "amq-squad resume") {
		t.Errorf("detached session must suggest resume, got %+v", err.Remedy)
	}
}

func TestFaultObjectActionKindIsAlwaysRepair(t *testing.T) {
	row := statusRecord{Role: "cto"}
	for _, code := range []string{"current_pane_collapse", "lead_pane_dead", "detached_session", "pane_origin_unprovable", "other"} {
		err := invariantErrorForVisibilityProblem(row, code, faultRepairScope{Session: "s"})
		if err.Remedy == nil {
			t.Errorf("code %q: remedy must be present", code)
			continue
		}
		if err.Remedy.ActionKind != "repair" {
			t.Errorf("code %q: remedy action_kind = %q, want repair", code, err.Remedy.ActionKind)
		}
	}
}

// --- Integration test: structured fault appears in status --json ---

func TestRepairFaultAppearsInStatusJSONForCollapsedLead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "lead", Binary: "codex", Handle: "lead", Session: "fix-96"}},
		Orchestrated:  true,
		Lead:          "lead",
		ExecutionMode: executionModeProjectLead,
	})
	seedAgentRecord(t, base, "fix-96", "lead", launch.Record{
		Binary: "codex", Handle: "lead", Role: "lead", AgentPID: 7001,
		AdoptionMode: "bare_agent_up", LauncherPaneID: "%42",
		Tmux: &launch.TmuxInfo{Session: "root", WindowID: "@1", PaneID: "%42"},
	})
	swapStatusPaneLister(t, []tmuxpane.TmuxPane{{PaneID: "%42"}}, nil)

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "fix-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{7001: true}, map[int]bool{7001: true}, time.Now()),
		RuntimeVersion:   "2.12.0",
	})
	if err != nil {
		t.Fatalf("status exec: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	errs := env.Data.Execution.InvariantErrors
	if len(errs) == 0 {
		t.Fatal("expected invariant_errors for collapsed lead, got none")
	}
	fault := errs[0]
	if fault.Code != "lead_pane_collapsed" {
		t.Errorf("fault code = %q, want lead_pane_collapsed", fault.Code)
	}
	if fault.DocRef == "" {
		t.Error("fault doc_ref must be present in JSON output")
	}
	if fault.Remedy == nil {
		t.Fatal("fault remedy must be present in JSON output")
	}
	if fault.Remedy.ActionKind != "repair" {
		t.Errorf("fault remedy action_kind = %q, want repair", fault.Remedy.ActionKind)
	}
	if !fault.Remedy.Available {
		t.Errorf("fault remedy available = false, want true (session is known)")
	}
	if !strings.Contains(fault.Remedy.Command, "amq-squad up") || !strings.Contains(fault.Remedy.Command, "fix-96") {
		t.Errorf("fault remedy command %q should include 'amq-squad up' and session 'fix-96'", fault.Remedy.Command)
	}
}

func TestRepairFaultAppearsInStatusJSONForDeadPane(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:       []team.Member{{Role: "lead", Binary: "codex", Handle: "lead", Session: "fix-96"}},
		Orchestrated:  true,
		Lead:          "lead",
		ExecutionMode: executionModeProjectLead,
	})
	// Pane %77 is in the launch record but not in the live set → pane dead.
	seedAgentRecord(t, base, "fix-96", "lead", launch.Record{
		Binary: "codex", Handle: "lead", Role: "lead", AgentPID: 7002,
		Tmux: &launch.TmuxInfo{Session: "squad", WindowID: "@3", PaneID: "%77", Target: "new-window"},
	})
	swapStatusPaneLister(t, nil, nil) // no live panes

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "fix-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{}, map[int]bool{}, time.Now()),
		RuntimeVersion:   "2.12.0",
	})
	if err != nil {
		t.Fatalf("status exec: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	errs := env.Data.Execution.InvariantErrors
	if len(errs) == 0 {
		t.Fatal("expected invariant_errors for dead pane, got none")
	}
	fault := errs[0]
	if fault.Remedy == nil {
		t.Fatal("fault remedy must be present for dead pane")
	}
	if !strings.Contains(fault.Remedy.Command, "amq-squad resume") {
		t.Errorf("dead pane remedy should reference 'amq-squad resume', got %q", fault.Remedy.Command)
	}
	if !strings.Contains(fault.Remedy.Command, "fix-96") {
		t.Errorf("dead pane remedy command %q should include session 'fix-96'", fault.Remedy.Command)
	}
}
