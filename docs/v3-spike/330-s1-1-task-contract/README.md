# Task state contract

line numbers as of 6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41

```text
$ git diff --stat 0c72f069e388ef461e6c0b341a9156c291a4d4a4 6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41 -- internal/task internal/routing internal/scheduler internal/schedproc internal/cli/scheduler_run.go
 internal/task/contract.go      | 124 +++++++++++
 internal/task/contract_test.go | 484 +++++++++++++++++++++++++++++++++++++++++
 2 files changed, 608 insertions(+)
```

```text
$ git diff --stat 6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41 8b86ded84b4b0adbac623309c331adf8037e48ac -- internal/task internal/routing internal/scheduler internal/schedproc internal/cli/scheduler_run.go
```

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
The five block-group fields cleared at `internal/task/scope.go:98` are
`BlockReason`, `BlockRoute`, `BlockDetail`, `RefusedPaths`, and `BlockAttempt`.

| From | Via | Writer | To | From guard / needs | Who check | Block-group clearing | Differs from this brief |
|---|---|---|---|---|---|---|---|
| new | `Add`, `AddWithFileWaits` | `AddWithFileWaits` | pending | creation transaction `task.go:263-276` | none; asserted `CreatedBy` stored at `task.go:277` | not applicable | no |
| pending | `Grant`, `GrantClean`, `Claim`, `ClaimServing` | `grant` | in_progress | see “Needs, row by row” | `Grant` is scheduler-facing; `GrantClean` is lead dispatch; `Claim` and `ClaimServing` accept the seat's asserted holder | none needed | no |
| pending | `Block`, `BlockWithPaths` | `block` | blocked | pending branch `lease.go:868-871` | none | move sets the group at `lease.go:818-819` | no |
| pending | `Cancel` | `Cancel` | cancelled | non-terminal guard `lease.go:960` | none | none | no |
| in_progress | `Done` | `complete` | completed | no prior report and no declared files; verdict rule `lease.go:719-739` | live holder `lease.go:689` | all five at `lease.go:744` | no |
| in_progress | `Complete`, `CompleteBound` | `complete` | completed | report and binding checks; verdict rule `lease.go:719-739` | none in store; scheduler/harness callers | all five at `lease.go:744` | no |
| in_progress | `Done`, `Complete`, `CompleteBound` | `block` | blocked | `Done` has no prior report and no declared files; `Complete` and `CompleteBound` have a report; `CompleteBound` has a nonnegative PR number and, when positive, a valid free binding; verdict missing/non-pass branch | live holder for `Done` at `lease.go:689`; none in store for commit paths | move sets the group at `lease.go:818-819` | no |
| in_progress | `RefuseCommit`, `RefuseCommitPaths` | `block` | blocked | exact-path, report, route, and reason checks `lease.go:789-816` | none in store; scheduler/harness callers | move sets the group at `lease.go:818-819` | no |
| in_progress | `Block`, `BlockWithPaths` | `block` | blocked | state branch `lease.go:861-863` | live holder `lease.go:863` | move sets the group at `lease.go:818-819` | no |
| in_progress | `Fail` | `Fail` | pending | live lease `lease.go:827` | live holder `lease.go:827` | none | no |
| in_progress | `Release` | `Release` | pending | state and lease `lease.go:896` | none | none | no |
| in_progress | `ExpireDue` | `ExpireDue` | pending | due lease and no report `lease.go:1058` | scheduler method | none | no |
| in_progress | `Cancel` | `Cancel` | cancelled | non-terminal guard `lease.go:960` | none | none | no |
| blocked | `ResetBy` | `ResetBy` | pending | non-empty caller, route not owner, and recorded-creator match `reset_by.go:20-32` | creator, or asserted handle when no creator is recorded | all five at `reset_by.go:34` | no |
| blocked | `Reset` | `Reset` | pending | blocked `lease.go:912` | scheduler call at `scheduler/roster.go:420` | all five at `lease.go:915` | no |
| blocked | `ResetIfBlocked` | `ResetIfBlocked` | pending | reason `reset_if.go:23`; optional match `reset_if.go:26` | scheduler call at `scheduler/tamper.go:261` | all five at `reset_if.go:29` | no |
| blocked | `Widen`, `WidenGated` | `WidenGated` | pending | validation/conflict checks `scope.go:48-79`; no report, refused paths present, and block reason `widen_requested` or `growth_gate_required` at `scope.go:54-55,93` | proven origin `schedproc/decide.go:281,292`; not self `scope.go:59` | the five block-group fields at `scope.go:97-99` | no |
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

