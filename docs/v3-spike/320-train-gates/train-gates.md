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

## Round-three review fixes for #922

The working tree starts at `8be23f5e996345fdd289c1e09824d2fe46e3dc01`
and carries #922 head `c4083798f9ef27a26c8882cb622e5b7eeaa4a540`.
Round three adds three boundaries:

- Every planning read of a PR, diff, checks, stable-head re-read, or base
  comparison sends a non-definite failure through the shared per-PR skip and
  `reconcile_read_failed` audit path. Exact-wrapper 401, 403, and 404 keep
  their existing fatal handling.
- Once `update_requested` is journaled or a merge is sent, a non-definite
  state read failure ends the run with `PR #N's state after the update or
  merge could not be read; the next run recovers it from the journal`. The
  next run performs that recovery before another PR can land. The source
  guard declares every direct Forge read as pre-mutation or post-mutation.
- A pending merge intent whose PR is already merged at another head records
  the reconcile mismatch and terminal merged-elsewhere refusal immediately,
  without a diff or checks read. Mixed refusal strings choose screen words in
  fixed priority: `update_conflict`, strict policy, merged elsewhere, then
  the existing rule order; their full detail remains unchanged.

### Round-three revert runs

Each mutation below was applied alone and removed before the next run.

| Row | Command | Exact rollback | First failing line |
| --- | --- | --- | --- |
| Planning PR, diff, checks, and stable-head re-read, both 502 and EOF | `go test ./internal/mergetrain -run 'TestEveryPlanningReadFailureSkipsOnlyThatPR/(PR\|Diff\|Checks)' -count=1` | Returned `checkPR`'s error before `preMutationReadFailure` in the planning loop | `read PR #1: ... Bad Gateway (HTTP 502)`; the diff, checks, re-read, and EOF subtests failed at their corresponding direct error |
| Planning base comparison, both 502 and EOF | `go test ./internal/mergetrain -run 'TestEveryPlanningReadFailureSkipsOnlyThatPR/Behind' -count=1` | Returned the compare error before `preMutationReadFailure` | `compare #1 with pinned base: ... Bad Gateway (HTTP 502)` |
| Read after update | `go test ./internal/mergetrain -run '^TestReadAfterUpdateFailureStopsAndNextRunRecovers$' -count=1` | Returned the raw `readUpdatedHead` error instead of `postMutationReadFailure` | `post-update read did not stop in words: read #1 after its update: ... Bad Gateway (HTTP 502)` |
| Read after merge | `go test ./internal/mergetrain -run '^TestReadAfterMergeFailureStopsAndNextRunRecovers$' -count=1` | Returned the raw PR read error from `mergedAt` | `post-merge read did not stop in words: EOF` |
| Direct read source guard | `go test ./internal/mergetrain -run '^TestEveryForgeReadDeclaresItsMutationBoundary$' -count=1` | Planted `t.Forge.PR` in an unlisted `undeclaredForgeRead` function | `undeclaredForgeRead has an undeclared Forge read PR` |
| Mixed-rule screen priority | `node --test --test-reporter=spec pi/test/words.test.ts` | Selected `rules[0]` instead of the priority list | actual `its head moved after the check`, expected `GitHub's up-to-date rule refused it; no gate overrides it` |
| Pending intent merged elsewhere | `go test ./internal/mergetrain -run '^TestPendingIntentMergedElsewhereIsTerminalBeforeDiffAndChecks$' -count=1` | Restored `cur.HeadSHA != e.Head` to the early `continue` condition | `pending intent mismatch was not terminal`; the fallback plan row had no recorded merge and reached the ordinary check path |

### Round-three gate record

Seat gates run unlocked in the sandbox by design. No host lock is used or
claimed here. The lead reruns the locked gates on the exact replacement PR
head after the harness creates it.

The final code-only diff under `internal/` and `pi/` has SHA-256
`18fe473f87717cc2739f1845720edff4c5da4821ac798167af70f28068f0d3ae`.
The harness assigns the final commit SHA after the done report.

| Gate | Exit | Result |
| --- | ---: | --- |
| Focused round-three Go rows and source guard | 0 | `internal/mergetrain`, 87.710 s; a later current-source subset passed in 13.920 s |
| `go test ./internal/mergetrain -count=1` | 0 | `ok`, 210.025 s in the final aggregate run |
| `go test ./internal/gatetarget -count=1` | 0 | `ok`, 0.327 s |
| isolated scrubbed-environment `go test ./internal/cli -count=1` | 0 | `ok`, 165.993 s; the final aggregate rerun also passed in 64.099 s |
| `npm --prefix pi test` | 0 | 116 passed, 0 failed, 1 staged-runtime skip, 44.552 s; rerun with temporary local-socket permission after the sandbox-only EPERM result |
| `npm --prefix pi run lint` | 0 | `lint ok: 29 files, pi 1.3.0` |
| `go build ./...` and `go vet ./...` | 0 | passed |
| fully AMQ-scrubbed `make ci` | 2 | `go vet` and every Go package passed; README and skills HTML sync checks passed; `skills-check` then stopped because PyYAML is not installed (`pip3 install pyyaml`) |
| `git diff --check` | 0 | clean; all 12 changed paths are inside the declared set |

## Round-four review fixes for #924

The working tree starts at `8be23f5e996345fdd289c1e09824d2fe46e3dc01`
and carries #924 head `055b563c816b694a204073167d16b943af6efe22`.

