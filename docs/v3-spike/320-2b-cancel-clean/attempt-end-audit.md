# Attempt-end cleanup audit

This audit records every `endAttempt` path that does not itself call
`CleanSeat`. File and line references are for the 3.2.0-2b-1 implementation.

| Outcome | End site | 3.2.0-2b-1 disposition |
| --- | --- | --- |
| blocked, verdict missing | `internal/task/lease.go:487` | Unchanged. The seat remains available for lead repair; preparation cleans before a later grant. Track immediate end cleanup in the 3.2.0 follow-up list. |
| blocked, verdict failed | `internal/task/lease.go:491` | Unchanged. The seat remains available for lead repair; preparation cleans before a later grant. Track immediate end cleanup in the 3.2.0 follow-up list. |
| completed | `internal/task/lease.go:497` | Unchanged. The commit step has already settled the worktree. Track defensive immediate cleanup in the 3.2.0 follow-up list. |
| commit refused | `internal/task/lease.go:552` | Unchanged. Preparation archives and cleans before reuse. Track immediate end cleanup in the 3.2.0 follow-up list. |
| failed | `internal/task/lease.go:585` | Unchanged. The exit check runs and preparation cleans before reuse. Track immediate end cleanup in the 3.2.0 follow-up list. |
| blocked | `internal/task/lease.go:621` | Unchanged except for the existing typed-widen archive. Track general blocked-attempt cleanup in the 3.2.0 follow-up list. |
| released | `internal/task/lease.go:657` | Unchanged. Preparation cleans before reuse. Track immediate end cleanup in the 3.2.0 follow-up list. |
| cancelled | `internal/task/lease.go:721` | Changed. `internal/scheduler/exits.go` closes or interrupts the seat, waits for confirmed exit, checks the sealed evidence ledger, archives the named task and attempt, cleans the worktree, then releases the held slot. |
| expired or unclaimed | `internal/task/lease.go:764` | Unchanged. Claimed expiry already closes before its exit check; preparation cleans before reuse. Track archive-and-clean at the end boundary in the 3.2.0 follow-up list. |

The changed cancellation path deliberately lives after process termination,
not in the task-store transaction. The store has no authority to stop a seat
process, and cleanup must never race a live writer.
