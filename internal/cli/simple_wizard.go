package cli

import (
	"bufio"
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

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/userconfig"
)

const wizardPlanSchemaVersion = 2

// gh#759/t13 commit 4 (cto's ruling): wizard is no longer its own bespoke
// drafting/roster-building implementation -- it is the literal composition
// of three already-locked verbs' own machinery: 'init' (gh#762, profile +
// team-rules.md + pointer stubs, digest-gated), 'brief' (gh#759 commit 1,
// the one place a drafter runs on a launch-capable path), and 'plan'/'start
// --apply' (gh#756/gh#757, zero-write preview + digest-gated launch). Wizard
// adds only ONE thing none of the three has on its own: a single combined
// confirmation covering all of them before any write happens.
//
// The goal-to-roster inference the old bespoke implementation used to run
// (turning a goal description into a proposed roster via the drafter) is
// dropped entirely, not reimplemented against init's roster machinery: a new
// profile now requires an explicit --roles, matching what 'init' itself
// requires non-interactively. Suggesting a roster from a goal is tracked
// separately as a future, explicitly off-launch-path verb (gh#790) -- never
// silently reintroduced into wizard's own combined, launch-capable flow.
type simpleWizardDependencies struct {
	Now          func() time.Time
	LookPath     func(string) (string, error)
	ReadConfig   func() (userconfig.Config, error)
	ConfigPath   func() (string, error)
	Start        func([]string, simpleStartDependencies, io.Reader, io.Writer) error
	StartDeps    simpleStartDependencies
	RunGoalDraft cliDrafterRunner
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
	}
}

