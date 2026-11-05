# t32: exact typed-widen paths

Base: `ce40a051` (#845 merged). This is part 2 of the t29 split and contains
only exact-path validation, persisted-record validation, and symlink-aware
protected-path checks.

## Result

- Typed `refused_paths` use an exact-file validator. Directory spellings,
  traversal, absolute paths, backslashes, and noncanonical persisted names
  are refused; glob metacharacters remain literal filename characters.
- Every task-store load validates `refused_paths` before returning a record.
- Before the existing widen policy runs, exact paths are resolved through the
  repository. A symlink into a protected path adds its literal alias to the
  protected set, so the existing protected-path growth gate refuses it.

## Reverting rows

- `TestCleanExactFilesSeparatesFileNamesFromGlobsAndDirectories`
- `TestBlockWithPathsRecordsAndRearmsTypedWidenRequest`
- `TestLoadRefusesInvalidRefusedPathsByName`
- `TestTaskCLIBlockCarriesTypedWidenPaths`
- `TestTypedWidenResolvesASymlinkIntoAProtectedPath`

## Verification

- `go test ./internal/task ./internal/cli`, with live AMQ launch variables
  removed from the test process: pass.
- `git diff --check`: pass.
