# 3.3.0 S2-1: the task machine's moves and outcome sets, driven

Base: `origin/v3` at `f56af9c8b07239fa5a52910e0c42f20df180ba59`. Tests only:
no product file changes. The plan reviewer's ruling of 2026-11-16 on the
S2/S3 scope (answers 1 and 2), and its ruling on this task's brief, are the
contract this note reports against, with the plan reviewer's round-2
ruling on Codex's four findings against #1008 (the method each row runs,
the whole block group, the returned record against the disk record, the
seeded blocked fixtures). Line cites are at that base.

## What is driven

- `internal/task/s2_moves_test.go`
  - `TestEveryMoveIsDrivenThroughTheStore`: each of the 20 rows of
    `task.Moves()` (`internal/task/contract.go:63-86`), each Via method of the
    row, each Who the driver names for that method: 41 subtests. Each builds a
    fresh store and a task in the row's From state that meets the row's
    Needs, and checks the From on disk. The test, not the driver, picks the
    method: it calls `reflect.ValueOf(store).MethodByName(via)` with the
    driver's arguments, so each (row, method) runs exactly the method the
    contract's Via names, and no shared helper can run another. It then checks
    the record re-read from disk against the row's To; the returned record
    against the disk record, the whole decoded struct, field by field (the
    named differences below); and the five block-group fields against exact
    expected values per (row, method). The expected values are test data
    written from the code, with the same status as the Who reading below:
    the reason, route, detail, attempt and paths each call writes (for
    example Escalate: `growth_gate_required`, `owner`, its detail, 0, none),
    the empty group after a row with ClearsBlockGroup, the group before for
    the one known disagreement (blocked to cancelled via Cancel), and the
    empty group before and after every other row. A row with no driver, a
    driver with no row, or a Who the row does not name fails the test, and
    the union of the drivers' Who per row must equal the row's Who. Every
    blocked From starts with all five block fields non-empty: the fixtures
    write a seed (`seed detail`, attempt 7, path `seed.go`) into any field the
    store's own block left empty, and the test checks all five before the
    call, so each "cleared" and "kept" check starts from a non-empty value.
    The known disagreements are read from
    `knownDisagreements` in `internal/task/contract_test.go`, the one F5 list
    `TestLeavingBlockedClearsTheBlockGroup` reads too: a row that keeps the
    block group needs exactly one entry, and an entry no driven row meets
    fails the test.
  - `TestEveryMoveRefusesAnUndeclaredFrom`: per (row, method), the method,
    called through the same `MethodByName` with the row's own arguments, on a
    task in a state the table lists for no move of that method, is refused,
    and the task file is byte-equal before and after (32 subtests). Two rows
    have a stand-in. The add rows have no From, so their stand-in is a refusal
    with no From: an add whose dependency does not exist is refused, and no
    task file is written. ExpireDue takes no id, so its stand-in is a no-op,
    not a refusal: on a blocked task it returns nothing, and the task file is
    byte-equal before and after.
- `internal/routing/s2_outcome_sets_test.go`: `router_retry_effect`
  (`effect`, its `count`), `weekly_report_failed` (`failedOutcome`),
  `verifier_fail_without_verdict` (`verifierResult`). Each of the eight
  outcomes; each conditional driven both ways; each condition's text compared
  with the one the test drives.
- `internal/scheduler/s2_outcome_sets_test.go`: `retry_budget`
  (`DefaultRouter.Route` at MaxAttempts 1), `exit_scan` (`scanAttemptsLog`
  with no Preparer), `seat_lapsed` (`idleSeatExcept`, three join cases), and
  `TestS2ExitScanCancelledWithACleaner`.

## The Who of each method

The store checks two Who itself: `live_holder` (`requireLiveHolder`,
`internal/task/lease.go:224-232`) and `creator` (`ResetBy`,
`internal/task/reset_by.go`). The other four (`scheduler`, `harness_shell`,
`proven_lead`, `asserted_handle`) the store does not check; their callers
prove them, and which Who calls which method is this test's reading. Each
driver names the Who that calls its method: Grant scheduler; GrantClean,
Claim, ClaimServing asserted_handle; Block and BlockWithPaths from pending
scheduler and asserted_handle, from in progress live_holder; Complete,
CompleteBound, RefuseCommit, RefuseCommitPaths scheduler and harness_shell;
Done and Fail live_holder; Release and Cancel asserted_handle; ExpireDue,
Reset, ResetIfBlocked scheduler; ResetBy creator and asserted_handle; Widen,
WidenGated, Escalate proven_lead.

## Known differences

- The returned record: three methods return a record whose `UpdatedAt`
  differs from the record they wrote, and no other field differs for any
  method. `Claim` (`internal/task/lease.go:359`), `ClaimServing` (`:430`) and
  `ExpireDue` (`:1072`) take their returned copy inside the store's update,
  before the commit stamps `UpdatedAt` (`internal/task/task.go:476-480`), and
  do not re-stamp it as `mutate` does (`task.go:502`), so they return the
  value from before the move. `UpdatedAt` is persisted, so this is a defect
  named for 3.2.x, not fixed here. The test names each (method, field) pair
  in `s2ReturnedDiffers`, fails on any other difference, and fails if a named
  pair no longer differs.
- Moves: the F5 list (`knownDisagreements`, `internal/task/contract_test.go`),
  its one entry: Cancel from blocked leaves the five block fields set
  (`internal/task/lease.go:968`).