| Row | Functions and ranges read | Tests on the path | Checks made in called functions | `Needs` in the data |
|---:|---|---|---|---|
| 2 | `grant` `lease.go:64-102`; `Grant` `:236-243`; `GrantClean` `:261-270`; `Claim` `:304-363`; `ClaimServing` `:369-434` | dispatchable class (`:239,266,317,342,386,412`); clean-dispatch seat (`:263`); id/candidate/pool/live-lease branches (`:309-355,374-426`); blocker, held-seat and leasable checks (`:72-80`) | `SeatHandle` `unclean.go:89` at `lease.go:65`; `Blocker` `lease.go:18` at `:72`; `HeldBy` `:51` at `:75`; `seatLeasable` `unclean.go:99` at `lease.go:79` | emptied: the whole four-entry condition exceeds 300 characters |
| 5 | `Done` `lease.go:687-704`; `complete` `:716-745` | no prior report (`:692`); no declared files (`:700`); seal accepted or verdict dropped (`:719-728`); if required or present, verdict exists and passes (`:729-739`) | `verdictInvalid` `verdict.go:243` at `lease.go:722`; `verifierRequired` `lease.go:750` at `:729` | `Done has no prior report and no declared files; when a verdict is required or a sealed verdict is present, that verdict passes` |
| 6 | `Complete` `lease.go:768-775`; `CompleteBound` `scope.go:131-146`; `complete` `lease.go:716-745` | report exists (`lease.go:770`, `scope.go:133`); CompleteBound number nonnegative and positive binding valid/free (`scope.go:136-142`); seal/verdict tests (`lease.go:719-739`) | `bindPR` `bind.go:78` at `scope.go:140`; `verdictInvalid` `verdict.go:243` at `lease.go:722`; `verifierRequired` `lease.go:750` at `:729` | `a report exists; CompleteBound also has a nonnegative PR number and, when positive, a valid free binding; when a verdict is required or a sealed verdict is present, that verdict passes` |
| 7 | same functions as rows 5-6 | `Done` report/file checks; `Complete` report check; `CompleteBound` report, nonnegative number (`scope.go:136`) and positive-number binding (`:139-142`); verdict required with none (`lease.go:729-734`) or sealed verdict present and non-pass (`:735-738`) | the row 5 and row 6 called checks above | `Done has no prior report and no declared files; Complete and CompleteBound have a report; CompleteBound also has a nonnegative PR number and, when positive, a valid free binding; a verdict is required but absent, or a sealed verdict is present and does not pass` |
| 8 | `RefuseCommit` `lease.go:781-783`; `RefuseCommitPaths` `:788-806`; `block` `:808-820` | exact paths (`:789-792`); report exists (`:794`); route empty/lead/owner (`:809-813`); reason non-empty (`:815-816`) | `CleanExactFiles` `files.go:57` at `lease.go:789` | `paths are exact, a report exists, the route is empty, lead, or owner, and the reason code is non-empty` |
| 12 | `ExpireDue` `lease.go:1053-1076` | lease exists, no report, and expiry due (`:1058`) | — | `no report; the lease is due` |
| 14 | `ResetBy` `reset_by.go:19-40` | caller non-empty (`:20-23`); route not owner (`:28-29`); caller matches a recorded creator, while an empty creator accepts any non-empty caller (`:31-32`) | — | `the caller is non-empty, the route is not owner, and the caller matches the creator when recorded; when no creator is recorded, any non-empty caller is accepted` |
| 16 | `ResetIfBlocked` `reset_if.go:21-31` | reason matches (`:23-24`); optional match absent or true (`:26-27`) | — | `the block reason matches, and the optional match function is absent or accepts the task` |
| 17 | `Widen`, `WidenGated` `scope.go:41-103` | added paths and union valid/non-empty, reason/actor non-empty (`:48-64`); actor not holder/reporter (`:59`); no active conflict (`:69-78`); no report, refused paths present, and either typed reason (`:54-55,93`) | `CleanDeclaredFiles` `files.go:17` at `scope.go:48,62`; `FilesOverlap` `files.go:112` at `scope.go:72` | `added paths and their union are valid and non-empty, reason and actor are non-empty, actor is not holder or reporter, no active file conflicts, the task has no report and has refused paths, and the block reason is widen_requested or growth_gate_required` |
| 18 | `Widen`, `WidenGated` `scope.go:41-103` | common validation/self/conflict tests (`:48-78`); report exists (`:86`) | `CleanDeclaredFiles` `files.go:17` at `scope.go:48,62`; `FilesOverlap` `files.go:112` at `scope.go:72` | `added paths and their union are valid and non-empty, reason and actor are non-empty, actor is not holder or reporter, no active file conflicts, and a report exists` |
| 19 | `Escalate` `scope.go:152-170`; `block` `lease.go:808-820` | reason code non-empty (`lease.go:815-816`); owner route is fixed and valid | — | `the reason code is non-empty` |

