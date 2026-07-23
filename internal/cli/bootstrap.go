package cli

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/role"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

//go:embed bootstrap.md
var defaultBootstrapTemplate string

type bootstrapContext struct {
	Role             string
	Handle           string
	Binary           string
	ToolProfile      string
	ToolConfig       string
	Session          string
	CWD              string
	Root             string
	AgentDir         string
	TeamHome         string
	TeamRulesPath    string
	RolePath         string
	LaunchPath       string
	BriefPath        string
	Operator         team.OperatorView
	OperatorDelivery operatorDeliveryData
	SelfOperator     *team.EffectiveSelfOperatorView
	OperatorGates    bool
	Execution        *executionModeData
	ActorExecution   *actorExecutionData
	PlannerLead      bool
	// MutationCapable (#497) gates the source-worktree-identity/ownership
	// block: derived from ActorExecution.ImplementationAllowedForYou, so a
	// read-only reviewer or a planner lead (who never implements) never
	// receives worktree-ownership instructions.
	MutationCapable bool
	// Orchestrated/IsLead/LeadHandle drive the spawned-worker READY handshake:
	// a non-lead member of an orchestrated team announces readiness to its lead
	// on startup so the lead can dispatch without guessing the worker's load
	// state (and without eating busy-guard rejections on a still-loading pane).
	Orchestrated         bool
	IsLead               bool
	LeadHandle           string
	CurrentTeam          []bootstrapTeamMember
	Workstreams          []bootstrapWorkstream
	Warnings             []string
	BootstrapExpectation *bootstrapack.Expectation
}

type bootstrapTeamMember struct {
	Role       string
	Handle     string
	Binary     string
	Session    string
	Project    string
	CWD        string
	Route      string
	RouteError string
	You        bool
}

func buildBootstrapPrompt(ctx bootstrapContext) (string, error) {
	ctx = sanitizeBootstrapContext(ctx)
	tpl, err := template.New("bootstrap").Funcs(template.FuncMap{
		"orDefault": func(s, fallback string) string {
			if s == "" {
				return fallback
			}
			return s
		},
		"shellQuote": shellQuote,
	}).Parse(defaultBootstrapTemplate)
	if err != nil {
		return "", fmt.Errorf("parse bootstrap template: %w", err)
	}
	var b bytes.Buffer
	if err := tpl.Execute(&b, ctx); err != nil {
		return "", fmt.Errorf("render bootstrap template: %w", err)
	}
	return b.String(), nil
}

