# Task state contract

line numbers as of 0c72f069e388ef461e6c0b341a9156c291a4d4a4

This note records the task machine implemented on that base. The declaration
does not change a command, reader, writer, or persisted string.

## State writes

Command:

```sh
rg -n '\.State\s*(,[^=]*)?=[^=]|State:\s' internal/task -g '*.go' -g '!**/*_test.go'
```

The command returned 13 lines:

| Site | Function | To |
|---|---|---|
| `internal/task/task.go:276` | `AddWithFileWaits` | `pending` |
| `internal/task/lease.go:90` | `grant` | `in_progress` |
| `internal/task/lease.go:744` | `complete` | `completed` |
| `internal/task/lease.go:818` | `block` | `blocked` |
| `internal/task/lease.go:831` | `Fail` | `pending` |
| `internal/task/lease.go:903` | `Release` | `pending` |
| `internal/task/lease.go:915` | `Reset` | `pending` |
| `internal/task/lease.go:968` | `Cancel` | `cancelled` |
| `internal/task/lease.go:1070` | `ExpireDue` | `pending` |
| `internal/task/scope.go:91` | `WidenGated` | `in_progress` |
| `internal/task/scope.go:97` | `WidenGated` | `pending` |
| `internal/task/reset_by.go:34` | `ResetBy` | `pending` |
| `internal/task/reset_if.go:29` | `ResetIfBlocked` | `pending` |

## Moves

“None” in the who column means the store accepts a caller-supplied handle or
is reached from a scheduler/harness path without checking that identity in the
writer. The proven-lead rows run after the decision socket proves its
origin at `internal/schedproc/decide.go:281,292`; the verified apply callback
reaches `widenDecision` through `internal/cli/scheduler_run.go:603-607`.

| From | Via | Writer | To | From guard / needs | Who check | Block-group clearing | Differs from this brief |
|---|---|---|---|---|---|---|---|
| new | `Add`, `AddWithFileWaits` | `AddWithFileWaits` | pending | creation transaction `task.go:263-276` | none; asserted `CreatedBy` stored at `task.go:277` | not applicable | no |
| pending | `Grant`, `GrantClean`, `Claim`, `ClaimServing` | `grant` | in_progress | see “Needs, row by row” | `Grant` is scheduler-facing; `GrantClean` is lead dispatch; `Claim` and `ClaimServing` accept the seat's asserted holder | none needed | no |
| pending | `Block`, `BlockWithPaths` | `block` | blocked | pending branch `lease.go:868-871` | none | move sets the group at `lease.go:818-819` | no |
| pending | `Cancel` | `Cancel` | cancelled | non-terminal guard `lease.go:960` | none | none | no |
| in_progress | `Done` | `complete` | completed | no prior report and no declared files; verdict rule `lease.go:719-739` | live holder `lease.go:689` | all five at `lease.go:744` | no |
| in_progress | `Complete`, `CompleteBound` | `complete` | completed | report and binding checks; verdict rule `lease.go:719-739` | none in store; scheduler/harness callers | all five at `lease.go:744` | no |
| in_progress | `Done`, `Complete`, `CompleteBound` | `block` | blocked | missing/non-pass verdict after each entry path's report/file/binding checks | live holder for `Done` at `lease.go:689`; none in store for commit paths | move sets the group at `lease.go:818-819` | no |
| in_progress | `RefuseCommit`, `RefuseCommitPaths` | `block` | blocked | exact-path, report, route, and reason checks `lease.go:789-816` | none in store; scheduler/harness callers | move sets the group at `lease.go:818-819` | no |
| in_progress | `Block`, `BlockWithPaths` | `block` | blocked | state branch `lease.go:861-863` | live holder `lease.go:863` | move sets the group at `lease.go:818-819` | no |
| in_progress | `Fail` | `Fail` | pending | live lease `lease.go:827` | live holder `lease.go:827` | none | no |
| in_progress | `Release` | `Release` | pending | state and lease `lease.go:896` | none | none | no |
| in_progress | `ExpireDue` | `ExpireDue` | pending | due lease and no report `lease.go:1058` | scheduler method | none | no |
| in_progress | `Cancel` | `Cancel` | cancelled | non-terminal guard `lease.go:960` | none | none | no |
| blocked | `ResetBy` | `ResetBy` | pending | non-empty caller, route not owner, and recorded-creator match `reset_by.go:20-32` | creator, or asserted handle when no creator is recorded | all five at `reset_by.go:34` | no |
| blocked | `Reset` | `Reset` | pending | blocked `lease.go:912` | scheduler call at `scheduler/roster.go:420` | all five at `lease.go:915` | no |
| blocked | `ResetIfBlocked` | `ResetIfBlocked` | pending | reason `reset_if.go:23`; optional match `reset_if.go:26` | scheduler call at `scheduler/tamper.go:261` | all five at `reset_if.go:29` | no |
| blocked | `Widen`, `WidenGated` | `WidenGated` | pending | validation/conflict checks `scope.go:48-79`; typed request `scope.go:54-55,93` | proven origin `schedproc/decide.go:281,292`; not self `scope.go:59` | all five at `scope.go:97-99` | no |
| blocked | `Widen`, `WidenGated` | `WidenGated` | in_progress | validation/conflict checks `scope.go:48-79`; report branch `scope.go:86` | proven origin `schedproc/decide.go:281,292`; not reporter `scope.go:59` | all five at `scope.go:91` | no |
| blocked | `Escalate` | `block` | blocked | reason non-empty `lease.go:815` after blocked guard `scope.go:154` | proven origin `schedproc/decide.go:281,292` | replaces group at `scope.go:162`; may preserve paths/attempt at `scope.go:165-167` | no |
| blocked | `Cancel` | `Cancel` | cancelled | non-terminal guard `lease.go:960` | none | none; the five fields remain at `lease.go:968` | no |

