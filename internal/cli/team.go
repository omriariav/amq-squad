package cli

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/omriariav/amq-squad/v3/internal/catalog"
	"github.com/omriariav/amq-squad/v3/internal/role"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// teamSubcommandHints is the subcommand list runTeam dispatches (the deleted
// `team resume` redirect excluded), shared by the unknown-subcommand hint and the
// usage-vs-dispatch drift test (team_usage_registry_test.go).
var teamSubcommandHints = []string{"init", "rules", "operator", "sync"}

func runTeam(args []string) error {
	if len(args) == 0 {
		printTeamUsage()
		return nil
	}
	switch args[0] {
	case "-h", "--help":
		printTeamUsage()
		return nil
	case "init":
		return runTeamInit(args[1:])
	case "resume":
		return runTeamResumeDeleted(args[1:])
	case "rules":
		return runTeamRules(args[1:])
	case "operator":
		return runTeamOperator(args[1:])
	case "sync":
		return runTeamSync(args[1:])
	default:
		// Unknown subcommand. Treat as flags to the smart default so
		// `amq-squad team --help` and similar still work.
		return unknownSubcommandError("team", args[0], teamSubcommandHints...)
	}
}

// runTeamResumeDeleted is gh#758/t11 slice B commit 4: `team resume` is
// deleted, not deprecated -- unlike t12's gh#762 verbs, which stay fully
// functional for a release with just a stderr notice (new.go's pattern),
// `team resume` never gets that grace period, per cto's ruling on
// task/t11. It named the exact same operation top-level `resume` already
// covers (and now, since slice B commit 2, drives the identical shared
// simple_start machinery under the hood); keeping two names alive for one
// command was never the point, only cutting over the classifier was.
func runTeamResumeDeleted(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprintln(os.Stderr, "amq-squad team resume - deleted (gh#758)\n\nThere is no resume verb in amq-squad 3.0; the top-level resume it pointed to was removed too.")
		return nil
	}
	return fmt.Errorf("team resume was deleted (gh#758); there is no resume verb in amq-squad 3.0")
}

func runTeamInit(args []string) error {
	return runTeamInitWithOptions(args, teamInitRunOptions{})
}

type teamInitRunOptions struct {
	SyncCommand string
	// CapturePlan, when set, is called with the fully-computed planned team
	// profile and rendered team-rules.md content right after both are built
	// (same point --dry-run branches from) and BEFORE any write or dry-run
	// print happens. runTeamInitWithOptions then returns nil immediately --
	// no write, no dry-run output. This is `init`'s (gh#762) hook into team
	// init's existing plan-then-write structure so it can compute its
	// init_digest over the same planned content team init would actually
	// write, without duplicating team init's ~500 lines of flag/persona/
	// role resolution logic.
	CapturePlan func(t team.Team, rulesContent string)
}

