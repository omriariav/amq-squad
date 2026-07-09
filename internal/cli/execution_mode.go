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

type executionInvariantError struct {
	Code    string       `json:"code"`
	Role    string       `json:"role,omitempty"`
	Message string       `json:"message"`
	DocRef  string       `json:"doc_ref,omitempty"`
	Remedy  *faultRemedy `json:"remedy,omitempty"`
}

type leadExecutionData struct {
	Declared            bool                  `json:"declared"`
	Posture             string                `json:"posture"`
	GoalSignificance    string                `json:"goal_significance"`
	DecisionTime        string                `json:"decision_time,omitempty"`
	Reason              string                `json:"reason,omitempty"`
	ChildBudget         int                   `json:"child_budget,omitempty"`
	PlannedDelegations  []string              `json:"planned_delegations,omitempty"`
	ReviewPlan          string                `json:"review_plan,omitempty"`
	IndependentReview   independentReviewData `json:"independent_review"`
	FinalRecommendation string                `json:"final_recommendation,omitempty"`
}

type releaseReadinessGateData struct {
	ID       string `json:"id"`
	Required bool   `json:"required"`
	Passed   bool   `json:"passed"`
	Detail   string `json:"detail"`
	Evidence string `json:"evidence,omitempty"`
}

type mergeAuthorityData struct {
	DefaultActor     string   `json:"default_actor"`
	WorkerPolicy     string   `json:"worker_policy"`
	Alternative      string   `json:"alternative,omitempty"`
	LifecycleActions []string `json:"lifecycle_actions,omitempty"`
	Detail           string   `json:"detail"`
}

type releaseReadinessData struct {
	Ready          bool                       `json:"ready"`
	State          string                     `json:"state"`
	Authority      string                     `json:"authority"`
	MergeAuthority mergeAuthorityData         `json:"merge_authority"`
	Detail         string                     `json:"detail"`
	Gates          []releaseReadinessGateData `json:"gates"`
}