`ClearsBlockGroup` and the `From` states above are checked against the code by
reading in S1-1. S2 adds driving tests for those facts.

### Store methods tied to moves

`go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1 -v`
logged 23 sorted method-to-writer lines:

```text
Add -> AddWithFileWaits
AddWithFileWaits -> AddWithFileWaits
Block -> block
BlockWithPaths -> block
Cancel -> Cancel
Claim -> grant
ClaimServing -> grant
Complete -> block, complete
CompleteBound -> block, complete
Done -> block, complete
Escalate -> block
ExpireDue -> ExpireDue
Fail -> Fail
Grant -> grant
GrantClean -> grant
RefuseCommit -> block
RefuseCommitPaths -> block
Release -> Release
Reset -> Reset
ResetBy -> ResetBy
ResetIfBlocked -> ResetIfBlocked
Widen -> WidenGated
WidenGated -> WidenGated
```

### Needs, row by row

The rule excludes the row's `From`-state test and the actor test represented by
`Who`. Row 2 is emptied because its four entry paths' complete condition does
not fit one sentence of at most 300 characters.

| Row | Functions and ranges read | Tests on the path | `Needs` in the data |
|---:|---|---|---|
| 2 | `grant` `lease.go:64-102`; `Grant` `:236-243`; `GrantClean` `:261-270`; `Claim` `:304-363`; `ClaimServing` `:369-434` | dispatchable class (`:239,266,317,342,386,412`); clean-dispatch seat (`:263`); id/candidate/pool/live-lease branches (`:309-355,374-426`); blocker, held-seat and leasable checks (`:72-80`) | emptied: the whole four-entry condition exceeds 300 characters |
| 5 | `Done` `lease.go:687-704`; `complete` `:716-745` | no prior report (`:692`); no declared files (`:700`); seal accepted or verdict dropped (`:719-728`); if required or present, verdict exists and passes (`:729-739`) | `Done has no prior report and no declared files; when a verdict is required or a sealed verdict is present, that verdict passes` |
| 6 | `Complete` `lease.go:768-775`; `CompleteBound` `scope.go:131-146`; `complete` `lease.go:716-745` | report exists (`lease.go:770`, `scope.go:133`); CompleteBound number nonnegative and positive binding valid/free (`scope.go:136-142`); seal/verdict tests (`lease.go:719-739`) | `a report exists; CompleteBound also has a nonnegative PR number and, when positive, a valid free binding; when a verdict is required or a sealed verdict is present, that verdict passes` |
| 7 | same functions as rows 5-6 | each entry's report/file/binding tests above; verdict required with none (`lease.go:729-734`) or sealed verdict present and non-pass (`:735-738`) | `Done has no prior report and no declared files; Complete and CompleteBound have a report, and CompleteBound has a valid free binding; a verdict is required but absent, or a sealed verdict is present and does not pass` |
| 8 | `RefuseCommit` `lease.go:781-783`; `RefuseCommitPaths` `:788-806`; `block` `:808-820` | exact paths (`:789-792`); report exists (`:794`); route empty/lead/owner (`:809-813`); reason non-empty (`:815-816`) | `paths are exact, a report exists, the route is empty, lead, or owner, and the reason code is non-empty` |
| 12 | `ExpireDue` `lease.go:1053-1076` | lease exists, no report, and expiry due (`:1058`) | `no report; the lease is due` |
| 14 | `ResetBy` `reset_by.go:19-40` | caller non-empty (`:20-23`); route not owner (`:28-29`); caller matches a recorded creator, while an empty creator accepts any non-empty caller (`:31-32`) | `the caller is non-empty, the route is not owner, and the caller matches the creator when recorded; when no creator is recorded, any non-empty caller is accepted` |
| 16 | `ResetIfBlocked` `reset_if.go:21-31` | reason matches (`:23-24`); optional match absent or true (`:26-27`) | `the block reason matches, and the optional match function is absent or accepts the task` |
| 17 | `Widen`, `WidenGated` `scope.go:41-103` | added paths and union valid/non-empty, reason/actor non-empty (`:48-64`); actor not holder/reporter (`:59`); no active conflict (`:69-78`); no report, refused paths, typed reason (`:54-55,93`) | `added paths and their union are valid and non-empty, reason and actor are non-empty, actor is not holder or reporter, no active file conflicts, and the task has no report, refused paths, and a widen-request reason` |
| 18 | `Widen`, `WidenGated` `scope.go:41-103` | common validation/self/conflict tests (`:48-78`); report exists (`:86`) | `added paths and their union are valid and non-empty, reason and actor are non-empty, actor is not holder or reporter, no active file conflicts, and a report exists` |
| 19 | `Escalate` `scope.go:152-170`; `block` `lease.go:808-820` | reason code non-empty (`lease.go:815-816`); owner route is fixed and valid | `the reason code is non-empty` |

