# t47c retried typed-widen archive

Base: `929cd97e`.

## Acceptance rows

> 1. Lock row: `TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection` in `internal/seatcommit/ledger_test.go` captures the ledger parent and the first worktree's `git write-tree` independently before either writer runs, commits a distinct second tree inside the held window, requires `ErrLedgerBusy`, and compares the surviving discard with both independent values. The hook release is bounded to three seconds and `t.Cleanup` closes it on every failing path.
> 2. Retried-attempt row: `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/retried_attempt_archives_its_own_snapshot` in `internal/cli/seat_fix1b_test.go` captures attempt 2's expected `git write-tree` independently, proves T2 differs from T1, invokes the real archive, requires both the second discard and dry run to name T2, and preserves attempt 1 byte for byte.
> 3. Absent-ledger row: `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/absent_ledger_preserves_the_first_decision_through_apply` in `internal/cli/seat_fix1b_test.go` calls `CheckWiden` with the same options before apply, then drives `applyLeadWiden` through `widenDecision` with a real filesystem failure and real `ArchiveAttempt`; the reply keeps that captured code and detail prefix before appending `on-demand archive pending`. The adjacent `archive_error_preserves_the_first_named_refusal` subtest keeps the unit seam row.
> 4. Unrecorded-attempt row: `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/missing_snapshot_without_typed_block_stays_refused` in `internal/cli/seat_fix1b_test.go` remains unchanged in purpose and proves the archive is not called.
> 5. Naming: `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/on_demand_keeps_the_first_archived_attempt` in `internal/cli/seat_fix1b_test.go` is the sequential seam name and no longer says `concurrent`.
> 6. Revert evidence below records each behavioral row's command, reverted line, and first assertion line. The lock revert removes only `withLedgerTryLock` and fails at the busy assertion, with no panic.

All fixtures use temporary homes and local repositories. They use no network, real AMQ, or tmux.

## Revert runs

### Lock exclusion

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/seatcommit -run TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection -count=1`
- Reverted `internal/seatcommit/commit.go:517`, old text `err = withLedgerTryLock(root, handle, func() error {`, to an immediately invoked closure, removing only the try-lock protection.
- First assertion line: `ledger_test.go:109: second archive entered the first writer's window: <nil>`.

### Retried attempt

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/retried_attempt_archives_its_own_snapshot' -count=1`
- Reverted `internal/cli/task_widen.go:191`, old text `if !typedWidenRequest(t) || wo.Commit == nil || wo.Commit.SnapshotAttempt != t.BlockAttempt {`, by restoring the old `d.ReasonCode != seatcommit.ReasonWidenSnapshotMissing` restriction.
- First assertion line: `seat_fix1b_test.go:402: attempt 2 widen = ... ReasonCode:widen_snapshot_attempt_mismatch ...`.

### Real absent-ledger failure

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/absent_ledger_preserves_the_first_decision_through_apply' -count=1`
- Reverted `internal/cli/task_widen.go:197`, old text `d.Detail = strings.TrimSpace(d.Detail + "; on-demand archive pending: " + err.Error())`, by removing the append.
- First assertion line: `seat_fix1b_test.go:442: filesystem archive failure replaced first decision: first=... reply={OK:false Code:widen_check_failed ...}`.

### Stubbed archive failure

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/archive_error_preserves_the_first_named_refusal' -count=1`
- Reverted the same `internal/cli/task_widen.go:197` detail append.
- First assertion line: `seat_fix1b_test.go:470: archive error replaced the first decision: {Allowed:false ReasonCode:base_unrecorded Detail:no core-recorded base ...}`.

### Unrecorded attempt guard

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/missing_snapshot_without_typed_block_stays_refused' -count=1`
- Reverted `internal/cli/task_widen.go:191`, old text `if !typedWidenRequest(t) || wo.Commit == nil || wo.Commit.SnapshotAttempt != t.BlockAttempt {`, to `if wo.Commit == nil {`.
- First assertion line: `seat_fix1b_test.go:457: unrecorded attempt = ... called=true`.

## T68 strengthened-assertion revert runs

- Independent lock snapshot: `go test ./internal/seatcommit -run TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection -count=1`. The mutation replaced `snapshot`'s final `write-tree` with the parent tree, which the old hook-relative comparison accepted. The independent expected-tree comparison first failed at `ledger_test.go:125: surviving discard = ... expected parent/tree=...`.
- Retried attempt T2: `go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/retried_attempt_archives_its_own_snapshot' -count=1`. The mutation copied attempt 1's discard into attempt 2 while changing only its attempt number. The old record-to-dry-run comparison accepted the copy; the independent T2 comparison first failed at `seat_fix1b_test.go:406: attempt 2 ledger/dry run = ... expected tree=... attempt 1 tree=...`.
- Captured first decision: `go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/absent_ledger_preserves_the_first_decision_through_apply' -count=1`. The mutation replaced both the first code and detail with `widen_snapshot_missing` while retaining the archive-pending suffix. The old reply-to-embedded-decision comparison accepted the swap; comparison with the pre-apply decision first failed at `seat_fix1b_test.go:442: filesystem archive failure replaced first decision`.

## Passing verification

- Race command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test -race ./internal/seatcommit -run TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection -count=1`.
- Full command, run outside the sandbox with every live `AMQ_SQUAD_*`, `AMQ_ROOT`, `AMQ_ME`, `AMQ_SESSION`, and `AMQ_WAKE_OWNER` variable removed: `go test ./internal/cli ./internal/seatcommit ./internal/scheduler ./internal/schedproc -count=1`.
- Result: all four packages passed.
- T68 final-state gate, with a temporary `HOME` and all live AMQ variables absent: `go test ./internal/cli ./internal/seatcommit ./internal/mergetrain ./internal/scheduler -count=1`; all four packages passed.
