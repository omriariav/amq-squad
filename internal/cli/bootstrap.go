package cli

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"text/template"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/role"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// bootstrap.go is a kept-dead extract (lead ruling 2): the rendered launch
// prompt is the ancestor of the 2b context verb (manifest C12, "cut
// pane/wake/goal").

//go:embed bootstrap.md
var defaultBootstrapTemplate string

type bootstrapContext struct {
	Role           string
	Handle         string
	Binary         string
	ToolProfile    string
	ToolConfig     string
	Session        string
	CWD            string
	Root           string
	AgentDir       string
	TeamHome       string
	TeamRulesPath  string
	RolePath       string
	LaunchPath     string
	BriefPath      string
	Operator       team.OperatorView
	OperatorGates  bool
	Execution      *executionModeData
	ActorExecution *actorExecutionData
	PlannerLead    bool
	// MutationCapable (#497) gates the worktree-ownership block.
	MutationCapable bool
	// Orchestrated/IsLead/LeadHandle drive a worker's READY handshake to its lead.
	Orchestrated bool
	IsLead       bool
	LeadHandle   string
	CurrentTeam  []bootstrapTeamMember
	Warnings     []string
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

// sanitizeBootstrapContext passes every exported string in the context,
// through nested structs, pointers and slices, through promptText.
func sanitizeBootstrapContext(ctx bootstrapContext) bootstrapContext {
	sanitizePromptValue(reflect.ValueOf(&ctx).Elem())
	return ctx
}

func sanitizePromptValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(promptText(v.String()))
		}
	case reflect.Pointer:
		if !v.IsNil() {
			sanitizePromptValue(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				sanitizePromptValue(v.Field(i))
			}
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			sanitizePromptValue(v.Index(i))
		}
	}
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

func bootstrapContextFor(rec launch.Record, agentDir, teamHome string) bootstrapContext {
	// One physical project spelling for every coordinate; the record bytes stay.
	teamHome = canonicalFilesystemPath(teamHome)
	rec.TeamHome = canonicalFilesystemPath(rec.TeamHome)
	rec.CWD = canonicalFilesystemPath(rec.CWD)
	rec.Root = canonicalFilesystemPath(rec.Root)
	rec.BaseRoot = canonicalFilesystemPath(rec.BaseRoot)
	agentDir = canonicalFilesystemPath(agentDir)
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
	// The profile is read once; each section degrades on its own when it fails.
	home := teamHome
	if home == "" {
		home = rec.CWD
	}
	t, terr := team.ReadProfile(home, rec.TeamProfile)
	var (
		operator      team.OperatorView
		operatorGates bool
		execution     *executionModeData
	)
	if terr == nil {
		operator, operatorGates = team.EffectiveOperator(t), team.SupportsOperatorGates(t)
		contract := executionContractForTeam(t, rec.TeamProfile, rec.Session, "amq_task_brief", "")
		execution = &contract
	}
	orchestrated, isLead, leadHandle := bootstrapOrchestration(rec, t, terr)
	currentTeam, warnings := bootstrapCurrentTeamWithRoster(rec, t, terr, strings.TrimSpace(rec.Session) != "")
	actorExecution, actorWarning := bootstrapActorExecution(rec, t, terr, execution)
	if actorWarning != "" {
		warnings = append(warnings, actorWarning)
	}
	// MutationCapable (#497) follows the resolved ImplementationAllowedForYou.
	mutationCapable := actorExecution != nil && actorExecution.ImplementationAllowedForYou
	return bootstrapContext{
		Role:            rec.Role,
		Handle:          rec.Handle,
		Binary:          rec.Binary,
		ToolProfile:     toolProfile,
		ToolConfig:      rec.ToolConfig,
		Session:         rec.Session,
		CWD:             rec.CWD,
		Root:            rec.Root,
		AgentDir:        agentDir,
		TeamHome:        teamHome,
		TeamRulesPath:   teamRulesPath,
		RolePath:        role.ExistingPath(agentDir),
		LaunchPath:      launch.ExistingPath(agentDir),
		BriefPath:       briefPathForProfile(resolveBriefHome(teamHome, rec.CWD), rec.TeamProfile, rec.Session),
		Operator:        operator,
		OperatorGates:   operatorGates,
		Execution:       execution,
		ActorExecution:  actorExecution,
		PlannerLead:     actorExecution != nil && actorExecution.IsLead && actorExecution.TeamLeadMode == team.LeadModePlanner && !actorExecution.ImplementationAllowedForYou,
		MutationCapable: mutationCapable,
		Orchestrated:    orchestrated,
		IsLead:          isLead,
		LeadHandle:      leadHandle,
		CurrentTeam:     currentTeam,
		Warnings:        warnings,
	}
}

