# 3.0.2 t24 and t23: the binding check before the commit and the push; one branch per task

Brief: 3.0.2-commitstep (t24, t23), the reviewer rulings of 2026-10-30 and
the amendments 2b and 2c of 2026-10-31. Branch `v3-302-commitstep` from
`origin/v3` at 2f88eca1, one implementer, scratch worktree.

## The defects (2026-10-30, session v31)

- t24 (blocker): the scheduler's commit step on task t20 committed 6b9808a5
  on the seat branch seat/dev and pushed it (the commit ledger says
  pushed), and only then refused the binding with pr_bound_elsewhere,
  because seat/dev already carried PR #815, bound to task t12 at head
  c338d3fe. PR #815 then held both tasks' commits and its head had moved
  past the bound head: the merge train refused it (pr_head_moved, ci
  absent) and t12 could not land through the train.
- t23: one branch per seat (seat/<handle>) makes a seat's second task wait
  on its first task's open PR until the train merges it.

### Day-2 evidence

The facts above are the brief's. The brief forbids this branch from
reading the live session (`.agent-mail/`) or calling GitHub, so the raw
lines are not quoted here. The lead fills this slot with the output of
these read-only commands:

```
# the commit ledger lines of t12 and t20 (seat dev, session v31)
jq -c '.commits[] | select(.task == "t12" or .task == "t20") | {task, commit, parent, pushed}' \
  .agent-mail/v31/extensions/amq-squad/commits/dev.json
# PR #815's two commits
gh pr view 815 --repo omriariav/amq-squad --json headRefName,headRefOid,commits \
  --jq '{headRefName, headRefOid, commits: [.commits[] | {oid, messageHeadline}]}'
```

Expected shape: t12's ledger commit c338d3fe… pushed on seat/dev; t20's
6b9808a5… with parent c338d3fe… and pushed true; PR #815 on seat/dev with
both commits and head 6b9808a5….

## The change

1. **The binding check runs before the commit and the push (t24).** With
   PR binding on (the default under a merge grant), the commit step reads
   the task branch's open PRs through gh after the path checks and before
   the verifier, the commit and the push. An open PR that the store binds
   to another task, or to none, or that comes from another repository or
   targets another base, refuses pr_bound_elsewhere, routed to the lead.
   A gh failure there defers as pr_pending. Either way nothing is
   committed, pushed, anchored or written to the ledger, and the seat's
   diff waits, as with outside_declared_files. The check after the push
   stays as the guard against a PR opened between the two.
