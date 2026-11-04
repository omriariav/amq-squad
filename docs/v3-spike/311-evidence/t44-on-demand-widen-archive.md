# t44 on-demand typed widen archive

Base: `3b4260b9` (#855 merged).

## Result

The scheduler decision path now closes the window between a typed block and
the next attempt scan. When `CheckWiden` reports `widen_snapshot_missing` for
the task's recorded `BlockAttempt`, the decision path calls the existing
sealed `ArchiveAttempt` writer and checks the widen again.

The fallback is limited to the matching typed block. Other missing snapshots
remain named refusals. The shared ledger key remains first-writer-wins: if
CleanSeat or the regular scheduler scan archives between the first check and
the fallback, the fallback leaves that entry unchanged and evaluates it.

The reverse-order row archives with CleanSeat, changes the live worktree, then
runs both later archive callers. It proves the ledger retains one discard with
the first writer's parent and tree.

## Verification

- `go test ./internal/schedproc ./internal/seatcommit ./internal/scheduler ./internal/cli -count=1`: pass with live AMQ seat variables removed.
- Focused immediate archive, concurrent first-writer, unrecorded typed block,
  and reverse-order ledger rows: pass.
- `git diff --check`: pass.
