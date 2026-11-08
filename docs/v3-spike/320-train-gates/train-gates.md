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

During merge reconciliation, a per-PR 5xx, transport error, deadline, rate
limit or killed `gh` writes `reconcile_read_failed` and skips only that PR.
A definite 4xx other than a rate-limit response remains a run-wide error.

## Regression rows

The focused command is:

```sh
GOCACHE=/tmp/amq-squad-reviewer-gocache go test ./internal/mergetrain -run 'Test(StrictRefusalHasWordsAndNoGate|AcceptedLegacyGateDoesNotLiftStrictRefusal|BranchUpdateConflictIsTypedAndUngated|MergedElsewhereIsTerminalForTheBoundHead|StrictRefusalIsRecheckedOnTheNextRun|TransientReconcileReadSkipsOnlyThatPR|DefiniteReconcileReadFailureStopsTheRun|ActionableRefusalStillRaisesGrowthGate|StrictPlanOwnerTextIsWordsFirst|UnliftableRefusalsPrintTheActionableWordsWithoutAGate)$' -count=1
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
| G4 | `TestTransientReconcileReadSkipsOnlyThatPR` | Return every reconcile read error instead of classifying it | `read PR #1 while reconciling: ...` in each of the five subtests |
| G4b | `TestDefiniteReconcileReadFailureStopsTheRun` | Return true for the classifier's definite-error fallback | `403 did not stop the run: <nil>` |
| G5 | `TestActionableRefusalStillRaisesGrowthGate` | Set `noGate := true` for every refusal | `actionable refusal lost its gate` |
| G6 | `pi/test/words.test.ts`, row 7 | Remove `update_conflict` from `trainRuleWords` | `train rule update_conflict has no words` |

Each mutation above was applied, the command was run to the named failure,
and the mutation was restored before the green validation run.
