// Package cli is the top-level command dispatcher for amq-squad.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"

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

	err = dispatch(args)
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

// runBareDefault handles `amq-squad` with no arguments. In a configured
// project (a team.json exists for the default profile) it runs the status
// board as the default command. In an unconfigured project it prints a short
// guidance message pointing at setup — it never renders the board and never
// crashes, since bare invocation is now the most common front door.
func runBareDefault() error {
	cwd, err := os.Getwd()
	if err != nil {
		// Even getwd failing should not crash the bare front door with a stack
		// trace; surface guidance and exit cleanly.
		fmt.Println("amq-squad: could not determine the current directory; run 'amq-squad --help' for usage.")
		return nil
	}
	if !team.Exists(cwd) {
		fmt.Print(`amq-squad: no team is configured in this project.

Get started:
  amq-squad team init     Pick roles and create .amq-squad/team.json
  amq-squad --help        Show all commands

Once a team exists, bare 'amq-squad' shows a live board of your sessions.
`)
		return nil
	}
	return runStatusBoard(cwd, false)
}

func dispatch(args []string) error {
	switch args[0] {
	case "team":
		return runTeam(args[1:])
	case "up":
		return runUp(args[1:])
	case "stop":
		return runStop(args[1:])
	case "down":
		// Deprecated alias for `stop`, kept for one release. runDown prints a
		// one-line stderr hint then runs the identical stop logic.
		return runDown(args[1:])
	case "status":
		return runStatus(args[1:])
	case "console":
		return runConsole(args[1:])
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
	case "launch":
		// Removed in 2.0. Kept as an explicit hint (not unknown-command) for
		// one release so muscle-memory invocations get a pointer.
		return usageErrorf("'launch' was removed in 2.0; use 'agent up <binary>' to launch a single agent.")
	case "restore":
		// Removed in 2.0. Print mode mapped to 'history'; exec mode to
		// 'agent resume <role>'. Surface both so either intent is covered.
		return usageErrorf("'restore' was removed in 2.0; use 'history' to list restorable records or 'agent resume <role>' to re-launch one.")
	case "list":
		// Removed in 2.0 in favor of 'status' (live agents) / 'history'
		// (restorable records).
		return usageErrorf("'list' was removed in 2.0; use 'status' for live agents or 'history' for restorable records.")
	case "completion":
		return runCompletion(args[1:])
	case "doctor":
		return runDoctor(args[1:])
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
  team      Set up and manage the team (init, rules, sync, profiles)
  up        Bring the team up (use --dry-run to print the launch plan)
  stop      Stop configured team members (SIGTERM; --force = SIGKILL). State is
            preserved on disk, so the session stays resumable.
  down      Deprecated alias for 'stop' (works for one release)
  status    Multi-session board (also the bare 'amq-squad'); --session for detail
  console   Read-only Mission Control TUI over all sessions (--once for CI)
  history   List restorable launch records
  resume    Plan how to bring the team back into the resolved workstream
  fork      Plan fresh launches in a new workstream branched off an existing one
  rm        Permanently remove a finished session (root dir + brief; confirm-gated)
  archive   Move a finished session aside instead of deleting (confirm-gated)
  completion Emit a shell completion script (bash, zsh, fish)
  doctor    Check this project's amq-squad / AMQ setup
  agent     Launch or resume a single agent (agent up / agent resume)
  version   Print the amq-squad version

Removed in 2.0 (each prints a one-line migration hint): launch (use 'agent up'),
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

Note: 'stop'/'down' without --force used to exit 2 ("graceful unavailable").
They now perform the SIGTERM teardown and exit 0 (or 3 on a partial run).

Examples:
  amq-squad team init --roles cto,fullstack --binary cto=codex
  amq-squad up --dry-run --no-bootstrap
  amq-squad rm issue-96 --yes
  amq-squad doctor --json | jq .

Run 'amq-squad <command> --help' for command-specific options.
`)
}
