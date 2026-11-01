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
`usage_limit_since`, `usage_limit_until`; document `lead_handle`
(`status.NullableFields`). Null
anywhere else, or a null document, fails the check.

## Document

| field | meaning |
| --- | --- |
| `schema_version` | 1 |
| `producer`, `producer_version` | `amq-squad` and its version |
| `generated_at` | RFC 3339 UTC |
| `root`, `session` | the AMQ session root and session name |
| `project_path` | the squad's repository root, when known |
| `lead_handle` | the handle of the seat whose role is `lead`, `null` when no seat has it; in the fleet form, fleet.yaml's lead handle fills a null one (additive; 4-3 acceptance 7) |
| `capabilities[]` | what this producer fills; see below |
| `buckets[]` | the four buckets in display order: `id`, `label`, `rule` |
| `counts` | rows per bucket: `all`, `needs_you`, `working`, `ready`, `inactive`; a row is a task, or a seat that holds no task |
| `seats[]` | seat rows |
| `tasks[]` | task rows |
| `attention[]` | the attention queue |
| `gates[]` | gate records of the gate store |
| `actions.new_task` | the "new task" action |
| `scheduler` | `max_seats`, `live_seats`, `usage_limit_since`, `usage_limit_until`; and from the scheduler's launch record (brief 31-1): `pid`, `started_at` (its own admission, null before a scheduler ran), `launch_start` (the squad launch's start, `AMQ_SQUAD_START`: the launch budget's first mark; null when the scheduler was started by hand), `live` (its pid answers) |
| `warnings[]` | read problems, one line each; a partial status is still valid |
| `lead` | the holder of the root's lead lock, from `lead.json` (brief 31-1), null when no lead took it: `handle`, `pid`, `start` (its raw start stamp), `pane` (its tmux pane, empty outside tmux), `recorded_at`, `live` (the pid answers with its recorded start stamp), `attach` (`tmux attach -t <pane>`, empty without a pane) |
| `decisions[]` | the scheduler's decisions log, its last 30 lines oldest first: `at`, `kind`, `seat`, `task_id`, `reason`, `reason_code`, `stage`. Refusals are among them (`exit_refused`, `roster_launch_failed`, `seat_not_up`, `commit_refused`, `up_stopped_lead`, ...) |
| `train` | the merge train's audit log: `runs[]`, its last two runs newest first (`run`, `started_at`, `last_at`, `last_kind`, `repository`, `base`, `prs`, `merged`, `refusal`, the run's newest refusal or stop, `done`: it stopped, it was a plan, or every PR of its batch merged, `dry_run`); `alerts[]`, the owner alerts (`id`, `kind`, `detail`, `gate_id`, `at`) |

Seat row: `handle`, `role`, `runtime`, `model`, `effort`, `state`, `live`
(true when the seat's session is up or its lease is live, whatever its state
or bucket: an open gate or a block overrides liveness in `state`, never here),
`reason_code`, `bucket`, `bucket_marker`, `task_id` (the leased task, or
empty), `project_path`, `last_activity_at`, `last_message_at`,
`last_message` (at most 200 characters), `prompt_state` (`running`, `idle`,
`unknown`), `tokens_in`, `tokens_out` (null when the runtime gives no
number), `warnings` (absent when none: one `<code>: <detail>` line per
seat warning, brief 31-1: `claude_hooks_silent`, a Claude seat that ran
with no hook report since its launch; `claude_allow_rules_present`, its
worktree's Claude allow rules; `claude_external_includes_pending`, a
CLAUDE.md it loads that imports a file outside its worktree; each was a
launch refusal before 3.1 and is a line, never a refusal, now; and from the
second PR: `launch_budget_exceeded`, a seat over the launch ceiling, naming
its slowest segment up to seat live; `wake_off`, a seat whose admitted wake
is off, with AMQ's audit reason (a Codex seat without AMQ's dialog hook, a
pi seat); `seat_stalled_at_dialog`, a seat at an open harness dialog (AMQ's
`.wake.dialog` marker in its mailbox); `pi_provider_key_missing`, a pi seat
whose `PROVIDER/ID` model has no key in its env needs or its pi login).
Brief 31-2 adds `acknowledged_warnings` (absent when none): the seat's
warnings the owner cleared with the screen's clear key for the seat's
current launch (`launch.launch_id`), each listed here instead of in
`warnings`. A clear is recorded by the scheduler as a `warning_cleared`
decision and a line in the scheduler directory's `clears.jsonl`; it names
the warning's code and the launch, so the same condition on a later launch
is in `warnings` again. A clear never changes `state`, `reason_code`,
`bucket` or `live`.
Brief 31-1 adds `worktree` and `branch` (from its launch record, empty
before one) and `launch`, its last launch, null without a staged record:
`launch_id`; `outcome` (`staged`, `applied`, `apply_failed`,
`exec_refused`, `proof_expired`, `live`: its accepted proof) with
`reason_code`; `wake`, AMQ's admitted wake (`mode`, `require_wake`,
`injector`, `audit_reason`), null when none was recorded; `added_at`, the add
key's press that added the seat to squad.yaml (brief 31-2 PR 2: its
`marks.json`, carried into every later launch's), null for a seat that was in
the squad at its start, never the launch's start; and `timing`, the
launch budget:

