# amq-squad 3.0, brief 2b-1 execution log: task store, scheduler, status contract

Branch `v3-2b-1` (the lead's name; the brief says `v3-2b1`), cut from `v3` at 2736ebdd
(after f63f2610, briefs review 1). Implementer: Opus 5.5, high effort, one worktree, no
helper agents. Contract: `docs/v3-phase2-briefs.md` brief 2b-1 with its rulings, the lead
rulings and review 1.

Gate for every commit: `go build ./... && go vet ./... && go test ./... &&
scripts/manifest-check.sh --final`, `GOOS=linux go build ./...`, `GOOS=windows go build ./...`,
plus `go test -race ./internal/task/... ./internal/scheduler/... ./internal/status/...`.

## Commits

### Commit 1: the shared contracts (task schema, status schema 1, bucket table, usage-limit interface)

Written first because 2b-2 (commit step), 2b-3 (detector, doctor self-check), 2b-4 (routing),
2b-5 (squad.yaml), 3-2, 4-3 and 4-4 code against them.

Built:
- `docs/task-store-design.md` rewritten as the 3.0 task schema 1: location
  `<AM_ROOT>/extensions/amq-squad/tasks/` (ruling E2), the journal commit protocol kept from
  v2, the field list (`class`, `declared_files`, `pool`, `lease` with holder, granted_at,
  expires_at, claimed_at, renewed_at, renewals, `attempt`, `attempts`, `wait_reason`, the block
  fields, the done `report`, `scope_changes`, `project_path`), the five states, the lease rules,
  the file-lock overlap rule and the verb table. The v2 fields that go are named.
- `internal/task/class.go`: the Go vocabulary of that schema (states, the closed class list,
  wait reasons, attempt outcomes, block routes, `Pool` with `Key`/`ParsePool`). The `Task` type
  itself lands with the store in commit 2 (the v2 `Task` type still occupies the name).
- `docs/status-contract.md` and `internal/status/schema.go`: `status --json` schema 1 with
  amendment 24's fields (task `title`, `updated_at`, `project_path`; seat `last_message_at`,
  `last_message`, `prompt_state`, `tokens_in`/`tokens_out` nullable), `schema_version`,
  `producer_version`, `generated_at`, `capabilities[]`, `attention[]` with argv actions, the
  "new task" action, gates, the scheduler stop; the classifier states (`needs_you`, `blocked`,
  `waiting`, `online`, `stale`, plus `unknown`) and `BucketFor`, the five-state-to-four-bucket
  table (plan 4.6, briefs ruling 6).
- `internal/status/testdata/status-v1.golden.json` and `internal/status/compat.go`
  (`CheckAdditive`): every key of the fixture must be present in the output with the same JSON
  type; added keys pass; null fixture values and null nullable objects pass.
- `internal/scheduler/usage_limit.go`: `UsageLimitEvent` and `UsageLimitSource`, the interface
  2b-3's detector implements (polled once per scheduler tick).

Tests (internal/status): the fixture decodes strictly into `status.Status`; round trip additive
both ways (the fixture covers every encoded field); removing `producer_version`, a seat's
`tokens_in`, a task's `lease_holder` or the new-task `argv` fails; a retyped `schema_version`
fails; added optional fields and a nulled nullable field pass; every action in the fixture
passes `ValidateAction` (argv, bare program, no shell); the bucket table, ten rows.

Rows retired: none. New rows (fate new, status new): internal/status/schema.go,
internal/status/compat.go, internal/scheduler/usage_limit.go, internal/task/class.go.

Decisions taken in this commit (reported as open rulings below):
- Five store states: `pending`, `in_progress`, `blocked`, `completed`, `cancelled`. v2's
  `failed` becomes an attempt outcome; a task with no retry left blocks with
  `retries_exhausted`.
- Bucket table: `blocked` to Needs you only when routed to the owner, else Working; `waiting`
  to Working (the plan 4.6 proposal).
- The contract carries both the classifier state and the bucket per row (open ruling 1 of the
  brief proposed five states only with amq-noc mapping; lead ruling 6 and plan 4.6 say the
  contract names the bucket, so both are carried).
- The "new task" action runs `amq-squad task add --root R` with `title` and `class` inputs; the
  scheduler dispatches it. Plan 4.7 says "dispatches through the kept dispatch verb"; `dispatch`
  needs a team role, which a command-center row does not have.