func sanitizeBootstrapContext(ctx bootstrapContext) bootstrapContext {
	ctx.Role = promptText(ctx.Role)
	ctx.Handle = promptText(ctx.Handle)
	ctx.Binary = promptText(ctx.Binary)
	ctx.ToolProfile = promptText(ctx.ToolProfile)
	ctx.ToolConfig = promptText(ctx.ToolConfig)
	ctx.Session = promptText(ctx.Session)
	ctx.CWD = promptText(ctx.CWD)
	ctx.Root = promptText(ctx.Root)
	ctx.AgentDir = promptText(ctx.AgentDir)
	ctx.TeamHome = promptText(ctx.TeamHome)
	ctx.TeamRulesPath = promptText(ctx.TeamRulesPath)
	ctx.LeadHandle = promptText(ctx.LeadHandle)
	ctx.RolePath = promptText(ctx.RolePath)
	ctx.LaunchPath = promptText(ctx.LaunchPath)
	ctx.BriefPath = promptText(ctx.BriefPath)
	ctx.Operator.Handle = promptText(ctx.Operator.Handle)
	ctx.Operator.InteractionMode = promptText(ctx.Operator.InteractionMode)
	ctx.OperatorDelivery.Handle = promptText(ctx.OperatorDelivery.Handle)
	ctx.OperatorDelivery.InteractionMode = promptText(ctx.OperatorDelivery.InteractionMode)
	ctx.OperatorDelivery.ApprovalSurface = promptText(ctx.OperatorDelivery.ApprovalSurface)
	ctx.OperatorDelivery.Contract = promptText(ctx.OperatorDelivery.Contract)
	ctx.OperatorDelivery.PollOwner = promptText(ctx.OperatorDelivery.PollOwner)
	ctx.OperatorDelivery.Reason = promptText(ctx.OperatorDelivery.Reason)
	ctx.OperatorDelivery.Guidance = promptText(ctx.OperatorDelivery.Guidance)
	ctx.OperatorDelivery.NotificationSemantics = promptText(ctx.OperatorDelivery.NotificationSemantics)
	ctx.OperatorDelivery.NotificationGuidance = promptText(ctx.OperatorDelivery.NotificationGuidance)
	for i := range ctx.OperatorDelivery.NotificationSinkTypes {
		ctx.OperatorDelivery.NotificationSinkTypes[i] = promptText(ctx.OperatorDelivery.NotificationSinkTypes[i])
	}
	if ctx.Execution != nil {
		ctx.Execution.Mode = promptText(ctx.Execution.Mode)
		ctx.Execution.ControlRoot = promptText(ctx.Execution.ControlRoot)
		ctx.Execution.TargetProjectRoot = promptText(ctx.Execution.TargetProjectRoot)
		ctx.Execution.Profile = promptText(ctx.Execution.Profile)
		ctx.Execution.Session = promptText(ctx.Execution.Session)
		ctx.Execution.NamespaceID = promptText(ctx.Execution.NamespaceID)
		ctx.Execution.VisibleLead = promptText(ctx.Execution.VisibleLead)
		ctx.Execution.LeadMode = promptText(ctx.Execution.LeadMode)
		ctx.Execution.MutableActor = promptText(ctx.Execution.MutableActor)
		ctx.Execution.GoalBinding = promptText(ctx.Execution.GoalBinding)
		ctx.Execution.VisibilityTopology = promptText(ctx.Execution.VisibilityTopology)
		ctx.Execution.ModeError = promptText(ctx.Execution.ModeError)
		ctx.Execution.Boundary = promptText(ctx.Execution.Boundary)
	}
	if ctx.ActorExecution != nil {
		ctx.ActorExecution.ActorRole = promptText(ctx.ActorExecution.ActorRole)
		ctx.ActorExecution.ActorHandle = promptText(ctx.ActorExecution.ActorHandle)
		ctx.ActorExecution.TeamLeadMode = promptText(ctx.ActorExecution.TeamLeadMode)
	}
	for i := range ctx.CurrentTeam {
		m := &ctx.CurrentTeam[i]
		m.Role = promptText(m.Role)
		m.Handle = promptText(m.Handle)
		m.Binary = promptText(m.Binary)
		m.Session = promptText(m.Session)
		m.Project = promptText(m.Project)
		m.CWD = promptText(m.CWD)
		m.Route = promptText(m.Route)
		m.RouteError = promptText(m.RouteError)
	}
	for i := range ctx.Workstreams {
		w := &ctx.Workstreams[i]
		w.Name = promptText(w.Name)
		w.Handles = promptText(w.Handles)
		w.LastTouched = promptText(w.LastTouched)
	}
	for i := range ctx.Warnings {
		ctx.Warnings[i] = promptText(ctx.Warnings[i])
	}
	return ctx
}

