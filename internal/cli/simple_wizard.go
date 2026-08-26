package cli

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/catalog"
	"github.com/omriariav/amq-squad/v2/internal/drafter"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/userconfig"
)

const wizardPlanSchemaVersion = 1

type simpleWizardDependencies struct {
	Now          func() time.Time
	LookPath     func(string) (string, error)
	ReadConfig   func() (userconfig.Config, error)
	ConfigPath   func() (string, error)
	Start        func([]string, simpleStartDependencies, io.Reader, io.Writer) error
	StartDeps    simpleStartDependencies
	RunGoalDraft cliDrafterRunner
	RunRules     cliDrafterRunner
	RunRole      cliDrafterRunner
}

func defaultSimpleWizardDependencies() simpleWizardDependencies {
	return simpleWizardDependencies{
		Now:          time.Now,
		LookPath:     exec.LookPath,
		ReadConfig:   userconfig.Read,
		ConfigPath:   userconfig.Path,
		Start:        runStartWithDependencies,
		StartDeps:    defaultSimpleStartDependencies(),
		RunGoalDraft: runGoalDrafter,
		RunRules:     runTeamRulesDrafter,
		RunRole:      runRoleDrafter,
	}
}

type simpleWizardRequest struct {
	Goal               string
	Project            string
	Profile            string
	ProfileExplicit    bool
	Session            string
	SessionExplicit    bool
	Repo               string
	Milestone          string
	TargetContract     string
	Lead               string
	LeadMode           string
	Roles              string
	Binary             string
	ActorMode          string
	CodexOnly          bool
	NoSessionPin       bool
	SharedCWDException string
	NewProfileExplicit bool
	Yes                bool
	JSON               bool
}

type simpleWizardReadiness struct {
	GlobalConfigPath string   `json:"global_config_path,omitempty"`
	DrafterSource    string   `json:"drafter_source"`
	DrafterSelection string   `json:"drafter_selection"`
	Executables      []string `json:"executables"`
}

type simpleWizardArtifact struct {
	Path     string                     `json:"path"`
	Action   string                     `json:"action"`
	Content  string                     `json:"content,omitempty"`
	Evidence *simpleWizardDraftEvidence `json:"drafter,omitempty"`
}

type simpleWizardDraftEvidence struct {
	Source   string             `json:"source"`
	Manual   bool               `json:"manual,omitempty"`
	Prompt   string             `json:"prompt,omitempty"`
	Evidence drafter.Evidence   `json:"evidence"`
	Attempts []drafter.Evidence `json:"attempts,omitempty"`
}

type simpleWizardLaunchPlan struct {
	Command string   `json:"command"`
	Root    string   `json:"root"`
	Roles   []string `json:"roles"`
}

type simpleWizardPlan struct {
	SchemaVersion int                    `json:"schema_version"`
	Flow          string                 `json:"flow"`
	Goal          string                 `json:"goal"`
	Project       string                 `json:"project"`
	Profile       string                 `json:"profile"`
	Session       string                 `json:"session"`
	Readiness     simpleWizardReadiness  `json:"readiness"`
	ProfilePlan   simpleWizardArtifact   `json:"profile_artifact"`
	RolePlans     []simpleWizardArtifact `json:"custom_roles,omitempty"`
	RulesPlan     simpleWizardArtifact   `json:"rules"`
	SyncPlans     []rules.SyncPlan       `json:"-"`
	BriefPlan     simpleWizardArtifact   `json:"brief"`
	LaunchPlan    simpleWizardLaunchPlan `json:"launch"`
	Team          team.Team              `json:"-"`
	Snapshots     []simpleWizardSnapshot `json:"-"`
	Existing      bool                   `json:"-"`
	Approved      bool                   `json:"approved"`
}

type simpleWizardSnapshot struct {
	Path   string
	Exists bool
	Digest [sha256.Size]byte
}

func runWizard(args []string) error {
	return runWizardWithVersion(args, "dev")
}

func runWizardWithVersion(args []string, version string) error {
	return runWizardWithDependencies(args, version, defaultSimpleWizardDependencies(), os.Stdin, os.Stdout, os.Stderr)
}

func runWizardWithDependencies(args []string, version string, deps simpleWizardDependencies, in io.Reader, out, errOut io.Writer) error {
	req, err := parseSimpleWizardRequest(args, errOut)
	if err != nil {
		return err
	}
	if req.JSON && req.Yes {
		return usageErrorf("wizard --json is preview-only and cannot be combined with --yes")
	}
	plan, err := buildSimpleWizardPlan(req, version, deps)
	if err != nil {
		return err
	}
	if req.JSON {
		return writeJSONEnvelope(out, "wizard_plan", plan)
	}
	renderSimpleWizardPlan(out, plan)
	if !req.Yes && !confirmSimpleWizard(out, in) {
		fmt.Fprintln(out, "wizard cancelled; nothing changed")
		return nil
	}
	if err := applySimpleWizardPlan(&plan); err != nil {
		return err
	}
	plan.Approved = true
	startArgs := []string{"--project", plan.Project, "--profile", plan.Profile, "--session", plan.Session, "--goal", plan.Goal, "--yes"}
	if deps.Start == nil {
		return fmt.Errorf("wizard start dependency is not configured")
	}
	if err := deps.Start(startArgs, deps.StartDeps, in, out); err != nil {
		return fmt.Errorf("wizard staged the reviewed artifacts, but start did not complete: %w; rerun the same wizard command to recover", err)
	}
	return nil
}

