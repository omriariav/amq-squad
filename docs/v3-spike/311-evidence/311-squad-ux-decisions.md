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

## The kinds the scan found (68)

Stops (27): `doorbell_failed`, `exit_refused`, `exits_ledger_unwritable`, `launch_failures_unread`, `lead_notice_failed`, `lead_unknown`, `ledger_tampered`, `phase_record_failed`, `roster_incomplete`, `roster_launch_failed`, `roster_relaunch_refused`, `roster_reload_failed`, `routing_flush_failed`, `scheduler_stopped`, `seat_close_failed`, `seat_gone`, `seat_launch_failed`, `seat_liveness_unknown`, `seat_not_up`, `seat_prepare_failed`, `seat_refused`, `seat_replacement_needs_relaunch`, `seat_request_expired`, `seat_request_failed`, `tick_error`, `up_stopped_lead`, `up_stopped_scheduler`.

Routine (41): `blocked`, `commit_adopted`, `commit_cancelled`, `commit_cancelling`, `commit_deferred`, `commit_refused`, `commit_running`, `completed`, `decision_socket_open`, `discovery_stale`, `dispatch_held`, `dispatched`, `exit_check_deferred`, `exit_check_duplicate`, `launch_failures_restored`, `lease_expired`, `lease_renewed`, `legacy_merge_head_missing`, `pr_bound`, `roster_adopted`, `roster_launched`, `roster_over_cap`, `roster_relaunch_requested`, `roster_reloaded`, `scheduler_started`, `seat_adopted`, `seat_adopted_closing`, `seat_cleaned`, `seat_close_requested`, `seat_closed`, `seat_closing`, `seat_first_message`, `seat_launch_retry`, `seat_request_cancel_pending`, `seat_request_cancelled`, `seat_requested`, `seat_up`, `session_created`, `task_reset`, `usage_limit`, `warning_cleared`.

## Rows

| row | what | where |
| --- | --- | --- |
| A | every decision kind Go writes is a stop with words or a routine record; no kind is both; every stop's words are plain and say what to do | pi/test/words.test.ts "row 7: every decision kind Go writes is a stop with words or a routine record" |
| B | an unknown kind shows "stopped something" or "recorded something", its kind below the rule; a routine record newer than a stop hides nothing; each stop kind, the newest decision, shows its words | pi/test/words.test.ts "row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop" |

## Revert runs

`python3 docs/v3-spike/311-evidence/311-squad-ux-decisions-revert-runs.py`
from a clean checkout: 6 runs, 6 caught. Each reverts production code only.

| # | row | revert | result | the failing output's first line |
| --- | --- | --- | --- | --- |
| 1 | A | a stop kind dropped from the table (seat_gone) | caught | `not ok 1 - row 7: every decision kind Go writes is a stop with words or a routine record` |
| 2 | A | a routine kind dropped from the table (legacy_merge_head_missing) | caught | `not ok 1 - row 7: every decision kind Go writes is a stop with words or a routine record` |
| 3 | B | the selector shows only the stops it knows (an unknown kind hidden) | caught | `not ok 1 - row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop` |
| 4 | B | an unknown kind's words as the 3.1.1-3 fallback (no kind below the line) | caught | `not ok 1 - row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop` |
| 5 | B | an unknown kind's record without its kind | caught | `not ok 1 - row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop` |
| 6 | B | a routine record treated as a stop (it hides the stop before it) | caught | `not ok 1 - row 7: an unknown decision kind shows, its kind below the rule; routine records hide no stop` |

## Gates

At code head `032e0172`:

- `go build ./... && go vet ./... && go test -count=1 ./...`: exit 0, 56 packages ok, 0 FAIL lines. Tail:

```
?   	github.com/omriariav/amq-squad/v3/scripts/redact-evidence	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/claude-probes	[no test files]
?   	github.com/omriariav/amq-squad/v3/spike/pi-r19	[no test files]
exit 0
```

- `cd pi && npm ci --offline --ignore-scripts && npm test`: exit 0. Tail:

```
ℹ tests 108
ℹ suites 0
ℹ pass 108
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
ℹ duration_ms 16033.037375
exit 0
```

- `make ci` (PyYAML venv on PATH): exit 0. Tail:

```
PERF pre-tool allow in a seat: p50 22.4 ms, p95 23.6 ms
PERF outside a seat: 60 runs, p50 6.1 ms, p95 6.7 ms, max 7.3 ms (budget p95 < 20 ms: met)
exit 0
```
