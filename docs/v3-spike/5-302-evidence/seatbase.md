# 3.0.2 t22: a seat's base is the base branch's tip at every launch

Branch `v3-302-seatbase`, from `origin/v3` at 2f88eca1. Reviewer ruling
2026-10-30.

## The finding (session v31, on 3.0.1)

The lead fast-forwarded the seat worktrees to the v3 tip before the
roster launch. The seats' harness commits still landed on old bases:

| commit | PR | seat | parent | what the parent is |
|---|---|---|---|---|
| c338d3fe | #815 | dev | a203f183 | the dev seat's t5 commit, itself on a1a67151 |
| e9e6c53b | #816 | reviewer | a1a67151 | the commit ledger's first base, two days of v3 behind |

v3 was at 2f88eca1. Both PRs sit on stale bases, and #816 conflicts with
the tip.

## Which code reset the base

Not `ensureWorktree` (internal/schedproc/slots.go): it left an existing
worktree untouched. Not the launch's recorded base in internal/seatlaunch
either: run.go records the worktree's head through
`seatcommit.RecordWorktreeBase`. The reset is the commit ledger plus
CleanSeat, both in internal/seatcommit:

1. internal/seatcommit/ledger.go:539-543, `recordBase`, keeps an existing
   ledger for the same worktree and branch. A relaunch therefore keeps the
   first launch's `Base` (a1a67151) and the last harness commit as `Head`
   (a203f183 for dev). The lead's fast-forward is never recorded.
2. internal/seatcommit/commit.go:586, `cleanSeat`, takes
   `parent := l.Parent()` (Head, else Base). At the first lease after the
   launch, the fast-forwarded worktree differs from that parent, so
   commit.go:624 (`read-tree -u --reset parent`) and commit.go:633
   (`update-ref refs/heads/<branch> parent`) move the files and the branch
   back. The seat then works, and the commit step commits, on the old
   parent.

## The change on this branch

- `ensureWorktree`, on the on-demand and the roster launch, takes the base
  branch's tip from the grant's push URL. The fetch runs in a shadow git
  directory on the repository's object store, so the shared repository's
  config, remotes and url rewrites are never read. Without a push URL in
  the grant, the local base branch is the tip.
- The resting branch `seat/<handle>` is reset to the tip locally. An
  existing worktree moves to the tip by a two-tree read-tree from its HEAD
  in the shadow, and its HEAD names the resting branch again. A local
  change the move would overwrite refuses the launch and stays in the
  worktree.
- A remote seat branch ahead of the tip and unmerged refuses the launch
  with `seat_branch_unmerged`, naming the branch and up to 11 commits. The
  harness never force-pushes.
- The launch record carries the base: `launch.Record.Base` (json `base`),
  copied from `seatlaunch.Seat.Base`, which the seat file cannot set.

Under this change the day-2 relaunch would have refused for dev and
reviewer: origin/seat/dev holds c338d3fe and origin/seat/reviewer holds
e9e6c53b, both unmerged. The lead decides, by merging, closing or deleting
them.

## One base, and the seam for the commit step (t23, t24)

Reviewer amendment 2026-10-31: the base is a per-task fact recorded at
dispatch, the base branch's tip when the task is dispatched. The launch and
the roster launch put the worktree on that tip, and CleanSeat between
leases resets to the tip, never during a held lease.

This branch does the launch half. The launch-time base is recorded on the
launch record only, as `launch.Record.Base` (json `base`), in the staged
record that `seatcommit.stagedRecord` already reads. No commit-ledger field
is added here. The ledger's per-task base and CleanSeat's reset target are
in internal/seatcommit, which the commit-step branch owns. CleanSeat
already runs only between leases: it reserves the seat under the task
store's lock. Until the commit-step change lands, CleanSeat at the first
lease still resets a relaunched seat to its old ledger parent.

## Rows

internal/schedproc/seatbase_test.go, each run against a fake remote (a bare
repository in the temp dir) as the grant's push URL:

| row | proves | fails when reverted |
|---|---|---|
| TestSeatBaseRelaunchAtTheTip | a relaunched seat with an old base and its own commit, left on another branch, gets the worktree, `seat/c1` and HEAD at the fetched tip through the roster launch; the next commit's parent is the tip; the compiled launch record's base is the tip | existing worktree left alone; local base in place of the fetch; record without the base; no HEAD switch |
| TestSeatBaseLocalBranchWithoutPushURL | without a push URL, a new worktree on a stale seat branch starts at the local base tip | no branch reset before the worktree is added |
| TestSeatBaseRemoteSeatBranchAheadRefuses | a remote seat branch with an unmerged commit refuses by name and moves nothing; once merged, the launch proceeds at the tip | no unmerged check |
| TestSeatBaseConflictingLocalChangeRefuses | a local change the move would overwrite refuses the launch and is kept | a plain reset in place of the two-tree merge |

Not on this branch: the row "CleanSeat after a lease resets to the tip". It
needs the seatcommit change above and belongs with t23 and t24.
