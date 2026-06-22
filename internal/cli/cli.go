// Package cli is the top-level command dispatcher for amq-squad.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// UsageError signals a misuse of the CLI (unknown command/flag, bad
// argument shape, missing required selector). main maps it to ExitUser
// (exit code 1) via cli.ExitCode.
type UsageError string

func (e UsageError) Error() string { return string(e) }

func usageErrorf(format string, args ...any) error {
	return UsageError(fmt.Sprintf(format, args...))
}

// Run dispatches to a subcommand. flag.ErrHelp from any --help path is
// swallowed so help output exits 0 across commands.
//
// Global output flags (--quiet, --verbose, --color) are peeled out of args
// before subcommand dispatch and stored on the package-level policy so any
// command can read them via outputPolicyCurrent. They may appear before or
// after the subcommand but never past a literal "--" boundary.
func Run(args []string, version string) error {
	args, policy, err := parseGlobalFlags(args)
	if err != nil {
		return err
	}
	prev := currentOutputPolicy
	currentOutputPolicy = policy
	defer func() { currentOutputPolicy = prev }()

	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help" || args[0] == "help") {
		printUsage()
		return nil
	}
	// BARE invocation (no args): in a configured project, run the status board
	// as the default command (docker-ps style). In an unconfigured project,
	// show a short guidance message — never the board, never a crash. Explicit
	// help paths above are unaffected.
	if len(args) == 0 {
		return runBareDefault()
	}
	if args[0] == "--version" || args[0] == "-v" {
		// Bare -v / --version stay text-only for backwards compat.
		fmt.Println("amq-squad", version)
		return nil
	}
	if args[0] == "version" {
		err := runVersion(version, args[1:])
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	err = dispatch(args, version)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

// runVersion prints the amq-squad version. With --json it emits a
// schema-versioned envelope on stdout; without --json it keeps the legacy
// human line so old scripts grep'ing for "amq-squad <version>" still work.
func runVersion(version string, args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned JSON envelope instead of the human version line")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad version - print the amq-squad version

Usage:
  amq-squad version [--json]

Examples:
  amq-squad version
  amq-squad version --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("version", versionEnvelopeData{Version: version})
	}
	fmt.Println("amq-squad", version)
	return nil
}

// versionEnvelopeData is the kind="version" payload.
type versionEnvelopeData struct {
	Version string `json:"version"`
}

// runBareDefault handles `amq-squad` with no arguments. In a project with ANY
// amq-squad footprint it runs the status board as the default command; only a
// genuinely empty project gets the short setup-guidance message. It never
// crashes, since bare invocation is now the most common front door.
func runBareDefault() error {
	cwd, err := os.Getwd()
	if err != nil {
		// Even getwd failing should not crash the bare front door with a stack
		// trace; surface guidance and exit cleanly.
		fmt.Println("amq-squad: could not determine the current directory; run 'amq-squad --help' for usage.")
		return nil
	}
	if !projectHasFootprint(cwd, scanBaseRootForProject) {
		fmt.Print(`amq-squad: no team is configured in this project.

Get started:
  amq-squad roles         List role IDs and market numbers
  amq-squad new team      Pick roles and create .amq-squad/team.json
  amq-squad --help        Show all commands

Once a team exists, bare 'amq-squad' shows a live board of your sessions.
`)
		return nil
	}
	return runStatusBoard(cwd, false)
}

// projectHasFootprint reports whether a project carries ANY amq-squad presence
// worth rendering the board for. The original gate ran the board only when the
// DEFAULT profile (team.json) existed, which wrongly steered projects that have
// ONLY named profiles (.amq-squad/teams/<name>.json, no team.json) into the
// "no team configured / run new team" guidance even though they have teams AND
// live sessions. A footprint is any of:
//
//   - a default-profile team.json (team.Exists), or
//   - one or more named profiles (team.ListProfiles returns >0), or
//   - discoverable sessions under the resolved AMQ base root.
//
// resolveBaseRoot is injected so this is unit-testable without a real `amq`; it
// is best-effort and any failure simply means "no discoverable sessions" rather
// than a hard error. The board itself degrades gracefully when empty, so a
// false positive here is harmless; the goal is to avoid the false NEGATIVE that
// hid real teams/sessions behind the setup guidance.
func projectHasFootprint(projectDir string, resolveBaseRoot func(string) (string, error)) bool {
	if team.Exists(projectDir) {
		return true
	}
	if profiles, err := team.ListProfiles(projectDir); err == nil && len(profiles) > 0 {
		return true
	}
	return projectHasDiscoverableSessions(projectDir, resolveBaseRoot)
}

// projectHasDiscoverableSessions reports whether the resolved AMQ base root has
// at least one session directory containing an agents/ child (the layout the
// board scans). It is best-effort: an unresolvable base root or an unreadable
// directory means "no discoverable sessions", never an error.
func projectHasDiscoverableSessions(projectDir string, resolveBaseRoot func(string) (string, error)) bool {
	if resolveBaseRoot == nil {
		return false
	}
	baseRoot, err := resolveBaseRoot(projectDir)
	if err != nil || strings.TrimSpace(baseRoot) == "" {
		return false
	}
	entries, err := os.ReadDir(baseRoot)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		agentsDir := filepath.Join(baseRoot, e.Name(), "agents")
		if info, err := os.Stat(agentsDir); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}