## Outcome sets

| Name | Reader | Outcomes | First cited line and its text |
|---|---|---|---|
| `retry_budget` | `internal/scheduler/hooks.go:110`, `DefaultRouter.Route` | failed, expired | `if a.Outcome == task.OutcomeFailed || a.Outcome == task.OutcomeExpired {` |
| `router_retry_effect` | `internal/routing/refusal.go:77-92`, `effect` | failed, expired; commit_refused and blocked conditionally | `var attemptOutcomeEffect = map[string]retryEffect{` |
| `weekly_report_failed` | `internal/routing/router.go:463-469`, `failedOutcome` | failed, expired, commit_refused, blocked | `func failedOutcome(outcome string) bool {` |
| `verifier_fail` | `internal/routing/router.go:1079-1100`, `verifierResult` | failed and commit_refused when the verifier is mechanical and the attempt has no verdict; blocked under the same conditions except detail `verdict_missing` | `func verifierResult(verifier string, a task.Attempt) string {` |
| `exit_scan` | `internal/scheduler/exits.go:477-478`, `scanAttemptsLog` | failed, expired, unclaimed | `switch a.Outcome {` |
| `seat_lapsed` | `internal/scheduler/scheduler.go:1105`, `idleSeatExcept` | unclaimed, expired | `if (a.Outcome == task.OutcomeUnclaimed || a.Outcome == task.OutcomeExpired) && (joinedAt.IsZero() || !a.GrantedAt.Before(joinedAt)) {` |

