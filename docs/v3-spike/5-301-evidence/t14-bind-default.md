# 3.0.1 t14: the commit step binds the draft PR by default under a merge grant

Defect (session v31, task t5): the scheduler's commit step committed and
pushed seat/dev but opened and bound no draft PR, because `scheduler run
--open-pr` was off by default and the lead did not pass it. The merge train
refuses such a task (task_unbound).

## The change

- `internal/cli/scheduler_run.go`: `commitStepOpensPR` reads the resolved
  grant through `seatcommit.TargetFor` (the reading `LaunchTarget` records
  as the push target). With a merge grant for the squad's repository the
  commit step opens (or finds) and binds the PR, with no flag. `--no-open-pr`
  is the off-switch and is refused under a merge grant, before the push
  target is computed or anything is written. `--open-pr` is a no-op kept for
  compatibility. The start line prints the effective choice. Binding still
  goes only through `task.Store.BindPR` (commitStep, unchanged).
- `internal/cli/lead_scheduler.go`: comment only. The lead's team mode
  passes no PR flag, so its scheduler binds by default.
- README (the commit step) and docs/v3-plan.md 4.3 (the commit-step
  paragraph, amendment 21/28): one sentence each.

## Rows

| Row | File | What it proves |
|---|---|---|
| TestSchedulerCommitStepBindsByDefaultUnderAMergeGrant | internal/cli/scheduler_run_test.go | `scheduler run` with no PR flag, owner merge grant o/r: the fake launcher seats s1, t1 is dispatched and reports done, the commit step pushes to the bare origin, the fake gh logs `pr list --head seat/s1` and `pr create`, and t1 completes with pr_repository o/r, pr_number 7, pr_head the seat head. Start line: `PR binding on (merge grant o/r)`. |
| TestSchedulerCommitStepOpensNoPRWithoutAMergeGrant | internal/cli/scheduler_run_test.go | No owner grants file, `--open-pr` passed: gh is never called, t1 completes unbound (committed, not pushed), start line `PR binding off (no merge grant)`. |
| TestSchedulerNoOpenPRRefusedUnderAMergeGrant | internal/cli/scheduler_run_test.go | `--no-open-pr` under the merge grant returns the refusal naming o/r and "the merge train needs each completed task bound to its PR"; no scheduler state file is written. Without a grant the choice is `off (--no-open-pr)`. |
| TestLeadSchedulerLeavesPRBindingOn | internal/cli/lead_scheduler_test.go | The lead's scheduler command carries no `--no-open-pr` (a guard: lead_scheduler.go had no behavior change to revert). |

No row reaches the network or the real HOME: HOME is a test directory,
the push target is a bare local origin (local_push_ack), gh is a shell
script through the `seatCommitGh` seam, and the AMQ launcher and sender are
the existing `schedulerAMQLauncher` and `schedulerSendRun` seams.

## Revert checks

| Mutation of scheduler_run.go | Result |
|---|---|
| The commit step gets open-pr off (the old default) | BindsByDefault FAIL: `binding: state completed pr 0 head "" repo ""` |
| `--no-open-pr` not refused under a grant | NoOpenPRRefused FAIL: `--no-open-pr under a merge grant: <nil>` (bounded 5 s context) |
| The whole file back to origin/v3 | build FAIL: `undefined: commitStepOpensPR`; on the old semantics every row's start-line check fails |
| `--open-pr` still live without a grant | passes: without a merge grant the launch records no push URL, the step never pushes, and the PR step is never reached. The old flag was inert there; the start-line check carries the no-grant row. |

## Help text

Before (origin/v3):

```
Usage:
  amq-squad scheduler run --seats FILE --lead HANDLE [--lead-pool r:m:e]... [--tick 2s] [--trust] [--max-seats N] [--base-branch B]
...
--open-pr   the commit step opens (or finds) each pushed seat branch's draft PR and binds it to the task   (default false)
```

After:

```
Usage:
  amq-squad scheduler run --seats FILE --lead HANDLE [--lead-pool r:m:e]... [--tick 2s] [--trust] [--max-seats N] [--base-branch B] [--no-open-pr]
...
The commit step binds by default under a merge grant: when the squad's
repository has a merge grant, it opens (or finds) each pushed seat branch's
draft PR and binds it to the task, since the merge train refuses an unbound
task (task_unbound). --no-open-pr turns that off and is refused under a
merge grant; without one there is no PR to open, and binding is off.
--open-pr is a no-op kept for compatibility. The start line prints the
effective choice.
...
--open-pr      no-op, kept for compatibility: the commit step binds each pushed seat branch's draft PR by default under a merge grant
--no-open-pr   the commit step opens and binds no PR (refused when the repository has a merge grant)
```

Start line after: `scheduler for <root> (session s1, lead lead, max 1 seats, tick 20ms, PR binding on (merge grant o/r)); decisions in <root>/.../decisions.jsonl`.

## Gate

`go build ./... && go vet ./internal/cli ./internal/schedproc && go test -count=1 ./internal/cli ./internal/schedproc`:

```
ok  	github.com/omriariav/amq-squad/v3/internal/cli
ok  	github.com/omriariav/amq-squad/v3/internal/schedproc
```
