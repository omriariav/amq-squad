# Known limit 35: a task branch at the fetched remote base

The clean now applies this rule:

    the clean treats a task-branch head as recorded when it is reachable from the remote base branch as fetched (or equals the launch record's base, or is a ledger commit); nothing is discarded; the refusal's words stay for a head the remote does not hold

The precheck ran with TIP and HEAD both at `f207c7d57189c97f254b16aeb69c0e1f95b080fa`; `git diff --stat f207c7d57189c97f254b16aeb69c0e1f95b080fa HEAD -- internal/seatcommit internal/launch internal/schedproc/slots.go docs/v3-followups.md docs/v3-spike/32x-seat-head-remote` printed nothing. The change is at `internal/seatcommit/commit.go:690-696`.

## What the clean reads

No ref of the seat repository names the fetched remote base branch. The fetch writes only the short-lived shadow ref and suppresses `FETCH_HEAD` at `internal/schedproc/slots.go:331-335`:

```go
specs := []string{"+refs/heads/" + base + ":refs/base"}
if refs["refs/heads/"+seat.Branch] != "" {
    specs = append(specs, "+refs/heads/"+seat.Branch+":refs/seat")
}
if _, err := fg.Run(sh.Dir, fenv, append([]string{"fetch", "-q", "--no-tags", "--no-write-fetch-head", "--", url}, specs...)...); err != nil {
```

The lease base is the tip fetched at the task's grant. `StartTask` selects it at `internal/seatcommit/commit.go:849-855`:

```go
rest := l.rest()
start := firstNonEmpty(strings.TrimSpace(base), rest)
base = start
if i := l.taskCommitAt(t.ID, branch); i >= 0 {
    c := l.Commits[i]
    start, base = c.Commit, firstNonEmpty(c.Base, c.Parent)
}
```

It seals that base before moving the branch at `internal/seatcommit/commit.go:856-863`:

```go
// The lease's start is recorded first, so the branch the worktree is
// moved to below is always one the ledger records (CleanSeat trusts
// the record, never the name).
if l.Lease == nil {
    l.Lease = &LedgerLease{Task: t.ID, Branch: branch, Base: base, Start: start, At: now.UTC()}
    if err := writeLedger(root, l, "start task"); err != nil {
        return err
    }
}
```

`cleanSeat` asks the seat repository's object store whether the worktree head is the lease base or its ancestor. A failed ancestry query records nothing and leaves the existing refusal in force.

## What it does not cover

- A worktree off its resting branch with no lease on the ledger keeps the old check.
- A head the remote holds now but the tip fetched at the grant does not is still refused; the clean makes no network call.
- When a grant names no push URL, the fetched tip is the repository's local base branch (`internal/schedproc/slots.go:290-296`).
- The lease base is read whatever branch HEAD is on.
- The fix makes no network call; that is shown by reading the change, not by a row.
- A lease base that is not a commit in the object store leaves the refusal in force; no driver row covers that case.
- A worktree with no lease is covered by the package's existing detached-HEAD test, not by this driver.
- No row reverts the discard record, the return to rest, or anything else after the refusal.

## The tests

The seven tests share a ground where B1 and B2 are in the seat repository's object store, origin `main` is B2, local `main` remains B0, and no ref of the seat repository names B1 or B2.

- `TestCleanSeatReturnsATaskBranchAtTheFetchedRemoteTip` builds a task branch at B2 and asserts a clean return to B0 with no discard.
- `TestCleanSeatReturnsATaskBranchAtAnEarlierCommitOfTheFetchedRemoteBranch` moves the task branch to B1 and asserts a discard whose parent is B2 and whose tree is B1's tree.
- `TestCleanSeatRefusesALocalCommitOnATaskBranchCutFromAMovedRemoteTip` commits on top of B2 and asserts the whole refusal, unchanged HEAD, branch, file, lease, and discard ledger.
- `TestCleanSeatRefusesACommitOnlyTheLocalBaseBranchHolds` moves the task branch to an unpushed local `main` commit and asserts the whole refusal without moving HEAD or its branch.
- `TestCleanSeatReturnsATaskBranchAtTheLaunchBase` starts at B0 and asserts the existing no-discard return to rest.
- `TestCleanSeatKeepsALeftoverDiffOnATaskBranchAtTheFetchedRemoteTip` edits the B2 worktree and asserts the discard parent, kept file, evidence ref, and tree.
- `TestCleanSeatRefusesAnUnrecordedCommitWhenTheLeaseHasNoBase` empties the sealed lease base and asserts the whole refusal, unchanged clean worktree, surviving empty-base lease, and no discard.

## Rows

The three rows came from one final `all` invocation. Each section records that command and the driver's complete output for the row.

### R0

```console
$ bash ../../../../amq-squad-lead/T/l35/inputs/rows-l35.sh all
=== row R0 ===
edit: none (your file)
$ go test ./internal/seatcommit/ -count=1 -timeout 240s -v -run '^(TestCleanSeatReturnsATaskBranchAtTheFetchedRemoteTip|TestCleanSeatReturnsATaskBranchAtAnEarlierCommitOfTheFetchedRemoteBranch|TestCleanSeatRefusesALocalCommitOnATaskBranchCutFromAMovedRemoteTip|TestCleanSeatRefusesACommitOnlyTheLocalBaseBranchHolds|TestCleanSeatReturnsATaskBranchAtTheLaunchBase|TestCleanSeatKeepsALeftoverDiffOnATaskBranchAtTheFetchedRemoteTip|TestCleanSeatRefusesAnUnrecordedCommitWhenTheLeaseHasNoBase|TestCleanSeatRefusesAnUnrecordedCommitOnTheCancelledTaskBranch)$'
exit: 0
--- PASS: TestCleanSeatRefusesAnUnrecordedCommitOnTheCancelledTaskBranch
--- PASS: TestCleanSeatReturnsATaskBranchAtTheFetchedRemoteTip
--- PASS: TestCleanSeatReturnsATaskBranchAtAnEarlierCommitOfTheFetchedRemoteBranch
--- PASS: TestCleanSeatRefusesALocalCommitOnATaskBranchCutFromAMovedRemoteTip
--- PASS: TestCleanSeatRefusesACommitOnlyTheLocalBaseBranchHolds
--- PASS: TestCleanSeatReturnsATaskBranchAtTheLaunchBase
--- PASS: TestCleanSeatKeepsALeftoverDiffOnATaskBranchAtTheFetchedRemoteTip
--- PASS: TestCleanSeatRefusesAnUnrecordedCommitWhenTheLeaseHasNoBase
pass 8 fail 0 skip 0
messages (commit ids printed as <sha>; the texts are your tests'):
restore: cmp exit 0
```

### R1

```console
$ bash ../../../../amq-squad-lead/T/l35/inputs/rows-l35.sh all
=== row R1 ===
edit: internal/seatcommit/commit.go is the TIP's (f207c7d57189c97f254b16aeb69c0e1f95b080fa), whole
$ go test ./internal/seatcommit/ -count=1 -timeout 240s -v -run '^(TestCleanSeatReturnsATaskBranchAtTheFetchedRemoteTip|TestCleanSeatReturnsATaskBranchAtAnEarlierCommitOfTheFetchedRemoteBranch|TestCleanSeatRefusesALocalCommitOnATaskBranchCutFromAMovedRemoteTip|TestCleanSeatRefusesACommitOnlyTheLocalBaseBranchHolds|TestCleanSeatReturnsATaskBranchAtTheLaunchBase|TestCleanSeatKeepsALeftoverDiffOnATaskBranchAtTheFetchedRemoteTip|TestCleanSeatRefusesAnUnrecordedCommitWhenTheLeaseHasNoBase|TestCleanSeatRefusesAnUnrecordedCommitOnTheCancelledTaskBranch)$'
exit: 1
--- PASS: TestCleanSeatRefusesAnUnrecordedCommitOnTheCancelledTaskBranch
--- FAIL: TestCleanSeatReturnsATaskBranchAtTheFetchedRemoteTip
--- FAIL: TestCleanSeatReturnsATaskBranchAtAnEarlierCommitOfTheFetchedRemoteBranch
--- PASS: TestCleanSeatRefusesALocalCommitOnATaskBranchCutFromAMovedRemoteTip
--- PASS: TestCleanSeatRefusesACommitOnlyTheLocalBaseBranchHolds
--- PASS: TestCleanSeatReturnsATaskBranchAtTheLaunchBase
--- FAIL: TestCleanSeatKeepsALeftoverDiffOnATaskBranchAtTheFetchedRemoteTip
--- PASS: TestCleanSeatRefusesAnUnrecordedCommitWhenTheLeaseHasNoBase
pass 5 fail 3 skip 0
messages (commit ids printed as <sha>; the texts are your tests'):
    commitstep302_test.go:376: clean: discarded=false want false err=seat_head_detached_unmerged: seat s1: the worktree's branch seat/s1-t1 holds an unrecorded commit <sha>; the harness never discards a local commit
    commitstep302_test.go:388: clean: discarded=false want true err=seat_head_detached_unmerged: seat s1: the worktree's branch seat/s1-t1 holds an unrecorded commit <sha>; the harness never discards a local commit
    commitstep302_test.go:477: clean: discarded=false want true err=seat_head_detached_unmerged: seat s1: the worktree's branch seat/s1-t1 holds an unrecorded commit <sha>; the harness never discards a local commit
restore: cmp exit 0
```

### R2

```console
$ bash ../../../../amq-squad-lead/T/l35/inputs/rows-l35.sh all
=== row R2 ===
edit: internal/seatcommit/commit.go is the TIP's (f207c7d57189c97f254b16aeb69c0e1f95b080fa), then one line:
edit: internal/seatcommit/commit.go:694
  before: if ahead != "" && !recorded {
  after:  if false && ahead != "" && !recorded {
$ go test ./internal/seatcommit/ -count=1 -timeout 240s -v -run '^(TestCleanSeatReturnsATaskBranchAtTheFetchedRemoteTip|TestCleanSeatReturnsATaskBranchAtAnEarlierCommitOfTheFetchedRemoteBranch|TestCleanSeatRefusesALocalCommitOnATaskBranchCutFromAMovedRemoteTip|TestCleanSeatRefusesACommitOnlyTheLocalBaseBranchHolds|TestCleanSeatReturnsATaskBranchAtTheLaunchBase|TestCleanSeatKeepsALeftoverDiffOnATaskBranchAtTheFetchedRemoteTip|TestCleanSeatRefusesAnUnrecordedCommitWhenTheLeaseHasNoBase|TestCleanSeatRefusesAnUnrecordedCommitOnTheCancelledTaskBranch)$'
exit: 1
--- FAIL: TestCleanSeatRefusesAnUnrecordedCommitOnTheCancelledTaskBranch
--- PASS: TestCleanSeatReturnsATaskBranchAtTheFetchedRemoteTip
--- PASS: TestCleanSeatReturnsATaskBranchAtAnEarlierCommitOfTheFetchedRemoteBranch
--- FAIL: TestCleanSeatRefusesALocalCommitOnATaskBranchCutFromAMovedRemoteTip
--- FAIL: TestCleanSeatRefusesACommitOnlyTheLocalBaseBranchHolds
--- PASS: TestCleanSeatReturnsATaskBranchAtTheLaunchBase
--- PASS: TestCleanSeatKeepsALeftoverDiffOnATaskBranchAtTheFetchedRemoteTip
--- FAIL: TestCleanSeatRefusesAnUnrecordedCommitWhenTheLeaseHasNoBase
pass 4 fail 4 skip 0
messages (commit ids printed as <sha>; the texts are your tests'):
    commitstep302_test.go:299: an unrecorded task-branch commit was cleaned: <nil>
    commitstep302_test.go:411: local task commit refusal = <nil>, want "seat_head_detached_unmerged: seat s1: the worktree's branch seat/s1-t1 holds an unrecorded commit <sha>; the harness never discards a local commit"
    commitstep302_test.go:443: local base commit refusal = <nil>, want "seat_head_detached_unmerged: seat s1: the worktree's branch seat/s1-t1 holds an unrecorded commit <sha>; the harness never discards a local commit"
    commitstep302_test.go:512: empty-base refusal = <nil>, want "seat_head_detached_unmerged: seat s1: the worktree's branch seat/s1-t1 holds an unrecorded commit <sha>; the harness never discards a local commit"
restore: cmp exit 0
keep directory removed: yes
```

## What ran

### `go build ./...`

```console
$ go build ./...
exit 0
```

### `go vet ./internal/seatcommit/`

```console
$ go vet ./internal/seatcommit/
exit 0
```

### `gofmt -l internal/seatcommit`

```console
$ gofmt -l internal/seatcommit
exit 0
```

### `go test ./internal/seatcommit/ -count=1 -timeout 420s`

```console
$ go test ./internal/seatcommit/ -count=1 -timeout 420s
ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	133.251s
exit 0
```

### `go test ./internal/schedproc/ -count=1 -timeout 240s`

```console
$ go test ./internal/schedproc/ -count=1 -timeout 240s
ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	28.388s
exit 0
```

### `bash ../../../../amq-squad-lead/T/l35/inputs/rows-l35.sh all`

```console
$ bash ../../../../amq-squad-lead/T/l35/inputs/rows-l35.sh all
keep directory removed: yes
exit 0
```

### `go run ./scripts/redact-evidence -check docs/v3-spike/32x-seat-head-remote/`

```console
$ go run ./scripts/redact-evidence -check docs/v3-spike/32x-seat-head-remote/
exit 0
```

### `git diff --check`

```console
$ git diff --check
exit 0
```

### `git diff --no-index --check /dev/null docs/v3-spike/32x-seat-head-remote/README.md`

```console
$ git diff --no-index --check /dev/null docs/v3-spike/32x-seat-head-remote/README.md
exit 1
```

### `grep -c '/Us''ers/' docs/v3-spike/32x-seat-head-remote/README.md`

```console
$ grep -c '/Us''ers/' docs/v3-spike/32x-seat-head-remote/README.md
0
exit 1
```

### `grep -cE '[[:space:]]+$' docs/v3-spike/32x-seat-head-remote/README.md`

```console
$ grep -cE '[[:space:]]+$' docs/v3-spike/32x-seat-head-remote/README.md
0
exit 1
```

### `grep -cE '[[:space:]]+$' internal/seatcommit/commitstep302_test.go`

```console
$ grep -cE '[[:space:]]+$' internal/seatcommit/commitstep302_test.go
0
exit 1
```

### `git status --porcelain --untracked-files=all`

```console
$ git status --porcelain --untracked-files=all
 M docs/v3-followups.md
 M internal/seatcommit/commit.go
 M internal/seatcommit/commitstep302_test.go
?? docs/v3-spike/32x-seat-head-remote/README.md
exit 0
```

### `git diff --stat`

```console
$ git diff --stat
 docs/v3-followups.md                      |  12 +-
 internal/seatcommit/commit.go             |   7 +
 internal/seatcommit/commitstep302_test.go | 223 ++++++++++++++++++++++++++++++
 3 files changed, 237 insertions(+), 5 deletions(-)
exit 0
```
