# 3.2.0 merge-train gates: only questions the owner can act on

Task t112 implements items 5 and 6 of the 3.2.0 plan. The source audit was
against origin/v3 at `93cb8782`, after #917. Tests use the in-process fake
GitHub service and temporary task/gate stores. They make no network request
and write under no real session root.

## Contract

Three outcomes are not growth decisions:

- `strict_required_status_checks_policy`: GitHub requires an up-to-date
  branch and the train cannot make one.
- `update_conflict`: the branch conflicts with its base.
- `pr_state` for a PR already merged at another head.

They are recorded as refusals with the next action in plain words. They do
not consult an accepted legacy grow-merge gate and do not raise a new gate.
Every other refusal keeps the prior gate path.

A merged-elsewhere refusal is terminal only for the same PR, task and bound
head. Its journal record carries the server head, merge commit and bound
head. The next run reconstructs the plan row from that record without a
GitHub read or another reconcile entry. Strict and conflict refusals remain
non-terminal because a later base can change their result.

Every start-of-run PR recovery uses one reader. A recorded merged-elsewhere
refusal is checked before any rebind, update or merge-reconcile read. An
unreadable PR writes `reconcile_read_failed` and skips only that PR unless an
exact `gh` or `GraphQL` wrapper proves HTTP 401, 403 or 404. A 401 or 403 ends
the run with authentication and access guidance; a 404 keeps the prior fatal
read behavior. Every other 4xx, 5xx, transport error, deadline, killed `gh`,
bare EOF and unknown wording skips only that PR.

The lead's screen is capped at 200 characters, so its strict, conflict and
merged-elsewhere stop lines use bounded instructions. The complete round-one
explanation remains in the refusal detail and merge-train plan text. A
merged-elsewhere screen line reads the task identifier from the recorded
refusal instead of falling back to the generic `pr_state` instruction.

## Regression rows

The focused command is:

```sh
GOCACHE=/tmp/amq-squad-reviewer-t115-gocache go test ./internal/mergetrain -run 'Test(StrictRefusalHasWordsAndNoGate|AcceptedLegacyGateDoesNotLiftStrictRefusal|BranchUpdateConflictIsTypedAndUngated|MergedElsewhereIsTerminalForTheBoundHead|StrictRefusalIsRecheckedOnTheNextRun|RecoveryReadErrorSkipsOnlyThatPR|DefiniteRecoveryReadFailureStopsTheRun|UpdateRecoveryReadFailureSkipsOnlyThatPR|RebindRecoveryReadFailureSkipsOnlyThatPR|MergedElsewhereTerminalSkipPrecedesEveryRecoveryRead|EveryRecoveryPRReadUsesSharedReader|ActionableRefusalStillRaisesGrowthGate|StrictPlanOwnerTextIsWordsFirst|UnliftableRefusalsPrintTheActionableWordsWithoutAGate)$' -count=1
```

All Go revert runs used `GOCACHE=/tmp/amq-squad-reviewer-gocache go test
./internal/mergetrain -run '^TEST$' -count=1`. The words revert used
`node --test --test-reporter=spec pi/test/words.test.ts`.

| Row | `TEST` | One-line revert run | First failing line |
| --- | --- | --- | --- |
| G1 | `TestStrictRefusalHasWordsAndNoGate` | Set `noGate := false` before gate lookup/raise | `unactionable refusal exposed a gate` |
| G1b | `TestAcceptedLegacyGateDoesNotLiftStrictRefusal` | The same `noGate := false` mutation consults and honors the legacy approval | `binding_failed: merged head ... does not match task t1's bound head ...` |
| G2 | `TestBranchUpdateConflictIsTypedAndUngated` | Return `RuleUpdateUnconfirmed` for the unmoved conflicting head | `update conflict: { ... Rule:branch_update_unconfirmed ... }` |
| G3 | `TestMergedElsewhereIsTerminalForTheBoundHead` | Make the `priorMergedElsewhere` replay branch unreachable | `terminal refusal was re-read or re-audited: reads 1 -> 2 reconcile 1 -> 2` |
| G3b | `TestStrictRefusalIsRecheckedOnTheNextRun` | Broaden the terminal audit predicate to every refusal for the task | `strict refusal was treated as terminal: reads 3 -> 3` |
| G4 | `TestRecoveryReadErrorSkipsOnlyThatPR` | Return every recovery read error before the skip path | `5xx_wrapper: read PR #1 while reconciling it: ... Bad Gateway (HTTP 502)` |
| G4a | `TestRecoveryReadErrorSkipsOnlyThatPR/unwrapped_403` | Classify `(HTTP 403)` from the wrapped error string instead of the exact `gh` stderr wrapper | `GitHub refused to read PR #1 with HTTP 403 ... gh pr view: gh: Forbidden (HTTP 403)` |
| G4b | `TestDefiniteRecoveryReadFailureStopsTheRun` | Send exact 401 and 403 wrappers through the skip path | `401_gh_wrapper: definite client error did not stop in words: <nil>` |
| G5 | `TestActionableRefusalStillRaisesGrowthGate` | Set `noGate := true` for every refusal | `actionable refusal lost its gate` |
| G6 | `pi/test/words.test.ts`, row 7 | Remove `update_conflict` from `trainRuleWords` | `train rule update_conflict has no words` |

