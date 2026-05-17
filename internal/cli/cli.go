// Package cli is the top-level command dispatcher for amq-squad.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
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

	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printUsage()
		return nil
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

func dispatch(args []string) error {
	switch args[0] {
	case "team":
		return runTeam(args[1:])
	case "up":
		return runUp(args[1:])
	case "down":
		return runDown(args[1:])
	case "status":
		return runStatus(args[1:])
	case "history":
		return runHistory(args[1:])
	case "resume":
		return runResume(args[1:])
	case "fork":
		return runFork(args[1:])
	case "launch":
		return runLaunch(args[1:])
	case "restore":
		return runRestore(args[1:])
	case "list":
		return runList(args[1:])
	case "completion":
		return runCompletion(args[1:])
	case "doctor":
		return runDoctor(args[1:])
	default:
		return usageErrorf("unknown command: %q. Run 'amq-squad --help' for usage.", args[0])
	}
}

func printUsage() {
	fmt.Print(`amq-squad - role-aware agent team launcher on top of AMQ

Usage:
  amq-squad <command> [options]

Commands:
  team      Pick your team once, then show or launch it on demand
  up        Bring the team up (use --dry-run to print the launch plan)
  down      Stop configured team members (currently --force only)
  status    Live state of this project's configured team
  history   List restorable launch records
  resume    Plan how to bring the team back into the resolved workstream
  fork      Plan fresh launches in a new workstream branched off an existing one
  launch    Launch a single agent with a role (called by 'team show' output)
  restore   Restore registered agents from local launch history
  list      List registered agents across known projects
  completion Emit a shell completion script (bash, zsh, fish)
  doctor    Check this project's amq-squad / AMQ setup
  version   Print the amq-squad version

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

Examples:
  amq-squad team init --roles cto,fullstack --binary cto=codex
  amq-squad up --dry-run --no-bootstrap
  amq-squad doctor --json | jq .

Run 'amq-squad <command> --help' for command-specific options.
`)
}