## Outcome sets

| Name | Reader | Outcomes | First cited line and its text |
|---|---|---|---|
| `retry_budget` | `internal/scheduler/hooks.go:110`, `DefaultRouter.Route` | failed, expired | `if a.Outcome == task.OutcomeFailed || a.Outcome == task.OutcomeExpired {` |
| `router_retry_effect` | `internal/routing/refusal.go:77-92`, `effect` | failed, expired; commit_refused and blocked conditionally | `var attemptOutcomeEffect = map[string]retryEffect{` |
| `weekly_report_failed` | `internal/routing/router.go:463-469`, `failedOutcome` | failed, expired, commit_refused, blocked | `func failedOutcome(outcome string) bool {` |
| `verifier_fail_without_verdict` | `internal/routing/router.go:1079-1100`, `verifierResult` | failed and commit_refused when the verifier is mechanical and the attempt has no verdict; blocked under the same conditions except detail `verdict_missing` | `func verifierResult(verifier string, a task.Attempt) string {` |
| `exit_scan` | `internal/scheduler/exits.go:477-478`, `scanAttemptsLog` | failed, expired, unclaimed | `switch a.Outcome {` |
| `seat_lapsed` | `internal/scheduler/scheduler.go:1105`, `idleSeatExcept` | unclaimed, expired | `if (a.Outcome == task.OutcomeUnclaimed || a.Outcome == task.OutcomeExpired) && (joinedAt.IsZero() || !a.GrantedAt.Before(joinedAt)) {` |

For `router_retry_effect`, `failed` and `expired` are unconditional.
`commit_refused` counts when its detail maps to true in `RefusalWorkFailure`.
`blocked` follows that map when classified and defaults to a work failure when
unclassified (`internal/routing/refusal.go:86-92`). The last fallback differs
from the draft brief's shorter conditional description; the declaration
follows the code.

For `verifier_fail_without_verdict`, a non-mechanical verifier returns
`no_check` before the outcome is read (`internal/routing/router.go:1080-1082`).
For a mechanical verifier, the verdict branches run at
`internal/routing/router.go:1083-1091`; the first `a.Outcome` read is at
`internal/routing/router.go:1092`. A present verdict that is neither pass nor
invalid returns fail before the outcome read. The set is the
fallback that remains: failed and commit_refused with no verdict, plus blocked
with no verdict unless its detail is `verdict_missing` (`:1092-1099`).

## Seen, not fixed