func parseSimpleWizardRequest(args []string, errOut io.Writer) (simpleWizardRequest, error) {
	leadingGoal := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		leadingGoal = strings.TrimSpace(args[0])
		args = args[1:]
	}
	fs := flag.NewFlagSet("wizard", flag.ContinueOnError)
	fs.SetOutput(errOut)
	goalFlag := fs.String("goal", "", "goal to turn into a reviewed squad launch")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "existing or proposed team profile")
	sessionFlag := fs.String("session", "", "workstream session (default: derived from the goal)")
	repoFlag := fs.String("repo", "", "GitHub owner/repo used as brief source context")
	milestoneFlag := fs.String("milestone", "", "GitHub milestone used as brief source context")
	targetContractFlag := fs.String("target-contract", "", "target amq-squad contract version")
	leadFlag := fs.String("lead", "cto", "lead role for a newly proposed profile")
	leadModeFlag := fs.String("lead-mode", team.LeadModePlanner, "new-profile lead posture: planner or builder")
	rolesFlag := fs.String("roles", "", "comma-separated roles overriding the goal-derived roster")
	binaryFlag := fs.String("binary", "", "per-role binary overrides, e.g. lead=codex,dev=claude")
	actorModeFlag := fs.String("actor-mode", "", "per-role execution capability overrides")
	codexOnlyFlag := fs.Bool("codex-only", false, "propose Codex for every new-profile seat")
	noSessionPinFlag := fs.Bool("no-session-pin", false, "create a reusable unpinned profile")
	sharedCWDExceptionFlag := fs.String("shared-cwd-exception", "", "recorded reason for allowing multiple implementation actors in one cwd")
	yesFlag := fs.Bool("yes", false, "apply the freshly rendered plan without prompting")
	fs.BoolVar(yesFlag, "y", false, "alias for --yes")
	jsonFlag := fs.Bool("json", false, "emit a preview-only wizard_plan JSON envelope")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	fs.Usage = func() {
		fmt.Fprint(errOut, `amq-squad wizard - deterministic goal-to-squad setup and start

Usage:
  amq-squad wizard "GOAL" [options]
  amq-squad wizard --goal TEXT [options]

The binary owns the ordered state machine: machine/drafter readiness, profile
preview or existing-profile selection, optional custom seats, rules refresh,
brief drafting, and one combined launch review. The final confirmation defaults
to No and no profile, role, rules, brief, namespace, or pane is changed before
that approval. A confirmed run delegates launch to the current locked `+"`start`"+`
implementation. Rerun the same command after interruption to roll forward.

Options:
`)
		fs.PrintDefaults()
		fmt.Fprint(errOut, `
Examples:
  amq-squad wizard "Ship issue 709"
  amq-squad wizard "Release v2.29.6" --profile release --session v2-29-6
  amq-squad wizard --goal "Build the reviewed change" --roles cto,fullstack --binary cto=codex
  amq-squad wizard "Preview only" --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return simpleWizardRequest{}, err
	}
	positional := fs.Args()
	if leadingGoal != "" {
		positional = append([]string{leadingGoal}, positional...)
	}
	if len(positional) > 1 {
		return simpleWizardRequest{}, usageErrorf("wizard takes one goal argument; got %d", len(positional))
	}
	goal := strings.TrimSpace(*goalFlag)
	if len(positional) == 1 {
		if goal != "" {
			return simpleWizardRequest{}, usageErrorf("wizard goal must be positional or passed with --goal, not both")
		}
		goal = strings.TrimSpace(positional[0])
	}
	if goal == "" {
		return simpleWizardRequest{}, usageErrorf("wizard requires a goal")
	}
	if strings.ContainsAny(goal, "\x00\r\n") || len(goal) > 2000 {
		return simpleWizardRequest{}, usageErrorf("wizard goal must be one line and at most 2000 bytes")
	}
	if strings.TrimSpace(*milestoneFlag) != "" && strings.TrimSpace(*repoFlag) == "" {
		return simpleWizardRequest{}, usageErrorf("wizard --milestone requires --repo owner/repo")
	}
	return simpleWizardRequest{
		Goal: goal, Project: strings.TrimSpace(*projectFlag),
		Profile: strings.TrimSpace(*profileFlag), ProfileExplicit: flagWasSet(fs, "profile"),
		Session: strings.TrimSpace(*sessionFlag), SessionExplicit: flagWasSet(fs, "session"),
		Repo: strings.TrimSpace(*repoFlag), Milestone: strings.TrimSpace(*milestoneFlag),
		TargetContract: strings.TrimSpace(*targetContractFlag), Lead: strings.TrimSpace(*leadFlag),
		LeadMode: strings.TrimSpace(*leadModeFlag), Roles: strings.TrimSpace(*rolesFlag),
		Binary: strings.TrimSpace(*binaryFlag), ActorMode: strings.TrimSpace(*actorModeFlag),
		CodexOnly: *codexOnlyFlag, NoSessionPin: *noSessionPinFlag,
		SharedCWDException: strings.TrimSpace(*sharedCWDExceptionFlag),
		NewProfileExplicit: flagWasSet(fs, "roles") || flagWasSet(fs, "binary") || flagWasSet(fs, "actor-mode") ||
			flagWasSet(fs, "codex-only") || flagWasSet(fs, "no-session-pin") || flagWasSet(fs, "shared-cwd-exception") ||
			flagWasSet(fs, "lead") || flagWasSet(fs, "lead-mode"),
		Yes: *yesFlag, JSON: *jsonFlag,
	}, nil
}

func buildSimpleWizardPlan(req simpleWizardRequest, version string, deps simpleWizardDependencies) (simpleWizardPlan, error) {
	deps = normalizeSimpleWizardDependencies(deps)
	project, err := resolveSimpleWizardProject(req.Project)
	if err != nil {
		return simpleWizardPlan{}, err
	}
	profile, existing, err := resolveSimpleWizardProfile(project, req)
	if err != nil {
		return simpleWizardPlan{}, err
	}
	session := req.Session
	if session == "" {
		session = sanitizeWorkstreamName(req.Goal)
	}
	if err := validateWorkstreamName(session); err != nil {
		return simpleWizardPlan{}, fmt.Errorf("wizard session: %w", err)
	}
	plan := simpleWizardPlan{
		SchemaVersion: wizardPlanSchemaVersion, Goal: req.Goal, Project: project,
		Profile: profile, Session: session, Existing: existing,
		Flow: "create_profile", Approved: false,
	}
	if err := preflightSimpleWizardReadiness(deps); err != nil {
		return simpleWizardPlan{}, err
	}
	if existing {
		plan.Flow = "existing_profile_session"
		if wizardNewProfileFlagsSet(req) {
			return simpleWizardPlan{}, usageErrorf("wizard profile %q already exists; --roles, --binary, --actor-mode, --lead, --lead-mode, --codex-only, --no-session-pin, and --shared-cwd-exception are new-profile options", profile)
		}
		if err := buildExistingSimpleWizardPlan(&plan, deps); err != nil {
			return simpleWizardPlan{}, err
		}
	} else if err := buildNewSimpleWizardPlan(&plan, req, version, deps); err != nil {
		return simpleWizardPlan{}, err
	}
	if err := buildSimpleWizardReadiness(&plan, deps); err != nil {
		return simpleWizardPlan{}, err
	}
	plan.Snapshots, err = snapshotSimpleWizardPaths(plan)
	if err != nil {
		return simpleWizardPlan{}, err
	}
	return plan, nil
}

func normalizeSimpleWizardDependencies(deps simpleWizardDependencies) simpleWizardDependencies {
	defaults := defaultSimpleWizardDependencies()
	if deps.Now == nil {
		deps.Now = defaults.Now
	}
	if deps.LookPath == nil {
		deps.LookPath = defaults.LookPath
	}
	if deps.ReadConfig == nil {
		deps.ReadConfig = defaults.ReadConfig
	}
	if deps.ConfigPath == nil {
		deps.ConfigPath = defaults.ConfigPath
	}
	if deps.Start == nil {
		deps.Start = defaults.Start
	}
	deps.StartDeps = normalizeSimpleStartDependencies(deps.StartDeps)
	if deps.RunGoalDraft == nil {
		deps.RunGoalDraft = defaults.RunGoalDraft
	}
	if deps.RunRules == nil {
		deps.RunRules = defaults.RunRules
	}
	if deps.RunRole == nil {
		deps.RunRole = defaults.RunRole
	}
	return deps
}

func resolveSimpleWizardProject(raw string) (string, error) {
	project := strings.TrimSpace(raw)
	if project == "" {
		var err error
		project, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("wizard getwd: %w", err)
		}
	}
	abs, err := filepath.Abs(project)
	if err != nil {
		return "", fmt.Errorf("wizard project: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("wizard project: %w", err)
	}
	if !info.IsDir() {
		return "", usageErrorf("wizard project is not a directory: %s", abs)
	}
	return filepath.Clean(abs), nil
}

func resolveSimpleWizardProfile(project string, req simpleWizardRequest) (string, bool, error) {
	if req.ProfileExplicit {
		profile, err := resolveProfileFlag(req.Profile)
		return profile, team.ExistsProfile(project, profile), err
	}
	if team.ExistsProfile(project, team.DefaultProfile) {
		return team.DefaultProfile, true, nil
	}
	named, err := team.ListProfiles(project)
	if err != nil {
		return "", false, err
	}
	if len(named) == 1 {
		return named[0], true, nil
	}
	profile := sanitizeWorkstreamName(req.Goal)
	if err := team.ValidateProfileName(profile); err != nil {
		return "", false, fmt.Errorf("wizard profile: %w", err)
	}
	if team.ExistsProfile(project, profile) {
		return profile, true, nil
	}
	return profile, false, nil
}

func wizardNewProfileFlagsSet(req simpleWizardRequest) bool {
	return req.NewProfileExplicit || req.Roles != "" || req.Binary != "" || req.ActorMode != "" || req.CodexOnly || req.NoSessionPin ||
		req.SharedCWDException != "" || req.Lead != "cto" || req.LeadMode != team.LeadModePlanner
}

func preflightSimpleWizardReadiness(deps simpleWizardDependencies) error {
	if _, err := deps.ReadConfig(); err != nil {
		return fmt.Errorf("wizard read global config: %w", err)
	}
	if _, err := deps.ConfigPath(); err != nil {
		return fmt.Errorf("wizard global config path: %w", err)
	}
	for _, name := range []string{"amq", "tmux"} {
		if _, err := deps.LookPath(name); err != nil {
			return fmt.Errorf("wizard readiness: required executable %q is unavailable: %w", name, err)
		}
	}
	return nil
}

func buildNewSimpleWizardPlan(plan *simpleWizardPlan, req simpleWizardRequest, version string, deps simpleWizardDependencies) error {
	goalData, err := buildGoalDraft(goalDraftOptions{
		Goal: req.Goal, Repo: req.Repo, Milestone: req.Milestone,
		TargetContract: req.TargetContract, Session: plan.Session, Profile: plan.Profile,
		Lead: req.Lead, LeadMode: req.LeadMode, Mode: executionModeProjectLead,
		ControlRoot: plan.Project, TargetProjectRoot: plan.Project,
		CodexOnly: req.CodexOnly, RuntimeVersion: version,
		Composition: team.CompositionSeeded, Visibility: visibilitySiblingTabs,
		ProvidedFields: map[string]bool{"target_project_root": true, "session": req.SessionExplicit, "profile": req.ProfileExplicit},
	})
	if err != nil {
		return err
	}
	if req.Roles != "" || req.Binary != "" {
		goalData.Roster, err = simpleWizardRoster(req, goalData)
		if err != nil {
			return err
		}
	}
	tm, err := simpleWizardTeam(req, goalData, deps.Now().UTC())
	if err != nil {
		return err
	}
	goalData.Roster = goalRosterFromTeam(tm)
	goalData.Lead = tm.Lead
	goalData.LeadMode = team.EffectiveLeadMode(tm)
	goalData.Execution = executionContractForTeam(tm, plan.Profile, plan.Session, goalBindingForNamespace(squadnamespace.Resolve(plan.Project, plan.Profile, plan.Session)).Mode, "", version)
	goalData.BriefSkeleton = renderGoalBriefSkeleton(goalData)
	previousGoalDrafter := runGoalDrafter
	runGoalDrafter = deps.RunGoalDraft
	err = applyGoalBriefDraft(&goalData)
	runGoalDrafter = previousGoalDrafter
	if err != nil {
		return err
	}
	if goalData.BriefDraft != nil && goalData.BriefDraft.Manual {
		return fmt.Errorf("wizard stopped before mutation: configured brief drafting requires in-session completion; rerun after configuring a headless drafter. Prompt:\n%s", goalData.BriefDraft.Prompt)
	}
	brief := goalData.BriefSkeleton
	plan.Team = tm
	profileBytes, err := simpleWizardProfileBytes(tm, plan.Project, plan.Profile)
	if err != nil {
		return err
	}
	plan.ProfilePlan = simpleWizardArtifact{Path: team.ProfilePath(plan.Project, plan.Profile), Action: "create", Content: string(profileBytes)}
	plan.BriefPlan = simpleWizardArtifact{Path: squadnamespace.BriefPath(plan.Project, plan.Profile, plan.Session), Action: "create", Content: brief}
	if goalData.BriefDraft != nil {
		plan.BriefPlan.Evidence = &simpleWizardDraftEvidence{Source: goalData.BriefDraft.ConfigSource, Evidence: goalData.BriefDraft.Evidence, Attempts: goalData.BriefDraft.Attempts}
	}
	rolePlans, err := draftSimpleWizardCustomRoles(plan, goalData, deps)
	if err != nil {
		return err
	}
	plan.RolePlans = rolePlans
	rulesBody, evidence, err := draftSimpleWizardRules(plan, deps)
	if err != nil {
		return err
	}
	plan.RulesPlan = simpleWizardArtifact{Path: rules.Path(plan.Project), Action: wizardFileAction(rules.Path(plan.Project), []byte(rulesBody)), Content: rulesBody, Evidence: evidence}
	plan.SyncPlans, err = rules.Plan(plan.Project, rulesBody)
	if err != nil {
		return fmt.Errorf("wizard plan pointer sync: %w", err)
	}
	plan.LaunchPlan = simpleWizardLaunch(plan)
	return nil
}

func buildExistingSimpleWizardPlan(plan *simpleWizardPlan, deps simpleWizardDependencies) error {
	tm, err := team.ReadProfile(plan.Project, plan.Profile)
	if err != nil {
		return fmt.Errorf("wizard read existing profile: %w", err)
	}
	if _, err := resolveTeamWorkstreamName(tm, plan.Session, true); err != nil {
		return fmt.Errorf("wizard existing-profile session: %w", err)
	}
	rulesBody, err := rules.Read(plan.Project)
	if err != nil {
		return fmt.Errorf("wizard read team rules: %w", err)
	}
	briefPath := squadnamespace.BriefPath(plan.Project, plan.Profile, plan.Session)
	briefBytes, exists, err := readSimpleStartBriefBytes(briefPath)
	if err != nil {
		return err
	}
	var evidence *simpleWizardDraftEvidence
	if !exists {
		draftDeps := deps.StartDeps
		draftDeps.ResolveDrafter = resolveCLIDrafter
		draftDeps.RunDrafter = deps.RunGoalDraft
		draft, draftErr := draftSimpleStartBrief(plan.Project, plan.Profile, plan.Session, plan.Goal, tm, normalizeSimpleStartDependencies(draftDeps))
		if draftErr != nil {
			return draftErr
		}
		if draft.Manual {
			return fmt.Errorf("wizard stopped before mutation: configured brief drafting requires in-session completion; rerun after configuring a headless drafter. Prompt:\n%s", draft.Prompt)
		}
		briefBytes = append([]byte(nil), draft.Document...)
		evidence = &simpleWizardDraftEvidence{Source: draft.ConfigSource, Evidence: draft.Evidence, Attempts: draft.Attempts}
	}
	plan.Team = tm
	profileBytes, err := os.ReadFile(team.ProfilePath(plan.Project, plan.Profile))
	if err != nil {
		return err
	}
	plan.ProfilePlan = simpleWizardArtifact{Path: team.ProfilePath(plan.Project, plan.Profile), Action: "reuse", Content: string(profileBytes)}
	plan.RulesPlan = simpleWizardArtifact{Path: rules.Path(plan.Project), Action: "reuse", Content: rulesBody}
	plan.BriefPlan = simpleWizardArtifact{Path: briefPath, Action: map[bool]string{true: "reuse", false: "create"}[exists], Content: string(briefBytes), Evidence: evidence}
	plan.LaunchPlan = simpleWizardLaunch(plan)
	return nil
}

func simpleWizardRoster(req simpleWizardRequest, data goalDraftData) ([]goalRosterMember, error) {
	roles := splitCSV(req.Roles)
	if req.Roles != "" && len(roles) == 0 {
		return nil, usageErrorf("wizard --roles selected no roles")
	}
	if len(roles) == 0 {
		for _, member := range data.Roster {
			roles = append(roles, member.Role)
		}
	}
	binaries, err := parseKV(req.Binary)
	if err != nil {
		return nil, fmt.Errorf("wizard --binary: %w", err)
	}
	binaries = lowercaseKeys(binaries)
	roster := make([]goalRosterMember, 0, len(roles))
	seen := map[string]bool{}
	for _, raw := range roles {
		id := strings.ToLower(strings.TrimSpace(raw))
		if seen[id] {
			continue
		}
		seen[id] = true
		if err := team.ValidateRoleID(id); err != nil {
			return nil, fmt.Errorf("wizard role %q: %w", id, err)
		}
		binary := strings.TrimSpace(binaries[id])
		reason := "Custom seat proposed from the operator-selected wizard roster."
		for _, proposed := range data.Roster {
			if proposed.Role == id {
				reason = proposed.Reason
				if binary == "" {
					binary = proposed.Binary
				}
				break
			}
		}
		if known := catalog.Lookup(id); known != nil {
			if binary == "" {
				binary = known.PreferredBinary
			}
			reason = known.Description
		} else if binary == "" {
			return nil, usageErrorf("wizard custom role %q requires --binary %s=claude|codex", id, id)
		}
		if req.CodexOnly {
			binary = "codex"
		}
		if binary != "claude" && binary != "codex" {
			return nil, usageErrorf("wizard --binary %s=%s: use claude or codex", id, binary)
		}
		roster = append(roster, goalRosterMember{Role: id, Handle: id, Binary: binary, Reason: reason})
	}
	if !seen[data.Lead] {
		return nil, usageErrorf("wizard --lead %s must be present in --roles", data.Lead)
	}
	for role := range binaries {
		if !seen[role] {
			return nil, usageErrorf("wizard --binary has unknown role %q", role)
		}
	}
	return roster, nil
}

func simpleWizardTeam(req simpleWizardRequest, data goalDraftData, created time.Time) (team.Team, error) {
	actorModes, err := parseKV(req.ActorMode)
	if err != nil {
		return team.Team{}, fmt.Errorf("wizard --actor-mode: %w", err)
	}
	actorModes = lowercaseKeys(actorModes)
	op := team.DefaultOperator()
	tm := team.Team{
		Project: data.TargetProjectRoot, Operator: &op, Orchestrated: true, Lead: data.Lead,
		Composition: team.CompositionSeeded, ExecutionMode: executionModeProjectLead,
		ControlRoot: data.TargetProjectRoot, TargetProjectRoot: data.TargetProjectRoot,
		TargetContract: strings.TrimPrefix(data.TargetContract, "v"), LeadMode: leadModeForPersist(data.LeadMode),
		SharedCwdException: req.SharedCWDException, CreatedAt: created,
	}
	implementationAssigned := false
	selected := map[string]bool{}
	for _, proposed := range data.Roster {
		selected[proposed.Role] = true
		mode := strings.TrimSpace(actorModes[proposed.Role])
		if mode == "" {
			if proposed.Role == data.Lead || implementationAssigned {
				mode = team.ActorModeReview
			} else {
				mode = team.ActorModeImplementation
				implementationAssigned = true
			}
		}
		if mode != team.ActorModeImplementation && mode != team.ActorModeReview {
			return team.Team{}, usageErrorf("wizard --actor-mode %s=%s: use implementation or review", proposed.Role, mode)
		}
		session := data.Session
		if req.NoSessionPin {
			session = ""
		}
		tm.Members = append(tm.Members, team.Member{Role: proposed.Role, Handle: proposed.Handle, Binary: proposed.Binary, Session: session, ActorMode: mode})
	}
	for role := range actorModes {
		if !selected[role] {
			return team.Team{}, usageErrorf("wizard --actor-mode has unknown role %q", role)
		}
	}
	normalized, err := team.NormalizeForWrite(data.TargetProjectRoot, data.Profile, tm)
	if err != nil {
		return team.Team{}, err
	}
	if row := worktreeIsolationCheckForSession(normalized, data.Profile, data.Session); row.Status == "blocked" {
		return team.Team{}, fmt.Errorf("wizard worktree isolation blocked: %s; %s", row.Evidence, row.Fix)
	}
	return normalized, nil
}

func goalRosterFromTeam(tm team.Team) []goalRosterMember {
	out := make([]goalRosterMember, 0, len(tm.Members))
	for _, member := range tm.Members {
		reason := "Operator-selected wizard seat."
		if known := catalog.Lookup(member.Role); known != nil {
			reason = known.Description
		}
		out = append(out, goalRosterMember{Role: member.Role, Handle: member.Handle, Binary: member.Binary, Reason: reason})
	}
	return out
}

func simpleWizardProfileBytes(tm team.Team, project, profile string) ([]byte, error) {
	normalized, err := team.NormalizeForWrite(project, profile, tm)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(normalized, "", "  ")
}

func draftSimpleWizardRules(plan *simpleWizardPlan, deps simpleWizardDependencies) (string, *simpleWizardDraftEvidence, error) {
	template, err := selectTeamRulesTemplate("auto", plan.Team)
	if err != nil {
		return "", nil, err
	}
	var prose *teamRulesProse
	var evidence *simpleWizardDraftEvidence
	if teamRulesNeedsDraft(plan.Team, template) {
		previous := runTeamRulesDrafter
		runTeamRulesDrafter = deps.RunRules
		draft, draftErr := draftTeamRulesProse(plan.Project, template, plan.Team)
		runTeamRulesDrafter = previous
		if draftErr != nil {
			return "", nil, draftErr
		}
		evidence = &simpleWizardDraftEvidence{Source: draft.ConfigSource, Manual: draft.Manual, Prompt: draft.Prompt, Evidence: draft.Evidence, Attempts: draft.Attempts}
		if draft.Manual {
			return "", evidence, fmt.Errorf("wizard stopped before mutation: team-rules drafting requires in-session completion. Prompt:\n%s", draft.Prompt)
		}
		prose = draft.Prose
	}
	body, err := renderTeamRulesWithTemplateDraft(plan.Team, template, prose)
	return body, evidence, err
}

func draftSimpleWizardCustomRoles(plan *simpleWizardPlan, data goalDraftData, deps simpleWizardDependencies) ([]simpleWizardArtifact, error) {
	var artifacts []simpleWizardArtifact
	for _, member := range data.Roster {
		if catalog.Lookup(member.Role) != nil {
			continue
		}
		path := team.CustomRolePath(plan.Project, member.Role)
		if existing, err := os.ReadFile(path); err == nil {
			artifacts = append(artifacts, simpleWizardArtifact{Path: path, Action: "reuse", Content: string(existing)})
			continue
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		peers := make([]string, 0, len(data.Roster)-1)
		for _, peer := range data.Roster {
			if peer.Role != member.Role {
				peers = append(peers, peer.Role)
			}
		}
		prompt := buildRoleDraftPrompt(member.Role, member.Role, member.Binary, member.Reason, peers, plan.BriefPlan.Content)
		resolved, err := resolveCLIDrafter(plan.Team.Drafter)
		if err != nil {
			return nil, err
		}
		result, runErr := deps.RunRole(context.Background(), resolved.Config, drafter.Request{Prompt: prompt, WorkingDirectory: plan.Project})
		if runErr != nil {
			return nil, fmt.Errorf("wizard draft custom role %q: %w; %s", member.Role, runErr, cliDrafterErrorEvidence(resolved.Source, result.Attempts, result.Evidence))
		}
		if result.UseInSession {
			return nil, fmt.Errorf("wizard stopped before mutation: custom role %q requires in-session drafting. Prompt:\n%s", member.Role, prompt)
		}
		document, err := validateRoleDraftDocument(result.Text, path, member.Role, member.Role, member.Binary, peers, plan.Session, roleDraftCurrentBranch(plan.Project))
		if err != nil {
			return nil, fmt.Errorf("wizard validate custom role %q: %w", member.Role, err)
		}
		artifacts = append(artifacts, simpleWizardArtifact{Path: path, Action: "create", Content: document, Evidence: &simpleWizardDraftEvidence{Source: resolved.Source, Evidence: result.Evidence, Attempts: result.Attempts}})
	}
	return artifacts, nil
}

func buildSimpleWizardReadiness(plan *simpleWizardPlan, deps simpleWizardDependencies) error {
	config, err := deps.ReadConfig()
	if err != nil {
		return fmt.Errorf("wizard read global config: %w", err)
	}
	configPath, _ := deps.ConfigPath()
	resolved, err := resolveCLIDrafter(plan.Team.Drafter)
	if err != nil {
		return err
	}
	selection := setupDrafterSelection(config.Drafter)
	if plan.Team.Drafter != nil {
		selection = setupDrafterSelection(plan.Team.Drafter)
	}
	executables := []string{"amq", "tmux"}
	for _, member := range plan.Team.Members {
		executables = append(executables, member.Binary)
	}
	sort.Strings(executables)
	executables = uniqueStrings(executables)
	for _, name := range executables {
		if _, err := deps.LookPath(name); err != nil {
			return fmt.Errorf("wizard readiness: required executable %q is unavailable: %w", name, err)
		}
	}
	plan.Readiness = simpleWizardReadiness{GlobalConfigPath: configPath, DrafterSource: resolved.Source, DrafterSelection: selection, Executables: executables}
	return nil
}

func simpleWizardLaunch(plan *simpleWizardPlan) simpleWizardLaunchPlan {
	roles := make([]string, 0, len(plan.Team.Members))
	for _, member := range plan.Team.Members {
		roles = append(roles, member.Role)
	}
	args := []string{"amq-squad", "start", "--project", plan.Project, "--profile", plan.Profile, "--session", plan.Session, "--goal", plan.Goal, "--yes"}
	return simpleWizardLaunchPlan{Command: shellJoin(args), Root: squadnamespace.AMQRoot(plan.Project, plan.Profile, plan.Session), Roles: roles}
}

func renderSimpleWizardPlan(out io.Writer, plan simpleWizardPlan) {
	fmt.Fprintln(out, "amq-squad wizard combined review")
	fmt.Fprintf(out, "\nStage 1/5 readiness: ready\n  drafter: %s (%s)\n  global config: %s\n  executables: %s\n", plan.Readiness.DrafterSelection, plan.Readiness.DrafterSource, orDash(plan.Readiness.GlobalConfigPath), strings.Join(plan.Readiness.Executables, ", "))
	fmt.Fprintf(out, "\nStage 2/5 profile: %s\n  flow: %s\n  project: %s\n  profile: %s\n  session: %s\n  path: %s\n", plan.ProfilePlan.Action, plan.Flow, plan.Project, plan.Profile, plan.Session, plan.ProfilePlan.Path)
	fmt.Fprintln(out, "\nExact profile bytes:")
	fmt.Fprintln(out, plan.ProfilePlan.Content)
	fmt.Fprintf(out, "\nStage 3/5 optional custom seats: %d\n", len(plan.RolePlans))
	for _, artifact := range plan.RolePlans {
		fmt.Fprintf(out, "\n%s (%s):\n%s\n", artifact.Path, artifact.Action, artifact.Content)
		writeSimpleWizardEvidence(out, artifact.Evidence)
	}
	fmt.Fprintf(out, "\nStage 4/5 rules: %s\n  path: %s\n\nExact rules bytes:\n%s", plan.RulesPlan.Action, plan.RulesPlan.Path, plan.RulesPlan.Content)
	if !strings.HasSuffix(plan.RulesPlan.Content, "\n") {
		fmt.Fprintln(out)
	}
	writeSimpleWizardEvidence(out, plan.RulesPlan.Evidence)
	fmt.Fprintf(out, "\nStage 5/5 brief and start review: %s\n  brief: %s\n  AM_ROOT: %s\n  roles: %s\n\nExact brief bytes:\n%s", plan.BriefPlan.Action, plan.BriefPlan.Path, plan.LaunchPlan.Root, strings.Join(plan.LaunchPlan.Roles, ", "), plan.BriefPlan.Content)
	if !strings.HasSuffix(plan.BriefPlan.Content, "\n") {
		fmt.Fprintln(out)
	}
	writeSimpleWizardEvidence(out, plan.BriefPlan.Evidence)
	fmt.Fprintf(out, "\nApproved execution:\n  %s\n\n", plan.LaunchPlan.Command)
}

func writeSimpleWizardEvidence(out io.Writer, evidence *simpleWizardDraftEvidence) {
	if evidence == nil {
		return
	}
	fmt.Fprintf(out, "Drafter config source: %s\n", evidence.Source)
	fmt.Fprint(out, cliDrafterAttemptsText(evidence.Attempts, evidence.Evidence))
}

func confirmSimpleWizard(out io.Writer, in io.Reader) bool {
	fmt.Fprint(out, "Apply these exact artifacts and start the squad? [y/N] ")
	line, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && strings.TrimSpace(line) == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func snapshotSimpleWizardPaths(plan simpleWizardPlan) ([]simpleWizardSnapshot, error) {
	paths := []string{plan.ProfilePlan.Path, plan.RulesPlan.Path, plan.BriefPlan.Path}
	for _, artifact := range plan.RolePlans {
		paths = append(paths, artifact.Path)
	}
	seen := map[string]bool{}
	var snapshots []simpleWizardSnapshot
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		snapshot, err := readSimpleWizardSnapshot(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, nil
}

func readSimpleWizardSnapshot(path string) (simpleWizardSnapshot, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return simpleWizardSnapshot{Path: path}, nil
	}
	if err != nil {
		return simpleWizardSnapshot{}, fmt.Errorf("wizard snapshot %s: %w", path, err)
	}
	return simpleWizardSnapshot{Path: path, Exists: true, Digest: sha256.Sum256(b)}, nil
}

func verifySimpleWizardSnapshots(snapshots []simpleWizardSnapshot) error {
	for _, accepted := range snapshots {
		current, err := readSimpleWizardSnapshot(accepted.Path)
		if err != nil {
			return err
		}
		if current.Exists != accepted.Exists || current.Digest != accepted.Digest {
			return fmt.Errorf("wizard plan changed while awaiting approval: %s; review and rerun", accepted.Path)
		}
	}
	return nil
}

func applySimpleWizardPlan(plan *simpleWizardPlan) error {
	if err := verifySimpleWizardSnapshots(plan.Snapshots); err != nil {
		return err
	}
	if !plan.Existing {
		if err := team.WithProfileLock(plan.Project, plan.Profile, func() error {
			if team.ExistsProfile(plan.Project, plan.Profile) {
				return fmt.Errorf("wizard profile appeared while awaiting approval: %s", plan.ProfilePlan.Path)
			}
			return team.WriteProfileUnderLock(plan.Project, plan.Profile, plan.Team)
		}); err != nil {
			return err
		}
	}
	for _, artifact := range plan.RolePlans {
		if artifact.Action != "create" {
			continue
		}
		if err := stageRoleDraft(artifact.Path, artifact.Content); err != nil {
			return err
		}
	}
	if plan.RulesPlan.Action != "reuse" {
		if err := verifySimpleWizardPathSnapshot(plan.Snapshots, plan.RulesPlan.Path); err != nil {
			return err
		}
		if err := rules.Write(plan.Project, plan.RulesPlan.Content); err != nil {
			return fmt.Errorf("wizard write rules: %w", err)
		}
	}
	if len(plan.SyncPlans) > 0 {
		if _, err := rules.Apply(plan.SyncPlans); err != nil {
			return fmt.Errorf("wizard sync root pointers: %w", err)
		}
	}
	if plan.BriefPlan.Action == "create" {
		if err := ensureSimpleStartBrief(plan.BriefPlan.Path, []byte(plan.BriefPlan.Content)); err != nil {
			return err
		}
	}
	return nil
}

func verifySimpleWizardPathSnapshot(snapshots []simpleWizardSnapshot, path string) error {
	for _, snapshot := range snapshots {
		if snapshot.Path == path {
			return verifySimpleWizardSnapshots([]simpleWizardSnapshot{snapshot})
		}
	}
	return fmt.Errorf("wizard internal error: no accepted snapshot for %s", path)
}

func wizardFileAction(path string, desired []byte) string {
	current, err := os.ReadFile(path)
	if err == nil && string(current) == string(desired) {
		return "reuse"
	}
	if os.IsNotExist(err) {
		return "create"
	}
	return "refresh"
}

func uniqueStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if len(out) == 0 || out[len(out)-1] != value {
			out = append(out, value)
		}
	}
	return out
}
