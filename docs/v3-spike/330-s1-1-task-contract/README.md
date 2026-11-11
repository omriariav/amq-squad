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
writer. The proven-lead rows run under the scheduler decision socket's lead
pane proof at `internal/cli/task_widen.go:149-152`; `Escalate` and `WidenGated`
are called within that verified apply path at lines 361 and 379.

| From | Via | Writer | To | From guard / needs | Who check | Block-group clearing | Differs from this brief |
|---|---|---|---|---|---|---|---|
| new | `Add`, `AddWithFileWaits` | `AddWithFileWaits` | pending | creation transaction `task.go:263-276` | none; asserted `CreatedBy` stored at `task.go:277` | not applicable | no |
| pending | `Grant`, `GrantClean`, `ClaimServing` | `grant` | in_progress | pending `lease.go:69`; blocker, held-seat, leasable checks `lease.go:72-80` | asserted holder normalized at `lease.go:65`; scheduler is the caller | none needed | no |
| pending | `Block`, `BlockWithPaths` | `block` | blocked | pending branch `lease.go:868-871` | none | move sets the group at `lease.go:818-819` | no |
| pending | `Cancel` | `Cancel` | cancelled | non-terminal guard `lease.go:960` | none | none | no |
| in_progress | `Done` | `complete` | completed | live lease `lease.go:689`; no declared files `lease.go:700`; verdict gate `lease.go:729-739` | live holder `lease.go:689` | all five at `lease.go:744` | no |
| in_progress | `Complete`, `CompleteBound` | `complete` | completed | report guards `lease.go:770` and `scope.go:133`; verdict gate `lease.go:729-739` | none in store; scheduler/harness callers | all five at `lease.go:744` | no |
| in_progress | `Done`, `Complete`, `CompleteBound` | `block` | blocked | missing/non-pass verdict `lease.go:729-737` | live holder for `Done` at `lease.go:689`; none in store for commit paths | move sets the group at `lease.go:818-819` | no |
| in_progress | `RefuseCommitPaths` | `block` | blocked | report guard `lease.go:794` | none in store; scheduler/harness callers | move sets the group at `lease.go:818-819` | no |
| in_progress | `Block`, `BlockWithPaths` | `block` | blocked | state branch `lease.go:861-863` | live holder `lease.go:863` | move sets the group at `lease.go:818-819` | no |
| in_progress | `Fail` | `Fail` | pending | live lease `lease.go:827` | live holder `lease.go:827` | none | no |
| in_progress | `Release` | `Release` | pending | state and lease `lease.go:896` | none | none | no |
| in_progress | `ExpireDue` | `ExpireDue` | pending | due lease and no report `lease.go:1058` | scheduler method | none | no |
| in_progress | `Cancel` | `Cancel` | cancelled | non-terminal guard `lease.go:960` | none | none | no |
| blocked | `ResetBy` | `ResetBy` | pending | blocked `reset_by.go:25`; route not owner `reset_by.go:28` | creator `reset_by.go:31` | all five at `reset_by.go:34` | no |
| blocked | `Reset` | `Reset` | pending | blocked `lease.go:912` | scheduler call at `scheduler/roster.go:420` | all five at `lease.go:915` | no |
| blocked | `ResetIfBlocked` | `ResetIfBlocked` | pending | reason `reset_if.go:23`; match `reset_if.go:26` | scheduler call at `scheduler/tamper.go:261` | all five at `reset_if.go:29` | no |
| blocked | `WidenGated` | `WidenGated` | pending | typed request `scope.go:54-55,93`; no report and refused paths | proven lead `cli/task_widen.go:149-152,379`; not self `scope.go:59` | all five at `scope.go:97-99` | no |
| blocked | `WidenGated` | `WidenGated` | in_progress | report branch `scope.go:86` | proven lead `cli/task_widen.go:149-152,379`; not reporter `scope.go:59` | all five at `scope.go:91` | no |
| blocked | `Escalate` | `block` | blocked | blocked `scope.go:154` | proven lead `cli/task_widen.go:149-152,361` | replaces group at `scope.go:162`; may preserve paths/attempt at `scope.go:165-167` | no |
| blocked | `Cancel` | `Cancel` | cancelled | non-terminal guard `lease.go:960` | none | none; the five fields remain at `lease.go:968` | no |

`ClearsBlockGroup` and the `From` states above are checked against the code by
reading in S1-1. S2 adds driving tests for those facts.

## Outcome sets

| Name | Reader | Outcomes | First cited line and its text |
|---|---|---|---|
| `retry_budget` | `internal/scheduler/hooks.go:110`, `DefaultRouter.Route` | failed, expired | `if a.Outcome == task.OutcomeFailed || a.Outcome == task.OutcomeExpired {` |
| `router_retry_effect` | `internal/routing/refusal.go:77-92`, `effect` | failed, expired; commit_refused and blocked conditionally | `var attemptOutcomeEffect = map[string]retryEffect{` |
| `weekly_report_failed` | `internal/routing/router.go:463-469`, `failedOutcome` | failed, expired, commit_refused, blocked | `func failedOutcome(outcome string) bool {` |
| `verifier_fail` | `internal/routing/router.go:1095-1097`, `verifierResult` | failed, commit_refused, blocked | `switch a.Outcome {` |
| `exit_scan` | `internal/scheduler/exits.go:477-478`, `scanAttemptsLog` | failed, expired, unclaimed | `switch a.Outcome {` |
| `seat_lapsed` | `internal/scheduler/scheduler.go:1105`, `idleSeatExcept` | unclaimed, expired | `if (a.Outcome == task.OutcomeUnclaimed || a.Outcome == task.OutcomeExpired) && (joinedAt.IsZero() || !a.GrantedAt.Before(joinedAt)) {` |

