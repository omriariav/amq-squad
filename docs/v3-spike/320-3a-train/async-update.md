# 3.2.0-3a: the merge train waits for GitHub's branch update before it proves and re-binds

Brief: `brief-320-3a-train-async-rebase.md` (approved by the plan reviewer
2026-11-05 with three edits: the journal before the mutation; #894 by a
recorded lead decision, not the general rule; the bounded wait at 120 s with
backoff). Lead PR, built in a scratch worktree on `origin/v3` at `87fb71e9`
(branch `v3-320-3a`). Declared files only: `internal/mergetrain/`,
`internal/cli/` (`merge_train.go`, `merge_train_rebind.go`,
`scheduler_run.go`), `internal/schedproc/` (`decide.go`),
`docs/v3-spike/320-3a-train/`, and `pi/extensions/lib/words.ts` (widened
by the lead after the third gate run below, for the four words entries the
pi words rows require). `internal/scheduler/` was in the declared set and needed no change:
`scheduler.Decision` already carries the fields the decision line uses.
`internal/task/` gained `Store.BindPRFrom` (Codex's second pass on #903,
below); `BindPR` itself is unchanged.

## What happened (the evidence)

The first real run that had to bring a seat branch up to date under the
strict up-to-date rule, run `mt-20261105T192313Z-ab184d07` on PR #894 (task
t88, docs only). From the squad's audit log
(`.agent-mail/v31/extensions/amq-squad/mergetrain/audit.jsonl`, the three
entries of the run that matter, verbatim):

```
{"schema":"amq-squad-merge-audit/1","at":"2026-11-05T19:23:20.853766Z","run":"mt-20261105T192313Z-ab184d07","kind":"batch","repository":"omriariav/amq-squad","base":"v3","base_sha":"87fb71e9b21b5192385edf39543c8545ef5a278d","pairs":[{"number":894,"head":"68792071d942003613da13562ed2c7cb1dd34529"}],"digest":"sha256:6e788975de3b4ac8d1b616f870dcfa820798b8539150a0d69b185c02be296938"}
{"schema":"amq-squad-merge-audit/1","at":"2026-11-05T19:23:32.475703Z","run":"mt-20261105T192313Z-ab184d07","kind":"batch","repository":"omriariav/amq-squad","base":"v3","base_sha":"87fb71e9b21b5192385edf39543c8545ef5a278d","pairs":[{"number":894,"head":"2555d34aa77084145bd7b3ca58f252c5178ac55a"}],"digest":"sha256:649911f08b9228ff19f1534b60f0af46bc0163c3bb900329f33808b21ba96ebd"}
{"schema":"amq-squad-merge-audit/1","at":"2026-11-05T19:23:32.772371Z","run":"mt-20261105T192313Z-ab184d07","kind":"refusal","repository":"omriariav/amq-squad","base":"v3","pr":894,"task":"t88","head":"2555d34aa77084145bd7b3ca58f252c5178ac55a","rules":["ci_green","force_push","pr_head_moved"],"detail":"ci_green: head 2555d34aa770: required checks not green: make ci@app15368=queued, make ci=queued; force_push: head was force-pushed 68792071d942 -> 2555d34aa770 by omriariav; pr_head_moved: PR #894 head 2555d34aa770 is not the pr_head 68792071d942 bound to task t88; re-bind through the commit step","gate_id":"merge-7c71656c5704aa8a"}
```

No `rebase`, no `rebind` entry between the two batches: the train planned
#894 at `68792071` on base `87fb71e9` (19:23:20Z), sent
`updatePullRequestBranch` with the head pinned, and the mutation's reply
(`.data.updatePullRequestBranch.pullRequest.headRefOid`, what `GH.Rebase`
returned) was `68792071`, the head as it was. `land()` saw `pushed ==
p.Head` and did nothing: no rebase entry, no patch proof, no rebind, no
BindPR, no waitCI. The check that followed read the PR at `2555d34a` and
refused it on three rules (19:23:32Z).

GitHub's side, read back with the owner's gh (read-only) while this PR was
built:

- the force-push timeline of #894: one `HeadRefForcePushedEvent`,
  `createdAt 2026-11-05T19:23:26Z`, `68792071d942003613da13562ed2c7cb1dd34529
  -> 2555d34aa77084145bd7b3ca58f252c5178ac55a`, actor `omriariav`, ref
  `seat/reviewer-t88` (the activity line the brief names: force_push by
  omriariav at 19:23:25Z, the commit's committer date; the event is stamped
  a second later);
- commit `2555d34a`: committer date `2026-11-05T19:23:25Z`, one parent,
  `87fb71e9b21b5192385edf39543c8545ef5a278d`, the base tip the train pinned;
- commit `68792071`: one parent, `8302baf1b662281a44a071facf0c320ce3f77a84`
  (the v3 tip the seat branched from).

So GitHub performed the rebase 5 s after the mutation answered, onto exactly
the base the train had pinned, and the train, binding the mutation's answer
only, saw none of it. Every later run (dry runs included) refused
`force_push` (`2555d34a` is in no rebase entry, so not in `trainHeads`) and
`pr_head_moved` (the binding still says `68792071`), and the task was
completed, so the seat's commit step could not re-bind. Every seat PR is
behind at train time (merges are serial), so no seat PR could land. The fake
gh of the tests answered the mutation with the rebased head at once, so no
row saw the asynchrony.

## The change

**Item 0, the journal before the mutation.** `land()` writes one
`update_requested` entry (the PR, the task, the bound head, the base tip it
pinned, `updatePullRequestBranch (REBASE) with the head pinned`) before it
calls `Forge.Rebase`. Item 1's read-back completes against it; item 3's
recovery requires it.