- `Release` (`internal/task/lease.go:893-903`), `Cancel` (`lease.go:957-969`), and pending `Block` (`lease.go:868-875`) do not authenticate the supplied actor in the store.
- `Cancel` leaves all five block-group fields set when cancelling a blocked task (`internal/task/lease.go:968`).
- `Store.Claim`, `Store.Widen`, and `Store.RefuseCommit` have zero non-test callers. Command: `for n in Claim Widen RefuseCommit; do rg -n "\\.${n}\\(" --glob '*.go' --glob '!**/*_test.go' . | wc -l; done`; counts: `0`, `0`, `0`.
- A task read from disk is unmarshaled at `internal/task/task.go:542`, then `RefusedPaths` is validated at lines 545-547. Command `rg -n 'ValidState|validState|validateState' internal/task --glob '*.go' --glob '!**/*_test.go'` returned 0 state-validator names.
- An unclassified blocked attempt detail defaults to a retry work failure, while an unclassified commit-refused detail does not (`internal/routing/refusal.go:86-92`).
- A blocked task with no recorded creator, whose block is not routed to the owner, is reset by any non-empty caller (`internal/task/reset_by.go:20-32`), by the compatibility design in the comment at lines 13-18.

## Checked by tests

- The AST guard scans each declared function body and the rest of each non-test
  file separately (`contract_test.go:59-76`). Function literals nested in a
  declared body are charged to that function; a matching form elsewhere gets
  the outside-declaration error at `contract_test.go:114-120`.

  | Form | Inside declared function body | Outside |
  |---|---|---|
  | address operand | address-specific error (`contract_test.go:123-131`) | outside-declaration error (`contract_test.go:126-128`) |
  | assignment `=`/`:=` + RHS one of five constants | counted (`contract_test.go:133-143`) | outside-declaration error (`contract_test.go:139-140`) |
  | assignment other/no RHS | expression error (`contract_test.go:139-140`) | outside-declaration error (`contract_test.go:139-140`) |
  | assignment other token | expression error (`contract_test.go:139-140`) | outside-declaration error (`contract_test.go:139-140`) |
  | `=` range target | expression error (`contract_test.go:145-155`) | outside-declaration error (`contract_test.go:145-155`) |
  | keyed element `State` | counted (`contract_test.go:158-167`) | outside-declaration error (`contract_test.go:163-165`) |

  `IncDecStmt` is not added because `State` is a string and `t.State++` does
  not compile.
- The type-informed method-set test resolves package functions and methods
  with `go/types`, normalizes instantiated functions with `Origin()` at
  `contract_test.go:253-258`, and derives indexed receiver base names at
  `contract_test.go:378-398`. It rejects a `Store` method used on a value other
  than the enclosing receiver (F1, `contract_test.go:274-279`, T2a-T2c), a
  package function or method use outside a function body (F2,
  `contract_test.go:283-290`, T2e), a same-package function use with no parsed
  declaration (`contract_test.go:259-262`, X4), and a declaration without a
  body (`contract_test.go:216-219`, build probe). A `Store` method declared in
  a non-test file with a pointer receiver fails by name, exported or not
  (`contract_test.go:316-320`, T3). It compares the resulting writer graph with
  each method's `Via` rows.
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
- A use of a package function or method through an interface declared in this package is a failure by name, not a limit; a call through a function-typed value, field or parameter, and a caller in another package, are not edges.
- A write through a whole-struct copy, through reflection, or by decoding into a task is outside this syntactic guard. The limit run below records the whole-struct probe and its exit 0.
- Keyed-literal spelling command `grep -rnE '^[[:space:]]+State[[:space:]]'
  --include='*.go' internal/task | grep -v _test.go` returned one line:
  `internal/task/task.go:59: State string` (spacing omitted here; the command's
  exact output is `internal/task/task.go:59:\tState         string    \`json:"state"\``).
- State-constant shadow command `grep -rnE
  '(StatePending|StateInProgress|StateBlocked|StateCompleted|StateCancelled)[^=!(){]*:='
  --include='*.go' internal/task | grep -v _test.go | wc -l` printed `0`.
