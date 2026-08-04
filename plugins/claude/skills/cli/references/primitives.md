# Operator primitives and namespace resolution

## Which primitive for which intent

| Intent | Primitive | Notes |
|---|---|---|
| See current state | `status` | projection; `--json` for a stable shape |
| Check the setup | `doctor` | severity is a contract, not advice |
| Decide what to do next | `next` | exits 1 when idle |
| Watch for events | `monitor` | read-only, must be bounded |
| Reach the human | `notify` | attention only; never approves or clears |
| Assign work | `dispatch` | durable; pane input is fallback |
| Move task state | `task` | the task store is the source of transitions |
| Prove a command ran | `evidence` | immutable, task-bound |
| Ask a human decision | `gate` | an open decision stays unsuppressed |
| Check an action is allowed | `verify` | read-only; non-zero is a blocker |
| Explain resolution | `context` | shows candidates and the winner |
| Inspect messaging | `amq` | env, ops, route, who, drain, list, read, thread, receipts |

## Namespace resolution, one dimension at a time

Three dimensions resolve independently, and conflating them is the most common
namespace error.

**Project** is *where the team-home is*. It decides which `.amq-squad/` directory is
authoritative. A relative path resolves against the project, not against your shell's
working directory — that distinction matters because a persisted record anchored to a
shell cwd reads differently depending on where the next command runs.

**Profile** is *which roster inside that project*. A project can hold several. Named
profiles require `--profile` for mutations; omitting it fails closed rather than
guessing.

**Session** is *which workstream namespace*. It scopes tasks, activity, gates, and
mailboxes.

### Precedence

| Source | Wins over | Notes |
|---|---|---|
| explicit flags | everything | always unambiguous; prefer them in scripts |
| injected environment | launch records | what a launched agent inherits |
| live launch records | project config | ambiguous when several are live |
| project config | documented defaults | `.amqrc` and friends |

When two live launch records disagree, resolution is **ambiguous** and the CLI
refuses rather than picking. That refusal is the feature. `context explain` prints
every candidate with the reason it won or lost.

## Failure mode to expect

```
error: ambiguous profile at live_launch_record precedence
```

Cause: more than one live launch record resolves for this project. Fix: pass
`--profile NAME` explicitly. The CLI prints that fix itself, which is worth reading
before reaching for anything more elaborate.

## Runtime control (tmux)

amq-squad owns the tmux control contract. Drive agents by stable command, never raw
`tmux send-keys`, and target the recorded **pane id** rather than a window name — names
are not stable across a session's life.

```sh
amq-squad focus --session S --role cto                          # bring a pane into view
amq-squad send --session S --role cto --body-file ./prompt.md   # deliver a prompt and submit
cat prompt.md | amq-squad send --session S --role qa --body-file -
```

`send` stages text in a tmux paste buffer, so multi-line text and shell metacharacters
arrive verbatim, and it carries a **busy-guard**: it refuses to deliver into a mid-turn
pane unless you pass `--force`. That refusal is a feature — interrupting a pane
mid-turn corrupts the agent's own transcript.

**`amq-squad send` is pane delivery, not a message.** It has no `--kind` and no
`--thread`. To post an inter-agent message use `amq send`, which is a different tool
with a different transport.

### The two body-passing rules are NOT the same

| tool | flag for file or stdin |
|---|---|
| `amq-squad send` | `--body-file FILE`, or `--body-file -` for stdin |
| bare `amq send` | `--body @file`, or `--body -` for stdin; **`--body-file` does not exist** |

Use a file or stdin for anything containing code, backticks, or `$()`. Inline `--body`
is for short plain prose only, because the calling shell expands it before the tool
ever sees it — a body with backticks arrives mangled or empty.

## Session lifecycle

```sh
amq-squad console --session S                 # Mission Control TUI, filtered to S
amq-squad down --role R --session S           # or --all; a selector is REQUIRED
amq-squad resume --session S                  # PLAN ONLY: prints the action table
amq-squad resume --session S --exec           # actually reopens the panes
amq-squad archive --session S                 # retire a finished session
amq-squad rm --session S                      # remove it
amq-squad fork --from SOURCE --as TARGET      # no --session flag on this one
```

Several of those have surprising defaults. Every row below was verified by running the
command, not by reading its flags:

| command | the surprise |
|---|---|
| `down` | exactly one selector is MANDATORY, `--role R` or `--all`. `down --session S` alone stops nothing and exits on a usage error |
| `resume` | plan-only by default. It prints a per-member action table and copy-pasteable commands; without `--exec` nothing reopens |
| `console` | a full-screen read-only Mission Control TUI rendered to `/dev/tty`, NOT an attach to the squad's pane. Use `--once` for a single non-interactive snapshot |
| `fork` | takes `--from SOURCE --as TARGET` and has no `--session` flag at all |

`start` reconciles an existing session by design: verified live roles stay running,
stopped roles respawn, and a partial launch rolls forward. Use `resume` instead when
the goal is to reattach saved conversations from launch history.
