package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// The execution modes are squad.yaml's (team.ExecutionModes).
const (
	executionModeGlobalOrchestrator = team.ExecutionModeGlobalOrchestrator
	executionModeProjectLead        = team.ExecutionModeProjectLead
	executionModeProjectTeam        = team.ExecutionModeProjectTeam
	executionModeDirectLeadSession  = team.ExecutionModeDirectLeadSession
)

// faultRemedy is a structured recovery action that follows the canonical
// action-object contract (docs/action-object-contract.md). ActionKind is
// always "repair" for fault remedy objects emitted alongside invariant errors.
type faultRemedy struct {
	Kind              string `json:"kind,omitempty"`
	ID                string `json:"id"`
	Label             string `json:"label"`
	ActionKind        string `json:"action_kind"` // always "repair"
	Command           string `json:"command,omitempty"`
	Available         bool   `json:"available"`
	Reason            string `json:"reason,omitempty"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type leadExecutionData struct {
	Declared            bool     `json:"declared"`
	Posture             string   `json:"posture"`
	GoalSignificance    string   `json:"goal_significance"`
	DecisionTime        string   `json:"decision_time,omitempty"`
	Reason              string   `json:"reason,omitempty"`
	ChildBudget         int      `json:"child_budget,omitempty"`
	PlannedDelegations  []string `json:"planned_delegations,omitempty"`
	ReviewPlan          string   `json:"review_plan,omitempty"`
	FinalRecommendation string   `json:"final_recommendation,omitempty"`
}

// undeclaredLeadExecution: squad.yaml carries no v2 lead-execution posture.
var undeclaredLeadExecution = leadExecutionData{Posture: "undeclared", GoalSignificance: "standard"}

type executionModeData struct {
	Mode                  string            `json:"mode"`
	ControlRoot           string            `json:"control_root,omitempty"`
	TargetProjectRoot     string            `json:"target_project_root,omitempty"`
	Profile               string            `json:"profile,omitempty"`
	Session               string            `json:"session,omitempty"`
	NamespaceID           string            `json:"namespace_id,omitempty"`
	VisibleLead           string            `json:"visible_lead,omitempty"`
	LeadMode              string            `json:"lead_mode"`
	MutableActor          string            `json:"mutable_actor,omitempty"`
	ImplementationAllowed bool              `json:"implementation_allowed"`
	GoalBinding           string            `json:"goal_binding,omitempty"`
	VisibilityTopology    string            `json:"visibility_topology,omitempty"`
	PollingRequired       bool              `json:"polling_required"`
	ModeError             string            `json:"mode_error,omitempty"`
	Boundary              string            `json:"boundary,omitempty"`
	LeadExecution         leadExecutionData `json:"lead_execution"`
}

// actorExecutionData is the execution contract rendered to one launched
// member. executionModeData remains the team/lead status surface; this view is
// deliberately actor-relative so a planner lead's no-implementation posture
// can never be inherited by implementation workers.
type actorExecutionData struct {
	ActorRole                   string `json:"actor_role"`
	ActorHandle                 string `json:"actor_handle"`
	IsLead                      bool   `json:"is_lead"`
	ImplementationAllowedForYou bool   `json:"implementation_allowed_for_you"`
	DelegationAllowedForYou     bool   `json:"delegation_allowed_for_you"`
	TeamLeadMode                string `json:"team_lead_mode"`
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

func executionContract(mode, controlRoot, targetRoot, profile, session, namespaceID, lead, goalBinding, topology string) executionModeData {
	mode, _ = normalizeExecutionMode(mode)
	contract := executionModeData{
		Mode:               mode,
		ControlRoot:        cleanRootOrDefault(controlRoot, cwdOrEmpty()),
		TargetProjectRoot:  cleanRootOrDefault(targetRoot, cwdOrEmpty()),
		Profile:            profile,
		Session:            session,
		NamespaceID:        namespaceID,
		VisibleLead:        lead,
		LeadMode:           team.LeadModeBuilder,
		GoalBinding:        goalBinding,
		VisibilityTopology: topology,
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
	contract.LeadExecution = undeclaredLeadExecution
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

func executionContractForTeam(t team.Team, profile, session, goalBinding, topology string) executionModeData {
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
	contract := executionContract(mode, controlRoot, targetRoot, profile, session,
		squadnamespace.Resolve(t.Project, profile, session).ID, lead, goalBinding, topology)
	applyLeadModeContract(&contract, t, lead)
	return contract
}

func actorExecutionContractForTeam(t team.Team, actorRole, actorHandle string, contract executionModeData) actorExecutionData {
	actorRole = strings.TrimSpace(actorRole)
	actorHandle = strings.TrimSpace(actorHandle)
	if actorHandle == "" {
		actorHandle = actorRole
	}
	lead := strings.TrimSpace(t.Lead)
	member, rostered := actorRosterMemberForTeam(t, actorRole, actorHandle)
	isLead := rostered && lead != "" && team.IsLead(t, member)
	implementationAllowed := false
	delegationAllowed := false
	if rostered {
		actorMode := team.EffectiveActorMode(t, member)
		if isLead {
			implementationAllowed = contract.ImplementationAllowed && actorMode == team.ActorModeImplementation
			delegationAllowed = t.Orchestrated
		} else if actorMode == team.ActorModeImplementation {
			switch contract.Mode {
			case executionModeProjectLead, executionModeProjectTeam:
				// The team lead mode constrains the lead, not assigned workers.
				// The authored role and durable task remain the worker's scope.
				implementationAllowed = true
			}
		}
	}
	return actorExecutionData{
		ActorRole:                   actorRole,
		ActorHandle:                 actorHandle,
		IsLead:                      isLead,
		ImplementationAllowedForYou: implementationAllowed,
		DelegationAllowedForYou:     delegationAllowed,
		TeamLeadMode:                team.EffectiveLeadMode(t),
	}
}

func actorRosterMemberForTeam(t team.Team, actorRole, actorHandle string) (team.Member, bool) {
	actorRole = strings.TrimSpace(actorRole)
	actorHandle = strings.TrimSpace(actorHandle)
	if actorHandle == "" {
		actorHandle = actorRole
	}
	for _, member := range t.Members {
		if strings.TrimSpace(member.Role) == actorRole && strings.TrimSpace(memberHandle(member)) == actorHandle {
			return member, true
		}
	}
	return team.Member{}, false
}

func applyLeadModeContract(contract *executionModeData, t team.Team, lead string) {
	if contract == nil {
		return
	}
	leadMode := team.EffectiveLeadMode(t)
	contract.LeadMode = leadMode
	if leadMode != team.LeadModePlanner {
		return
	}
	switch contract.Mode {
	case executionModeProjectLead, executionModeProjectTeam, executionModeDirectLeadSession:
	default:
		return
	}
	contract.MutableActor = plannerMutableActor(t, lead)
	contract.ImplementationAllowed = false
	contract.Boundary = "planner/reviewer lead owns planning, dispatch, review, gates, and final evidence; implementation must be delegated to workers"
}

func plannerMutableActor(t team.Team, lead string) string {
	roles := make([]string, 0, len(t.Members))
	for _, member := range t.Members {
		roles = append(roles, member.Role)
	}
	return plannerMutableActorFromRoles(lead, roles)
}

func plannerMutableActorFromRoles(lead string, roles []string) string {
	lead = strings.TrimSpace(lead)
	var workers []string
	for _, role := range roles {
		role = strings.TrimSpace(role)
		if role == "" || role == lead {
			continue
		}
		workers = append(workers, role)
	}
	if len(workers) == 1 {
		return workers[0]
	}
	return "delegated_workers"
}
