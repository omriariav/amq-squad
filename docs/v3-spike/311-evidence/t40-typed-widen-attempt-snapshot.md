# t40 typed widen attempt snapshot

Base: `aec7a616` (#852 merged).

## Result

The smaller design won: CleanSeat's existing discarded-tree archive is the
snapshot record. Each discard now carries the stopped task attempt number.
Typed widen dry runs select the archived parent and tree by task id plus
attempt number, then compute raw changes and numstat from Git objects. They do
not read the reusable seat's current lease or worktree contents.

Missing archives fail with `widen_snapshot_missing`. An archive for the task
under another attempt fails with `widen_snapshot_attempt_mismatch`. The rows
reuse the seat before widening and prove both an over-cap request and an
approved protected-path retry still use the stopped attempt.

`WidenGated` now treats only `widen_requested` and
`growth_gate_required` as reportless typed requests. A path-bearing block
relabelled `outside_declared_files` stays blocked.

## Verification

- `go test ./internal/task ./internal/cli ./internal/seatcommit ./internal/scheduler -count=1`: pass with live AMQ seat variables removed.
- Focused snapshot, seat-reuse, and relabelled-block rows: pass.
- `git diff --check`: pass.
