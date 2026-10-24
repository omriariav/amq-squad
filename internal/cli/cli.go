// Package cli is the top-level command dispatcher for amq-squad.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

// UsageError signals a misuse of the CLI (unknown command/flag, bad
// argument shape, missing required selector). main maps it to ExitUser
// (exit code 1) via cli.ExitCode.
type UsageError string

func (e UsageError) Error() string { return string(e) }

func usageErrorf(format string, args ...any) error {
	return UsageError(fmt.Sprintf(format, args...))
}

// unknownSubcommandError is the single human- and machine-readable contract
// for commands that reject a subcommand. Callers must supply the complete,
// ordered set of valid subcommands; accepting an empty or malformed set would
// make the recovery text authoritative but false (#561).
func unknownSubcommandError(verb, given string, valid ...string) error {
	verb = strings.TrimSpace(verb)
	if verb == "" {
		panic("unknownSubcommandError: verb must not be empty")
	}
	if len(valid) == 0 {
		panic("unknownSubcommandError: valid subcommands must not be empty")
	}

	quoted := make([]string, len(valid))
	seen := make(map[string]struct{}, len(valid))
	for i, subcommand := range valid {
		subcommand = strings.TrimSpace(subcommand)
		if subcommand == "" {
			panic("unknownSubcommandError: valid subcommand must not be empty")
		}
		if _, duplicate := seen[subcommand]; duplicate {
			panic(fmt.Sprintf("unknownSubcommandError: duplicate valid subcommand %q", subcommand))
		}
		seen[subcommand] = struct{}{}
		quoted[i] = fmt.Sprintf("'%s'", subcommand)
	}

	var recovery string
	switch len(quoted) {
	case 1:
		recovery = quoted[0]
	case 2:
		recovery = quoted[0] + " or " + quoted[1]
	default:
		recovery = strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
	}
	return usageErrorf("unknown '%s' subcommand: %q. Try %s.", verb, given, recovery)
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
	// BARE invocation (no args) prints the usage. 3.0: the v2 multi-session
	// board moved to amq-noc (manifest C3), so there is no board default.
	if len(args) == 0 {
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

func dispatch(args []string, version string) error {
	cmd, ok := lookupCommand(args[0], version)
	if !ok {
		return usageErrorf("unknown command: %q. Run 'amq-squad --help' for usage.", args[0])
	}
	return cmd.Run(args[1:])
}

func printUsage() {
	fmt.Print(`amq-squad - role-aware agent team launcher on top of AMQ

Usage:
  amq-squad <command> [options]

Commands:
`)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, cmd := range commandCatalog {
		fmt.Fprintf(w, "  %s\t%s\n", cmd.Name, cmd.Summary)
	}
	fmt.Fprintln(w, "  version\tPrint the amq-squad version")
	_ = w.Flush()
	fmt.Print(`
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
  amq-squad roles
  amq-squad team propose --roles cto,fullstack --runtime fullstack=codex --lead cto
  amq-squad team check
  amq-squad status --project ~/Code/app --session issue-435 --json
  amq-squad dispatch --session issue-96 --role qa --subject "Validate" --body-file task.md
  amq-squad task list --session issue-96
  amq-squad context --me cto --json
  amq-squad guard --me cto --stdin --json < pretooluse.json
  amq-squad doctor --project ~/Code/app --json | jq .
  amq-squad trust ~/Code/app

Run 'amq-squad <command> --help' for command-specific options.
`)
}
