# @amq-squad/pi

The amq-squad package for the built-in pi (plan 4.1, 4.4, 16.3). The lead and
every pi seat load it with `-e <runtime>/pi`; `amq-squad lead` and
`amq-squad pi run` start pi that way. It is a normal pi package, so a user's
own pi can load it too (`pi -e ./pi`), but without an AMQ identity (`AM_ME`,
`AM_ROOT`) it stays inactive.

## What it does on every turn

| pi event | call | on failure |
| --- | --- | --- |
| `before_agent_start` | `amq-squad context --me H --json`: role and context into the system prompt every turn; the role also as the first message of a session | the turn runs with a note that context is unavailable |
| `tool_call` | `amq-squad guard --me H --stdin` | the call is blocked (fail closed, R36): guard missing, not startable, timed out (5 s, `AMQ_SQUAD_HOOK_TIMEOUT_MS`), non-zero exit, or no verdict |
| `tool_result` | `amq-squad evidence ingest --me H` | never blocks; the TUI status line says so |

`deny` blocks with its reason. `needs_gate` holds the call until guard allows
it, which happens only when the owner's signed answer is recorded by the core
(R32). A local dialog or an amq-remote answer can only block a typed gate.

## Contracts with the rest of amq-squad

- **Task scope is a hint, not enforcement.** A `write` or `edit` outside the
  current task's declared files is blocked in session with the files named.
  The harness commit step (2b-2 milestone B) is the enforcement point
  (amendment 21): it refuses a diff with any file outside the declared set,
  whatever the seat did in session, including through bash.
- **`amq_task_reset` (lead only) calls the task store's `Reset` through
  `amq-squad task reset ID --me H`, and nothing else.** It takes a task the
  commit step refused (`RefuseCommit`: state `blocked`, `block_route` lead,
  the reason code and the file in `block_detail`) back to `pending`, discards
  the seat's report, and the scheduler leases it again. It never widens the
  declared files. A widening is a different act (`Widen` in the store, a
  recorded lead decision inside the grant's limits); it is not a package tool
  yet, and a widening that would cross a grant limit is refused by the core
  and escalates to an owner-signed gate, never decided by the model.
- **The refusal reaches the lead through the task store first.** The commit
  step writes the refusal into the task (`blocked`, `block_reason`,
  `block_detail`, `block_route: lead`); that record is the truth, and
  `amq_task_list` shows it. A doorbell to the lead's handle (an AMQ message
  naming the task, its reason code and the refused file) wakes it; it is a
  hint, and a lost doorbell loses nothing because the store keeps the block.
  In the 4-1 R19 harness the doorbell is the lead's next turn with that text.
- **Native tools**: `amq_send`, `amq_drain`, `amq_reply` (the amq CLI, bodies
  on stdin), `amq_task_list`, `amq_task_claim`, `amq_task_done` (the kept
  `amq-squad task` verbs); the lead also has `amq_task_add` and
  `amq_task_reset`.
- **Models**: a model outside `AMQ_SQUAD_MODELS` (the seat's registry entry) is
  switched back and never sent a request; the launcher also scrubs provider
  credentials and writes a per-seat `models.json`.
- **Loading**: pi 1.3.0 loads this package through jiti; the launcher forces
  `JITI_FS_CACHE=false`, so the TypeScript is transformed in memory from the
  bytes the launch re-hash verified, never from a cache file, and sets
  `TMPDIR` to the seat's own 0700 directory.
- **Bridge revision 4**: `lib/amq-bridge.ts` is AMQ's reference bridge
  (v0.107.0, MIT) with tool approval and session identity added.

## Development

No build step: pi loads the TypeScript with jiti, and the tests run with
Node's type stripping (Node 22.19 or later). The code uses erasable syntax only.

```sh
npm ci --offline   # from the S3 npm cache; the lock pins pi 1.3.0
npm run lint       # erasable syntax, import rules, pins (no tsc: phase 5)
npm test
```