func promptText(s string) string {
	// Keep prompt data single-line and out of inline-code delimiters; team.Read
	// already rejects control characters in persisted team fields.
	var b strings.Builder
	lastSpace := false
	for _, r := range s {
		if r < 0x20 || r == 0x7f || r == '`' {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}

func bootstrapExpectationForLaunch(rec launch.Record, promptAppended, noBootstrap bool, suppressedReason ...string) (bootstrapack.Expectation, error) {
	required := promptAppended && bootstrapActorCanAttest(rec)
	expect, err := bootstrapack.NewExpectation(required, rec.StartedAt)
	if err != nil {
		return bootstrapack.Expectation{}, err
	}
	if required {
		return expect, nil
	}
	switch {
	case len(suppressedReason) > 0 && strings.TrimSpace(suppressedReason[0]) != "":
		expect.NotRequiredReason = strings.TrimSpace(suppressedReason[0])
	case rec.Conversation != "":
		expect.NotRequiredReason = "true conversation reattach does not require bootstrap acknowledgement"
	case noBootstrap:
		expect.NotRequiredReason = "launch explicitly disabled the bootstrap prompt"
	case !promptAppended:
		expect.NotRequiredReason = "bootstrap prompt was not appended"
	default:
		expect.NotRequiredReason = "bootstrap actor is not a verified configured roster role"
	}
	return expect, nil
}

func bootstrapActorCanAttest(rec launch.Record) bool {
	if rec.Tmux == nil || strings.TrimSpace(rec.Tmux.PaneID) == "" || strings.TrimSpace(rec.Role) == "" || strings.TrimSpace(rec.Handle) == "" {
		return false
	}
	home := strings.TrimSpace(rec.TeamHome)
	if home == "" {
		return false
	}
	profile := rec.TeamProfile
	t, err := team.ReadProfile(home, profile)
	if err != nil {
		return false
	}
	m, ok := operatorRosterMember(t, rec.Role, rec.Handle)
	if !ok || !sameFilesystemPath(m.EffectiveCWD(t.Project), rec.CWD) {
		return false
	}
	if session := strings.TrimSpace(m.Session); session != "" && session != strings.TrimSpace(rec.Session) {
		return false
	}
	return true
}

func appendGeneratedBootstrapPrompt(args []string, prompt string) []string {
	out := append([]string(nil), args...)
	for _, arg := range out {
		if arg == "--" {
			return append(out, prompt)
		}
	}
	return append(out, "--", prompt)
}

func bootstrapContextFor(rec launch.Record, agentDir, teamHome string) bootstrapContext {
	toolProfile := strings.TrimSpace(rec.ToolProfile)
	if toolProfile == "" {
		toolProfile = team.ToolProfileFull
	}
	teamRulesPath := ""
	if teamHome != "" {
		teamRulesPath = rules.Path(teamHome)
	} else if _, err := os.Stat(rules.Path(rec.CWD)); err == nil {
		teamRulesPath = rules.Path(rec.CWD)
	}
	operator, operatorGates := bootstrapOperator(rec, teamHome)
	var selfOperator *team.EffectiveSelfOperatorView
	if t, err := team.ReadProfile(teamHome, rec.TeamProfile); err == nil && t.Operator != nil && t.Operator.InteractionMode == team.OperatorInteractionSelfOperator {
		view := team.EffectiveSelfOperator(t, rec.Session)
		selfOperator = &view
	}
	orchestrated, isLead, leadHandle := bootstrapOrchestration(rec, teamHome)
	exactSessionRoster := false
	if home := strings.TrimSpace(teamHome); home != "" && strings.TrimSpace(rec.Session) != "" {
		_, err := os.Stat(preparedRunPath(home, rec.TeamProfile, rec.Session))
		exactSessionRoster = err == nil || !os.IsNotExist(err)
	}
	currentTeam, warnings := bootstrapCurrentTeamWithRoster(rec, teamHome, exactSessionRoster)
	execution := bootstrapExecution(rec, teamHome)
	actorExecution, actorWarning := bootstrapActorExecution(rec, teamHome, execution)
	if actorWarning != "" {
		warnings = append(warnings, actorWarning)
	}
	// MutationCapable (#497) derives from the already-resolved
	// ImplementationAllowedForYou rather than a second raw team.ActorMode
	// check: that field already accounts for execution-mode narrowing (e.g.
	// a planner lead is never mutation-capable even though its role default
	// is ActorModeImplementation), so this can never disagree with the
	// worker-implementation-posture text elsewhere in the same bootstrap.
	mutationCapable := actorExecution != nil && actorExecution.ImplementationAllowedForYou
	return bootstrapContext{
		Role:          rec.Role,
		Handle:        rec.Handle,
		Binary:        rec.Binary,
		ToolProfile:   toolProfile,
		ToolConfig:    rec.ToolConfig,
		Session:       rec.Session,
		CWD:           rec.CWD,
		Root:          rec.Root,
		AgentDir:      agentDir,
		TeamHome:      teamHome,
		TeamRulesPath: teamRulesPath,
		RolePath:      role.ExistingPath(agentDir),
		LaunchPath:    launch.ExistingPath(agentDir),
		// Brief resolution uses the same rule as the live-launch ensure
		// step so bootstrap can never name a path that ensure skipped (or
		// vice versa).
		BriefPath:            briefPathForProfile(resolveBriefHome(teamHome, rec.CWD), rec.TeamProfile, rec.Session),
		Operator:             operator,
		OperatorDelivery:     operatorDeliveryForRecord(rec, teamHome),
		SelfOperator:         selfOperator,
		OperatorGates:        operatorGates,
		Execution:            execution,
		ActorExecution:       actorExecution,
		PlannerLead:          actorExecution != nil && actorExecution.IsLead && actorExecution.TeamLeadMode == team.LeadModePlanner && !actorExecution.ImplementationAllowedForYou,
		MutationCapable:      mutationCapable,
		Orchestrated:         orchestrated,
		IsLead:               isLead,
		LeadHandle:           leadHandle,
		CurrentTeam:          currentTeam,
		Workstreams:          siblingWorkstreamSummaries(rec.Root, rec.Session),
		Warnings:             warnings,
		BootstrapExpectation: rec.BootstrapExpectation,
	}
}

func bootstrapActorExecution(rec launch.Record, teamHome string, execution *executionModeData) (*actorExecutionData, string) {
	fallback := &actorExecutionData{
		ActorRole:   strings.TrimSpace(rec.Role),
		ActorHandle: strings.TrimSpace(rec.Handle),
	}
	if fallback.ActorHandle == "" {
		fallback.ActorHandle = fallback.ActorRole
	}
	if execution == nil {
		return fallback, "actor execution contract could not load the team execution profile; implementation and delegation are denied. Repair the named profile and relaunch before accepting mutation work."
	}
	fallback.TeamLeadMode = execution.LeadMode
	home := teamHome
	if home == "" {
		home = rec.CWD
	}
	t, err := team.ReadProfile(home, rec.TeamProfile)
	if err != nil {
		return fallback, fmt.Sprintf("actor execution contract could not read profile %q: %v; implementation and delegation are denied. Repair the profile and relaunch before accepting mutation work.", rec.TeamProfile, err)
	}
	t, err = projectPreparedRunStagedTeamForRecord(t, rec)
	if err != nil {
		return fallback, fmt.Sprintf("actor execution contract could not validate the authoritative staged claim: %v; implementation and delegation are denied. Repair the staged claim and relaunch.", err)
	}
	actor := actorExecutionContractForTeam(t, rec.Role, rec.Handle, *execution)
	if _, ok := actorRosterMemberForTeam(t, rec.Role, rec.Handle); !ok {
		return &actor, fmt.Sprintf("actor identity %s/%s does not match the exact profile roster; implementation and delegation are denied. Repair the launch record or profile and relaunch.", rec.Role, rec.Handle)
	}
	return &actor, ""
}

func operatorDeliveryForRecord(rec launch.Record, teamHome string) operatorDeliveryData {
	home := teamHome
	if home == "" {
		home = rec.CWD
	}
	t, err := team.ReadProfile(home, rec.TeamProfile)
	if err != nil {
		return operatorDeliveryData{}
	}
	return operatorDeliveryForTeam(t)
}

func bootstrapExecution(rec launch.Record, teamHome string) *executionModeData {
	home := teamHome
	if home == "" {
		home = rec.CWD
	}
	t, err := team.ReadProfile(home, rec.TeamProfile)
	if err != nil {
		return nil
	}
	t, err = projectPreparedRunStagedTeamForRecord(t, rec)
	if err != nil {
		return nil
	}
	goalBinding := bootstrapGoalBindingMode(rec, t)
	execution := executionContractForTeam(t, rec.TeamProfile, rec.Session, goalBinding, "", "dev")
	return &execution
}

func bootstrapGoalBindingMode(rec launch.Record, t team.Team) string {
	if bootstrapRecordIsVisibleLead(rec, t) {
		if launchRecordHasGoalBinding(rec) {
			return rec.GoalBinding.Mode
		}
		// Preparation records accepted goal intent before any pane exists. That
		// state is deliberately not delivered launch evidence, but its exact,
		// binary-specific mode still belongs in the generated bootstrap preview.
		// Keep this path narrower than launchRecordHasGoalBinding so preparation
		// can never promote itself to verified live-goal delivery.
		if mode, ok := preparedBootstrapGoalBindingMode(rec); ok {
			return mode
		}
		if projectExecutionModeRequiresGoalBinding(t) {
			if contract, err := goalDeliveryContractForBinary(rec.Binary); err == nil {
				return contract.Mode + "_missing"
			}
			return "goal_delivery_unsupported"
		}
	}
	return "amq_task_brief"
}

func preparedBootstrapGoalBindingMode(rec launch.Record) (string, bool) {
	if rec.GoalBinding == nil || rec.GoalBinding.Source != "prepared-run" || rec.GoalBinding.DeliveryState != goalBindingDeliveryPrepared {
		return "", false
	}
	contract, err := goalDeliveryContractForBinary(rec.Binary)
	if err != nil {
		return "", false
	}
	if _, _, err := goalBindingPayload(rec.GoalBinding, contract); err != nil {
		return "", false
	}
	return contract.Mode, true
}

func bootstrapRecordIsVisibleLead(rec launch.Record, t team.Team) bool {
	lead := strings.TrimSpace(t.Lead)
	if lead == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(rec.Role), lead)
}

// bootstrapOrchestration reports whether this launch belongs to a lead-
// orchestrated team, whether THIS agent is the lead, and the lead's handle. A
// non-lead member uses it to announce READY to the lead on startup. Returns
// zero values (no handshake) when the profile is unreadable or not orchestrated.
func bootstrapOrchestration(rec launch.Record, teamHome string) (orchestrated, isLead bool, leadHandle string) {
	home := teamHome
	if home == "" {
		home = rec.CWD
	}
	t, err := team.ReadProfile(home, rec.TeamProfile)
	if err != nil || !t.Orchestrated || strings.TrimSpace(t.Lead) == "" {
		return false, false, ""
	}
	isLead = strings.EqualFold(strings.TrimSpace(rec.Role), strings.TrimSpace(t.Lead))
	for _, m := range t.Members {
		if strings.EqualFold(m.Role, t.Lead) {
			leadHandle = memberHandle(m)
			break
		}
	}
	return true, isLead, leadHandle
}

func bootstrapOperator(rec launch.Record, teamHome string) (team.OperatorView, bool) {
	home := teamHome
	if home == "" {
		home = rec.CWD
	}
	t, err := team.ReadProfile(home, rec.TeamProfile)
	if err != nil {
		return team.OperatorView{}, false
	}
	return team.EffectiveOperator(t), team.SupportsOperatorGates(t)
}

func bootstrapCurrentTeam(rec launch.Record, teamHome string) ([]bootstrapTeamMember, []string) {
	return bootstrapCurrentTeamWithRoster(rec, teamHome, false)
}

func bootstrapCurrentTeamWithRoster(rec launch.Record, teamHome string, exactSessionRoster bool) ([]bootstrapTeamMember, []string) {
	home := teamHome
	if home == "" {
		home = rec.CWD
	}
	t, err := team.ReadProfile(home, rec.TeamProfile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("current team routing unavailable: %v", err)}
	}
	if len(t.Members) == 0 {
		return nil, nil
	}
	if exactSessionRoster {
		active, _ := filterMembersBySession(t.Members, rec.Session)
		t.Members = active
		if len(t.Members) == 0 {
			return nil, nil
		}
	}
	return bootstrapCurrentTeamForTeam(rec, t)
}

