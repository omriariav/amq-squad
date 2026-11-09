# 3.2.0-rc.2 legacy lease normalization

Task t126, based on `origin/v3` at `60cc544a`. The implementation keeps the
persisted lease schema unchanged.

## Result

An unstamped live lease now gets a budget end equal to the later of:

- its grant plus the task budget, defaulting to 90 minutes for a 3.1.2 row;
- the normalization time plus `task.DefaultLeaseDuration`.

The existing expiry clamp remains one-way: normalization may shorten
`ExpiresAt`, but never moves it forward. A lease already past its own expiry
still ends as `lease_expired`. A shared helper applies the same stamp when the
lead wins the race and extends an unstamped lease, before adding the extension.

#891 chose one lease length for the renewal paths. That is
`task.DefaultLeaseDuration`, ten minutes, and equals the scheduler's configured
lease duration in practice. This task does not thread scheduler configuration
through the normalization API.

A normalization row is emitted only when a task field changed. An attempt-0
legacy row therefore writes and logs once without inventing an attempt number;
later scheduler, manual-renew, and hook passes neither rewrite the task nor log
another `lease_normalised` line.

## Acceptance rows

All fixtures use a fixed clock `T`. They claim a task and then clear the legacy
lease's attempt, budget, and absolute budget end.

| Row | Test | Result |
| --- | --- | --- |
| 1, old grant | `TestLegacyBudgetEndUsesLaterOfGrantBudgetAndOneLease/old_grant_gets_one_lease` | Grant `T-100m` stamps `T+10m`; expiry stays `T+5m`; the first expiry pass changes nothing. |
| 2, young grant | `TestLegacyBudgetEndUsesLaterOfGrantBudgetAndOneLease/young_grant_keeps_its_budget` | Grant `T-5m` stamps `T+85m`, proving the max is not replaced by the grace alone. |
| 3, idempotent callers | `TestLegacyBudgetNormalizationIsIdempotentAcrossCallers` | Later blocking and no-wait passes return no rows and preserve `T+10m`. |
| 4, scheduler tick | `TestSchedulerGivesAnOldLegacyAttemptOneLease` | First tick logs one normalization and one budget warning, no expiry/spent/closing line; second tick logs no normalization. |
| 5, grace end and extension | `TestLegacyGraceEndsUnlessTheLeadExtendsIt` | The unextended renewed attempt ends at `T+10m` as `task budget of 1h30m0s spent`; its extended twin remains live. |
| 6, extend first | `TestExtendBeforeNormalizationAddsToOneLeaseGrace` | A `T-3h` unstamped lease extended by 30 minutes ends no earlier than `T+40m`. |
| 7, attempt 0 | `TestAttemptZeroNormalizationWritesAndLogsOnce` | Ticks at `T`, `T+1m`, and `T+2m` produce one normalization total, leave task bytes and store generation unchanged after `T`, and preserve attempt 0. |
| Expired lease guard | `TestLegacyLeasePastItsOwnExpiryIsNotRevived` | An own expiry at `T-1m` remains past and ends as `lease_expired`, not budget spent. |
| Existing compatibility row | `TestPreUpgradeLeaseIsNormalizedExactlyOnce` | Unchanged and green; its young grant continues to win over the one-lease grace. |

The scheduler rows remain in `internal/task/normalise320_tick_test.go` as an
external `task_test` package so this task stays disjoint from the live
`internal/scheduler/` file lock.

## Revert runs

Each temporary production edit was restored immediately after its failing run.
Commands used a sandbox-local Go cache.

