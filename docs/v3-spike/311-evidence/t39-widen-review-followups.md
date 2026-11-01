# t39 widen and tree-link review follow-ups

Base: `eeb56c9a` (#848, #850, and #851 merged).

## Result

- Widen entries are normalized with the store's declared-file rules before
  exact-path detection. `./source-link` resolves and persists its destination,
  while a real glob such as `[s]ource-link` remains unresolved.
- The four widen states exercise `applyLeadWiden` with real task records and
  the report branches' staged seat evidence.
- Gate facts retain valid resolved-path pairs up to their limits and record
  `and N more` below the rule for pairs omitted by the pair or byte bound.
- The retarget row records the original alias widening before changing the
  link. The approval row creates two aliases and proves both bind to the same
  resolved-file target.
- The static tree-link row rejects filesystem imports in `treelink.go` and
  checks both production calls pass the object source without a filesystem
  call in the same statement.

## Verification

- `go test ./internal/task ./internal/cli ./internal/seatcommit ./internal/gatestore ./internal/gateverify -count=1`: pass with live AMQ seat variables removed.
- `go test ./internal/cli -run TestEveryWidenBranchRoutesLiteralPathsThroughResolution -count=1`: pass with live AMQ seat variables removed.
- `git diff --check`: pass.
