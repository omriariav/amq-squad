# 3.2.0-0c-B: a tamper alarm stops the lead once, and the lead's recovery is a recorded decision

Brief: `brief-320-0c-B-recovery.md` (part B of 3.2.0-0c, approved with the
plan reviewer's three edits: who may recover, the chain continues, a
carried entry is checked). Branch `v3-320-0c-B`, cut from `origin/v3` at
`87fb71e9` (#893 merged, the directory-slot quarantine of part A). Built as
a lead PR by the plan reviewer's ruling (the codex seat is closed until the
next release). Declared files only: `internal/scheduler/`,
`internal/cli/`, `internal/schedproc/`, `internal/seatcommit/`,
`internal/uninstall/`, `pi/extensions/lib/words.ts`, `pi/test/`,
`docs/v3-spike/320-0c-quarantine/`. Four code commits (`33987fed`
seatcommit, `5ab002d3` scheduler and schedproc, `417a977e` cli, `7ca9da50`
uninstall and words), then this note.

## Why

0c-A makes every refused ledger read quarantine the body and head and
refuses the write. What was still 3.1.2 behaviour on `87fb71e9`: the
scheduler's prepare retried a tampered seat every tick (1,949 identical
`seat_prepare_failed` lines over 65 minutes on 2026-11-05), the task stayed
pending with no escalation, a cleared warning alone would have looped (the
next reader refuses the same document), the commit step mapped a
write-time refusal to a deferral note (`commit ledger: ...`, retried into
the same refusal at the next tick), the sidecar's binary line was a
settable variable nobody set, and uninstall listed the quarantine under the
session state, removed by `--write` alone.

## The rule as built