func bootstrapCurrentTeamForTeam(rec launch.Record, t team.Team) ([]bootstrapTeamMember, []string) {
	currentProject := projectIdentityForCWD(rec.CWD)
	out := make([]bootstrapTeamMember, 0, len(t.Members))
	for _, m := range t.Members {
		cwd := m.EffectiveCWD(t.Project)
		project := projectIdentityForCWD(cwd)
		handle := memberHandle(m)
		session := routingSessionForMember(rec, m)
		route, routeError := routeCommandFor(rec.Root, rec.Session, currentProject, project, samePath(rec.CWD, cwd), rec.Handle, handle, session)
		out = append(out, bootstrapTeamMember{
			Role:       m.Role,
			Handle:     handle,
			Binary:     m.Binary,
			Session:    session,
			Project:    project.DisplayName(),
			CWD:        cwd,
			Route:      route,
			RouteError: routeError,
			You:        sameLaunchTarget(rec, cwd, handle, m),
		})
	}
	return out, nil
}

func routingSessionForMember(rec launch.Record, m team.Member) string {
	if rec.SharedWorkstream || (rec.Session != "" && rec.Session != rec.Role && rec.Session != rec.Handle) {
		return rec.Session
	}
	return m.Session
}

func memberHandle(m team.Member) string {
	if m.Handle != "" {
		return m.Handle
	}
	if m.Role != "" {
		return m.Role
	}
	return m.Binary
}