2. **One branch per task (t23).** Each task commits on
   `seat/<handle>-<task id>` (for example `seat/dev-t12`), beside the
   resting branch `seat/<handle>`. Git cannot hold `seat/<handle>` and a
   directory of task branches under it, so the separator is a hyphen. The
   name is a label only. Nothing parses it back into a handle and a task,
   because a handle can itself contain a hyphen. The ledger's per-task
   record (`lease`, and each commit's `branch`) is the only source of
   which task a branch carries. CleanSeat accepts a worktree on a task
   branch only when the ledger records that branch.
3. **One base (amendment 2b).** The base is a per-task fact recorded at
   dispatch. Its source is the base tip the launch recorded on the launch
   record (t22's `base` on the staged record). seatcommit reads that key
   from the record's JSON, so it needs no `launch.Record` field of its own.
   Only a record from before 3.0.2, which has no `base`, falls back to the
   seat repository's local base branch, and then to the ledger's resting
   commit. CleanSeat, between leases and never during a held lease, resets
   the worktree to that base and records it as the ledger's `rest`.
   `StartTask` runs from the scheduler's doorbell right after the grant. It
   first records the lease's start on the ledger (`lease`: task, branch,
   base, start), then moves the worktree onto the task's branch at that
   base, or at the task's own earlier commit on a later attempt. Each
   commit records its `branch` and `base`. This fixes the day-2 finding on
   this side of the seam: `recordBase` keeps the ledger of the same
   worktree and branch across a relaunch, and CleanSeat used to reset to
   that ledger's old parent, undoing the launch's move to the tip.
4. **The PR per task.** The step pushes the task branch and opens and
   binds its own draft PR. A later attempt appends to the same branch and
   binds the same PR again at the new head (amendment 2c).
5. **Back to a clean base.** After an OK step the worktree returns to
   `seat/<handle>` at the base. CleanSeat does the same at every lease
   boundary. Each commit is also anchored under
   `refs/amq-squad/commits/<seat>/<task>`, so a later attempt can start
   from it even if the local branch is gone.

## The seam with t22 (v3-302-seatbase)

This branch touches neither `internal/seatlaunch`, `internal/launch` nor
`ensureWorktree`. t22 puts the launch-time base on the launch record, and
this branch owns the ledger and the lease boundary. When both are merged,
`launchBase` in `internal/seatcommit/commit.go` can read `rec.Base`
instead of the raw JSON key; the value is the same. A launch that leaves
an existing worktree on a task branch is safe: CleanSeat at the next
boundary returns it, because the ledger records that branch.

## Rows

| Row | File | What it proves |
|---|---|---|
| TestBindingCheckRunsBeforeTheCommit (bound elsewhere) | internal/seatcommit/commitstep302_test.go | The v31 shape. The task branch carries open PR #815, which the store binds to another task at head c…c. The step refuses pr_bound_elsewhere naming #815 and "nothing is committed or pushed". There is no commit, no push to the origin, no anchor, no `pr create`, the ledger bytes are unchanged, and the other task's binding is unchanged. |
| TestBindingCheckRunsBeforeTheCommit (gh fails) | same | gh fails at the check: a pr_pending deferral, with the same nothing-written assertions. |
| TestTwoTasksOnOneSeatGetTwoBranchesAndTwoPRs | same | Two consecutive tasks on one seat. The leased worktree is on `seat/s1-t1`. After the step it is clean on `seat/s1`. The base moves on main. The second lease starts at the new tip. The two branches are pushed with PRs #1 and #2, each bound to its own task. The first commit's parent is the launch base, the second's is the new tip. The second branch does not contain the first commit. The ledger names the branch and base of each commit. |
| TestLaterAttemptAppendsToItsOwnBranchAndPR | same | A task with PR #1 from an earlier attempt is sent back (refused, reset) and leased again. StartTask puts the worktree at the earlier commit. The new commit's parent is that commit, on the same branch, pushed fast-forward. The same PR #1 is bound again at the new head, with one `pr create` in all. |
| TestTaskBranchIsALabelTheRecordDecides | internal/seatcommit/commitstep302_test.go | Seat `qa-2`, a hyphenated handle. The seat checks out a branch it made, `seat/qa-2-t9`, which only looks like a task branch. CleanSeat refuses it and touches nothing, because the ledger records no such branch. The leased task t1 is on `seat/qa-2-t1`, and its commit is recorded with that task and branch. |
| TestLeaseBoundaryResetsToTheLaunchRecordsBase | same | The day-2 shape. The ledger keeps its first base, the launch record carries a newer base, and the local main moved further after the launch. The lease boundary resets to the launch record's base. The task's commit takes it as parent and base, and the ledger's `rest` is that base. |
| TestDoorbellAndLeadNoticeSend | internal/schedproc/slots_test.go | The doorbell runs Start before the AMQ send. A Start error still rings the seat and is returned. |
| TestSchedulerCommitStepBindsByDefaultUnderAMergeGrant | internal/cli/scheduler_run_test.go | Through `scheduler run`, the worktree after dispatch is on `refs/heads/seat/s1-t1`. gh sees `pr list --head seat/s1-t1` and `pr create --draft --head seat/s1-t1`. t1 completes bound at the anchored commit. |

Updated rows, for the same reasons: the old branch name, and the worktree
back at rest after the step.

- TestCommitInsideDeclaredFilesCommitsAndPushes: the task branch on the
  origin, `seat/s1` at the base, the anchor at the commit, and the ledger's
  branch, base and rest.
- TestCommitStepOpensAndBindsThePR: the task branch in `pr create`.
- TestPushRejectedIsNeverForced: the diverged branch is the task branch.
- TestPRFailureDefersAndTheRetryKeepsTheCheckedDiff: gh answers the
  pre-check, then fails after the push.
- TestHTTPSPushUsesOnlyThePinnedGhCredentialHelper and
  TestPushGoesToTheGrantsURLNeverTheSeatsConfig: the task branch, and no
  branch at all on the seat's pushurl.
- TestVerifierModuleCacheTwoRunsOneFetch: the second task, starting from
  the base, writes its own module files.
- TestSeatCommitOpenPRBindsTheTask: the bound head is the anchored commit,
  and the worktree is back on `seat/s1`.
- The fixture's `originBranch` now reads every seat branch on the origin,
  so a "nothing pushed" assertion still catches a push to a task branch.

TestSecondTaskDiffsFromTheLastHarnessCommit is removed. It asserted the
3.0.1 rule (the second task's parent is the first task's commit), which
t23 reverses. TestTwoTasksOnOneSeatGetTwoBranchesAndTwoPRs replaces it.

No row reaches the network or the real HOME. The origin is a bare local
repository with local_push_ack, gh is a shell script through the `Gh` and
`seatCommitGh` seams, and the scheduler-run rows use the existing fake
launcher and sender.

## Revert checks

Each mutation was applied alone to the committed change, the named rows
were run, and the file was restored.

| Mutation | Row | Result |
|---|---|---|
| the pre-check never stops the step | TestBindingCheckRunsBeforeTheCommit | FAIL |
| CleanSeat keeps the resting commit instead of the base tip | TestTwoTasksOnOneSeatGetTwoBranchesAndTwoPRs | FAIL |
| StartTask ignores the task's earlier commit (a later attempt starts from the base) | TestLaterAttemptAppendsToItsOwnBranchAndPR | FAIL |
| the reused PR is not bound again | TestLaterAttemptAppendsToItsOwnBranchAndPR | FAIL |
| CleanSeat goes by the name's prefix | TestTaskBranchIsALabelTheRecordDecides | FAIL |
| the reset target is the local base branch, not the launch record's base | TestLeaseBoundaryResetsToTheLaunchRecordsBase | FAIL |
| the reset target is the ledger's parent (the day-2 code) | TestLeaseBoundaryResetsToTheLaunchRecordsBase, TestTwoTasksOnOneSeat… | FAIL |
| no return to rest after an OK step | TestTwoTasksOnOneSeat…, TestCommitInsideDeclaredFiles… | FAIL |
| the doorbell skips Start | TestDoorbellAndLeadNoticeSend | FAIL |
| `scheduler run` wires no Start | TestSchedulerCommitStepBindsByDefaultUnderAMergeGrant | FAIL |

## Known limits

- **Records from before 3.0.2.** A launch record without `base` falls
  back to the seat repository's local base branch. A same-UID seat can
  move that ref. t22's launch-time base, fetched from the grant's push
  URL, closes this for every 3.0.2 launch.
- **The base advances only at a launch.** CleanSeat resets to the base the
  last launch recorded. A seat that stays up across many tasks keeps that
  base until the next launch. That is the one-base rule as ruled.
- **A later attempt on another seat.** It gets a new branch
  `seat/<other>/<task>` and a new PR. The store's binding then moves to
  the new PR, and the old PR is left open.
- **A later attempt with no StartTask.** A manual claim, or a doorbell
  whose Start failed, leaves the worktree off the task's branch. The step
  refuses it (worktree reason) instead of committing a diff that does not
  follow the branch. A first attempt in that state commits from the
  resting commit as usual.
- **depends_on and the base.** A task that depends on another, still
  unmerged task starts from the base tip without that task's commit.