**Rule 1, the typed block raised once (`internal/scheduler/tamper.go`,
`scheduler.go`; `internal/schedproc/prepare.go`).** `seatcommit.quarantine`
now returns a typed `QuarantinedError` (the seat, the head seq the slot is
named by, the slot's path; its text is 0c-A's `...; the refused document is
quarantined at <slot>`, and it unwraps to `sealchain.ErrTampered`).
`CleanSeats.PrepareSeat` maps any refusal that is `ErrTampered` (the read,
or `CleanSeat`'s write-time refusal) to `scheduler.LedgerTamperedError`
with those fields (`prepareError`); a copy that failed leaves the path
empty and the detail says so. The scheduler's dispatch hands a prepare
error to `prepareFailed`: a `LedgerTamperedError` is `blockOnTamper`, which
logs one `seat_prepare_failed` line with `ReasonCode: ledger_tampered` and
the detail `seat H: quarantined at <path> (seq N): <refusal>` (the path
first, since the screen's record line is cut at 120 characters), blocks
the task `ledger_tampered` for the lead with the same detail, logs
`blocked`, and records the seat in `s.tampered` with the task. `idleSeat`
skips a held seat, so no prepare of it is attempted and nothing more is
logged until the recovery; a second task of the pool waits for another
seat (in roster mode, `seat_cap`: the held seat is live and busy from the
dispatch's view). The hold is written to the state file
(`State.LedgerTampered`) and restored at a restart
(`RestoreLedgerTampers`, logged once as `ledger_tampers_restored`), so the
alarm is raised once, not once per incarnation.

**Rule 2, every other prepare failure once per (task, seat, reason)
(`tamper.go`).** `prepareFailed` keeps the last failure per seat: the task
(its id and its record's last change, so any change to the task resets
it), the reason text and a count. The first failure of a key logs
`seat_prepare_failed` as before; the second logs one
`seat_prepare_suppressed` count line ("the same prepare failure repeats;
further repeats are counted, not logged, until the task, the seat or the
reason changes"); later repeats are counted. A different reason, a changed
task, another seat, or a prepare that passes (`preparePassed`) starts the
sequence again. The task stays pending and the seat is tried every tick,
as before.

**Rule 3, the recovery (`internal/seatcommit/recover.go`,
`internal/schedproc/recover.go`, `decide.go`, `process.go`,
`internal/cli/scheduler_recover.go`).** The way back is a lead decision
over the scheduler's decision socket, a new kind
`amq-squad.ledger-recover.v1` proved under R4 exactly as a widening (the
kernel-named sender's stamped chain must reach the lead's recorded
incarnation, proved before the request is read and again after). The CLI
verb `scheduler recover --seat H --seq N [--slot K] [--carry TASK:COMMIT
...] --reason TEXT` reads the launch record and sends; it writes nothing
(the verb's row checks the root holds no ledger, quarantine or seed after
a send). The scheduler's apply step (`recoverDecision`) checks the shape,
runs `Recovery.Apply` (`seatcommit.RecoverLedger` with the launch's record
of the grant, the target `scheduler run` computed at its start), appends
one `ledger_recovered` line naming the lead, the seat, the quarantined seq,
the slot, the new seq, the carried entries, the base and the reason, and
calls `Scheduler.LedgerRecovered`, which at the next tick drops the hold
and returns every task the alarm blocked `ledger_tampered` to pending
(`task_reset`, "the commits ledger of seat H was recovered by the lead").
A clear is not the recovery: `ledger_tampered` is not in
`ClearableWarnings` (refused `clear_not_a_warning`, as before), and no
clear touches `s.tampered`.

`RecoverLedger`, under the seat's ledger lock: the document on disk must
be tampered (one that verifies is `recover_not_tampered`; one absent with
no head is the launch's to record) and byte-identical to the slot's copies
(body and head, presence included): a document edited since the slot was
made is quarantined in a slot of its own through 0c-A's `quarantine` with
the verb `scheduler recover`, and the decision is refused
`recover_slot_stale` naming that slot, so the lead decides on what is on
disk, never on a copy of something else. The slot is read through the same
descriptor-anchored, no-follow opens the copy uses (`readSlot`), and its
sidecar must name this seat and carry the digests of the copies present
(the sha256 of each copy is recomputed and compared). The chain continues:
N and the quarantined head's link tag come from the head copy
(`seq`, `link_tag`), else from the body copy's own seal (a slot whose head
did not decode, `sequnknown`, named `--seq unknown`); the new first link is
`sealchain.Seal` with prev `{Seq: N, Tag: <that tag>}`, so it is seq N+1
and links to the quarantined head; the body is written with `atomicfile`
and the head moved with `sealchain.WriteHead` under the session key; the
written chain is read back through `readLedger` before the result is
returned. The new body is what a launch records, observed now: the
worktree and branch of the staged launch record, the base as the hardened
git resolves the worktree's HEAD (the core's observation, as
`RecordWorktreeBase` makes it), the grant's target from the caller, the
evidence counter from the highest `refs/amq-squad/evidence/<seat>/...`
number the seat repository holds (so a later anchor never overwrites an
earlier chain's ref), no lease and no rest, and `Recovered`
(`LedgerRecovery`: the quarantined seq, the slot's path, the body and head
sha256 from the sidecar, who, why, when, the carried entries as
`task:commit`). The quarantined body's own base, rest, lease and target are
not carried: a tampered `gh_path` there would be executed by the next push.
The quarantined copies are read for their digests and entries, never as a
ledger (nothing verifies or moves them), never sealed, never removed.
Restoring the quarantined body, or the seq-N original it was edited from,
after the recovery reads as tampered: its tag fails, or its seq is behind
the head.

A carried entry is checked, not trusted: it must be in the quarantined
body by task and commit (`recover_entry_unknown`); its commit must be on
origin's branch for the task (the entry's branch, else `TaskBranch`), read
through the launch's push URL with `ls-remote` (the pinned gh as the
credential helper for https, as the push uses it) and `merge-base
--is-ancestor` against that tip in the seat repository
(`recover_entry_unpushed`; a launch with no push URL carries nothing); and
`<commit>^{tree}` must equal the quarantined entry's tree
(`recover_entry_tree_mismatch`). One entry that fails refuses the whole
recovery and nothing is written. A carried entry keeps the quarantined
record's fields, is marked `Pushed` and `Note: "carried from quarantine seq
N"` (a new `omitempty` field of `LedgerCommit`, so a ledger sealed before
it still verifies), and the recovery record lists it.

**Rule 4, the commit step's write-time refusal (`commit.go`
`writeOutcome`).** The two `writeLedger` calls of `commitAndPush` (the
commit recorded, the push recorded) map a `sealchain.ErrTampered` to the
typed block: `ledger_tampered`, route lead, detail `the commits ledger
refused the write: <refusal with the slot>; nothing is recorded or pushed
until the lead recovers the ledger (scheduler recover)`; any other write
failure stays a deferral. The read-time refusal at the step's start
(`tamperedRefusal`) is unchanged: `ledger_tampered` routed to the owner
with its 3.1 words. The settle's note after a successful push is unchanged
too (the commit and the push are done; the next prepare raises the block).