func bootstrapActorExecution(rec launch.Record, t team.Team, terr error, execution *executionModeData) (*actorExecutionData, string) {
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
	actor := actorExecutionContractForTeam(t, rec.Role, rec.Handle, *execution)
	if _, ok := actorRosterMemberForTeam(t, rec.Role, rec.Handle); !ok {
		return &actor, fmt.Sprintf("actor identity %s/%s does not match the exact profile roster; implementation and delegation are denied. Repair the launch record or profile and relaunch.", rec.Role, rec.Handle)
	}
	return &actor, ""
}

// bootstrapOrchestration reports whether the team is lead-orchestrated, whether
// this agent is the lead, and the lead's handle (zero values: no handshake).
func bootstrapOrchestration(rec launch.Record, t team.Team, terr error) (orchestrated, isLead bool, leadHandle string) {
	if terr != nil || !t.Orchestrated || strings.TrimSpace(t.Lead) == "" {
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

func bootstrapCurrentTeamWithRoster(rec launch.Record, t team.Team, terr error, exactSessionRoster bool) ([]bootstrapTeamMember, []string) {
	if terr != nil {
		if os.IsNotExist(terr) {
			return nil, nil
		}
		return nil, []string{fmt.Sprintf("current team routing unavailable: %v", terr)}
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

// sameLaunchTarget decides which roster row is marked "(you)" (canonical cwd, #618).
func sameLaunchTarget(rec launch.Record, cwd, handle string, m team.Member) bool {
	return rec.Role == m.Role &&
		rec.Handle == handle &&
		rec.Session == routingSessionForMember(rec, m) &&
		samePath(rec.CWD, cwd)
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
	args := []string{"amq", "send"}
	if sourceRoot != "" {
		args = append(args, "--root", sourceRoot)
		if fromHandle != "" {
			args = append(args, "--me", fromHandle)
		}
	}
	args = append(args, "--to", handle)
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
	argv = pinBootstrapAMQSendArgv(argv, sourceRoot, fromHandle)
	if fromHandle != "" && handle != "" && fromHandle != handle {
		argv = append(argv, "--thread", canonicalP2PThread(fromHandle, handle))
	}
	return shellCommand(argv[0], argv[1:]...), "", true
}

func pinBootstrapAMQSendArgv(argv []string, root, handle string) []string {
	if len(argv) < 2 || argv[0] != "amq" || argv[1] != "send" {
		return argv
	}
	if strings.TrimSpace(root) == "" {
		return argv
	}
	hasRoot, hasMe := false, false
	for i := 2; i < len(argv); i++ {
		switch argv[i] {
		case "--root":
			hasRoot = true
		case "--me":
			hasMe = true
		}
	}
	pinned := append([]string(nil), argv[:2]...)
	if strings.TrimSpace(root) != "" && !hasRoot {
		pinned = append(pinned, "--root", root)
	}
	if strings.TrimSpace(handle) != "" && !hasMe {
		pinned = append(pinned, "--me", handle)
	}
	return append(pinned, argv[2:]...)
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

// samePath compares canonical forms, not recorded spellings (#618): a symlinked
// or differently cased spelling of one directory is the same directory.
func samePath(a, b string) bool {
	return canonicalFilesystemPath(a) == canonicalFilesystemPath(b)
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