For `router_retry_effect`, `failed` and `expired` are unconditional.
`commit_refused` counts when its detail maps to true in `RefusalWorkFailure`.
`blocked` follows that map when classified and defaults to a work failure when
unclassified (`internal/routing/refusal.go:86-92`). The last fallback differs
from the draft brief's shorter conditional description; the declaration
follows the code.

For `verifier_fail`, a verdict on the attempt decides the result alone,
whatever the outcome (`internal/routing/router.go:1083-1091`). The three
outcomes are fallback cases reached for a mechanical verifier with no
attempt verdict; blocked with detail `verdict_missing` returns `not_run`
before the fallback (`:1092-1097`).

## Seen, not fixed

- `Release` (`internal/task/lease.go:893-903`), `Cancel` (`lease.go:957-969`), and pending `Block` (`lease.go:868-875`) do not authenticate the supplied actor in the store.
- `Cancel` leaves all five block-group fields set when cancelling a blocked task (`internal/task/lease.go:968`).
- `Store.Claim`, `Store.Widen`, and `Store.RefuseCommit` have zero non-test callers. Command: `for n in Claim Widen RefuseCommit; do rg -n "\\.${n}\\(" --glob '*.go' --glob '!**/*_test.go' . | wc -l; done`; counts: `0`, `0`, `0`.
- A task read from disk is unmarshaled at `internal/task/task.go:542`, then `RefusedPaths` is validated at lines 545-547. Command `rg -n 'ValidState|validState|validateState' internal/task --glob '*.go' --glob '!**/*_test.go'` returned 0 state-validator names.
- An unclassified blocked attempt detail defaults to a retry work failure, while an unclassified commit-refused detail does not (`internal/routing/refusal.go:86-92`).
- A task with no recorded creator is reset by any non-empty caller (`internal/task/reset_by.go:20-32`), by the compatibility design in the comment at lines 13-18.

## Checked by tests

- The AST assignment guard finds 13 non-test state-write sites, accepts
  the five named state constants, and compares the found `(writer, to)` keys
  both ways with the declaration; it also rejects taking the address of a
  state field.
- The syntax-derived method-set test checks reflected exported `Store`
  methods against all writers reachable by direct receiver/package calls and
  against the writers declared by each method's `Via` rows.
- The state, outcome, and route lists match the corresponding `class.go`
  constants in source order.
- Moves name declared, reachable states, leave no terminal state, name real
  exported `Store` methods, use the six actor labels, and give each
  non-terminal state an outgoing move.
- The known blocked-to-cancelled disagreement must still match a declared
  move; this catches a removed move, but not code fixed while the declaration
  still says `ClearsBlockGroup: false`.
- Outcome-set names are distinct, their members are declared outcomes, no set
  is empty, and one outcome cannot have two conditional entries in one set.
- The generated JSON matches `JSON()` byte for byte and parses back to five
  states and 13 distinct `(writer, to)` pairs.

## Checked by reading

- The 20 `From` states, actor checks, and block-group effects are readings of
  the source; the `Needs` of rows with a non-empty one were re-read under the
  whole-condition rule.
- The six outcome sets' membership is a reading in S1-1; S2's driving tests tie each to its reader.
- A call through a function value, a method value or an interface, and a caller in another package, are not edges; the test resolves calls by name, without type information.
- A write through a whole-struct copy is seen by no static guard here; S2's driving tests cover it.
- The generated artifact is not read by a command in S1-1 and is outside the
  shipped `pi/extensions` payload path.

## S1-1b reverts

The nine temporary edits ran after the implementation and ordinary
verification. R2, R5, N2, and N4 restored their named existing product file;
`git diff --stat -- internal/task/lease.go internal/task/class.go` printed
nothing after the last restore.

