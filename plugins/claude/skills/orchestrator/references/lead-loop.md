# Lead loop, review tiers, and reconciliation

Extracted from `docs/skills.md` so the lead's playbook lives with the skill that
uses it. The guide remains the narrative overview; this file is the operational
detail.

## The loop

```sh
amq-squad status --project P --session S --json
```

`status` returns the status contract, schema 1 (`docs/status-contract.md`). Select
one bounded action from `attention[]` (each item has a stable `id`, a `kind`, a
`severity` and an optional `action` with an `argv`), the `needs_you` bucket, and the
task rows. Act, push the durable update, then park/end the turn so AMQ wake can
deliver new work. Re-read the active brief when scope, tasks, or status indicate an
update.

A seat row's `state` is one of `needs_you`, `blocked`, `waiting`, `online`, `stale`
or `unknown`; `live` says whether its session is up or its lease is live, whatever
its state. A seat that is `online` with no task is in the `ready` bucket and can take
work. `prompt_state` (`running`, `idle`, `unknown`) comes from the evidence ledger;
`unknown` is not proof of idleness.

## Planning and dispatch

Create tasks in the store; the scheduler grants leases to seats of the task's pool.
`dispatch --create-task` (or `--task ID`) grants the lease and sends the doorbell in
one step. Pane input is never the assignment; the lease is.

```sh
amq-squad task add --title TEXT --class review --project P --session S
amq-squad dispatch --project P --session S --role ROLE --subject TEXT --body-file FILE --task ID
```

Give an implementation task its declared files (`--files`) and one implementer; a
review task gets a different seat than the one that implemented it.

## Review risk tiers

Batch review at invariant or candidate-head boundaries, not after every micro-edit.
Match depth to risk:

| Risk | Change shape | Required evidence |
|---|---|---|
| Low | docs, projections | focused regular tests plus drift checks |
| Medium | state transitions | adversarial identity/idempotence tests, focused race tests |
| High | authority, lifecycle, release, recovery | exact-head full regular and race suites, recorded evidence |

Before any merge-ready claim: an independent reviewer verifies the **exact head SHA**
being proposed, and the evidence names subject, head SHA, CI and review. A review
against a branch name, a stale checkout, or an earlier SHA does not count. The merge
train then checks the grant, and `verify merge` reads its audit log afterwards.

## Reconciliation

```sh
amq-squad task reconcile --project P --session S --json
```

`task reconcile` replays the store's journal under its lock and lists the in-progress
tasks whose lease expired without a report; the scheduler returns those to the pool.
It changes no task and never answers or closes a gate: an open owner decision stays
open until the owner answers it.

## Evidence

The hook ledger is the evidence: one line per tool result of each seat, redacted when
read. `verify merge --task ID` joins a task's ledger lines inside its lease windows
and its outcome-log rows. Ask seats to name their checks in `task done --evidence`;
do not paste command output into a message as proof.

## Dispatch and drain

Dispatch over durable AMQ, not pane injection. An AMQ message queues and survives a
stopped seat; the scheduler restarts a seat on demand for its pool.

Seats PUSH reports; drain once rather than poll. Inside your seat, bare
`amq drain --include-body` reads your inbox with the identity the launch set.

### No blocking waits in the lead

`dispatch` accepts `--wait-for STAGE --wait-timeout DURATION`; a wait longer than the
lead posture allows is refused unless you pass `--override-wait-posture
--wait-posture-reason WHY`, which is recorded. Hand-written `sleep` or `until` loops
cannot be intercepted and remain forbidden lead posture. Drain once, then park the
turn.
