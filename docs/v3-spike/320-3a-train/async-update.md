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
pi words rows require). This PR's `internal/schedproc/` changes are
`decide.go` (the re-bind decision kind) and `rebind_test.go`, both from
brief item 6 (`git diff d522a066 HEAD -- internal/schedproc/` names
exactly these two); the merge of `origin/v3` (`675508d4`) brought only
`liveness2_test.go` (PR #905's test-only fix), and no commit of this PR's
own past `a097f8e1` touches `internal/schedproc/`. `internal/scheduler/`
was in the declared set and needed no change:
`scheduler.Decision` already carries the fields the decision line uses.
`internal/task/` gained `Store.BindPRFrom` (Codex's second pass on #903,
below); `BindPR` itself is unchanged. The pi words table carries every
audit entry kind this PR adds (`update_requested`, `update_failed`,
`update_ambiguous`, `rebind_superseded`, `rebind_readmitted`), the rule
`branch_update_unconfirmed` and the two decision kinds, since its row 7
reads them from the Go sources.

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
moved by then is accepted. Each read runs under its own context bounded by
the remaining wait (`context.WithTimeout`, Codex's re-pass on #903), so a
stalled gh returns within the deadline (`GH.exec` uses
`exec.CommandContext`, which kills it, and `WaitDelay` 2 s, commit
`e058974a`, so a child of gh's holding its pipes past the kill, a
credential helper or a pager, does not hold the read: the bound row's
stand-in found `Output` waiting 30 s for the child); a read that times
out counts as unmoved for that poll, decided by the read context's own
deadline, whatever gh's error says (Codex's third pass, commit
`d8c0e0f8`: a gh killed by its context returns an exit error, not a
context error, so the first form ended a stalled production read with
that error instead of the unconfirmed refusal). The command runs the whole train
under `mergetrain.RunBound(n)`, per PR the update wait, the CI wait and
ten minutes, so a stalled call anywhere ends the run instead of hanging
it, and says so in words (commit `bf4c6458`): `merge-train: the run timed
out after <bound>, a call to GitHub did not return; nothing more was
merged, and the next run starts from the journal` above the rule,
`refused (run_timeout): <the call's error>` below.

**A failed mutation, two classes** (the plan reviewer's split of Codex's
item 4 on #903, commit `c26b17cc`). A mutation can fail on this side while
GitHub completed it, so retiring the request on every failure would leave a
real update unrecoverable, another #894. `definiteRefusal` reads gh's
error text (`GH.run` wraps it as `gh api graphql: <exit>: <stderr>`):

- definite, `update_failed`, the request retired (`lastUpdateRequest`
  drops a request followed by it, so a seat's own push after it is never
  recovered as the train's): an HTTP 4xx (`gh: <message> (HTTP 4xx)`:
  422 Validation Failed, 403 Forbidden, 404, 409), or a GraphQL error in
  gh's wrapper whose message is a known refusal from the closed table
  `graphQLRefusals` in `update.go` (each entry GitHub's exact message with
  its source in a comment, the plan reviewer's clarification on #903,
  commit `bf4c6458`). The match (the plan reviewer's rulings on Codex's
  third pass, commits `77a728cf` and `b9964e30`) strips one of gh's two
  documented wrappers, each only as a whole, and compares the rest
  exactly, case aside: `GH.run` returns a `GHError` that carries gh's
  stderr as printed (its error text unchanged), `definiteRefusal` reads
  gh's last stderr line only, and `refusalMessage` removes either
  wrapper; the rest must equal a table entry (`strings.EqualFold`). The
  two wrappers, read on gh 2.100.0 (whose `go.mod` pins go-gh v2.16.0),
  each measured with one read-only query of a PR that does not exist:
  - `gh: <message>`, the line `gh api graphql` prints, which is
    `GH.Rebase`'s path: cli/cli v2.100.0 `pkg/cmd/api/api.go`,
    `parseErrorResponse` joins the body's `errors[].message` with `\n`
    (line 722) and `processResponse` prints
    `fmt.Fprintf(opts.IO.ErrOut, "gh: %s\n", serverError)` (line 557).
    Measured: `gh api graphql -f
    query='query{repository(owner:"omriariav",name:"amq-squad"){pullRequest(number:999999){id}}}'`
    printed on stderr `gh: Could not resolve to a PullRequest with the
    number of 999999.` (the body, with `"path":["repository","pullRequest"]`,
    on stdout).
  - `GraphQL: <message> (<path>)`, the form gh's other commands print:
    cli/go-gh v2.16.0 `pkg/api/errors.go`, `GraphQLError.Error` (lines
    59-68) writes `GraphQL: ` and each error as `<message> (<path>)`, the
    path's elements joined by `.` (`pathString`, line 82), several errors
    joined by `, `. Measured: `gh pr view 999999 -R omriariav/amq-squad
    --json id` printed `GraphQL: Could not resolve to a PullRequest with
    the number of 999999. (repository.pullRequest)`.

  "Whole" means a GraphQL line must carry its path (a pathless one is not
  the second form). A fragment of a table message inside other text,
  anything extra before or after a wrapper, a line after gh's, several
  errors (gh api's newline, go-gh's `, `) and a `gh: HTTP <code>` line
  are ambiguous, never definite. The format belongs to gh's version: these
  are gh 2.100.0's wrappers, and a gh that changes them falls to
  ambiguous, the safe side (the request stays live and recovers only a
  proved push inside its window), never to definite. The table: `Head
  branch was modified. Review and try the update again.` (GitHub's
  message when `expectedHeadOid` is no longer the head; the REST "Update a
  pull request branch" page documents the 422 with this body, and the
  mutation answers the same text) and `Resource not accessible by
  integration` (GitHub's GraphQL error for a token short of access). A
  conflict's and a forbidden's GraphQL wordings are not evidenced exactly
  and are not in the table (ambiguous: the request stays live, bounded by
  its window; the HTTP 403 form is the 4xx rule). The fake gh prints a
  sentinel line of its own (`fake: expected head X, the head is Y`) and
  then the first message in `gh api graphql`'s real form, `gh: Head
  branch was modified. Review and try the update again.`, as its last
  line, so it is classed by the same rule through its own exact output
  and its sentinel has no table entry; the failed-mutation row runs it
  through `GH.Rebase` and `GH.run`, the production path, and ends
  `update_failed`. The run stops with `rebase: <error>`. Only
  these are definite (Codex's re-pass on #903, commit `1491a9f1`: the first
  form took any other GraphQL wording for a refusal and would have retired
  a request GitHub had completed). A new refusal GitHub documents is one
  table line with its exact wording and source.
- ambiguous, `update_ambiguous`, the request stays live, everything else:
  an HTTP 5xx (`(HTTP 500)`, `(HTTP 502)`, `(HTTP 503)`), a transport
  error (`Post "https://api.github.com/graphql": context deadline
  exceeded`, `error connecting to api.github.com`, a reset or EOF), gh
  killed or exiting with no text, and a GraphQL message of any other
  wording (`Internal server error`, `Something went wrong while executing
  your query`, `timed out`, a wording this code has never seen). The
  train tells a 4xx body from a transport failure by gh's text alone:
  `(HTTP 4` is a 4xx body, `GraphQL:` with a known refusal is a refusal
  body; an unknown wording never retires. The train then reads the
  head back at once, as if the mutation had answered: an update GitHub
  did complete is proved, bound and merged in the same run (the
  `update_ambiguous` entry before the `rebase` entry); one it did not ends
  the bounded wait unconfirmed, and the request stays live for the next
  run's recovery. An answer that cannot be read is no failure at all: the
  mutation's answer is never used, only the read-back.

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

- exactly one force push inside the journaled request's window, and that
  one from the bound head to the current head. The window runs from the
  request's time to the end of its bounded wait plus one poll
  (`UpdateWait` + `UpdateWindowSlack`, 120 s + the 16 s last backoff:
  `requestWindowEnd`), since GitHub lands the update inside the wait and a
  read that started before the deadline completes at most one poll after
  it; the timeline query reads each event's `createdAt` (`ForcePush.At`),
  the comparison is at second precision, the timeline's, with the equal
  second inside at both ends (`pushedAfter`); an event with no time cannot
  be placed and refuses; a push after the window is never the update's,
  and the reconcile entry says how many and when the window ended. The
  window applies to every live request, ambiguous or not (the reviewer
  named it for the ambiguous class; the reasoning is the same for a
  request whose mutation answered, and the bound only narrows what the
  recovery accepts);
- the current head's first parent equal to the journaled base tip, or to
  the base's current tip if the base moved since, and the record says which
  (`first parent X is the journaled base tip` / `is the base's current
  tip`; the first matching base names it, so the journaled base wins when
  the two are one commit);
- the patch-id proof: the bound head and the current head against that
  parent, equal.

Then the `rebase` entry (`recovered: the update journaled by run <run>
moved the branch and was not recorded; first parent ... ; one force push
..., inside the request's window (to <end>)`, and for an ambiguous
request `; the request's mutation had failed without a definite refusal
(...)`), the `rebind` entry, the binding write (`BindPR`), the request item's
bound head moved so the plan check sees it, and CI waited at the recovered
head as after the train's own update. A failing part is one `reconcile`
entry naming it (`... binding not recovered`), the binding stays, and the
check refuses as before. A moved head with no request on record is not
touched: a force push with no train request on record is exactly the event
`pr_head_moved` exists to stop, and the owner's identity on the push does
not tell the train's push from the owner's own hand. The converse holds
too: a journaled request whose mutation failed ambiguously (and so may
or may not have run), followed by a seat's own force push from the bound
head inside the window, is accepted only when the proof holds, which is
safe on content, since the patch-id proof is what matters, not who
pushed; a request GitHub definitely refused is retired and recovers
nothing; a changed patch in the live case is refused
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
its old head, so they bind nothing later). On that path `Rebind` writes a
terminal `rebind_superseded` entry (the PR, the task, the bound head, the
named head, the reason) under the same run id (the plan reviewer on #903,
commit `f707b9cd`): `rebind_superseded(H)` is terminal for the head SHA
H. One function decides it (the plan reviewer's ruling on Codex's third
pass, commit `07c6e5b7`): `headAdmitted(journal, repository, pr, H)`
(`audit.go`) reads the latest terminal entry for H (`rebind_superseded`
or `rebind_readmitted`) and is false while that is `rebind_superseded`,
until a later `rebind_readmitted` exists. Every path that trusts or binds
a head calls it: `Audit.trainHeads` keeps a `rebase` entry's head only
when it is admitted, whatever the order of the rebase entries (Codex's
re-pass: overlapping proof attempts can journal a rebase of the head
after its superseded entry), so a push of exactly that head is not the
train's own and a later run's force-push rule refuses it;
`reconcileRebinds` and `reconcileUpdates` skip it (below); `land()`
checks the head it read back after its own update before the rebase
entry and the binding write, and refuses a superseded one `pr_head_moved`
naming the verb, nothing bound; `Rebind` reads it before its own write,
to word its readmission entry (`it is the train's own again, after a
superseded attempt`). Since Codex's fourth pass (commit `c31a4256`) the
authoritative check is at the write itself: `bindAdmitted` (`binding.go`)
is the only writer of a binding; under the audit log's lock it re-reads
the journal, calls `headAdmitted` on that read and only then writes, so
the earlier checks above are fast paths that keep a superseded head out
of the journal's rebase and rebind entries, and a supersede that lands
after them is still refused at the write. A source guard fails by file
and line on any reference to `BindPR` or `BindPRFrom` in a non-test file
outside `bindAdmitted` and `StoreBinder`'s two forwarding methods.
"Latest" is the journal's order, the append
sequence, never the entries' timestamps: a clock step cannot reorder a
supersede and a readmit (`headAdmitted` never reads `At`). Only the lead's `merge-train rebind`
verb brings H back (the reviewer's clarifications, commits `bf4c6458` and
`75cf54a5`): its applied binding writes the terminal `rebind_readmitted`
entry (the PR, the task, the heads) under the verb's own run, right after
`BindPRFrom` succeeds; no train code path writes it (a source guard fails
by file if one starts to), and the train never recovers a superseded head
either: `reconcileUpdates` meets a head that is not admitted
(`headAdmitted`) and leaves the binding where it is,
with a reconcile entry that names the verb, so the verb's proof reads the
binding where it expects it; and `reconcileRebinds`, which runs before it
and replays a journaled `rebind` entry whose head the server shows, skips
a superseded head the same way (Codex's third pass, commit `d8c0e0f8`: the
refused re-bind's own journal is rebase, rebind, then superseded, and the
rebind entry is exactly what the replay would bind). The audit-first order is unchanged. A refusal whose decision
line cannot be written names that in its detail, under the refusal's own
code. The decision log carries one line either way:
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

## The rulings on Codex's third pass (round five)

The plan reviewer's rulings on Codex's third pass at `675508d4`, by item
(commits `07c6e5b7`, `77a728cf` and `b9964e30`, on `e2ba5c18`):

1. **One admission function, not a fix per site.** `headAdmitted`
   (`audit.go`) is the one check: the latest terminal entry for a head by
   the journal's append order, never timestamps, false for
   `rebind_superseded` until a later `rebind_readmitted`. `trainHeads`,
   `reconcileRebinds`, `reconcileUpdates`, `land()`'s binding write and
   `Rebind` call it; `terminalKinds` and `Audit.supersededHead` are folded
   into it. `land()` gained the check it lacked: the train's own update can
   produce a head a lead re-bind superseded meanwhile, and that head is now
   refused `pr_head_moved` before the rebase entry and the binding write.
   The source guard is extended to the binding write (by file, function and
   line). Rows: Codex's sequence journaled by the real refused re-bind, one
   per caller, the guard (below); revert runs below.
2. **The refusal match strips only gh's documented wrappers.** `GH.run`
   returns a `GHError` carrying gh's stderr as printed; `definiteRefusal`
   reads gh's last stderr line and `refusalMessage` removes, only as a
   whole, `gh: <message>` (what `gh api graphql`, `GH.Rebase`'s command,
   prints) or `GraphQL: <message> (<path>)` (what gh's other commands
   print); the rest must equal a `graphQLRefusals` entry by
   `strings.EqualFold`. The first form of this round (`77a728cf`) took
   only the second wrapper; this round's measurement showed `gh api
   graphql` prints the first, so a real refusal on the production path was
   ambiguous and the table never fired there. Reported to the lead, and
   the plan reviewer ruled both wrappers in (`b9964e30`). Both forms are
   cited in the code's comment and above with the gh source lines and the
   gh 2.100.0 measurement; the format is gh's version's, and a changed one
   falls to ambiguous. The fake's refusal prints the real `gh: ...` line,
   after its sentinel line, which stays out of the table.
3. **The read's own deadline** (already in, commit `d8c0e0f8`, kept):
   `readUpdatedHead` classes a stalled read on the read context's
   `ctx.Err()` (its deadline), never on the error's type, and
   `TestStalledGhReadIsUnmovedForThePoll` runs the read-back through
   `GH.exec` to a real gh stand-in that sleeps past the wait.
4. **This note**: the batch by item (here), the rows, the revert runs, the
   declared-files line (this PR's `internal/schedproc/` changes are
   `decide.go` and `rebind_test.go`, from brief item 6; the merge of
   `origin/v3` brought only `liveness2_test.go`) and the gates.

What this round kept from the work in progress left uncommitted on
`e2ba5c18`: `headAdmitted` and its calls in `trainHeads`,
`reconcileRebinds`, `reconcileUpdates`, `land()` and `Rebind` (the
function's body rewritten to read the journal once per head instead of
through `terminalKinds`, which is gone with `supersededHead`); `Rebind`'s
readmission wording; the fake's sentinel on its own line; and the
wrapper-only match, with three changes: it reads gh's stderr through
`GHError` instead of searching the wrapped error text (a prefix outside
gh's wrapper read as definite before), it anchors on gh's last line, and
it compares with `strings.EqualFold` (the plain `==` failed the
lower-case row).

## Codex's fourth pass (round six)

Two P2 on `82bc6856` (commit `c31a4256`):

1. **Admission on a stale snapshot.** `reconcileUpdates` checked
   `headAdmitted` on the journal read once before its PR loop (an earlier
   PR's `waitCI` can take twenty minutes), `reconcileRebinds` on a read
   taken before its PR reads, and the proof's network calls sat between
   the check and the write. `bindAdmitted(audit, binder, record, write)`
   is now the one writer of a binding: under the audit log's lock (the
   lock `Append` takes, `Audit.locked`) it re-reads the journal, calls
   `headAdmitted` on that read and then calls `BindPR` or, for the lead's
   conditional write, `BindPRFrom`; no entry can be appended between the
   check and the write. A superseded head is refused (`ErrHeadSuperseded`,
   no binding written) and, once the lock is released, a reconcile entry
   names the lead's `merge-train rebind` verb; `land` then refuses the PR
   `pr_head_moved`, and the reconciles leave it to the check. `land`,
   `reconcileRebinds`, `reconcileUpdates` and `Rebind` all write through
   it; `Rebind`'s write is the re-admission (`Readmit`): it binds a
   superseded head and its `admitted` result words the readmission entry.
   The callers' earlier checks stay as fast paths. Lock order: the audit
   lock, then the task store's lock inside the write; no path takes them
   the other way.
2. **The guard was fooled** by an unexecuted closure calling
   `headAdmitted` before an outer `BindPR`, could not see a method value,
   and exempted every `StoreBinder` method. It is now a plain reference
   check, by file and line, with source fixtures for the method value, the
   closure and another `StoreBinder` method.

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
| definite refusal | Codex #903 (4), the reviewer's split, on the production path (the ruling on the two wrappers): the fake gh asked to update from a stale pin through `GH.Rebase` and `GH.run` refuses in `gh api graphql`'s real form, `gh: Head branch was modified. Review and try the update again.`: the run stops with `rebase: gh api graphql -F: exit status 1: fake: expected head ...` ending in that line, the journal has `update_requested` then `update_failed` (its detail ends in that line), `lastUpdateRequest` finds no live request; a seat's own force push with the same patch onto the base tip is then not recovered (no reconcile, rebase or rebind entry, the binding unchanged) and the next run refuses it on `force_push`. A seat push with a changed patch after a live request is R3's `a differing patch-id` subtest. | `internal/mergetrain` `TestFailedMutationLeavesNoLiveRequest` |
| ambiguous, landed | The reviewer's split: the fake performs the (asynchronous) update and the Forge returns `context deadline exceeded`: the same run reads the head back, proves, binds and merges it; `update_ambiguous` (the error in its detail) precedes the `rebase` entry. | `internal/mergetrain` `TestAmbiguousMutationKeepsTheRequestLiveInsideItsWindow` (`the update landed and the answer was lost`) |
| ambiguous, in the window | The update never ran and the Forge returned the transport error: the run ends unconfirmed with the request live and stamped ambiguous; a force push by hand stamped 100 s after the request is recovered at the next run (the rebase entry names the window and the ambiguous failure), bound and merged. | `internal/mergetrain` `TestAmbiguousMutationKeepsTheRequestLiveInsideItsWindow` (`a push inside the window is recovered`) |
| ambiguous, after the window | The same, the push stamped 200 s after the request (past 120 s + 16 s): refused `force_push` and `pr_head_moved`, the binding unchanged, the reconcile entry says `1 after the window, which ended at <time>` and `binding not recovered`. | `internal/mergetrain` `TestAmbiguousMutationKeepsTheRequestLiveInsideItsWindow` (`a push after the window is refused`) |
| the classes | gh's error texts map to their class (the 4xx and GraphQL refusals definite; 5xx, transport, context deadline, a killed gh, "Something went wrong" and "timed out" ambiguous); the window's end is the request's time plus 136 s; an unreadable request time has no window. | `internal/mergetrain` `TestDefiniteRefusalClassesTheGhError` |
| same second | Codex #903 (6): `pushedAfter` at second precision: a push in the request's own second is after it, the second before is not, a sub-second request past the push's second is after the push; no time or an unparseable time is not after. | `internal/mergetrain` `TestPushedAfterTakesTheSameSecondAsAfter` |
| no createdAt | Codex #903 (6): R3's `a force push with no time` subtest: the journal entry present, the one force push with an empty time, the reconcile entry says it `carries no time`, `binding not recovered`. | `internal/mergetrain` `TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof` |
| journal at the mutation | Codex #903 (6): R1 reads the audit kinds the fake saw when the mutation was called: `update_requested` present, no `rebase`. | `internal/mergetrain` `TestAsyncUpdateBindsTheHeadReadBack` |
| journaled base first | Codex #903 (8): a real-run batch base that is also the base's current tip names the parent as `the base tip of run mt-earlier's batch`. | `internal/mergetrain` `TestLeadRebindIsProvedBeforeTheBindingIsWritten` (`a journaled base that is also the current tip ...`) |
| unwritten line | Codex #903 (5): the decisions log's path taken by a directory: the verb's refusal keeps its code and names `the decision line was not written: ...`. | `internal/cli` `TestMergeTrainRebindNamesAnUnwrittenDecisionLine` |
| superseded | The reviewer on #903: after the stale-write refusal the rebase entry is journaled and a `rebind_superseded` entry follows it under the same run with the task, the heads and the reason; `trainHeads` lacks the head; a following real run's check refuses the PR (its head is that head, the binding elsewhere) on `force_push` as well as `pr_head_moved`. | `internal/mergetrain` `TestRebindRefusesWhenTheBindingMovedMeanwhile` |
| superseded, any order | Codex's re-pass (2): the entries `rebase(H)`, `rebind_superseded(H)`, `rebase(H)` leave H out of `trainHeads`; another PR's superseded entry does not touch this PR's heads; a `rebind_readmitted(H)` after them brings H back, and a `rebind_superseded(H)` after that takes it out again (the latest terminal entry decides); a readmitted entry stamped a day earlier but appended later wins, and a superseded entry stamped a day later but appended earlier does not (the journal's order, never timestamps). | `internal/mergetrain` `TestSupersededHeadStaysOutWhateverTheOrder` |
| the verb restores H | The reviewer's clarification: after a superseded attempt, the task bound back at the old head and the verb applied (old to H): the binding is H, `rebind_readmitted` is journaled under the verb's run with the task and the heads, `trainHeads` has H, and the next real run lands the PR. | `internal/mergetrain` `TestRebindRefusesWhenTheBindingMovedMeanwhile` |
| only the verb re-admits | A train run meeting a superseded head on a journaled request and a sound proof: the binding stays, no rebind, rebase or readmitted entry is written, the head stays out of `trainHeads`, the reconcile entry says `only merge-train rebind re-admits it`, and the check refuses the head on `force_push`; the verb then re-admits it and the next run lands the PR. The source guard: among the package's non-test files only `rebind.go` writes `Kind: "rebind_readmitted"`. | `internal/mergetrain` `TestTrainNeverReadmitsASupersededHead`; `TestOnlyTheRebindVerbWritesReadmitted` |
| one row per refusal | The closed table, through `GH.run`'s wrapper (`ghFailure`: the exec error and gh's stderr as printed), one class row each, in both of gh's wrappers: `Head branch was modified. Review and try the update again.` as `gh: <message>` (as printed, in lower case, and as the fake prints it: its sentinel line, then the `gh:` line last) and as `GraphQL: <message> (updatePullRequestBranch)` (as printed and in lower case); `Resource not accessible by integration` as `gh: <message>` and under the mutation's path and a dotted path; the 4xx forms 422, 403, 404; ambiguous: the unknown wordings, `Pull request is not mergeable`, a bare `Forbidden`, `Internal server error`, `timed out`, `Something went wrong ...`, the 5xx forms, the transport errors, a killed gh with no text. | `internal/mergetrain` `TestDefiniteRefusalClassesTheGhError` |
| the replay skip | Codex's third pass (1): the refused re-bind's journal as Rebind writes it (rebase, rebind with the `patch-id:` detail, then rebind_superseded), a journaled request and the push: `reconcileRebinds` does not replay the rebind entry and `reconcileUpdates` does not recover the head; two reconcile entries name the verb (`does not replay it`, `does not recover it`); the binding stays, the head stays out, the check refuses it on `force_push`; the verb re-admits it and the next run lands the PR. | `internal/mergetrain` `TestTrainNeverReadmitsASupersededHead` |
| a killed gh read | Codex's third pass (3): the read-back through `GH.exec` (the real exec path, `CommandContext`) to a gh stand-in that sleeps past a 300 ms wait: the killed read is unmoved for its poll, the run ends refused unconfirmed within 5 s of the mutation, nothing bound. The in-process blocking fake's row (`TestStalledReadReturnsWithinTheBoundedWait`) stands beside it; both time the wait from the mutation's return, since the fake's git work runs under the suite's load. | `internal/mergetrain` `TestStalledGhReadIsUnmovedForThePoll` |
| the run's bound in words | The reviewer's clarification on (3): the command under a 300 ms bound (a seam) with a gh stand-in that sleeps, its child holding the pipes past the kill, a dry run under a temp root with the default grants: the run ends within the bound and the error reads `merge-train: the run timed out after 300ms, a call to GitHub did not return; nothing more was merged, and the next run starts from the journal` above the rule and `refused (run_timeout): read branch protection: ...` below. | `internal/cli` `TestMergeTrainRunEndsAtItsBoundInWords` |
| unknown wording | Codex's re-pass (1): `GraphQL: Internal server error` and a wording never seen are ambiguous in the class table; through the run, the fake performs the update and the Forge returns the unknown wording: `update_ambiguous`, no `update_failed`, the head read back, bound and merged. | `internal/mergetrain` `TestDefiniteRefusalClassesTheGhError`; `TestAmbiguousMutationKeepsTheRequestLiveInsideItsWindow` (`an unknown GraphQL wording ...`) |
| stalled read | Codex's re-pass (3): a Forge whose PR reads after the mutation block until their context ends, under a 300 ms wait on the real clock: the run ends refused unconfirmed within 3 s, nothing bound; `RunBound(3)` is three times the update wait, the CI wait and ten minutes, and `RunBound(0)` is one PR's. | `internal/mergetrain` `TestStalledReadReturnsWithinTheBoundedWait` |
| the endpoints | Codex's re-pass (4): a force push stamped in the request's own second and one stamped in the window's last second (136 s) are recovered; one stamped a second before the request (`no force push in the journaled request's window`) and one a second past the window (`1 after the window, which ended at`) are refused with the reason in the reconcile entry. | `internal/mergetrain` `TestAmbiguousMutationKeepsTheRequestLiveInsideItsWindow` (four subtests) |
| the #894 shape | Codex's re-pass (4): a journaled request with no `update_failed` and no `update_ambiguous` (the run died after the mutation), the push stamped 5 s after it: recovered on the proof, the record naming the window; every recovered row's record names it. | `internal/mergetrain` `TestUnrecordedUpdateIsRecoveredOnTheJournalAndTheProof` (`the run died after the mutation ...`) |
| production cancel | Codex's re-pass (4): no Sleep seam; the context is cancelled 150 ms after the mutation, inside the first real 1 s backoff: the wait ends within 900 ms of the mutation, refused unconfirmed, nothing bound. | `internal/mergetrain` `TestCancellationEndsTheProductionSleep` |
| the replay (one admission) | The plan reviewer's ruling on Codex's third pass, item 1, Codex's sequence: the real refused re-bind (a Binder that advances the binding right before the conditional write) journals `rebase(old->H)`, `rebind(old->H)`, `rebind_superseded(H)` in that order under one run; with the task bound back at old and the PR's head H, a real run leaves H unbound: the binding stays at old, no `rebase` or `rebind` entry under the run, a reconcile entry `does not replay it, only merge-train rebind re-admits it`, the check refuses H on `force_push`. | `internal/mergetrain` `TestRefusedRebindJournalLeavesTheHeadUnbound` |
| the recovery (one admission) | A journaled request for the bound head, one force push to H on the base tip with the patch unchanged (the recovery's proof holds) and `rebind_superseded(H)` with no rebind entry: the run does not recover H, the binding stays, nothing rebound, a reconcile entry `does not recover it, only merge-train rebind re-admits it`. | `internal/mergetrain` `TestSupersededHeadIsNotRecoveredFromAJournaledRequest` |
| land's bind (one admission) | The train's own update produces H, and `rebind_superseded(H)` is journaled before the read-back (a lead re-bind of H refused meanwhile): the proof holds and the run refuses H `pr_head_moved` with `superseded by a lead re-bind decision`; no `rebase` or `rebind` entry for the PR under the run, the binding unchanged, nothing merged. | `internal/mergetrain` `TestTrainUpdateOfASupersededHeadIsNotBound` |
| the verb (one admission) | A first re-bind's readmission entry reads `the lead's re-bind bound this head; it is the train's own`; after the real refused re-bind of the same head it reads `... it is the train's own again, after a superseded attempt`, the head is admitted and bound, and the next run lands the PR. | `internal/mergetrain` `TestRebindWordsTheReadmissionAfterASupersededAttempt` (2 subtests) |
| trainHeads (one admission) | `superseded, any order` above: the same row now runs through `headAdmitted`. | `internal/mergetrain` `TestSupersededHeadStaysOutWhateverTheOrder` |
| the admission guard | Codex's fourth pass (2): a plain reference check over the package's non-test files (go/ast): any `BindPR` or `BindPRFrom` identifier (a call, a method value, a closure's call) outside `bindAdmitted` and `StoreBinder`'s forwarding `BindPR` and `BindPRFrom` fails as `<file>:<line>: <function> references <name>`; declarations (the interface's methods, a method's own name) are not references; at least two references are found inside the exempt functions. Source fixtures fed to the same checker: a method value (`bind := t.Binder.BindPR; bind(...)`), a closure that calls `headAdmitted` without running before an outer `BindPR`, and a `StoreBinder` method other than the two forwarders, each reported once by file and line. | `internal/mergetrain` `TestOnlyTheRebindVerbWritesReadmitted` (`only bindAdmitted references the binding write`, `the guard sees a method value, a closure and another StoreBinder method`) |
| superseded meanwhile | Codex's fourth pass (1): `rebind_superseded(H)` appended after the reconcile's journal snapshot, inside the recovery's proof (the hook on `FirstParent(H)`, a journaled request and one force push to H, the proof sound) and inside the replay's PR read (the hook on the first `PR` read, a journaled proof-backed rebind entry for the bound head): H is not bound, the binding stays, a reconcile entry reads `superseded by a lead re-bind decision before the binding write; the train does not bind it, only merge-train rebind re-admits it`, and the check refuses H. | `internal/mergetrain` `TestSupersededDuringTheRecoveryIsNotBound` (2 subtests) |
| the wrappers whole | Item 2 as ruled: the match strips one of gh's two wrappers whole from gh's last stderr line, `gh: <message>` or `GraphQL: <message> (<path>)`, and compares the rest exactly, case aside. Definite: the table text in either wrapper (rows (1) and (2): the fake's `gh:` refusal through the production path is `definite refusal` above). Ambiguous, never definite (row (3), the text with anything extra): around `gh: ` a path after the message, `Retry later.` after it, `gh: gh: `, `warning: gh: `, `gh:` with no space, a second line (gh api's newline between several errors, or gh's scopes line after it), `gh: HTTP 502`, `gh: HTTP 422`; around `GraphQL: ` no path, a second path, the old fake's `[fake: ...]` after the path, a line after it, two errors on one line, `warning: GraphQL: `, `graphql: `; each table message's fragment inside unfamiliar text (`... while checking expected head ...`, `Internal server error: Head branch was modified...`, `Head branch was modified... Retry later.`, `Something went wrong, resource not accessible by integration` in both wrappers, `Resource not accessible by integration token`, `... not mergeable by ...`), a prefix of a message (`Head branch was modified`, `Resource not accessible`, in both wrappers), and an error not from gh carrying the GraphQL text; the fake's pinned-head mismatch through `GH.run` is definite by its own exact output with no table entry. | `internal/mergetrain` `TestDefiniteRefusalClassesTheGhError` |

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

### The reviewer's split of item 4 (three runs, on `c26b17cc`)

`903c ambiguous kept live`: `internal/mergetrain/train.go` (`land`)
`if definiteRefusal(err) {` to `if definiteRefusal(err) || true {` (every
failure retires the request);
`go test -count=1 -timeout 300s -run ^TestAmbiguousMutationKeepsTheRequestLiveInsideItsWindow$ ./internal/mergetrain/`;
```
async_test.go:700: the lost answer's update was not read back and merged: rebase: Post "https://api.github.com/graphql": context deadline exceeded [{Number:1 Task:t1 Head:eb8091a38cf49c93de120e39bf12d97f9d133840 Draft:true Changed:2 Files:[pkg/a.go] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:true RebaseRule:strict_required_status_checks_policy RebaseReason:GitHub requires the branch to be up to date before merging PatchProof:0x7797120681e0 Overlap:[] Status:planned Merged: title: mergeable:MERGEABLE}]
```
restored: `ok ... 3.653s`.

`903c the window`: `internal/mergetrain/update.go` (`reconcileUpdates`)
`until := requestWindowEnd(requested.At, t.updateWait())` to `until := ""`
(no window); the same command (the subtest `a push after the window is refused`);
```
async_test.go:764: the push after the window was recovered: <nil> [{Number:1 Task:t1 Head:5ecb480c02228e6d151ee7950e772b3d73d92ba9 Draft:true Changed:2 Files:[pkg/a.go] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:merged Merged:9aa706cc35ac0ad1dcabdf866f6b423991f56bb0 title: mergeable:MERGEABLE}] (bound 5ecb480c02228e6d151ee7950e772b3d73d92ba9)
```
restored: `ok ... 3.664s`.

`903c the classes`: `internal/mergetrain/update.go` (`definiteRefusal`)
`if !strings.Contains(err.Error(), "GraphQL:") {` to `... && false {` (every
failure a refusal); `go test -count=1 -timeout 300s -run ^TestDefiniteRefusalClassesTheGhError$ ./internal/mergetrain/`;
```
async_test.go:794: definiteRefusal("gh api graphql: exit status 1: gh: Bad Gateway (HTTP 502)") = true, want false
```
restored: `ok ... 0.262s`.

### The superseded re-bind (one run, on `f707b9cd`)

`903d the superseded exclusion`: `internal/mergetrain/audit.go`
(`trainHeads`) `case "rebind_superseded":` to `case
"rebind_superseded_ignored":` (the head stays the train's);
`go test -count=1 -timeout 300s -run ^TestRebindRefusesWhenTheBindingMovedMeanwhile$ ./internal/mergetrain/`;
```
async_test.go:539: the superseded head stays the train's: rebase 0, superseded 2, trainHeads map[53a5af45fbfca052f57b5e66493695d0870f015f:true]
```
restored: `ok ... 1.076s`.

### Codex's re-pass on #903 (seven runs, on `1491a9f1`)

`903e the default class`: `internal/mergetrain/update.go`
(`definiteRefusal`) the closing `return false` to `return true` (any
other GraphQL error a refusal);
`go test -count=1 -timeout 300s -run ^TestDefiniteRefusalClassesTheGhError$ ./internal/mergetrain/`;
```
async_test.go:880: definiteRefusal("gh api graphql: exit status 1: GraphQL: a wording this code has never seen (updatePullRequestBranch)") = true, want false
```
restored: `ok ... 0.240s`.

`903e the order-dependent exclusion`: `internal/mergetrain/audit.go`
(`trainHeads`) `superseded[e.NewHead] = true` to `delete(out, e.NewHead)`
(the exclusion while reading the entry);
`go test -count=1 -timeout 300s -run ^TestSupersededHeadStaysOutWhateverTheOrder$ ./internal/mergetrain/`;
```
async_test.go:910: trainHeads map[dddddddddddddddddddddddddddddddddddddddd:true eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:true]
```
restored: `ok ... 0.266s`.

`903e the read unbounded`: `internal/mergetrain/update.go`
(`readUpdatedHead`) `readCtx, cancel := context.WithTimeout(ctx, remaining)`
to `readCtx, cancel := context.WithCancel(ctx)` (no bound on the read);
`go test -count=1 -timeout 40s -run ^TestStalledReadReturnsWithinTheBoundedWait$ ./internal/mergetrain/`;
```
panic: test timed out after 40s
```
(the blocked read never returns; the row ends only by the test's own
timeout) restored: `ok ... 1.340s`.

`903e the production select`: `internal/mergetrain/update.go`
(`sleepOrDone`) `case <-ctx.Done():` gains `<-timer.C` before its
return (the timer waited out);
`go test -count=1 -timeout 300s -run ^TestCancellationEndsTheProductionSleep$ ./internal/mergetrain/`;
```
async_test.go:984: the cancel did not end the sleep: 1.022245s after the mutation
```
restored: `ok ... 1.148s`.

`903e the journal after the mutation`: `internal/mergetrain/train.go`
(`land`) the `update_requested` write and the `Forge.Rebase` block
swapped (the entry written after the mutation; the entry order of R1
still holds, since the entry precedes the read-back);
`go test -count=1 -timeout 300s -run ^TestAsyncUpdateBindsTheHeadReadBack$ ./internal/mergetrain/`;
```
async_test.go:142: the journal at the mutation: [protection batch]
```
restored: `ok ... 1.323s`.

`903e the competing push`: `internal/mergetrain/train.go` (`land`)
`if parent != afterBase && parent != beforeBase {` to `... && false {`
(the parent unchecked; the late commit's patch then catches it under the
wrong rule); `go test -count=1 -timeout 300s -run ^TestFailedProofLeavesNoRebaseEntry$ ./internal/mergetrain/`
(the subtest `a parent that is not the base tip`);
```
async_test.go:496: the proof did not refuse: <nil> [{Number:1 Task:t1 Head:f20ea84d9dad40a6184383b38ee73185fc4e12af Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:rebase_patch_changed Detail:its change moved during the update}] GateID: GateError: Lifted:false After:[] RebaseForCI:true RebaseRule:strict_required_status_checks_policy RebaseReason:GitHub requires the branch to be up to date before merging PatchProof:0xc8bd32c4df0 Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}]
```
restored: `ok ... 2.191s`.

`903e the equal second`: `internal/mergetrain/update.go` (`pushedAfter`)
`!a.Truncate(time.Second).Before(s.Truncate(time.Second))` to
`a.Truncate(time.Second).After(s.Truncate(time.Second))` (strict: the
equal second outside); `go test -count=1 -timeout 300s -run ^TestAmbiguousMutationKeepsTheRequestLiveInsideItsWindow$ ./internal/mergetrain/`
(the subtest `the request's own second`);
```
async_test.go:831: not recovered: [{Number:1 Task:t1 Head:33575a39095a2fae8fca901ee0d76dd408bd7b65 Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed 8d0b4e5c10ab -> 33575a39095a by owner} {Rule:pr_head_moved Detail:PR #1 head 33575a39095a is not the pr_head 8d0b4e5c10ab bound to task t1; re-bind through the commit step}] GateID:merge-92aade0b8553b221 GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}] (bound 8d0b4e5c10ab...)
```
restored: `ok ... 9.149s`.

The #894-shape row's revert is `R3 the journal condition` above (the
request's presence is what admits the recovery); the window's end has
`903c the window`.

### The reviewer's clarifications (seven runs, on `75cf54a5`)

`903g the readmission`: `internal/mergetrain/audit.go` (`terminalKinds`)
`case "rebind_superseded", "rebind_readmitted":` to `case "rebind_superseded":`
(the readmission not read);
`go test -count=1 -timeout 300s -run ^TestSupersededHeadStaysOutWhateverTheOrder$ ./internal/mergetrain/`;
```
async_test.go:941: the re-admitted head is out: map[eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:true]
```
restored: `ok ... 0.501s`.

`903g the timestamps`: `internal/mergetrain/audit.go` (`terminalKinds`)
the loop keeps, per head, the terminal entry with the latest `At` instead
of the latest position; the same command;
```
async_test.go:958: the later-appended, earlier-stamped readmission lost: map[eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:true]
```
restored: `ok ... 0.416s`.

`903g the superseded recovery`: `internal/mergetrain/update.go`
(`reconcileUpdates`) `if t.Audit.supersededHead(req.Repository, it.Number, cur.HeadSHA) {`
to `... && false {` (the recovery proceeding on a superseded head);
`go test -count=1 -timeout 300s -run ^TestTrainNeverReadmitsASupersededHead$ ./internal/mergetrain/`;
```
async_test.go:992: the superseded head was recovered: <nil> [{Number:1 Task:t1 Head:9e3553acd9dfadb9db7555f4370a83c255a49f4b Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed abc35a28066c -> 9e3553acd9df by owner}] GateID:merge-a22305090651e2c7 GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}] (bound 9e3553acd9dfadb9db7555f4370a83c255a49f4b)
```
(the binding moved to the superseded head) restored: `ok ... 9.344s`.

`903g land writes readmitted`: `internal/mergetrain/train.go` (`land`) a
`rebind_readmitted` write inserted before the binding write (a train path
re-admitting);
`go test -count=1 -timeout 300s -run ^TestOnlyTheRebindVerbWritesReadmitted$ ./internal/mergetrain/`;
```
async_test.go:1030: train.go writes a rebind_readmitted entry; only the lead's verb (rebind.go) may
```
restored: `ok ... 0.240s`.

`903f a table entry`: `internal/mergetrain/update.go` (`graphQLRefusals`)
the `"not mergeable",` line dropped;
`go test -count=1 -timeout 300s -run ^TestDefiniteRefusalClassesTheGhError$ ./internal/mergetrain/`;
```
async_test.go:903: definiteRefusal("gh api graphql: exit status 1: GraphQL: Pull request is not mergeable (updatePullRequestBranch)") = false, want true
```
restored: `ok ... 0.236s`.

`903f the bound in words`: `internal/cli/merge_train.go` (`runMergeTrain`)
`case runErr != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):` to
`... && false:` (the generic error form);
`go test -count=1 -timeout 300s -run ^TestMergeTrainRunEndsAtItsBoundInWords$ ./internal/cli/`;
```
merge_train_rebind_test.go:250: no rule in "merge-train: read branch protection: gh api repos/owner/name/rules/branches/main: signal: killed: "
```
restored: `ok ... 0.762s`.

`903f the wait delay`: `internal/mergetrain/gh.go` (`GH.exec`)
`cmd.WaitDelay = 2 * time.Second` to `cmd.WaitDelay = 0 * time.Second`
(Output waits for the child's pipes); the same command;
```
merge_train_rebind_test.go:248: the run did not end at its bound: merge-train: the run timed out after 300ms, a call to GitHub did not return; nothing more was merged, and the next run starts from the journal
```
(after 30 s, the child's sleep) restored: `ok ... 2.745s`.

### Codex's third pass (three runs, on `d8c0e0f8` and `6811a0ba`)

`903h the replay skip`: `internal/mergetrain/train.go` (`reconcileRebinds`)
`if terminalKinds(entries, req.Repository, it.Number)[pending.NewHead] == "rebind_superseded" {`
to `... && false {` (the replay proceeding);
`go test -count=1 -timeout 300s -run ^TestTrainNeverReadmitsASupersededHead$ ./internal/mergetrain/`;
```
async_test.go:1007: the superseded head was recovered: <nil> [{Number:1 Task:t1 Head:b1df76960f7703d86b0d274710199e0f8bf49773 Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed 970d7466487f -> b1df76960f77 by owner}] GateID:merge-b2ce10ef4b5ae171 GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}] (bound b1df76960f77...)
```
(the replay bound the superseded head) restored: `ok ... 1.857s`.

`903h the substring match`: `internal/mergetrain/update.go`
(`definiteRefusal`) `if strings.HasPrefix(msg, strings.ToLower(refusal)) {`
to `if strings.Contains(msg, strings.ToLower(refusal)) {` (the fragment
anywhere); `go test -count=1 -timeout 300s -run ^TestDefiniteRefusalClassesTheGhError$ ./internal/mergetrain/`
(on `6811a0ba`, whose rows carry a table message inside another sentence);
```
async_test.go:909: definiteRefusal("gh api graphql: exit status 1: GraphQL: Something went wrong, resource not accessible by integration (updatePullRequestBranch)") = true, want false
```
restored: `ok ... 0.416s`.

`903h the error type alone`: `internal/mergetrain/update.go`
(`readUpdatedHead`) `timedOut := readCtx.Err() == context.DeadlineExceeded && ctx.Err() == nil`
to `timedOut := errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil`;
`go test -count=1 -timeout 300s -run ^TestStalledGhReadIsUnmovedForThePoll$ ./internal/mergetrain/`;
```
async_test.go:1168: the killed gh read: read #1 after its update: gh pr view 1: signal: killed:  [{Number:1 Task:t1 Head:07dcd571f322effe38ab92d5f20e2a6407c5987d Draft:true Changed:2 Files:[pkg/a.go] Refusals:[] GateID: GateError: Lifted:false After:[] RebaseForCI:true RebaseRule:strict_required_status_checks_policy RebaseReason:GitHub requires the branch to be up to date before merging PatchProof:0x... Overlap:[] Status:planned Merged: title: mergeable:MERGEABLE}]
```
restored: `ok ... 1.535s`.

### The rulings on Codex's third pass (eleven runs, on `07c6e5b7` and `77a728cf`)

Each by `python3 revert.py <file> <old> <new> <row>` from the job scratch
(not the tree): the one expression replaced, the row run alone with
`go test -count=1 -timeout 300s -run ^<row>$ ./internal/mergetrain/`,
the first failing line verbatim (the class rows iterate a map, so the
first failing row is whichever came first), then `git checkout -- <file>`
and the row re-run. The `903j` runs are on the first form of item 2
(`77a728cf`, one wrapper, `graphQLMessage`); since `b9964e30` the
function is `refusalMessage` with both wrappers, and its runs are `903k`
below.

`903i trainHeads`: `internal/mergetrain/audit.go` (`trainHeads`)
`&& headAdmitted(entries, repository, pr, e.NewHead) {` to ` {` (every
rebase entry's head kept); `TestSupersededHeadStaysOutWhateverTheOrder`;
```
async_test.go:942: trainHeads map[dddddddddddddddddddddddddddddddddddddddd:true eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee:true]
```
restored: `ok ... 0.403s`.

`903i the replay`: `internal/mergetrain/train.go` (`reconcileRebinds`)
`if !headAdmitted(entries, req.Repository, it.Number, pending.NewHead) {`
to `... && false {`; `TestRefusedRebindJournalLeavesTheHeadUnbound`;
```
admission_test.go:67: the superseded head was bound: <nil> [{Number:1 Task:t1 Head:c50411e0c82c6ea3167a256384f8ccb7cca6be14 Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed 9f8cab047840 -> c50411e0c82c by owner}] GateID:merge-00989cbbcf0dabbc GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}] (bound c50411e0c82c6ea3167a256384f8ccb7cca6be14)
```
(the replay bound H) restored: `ok ... 1.976s`.

`903i the recovery`: `internal/mergetrain/update.go` (`reconcileUpdates`)
`if !headAdmitted(entries, req.Repository, it.Number, cur.HeadSHA) {` to
`... && false {`; `TestSupersededHeadIsNotRecoveredFromAJournaledRequest`;
```
admission_test.go:97: the superseded head was recovered: <nil> [{Number:1 Task:t1 Head:6ed9133255db19b1c1bcdbb4371dfc923e973ded Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed 5ce5d91fe7bd -> 6ed9133255db by owner}] GateID:merge-65a91a2901c09bd2 GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}] (bound 6ed9133255db19b1c1bcdbb4371dfc923e973ded)
```
restored: `ok ... 1.175s`.

`903i land's bind`: `internal/mergetrain/train.go` (`land`)
`if !headAdmitted(journal, req.Repository, p.Number, pushed) {` to
`... && false {`; `TestTrainUpdateOfASupersededHeadIsNotBound`;
```
admission_test.go:137: the superseded head was bound: <nil> [{Number:1 Task:t1 Head:eb591d96e6b9b3b900f39b1e5adaf60cc2e7345e Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed b98f8de55df7 -> eb591d96e6b9 by owner}] GateID:merge-ff90bc303f28a82a GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:0x4d96f9c8050 Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}] (bound eb591d96e6b9b3b900f39b1e5adaf60cc2e7345e)
```
(the train bound H; trainHeads still lacks it, so the check refused the
push) restored: `ok ... 1.076s`.

`903i the verb`: `internal/mergetrain/rebind.go` (`Rebind`)
`admitted := headAdmitted(entries, repository, in.PR, in.To)` to
`admitted := true`; `TestRebindWordsTheReadmissionAfterASupersededAttempt`;
```
admission_test.go:184: the readmission: "the lead's re-bind bound this head; it is the train's own"
```
restored: `ok ... 2.055s`.

`903i the guard`: `internal/mergetrain/update.go` (`reconcileUpdates`)
`if !headAdmitted(entries, req.Repository, it.Number, cur.HeadSHA) {` to
`if false {` (the call gone); `TestOnlyTheRebindVerbWritesReadmitted`;
```
async_test.go:1091: update.go: reconcileUpdates calls BindPR (line 358) without first calling headAdmitted
```
restored: `ok ... 0.264s`.

`903j the anchor`: `internal/mergetrain/update.go` (`graphQLMessage`) the
two lines `lines := strings.Split(strings.TrimSpace(stderr), "\n")` and
`msg, ok := strings.CutPrefix(strings.TrimRight(lines[len(lines)-1], "\r"), "GraphQL: ")`
to `_, msg, ok := strings.Cut(stderr, "GraphQL: ")` and
`msg = strings.TrimSpace(msg)` (the prefix found anywhere);
`TestDefiniteRefusalClassesTheGhError`;
```
async_test.go:947: definiteRefusal("gh api graphql: exit status 1: warning: GraphQL: Head branch was modified. Review and try the update again. (updatePullRequestBranch)") = true, want false
```
restored: `ok ... 0.669s`.

`903j the prefix match`: (`definiteRefusal`)
`if strings.EqualFold(msg, refusal) {` to
`if strings.HasPrefix(strings.ToLower(msg), strings.ToLower(refusal)) {`;
the same row;
```
async_test.go:947: definiteRefusal("gh api graphql: exit status 1: GraphQL: Head branch was modified. Review and try the update again. Retry later. (updatePullRequestBranch)") = true, want false
```
restored: `ok ... 0.697s`.

`903j the path`: (`graphQLMessage`) `open >= 0 && strings.HasSuffix(msg, ")")`
to `open >= 0 && false && strings.HasSuffix(msg, ")")` (the path never
removed); the same row;
```
async_test.go:947: definiteRefusal("gh api graphql: exit status 1: GraphQL: Resource not accessible by integration (updatePullRequestBranch)") = false, want true
```
restored: `ok ... 0.685s`.

`903j the case`: (`definiteRefusal`) `if strings.EqualFold(msg, refusal) {`
to `if msg == refusal {`; the same row;
```
async_test.go:947: definiteRefusal("gh api graphql: exit status 1: GraphQL: head branch was modified. review and try the update again.") = false, want true
```
restored: `ok ... 0.684s`.

`903j gh's line`: (`definiteRefusal`)
`msg, ok := graphQLMessage(ge.Stderr)` to
`msg, ok := graphQLMessage(err.Error())` (this package's wrapper read as
gh's line); the same row;
```
async_test.go:947: definiteRefusal("gh api graphql: exit status 1: GraphQL: Head branch was modified. Review and try the update again.") = false, want true
```
restored: `ok ... 0.678s`.

### The ruling on gh's two wrappers (three runs, on `b9964e30`)

The same method as the runs above.

`903k the gh wrapper`: `internal/mergetrain/update.go` (`refusalMessage`)
the three lines `if msg, ok := strings.CutPrefix(last, "gh: "); ok {`,
`return msg, true`, `}` removed (only the GraphQL wrapper);
`TestFailedMutationLeavesNoLiveRequest`;
```
async_test.go:667: the failed mutation did not stop the run: <nil> ""
```
(row 1 ends ambiguous: the fake's real `gh:` refusal is classed
`update_ambiguous`, the run reads the head back and goes on instead of
stopping on `update_failed`) restored: `ok ... 1.317s`. The same revert,
`TestDefiniteRefusalClassesTheGhError`;
```
async_test.go:972: definiteRefusal("gh api graphql: exit status 1: gh: head branch was modified. review and try the update again.") = false, want true
```
restored: `ok ... 0.694s`.

`903k the GraphQL wrapper whole`: (`refusalMessage`) the line without its
path accepted (`return "", false` under the path check to `return msg,
true`); `TestDefiniteRefusalClassesTheGhError`;
```
async_test.go:972: definiteRefusal("gh api graphql: exit status 1: GraphQL: Head branch was modified. Review and try the update again.") = true, want false
```
restored: `ok ... 1.106s`.

### Codex's fourth pass (five runs, on `c31a4256`)

The same method as the runs above. The `903i` runs at `07c6e5b7` reverted
the callers' checks, which are fast paths since this pass; the write's own
check is `903l the fresh read`.

`903l the fresh read`: `internal/mergetrain/binding.go` (`bindAdmitted`)
`admitted = headAdmitted(journal, w.Repository, w.PR, w.To)` to
`admitted = headAdmitted(journal[:0], w.Repository, w.PR, w.To)` (the
fresh read ignored: only the callers' earlier snapshot checks left);
`go test -count=1 -timeout 300s -run '^TestSupersededDuringTheRecoveryIsNotBound$/^during_the_recovery.*$' ./internal/mergetrain/`;
```
admission_test.go:251: the head superseded during the proof was bound: <nil> [{Number:1 Task:t1 Head:1abfd3d0cb6876dd2b82d5b70d09b183bfef0a9d Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed 6735a02b6fbe -> 1abfd3d0cb68 by owner}] GateID:merge-dc820aed90387398 GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}] (bound 1abfd3d0cb6876dd2b82d5b70d09b183bfef0a9d, hooked true)
```
restored: `ok ... 5.406s`. The same revert, the subtest `during the
replay's PR read`;
```
admission_test.go:272: the head superseded during the replay's read was bound: <nil> [{Number:1 Task:t1 Head:ad870d589bc6bfebc089a5f9b86718bc7c0f0aaf Draft:true Changed:2 Files:[pkg/a.go] Refusals:[{Rule:force_push Detail:head was force-pushed 21d012f64212 -> ad870d589bc6 by owner}] GateID:merge-4ed6f9e31f0b9a18 GateError: Lifted:false After:[] RebaseForCI:false RebaseRule: RebaseReason: PatchProof:<nil> Overlap:[] Status:refused Merged: title: mergeable:MERGEABLE}] (bound ad870d589bc6bfebc089a5f9b86718bc7c0f0aaf)
```
restored: `ok ... 1.692s`.

`903l the method value`: `internal/mergetrain/async_test.go`
(`bindingReferences`) the reference test (`n.(*ast.Ident)`) to a call
test (a `*ast.CallExpr` whose `Fun` is a selector named `BindPR` or
`BindPRFrom`); `go test -count=1 -timeout 300s -run ^TestOnlyTheRebindVerbWritesReadmitted$ ./internal/mergetrain/`;
```
async_test.go:1169: methodvalue.go: the guard reported [], want one reference by file and line
```
restored: `ok ... 1.027s`.

`903l the closure`: (`bindingReferences`) a function that calls
`headAdmitted` anywhere in its body allowed (the old rule's shape); the
same row;
```
async_test.go:1169: closure.go: the guard reported [], want one reference by file and line
```
restored: `ok ... 0.229s`.

`903l every StoreBinder method`: (`bindingReferences`)
`|| (receiverIs(fn, "StoreBinder") && isBind(fn.Name.Name))` to
`|| receiverIs(fn, "StoreBinder")`; the same row;
```
async_test.go:1169: storebinder.go: the guard reported [], want one reference by file and line
```
restored: `ok ... 0.551s`.

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

On `c26b17cc` (the reviewer's split of item 4, `internal/mergetrain/`
only): `go build ./... && go vet ./...` exit 0; `go test -count=1
./internal/mergetrain/ ./internal/cli/ ./internal/schedproc/
./internal/task/` all four `ok`, exit 0; `go run ./scripts/redact-evidence
-check docs/v3-spike/320-3a-train/` on this note: exit 0.

On `f707b9cd` (the superseded re-bind, `internal/mergetrain/` only): `go
build ./... && go vet ./...` exit 0; `go test -count=1
./internal/mergetrain/ ./internal/cli/ ./internal/schedproc/
./internal/task/` all four `ok`, exit 0; `go run ./scripts/redact-evidence
-check docs/v3-spike/320-3a-train/` on this note: exit 0.

On `3217d22d` (the pi words entries for `update_failed`,
`update_ambiguous` and `rebind_superseded`, the entry kinds the #903 batch
added; CI on `5dedb4b7` had stopped at pi-test's row 7, `train entry kind
update_failed has no words`): `cd pi && npm test` `ℹ tests 116`, `ℹ pass
115`, `ℹ fail 0`, `ℹ skipped 1`, exit 0; `node --test test/words.test.ts`
exit 0; `go build ./... && go vet ./...` exit 0; `go run
./scripts/redact-evidence -check docs/v3-spike/320-3a-train/` on this
note: exit 0.

On `1491a9f1` (Codex's re-pass on #903, `internal/mergetrain/` and
`internal/cli/merge_train.go`): `go build ./... && go vet ./...` exit 0;
`go test -count=1 ./internal/mergetrain/ ./internal/cli/
./internal/schedproc/ ./internal/task/` all four `ok`, exit 0; `go run
./scripts/redact-evidence -check docs/v3-spike/320-3a-train/` on this
note: exit 0.

On `75cf54a5` (the reviewer's clarifications: `internal/mergetrain/`,
`internal/cli/merge_train.go`, `pi/extensions/lib/words.ts`): `go build
./... && go vet ./...` exit 0; `go test -count=1 ./internal/mergetrain/
./internal/cli/ ./internal/schedproc/ ./internal/scheduler/
./internal/task/` all five `ok`, exit 0; `cd pi && node --test
test/words.test.ts` 17 pass, 0 fail, exit 0 (and `npm test` on the words
commit: 116 tests, 115 pass, 1 skipped); `git diff a097f8e1 HEAD --stat`
names no file under `internal/schedproc/`; `go run
./scripts/redact-evidence -check docs/v3-spike/320-3a-train/` on this
note: exit 0.

The CI failure at 6e137632 (`internal/schedproc`
`TestCancelDuringARunningStepFreesTheSeat`, both attempts; green at
5dedb4b7): not a regression of this PR. The schedproc test binary built
with `-trimpath -buildvcs=false` has the same sha256 at both commits
(75531025f8f4...), the six commits between touch only mergetrain, the pi
words table and this note, and the decision server is never started by
the row's rig. It is a pre-existing race of the test against the cancelled
step's return (under the runner's load the step has not returned when the
next tick runs, and the rig leases to the other seat); the test-only fix is
PR #905, which lands on v3 first. The production zero wait in
`Committer.CancelCommit` (`committer.go:131`, an unwinding cancel leaves
the seat busy for a tick) is the plan reviewer's 3.2.x item, with the
bounded-grace proposal; nothing under `internal/schedproc/` changes in
this PR.

On `9f00fcea` (Codex's third pass, `internal/mergetrain/` only, on the
merged head `675508d4`): `go build ./... && go vet ./...` exit 0; `go
test -count=1 ./internal/mergetrain/ ./internal/cli/ ./internal/schedproc/
./internal/scheduler/ ./internal/task/` all five `ok`, exit 0; `cd pi &&
node --test test/words.test.ts` 17 pass, 0 fail, exit 0; `go run
./scripts/redact-evidence -check docs/v3-spike/320-3a-train/` on this
note: exit 0.

On `77a728cf` (the rulings on Codex's third pass: `internal/mergetrain/`
only; the note's commit after it has the same Go tree), from the worktree
root:

- `go build ./... && go vet ./... && go test -count=1 ./...` (the whole
  module): first run exit 1 on one row outside this PR's files,
  `internal/seatlaunch` `TestProofConnectionsAreBounded`
  (`fix3c_test.go:176: an idle connection was not dropped at the read
  deadline: <nil>`, a 2 s timing row, run while the pi suite ran beside
  it; no file under `internal/seatlaunch/` differs from `87fb71e9`); alone
  `ok ... 2.265s`; the whole gate run once more: exit 0, 56 packages `ok`.
- The pi gate: `cd pi && npm test` first run exit 1 on one row,
  `R53: the owner's yes switches the lead's tools ...` (`amq-squad guard
  failed closed (timed out after 5000 ms)`, a 5 s guard budget, run while
  the whole-module Go suite loaded the host); run again alone: exit 0,
  `ℹ tests 116`, `ℹ pass 115`, `ℹ fail 0`, `ℹ skipped 1`.
  `node scripts/lint.mjs`: exit 0 (`lint ok: 29 files, pi 1.3.0`).
- `NPM_FLAGS=--offline make ci` (a PyYAML 6.0.3 venv under the job scratch
  first on PATH): exit 0, 70 packages `ok`, pi-test `ℹ tests 116`,
  `ℹ pass 115`, `ℹ fail 0`.
- The format gate under go.mod's toolchain: `GOTOOLCHAIN=go1.26.10 make
  fmt-check` exit 0, and again with the go1.26.10 toolchain's `bin/`
  first on PATH (`make fmt-check` runs `gofmt` from PATH, which
  `GOTOOLCHAIN` does not change; `which gofmt` named the 1.26.10 one):
  exit 0. The changed files were written with that toolchain's `gofmt -w`.
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-3a-train/`:
  exit 0.

On `b9964e30` (the ruling on gh's two wrappers, `internal/mergetrain/`
only; the note's commit after it has the same Go tree), from the worktree
root, each run once:

- `go build ./... && go vet ./... && go test -count=1 ./...` (the whole
  module): exit 0, 56 packages `ok`.
- `cd pi && npm test`: exit 0, `ℹ tests 116`, `ℹ pass 115`, `ℹ fail 0`,
  `ℹ skipped 1`; `node scripts/lint.mjs`: exit 0.
- `NPM_FLAGS=--offline make ci` (the PyYAML venv first on PATH): exit 0,
  70 packages `ok`, pi-test `ℹ fail 0`.
- `GOTOOLCHAIN=go1.26.10 make fmt-check`: exit 0; with the go1.26.10
  toolchain's `bin/` first on PATH (`which gofmt` the 1.26.10 one): exit
  0.
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-3a-train/`:
  exit 0.

On `c31a4256` (Codex's fourth pass, `internal/mergetrain/` only; the
note's commit after it has the same Go tree), from the worktree root,
each run once, one after another:

- `go build ./... && go vet ./... && go test -count=1 ./...` (the whole
  module): exit 0, 56 packages `ok`.
- `cd pi && npm test`: exit 0, `ℹ tests 116`, `ℹ pass 115`, `ℹ fail 0`,
  `ℹ skipped 1`; `node scripts/lint.mjs`: exit 0.
- `NPM_FLAGS=--offline make ci` (the PyYAML venv first on PATH): exit 0,
  70 packages `ok`, pi-test `ℹ fail 0`.
- `make fmt-check` with the go1.26.10 toolchain's `bin/` first on PATH
  and `GOTOOLCHAIN=go1.26.10` (`which gofmt` the 1.26.10 one): exit 0.
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-3a-train/`:
  exit 0.

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
  with the head unmoved, ignores (the check decides); a mutation GitHub
  definitely refused is marked `update_failed` and recovers nothing; one
  that failed ambiguously stays live and recovers only a push inside its
  window.
- The classes are read from gh's error text; a message gh changes the
  wording of falls to the ambiguous class (the request stays live, bounded
  by its window), never to the definite one, unless it carries `(HTTP 4`
  or is, on gh's last stderr line, exactly a table message in one of
  gh's two wrappers, whole.
- The refusal classes rest on gh's printed format, which belongs to gh's
  version: the two wrappers are gh 2.100.0's (`gh: <message>` from `gh
  api graphql`, `GraphQL: <message> (<path>)` from go-gh v2.16.0's
  `GraphQLError`), measured and cited above. A gh that prints either
  differently, or adds text around it, falls to the ambiguous class (the
  request stays live, bounded by its window), the safe side, never to the
  definite one; the class rows pin both forms, so a change of format
  shows there first.
- `bindAdmitted` holds the audit lock across the task store's write, so a
  slow store write delays the journal's appenders (the train's own run and
  the lead's verb) for its length; the store write is local and short.
- A stale re-bind refused on the conditional write leaves its `rebase` and
  `rebind` entries journaled under the re-bind's run, followed by the
  terminal `rebind_superseded` entry: `headAdmitted` (`audit.go`) refuses
  the head that entry names on every path that trusts or binds it, so the
  force-push rule treats a push of it as not the train's, and no replay,
  recovery or train update binds it until the lead's verb re-admits it.
- The lead verb's base candidates are the journaled bases first, then the
  base's current tip; the first match names the parent (a journaled base
  that is also the current tip is named as journaled), and a parent that
  equals only the current tip is accepted on the proof alone (one force
  push, equal patch).
- The scheduler's apply step runs the owner's `gh` from the scheduler's
  process environment; a scheduler started without `gh` on its PATH refuses
  every re-bind `rebind_failed` with gh's error, and the decision line
  records it.
