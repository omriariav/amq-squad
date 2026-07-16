package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/role"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// orchestrateTmuxRun executes a tmux command. It is a package var so tests can
// stub it and assert the launch was invoked with the expected arguments,
// matching the injectable-runner pattern used elsewhere in this package
// (externalLeadWakeCommand, runAMQCommand).
var orchestrateTmuxRun = func(args ...string) error { return exec.Command("tmux", args...).Run() }

var (
	runStartUpWithVersion   = runUpWithVersion
	runStartGoalWithVersion = runGoalWithVersion

	runStartLeadReadyTimeout        = 45 * time.Second
	runStartLeadReadyInitialBackoff = 250 * time.Millisecond
	runStartLeadReadyMaxBackoff     = 2 * time.Second
	runStartLeadReadySleep          = time.Sleep
	runStartLeadReadyNow            = time.Now
	runStartLeadReadyCheck          = defaultRunStartLeadReadyCheck
)

type runStartGoalDeliveryOptions struct {
	Project string
	Profile string
	Session string
	Role    string
	Goal    string
	Version string
}

type runStartLeadReadiness struct {
	Ready  bool
	Detail string
}

func insideTmux() bool { return strings.TrimSpace(os.Getenv("TMUX")) != "" }

func validOrchestrateAgent(agent string) error {
	switch agent {
	case "claude", "codex":
		return nil
	default:
		return usageErrorf("--agent must be claude or codex, got %q", agent)
	}
}

// -----------------------------------------------------------------------------
// global: multi-run global / NOC orchestrator (poller, no wake)
// -----------------------------------------------------------------------------

func runGlobal(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad global - stand up a global / NOC orchestrator

Usage:
  amq-squad global start [--root DIR] [--agent claude|codex] [--name WINDOW] [--go]

A global orchestrator is a control-plane conversation that supervises MANY runs
across repos from a neutral root. It is a POLLER by design: it owns no single
mailbox, so there is nothing to wake it on. It drives each run by explicit
--project/--profile/--session and keeps the multi-run board (see the
amq-squad-orchestrator skill).

Preview by default (prints the plan and the poll/steer cheatsheet); pass --go to
open the tmux window and launch the agent.
`)
		if len(args) == 0 {
			return usageErrorf("global requires a subcommand (start)")
		}
		return nil
	}
	switch args[0] {
	case "start":
		return runGlobalStart(args[1:])
	default:
		return usageErrorf("unknown 'global' subcommand: %q. Try start.", args[0])
	}
}

func runGlobalStart(args []string) error {
	fs := flag.NewFlagSet("global start", flag.ContinueOnError)
	defaultRoot := ""
	if home, err := os.UserHomeDir(); err == nil {
		defaultRoot = filepath.Join(home, "Code")
	}
	root := fs.String("root", defaultRoot, "neutral root directory the supervisor runs from")
	agent := fs.String("agent", "claude", "agent binary to launch: claude or codex")
	name := fs.String("name", "global-orch", "tmux window name")
	model := fs.String("model", "", "model to pass to the agent (e.g. claude-opus-4-8, gpt-5.6-terra)")
	codexArgs := fs.String("codex-args", "", "extra args when --agent codex (e.g. reasoning effort); space-split")
	claudeArgs := fs.String("claude-args", "", "extra args when --agent claude; space-split")
	goFlag := fs.Bool("go", false, "actually open the window and launch the agent (default: preview only)")
	fs.Usage = func() { _ = runGlobal([]string{"-h"}) }
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	if err := validOrchestrateAgent(*agent); err != nil {
		return err
	}
	// Build the agent argv: binary, then model, then the matching per-binary
	// passthrough. Global start remains a single-agent surface, so effort rides
	// directly in --codex-args / --claude-args; the role-mapped --effort flag is
	// only meaningful for a multi-role project run.
	agentArgv := []string{*agent}
	if strings.TrimSpace(*model) != "" {
		agentArgv = append(agentArgv, "--model", strings.TrimSpace(*model))
	}
	extra := *claudeArgs
	if *agent == "codex" {
		extra = *codexArgs
	}
	if fields := strings.Fields(extra); len(fields) > 0 {
		agentArgv = append(agentArgv, fields...)
	}
	if strings.TrimSpace(*root) == "" {
		return usageErrorf("global start requires --root (could not infer a home directory)")
	}
	if info, err := os.Stat(*root); err != nil || !info.IsDir() {
		return usageErrorf("root directory does not exist: %s", *root)
	}

	fmt.Printf("global orchestrator (poller mode -- no wake by design)\n")
	fmt.Printf("  root:   %s\n", *root)
	fmt.Printf("  agent:  %s\n", *agent)
	fmt.Printf("  window: %s\n", *name)
	fmt.Printf("  launch: tmux new-window -c %s -n %s %s\n", *root, *name, strings.Join(agentArgv, " "))

	if !*goFlag {
		fmt.Print(`
PREVIEW only -- nothing launched. Re-run with --go to open the window.
`)
		printGlobalCheatsheet()
		return nil
	}

	if !insideTmux() {
		return usageErrorf("not inside tmux; global start --go must run from a tmux session (visible spawns require it)")
	}
	if _, err := exec.LookPath(*agent); err != nil {
		return usageErrorf("%s not found on PATH", *agent)
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return usageErrorf("tmux not found on PATH")
	}
	tmuxArgs := append([]string{"new-window", "-c", *root, "-n", *name}, agentArgv...)
	if err := orchestrateTmuxRun(tmuxArgs...); err != nil {
		return fmt.Errorf("tmux new-window failed: %w", err)
	}
	quietNotice("launched %s in tmux window %q at %s\n", *agent, *name, *root)
	printGlobalCheatsheet()
	return nil
}

func printGlobalCheatsheet() {
	fmt.Print(`
In the new window, invoke the amq-squad-orchestrator skill, then drive each run
by explicit namespace (never by cwd):

  amq-squad goal draft  --goal "..." --repo <owner/repo> --session <s> --profile <p> --lead <role> --skill-invocation
  amq-squad goal start  --project <repo> --profile <p> --session <s> --goal "..." --dry-run --json
  amq-squad goal start  --project <repo> --profile <p> --session <s> --goal "..." --yes --json

Poll / steer / approve:
  amq-squad monitor  --project <repo> --profile <p> --session <s> --once --json
  amq-squad status   --project <repo> --profile <p> --session <s> --json
  amq-squad next     --project <repo> --profile <p> --session <s> --json
  amq-squad operator answer   --project <repo> --profile <p> --session <s> --gate <topic> --to <lead> --approved --reason "..."

To drive ONE run wake-first instead of polling it, use 'amq-squad run start --project <repo>'.
`)
}

// -----------------------------------------------------------------------------
// run: create one orchestrated run in a project (managed spawn)
// -----------------------------------------------------------------------------

func runRunCmd(args []string, version string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad run - create one orchestrated run in a project

Usage:
  amq-squad run start -p PROJECT -s SESSION [--profile P] [--lead ROLE]
      [--roles "a,b,c"] [--binary "role=bin,..."] [--model "role=model,..."]
      [--effort "role=level,..."]
      [--operator-mode lead_pane|separate_terminal|noc|self_operator]
      [--self-operator-lead ROLE --self-operator-allow merge]
      [--operator-notifications]
      [--lead-mode builder|planner]
      [--codex-args "..."] [--claude-args "..."]
      [--visibility sibling-tabs|detached|current] [--external-lead]
      [--layout-preset lead-left|lead-top|even-grid|one-window-per-agent]
      [--launcher-pane close-after-start|keep]
      [--goal TEXT] [--goal-source SOURCE] [--goal-digest SHA256]
      [--seed-from REF] [--tool-profile "role=profile,..."]
      --launch-shape working-team-together|lead-only-staged
      [--staged-roles "role,..."]
      [--prepare-plan | --prepare | --readiness-json | --go]
      [--interactive]
      [--wizard-ui auto|tui|numbered] [--numbered|--accessible]

With no flags in an interactive terminal, or with --interactive explicitly,
run start opens the guided preview wizard. Non-TTY and CI invocations never
prompt. The wizard first renders a read-only preparation proposal, then uses
two separate default-No approvals: "Prepare coordination artifacts? [y/N]"
before any profile/brief/rules/pointer/manifest write, and "Launch now? [y/N]"
only after accepted-artifact readiness passes. The launch call carries the
accepted --launch-shape, --goal-source, and --goal-digest binding; it never
repairs or rewrites preparation artifacts.
Its first choice can instead delegate to canonical global start for a
Global/NOC poller. --interactive and --go are mutually exclusive.

Managed model: amq-squad spawns the whole team (incl. the lead); panes are
registered and wake-live automatically. This wraps the create sequence so the
--project/--profile/--session namespace is typed once:

    new team (if --roles) -> up --visibility <mode>

Visibility defaults to sibling-tabs: agents open as sibling tmux windows in the
current visible tmux session (requires a visible tmux pane when --go is used).
Pass --visibility detached for hidden workers in a separate tmux session. Choose
binary via --binary and model via --model. --effort normalizes role-specific
values into the existing per-member CodexArgs or ClaudeArgs fields. For an
existing profile it is launch-only and never rewrites stored member args; it
adds no persisted effort field or launch semantics.
Operator mode accepts lead_pane, separate_terminal, noc, or self_operator.
Fresh self_operator profiles require --self-operator-lead and the explicit
merge-only --self-operator-allow; no default is inferred and spawn remains
human-only.
--operator-notifications is an independent fresh-profile add-on. It configures
attention-only desktop delivery and never changes who may answer or approve a
gate. Existing profiles remain authoritative and are never rewritten.

The non-interactive contract is proposal -> explicitly approved preparation ->
readiness -> separately approved launch. Use --prepare-plan for the read-only
proposal, repeat the accepted command with --prepare to write artifacts without
launching, inspect --readiness-json, then use --go only with the exact accepted
launch shape and goal binding. Every mutation/launch gate defaults to No.

External-lead mode (--external-lead) binds the current tmux pane as the
configured lead, then spawns only the remaining workers. It never registers a
separate orchestrator handle or re-points lead state.
`)
		if len(args) == 0 {
			return usageErrorf("run requires a subcommand (start)")
		}
		return nil
	}
	switch args[0] {
	case "start":
		return runRunStart(args[1:], version)
	default:
		return usageErrorf("unknown 'run' subcommand: %q. Try start.", args[0])
	}
}

