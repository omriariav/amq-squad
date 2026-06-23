# Native task store — design (v2.0 Slice B)

The lead's dispatch mechanism becomes **task-pull**, not only pane-push: the
lead decomposes the goal into tasks; workers claim them and self-schedule around
dependencies. This is the binary-neutral, amq-squad-native analog of the
`amq swarm` task list (which is Claude-Code-bound and, critically, has **no
create verb**). Ours has `task add`, so an any-binary lead can decompose.

## Storage

- One directory per workstream: `.amq-squad/tasks/<session>/`.
- **One JSON file per task**: `<session-tasks-dir>/<id>.json`. Per-file (vs a
  single `tasks.json`) keeps writes small and lets `list` be a directory scan.
- **Locking**: all mutations take an exclusive lock on a sidecar
  `.amq-squad/tasks/<session>/.lock` (via `internal/flock`, the same helper
  Slice A introduced). The lock is held across the whole read-modify-write so
  concurrent `claim`s can't both win. `add` also serializes id allocation under
  the lock.
- **Atomic writes**: each task file is written `<id>.json.tmp` → `os.Rename`, so
  a crash mid-write never leaves a partial/invalid task file. (Same pattern as
  `team.WriteProfile`.)

## Task schema (`<id>.json`)

```json
{
  "id": "t1",
  "title": "Wire the rate limiter",
  "description": "…",                // optional
  "status": "pending",               // pending|in_progress|completed|failed|blocked
  "assigned_to": "",                 // handle of the claiming agent
  "depends_on": ["t0"],              // ids that must be completed before claim
  "created_at": "2026-06-13T…Z",
  "updated_at": "2026-06-13T…Z",
  "evidence": "",                    // set on done
  "failure_reason": "",              // set on fail
  "block_reason": "",                // set on block
  "reset_reason": "",                // set on reset
  "dispatch": {                       // optional durable AMQ link
    "assignee": "fullstack",
    "thread": "p2p/cto__fullstack",
    "kind": "todo",
    "subject": "Wire the rate limiter",
    "message_id": "2026-...",
    "dispatched_at": "2026-06-13T…Z"
  }
}
```

amq-squad is the **sole writer** of `.amq-squad/tasks/` (unlike swarm, which
shares `~/.claude/tasks/` with Claude Code), so tasks round-trip through the
typed struct and unknown-field preservation is not needed in Slice B. If an
interop adapter ever shares this store, switch to a `map[string]any` round-trip
(as swarm does) to preserve foreign fields.

## State machine

```
pending ──claim──▶ in_progress ──done──▶ completed
                       │
                       ├──fail──▶ failed
                       └──block─▶ blocked
```

- `claim`: allowed only from `pending`, and only when **every** id in
  `depends_on` is `completed` (dependency gating). Sets `assigned_to` and
  `in_progress`; clears terminal fields.
- `done` / `fail` / `block`: allowed only from `in_progress`.
  If the task has an assignee, the transition requires `--me` to match
  `assigned_to`. `done`→`completed` (+ optional evidence); `fail`→`failed`
  (+ reason); `block`→`blocked` (+ reason).
- `reset`: returns a non-pending task to `pending`, clears the assignee and
  terminal fields, optionally records `reset_reason`, and can then be claimed
  again. For assigned tasks, `--me` must match the assignee.
- Any other transition is rejected with a clear error naming the current state.

## ID allocation

Under the store lock, scan existing task files, take the max `t<N>` suffix, and
allocate `t<N+1>` (starts at `t1`). Deterministic, sortable, and race-free
because allocation happens inside the lock.

## Verbs (`amq-squad task …`, new `runTask` dispatcher)

| Verb | Effect |
| --- | --- |
| `task add --title T [--desc D] [--depends-on id,…] [--assign role] --session S` | create a `pending` task; **the goal→task decomposition primitive** |
| `task list [--status S] [--json] --session S` | list tasks (table or `tasks` JSON envelope) |
| `task show <id> [--json] --session S` | show one task, including dispatch metadata when present |
| `task claim <id> --me handle --session S` | pending + deps-completed → in_progress, assigned to `handle` |
| `task done <id> --me handle [--evidence E] --session S` | in_progress (by assignee) → completed |
| `task fail <id> --me handle [--reason R] --session S` | in_progress (by assignee) → failed |
| `task block <id> --me handle [--reason R] --session S` | in_progress (by assignee) → blocked |
| `task reset <id> --me handle [--reason R] --session S` | non-pending (by assignee when assigned) → pending |

- `--session` resolves the workstream (required; tasks are per-workstream).
- `claim`/`done`/`fail`/`block`/`reset` operate on an existing id; transitions
  are validated and assigned terminal/reset transitions are assignee-only.

## Dispatch linkage

Plain `amq-squad dispatch` stays AMQ-only for one-off messages. Task-backed
dispatch is explicit:

- `dispatch --create-task` creates a native pending task assigned to the target
  role's handle, sends the durable AMQ message, then records the AMQ message id
  and thread metadata on the task.
- `dispatch --task <id>` sends the durable AMQ message and links the returned
  message metadata to an existing task.
- Human and JSON dispatch output include the task id when a native task is
  involved. If the AMQ send fails after a `--create-task`, the task remains in
  the store without dispatch metadata so the lead can inspect or reset it rather
  than losing the audit record.
- If the AMQ send succeeds but recording dispatch metadata fails, amq-squad
  reports an error and does not pane-nudge the worker. The durable AMQ message
  may already be queued, so amq-squad does not try to roll it back. The native
  task remains inspectable without dispatch metadata; the lead can reconcile
  from the AMQ send result, manually reset or complete the task, and re-nudge
  the worker if needed.

## Still deferred

- Cycle detection is unnecessary: deps must reference already-created (lower-id)
  tasks, so the graph is a DAG by construction — see `Add`.
- Bridge of task changes to AMQ notifications (the swarm-bridge analog) — folds
  into Slice C's orchestrate loop / Phase 1.
- The lead's *judgment* about which tasks to create — that is Slice C (the
  orchestrate-from-goal skill), validated at the Slice D eval gate.