**Item 1, the bounded read-back and the proof.** The mutation's answer is
never bound. `Train.readUpdatedHead` (`update.go`) reads the PR back until
its head differs from the pinned one, sleeping 1 s, 2 s, 4 s, 8 s, 16 s, 16
s, ... between reads (the last sleep clamped to what remains), bounded by
`Train.UpdateWait` (default `DefaultUpdateWait`, 120 s). A wait that ends
with the head unmoved (an update GitHub rejected asynchronously, a conflict,
leaves the head where it was), or whose context ended (the sleep returns at
`ctx.Done`; Codex's second pass on #903), is a refusal in words, `GitHub did not update
the branch of PR #N within 2m0s (the head did not move); nothing is bound and
nothing is merged`, under the rule `branch_update_unconfirmed`, with no
gate raised and nothing bound (`refuseUpdate`, the generalization of the old
`refusePatchProof`). The command prints it as `#N was not merged: <the
words>` and then `rule: branch_update_unconfirmed` and `head:` below it.
The bound is on the waiting, not a cutoff on the read: a read that starts
before the deadline may complete up to one poll after it, and a head that
moved by then is accepted. A mutation that fails outright (`Forge.Rebase`
returns an error) writes an `update_failed` entry (the PR, the bound head,
the error) and stops the run; `lastUpdateRequest` drops a request followed
by it, so a seat's own push after a request GitHub never took is never
recovered as the train's (Codex on #903).

The head read back is proved before it is bound: `Forge.FirstParent` (new;
`gh api repos/{r}/commits/{sha} --jq '.parents[0].sha // ""'`) must be the
base tip the train rebased onto (`afterBase`, read after the update, or
`beforeBase`, the pinned one, when the base moved on since; the commit step
makes one commit per task with `commit-tree -p <base>`, so a rebased seat
head has exactly one commit on the tip), and its patch-id against that
parent must equal the bound head's (the existing proof, unchanged in shape;
`PatchID(parent, pushed)` in place of `PatchID(afterBase, pushed)`, the same
compare since the merge base is the parent). A head on any other parent is a
push after the update, never the update's result: refused `pr_head_moved`
in words through `refuseUpdate`, nothing bound (the fix3 late-push row
keeps its outcome: refused, the late head not bound).

**Item 2, the rebase entry.** Written with the head read back, so
`trainHeads` names it and the force-push rule stays quiet on the train's
own update. For a bound PR it is written only after the proof has passed
(the plan reviewer's blocker on #903, commit `2f28e5fd`): an entry written
before the proof stayed in the journal when the proof failed, so
`trainHeads` named an unproven head and the next run's force-push rule took
that head for the train's own push, exactly the protection this PR
restores. The order is the rebase entry, the rebind entry, the binding
write, all before the check; an unbound PR (no task, so no proof) keeps
the entry as before and the code says so. The rebind entry's detail keeps
its `patch-id: <id>` first line (`reconcileRebinds` matches it) and adds
`first parent: <sha>`.

**Item 3, the recovery.** `Train.reconcileUpdates` runs at the start of a
real run after `reconcileRebinds`. For a bound PR with a real-run
`update_requested` on record for its bound head (`lastUpdateRequest`,
`audit.go`) whose server head is off the binding, it proves the current
head as the update's result (`proveUpdate`, shared with item 6) and
requires ALL of:

- exactly one force push after the journaled request (the timeline query
  now reads each event's `createdAt`, `ForcePush.At`; the comparison is at
  second precision, the timeline's; an event with no time cannot be placed
  and refuses), and that one from the bound head to the current head;
- the current head's first parent equal to the journaled base tip, or to
  the base's current tip if the base moved since, and the record says which
  (`first parent X is the journaled base tip` / `is the base's current
  tip`; the first matching base names it, so the journaled base wins when
  the two are one commit);
- the patch-id proof: the bound head and the current head against that
  parent, equal.

Then the `rebase` entry (`recovered: the update journaled by run <run>
moved the branch and was not recorded; first parent ... ; one force push
...`), the `rebind` entry, the binding write (`BindPR`), the request item's
bound head moved so the plan check sees it, and CI waited at the recovered
head as after the train's own update. A failing part is one `reconcile`
entry naming it (`... binding not recovered`), the binding stays, and the
check refuses as before. A moved head with no request on record is not
touched: a force push with no train request on record is exactly the event
`pr_head_moved` exists to stop, and the owner's identity on the push does
not tell the train's push from the owner's own hand. The converse holds
too: a journaled request whose mutation never ran (or was rejected),
followed by a seat's own force push from the bound head, is accepted only
when the proof holds, which is safe on content, since the patch-id proof
is what matters, not who pushed; a changed patch in that case is refused
(R3's `a differing patch-id` subtest: the journal entry exists, one force
push from the bound head with a different patch-id, the reconcile entry
says the head is `not its result`, nothing bound). `pushedAfter` compares
at second precision and takes an equal second as after, since the
timeline reports whole seconds and a push in the request's own second is
the earliest the update can land.

**Item 4, the fake gh.** `AsyncUpdate` (PR numbers): the mutation performs
the rebase into `refs/pending/<branch>`, records it as pending and answers
the head as it was; the branch moves (`update-ref`, the force-push event
with its time appended) on the `AsyncViews[n]`-th later `pr view` (default
1). `AsyncRejected`: the mutation answers the head as it was and the branch
never moves; when the update lands, `PushAfterRebase` lands a competing
commit on the rebased head at once and `AlterAfterRebase` has altered the
hunk during the update, as in the synchronous path (Codex on #903: the
failed-proof row runs over this). With `AuditPath` set the mutation records
the audit entry kinds present the moment it is called. `FirstParent` is
answered from the bare repository (`rev-parse <sha>^`). Every force push
the fake records carries `At`.

**Item 6, the lead verb.** `amq-squad merge-train rebind --pr 894 --from
68792071d942003613da13562ed2c7cb1dd34529 --to
2555d34aa77084145bd7b3ca58f252c5178ac55a` (`merge_train_rebind.go`), in the
family of `task widen`: no `--me`; the launch record's decision socket;
`schedproc.SendRebind` with a `RebindRequest` of kind
`amq-squad.mergetrain-rebind.v1` (the socket's whitelist and `apply` know
it; `DecideOptions.Rebind` is the hook, nil refuses `rebind_unsupported`).
The socket proves the sender's process descends from the lead's pane before
it reads a byte, as for every decision: from a seat's process the reply is
`not_lead` and the train is asked nothing. The scheduler's apply step
(`rebindDecision`, wired in `decisionOptions`) runs `mergetrain.Rebind` over
the owner's gh: the one task bound to the PR at `--from` (else
`rebind_unbound`), the PR's head equal to `--to` (else
`rebind_head_mismatch`), then `proveUpdate` with the base candidates the
train pinned for that PR at that head (every real-run `update_requested`
base, every real-run `batch` base whose pairs list the PR at the bound head,
and the base's current tip; for #894 the batch of run ab184d07 at
`87fb71e9`), each failing part `rebind_unproved` with the part in words;
then the `rebase` and `rebind` entries under a run id of their own
(`mt-rebind-<stamp>-<hex>`, detail `recovered by the lead <lead>'s rebind
decision: ...`) and the binding write. The write is `task.Store.BindPRFrom`
(Codex's second pass on #903): it binds only while the task is still bound
at `--from`, checked and written under the store's lock, so a train run
that advanced the binding (and recorded a merge) between the proof and the
write keeps its head; the stale re-bind is refused `rebind_head_mismatch`
naming that the binding moved, and the two entries stay journaled
(`reconcileRebinds` applies a rebind entry only to a task still bound at
its old head, so they bind nothing later). A refusal whose decision line
cannot be written names that in its detail, under the refusal's own code. The decision log carries one line either way:
`merge_train_rebound` (the task, the heads, the parent and what it is, the
patch-id, the force push, the audit run) or `merge_train_rebind_refused`
with the code. The verb prints the proof, or the refusal with the plain line
above the rule and the code and detail below it (brief 3.1.1-3's form);
exit 15 on a refusal.

#894 is this path's live row, run by the lead after this PR merges: the
proof as read above holds (one force push `68792071 -> 2555d34a` by
omriariav, first parent `87fb71e9` = the batch base of run ab184d07, the
docs-only patch unchanged by a clean rebase). The general recovery's live
row comes the first time the new code meets an asynchronous update.

## Rows

| Row | What it proves | Test |
|---|---|---|
| R1 | Against the asynchronous fake (the mutation answers the old head; the branch moves on the second read) a strict real run binds the head read back: `update_requested` (the old head, the base) before `rebase` (old head to the head read back) before `rebind` before `merge`; the patch proof matches at the new head; the task is bound to it; CI is read at least twice at the new head and its first parent is read; one 1 s backoff sleep; the pending update consumed; no refusal (the force-push rule quiet). | `internal/mergetrain` `TestAsyncUpdateBindsTheHeadReadBack` |
| R2 | An update GitHub rejected asynchronously: the wait ends after exactly 120 s of sleeps (1, 2, 4, 8, 16 s, then 16 s steps, the last clamped), the PR is refused under `branch_update_unconfirmed` with the words `GitHub did not update the branch of PR #1 within 2m0s`, no gate, the binding unchanged, nothing merged, the branch unmoved; the journal has the `update_requested` and the refusal entry and no `rebase` or `rebind`. | `internal/mergetrain` `TestUnmovedUpdateIsRefusedInWords` |
| R3 live | The update lands after the bounded wait: the run that asked refuses it unconfirmed; the next run recovers it from the journaled request (the `rebase` entry names `recovered: the update journaled by run <first run>`, then the `rebind`), binds the moved head and lands the PR. | `internal/mergetrain` `TestLateUpdateIsRecoveredByTheNextRun` |
| R3 | The conditions, by hand (the request journaled, the branch rebased and force-pushed in the work clone with the timeline event): with the journal and one push onto the journaled base tip the PR is recovered and merged (rebase, rebind, merge in order); with the base moved since, onto the current tip, recovered and the record says `is the base's current tip`; the same push with no journal entry stays refused `pr_head_moved`, no reconcile attempted, the binding stays; a differing patch-id, a second force push, and a parent that is not the base tip each stay refused with one `reconcile` entry naming the part (`the change moved during the update`, `2 force pushes after the journaled request`, `first parent`) and `binding not recovered`, no `rebind`, nothing merged. | `internal/mergetrain` `TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof` (6 subtests) |
| R4 | Every existing row passes with the synchronous fake (the package's suite). | `internal/mergetrain` (`go test`) |
| proof first | #903's blocker: a failed proof leaves no rebase entry. Over the asynchronous fake, with the hunk altered during the update (`rebase_patch_changed`) and with a competing push landing on the rebased head as the update lands (`pr_head_moved` on the parent), the first run is refused with no `rebase` entry for the PR, `trainHeads` does not name the head, the binding is unchanged, and the next run's check refuses the head on `force_push`; nothing merged. | `internal/mergetrain` `TestFailedProofLeavesNoRebaseEntry` (2 subtests) |
| verb | `mergetrain.Rebind`: proved and bound (the batch base of an earlier run pinned, the base moved on since: the parent is `the base tip of run mt-earlier's batch`, the binding written, the `rebase` and `rebind` entries under an `mt-rebind-` run, the next real run lands the PR with the force-push rule quiet); onto the current tip; each failing part refused with nothing written (not a full SHA `rebind_invalid`; no task at the named head `rebind_unbound`; the PR head not the named head `rebind_head_mismatch`; no force push, two force pushes, a parent that is not a base tip, a differing patch-id, each `rebind_unproved` with the part in words). | `internal/mergetrain` `TestLeadRebindIsProvedBeforeTheBindingIsWritten` (9 subtests) |
| socket | The re-bind kind over the decision socket: applied through the `Rebind` hook from the lead's pane; `not_lead` and nothing applied from any other process; `rebind_unsupported` with no hook. | `internal/schedproc` `TestRebindDecisionIsProvedLikeAWidening` |
| not_lead | Through the verb, with the scheduler's production apply step behind the socket and the train's re-bind stubbed: from a seat's process the reply is `not_lead`, the train is asked nothing, the plain line is above the rule with the code below, exit 15, no decision line. | `internal/cli` `TestMergeTrainRebindFromASeatIsNotLead` |
| applied | From the lead's pane the train is asked with the PR, the heads, the lead and the session's audit log; the verb prints the task, the heads, the parent and what it is, the patch-id and the audit run; the decision log has one `merge_train_rebound` line with the task, the lead, the heads and the parent. | `internal/cli` `TestMergeTrainRebindFromTheLeadIsAppliedAndRecorded` |
| refused | A part of the proof that fails: the verb's plain line names the proof, the code and the failing part sit below the rule, one `merge_train_rebind_refused` line with the code. | `internal/cli` `TestMergeTrainRebindRefusedOnTheProofNamesThePart` |
| shape | `--me` refused, the three flags required, no scheduler a plain error. | `internal/cli` `TestMergeTrainRebindVerbShape` |
| R2 words | The command's text for an unconfirmed update: `#894 was not merged: GitHub did not update ...` first, `rule: branch_update_unconfirmed` below, no code on the plain line. | `internal/cli` `TestMergeTrainPrintsTheUnconfirmedUpdateInWords` |
| stale write | Codex #903 (2): a Binder that advances the task's binding right before the conditional write (a concurrent train run's stand-in): the re-bind is refused `rebind_head_mismatch` naming that the binding moved, the advanced head stays; `BindPRFrom` with the wrong head is `ErrPRBindingMismatch` and writes nothing, with the right head it binds. | `internal/mergetrain` `TestRebindRefusesWhenTheBindingMovedMeanwhile` |
| cancel | Codex #903 (3): the Sleep seam runs the test clock and cancels the context on the first sleep: the wait ends after that one sleep, the PR is refused `branch_update_unconfirmed`, nothing bound or merged (a wait that ignored the cancel would sleep eleven times to the 120 s bound). | `internal/mergetrain` `TestCancelledUpdateWaitIsRefusedUnconfirmed` |
| past the deadline | Codex #903 (3): every PR read takes 61 s of the test clock; the second read-back starts before the 120 s deadline and completes after it, sees the moved head, and the train proves, binds and merges it. | `internal/mergetrain` `TestPollCrossingTheDeadlineAcceptsAMovedHead` |
| failed mutation | Codex #903 (4): a Forge whose `Rebase` fails: the run stops with `rebase: ...`, the journal has `update_requested` then `update_failed` (the error in its detail), `lastUpdateRequest` finds no live request; a seat's own force push with the same patch onto the base tip is then not recovered (no reconcile, rebase or rebind entry, the binding unchanged) and the next run refuses it on `force_push`. A seat push with a changed patch after a live request is R3's `a differing patch-id` subtest. | `internal/mergetrain` `TestFailedMutationLeavesNoLiveRequest` |
| same second | Codex #903 (6): `pushedAfter` at second precision: a push in the request's own second is after it, the second before is not, a sub-second request past the push's second is after the push; no time or an unparseable time is not after. | `internal/mergetrain` `TestPushedAfterTakesTheSameSecondAsAfter` |
| no createdAt | Codex #903 (6): R3's `a force push with no time` subtest: the journal entry present, the one force push with an empty time, the reconcile entry says it `carries no time`, `binding not recovered`. | `internal/mergetrain` `TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof` |
| journal at the mutation | Codex #903 (6): R1 reads the audit kinds the fake saw when the mutation was called: `update_requested` present, no `rebase`. | `internal/mergetrain` `TestAsyncUpdateBindsTheHeadReadBack` |
| journaled base first | Codex #903 (8): a real-run batch base that is also the base's current tip names the parent as `the base tip of run mt-earlier's batch`. | `internal/mergetrain` `TestLeadRebindIsProvedBeforeTheBindingIsWritten` (`a journaled base that is also the current tip ...`) |
| unwritten line | Codex #903 (5): the decisions log's path taken by a directory: the verb's refusal keeps its code and names `the decision line was not written: ...`. | `internal/cli` `TestMergeTrainRebindNamesAnUnwrittenDecisionLine` |

Rule 18: every row runs under a `t.TempDir` root; the mergetrain rows run
the fake gh over a local bare repository under the test binary's temporary
`HOME` (`TestMain`), the schedproc and cli rows bind the decision socket
under a temp root; no network, no real session root, nothing of the owner's
read. The evidence above was read from the real audit log and from GitHub
with read-only calls only; no writing gh command ran against the
repository.

## Revert runs

Each run on the committed tree `07b58d4d` (the two code commits): the line
flipped, the row run alone, the first failing line verbatim (SHAs as the
fixture produced them), then `git checkout -- <file>` and the row re-run
green. The runner is kept in the job scratch, not committed. A flip is a
compiling inverse of the line's rule.

### R1 the read-back loop

```
go test -count=1 -timeout 300s -run ^TestAsyncUpdateBindsTheHeadReadBack$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/update.go` (`readUpdatedHead`)
```
		if cur.HeadSHA != pinned {
```
to:
```
		if cur.HeadSHA != pinned || true { // the first read taken as the update's result
```
First failing line:
```
async_test.go:96: the head read back was not proved and merged: {Number:1 Task:t1 Head:78a7ef1cfc12239356eef5d660733c311c13a454 Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:pr_head_moved Detail:the head read back after the update, 78a7ef1cfc12, is not the update's result: its first parent f40014816471 is not the base tip d47eaff9cf08 the train rebased onto (a push after the update); nothing is bound; re-bind through the commit step}] GateID: GateError: Lifted:false After:[] RebaseForCI:true RebaseRule:strict_required_status_checks_policy RebaseReason:GitHub requires the branch to be up to date before merging PatchProof:0x5865ce62a500 Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}
```
`--- FAIL: TestAsyncUpdateBindsTheHeadReadBack (0.78s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	1.278s`.

### R1 the journal (item 0)

```
go test -count=1 -timeout 300s -run ^TestAsyncUpdateBindsTheHeadReadBack$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/train.go` (`land`)
```
		if err := entry(Entry{Kind: "update_requested", PR: p.Number,
```
to:
```
		if err := entry(Entry{Kind: "update_not_journaled", PR: p.Number,
```
First failing line:
```
async_test.go:122: entry order (requested -1, rebase 3, rebind 4, merge 7): [batch update_not_journaled rebase rebind batch merge_intent merge]
```
`--- FAIL: TestAsyncUpdateBindsTheHeadReadBack (1.02s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	1.259s`.

### R2 the bound

```
go test -count=1 -timeout 300s -run ^TestUnmovedUpdateIsRefusedInWords$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/train.go` (`land`)
```
		if !moved {
```
to:
```
		if !moved && false { // the wait's end taken as the update's result
```
First failing line:
```
async_test.go:155: the unmoved update was not refused in words: {Number:1 Task:t1 Head:9203d4babbcd23242f0991413a386da802b13986 Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:pr_head_moved Detail:the head read back after the update, 9203d4babbcd, is not the update's result: its first parent d1ea61e1c9b3 is not the base tip f74e1e0ceea2 the train rebased onto (a push after the update); nothing is bound; re-bind through the commit step}] GateID: GateError: Lifted:false After:[] RebaseForCI:true RebaseRule:strict_required_status_checks_policy RebaseReason:GitHub requires the branch to be up to date before merging PatchProof:0x3073e25a2050 Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}
```
`--- FAIL: TestUnmovedUpdateIsRefusedInWords (0.86s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	1.093s`.

### R3 live the recovery

```
go test -count=1 -timeout 300s -run ^TestLateUpdateIsRecoveredByTheNextRun$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/update.go` (`reconcileUpdates`)
```
	if t.Binder == nil || t.Tasks == nil {
```
to:
```
	if t.Binder == nil || t.Tasks == nil || true {
```
First failing line:
```
async_test.go:199: the moved branch was not recovered and merged: <nil> [{Number:1 Task:t1 Head:119c6f89e2d7781930e41114416e78ca93f4383a Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed ad5e2c597208 -> 119c6f89e2d7 by owner} {Rule:pr_head_moved Detail:PR #1 head 119c6f89e2d7 is not the pr_head ad5e2c597208 bound to task t1; re-bind through the commit step}] GateID:merge-097990fecdb03bfa GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}]
```
`--- FAIL: TestLateUpdateIsRecoveredByTheNextRun (0.97s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	1.557s`.

### R3 the journal condition

```
go test -count=1 -timeout 300s -run ^TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/update.go` (`reconcileUpdates`)
```
		if !ok {
			continue
		}
```
to:
```
		if !ok {
			requested.At = "1970-01-01T00:00:00Z" // a push with no request on record recovered
		}
```
First failing line:
```
async_test.go:300: recovered without the proof: {Number:1 Task:t1 Head:237809e9c19a18a0207f5a4292df5540103ccc80 Draft:true Changed:2 Files:[pkg/a.go] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:merged Merged:954b8d9a4123cedbae026e811c3bef889fd5ced2 title: mergeable:MERGEABLE} (bound 237809e9c19a18a0207f5a4292df5540103ccc80)
```
(the subtest `the same push with no journal entry`)
`--- FAIL: TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof (5.39s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	5.356s`.

### R3 the one force push

```
go test -count=1 -timeout 300s -run ^TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/update.go` (`proveUpdate`)
```
	case len(after) > 1:
```
to:
```
	case len(after) > 2:
```
First failing line (the subtest `a second force push`; the first push is
then taken and refused for its target, not the count):
```
async_test.go:310: the refusal's record does not name the failing part "2 force pushes after the journaled request": 2 [{amq-squad-merge-audit/1 2026-11-05T20:13:18.653856Z mt-earlier update_requested owner/fixture main 29f16f2fbc1c9772e21be6f0142ed089d117b679 []  1 t1 9bd54af81b466c1bbf155ff77cead8ecf76dded9   []   false} {amq-squad-merge-audit/1 2026-11-05T20:13:18.835228Z mt-20261105T201318Z-5666b9ba protection owner/fixture main  []  0     [ruleset 7] ok  false} {amq-squad-merge-audit/1 2026-11-05T20:13:18.855154Z mt-20261105T201318Z-5666b9ba reconcile owner/fixture main  []  1 t1 9bd54af81b466c1bbf155ff77cead8ecf76dded9 290fd0e2f7d9a89a2688dc32e522e719677b4d6c  [] the update journaled by run mt-earlier was not recorded, and the current head is not its result: the one force push after the journaled request is 9bd54af81b46 -> ca798fa659dd, not from the bound head 9bd54af81b46 to the current head 290fd0e2f7d9
```
`--- FAIL: TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof (5.13s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	5.329s`.

### R3 the parent

```
go test -count=1 -timeout 300s -run ^TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/update.go` (`proveUpdate`)
```
	if proof.ParentIs == "" {
```
to:
```
	if proof.ParentIs == "" && false {
```
First failing line (the subtest `a parent that is not the base tip`; with
the parent unchecked the patch-id against the wrong parent catches it, under
the wrong words):
```
async_test.go:310: the refusal's record does not name the failing part "first parent": 2 [{amq-squad-merge-audit/1 2026-11-05T20:13:31.507571Z mt-earlier update_requested owner/fixture main 1c05fa0376feeb6499a353c65ce3947d5a0f99d2 []  1 t1 5d6ae98b9468f7c1094466f3d9bac2ec7cec1ed9   []   false} {amq-squad-merge-audit/1 2026-11-05T20:13:31.646863Z mt-20261105T201331Z-453ec30f protection owner/fixture main  []  0     [ruleset 7] ok  false} {amq-squad-merge-audit/1 2026-11-05T20:13:31.696542Z mt-20261105T201331Z-453ec30f reconcile owner/fixture main  []  1 t1 5d6ae98b9468f7c1094466f3d9bac2ec7cec1ed9 c7fe0287c960ca0853c596a97aa6044334606cf4  [] the update journaled by run mt-earlier was not recorded, and the current head is not its result: the change moved during the update: patch-id bbf38924e8ea545a3ea83c3d7225f5f52bf494ca at the bound head, c11eef0577967fd72c1945d2c46ac6f17e223f0e at the current head
```
`--- FAIL: TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof (5.20s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	5.383s`.

### R3 the patch-id

```
go test -count=1 -timeout 300s -run ^TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/update.go` (`proveUpdate`)
```
	if proof.OldID != proof.NewID {
```
to:
```
	if proof.OldID != proof.NewID && false {
```
First failing line (the subtest `a differing patch-id`):
```
async_test.go:300: recovered without the proof: {Number:1 Task:t1 Head:0e971dc527b7401e2a4c4c7a64a50a2332ed144a Draft:true Changed:4 Files:[pkg/a.go] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:merged Merged:331f15bcc8bd5dc99300fc8574021cad553c8d7a title: mergeable:MERGEABLE} (bound 0e971dc527b7401e2a4c4c7a64a50a2332ed144a)
```
`--- FAIL: TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof (5.30s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	5.344s`.

### verb the bound head

```
go test -count=1 -timeout 300s -run ^TestLeadRebindIsProvedBeforeTheBindingIsWritten$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/rebind.go` (`Rebind`)
```
		if t.PRNumber == in.PR && t.PRHead == in.From {
```
to:
```
		if t.PRNumber == in.PR { // the bound head not checked
```
First failing line (the subtest `no task bound at the named head`):
```
async_test.go:424: want rebind_unbound with "0 tasks are bound to PR #1 at head aaaaaaaaaaaa", got rebind refused (rebind_unproved): the one force push is ecda86c6cd94 -> 4df6b76fe335, not from the bound head aaaaaaaaaaaa to the current head 4df6b76fe335
```
`--- FAIL: TestLeadRebindIsProvedBeforeTheBindingIsWritten (6.60s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	7.217s`.

### verb the named head

```
go test -count=1 -timeout 300s -run ^TestLeadRebindIsProvedBeforeTheBindingIsWritten$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/rebind.go` (`Rebind`)
```
	if cur.HeadSHA != in.To {
```
to:
```
	if cur.HeadSHA != in.To && false {
```
First failing line (the subtest `the PR head is not the named head`):
```
async_test.go:424: want rebind_head_mismatch with "PR #1's head is", got rebind refused (rebind_unproved): the one force push is 8aa6ae329cce -> d9e04cf93cae, not from the bound head 8aa6ae329cce to the current head aaaaaaaaaaaa
```
`--- FAIL: TestLeadRebindIsProvedBeforeTheBindingIsWritten (6.62s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	6.913s`.

### verb the proof before the write

```
go test -count=1 -timeout 300s -run ^TestLeadRebindIsProvedBeforeTheBindingIsWritten$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/rebind.go` (`Rebind`)
```
	if why != "" {
```
to:
```
	if why != "" && false {
```
First failing line (the subtest `no force push from the bound head`):
```
async_test.go:424: want rebind_unproved with "no force push; the bound head", got <nil>
```
`--- FAIL: TestLeadRebindIsProvedBeforeTheBindingIsWritten (6.81s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	6.970s`.

### socket the kind

```
go test -count=1 -timeout 300s -run ^TestRebindDecisionIsProvedLikeAWidening$ ./internal/schedproc/
```
Reverted `internal/schedproc/decide.go` (`serve`, the kinds the socket reads)
```
 || kind.Kind == RebindRequestKind) {
```
to:
```
) {
```
First failing line:
```
rebind_test.go:51: the lead's re-bind: <nil> {OK:false Code:decision_malformed Detail:no decision Data:null} (applied [])
```
`--- FAIL: TestRebindDecisionIsProvedLikeAWidening (0.00s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.373s`.

### not_lead

```
go test -count=1 -timeout 300s -run ^TestMergeTrainRebindFromASeatIsNotLead$ ./internal/cli/
```
Reverted `internal/schedproc/decide.go` (`origin`, the proof every decision passes)
```
	if pane == nil {
```
to:
```
	if pane == nil && false {
```
First failing line:
```
merge_train_rebind_test.go:91: the train was asked
```
`--- FAIL: TestMergeTrainRebindFromASeatIsNotLead (0.00s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.448s`.

### verb the decision line

```
go test -count=1 -timeout 300s -run ^TestMergeTrainRebindFromTheLeadIsAppliedAndRecorded$ ./internal/cli/
```
Reverted `internal/cli/merge_train_rebind.go` (`rebindDecision`)
```
	if err := schedproc.AppendDecision(sel.root, now, scheduler.Decision{Kind: "merge_train_rebound", TaskID: res.Task, Seat: lead,
```
to:
```
	if err := schedproc.AppendDecision(sel.root+"/elsewhere", now, scheduler.Decision{Kind: "merge_train_rebound", TaskID: res.Task, Seat: lead, // the decision log of this root carries no line
```
First failing line:
```
merge_train_rebind_test.go:135: the decision line: []
```
`--- FAIL: TestMergeTrainRebindFromTheLeadIsAppliedAndRecorded (0.00s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.311s`.

### verb the code below the rule

```
go test -count=1 -timeout 300s -run ^TestMergeTrainRebindRefusedOnTheProofNamesThePart$ ./internal/cli/
```
Reverted `internal/cli/merge_train_rebind.go` (`rebindReplyRefusal`)
```
	return fmt.Sprintf("merge-train rebind #%d was refused: %s\n\n%s\nrefused (%s): %s", pr, plain, gatestore.Rule, reply.Code, reply.Detail)
```
to:
```
	return fmt.Sprintf("merge-train rebind #%d was refused (%s): %s\n\n%s\n%s", pr, reply.Code, plain, gatestore.Rule, reply.Detail)
```
First failing line:
```
merge_train_rebind_test.go:156: the plain line: "merge-train rebind #894 was refused (rebind_unproved): the named head is not proved to be the train's update of the bound head; nothing is bound"
```
`--- FAIL: TestMergeTrainRebindRefusedOnTheProofNamesThePart (0.00s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.331s`.

### R2 the words

```
go test -count=1 -timeout 300s -run ^TestMergeTrainPrintsTheUnconfirmedUpdateInWords$ ./internal/cli/
```
Reverted `internal/cli/merge_train.go` (`printMergeTrain`)
```
return f.Rule == mergetrain.RuleUpdateUnconfirmed }); i >= 0 {
```
to:
```
return f.Rule == mergetrain.RuleUpdateUnconfirmed }); i >= 0 && false {
```
First failing line:
```
merge_train_rebind_test.go:198: the refusal is not in words with the code below the rule:
```
`--- FAIL: TestMergeTrainPrintsTheUnconfirmedUpdateInWords (0.00s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.323s`.

### proof first (#903)

On `2f28e5fd`:
```
go test -count=1 -timeout 300s -run ^TestFailedProofLeavesNoRebaseEntry$ ./internal/mergetrain/
```
Reverted `internal/mergetrain/train.go` (`land`, the entry's write)
```
		if !bound {
```
to:
```
		if true { // the old order: the entry before the proof
```
First failing line (the subtest `a differing patch-id`):
```
async_test.go:460: a rebase entry names the unproven head: [batch update_requested rebase refusal batch]
```
`--- FAIL: TestFailedProofLeavesNoRebaseEntry (1.65s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/mergetrain	2.370s`.

### Codex's second pass on #903 (seven runs, on `f4a7cbe3`)

Each: the flip, the row's command, the first failing line, restored `ok`.

`903b the conditional write`: `internal/mergetrain/rebind.go`
`in.Binder.BindPRFrom(tk.ID, repository, in.PR, in.From, in.To)` to
`in.Binder.BindPR(tk.ID, repository, in.PR, in.To)`;
`go test -count=1 -timeout 300s -run ^TestRebindRefusesWhenTheBindingMovedMeanwhile$ ./internal/mergetrain/`;
```
async_test.go:522: the stale re-bind was not refused: <nil>
```
restored: `ok ... 1.023s`.

`903b the cancellation`: `internal/mergetrain/update.go` (`sleepOrDone`,
the seam path) `return ctx.Err() == nil` to `return true`;
`go test -count=1 -timeout 300s -run ^TestCancelledUpdateWaitIsRefusedUnconfirmed$ ./internal/mergetrain/`;
```
async_test.go:563: the cancelled wait: <nil> [{Number:1 Task:t1 Head:5f7d5a35c6c409363b944eade06fde8d5b55e983 Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:branch_update_unconfirmed Detail:GitHub did not update the branch of PR #1 within 2m0s (the head did not move); nothing is bound and nothing is merged}] GateID: GateError: Lifted:false After:[] RebaseForCI:true RebaseRule:strict_required_status_checks_policy RebaseReason:GitHub requires the branch to be up to date before merging PatchProof:0x417eee2fcb90 Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}] (sleeps 11)
```
restored: `ok ... 1.059s`.

`903b the poll past the deadline`: `internal/mergetrain/update.go`
(`readUpdatedHead`) `if cur.HeadSHA != pinned {` to
`if cur.HeadSHA != pinned && t.now().Before(deadline) {` (a cutoff on the
read); `go test -count=1 -timeout 300s -run ^TestPollCrossingTheDeadlineAcceptsAMovedHead$ ./internal/mergetrain/`;
```
async_test.go:583: the head read past the deadline was not accepted: <nil> [{Number:1 Task:t1 Head:a3cabf8ff4423521a5ddd07ba3ad39b5e87849de Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:branch_update_unconfirmed Detail:GitHub did not update the branch of PR #1 within 2m0s (the head did not move); nothing is bound and nothing is merged}] GateID: GateError: Lifted:false After:[] RebaseForCI:true RebaseRule:strict_required_status_checks_policy RebaseReason:GitHub requires the branch to be up to date before merging PatchProof:0x31d190f4460 Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}]
```
restored: `ok ... 1.238s`.

`903b update_failed`: `internal/mergetrain/audit.go` (`lastUpdateRequest`)
`case "update_failed":` to `case "update_failed_ignored":`;
`go test -count=1 -timeout 300s -run ^TestFailedMutationLeavesNoLiveRequest$ ./internal/mergetrain/`;
```
async_test.go:620: the failed request is still live
```
restored: `ok ... 1.181s`.

`903b the decision-log failure`: `internal/cli/merge_train_rebind.go`
(`rebindDecision`) `detail += "\nthe decision line was not written: " + aerr.Error()`
to `_ = aerr`; `go test -count=1 -timeout 300s -run ^TestMergeTrainRebindNamesAnUnwrittenDecisionLine$ ./internal/cli/`;
```
merge_train_rebind_test.go:220: the refusal does not name the unwritten decision line: merge-train rebind #894 was refused: the named head is not proved to be the train's update of the bound head; nothing is bound
```
restored: `ok ... 0.424s`.

`903b no createdAt`: `internal/mergetrain/update.go` (`proveUpdate`)
`if fp.At == "" {` to `if fp.At == "" && false {`;
`go test -count=1 -timeout 300s -run ^TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof$ ./internal/mergetrain/`
(the subtest `a force push with no time`; the push with no time is then
dropped as "before" and the words are the wrong ones);
```
async_test.go:325: the refusal's record does not name the failing part "carries no time": 2 [{amq-squad-merge-audit/1 2026-11-05T21:27:38.286024Z mt-earlier update_requested owner/fixture main d1a4a07309a95fc3514b2855f7fa20efa01416ac []  1 t1 93bb6a8c1e3a5aa382b45feaede7f82e4310a832   []   false} {amq-squad-merge-audit/1 2026-11-05T21:27:38.404468Z mt-20261105T212738Z-3d02de06 protection owner/fixture main  []  0     [ruleset 7] ok  false} {amq-squad-merge-audit/1 2026-11-05T21:27:38.425064Z mt-20261105T212738Z-3d02de06 reconcile owner/fixture main  []  1 t1 93bb6a8c1e3a5aa382b45feaede7f82e4310a832 9c7e4ad86474a8a9971476a6b64dcc106d423ad2  [] the update journaled by run mt-earlier was not recorded, and the current head is not its result: no force push after the journaled request; the bound head 93bb6a8c1e3a was not moved by an update
```
restored: `ok ... 6.150s`.

`903b the journaled base first`: `internal/mergetrain/update.go`
(`proveUpdate`) the `break` after `proof.ParentIs = b.which` removed (the
last match wins); `go test -count=1 -timeout 300s -run ^TestLeadRebindIsProvedBeforeTheBindingIsWritten$ ./internal/mergetrain/`
(the subtest `a journaled base that is also the current tip is named as journaled`);
```
async_test.go:396: <nil> {Run:mt-rebind-20261105T212748Z-2ba10090 Task:t1 Repository:owner/fixture Base:main PR:1 From:dd47a5857d185fa2a1ecbfb56bf0be4bd22ec40f To:ab334a7b8d85c4a64eab6ee314ae23593b26d608 Parent:d436c0c56bf40c547b52908e1163d9e6789428f1 ParentIs:the base's current tip PatchID:bbf38924e8ea545a3ea83c3d7225f5f52bf494ca Push:dd47a5857d185fa2a1ecbfb56bf0be4bd22ec40f -> ab334a7b8d85c4a64eab6ee314ae23593b26d608 by owner}
```
restored: `ok ... 7.963s`.

R4 has no revert: it is the package's existing rows, run green on the new
code (the gates below).

## Gates

On `07b58d4d` (the two code commits; the head after this note's commit has
the same Go tree), from the worktree root:

- `go build ./... && go vet ./...`: exit 0.
- `go test -count=1 ./internal/mergetrain/ ./internal/cli/ ./internal/schedproc/
  ./internal/scheduler/ ./internal/task/`: all five `ok` (mergetrain 86 s,
  cli 76 s, schedproc 43 s, scheduler 31 s, task 42 s), exit 0.
- `NPM_FLAGS=--offline make ci`, three runs, none exit 0:
  1. exit 2 at `skills-check`: `fmt-check`, `vet` and `test` (56 packages
     `ok`, no `FAIL`) passed first; the host's `python3` (homebrew 3.15,
     3.11 and `/usr/bin/python3` alike) has no PyYAML, which
     `scripts/generate-plugin-skills.py` needs. A venv with pyyaml 6.0.3 was
     made under the job scratch (not the tree) for the next runs.
  2. (venv first on PATH) exit 2 at `test`: one row outside this PR's files,
     `internal/seatlaunch` `TestPiSeatPlansThroughTheUpstreamAdapter`
     (`amqpi_test.go:75: outcome unsupported (launcher_not_available)`, a
     5 s budget row) failed under the full parallel run on a host loaded
     with other agents' test runs; it passed in run 1, it passes alone
     (`ok ... 2.112s`), and nothing here touches `internal/seatlaunch/`.
     Every other package was `ok`.
  3. (venv first on PATH) exit 2 at `pi-test`, the last target: `fmt-check`,
     `vet`, `test` (70 packages `ok`, no `FAIL`), the readme and docs html
     checks, the skill checks, `release-validator-test`,
     `go-files-scope-test` and `claude-hooks-test` all passed; `pi-test`
     ran `ℹ tests 116`, `ℹ pass 113`, `ℹ fail 2`, `ℹ skipped 1`. The two
     rows are the pi words table's completeness rows
     (`pi/test/words.test.ts` row 7): `train rule branch_update_unconfirmed
     has no words` and `decision kind merge_train_rebind_refused is neither
     a stop with words nor routine`. They are this PR's doing: the rows read
     every train rule, every audit entry kind and every decision kind from
     the Go sources and require plain words for each in
     `pi/extensions/lib/words.ts`, and this PR adds the rule
     `branch_update_unconfirmed`, the entry kind `update_requested` and the
     decision kinds `merge_train_rebound` and `merge_train_rebind_refused`.
     `pi/` is outside this PR's declared files, so the entries are not
     added here: they are reported to the lead as the one thing the brief's
     gates need outside its set (four table entries: the rule's two lines
     under `TRAIN_RULE_WORDS`, the kind's line under `TRAIN_KIND_WORDS`,
     `merge_train_rebound` under `ROUTINE_DECISIONS` and
     `merge_train_rebind_refused` as a stop under `DECISION_WORDS`, or the
     lead's ruling on where each goes).
  4. The lead widened the declared set to `pi/extensions/lib/words.ts` for
     exactly those four entries (commit `2b80bcd1`: the rule's two lines
     `GitHub did not update the branch in time` / `run the train again; if
     it repeats, ask the owner whether the update conflicts`; the kind's
     line `asked GitHub to bring a PR up to date`; `merge_train_rebound` a
     routine record; `merge_train_rebind_refused` a stop naming the lead Go
     records as the seat, `read the proof's failing part below the line`).
     On `2b80bcd1`, venv first on PATH: exit 0; `fmt-check`, `vet`, `test`
     (70 packages `ok`, no `FAIL`), the readme and docs html checks, the
     skill checks, `release-validator-test`, `go-files-scope-test`,
     `claude-hooks-test` and `pi-test` (`ℹ tests 116`, `ℹ pass 115`,
     `ℹ fail 0`, `ℹ skipped 1`, the staged-runtime row, local-only).
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-3a-train/`:
  exit 0 (run again on this note's final text: exit 0).

On `2f28e5fd` (#903's blocker, `internal/mergetrain/` only), from the
worktree root: `go build ./... && go vet ./...` exit 0; `go test -count=1
./internal/mergetrain/ ./internal/cli/ ./internal/schedproc/` all three
`ok`, exit 0; `go run ./scripts/redact-evidence -check
docs/v3-spike/320-3a-train/` on this note: exit 0. No further
offline `make ci` (GitHub's CI is the full run, the lead's ruling).

On `f4a7cbe3` (Codex's second pass on #903: `internal/mergetrain/`,
`internal/task/` (`BindPRFrom`), `internal/cli/`): `go build ./... && go
vet ./...` exit 0; `go test -count=1 ./internal/mergetrain/
./internal/cli/ ./internal/schedproc/ ./internal/task/` all four `ok`,
exit 0; `go run ./scripts/redact-evidence -check
docs/v3-spike/320-3a-train/` on this note: exit 0.

## Residual risk

- The first-parent check assumes one commit per task on the seat branch,
  which is what the commit step produces (`commit-tree -p <base>`, one
  commit, re-pushed in place); a PR whose branch carries more than one
  commit is refused `pr_head_moved` after its update, with the words naming
  the parent, and lands only through the owner's gate. The compare-based
  patch proof is unchanged.
- The recovery places the force push after the journaled request at second
  precision (the timeline's `createdAt`); a request and a push inside the
  same second are "after". A seat's own force push inside that second,
  onto exactly the pinned base with exactly the bound head's patch, would be
  recovered as the train's: the proof holds the same content either way.
- The bounded wait is wall time in the train's process, and bounds the
  waiting, not the read: a read that starts before the deadline may
  complete up to one poll after it. A run whose process dies inside the
  wait leaves the journaled request, which the next run recovers from or,
  with the head unmoved, ignores (the check decides); a mutation that
  failed outright is marked `update_failed` and recovers nothing.
- A stale re-bind refused on the conditional write leaves its `rebase` and
  `rebind` entries journaled under the re-bind's run: the proof held on
  content, the binding was simply elsewhere, and `reconcileRebinds` applies
  a rebind entry only to a task still bound at its old head.
- The lead verb's base candidates are the journaled bases first, then the
  base's current tip; the first match names the parent (a journaled base
  that is also the current tip is named as journaled), and a parent that
  equals only the current tip is accepted on the proof alone (one force
  push, equal patch).
- The scheduler's apply step runs the owner's `gh` from the scheduler's
  process environment; a scheduler started without `gh` on its PATH refuses
  every re-bind `rebind_failed` with gh's error, and the decision line
  records it.
