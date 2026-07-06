package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

// runAgent dispatches the `agent` subgroup: `agent up <binary>` launches a
// single agent and `agent resume <role>` re-launches a saved one. These are
// the canonical single-agent verbs in 2.0 (the legacy top-level `launch` and
// `restore` verbs were removed).
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
  amq-squad agent resume <role> [--project dir1,dir2,...] [restore options]

agent up launches a single agent with role metadata.
agent resume re-launches a saved agent by role from local launch history.

Examples:
  amq-squad agent up codex --role cto --session issue-96
  amq-squad agent resume cto
`

// runAgentUp is the single-agent launcher front door. The binary positional
// and all launch flags are accepted; it translates the modern flags-after-
// binary shape and delegates to the shared launcher body (runLaunch).
func runAgentUp(args []string) error {
	if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(os.Stderr, `amq-squad agent up - launch a single agent with role metadata

Usage:
  amq-squad agent up <binary> [--project DIR] [launch options] [-- <binary-flags>]

Run 'amq-squad agent up <binary> --help' for the full flag surface.

Examples:
  amq-squad agent up codex --role cto --session issue-96
  amq-squad agent up codex --project ~/Code/app --session issue-96
  amq-squad agent up claude --no-bootstrap
`)
		return nil
	}
	// Default the agent handle to its --role before translating, so a hand-typed
	// `agent up <binary> --role R` (no --me) routes to the role's mailbox
	// instead of the binary basename (see defaultMeFromRole).
	args = defaultMeFromRole(args)
	// agent up syntax is `agent up <binary> [launch flags] [-- child args]`.
	// runLaunch's parser expects `[flags] <binary> [-- child]`, so translate
	// before delegating: lift the binary to after the launch flags. This lets
	// modern callers type flags after the binary while runLaunch's flag
	// parser still sees flags first.
	translated := translateAgentUpArgs(args)
	return runLaunch(translated)
}

// defaultMeFromRole makes `agent up <binary> --role R` (no --me) route to the
// role's mailbox instead of the binary basename. Without it, every same-binary
// agent shares one handle (claude/codex), so peer reports and gate replies
// misroute — a real footgun the first 2.0 dogfood hit. The team-launch and
// resume paths already pass an explicit --me per member; this closes the gap on
// the direct single-agent front door (and the skill pairs --role with --me, so
// this only backstops a hand-typed launch).
//
// It operates on agent-up-shaped args (binary positional first, launch flags
// after, child args behind a `--`). Only the launch-flag region is inspected;
// the child block is never touched. The derived handle must be a valid slug
// handle (same rule as roster handles) — an exotic --role keeps the old
// binary-basename default rather than synthesizing an invalid handle.
func defaultMeFromRole(args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	binary := args[0]
	rest := args[1:]
	// Read --role/--me ONLY from the genuine launch-flag region. Using the same
	// boundary as translateAgentUpArgs is what keeps a CHILD `--role` (e.g.
	// `agent up codex "prompt" --role x`, where the prompt positional already
	// opened the child block) from being mistaken for a launch role.
	end, _ := agentUpFlagsEnd(rest)
	role, haveMe := agentUpRoleAndMe(rest[:end])
	if haveMe || role == "" {
		return args
	}
	handle := strings.ToLower(strings.TrimSpace(role))
	if team.ValidateHandle(handle) != nil {
		return args
	}
	out := make([]string, 0, len(args)+2)
	out = append(out, binary, "--me", handle)
	out = append(out, rest...)
	return out
}

// agentUpRoleAndMe scans a slice of launch flags (the region BEFORE the child
// boundary, as returned by agentUpFlagsEnd) for the agent's --role value and
// whether --me was given. Within this region every value-consuming flag has its
// value present, so a name match is reliable; the default branch skips other
// flags' value tokens so a value can never be read as --role/--me.
func agentUpRoleAndMe(flags []string) (role string, haveMe bool) {
	for i := 0; i < len(flags); i++ {
		a := flags[i]
		name := a
		hasEq := strings.Contains(a, "=")
		if hasEq {
			name = a[:strings.IndexByte(a, '=')]
		}
		switch name {
		case "--me", "-me":
			haveMe = true
			if !hasEq && i+1 < len(flags) {
				i++ // skip the handle value token
			}
		case "--role", "-role":
			if hasEq {
				role = a[strings.IndexByte(a, '=')+1:]
			} else if i+1 < len(flags) && !strings.HasPrefix(flags[i+1], "-") {
				role = flags[i+1]
				i++
			}
		default:
			if !hasEq {
				switch launchKnownFlag(name) {
				case "string", "string-accepts-dash":
					if i+1 < len(flags) {
						i++
					}
				}
			}
		}
	}
	return role, haveMe
}

// agentUpFlagsEnd finds where the launch-flag region of `agent up <binary>
// [flags...] [-- child]` ends and the child block begins, given rest = the args
// AFTER the binary positional. It is the single source of truth for that split,
// shared by translateAgentUpArgs (which reorders around it) and
// defaultMeFromRole (which reads --role/--me from rest[:end]).
//
//   - An explicit `--` boundary wins: end is its index, explicitDashDash=true,
//     and rest[end:] (which still starts with `--`) is the child block verbatim.
//   - Otherwise the first unrecognized token (a bare positional or an unknown
//     `-...` flag, or a bare string flag with no usable value) opens the child
//     block; recognized launch flags and their values are consumed first.
func agentUpFlagsEnd(rest []string) (end int, explicitDashDash bool) {
	for i, a := range rest {
		if a == "--" {
			return i, true
		}
	}
	end = len(rest)
walk:
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if !strings.HasPrefix(a, "-") {
			// Bare positional after the binary: child boundary.
			end = i
			break walk
		}
		// `--flag=value` syntax: split and check name.
		name := a
		hasEq := strings.Contains(a, "=")
		if hasEq {
			name = a[:strings.IndexByte(a, '=')]
		}
		switch launchKnownFlag(name) {
		case "string":
			if hasEq {
				continue
			}
			// Bare `--role` is only a complete launch flag if the next token is
			// a non-flag value. If the next token is missing or dash-prefixed,
			// that token is part of the child block (legacy `launch` parses it
			// that way too because the binary positional came first).
			if i+1 >= len(rest) || strings.HasPrefix(rest[i+1], "-") {
				end = i
				break walk
			}
			i++ // consume value token
		case "string-accepts-dash":
			if hasEq {
				continue
			}
			// `--codex-args` / `--claude-args` carry native child-flag strings
			// that commonly start with `-` (e.g. `--enable goals`, `--chrome`);
			// always consume the next token as the value when present.
			if i+1 >= len(rest) {
				end = i
				break walk
			}
			i++ // consume value token (may start with `-`)
		case "bool":
			continue
		default:
			// Unknown `-...` token: child block starts here.
			end = i
			break walk
		}
	}
	return end, false
}

