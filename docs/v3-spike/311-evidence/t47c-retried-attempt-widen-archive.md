# t47c retried typed-widen archive

Base: `929cd97e`.

## Acceptance rows

> 1. Lock row: `TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection` in `internal/seatcommit/ledger_test.go` commits a different parent inside the held window, uses a single-use nonblocking hook, requires `ErrLedgerBusy`, and retains the first parent and tree with bounded waits.
> 2. Retried-attempt row: `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/retried_attempt_archives_its_own_snapshot` in `internal/cli/seat_fix1b_test.go` starts from an archived attempt 1, observes a non-missing mismatch for attempt 2, invokes the real archive, measures attempt 2, and preserves attempt 1 byte for byte.
> 3. Absent-ledger row: `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/absent_ledger_preserves_the_first_decision_through_apply` in `internal/cli/seat_fix1b_test.go` drives `applyLeadWiden` through `widenDecision` with a real filesystem failure and real `ArchiveAttempt`; the reply keeps the first code, appends `on-demand archive pending`, and is never `widen_failed`. The adjacent `archive_error_preserves_the_first_named_refusal` subtest keeps the unit seam row.
> 4. Unrecorded-attempt row: `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/missing_snapshot_without_typed_block_stays_refused` in `internal/cli/seat_fix1b_test.go` remains unchanged in purpose and proves the archive is not called.
> 5. Naming: `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/on_demand_keeps_the_first_archived_attempt` in `internal/cli/seat_fix1b_test.go` is the sequential seam name and no longer says `concurrent`.
> 6. Revert evidence below records each behavioral row's command, reverted line, and first assertion line. The lock revert removes only `withLedgerTryLock` and fails at the busy assertion, with no panic.

All fixtures use temporary homes and local repositories. They use no network, real AMQ, or tmux.

## Revert runs

### Lock exclusion

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/seatcommit -run TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection -count=1`
- Reverted `internal/seatcommit/commit.go:517`, old text `err = withLedgerTryLock(root, handle, func() error {`, to an immediately invoked closure, removing only the try-lock protection.
- First assertion line: `ledger_test.go:105: second archive entered the first writer's window: <nil>`.

### Retried attempt

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/retried_attempt_archives_its_own_snapshot' -count=1`
- Reverted `internal/cli/task_widen.go:191`, old text `if !typedWidenRequest(t) || wo.Commit == nil || wo.Commit.SnapshotAttempt != t.BlockAttempt {`, by restoring the old `d.ReasonCode != seatcommit.ReasonWidenSnapshotMissing` restriction.
- First assertion line: `seat_fix1b_test.go:395: attempt 2 widen = ... ReasonCode:widen_snapshot_attempt_mismatch ...`.

### Real absent-ledger failure

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/absent_ledger_preserves_the_first_decision_through_apply' -count=1`
- Reverted `internal/cli/task_widen.go:197`, old text `d.Detail = strings.TrimSpace(d.Detail + "; on-demand archive pending: " + err.Error())`, by removing the append.
- First assertion line: `seat_fix1b_test.go:429: filesystem archive failure replaced first decision: reply={OK:false Code:widen_check_failed ...}`.

### Stubbed archive failure

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/archive_error_preserves_the_first_named_refusal' -count=1`
- Reverted the same `internal/cli/task_widen.go:197` detail append.
- First assertion line: `seat_fix1b_test.go:457: archive error replaced the first decision: {Allowed:false ReasonCode:base_unrecorded Detail:no core-recorded base ...}`.

### Unrecorded attempt guard

- Command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test ./internal/cli -run 'TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/missing_snapshot_without_typed_block_stays_refused' -count=1`
- Reverted `internal/cli/task_widen.go:191`, old text `if !typedWidenRequest(t) || wo.Commit == nil || wo.Commit.SnapshotAttempt != t.BlockAttempt {`, to `if wo.Commit == nil {`.
- First assertion line: `seat_fix1b_test.go:444: unrecorded attempt = ... called=true`.

## Passing verification

- Race command: `env -u AMQ_LAUNCH_NONCE -u AMQ_SQUAD_SEAT -u AMQ_SQUAD_TASK_ID -u AMQ_SQUAD_TASK_LEASE_TOKEN -u AM_SESSION GOCACHE=/private/tmp/amq-squad-reviewer-go-cache go test -race ./internal/seatcommit -run TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection -count=1`.
- Full command, run outside the sandbox with every live `AMQ_SQUAD_*`, `AMQ_ROOT`, `AMQ_ME`, `AMQ_SESSION`, and `AMQ_WAKE_OWNER` variable removed: `go test ./internal/cli ./internal/seatcommit ./internal/scheduler ./internal/schedproc -count=1`.
- Result: all four packages passed.
