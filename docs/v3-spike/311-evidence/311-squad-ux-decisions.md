# 3.1.1-3 follow-up: the decision kinds in the words table's scan

Authority: the plan reviewer's ruling on Codex's last finding on #863. Branch
`v3-311-ux2`, from `a9d83c02` (#863 merged).

## The gap

The words table's completeness check (pi/test/words.test.ts, row 7) read
the train's Entry kinds, the reasons, the warnings and the rules from Go,
but not the scheduler's decision kinds; the decision tests iterated only
kinds already in the table, and the seats block's selector showed only a
fixed list of stop kinds. A new stop decision added in Go without words was
hidden from the screen with every test passing.

## The change

- **One source: Go, read at test time.** The scan reads every
  `Decision{Kind: ...}` in the non-test files of the decision log's three
  writers: internal/scheduler (the scheduler's own decisions),
  internal/schedproc (the scheduler process's: start, stop, adoption, the
  warning clears) and internal/cli (which appends through
  `schedproc.AppendDecision`: `up`, the lead's phase record, the discovery
  record). A kind is a string literal, a constant of those packages, or a
  call of up.go's `stopped(KIND, ...)` helper; an identifier the scan cannot
  resolve fails the row (except that helper's own `kind` parameter, covered
  by its call sites). No Go file changed: the sources are the list.
- **Every kind is a stop or routine.** A stop (`DECISION_WORDS`, pi
  extensions/lib/words.ts) shows in the seats block as what happened and
  what to do; a routine record (`ROUTINE_DECISIONS`) shows nothing. A kind
  in neither fails the row by name. Seventeen kinds that need the lead's
  look became stops beside the 3.1.1-3 ten: `roster_reload_failed`,
  `roster_relaunch_refused`, `roster_incomplete`, `seat_gone`,
  `seat_liveness_unknown`, `seat_request_expired`,
  `seat_replacement_needs_relaunch`, `exits_ledger_unwritable`,
  `doorbell_failed`, `lead_notice_failed`, `launch_failures_unread`,
  `lead_unknown`, `routing_flush_failed`, `phase_record_failed`,
  `tick_error`, `up_stopped_lead`, `up_stopped_scheduler`.
  `legacy_merge_head_missing` (#865: a task dispatched though a dependency's
  merge record has no head, written before 3.1.1) is routine: the dispatch
  goes on.
- **At run time a kind no table knows is shown, never hidden.** The seats
  block's selector shows the newest decision that is a stop or an unknown
  kind (a clean scheduler stop, with no reason, stays routine). An unknown
  kind reads "the scheduler stopped something; its kind is below the line"
  when its name reads as a failure (it ends `_failed`, `_refused`,
  `_error`, `_unwritable`, `_unreadable`, `_missing`, `_expired`, `_gone`,
  `_unknown`, `_tampered`, `_invalid` or `_lost`), else "the scheduler
  recorded something; its kind is below the line"; below the rule its kind,
  the seat and the reason.

## What clears a shown stop (the plan reviewer on #866)

A shown stop clears when a later decision resolves it (the same seat's
`seat_up`, or a `roster_launched`, for a seat's stop; the same task's
`dispatched`, or a `roster_launched`, for a seat request's stop, which
records the task and no seat; a `roster_launched` or `roster_reloaded` for a
roster's stop; a `scheduler_started` for a scheduler's stop), and any other
stop clears only when it leaves the status document's last 30 decisions;
`/dismiss` acts on seat warnings, not on decisions, so it clears none.

This answers Codex's first gap on #866 too: after `seat_gone` (dev), then
`seat_up` (dev) and `roster_launched`, the seats block no longer says "seat
dev's process is gone"; the row runs exactly that sequence.

The rule is `RESOLVED_BY` in pi/extensions/lib/words.ts: the seat stops
(`seat_not_up`, `seat_launch_failed`, `seat_prepare_failed`,
`seat_refused`, `seat_gone`, `seat_liveness_unknown`), the request's stop
(`seat_request_expired`, by its task), the roster stops (`roster_launch_failed`,
`roster_reload_failed`, `roster_relaunch_refused`, `roster_incomplete`,
`seat_replacement_needs_relaunch`) and the scheduler stops
(`scheduler_stopped`, `lead_unknown`, `up_stopped_scheduler`). Go logs
`seat_up` with the seat's handle and `roster_launched` with none, so a
roster launch resolves every seat's stop. When the newest stop is
resolved, the seats block shows the newest unresolved one before it.

**Known limit.** The other stops have no resolving record in the log:
`tick_error` (a successful tick logs nothing), `exit_refused`,
`ledger_tampered`, `seat_close_failed`, `doorbell_failed`,
`lead_notice_failed`, `exits_ledger_unwritable`, `launch_failures_unread`,
`routing_flush_failed`, `phase_record_failed`, `seat_request_failed` and
`up_stopped_lead`. Each shows until it leaves the last 30 decisions
(`DecisionsTail`). A morning `tick_error` on a quiet squad can stay all day.
3.1.1 leaves that as it is: clearing one would need a decision a resolving
action writes (an owner's acknowledgement of a decision, which
`scheduler clear` does not take today).

## Each sentence names the field Go records (Codex on #866)

`seat_request_expired` records the task and no seat (internal/scheduler
scheduler.go), and its sentence said "the request for seat t9 expired",
naming the task as a seat. Each stop's words now take the decision's own
fields: a seat's sentence names `Seat` and reads "a seat" when the record
has none, never the task's id in its place; `seat_request_expired` and
`seat_request_failed` name the task ("the seat request for t9 expired before
a seat came up"); `doorbell_failed` names both ("seat dev could not be rung
for t9"); `roster_incomplete` and `seat_replacement_needs_relaunch` name the
seats Go joins into `Seat`. Checked against the Go writers: `seat_gone`,
`seat_liveness_unknown`, `seat_close_failed`, `seat_refused`, `seat_not_up`,
`seat_launch_failed` and `exit_refused` record the seat;
`seat_prepare_failed` and `doorbell_failed` the seat and the task;
`seat_request_failed`, `seat_request_expired` and `lead_notice_failed` the
task.

## The scan walks braces (Codex on #866)

The literal pattern stopped at a brace inside a string, so
`Decision{Reason: fmt.Sprintf("{}"), Kind: "new_failed"}` was skipped. The
scan now walks each `Decision{...}` literal by brace depth, skipping
interpreted strings, raw strings, runes and comments, and reads `Kind:` at
the literal's own depth only (a nested struct's `Kind` is not the
decision's). A fixture row pins a brace inside a string, a raw string with
braces, a comment with a brace and a nested struct's `Kind`.

## Handles in the row 4 sweep (Codex on #866)

A seat's handle is a name, not a code: every seat line carries it, so it
stays above the rule. The sweep allows a token equal to a handle of the
document's seats (`seats[].handle`), and its fixture has a snake_case handle
(`senior_dev`) and a hex-looking one (`1deadbeef`), on their seat lines and
in the stop lines; the same token that is no handle still fails. This is a
rule of the row, not of the screen, which shows handles as it did.

## The kinds the scan found (68)

Stops (27): `doorbell_failed`, `exit_refused`, `exits_ledger_unwritable`, `launch_failures_unread`, `lead_notice_failed`, `lead_unknown`, `ledger_tampered`, `phase_record_failed`, `roster_incomplete`, `roster_launch_failed`, `roster_relaunch_refused`, `roster_reload_failed`, `routing_flush_failed`, `scheduler_stopped`, `seat_close_failed`, `seat_gone`, `seat_launch_failed`, `seat_liveness_unknown`, `seat_not_up`, `seat_prepare_failed`, `seat_refused`, `seat_replacement_needs_relaunch`, `seat_request_expired`, `seat_request_failed`, `tick_error`, `up_stopped_lead`, `up_stopped_scheduler`.

Routine (41): `blocked`, `commit_adopted`, `commit_cancelled`, `commit_cancelling`, `commit_deferred`, `commit_refused`, `commit_running`, `completed`, `decision_socket_open`, `discovery_stale`, `dispatch_held`, `dispatched`, `exit_check_deferred`, `exit_check_duplicate`, `launch_failures_restored`, `lease_expired`, `lease_renewed`, `legacy_merge_head_missing`, `pr_bound`, `roster_adopted`, `roster_launched`, `roster_over_cap`, `roster_relaunch_requested`, `roster_reloaded`, `scheduler_started`, `seat_adopted`, `seat_adopted_closing`, `seat_cleaned`, `seat_close_requested`, `seat_closed`, `seat_closing`, `seat_first_message`, `seat_launch_retry`, `seat_request_cancel_pending`, `seat_request_cancelled`, `seat_requested`, `seat_up`, `session_created`, `task_reset`, `usage_limit`, `warning_cleared`.

## Rows

| row | what | where |
| --- | --- | --- |
| A | every decision kind Go writes is a stop with words or a routine record; no kind is both; every stop's words are plain, say what to do and name the field Go records (no task id as a seat) | pi/test/words.test.ts "row 7: every decision kind Go writes is a stop with words or a routine record" |
| C | a stop followed by its resolving decision no longer shows (the same seat's `seat_up`, a roster launch, the scheduler starting again; Codex's seat_gone, seat_up, roster_launched); a request's stop clears on its task's `dispatched`, not another task's; another seat's `seat_up` clears nothing; a cleared newest stop leaves an older unresolved one showing; a `tick_error` stays; each resolver is a routine record | pi/test/words.test.ts "row 7: a stop followed by its resolving decision no longer shows" |
| D | the decision scan reads Kind past a brace in a string, a raw string and a comment, and not a nested struct's | pi/test/words.test.ts "row 7: the decision scan reads Kind past braces in strings" |
| B | an unknown kind shows "stopped something" or "recorded something", its kind below the rule; a routine record newer than a stop hides nothing; each stop kind, the newest decision, shows its words | pi/test/words.test.ts "row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop" |

## Revert runs

`python3 docs/v3-spike/311-evidence/311-squad-ux-decisions-revert-runs.py`
from a clean checkout, at code head `d7ca3895`: 13 runs, 13 caught. Each
reverts production code only: cases 1 to 6 for the scan and the run time,
7 to 10 for the clearing rule, 11 to 13 for Codex's field and request fixes.
The brace-walking scan and the sweep's handle allowance are the rows' own
code, so their guard is the fixture row and the fixture's handles, not a
revert of production.

| # | row | revert | result | the failing output's first line |
| --- | --- | --- | --- | --- |
| 1 | A | a stop kind dropped from the table (seat_gone) | caught | `not ok 1 - row 7: every decision kind Go writes is a stop with words or a routine record` |
| 2 | A | a routine kind dropped from the table (legacy_merge_head_missing) | caught | `not ok 1 - row 7: every decision kind Go writes is a stop with words or a routine record` |
| 3 | B | the selector shows only the stops it knows (an unknown kind hidden) | caught | `not ok 1 - row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop` |
| 4 | B | an unknown kind's words as the 3.1.1-3 fallback (no kind below the line) | caught | `not ok 1 - row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop` |
| 5 | B | an unknown kind's record without its kind | caught | `not ok 1 - row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop` |
| 6 | B | a routine record treated as a stop (it hides the stop before it) | caught | `not ok 1 - row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop` |
| 7 | C | no clearing: a resolved stop still shows | caught | `not ok 1 - row 7: a stop followed by its resolving decision no longer shows` |
| 8 | C | another seat's seat_up clears a seat's stop | caught | `not ok 1 - row 7: a stop followed by its resolving decision no longer shows` |
| 9 | C | the scheduler starting again does not clear its stop | caught | `not ok 1 - row 7: a stop followed by its resolving decision no longer shows` |
| 10 | C | a resolved newest stop hides the older unresolved one | caught | `not ok 1 - row 7: a stop followed by its resolving decision no longer shows` |
| 11 | D | seat_request_expired's sentence names the task as a seat | caught | `not ok 1 - row 7: every decision kind Go writes is a stop with words or a routine record` |
| 12 | D | a seat's sentence falls back to the task's id | caught | `not ok 1 - row 7: every decision kind Go writes is a stop with words or a routine record` |
| 13 | D | a request's stop matched by seat, which it does not record | caught | `not ok 1 - row 7: a stop followed by its resolving decision no longer shows` |

## Gates

At code head `d7ca3895`:

- `go build ./... && go vet ./... && go test -count=1 ./...`: exit 0, 56 packages ok, 0 FAIL lines. Tail:

```
?   	github.com/omriariav/amq-squad/v3/scripts/redact-evidence	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/claude-probes	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/pi-r19	[no test files]
exit 0
```

- `cd pi && npm ci --offline --ignore-scripts && npm test`: exit 0. Tail:

```
ℹ tests 110
ℹ suites 0
ℹ pass 110
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 15639.287666
exit 0
```

- `make ci` (PyYAML venv on PATH): exit 0. Tail:

```
PERF pre-tool allow in a seat: p50 21.1 ms, p95 21.5 ms
PERF outside a seat: 60 runs, p50 5.7 ms, p95 6.2 ms, max 6.4 ms (budget p95 < 20 ms: met)
exit 0
```
