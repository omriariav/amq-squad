# Operator primitives and root resolution

## Which primitive for which intent

| Intent | Primitive | Notes |
|---|---|---|
| See current state | `status` | the status contract; `--json` for the schema 1 document |
| See every squad | `status --fleet` | reads `~/.amq-squad/fleet.yaml`, one document per squad |
| Check the host | `doctor` | severity is a contract, not advice; it writes nothing by default |
| A seat's own view | `context` | role, rules digest, current task, open gates; reads only |
| Decide what to do next | `status` | inspect one JSON snapshot and select one bounded action |
| Watch for events | `status` then park | AMQ wake delivers pending work to the seat |
| Move task state | `task` | the task store is the source of transitions |
| Queue work for a seat | `dispatch` | durable AMQ; with `--create-task` or `--task` it also grants the lease |
| Prove what ran | `evidence list` | the hook ledger, redacted; read-only |
| Check one tool call | `guard` | allow, needs_gate or deny; advisory, deterministic |
| Reach the owner | `gate` | typed gates are answered only by the owner's signed reply |
| Check an action is approved | `verify action` | read-only; non-zero is a blocker |
| Check merge or release evidence | `verify merge`, `verify release` | read-only; never merges or tags |
| Inspect messaging | `amq env`, `amq route` | the resolved AMQ context and one route explained |
| List roles | `roles` | catalog roles and staged custom roles |

## Root resolution

3.0 has one squad per repository (`<repo root>/squad.yaml`) and keeps the task store,
the ledgers and the gate store under the AMQ session root
(`<root>/extensions/amq-squad/`). Two ways name that root:

- `--root DIR`, else the `AM_ROOT` environment variable. Inside a seat the launch
  record sets it; a flag may fill an unset variable but never contradicts a set one.
- `--project DIR --session S`: the root resolved from the repository's AMQ
  configuration (`.amqrc`) and the session name.

```sh
amq-squad amq env --project P --session S --json
amq-squad amq route --to ROLE --project P --session S --json
```

`amq env` prints the resolved context; `amq route` explains where a message to one
handle would go, including cross-project routing (`--target-project`,
`--target-session`). Read them before suspecting the mailbox.

## Messaging

Inside a seat, use bare `amq send`, `amq drain` and `amq thread`: the launch set the
whole identity. From the owner's terminal, pass the root explicitly to raw `amq`
(`amq drain --root ROOT --me H --include-body`).

`amq-squad dispatch` is the lead's durable send: it resolves the root from the
repository, sends one AMQ message, and with `--create-task` or `--task ID` grants the
task's lease to the seat before sending. The message is a doorbell; the lease is the
assignment.

### Body-passing rules

| tool | flag for file or stdin |
|---|---|
| `amq-squad dispatch` | `--body-file FILE`, or `--body-file -` for stdin |
| bare `amq send` | `--body @file`, or `--body -` for stdin; **`--body-file` does not exist** |

Use a file or stdin for anything containing code, backticks, or `$()`. Inline `--body`
is for short plain prose only, because the calling shell expands it before the tool
ever sees it.

## What 3.0 removed

The v2 runtime-control and lifecycle verbs are gone: there is no `start`, `resume`,
`down`, `send`, `focus`, `agent`, `init` or `wizard` verb, and no `team member`,
`team sync` or `team shared-cwd-exception`. Seats start through the AMQ launch API
(the scheduler beside `amq-squad lead --team`, from squad.yaml), a stopped seat is the scheduler's
to restart, and the squad is `squad.yaml`, changed only through `team propose` and
`team apply`. `amq-squad migrate` converts a v2 profile once.
