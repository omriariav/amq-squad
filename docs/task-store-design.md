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
  "block_reason": ""                 // set on block
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
- `done` / `fail` / `block`: allowed only from `in_progress`. `done`→`completed`
  (+ optional evidence); `fail`→`failed` (+ reason); `block`→`blocked`
  (+ reason). A `blocked`/`failed` task can be re-`claim`ed only after it is
  reset to `pending` (a later `task reset` verb; out of scope for Slice B —
  blocked/failed are terminal here).
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
| `task claim <id> --me handle --session S` | pending + deps-completed → in_progress, assigned to `handle` |
| `task done <id> [--evidence E] --session S` | in_progress (by assignee) → completed |
| `task fail <id> [--reason R] --session S` | in_progress → failed |
| `task block <id> [--reason R] --session S` | in_progress → blocked |

- `--session` resolves the workstream (required; tasks are per-workstream).
- `claim`/`done`/`fail`/`block` operate on an existing id; transitions are
  validated; ownership: `done`/`fail`/`block` require the task to be
  `in_progress` (Slice B does not enforce assignee identity — a single-operator
  convenience; assignee-only transitions are a Phase-1 hardening with the
  seeded-approval work).

## Not in Slice B (deferred)

- `task reset` (blocked/failed → pending), a `task show <id>` read verb, and
  assignee-only transition enforcement — Phase 1. (List is the only read
  surface in Slice B; terminal states are one-way.)
- Cycle detection is unnecessary: deps must reference already-created (lower-id)
  tasks, so the graph is a DAG by construction — see `Add`.
- Bridge of task changes to AMQ notifications (the swarm-bridge analog) — folds
  into Slice C's orchestrate loop / Phase 1.
- The lead's *judgment* about which tasks to create — that is Slice C (the
  orchestrate-from-goal skill), validated at the Slice D eval gate.
