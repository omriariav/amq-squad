# Recovery ladder

Climb in order. Each rung is cheaper and less destructive than the next, and
skipping to the bottom is how a recoverable run becomes an unrecoverable one.

## 1. Inspect before acting

```
amq-squad status --session S --json
amq-squad monitor --session S --once --json
amq-squad doctor --session S
amq-squad task list --session S
```

Distinguish *stalled* from *busy* first. Fresh activity means busy; absent
activity plus a live process means look at the pane before assuming failure.

## 2. Re-nudge queued work

Prefer `dispatch`, or a drain-only `send`, over anything that touches a pane. A
worker that never drained its inbox is not a stalled worker.

## 3. Resume

`amq-squad resume` for stale agents, or the actions the status projection names.
Resume rebuilds from the durable record, which is why the record is worth keeping
honest.

## 4. Escalate to the operator

A blocker that needs a human decision is a gate, not a retry. Raise it on the
gate thread and surface it on the operator-visible surface. Do not loop waiting
for a human who has not been told.

## 5. Last resort, recorded

Raw pane input (`tmux send-keys`) only after operator direction, and only
recorded as such. It bypasses every durable record the rest of this ladder relies
on.

## Leadership handoff

Before replacement, the outgoing lead records: current head, active tasks and
leases, worker activity, open gates, evidence paths, decisions taken, known
risks, and the next safe action.

The replacement lead must ACK that checkpoint and advance the leadership epoch
before dispatching anything. A second lead dispatching on a stale epoch is how
two leads end up owning one task.

## Multi-workstream board

A `global_orchestrator` holding more than one active workstream in one
conversation keeps an in-conversation board and refreshes it after every poll,
gate answer, spawn, stop, final report, or recovery action.

Track per run: name, repo, profile/session, lead and pane id, state (`running`,
`gated`, `blocked`, `paused`, `stale`, `done`, `closed`), last checked, next poll
or wake source, current gate or blocker, last action, next action, and the exact
polling command.

Demote closed runs to `next action: none - closed` so they stop competing for
attention with active gates and stale runs.