func runRunStart(args []string, version string) error {
	interactive, interactiveSpecified, args, err := runStartInteractiveTrigger(args)
	if err != nil {
		return err
	}
	if interactive && runStartHasHelpFlag(args) {
		return runRunCmd([]string{"--help"}, version)
	}
	if interactive && runStartHasGoFlag(args) {
		return usageErrorf("--interactive cannot be combined with --go; approve launch only at the wizard's final confirmation")
	}
	if interactive {
		if !runStartWizardEligible() {
			return usageErrorf("run start --interactive requires an interactive terminal and is disabled in CI; use the canonical flag form instead")
		}
		return runStartWizardRunner(args, version)
	}
	if len(args) == 0 && !interactiveSpecified && runStartWizardEligible() {
		return runStartWizardRunner(nil, version)
	}

	fs := flag.NewFlagSet("run start", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project / team-home directory (repo root)")
	sessionFlag := fs.String("session", "", "workstream session name")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	leadFlag := fs.String("lead", "", "lead role (default: cto when creating a roster; else inferred from the profile)")
	leadModeFlag := fs.String("lead-mode", "", "lead implementation posture when creating a roster: builder (default) or planner")
	rolesFlag := fs.String("roles", "", "create the roster first: comma-separated role ids")
	binaryFlag := fs.String("binary", "", "per-role binary assignments, e.g. \"fullstack=codex,qa=codex\"")
	modelFlag := fs.String("model", "", "per-role model overrides, e.g. \"cto=gpt-5.6-sol,fullstack=sonnet\"")
	effortFlag := fs.String("effort", "", "per-role effort, e.g. \"cto=high,qa=medium\" (launch-only for existing profiles; normalized into native member args)")
	toolProfileFlag := fs.String("tool-profile", "", "per-role tool policy assignments, e.g. \"cto=full,qa=browser\"")
	launchShapeFlag := fs.String("launch-shape", "", "accepted launch shape: working-team-together or lead-only-staged")
	stagedRolesFlag := fs.String("staged-roles", "", "comma-separated authored roles excluded from the initial profile")
	operatorModeFlag := fs.String("operator-mode", "", "operator interaction contract: lead_pane, separate_terminal, noc, or self_operator")
	selfOperatorLeadFlag := fs.String("self-operator-lead", "", "lead role delegated for exact-session self-operator policy")
	selfOperatorAllowFlag := fs.String("self-operator-allow", "", "explicit self-operator allowlist (v2.19: merge only)")
	operatorNotifications := fs.Bool("operator-notifications", false, "enable attention-only operator notifications for a newly created profile")
	codexArgsFlag := fs.String("codex-args", "", "extra args for every Codex member (e.g. reasoning effort)")
	claudeArgsFlag := fs.String("claude-args", "", "extra args for every Claude member")
	visibilityFlag := fs.String("visibility", visibilitySiblingTabs, "spawn topology: sibling-tabs (visible default), detached (hidden), or current")
	layoutPresetFlag := fs.String("layout-preset", "", "deterministic layout: lead-left, lead-top, even-grid, or one-window-per-agent")
	launcherPaneFlag := fs.String("launcher-pane", "", "launcher disposition after successful start: close-after-start or keep")
	goalFlag := fs.String("goal", "", "after spawn, deliver this goal to the lead")
	goalSourceFlag := fs.String("goal-source", "", "accepted goal binding source (wizard-generated)")
	goalDigestFlag := fs.String("goal-digest", "", "accepted goal binding digest (wizard-generated)")
	seedFlag := fs.String("seed-from", "", "seed the workstream brief from a reference (e.g. issue:96)")
	externalLead := fs.Bool("external-lead", false, "bind the current pane as the lead and spawn only remaining workers")
	prepareFlag := fs.Bool("prepare", false, "write only the explicitly approved coordination artifacts; never launch")
	preparePlanFlag := fs.Bool("prepare-plan", false, "print the canonical read-only coordination-artifact preparation proposal and exit")
	readinessJSON := fs.Bool("readiness-json", false, "print machine-readable accepted-artifact readiness and exit")
	goFlag := fs.Bool("go", false, "create for real (default: preview only)")
	fs.Usage = func() { _ = runRunCmd([]string{"-h"}, version) }
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	if *prepareFlag && *goFlag {
		return usageErrorf("--prepare and --go are separate approvals and cannot be combined")
	}
	if *preparePlanFlag && (*prepareFlag || *goFlag || *readinessJSON) {
		return usageErrorf("--prepare-plan is a read-only proposal and cannot be combined with --prepare, --go, or --readiness-json")
	}
	project := strings.TrimSpace(*projectFlag)
	session := strings.TrimSpace(*sessionFlag)
	preflight := runStartPreflight(runStartPreflightInput{
		Project:                  project,
		Profile:                  *profileFlag,
		ProfileExplicit:          flagWasSet(fs, "profile") || flagWasSet(fs, "P"),
		Session:                  session,
		Roles:                    *rolesFlag,
		Binary:                   *binaryFlag,
		Visibility:               *visibilityFlag,
		LeadMode:                 *leadModeFlag,
		Lead:                     *leadFlag,
		LeadModeSet:              flagWasSet(fs, "lead-mode"),
		Effort:                   *effortFlag,
		EffortSet:                flagWasSet(fs, "effort"),
		OperatorMode:             *operatorModeFlag,
		OperatorModeSet:          flagWasSet(fs, "operator-mode"),
		SelfOperatorLead:         *selfOperatorLeadFlag,
		SelfOperatorAllow:        *selfOperatorAllowFlag,
		SelfOperatorPolicySet:    flagWasSet(fs, "self-operator-lead") || flagWasSet(fs, "self-operator-allow"),
		OperatorNotifications:    *operatorNotifications,
		OperatorNotificationsSet: flagWasSet(fs, "operator-notifications"),
		LayoutPreset:             *layoutPresetFlag,
		LayoutPresetSet:          flagWasSet(fs, "layout-preset"),
		LauncherPane:             *launcherPaneFlag,
		LauncherPaneSet:          flagWasSet(fs, "launcher-pane"),
		VisibilitySet:            flagWasSet(fs, "visibility"),
		ExternalLead:             *externalLead,
	})
	if err := preflight.Err(); err != nil {
		return err
	}
	visibility := preflight.Visibility
	profile := preflight.Profile
	teamPresent := preflight.TeamPresent
	rolesText := strings.TrimSpace(*rolesFlag)
	freshRoster := preflight.FreshRoster
	leadMode := preflight.LeadMode
	if *goFlag {
		requestedShape := strings.TrimSpace(*launchShapeFlag)
		if requestedShape != runwizard.LaunchShapeWorkingTeamTogether && requestedShape != runwizard.LaunchShapeLeadOnlyStaged {
			return usageErrorf("live project launch requires explicit --launch-shape %s or %s; prepare or migrate the exact roster before launch", runwizard.LaunchShapeWorkingTeamTogether, runwizard.LaunchShapeLeadOnlyStaged)
		}
	}
	layoutSelection, err := resolveRunStartLayout(runStartLayoutInput{
		Visibility: preflight.Visibility, VisibilitySet: true,
		Preset: preflight.LayoutPreset, PresetSet: preflight.LayoutPreset != "",
		LauncherPane: preflight.LauncherPane, LauncherPaneSet: preflight.LauncherPane != "",
		ExternalLead: *externalLead,
	})
	if err != nil {
		return err
	}
	runContext := acceptedRunContext{Version: version, Topology: acceptedTopology(layoutSelection, *externalLead)}
	var liveGoalBinding acceptedGoalBinding
	if *goFlag {
		result := calculateRunReadinessWithContext(project, profile, session, runContext)
		printRunReadiness(result)
		if !result.Ready {
			return fmt.Errorf("launch blocked: artifact readiness failed for %s", result.Namespace)
		}
		if result.LaunchShape != strings.TrimSpace(*launchShapeFlag) {
			return fmt.Errorf("launch blocked: accepted launch shape %q differs from requested %q", result.LaunchShape, strings.TrimSpace(*launchShapeFlag))
		}
		liveGoalBinding, err = preparedRunLiveGoalBinding(project, profile, session, *goalFlag, *goalSourceFlag, *goalDigestFlag)
		if err != nil {
			return fmt.Errorf("launch blocked: accepted live goal binding mismatch: %w", err)
		}
		if err := validatePreparedLaunchBootstrapInputs(project, profile, session, runContext, *modelFlag, *effortFlag, *codexArgsFlag, *claudeArgsFlag); err != nil {
			return fmt.Errorf("launch blocked: accepted bootstrap input mismatch: %w", err)
		}
	}

	// Lead resolution: when creating a fresh roster, default the lead to cto.
	// For an existing team, leave --role unset so `goal start` infers the
	// configured lead from the profile instead of assuming cto (which may not
	// be that team's lead).
	explicitLead := strings.TrimSpace(*leadFlag)
	leadForNewTeam := explicitLead
	if leadForNewTeam == "" {
		leadForNewTeam = "cto"
	}
	externalLeadRole := ""
	if *externalLead {
		externalLeadRole, err = resolveRunStartExternalLead(project, profile, explicitLead, freshRoster, leadForNewTeam)
		if err != nil {
			return err
		}
		preflightTeam, err := runStartExternalLeadPreflightTeam(project, profile, session, rolesText, freshRoster, externalLeadRole)
		if err != nil {
			return err
		}
		if err := validateRunStartExternalLeadPreconditions(project, profile, session, externalLeadRole, preflightTeam); err != nil {
			return err
		}
	}

	var preparationProposal runPreparationProposal
	if *preparePlanFlag || *prepareFlag {
		var proposalTeam team.Team
		if teamPresent {
			existing, readErr := team.ReadProfile(project, profile)
			if readErr != nil {
				return readErr
			}
			proposalTeam = existing
			if explicitLead != "" && explicitLead != strings.TrimSpace(existing.Lead) {
				return usageErrorf("preparation lead %q differs from existing profile lead %q; set the profile lead with `amq-squad team lead set %s --project %s --profile %s`, then rerun preparation for --session %s",
					explicitLead, existing.Lead, shellQuote(explicitLead), shellQuote(project), shellQuote(profile), shellQuote(session))
			}
		} else {
			proposalTeam, err = projectedRunPreparationTeam(project, session, leadForNewTeam, leadMode, rolesText, *binaryFlag, *modelFlag, *effortFlag)
			if err != nil {
				return err
			}
		}
		proposal, proposalErr := buildRunPreparationProposal(runPreparationProposalInput{
			Project:         project,
			Profile:         profile,
			Session:         session,
			LaunchShape:     *launchShapeFlag,
			StagedRoles:     *stagedRolesFlag,
			ToolProfile:     *toolProfileFlag,
			Goal:            *goalFlag,
			GoalSource:      *goalSourceFlag,
			GoalDigest:      *goalDigestFlag,
			Seed:            *seedFlag,
			Team:            proposalTeam,
			ExistingProfile: teamPresent,
			Context:         runContext,
		})
		if proposalErr != nil {
			return proposalErr
		}
		preparationProposal = proposal
		runContext.PointerPlans = append([]rules.SyncPlan(nil), proposal.PointerPlans...)
		printRunPreparationProposal(proposal)
		if *preparePlanFlag {
			return nil
		}
		runPreparationAfterProposal()
	}

	// Build the create commands as argument slices we can run in-process. This
	// keeps one tested implementation (no shell-out, structured errors) and lets
	// the CLI flag layer own things the scripts got wrong (e.g. --binary is a
	// single role=bin,... string here, matching `team.go`, not repeatable).
	var newTeamArgs []string
	if rolesText != "" {
		newTeamArgs = []string{"team", "--project", project}
		if strings.TrimSpace(*profileFlag) != "" {
			newTeamArgs = append(newTeamArgs, "--profile", *profileFlag)
		}
		// Pin the roster to this run's session so the following `up <session>`
		// finds its members; without --session the members default to another
		// workstream and `up` refuses them.
		newTeamArgs = append(newTeamArgs, "--session", session, "--roles", *rolesFlag, "--orchestrated", "--lead", leadForNewTeam)
		if flagWasSet(fs, "lead-mode") {
			newTeamArgs = append(newTeamArgs, "--lead-mode", leadMode)
		}
		if strings.TrimSpace(*binaryFlag) != "" {
			newTeamArgs = append(newTeamArgs, "--binary", *binaryFlag)
		}
		if strings.TrimSpace(*effortFlag) != "" {
			newTeamArgs = append(newTeamArgs, "--effort", *effortFlag)
		}
		// Non-full profiles require generated binary-native materialization.
		// Create the roster at the backward-compatible full baseline first;
		// preparation applies the accepted profiles before readiness/launch.
		if flagWasSet(fs, "operator-mode") {
			newTeamArgs = append(newTeamArgs, "--operator-mode", strings.TrimSpace(*operatorModeFlag))
		}
		if flagWasSet(fs, "self-operator-lead") {
			newTeamArgs = append(newTeamArgs, "--self-operator-lead", strings.TrimSpace(*selfOperatorLeadFlag))
		}
		if flagWasSet(fs, "self-operator-allow") {
			newTeamArgs = append(newTeamArgs, "--self-operator-allow", strings.TrimSpace(*selfOperatorAllowFlag))
		}
		if *operatorNotifications {
			newTeamArgs = append(newTeamArgs, "--operator-notifications")
		}
		newTeamArgs = appendPassthroughArgs(newTeamArgs, *modelFlag, *codexArgsFlag, *claudeArgsFlag)
	}
	upArgs := []string{session, "--project", project}
	if layoutSelection.Preset != "" {
		upArgs = append(upArgs, "--terminal", "tmux", "--target", layoutSelection.Target, "--layout", layoutSelection.SpawnLayout)
	} else {
		upArgs = append(upArgs, "--visibility", visibility)
	}
	if strings.TrimSpace(*profileFlag) != "" {
		upArgs = append(upArgs, "--profile", *profileFlag)
	}
	if strings.TrimSpace(*seedFlag) != "" {
		upArgs = append(upArgs, "--seed-from", *seedFlag)
	}
	// A fresh profile already persisted these values through new team above.
	// Forwarding them to up as a second launch layer duplicated native argv.
	upArgs = appendExistingTeamPassthroughArgs(upArgs, teamPresent, *modelFlag, *codexArgsFlag, *claudeArgsFlag)
	if teamPresent && strings.TrimSpace(*effortFlag) != "" {
		upArgs = append(upArgs, "--effort", *effortFlag)
	}

	if *readinessJSON {
		result := calculateRunReadinessWithContext(project, profile, session, runContext)
		if err := writeJSONEnvelope(os.Stdout, "run_readiness", result); err != nil {
			return err
		}
		if !result.Ready {
			return fmt.Errorf("artifact readiness failed for %s", result.Namespace)
		}
		return nil
	}

	leadModeDisplay := leadMode
	if !flagWasSet(fs, "lead-mode") && teamPresent {
		if existing, err := team.ReadProfile(project, *profileFlag); err == nil {
			leadModeDisplay = team.EffectiveLeadMode(existing)
		}
	}

	// freshRoster is true only when --roles is given AND the profile does not
	// already exist, i.e. this invocation actually creates the roster. When the
	// profile already exists, --roles is a no-op and we must NOT assume the lead
	// is cto: goal delivery infers the profile's configured lead instead.
	leadDisplay := explicitLead
	if explicitLead == "" {
		if freshRoster {
			leadDisplay = leadForNewTeam
		} else {
			leadDisplay = "(inferred from profile)"
		}
	}
	if *externalLead {
		leadDisplay = externalLeadRole
	}
	upStep := 1
	if freshRoster {
		upStep = 2
	}
	modelLabel := "managed model"
	if *externalLead {
		modelLabel = "external lead"
	}
	fmt.Printf("orchestrated run (%s)\n", modelLabel)
	fmt.Printf("  project: %s\n", project)
	fmt.Printf("  profile: %s\n", profileOrDefault(*profileFlag))
	fmt.Printf("  session: %s\n", session)
	fmt.Printf("  lead:    %s\n", leadDisplay)
	fmt.Printf("  lead-mode: %s\n", leadModeDisplay)
	if strings.TrimSpace(*launchShapeFlag) != "" {
		initialRoster := sortedUniqueRoles(rolesText)
		if teamPresent {
			existing, readErr := team.ReadProfile(project, profile)
			if readErr != nil {
				return readErr
			}
			active, _ := filterMembersBySession(existing.Members, session)
			initialRoster = teamMemberRoles(active)
		}
		fmt.Printf("  launch-shape: %s\n", strings.TrimSpace(*launchShapeFlag))
		fmt.Printf("  initial launch: %d members - %s\n", len(initialRoster), displayRoleList(initialRoster))
		fmt.Printf("  staged later: %d roles - %s\n", len(sortedUniqueRoles(*stagedRolesFlag)), displayRoleList(sortedUniqueRoles(*stagedRolesFlag)))
	}
	if freshRoster {
		leadModeSuffix := ""
		if flagWasSet(fs, "lead-mode") {
			leadModeSuffix = " --lead-mode " + leadMode
		}
		fmt.Printf("  step 1:  amq-squad new team --roles %s --orchestrated --lead %s%s\n", *rolesFlag, leadForNewTeam, leadModeSuffix)
	} else if len(newTeamArgs) > 0 {
		fmt.Printf("  note:    profile %s already exists; --roles ignored, using the existing roster\n", profileOrDefault(*profileFlag))
	}
	if *externalLead {
		fmt.Printf("  step %d:  bind current pane as external lead %s\n", upStep, externalLeadRole)
		upStep++
	}
	fmt.Printf("  step %d:  amq-squad up %s --visibility %s\n", upStep, session, visibility)
	if layoutSelection.Preset != "" {
		fmt.Printf("  layout:  %s -> target=%s layout=%s\n", layoutSelection.Preset, layoutSelection.Target, layoutSelection.SpawnLayout)
	}
	if layoutSelection.LauncherPane != "" {
		fmt.Printf("  launcher-pane: %s\n", layoutSelection.LauncherPane)
	}
	if visibility == visibilityDetached {
		fmt.Printf("  (hidden: agents run in a detached tmux session; attach via the `attach_control` action from `status --json`, or `amq-squad focus`, when you want eyes on them)\n")
	}
	if !*goFlag && (visibility == visibilitySiblingTabs || visibility == visibilityCurrent) && !insideTmux() {
		fmt.Printf("  note:    --go with --visibility %s requires a visible tmux pane; run inside tmux or pass --visibility detached.\n", visibility)
	}
	if strings.TrimSpace(*goalFlag) != "" {
		previewRole := leadDisplay
		if strings.HasPrefix(previewRole, "(") {
			resolved, err := resolveRunStartGoalLead(project, profile, explicitLead, freshRoster, leadForNewTeam)
			if err != nil {
				return err
			}
			previewRole = resolved
		}
		previewCmd := runStartGoalRetryCommand(runStartGoalDeliveryOptions{
			Project: project,
			Profile: profile,
			Session: session,
			Role:    previewRole,
			Goal:    *goalFlag,
		})
		fmt.Printf("  step %d:  %s\n", upStep+1, previewCmd)
	}

	if *prepareFlag {
		if err := revalidateRunPreparationPointerPlans(runContext.PointerPlans); err != nil {
			return fmt.Errorf("revalidate accepted pointer plan before preparation writes: %w", err)
		}
		result, err := executeRunPreparationTransaction(preparationProposal.MutationPaths, preparedRunPath(project, profile, session), func() (runReadinessResult, error) {
			if freshRoster {
				quietNotice("preparing accepted roster; no panes will launch...\n")
				if err := runNew(newTeamArgs); err != nil {
					return runReadinessResult{}, err
				}
			} else if len(newTeamArgs) > 0 {
				quietNotice("profile %q already exists; accepted preparation will not rewrite its roster\n", profileOrDefault(*profileFlag))
			}
			if err := applyRunStartToolProfiles(project, profile, *toolProfileFlag); err != nil {
				return runReadinessResult{}, err
			}
			return prepareRunArtifacts(project, profile, session, strings.TrimSpace(*launchShapeFlag), *stagedRolesFlag, *goalFlag, *goalSourceFlag, *goalDigestFlag, *seedFlag, runContext)
		})
		printRunReadiness(result)
		if err != nil {
			return err
		}
		fmt.Println("Preparation complete. No panes or workers were launched.")
		return nil
	}

	if !*goFlag {
		printLayoutFinalizationDryRun(layoutSelection, os.Getenv("TMUX_PANE"))
		if *externalLead {
			return runStartExternalLeadPreview(runStartExternalLeadPreviewOptions{
				NewTeamArgs: newTeamArgs,
				Project:     project,
				Profile:     profile,
				Session:     session,
				Lead:        externalLeadRole,
				FreshRoster: freshRoster,
				TeamPresent: teamPresent,
				Visibility:  visibility,
				Layout:      layoutSelection,
				Model:       *modelFlag,
				Effort:      *effortFlag,
				CodexArgs:   *codexArgsFlag,
				ClaudeArgs:  *claudeArgsFlag,
				Version:     version,
			})
		}
		return runStartPreview(newTeamArgs, upArgs, freshRoster, teamPresent, version)
	}

	if (visibility == visibilitySiblingTabs || visibility == visibilityCurrent) && !insideTmux() {
		return usageErrorf("not inside tmux; --visibility %s requires a visible tmux pane. Run inside tmux, or pass --visibility detached to spawn hidden.", visibility)
	}
	finalizationContext, err := prepareLayoutFinalization(layoutSelection)
	if err != nil {
		return err
	}

	if *externalLead {
		var externalSeedContent string
		if strings.TrimSpace(*seedFlag) != "" {
			body, err := resolveSeed(*seedFlag)
			if err != nil {
				return err
			}
			externalSeedContent = buildSeedBrief(*seedFlag, body, seedNow())
		}
		if freshRoster {
			quietNotice("creating roster...\n")
			if err := runNew(newTeamArgs); err != nil {
				return err
			}
		} else if len(newTeamArgs) > 0 {
			quietNotice("profile %q already exists; skipping new team (using existing roster)\n", profileOrDefault(*profileFlag))
		}
		if err := validateRunStartExternalLeadWorkerLaunch(project, layoutSelection, session, profile, externalLeadRole, *modelFlag, *effortFlag, *codexArgsFlag, *claudeArgsFlag); err != nil {
			return err
		}
		quietNotice("binding current pane as external lead %s...\n", externalLeadRole)
		if err := runStartRegisterExternalLead(project, profile, session, externalLeadRole); err != nil {
			return err
		}
		opts, err := runStartTeamLaunchOptions(layoutSelection, session, profile, externalSeedContent, false, false, externalLeadRole, true, *modelFlag, *effortFlag, *codexArgsFlag, *claudeArgsFlag)
		if err != nil {
			return err
		}
		var launchResult teamLaunchResult
		opts.ResultSink = func(result teamLaunchResult) { launchResult = result }
		quietNotice("spawning remaining workers (--visibility %s)...\n", visibility)
		if err := runInProject(project, func() error { return executeTeamLaunch(opts, true, false) }); err != nil {
			return err
		}
		goalOpts := runStartGoalDeliveryOptions{
			Project: project,
			Profile: profile,
			Session: session,
			Role:    externalLeadRole,
			Goal:    liveGoalBinding.Text,
			Version: version,
		}
		quietNotice("waiting for lead readiness before goal delivery...\n")
		if err := deliverRunStartGoalWhenReady(goalOpts); err != nil {
			return err
		}
		quietNotice("done. Current pane is the lead; drive remaining workers with dispatch/monitor/collect.\n")
		if layoutSelection.requestedFinalization() {
			plan, planErr := buildLayoutFinalizationPlan(project, profile, session, externalLeadRole, layoutSelection, finalizationContext, launchResult, true)
			if planErr != nil {
				warnLayoutFinalization(project, profile, session, planErr)
			} else if scheduleErr := scheduleLayoutFinalization(plan); scheduleErr != nil {
				warnLayoutFinalization(project, profile, session, scheduleErr)
			}
		}
		return nil
	}

	// 1) roster
	if freshRoster {
		quietNotice("creating roster...\n")
		if err := runNew(newTeamArgs); err != nil {
			return err
		}
		if err := applyRunStartToolProfiles(project, profile, *toolProfileFlag); err != nil {
			return err
		}
	} else if len(newTeamArgs) > 0 {
		quietNotice("profile %q already exists; skipping new team (using existing roster)\n", profileOrDefault(*profileFlag))
	}
	// 2) spawn
	quietNotice("spawning team (--visibility %s)...\n", visibility)
	var launchResult teamLaunchResult
	if layoutSelection.requestedFinalization() {
		var seedContent string
		if strings.TrimSpace(*seedFlag) != "" {
			body, seedErr := resolveSeed(*seedFlag)
			if seedErr != nil {
				return seedErr
			}
			seedContent = buildSeedBrief(*seedFlag, body, seedNow())
		}
		launchOpts, launchErr := runStartTeamLaunchOptions(layoutSelection, session, profile, seedContent, false, false, "", false, *modelFlag, *effortFlag, *codexArgsFlag, *claudeArgsFlag)
		if launchErr != nil {
			return launchErr
		}
		launchOpts.WarnStubBrief = strings.TrimSpace(*seedFlag) == ""
		// Preserve canonical `up`'s refuse-existing/TOCTOU guard on the direct
		// result-returning launch path used by layout finalization.
		launchOpts.Fresh = true
		launchOpts.ResultSink = func(result teamLaunchResult) { launchResult = result }
		if launchErr := runInProject(project, func() error { return executeTeamLaunch(launchOpts, true, false) }); launchErr != nil {
			return launchErr
		}
	} else if err := runStartUpWithVersion(upArgs, version); err != nil {
		return err
	}
	// 3) accepted goal delivery. Resolve the role before waiting so the
	// fallback command is exact and ready to paste if the cold spawn never
	// reaches a deliverable pane.
	leadRole, err := resolveRunStartGoalLead(project, profile, explicitLead, freshRoster, leadForNewTeam)
	if err != nil {
		return err
	}
	opts := runStartGoalDeliveryOptions{
		Project: project,
		Profile: profile,
		Session: session,
		Role:    leadRole,
		Goal:    liveGoalBinding.Text,
		Version: version,
	}
	quietNotice("waiting for lead readiness before goal delivery...\n")
	if err := deliverRunStartGoalWhenReady(opts); err != nil {
		return err
	}
	quietNotice("done. Attach to the lead window and drive with dispatch/monitor/collect.\n")
	if layoutSelection.requestedFinalization() {
		leadRole, leadErr := resolveRunStartGoalLead(project, profile, explicitLead, freshRoster, leadForNewTeam)
		if leadErr != nil {
			warnLayoutFinalization(project, profile, session, leadErr)
		} else {
			plan, planErr := buildLayoutFinalizationPlan(project, profile, session, leadRole, layoutSelection, finalizationContext, launchResult, false)
			if planErr != nil {
				warnLayoutFinalization(project, profile, session, planErr)
			} else if scheduleErr := scheduleLayoutFinalization(plan); scheduleErr != nil {
				warnLayoutFinalization(project, profile, session, scheduleErr)
			}
		}
	}
	return nil
}

func appendExistingTeamPassthroughArgs(dst []string, teamPresent bool, model, codexArgs, claudeArgs string) []string {
	if !teamPresent {
		return dst
	}
	return appendPassthroughArgs(dst, model, codexArgs, claudeArgs)
}

// runStartPreview runs the read-only dry-run validation and reports honestly.
// It never claims success over a check it could not run: on a fresh project the
// roster does not exist yet, so `up --dry-run` cannot validate it and the
// preview says so instead of printing a misleading OK.
func runStartPreview(newTeamArgs, upArgs []string, freshRoster, teamPresent bool, version string) error {
	fmt.Print("\nPREVIEW -- running read-only --dry-run validation; nothing is created.\n")
	if freshRoster {
		if err := runNew(append(append([]string{}, newTeamArgs...), "--dry-run")); err != nil {
			return fmt.Errorf("roster dry-run failed: %w", err)
		}
	}
	if teamPresent {
		// Strip --seed-from for the validation dry-run: `up --dry-run --seed-from`
		// returns early with only a brief-candidate preview and skips roster/
		// session validation, which would let preview print "OK" for a spawn that
		// `--go` cannot actually perform. The seed is written at --go regardless.
		validateArgs, seeded := stripFlagValue(upArgs, "--seed-from")
		if err := runStartUpWithVersion(append(validateArgs, "--dry-run"), version); err != nil {
			return fmt.Errorf("spawn dry-run failed: %w", err)
		}
		fmt.Print("\nPreview OK. This validates only the current spawn plan; it does not approve preparation or launch.\n")
		if seeded {
			fmt.Print("(the --seed-from brief is written only by an explicitly approved --prepare call.)\n")
		}
		printRunStartPreparationNext(false)
		return nil
	}
	fmt.Print("\nRoster plan validated. Spawn (up) validation is deferred: the team does\n" +
		"not exist yet, so `up --dry-run` cannot check the roster in preview.\n")
	printRunStartPreparationNext(false)
	return nil
}

func printRunStartPreparationNext(externalLead bool) {
	external := ""
	if externalLead {
		external = " Keep --external-lead on every stage."
	}
	fmt.Printf("Next: re-run the same namespace, roster, topology, and goal with an explicit --launch-shape and --prepare-plan.%s\n", external)
	fmt.Print("After reviewing the proposal, replace --prepare-plan with --prepare for the separate default-No preparation approval.\n")
	fmt.Print("Then inspect --readiness-json. Launch only after a separate default-No approval, using --go with the accepted --launch-shape, --goal-source, and --goal-digest.\n")
}

type runStartExternalLeadPreviewOptions struct {
	NewTeamArgs []string
	Project     string
	Profile     string
	Session     string
	Lead        string
	FreshRoster bool
	TeamPresent bool
	Visibility  string
	Layout      runStartLayoutSelection
	Model       string
	Effort      string
	CodexArgs   string
	ClaudeArgs  string
	Version     string
}

func runStartExternalLeadPreview(opts runStartExternalLeadPreviewOptions) error {
	fmt.Print("\nPREVIEW -- validating external-lead binding and worker launch; nothing is created.\n")
	if opts.FreshRoster {
		if err := runNew(append(append([]string{}, opts.NewTeamArgs...), "--dry-run")); err != nil {
			return fmt.Errorf("roster dry-run failed: %w", err)
		}
		fmt.Print("\nExternal-lead adoption preconditions validated. Spawn validation is deferred: the team does\n" +
			"not exist yet, so the worker-only launch plan cannot be checked without writing the roster.\n")
		printRunStartPreparationNext(true)
		return nil
	}
	if opts.TeamPresent {
		if err := validateRunStartExternalLeadWorkerLaunch(opts.Project, opts.Layout, opts.Session, opts.Profile, opts.Lead, opts.Model, opts.Effort, opts.CodexArgs, opts.ClaudeArgs); err != nil {
			return fmt.Errorf("worker spawn dry-run failed: %w", err)
		}
		fmt.Print("\nPreview OK. This validates only external-lead adoption and the worker spawn plan.\n")
		printRunStartPreparationNext(true)
		return nil
	}
	return usageErrorf("no team profile %q in %s and no --roles given; pass --roles to create one or create the team first", opts.Profile, opts.Project)
}

func runStartTeamLaunchOptions(layout runStartLayoutSelection, session, profile, seedContent string, seedForce, dryRun bool, externalLeadRole string, allowLeadOnly bool, modelRaw, effortRaw, codexArgsRaw, claudeArgsRaw string) (teamLaunchOptions, error) {
	modelOverrides, err := parseKV(modelRaw)
	if err != nil {
		return teamLaunchOptions{}, fmt.Errorf("parse --model: %w", err)
	}
	modelOverrides = lowercaseKeys(modelOverrides)
	effortOverrides, err := parseEffortOverrides(effortRaw)
	if err != nil {
		return teamLaunchOptions{}, err
	}
	binaryArgs, err := parseBinaryArgFlags(codexArgsRaw, claudeArgsRaw)
	if err != nil {
		return teamLaunchOptions{}, err
	}
	opts := teamLaunchOptions{
		Terminal:        "tmux",
		Target:          "current-window",
		Layout:          "vertical",
		Workstream:      session,
		Stagger:         750 * time.Millisecond,
		SquadBin:        teamSquadBin(),
		BinaryArgs:      binaryArgs,
		ModelOverrides:  modelOverrides,
		EffortOverrides: effortOverrides,
	}
	if err := applyLaunchVisibility(&opts, layout.Visibility, false, false, false, true); err != nil {
		return teamLaunchOptions{}, err
	}
	if layout.Preset != "" {
		opts.Target = layout.Target
		opts.Layout = layout.SpawnLayout
	}
	opts.DryRun = dryRun
	opts.Fresh = false
	opts.Workstream = session
	opts.Profile = profile
	opts.SeedBriefContent = seedContent
	opts.SeedBriefForce = seedForce
	opts.WarnStubBrief = seedContent == "" && !dryRun
	opts.ExternalLeadRole = externalLeadRole
	opts.AllowNoMembersAfterExternalLead = allowLeadOnly
	return opts, nil
}

func resolveRunStartExternalLead(project, profile, explicitLead string, freshRoster bool, leadForNewTeam string) (string, error) {
	if freshRoster {
		lead := strings.TrimSpace(explicitLead)
		if lead == "" {
			lead = strings.TrimSpace(leadForNewTeam)
		}
		if lead == "" {
			return "", usageErrorf("--external-lead requires a lead role")
		}
		return strings.ToLower(lead), nil
	}
	t, err := team.ReadProfile(project, profile)
	if err != nil {
		return "", fmt.Errorf("read team profile %q: %w", profile, err)
	}
	lead := strings.TrimSpace(t.Lead)
	if lead == "" && len(t.Members) == 1 {
		lead = strings.TrimSpace(t.Members[0].Role)
	}
	if lead == "" {
		return "", usageErrorf("--external-lead cannot infer lead role from profile %q; run `amq-squad team lead set <role>` first or pass --lead matching the configured lead", profile)
	}
	lead = strings.ToLower(lead)
	if explicit := strings.ToLower(strings.TrimSpace(explicitLead)); explicit != "" && explicit != lead {
		return "", usageErrorf("--external-lead cannot change existing profile lead from %q to %q; run `amq-squad team lead set %s --project %s --profile %s` first", lead, explicit, shellQuote(explicit), shellQuote(project), shellQuote(profile))
	}
	return lead, nil
}

func runStartExternalLeadPreflightTeam(project, profile, session, rolesText string, freshRoster bool, lead string) (team.Team, error) {
	if !freshRoster {
		t, err := team.ReadProfile(project, profile)
		if err != nil {
			return team.Team{}, fmt.Errorf("read team profile %q: %w", profile, err)
		}
		return t, nil
	}
	if strings.TrimSpace(rolesText) == "" {
		return team.Team{}, usageErrorf("no team profile %q in %s and no --roles given; pass --roles to create one or create the team first", profile, project)
	}
	customDefs := map[string]role.Definition{}
	if err := discoverStagedCustomRoleDefs(project, customDefs); err != nil {
		return team.Team{}, err
	}
	picked, err := resolveTeamSelection(rolesText, customDefs)
	if err != nil {
		return team.Team{}, err
	}
	seen := map[string]bool{}
	var members []team.Member
	for _, role := range picked {
		role = strings.ToLower(strings.TrimSpace(role))
		if role == "" || seen[role] {
			continue
		}
		seen[role] = true
		members = append(members, team.Member{Role: role, Handle: role, Session: session})
	}
	if lead != "" && !seen[lead] {
		return team.Team{}, usageErrorf("run start --external-lead lead %q is not included in --roles %q; include the lead role or change --lead", lead, rolesText)
	}
	return team.Team{
		Project:      project,
		Orchestrated: true,
		Lead:         lead,
		Members:      members,
	}, nil
}

func validateRunStartExternalLeadPreconditions(project, profile, session, lead string, t team.Team) error {
	id, err := currentPaneIdentity()
	if err != nil {
		return err
	}
	if id == nil || strings.TrimSpace(id.PaneID) == "" {
		return usageErrorf("run start --external-lead requires the current lead pane to be a tmux pane (TMUX/TMUX_PANE unset)")
	}
	if !currentWorkingDirMatches(project) {
		return usageErrorf("run start --external-lead must be executed from the lead member project root %s; current working directory does not match --project", project)
	}
	if _, ok := memberByRole(t, lead); !ok {
		return usageErrorf("run start --external-lead lead %q is not a member of the target roster", lead)
	}
	if err := authorizeRunStartExternalLeadAdoption(project, profile, session, lead, t, id.PaneID); err != nil {
		return err
	}
	exists, root, err := teamWorkstreamExistsOrRestorable(t, profile, session)
	if err != nil {
		return err
	}
	if exists {
		return existingSessionRefusal(session, root)
	}
	return nil
}

func authorizeRunStartExternalLeadAdoption(project, profile, session, lead string, t team.Team, paneID string) error {
	member, ok := memberByRole(t, lead)
	if !ok {
		return usageErrorf("run start --external-lead lead %q is not a member of the target roster", lead)
	}
	cwd := member.EffectiveCWD(t.Project)
	if strings.TrimSpace(cwd) == "" {
		cwd = project
	}
	handle := memberHandle(member)
	env, err := resolveAMQEnvForTeamProfile(cwd, profile, session, handle)
	if err != nil {
		return fmt.Errorf("resolve amq env: %w", err)
	}
	if env.Me != "" {
		handle = env.Me
	}
	root := absoluteAMQRoot(cwd, env.Root)
	agentDir := filepath.Join(root, "agents", handle)
	existingRec, existingRecErr := launch.Read(agentDir)
	_, err = authorizeLeadRegister(leadRegisterAuthInput{
		Team:              t,
		Member:            member,
		Role:              lead,
		Handle:            handle,
		Profile:           profile,
		Workstream:        env.SessionName,
		Root:              root,
		CWD:               cwd,
		PaneID:            paneID,
		TargetMode:        leadRegisterTargetMode(t, lead),
		ExistingRecord:    existingRec,
		ExistingRecordErr: existingRecErr,
		AdoptProjectLead:  true,
	})
	return err
}

func validateRunStartExternalLeadWorkerLaunch(project string, layout runStartLayoutSelection, session, profile, lead, modelRaw, effortRaw, codexArgsRaw, claudeArgsRaw string) error {
	launchOpts, err := runStartTeamLaunchOptions(layout, session, profile, "", false, true, lead, true, modelRaw, effortRaw, codexArgsRaw, claudeArgsRaw)
	if err != nil {
		return err
	}
	return runInProject(project, func() error {
		return executeTeamLaunch(launchOpts, true, false)
	})
}

func runStartRegisterExternalLead(project, profile, session, lead string) error {
	args := []string{
		"register",
		"--project", project,
		"--profile", profile,
		"--session", session,
		"--role", lead,
		"--adopt-project-lead",
	}
	return runLead(args)
}

func teamExistsForProfile(project, profile string) bool {
	if strings.TrimSpace(profile) == "" {
		return team.Exists(project)
	}
	return team.ExistsProfile(project, profile)
}

func profileOrDefault(profile string) string {
	if strings.TrimSpace(profile) == "" {
		return "default"
	}
	return profile
}

func pinnedSessionList(members []team.Member) []string {
	seen := make(map[string]bool)
	var out []string
	for _, m := range members {
		session := strings.TrimSpace(m.Session)
		if session == "" || seen[session] {
			continue
		}
		seen[session] = true
		out = append(out, session)
	}
	return out
}

func runStartProfileFixArg(profile string) string {
	if strings.TrimSpace(profile) == "" {
		return ""
	}
	return " --profile " + shellQuote(profile)
}

func runStartRolesFixArg(roles string) string {
	if strings.TrimSpace(roles) == "" {
		return "<roles>"
	}
	return shellQuote(roles)
}

func resolveRunStartGoalLead(project, profile, explicitLead string, freshRoster bool, leadForNewTeam string) (string, error) {
	if strings.TrimSpace(explicitLead) != "" {
		return strings.TrimSpace(explicitLead), nil
	}
	if freshRoster {
		return strings.TrimSpace(leadForNewTeam), nil
	}
	t, err := team.ReadProfile(project, profile)
	if err != nil {
		return "", fmt.Errorf("read team profile %q for goal delivery lead: %w", profile, err)
	}
	lead := strings.TrimSpace(t.Lead)
	if lead == "" && len(t.Members) == 1 {
		lead = strings.TrimSpace(t.Members[0].Role)
	}
	if lead == "" {
		return "", usageErrorf("cannot infer lead role for goal delivery from profile %q; pass --lead", profile)
	}
	return lead, nil
}

func deliverRunStartGoalWhenReady(opts runStartGoalDeliveryOptions) error {
	retryCmd := runStartGoalRetryCommand(opts)
	if err := waitForRunStartLeadReady(opts); err != nil {
		printRunStartGoalRetry(retryCmd)
		return err
	}
	quietNotice("delivering goal to lead...\n")
	args := []string{
		"start",
		"--project", opts.Project,
		"--profile", opts.Profile,
		"--session", opts.Session,
		"--role", opts.Role,
		"--goal", opts.Goal,
		"--yes",
	}
	if err := runStartGoalWithVersion(args, opts.Version); err != nil {
		var sentReceiptErr *goalFallbackSentReceiptError
		if errors.As(err, &sentReceiptErr) {
			fmt.Fprintf(os.Stderr, "warning: claim-once goal fallback %s was sent on %s, but local receipt persistence failed: %v\n", sentReceiptErr.MessageID, sentReceiptErr.Thread, sentReceiptErr.ReceiptErr)
			fmt.Fprintf(os.Stderr, "warning: do not blindly retry this goal attempt; inspect root %s and attempt/message evidence first. Continuing the launch — layout finalization and the launcher-pane policy still run.\n", sentReceiptErr.Root)
			return nil
		}
		var queued *tmuxpane.QueuedInputError
		if errors.As(err, &queued) {
			fmt.Fprintf(os.Stderr, "warning: goal queued in the lead's input; it will submit when the agent goes idle: %v\n", err)
			fmt.Fprintf(os.Stderr, "warning: continuing the launch — layout finalization and the launcher-pane policy still run.\n")
			fmt.Fprintf(os.Stderr, "If the goal never appears, re-deliver with:\n  %s\n", retryCmd)
			return nil
		}
		// An unconfirmed pane submit is ambiguous, not a failure (see
		// classifyNudgeResult): the goal text is pasted in the lead's input,
		// and a busy agent queues typed input until it goes idle. Aborting
		// here would also skip layout finalization and the launcher-pane
		// policy, punishing a likely success. Warn and continue; hard
		// delivery errors still abort.
		var unconfirmed *tmuxpane.SubmitUnconfirmedError
		if errors.As(err, &unconfirmed) {
			fmt.Fprintf(os.Stderr, "warning: goal submission was not confirmed: %v\n", err)
			fmt.Fprintf(os.Stderr, "warning: the goal text is staged in the lead's input; a busy agent queues it and submits when idle (or after one manual Enter). Continuing the launch — layout finalization and the launcher-pane policy still run.\n")
			fmt.Fprintf(os.Stderr, "If the goal never appears, re-deliver with:\n  %s\n", retryCmd)
			return nil
		}
		printRunStartGoalRetry(retryCmd)
		return fmt.Errorf("goal delivery failed after lead became ready: %w", err)
	}
	return nil
}

func waitForRunStartLeadReady(opts runStartGoalDeliveryOptions) error {
	timeout := runStartLeadReadyTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	backoff := runStartLeadReadyInitialBackoff
	if backoff <= 0 {
		backoff = 250 * time.Millisecond
	}
	maxBackoff := runStartLeadReadyMaxBackoff
	if maxBackoff <= 0 {
		maxBackoff = 2 * time.Second
	}
	deadline := runStartLeadReadyNow().Add(timeout)
	lastDetail := "lead is not ready yet"
	var lastErr error
	for {
		ready, err := runStartLeadReadyCheck(opts.Project, opts.Profile, opts.Session, opts.Role)
		if err != nil {
			lastErr = err
			lastDetail = "readiness check error: " + err.Error()
		} else if strings.TrimSpace(ready.Detail) != "" {
			lastDetail = strings.TrimSpace(ready.Detail)
		}
		if err == nil && ready.Ready {
			return nil
		}
		now := runStartLeadReadyNow()
		if !now.Before(deadline) {
			if lastErr != nil {
				return fmt.Errorf("lead role %q did not become ready within %s: %s: %w", opts.Role, timeout, lastDetail, lastErr)
			}
			return fmt.Errorf("lead role %q did not become ready within %s: %s", opts.Role, timeout, lastDetail)
		}
		sleepFor := backoff
		if remaining := deadline.Sub(now); sleepFor > remaining {
			sleepFor = remaining
		}
		runStartLeadReadySleep(sleepFor)
		if backoff < maxBackoff {
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func defaultRunStartLeadReadyCheck(project, profile, session, role string) (runStartLeadReadiness, error) {
	t, err := team.ReadProfile(project, profile)
	if err != nil {
		return runStartLeadReadiness{}, err
	}
	rows := buildStatusRows(t, profile, session, defaultDuplicateLaunchProbe)
	for _, row := range rows {
		if row.Role != role && row.Handle != role {
			continue
		}
		if row.Status == statusStateLive &&
			row.Signals.AgentAlive &&
			row.Signals.BinaryMatch &&
			row.Tmux != nil &&
			row.Tmux.PaneAlive {
			return runStartLeadReadiness{Ready: true, Detail: fmt.Sprintf("role %s live in pane %s", row.Role, row.Tmux.PaneID)}, nil
		}
		detail := strings.TrimSpace(row.Detail)
		if detail == "" {
			detail = fmt.Sprintf("status=%s agent_alive=%t binary_match=%t pane_alive=%t", row.Status, row.Signals.AgentAlive, row.Signals.BinaryMatch, row.Tmux != nil && row.Tmux.PaneAlive)
		}
		return runStartLeadReadiness{Detail: detail}, nil
	}
	return runStartLeadReadiness{Detail: fmt.Sprintf("role %s not found in status rows", role)}, nil
}

func runStartGoalRetryCommand(opts runStartGoalDeliveryOptions) string {
	parts := []string{
		"amq-squad", "goal", "start",
		"--project", shellQuote(opts.Project),
		"--profile", shellQuote(opts.Profile),
		"--session", shellQuote(opts.Session),
		"--role", shellQuote(opts.Role),
		"--goal", shellQuote(opts.Goal),
		"--yes",
	}
	return strings.Join(parts, " ")
}

func printRunStartGoalRetry(cmd string) {
	fmt.Fprintf(os.Stderr, "Goal delivery did not complete. Retry manually with:\n  %s\n", cmd)
}

// stripFlagValue returns args with every `flag value` pair removed, and whether
// any was present. Used to drop --seed-from from the preview validation dry-run.
func stripFlagValue(args []string, flag string) (stripped []string, had bool) {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == flag {
			had = true
			i++ // skip the value
			continue
		}
		out = append(out, args[i])
	}
	return out, had
}

// appendPassthroughArgs forwards model / per-binary arg overrides verbatim to
// `new team` and `up`, which already parse the "role=model,..." and raw-arg
// formats. Forwarding the strings unchanged keeps one parser and avoids a
// second, drift-prone format in this layer.
func appendPassthroughArgs(dst []string, model, codexArgs, claudeArgs string) []string {
	if strings.TrimSpace(model) != "" {
		dst = append(dst, "--model", model)
	}
	if strings.TrimSpace(codexArgs) != "" {
		dst = append(dst, "--codex-args", codexArgs)
	}
	if strings.TrimSpace(claudeArgs) != "" {
		dst = append(dst, "--claude-args", claudeArgs)
	}
	return dst
}
