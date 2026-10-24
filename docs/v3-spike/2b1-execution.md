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

## Open rulings

1. New-task action: `task add` (above) against plan 4.7's "kept dispatch verb".
2. Five store states with `failed` folded into attempts (above).
