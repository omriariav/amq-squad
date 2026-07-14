# Native task store and atomic lifecycle

The native task store is the durable coordination record for pull-based work.
Leads decompose a goal into tasks, workers claim them, and dependency gates make
the next eligible work deterministic across Claude, Codex, and other binaries.

## Storage and transaction boundary

- The default profile stores one JSON file per task under
  `.amq-squad/tasks/<session>/`; named profiles use
  `.amq-squad/tasks/<profile>/<session>/`.
- Every read-modify-write operation holds the store's exclusive `.lock` for the
  complete operation. Readers also take the lock so they cannot observe only
  some after-images from a multi-task transition.
- Every mutation, including a single-task mutation, writes a committed
  `.transaction.json` journal containing the complete task after-images. The
  commit protocol is: write and `fsync` the journal temp file, rename it to the
  committed journal, `fsync` the containing directory, write/rename/`fsync`
  every task after-image, remove the journal, then `fsync` the directory again.
- Once the journal rename and directory sync complete, the transaction is
  committed. An official read, mutation, or `task reconcile` replays a committed
  journal idempotently before exposing task state. An abandoned
  `.transaction.json.tmp` did not cross the commit point and is only removed by
  `task reconcile --apply`.
- Directory `fsync` is enforced on Unix platforms, including Linux and macOS.
  Windows lacks the same portable `os.File.Sync` directory contract, so
  directory sync is a documented best-effort no-op there. Files themselves are
  still synced before rename. Filesystems returning `EINVAL` or `ENOTSUP` for a
  directory sync are treated as not supporting that operation.

This journal is an internal recovery record, not a second task API. Task JSON
files remain the inspectable source after recovery.

## Additive task schema

Existing task files remain valid. Missing lease, readiness, link, or outbox
fields are interpreted as legacy data and are not silently rewritten by a
read-only command.

Core fields remain `id`, `title`, `description`, `status`, `assigned_to`,
`depends_on`, timestamps, terminal reasons/evidence, and optional `dispatch`
metadata. The atomic lifecycle adds:

- `ready_at`, set when a task first has all dependencies completed;
- `lease` with owner, issuance, renewal, expiry, and optional stale-observed
  timestamp;
- audited `dependency_overrides` and `releases`;
- direct `replaces` / `replaced_by` and `review_of` / `review_tasks` links;
- `final_head` for the immutable accepted commit or equivalent artifact;
- `outbox` delivery intents with stable IDs and explicit delivery state;
- `completion_notification_suppressed` when `--no-notify` is deliberate.

The six task states are `pending`, `in_progress`, `completed`, `failed`,
`blocked`, and `cancelled`.

## Claims, dependencies, and leases

`task claim` normally accepts only a pending task whose dependencies are all
completed. `--override-dependencies --reason WHY` is the explicit recovery
escape hatch: it records the actor, time, reason, and exact unmet dependency
states before claiming.

Every new claim receives a renewable lease (two hours by default). Lease expiry
is evidence of possible abandonment, not permission to steal work. Reconcile
reports stale leases and legacy in-progress tasks that have no lease, but never
auto-unclaims or reassigns either. The assignee can run `task renew`; another
actor can use `task release --me H --reason WHY` to make an explicit audited
release. Renewing a legacy in-progress task is its additive migration path.

## Atomic completion and successor dispatch

`task done` performs one transaction that can include all of the following:

1. mark the predecessor completed, store evidence and `--final-head`, and clear
   its lease;
2. stamp `ready_at` on every directly affected dependent that is now eligible;
3. with `--dispatch-next ID`, validate that the chosen direct dependent is
   pending, fully unblocked, and assigned, then claim it with a lease;
4. commit delivery intents for the canonical completion signal and optional
   successor dispatch.

Only after that transaction commits may the CLI send AMQ. When the completed
task has dispatch routing metadata, the default completion signal is AMQ kind
`status` with subject `DONE: <task title>`; AMQ has no `done` kind. `--no-notify`
records explicit suppression. A successor dispatch uses AMQ kind `todo`.
The committed task/successor dispatch outbox is itself a durable return route,
so completion signaling does not depend on a later legacy metadata-link write.

`amq-squad dispatch --create-task` and `dispatch --task ID` follow the same
boundary: they commit the claim and delivery intent, mark the intent `sending`,
and only then invoke `amq send`. Plain dispatch without task backing remains an
AMQ-only operation.

## Delivery outbox and recovery

Delivery intent states are:

| State | Meaning | Recovery |
| --- | --- | --- |
| `pending` | intent committed; send has not begun | `task deliver` |
| `sending` | send began; durable outcome is unknown | never auto-resend; use `task retry-delivery --confirm-not-delivered` only after verifying non-delivery |
| `delivered` | message ID or successful outcome recorded | no action |
| `failed` | send returned a definite failure without a message ID | audited `task retry-delivery`, or explicitly release the task |

Retry records the actor, reason, timestamp, and whether uncertain non-delivery
was confirmed. Reconcile only diagnoses and prints executable scoped commands;
it never sends or retries an external message. This prevents duplicate work
after a crash between the send and its local finalization.

## Direct lifecycle links

`task cancel --replacement ID` atomically records both sides of a supersession
link and rejects cycles. `task add --review-of ID` records both sides of a review
link. Reconcile reports dangling, asymmetric, or cyclic links and only applies a
deterministic missing reverse-link repair when there is no conflict. It never
guesses which side of a conflicting link is authoritative.

## Commands

| Command | Effect |
| --- | --- |
| `task add` | create a pending task, optionally assigned, dependency-gated, or linked with `--review-of` |
| `task list` / `task show` | inspect all additive fields; support schema-versioned JSON |
| `task claim` / `task renew` | claim with a lease or renew/migrate an active lease |
| `task done` | atomic completion, dependent release, optional successor claim, and committed outbox |
| `task fail` / `task block` / `task reset` | explicit terminal or reset transitions |
| `task cancel` | cancel with a required reason and optional replacement link |
| `task release` | audited explicit ownership release; never automatic |
| `task deliver` | deliver one pending intent |
| `task retry-delivery` | audited failed retry, or confirmed-not-delivered uncertain retry |
| `task reconcile` | read-only deterministic diagnosis and committed-journal replay |
| `task reconcile --apply` | apply safe internal repairs; never external delivery |

Dependency IDs must reference already-created tasks. Because IDs increase
monotonically, dependency edges form a DAG by construction. Replacement and
review links are separately cycle-checked because legacy or externally edited
files may violate their normal creation order.

`amq-squad status` may still surface completion-like reports for older task
records, but it never silently completes a task. Task transitions and AMQ
reports are complementary durable records; the atomic `task done` notification
is the canonical command path when dispatch routing is present.