func sameLaunchTarget(rec launch.Record, cwd, handle string, m team.Member) bool {
	return rec.Role == m.Role &&
		rec.Handle == handle &&
		rec.Session == routingSessionForMember(rec, m) &&
		rec.CWD == cwd
}

type projectIdentity struct {
	Name  string
	Dir   string
	Known bool
}

func (p projectIdentity) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return "(unknown)"
}

func routeCommandFor(sourceRoot, sourceSession string, currentProject, targetProject projectIdentity, sameCWD bool, fromHandle, handle, session string) (string, string) {
	if !sameCWD && (!currentProject.Known || !targetProject.Known) {
		return "", "AMQ project identity is missing; add .amqrc project names or use amq route manually"
	}
	if !sameCWD && currentProject.Name == targetProject.Name && !samePath(currentProject.Dir, targetProject.Dir) {
		return "", "AMQ project identity is ambiguous; matching project names come from different .amqrc roots"
	}
	if sourceRoot != "" && fromHandle != "" && handle != "" {
		if route, routeError, ok := routeExplainCommand(sourceRoot, sourceSession, currentProject, targetProject, sameCWD, fromHandle, handle, session); ok {
			return route, routeError
		}
	}
	args := []string{"amq", "send", "--to", handle}
	if !sameCWD && currentProject.Name != targetProject.Name {
		args = append(args, "--project", targetProject.Name)
	}
	if sourceSession != "" && session != "" && session != sourceSession {
		args = append(args, "--from-session", sourceSession)
	}
	if session != "" {
		args = append(args, "--session", session)
	}
	if fromHandle != "" && handle != "" && fromHandle != handle {
		args = append(args, "--thread", canonicalP2PThread(fromHandle, handle))
	}
	for i, arg := range args {
		args[i] = shellQuote(arg)
	}
	return strings.Join(args, " "), ""
}

