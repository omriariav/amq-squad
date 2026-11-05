# t30: typed widen flow fixes

Base: `2a124ca8` (#844 merged). This is part 1 of the t29 split and contains
only the growth-gate retry and size-cap dry-run changes.

## Result

- A typed `widen_requested` block keeps its exact `refused_paths` while a
  grant crossing waits for the owner's growth gate.
- The accepted retry recognizes the preserved typed request, records the
  accepted gate, and returns the task to `pending` for redispatch.
- A typed block dry-runs the stopped seat's current diff using the holder of
  its ended attempt. A diff above the size cap therefore raises the same
  growth gate as a harness commit refusal, even though the block has no done
  report.

## Reverting rows

- `TestTypedWidenPathsSurviveEscalationAndApprovedRetry`
- `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/protected_retry`
- `TestTypedWidenRetriesAfterGrowthGateAndDryRunsTheSeatDiff/size_cap_dry_run`
- Existing compatibility rows: `TestLeadWidenOnBlockedTaskUsesOnlyItsRecordedExactPaths` and `TestSeatCommitAndWidenThroughTheGrowthGate`.

## Verification

- Focused rows across `internal/task`, `internal/seatcommit`, and
  `internal/cli`: pass.
- `go test ./internal/task ./internal/seatcommit ./internal/cli -count=1`,
  with live AMQ launch variables removed from the test process: pass.
- `git diff --check`: pass.