// translateAgentUpArgs reorders `agent up <binary> [flags...] [-- child]`
// into the legacy `launch [flags...] <binary> [-- child]` shape so
// runLaunch's parser sees flags first. Inputs that do not start with a
// binary positional (e.g. `--help`, empty) are passed through unchanged
// so runLaunch reports the right error.
//
// When the caller omits the `--` boundary, this function preserves
// behavior parity with legacy `launch [flags...] <binary> <child...>`:
// only recognized launch flags (and their values) after `<binary>` are
// kept as launch flags. The first unrecognized token (whether a non-flag
// positional or an unknown `-...` flag) becomes the implicit child
// boundary, and the synthesized `--` is inserted before it so native
// child flags like `--foo` do not collide with runLaunch's flag parser.
func translateAgentUpArgs(args []string) []string {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return args
	}
	binary := args[0]
	rest := args[1:]

	// Split launch flags from the child block via the shared boundary helper.
	end, explicitDashDash := agentUpFlagsEnd(rest)
	flags := rest[:end]
	child := rest[end:]
	out := append([]string{}, flags...)
	out = append(out, binary)
	if explicitDashDash {
		// child still starts with the explicit `--`; keep it verbatim.
		out = append(out, child...)
	} else if len(child) > 0 {
		// implicit boundary: synthesize `--` so native child flags like
		// `--foo` do not collide with runLaunch's flag parser.
		out = append(out, "--")
		out = append(out, child...)
	}
	return out
}

// launchKnownFlag classifies a token as a launch flag for translation.
//
//   - "string"              : value-consuming flag whose value is a normal
//     (non-dash-prefixed) token. Bare or
//     dash-followed forms terminate the launch
//     region for child-arg parity with legacy
//     launch.
//   - "string-accepts-dash" : value-consuming flag documented to carry
//     native child-arg strings (often starting
//     with `-`), e.g. --codex-args / --claude-args.
//   - "bool"                : flag-only.
//   - ""                    : unknown.
//
// Mirrors the fs.String / fs.Bool registrations in launch.go so `agent up`
// can split launch flags from native child flags when no `--` boundary is
// given.
func launchKnownFlag(name string) string {
	switch name {
	case "--codex-args", "--claude-args", "--launcher-args",
		"--wake-inject-arg",
		"-codex-args", "-claude-args", "-launcher-args",
		"-wake-inject-arg":
		return "string-accepts-dash"
	case "--role", "--session", "--me", "--root", "--project", "--team-home", "--team-profile",
		"--conversation", "--conversation-id", "--trust", "--model", "--launcher", "--wake-inject-via",
		"--spawn-origin", "--spawn-depth",
		"-role", "-session", "-me", "-root", "-project", "-team-home", "-team-profile",
		"-conversation", "-conversation-id", "-trust", "-model", "-launcher", "-wake-inject-via",
		"-spawn-origin", "-spawn-depth":
		return "string"
	case "--team-workstream", "--no-bootstrap", "--no-default-args",
		"--force-duplicate", "--dry-run", "--no-require-wake", "--no-gitignore",
		"-team-workstream", "-no-bootstrap", "-no-default-args",
		"-force-duplicate", "-dry-run", "-no-require-wake", "-no-gitignore",
		// Help flags after the binary should fall through to runLaunch
		// (which prints amq-squad launch help) rather than flow to the
		// child binary as `-- --help`. Native child help is still
		// reachable behind an explicit `--` boundary.
		"--help", "-help", "-h":
		return "bool"
	}
	return ""
}

// runAgentResume turns `agent resume <role> [extras]` into the shared replay
// body's `--exec --role <role> [extras]` call. The role is the only required
// positional; extras flow into the replay body unchanged so callers can still
// filter by --handle / --session / --conversation / --project.
func runAgentResume(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad agent resume - re-launch a saved agent by role

Usage:
  amq-squad agent resume <role> [--project dir1,dir2,...] [restore options]

Re-launches a saved agent from local launch history. Exactly one saved
record must match; on match, amq-squad changes to that record's cwd and
execs the saved launch through 'amq coop exec'.
Default scope is the current working directory. Pass --project to scan one
or more other team-homes without changing directories.

Examples:
  amq-squad agent resume cto
  amq-squad agent resume cto --project ~/Code/app
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
	return runRestore(forwarded)
}
