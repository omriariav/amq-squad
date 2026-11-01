# t34: resolved paths on every widen branch

Base: `2ca2f88b` (#847 merged).

## Result

- Every exact-looking widen entry resolves before policy and persistence on
  nonblocked, protected-report, growth-gate-report, and typed-request paths.
  Real glob patterns keep their existing behavior.
- Growth-gate records carry `<resolved path> via link <alias>` below the rule.
  No path is added to the plain question above the rule.
- Approval binds the resolved file target. A different alias to that same
  file intentionally reuses the approval because the owner approves a file,
  not a spelling.
- The standalone protected-added row uses a temporary HOME and a path that is
  not implicitly protected. Added rows cover retarget-at-commit, alias reuse,
  unreadable resolution, and NFC folding of a managed name.

## Reverting rows

- `TestEveryWidenBranchRoutesLiteralPathsThroughResolution`
- `TestResolvedPathLinkIsOnlyBelowTheGateRule`
- `TestGrowthApprovalBindsTheResolvedFileAcrossAliases`
- `TestCommitRefusesARetargetedLinkInsteadOfFollowingItsWidenedTarget`
- `TestResolveExactFilesFollowsLinksAndRefusesUnsafeTargets`
- `TestResolvedProtectedWidenGateNamesTheTarget`

## Verification

- `go test ./internal/task ./internal/cli ./internal/seatcommit ./internal/gatestore -count=1`, with live AMQ variables removed: pass.
- `go test ./internal/gateverify -count=1`: pass.
- `git diff --check`: pass.
