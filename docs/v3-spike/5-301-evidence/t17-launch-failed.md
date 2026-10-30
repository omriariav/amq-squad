# 3.0.1 t17: a failed seat launch is a decision, a gone seat is seen each tick (execution note)

Branch v3-301-t17 on origin/v3-301-t16 17cde302 (the brief named 609b0fd5
or later; the branch was cut at 609b0fd5, fast-forwarded to 25812dd5 before
the first commit, rebased onto bd435bff before it was pushed, then t16's
rounds 5, 39b82e10, and 6, 17cde302, merged in). Rounds 5 and 6 touched
internal/seatlaunch/launcher_test.go (a cleanup in newLauncherFixture, a new
row, its done-channel wait), proof_unix.go and the new staged.go; both
merges kept both sides without conflict. Brief:
3.0.1-t17, the reviewer rulings of 2026-10-30 (session v31).

## The day's evidence (2026-10-30, session v31)

The reviewer's finding, as the brief records it (the live session was not
read; the brief forbids touching it): the reviewer seat's launch failed
thirteen times over 27 minutes and nothing recorded why. The decisions log
held, for each attempt, `seat_requested` and later `seat_request_cancelled`
with no reason field, and `amq-squad status` showed the waiting task as
`seat_starting` the whole time.

The cause in the code, on t16's head:

- `seatlaunch.Launcher.settle` (internal/seatlaunch/launcher.go, settleOne)
  kept the failure in `r.err` and posted nothing for a failed or unresolved
  request; only `SeatUp` was ever posted.
- The scheduler saw only its reservation: it waited for the seat start
  timeout, asked the launcher to cancel (which confirms a failed request, or
  an unresolved one once the token window has passed), logged
  `seat_request_cancelled` without a reason, and requested a seat again at
  the next dispatch. Each cycle was a silent retry.
- The task's wait reason stayed `seat_starting`, which status treats as
  resolving by itself (no attention item).
- t16 added the roster launch's own failure (`roster_launch_failed` with the
  stage, reason code and detail) but only for a launch that fails as a
  whole, and a whole-roster failure ends the process; a seat of an applied
  roster that did not prove was a `seat_not_up` line and then the same
  silent reservation expiry.

Separately (t16's open point 2, the reviewer's ruling 2): nothing in
production posted `SeatGone` or `SeatActivity` (only tests did), so a roster
seat whose process died on its own stayed parked and "idle" until a
restart. The replacement path (`seat_replacement_needs_relaunch`) fired only
for seats the scheduler closed itself, seats that never proved, and after a
restart.

## Design

1. **Launch failure as a decision.** A new event, `SeatLaunchFailed`,
   carries the request id, the launch attempt, the stage, AMQ's reason code
   and the failure detail. The seat launcher posts one per request whose
   launch failed or did not resolve, from the one settle path every launch
   goes through (the per-seat `RequestSeat` and the roster's `LaunchRoster`,
   at start and at the lead's relaunch). The detail goes through
   `redact.Block` in the launcher and again in the scheduler. The stage is
   the launch's own when it stopped before its seats ran (`prepare_refused`,
   `decisions_pending`, `resource_present`, `apply_failed`, `launch_error`)
   and `seat_not_up` for a seat of an applied launch that did not prove,
   with the seat's own reason code (`apply_ambiguous` when no one pane is
   its evidence).
   - The scheduler logs `seat_launch_failed` with `stage`, `reason_code` and
     the detail in `reason` (two new omitempty fields on every decision line),
     on the task the request was for.
   - It keeps each pool's last failure. A task that waits for that pool's
     seat waits with the failure's reason (AMQ's code, else the stage) in
     place of `seat_starting`, and the state file carries the failure
     (`launch_failures`) for status.
   - A second launch attempt of the pool that fails with the same reason
     blocks the waiting task for the lead with that reason (task block,
     route lead); no seat is requested again. One attempt counts once,
     however many of its seats failed. A seat of the pool that comes up
     clears the failure.
   - The lead's retry: a task the scheduler blocked so that is pending again
     with no failure since (the lead reset it) logs `seat_launch_retry` and
     the count starts over. In roster mode the block names
     `amq-squad scheduler relaunch`; after any full roster launch the tasks
     blocked on a failure return to pending with the relaunch-blocked ones,
     and dispatch blocks them again only if the pool's seat failed again.
   - A roster launch that fails as a whole still logs `roster_launch_failed`
     and ends the process (t16). Before it ends, the scheduler applies the
     launcher's events, notes a failure for any roster seat the launcher
     posted none for (a launch that failed before its requests were taken),
     and writes the state file, so status names the reason after the exit.
   - The relaunch block (`seat_replacement_needs_relaunch`) names the pool's
     last launch failure when there is one.
   - Status: the CLI's contract builder (status, status --fleet, remote)
     puts the failure's reason on the failed seat's row when the seat is not
     live, and adds a `seat_launch_failed` attention item with the stage,
     the reason, the count and the detail (high from the second failure).