Each mutation above was applied, the command was run to the named failure,
and the mutation was restored before the green validation run.

## Round-two review fixes for #919

The carried head is `5489e28f145130c9427b5934af948a6039913da0`.
Round two adds the following rows without weakening the round-one contract:

| Row | Test | Mutation run | First failing line |
| --- | --- | --- | --- |
| R2-1 forms | `TestRecoveryReadErrorSkipsOnlyThatPR` | Insert `return ... fmt.Errorf("read PR ...")` before `recoveryPRReader.read` classifies the error; run `GOCACHE=/tmp/amq-squad-reviewer-t115-gocache go test ./internal/mergetrain -run '^TestRecoveryReadErrorSkipsOnlyThatPR$' -count=1` | `5xx_wrapper: read PR #1 while reconciling it: ... Bad Gateway (HTTP 502)` |
| R2-1 wrapper boundary | `TestRecoveryReadErrorSkipsOnlyThatPR/unwrapped_403` | Accept `(HTTP 403)` from `err.Error()` before unwrapping `GHError`; run the named subtest | `GitHub refused to read PR #1 with HTTP 403 ... gh pr view: gh: Forbidden (HTTP 403)` |
| R2-2 update | `TestUpdateRecoveryReadFailureSkipsOnlyThatPR` | Replace `reads.read(..., "recovering its update")` with direct `t.Forge.PR`; run the named test | `gh api graphql: exit status 1: gh: Bad Gateway (HTTP 502)` |
| R2-2 rebind | `TestRebindRecoveryReadFailureSkipsOnlyThatPR` | Replace `reads.read(..., "recovering its re-bind")` with direct `t.Forge.PR`; run the named test | `gh api graphql: exit status 1: gh: Bad Gateway (HTTP 502)` |
| R2-2 terminal | `TestMergedElsewhereTerminalSkipPrecedesEveryRecoveryRead` | Check only `r.skipped` at the start of `recoveryPRReader.read`, bypassing `terminalOrSkipped`; run the named test | `terminal refusal was read by recovery: ... reads 1 -> 3` |
| R2-2 source guard | `TestEveryRecoveryPRReadUsesSharedReader` | The update or rebind direct-read mutation above; run the named test | `reconcileUpdates in update.go reads Forge.PR directly; use recoveryPRReader.read` |
| R2-3 strict screen | `pi/test/words.test.ts`, row 4 | Make the strict compact branch unreachable; run `node --test --test-reporter=spec pi/test/words.test.ts` | actual began `the merge train stopped at PR #9`, expected `#9: GitHub's up-to-date rule refused it` |
| R2-3 conflict screen | `pi/test/words.test.ts`, row 4 | Make the conflict compact branch unreachable; run the words test | actual began `the merge train stopped at PR #9`, expected `#9 conflicts with the base` |
| R2-3 merged screen | `pi/test/words.test.ts`, row 4 | Make the `pr_state` record branch unreachable; run the words test | actual ended `check the PR`, expected `nothing to land ... task t115's record` |

Green validation after every mutation was restored:

```sh
GOCACHE=/tmp/amq-squad-reviewer-t115-gocache go test ./internal/mergetrain -count=1
node --test --test-reporter=spec pi/test/words.test.ts
```

### Round-two gate record

The round-two working tree is based on `8be23f5e996345fdd289c1e09824d2fe46e3dc01`
and carries #919 head `5489e28f145130c9427b5934af948a6039913da0`.
The code-only diff (`internal/` and `pi/`) at validation time has SHA-256
`842afa9458b09aa6c1f3610099749c9730ce9980fc8aeb9dc5644c595ba9954b`.
The harness assigns the final commit SHA after the seat reports done; this
note does not claim that not-yet-created SHA.

| Gate | Exit | Result |
| --- | ---: | --- |
| `go test ./internal/mergetrain -run 'Test(RecoveryReadErrorSkipsOnlyThatPR\|DefiniteRecoveryReadFailureStopsTheRun\|UpdateRecoveryReadFailureSkipsOnlyThatPR\|RebindRecoveryReadFailureSkipsOnlyThatPR\|MergedElsewhereTerminalSkipPrecedesEveryRecoveryRead\|EveryRecoveryPRReadUsesSharedReader)$' -count=1` | 0 | `ok`, 14.324 s |
| `go test ./internal/mergetrain -count=1` | 0 | `ok`, 112.370 s |
| isolated `go test ./internal/cli -count=1` | 0 | `ok`, 56.125 s; live seat context variables were removed only from the test process |
| `node --test --test-reporter=spec pi/test/*.test.ts` | 0 | 116 passed, 0 failed, 1 staged-runtime skip |
| `go build ./...` | 0 | passed; the sandbox denied a non-fatal module stat-cache temp write |
| `go vet ./...` | 0 | passed |
| `git diff --check HEAD` | 0 | clean |