- Outcome sets: with a Preparer that cleans attempts (the squad's), a
  cancelled attempt is queued by `scanAttemptsLog` as a close-and-clean item
  (`internal/scheduler/exits.go:479-521`). `exit_scan` does not list
  cancelled. This is a declaration error, not a behaviour defect.
  `TestS2ExitScanCancelledWithACleaner` names it. S2-2 adds the conditional
  `cancelled` entry to `exit_scan` in `internal/task/contract.go` and folds
  this test into the set's reader test.

## Facts found, not defects

- Widen and WidenGated on a pending or in-progress task, or on a blocked task
  with no report that is not a typed request, add declared files and move no
  state (`internal/task/scope.go:47-103`). The table lists only blocked for
  them, so their refusal row uses a terminal From.
- Claim on an in-progress task by its holder, and Done with declared files,
  move no state either.
- `idleSeatExcept` counts an unclaimed or expired attempt only when it was
  granted in the seat's current join (`internal/scheduler/scheduler.go:1100-1106`);
  the condition applies to both members of `seat_lapsed` alike. It is a fact
  of the reader, recorded here; the set gains no conditional entry for it.

## Rows

### R1

```text
cd /Users/omri/amq-squad-lead/T/s2s3/inputs && shasum -a 256 -c MANIFEST.sha256
docs/v3-spike/330-s2-1-driving/README.md: OK
internal/routing/s2_outcome_sets_test.go: OK
internal/scheduler/s2_outcome_sets_test.go: OK
internal/task/contract_test.go: OK
internal/task/s2_moves_test.go: OK
```

### R2

```text
cmp /Users/omri/amq-squad-lead/T/s2s3/inputs/internal/task/contract_test.go internal/task/contract_test.go
(no output; exit 0)

cmp /Users/omri/amq-squad-lead/T/s2s3/inputs/internal/task/s2_moves_test.go internal/task/s2_moves_test.go
(no output; exit 0)

cmp /Users/omri/amq-squad-lead/T/s2s3/inputs/internal/routing/s2_outcome_sets_test.go internal/routing/s2_outcome_sets_test.go
(no output; exit 0)

cmp /Users/omri/amq-squad-lead/T/s2s3/inputs/internal/scheduler/s2_outcome_sets_test.go internal/scheduler/s2_outcome_sets_test.go
(no output; exit 0)
```

### R2b

```text
cmp <(sed '/^## Rows/q' /Users/omri/amq-squad-lead/T/s2s3/inputs/docs/v3-spike/330-s2-1-driving/README.md) <(sed '/^## Rows/q' docs/v3-spike/330-s2-1-driving/README.md) && echo "the note above ## Rows is the input"
the note above ## Rows is the input
```

### R3

```text
git diff --numstat "$(git merge-base HEAD origin/v3)"
9	3	internal/task/contract_test.go

git status --porcelain --untracked-files=all
 M internal/task/contract_test.go
?? docs/v3-spike/330-s2-1-driving/README.md
?? internal/routing/s2_outcome_sets_test.go
?? internal/scheduler/s2_outcome_sets_test.go
?? internal/task/s2_moves_test.go
```

### R4

```text
go test ./internal/task/ -run '^(TestEveryMoveIsDrivenThroughTheStore|TestEveryMoveRefusesAnUndeclaredFrom)$' -count=1 -v -timeout 6m | grep -c -- '    --- PASS: '
73
```

### R5

```text
go test ./internal/routing/ -run '^TestS2' -count=1 -v -timeout 4m
=== RUN   TestS2RouterRetryEffectSetIsItsReader
--- PASS: TestS2RouterRetryEffectSetIsItsReader (0.00s)
=== RUN   TestS2WeeklyReportFailedSetIsItsReader
--- PASS: TestS2WeeklyReportFailedSetIsItsReader (0.00s)
=== RUN   TestS2VerifierFailWithoutVerdictSetIsItsReader
--- PASS: TestS2VerifierFailWithoutVerdictSetIsItsReader (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/routing	0.353s
```

### R6

```text
go test ./internal/scheduler/ -run '^TestS2' -count=1 -v -timeout 4m
=== RUN   TestS2RetryBudgetSetIsItsReader
--- PASS: TestS2RetryBudgetSetIsItsReader (0.00s)
=== RUN   TestS2ExitScanSetIsItsReader
--- PASS: TestS2ExitScanSetIsItsReader (0.00s)
=== RUN   TestS2ExitScanCancelledWithACleaner
--- PASS: TestS2ExitScanCancelledWithACleaner (0.00s)
=== RUN   TestS2SeatLapsedSetIsItsReader
--- PASS: TestS2SeatLapsedSetIsItsReader (0.00s)
PASS
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.371s
```

### R7

```text
go test ./internal/task/ -count=1 -timeout 7m
ok  	github.com/omriariav/amq-squad/v3/internal/task	67.253s

go test ./internal/routing/ -count=1 -timeout 7m
ok  	github.com/omriariav/amq-squad/v3/internal/routing	8.616s

go test ./internal/scheduler/ -count=1 -timeout 7m
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	27.535s
```

### R8

```text
go vet ./internal/task/ ./internal/routing/ ./internal/scheduler/
(no output; exit 0)

gofmt -l internal/task internal/routing internal/scheduler
(no output; exit 0)
```
