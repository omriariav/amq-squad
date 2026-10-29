# The daily loop: inspect, act, verify

Extracted from `docs/skills.md` so the operational detail lives with the skill that
uses it. In 3.0 a seat is launched by the AMQ launch API through `seat-exec`, which
sets `AM_ME`, `AM_ROOT` and `AM_SESSION`; the commands below pass explicit
coordinates so they also run from the owner's terminal.

## Inspect before acting

```sh
amq-squad status --project P --session S --json
# skill-invocation-check:skip — doctor probes gh and the provider CLIs' logins, which can reach the network
amq-squad doctor --project P
```

`status` is the status contract, schema 1 (`docs/status-contract.md`): seats, tasks,
the attention queue, gates and the scheduler, each row in one of four buckets
(needs you, working, ready, inactive). It never writes. `doctor` is the host check.
After one inspection, act or park; do not hand-roll a polling loop.

Read the output verbatim. Do not re-render it.

## Act on exactly one thing

```sh
amq-squad task list --project P --session S
amq-squad task claim ID --me H --project P --session S
```

Claim one task at a time. The scheduler usually granted the lease to you already;
`task claim --me H` without an id pulls the next eligible task in your pool. Push
ACK and meaningful progress over durable AMQ on the task thread so the lead can tell
busy from stalled without opening your pane.

## Verify with evidence, not assertion

Evidence is the hook ledger: every tool result in a Claude seat is one ledger line,
written by the PostToolUse hook, and it renews your lease. Read it back with
`evidence list`, and name the checks you ran in `task done --evidence`:

```sh
amq-squad evidence list --me H --root ROOT --last 5 --json
amq-squad task done ID --me H --evidence "make ci ok" --project P --session S
```

A task with declared files waits after `done` for the harness commit step. You never
commit, push or merge: the harness commits your worktree's tree after checking every
changed path against the task's declared files and the protected paths.

## Report without being polled

Push progress, blockers, and completion proactively. Inside a seat use bare `amq`
commands: the launch set the whole AMQ identity. A durable task's body is the
authoritative instruction; a pane prompt is wake or fallback only.

Map intent to the right message kind rather than inventing one: progress and done are
status, blocked is a question, ready for review is a review request, verdicts are
review responses, decisions are decisions, and assigned work is a todo.