type routeExplainResult struct {
	Routable       bool     `json:"routable"`
	Argv           []string `json:"argv"`
	DisplayCommand string   `json:"display_command"`
	Error          string   `json:"error"`
}

func routeExplainCommand(sourceRoot, sourceSession string, currentProject, targetProject projectIdentity, sameCWD bool, fromHandle, handle, session string) (string, string, bool) {
	args := []string{"route", "explain", "--from-root", sourceRoot, "--me", fromHandle, "--to", handle, "--json"}
	if !sameCWD && currentProject.Name != targetProject.Name && targetProject.Name != "" {
		args = append(args, "--project", targetProject.Name)
	}
	if session != "" && session != sourceSession {
		args = append(args, "--session", session)
	}
	cmd := exec.Command("amq", args...)
	cmd.Env = amqexec.NoUpdateCheckEnv(nil)
	out, err := cmd.Output()
	if err != nil {
		return "", "", false
	}
	var parsed routeExplainResult
	if err := json.Unmarshal(out, &parsed); err != nil {
		return "", "", false
	}
	if !parsed.Routable {
		if parsed.Error == "" {
			parsed.Error = "AMQ route explain reported route is not routable"
		}
		return "", parsed.Error, true
	}
	if len(parsed.Argv) == 0 {
		return "", "AMQ route explain returned empty argv", true
	}
	argv := append([]string(nil), parsed.Argv...)
	if fromHandle != "" && handle != "" && fromHandle != handle {
		argv = append(argv, "--thread", canonicalP2PThread(fromHandle, handle))
	}
	return shellCommand(argv[0], argv[1:]...), "", true
}

func projectIdentityForCWD(cwd string) projectIdentity {
	if cwd == "" {
		return projectIdentity{}
	}
	if env, err := resolveAMQEnvInDir(cwd, "", "", "amq-squad"); err == nil && env.Project != "" {
		dir := env.BaseRoot
		if dir == "" {
			dir = env.Root
		}
		return projectIdentity{Name: env.Project, Dir: dir, Known: true}
	}
	if dir, name := findProjectName(cwd); name != "" {
		return projectIdentity{Name: name, Dir: dir, Known: true}
	}
	return projectIdentity{}
}

func samePath(a, b string) bool {
	aa, err := filepath.Abs(a)
	if err != nil {
		aa = filepath.Clean(a)
	}
	bb, err := filepath.Abs(b)
	if err != nil {
		bb = filepath.Clean(b)
	}
	return aa == bb
}

func findProjectName(start string) (string, string) {
	dir, err := filepath.Abs(start)
	if err != nil {
		dir = start
	}
	for {
		path := filepath.Join(dir, ".amqrc")
		if b, err := os.ReadFile(path); err == nil {
			var cfg struct {
				Project string `json:"project"`
			}
			if json.Unmarshal(b, &cfg) == nil && cfg.Project != "" {
				return dir, cfg.Project
			}
			return dir, ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", ""
		}
		dir = parent
	}
}
