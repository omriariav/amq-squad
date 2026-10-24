# Status contract, schema 1

`amq-squad status --json` is the versioned contract of plan 4.6 (R43). amq-noc
reads it and AMQ primitives only (R44, 4.7): it never reads `.amq-squad/`
files or the stores and never composes an amq-squad command. The Go form is
`internal/status/schema.go`; the reference document is
`internal/status/testdata/status-v1.golden.json`, which amq-noc vendors byte
for byte.

## Versioning

`schema_version` is 1. Inside version 1 a change is additive only: a field
is never removed, renamed or retyped; a new field may appear. The test
`internal/status` fails when a field of the fixture disappears from the
output or changes JSON type, and passes when a field is added. A breaking
change is version 2. Nullable fields are always present and `null` when
unknown; lists are always present, possibly empty. The nullable fields are
exactly: seat `last_activity_at`, `last_message_at`, `tokens_in`,
`tokens_out`; task `pool`, `lease_expires_at`; attention `action`; scheduler
`usage_limit_since`, `usage_limit_until` (`status.NullableFields`). Null
anywhere else, or a null document, fails the check.

## Document

| field | meaning |
| --- | --- |
| `schema_version` | 1 |
| `producer`, `producer_version` | `amq-squad` and its version |
| `generated_at` | RFC 3339 UTC |
| `root`, `session` | the AMQ session root and session name |
| `project_path` | the squad's repository root, when known |
| `capabilities[]` | what this producer fills; see below |
| `buckets[]` | the four buckets in display order: `id`, `label`, `rule` |
| `counts` | rows per bucket: `all`, `needs_you`, `working`, `ready`, `inactive`; a row is a task, or a seat that holds no task |
| `seats[]` | seat rows |
| `tasks[]` | task rows |
| `attention[]` | the attention queue |
| `gates[]` | gate records of the gate store |
| `actions.new_task` | the "new task" action |
| `scheduler` | `max_seats`, `live_seats`, `usage_limit_since`, `usage_limit_until` |
| `warnings[]` | read problems, one line each; a partial status is still valid |

Seat row: `handle`, `role`, `runtime`, `model`, `effort`, `state`,
`reason_code`, `bucket`, `bucket_marker`, `task_id` (the leased task, or
empty), `project_path`, `last_activity_at`, `last_message_at`,
`last_message` (at most 200 characters), `prompt_state` (`running`, `idle`,
`unknown`), `tokens_in`, `tokens_out` (null when the runtime gives no
number).

Task row: `id`, `title`, `class`, `task_state` (the store state), `state`,
`reason_code`, `bucket`, `bucket_marker`, `pool` (`{runtime, model,
effort}` or null), `lease_holder`, `lease_expires_at`, `lease_claimed`,
`attempt`, `wait_reason`, `block_reason`, `depends_on`, `project_path`,
`created_at`, `updated_at`.

Attention item: `id` (stable across runs for the same condition), `kind`,
`severity` (`high`, `medium`, `low`), `subject`, `since`, `seat`, `task_id`,
`gate_id`, `bucket`, `action`.

Action: `id` (stable), `label`, `argv` (an array; `argv[0]` is a bare
program name, never a shell), `inputs[]` (`name`, `flag`, `required`,
`choices`). The renderer runs `argv` plus, for each input the user gave, its
`flag` and value. Never a shell string (4.6).

## Classifier states

Every seat and task row carries one state from a closed list: the five
states of 4.6 plus `unknown`.

| state | seat row | task row |
| --- | --- | --- |
| `needs_you` | the seat has an open typed gate | blocked with `block_route: owner`, or an open gate bound to the task |
| `blocked` | its task is blocked (lead) | blocked with `block_route: lead` |
| `waiting` | holds a lease it has not claimed | pending, or leased and not yet claimed |
| `online` | live (process, presence or evidence within the freshness window) | leased, claimed, lease not expired |
| `stale` | signals exist but none is live | lease expired, or the task is completed or cancelled |
| `unknown` | no signal at all | an unreadable or unknown store state |

## Bucket table

The contract names the bucket of every row; amq-noc does not compute it.
`internal/status.BucketFor` is the table (plan 4.6, briefs ruling 6).

| state | condition | bucket |
| --- | --- | --- |
| `needs_you` | | Needs you |
| `blocked` | routed to the owner | Needs you |
| `blocked` | routed to the lead | Working |
| `waiting` | | Working |
| `online` | with a task | Working |
| `online` | no task | Ready |
| `stale` | | Inactive |
| `unknown` | | Inactive, `bucket_marker: "unknown"` |

This fixes the two open cells of plan 4.6 as proposed there: `blocked` goes
to Needs you only when routed to the owner, else to Working; `waiting` goes
to Working.

## Capabilities

`capabilities[]` lists what this producer fills. A field whose capability
is absent is present with its empty or null value. Schema 1 names:
`tasks`, `leases`, `pools`, `attention`, `gates`, `buckets`,
`new_task_action`, and the per-source seat capabilities
`seat.last_message:<source>`, `seat.prompt_state:<source>` and
`seat.tokens:<source>`. A seat's `tokens_in`/`tokens_out` are null when its
runtime reports no number (Claude Code hook payloads carry none; briefs
lead ruling 5), and `seat.tokens:evidence_ledger` means the producer sums
token counts found in evidence ledger payloads.