func runTeamInitWithOptions(args []string, opts teamInitRunOptions) error {
	fs := flag.NewFlagSet("team init", flag.ContinueOnError)
	personasFlag := fs.String("personas", "", "comma-separated persona IDs to include (alias for --roles)")
	rolesFlag := fs.String("roles", "", "comma-separated role/persona IDs to include (skips interactive prompt)")
	roleFileFlag := fs.String("role-file", "", "comma-separated custom role files (.md/.yaml/.json) to include as team members")
	binaryFlag := fs.String("binary", "", "per-persona CLI overrides, e.g. fullstack=codex,qa=claude")
	sessionFlag := fs.String("session", "", "AMQ workstream session name for all members (lowercase a-z, 0-9, -, _)")
	noSessionPinFlag := fs.Bool("no-session-pin", false, "create an unpinned template profile: members carry no session, so `run start --profile NAME --session <any>` can launch it for any workstream (cannot be combined with --session)")
	cwdFlag := fs.String("cwd", "", "per-persona working directory overrides, e.g. qa=/path/to/sibling-project")
	modelFlag := fs.String("model", "", "per-persona model overrides, e.g. cto=gpt-5.6-sol,fullstack=sonnet")
	effortFlag := fs.String("effort", "", "per-persona effort normalized into native member args, e.g. cto=high,qa=medium")
	toolProfileFlag := fs.String("tool-profile", "", "per-role capability policy, e.g. cto=full,dev=coding,qa=minimal")
	toolConfigFlag := fs.String("tool-config", "", "per-role native policy materialization: Claude settings path or Codex profile name")
	actorModeFlag := fs.String("actor-mode", "", "per-role execution capability: implementation or review, e.g. dev=implementation,reviewer=review")
	trustRaw := fs.String("trust", "", "Codex trust profile for generated commands: approve-for-me (default), sandboxed, or trusted")
	codexArgsRaw := fs.String("codex-args", "", "extra Codex args for every Codex member, e.g. '--enable goals'")
	claudeArgsRaw := fs.String("claude-args", "", "extra Claude args for every Claude member, e.g. '--chrome'")
	operatorFlag := fs.String("operator", team.DefaultOperatorHandle, "virtual operator mailbox handle for human gates (default: user)")
	operatorModeFlag := fs.String("operator-mode", "", "operator interaction contract: lead_pane, separate_terminal, or noc")
	operatorNotifications := fs.Bool("operator-notifications", false, "enable attention-only operator notifications with the default desktop sink")
	noOperator := fs.Bool("no-operator", false, "disable the virtual operator participant for this profile")
	orchestratedFlag := fs.Bool("orchestrated", false, "wire the squad for lead-agent orchestration: inject the reporting norm into team-rules.md and mark the lead role")
	leadFlag := fs.String("lead", "", "role that leads an orchestrated squad (must be a team member; implies --orchestrated)")
	leadModeFlag := fs.String("lead-mode", "", "lead implementation posture: builder (default) or planner")
	modeFlag := fs.String("mode", "", "execution mode: global_orchestrator, project_lead, project_team, or direct_lead_session")
	controlRootFlag := fs.String("control-root", "", "control-plane root directory for the execution contract (default: project/team-home)")
	targetProjectRootFlag := fs.String("target-project-root", "", "target project root for the execution contract (default: project/team-home)")
	targetContractFlag := fs.String("target-contract", "", "target amq-squad contract version for compatibility checks")
	sharedCwdExceptionFlag := fs.String("shared-cwd-exception", "", "explicit recorded reason for letting 2+ mutation-capable members share one working directory (#497); readiness fails closed on a detected collision without one")
	compositionFlag := fs.String("composition", team.CompositionSeeded, "composition mode: seeded (default) or autonomous")
	maxAgentsFlag := fs.Int("max-agents", 0, "autonomous guardrail: maximum active agents")
	maxTotalSpawnsFlag := fs.Int("max-total-spawns", 0, "autonomous guardrail: maximum total autonomous spawns")
	allowedRolesFlag := fs.String("allowed-roles", "", "autonomous guardrail: comma-separated role allowlist")
	allowedRoleClassesFlag := fs.String("allowed-role-classes", "", "autonomous guardrail: comma-separated role-class allowlist")
	budgetTurnsFlag := fs.Int("budget-turns", 0, "autonomous guardrail: maximum lead turns before operator review")
	idleReapMinutesFlag := fs.Int("idle-reap-minutes", 0, "autonomous guardrail: idle minutes before prune is allowed")
	dryRun := fs.Bool("dry-run", false, "preview the team profile and rules paths without writing files")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned team_profile_plan envelope instead of the human dry-run preview")
	force := fs.Bool("force", false, "overwrite an existing team.json")
	_ = fs.String("project", "", "project/team-home directory to initialize (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to initialize (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team init - set up this project's agent team

Usage:
  amq-squad team init [--project DIR] [--profile NAME] [--personas id1,id2,...|numbers|all] [--session workstream] [--operator-mode lead_pane|separate_terminal|noc] [other flags]
  amq-squad team init [--project DIR] [--profile NAME] [--roles id1,id2,...|numbers|all] [--session workstream] [--operator-mode lead_pane|separate_terminal|noc] [other flags]

Without --personas or --roles, prompts interactively: first choose personas,
then choose the CLI for each persona. Writes the team config under
<cwd>/.amq-squad/: the default profile goes to team.json; --profile NAME
writes to teams/<name>.json instead. Seeds <cwd>/.amq-squad/team-rules.md
if it does not already exist (single source of truth across profiles).
The directory where this runs, or DIR from --project, becomes the team-home.
Members can live in other directories via --cwd role=/path. Relative --cwd
values under --project resolve from DIR. Default AMQ workstream sessions are
derived from the team-home directory name.

Pass --no-session-pin to create an unpinned template profile instead: members
carry no session, so 'run start --profile NAME --session <any>' can launch this
roster for any workstream (a day-to-day reusable squad, e.g. a release-train
roster relaunched per release). Cannot combine with --session.

Custom roles: a --roles/--personas entry that is not a built-in persona is
treated as a custom role. Team formation auto-discovers authored custom roles
from .amq-squad/roles/<id>.md. Each custom role must be a valid slug
(lowercase a-z, 0-9, -, _) and must carry an explicit --binary role=<cli>
entry unless its discovered/loaded role file has a 'binary:' field, e.g.
--roles researcher --binary researcher=codex. Built-in personas keep their
catalog defaults unless overridden.

Custom roles from a file: --role-file PATH (comma-separated) loads roles from
.md (optionally with YAML frontmatter), .yaml, or .json files. A role file may
also be referenced inline, e.g. --roles cto,./roles/researcher.md. The file's
'binary:' field satisfies the binary requirement (and --binary overrides it).
The authored document is staged under .amq-squad/roles/<id>.md and seeds that
agent's role.md at launch.

Execution capability: --actor-mode role=implementation|review pins what each
member may DO (mutate code vs review-only), independent of its persona title,
e.g. --actor-mode dev=implementation,reviewer=review. Unset roles default to
implementation, except a lead under --lead-mode planner which defaults to
review. The resolved mode per member is echoed as actor_mode in the
--dry-run --json team_profile_plan member entries (the human --dry-run
table does not show it).

Operator interaction: --operator-mode accepts lead_pane, separate_terminal,
or noc. Only the owner answers typed gates.
--operator-notifications independently enables attention-only delivery through
the default desktop sink. It never changes the interaction mode or approves a
gate; run the scoped operator watcher on the host that should show alerts.

Orchestration (opt-in, default off): --orchestrated wires the squad for
lead-agent orchestration. It records the lead in team.json and injects the
orchestration reporting norm into team-rules.md (the lead loads the
amq-squad-orchestrator skill; children push status/question/review_request over
AMQ). Pass --lead ROLE to name the lead (implies --orchestrated); the lead must
be a team member. Without --lead, a single-member team self-selects and a team
with a cto defaults to cto.

Autonomous composition is opt-in and requires --orchestrated plus an explicit
policy: --composition autonomous --max-agents N --max-total-spawns N
--allowed-roles role,... (or --allowed-role-classes class,...) --budget-turns N.
It never authorizes merges, pushes, releases, destructive filesystem actions,
external communications, or provider side effects.

Known personas:
`)
		for _, r := range catalog.All() {
			fmt.Fprintf(os.Stderr, "  %-10s %s (default CLI: %s)\n", r.ID, r.Label, r.PreferredBinary)
		}
		fmt.Fprint(os.Stderr, `
Examples:
  amq-squad team init --roles cto,fullstack --binary cto=codex
  amq-squad team init --roles researcher --binary researcher=codex
  amq-squad team init --role-file ./roles/researcher.md --roles cto
  amq-squad team init --roles cto,fullstack --operator user
  amq-squad team init --roles cto,fullstack --operator operator
  amq-squad team init --roles cto,fullstack --no-operator
  amq-squad team init --roles cto,fullstack,qa --orchestrated --lead cto
  amq-squad team init --roles dev,reviewer --actor-mode dev=implementation,reviewer=review
  amq-squad team init --project ~/Code/app --roles cto,qa
  amq-squad team init --roles 2,9
  amq-squad team init --roles all
  amq-squad team init --roles cto,qa --dry-run
  amq-squad team init --roles cto,qa --dry-run --json
  amq-squad team init --profile review --roles cto --session review
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if flagWasSet(fs, "project") {
		project, rest, err := peelProjectFlag(args)
		if err != nil {
			return err
		}
		return runInProject(project, func() error { return runTeamInitWithOptions(rest, opts) })
	}
	if *jsonOut && !*dryRun {
		return usageErrorf("--json requires --dry-run on `team init`; the live write path does not have a JSON contract")
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	trustMode, err := normalizeTrustMode(*trustRaw)
	if err != nil {
		return err
	}
	if *rolesFlag != "" && *personasFlag != "" {
		return fmt.Errorf("use either --personas or --roles, not both")
	}
	if *noOperator && flagWasSet(fs, "operator") {
		return fmt.Errorf("use either --operator or --no-operator, not both")
	}
	if *noOperator && flagWasSet(fs, "operator-mode") {
		return fmt.Errorf("use either --operator-mode or --no-operator, not both")
	}
	if *noOperator && *operatorNotifications {
		return fmt.Errorf("use either --operator-notifications or --no-operator, not both")
	}
	operator := team.DefaultOperator()
	if *noOperator {
		operator = team.DisabledOperator()
	} else {
		handle := strings.TrimSpace(*operatorFlag)
		if handle == "" {
			return fmt.Errorf("--operator: handle cannot be empty")
		}
		if err := team.ValidateHandle(handle); err != nil {
			return fmt.Errorf("--operator: %w", err)
		}
		operator.Handle = handle
		if flagWasSet(fs, "operator-mode") {
			mode, modeErr := validateCanonicalOperatorMode(*operatorModeFlag)
			if modeErr != nil {
				return fmt.Errorf("--operator-mode: %w", modeErr)
			}
			operator.InteractionMode = mode
		}
		if *operatorNotifications {
			operator.Notifications = &team.OperatorNotificationPolicy{Enabled: true, DeliverySemantics: "attention_only", Sinks: []team.OperatorNotificationSinkConfig{{ID: "desktop", Type: "desktop", Timeout: "10s"}}}
		}
	}
	if *noSessionPinFlag {
		if flagWasSet(fs, "session") {
			return fmt.Errorf("use either --no-session-pin or --session, not both: an unpinned template profile has no session to pin")
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	agentCatalog := loadAgentCatalogAndWarn(cwd)

	profileExists := team.ExistsProfile(cwd, profile)
	// CapturePlan mode never writes (same guarantee --dry-run makes), so the
	// existing-profile refusal below -- which exists to stop an accidental
	// non-force WRITE from clobbering a profile -- does not apply to it.
	// init (gh#762) relies on this: computing a refresh plan/digest against
	// an EXISTING, unchanged profile is exactly what its "creates or
	// refreshes" idempotence requires, and that must work without --force.
	if profileExists && !*force && !*dryRun && opts.CapturePlan == nil {
		return fmt.Errorf("team config already exists at %s. Use --force to overwrite.", team.ProfilePath(cwd, profile))
	}

	binaryOverrides, err := parseKV(*binaryFlag)
	if err != nil {
		return fmt.Errorf("parse --binary: %w", err)
	}
	if flagWasSet(fs, "session") && strings.ContainsAny(*sessionFlag, "=,") {
		return fmt.Errorf("old per-role --session syntax is no longer supported; pass one shared workstream name, e.g. --session issue-96")
	}
	workstream, err := resolveWorkstreamName(cwd, *sessionFlag, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	cwdOverrides, err := parseKV(*cwdFlag)
	if err != nil {
		return fmt.Errorf("parse --cwd: %w", err)
	}
	modelOverrides, err := parseKV(*modelFlag)
	if err != nil {
		return fmt.Errorf("parse --model: %w", err)
	}
	effortOverrides, err := parseEffortOverrides(*effortFlag)
	if err != nil {
		return err
	}
	toolProfiles, err := parseKV(*toolProfileFlag)
	if err != nil {
		return fmt.Errorf("parse --tool-profile: %w", err)
	}
	toolConfigs, err := parseKV(*toolConfigFlag)
	if err != nil {
		return fmt.Errorf("parse --tool-config: %w", err)
	}
	toolProfiles, toolConfigs = lowercaseKeys(toolProfiles), lowercaseKeys(toolConfigs)
	actorModes, err := parseKV(*actorModeFlag)
	if err != nil {
		return fmt.Errorf("parse --actor-mode: %w", err)
	}
	actorModes = lowercaseKeys(actorModes)
	binaryArgs, err := parseBinaryArgFlags(*codexArgsRaw, *claudeArgsRaw)
	if err != nil {
		return err
	}
	// Validate trust + binary-args contradictions up-front so a team config
	// never persists a setting that would fail at launch time.
	if err := validateTrustCombination(trustMode, flagWasSet(fs, "trust"), false, binaryArgs); err != nil {
		return err
	}
	// Normalize override keys to lowercase so we can match them against the
	// chosen persona IDs without surprises.
	modelOverrides = lowercaseKeys(modelOverrides)
	binaryOverrides = lowercaseKeys(binaryOverrides)

	// customDefs holds custom roles loaded from files (via --role-file or an
	// inline path token in --roles/--personas) plus staged roles discovered
	// from .amq-squad/roles/, keyed by resolved role id.
	customDefs := map[string]role.Definition{}
	roleFilePaths := splitCSV(*roleFileFlag)
	fileSelected := make([]string, 0, len(roleFilePaths))
	for _, p := range roleFilePaths {
		id, err := loadRoleFileDef(p, customDefs)
		if err != nil {
			return err
		}
		fileSelected = append(fileSelected, id)
	}

	var picked []string
	interactive := *rolesFlag == "" && *personasFlag == "" && len(roleFilePaths) == 0
	if interactive {
		if err := discoverStagedCustomRoleDefs(cwd, customDefs); err != nil {
			return err
		}
	}
	// The non-interactive flag paths accept custom role slugs (resolved by the
	// member loop below, which requires a --binary or a role file for each) and
	// inline role-file paths. The interactive prompt accepts built-ins plus
	// staged roles discovered under .amq-squad/roles/.
	if *personasFlag != "" {
		picked, err = resolveTeamSelection(*personasFlag, customDefs)
		if err != nil {
			return err
		}
	} else if *rolesFlag != "" {
		picked, err = resolveTeamSelection(*rolesFlag, customDefs)
		if err != nil {
			return err
		}
	} else if len(roleFilePaths) > 0 {
		// Role files only: the selection is exactly the file-defined roles.
		picked = nil
	} else {
		reader := bufio.NewReader(os.Stdin)
		picked, err = promptPersonaSelection(reader, os.Stderr, customDefs)
		if err != nil {
			return err
		}
		if err := promptBinarySelection(reader, os.Stderr, picked, binaryOverrides, customDefs); err != nil {
			return err
		}
		if err := promptModelSelection(reader, os.Stderr, modelOverrides); err != nil {
			return err
		}
		if !flagWasSet(fs, "trust") {
			chosen, err := promptTrustSelection(reader, os.Stderr, trustMode)
			if err != nil {
				return err
			}
			trustMode = chosen
		}
		if !flagWasSet(fs, "session") && !*noSessionPinFlag {
			chosen, err := promptWorkstreamSelection(reader, os.Stderr, workstream)
			if err != nil {
				return err
			}
			workstream = chosen
		}
	}
	// Roles named via --role-file are always part of the team, even when they
	// are not also listed in --roles/--personas. Duplicates are dropped by the
	// member loop's seen-set below.
	picked = append(picked, fileSelected...)
	if len(picked) == 0 {
		return fmt.Errorf("no personas selected, aborting")
	}
	if !interactive {
		if err := discoverStagedCustomRoleDefs(cwd, customDefs); err != nil {
			return err
		}
	}

	// Reject --model role=model where role is not a chosen persona, so a
	// typo never silently drops a model override.
	pickedSet := make(map[string]bool, len(picked))
	for _, id := range picked {
		pickedSet[strings.ToLower(strings.TrimSpace(id))] = true
	}
	if err := validateModelOverrideKeys(modelOverrides, pickedSet); err != nil {
		return err
	}
	if err := validateEffortOverrideKeys(effortOverrides, pickedSet, "--roles"); err != nil {
		return err
	}
	for label, values := range map[string]map[string]string{"--tool-profile": toolProfiles, "--tool-config": toolConfigs, "--actor-mode": actorModes} {
		var unknown []string
		for role := range values {
			if !pickedSet[role] {
				unknown = append(unknown, role)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return fmt.Errorf("%s has unknown role(s): %s", label, strings.Join(unknown, ", "))
		}
	}

	members := make([]team.Member, 0, len(picked))
	seen := make(map[string]bool)
	for _, id := range picked {
		id = strings.TrimSpace(strings.ToLower(id))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		memberSession := workstream
		if *noSessionPinFlag {
			memberSession = ""
		}
		var binary string
		if r := catalog.Lookup(id); r != nil {
			binary = r.PreferredBinary
			if b, ok := binaryOverrides[id]; ok {
				binary = b
			}
			if interactive && binary == "" {
				binary = r.PreferredBinary
			}
		} else {
			// Custom role: not in the built-in catalog. It must be a valid role
			// slug and must carry an explicit --binary entry, since there is no
			// catalog default to fall back to. Everything downstream (team.json,
			// team-rules, bootstrap, status/history, launch) already treats
			// non-catalog roles as first-class members.
			if err := team.ValidateRoleID(id); err != nil {
				return fmt.Errorf("custom role %q: %w", id, err)
			}
			binary = strings.TrimSpace(binaryOverrides[id])
			if binary == "" {
				if def, ok := customDefs[id]; ok {
					binary = strings.TrimSpace(def.Binary)
				}
			}
			if binary == "" {
				return fmt.Errorf("custom role %q requires --binary %s=<cli> (or a 'binary:' field in its role file)", id, id)
			}
		}
		m := team.Member{
			Role:      id,
			Binary:    binary,
			Handle:    id,
			Session:   memberSession,
			ActorMode: team.ActorModeImplementation,
		}
		if mode, ok := actorModes[id]; ok {
			m.ActorMode = strings.ToLower(strings.TrimSpace(mode))
			if m.ActorMode != team.ActorModeImplementation && m.ActorMode != team.ActorModeReview {
				return fmt.Errorf("--actor-mode %s=%s: use %s or %s", id, mode, team.ActorModeImplementation, team.ActorModeReview)
			}
		}
		if model, ok := modelOverrides[id]; ok {
			m.Model = strings.TrimSpace(model)
		}
		if effort, ok := effortOverrides[id]; ok {
			if err := applyMemberEffortCatalog(&m, effort, agentCatalog); err != nil {
				return err
			}
		}
		m.ToolProfile = strings.TrimSpace(toolProfiles[id])
		m.ToolConfig = strings.TrimSpace(toolConfigs[id])
		if c, ok := cwdOverrides[id]; ok {
			// #538 F2: use the SHARED resolver. This path previously had its own
			// copy, which anchored a relative value to the shell working directory
			// and compared team-home by raw string equality, so create and
			// add/update could record different values for the same input. Two
			// writers with two origins is the #539/#540 defect; there is now one
			// function and one origin (the project).
			resolved, err := memberCWDOverride(cwd, c)
			if err != nil {
				return fmt.Errorf("resolve cwd for %s: %w", id, err)
			}
			m.CWD = resolved
		}
		members = append(members, m)
	}

	orchestrated, leadRole, err := resolveOrchestration(*orchestratedFlag, *leadFlag, members)
	if err != nil {
		return err
	}
	executionMode, err := normalizeExecutionMode(*modeFlag)
	if err != nil {
		return err
	}
	leadMode, err := normalizeLeadMode(*leadModeFlag)
	if err != nil {
		return err
	}
	if !flagWasSet(fs, "mode") {
		executionMode = defaultExecutionModeForTeam(orchestrated)
	}
	if orchestrated && leadMode == team.LeadModePlanner {
		for i := range members {
			if strings.EqualFold(members[i].Role, leadRole) {
				members[i].ActorMode = team.ActorModeReview
			}
		}
	}
	composition := strings.TrimSpace(*compositionFlag)
	autonomousPolicy, err := resolveAutonomousPolicy(composition, *maxAgentsFlag, *maxTotalSpawnsFlag, *allowedRolesFlag, *allowedRoleClassesFlag, *budgetTurnsFlag, *idleReapMinutesFlag)
	if err != nil {
		return err
	}
	// #290: a global_orchestrator run edits code in target_project_root, which is
	// NOT the control root. From a neutral control root there is no reliable
	// owner/repo -> local path mapping, so refuse to silently default
	// target_project_root to cwd. The operator must pass an explicit, confirmed
	// path; a global_orchestrator run must never begin from an unconfirmed/guessed
	// project tree. Other modes (the lead runs inside the project) keep the cwd
	// default.
	if executionMode == executionModeGlobalOrchestrator && !flagWasSet(fs, "target-project-root") {
		return usageErrorf("global_orchestrator requires an explicit --target-project-root: the control root is not the project tree, and amq-squad will not silently use the current directory. Pass the confirmed local checkout path.")
	}

	t := team.Team{
		Project: cwd,
		// Intentionally do NOT stamp t.Workstream here. The pinned workstream
		// default is a deprecated shim (removal in 2.1); new teams must not pin
		// one. Live session resolution infers a shared member session or falls
		// back to the project basename. The field remains readable for old
		// team.json files. Member sessions still carry the chosen workstream.
		Trust:              trustMode,
		Operator:           &operator,
		BinaryArgs:         binaryArgs,
		Members:            members,
		Orchestrated:       orchestrated,
		Lead:               leadRole,
		Composition:        composition,
		Autonomous:         autonomousPolicy,
		ExecutionMode:      executionMode,
		ControlRoot:        cleanRootOrDefault(*controlRootFlag, cwd),
		TargetProjectRoot:  cleanRootOrDefault(*targetProjectRootFlag, cwd),
		TargetContract:     strings.TrimPrefix(strings.TrimSpace(*targetContractFlag), "v"),
		LeadMode:           leadModeForPersist(leadMode),
		SharedCwdException: strings.TrimSpace(*sharedCwdExceptionFlag),
	}
	rulesContent, err := renderTeamRules(t)
	if err != nil {
		return fmt.Errorf("render team-rules.md: %w", err)
	}
	if opts.CapturePlan != nil {
		opts.CapturePlan(t, rulesContent)
		return nil
	}
	if *dryRun {
		return printTeamInitDryRun(teamInitDryRun{
			TeamHome:    cwd,
			Profile:     profile,
			ProfilePath: team.ProfilePath(cwd, profile),
			RulesPath:   rules.Path(cwd),
			Workstream:  workstream,
			Trust:       trustMode,
			Exists:      profileExists,
			Team:        t,
			JSON:        *jsonOut,
			SyncCommand: opts.SyncCommand,
		})
	}
	if err := team.WriteProfile(cwd, profile, t); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Wrote %s with %d members.\n", team.ProfilePath(cwd, profile), len(members))
	// Stage authored documents for file-defined custom roles so `up` seeds each
	// agent's role.md from the file instead of the minimal fallback.
	for _, m := range members {
		def, ok := customDefs[m.Role]
		if !ok {
			continue
		}
		if customRoleSourceIsStaged(cwd, def.Source) {
			continue
		}
		wrote, err := stageCustomRoleDoc(cwd, def)
		if err != nil {
			return fmt.Errorf("stage role doc for %s: %w", m.Role, err)
		}
		if wrote {
			fmt.Fprintf(os.Stderr, "Wrote %s.\n", team.CustomRolePath(cwd, m.Role))
		}
	}
	wroteRules, err := rules.Ensure(cwd, rulesContent)
	if err != nil {
		return fmt.Errorf("seed team-rules.md: %w", err)
	}
	if wroteRules {
		fmt.Fprintf(os.Stderr, "Wrote %s.\n", rules.Path(cwd))
	} else if existing, rerr := rules.Read(cwd); rerr == nil && !teamRulesDescribesRoster(existing, t) {
		// team-rules.md is one shared file per team-home (no-clobber). This
		// profile reused an existing file whose roster description does not name
		// every member of THIS profile, so its "## Role Scope" roster is stale
		// for this profile. It is not a hard error — agents route from the live
		// routing block injected at bootstrap, not from this file's roster — but
		// the operator should know the doc and this profile disagree.
		fmt.Fprintf(os.Stderr, "Warning: %s already exists and was left unchanged; its roster does not describe this profile's members. Agents use the live routing block injected at bootstrap, not this file's roster, so this is cosmetic. Reconcile the doc if the rosters should match.\n", rules.Path(cwd))
	}
	if interactive {
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "Next:")
		fmt.Fprintln(os.Stderr, "  amq-squad doctor               # check the configured roster")
	}
	return nil
}

type teamInitDryRun struct {
	TeamHome    string
	Profile     string
	ProfilePath string
	RulesPath   string
	Workstream  string
	Trust       string
	Exists      bool
	Team        team.Team
	JSON        bool
	SyncCommand string
}

type teamProfilePlanMember struct {
	Role                 string   `json:"role"`
	Handle               string   `json:"handle"`
	Binary               string   `json:"binary"`
	Model                string   `json:"model,omitempty"`
	CWD                  string   `json:"cwd"`
	Session              string   `json:"session"`
	ActorMode            string   `json:"actor_mode"`
	ToolProfile          string   `json:"tool_profile"`
	ToolConfig           string   `json:"tool_config,omitempty"`
	ToolMCPConfig        string   `json:"tool_mcp_config,omitempty"`
	ToolEnabledSet       []string `json:"tool_enabled_set"`
	ToolRevokedSet       []string `json:"tool_revoked_set"`
	ToolPolicySource     string   `json:"tool_policy_source"`
	ToolPolicySources    []string `json:"tool_policy_sources,omitempty"`
	ToolPolicyPrecedence string   `json:"tool_policy_precedence"`
	ToolPolicyDrift      []string `json:"tool_policy_drift"`
	ToolLaunchArgs       []string `json:"tool_launch_args,omitempty"`
	ClaudeArgs           []string `json:"claude_args,omitempty"`
	CodexArgs            []string `json:"codex_args,omitempty"`
	PermissionAllowlist  []string `json:"permission_allowlist,omitempty"`
}

type teamProfilePlan struct {
	TeamHome        string                  `json:"team_home"`
	Project         string                  `json:"project"`
	Profile         string                  `json:"profile"`
	ProfilePath     string                  `json:"profile_path"`
	RulesPath       string                  `json:"rules_path"`
	Workstream      string                  `json:"workstream"`
	Trust           string                  `json:"trust"`
	Orchestrated    bool                    `json:"orchestrated"`
	Lead            string                  `json:"lead,omitempty"`
	LeadMode        string                  `json:"lead_mode"`
	ExistingProfile bool                    `json:"existing_profile"`
	Members         int                     `json:"members"`
	BinaryArgs      map[string][]string     `json:"binary_args,omitempty"`
	Operator        team.OperatorView       `json:"operator"`
	Capabilities    team.Capabilities       `json:"capabilities"`
	Autonomous      team.AutonomousStatus   `json:"autonomous"`
	Execution       executionModeData       `json:"execution"`
	SyncCommand     string                  `json:"sync_command,omitempty"`
	Plan            []teamProfilePlanMember `json:"plan"`
}

func printTeamInitDryRun(p teamInitDryRun) error {
	if p.JSON {
		return printJSONEnvelope("team_profile_plan", buildTeamProfilePlan(p))
	}
	fmt.Fprintln(os.Stdout, "# amq-squad team init --dry-run")
	fmt.Fprintf(os.Stdout, "# team-home: %s\n", p.TeamHome)
	fmt.Fprintf(os.Stdout, "# profile: %s\n", p.Profile)
	fmt.Fprintf(os.Stdout, "# profile-path: %s\n", p.ProfilePath)
	fmt.Fprintf(os.Stdout, "# team-rules: %s\n", p.RulesPath)
	fmt.Fprintf(os.Stdout, "# workstream: %s\n", p.Workstream)
	fmt.Fprintf(os.Stdout, "# trust: %s\n", p.Trust)
	if p.Team.Orchestrated {
		fmt.Fprintf(os.Stdout, "# orchestrated: yes (lead: %s)\n", p.Team.Lead)
	} else {
		fmt.Fprintln(os.Stdout, "# orchestrated: no")
	}
	fmt.Fprintf(os.Stdout, "# lead-mode: %s\n", team.EffectiveLeadMode(p.Team))
	fmt.Fprintf(os.Stdout, "# composition: %s\n", team.EffectiveComposition(p.Team))
	execution := executionContractForTeam(p.Team, p.Profile, p.Workstream, "amq_task_brief", "")
	fmt.Fprintf(os.Stdout, "# mode: %s\n", execution.Mode)
	if execution.MutableActor != "" {
		fmt.Fprintf(os.Stdout, "# mutable-actor: %s\n", execution.MutableActor)
	}
	fmt.Fprintf(os.Stdout, "# implementation-allowed: %t\n", execution.ImplementationAllowed)
	if p.Exists {
		fmt.Fprintln(os.Stdout, "# existing-profile: yes (live run requires --force to overwrite)")
	} else {
		fmt.Fprintln(os.Stdout, "# existing-profile: no")
	}
	fmt.Fprintln(os.Stdout, "# writes: none")
	fmt.Fprintln(os.Stdout)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "ROLE\tHANDLE\tCLI\tMODEL\tTOOLS\tCWD\tSESSION"); err != nil {
		return err
	}
	for _, m := range orderedTeamMembers(p.Team.Members) {
		model := m.Model
		if model == "" {
			model = "-"
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			m.Role,
			m.Handle,
			m.Binary,
			model,
			m.EffectiveToolProfile(),
			m.EffectiveCWD(p.Team.Project),
			m.Session,
		); err != nil {
			return err
		}
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if p.SyncCommand != "" {
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, "# sync preview")
		fmt.Fprintln(os.Stdout, "# would run: "+p.SyncCommand)
	}
	return nil
}

func buildTeamProfilePlan(p teamInitDryRun) teamProfilePlan {
	rows := make([]teamProfilePlanMember, 0, len(p.Team.Members))
	for _, m := range orderedTeamMembers(p.Team.Members) {
		rows = append(rows, teamProfilePlanMember{
			Role:                 m.Role,
			Handle:               m.Handle,
			Binary:               m.Binary,
			Model:                m.Model,
			CWD:                  m.EffectiveCWD(p.Team.Project),
			Session:              m.Session,
			ActorMode:            team.EffectiveActorMode(p.Team, m),
			ToolProfile:          m.EffectiveToolProfile(),
			ToolConfig:           m.ToolConfig,
			ToolMCPConfig:        m.ToolMCPConfig,
			ToolEnabledSet:       m.ToolEnabledSet(),
			ToolRevokedSet:       append([]string(nil), m.ToolBlocklist...),
			ToolPolicySource:     m.ToolPolicySource(),
			ToolPolicySources:    append([]string(nil), m.ToolPolicySources...),
			ToolPolicyPrecedence: m.ToolPolicyPrecedence(),
			ToolPolicyDrift:      append([]string(nil), m.ToolPolicyDrift...),
			ToolLaunchArgs:       m.ToolArgs(),
			ClaudeArgs:           m.ClaudeArgs,
			CodexArgs:            m.CodexArgs,
			PermissionAllowlist:  m.PermissionAllowlist,
		})
	}
	return teamProfilePlan{
		TeamHome:        p.TeamHome,
		Project:         p.Team.Project,
		Profile:         p.Profile,
		ProfilePath:     p.ProfilePath,
		RulesPath:       p.RulesPath,
		Workstream:      p.Workstream,
		Trust:           p.Trust,
		Orchestrated:    p.Team.Orchestrated,
		Lead:            p.Team.Lead,
		LeadMode:        team.EffectiveLeadMode(p.Team),
		ExistingProfile: p.Exists,
		Members:         len(rows),
		BinaryArgs:      p.Team.BinaryArgs,
		Operator:        team.EffectiveOperator(p.Team),
		Capabilities:    team.EffectiveCapabilities(p.Team),
		Autonomous:      team.EffectiveAutonomousStatus(p.Team),
		Execution:       executionContractForTeam(p.Team, p.Profile, p.Workstream, "amq_task_brief", ""),
		SyncCommand:     p.SyncCommand,
		Plan:            rows,
	}
}

func memberEffectiveModel(m team.Member, overrides map[string]string) string {
	if v, ok := overrides[strings.ToLower(m.Role)]; ok {
		return strings.TrimSpace(v)
	}
	return strings.TrimSpace(m.Model)
}

// validateModelOverrideKeys rejects --model role=model entries whose role is
// not one of the known roles. Silent drops on typos are a DX trap; an error
// makes the mistake visible.
func validateModelOverrideKeys(overrides map[string]string, known map[string]bool) error {
	if len(overrides) == 0 {
		return nil
	}
	var unknown []string
	for k := range overrides {
		if !known[strings.ToLower(strings.TrimSpace(k))] {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("--model has unknown role(s): %s", strings.Join(unknown, ", "))
}

func resolveAutonomousPolicy(composition string, maxAgents, maxTotalSpawns int, allowedRoles, allowedRoleClasses string, budgetTurns, idleReapMinutes int) (*team.AutonomousPolicy, error) {
	composition = strings.TrimSpace(composition)
	if composition == "" {
		composition = team.CompositionSeeded
	}
	switch composition {
	case team.CompositionSeeded:
		if maxAgents != 0 || maxTotalSpawns != 0 || strings.TrimSpace(allowedRoles) != "" || strings.TrimSpace(allowedRoleClasses) != "" || budgetTurns != 0 || idleReapMinutes != 0 {
			return nil, fmt.Errorf("autonomous policy flags require --composition autonomous")
		}
		return nil, nil
	case team.CompositionAutonomous:
		p := team.AutonomousPolicy{
			MaxActiveAgents:    maxAgents,
			MaxTotalSpawns:     maxTotalSpawns,
			AllowedRoles:       splitCommaList(allowedRoles),
			AllowedRoleClasses: splitCommaList(allowedRoleClasses),
			BudgetTurns:        budgetTurns,
			IdleReapMinutes:    idleReapMinutes,
		}
		if err := team.ValidateAutonomousPolicy(p); err != nil {
			return nil, err
		}
		return &p, nil
	default:
		return nil, fmt.Errorf("--composition: invalid mode %q: use %s or %s", composition, team.CompositionSeeded, team.CompositionAutonomous)
	}
}

// resolveOrchestration turns the --orchestrated/--lead flags into the team's
// orchestration state. Naming a --lead implies orchestration. When --orchestrated
// is set without an explicit lead, a single-member team self-selects that member
// and a team with a cto defaults to cto; otherwise the caller must name the lead.
// The chosen lead must be one of the team's member roles (never the operator).
func resolveOrchestration(orchestratedFlag bool, leadFlag string, members []team.Member) (bool, string, error) {
	lead := strings.TrimSpace(strings.ToLower(leadFlag))
	orchestrated := orchestratedFlag || lead != ""
	if !orchestrated {
		return false, "", nil
	}
	roleSet := make(map[string]bool, len(members))
	for _, m := range members {
		roleSet[m.Role] = true
	}
	if lead == "" {
		switch {
		case roleSet["cto"]:
			lead = "cto"
		case len(members) == 1:
			lead = members[0].Role
		default:
			return false, "", fmt.Errorf("--orchestrated needs a lead: pass --lead <role> (one of: %s)", strings.Join(memberRolesSorted(members), ", "))
		}
	}
	if !roleSet[lead] {
		return false, "", fmt.Errorf("--lead %q is not a team member (members: %s)", lead, strings.Join(memberRolesSorted(members), ", "))
	}
	return true, lead, nil
}

func memberRolesSorted(members []team.Member) []string {
	out := make([]string, 0, len(members))
	for _, m := range members {
		out = append(out, m.Role)
	}
	sort.Strings(out)
	return out
}

func lowercaseKeys(m map[string]string) map[string]string {
	if len(m) == 0 {
		return m
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

func orderedTeamMembers(members []team.Member) []team.Member {
	idx := make(map[string]int, len(catalog.IDs()))
	for i, id := range catalog.IDs() {
		idx[id] = i
	}
	out := append([]team.Member(nil), members...)
	sort.SliceStable(out, func(i, j int) bool {
		left, lok := idx[out[i].Role]
		right, rok := idx[out[j].Role]
		if !lok && !rok {
			return out[i].Role < out[j].Role
		}
		if !lok {
			return false
		}
		if !rok {
			return true
		}
		return left < right
	})
	return out
}

func uniqueMemberCWDs(projectDir string, members []team.Member) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range members {
		cwd := m.EffectiveCWD(projectDir)
		if seen[cwd] {
			continue
		}
		seen[cwd] = true
		out = append(out, cwd)
	}
	sort.Strings(out)
	return out
}

func promptPersonaSelection(reader *bufio.Reader, out io.Writer, customDefs map[string]role.Definition) ([]string, error) {
	fmt.Fprintln(out, "Squad market")
	printPersonaMarket(out, customDefs)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Choose personas to hire:")
	fmt.Fprintln(out, "  names or numbers, comma-separated")
	fmt.Fprintln(out, "  examples: cto,junior-dev,qa | 2,8,9 | all")
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")

	line, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read selection: %w", err)
	}
	return parsePersonaSelection(line, customDefs)
}

// promptModelSelection asks the user for per-role model overrides. Empty
// input keeps current overrides untouched. Existing values from --model are
// preserved and may be augmented.
func promptModelSelection(reader *bufio.Reader, out io.Writer, overrides map[string]string) error {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Model overrides?")
	fmt.Fprintln(out, "  Press Enter to keep binary defaults.")
	fmt.Fprintln(out, "  Example: cto=gpt-5.6-sol,fullstack=sonnet")
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read model overrides: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	parsed, err := parseKV(line)
	if err != nil {
		return fmt.Errorf("parse model overrides: %w", err)
	}
	for k, v := range parsed {
		overrides[strings.ToLower(k)] = strings.TrimSpace(v)
	}
	return nil
}

// promptWorkstreamSelection asks for the AMQ workstream session name with
// the project default offered as Enter-to-accept. The chosen value is
// validated to AMQ session naming rules.
func promptWorkstreamSelection(reader *bufio.Reader, out io.Writer, defaultName string) (string, error) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Workstream / AMQ session?")
	fmt.Fprintln(out, "  lowercase a-z, 0-9, -, _ only.")
	fmt.Fprintf(out, "  Press Enter to use %q.\n", defaultName)
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read workstream: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultName, nil
	}
	if err := team.ValidateSessionName(line); err != nil {
		return "", err
	}
	return line, nil
}

// promptTrustSelection asks for the Codex trust profile. Default is the
// current trust mode. Returns the selected normalized trust mode.
func promptTrustSelection(reader *bufio.Reader, out io.Writer, current string) (string, error) {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Codex trust profile?")
	fmt.Fprintln(out, "  approve-for-me (default) - workspace-write, on-request, auto_review")
	fmt.Fprintln(out, "  sandboxed          - Codex prompts for approvals/sandbox")
	fmt.Fprintln(out, "  trusted   (local power) - prepends --dangerously-bypass-approvals-and-sandbox")
	fmt.Fprintf(out, "  Press Enter for %s.\n", current)
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read trust profile: %w", err)
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return current, nil
	}
	mode, err := normalizeTrustMode(strings.ToLower(line))
	if err != nil {
		return "", err
	}
	return mode, nil
}

func promptBinarySelection(reader *bufio.Reader, out io.Writer, personas []string, overrides map[string]string, customDefs map[string]role.Definition) error {
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Squad plan")
	printSquadPlan(out, personas, overrides, customDefs)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "CLI overrides?")
	fmt.Fprintln(out, "  Press Enter to keep defaults.")
	fmt.Fprintln(out, "  Example: fullstack=codex,junior-dev=claude")
	fmt.Fprintln(out)
	fmt.Fprint(out, "> ")
	line, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read CLI overrides: %w", err)
	}
	overrideLine := strings.TrimSpace(line)
	if overrideLine == "" {
		return nil
	}
	parsed, err := parseKV(overrideLine)
	if err != nil {
		return fmt.Errorf("parse CLI overrides: %w", err)
	}
	for k, v := range parsed {
		overrides[strings.ToLower(k)] = v
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Updated squad plan")
	printSquadPlan(out, personas, overrides, customDefs)
	return nil
}

func printPersonaMarket(out io.Writer, customDefs map[string]role.Definition) {
	fmt.Fprintf(out, "  %-2s %-12s %-31s %-7s %s\n", "#", "PERSONA", "PROFILE", "CLI", "FIT")
	for i, r := range catalog.All() {
		fmt.Fprintf(out, "  %-2d %-12s %-31s %-7s %s\n", i+1, r.ID, r.Label, r.PreferredBinary, r.Profile)
	}
	ids := customRoleIDs(customDefs)
	for _, id := range ids {
		def := customDefs[id]
		label := strings.TrimSpace(def.Label)
		if label == "" {
			label = id
		}
		binary := strings.TrimSpace(def.Binary)
		if binary == "" {
			binary = "(set)"
		}
		fit := strings.TrimSpace(def.Description)
		if fit == "" {
			fit = "staged custom role"
		}
		fmt.Fprintf(out, "  %-2s %-12s %-31s %-7s %s\n", "-", id, label, binary, fit)
	}
}

func parsePersonaSelection(line string, customDefs map[string]role.Definition) ([]string, error) {
	var out []string
	for _, tok := range strings.Split(line, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		ids, err := catalog.ResolveSelection(tok)
		if err == nil {
			out = append(out, ids...)
			continue
		}
		id := strings.ToLower(tok)
		if _, ok := customDefs[id]; ok {
			out = append(out, id)
			continue
		}
		known := catalog.IDs()
		if customIDs := customRoleIDs(customDefs); len(customIDs) > 0 {
			known = append(known, customIDs...)
		}
		return nil, fmt.Errorf("unknown persona/role %q. Known personas: %s", tok, strings.Join(known, ", "))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no selection provided")
	}
	return out, nil
}

// resolveTeamSelection resolves a --roles/--personas CSV that may mix catalog
// IDs, market numbers, "all", custom slugs, and inline role-file paths. File
// tokens are parsed into customDefs and contribute their resolved role id.
func resolveTeamSelection(line string, customDefs map[string]role.Definition) ([]string, error) {
	var out []string
	for _, tok := range strings.Split(line, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if role.LooksLikeRoleFile(tok) {
			id, err := loadRoleFileDef(tok, customDefs)
			if err != nil {
				return nil, err
			}
			out = append(out, id)
			continue
		}
		ids, err := catalog.ResolveSelectionAllowingCustom(tok)
		if err != nil {
			return nil, err
		}
		out = append(out, ids...)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no selection provided")
	}
	return out, nil
}

// loadRoleFileDef parses a custom role file, validates its id, registers it in
// defs, and returns the resolved role id. Two files resolving to the same id
// are rejected so a typo can't silently shadow another role.
func loadRoleFileDef(path string, defs map[string]role.Definition) (string, error) {
	abs, err := expandPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve role file %q: %w", path, err)
	}
	def, err := role.ParseFile(abs)
	if err != nil {
		return "", err
	}
	if err := team.ValidateRoleID(def.ID); err != nil {
		return "", fmt.Errorf("role file %s: %w", path, err)
	}
	// A role file is for a custom role. If its id collides with a built-in
	// persona, the built-in wins at launch and the file's binary + authored
	// document would be silently ignored, so reject it outright.
	if catalog.Lookup(def.ID) != nil {
		return "", fmt.Errorf("role file %s: id %q is a built-in persona; choose a different id for a custom role", path, def.ID)
	}
	if existing, ok := defs[def.ID]; ok && existing.Source != def.Source {
		return "", fmt.Errorf("custom role id %q is defined by two files: %s and %s", def.ID, existing.Source, def.Source)
	}
	defs[def.ID] = def
	return def.ID, nil
}

// discoverStagedCustomRoleDefs loads authored custom-role documents staged
// under <projectDir>/.amq-squad/roles. The file name is the role ID source of
// truth for staged docs; the Markdown heading is display copy and may differ.
// Existing definitions win so explicit --role-file / inline paths can refresh
// a staged role without being rejected as duplicates.
func discoverStagedCustomRoleDefs(projectDir string, defs map[string]role.Definition) error {
	entries, err := os.ReadDir(team.RolesDir(projectDir))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read custom roles dir: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, entry := range entries {
		if entry.IsDir() || !isCustomRoleFileName(entry.Name()) {
			continue
		}
		path := filepath.Join(team.RolesDir(projectDir), entry.Name())
		def, err := parseStagedCustomRoleDef(path)
		if err != nil {
			return err
		}
		if _, ok := defs[def.ID]; ok {
			continue
		}
		defs[def.ID] = def
	}
	return nil
}

func isCustomRoleFileName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".md", ".markdown", ".yaml", ".yml", ".json":
		return true
	default:
		return false
	}
}