| Revert | Command | Exit | First failing line |
|---|---|---:|---|
| R1: delete the `Release` move | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:100: lease.go:903: state-write key Release->pending is not declared` |
| R2: add `t.State = StateBlocked` inside `Release` | same | 1 | `contract_test.go:100: lease.go:904: state-write key Release->blocked is not declared` |
| R3: delete `verifier_fail`'s blocked conditional | `go test ./internal/statecontract/ -run TestContractFileIsCurrent -count=1` | 1 | `contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>` |
| R4: delete the `knownDisagreements` entry | `go test ./internal/task/ -run TestLeavingBlockedClearsTheBlockGroup -count=1` | 1 | `contract_test.go:432: blocked move via Cancel to cancelled does not clear the block group` |
| R5: add `OutcomeProbe = "probe"` | `go test ./internal/task/ -run TestContractListsMatchTheConstants -count=1` | 1 | `contract_test.go:310: Outcome constants [OutcomeCompleted OutcomeUnclaimed OutcomeExpired OutcomeFailed OutcomeReleased OutcomeBlocked OutcomeCancelled OutcomeCommitRefused OutcomeProbe] have values [completed unclaimed expired failed released blocked cancelled commit_refused probe]; contract has [completed unclaimed expired failed released blocked cancelled commit_refused]` |
| N1: remove `Claim` from row 2 | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:219: Store.Claim reaches writers [grant]; Moves declares []` |
| N2: add `Store.ProbeRelease` forwarding to `Release` | same | 1 | `contract_test.go:219: Store.ProbeRelease reaches writers [Release]; Moves declares []` |
| N3: add `Get` to row 1 | same | 1 | `contract_test.go:219: Store.Get reaches writers []; Moves declares [AddWithFileWaits]` |
| N4: take `&t.State` inside `Release` | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:62: lease.go:904: the address of a state field is taken; the write that follows is not declared` |

## S1-1b verification commands

| Command | Exit | Result |
|---|---:|---|
| `go build ./...` | 0 | no output |
| `go vet ./internal/task/ ./internal/statecontract/` | 0 | no output |
| `gofmt -l internal/task internal/statecontract` | 0 | no output |
| `go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract -count=1` | 0 | pass, 0.346s |
| `go test ./internal/statecontract/ -count=1` | 0 | pass, 0.198s |
| `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1 -v` | 0 | pass, 23 sorted method lines, 0.345s |
| `go test ./internal/task/ -count=1` | 0 | pass, 24.268s |
| `git diff --stat` | 0 | exactly the four declared files |
| `git status --porcelain` | 0 | the same four paths, modified and none new |
| `grep -c '/''Users/' docs/v3-spike/330-s1-1-task-contract/README.md` | 1 | printed `0` (grep's no-match status) |
| `git diff --check` | 0 | no output |
| `go run ./scripts/redact-evidence -check docs/v3-spike/330-s1-1-task-contract/` | 0 | no output |

## Corrections (2026-11-11, S1-1b)

line numbers as of bb4334e2a8db8703b8b5fd270d7c218c6f9e1bfa

- F1: S1-1 omitted `Claim`, `RefuseCommit`, and `Widen` from `Via`; they now follow their forwarding calls to `grant`, `RefuseCommitPaths`, and `WidenGated` at `internal/task/lease.go:304-353,781-782` and `internal/task/scope.go:41-42`.
- F3: S1-1 said “a required verdict”; rows 5-7 now say that the gate runs when a verdict is required or a sealed verdict is present, and any present sealed verdict must pass (`internal/task/lease.go:719-739`).
- F4: S1-1 named `creator` alone; row 14 now names `creator` and `asserted_handle`, and records that an empty creator accepts any non-empty caller (`internal/task/reset_by.go:20-32`).
- F5: S1-1 listed `verifier_fail` without scope; its three conditionals now record the mechanical/no-verdict fallback and blocked's `verdict_missing` exclusion (`internal/routing/router.go:1079-1100`).
- F7: S1-1's revert lines `81,81,236,114` predated later test edits and are replaced by the S1-1b runs above; its claimed proof at `cli/task_widen.go:149-152` is corrected to `internal/schedproc/decide.go:281,292`; row 2 now distinguishes scheduler, lead-dispatch, and seat claim callers; the stale-exception claim is narrowed; and the reading limits now name whole-struct copies plus function/method/interface values and cross-package callers.