func dispatch(args []string, version string) error {
	switch args[0] {
	case "team":
		return runTeam(args[1:])
	case "lead":
		return runLead(args[1:])
	case "task":
		return runTask(args[1:])
	case "verify":
		return runVerify(args[1:])
	case "new":
		return runNew(args[1:])
	case "roles":
		return runRoles(args[1:])
	case "up":
		return runUp(args[1:])
	case "stop":
		return runStop(args[1:])
	case "brief":
		return runBrief(args[1:])
	case "threads":
		return runThreads(args[1:])
	case "thread":
		return runThread(args[1:])
	case "status":
		return runStatus(args[1:])
	case "focus", "open":
		return runFocus(args[1:])
	case "send":
		return runSend(args[1:])
	case "dispatch":
		return runDispatch(args[1:])
	case "collect":
		return runCollect(args[1:])
	case "prune-panes":
		return runPrunePanes(args[1:])
	case "console":
		return runConsole(args[1:])
	case "notify":
		return runNotify(args[1:])
	case "amq":
		return runAMQ(args[1:])
	case "history":
		return runHistory(args[1:])
	case "resume":
		return runResume(args[1:])
	case "fork":
		return runFork(args[1:])
	case "rm":
		return runRm(args[1:], rmModeDelete)
	case "archive":
		return runRm(args[1:], rmModeArchive)
	case "completion":
		return runCompletion(args[1:])
	case "doctor":
		return runDoctor(args[1:], version)
	case "agent":
		return runAgent(args[1:])
	default:
		return usageErrorf("unknown command: %q. Run 'amq-squad --help' for usage.", args[0])
	}
}

func printUsage() {
	fmt.Print(`amq-squad - role-aware agent team launcher on top of AMQ

Usage:
  amq-squad <command> [options]

Commands:
  new       Create a team, named profile, or workstream session
  roles     List built-in role IDs and market numbers for team creation
  team      Set up and manage the team (init, rules, lead, member, sync, profiles)
  lead      Register or inspect an external orchestrator lead
  task      Native pull-based task store (add/list/claim/done/fail/block)
  verify    Deterministic preflight checks (verify merge)
  up        Bring the team up (use --dry-run to print the launch plan)
  stop      Stop configured team members (SIGTERM; --force = SIGKILL). State is
            preserved on disk, so the session stays resumable.
  brief     Print a workstream brief and classify it as none, stub, or real
  threads   List collapsed AMQ thread summaries for one workstream
  thread    Read one AMQ thread transcript by project and session
  status    Multi-session board (also bare 'amq-squad'); --project and --session for detail
  collect   Drain once, optionally wait once for a report, then drain once
  prune-panes Reclaim orphaned amq-squad tmux panes (confirm-gated)
  console   Read-only Mission Control TUI over all sessions (--once for CI)
  notify    Emit de-duplicated operator attention notifications
  amq       Project-aware AMQ diagnostics and confirm-gated maintenance
  history   List restorable launch records
  resume    Plan how to bring the team back into the resolved workstream
  fork      Plan fresh launches in a new workstream branched off an existing one
  rm        Permanently remove a finished session (root dir + brief; confirm-gated)
  archive   Move a finished session aside instead of deleting (confirm-gated)
  completion Emit a shell completion script (bash, zsh, fish)
  doctor    Check amq-squad / AMQ setup (use --project and --profile for other teams)
  agent     Launch or resume a single agent (agent up / agent resume)
  version   Print the amq-squad version

Removed in 2.0 (see MIGRATION.md): down (use 'stop'), launch (use 'agent up'),
restore (use 'history' or 'agent resume'), list (use 'status' or 'history'),
team show (use 'up --dry-run'), team launch (use 'up').

Global flags (accepted before or after the subcommand, until a literal "--"):
  --quiet              Suppress non-data success/progress notices.
  --verbose            Print additional diagnostic detail.
  --color auto|always|never
                       Control ANSI color output (default auto; honors NO_COLOR).

Exit codes:
  0  success
  1  usage / user error (unknown flag, bad argument, missing required input)
  2  system / runtime error (IO, process, config, environment)
  3  partial success (some targets succeeded, some failed)

Note: 'stop' performs the SIGTERM teardown and exits 0 (or 3 on a partial run).

Examples:
  amq-squad new team --roles cto,fullstack --binary cto=codex
  amq-squad new profile review --roles cto,qa
  amq-squad roles
  amq-squad new session issue-96
  amq-squad brief --session issue-96
  amq-squad verify merge --evidence evidence.json
  amq-squad team init --roles cto,fullstack --binary cto=codex
  amq-squad up --dry-run --no-bootstrap
  amq-squad notify --project ~/Code/app
  amq-squad stop --project ~/Code/app --all --session issue-96
  amq-squad amq route --session issue-96 --me cto --to fullstack
  amq-squad rm issue-96 --yes
  amq-squad doctor --project ~/Code/app --profile review --json | jq .

Run 'amq-squad <command> --help' for command-specific options.
`)
}