type simpleWizardRequest struct {
	Goal               string
	Project            string
	Profile            string
	ProfileExplicit    bool
	Session            string
	SessionExplicit    bool
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
	RulesPlan     simpleWizardArtifact   `json:"rules"`
	BriefPlan     simpleWizardArtifact   `json:"brief"`
	LaunchPlan    simpleWizardLaunchPlan `json:"launch"`
	InitDigest    string                 `json:"init_digest,omitempty"`
	InitArgs      []string               `json:"-"`
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

// runWizardWithVersion keeps the version parameter for command_registry.go's
// existing call site; wizard's own logic no longer threads a target
// contract version through the composed verbs (each resolves its own
// default the same way a direct invocation would).
func runWizardWithVersion(args []string, _ string) error {
	return runWizardWithDependencies(args, defaultSimpleWizardDependencies(), os.Stdin, os.Stdout, os.Stderr)
}

func runWizardWithDependencies(args []string, deps simpleWizardDependencies, in io.Reader, out, errOut io.Writer) error {
	req, err := parseSimpleWizardRequest(args, errOut)
	if err != nil {
		return err
	}
	if req.JSON && req.Yes {
		return usageErrorf("wizard --json is preview-only and cannot be combined with --yes")
	}
	plan, err := buildSimpleWizardPlan(req, deps)
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
	if deps.Start == nil {
		return fmt.Errorf("wizard start dependency is not configured")
	}
	startArgs, err := wizardStartArgs(plan, deps.StartDeps, out)
	if err != nil {
		return fmt.Errorf("wizard staged the reviewed artifacts, but preparing the launch did not complete: %w; rerun the same wizard command to recover", err)
	}
	if err := deps.Start(startArgs, deps.StartDeps, in, out); err != nil {
		return fmt.Errorf("wizard staged the reviewed artifacts, but start did not complete: %w; rerun the same wizard command to recover", err)
	}
	return nil
}

// wizardStartArgs builds the argv wizard hands to deps.Start after its own
// one confirmation already approved both the staged artifacts and the
// launch that follows them.
//
// gh#757 finding (fullstack, task/t8): plain --yes silently no-ops on the
// launchapi default path -- start's own digest gate (simple_start.go) reads
// its confirmation from a --apply <subject_digest> match or an interactive
// prompt, and --yes affects neither, so a --yes-only wizard handoff into a
// non-interactive reader reads as "cancelled" with nothing launched and
// nothing reported as an error.
//
// The fix (cto's ruling on task/t8, option (i)): run the identical
// parseSimpleStartRequest -> buildSimpleStartPlan -> (launchapiTeamLaunchBackend{}).prepare
// sequence start's own probe uses, with the same args this function is
// about to hand to deps.Start (minus --apply, which does not exist yet).
// Reusing exactly those functions -- not internal/cli/plan.go's planPrepare,
// which independently resolves trust mode and startup-prompt text for the
// standalone `plan` command and is not guaranteed to compile the identical
// intent -- is what makes the subject_digest this function prints
// byte-identical to what start's own probe recomputes moments later: same
// code, same inputs, called back to back in one process. start's own probe
// re-verifies it anyway (the session-lock-guarded second Prepare in
// runStartWithDependencies), so any actual drift between the two calls
// still refuses closed there; this is only what makes the common,
// no-drift case succeed instead of always refusing.
//
// A legacy-backend or already-fully-live team (no members left to spawn)
// keeps the pre-gh#757 --yes behavior unchanged: --apply only has meaning
// on the launchapi path, and start itself accepts --yes harmlessly on
// every path (a no-op notice on launchapi, its real meaning on legacy).
func wizardStartArgs(plan simpleWizardPlan, startDeps simpleStartDependencies, out io.Writer) ([]string, error) {
	base := []string{"--project", plan.Project, "--profile", plan.Profile, "--session", plan.Session, "--goal", plan.Goal}
	probeReq, err := parseSimpleStartRequest(base)
	if err != nil {
		return nil, fmt.Errorf("parse start args for launch preview: %w", err)
	}
	probeAccepted, err := buildSimpleStartPlan(probeReq, startDeps)
	if err != nil {
		return nil, fmt.Errorf("build launch preview: %w", err)
	}
	if !probeReq.LaunchapiPath || len(probeAccepted.SpawnTeam.Members) == 0 {
		return append(append([]string(nil), base...), "--yes"), nil
	}
	probePrepared, _, err := (launchapiTeamLaunchBackend{}).prepare(probeAccepted.SpawnTeam, probeAccepted.LaunchOptions)
	if err != nil {
		return nil, fmt.Errorf("preview launch plan: %w", err)
	}
	printPlanResult(out, probePrepared.Result)
	// Per cto's ruling: wizard's one confirmation only covers the plan as
	// rendered by renderSimpleWizardPlan, which does not preview
	// trust/rebind/capability required actions. Never auto-decide one here
	// -- refuse closed and name it, same "no action ever auto-selected"
	// norm start's own interactive gate flow already holds.
	if len(probePrepared.Result.RequiredActions) > 0 {
		ra := probePrepared.Result.RequiredActions[0]
		return nil, fmt.Errorf("%d operator decision(s) required before this launch (first: action_id=%s kind=%s reason_code=%s allowed_decisions=%v); wizard does not auto-decide trust/rebind/capability gates -- resolve via 'amq-squad start --decision %s=<choice>' or the gate/<topic> thread, then re-run wizard", len(probePrepared.Result.RequiredActions), ra.ActionID, ra.Kind, ra.ReasonCode, ra.AllowedDecisions, ra.ActionID)
	}
	digest := probePrepared.Result.SubjectDigest
	fmt.Fprintf(out, "Launch subject_digest: %s\n", digest)
	return append(append([]string(nil), base...), "--apply", digest), nil
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
	leadFlag := fs.String("lead", "cto", "lead role for a newly proposed profile")
	leadModeFlag := fs.String("lead-mode", team.LeadModePlanner, "new-profile lead posture: planner or builder")
	rolesFlag := fs.String("roles", "", "comma-separated roles for a new profile (required to create one; wizard never infers a roster from the goal)")
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
		fmt.Fprint(errOut, `amq-squad wizard - a single combined confirmation over brief + init + plan + start

Usage:
  amq-squad wizard "GOAL" [options]
  amq-squad wizard --goal TEXT [options]

wizard is the literal composition of 'init' (profile + team-rules.md +
pointer stubs), 'brief' (drafts the workstream brief), and 'plan'/'start
--apply' (zero-write launch preview, then launch) -- it adds only ONE
thing none of those three has on its own: a single combined confirmation
covering all of them before any write happens. The final confirmation
defaults to No; nothing is created, drafted, or launched before that
approval. Rerun the same command after interruption to roll forward.

Creating a new profile requires an explicit --roles: wizard never infers a
roster from the goal text (roster suggestion from a goal is a separate,
off-launch-path verb, tracked at gh#790 -- never silently reintroduced
here). Launching into an EXISTING profile/session reuses it as-is and
rejects new-profile-only flags (--roles, --binary, --actor-mode,
--codex-only, --no-session-pin, --shared-cwd-exception, --lead,
--lead-mode).

Options:
`)
		fs.PrintDefaults()
		fmt.Fprint(errOut, `
Examples:
  amq-squad wizard "Ship issue 709" --roles cto,fullstack
  amq-squad wizard "Release v2.29.6" --profile release --session v2-29-6
  amq-squad wizard --goal "Build the reviewed change" --roles cto,fullstack --binary cto=codex
  amq-squad wizard "Preview only" --roles cto --json
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
	return simpleWizardRequest{
		Goal: goal, Project: strings.TrimSpace(*projectFlag),
		Profile: strings.TrimSpace(*profileFlag), ProfileExplicit: flagWasSet(fs, "profile"),
		Session: strings.TrimSpace(*sessionFlag), SessionExplicit: flagWasSet(fs, "session"),
		Lead: strings.TrimSpace(*leadFlag), LeadMode: strings.TrimSpace(*leadModeFlag),
		Roles: strings.TrimSpace(*rolesFlag), Binary: strings.TrimSpace(*binaryFlag), ActorMode: strings.TrimSpace(*actorModeFlag),
		CodexOnly: *codexOnlyFlag, NoSessionPin: *noSessionPinFlag,
		SharedCWDException: strings.TrimSpace(*sharedCWDExceptionFlag),
		NewProfileExplicit: flagWasSet(fs, "roles") || flagWasSet(fs, "binary") || flagWasSet(fs, "actor-mode") ||
			flagWasSet(fs, "codex-only") || flagWasSet(fs, "no-session-pin") || flagWasSet(fs, "shared-cwd-exception") ||
			flagWasSet(fs, "lead") || flagWasSet(fs, "lead-mode"),
		Yes: *yesFlag, JSON: *jsonFlag,
	}, nil
}

func buildSimpleWizardPlan(req simpleWizardRequest, deps simpleWizardDependencies) (simpleWizardPlan, error) {
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
		if err := buildExistingSimpleWizardPlan(&plan, req, deps); err != nil {
			return simpleWizardPlan{}, err
		}
	} else if err := buildNewSimpleWizardPlan(&plan, req, deps); err != nil {
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
	cfg, err := deps.ReadConfig()
	if err != nil {
		return fmt.Errorf("wizard read global config: %w", err)
	}
	if _, err := deps.ConfigPath(); err != nil {
		return fmt.Errorf("wizard global config path: %w", err)
	}
	// gh#760: catch a yoetz-preset drafter with no model here, before the
	// drafter ever runs, rather than only at yoetz's own opaque invocation
	// failure. Same rule setup.go already enforces via ValidateGlobal.
	if err := drafter.ValidateGlobal(cfg.Drafter); err != nil {
		return fmt.Errorf("wizard readiness: drafter config: %w", err)
	}
	for _, name := range []string{"amq", "tmux"} {
		if _, err := deps.LookPath(name); err != nil {
			return fmt.Errorf("wizard readiness: required executable %q is unavailable: %w", name, err)
		}
	}
	return nil
}

// buildInitArgsForNewProfile translates wizard's own new-profile flags into
// the argv 'init' (internal/cli/init.go) itself accepts, so a new profile is
// created through init's own machinery rather than a wizard-local
// reimplementation of roster/rules construction. Requiring --roles here
// (rather than falling through to init's own interactive stdin prompt when
// it is absent, or inferring one from the goal) is cto's ruling on task/t13:
// no silent degradation -- a new profile's roster is always an explicit,
// operator-supplied decision now.
func buildInitArgsForNewProfile(req simpleWizardRequest, project, profile, session string) ([]string, error) {
	if req.Roles == "" {
		return nil, usageErrorf("wizard requires --roles to create a new profile %q: wizard no longer infers a roster from the goal text (roster suggestion from a goal is tracked separately at gh#790, not part of this launch-capable path) -- pass --roles explicitly, e.g. --roles cto,fullstack, or run 'amq-squad init --roles ...' yourself first", profile)
	}
	args := []string{"--project", project, "--profile", profile, "--roles", req.Roles, "--lead", req.Lead, "--lead-mode", req.LeadMode}
	if req.NoSessionPin {
		args = append(args, "--no-session-pin")
	} else {
		args = append(args, "--session", session)
	}
	if req.CodexOnly {
		if req.Binary != "" {
			return nil, usageErrorf("wizard --codex-only and --binary are mutually exclusive; --codex-only already forces every role to codex")
		}
		roles := splitCSV(req.Roles)
		pairs := make([]string, 0, len(roles))
		for _, role := range roles {
			pairs = append(pairs, strings.ToLower(strings.TrimSpace(role))+"=codex")
		}
		args = append(args, "--binary", strings.Join(pairs, ","))
	} else if req.Binary != "" {
		args = append(args, "--binary", req.Binary)
	}
	if req.ActorMode != "" {
		args = append(args, "--actor-mode", req.ActorMode)
	}
	if req.SharedCWDException != "" {
		args = append(args, "--shared-cwd-exception", req.SharedCWDException)
	}
	return args, nil
}

// buildNewSimpleWizardPlan computes a new profile's plan entirely through
// init's own zero-write computeInitPlan (internal/cli/init.go) -- the
// planned team.Team and rendered team-rules.md it captures are init's real
// output, not a wizard-local reconstruction, so applySimpleWizardPlan can
// later hand the identical argv to init's own apply path unchanged.
func buildNewSimpleWizardPlan(plan *simpleWizardPlan, req simpleWizardRequest, deps simpleWizardDependencies) error {
	initArgs, err := buildInitArgsForNewProfile(req, plan.Project, plan.Profile, plan.Session)
	if err != nil {
		return err
	}
	initialized, err := computeInitPlan(initArgs)
	if err != nil {
		return err
	}
	profileJSON, err := json.MarshalIndent(initialized.Team, "", "  ")
	if err != nil {
		return fmt.Errorf("wizard marshal planned profile: %w", err)
	}
	plan.InitArgs = initArgs
	plan.InitDigest = initialized.Digest
	plan.Team = initialized.Team
	plan.ProfilePlan = simpleWizardArtifact{Path: team.ProfilePath(plan.Project, plan.Profile), Action: "create", Content: string(profileJSON)}
	plan.RulesPlan = simpleWizardArtifact{Path: rules.Path(plan.Project), Action: "create", Content: initialized.RulesContent}
	if err := buildSimpleWizardBrief(plan, req, deps); err != nil {
		return err
	}
	plan.LaunchPlan = simpleWizardLaunch(plan)
	return nil
}

func buildExistingSimpleWizardPlan(plan *simpleWizardPlan, req simpleWizardRequest, deps simpleWizardDependencies) error {
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
	plan.Team = tm
	profileBytes, err := os.ReadFile(team.ProfilePath(plan.Project, plan.Profile))
	if err != nil {
		return err
	}
	plan.ProfilePlan = simpleWizardArtifact{Path: team.ProfilePath(plan.Project, plan.Profile), Action: "reuse", Content: string(profileBytes)}
	plan.RulesPlan = simpleWizardArtifact{Path: rules.Path(plan.Project), Action: "reuse", Content: rulesBody}
	if err := buildSimpleWizardBrief(plan, req, deps); err != nil {
		return err
	}
	plan.LaunchPlan = simpleWizardLaunch(plan)
	return nil
}

// buildSimpleWizardBrief drafts (or reuses) the session's brief through the
// exact same primitive 'brief --goal' itself calls (draftSimpleStartBrief),
// shared verbatim by both the new- and existing-profile flows -- brief
// drafting no longer has a wizard-specific path.
func buildSimpleWizardBrief(plan *simpleWizardPlan, req simpleWizardRequest, deps simpleWizardDependencies) error {
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
		draft, draftErr := draftSimpleStartBrief(plan.Project, plan.Profile, plan.Session, req.Goal, plan.Team, normalizeSimpleStartDependencies(draftDeps))
		if draftErr != nil {
			return draftErr
		}
		if draft.Manual {
			return fmt.Errorf("wizard stopped before mutation: configured brief drafting requires in-session completion; rerun after configuring a headless drafter. %s\nPrompt:\n%s",
				cliDrafterErrorEvidence(draft.ConfigSource, draft.Attempts, draft.Evidence), draft.Prompt)
		}
		briefBytes = append([]byte(nil), draft.Document...)
		evidence = &simpleWizardDraftEvidence{Source: draft.ConfigSource, Evidence: draft.Evidence, Attempts: draft.Attempts}
	}
	plan.BriefPlan = simpleWizardArtifact{Path: briefPath, Action: map[bool]string{true: "reuse", false: "create"}[exists], Content: string(briefBytes), Evidence: evidence}
	return nil
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
	// --json preview only: this runs before applySimpleWizardPlan has
	// written anything, so no subject_digest can exist yet to show the real
	// --apply <digest> invocation wizardStartArgs computes at actual
	// handoff time (gh#757). This --yes rendering is illustrative shell
	// text, not what --json mode (which never calls deps.Start) or the
	// real approved-and-applied path actually runs.
	args := []string{"amq-squad", "start", "--project", plan.Project, "--profile", plan.Profile, "--session", plan.Session, "--goal", plan.Goal, "--yes"}
	return simpleWizardLaunchPlan{Command: shellJoin(args), Root: squadnamespace.AMQRoot(plan.Project, plan.Profile, plan.Session), Roles: roles}
}

func renderSimpleWizardPlan(out io.Writer, plan simpleWizardPlan) {
	fmt.Fprintln(out, "amq-squad wizard combined review")
	fmt.Fprintf(out, "\nStage 1/4 readiness: ready\n  drafter: %s (%s)\n  global config: %s\n  executables: %s\n", plan.Readiness.DrafterSelection, plan.Readiness.DrafterSource, orDash(plan.Readiness.GlobalConfigPath), strings.Join(plan.Readiness.Executables, ", "))
	fmt.Fprintf(out, "\nStage 2/4 profile & rules (via 'init'): %s\n  flow: %s\n  project: %s\n  profile: %s\n  session: %s\n  path: %s\n", plan.ProfilePlan.Action, plan.Flow, plan.Project, plan.Profile, plan.Session, plan.ProfilePlan.Path)
	fmt.Fprintln(out, "\nExact profile bytes:")
	fmt.Fprintln(out, plan.ProfilePlan.Content)
	fmt.Fprintf(out, "\nrules: %s\n  path: %s\n\nExact rules bytes:\n%s", plan.RulesPlan.Action, plan.RulesPlan.Path, plan.RulesPlan.Content)
	if !strings.HasSuffix(plan.RulesPlan.Content, "\n") {
		fmt.Fprintln(out)
	}
	fmt.Fprintf(out, "\nStage 3/4 brief (via 'brief'): %s\n  brief: %s\n  AM_ROOT: %s\n  roles: %s\n\nExact brief bytes:\n%s", plan.BriefPlan.Action, plan.BriefPlan.Path, plan.LaunchPlan.Root, strings.Join(plan.LaunchPlan.Roles, ", "), plan.BriefPlan.Content)
	if !strings.HasSuffix(plan.BriefPlan.Content, "\n") {
		fmt.Fprintln(out)
	}
	writeSimpleWizardEvidence(out, plan.BriefPlan.Evidence)
	fmt.Fprintf(out, "\nStage 4/4 approved execution (via 'plan'/'start'):\n  %s\n\n", plan.LaunchPlan.Command)
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

// snapshotSimpleWizardPaths only needs to guard the brief path against a
// change while awaiting approval: the profile/rules/pointer-stub half is
// guarded instead by recomputing computeInitPlan fresh at apply time and
// comparing its digest to the one shown in the preview (applySimpleWizardPlan),
// the same ABA-safety init's own --apply already relies on.
func snapshotSimpleWizardPaths(plan simpleWizardPlan) ([]simpleWizardSnapshot, error) {
	snapshot, err := readSimpleWizardSnapshot(plan.BriefPlan.Path)
	if err != nil {
		return nil, err
	}
	return []simpleWizardSnapshot{snapshot}, nil
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
		fresh, err := computeInitPlan(plan.InitArgs)
		if err != nil {
			return err
		}
		if fresh.Digest != plan.InitDigest {
			return fmt.Errorf("wizard plan changed while awaiting approval: profile/rules/pointer-stub plan digest %s no longer matches the reviewed %s; review and rerun", fresh.Digest, plan.InitDigest)
		}
		if err := applyInitPlan(plan.InitArgs, fresh, false); err != nil {
			return err
		}
	}
	if plan.BriefPlan.Action == "create" {
		if err := ensureSimpleStartBrief(plan.BriefPlan.Path, []byte(plan.BriefPlan.Content)); err != nil {
			return err
		}
	}
	return nil
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