- Command `grep -rn 'state-contract' --include='*.go' --include='*.ts'
  --include='*.js' --include='*.mjs' --include='*.json' --include='Makefile'
  --include='*.yml' --include='*.sh' . | grep -v '/node_modules/'` prints
  seven lines: six in `internal/statecontract` and the generated file's own
  schema line. `scripts/build-pi-runtime.sh:92` copies `pi/extensions`;
  it does not copy `pi/test`.

## S1-1c round-two reverts and limit runs

The twenty-two revert results, the whole-struct limit run, and the bodyless
build run use the final round-two test file. Each temporary edit appeared in
`git diff --stat` before its run and was restored afterward. R1, R3, N1, and
N3 restored `contract.go` to blob `da48a86c3534ad6a74c9df3cc2b23e478c4720d8`;
R4 restored an exact saved copy by `cmp` exit 0.

| Revert | Command | Exit | First failing line |
|---|---|---:|---|
| R1: delete the `Release` move | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:91: lease.go:903: state-write key Release->pending is not declared` |
| R2: add `t.State = StateBlocked` inside `Release` | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:91: lease.go:903: state-write key Release->blocked is not declared` |
| R3: delete `verifier_fail_without_verdict`'s blocked conditional without updating JSON | `go test ./internal/statecontract/ -run TestContractFileIsCurrent -count=1` | 1 | `contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>` |
| R4: delete the `knownDisagreements` entry | `go test ./internal/task/ -run TestLeavingBlockedClearsTheBlockGroup -count=1` | 1 | `contract_test.go:573: blocked move via Cancel to cancelled does not clear the block group` |
| R5: add `OutcomeProbe = "probe"` | `go test ./internal/task/ -run TestContractListsMatchTheConstants -count=1` | 1 | `contract_test.go:451: Outcome constants [OutcomeCompleted OutcomeUnclaimed OutcomeExpired OutcomeFailed OutcomeReleased OutcomeBlocked OutcomeCancelled OutcomeCommitRefused OutcomeProbe] have values [completed unclaimed expired failed released blocked cancelled commit_refused probe]; contract has [completed unclaimed expired failed released blocked cancelled commit_refused]` |
| N1: remove `Claim` from row 2 | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:342: Store.Claim reaches writers [grant]; Moves declares []` |
| N2: add receiver-forwarding `Store.ProbeRelease` | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:342: Store.ProbeRelease reaches writers [Release]; Moves declares []` |
| N3: add `Get` to row 1 | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:342: Store.Get reaches writers []; Moves declares [AddWithFileWaits]` |
| N4: take `&t.State` inside `Release` | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:107: lease.go:903: the address of a state field is taken; the write that follows is not declared` |
| T1a: take `&(t.State)` inside `Release` | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:107: lease.go:903: the address of a state field is taken; the write that follows is not declared` |
| T1b: assign `(t.State) = StateBlocked` inside `Release` | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:91: lease.go:903: state-write key Release->blocked is not declared` |
| T1c: assign `t.State` as an `=` range value inside `Release` | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:120: lease.go:903: a state written from an expression is not declared` |
| T2a: package `ProbeParam` calls `s.Release` on a parameter | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:279: lease.go:909: ProbeParam uses Store.Release on a value that is not the enclosing method's receiver` |
| T2b: `Store.ProbeCopy` calls `o.Release` on a receiver copy | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:279: lease.go:910: ProbeCopy uses Store.Release on a value that is not the enclosing method's receiver` |
| T2c: package `ProbeExpr` uses method expression `Store.Release` | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:279: lease.go:909: ProbeExpr uses Store.Release on a value that is not the enclosing method's receiver` |
| T2d: `Store.ProbeValue` takes receiver method value `s.Release` | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:342: Store.ProbeValue reaches writers [Release]; Moves declares []` |
| T2e: package-level `var probeOutside = Store.Release` | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:290: lease.go:908: Release is used outside a function body` |
| T3: add unexported pointer-receiver `(*Store).probePtr` | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:319: Store method probePtr has a pointer receiver` |
| X9: replace the `Release` tuple with `t.State += StatePending` and `t.Report = nil` | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:120: lease.go:903: a state written from an expression is not declared` |
| X15: add package `var probeLit = func(t *Task) { t.State = StateBlocked }` | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:117: lease.go:908: a state written outside a declared function is not declared` |
| X12: add generic `probeBox[T].do` and `Store.ProbeGen` | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:342: Store.ProbeGen reaches writers [block]; Moves declares []` |
| X4: call an in-package interface's `Release` method from `ProbeIface` | `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1` | 1 | `contract_test.go:261: lease.go:913: ProbeIface uses Release, which has no parsed declaration` |