| `timing` field | meaning |
| --- | --- |
| `start` | the start of the seat's own launch (its `marks.json`, beside its staged record: for the launch at `scheduler run`, the squad launch's `AMQ_SQUAD_START`, which `amq-squad up` sets; for a relaunch, the relaunch decision's time, or the add key's press for a seat the relaunch's reload added, `scheduler relaunch --start`), null when its launch recorded none or its proof predates it (an adopted seat keeps its own launch's start) |
| `seat_live` | the accepted proof's stamp (`proof.json`'s `at`): the pane runs the provider with its bootstrap delivered |
| `first_message` | the seat's first AMQ message since this launch (the message's own `created`, recorded in the seat's `marks.json` by the scheduler, which also logs `seat_first_message`), null before one |
| `to_seat_live_ms`, `to_first_message_ms` | the start to each mark, null when either end is missing |
| `ceiling_ms` | 10000: the ceiling binds the start to seat live; the first message has none |
| `over_ceiling` | `to_seat_live_ms` is over the ceiling (the seat also carries `launch_budget_exceeded`) |
| `slowest_segment` | the longest of the segments |
| `segments[]` | `name` and `ms` (null when a record bounding it is missing), in order: `drain`, for every seat a relaunch launched (its start, the relaunch's stamp or the key press, to the end of the relaunch's drain, `drain_end` in its `marks.json`), `prepare` (the start, or the drain's end, to the staged record's `started_at`), `mint` (to the proof ticket's `issued_at`), `apply` (to AMQ's apply outcome), `seat_exec` (to the accepted proof) |

Task row: `id`, `title`, `class`, `task_state` (the store state), `state`,
`reason_code`, `bucket`, `bucket_marker`, `pool` (`{runtime, model,
effort}` or null), `lease_holder`, `lease_expires_at`, `lease_claimed`,
`attempt`, `wait_reason`, `block_reason`, `depends_on`, `project_path`,
`created_at`, `updated_at`; and (brief 31-1) `evidence`, `pr_repository`,
`pr_number`, `pr_head` (the PR the commit step, or the train's rebase,
bound to it; empty and 0 when none); `wait_holders`, for a pending task
whose `wait_reason` is `file_lock`, the ids of the tasks holding the lock by
the task store's rule (in progress or blocked, declared files overlapping),
`[]` otherwise; empty under `file_lock` means no task holds it any more (a
stale wait).

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

## Fleet form

`amq-squad status --fleet --json` reads the fleet registry
`~/.amq-squad/fleet.yaml` (plan 16.3) and prints one document (briefs ruling
16):

```
{"schema_version": 1, "kind": "fleet", "producer": "amq-squad",
 "producer_version": "...", "generated_at": "<RFC 3339 UTC>",
 "squads": [<one schema 1 status document per squad, unchanged>]}
```

The squads are in registry order. Each is the single-squad document of its
root, unchanged except that a null `lead_handle` takes the squad's lead
handle from fleet.yaml. The single-squad document carries no `kind`; a
reader treats a document with `squads` as a fleet and anything else as one
squad (`status.IsFleet`). The reference is
`internal/status/testdata/status-fleet-v1.golden.json`, whose one squad is
the single-squad golden with `lead_handle` null; a test keeps the two in
step. The operator mailbox is the owner's `user` handle; the contract
carries no operator field.

`waiting[]` (additive to the ruling 16 shape) lists the squads the mayor
declined or deferred for the fleet grant's cap and that therefore have no
squad document: `squad`, `repository`, `project_path`, `wait_reason`
(`fleet_cap`), `cap` (`max_squads` or `cost_per_day`), `since` (the first
hold, RFC 3339 UTC) and `detail`. A squad leaves the list when it starts.

`operator` (additive) is the operator session's own seat row: handle `user`,
role `operator`, classified and bucketed like any seat row, from the
operator's AMQ root named in fleet.yaml, when the session has left a launch
record, presence or evidence there; `null` otherwise. A squad document
still skips the `user` handle. Like every seat row it carries `live`; the
Buzz attachment reads the operator's presence from it (4-5).

`lead_handle` is a document field (one per squad document) and `live` a seat
field (one per seat row and on `operator`); neither replaces the other: the
lead's presence is its seat row's `live`, found by `lead_handle`.

## Recorded limits

- Every free-text field (seat message bodies, warnings, wake audit
  reasons, task titles, evidence, block and wait reasons with the reason
  codes and attention ids that copy them, attention subjects, decision
  reasons, the train's refusals and alert details) passes `redact.Block`
  before it is serialized: the document feeds the screen and the lead's
  model.
- The status read takes no lock (brief 31-1): `lead.live` reads the pid and
  its start stamp, never the lead lock, and `scheduler.live` signals the
  recorded pid only.
- A roster relaunch (`scheduler relaunch`) records its own start, the
  relaunch decision's time, for every seat it launches, and a `drain_end`;
  a seat its reload added records the key press (`--start`) as the start of
  that relaunch, and as `launch.added_at` from then on. A relaunched seat's
  budget includes the drain (its `drain` segment), so it is not a cold
  launch's.

- `live` from a launch record is the recorded `AgentPID` answering signal-0
  (`status.Collect`); the binary and the process birth time are not checked,
  so a pid reused after a seat or lead died without a `StoppedAt` reads live.
  Presence and the Buzz attachment inherit it. 3.0.x follow-up
  (docs/v3-followups.md).
