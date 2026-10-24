# Task store (3.0)

The task store is the durable record the scheduler dispatches from (plan
4.5, R17). The lead decomposes, labels each task with a class, sets
priorities and converges; it does not poll. Dispatch is tracked by task
leases, not by mail: a doorbell message may be lost or read in the middle
of a turn, the lease is what decides (S5 lead loop run 2, S6 task N4).

This file is the contract that 2b-2 (commit step), 2b-4 (routing), 2b-5
(squad.yaml), 3-2 (merge train) and 4-4 (amq-noc) code against. Schema 1.
The Go form is `internal/task` (`class.go` holds the vocabulary).

## Location and transaction boundary

- One store per squad session, under the AMQ session root:
  `<AM_ROOT>/extensions/amq-squad/tasks/`, beside the gate store, the
  answers and the hook ledger (briefs review 1, ruling E2). Nothing is
  written under the team home or the repository (R7, 4.7). Directory
  0700, files 0600. The guard's implicit protected path
  `<root>/extensions/amq-squad/` covers it, so a seat changes the store
  only through `amq-squad task`.
- One JSON file per task, `<id>.json`, ids `t1`, `t2`, ... allocated as
  the maximum plus one.
- Every read-modify-write holds the exclusive advisory lock `.lock` in
  that directory for the whole operation.
- Every mutation, single-task or not, writes a committed journal
  `.transaction.json` with the complete after-images. Commit protocol
  (kept from v2): write and fsync the journal temp file, rename it to the
  journal, fsync the directory (the commit point), write, rename and fsync
  each after-image, remove the journal, fsync the directory. The next
  locked read or mutation replays a committed journal idempotently. An
  abandoned `.transaction.json.tmp` never crossed the commit point.
- Lock-free readers (`context`, `status`) read the task files and overlay
  the after-images of a committed journal that is still present, and never
  write. Task files are only ever replaced by rename. Every apply bumps the
  store generation `.generation` (replaced by rename) after the after-images
  and before it removes the journal; a reader reads the generation before
  and after its scan and retries on a change, so it never sees part of one
  transaction and part of the next (review finding 4).
- Directory fsync is enforced on Unix and a no-op on Windows. The lock is
  a no-op on Windows (internal/flock), so Windows gets no concurrency
  guarantee.

## Task fields (schema 1)

