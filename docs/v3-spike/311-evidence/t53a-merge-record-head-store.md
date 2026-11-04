# t53a merge-record-head store evidence

## Acceptance rows

1. RecordPRMerge refuses a merge at another head: `TestBoundDependencyWaitsForItsRecordedMerge` (`internal/task/bind_test.go`) reports "merge at another head was accepted" when the `t.PRHead != head` clause is reverted.
2. WaitDependencies waits when the stored head differs from the current PRHead: `TestBoundDependencyWaitsForItsRecordedMerge` (`internal/task/bind_test.go`) reports "a merge fact for a stale head released the dependent" when the head comparison in `internal/task/lease.go` is reverted.
3. Idempotence, an identical fact does not rewrite the task: `TestBoundDependencyWaitsForItsRecordedMerge` (`internal/task/bind_test.go`) reports "identical merge fact rewrote the task" when the early return is reverted.
4. A changed binding clears the fact: `TestBoundDependencyWaitsForItsRecordedMerge` (`internal/task/bind_test.go`) reports "a changed binding retained stale merge proof" when the clear in `bindPR` is reverted.
5. A PR-less dependency still releases on completion: existing `TestDependenciesGateTheLease` (`internal/task/task_test.go`) is unchanged and passes.
6. Callers pass the merged head and one train merge records it: extended `TestRealRunReadsTheTaskBoundToThePR` (`internal/mergetrain/fix2_test.go`) checks `PRMergeHead` and `PRMergeCommit` after the existing fake-forge merge.
7. A dependency whose legacy merge record has a commit and no head releases its dependent: `TestLegacyCommitOnlyMergeRecordReleasesDependency` (`internal/task/bind_test.go`) reports "legacy commit-only merge did not release dependent" when that acceptance is reverted.

## Recorded revert runs

All commands ran with every live `AMQ_*` seat variable removed, `HOME=/private/tmp/amq-squad-reviewer-home`, `GOCACHE=/private/tmp/amq-squad-reviewer-go-cache`, `GOPATH=/Users/omri/go`, and `GOMODCACHE=/Users/omri/go/pkg/mod`.

1. Command: `go test ./internal/task -run TestBoundDependencyWaitsForItsRecordedMerge -count=1`. Reverted line: `if !strings.EqualFold(t.PRRepository, repository) || t.PRNumber != pr || t.PRHead != head {` to old text `if !strings.EqualFold(t.PRRepository, repository) || t.PRNumber != pr {`. First failure: `bind_test.go:75: merge at another head was accepted: record PR merge: task t1 already records head aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`.
2. Command: `go test ./internal/task -run TestBoundDependencyWaitsForItsRecordedMerge -count=1`. Reverted line: `if dep.PRMergeCommit == "" || dep.PRMergeHead != "" && dep.PRMergeHead != dep.PRHead {` to old text `if dep.PRMergeCommit == "" {`. First failure: `bind_test.go:97: a merge fact for a stale head released the dependent: ""`.
3. Command: `go test ./internal/task -run TestBoundDependencyWaitsForItsRecordedMerge -count=1`. Reverted lines: removed the early return `if t.PRMergeHead == head && t.PRMergeCommit == commit { out = *t; return nil }`. First failure: `bind_test.go:72: identical merge fact rewrote the task: <nil> {Schema:1 ID:t1 ... UpdatedAt:2026-10-24 12:00:02 +0000 UTC ...}`.
4. Command: `go test ./internal/task -run TestBoundDependencyWaitsForItsRecordedMerge -count=1`. Reverted line: `t.PRMergeHead, t.PRMergeCommit = "", ""` to old text `t.PRMergeCommit = ""`. First failure: `bind_test.go:88: a changed binding retained stale merge proof: {Schema:1 ID:t1 ... PRHead:dddddddddddddddddddddddddddddddddddddddd PRMergeHead:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa PRMergeCommit: ...}`.
5. Command: `go test ./internal/task -run TestDependenciesGateTheLease -count=1`. The unchanged existing row passed; no production line was reverted because this is the compatibility pin.
6. Command: `go test ./internal/mergetrain -run TestRealRunReadsTheTaskBoundToThePR -count=1`. Reverted line: `t.recordTaskMerge(it.Task, req.Repository, p.Number, p.Head, sha)` to old text `t.recordTaskMerge(it.Task, req.Repository, p.Number, "", sha)`. First failure: `fix2_test.go:91: binding_failed: record task t1 merge e84006f408a1: record PR merge: need owner/name, a PR number and full lowercase head and merge SHAs (got "owner/fixture" #1 "" "e84006f408a1e6b6c5c26a0c48c1c840cbe4304b")`.
7. Command: `go test ./internal/task -run TestLegacyCommitOnlyMergeRecordReleasesDependency -count=1`. Reverted line: `if dep.PRMergeCommit == "" || dep.PRMergeHead != "" && dep.PRMergeHead != dep.PRHead {` to old text `if dep.PRMergeCommit == "" || dep.PRMergeHead != dep.PRHead {`. First failure: `bind_test.go:116: legacy commit-only merge did not release dependent: "waiting for t1's PR #7 to merge"`.

Migration audit: legacy merge record accepted; merge head was not recorded. The legacy test emits the same audit text with `t.Log`.