For `router_retry_effect`, `failed` and `expired` are unconditional.
`commit_refused` counts when its detail maps to true in `RefusalWorkFailure`.
`blocked` follows that map when classified and defaults to a work failure when
unclassified (`internal/routing/refusal.go:86-92`). The last fallback differs
from the draft brief's shorter conditional description; the declaration
follows the code.

## Seen, not fixed

- `Release` (`internal/task/lease.go:893-903`), `Cancel` (`lease.go:957-969`), and pending `Block` (`lease.go:868-875`) do not authenticate the supplied actor in the store.
- `Cancel` leaves all five block-group fields set when cancelling a blocked task (`internal/task/lease.go:968`).
- `Store.Claim`, `Store.Widen`, and `Store.RefuseCommit` have zero non-test callers. Command: `for n in Claim Widen RefuseCommit; do rg -n "\\.${n}\\(" --glob '*.go' --glob '!**/*_test.go' . | wc -l; done`; counts: `0`, `0`, `0`.
- A task read from disk is unmarshaled at `internal/task/task.go:542` and only `RefusedPaths` is validated at lines 545-547. Command `rg -n 'ValidState|validState|validateState' internal/task --glob '*.go' --glob '!**/*_test.go'` returned 0 state-validator names.
- An unclassified blocked attempt detail defaults to a retry work failure, while an unclassified commit-refused detail does not (`internal/routing/refusal.go:86-92`).

## Checked by tests

- The AST assignment guard finds 13 non-test state-write sites, accepts only
  the five named state constants, and compares the found `(writer, to)` keys
  both ways with the declaration.
- The state, outcome, and route lists match the corresponding `class.go`
  constants in source order.
- Moves name declared, reachable states, leave no terminal state, name real
  exported `Store` methods, use the six actor labels, and give each
  non-terminal state an outgoing move.
- The known blocked-to-cancelled disagreement is explicit and cannot become a
  stale exception.
- Outcome-set names are distinct, their members are declared outcomes, and no
  set is empty.
- The generated JSON matches `JSON()` byte for byte and parses back to five
  states and 13 distinct `(writer, to)` pairs.

## Checked by reading

- The 20 `From` states, needs, actor checks, and block-group effects in the
  moves table are readings of the source on the named base.
- The six outcome sets' membership is a reading in S1-1; S2's driving tests tie each to its reader.
- The generated artifact is not read by a command in S1-1 and is outside the
  shipped `pi/extensions` payload path.

## Reverts

Each temporary edit was restored before the next run. R2 and R5 used
`git checkout --` on only the named existing file. The final command
`git diff --stat -- internal/task/lease.go internal/task/class.go` printed
nothing.

| Revert | Command | Exit | First failing line |
|---|---|---:|---|
| R1: delete the `Release` move | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:81: lease.go:903: state-write key Release->pending is not declared` |
| R2: add `t.State = StateBlocked` inside `Release` | `go test ./internal/task/ -run TestEveryStateWriteIsDeclared -count=1` | 1 | `contract_test.go:81: lease.go:904: state-write key Release->blocked is not declared` |
| R3: flip `Reset.ClearsBlockGroup` without updating JSON | `go test ./internal/statecontract/ -run TestContractFileIsCurrent -count=1` | 1 | `contract_test.go:28: pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: <nil>` |
| R4: delete `knownDisagreements`' `Cancel` entry | `go test ./internal/task/ -run TestLeavingBlockedClearsTheBlockGroup -count=1` | 1 | `contract_test.go:236: blocked move via Cancel to cancelled does not clear the block group` |
| R5: add `OutcomeProbe = "probe"` | `go test ./internal/task/ -run TestContractListsMatchTheConstants -count=1` | 1 | `contract_test.go:114: Outcome constants [OutcomeCompleted OutcomeUnclaimed OutcomeExpired OutcomeFailed OutcomeReleased OutcomeBlocked OutcomeCancelled OutcomeCommitRefused OutcomeProbe] have values [completed unclaimed expired failed released blocked cancelled commit_refused probe]; contract has [completed unclaimed expired failed released blocked cancelled commit_refused]` |

## Verification commands

| Command | Exit | Result |
|---|---:|---|
| `go build ./...` | 0 | no output |
| `go vet ./internal/task/ ./internal/statecontract/` | 0 | no output |
| `gofmt -l internal/task internal/statecontract` | 0 | no output |
| `go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract -count=1` | 0 | pass, 0.511s |
| `go test ./internal/statecontract/ -count=1` | 0 | pass, 0.248s |
| `go test ./internal/task/ -count=1` | 0 | pass, 103.414s |
| `git diff --stat` | 0 | no tracked files changed |
| `git status --porcelain --untracked-files=all` | 0 | exactly the six declared new paths |
| `grep -c '/''Users/' docs/v3-spike/330-s1-1-task-contract/README.md` | 1 | printed `0` (grep's no-match status) |
| `git diff --no-index --check /dev/null docs/v3-spike/330-s1-1-task-contract/README.md \| grep -v '^+' ; true` | 0 | no output |
| `go run ./scripts/redact-evidence -check docs/v3-spike/330-s1-1-task-contract/` | 0 | no output |
