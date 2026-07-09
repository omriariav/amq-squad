package cli

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
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
	model := fs.String("model", "", "model to pass to the agent (e.g. claude-opus-4-8, gpt-5)")
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
	// passthrough (effort etc. rides here, consistent with --codex-args /
	// --claude-args elsewhere; amq-squad has no first-class --effort flag).
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
      [--lead-mode builder|planner]
      [--codex-args "..."] [--claude-args "..."]
      [--visibility detached|sibling-tabs|current] [--goal TEXT] [--seed-from REF] [--go]

Managed model: amq-squad spawns the whole team (incl. the lead); panes are
registered and wake-live automatically. This wraps the create sequence so the
--project/--profile/--session namespace is typed once:

    new team (if --roles) -> up --visibility <mode>

Visibility defaults to detached (hidden): agents run in a separate tmux session
you don't see, and you supervise via status/console/monitor + wake, attaching
only to intervene. Pass --visibility sibling-tabs for visible tabs (requires a
visible tmux pane). Choose binary via --binary, model via --model, and effort
via --codex-args/--claude-args (amq-squad has no first-class --effort flag).

Preview by default (prints the plan and runs read-only --dry-run validation, so
its failures surface honestly); pass --go to create for real.

External-lead mode (your current pane IS the lead) is not yet supported here;
see issue #339.
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
	fs := flag.NewFlagSet("run start", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project / team-home directory (repo root)")
	fs.StringVar(projectFlag, "p", "", "alias for --project")
	sessionFlag := fs.String("session", "", "workstream session name")
	fs.StringVar(sessionFlag, "s", "", "alias for --session")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	fs.StringVar(profileFlag, "P", "", "alias for --profile")
	leadFlag := fs.String("lead", "", "lead role (default: cto when creating a roster; else inferred from the profile)")
	leadModeFlag := fs.String("lead-mode", "", "lead implementation posture when creating a roster: builder (default) or planner")
	rolesFlag := fs.String("roles", "", "create the roster first: comma-separated role ids")
	binaryFlag := fs.String("binary", "", "per-role binary assignments, e.g. \"fullstack=codex,qa=codex\"")
	modelFlag := fs.String("model", "", "per-role model overrides, e.g. \"cto=gpt-5,fullstack=sonnet\"")
	codexArgsFlag := fs.String("codex-args", "", "extra args for every Codex member (e.g. reasoning effort)")
	claudeArgsFlag := fs.String("claude-args", "", "extra args for every Claude member")
	visibilityFlag := fs.String("visibility", visibilityDetached, "spawn topology: detached (hidden, default), sibling-tabs (visible tabs), or current")
	goalFlag := fs.String("goal", "", "after spawn, deliver this goal to the lead")
	seedFlag := fs.String("seed-from", "", "seed the workstream brief from a reference (e.g. issue:96)")
	externalLead := fs.Bool("external-lead", false, "(unsupported yet, see #339) your current pane is the lead")
	goFlag := fs.Bool("go", false, "create for real (default: preview only)")
	fs.Usage = func() { _ = runRunCmd([]string{"-h"}, version) }
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	if *externalLead {
		return usageErrorf("external-lead mode is not yet supported (see #339); pane binding must be verified against the CLI before it ships. Use the managed model for now.")
	}
	project := strings.TrimSpace(*projectFlag)
	session := strings.TrimSpace(*sessionFlag)
	if project == "" {
		return usageErrorf("run start requires --project (-p)")
	}
	if session == "" {
		return usageErrorf("run start requires --session (-s)")
	}
	if err := team.ValidateSessionName(session); err != nil {
		return usageErrorf("invalid --session: %v", err)
	}
	if info, err := os.Stat(project); err != nil || !info.IsDir() {
		return usageErrorf("project directory does not exist: %s", project)
	}
	visibility, err := normalizeLaunchVisibility(*visibilityFlag)
	if err != nil {
		return usageErrorf("%v", err)
	}
	if visibility == visibilityPlan {
		return usageErrorf("--visibility plan is not valid for run start; it previews by default and creates with --go")
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	if err := ensureNoNamespaceCreationCollision("run start", project, profile, session); err != nil {
		return err
	}
	teamPresent := teamExistsForProfile(project, *profileFlag)
	rolesText := strings.TrimSpace(*rolesFlag)
	freshRoster := rolesText != "" && !teamPresent
	if err := ensureRunStartExistingProfileMatchesSession(project, *profileFlag, session, *rolesFlag, teamPresent, freshRoster); err != nil {
		return err
	}
	leadMode, err := normalizeLeadMode(*leadModeFlag)
	if err != nil {
		return err
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
		newTeamArgs = appendPassthroughArgs(newTeamArgs, *modelFlag, *codexArgsFlag, *claudeArgsFlag)
	}
	upArgs := []string{session, "--project", project, "--visibility", visibility}
	if strings.TrimSpace(*profileFlag) != "" {
		upArgs = append(upArgs, "--profile", *profileFlag)
	}
	if strings.TrimSpace(*seedFlag) != "" {
		upArgs = append(upArgs, "--seed-from", *seedFlag)
	}
	upArgs = appendPassthroughArgs(upArgs, *modelFlag, *codexArgsFlag, *claudeArgsFlag)

	if len(newTeamArgs) == 0 && !teamPresent {
		return usageErrorf("no team profile %q in %s and no --roles given; pass --roles to create one or create the team first", profileOrDefault(*profileFlag), project)
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
	if flagWasSet(fs, "lead-mode") && !freshRoster {
		return usageErrorf("--lead-mode applies only when run start creates a new roster; for an existing profile use `amq-squad team lead set <role> --lead-mode %s` first", leadMode)
	}

	leadDisplay := explicitLead
	if explicitLead == "" {
		if freshRoster {
			leadDisplay = leadForNewTeam
		} else {
			leadDisplay = "(inferred from profile)"
		}
	}
	upStep := 1
	if freshRoster {
		upStep = 2
	}
	fmt.Printf("orchestrated run (managed model)\n")
	fmt.Printf("  project: %s\n", project)
	fmt.Printf("  profile: %s\n", profileOrDefault(*profileFlag))
	fmt.Printf("  session: %s\n", session)
	fmt.Printf("  lead:    %s\n", leadDisplay)
	fmt.Printf("  lead-mode: %s\n", leadModeDisplay)
	if freshRoster {
		leadModeSuffix := ""
		if flagWasSet(fs, "lead-mode") {
			leadModeSuffix = " --lead-mode " + leadMode
		}
		fmt.Printf("  step 1:  amq-squad new team --roles %s --orchestrated --lead %s%s\n", *rolesFlag, leadForNewTeam, leadModeSuffix)
	} else if len(newTeamArgs) > 0 {
		fmt.Printf("  note:    profile %s already exists; --roles ignored, using the existing roster\n", profileOrDefault(*profileFlag))
	}
	fmt.Printf("  step %d:  amq-squad up %s --visibility %s\n", upStep, session, visibility)
	if visibility == visibilityDetached {
		fmt.Printf("  (hidden: agents run in a detached tmux session; attach via the `attach_control` action from `status --json`, or `amq-squad focus`, when you want eyes on them)\n")
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

	if !*goFlag {
		return runStartPreview(newTeamArgs, upArgs, freshRoster, teamPresent, version)
	}

	if (visibility == visibilitySiblingTabs || visibility == visibilityCurrent) && !insideTmux() {
		return usageErrorf("not inside tmux; --visibility %s requires a visible tmux pane. Use --visibility detached to spawn hidden, or attach a tmux session first.", visibility)
	}

	// 1) roster
	if freshRoster {
		quietNotice("creating roster...\n")
		if err := runNew(newTeamArgs); err != nil {
			return err
		}
	} else if len(newTeamArgs) > 0 {
		quietNotice("profile %q already exists; skipping new team (using existing roster)\n", profileOrDefault(*profileFlag))
	}
	// 2) spawn
	quietNotice("spawning team (--visibility %s)...\n", visibility)
	if err := runStartUpWithVersion(upArgs, version); err != nil {
		return err
	}
	// 3) optional goal delivery. Resolve the role before waiting so the
	// fallback command is exact and ready to paste if the cold spawn never
	// reaches a deliverable pane.
	if strings.TrimSpace(*goalFlag) != "" {
		leadRole, err := resolveRunStartGoalLead(project, profile, explicitLead, freshRoster, leadForNewTeam)
		if err != nil {
			return err
		}
		opts := runStartGoalDeliveryOptions{
			Project: project,
			Profile: profile,
			Session: session,
			Role:    leadRole,
			Goal:    *goalFlag,
			Version: version,
		}
		quietNotice("waiting for lead readiness before goal delivery...\n")
		if err := deliverRunStartGoalWhenReady(opts); err != nil {
			return err
		}
	}
	quietNotice("done. Attach to the lead window and drive with dispatch/monitor/collect.\n")
	return nil
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
		fmt.Print("\nPreview OK. Re-run with --go to create it.\n")
		if seeded {
			fmt.Print("(the --seed-from brief is written at --go; preview validated the roster/session without it.)\n")
		}
		return nil
	}
	fmt.Print("\nRoster plan validated. Spawn (up) validation is deferred: the team does\n" +
		"not exist yet, so `up --dry-run` cannot check the roster in preview.\n" +
		"Re-run with --go to create the team and spawn.\n")
	return nil
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

func ensureRunStartExistingProfileMatchesSession(project, profile, session, roles string, teamPresent, freshRoster bool) error {
	if !teamPresent || freshRoster {
		return nil
	}
	profileName := profileOrDefault(profile)
	t, err := team.ReadProfile(project, profileName)
	if err != nil {
		return fmt.Errorf("read team profile %q: %w", profileName, err)
	}
	active, skipped := filterMembersBySession(t.Members, session)
	if len(active) > 0 || len(t.Members) == 0 || len(skipped) == 0 {
		return nil
	}
	pins := pinnedSessionList(skipped)
	pinned := strings.Join(pins, ", ")
	if len(pins) == 1 {
		pinned = pins[0]
	}
	var b strings.Builder
	fmt.Fprintf(&b, "run start refused: profile %q in %s is pinned to workstream %s, not %q; no team members would run for the requested session.\n", profileName, project, pinned, session)
	if strings.TrimSpace(roles) != "" {
		fmt.Fprintf(&b, "--roles %q would be ignored because profile %q already exists; run start uses the existing roster instead of replacing it.\n", roles, profileName)
	}
	firstPinned := pins[0]
	fmt.Fprintf(&b, "Fixes:\n")
	fmt.Fprintf(&b, "  - run the existing roster on its pinned session: amq-squad run start --project %s%s --session %s\n", shellQuote(project), runStartProfileFixArg(profile), shellQuote(firstPinned))
	fmt.Fprintf(&b, "  - create a session-pinned roster under a named profile: amq-squad run start --project %s --profile <name> --session %s --roles %s\n", shellQuote(project), shellQuote(session), runStartRolesFixArg(roles))
	return usageErrorf("%s", strings.TrimRight(b.String(), "\n"))
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