type independentReviewData struct {
	Status    string `json:"status"`
	Evidence  string `json:"evidence,omitempty"`
	Reviewer  string `json:"reviewer,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	Reference string `json:"reference,omitempty"`
	Reason    string `json:"reason,omitempty"`
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
	LeadMode              string                    `json:"lead_mode,omitempty"`
	MutableActor          string                    `json:"mutable_actor,omitempty"`
	ImplementationAllowed bool                      `json:"implementation_allowed"`
	GoalBinding           string                    `json:"goal_binding,omitempty"`
	VisibilityTopology    string                    `json:"visibility_topology,omitempty"`
	PollingRequired       bool                      `json:"polling_required"`
	InvariantsEvaluated   bool                      `json:"invariants_evaluated"`
	InvariantOK           bool                      `json:"invariant_ok"`
	InvariantErrors       []executionInvariantError `json:"invariant_errors,omitempty"`
	ModeError             string                    `json:"mode_error,omitempty"`
	Boundary              string                    `json:"boundary,omitempty"`
	VersionCompatibility  versionCompatibilityData  `json:"version_compatibility"`
	LeadExecution         leadExecutionData         `json:"lead_execution"`
	ReleaseReadiness      releaseReadinessData      `json:"release_readiness"`
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

func normalizeLeadMode(mode string) (string, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return team.LeadModeBuilder, nil
	}
	switch mode {
	case team.LeadModeBuilder, team.LeadModePlanner:
		return mode, nil
	default:
		return "", fmt.Errorf("unsupported lead mode %q (want builder or planner)", mode)
	}
}

func leadModeForPersist(mode string) string {
	if strings.TrimSpace(mode) == "" || strings.TrimSpace(mode) == team.LeadModeBuilder {
		return ""
	}
	return strings.TrimSpace(mode)
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
		LeadMode:             team.LeadModeBuilder,
		GoalBinding:          goalBinding,
		VisibilityTopology:   topology,
		InvariantsEvaluated:  false,
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
	applyLeadExecutionContract(&contract, nil)
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
	contract := executionContract(
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
	applyLeadModeContract(&contract, t, lead)
	applyLeadExecutionContract(&contract, t.LeadExecution)
	return contract
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

func applyLeadModeToDraftContract(contract *executionModeData, leadMode string, lead string, roster []goalRosterMember) {
	if contract == nil {
		return
	}
	leadMode = strings.TrimSpace(leadMode)
	if leadMode == "" {
		leadMode = team.LeadModeBuilder
	}
	contract.LeadMode = leadMode
	if leadMode != team.LeadModePlanner {
		return
	}
	switch contract.Mode {
	case executionModeProjectLead, executionModeProjectTeam, executionModeDirectLeadSession:
	default:
		return
	}
	contract.MutableActor = plannerMutableActorFromRoles(lead, rosterRoles(roster))
	contract.ImplementationAllowed = false
	contract.Boundary = "planner/reviewer lead owns planning, dispatch, review, gates, and final evidence; implementation must be delegated to workers"
	contract.ReleaseReadiness = releaseReadinessForExecution(*contract)
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

func rosterRoles(roster []goalRosterMember) []string {
	roles := make([]string, 0, len(roster))
	for _, member := range roster {
		roles = append(roles, member.Role)
	}
	return roles
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

func applyLeadExecutionContract(contract *executionModeData, exec *team.LeadExecution) {
	if contract == nil {
		return
	}
	contract.LeadExecution = leadExecutionDataForConfig(exec)
	contract.ReleaseReadiness = releaseReadinessForExecution(*contract)
}

func leadExecutionDataForConfig(exec *team.LeadExecution) leadExecutionData {
	if exec == nil {
		return leadExecutionData{
			Declared:          false,
			Posture:           "undeclared",
			GoalSignificance:  team.GoalSignificanceStandard,
			IndependentReview: independentReviewData{Status: team.IndependentReviewRequired},
		}
	}
	significance := strings.TrimSpace(exec.GoalSignificance)
	if significance == "" {
		significance = team.GoalSignificanceStandard
	}
	return leadExecutionData{
		Declared:            true,
		Posture:             strings.TrimSpace(exec.Posture),
		GoalSignificance:    significance,
		DecisionTime:        strings.TrimSpace(exec.DecisionTime),
		Reason:              strings.TrimSpace(exec.Reason),
		ChildBudget:         exec.ChildBudget,
		PlannedDelegations:  append([]string(nil), exec.PlannedDelegations...),
		ReviewPlan:          strings.TrimSpace(exec.ReviewPlan),
		IndependentReview:   independentReviewDataForConfig(exec.IndependentReview),
		FinalRecommendation: strings.TrimSpace(exec.FinalRecommendation),
	}
}

func independentReviewDataForConfig(review *team.IndependentReview) independentReviewData {
	if review == nil {
		return independentReviewData{Status: team.IndependentReviewRequired}
	}
	status := strings.TrimSpace(review.Status)
	if status == "" {
		status = team.IndependentReviewRequired
	}
	return independentReviewData{
		Status:    status,
		Evidence:  strings.TrimSpace(review.Evidence),
		Reviewer:  strings.TrimSpace(review.Reviewer),
		ThreadID:  strings.TrimSpace(review.ThreadID),
		Reference: strings.TrimSpace(review.Reference),
		Reason:    strings.TrimSpace(review.Reason),
	}
}

func releaseReadinessForExecution(contract executionModeData) releaseReadinessData {
	leadExec := contract.LeadExecution
	gates := []releaseReadinessGateData{
		{
			ID:       "visible_lead_invariants_ok",
			Required: true,
			Passed:   contract.InvariantsEvaluated && contract.InvariantOK,
			Detail:   visibleLeadInvariantDetail(contract),
			Evidence: invariantEvidence(contract),
		},
		{
			ID:       "lead_execution_declared",
			Required: true,
			Passed:   leadExec.Declared && leadExec.Posture != "" && leadExec.Posture != "undeclared",
			Detail:   "release lead declares solo, delegated, or visible_team execution posture",
			Evidence: leadExec.Posture,
		},
		{
			ID:       "lead_final_recommendation",
			Required: true,
			Passed:   strings.TrimSpace(leadExec.FinalRecommendation) != "",
			Detail:   "lead-owned final recommendation is present",
			Evidence: leadExec.FinalRecommendation,
		},
		{
			ID:       "independent_review_evidence_or_waiver",
			Required: true,
			Passed:   independentReviewGatePassed(leadExec.IndependentReview),
			Detail:   "independent review is complete with evidence, or explicitly waived with a reason",
			Evidence: independentReviewEvidence(leadExec.IndependentReview),
		},
	}
	if requiresSoloJustification(contract, leadExec) {
		gates = append(gates, releaseReadinessGateData{
			ID:       "solo_justification_for_non_trivial_goal",
			Required: true,
			Passed:   strings.TrimSpace(leadExec.Reason) != "",
			Detail:   "solo release execution on a non-trivial goal has an explicit justification",
			Evidence: leadExec.Reason,
		})
	}
	ready := true
	for _, gate := range gates {
		if gate.Required && !gate.Passed {
			ready = false
			break
		}
	}
	state := "blocked"
	if ready {
		state = "ready"
	} else if !contract.InvariantsEvaluated {
		state = "not_evaluated"
	}
	return releaseReadinessData{
		Ready:          ready,
		State:          state,
		Authority:      "declared_evidence_only",
		MergeAuthority: defaultMergeAuthority(),
		Detail:         "release readiness surfaces declared posture and evidence pointers; it does not independently authorize release or verify external evidence",
		Gates:          gates,
	}
}

func defaultMergeAuthority() mergeAuthorityData {
	return mergeAuthorityData{
		DefaultActor: "visible_lead",
		WorkerPolicy: "workers_do_not_merge_by_default",
		Alternative:  "worker_merge_requires_verifiable_authorization_artifact",
		LifecycleActions: []string{
			"merge",
			"push",
			"tag",
			"release",
			"issue_close",
		},
		Detail: "visible lead owns the merge and lifecycle action path by default after exact-head readiness gates and operator approval; workers escalate merge, push, tag, release, or issue-close requests unless an explicit verifiable authorization artifact binds the request to the same subject, head, and gate evidence",
	}
}

func requiresSoloJustification(contract executionModeData, leadExec leadExecutionData) bool {
	if leadExec.Posture != team.LeadExecutionSolo {
		return false
	}
	if leadExec.GoalSignificance == team.GoalSignificanceTrivial {
		return false
	}
	switch contract.Mode {
	case executionModeProjectLead, executionModeProjectTeam:
		return true
	default:
		return false
	}
}

func invariantEvidence(contract executionModeData) string {
	if !contract.InvariantsEvaluated {
		return "not_evaluated"
	}
	if contract.InvariantOK {
		return "ok"
	}
	if len(contract.InvariantErrors) == 0 {
		return "failed"
	}
	parts := make([]string, 0, len(contract.InvariantErrors))
	for _, err := range contract.InvariantErrors {
		parts = append(parts, err.Code)
	}
	return strings.Join(parts, ",")
}

func visibleLeadInvariantDetail(contract executionModeData) string {
	if !contract.InvariantsEvaluated {
		return "visible lead invariants were not evaluated on this static/config surface"
	}
	return "visible lead invariants are satisfied"
}

func independentReviewGatePassed(review independentReviewData) bool {
	switch review.Status {
	case team.IndependentReviewComplete:
		return independentReviewEvidencePresent(review)
	case team.IndependentReviewWaived:
		return strings.TrimSpace(review.Reason) != ""
	default:
		return false
	}
}

func independentReviewEvidencePresent(review independentReviewData) bool {
	return strings.TrimSpace(review.Evidence) != "" ||
		strings.TrimSpace(review.Reviewer) != "" ||
		strings.TrimSpace(review.ThreadID) != "" ||
		strings.TrimSpace(review.Reference) != ""
}

func independentReviewEvidence(review independentReviewData) string {
	parts := []string{}
	if review.Status != "" {
		parts = append(parts, "status="+review.Status)
	}
	if review.Reviewer != "" {
		parts = append(parts, "reviewer="+review.Reviewer)
	}
	if review.ThreadID != "" {
		parts = append(parts, "thread="+review.ThreadID)
	}
	if review.Reference != "" {
		parts = append(parts, "ref="+review.Reference)
	}
	if review.Evidence != "" {
		parts = append(parts, "evidence="+review.Evidence)
	}
	if review.Reason != "" {
		parts = append(parts, "reason="+review.Reason)
	}
	return strings.Join(parts, "; ")
}