The limit probe added `c := *t`, `c = Task{ID: t.ID}`, and `*t = c`
inside `Release`:

| Run | Command | Exit | Result |
|---|---|---:|---|
| whole-struct limit | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 0 | `ok github.com/omriariav/amq-squad/v3/internal/task 0.360s` |
| bodyless declaration | append `func probeBodyless()` to `internal/task/class.go`; `go build ./internal/task/` | 1 | `internal/task/class.go:102:6: missing function body` |

After the last restore,
`git diff --stat -- internal/task/lease.go internal/task/class.go` exited 0
with no output.

## S1-1c verification commands

| Command | Exit | Result |
|---|---:|---|
| `go build ./...` | 0 | no output |
| `go vet ./internal/task/ ./internal/statecontract/` | 0 | no output |
| `gofmt -l internal/task internal/statecontract` | 0 | no output |
| `go test ./internal/statecontract/ -count=1` | 0 | pass, 0.488s; generated file was checked without an update flag |
| `go test ./internal/task/ -run TestEveryStoreMethodIsTiedToItsMoves -count=1 -v` | 0 | pass, 23 sorted method lines, type check 0.61s |
| `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1 -v` | 0 | pass, 0.00s |
| `go test ./internal/task/ -count=1` | 0 | pass, 24.449s |
| `git diff --stat` | 0 | exactly the four declared files |
| `git status --porcelain` | 0 | the same four paths, modified and none new |
| `grep -c '/''Users/' docs/v3-spike/330-s1-1-task-contract/README.md` | 1 | printed `0` |
| `git diff --check` | 0 | no output |
| `go run ./scripts/redact-evidence -check docs/v3-spike/330-s1-1-task-contract/` | 0 | no output |

## Corrections (2026-11-11, S1-1b)

line numbers as of bb4334e2a8db8703b8b5fd270d7c218c6f9e1bfa

- F1: S1-1 omitted `Claim`, `RefuseCommit`, and `Widen` from `Via`; they now follow their forwarding calls to `grant`, `RefuseCommitPaths`, and `WidenGated` at `internal/task/lease.go:304-353,781-782` and `internal/task/scope.go:41-42`.
- F3: S1-1 said “a required verdict”; rows 5 and 6 now require a passing verdict when one is required or a sealed one is present, while row 7 records the missing/non-pass failing side (`internal/task/lease.go:719-739`).
- F4: S1-1 named `creator` alone; `ResetBy` applies three ordered checks: the caller is non-empty at `internal/task/reset_by.go:20-23`, the route is not owner at `:28-29`, and a recorded creator matches the caller at `:31-32`. Row 14 names `creator` and `asserted_handle` for that bounded compatibility path.
- F5: S1-1 listed `verifier_fail` without scope; `verifier_fail_without_verdict` records the mechanical/no-verdict fallback and blocked's `verdict_missing` exclusion (`internal/routing/router.go:1079-1100`).
- F7a: S1-1 printed revert lines `81,81,236,114`; the merged S1-1 file's corresponding lines were `78,78,233,111`, and the S1-1c runs above supersede both sets.
- F7b: S1-1 cited `cli/task_widen.go:149-152` as the origin proof; the proof is at `internal/schedproc/decide.go:281,292`.
- F7c: S1-1 treated row 2's callers alike; the row now distinguishes scheduler, lead dispatch, and seat claim entry paths at `internal/task/lease.go:236-434`.
- F7d: S1-1's reset compatibility statement omitted the route check; `internal/task/reset_by.go:28-32` refuses owner-routed blocks before applying the creator fallback.
- F7e: S1-1's reading limits described name-based calls; `internal/task/contract_test.go:181-364` resolves typed uses, normalizes instantiated functions with `Origin()`, enforces F1/F2, and charges the T2d receiver method value. The interface/function-value/caller and syntactic whole-struct limits are bounded above.

