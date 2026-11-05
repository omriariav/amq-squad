# t27: typed seat widen requests

Base: `915af0c4`

Reissued as t28 on base `31bf161c` after the owner raised the task size cap.
The retained t27 evidence ref applied cleanly to that base.

## Contract

- A seat blocks with reason `widen_requested` and one or more exact paths.
- The task store normalizes, sorts, and deduplicates those paths into `refused_paths`; other block reasons cannot carry them.
- The seat-visible Pi tool is `amq_task_block`. It sends every requested file as a separate `--path` argument.
- The lead's Pi tool and direct `task widen` path accept `outside_declared_files` and `widen_requested`, and widen only a subset of the recorded exact paths.
- A successful typed widen clears the block and returns the task to `pending` for a fresh scheduler dispatch.
- The pre-existing protected-path growth-gate retry remains intact for a harness report awaiting the owner's gate.
- Other block reasons retain the `task reset ID --pool RUNTIME:MODEL:EFFORT` refusal guidance.

Brief 31-3 PR 1/PR 2 and brief 31-4 compatibility: `amq_task_block` is seat-visible and therefore joins the registered Pi team list. The two-way phone still adds no lead-only tool, and no gate-answer or confirm tool is exposed.

## Reverting rows

| Change | Reverting evidence |
|---|---|
| Store records only typed normalized paths and rearms to pending | `TestBlockWithPathsRecordsAndRearmsTypedWidenRequest` |
| CLI block carries exact paths and refuses invalid shapes | `TestTaskCLIBlockCarriesTypedWidenPaths` |
| Direct lead widening is limited to the recorded exact subset | `TestLeadWidenOnBlockedTaskUsesOnlyItsRecordedExactPaths` |
| Existing protected-path owner-gate retry remains valid | `TestSeatCommitAndWidenThroughTheGrowthGate` |
| Pi block/widen argv and registered tool surfaces | `pi/test/tools.test.ts`, `pi/test/scope.test.ts`, `pi/test/phase.test.ts`, `pi/test/keys.test.ts` |
| Direct `typebox` import is installable from the lock | `npm ci --offline --ignore-scripts` |

## Verification

- `go test ./internal/task -run 'TestBlockWithPathsRecordsAndRearmsTypedWidenRequest|TestDoneWithDeclaredFilesWaitsForTheCommitStep' -count=1`: pass.
- `go test ./internal/cli -run 'TestTaskCLIBlockCarriesTypedWidenPaths|TestLeadWidenOnBlockedTaskUsesOnlyItsRecordedExactPaths|TestSeatCommitAndWidenThroughTheGrowthGate' -count=1`: pass.
- `npm ci --offline --ignore-scripts`: pass, 118 packages installed, 0 vulnerabilities.
- `npm run lint`: pass, 24 files against Pi 1.3.0.
- `npm test`: pass, 78 tests, 0 failures (run with local Unix socket permission).
- Full `go test ./internal/task`: pass. The full `internal/cli` package also reached the new growth-gate regression; unrelated release-domain fixtures failed under the sanitized test identity with `immutable temp identity invalid`, and one tmux identity assertion was environment-specific. Focused task and widening rows above pass.

## t28 reissue verification

- `go test ./internal/task ./internal/cli -run 'TestBlockWithPathsRecordsAndRearmsTypedWidenRequest|TestDoneWithDeclaredFilesWaitsForTheCommitStep|TestTaskCLIBlockCarriesTypedWidenPaths|TestLeadWidenOnBlockedTaskUsesOnlyItsRecordedExactPaths|TestSeatCommitAndWidenThroughTheGrowthGate' -count=1`, with the live seat identity removed from the test process: pass.
- `go test ./internal/task -count=1`, with the live seat identity removed from the test process: pass.
- `npm ci --offline --ignore-scripts`: pass, 118 packages installed, 0 vulnerabilities.
- `npm run lint`: pass, 24 files against Pi 1.3.0.
- `npm test`, with local Unix-socket permission: pass, 82 tests, 0 failures.
