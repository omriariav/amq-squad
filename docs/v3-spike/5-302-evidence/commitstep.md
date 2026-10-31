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

Collected by the lead, read-only, from the live commit ledger of seat dev
(session v31) and from the PR page. This branch read neither.

Commit ledger, seat dev (`branch` is null: the 3.0.1 ledger has no
per-commit branch; both commits went to seat/dev):

```
t12  commit c338d3fe654fe802e257d8d3eb5e1c1aa13a42c0  parent a203f18310ae81a493762fb07a972034d063cbcb  pushed true  branch null
t20  commit 6b9808a586bbb70060ab33482b334622ab1462db  parent c338d3fe654fe802e257d8d3eb5e1c1aa13a42c0  pushed true  branch null
```

PR #815 (head branch seat/dev, bound to t12 at c338d3fe), its two commits:

```
c338d3fe  t12: 3.0.x: a task re-pin before the first claim is honoured; after a…
6b9808a5  t20: 3.0.2: a seat's own block with a widen request has no caller: th…
```

t20's commit has t12's commit as its parent and sits on the same seat
branch. It was pushed before the binding check refused it, so PR #815's
head moved past t12's bound head.

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

   The ledger contract: a step refused or deferred by the path checks or
   the binding check leaves the ledger byte-for-byte unchanged. No ledger
   write of any kind comes before the binding check passes. A quarantine
   (the owner's config changed during the run) records its evidence only
   after the binding check. When the binding check does not pass, the seat
   is still quarantined in the task store, with nothing written to the
   ledger. After the binding check, a verified quarantine clear may be
   lifted onto the ledger before the verifier runs. The binding check reads
   every open PR on the head with `gh pr list --limit 100`; an answer that
   reaches the limit defers the step as pr_pending, because the step cannot
   prove it examined every PR. A retry of a recorded commit is checked like
   a fresh diff against the grant in force now. Its digest is recomputed
   and compared with the recorded one, and a mismatch refuses
   `recorded_diff_mismatch` to the owner.
2. **One branch per task (t23).** Each task commits on
   `seat/<handle>-<task id>` (for example `seat/dev-t12`), beside the
   resting branch `seat/<handle>`. Git cannot hold `seat/<handle>` and a
   directory of task branches under it, so the separator is a hyphen. The
   name is a label only. Nothing parses it back into a handle and a task,
   because a handle can itself contain a hyphen. The ledger's per-task
   record (`lease`, and each commit's `branch`) is the only source of
   which task a branch carries. CleanSeat accepts a worktree off the
   resting branch only on the branch of the lease the ledger records
   (`Lease.Branch`), not on a branch a commit merely names.
3. **One base (amendment 2b, the reviewer's base ruling).** The base is a
   per-task fact, fetched at the grant. The doorbell's Start is
   `Slots.TaskStart`. It fetches the remote base tip with the launch's own
   fetch: `Slots.GrantBase` reuses t22's `baseTip`, which reads the push
   URL's tip through `seatcommit.FetchCredentials` in a shadow git
   directory. It then passes the tip to `seatcommit.StartTask`, whose new
   `base` parameter cuts `seat/<handle>-<task id>` from it and records it
   as the task's base on the ledger (`lease`, then each commit's `base`).
   A later attempt keeps its branch's last commit and that branch's base.
   A failed fetch takes the unprepared path (item 4). A seat with no commit
   ledger is left as it is. Between leases, and never during a held lease,
   CleanSeat resets the resting branch to the launch's base,
   `launch.Record.Base`. The return after an OK step does the same. A
   record without a base (a launch before 3.0.2) keeps the ledger's resting
   commit, and the step notes `launch_base_missing`. The base is never a
   local branch of the shared repository. This also fixes the day-2 reset:
   `recordBase` keeps the ledger of the same worktree and branch across a
   relaunch, and CleanSeat used to reset to that ledger's old parent.
4. **An unprepared lease is not rung.** If the doorbell's `StartTask`
   fails, the seat is not rung. The doorbell returns a
   `scheduler.LeaseUnpreparedError`, and the scheduler blocks the task for
   the lead as `task_branch_unprepared`, with the error as the detail. The
   holder's block ends the attempt and releases the lease in one store
   transaction. No seat works on the resting branch. Any other doorbell
   error stays a logged hint.
5. **The PR per task.** The step pushes the task branch and opens and
   binds its own draft PR. A later attempt appends to the same branch and
   binds the same PR again at the new head (amendment 2c).
6. **Back to a clean base.** After an OK step the worktree returns to
   `seat/<handle>` at the base. CleanSeat does the same at every lease
   boundary. Each commit is also anchored under
   `refs/amq-squad/commits/<seat>/<task>`, so a later attempt can start
   from it even if the local branch is gone.

## The seam with t22 (merged as PR #820)

This branch merged origin/v3 at d01ee9e9 (merge e3a900cc) with no
conflict. It reuses t22's fetch, `Slots.baseTip`, through a new
`Slots.GrantBase`, and its fields, `launch.Record.Base` and
`seatcommit.FetchCredentials`. `ensureWorktree` is unchanged. The
lenient JSON read of the staged record's `"base"` key from before the
merge is gone.

## Rows

| Row | File | What it proves |
|---|---|---|
| TestBindingCheckRunsBeforeTheCommit (bound elsewhere) | internal/seatcommit/commitstep302_test.go | The v31 shape. The task branch carries open PR #815, which the store binds to another task at head c…c. The step refuses pr_bound_elsewhere naming #815 and "nothing is committed or pushed". There is no commit, no push to the origin, no anchor, no `pr create`, the ledger bytes are unchanged, and the other task's binding is unchanged. |
| TestBindingCheckRunsBeforeTheCommit (gh fails) | same | gh fails at the check: a pr_pending deferral, with the same nothing-written assertions. |
| TestTwoTasksOnOneSeatGetTwoBranchesAndTwoPRs | same | Two consecutive tasks on one seat. The leased worktree is on `seat/s1-t1`. After the step it is clean on `seat/s1`. The base moves on main, and the grant hands StartTask the new tip. The second lease starts at that tip. The two branches are pushed with PRs #1 and #2, each bound to its own task. The first commit's parent is the launch base, the second's is the new tip. The second branch does not contain the first commit. The ledger names the branch and base of each commit. |
| TestLaterAttemptAppendsToItsOwnBranchAndPR | same | A task with PR #1 from an earlier attempt is sent back (refused, reset) and leased again. StartTask puts the worktree at the earlier commit. The new commit's parent is that commit, on the same branch, pushed fast-forward. The same PR #1 is bound again at the new head, with one `pr create` in all. |
| TestTaskBranchIsALabelTheRecordDecides | internal/seatcommit/commitstep302_test.go | Seat `qa-2`, a hyphenated handle. The seat checks out a branch it made, `seat/qa-2-t9`, which only looks like a task branch. CleanSeat refuses it and touches nothing, because the ledger records no such branch. The leased task t1 is on `seat/qa-2-t1`, and its commit is recorded with that task and branch. After t1 completes, the seat checks out t1's branch again. A commit names that branch, but no lease is behind it, so CleanSeat refuses it too. (A task id such as "t1-b" cannot be made: the store takes only canonical t<N> ids.) |
| TestEveryOpenPROnTheBranchIsExamined | internal/seatcommit/commitstep302_test.go | Codex finding 2. Two open PRs on one head: this task's own #1 first, then #815, bound to another task. The step refuses pr_bound_elsewhere naming #815 before anything is written. |
| TestRecordedCommitWithNoNewEditsIsPushedAndBound | same | Codex finding 1. An attempt records its commit and the push fails (a pre-receive hook rejects it). The task is refused and reset, and the next lease starts on that commit. A done report with no new edits pushes the recorded commit to the task branch and binds PR #1 at it. |
| TestQuarantineClearIsLiftedOnlyAfterTheBindingCheck | same | Codex finding 3. A seat with a verified quarantine clear, whose task branch carries another task's bound PR, is refused with the ledger byte for byte unchanged. With that PR gone, the same step lifts the clear and commits. |
| TestRecordedCommitRetryIsCheckedAgainstTheGrantNow | internal/seatcommit/commitstep302_test.go | Codex finding 1 on c09a8887. A recorded commit whose push failed is retried with no new edits. A size cap tightened between the attempts refuses the retry with size_cap_exceeded. A recorded digest that is not the commit's diff refuses with recorded_diff_mismatch. Nothing is pushed either way. |
| TestAFullPagePRListDefers | same | Codex finding 2 on c09a8887. The fake gh returns exactly 100 open PRs. The step defers as pr_pending with nothing written, and the call carries `--limit 100`. |
| TestQuarantineWritesNothingBeforeTheBindingCheck | same | Codex finding 3 on c09a8887. The owner's config changes during the run, and the task branch carries another task's bound PR. The seat is quarantined (global_config_changed, routed to the owner), the detail names pr_bound_elsewhere, the ledger is byte for byte unchanged, and no evidence ref is anchored. |
| TestAnotherTasksLeaseRefusesBeforeTheQuarantine | internal/seatcommit/commitstep302_test.go | Codex re-check on 7c1a8098. The owner's config changed, and the ledger holds a lease of another task (t99). The step refuses lease_task_mismatch to the lead, does not quarantine, leaves the ledger byte for byte unchanged and anchors no evidence ref. |
| TestRowGitsReadNoRealHome | same | Codex re-check on 7c1a8098, P3. The process HOME has a `~/.gitconfig` no git can parse. A full lease, commit, push and binding still pass, and so do the helpers `originRef`, `originBranch` and `runGit`. |
| TestTaskBranchCoexistsWithTheSeatBranchOnTheRemote | same | A remote that still carries the 3.0.1 `seat/s1` takes `seat/s1-t1` beside it, and `seat/s1` is unchanged. |
| TestUnpreparedLeaseBlocksTheTaskForTheLead | internal/scheduler/scheduler_test.go | The doorbell cannot put the seat on the task's branch. The task is blocked as task_branch_unprepared, routed to the lead, with the error in its detail. The lease is gone, the attempt is recorded, the seat holds nothing, and no doorbell_failed is logged. |
| TestRecordWithoutBaseKeepsTheRestingCommit | same | A launch record with no `base`, and the owner's local main moved ahead, unpushed. The lease, the commit's parent and `rest` stay on the resting commit. The step notes `launch_base_missing`. |
| TestGrantBasesTheTaskOnTheRemoteTipFetchedAtTheGrant | internal/schedproc/grantbase_test.go | A seat launched on tip A. A merge on the fake remote moves its main to B, while the local main stays at A. The grant's `TaskStart` fetches B and puts the worktree on `seat/c1-t1` at B, and the ledger's lease base is B. The task's commit has parent B, the ledger's commit says base B, and the remote's `seat/c1-t1` is the commit. |
| TestGrantBaseFetchFailureRingsNothing | same | The push URL cannot be fetched. The doorbell sends nothing and returns `LeaseUnpreparedError`. No lease start is written, and the worktree stays on `seat/c1`. |
| TestLeaseBoundaryResetsToTheLaunchRecordsBase | same | The day-2 shape. The ledger keeps its first base, the launch record carries a newer base, and the local main moved further after the launch. The lease boundary resets to the launch record's base. The task's commit takes it as parent and base, and the ledger's `rest` is that base. |
| TestDoorbellAndLeadNoticeSend | internal/schedproc/slots_test.go | The doorbell runs Start before the AMQ send. A Start error rings nothing and comes back as a LeaseUnpreparedError. A prepared seat is rung. |
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

No row reaches the network. The origin is a bare local repository with
local_push_ack, gh is a shell script through the `Gh` and `seatCommitGh`
seams, and the scheduler-run rows use the existing fake launcher and
sender.

No git in these rows reads a global or system git config. The harness's
own git calls carry `GIT_CONFIG_GLOBAL=/dev/null` and
`GIT_CONFIG_NOSYSTEM=1`. The gits the seatcommit rows run themselves add
the fixture's temp HOME to that: `sh`, `originBranch`, `originRef`,
`runGit`, and the fake gh's head lookup. So does the commit step's own
environment in the fixture (`Options.Env`), and its `Home` is the
fixture's temp home. TestRowGitsReadNoRealHome proves it: with a process
HOME whose `~/.gitconfig` no git can parse, a full lease, commit, push and
binding pass.

Calls of CleanSeat and StartTask that pass no environment inherit the
process HOME. Their git still reads no global or system config, because
they carry the same harness variables. The schedproc and cli rows set a
temp HOME for the whole test, through `squadFixture` and `t.Setenv`.

## Revert checks

28 mutations, each applied alone to the code at the head of this branch.
For each one, the named rows were run and the file was restored. Every
mutation compiled, and every result is a real test failure (`--- FAIL`),
not a build error. The fake gh's explicit environment has no mutation of
its own. The commit step's gh environment already carries
`GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CONFIG_NOSYSTEM=1`, and that
environment reaches the script, so removing the prefix alone changes
nothing a row can observe.

| Mutation | Row | Result |
|---|---|---|
| another task's lease lets the quarantine write unchecked | TestAnotherTasksLeaseRefusesBeforeTheQuarantine | FAIL |
| the rows' git helpers inherit the process environment | TestRowGitsReadNoRealHome | FAIL |
| a recorded commit's retry skips the checks (the old lease-mode copy) | TestRecordedCommitRetryIsCheckedAgainstTheGrantNow/a_cap | FAIL |
| the recorded digest is not compared | TestRecordedCommitRetryIsCheckedAgainstTheGrantNow/a_recorded | FAIL |
| a full page of open PRs passes | TestAFullPagePRListDefers | FAIL |
| gh pr list without --limit | TestAFullPagePRListDefers | FAIL |
| the quarantine writes before the binding check | TestQuarantineWritesNothingBeforeTheBindingCheck | FAIL |
| the binding check never stops the step | TestBindingCheckRunsBeforeTheCommit | FAIL |
| the binding check stops at the first acceptable PR | TestEveryOpenPROnTheBranchIsExamined | FAIL |
| no new edits on the recorded commit returns OK before the push | TestRecordedCommitWithNoNewEditsIsPushedAndBound | FAIL |
| the quarantine clear is written before the binding check | TestQuarantineClearIsLiftedOnlyAfterTheBindingCheck | FAIL |
| a later attempt starts from the base, not its earlier commit | TestLaterAttemptAppendsToItsOwnBranchAndPR | FAIL |
| the reused PR is not bound again | TestLaterAttemptAppendsToItsOwnBranchAndPR | FAIL |
| CleanSeat goes by the branch name's prefix | TestTaskBranchIsALabelTheRecordDecides | FAIL |
| CleanSeat also takes a branch a commit names | TestTaskBranchIsALabelTheRecordDecides | FAIL |
| the task branch under seat/<handle>/ | TestTaskBranchCoexistsWithTheSeatBranchOnTheRemote | FAIL |
| StartTask ignores its base parameter | TestTwoTasksOnOneSeatGetTwoBranchesAndTwoPRs | FAIL |
| CleanSeat ignores the launch record's base | TestLeaseBoundaryResetsToTheLaunchRecordsBase | FAIL |
| the reset target is the ledger's parent (the day-2 code) | TestLeaseBoundaryResetsToTheLaunchRecordsBase | FAIL |
| a record without a base falls back to the local base branch | TestRecordWithoutBaseKeepsTheRestingCommit | FAIL |
| no return to rest after an OK step | TestTwoTasksOnOneSeatGetTwoBranchesAndTwoPRs, TestCommitInsideDeclaredFilesCommitsAndPushes | FAIL |
| the doorbell skips Start | TestDoorbellAndLeadNoticeSend | FAIL |
| the doorbell rings despite a Start error | TestDoorbellAndLeadNoticeSend | FAIL |
| the grant passes StartTask no fetched tip | TestGrantBasesTheTaskOnTheRemoteTipFetchedAtTheGrant | FAIL |
| a failed fetch at the grant is ignored | TestGrantBaseFetchFailureRingsNothing | FAIL |
| the scheduler only logs an unprepared ring | TestUnpreparedLeaseBlocksTheTaskForTheLead | FAIL |
| TaskStart fetches for a seat with no commit ledger | TestSchedulerRunVerbDispatchesRingsAndStops | FAIL |
| `scheduler run` wires no Start | TestSchedulerCommitStepBindsByDefaultUnderAMergeGrant | FAIL |

## Known limits

- **Records from before 3.0.2.** A launch record without `base` keeps the
  ledger's resting commit until the next launch records a base.
- **A later attempt on another seat.** It gets a new branch
  `seat/<other>/<task>` and a new PR. The store's binding then moves to
  the new PR, and the old PR is left open.
- **A later attempt with no StartTask.** A manual claim (a doorbell whose
  Start failed now blocks the task instead) leaves the worktree off the
  task's branch. The step
  refuses it (worktree reason) instead of committing a diff that does not
  follow the branch. A first attempt in that state commits from the
  resting commit as usual.
- **depends_on and the base.** A task that depends on another, still
  unmerged task starts from the base tip without that task's commit.