| field | type | meaning |
| --- | --- | --- |
| `schema` | int | 1 |
| `id` | string | `t<N>` |
| `title` | string | one line; required |
| `description` | string | optional body |
| `class` | string | one of the closed list of plan 6.1: `extract`, `summarize`, `script`, `implement`, `plan`, `review`; required (a task without a class is refused) |
| `state` | string | `pending`, `in_progress`, `blocked`, `completed`, `cancelled` (below) |
| `priority` | int | higher dispatches first; ties go to the lower id |
| `depends_on` | []string | ids that must be `completed` first; they must exist at creation, so the graph is a DAG by construction |
| `declared_files` | []string | the task-declared file set of 7.2: path globs relative to the repository root, `filepath.Match` syntax per segment, a trailing `/` or `/**` covers a tree; empty means the task changes no file. Used for file locks here, by the harness commit step (2b-2) and the merge train (3-2) |
| `project_path` | string | absolute repository root the task works in (amendment 24) |
| `pool` | object or null | `{runtime, model, effort}`, the runtime x model x effort key (4.5); set by the lead or by the router (2b-4); null until routed |
| `lease` | object or null | the current lease, below; null when no seat holds the task |
| `attempt` | int | number of leases granted so far (1 for the first dispatch) |
| `attempts` | []object | one row per ended attempt: `n`, `holder`, `pool`, `granted_at`, `ended_at`, `outcome`, `detail` |
| `wait_reason` | string | why a `pending` task is not dispatched: `dependencies`, `file_lock`, `no_route`, `seat_starting`, `seat_cap`, `usage_limit`; 2b-4 adds routing reasons (`privacy_class`, `cost_cap`, ...); empty when not waiting |
| `block_reason` | string | reason code of a `blocked` task (`seat_blocked`, `retries_exhausted`, a commit-step refusal code, `gate_open`, ...) |
| `block_route` | string | who owes the decision: `lead` or `owner` |
| `block_detail` | string | human text for the block |
| `report` | object or null | the holder's done report awaiting the commit step: `by`, `at`, `evidence`, `final_head` |
| `evidence` | string | the accepted completion evidence (copied from the report) |
| `cancel_reason` | string | why the lead cancelled |
| `scope_changes` | []object | recorded lead decisions that widened `declared_files`: `actor`, `at`, `reason`, `added` (2b-2's widening path) |
| `created_by` | string | handle that added the task |
| `created_at`, `updated_at` | time | RFC 3339 UTC |
| `closed_at` | time or null | when the task became `completed` or `cancelled` |

Lease object:

| field | type | meaning |
| --- | --- | --- |
| `holder` | string | seat handle; one seat holds at most one lease |
| `granted_at` | time | when the scheduler (or `dispatch`) granted it |
| `expires_at` | time | lease expiry |
| `claimed_at` | time or null | when the holder claimed it; null is an unclaimed assignment |
| `renewed_at` | time or null | last renewal |
| `renewals` | int | renewal count |

Fields that v2 carried and 3.0 drops: the outbox and delivery states, the
dispatch metadata, completion notification suppression, lifecycle
generations and events, command-evidence links, completion reconcile,
leadership epochs, intent/artifact/implementer/reviewer contracts, and
review/replacement links. v2 task files are not read: the 3.0 store lives
in a new directory, so there is nothing to migrate in place.

## States

| state | lease | meaning |
| --- | --- | --- |
| `pending` | none | queued; `wait_reason` names why it is not dispatched yet |
| `in_progress` | held | a seat holds the lease, claimed or not yet claimed |
| `blocked` | none | stopped on a decision the lead owes (`block_route: lead`) or, routed, the owner (`block_route: owner`) |
| `completed` | none | terminal; the done report passed the commit step |
| `cancelled` | none | terminal; the lead cancelled it |

v2's `failed` is not a task state in 3.0: a failed attempt ends the lease,
is recorded in `attempts`, and the task returns to `pending`, where the
retry rule of 6.2 (2b-4's router hook) picks the next pool or, when no
retry is left, blocks the task with `retries_exhausted` for the lead
(lead ruling, 2026-10-24).

Mapping from the v2 states (for readers of v2 docs; v2 files are not
migrated):

| v2 state | 3.0 |
| --- | --- |
| `pending` | `pending` |
| `in_progress` | `in_progress` (the lease says claimed or not) |
| `completed_pending_reconcile` | `in_progress` with a `report` awaiting the commit step |
| `completed` | `completed` |
| `failed` | an attempt row with outcome `failed`; the task goes back to `pending`, or to `blocked` with `retries_exhausted` |
| `blocked` | `blocked` with `block_route: lead` |
| `cancelled` | `cancelled` |

## Leases

- Grant: a `pending` task whose dependencies are all completed and whose
  `declared_files` overlap no `in_progress` or `blocked` task's set gets a
  lease for one seat. `attempt` grows by one. Default length 10 minutes
  (a proposal, measured in docs/v3-spike/2b1-execution.md).
- Claim: the holder claims its lease (`task claim --me H`); a seat may also
  claim the next eligible pending task of its pool in one transaction
  (pull). The store guarantees one holder per task under the lock.
- Renewal: `evidence ingest` renews the claimed lease of the handle on
  every tool result (rate-limited to once per tenth of the lease length),
  and the scheduler renews it on seat activity. `context` is read-only and
  never renews (2a-3 contract, review 1 ruling). The interval is rechecked
  against the locked lease, and `renewed_at` and `expires_at` only move
  forward (finding 5). The ingest renewal takes the lock with a try-lock and
  skips on contention (`lease_renewal: skipped` in the ledger line).
- An expired lease is over at `expires_at` (now >= expires_at), before the
  scheduler's tick returns it: claim, done, fail and block by the holder
  are refused with `ErrLeaseExpired` (finding 3).
- An unclaimed lease is never renewed: it returns to the pool at
  `expires_at` (outcome `unclaimed`), and the scheduler re-dispatches it to
  another seat. A claimed lease that expires returns too (outcome
  `expired`).
- Done: the holder files a report (`task done`). A task with no
  `declared_files` completes at once. A task with declared files waits,
  still `in_progress`, for the scheduler's commit step (2b-2's `seat
  commit`); success completes it (`Complete`), a refusal blocks it with
  the refusal's reason code, routed to the lead (`RefuseCommit`). After a
  refusal the report stays and the reporting seat stays reserved (no new
  lease; its worktree diff waits). A recorded lead widening (`Widen`)
  re-arms the commit: the lease goes back to the reporting seat, claimed,
  and the commit step runs again. `reset` discards the report. A widening
  of an in-progress or blocked task whose new set overlaps another active
  or blocked task's set is refused under the lock with reason code
  `file_lock_conflict`, so the exclusion the grant gave holds (finding 2).
- A lease with a done report awaiting the commit step does not expire.
- One seat holds at most one lease (`HeldBy`): a lease, or a blocked task
  whose done report it filed.
- Fail, block, release, cancel end the lease and append the attempt row.

## File locks

Two tasks hold a file lock conflict when one's `declared_files` may match
a path the other's may match. The check is conservative: two globs
overlap when, compared segment by segment up to the first segment with a
glob character in either, one is a prefix of the other, or the literal
segments match each other as patterns. A false overlap delays a task;
a missed overlap never happens.

## Commands

| command | who | effect |
| --- | --- | --- |
| `task add --title T --class C [--desc D] [--depends-on ids] [--files globs] [--pool r:m:e] [--priority N] [--project-path P]` | lead | a pending task |
| `task list [--state S] [--json]`, `task show ID [--json]` | anyone | read |
| `task claim [ID] --me H [--pool r:m:e]` | seat | claim the own lease, or pull the next eligible task |
| `task renew --me H` | seat | renew the own claimed lease |
| `task done ID --me H [--evidence E] [--final-head SHA]` | holder | done report |
| `task fail ID --me H --reason R` | holder | failed attempt, back to the pool |
| `task block ID --me H --reason R [--route owner]` | holder or lead | blocked |
| `task release ID --me H --reason R` | holder or lead | lease back to the pool |
| `task reset ID --me H [--reason R] [--pool r:m:e]` | lead | blocked to pending |
| `task set ID --me H [--priority N] [--pool r:m:e]` | lead | reprioritize or route |
| `task cancel ID --me H --reason R` | lead | cancelled |
| `task reconcile [--json]` | anyone | replay a committed journal; list expired leases |

The store root comes from `--root`, else `AM_ROOT`, else the AMQ root of
`--project/--profile/--session`.
