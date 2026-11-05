# t33: resolved typed-widen paths

Base: `0cca462e` (#846 merged). This follow-up makes a typed widen operate on
the real repository file, never its symlink alias.

## Result

- Exact paths resolve symlink chains and symlinked parents. Widening declares
  the resolved repository-relative target; outside, dangling, cyclic,
  unreadable, and existing-directory targets are refused by alias and target.
- Protected and managed-name checks run on the resolved target. Growth-gate
  targets therefore name the real file, while the decision record retains the
  alias-to-target mapping.
- Protected-path and managed-name comparisons use NFC normalization and
  Unicode case folding.
- Empty exact-path entries are invalid, and journal overlays validate
  `refused_paths` before returning task records.

## Reverting rows

- `TestResolveExactFilesFollowsLinksAndRefusesUnsafeTargets`
- `TestResolvedProtectedWidenGateNamesTheTarget`
- `TestTypedWidenResolvesASymlinkIntoAProtectedPath`
- `TestCleanExactFilesSeparatesFileNamesFromGlobsAndDirectories`
- `TestSnapshotRefusesInvalidRefusedPathsInJournal`

## Verification

- `go test ./internal/task ./internal/seatcommit ./internal/cli -count=1`,
  with live AMQ launch variables removed from the test process: pass.
- `git diff --check`: pass.