2. **SeatGone from the launch ledger each tick.** The scheduler gets a
   `SeatLiveness` hook. Each tick, in roster mode, before the events are
   applied, it asks about every live roster seat (not closing) by the
   request the seat joined on, and posts `SeatGone` with reason
   `process_exited` for each whose process is gone; the same tick applies
   it, so the gone-seat handling (the exit check, then the relaunch path for
   its pool's tasks) runs at once. `schedproc.Liveness` answers from the
   launch ledger: the seat's accepted proof (pid and raw start stamp), read
   once per seat request and held, then one start-stamp read of that pid per
   tick. A stamp that differs (the pid reused) is gone; a pid whose stamp
   cannot be read is gone only when signal 0 also says it is gone; a pid
   alive with an unreadable stamp, a proof that cannot be read, or a proof
   of another request is unknown: nothing is posted and
   `seat_liveness_unknown` is logged once per answer. The verb wires it; a
   restart's adoption hands the scheduler the proof's request id.

**What it costs.** Per live roster seat per tick (2 s by default): one
start-stamp read of the recorded pid. On darwin that is one
`sysctl(KERN_PROC_PID)`; on Linux one read of `/proc/<pid>/stat`. The proof
file is read once per seat request, then held. A signal 0 is sent only when
the stamp read fails, to tell a gone pid from an unreadable stamp. For a
roster of 12 seats that is 12 such reads every 2 s. Nothing is
written.

Decision kinds added: `seat_launch_failed`, `seat_launch_retry`,
`seat_liveness_unknown`. Reused: `blocked` (with the failure's reason, stage
and reason code), `seat_gone` (now with `reason_code` `process_exited` and
the detail when the liveness check posted it), `task_reset` (also for a task
blocked on a failure). New decision fields: `stage`, `reason_code`. New
state field: `launch_failures`.

## Rows (fake AMQ, fake launcher; no network, no real HOME, no tmux)

The seatlaunch rows run under the package's refusing tmux stub (t16 round
3). The schedproc liveness row uses real processes (a `sleep` child and the
test process) and the real start stamp; nothing is signalled but the test's
own child.