func parseStagedCustomRoleDef(path string) (role.Definition, error) {
	def, err := role.ParseFile(path)
	if err != nil {
		return role.Definition{}, err
	}
	id := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	id = strings.ToLower(strings.TrimSpace(id))
	if err := team.ValidateRoleID(id); err != nil {
		return role.Definition{}, fmt.Errorf("role file %s: %w", path, err)
	}
	if catalog.Lookup(id) != nil {
		return role.Definition{}, fmt.Errorf("role file %s: id %q is a built-in persona; choose a different id for a custom role", path, id)
	}
	def.ID = id
	def.Source = path
	return def, nil
}

func customRoleIDs(defs map[string]role.Definition) []string {
	ids := make([]string, 0, len(defs))
	for id := range defs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func customRoleSourceIsStaged(projectDir, source string) bool {
	source = strings.TrimSpace(source)
	if source == "" {
		return false
	}
	absSource, err := filepath.Abs(source)
	if err != nil {
		return false
	}
	absRolesDir, err := filepath.Abs(team.RolesDir(projectDir))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absRolesDir, absSource)
	if err != nil {
		return false
	}
	return rel != "." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".."
}

// stageCustomRoleDoc writes a custom role's authored document under
// <projectDir>/.amq-squad/roles/<id>.md. It is idempotent: identical content
// is left untouched. Returns true when it wrote (or rewrote) the file.
func stageCustomRoleDoc(projectDir string, def role.Definition) (bool, error) {
	path := team.CustomRolePath(projectDir, def.ID)
	body := def.Document()
	if existing, err := os.ReadFile(path); err == nil && string(existing) == body {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, fmt.Errorf("ensure roles dir: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return false, fmt.Errorf("write role doc: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return false, fmt.Errorf("rename role doc: %w", err)
	}
	return true, nil
}

func printSquadPlan(out io.Writer, personas []string, overrides map[string]string, customDefs map[string]role.Definition) {
	fmt.Fprintf(out, "  %-12s %-31s %-7s %s\n", "PERSONA", "PROFILE", "CLI", "SESSION")
	for _, raw := range personas {
		id := strings.TrimSpace(strings.ToLower(raw))
		if id == "" {
			continue
		}
		r := catalog.Lookup(id)
		if r == nil {
			label := "(custom role)"
			binary := ""
			if def, ok := customDefs[id]; ok {
				if strings.TrimSpace(def.Label) != "" {
					label = strings.TrimSpace(def.Label)
				}
				binary = strings.TrimSpace(def.Binary)
			}
			if b, ok := overrides[id]; ok {
				binary = b
			}
			fmt.Fprintf(out, "  %-12s %-31s %-7s %s\n", id, label, binary, id)
			continue
		}
		binary := r.PreferredBinary
		if b, ok := overrides[id]; ok {
			binary = b
		}
		fmt.Fprintf(out, "  %-12s %-31s %-7s %s\n", id, r.Label, binary, id)
	}
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// expandPath resolves a user-supplied path: expands a leading "~" or "~/"
// to the user's home directory, then makes the result absolute.
func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		if p == "~" {
			p = home
		} else {
			p = filepath.Join(home, p[2:])
		}
	}
	return filepath.Abs(p)
}

