package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	executionModeGlobalOrchestrator = "global_orchestrator"
	executionModeProjectLead        = "project_lead"
	executionModeProjectTeam        = "project_team"
	executionModeDirectLeadSession  = "direct_lead_session"
)

type versionCompatibilityData struct {
	RunningVersion string `json:"running_version,omitempty"`
	TargetContract string `json:"target_contract,omitempty"`
	Compatible     bool   `json:"compatible"`
	Detail         string `json:"detail"`
}

type executionInvariantError struct {
	Code    string `json:"code"`
	Role    string `json:"role,omitempty"`
	Message string `json:"message"`
}

type executionModeData struct {
	Mode                  string                    `json:"mode"`
	ControlRoot           string                    `json:"control_root,omitempty"`
	TargetProjectRoot     string                    `json:"target_project_root,omitempty"`
	Profile               string                    `json:"profile,omitempty"`
	Session               string                    `json:"session,omitempty"`
	NamespaceID           string                    `json:"namespace_id,omitempty"`
	VisibleLead           string                    `json:"visible_lead,omitempty"`
	VisibleTeamMembers    []string                  `json:"visible_team_members,omitempty"`
	MutableActor          string                    `json:"mutable_actor,omitempty"`
	ImplementationAllowed bool                      `json:"implementation_allowed"`
	GoalBinding           string                    `json:"goal_binding,omitempty"`
	VisibilityTopology    string                    `json:"visibility_topology,omitempty"`
	PollingRequired       bool                      `json:"polling_required"`
	InvariantOK           bool                      `json:"invariant_ok"`
	InvariantErrors       []executionInvariantError `json:"invariant_errors,omitempty"`
	ModeError             string                    `json:"mode_error,omitempty"`
	Boundary              string                    `json:"boundary,omitempty"`
	VersionCompatibility  versionCompatibilityData  `json:"version_compatibility"`
}

func normalizeExecutionMode(mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return executionModeProjectLead, nil
	}
	switch mode {
	case executionModeGlobalOrchestrator, executionModeProjectLead, executionModeProjectTeam, executionModeDirectLeadSession:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported execution mode %q (want global_orchestrator, project_lead, project_team, or direct_lead_session)", mode)
	}
}

func cwdOrEmpty() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
}

func cleanRootOrDefault(root, fallback string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		root = strings.TrimSpace(fallback)
	}
	if root == "" {
		return ""
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(root)
}

func versionCompatibility(running, target string) versionCompatibilityData {
	running = strings.TrimSpace(running)
	target = strings.TrimPrefix(strings.TrimSpace(target), "v")
	if target == "" {
		target = "2.10.0"
	}
	if running == "" {
		return versionCompatibilityData{
			TargetContract: target,
			Compatible:     false,
			Detail:         "running amq-squad version is unavailable; cannot verify target contract compatibility",
		}
	}
	if strings.EqualFold(running, "dev") {
		return versionCompatibilityData{
			RunningVersion: running,
			TargetContract: target,
			Compatible:     true,
			Detail:         "running a dev build; target contract compatibility must be validated by tests before release",
		}
	}
	runParts, okRun := parseSemverParts(strings.TrimPrefix(running, "v"))
	targetParts, okTarget := parseSemverParts(target)
	if !okRun || !okTarget {
		return versionCompatibilityData{
			RunningVersion: running,
			TargetContract: target,
			Compatible:     false,
			Detail:         "could not parse running or target version; stop before relying on target-only workflow behavior",
		}
	}
	compatible := compareSemverParts(runParts, targetParts) >= 0
	detail := fmt.Sprintf("running amq-squad %s satisfies target contract %s", running, target)
	if !compatible {
		detail = fmt.Sprintf("running amq-squad %s is older than target contract %s; stop or use AMQ task + brief fallback before relying on target-only behavior", running, target)
	}
	return versionCompatibilityData{
		RunningVersion: running,
		TargetContract: target,
		Compatible:     compatible,
		Detail:         detail,
	}
}