| Brief row | Test | Package |
| --- | --- | --- |
| 1. a fake apply failure yields the decision with stage, reason code and detail (per-seat path, the path of the thirteen cancels), credential-free; the task waits with the reason | TestPerSeatLaunchFailureReachesTheSchedulerWithItsReason | seatlaunch |
| 1. a roster apply failure names each seat (stage, code, detail, credential-free) and leaves the state; a roster seat that did not prove is stage seat_not_up | TestRosterLaunchFailuresAreSeatLaunchFailedDecisions | seatlaunch |
| 1. the decision, the wait reason in place of seat_starting, the state file; the second failure blocks the task (route lead); no retry while blocked; the lead's reset is the retry | TestLaunchFailureIsADecisionAndASecondOneBlocksTheTask | scheduler |
| 1. the repeat is per reason and per attempt; a seat up clears it | TestLaunchFailureRepeatIsPerReasonAndPerAttempt | scheduler |
| 1. roster: fails at start and at the relaunch, the second blocks with the reason and the verb; the relaunch block names the first failure; a good relaunch returns the task | TestRosterSeatFailingTwiceBlocksForTheLead | scheduler |
| 1. a whole roster failure names each seat and writes the state | TestFailedRosterLaunchNamesEachSeatAndWritesTheState | scheduler |
| 1. the status row shows it (seat row, task row, attention, table) | TestStatusShowsTheLastLaunchFailureReason | cli |
| 1. end to end through the verb: the decisions log line, status after the refusal; the verb wires the liveness check | TestSchedulerRunRefusalLeavesTheReasonForStatus | cli |
| 2. gone within one tick, the relaunch path at once; a live seat untouched; unknown posts nothing, logged once | TestGoneRosterSeatProcessIsPostedGoneWithinOneTick | scheduler |
| 2. real processes: the dead pid gone within one tick, a reused pid gone, a live seat untouched; the proof read once | TestLivenessFromTheLaunchLedgerSeesAGoneSeatWithinOneTick | schedproc |
| 2. unknown is not gone (unreadable stamp, another request's proof, no proof) | TestLivenessUnknownIsNotGone | schedproc |
| 2. an adopted seat is checked by its proof's request | TestAdoptedRosterSeatIsCheckedByItsProofsRequest | schedproc |

Changed existing rows:

- TestLauncherCancelConfirmsFailedAndUnknownRequests and
  TestAmbiguousApplyObservationsKeepTheRequestUnresolved asserted that a
  failed or ambiguous launch posts nothing at all, which is the defect.
  They now assert no `SeatUp` and exactly one `SeatLaunchFailed` (stage
  `apply_failed`; reason `apply_ambiguous`).
- internal/scheduler/scheduler_test.go's determinism row built `Event`
  values positionally; the new fields made those literals invalid, so they
  are keyed now (no behaviour change).

### Revert check

Each change undone alone, the rows that cover it run and must fail; the
file is restored after each (`revert_check_t17.py` in the job's scratch
directory). All 24 fail.

| Mutation | Rows | Result |
| --- | --- | --- |
| the launcher posts no failure | TestPerSeatLaunchFailureReachesTheSchedulerWithItsReason, TestLauncherCancelConfirmsFailedAndUnknownRequests | fail |
| the failure event not noted | TestLaunchFailureIsADecisionAndASecondOneBlocksTheTask, TestPerSeatLaunchFailureReachesTheSchedulerWithItsReason | fail |
| the detail not redacted | TestLaunchFailureIsADecisionAndASecondOneBlocksTheTask | fail |
| the wait stays seat_starting | TestLaunchFailureIsADecisionAndASecondOneBlocksTheTask, TestPerSeatLaunchFailureReachesTheSchedulerWithItsReason | fail |
| no block on the repeat | TestLaunchFailureIsADecisionAndASecondOneBlocksTheTask, TestRosterSeatFailingTwiceBlocksForTheLead | fail |
| the on-demand dispatch does not block | TestLaunchFailureIsADecisionAndASecondOneBlocksTheTask | fail |
| the roster dispatch does not block | TestRosterSeatFailingTwiceBlocksForTheLead | fail |
| one attempt counted per seat | TestLaunchFailureRepeatIsPerReasonAndPerAttempt | fail |
| a seat up does not clear | TestLaunchFailureRepeatIsPerReasonAndPerAttempt, TestRosterSeatFailingTwiceBlocksForTheLead | fail |
| the lead's reset is not a retry | TestLaunchFailureIsADecisionAndASecondOneBlocksTheTask | fail |
| a whole roster failure not settled | TestFailedRosterLaunchNamesEachSeatAndWritesTheState, TestRosterLaunchFailuresAreSeatLaunchFailedDecisions, TestSchedulerRunRefusalLeavesTheReasonForStatus | fail |
| the relaunch does not reset failure blocks | TestRosterSeatFailingTwiceBlocksForTheLead | fail |
| the relaunch block without the failure | TestRosterSeatFailingTwiceBlocksForTheLead | fail |
| no liveness check in the tick | TestGoneRosterSeatProcessIsPostedGoneWithinOneTick, TestLivenessFromTheLaunchLedgerSeesAGoneSeatWithinOneTick, TestAdoptedRosterSeatIsCheckedByItsProofsRequest | fail |
| an unknown answer logged every tick | TestGoneRosterSeatProcessIsPostedGoneWithinOneTick | fail |
| seat_gone without its cause | TestGoneRosterSeatProcessIsPostedGoneWithinOneTick, TestLivenessFromTheLaunchLedgerSeesAGoneSeatWithinOneTick | fail |
| the seat keeps no request id | TestGoneRosterSeatProcessIsPostedGoneWithinOneTick | fail |
| a reused pid judged by signal 0 | TestLivenessFromTheLaunchLedgerSeesAGoneSeatWithinOneTick | fail |
| an unreadable stamp taken as gone | TestLivenessUnknownIsNotGone | fail |
| the proof read every tick | TestLivenessFromTheLaunchLedgerSeesAGoneSeatWithinOneTick | fail |
| another request's proof accepted | TestLivenessUnknownIsNotGone | fail |
| adoption without the proof's request | TestAdoptedRosterSeatIsCheckedByItsProofsRequest | fail |
| the verb wires no liveness | TestSchedulerRunRefusalLeavesTheReasonForStatus | fail |
| status reads no launch failure | TestStatusShowsTheLastLaunchFailureReason, TestSchedulerRunRefusalLeavesTheReasonForStatus | fail |

### Gate

```
go build ./... && go vet ./internal/seatlaunch ./internal/schedproc ./internal/scheduler ./internal/cli && go test -count=1 ./internal/seatlaunch ./internal/schedproc ./internal/scheduler ./internal/cli
ok  	github.com/omriariav/amq-squad/v3/internal/seatlaunch
ok  	github.com/omriariav/amq-squad/v3/internal/schedproc
ok  	github.com/omriariav/amq-squad/v3/internal/scheduler
ok  	github.com/omriariav/amq-squad/v3/internal/cli
```

`make readme-html-check` passes (README.html regenerated from README.md).

## What the brief got wrong, and open points

1. **A whole-roster failure cannot repeat inside one scheduler.** t16 ends
   the process on a roster launch that fails as a whole, and the brief's
   own ruling keeps that. So "a second failure with the same reason blocks
   the waiting task" applies to per-seat failures: a seat of an applied
   roster that did not prove, at the start launch and again at the lead's
   relaunch, and the on-demand per-seat path. The counts and the blocks
   survive a restart in the state file (round 2, Codex finding 2); the
   supervisor's restarts after a `launch_error` are capped at five fast
   failures (t16), and an AMQ refusal is not restarted at all. The whole-roster failure is now named
   per seat and left in the state file, so status shows it after the exit.
2. **The wait reason is AMQ's code.** A task that waits for a pool whose
   last launch failed now has that failure's reason as its `wait_reason`
   (for example `binding_present_without_resumable_conversation`), so
   status, the fleet document and any reader of the task store show it
   without a status change; status lists it as a low attention item
   (`task_waiting`), as it does any wait that is not quiet. The wait-reason
   list in docs/task-store-design.md is outside this brief's files and does
   not name this; the lead widens if it should.
3. **Status is enriched in the CLI.** The seat row and the attention item
   are added in the CLI's contract builder (internal/cli/status_launchfail.go,
   called by status, status --fleet and remote). internal/status is outside
   the declared files and unchanged, so a caller of `status.Build` alone
   (doctor's schema check) does not see them. A seat that never ran has no
   row (rows come from the mailboxes); its failure shows on the task row
   and in the attention queue.
4. **Two lines for one fact.** A seat of an applied roster that did not
   prove logs t16's `seat_not_up` and t17's `seat_launch_failed`, and a
   whole-roster failure logs `roster_launch_failed` and one
   `seat_launch_failed` per seat. Kept so every t16 row keeps its meaning;
   `seat_launch_failed` is the one with the fields.
5. **A second SeatGone for one attempt is checked once** (the reviewer's
   ruling, round 5, which replaced round 4's guard in the handler). The
   exit queue deduplicates gone items by handle and attempt
   (internal/scheduler/exits.go:137, in queueExit at :128). The attempt is
   the request id the seat joined on: the SeatGone names it, or the live
   seat's is taken (scheduler.go:458). The liveness gone names it. A second
   item for the same gone seat and attempt is dropped before it runs, and
   logged once as `exit_check_duplicate` (scheduler.go:485). So a liveness
   gone followed by a launcher's confirmation gives one exit check and one
   slot release, and the held task is untouched. A gone for a new attempt
   of the same handle queues as before (R3, P1-9). A gone that names no
   request for a handle no longer in the seat map has no attempt and still
   queues, as before. Without an exit checker (tests only; the verb always
   wires one) the launcher's SeatGone hook is called per gone. The liveness
   check remains the only SeatGone producer in production. Row:
   TestASecondSeatGoneForOneAttemptIsCheckedOnce. The revert check
   (`revert_check_t17_dedupe.py`) undoes three parts alone: the dedupe, the
   item's attempt, and the live seat's request for a gone that names none.
   The row fails each time.
6. **Liveness is roster mode only**, as the brief says; on-demand seats are
   test-only since t16 and still close on idleness. A seat that dies while
   it holds a lease takes the existing gone-seat path: its exit check is
   queued with the task, and the lease runs to its expiry as before.
7. **SeatActivity still has no producer.** The brief asked for SeatGone
   only; a parked seat's activity is not observed (it renews no claimed
   lease, so nothing depends on it today).
8. Outside the brief's declared files (seatlaunch's launcher.go and
   roster.go): internal/seatlaunch/run.go, report.go and the new
   amqoutput.go, for Codex finding 1 and the reviewer's chokepoint, which
   the lead's messages named. The other new files are
   internal/scheduler/launchfail.go, internal/schedproc/liveness_ledger.go
   and internal/cli/status_launchfail.go, with their tests.