func parseKV(s string) (map[string]string, error) {
	out := map[string]string{}
	if s == "" {
		return out, nil
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 || eq == len(pair)-1 {
			return nil, fmt.Errorf("expected key=value, got %q", pair)
		}
		k := strings.TrimSpace(pair[:eq])
		v := strings.TrimSpace(pair[eq+1:])
		out[k] = v
	}
	return out, nil
}

func runTeamRules(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad team rules - manage .amq-squad/team-rules.md

Usage:
  amq-squad team rules show [--project DIR]
                                       Print team-rules.md
  amq-squad team rules init [--project DIR] [--profile NAME] [--template auto|dev-only|product-squad|scrum|custom] [--force]
                                       Seed or refresh team-rules.md
  amq-squad team rules templates
                                       List available templates

Examples:
  amq-squad team rules templates
  amq-squad team rules show
  amq-squad team rules show --project ~/Code/app
  amq-squad team rules init
  amq-squad team rules init --template product-squad
  amq-squad team rules init --profile codex-v2-5-0 --template auto --force
  amq-squad team rules init --project ~/Code/app
  amq-squad team rules init --force
`)
		if len(args) == 0 {
			return usageErrorf("rules requires a subcommand (e.g. 'show' or 'init')")
		}
		return nil
	}
	switch args[0] {
	case "templates":
		fs := flag.NewFlagSet("team rules templates", flag.ContinueOnError)
		fs.Usage = func() {
			fmt.Fprint(os.Stderr, `amq-squad team rules templates - list available team-rules templates

Usage:
  amq-squad team rules templates

Templates can be used with 'amq-squad team rules init --template NAME'.
`)
		}
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		for _, tmpl := range teamRulesTemplates {
			fmt.Fprintf(os.Stdout, "%-14s %s\n", tmpl.Name, tmpl.Description)
		}
		return nil
	case "show":
		fs := flag.NewFlagSet("team rules show", flag.ContinueOnError)
		projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
		fs.Usage = func() {
			fmt.Fprint(os.Stderr, `amq-squad team rules show - print .amq-squad/team-rules.md

Usage:
  amq-squad team rules show [--project DIR]

--project targets another team-home without changing directories.

Examples:
  amq-squad team rules show
  amq-squad team rules show --project ~/Code/app
`)
		}
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		ctx, err := resolveCanonicalContext(contextResolveOptions{
			ProjectFlag: *projectFlag, ProjectExplicit: flagWasSet(fs, "project"),
		})
		if err != nil {
			return err
		}
		emitContextDiagnostics(ctx)
		body, err := rules.Read(ctx.ProjectDir)
		if err != nil {
			if os.IsNotExist(err) {
				return fmt.Errorf("no team-rules.md at %s. Run 'amq-squad team rules init' first.", rules.Path(ctx.ProjectDir))
			}
			return fmt.Errorf("read team-rules.md: %w", err)
		}
		if _, err := fmt.Fprint(os.Stdout, body); err != nil {
			return err
		}
		if !strings.HasSuffix(body, "\n") {
			_, err = fmt.Fprintln(os.Stdout)
			return err
		}
		return nil
	case "init":
		fs := flag.NewFlagSet("team rules init", flag.ContinueOnError)
		force := fs.Bool("force", false, "overwrite an existing team-rules.md with the generated template")
		projectFlag := fs.String("project", "", "project/team-home directory to update (default: cwd)")
		profileFlag := fs.String("profile", "", "team profile to render when reading team.json (default: default)")
		templateFlag := fs.String("template", "auto", "team-rules template: auto, dev-only, product-squad, scrum, or custom")
		fs.Usage = func() {
			fmt.Fprint(os.Stderr, `amq-squad team rules init - seed or refresh .amq-squad/team-rules.md

Usage:
  amq-squad team rules init [--project DIR] [--profile NAME] [--template auto|dev-only|product-squad|scrum|custom] [--force]

--project targets another team-home without changing directories.
--profile renders a named team profile. team-rules.md is still shared per team-home.
--template auto selects dev-only, product-squad, scrum, or custom from the roster.
Custom templates and custom role scopes use the shared drafter resolver. The
generated fragment is structurally validated before it is wrapped in the
canonical policy sections and written. In-session fallback prints the filled
prompt and writes nothing.

Examples:
  amq-squad team rules init
  amq-squad team rules init --template dev-only
  amq-squad team rules init --profile codex-v2-5-0 --template auto --force
  amq-squad team rules init --project ~/Code/app
  amq-squad team rules init --force
`)
		}
		if err := parseFlags(fs, args[1:]); err != nil {
			return err
		}
		ctx, err := resolveCanonicalContext(contextResolveOptions{
			ProjectFlag: *projectFlag, ProfileFlag: *profileFlag,
			ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"),
		})
		if err != nil {
			return err
		}
		emitContextDiagnostics(ctx)
		projectDir, profile := ctx.ProjectDir, ctx.Profile
		if _, err := selectTeamRulesTemplate(*templateFlag, team.Team{}); err != nil {
			return err
		}
		content := rules.StubContent
		if team.ExistsProfile(projectDir, profile) {
			t, err := team.ReadProfile(projectDir, profile)
			if err != nil {
				return fmt.Errorf("read profile %q: %w", profile, err)
			}
			selectedTemplate, err := selectTeamRulesTemplate(*templateFlag, t)
			if err != nil {
				return err
			}
			if strings.TrimSpace(*templateFlag) == "" || strings.TrimSpace(*templateFlag) == "auto" {
				quietNotice("Selected team-rules template: %s\n", selectedTemplate)
			}
			var drafted *teamRulesProse
			_, rulesStatErr := os.Stat(rules.Path(projectDir))
			shouldDraft := *force || os.IsNotExist(rulesStatErr)
			if rulesStatErr != nil && !os.IsNotExist(rulesStatErr) {
				return fmt.Errorf("inspect team-rules.md: %w", rulesStatErr)
			}
			if teamRulesNeedsDraft(t, selectedTemplate) && shouldDraft {
				draft, err := draftTeamRulesProse(projectDir, selectedTemplate, t)
				if err != nil {
					return err
				}
				if draft.Manual {
					writeManualTeamRulesDraft(os.Stdout, draft)
					return nil
				}
				drafted = draft.Prose
				quietNotice("Drafter config source: %s\n", draft.ConfigSource)
				quietNotice("%s", cliDrafterAttemptsText(draft.Attempts, draft.Evidence))
			}
			rendered, err := renderTeamRulesWithTemplateDraft(t, selectedTemplate, drafted)
			if err != nil {
				return fmt.Errorf("render team-rules.md: %w", err)
			}
			content = rendered
		} else if flagWasSet(fs, "profile") {
			return fmt.Errorf("team profile %q not found at %s", profile, team.ProfilePath(projectDir, profile))
		}
		if *force {
			if err := rules.Write(projectDir, content); err != nil {
				return fmt.Errorf("write team-rules.md: %w", err)
			}
			quietNotice("Wrote %s\n", rules.Path(projectDir))
			return nil
		}
		wrote, err := rules.Ensure(projectDir, content)
		if err != nil {
			return fmt.Errorf("seed team-rules.md: %w", err)
		}
		if wrote {
			quietNotice("Wrote %s\n", rules.Path(projectDir))
		} else {
			quietNotice("%s already exists, leaving it alone.\n", rules.Path(projectDir))
		}
		return nil
	default:
		return unknownSubcommandError("team rules", args[0], "init")
	}
}

func runTeamSync(args []string) error {
	fs := flag.NewFlagSet("team sync", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "write the planned changes (default: preview only)")
	allowOutside := fs.Bool("allow-outside", false, "allow sync writes outside the team-home directory")
	projectFlag := fs.String("project", "", "project/team-home directory to sync (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile whose member cwds to sync (default: default profile)")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad team sync - sync CLAUDE.md and AGENTS.md from team-rules.md

Usage:
  amq-squad team sync [--project DIR]       Preview what would change (exit 1 if drift)
  amq-squad team sync --apply               Write the managed block into both files
  amq-squad team sync --profile NAME ...    Sync the named profile's member cwds only
  amq-squad team sync --apply --allow-outside
                                            Also write configured member cwds outside team-home

Existing content in CLAUDE.md / AGENTS.md is preserved. On first run,
existing content is adopted as the user region and a managed block is
appended between markers. Subsequent runs only refresh the managed block.

When team members span multiple directories, sync walks every unique cwd
in team.json and syncs CLAUDE.md + AGENTS.md in each. Use --allow-outside
when a member cwd is outside the team-home directory.

Examples:
  amq-squad team sync
  amq-squad team sync --project ~/Code/app --apply
  amq-squad team sync --apply
  amq-squad team sync --profile review --apply
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: *projectFlag, ProfileFlag: *profileFlag,
		ProjectExplicit: flagWasSet(fs, "project"), ProfileExplicit: flagWasSet(fs, "profile"),
	})
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	projectDir, profile := ctx.ProjectDir, ctx.Profile

	body, err := rules.Read(projectDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no team-rules.md at %s. Run 'amq-squad team rules init' first.", rules.Path(projectDir))
		}
		return err
	}

	// Walk every unique cwd the selected profile spans so each project's
	// CLAUDE.md and AGENTS.md picks up the managed block. Sync is profile-
	// scoped: --profile NAME walks that profile only; no flag walks the
	// default profile only. There is no all-profiles sync. An explicit
	// non-default --profile that does not resolve to an existing config is
	// a hard error (matching up/status/down/resume/fork); a plain
	// `team sync` without --profile preserves the legacy fallback of
	// syncing just team-home when no team.json is configured.
	explicitProfile := flagWasSet(fs, "profile") && profile != team.DefaultProfile
	if explicitProfile && !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	targetDirs := []string{projectDir}
	if team.ExistsProfile(projectDir, profile) {
		t, err := team.ReadProfile(projectDir, profile)
		if err != nil {
			return fmt.Errorf("read team: %w", err)
		}
		targetDirs, err = syncTargetDirs(projectDir, t.Members, *allowOutside)
		if err != nil {
			return err
		}
		// Default-profile sync keeps the legacy "always include team-home"
		// behavior so the project's root CLAUDE.md / AGENTS.md stay in
		// sync even when no member lives there. For an explicit non-default
		// profile, sync is scoped to that profile's member cwds exactly,
		// matching the locked Step 9A semantics.
		if !explicitProfile {
			targetDirs, err = ensureTeamHomeSyncTarget(targetDirs, projectDir)
			if err != nil {
				return err
			}
		}
	}

	var allPlans []rules.SyncPlan
	for _, dir := range targetDirs {
		plans, err := rules.Plan(dir, body)
		if err != nil {
			return fmt.Errorf("plan sync for %s: %w", dir, err)
		}
		allPlans = append(allPlans, plans...)
	}

	drift := false
	var currentDir string
	for _, p := range allPlans {
		dir := filepath.Dir(p.Target)
		if dir != currentDir {
			if currentDir != "" {
				fmt.Println()
			}
			fmt.Printf("# %s\n", dir)
			currentDir = dir
		}
		status := describePlan(p)
		fmt.Printf("  %-12s %s\n", p.Basename, status)
		if !p.Unchanged {
			drift = true
		}
	}

	if !*apply {
		if drift {
			quietNotice("\nPreview only. Re-run with --apply to write.\n")
			return fmt.Errorf("drift detected")
		}
		return nil
	}

	n, err := rules.Apply(allPlans)
	if err != nil {
		return err
	}
	quietNotice("Wrote %d file(s).\n", n)
	return nil
}

func containsString(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func syncTargetDirs(projectDir string, members []team.Member, allowOutside bool) ([]string, error) {
	home, err := canonicalDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve team-home: %w", err)
	}
	targets := uniqueMemberCWDs(home, members)
	if len(targets) == 0 {
		targets = []string{home}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(targets))
	for _, raw := range targets {
		dir, err := canonicalDir(raw)
		if err != nil {
			return nil, fmt.Errorf("resolve sync target %s: %w", raw, err)
		}
		if !allowOutside && !pathWithin(home, dir) {
			return nil, fmt.Errorf("sync target %s is outside team-home %s; pass --allow-outside to write there", dir, home)
		}
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out, nil
}

func ensureTeamHomeSyncTarget(targetDirs []string, projectDir string) ([]string, error) {
	homeDir, err := canonicalDir(projectDir)
	if err != nil {
		return nil, fmt.Errorf("resolve team-home: %w", err)
	}
	if containsString(targetDirs, homeDir) {
		return targetDirs, nil
	}
	out := append([]string(nil), targetDirs...)
	out = append(out, homeDir)
	sort.Strings(out)
	return out, nil
}

func canonicalDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", abs)
	}
	resolved, err := resolveExistingFilesystemPath(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func describePlan(p rules.SyncPlan) string {
	switch {
	case p.Unchanged:
		return "up to date"
	case p.Creating:
		return "will create"
	case p.Adopting:
		return "will adopt existing content and add managed block"
	default:
		return "will refresh managed block"
	}
}

// printTeamUsage writes to stderr, matching every other help block in this
// package (fmt.Fprint(os.Stderr, ...)) -- found via TestTeamSubcommandHelpDispatch
// (gh#762): this was the one help text still using fmt.Print (stdout).
func printTeamUsage() {
	fmt.Fprint(os.Stderr, `amq-squad team - manage this project's agent team

Usage:
  amq-squad team                      Print this usage
  amq-squad team init [options]       Pick personas, choose CLIs, and seed rules
  amq-squad team resume               Deleted (gh#758); there is no resume verb in 3.0
  amq-squad team rules init [--force] Seed or refresh team-rules.md
  amq-squad team operator set --mode MODE [options]
  amq-squad team operator self pause|resume --session S
                                      Configure the operator interaction mode
  amq-squad team sync [--apply] [--profile NAME]
                                      Sync CLAUDE.md and AGENTS.md from team-rules.md
                                      (default: preview; --apply writes; --profile
                                      scopes to that profile's member cwds)

Most subcommands accept --profile NAME to operate on a named profile under
.amq-squad/teams/<name>.json; omit the flag (or pass --profile default) to
operate on .amq-squad/team.json.

Personas come from the built-in catalog. Run 'amq-squad team init --help' to
see them and the available flags. Use --binary fullstack=codex to run a
persona with a different CLI.

Examples:
  amq-squad team init --roles cto,fullstack --binary cto=codex
  amq-squad team sync --apply
`)
}

// splitCommaList splits a comma-separated flag value, trimming blanks.
func splitCommaList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