func executionContract(mode, controlRoot, targetRoot, profile, session, namespaceID, lead, goalBinding, topology, runningVersion, targetContract string, visible []string) executionModeData {
	mode, _ = normalizeExecutionMode(mode)
	contract := executionModeData{
		Mode:                 mode,
		ControlRoot:          cleanRootOrDefault(controlRoot, cwdOrEmpty()),
		TargetProjectRoot:    cleanRootOrDefault(targetRoot, cwdOrEmpty()),
		Profile:              profile,
		Session:              session,
		NamespaceID:          namespaceID,
		VisibleLead:          lead,
		VisibleTeamMembers:   append([]string(nil), visible...),
		GoalBinding:          goalBinding,
		VisibilityTopology:   topology,
		InvariantOK:          true,
		VersionCompatibility: versionCompatibility(runningVersion, targetContract),
	}
	switch mode {
	case executionModeGlobalOrchestrator:
		contract.MutableActor = ""
		contract.ImplementationAllowed = false
		contract.PollingRequired = true
		contract.ModeError = "global_orchestrator cannot inspect or edit project code directly without an active project_lead, project_team, or explicit direct_lead_session conversion"
		contract.Boundary = "control-plane only: preview, select/register target, route approvals/directives, and monitor evidence"
	case executionModeProjectTeam:
		contract.MutableActor = lead
		contract.ImplementationAllowed = strings.TrimSpace(lead) != ""
		contract.PollingRequired = false
		contract.Boundary = "visible project team: lead owns goal and visible members may be inspected directly by the operator"
	case executionModeDirectLeadSession:
		contract.MutableActor = lead
		contract.ImplementationAllowed = strings.TrimSpace(lead) != ""
		contract.PollingRequired = false
		contract.Boundary = "current session is explicitly the project lead and may mutate the target project"
	default:
		contract.MutableActor = lead
		contract.ImplementationAllowed = strings.TrimSpace(lead) != ""
		contract.PollingRequired = false
		contract.Boundary = "one visible project-root lead owns implementation, validation, child delegation, and final evidence"
	}
	return contract
}

func defaultExecutionModeForTeam(orchestrated bool) string {
	if orchestrated {
		return executionModeProjectLead
	}
	return executionModeDirectLeadSession
}

func effectiveTeamExecutionMode(t team.Team) string {
	if mode, err := normalizeExecutionMode(t.ExecutionMode); err == nil && strings.TrimSpace(t.ExecutionMode) != "" {
		return mode
	}
	return defaultExecutionModeForTeam(t.Orchestrated)
}

func executionContractForTeam(t team.Team, profile, session, goalBinding, topology, runningVersion string) executionModeData {
	mode := effectiveTeamExecutionMode(t)
	lead := strings.TrimSpace(t.Lead)
	if lead == "" && len(t.Members) == 1 && (mode == executionModeProjectLead || mode == executionModeProjectTeam) {
		lead = t.Members[0].Role
	}
	controlRoot := strings.TrimSpace(t.ControlRoot)
	if controlRoot == "" {
		controlRoot = t.Project
	}
	targetRoot := strings.TrimSpace(t.TargetProjectRoot)
	if targetRoot == "" {
		targetRoot = t.Project
	}
	return executionContract(
		mode,
		controlRoot,
		targetRoot,
		profile,
		session,
		squadnamespace.Resolve(t.Project, profile, session).ID,
		lead,
		goalBinding,
		topology,
		runningVersion,
		t.TargetContract,
		visibleMembersForExecutionMode(mode, t, lead),
	)
}

func visibleMembersForExecutionMode(mode string, t team.Team, lead string) []string {
	if mode == executionModeProjectTeam {
		out := make([]string, 0, len(t.Members))
		for _, member := range t.Members {
			if strings.TrimSpace(member.Role) != "" {
				out = append(out, member.Role)
			}
		}
		return out
	}
	if strings.TrimSpace(lead) == "" {
		return nil
	}
	return []string{lead}
}

func topologyMode(topology *statusTopology) string {
	if topology == nil {
		return ""
	}
	return topology.Mode
}