"Post-mutation means after this run's own update or merge request." Within
the run that made the mutation, a non-definite read error ends the run in the
recovery words. Recovery in a later run reads the journaled update's proof
fresh and was built for this state, so a non-definite proof read writes
`reconcile_read_failed`, skips that PR, and lets the next PR proceed. A later
run tries recovery again.

Round four adds these boundaries:

- `readUpdatedHead` has three deadline outcomes. A head seen moved wins, even
  after an earlier failed poll. All successful polls seeing the pinned head,
  with timeouts counted as unmoved polls, produce
  `branch_update_unconfirmed`. A non-definite read failure with no observed
  move ends the run in the recovery words.
- Recovery proof reads of first parent and both patch ids use the same
  pre-mutation per-PR skip boundary as the other recovery reads.
- A non-definite Checks read in `waitCI` ends the run immediately in the
  recovery words. It is never silently retried to the deadline.
- The Forge read source guard declares each call site by its ordinal, scans
  `internal/cli/merge_train.go` too, and rejects an extra read inside an
  otherwise declared function. `checkPR` takes an explicit `readPhase`; the
  planning call is pre-mutation and the recheck after an update is
  post-mutation.
- `pr_state` is promoted in the screen priority only when its detail is the
  merged-elsewhere form. A plain `ci_green,pr_state` refusal keeps the first
  rule's CI guidance.
- Both EOF and an HTTP 502 from the read after a merge return the recovery
  words and are recovered on the next run.
- Merged-elsewhere detail names the server head and the distinct merge
  commit independently.

### Round-four revert runs

Each mutation was applied alone and removed before the next command.

| Row | Command and exact rollback | First failing line |
| --- | --- | --- |
| Recovery proof read | `go test ./internal/mergetrain -run '^TestRecoveryProofReadFailureSkipsOnlyThatPR$' -count=1`; returned `proveUpdate`'s error directly instead of calling `preMutationReadFailure` | `train_gates_320_test.go:592: read the first parent of ...: gh: Bad Gateway (HTTP 502)` |
| Three deadline outcomes | `go test ./internal/mergetrain -run '^TestUpdatedHeadDeadlineHasThreeOutcomes$' -count=1`; returned nil instead of `nonDefiniteErr` at the deadline | `train_gates_320_test.go:512: outcome = head aaaa... moved false err <nil> after 3 reads` |
| waitCI read failure | `go test ./internal/mergetrain -run '^TestWaitCINonDefiniteReadFailureStopsInRecoveryWords$' -count=1`; silently slept and retried after the Checks error | `train_gates_320_test.go:634: waitCI read did not stop in recovery words: <nil>` |
| Per-call-site source guard | `go test ./internal/mergetrain -run '^TestEveryForgeReadDeclaresItsMutationBoundary$' -count=1`; planted a second `t.Forge.PR` inside `checkPR` | `train_gates_320_test.go:408: checkPR has an undeclared Forge read PR at call site 3` |
| checkPR caller phase | The same guard command; changed `land`'s call from `readPhasePostMutation` to `readPhasePreMutation` | `train_gates_320_test.go:396: land calls checkPR with phase readPhasePreMutation, want readPhasePostMutation` |
| Post-update checkPR routing | `go test ./internal/mergetrain -run '^TestPostUpdateCheckPRReadFailureStopsInRecoveryWords$' -count=1`; routed the post-update check through the pre-mutation skip branch | `train_gates_320_test.go:667: ... err <nil> ... Status:skipped` |
| Plain pr_state priority | `node --test --test-reporter=spec pi/test/words.test.ts`; promoted every `pr_state` regardless of detail | actual `the PR is not open or not mergeable`, expected `its CI is not green` |
| 502 after merge | `go test ./internal/mergetrain -run 'TestReadAfterMergeFailureStopsAndNextRunRecovers/502' -count=1`; returned the raw `mergedAt` error | `train_gates_320_test.go:776: post-merge read did not stop in words: ... Bad Gateway (HTTP 502)` |
| Merged-elsewhere merge commit | `go test ./internal/mergetrain -run '^TestMergedElsewhereIsTerminalForTheBoundHead$' -count=1`; printed the server head in the merge-commit slot | `train_gates_320_test.go:118: merged-elsewhere words do not distinguish server head ... and merge commit ...` |

The restored focused rows passed together in 5.310 s, and the restored words
test passed all 18 tests.

### Round-four gate record

Seat gates ran unlocked in the sandbox. The lead reruns locked gates on the
exact replacement PR head after the harness creates it.

| Gate | Exit | Result |
| --- | ---: | --- |
| Restored round-four rows and source guard | 0 | `internal/mergetrain`, 5.310 s |
| `go test ./internal/mergetrain -count=1` | 0 | `ok`, 110.350 s |
| `go test ./internal/gatetarget -count=1` | 0 | `ok`, 0.336 s |
| Scrubbed-environment `go test ./internal/cli -count=1` | 0 | `ok`, 63.424 s |
| `npm --prefix pi test` with local test-socket permission | 0 | 117 tests: 116 passed, 0 failed, 1 staged-runtime skip, 15.286 s |
| `npm --prefix pi run lint` | 0 | `lint ok: 29 files, pi 1.3.0` |
| `go build ./...`, `go vet ./...`, `git diff --check` | 0 | passed |
| Fully AMQ-scrubbed `make ci` | 2 | repository-wide vet and Go tests passed, including `internal/mergetrain` in 220.827 s; README and skills HTML were in sync; `skills-check` then stopped because host Python lacks PyYAML (`pip3 install pyyaml`) |

The 12 changed paths are all inside the task's declared set. The PyYAML stop
is a host dependency failure after the source gates above, not a product test
failure; later make targets did not run in that invocation.