## Round 2: the Codex findings on 5b9202fd (2026-10-30)

**Codex 1 (blocker): AMQ's stderr was redacted after it was wrapped.**
When `amq launch --prepare` or `--apply` printed output that is not JSON,
`prepareCLI` and `applyCLI` (internal/seatlaunch/run.go) built an error
"amq launch --apply exited N without an ApplyResult: <stderr>". The
redaction ran later, on the whole message. `redact.Block` finds a YAML key
that owns the quoted value on the next line only at a line's start, and
the prefix buried the key. So stderr shaped as `api_key:` with the quoted
value on the next line kept the value in the decision, the state file and
status. Now both functions redact AMQ's raw stderr as one block before
anything wraps it, for the error and for the FailureDetail alike (round
3 makes this the one chokepoint, `amqDetail`). Every reason and detail t17 logs takes AMQ's output
from those two functions. The redaction in the launcher's event and in the
scheduler stays, as a second pass. run.go is outside the brief's declared
files; the lead's message named these functions.

**Codex 2: the failures and the blocks were memory only.** A restart lost
both. A task blocked on a repeated failure then stayed blocked after a
later successful launch, because the reset no longer recognised it, and
the repeat count started over at one. Now the state file carries each
failure's attempt (`launch_id`) and sequence number (`seq`) and the blocked
tasks (`launch_blocked`: task, pool, the failure's `seq`).
`schedproc.Run` restores them before the first tick
(`launch_failures_restored`, or `launch_failures_unread` when the state file
cannot be read), and the sequence counter continues from the highest
restored number. A failure after the restart is therefore later than the
block, so it is never taken for the lead's retry.

**Codex 3 (nit): the proof read had no deadline.** The liveness check's
read of the proof file is bounded by `Liveness.ReadBound`. The verb passes
the tick's share, half the tick split over the roster and at least 10 ms;
the default is 250 ms. A read past the bound is unknown, never gone. While
it is still in flight no second read of that seat starts, and its result is
taken when it lands. The proof is still read once per seat request, so the
steady cost is unchanged: one start-stamp read per seat per tick.

| Finding | Row | Package |
| --- | --- | --- |
| 1, prepare and apply, per-seat path: the value in neither the decision nor the state file | TestInvalidJSONStderrIsRedactedBeforeItIsWrapped (subtests prepare, apply) | seatlaunch |
| 1, roster path: not in roster_launch_failed, either seat_launch_failed, the error or the state file | TestInvalidJSONRosterFailureIsRedacted | seatlaunch |
| 1, end to end through `scheduler run` and the real launcher: not in the error, decisions.jsonl, the state file, status --json or the status table | TestInvalidJSONThroughTheVerbLeavesNoCredentialInStatus | cli |
| 2, a restart between the block and a successful launch recovers the task; a repeat across a restart blocks; a failure after a restart re-blocks and is not a retry | TestLaunchFailuresAndBlocksSurviveARestart | scheduler |
| 2, Run restores the previous incarnation's state before the first tick | TestRunRestoresTheLaunchFailuresOfThePreviousIncarnation | schedproc |
| 3, a slow read is unknown, one read in flight, the landed read taken, the tick's share | TestLivenessProofReadIsBounded | schedproc |
| 3, the verb passes the bound | TestSchedulerRunRefusalLeavesTheReasonForStatus (extended) | cli |

The canaries are built at run time, use the key-then-quoted-value-on-the-
next-line shape, and the seatlaunch rows check them against
`redact.RealCredentialShapes`.

Revert check (`revert_check_t17_codex.py` in the job's scratch directory),
each change undone alone, its rows run:

| Mutation | Rows | Result |
| --- | --- | --- |
| prepare's stderr wrapped raw | TestInvalidJSONStderrIsRedactedBeforeItIsWrapped/prepare, TestInvalidJSONThroughTheVerbLeavesNoCredentialInStatus | fail |
| apply's stderr wrapped raw | TestInvalidJSONStderrIsRedactedBeforeItIsWrapped/apply, TestInvalidJSONRosterFailureIsRedacted | fail |
| Run restores nothing | TestRunRestoresTheLaunchFailuresOfThePreviousIncarnation | fail |
| the blocks not restored | TestLaunchFailuresAndBlocksSurviveARestart | fail |
| the failures not restored | TestLaunchFailuresAndBlocksSurviveARestart | fail |
| the blocks not written to the state file | TestLaunchFailuresAndBlocksSurviveARestart | fail |
| the sequence not restored (both restore points) | TestLaunchFailuresAndBlocksSurviveARestart | fail |
| the proof read unbounded | TestLivenessProofReadIsBounded | fail |
| a second read while one is in flight | TestLivenessProofReadIsBounded | fail |
| the verb passes no bound | TestSchedulerRunRefusalLeavesTheReasonForStatus | fail |

The sequence mutation first disabled only the failures' restore point and
the row passed, because the blocks' restore point also sets the counter.
With both disabled it fails; the table records that form.

## Round 3: the reviewer's chokepoint for finding 1 (2026-10-30)

The reviewer replaced "on every path" with one chokepoint. `amqDetail`
(internal/seatlaunch/amqoutput.go) is the one function that turns AMQ's
output into a reason or a detail: it trims the raw text and runs
`redact.Block` on it before anything prefixes, wraps or flattens it, and it
is idempotent. Every carrier goes through it:

- `prepareCLI` and `applyCLI` pass RunAMQ's stderr through it before their
  error wrapping and before returning the detail.
- Every write of a FailureDetail: the Result's (prepare, resource_present,
  apply), the ApplyResult's, and the staged record's apply (`recordApply`).
- The launch failure the launcher posts, and the roster outcome's detail
  (the Result's FailureDetail and the launch error).
- The operator's report (`Report`) reads both FailureDetails through it
  before `oneLine` flattens them; flattening first would put the key in the
  middle of a line, where the multiline rule does not see it.

t16's round 7 was to redact Apply.FailureDetail at the source in run.go and
report.go; the lead routes that to this chokepoint instead, so a merge of
t16's round 7 is expected.

| Row | What it holds | Package |
| --- | --- | --- |
| TestAMQOutputHasOneChokepoint | over the package's non-test Go files (go/ast): RunAMQ is called at exactly two sites; its stderr is used only as amqDetail's argument (directly or through string(...)); every FailureDetail read is amqDetail's argument; every FailureDetail write (assignment or composite field) is an amqDetail call; a bypass fails naming the file and line | seatlaunch |
| TestApplyFailureDetailGoesThroughTheChokepoint | an Apply refused with valid JSON and the canary on stderr's first line: not in either decision kind, the state file, the staged records, the Result's two FailureDetails or the report | seatlaunch |
| TestReportRedactsBeforeItFlattens | a Result holding AMQ's raw text prints neither value; amqDetail is idempotent | seatlaunch |
| TestInvalidJSONStderrIsRedactedBeforeItIsWrapped, TestInvalidJSONRosterFailureIsRedacted, TestInvalidJSONThroughTheVerbLeavesNoCredentialInStatus | round 2's rows, now through the chokepoint | seatlaunch, cli |

Status is checked end to end for the prepare caller (through `scheduler
run` and the real launcher). For the other callers the rows check the state
file, which is the only place status reads a launch failure's detail from.
Driving an Apply through the verb would need the whole Claude seat setup
(the mode probe, the argument forms, the proof socket) in the CLI package.

Revert check (`revert_check_t17_choke.py` in the job's scratch directory),
each change undone alone:

| Mutation | Rows | Result |
| --- | --- | --- |
| the report's apply detail flattened raw | TestReportRedactsBeforeItFlattens, TestAMQOutputHasOneChokepoint | fail |
| the report's result detail flattened raw | TestReportRedactsBeforeItFlattens, TestAMQOutputHasOneChokepoint | fail |
| prepare's stderr wrapped raw | TestInvalidJSONStderrIsRedactedBeforeItIsWrapped/prepare, TestInvalidJSONThroughTheVerbLeavesNoCredentialInStatus, TestAMQOutputHasOneChokepoint | fail |
| apply's stderr wrapped raw | TestInvalidJSONStderrIsRedactedBeforeItIsWrapped/apply, TestInvalidJSONRosterFailureIsRedacted, TestAMQOutputHasOneChokepoint | fail |
| the staged record's apply detail around the chokepoint | TestAMQOutputHasOneChokepoint | fail |
| the ApplyResult's detail around the chokepoint | TestAMQOutputHasOneChokepoint | fail |
| the roster outcome's detail around the chokepoint | TestAMQOutputHasOneChokepoint | fail |
| a new caller reads RunAMQ's stderr raw | TestAMQOutputHasOneChokepoint | fail |

The three "around the chokepoint" mutations leak nothing today, because the
detail they carry was already redacted at the stderr. Only the source scan
catches them, which is the point of the row.

## Round 5: t16's round 7 merged; the double fire per the reviewer's ruling

t16's round 7 (8c0cf31e) is merged into t17 (a9a1c890). Its commit 4d12131e
redacted amq's stderr at the source with a function of its own,
`amqDiagnostic` in run.go, called in `prepareCLI` and `applyCLI`. That
duplicated this branch's chokepoint. The merge keeps `amqDetail` at both
calls and drops `amqDiagnostic` and its import, so `amqDetail` stays the
one chokepoint and TestAMQOutputHasOneChokepoint passes. No t16 row
asserted on `amqDiagnostic`. t16's note still names it (t16-roster-launch.md
line 689); in this branch the function is `amqDetail` in amqoutput.go.

The double fire follows the reviewer's ruling, item 5 above: the exit
queue deduplicates by handle and attempt. Round 4's guard in the SeatGone
handler (`goneRepeat`, the `seat_gone_ignored` decision) is removed with
its row. Kept, the guard would have stopped a second gone before the queue
saw it, so no row could fail when the dedupe is removed. What the guard did
beyond the ruling is gone with it: a gone that names an earlier request
than the live seat's now ends the live seat. No producer posts such a gone
today.
