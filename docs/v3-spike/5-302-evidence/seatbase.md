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
- An HTTPS push URL fetches with the commit step's push authentication
  (ruling 2026-10-31): `seatcommit.FetchCredentials`
  (internal/seatcommit/fetchcred.go) gives the pinned gh the launch
  recorded, re-hashed now, as the only credential helper on ls-remote and
  fetch, with GH_TOKEN and GITHUB_TOKEN the one pass-through, so a private
  repository launches. An HTTPS URL with no pinned gh refuses with
  `tool_unpinned` (registry.PinMissing) and never fetches anonymously. SSH
  and local URLs are unchanged. pushCredentials is left as it is; making it
  call the shared helper is a 3.1 follow-up.
- The resting branch `seat/<handle>` is reset to the tip locally. An
  existing worktree moves to the tip by a two-tree read-tree from its HEAD
  in the shadow, and its HEAD names the resting branch again. A local
  change the move would overwrite refuses the launch and stays in the
  worktree.
- A remote seat branch ahead of the tip and unmerged refuses the launch
  with `seat_branch_unmerged`, naming the branch and up to 11 commits. The
  harness never force-pushes.
- Codex round on fe52c154: a local resting branch holding commits the tip
  does not refuses with `seat_branch_unpushed` (the harness never discards
  a local commit), and both update-refs of the branch pass the expected old
  value, so a concurrent move refuses. An existing worktree whose HEAD
  names no commit refuses with `seat_head_unreadable`; the move is only the
  two-tree read-tree, never a reset. A plain clone (a `.git` directory) is
  moved like a linked worktree. The fetched base must be the object id
  ls-remote reported (`base_tip_moved` otherwise). The fetch's git runs
  with the push's environment through `seatcommit.HarnessEnv`.
- Codex round on 14697996: a worktree HEAD at a commit the tip does not
  hold, other than the resting branch's checked value, refuses with
  `seat_head_detached_unmerged` before any move. An existing worktree's
  `.git` must lead to its own git dir: a symlink, or a gitdir file naming
  anything but `<common>/worktrees/<name>` of the project repository with a
  back-pointer to this worktree, refuses with `seat_gitdir_foreign`. The
  schedproc fixture sets a temp HOME and git's config variables for the
  whole test process, so the code under test sees them.
- The launch record carries the base: `launch.Record.Base` (json `base`),
  copied from `seatlaunch.Seat.Base`, which the seat file cannot set.

On the real repository, origin `seat/dev` carries c338d3fe and 6b9808a5
and origin `seat/reviewer` carries e9e6c53b, none of them on the tip, so
`seat_branch_unmerged` fires on the first 3.0.2 launch until the owner
deletes those remote branches; that refusal is expected and is the row's
live evidence. The local branches hold the same commits (seat/dev at
6b9808a5 with c338d3fe, seat/reviewer at e9e6c53b), so after the remote
ones go, `seat_branch_unpushed` fires until the local branches are deleted
too.

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

internal/schedproc/seatbase_test.go. No row reaches the network: the first
four use a local bare repository in the temp dir as the push URL (a local
path, never a real URL); the two HTTPS rows run the real URL shape only
through a fake git that records the argv of ls-remote and fetch and answers
them itself, with a fake pinned gh, as the commit step's push rows fake the
push. Those two rows are the ones that cover the HTTPS path.

| row | proves | fails when reverted |
|---|---|---|
| TestSeatBaseRelaunchAtTheTip | a relaunched seat with an old base and its own commit (pushed and merged), left on another branch, lands at the fetched tip through the roster launch; the next commit's parent is the tip; the marshalled launch record carries `"base"` with the tip | existing worktree left alone; local base in place of the fetch; record without the base; JSON tag changed; no HEAD switch |
| TestSeatBaseLocalBranchWithoutPushURL | without a push URL, a new worktree on a stale seat branch starts at the local base tip | no branch reset before the worktree is added |
| TestSeatBaseRemoteSeatBranchAheadRefuses | an unmerged remote seat branch refuses by name and moves nothing; once merged, the launch proceeds | no unmerged check |
| TestSeatBaseConflictingLocalChangeRefuses | a local change the move would overwrite refuses and is kept | a reset in place of the two-tree merge |
| TestSeatBaseLocalUnpushedCommitRefuses | a local-only commit on seat/c1, remote seat branch absent, refuses with `seat_branch_unpushed`; the commit and its file survive | no unpushed check |
| TestSeatBaseConcurrentBranchMoveRefuses | a branch moved (existing worktree) or created (new worktree) mid-launch refuses; the concurrent value stays | no expected old value on either update-ref |
| TestSeatBaseUnbornHeadRefuses | a worktree whose HEAD names nothing refuses with `seat_head_unreadable`; an uncommitted change survives | the unreadable HEAD falling through to a reset |
| TestSeatBasePlainCloneWorktree | a plain clone as the seat worktree lands on the tip with HEAD on seat/c1 | a gitdir file required |
| TestSeatBaseHTTPSFetchUsesThePinnedGh | an HTTPS URL's ls-remote and fetch run with the pinned gh as the only helper and GH_TOKEN; the environment is the push's list (GIT_SSH_VARIANT in, GIT_SSH_EXTRA out) with the fixture's temp HOME | no FetchCredentials call; helper args not passed; no GH_TOKEN; a GIT_SSH* prefix; the fixture's HOME not set for the process |
| TestSeatBaseHTTPSWithoutPinnedGhRefuses | no pinned gh refuses with `tool_unpinned`; no fetch runs | no FetchCredentials call; FetchCredentials returning nothing for HTTPS |
| TestSeatBaseHTTPSPinMismatchRefuses | a pinned gh failing its sha256 refuses with `tool_pin_mismatch`; no fetch runs | no FetchCredentials call |
| TestSeatBaseShadowRemoved | the throwaway directories (the launch's and AddWorktree's) are gone after a launch, a moving relaunch, a refusal in the move and one after the fetch | any of the three left |
| TestSeatBaseDetachedLocalCommitRefuses | a HEAD detached at a local-only commit refuses with `seat_head_detached_unmerged`; the commit and its file survive | no HEAD containment check |
| TestSeatBaseForeignGitdirRefuses | a .git symlink, a file naming another worktree's git dir, a forged git dir outside worktrees/ and another repository's worktree with a forged back-pointer each refuse with `seat_gitdir_foreign`; the checkouts they lead to are untouched | no symlink check; no back-pointer check; no worktrees/ check; no project repository check |
| TestSeatBaseFetchedTipMustMatchLsRemote | a fetched base other than ls-remote's id refuses with `base_tip_moved`; no worktree | no comparison |

Not on this branch: the row "CleanSeat after a lease resets to the tip". It
needs the seatcommit change above and belongs with t23 and t24.
