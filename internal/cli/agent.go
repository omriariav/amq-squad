package cli

import (
	"fmt"
	"os"
	"strings"
)

// runAgent dispatches the `agent` subgroup. In 1.0 it hosts the
// modern names for `launch <binary>` and `restore --exec --role R`,
// which keep working as deprecated aliases until 2.0.
func runAgent(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, agentUsage)
		if len(args) == 0 {
			return usageErrorf("agent requires a subcommand: 'up' or 'resume'")
		}
		return nil
	}
	switch args[0] {
	case "up":
		return runAgentUp(args[1:])
	case "resume":
		return runAgentResume(args[1:])
	default:
		return usageErrorf("unknown 'agent' subcommand: %q. Try 'up' or 'resume'.", args[0])
	}
}

const agentUsage = `amq-squad agent - launch or resume a single agent

Usage:
  amq-squad agent up <binary> [launch options]
  amq-squad agent resume <role> [restore options]

agent up is the modern name for 'amq-squad launch <binary>'.
agent resume is the modern name for 'amq-squad restore --exec --role <role>'.

Examples:
  amq-squad agent up codex --role cto --session issue-96
  amq-squad agent resume cto
`

// runAgentUp delegates to runLaunch verbatim. The binary positional and
// all launch flags are accepted; the modern verb is structural sugar.
func runAgentUp(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, `amq-squad agent up - launch a single agent with role metadata

Usage:
  amq-squad agent up <binary> [launch options] [-- <binary-flags>]

Equivalent to 'amq-squad launch <binary> ...'. See 'amq-squad launch --help'
for the full flag surface.

Examples:
  amq-squad agent up codex --role cto --session issue-96
  amq-squad agent up claude --no-bootstrap
`)
		return nil
	}
	// agent up syntax is `agent up <binary> [launch flags] [-- child args]`.
	// runLaunch's parser is still `launch [flags] <binary> [-- child]`, so
	// translate before delegating: lift the binary to after the launch
	// flags. This keeps the legacy launch parser untouched while letting
	// modern callers type flags after the binary.
	translated := translateAgentUpArgs(args)
	prev := launchFromAgentUp
	launchFromAgentUp = true
	defer func() { launchFromAgentUp = prev }()
	return runLaunch(translated)
}

// translateAgentUpArgs reorders `agent up <binary> [flags...] [-- child]`
// into the legacy `launch [flags...] <binary> [-- child]` shape so
// runLaunch's parser sees flags first. Inputs that do not start with a
// binary positional (e.g. `--help`, empty) are passed through unchanged
// so runLaunch reports the right error.
func translateAgentUpArgs(args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	binary := args[0]
	rest := args[1:]
	boundary := -1
	for i, a := range rest {
		if a == "--" {
			boundary = i
			break
		}
	}
	if boundary < 0 {
		out := append([]string{}, rest...)
		out = append(out, binary)
		return out
	}
	flags := rest[:boundary]
	child := rest[boundary:]
	out := append([]string{}, flags...)
	out = append(out, binary)
	out = append(out, child...)
	return out
}

// runAgentResume turns `agent resume <role> [extras]` into
// `restore --exec --role <role> [extras]`. The role is the only required
// positional; extras flow into restore unchanged so callers can still
// filter by --handle / --session / --conversation / --project.
func runAgentResume(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad agent resume - re-launch a saved agent by role

Usage:
  amq-squad agent resume <role> [restore options]

Equivalent to 'amq-squad restore --exec --role <role> ...'. Exactly one
saved record must match; on match, amq-squad changes to that record's cwd
and execs the saved launch through 'amq coop exec'.

Examples:
  amq-squad agent resume cto
  amq-squad agent resume fullstack --session issue-96
`)
		if len(args) == 0 {
			return usageErrorf("agent resume requires a role positional argument")
		}
		return nil
	}
	role := args[0]
	rest := args[1:]
	forwarded := append([]string{"--exec", "--role", role}, rest...)
	prev := restoreFromAgentResume
	restoreFromAgentResume = true
	defer func() { restoreFromAgentResume = prev }()
	return runRestore(forwarded)
}