## Corrections (2026-11-11, S1-1c)

line numbers as of 6a0fe4b1742f5c331d8e3c87d6cf9a6c603a1e41

- G1: S1-1b said `CompleteBound` needed a valid free binding in row 7; it now says the PR number is nonnegative and a positive number needs a valid free binding, matching `internal/task/scope.go:131-146`.
- G2: S1-1b's row 17 ended "no report, refused paths, and a widen-request reason" (contract.go:81 at 6a0fe4b1): it read two ways on the refused paths and named one reason where the code accepts two; the row now requires refused paths to be present and accepts widen_requested or growth_gate_required, matching internal/task/scope.go:54-55,93.
- G3: S1-1b named `verifier_fail` as though its conditions were the reader's whole rule; `verifier_fail_without_verdict` is explicitly the fallback after the mechanical and verdict branches at `internal/routing/router.go:1080-1099`. The misleading wording came from the S1-1b brief.
- G4: S1-1b's guard missed the parenthesized address target tested by T1a, the parenthesized assignment target tested by T1b, and the `=` range target tested by T1c; `internal/task/contract_test.go:49-169` unwraps and checks those forms.
- G5: S1-1b built method edges by syntax and names; `internal/task/contract_test.go:181-364` type-checks uses, normalizes generic instantiations with `Origin()`, enforces F1/F2 and missing-declaration failures, and charges T2d's receiver method value. The checked-by-reading interface and function-value sentence bounds what remains outside the edge graph.
- G6: S1-1b's check ran on exported Store methods (contract_test.go:195-196 at 6a0fe4b1); T-3 does not read the name's case (row T3's method is unexported).
- G7: S1-1b's note used the older heading SHA, incomplete row conditions, the old set name, and unsupported reading-limit claims; the heading, tables, checked sections, S1-1c runs, and these corrections now carry the measured commands and deciding lines.

## Round 2 (2026-11-11)

- This round replaces PR #973 at head `abafeaf440b98f960bcf848bc18e5f9a437fcfad`; that PR was not merged.
- `internal/task/contract.go` and `pi/test/state-contract.json` are the carry bytes: blobs `da48a86c3534ad6a74c9df3cc2b23e478c4720d8` and `5f48716ea00622f255a7e796f589f4f23497ff01`.
- H1 before: `+=` was treated like `=`. After: other assignment tokens fail at `contract_test.go:139-140`; X9 decides it.
- H2 before: the state guard walked declared function bodies. After: file-scope forms are inspected at `contract_test.go:70-76`; X15 decides the outside-function error.
- H3 before: same-package typed uses could be dropped, generic instantiations were not normalized, bodyless declarations were skipped, and indexed receiver names were missed. After: `Origin()` and missing-key handling are at `contract_test.go:253-262`, bodyless indexing is at `:201-225`, and indexed receiver parsing is at `:378-398`; X12 and X4 decide the call-graph cases. The bodyless case has no revert row because its deciding run is the build row.
- H4 before: the checked-by-tests prose overstated the guard and call graph. After: the form matrix and the exact plan-reviewer sentence above separate tested failures from reading limits.
- H5 before: claims about block clearing, outcome ordering, `ResetBy`, and the S1-1b corrections were unbounded. After: the Moves preface, outcome paragraph, F4, and G2/G4/G6 cite their deciding lines and probes.
