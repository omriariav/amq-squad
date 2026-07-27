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