**Rule 5, the version (`internal/cli/cli.go`).** `cli.Run` sets
`seatcommit.BinaryVersion = version`, the string main resolved from the
ldflag and `amq-squad version` prints; the sidecar's `binary:` line is
that string.

**Rule 6, uninstall (`internal/uninstall/plan.go`, `execute.go`).**
`quarantine` joins `sealedNames`: listed in the dry run in the sealed
ledgers group beside the commits ledger with its own note ("the commits
ledger's quarantine (the refused documents a tamper alarm copied; evidence,
kept with the ledger)"), kept by `--write` alone, removed only with
`--purge` as the ledger is, by no verb and never by the harness. The
brief's "session group" is read as the group the commits ledger is in (the
sealed ledgers), since the rule is "like the commits ledger"; on
`87fb71e9` the directory fell to the default session-state group and
`--write` alone removed it.

**Rule 7, the words (`pi/extensions/lib/words.ts`).** `BLOCK_WORDS.
ledger_tampered` is "the seat's commit record failed its check; the lead
investigates before the seat works again". `decisionWords` already
composes a stop's line from its reason code through the block table, so a
`seat_prepare_failed` whose `reason_code` is `ledger_tampered` reads "seat
H could not be prepared for T: the seat's commit record failed its check;
the lead investigates before the seat works again", and "relaunch the
roster" (every other prepare failure's remedy) does not appear; the
record line below the rule is the decision's reason, which starts with the
seat and the quarantine path. The typed block reads with the same words in
the tasks block; `blockNeedsRecord` now fetches a `ledger_tampered` block's
detail, and `blockWords` puts `T: seat H, quarantined at <path>`
(`tamperRecord`, from the detail's `seat H: ... quarantined at PATH`) on
the record line. `seat_prepare_suppressed` is a stop with words, resolved
by the seat's `seat_up` or a roster launch; `ledger_recovered` and
`ledger_tampers_restored` are routine; `scheduler recover` is a verb head
for `plainDetail`. The row-7 scan passes: the new constant
`scheduler.ReasonLedgerTampered` maps (`ledger_tampered`), every new
`Decision{Kind:}` is a stop with words or routine, and the three
expectations that quoted the old owner wording for an `exit_refused` stop
move to the new words. What the brief says in one line and this PR does
not do as written: "the typed block shows under waiting on you". The
screen's "waiting on you" block lists open gates
(`pi/extensions/lib/screen.ts`, outside the declared set); every
lead-routed block shows in the tasks block, and this one does too, with
the words above. The lead sees it there, and the seat's stop line in the
seats block says the same.

## The quarantine layout read

A slot is `<root>/extensions/amq-squad/quarantine/commits.<handle>.seq<N>[.k]/`
with `body.json`, `head.json` and `quarantine.txt`; the decision names it
by handle, seq (`--seq N`, or `unknown`) and k (`--slot K`, 1 for the first);
`RecoverSlotName` builds the name as `quarantineBase` does. The body sha256
the new chain records is the sidecar's `body: body.json sha256 <hex>` line,
checked against the copy. Uninstall lists the whole directory. The
sidecar's `binary:` line is `seatcommit.BinaryVersion`, threaded from the
CLI.

## Rows

| Row | What it proves | Test |
|---|---|---|
| B1 | A tampered ledger at prepare produces exactly one `seat_prepare_failed` line (with `reason_code` `ledger_tampered`) and one typed block over 300 ticks; the block's detail and the line name the seat, the quarantine path and the seq; the seat is prepared once and never again; a second task of the pool is never dispatched to it and stays pending. Over the real preparer and a real tampered ledger: one line and one block over 40 ticks, the line and the block naming the slot. | `internal/scheduler` `TestTamperAlarmBlocksOnceAndHoldsTheSeat`; `internal/schedproc` `TestRecoveryThroughTheSchedulerMakesTheSeatGrantable` |
| B1 restart | The hold is in the state file (`ledger_tampered`: the seat, the seq, the task); a new incarnation restores it and never prepares the held seat (one prepare across the restart). | `internal/scheduler` `TestTamperHoldSurvivesARestart` |
| B2 | After the recovery decision the seat is grantable: the ledger half (the chain, below); the scheduler half (`LedgerRecovered` returns the blocked task to pending with `task_reset` and the next tick prepares and dispatches to the seat); the process half (the apply step writes the chain through `Recovery.Apply`, appends the `ledger_recovered` line naming the lead, the seat, `quarantine seq 1`, the slot, `continues at seq 2`, the carried entries and the reason; the next tick's `task_reset` and `dispatched` to the seat, whose real `CleanSeat` now passes; the quarantine copy is byte-identical after). | `internal/seatcommit` `TestRecoveryContinuesTheChainAfterTheQuarantinedSeq`; `internal/scheduler` `TestRecoveryReleasesTheSeatAndReturnsTheTask`; `internal/schedproc` `TestRecoveryThroughTheSchedulerMakesTheSeatGrantable` |
| B3 | A clear without a recovery decision does not make the seat grantable: `ledger_tampered` is refused `clear_not_a_warning`; a warning the seat does show, cleared, and the lead's own reset of the task, leave the seat held (10 ticks: no dispatch, no prepare line). | `internal/schedproc` `TestRecoveryThroughTheSchedulerMakesTheSeatGrantable`, `TestRecordClearAcceptsOnlyAWarningShowingOnTheCurrentLaunch` (0c-A's, unchanged); `internal/scheduler` `TestRecoveryReleasesTheSeatAndReturnsTheTask` |
| B3b | Over the decision socket: a sender whose chain does not reach the lead is `not_lead` and nothing is applied; the lead's pane is applied with the request as sent; a scheduler without the hook refuses `recover_unsupported`; a malformed seat, seq, slot or carry is `recover_malformed` before the chain writer runs; the writer's refusal code is the reply's. | `internal/schedproc` `TestRecoveryIsProvedFromTheLeadsPane` |
| B3c | The new chain's first link is seq 2 with prev = the quarantined head's `link_tag` (seq 1 tampered); the recovered body records the quarantined body's sha256 and the slot's path, the launch record's worktree and branch, the worktree's HEAD as base and the grant; the quarantined body copied back is refused as tampered, and so is the seq-1 original it was edited from (`is at link 1, its head records link 2`); the next writer continues at seq 3 keeping the recovery record. | `internal/seatcommit` `TestRecoveryContinuesTheChainAfterTheQuarantinedSeq` |
| B3 refusals | A verifying ledger is `recover_not_tampered`; a slot or k that is not there `recover_slot_missing`; a planted slot whose sidecar names another seat `recover_slot_invalid`; a document edited again since the slot was made is quarantined in `<slot>.2` and the decision refused `recover_slot_stale` naming it, nothing written, the first slot untouched; the decision on `.2` is then accepted. | `internal/seatcommit` `TestRecoveryRefusesWhatItCannotCheck` |
| B4 | A non-tamper prepare failure over 200 ticks logs one `seat_prepare_failed` and one `seat_prepare_suppressed` line; the seat is tried every tick and the task stays pending; a changed reason, a changed task record, a passing prepare, and another seat each start the sequence again. | `internal/scheduler` `TestPrepareFailureIsLoggedOnceThenCounted` |
| B5 | A real pushed entry named by task and commit is carried (`Pushed`, `Note: carried from quarantine seq N`, listed in the recovery record); the forged entries the tampered body also holds are not; naming an unpushed commit (`recover_entry_unpushed`), the real commit under a forged tree (`recover_entry_tree_mismatch`) or an entry the body lacks (`recover_entry_unknown`) refuses the whole recovery with the document unchanged. | `internal/seatcommit` `TestRecoveryCarriesOnlyCheckedEntries` |
| B6 | The document edited after the step's read and before its write (the `AfterSnapshot` seam): the step's answer is the typed block `ledger_tampered` routed to the lead, not a deferral, its detail naming the write and the slot; the slot holds the refused body; nothing is pushed; the document is unmoved. | `internal/seatcommit` `TestWriteTimeRefusalIsTheTypedBlock` |
| B7 | `Run([]string{"version"}, "3.2.0-test.7")` prints `amq-squad 3.2.0-test.7` and sets `seatcommit.BinaryVersion`; a quarantine made after it carries `binary: 3.2.0-test.7`. | `internal/cli` `TestSidecarCarriesTheVersionTheCLIPrints` |
| B8 | The dry run lists the quarantine directory in the sealed ledgers group beside the commits ledger with its note, not under the session state; `--write` alone keeps both; `--purge` removes both. | `internal/uninstall` `TestQuarantineIsListedAndRemovedWithTheCommitsLedger` |
| B9 | The screen's seat stop line for a tamper prepare failure is the tamper words; "relaunch the roster" does not appear; the record below the rule names the path; every other prepare failure keeps its remedy; the count line reads; the typed block reads with the same words in the tasks block with `T: seat H, quarantined at <path>` below the rule, from the block's detail (and from the commit step's write-time detail). | `pi/test/recovery.test.ts` (two tests) |
| the verb | `scheduler recover` refuses missing or malformed flags and `--me` before any send; with no launch record it names the missing scheduler; a record with no socket is `scheduler_starting` (exit 15); a send carries the seat, seq, slot, carry and reason as given and prints the reply (the slot, the chain's seqs, each carried entry); the verb writes no ledger, quarantine or seed; a refusal is exit 15 with the code. | `internal/cli` `TestSchedulerRecoverSendsTheDecisionOverTheSocket` |

Rule 18: every row runs under a `t.TempDir` root (the seatcommit fixture's
`amq/sess`; the scheduler harness's store; the schedproc live rig's root
with a temp seat repository; the cli rows' temp roots; the uninstall
fixture), no network (the origins are bare repositories in the temp dir;
`ls-remote` reads them by path). The seatcommit rows and the schedproc
process row set `HOME` to a temp dir with `t.Setenv`, since they run git;
the scheduler, cli and uninstall rows run no git of their own (the cli
version row quarantines a hand-edited ledger with no repository) and leave
`HOME` as it is; the pi rows render a fixture.

## Revert runs

Each run on the committed tree `7ca9da50`: the line flipped, the row run
alone, the first failing line verbatim (the temp-dir prefix as printed),
then `git checkout -- <file>` and the row re-run green. The runner is in
the job scratch, not committed. A flip is a compiling inverse of the line's
rule. In the B3c and B6 lines the fixture's 40- and 64-character hex values
(a link tag, digests, commit ids of the temp repository) are written as
`<40 hex>` and `<64 hex>`, so this note holds no hex run.

### B1

```
go test -count=1 -run ^TestTamperAlarmBlocksOnceAndHoldsTheSeat$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:73`
```
	if errors.As(err, &tampered) {
		return s.blockOnTamper(t, handle, tampered, now, log)
	}
```
to:
```
	if errors.As(err, &tampered) && false {
		return s.blockOnTamper(t, handle, tampered, now, log)
	}
```
First failing line (exit 1):
```
tamper_test.go:70: blocked 0 times, the seat prepared 300 times; want 1 and 1
```
`--- FAIL: TestTamperAlarmBlocksOnceAndHoldsTheSeat (0.11s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.479s`.

### B1 restart

```
go test -count=1 -run ^TestTamperHoldSurvivesARestart$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:182`
```
	for _, tm := range st.LedgerTampered {
```
to:
```
	for _, tm := range st.LedgerTampered[:0] {
```
First failing line (exit 1):
```
tamper_test.go:136: the restarted scheduler tried the held seat: [{Kind:seat_up TaskID: Seat:seat-a Pool:claude:sonnet:medium Reason: Stage: ReasonCode:} {Kind:seat_prepare_failed TaskID:t2 Seat:seat-a Pool: Reason:seat seat-a: quarantined at /root/extensions/amq-squad/quarantine/commits.seat-a.seq3 (seq 3): the commit ledger of seat seat-a: commit ledger seat-a: ledger_tampered: the tag of commits.seat-a link 3 fails; the refused document is quarantined at /root/extensions/amq-squad/quarantine/commits.seat-a.seq3 Stage: ReasonCode:ledger_tampered} {Kind:blocked TaskID:t2 Seat:seat-a Pool: Reason:ledger_tampered Stage: ReasonCode:}]
```
`--- FAIL: TestTamperHoldSurvivesARestart (0.09s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.379s`.

### B2 the scheduler

```
go test -count=1 -run ^TestRecoveryReleasesTheSeatAndReturnsTheTask$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:146`
```
	s.recovered = append(s.recovered, handle)
```
to:
```
	_ = handle // the recovery not applied
```
First failing line (exit 1):
```
tamper_test.go:185: the recovery did not return the task and dispatch it: []
```
`--- FAIL: TestRecoveryReleasesTheSeatAndReturnsTheTask (0.13s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.401s`.

### B2 the ledger

```
go test -count=1 -run ^TestRecoveryContinuesTheChainAfterTheQuarantinedSeq$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:238`
```
		Target: o.Target, EvidenceSeq: evidenceSeqOf(repo, o.Handle), Recovered: recovery}
```
to:
```
		Target: o.Target, EvidenceSeq: evidenceSeqOf(repo, o.Handle), Recovered: nil}
```
First failing line (exit 1):
```
recover_test.go:97: the recovery record: <nil>
```
`--- FAIL: TestRecoveryContinuesTheChainAfterTheQuarantinedSeq (0.22s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.656s`.

### B2 the process

```
go test -count=1 -run ^TestRecoveryThroughTheSchedulerMakesTheSeatGrantable$ ./internal/schedproc/
```
Reverted `internal/schedproc/recover.go:136`
```
		s.LedgerRecovered(req.Seat)

```
to:
```
		_ = s // the scheduler not told

```
First failing line (exit 1):
```
recover_test.go:211: after the recovery: []
```
`--- FAIL: TestRecoveryThroughTheSchedulerMakesTheSeatGrantable (0.81s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	1.281s`.

### B3

```
go test -count=1 -run ^TestRecoveryThroughTheSchedulerMakesTheSeatGrantable$ ./internal/schedproc/
```
Reverted `internal/scheduler/scheduler.go:1028`
```
		if s.tamperedSeat(h) {
			continue // held by a tamper alarm until the lead's recovery
		}

```
to:
```
(the lines removed)
```
First failing line (exit 1):
```
recover_test.go:173: the cleared seat was tried: [{Kind:seat_prepare_failed TaskID:t1 Seat:seat-t1 Pool: Reason:seat seat-t1: quarantined at /var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryThroughTheSchedulerMakesTheSeatGrantable762043289/002/extensions/amq-squad/quarantine/commits.seat-t1.seq1 (seq 1): the commit ledger of seat seat-t1: commit ledger seat-t1: ledger_tampered: the tag of commits.seat-t1 link 1 fails; the refused document is quarantined at /var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryThroughTheSchedulerMakesTheSeatGrantable762043289/002/extensions/amq-squad/quarantine/commits.seat-t1.seq1 Stage: ReasonCode:ledger_tampered} {Kind:blocked TaskID:t1 Seat:seat-t1 Pool: Reason:ledger_tampered Stage: ReasonCode:}]
```
`--- FAIL: TestRecoveryThroughTheSchedulerMakesTheSeatGrantable (0.76s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	1.258s`.

### B3b

```
go test -count=1 -run ^TestRecoveryIsProvedFromTheLeadsPane$ ./internal/schedproc/
```
Reverted `internal/schedproc/decide.go:361`
```
	if pane == nil {
		return DecideReply{Code: RefusedNotLead,
```
to:
```
	if pane == nil && false {
		return DecideReply{Code: RefusedNotLead,
```
First failing line (exit 1):
```
recover_test.go:264: a seat's process: {OK:true Code: Detail: Data:null} <nil> (applied 1)
```
`--- FAIL: TestRecoveryIsProvedFromTheLeadsPane (0.00s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.368s`.

### B3c

```
go test -count=1 -run ^TestRecoveryContinuesTheChainAfterTheQuarantinedSeq$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:246`
```
	link := sealchain.Seal(key.Key, ledgerLabel, &sealchain.Link{Seq: oldSeq, Tag: prev}, l)
```
to:
```
	link := sealchain.Seal(key.Key, ledgerLabel, nil, l)
```
First failing line (exit 1):
```
recover_test.go:87: the result: {Handle:s1 Slot:/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryContinuesTheChainAfterTheQuarantinedSeq2031675562/001/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1 OldSeq:1 NewSeq:1 Prev:<64 hex> BodySHA256:<64 hex> HeadSHA256:<64 hex> Base:<40 hex> Carried:[]}
```
`--- FAIL: TestRecoveryContinuesTheChainAfterTheQuarantinedSeq (0.22s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.563s`.

### B4

```
go test -count=1 -run ^TestPrepareFailureIsLoggedOnceThenCounted$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:81`
```
	if f == nil || f.task != key || f.reason != err.Error() {
```
to:
```
	if f == nil || f.task != key || f.reason != err.Error() || true {
```
First failing line (exit 1):
```
tamper_test.go:213: 200 failed lines and 0 suppressed lines over 200 ticks, want 1 and 1
```
`--- FAIL: TestPrepareFailureIsLoggedOnceThenCounted (0.09s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.452s`.

### B5

```
go test -count=1 -run ^TestRecoveryCarriesOnlyCheckedEntries$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:407`
```
		if err != nil || tree != e.Tree {
```
to:
```
		if err != nil {
```
First failing line (exit 1):
```
recover_test.go:248: not a recovery refusal: <nil>
```
`--- FAIL: TestRecoveryCarriesOnlyCheckedEntries (0.59s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.918s`.

### B6

```
go test -count=1 -run ^TestWriteTimeRefusalIsTheTypedBlock$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/commit.go:909`
```
	if errors.Is(err, sealchain.ErrTampered) {
		return r.refuse(ReasonLedgerTampered, task.RouteLead,
```
to:
```
	if errors.Is(err, sealchain.ErrTampered) && false {
		return r.refuse(ReasonLedgerTampered, task.RouteLead,
```
First failing line (exit 1):
```
recover_test.go:295: the write-time refusal is not the typed block for the lead: {OK:false Deferred:true ReasonCode: Route: Detail:commit ledger: commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestWriteTimeRefusalIsTheTypedBlock1064647292/001/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1 Task:t1 Seat:s1 Branch:seat/s1-t1 Parent:<40 hex> Tree:<40 hex> Commit:<40 hex> Pushed:false Changes:[{Path:cmd/root.go Added:1 Deleted:0 Binary:false}] Lines:1 Files:1 CapLines:300 CapFiles:5 DeclaredDigest:sha256:<64 hex> DiffDigest:sha256:<64 hex> Violations:[] Notes:[] PRRepository: PRNumber:0 PRHead: Verifier:<nil>}
```
`--- FAIL: TestWriteTimeRefusalIsTheTypedBlock (0.34s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.678s`.

### B7

```
go test -count=1 -run ^TestSidecarCarriesTheVersionTheCLIPrints$ ./internal/cli/
```
Reverted `internal/cli/cli.go:80`
```
	seatcommit.BinaryVersion = version

```
to:
```
	_ = seatcommit.BinaryVersion

```
First failing line (exit 1):
```
scheduler_recover_test.go:116: BinaryVersion is "" after Run
```
`--- FAIL: TestSidecarCarriesTheVersionTheCLIPrints (0.00s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.382s`.

### B8

```
go test -count=1 -run ^TestQuarantineIsListedAndRemovedWithTheCommitsLedger$ ./internal/uninstall/
```
Reverted `internal/uninstall/plan.go:81`
```
	sealedNames = map[string]bool{"commits": true, "quarantine": true, "ledger": true, "routing": true}
```
to:
```
	sealedNames = map[string]bool{"commits": true, "ledger": true, "routing": true}
```
First failing line (exit 1):
```
quarantine_test.go:39: the sealed group does not list the commits ledger and its quarantine:
```
`--- FAIL: TestQuarantineIsListedAndRemovedWithTheCommitsLedger (0.23s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/uninstall	0.510s`.

### B9

```
cd pi && node --test --test-reporter=spec test/recovery.test.ts
```
Reverted `pi/extensions/lib/words.ts:544`
```
	const f = own(BLOCK_WORDS, code);
	// a launch decision
```
to:
```
	const f = undefined;
	// a launch decision
```
First failing line (exit 1):
```
✖ B9: a tamper prepare failure shows the tamper words and the path; relaunch the roster does not appear (1.4535ms)
```
`✖ B9: a tamper prepare failure shows the tamper words and the path; relaunch the roster does not appear (1.4535ms); ℹ fail 1`
restored and re-run (exit 0): `ℹ pass 2; ℹ fail 0`.

### the verb

```
go test -count=1 -run ^TestSchedulerRecoverSendsTheDecisionOverTheSocket$ ./internal/cli/
```
Reverted `internal/cli/scheduler_recover.go:38`
```
	if flagWasSet(f.fs, "me") {
		return usageErrorf("scheduler recover: takes no --me
```
to:
```
	if flagWasSet(f.fs, "me") && false {
		return usageErrorf("scheduler recover: takes no --me
```
First failing line (exit 1):
```
scheduler_recover_test.go:49: --me: scheduler recover: no running scheduler for /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestSchedulerRecoverSendsTheDecisionOverTheSocket4191911564/001 (the recovery is the scheduler's to write): open /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestSchedulerRecoverSendsTheDecisionOverTheSocket4191911564/001/extensions/amq-squad/scheduler/launch.json: no such file or directory (sent 0)
```
`--- FAIL: TestSchedulerRecoverSendsTheDecisionOverTheSocket (0.00s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/cli	0.384s`.


## Gates

On `7ca9da50` (the four code commits; this note is the only change
after it), each exit 0, from the worktree root:

- `go build ./... && go vet ./...`: exit 0.
- `go test -count=1 ./internal/scheduler/ ./internal/schedproc/
  ./internal/seatcommit/ ./internal/cli/ ./internal/uninstall/`: exit 0,
  five `ok`.
- `NPM_FLAGS=--offline make ci`: exit 0, with the job scratch's PyYAML
  venv first on PATH (`PATH=<job tmp>/pyvenv/bin:$PATH`, as the 0c-A note
  ran it; no network, nothing installed outside the scratch). It ran
  fmt-check, `go vet`, `go test -timeout 20m ./...` (70 `ok`, no `FAIL`),
  the README and docs html checks, skills-check, skill-routing-check,
  skill-command-check, skill-invocation-check, release-validator-test,
  go-files-scope-test, claude-hooks-test and pi-test: `cd pi && npm ci
  --ignore-scripts --offline && npm test`, tail `ℹ tests 118`, `ℹ pass
  117`, `ℹ fail 0`, `ℹ skipped 1` (the staged-runtime row, local-only).
  No timing row stopped it; it ran once.
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`:
  exit 0 (on the tree with this note).
- `GOOS=windows go build ./internal/seatcommit/`: exit 0 (the slot read
  goes through 0c-A's path-based version off unix).

## Residual risk and what the brief leaves to later

- The screen's "waiting on you" block is open gates (`screen.ts`, outside
  this task's set); the typed block shows in the tasks block with the
  rule-7 words and the seat stop line in the seats block says the same.
  Moving lead-routed blocks under "waiting on you" is a screen change for a
  screen brief.
- The commit step's read-time refusal (`tamperedRefusal`, the start of the
  step and the exit check) still routes `ledger_tampered` to the owner with
  its 3.1 words ("the owner inspects the commits ledger and the evidence
  refs"); the brief's rules name the prepare block and the write-time
  refusal only. The recovery is the lead's either way: a task blocked for
  the owner is not reset by the lead (`ResetBy`), but the recovery returns
  only the tasks the prepare alarm held, so such a task waits for the
  owner's reset after the lead's recovery. One line to change if the
  reviewer wants the read-time refusal routed to the lead too.
- The recovered body's base is the worktree's HEAD as observed at the
  recovery (what a launch observes); the seat is idle at that point (held
  since its prepare). A seat repository the seat itself moved between the
  alarm and the recovery gives that HEAD as the base, as it would at a
  relaunch. The next `StartTask` sets the lease's start from the base tip
  fetched at the grant, so the base matters until then.
- The evidence refs of discards the quarantined body recorded are not
  carried (the discard records are untrusted); the refs stay in the seat
  repository under `refs/amq-squad/evidence/<seat>/...` and the counter
  continues past them, so nothing is overwritten and nothing is pruned by
  the harness. `evidence purge` after the retention is as before.
- A held seat stays held across a roster relaunch (the hold is by handle),
  so a relaunched seat whose ledger is still tampered is not prepared; the
  launch's `RecordWorktreeBase` refuses as a writer (0c-A) and the lead
  recovers first.
- The count line of rule 2 is logged at the second repeat, with no total:
  the count lives in the scheduler's memory and is not written to the
  state file.
- A slot whose head did not decode (`sequnknown`) is recovered from the
  body copy's own seal (`--seq unknown`); a slot with neither a decodable
  head nor a sealed body is `recover_seq_unknown` and stays as evidence.
- Off unix the slot is read through the path-based compatibility version
  of 0c-A.
