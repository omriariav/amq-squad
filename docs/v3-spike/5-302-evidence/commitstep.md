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
   `seat/<handle>/<task id>`, and the ledger records the branch and the
   base on each commit. The base is the base branch's tip at the task's
   dispatch (amendment 2b): CleanSeat reads it at the lease boundary and
   records it as the ledger's `rest`. `StartTask`, run by the scheduler's
   doorbell right after the grant, records the lease's start (`lease`:
   task, branch, base, start). It moves the worktree onto the task's
   branch at that tip, or at the task's own earlier commit on a later
   attempt. The step pushes the task branch and opens and binds its own
   draft PR. A later attempt appends to the same branch and binds the same
   PR again at the new head (amendment 2c).
3. **Back to a clean base.** After an OK step the worktree returns to the
   resting branch `seat/<handle>` at the base tip. CleanSeat does the same
   at every lease boundary, and never during a held lease. The task
   branch's local ref is removed there. Its commit stays anchored under
   `refs/amq-squad/commits/<seat>/<task>`, so a later attempt can start
   from it after gc.

## The resting branch

The worktree keeps `seat/<handle>` as its resting branch, which is the
branch the launch creates and records. Git cannot hold `seat/<handle>` and
`seat/<handle>/<task>` at once, locally or on a remote, because one would
be a file and the other a directory under the same ref path. So the two
take turns. `StartTask` removes the local `seat/<handle>` ref when it
creates the task branch. The return to rest removes the task branch's
local ref and recreates `seat/<handle>` at the tip.

On the remote, the 3.0.1 branch `seat/<handle>` blocks every push to
`seat/<handle>/<task>`. That is the state of `origin/seat/dev`, the head
branch of PR #815. The step refuses such a push as push_rejected and names
the branch to delete. It is never deferred tick after tick. Before the
first 3.0.2 squad run, the owner deletes each remote `seat/<handle>` once
its PR is merged or closed.

## The seam with t22 (v3-302-seatbase)

This branch touches neither `internal/seatlaunch` nor `ensureWorktree`.
t22 owns the launch-time base and this branch owns the lease boundary.
Three points meet:

- **Reading the base tip.** `seatcommit.baseTip` reads the seat
  repository's `refs/heads/<the launch's base branch>`, the same ref
  `AddWorktree` starts a seat branch from. If t22 reads the tip another
  way, for example by fetching from the grant's push URL, it should export
  one reader and CleanSeat and `settle` should call it.
- **The resting branch at launch.** A launch that resets the worktree's
  branch must handle a worktree that is resting on `seat/<handle>` (the
  normal case). It must also handle one left on `seat/<handle>/<task>`
  when a step did not settle. It must not recreate `seat/<handle>` while
  any `seat/<handle>/…` ref exists, or git refuses the ref. Calling
  `seatcommit.CleanSeat` is safe at launch: it records `rest`, clears
  `lease` and juggles the refs.
- **An existing ledger.** `RecordWorktreeBase` keeps an existing ledger
  for the same worktree and branch. A launch that moves the worktree to a
  new tip should go through CleanSeat, which writes `rest`, so the
  ledger's parent follows the worktree.

## Rows

| Row | File | What it proves |
|---|---|---|
| TestBindingCheckRunsBeforeTheCommit (bound elsewhere) | internal/seatcommit/commitstep302_test.go | The v31 shape. The task branch carries open PR #815, which the store binds to another task at head c…c. The step refuses pr_bound_elsewhere naming #815 and "nothing is committed or pushed". There is no commit, no push to the origin, no anchor, no `pr create`, the ledger bytes are unchanged, and the other task's binding is unchanged. |
| TestBindingCheckRunsBeforeTheCommit (gh fails) | same | gh fails at the check: a pr_pending deferral, with the same nothing-written assertions. |
| TestTwoTasksOnOneSeatGetTwoBranchesAndTwoPRs | same | Two consecutive tasks on one seat. The leased worktree is on `seat/s1/t1`. After the step it is clean on `seat/s1`. The base moves on main. The second lease starts at the new tip. The two branches are pushed with PRs #1 and #2, each bound to its own task. The first commit's parent is the launch base, the second's is the new tip. The second branch does not contain the first commit. The ledger names the branch and base of each commit. |
| TestLaterAttemptAppendsToItsOwnBranchAndPR | same | A task with PR #1 from an earlier attempt is sent back (refused, reset) and leased again. StartTask puts the worktree at the earlier commit. The new commit's parent is that commit, on the same branch, pushed fast-forward. The same PR #1 is bound again at the new head, with one `pr create` in all. |
| TestPushRejectedIsNeverForced (the 3.0.1 seat branch) | internal/seatcommit/commit_test.go | A remote that carries `seat/s1` refuses `seat/s1/t1`. The step returns push_rejected with the remote untouched, never a deferral. |
| TestDoorbellAndLeadNoticeSend | internal/schedproc/slots_test.go | The doorbell runs Start before the AMQ send. A Start error still rings the seat and is returned. |
| TestSchedulerCommitStepBindsByDefaultUnderAMergeGrant | internal/cli/scheduler_run_test.go | Through `scheduler run`, the worktree after dispatch is on `refs/heads/seat/s1/t1`. gh sees `pr list --head seat/s1/t1` and `pr create --draft --head seat/s1/t1`. t1 completes bound at the anchored commit. |

Updated rows, for the same reasons: the old branch name, and the worktree
back at rest after the step.

- TestCommitInsideDeclaredFilesCommitsAndPushes: the task branch on the
  origin, `seat/s1` at the base, the anchor at the commit, and the ledger's
  branch, base and rest.
- TestCommitStepOpensAndBindsThePR: the task branch in `pr create`.
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
| the refname-conflict refusal removed (the push defers) | TestPushRejectedIsNeverForced | FAIL |
| no return to rest after an OK step | TestTwoTasksOnOneSeat…, TestCommitInsideDeclaredFiles… | FAIL |
| the doorbell skips Start | TestDoorbellAndLeadNoticeSend | FAIL |
| `scheduler run` wires no Start | TestSchedulerCommitStepBindsByDefaultUnderAMergeGrant | FAIL |

## Known limits

- **The base tip is a local ref.** The base tip is the seat repository's
  local base branch. A same-UID seat can move that ref. The next task's
  diff is then checked against a parent the seat chose, so content the
  seat planted in that commit is not path-checked. The ref is also stale
  until someone updates the main checkout. Both fixes read the tip from
  the grant's push URL, which is network on the tick, and they belong with
  t22's launch-time reader (above).
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
