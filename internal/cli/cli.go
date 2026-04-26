// Package cli is the top-level command dispatcher for amq-squad.
package cli

import "fmt"

// UsageError signals a misuse of the CLI; main prints it and exits 2.
type UsageError string

func (e UsageError) Error() string { return string(e) }

func usageErrorf(format string, args ...any) error {
	return UsageError(fmt.Sprintf(format, args...))
}

// Run dispatches to a subcommand.
func Run(args []string, version string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printUsage()
		return nil
	}
	if args[0] == "--version" || args[0] == "-v" || args[0] == "version" {
		fmt.Println("amq-squad", version)
		return nil
	}

	switch args[0] {
	case "team":
		return runTeam(args[1:])
	case "launch":
		return runLaunch(args[1:])
	case "restore":
		return runRestore(args[1:])
	case "list":
		return runList(args[1:])
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
  launch    Launch a single agent with a role (called by 'team show' output)
  restore   Restore registered agents from local launch history
  list      List registered agents across known projects
  version   Print the amq-squad version

Run 'amq-squad <command> --help' for command-specific options.
`)
}
