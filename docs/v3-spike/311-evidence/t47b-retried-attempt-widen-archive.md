# t47b retried typed-widen archive

The on-demand archive calls `ArchiveAttempt` directly after the typed-block
guards. Its `(task ID, BlockAttempt)` dedup is therefore made inside the ledger
lock. Archive failure appends evidence to the first `CheckWiden` decision and
never replaces its named refusal.

`TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection` pauses
the first archive after snapshotting and before ledger write, changes the
worktree, then starts the second archive. The second receives `ErrLedgerBusy`;
the surviving discard is the first writer's parent and tree. The wait points
are bounded at ten seconds. Run under the race detector with:

```
go test -race ./internal/seatcommit -run TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection -count=1
```

Revert evidence: removing the `withLedgerTryLock` protection lets the second
writer enter the held window and the lock-proof row fails. Requiring
`widen_snapshot_missing` again makes the retried-attempt row fail. Returning an
archive error from the helper without preserving the decision makes the named-
refusal row fail. The existing #856 immediate archive, unrecorded block,
reverse-order, and first-writer rows remain in `seat_fix1b_test.go` and
`ledger_test.go`; all fixture rows use temporary HOME and no network, AMQ, or
tmux.