| Rows | Reverted expression | Command | First failure |
| --- | --- | --- | --- |
| 1 | Removed the grace max, leaving `end := t.Lease.GrantedAt.Add(t.Budget)`. | `go test ./internal/task -run 'TestLegacyBudgetEndUsesLaterOfGrantBudgetAndOneLease/old_grant_gets_one_lease' -count=1` | `normalise320_test.go:48`: got `07:50`, wanted `08:10`. |
| 2 | Replaced the max with `end := now.Add(DefaultLeaseDuration)`. | `go test ./internal/task -run 'TestLegacyBudgetEndUsesLaterOfGrantBudgetAndOneLease/young_grant_keeps_its_budget' -count=1` | `normalise320_test.go:48`: got `08:10`, wanted `09:25`. |
| 3 | Removed the grace max, leaving grant plus budget. | `go test ./internal/task -run 'TestLegacyBudgetNormalizationIsIdempotentAcrossCallers' -count=1` | `normalise320_test.go:75`: budget end was `07:50`, not `T+10m`. |
| 4 | Removed the grace max, leaving grant plus budget. | `go test ./internal/task -run 'TestSchedulerGivesAnOldLegacyAttemptOneLease' -count=1` | `normalise320_tick_test.go:68`: first tick logged `task_budget_spent`. |
| 5 | Removed the grace max, leaving grant plus budget. | `go test ./internal/task -run 'TestLegacyGraceEndsUnlessTheLeadExtendsIt' -count=1` | `normalise320_test.go:106`: renewal was refused as `task budget spent`. |
| 6 | Restored the old `ExtendBudget` branch, stamping `GrantedAt.Add(t.Budget)` after increasing the budget. | `go test ./internal/task -run TestExtendBeforeNormalizationAddsToOneLeaseGrace -count=1` | `normalise320_test.go:134`: got `07:00`, wanted `08:40`. |
| 7, stamp guard | Removed the helper's `BudgetEndsAt.IsZero()` guard. | `go test ./internal/task -run TestAttemptZeroNormalizationWritesAndLogsOnce -count=1` | `normalise320_tick_test.go:107`: `T+1m` logged again and moved the end to `08:11`. |
| 7, write guard | Restored the old row-per-pass behavior by removing `if !changed { continue }`. | `go test ./internal/task -run TestAttemptZeroNormalizationWritesAndLogsOnce -count=1` | `normalise320_tick_test.go:107`: `T+1m` logged `lease_normalised` again. |

## Gates

Run in the seat sandbox on 2026-11-10. `GOCACHE` was placed under `/tmp`
because the sandbox refuses writes to the host Go cache.

- `go build ./...`: exit 0. Go printed a non-fatal module stat-cache write
  warning for the host cache.
- `go vet ./...`: exit 0.
- Focused acceptance command over the new rows and the unchanged compatibility
  row: pass.
- `go test ./internal/task/... ./internal/scheduler/... ./internal/cli/...`:
  `internal/task` and `internal/scheduler` pass. `internal/cli` cannot complete
  in this seat sandbox because its Unix listeners fail with `bind: operation
  not permitted`; dependent scheduler-socket rows then time out. The focused
  caller row `TestManualRenewNormalizesAndBindsTheLegacyAttempt` passes.
- `go test ./...`: run to completion. The changed package `internal/task` and
  `internal/scheduler` pass, along with the unaffected packages that do not
  need listeners. The overall command exits 1 because the seat sandbox denies
  Unix or loopback TCP binds in `internal/buzz`, `internal/cli`,
  `internal/gateverify`, `internal/registry`, `internal/schedproc`,
  `internal/seatcommit`, and `internal/seatlaunch`. The lead must rerun the
  locked gate on the PR head outside this seat sandbox.
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-normalise/`:
  exit 0.

No test reaches an external network. The scheduler tests are in-process and
all stores write only below `t.TempDir()`.

## Release note

The rc.2 release PR should replace these two current passages in
`docs/v3.2.0-release-notes.md`.

Legacy-lease paragraph to replace:

> A claimed task with no pending done report whose lease was granted more than
> 90 minutes before the upgrade is over its budget at once: it expires on the
> scheduler's first tick and its seat is closed.

Owner-step sentence to replace:

> A claimed task with no pending done report whose lease was granted more than
> 90 minutes before the restart is already past its budget end: the scheduler's
> first tick expires it (`task_budget_spent`), closes its seat and puts the task
> back to pending.

Use these words in both places: the upgraded budget end is the later of the
lease grant plus 90 minutes and the restart plus one lease, ten minutes. The
stamp alone therefore ends no attempt on the first scheduler tick. The lead
gets `task_budget_warning` and at least one lease in which to run `task extend`
for work that should keep running. An attempt still ends at the stamped budget
end when it is not extended, and an already-expired own lease is not revived.
