# t47 retried typed-widen archive

Base: `74730bca` (#856 merged).

## Result

The on-demand widen archive now uses the durable `(task ID, BlockAttempt)`
key. It no longer relies on `CheckWiden` returning `widen_snapshot_missing`.
An earlier discard for the same task therefore cannot make a retried typed
widen wait for the scheduler scan: the second attempt receives its own archive
and its dry run reads that attempt's parent and tree.

The archive and `CleanSeat` contend through the existing ledger lock. The
race row releases both writers from one barrier 50 times and permits the
non-winning try-lock call to report `ErrLedgerBusy`. Every iteration records
exactly one discard for the task and attempt, with the parent and tree that the
two writers saw before the barrier opened.

## Verification

- `go test ./internal/cli -run 'TestTypedWiden(ArchivesARetriedAttemptOnDemand|ArchiveContendsWithCleanSeat)' -count=1`: pass with live AMQ seat variables removed.
- `go test -race ./internal/cli -run TestTypedWidenArchiveContendsWithCleanSeat -count=1`: pass with live AMQ seat variables removed.
- `go test ./internal/schedproc ./internal/seatcommit ./internal/scheduler ./internal/cli -count=1`: pass with live AMQ seat variables removed.
- `git diff --check`: pass.

## Revert runs

The four #856 rows remain covered by `t44-on-demand-widen-archive.md`:
immediate typed archive, unrecorded typed block refusal, reverse-order archive,
and the pre-existing first-writer row.

1. Revert the keyed trigger in `archiveTypedWidenAttempt` to require
   `ReasonWidenSnapshotMissing`. `TestTypedWidenArchivesARetriedAttemptOnDemand`
   fails because the old attempt causes a snapshot-mismatch refusal.
2. Revert the retried-attempt row's second archive path. The same row fails:
   no discard exists for attempt 2 and its dry run cannot use attempt 2's tree.
3. Drop `withLedgerTryLock` from the archive test seam (or remove the locking
   in either writer). `go test -race ./internal/cli -run
   TestTypedWidenArchiveContendsWithCleanSeat -count=1` reports the race or
   the row observes an invalid ledger result.

Each revert is local, runs against the fixture's temporary HOME, and reaches no
network, real AMQ, or tmux.