Lead rulings on commit 1 (2026-10-24): both decisions accepted. Five store states, v2's
`failed` an attempt outcome, with the v2-to-3.0 state mapping recorded in the design doc (done
in commit 2). The new-task action runs `task add`; dispatch is the scheduler's act, and plan 2.7
corrects amendment 24's wording.

### Commit 2: the 3.0 task store (internal/task rewritten, the task verbs on it)

Built:
- `internal/task/task.go` rewritten: the schema 1 `Task`, `Lease`, `Attempt`, `Report`,
  `ScopeChange` types; `Store` opened on an AMQ session root (`Open`, `DirFor`:
  `<root>/extensions/amq-squad/tasks/`, dir 0700, files 0600); `Add` (class required from the
  closed list, dependencies must exist, declared files normalized), `List`, `Get`; `Snapshot`,
  the lock-free read that overlays a committed journal and never writes; `DispatchOrder`
  (priority, then id); `ValidID`.
- `internal/task/transaction.go`: the v2 journal commit protocol kept (journal temp, rename,
  dir fsync as the commit point, after-images, journal removal), on 0600 files, with a fault seam
  at the commit boundaries.
- `internal/task/lease.go` (new): `Blocker` (dependencies, file locks), `HeldBy` (one seat, one
  lease; a seat whose done report was refused stays reserved), `Grant`, `Claim` (own lease, or
  pull the next eligible task of a pool in one transaction), `Renew` (claimed leases only, a
  lock-free pre-check and a minimum interval so the hook path rarely writes), `Done` (completes
  at once without declared files, else waits for the commit step), `Complete` and
  `RefuseCommit` (the commit step's two answers, for 2b-2), `Fail`, `Block`, `Release`,
  `Reset`, `Set`, `Cancel`, `Widen` (records the lead decision, re-arms a refused commit),
  `SetWait` (writes only on change), `ExpireDue` (unclaimed and expired leases back to the pool,
  never a lease whose done report waits for the commit step).
- `internal/task/files.go` (new): `CleanDeclaredFiles` and `FilesOverlap`, the conservative
  glob overlap (segment-wise; `**`, tree suffixes; two globs in one segment count as overlapping).
- `internal/cli/task.go` rewritten on the store: add, list, show, claim, renew, done (alias
  complete), fail, block, release, reset, set, cancel, reconcile. Store root from `--root`, else
  `AM_ROOT`, else the AMQ root of `--project/--profile/--session`; a scoped mutation holds the
  namespace writer admission as `gate raise` does. The operator handle is refused as an actor.
- `dispatch --create-task/--task` on the new store: the task is created with `--task-class`
  (required), `--task-files`, `--task-pool`, and its lease granted to the child before the AMQ
  send; the message is a doorbell. `--task` accepts a pending task or a lease the child already
  holds. The planner-lead refusal now keys on class `implement`.
- `context` (hook_context.go) reads the current task from the store under the session root:
  the task whose lease the seat holds (`HeldBy`), lock-free; it reports `class`,
  `declared_files`, `lease_claimed`, and prints the claim command for an unclaimed lease. It
  never writes (acceptance 7; the test compares the task file bytes before and after).
- `gate raise --task` checks the task in the store under the session root (two-line change in
  gate.go, kept small for 3-1). internal/state's snapshot task activity reads the new store.
- docs/task-store-design.md: the v2-to-3.0 state mapping (lead ruling), and the commit-refusal
  rule (the report stays, the seat stays reserved, `Widen` re-arms).

Rows retired (status, with the ledger reason quoted):
- internal/task/task.go "five states and leases exist; pools are new": shrunk, 702 to 389
  (target 676).
- internal/task/transaction.go, same reason: shrunk, 216 to 177 (target 183).
- internal/task/command_evidence.go, completion_reconcile.go, lifecycle_event.go,
  leadership.go, reconcile.go (same reason) and lifecycle.go (reviewed in the same pass, as the
  brief says): deleted. The command-evidence links, completion reconcile, lifecycle events,
  leadership epochs, outbox delivery and v2 reconcile have no place in the 3.0 schema.
- internal/cli/task.go "keep lifecycle CLI, drop pane nudges": shrunk, 1296 to 523 (target 800).
- internal/cli/task_selection.go and task_completion_evidence.go, same reason: deleted (the
  namespace task selection and the AMQ-message completion reconcile are v2-only).
- New rows: internal/task/lease.go (434), internal/task/files.go (109).
- Touched rows synced: dispatch.go 440 to 391 (shrunk, target 450), hook_context.go 174 (new),
  evidence.go 777 to 47 (deferred-replace), gate.go 175 to 174, mutation_json.go 61 to 57,
  status.go 838 to 809, state/snapshot.go 282 to 275, command_registry.go (summary only).

Behavior changes named:
1. The task store moves from `<team home>/.amq-squad/tasks/<profile>/<session>/` to
   `<AM_ROOT>/extensions/amq-squad/tasks/` (ruling E2). v2 task files are not read or migrated.
2. Verbs removed with the v2 store: `task event`, `deliver`, `retry-delivery`, `leadership`,
   `handoff`, `reconcile <id> --evidence-id` (completion reconcile); flags removed:
   `--dispatch-next`, `--no-notify`, `--override-dependencies`, `--replacement`, `--review-of`,
   `--assign`, the gate/completion-generation flags of done. New verb `task set`.
3. `task add` requires `--class`. `dispatch --create-task` requires `--task-class`; the
   structured intent flags (`--task-intent`, `--task-artifact`, `--task-expected-base`,
   `--task-implementer`, `--task-reviewer`, `--task-parallel-work`) are gone.
4. `evidence run`, `show`, `list`, `recover`, `lookup` are cut: they bound command evidence to a
   v2 task through its namespace, its command-evidence links and its dispatch thread, none of
   which exist in 3.0. `evidence ingest` stays. evidence.go is 3-2's deferred-replace row ("3-2
   deletes it, with evidenceSecretName and evidenceTaskLink moved into the ledger readers");
   `evidenceSecretName` is kept in place for 3-2; `evidenceTaskLink` mapped a command outcome
   to the v2 link fields (attempt id, actor, subject, process and finalization state, manifest,
   outcome and summary paths and sha256), recorded here because the function went with the
   v2 `CommandEvidenceLink` type. Open ruling 3 below.
5. A claim lease defaults to 10 minutes (was 2 hours), and an unclaimed lease is never
   renewed.
6. `status` (still the v2 renderer until commit 4) drops the `task_legacy_status` warning and
   reads current-task activity from the new store.

Tests: internal/task rewritten (11 tests): validation, lease lifecycle (grant, claim by the
holder only, renewal interval, done), the commit-step path (file lock held through a refusal,
seat reserved, widen re-arms, complete releases the lock), dependency gating and one lease per
seat, `ExpireDue` (unclaimed vs expired outcomes, re-grant), fail/block/release/reset/set/cancel,
dispatch order and pool filter, `SetWait`, the file-overlap table (13 pairs, both orders), the
journal crash cases (after the commit point: the lock-free snapshot overlays and the locked read
replays; before it: nothing visible), and acceptance 8 (below). internal/cli: task_test.go
(3 tests: the CLI flow at the session root, 11 refusals, reconcile), dispatch_task_test.go
(6 tests: lease before the doorbell, class required, a failed send keeps the lease, five
refusals before send, the leadership-epoch refusal, the review-actor refusal on both paths), the
context test seeded through the store with the no-write check and an unclaimed lease for qa.
Retired with the v2 store: 98 test functions (the v2 internal/task suites; the cli task,
completion evidence, completion gate, t44, v228 contract and operator-guard suites; the v2
dispatch task tests; four status tests of v2 task warnings; the v2 evidence suite; the two
outbox-outcome assertions in amq_send_test.go). The operator-guard case is kept as a refusal row
of TestTaskCLIRefusals.

Acceptance 8 (concurrency): `TestConcurrentClaimsEightProcesses` starts 8 processes of the test
binary against one store with 40 tasks; each loops pull-claim and done. Result: every task
completed with `attempt` 1 and one attempt row, no task claimed twice, 5 tasks per process. With
the store lock replaced by a direct call the test fails (a rename race on the after-image),
checked by hand. Windows: skipped (the lock is a no-op there, internal/flock).

## Open rulings

1. (closed) New-task action: `task add`.
2. (closed) Five store states.
3. `evidence run/show/list/recover/lookup` cut in 2b-1 with the v2 store (behavior change 4),
   ahead of 3-2's delete of evidence.go. The plugin skill docs under plugins/ still describe
   `evidence run` and the v2 task verbs; the skills are rewritten by the phase 4 plugin briefs.
