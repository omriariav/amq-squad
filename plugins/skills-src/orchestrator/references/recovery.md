# Recovery ladder

Climb in order. Each rung is cheaper and less destructive than the next, and skipping
to the bottom is how a recoverable run becomes an unrecoverable one.

## 1. Inspect before acting

```sh
amq-squad status --project P --session S --json
amq-squad task list --project P --session S
amq-squad task reconcile --project P --session S --json
# skill-invocation-check:skip — doctor probes gh and the provider CLIs' logins, which can reach the network
amq-squad doctor --project P
```

Distinguish *stalled* from *busy* first. A live seat plus recent ledger evidence
means busy. `task reconcile` lists the leases that expired without a report.

## 2. Re-ring the doorbell

Prefer a durable AMQ message over anything that touches a pane. A seat that never
drained its inbox is not a stalled seat: `dispatch --task ID` re-grants the lease and
rings again.

## 3. Return the task to the pool

An expired lease returns to the pool on its own. To move a task deliberately:

```sh
amq-squad task release ID --me H --reason TEXT --project P --session S
amq-squad task reset ID --me H --project P --session S
amq-squad task set ID --me H --priority 1 --project P --session S
amq-squad task cancel ID --me H --reason TEXT --project P --session S
```

`task release` returns a leased in-progress task to pending; `task reset` moves a
blocked task back to pending and discards a refused done report (optionally onto
another `--pool`); `task set` changes the priority or pool of a task that is not
terminal. The scheduler starts a seat for the pool on demand. A history_tampered
block clears only through a reset after the router observed it.

## 4. Escalate to the owner

A blocker that needs an owner decision is a gate or a block routed to the owner
(`task block ... --route owner`), not a retry. A seat quarantined for a global agent
config change stays quarantined until the owner runs `seat clear` from the owner's
own terminal; the lead never clears it.

## 5. Last resort, recorded

Raw pane input (`tmux send-keys`) only after owner direction, and only recorded as
such. It bypasses every durable record the rest of this ladder relies on.

## Leadership handoff

Before replacement, the outgoing lead records: current head, active tasks, seat
progress, open gates, evidence paths, decisions taken, known risks, and the next safe
action.

The replacement lead reads `status` and `gate status` before dispatching anything. A
second lead dispatching on stale state is how two leads end up owning one task; the
scheduler accepts widening only from the lead of its launch record.

## Multi-squad board

The operator session holding more than one squad reads them together:

```sh
amq-squad status --fleet --json
amq-squad operator inbox --json
```

`status --fleet` prints one schema 1 document per squad of `~/.amq-squad/fleet.yaml`
plus the squads waiting on the fleet cap; `operator inbox` lists every open gate of
every squad, oldest first, and the lead questions with their relay argv. Track per
squad: name, repository, session, lead handle, state, current gate or blocker, last
action and next action. Demote closed squads so they stop competing for attention.
