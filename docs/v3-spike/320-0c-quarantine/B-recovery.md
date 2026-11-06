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
uninstall and words), then this note; then `88df5bad`, Codex's pass on
#904, and `8017ffea`, the plan reviewer's rulings on that batch (the
section "Codex's pass on #904" below; the rows they added and their revert
runs are in their own lists); then `ccec9400`, the reviewer's two items on
`c9826c31` (the covers check's deciding row and the verified recovery; the
seatlaunch stop classified); then `23f315e3` and `01088a5c`, Codex's
re-pass on `c9826c31` (only not-found skips a task of a released hold, a
failing read or reset keeps the hold and is logged once then counted, the
timeout row's child pid); then `c27ad8fc`, the verified recovery's
"behind the slot" refusal made nil-safe; then `b250b951`, the note; then
`9de43a6c` and `8eeeaf24`, Codex's re-pass on `b250b951` (the section of
that name below), then `af1a6124`, the note; then `ad3330e0`, the plan
reviewer's ruling on `af1a6124` (the section "The plan reviewer on
af1a6124"), and `bd683f28`, its row for a hold on an earlier slot; then
`ede2226c`, the note; then `e1fc443c`, the masked key's row the reviewer
asked for; then this note. The helper move of `8017ffea` is between
two declared packages (`internal/schedproc/slots.go` to
`internal/seatcommit/baseshadow.go`), so it left the declared set
unchanged. `9de43a6c` widens it by two files, `internal/task/reset_if.go`
and `internal/task/reset_if_test.go` (the conditional reset Codex's P2-3
asks for), and `ad3330e0` changes the same two; the plan reviewer
accepted `internal/task` as a stated widening on `af1a6124`; the screen fix of the second phase (below,
"Gates") will widen it to `pi/extensions/lib/screen.ts` and its test.

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
the slot, the new seq, the carried entries, the base and the reason. The
release is the scheduler's own reconciliation, every tick, against the
seat's sealed ledger (Codex's P2 on #904, below): the preparer reads the
recovery record the ledger carries (`RecoveryReader`, `CleanSeats` through
`seatcommit.RecoveryOf`, no quarantine on a refusal), and a hold whose slot
the record names, or whose seq it is at or past, is released: every task
the alarm blocked `ledger_tampered` returns to pending (`task_reset`, "the
commits ledger of seat H was recovered by the lead"), then the hold goes
and `ledger_hold_released` is logged. A clear is not the recovery:
`ledger_tampered` is not in `ClearableWarnings` (refused
`clear_not_a_warning`, as before), and no clear touches `s.tampered`.

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
(`recover_entry_tree_mismatch`). These checks run in the harness's own git
directory (the per-step shadow, as the push does; Codex's P1 below), and
the one transport call is bounded (Codex's P2 below). One entry that fails
refuses the whole recovery and nothing is written. A carried entry keeps the quarantined
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

## Codex's pass on #904 (`88df5bad`)

Codex read `1107dabe` statically (its sandbox could not build). Each item,
and what changed:

- **P1, the carry check through the seat's git config.** `carryEntries`
  ran `ls-remote` through `seatRepo` (`GIT_DIR` = the seat repository's
  common dir), whose config the seat controls; unsetting filter drivers
  does not disable `url.*.insteadOf`, `core.sshCommand` or transport
  settings, so a planted rewrite could redirect the launch's push URL to a
  repository holding an unpushed commit (which then passed), and the SSH
  config could run seat-chosen commands in the scheduler's process. The
  plan reviewer's ruling: no new hardening; the merged one exactly, the
  pair the launch's base fetch uses (`baseTip`, 3.0.2 t22): a shadow git
  directory of the harness's own on the repository's object store (none of
  the shared repository's config, remotes or url rewrites read) and
  `seatcommit.FetchCredentials` (the pinned gh as the only credential
  helper for https, or anonymous). `seatcommit` cannot import `schedproc`,
  so the helper moved down (`baseGit`, `baseShadow`, `newBaseShadow` of
  `slots.go` are `seatcommit.BaseGit`, `BaseShadow`, `NewBaseShadow` in
  `baseshadow.go`; `moveWorktree` takes the git as its first argument) and
  both callers use it; the launch's fetch is unchanged in what it does.
  `carryEntries` makes the shadow on the seat repository's common dir and
  runs every check there (`ls-remote` with the fetch's credentials;
  `cat-file`, `merge-base`, `rev-parse` on the seat's objects through
  `GIT_OBJECT_DIRECTORY`). The base observation and the evidence counter
  stay on `seatRepo` (no transport; refs the seat repository holds). Row:
  `url.<evil>.insteadOf` of the origin and a `core.sshCommand` with a
  marker planted in the seat repository; the seat's own `ls-remote` follows
  the redirect and an ssh URL runs the planted command (both shown); the
  recovery refuses the unpushed entry, carries the real one, and the marker
  never appears.
- **P2, the unbounded transport.** `exec.Command(...).Run()` had no bound
  while the decision server clears its deadline before the apply step, so
  an unreachable origin held the ledger lock and the handler for as long as
  git waited. `BaseGit.RunBounded` runs one git call under a context in
  its own process group (`ownGroup`, the verifier's), killed as a group on
  expiry (`cmd.Cancel` through `killGroup`), `WaitDelay` the verifier's,
  the error naming the bound; the recovery's one network call (the
  `ls-remote`, once per distinct carried branch, none with no carry) runs
  under `RecoverTransportTimeout` (30 s, a variable the row shortens), and
  a transport failure refuses before anything is written, the ledger lock
  released with the refusal. The launch's fetch keeps `BaseGit.Run`
  (unbounded, `context.Background`), as merged: that gap predates rc.1 and
  goes to the 3.2.x list; the bounded runner is in the shared helper for
  that small PR. Row: a stalled ssh plumbing ends in
  `recover_entry_unpushed` ("(bound 500ms) ... did not answer within its
  bound; killed") within the bound, and the next recovery takes the lock at
  once and completes.
- **P2, the lost completion.** The release was a message
  (`LedgerRecovered`) consumed by the next tick: a failed decision append
  replied a refusal with the chain written, a retry refused
  `recover_not_tampered`, a crash before the state write left the restored
  hold with no message, and the tick deleted each hold before resetting
  its tasks, so a reset error lost the batch. Now the sealed ledger's
  `LedgerRecovery` is the source of truth: every tick, `applyRecoveries`
  asks the preparer what recovery each held seat's ledger records
  (`RecoveryReader.LedgerRecovery`, read without a quarantine) and releases
  a hold the record covers (`covers`: the hold's slot path, or a seq at or
  past the hold's numeric seq; a hold of an unknown seq by its slot only),
  resetting first and deleting after; a reset that fails returns the error
  and keeps the hold for the next tick (a task already returned is
  skipped). `RecoverLedger` decided again on a recovered ledger whose
  record names the same slot answers as made (`AlreadyRecovered`, the
  record's seqs and digests, the carried entries by their note) and writes
  nothing; the apply step records that line ("had recovered ... the
  decision sent again") and replies OK; a failed append is
  `recover_unrecorded` with the detail saying the chain is written and the
  seat releases from it. `Scheduler.LedgerRecovered` is gone (one path).
  Rows: the append fails, the tick releases anyway, the retry answers OK
  and records; the crash before the state write (the restored hold
  released on the new incarnation's first tick, the state file cleared);
  the retry in seatcommit; two seats held and one recovered across the
  state file and a restart (the other never prepared).
- **P2, the suppression per seat.** `prepareFails` was keyed by seat and
  remembered the last task, so two pending tasks alternating on one
  failing seat reset each other and logged two lines a tick. Keyed per
  task and seat (`prepareKey`), the task's record change and the reason
  in the value; a passing prepare clears the seat's keys. Rows: two tasks,
  one line and one count line each over 200 ticks; the same seat passing
  (the task dispatched and released) then failing on the same task is
  logged again.
- **P3, the record parser.** `TAMPER_DETAIL` required `seat H:` while the
  write-time detail starts `the commits ledger refused the write: commit
  ledger H:`, so it fell back to a 200-character excerpt that cut the
  path, and the row hid it with a synthetic prefix. The regex reads both
  forms (`(?:seat|commit ledger) (\S+):`), and the row uses Go's own
  write-time text.
- **P2, the rows** named above, in the list below; B4's reset on the same
  seat replaces the seat switch.
- **P3, the note:** the older-reader paragraph in the residuals, and the
  compatibility row: a 3.1.2-shaped reader (decode without `Recovered` and
  the carried entries' `Note`, re-encode, verify) refuses a recovered
  ledger with the tamper reason (`the tag of commits.<h> link <N> fails`),
  this binary's reader verifies it; the expected behaviour until 0e lands.
- **The "waiting on you" gap, stated sharply (Codex, the reviewer's
  changed ruling):** the screen derives that section only from open gates,
  so it can say "Nothing is waiting on you" while a lead-routed
  `ledger_tampered` block exists; the tasks block and the seat stop line
  carry the block. A screen that says nothing waits while a block is open
  is false, and no note excuses it: the fix goes in this PR as a stated
  widening to `pi/extensions/lib/screen.ts` and its test, in a second
  phase after t102 (the seat's screen task, which also changes
  `screen.ts`) merges on v3 (the plan in "Gates").

## The plan reviewer on c9826c31 (`ccec9400`)

**The covers check.** The reviewer's rule: a check no row can fail is
unreachable or untested; find the input where it decides, or remove it.
It decides, and the input is reachable. `covers` is asked only when the
seat's ledger verifies and carries a recovery record (`RecoveryReader`
answers ok); it then decides whether that record is the hold's: the hold's
slot path, or a seq at or past the hold's. The record that is not the
hold's: an earlier recovery of the same seat (seq 3, its slot) while the
hold is from a later alarm (seq 150). The ledger verifies with that older
record when the later alarm was transient, the reachable input: the
session seed unreadable for one read gives `ledger_tampered: the session
seed: ...`, the document is quarantined at the head's seq and the seat is
held; the seed readable again, the document verifies with whatever record
it had (an older one, or none). Before `ccec9400` that seat was a dead end:
the reconciliation did not release it (right: no decision), and the lead's
recovery refused `recover_not_tampered` (the document verifies), so no
decision could ever be recorded. Now `RecoverLedger` on a document that
verifies, naming the slot (which must exist and be this seat's, at a seq
the live chain is at or past), records the lead's decision as the next
link of the same body: `LedgerRecovery` with `Verified: true`, the slot,
the digests, who and why; nothing restarted, nothing carried (a carry on a
verifying ledger is refused: its entries stand); the apply step's line says
"recorded the release of"; the reconciliation then reads a record that
covers the hold and releases the seat. The chain-continuation mode is
unchanged; both modes write through `sealWrite` (the recovery's own sealed
write, never `writeLedger`). Rows: the older record does not release the
hold and the hold's own does (the scheduler, with its one-check revert,
`covers` made always true, which fails on the older record releasing); the
seed unreadable for one read, the document verifying again, `RecoveryOf`
empty, the carry refused, the verified decision and its record, the body
otherwise unchanged (seatcommit); the restored document end to end through
the apply step and the tick (schedproc). A hold whose ledger verifies with
no record at all never reaches `covers` (ok is false) and is released the
same way, by the verified decision.

**The seatlaunch stop.** The `make ci` on `88df5bad` that stopped: the
test is `internal/seatlaunch` `TestPiSeatPlansThroughTheUpstreamAdapter`,
the failing line `amqpi_test.go:75: outcome unsupported
(launcher_not_available)`. It is the same row the 3a agent's run listed.
It asks the local AMQ binary's pi adapter to plan a seat, outside this
task's files; that run was beside a second `go test` of five packages in
the same tree and my edits to the helper move, so the tree was not quiet;
the runs on `8017ffea`, `a692ebca` and `ccec9400` passed it on a quiet
tree. It did not fail because files changed mid-run (no compile error; the
adapter answered unavailable), so it is the #905 kind of flake, under
load (AMQ's tmux detection has a 5 s deadline). `docs/v3-followups.md`
already names it, as a 3.0.x flake (the test-flakes line and the flakes
to watch); this note puts it on the 3.2.x list by its name ("Residual
risk" below), and the gates on `c27ad8fc` passed it on a quiet tree.

## Codex's re-pass on c9826c31 (`23f315e3`, `01088a5c`)

- **P2, any store read error read as "gone".** The release skipped a task
  on any error of `Store.Get` and then deleted the hold, so a transient
  store error left the task blocked with nothing to reconcile. Only
  `task.ErrNotFound` skips a task now; any other read error, or a reset
  that fails, keeps the hold for the next tick (a task already returned is
  skipped then), and, the plan reviewer's addition, the hold is retried
  every tick with its line under the same per-task, per-seat suppression
  with a reason of its own: `ledger_release_failed` once,
  `ledger_release_suppressed` once, then counted, so a persistent store
  error never floods the log and never loses the hold. The prepare
  failures and the release retries share `logOnce` (its two decisions are
  literals, so the words table's row-7 scan reads every kind; `23f315e3`
  then `01088a5c`). Row: a persistent read error over 30 ticks, one line
  and one count line, the hold kept and the task blocked, the release when
  the error clears, and a task the store no longer has skipped with the
  hold released. The error is logged, not returned from the tick: a
  returned tick error ends the rest of that tick (the relaunch ask, the
  grants) and the process logs it on every tick with no suppression
  (`internal/schedproc/process.go`, the tick loop), so a persistent store
  error would flood the log, the thing the reviewer's addition rules out;
  `applyRecoveries` now always answers nil.
- **P2, no row made a reset fail.** The delete moved before the resets
  would have passed every row. Row: the reset fails once (a seam), the
  hold stays and the task stays blocked, the retry on the next tick
  completes the release; its revert is exactly that move.
- **P2, the timeout row did not prove the child died.** Killing git alone
  would have passed: the wait delay closes the inherited pipes within the
  row's ten seconds. The ssh stand-in writes its own pid and the row
  asserts it is dead after the bound (signal 0, polled up to five
  seconds); its revert kills git alone, and the stand-in lives on.
- **Found on the way (`c27ad8fc`).** `recordVerified` refused a live
  ledger with no seal or one behind the slot's seq in one condition whose
  message read `live.Seal.Seq`, so a nil seal would have panicked under
  the ledger lock. A verifying ledger is always sealed (the seal check
  refuses a stripped seal), so no row reaches it; the refusal now reads the
  link number through a guard and refuses the same way.
- Confirmed closed by Codex's inspection, no change: the shared hardening
  with the launch's arguments and credentials unchanged (its runner also
  gaining the process-group handling and the wait delay), the bounded
  transport with the lock released on its path, the ledger-backed
  reconciliation with `AlreadyRecovered`, the restart, two-seat and
  interrupted-append rows, the suppression per task and seat with a
  changed reason logging again, the parser on both forms, the
  compatibility row and the note's statements.

## Codex's re-pass on b250b951 (`9de43a6c`, `8eeeaf24`)

Codex re-read `b250b951` (its sandbox could not run `go build`, so the
gates below are the run evidence) and found three P2 and one P3.

- **P2-1, an old recovery releases a new hold.** `covers` took any record
  at or past the hold's seq, and that seq is read from the quarantined
  head, which is never verified. The ledger records a recovery at seq 3;
  the head's seq is changed to 1, the prepare's alarm holds the seat at
  seq 1; the head is put back, the ledger verifies again, and the seq-3
  record (3 >= 1) released the new hold with no new decision. Now the
  hold carries a baseline: the record the refused body already had, which
  the quarantine decodes from the refused bytes it copies
  (`QuarantinedError.Prior`, `priorRecovery`), the prepare's alarm passes
  on (`LedgerTamperedError.Baseline`, schedproc's `prepareError`) and the
  hold keeps in the state file (`LedgerTamper.Baseline`, restored with the
  hold). `covers` refuses a record equal to the baseline (its seq, slot,
  link and time: `RecoveryMark`, which `RecoveryReader` now answers in
  place of the seq and slot) before the slot and seq checks. The
  baseline is decoded without verification on purpose: the refused body
  is the only copy of what the ledger held at the alarm (the live one is
  the refused one), and a baseline can only stop a release, never cause
  one. A slot match was already safe (the alarm creates the slot, so a
  record naming it was decided after the alarm); the seq path is the one
  the baseline closes. Not chosen: "decided after the alarm's time", which
  needs no baseline but leaves a hold unreleasable for good when the wall
  clock steps back between the alarm and the decision (a retried decision
  answers `AlreadyRecovered` and writes nothing new). The residual: a
  refused body that does not decode gives no baseline, and the seq path
  is then as before; such a body has no readable record to replay either.
  (Closed by the plan reviewer's ruling, the next section: the seq path
  is gone, and a record that predates the alarm cannot name a slot the
  alarm created, so a refused body with no baseline, undecodable or
  stripped of its record, replays nothing; the one exception, a slot the
  quarantine reuses for an identical refusal, is in that section.)
- **P2-2, temp names defeat the suppression.** `logOnce` compared the
  whole error text, so a persistent disk-full during `CleanSeat` that
  names a fresh `amq-squad-shadow-<random>` (or an atomic write's temp)
  each tick logged a new first failure every tick. The suppression key is
  now the decision's kind, task and seat plus the reason's identity
  (`reasonIdentity`): the text with every path masked to `<path>` and
  every run of digits to `#`; the logged record keeps the full text.
  #909's shape (a code plus the stage, the detail logged) is the model;
  a prepare failure has no code of its own (any error of the ledger read
  or the clean), and a constant code would make a failure that says
  something else silent, which the changed-reason rows of #904 (B4's end,
  the P2 per task) require to log again. The residual: two failures that
  differ only in a path or a number are one failure, counted.
- **P2-3, the recovery could clear an unrelated block.** The release read
  the task (blocked `ledger_tampered`) and then called the unconditional
  `Reset` under a second lock acquisition, so a decision in between (the
  lead's reset, then a new block of another reason) was cleared. The reset
  is now `task.Store.ResetIfBlocked(id, ledger_tampered)`: the state and
  the reason are checked inside the one store mutation that resets, and
  anything else is `task.ErrBlockChanged` with nothing moved, which the
  release counts as "reset by the lead already" (the task skipped, no
  `task_reset` line, the hold released once no task is kept).
- **P3, a retried verified recovery reported the wrong link.** The
  `AlreadyRecovered` answer rebuilt the new link as the record's seq plus
  one and dropped `Verified`: a slot at seq 4 decided on a live chain at
  link 7 wrote link 8, and a retry said 5, not verified. The record now
  keeps `Link`, the seq of the link that sealed it (the quarantined seq
  plus one for a chain continuation, the live link plus one for a
  verified decision), and the retry reports it and the record's
  `Verified`; a record written before the field falls back to seq plus
  one, which is right for the chain continuation, the only mode before
  `ccec9400`.

## The plan reviewer on af1a6124 (`ad3330e0`)

The reviewer kept P2-2 and P3 as built and tightened P2-1 and P2-3.

- **A hold is answered by its exact slot only.** Slot names are unique
  by construction (`commits.<h>.seq<N>[.k]`); a seq is not, and the seq a
  hold is named by comes from a head that was never verified (the
  incident was itself a lowered seq). `covers` now accepts a record only
  when it names the hold's own slot and is not the baseline; the seq
  path is gone. A lowered-seq replay of an older record is refused even
  when the refused body carried no baseline, and a recovery for another
  slot of the same seq (`.2`) is refused.
- **A hold with no slot is released only by the lead's verb.** A
  refusal that quarantined nothing gives a hold with no slot, which no
  record covers. The recover verb's apply step (`recoverDecision`, now
  given the scheduler) reads the record it has just sealed
  (`seatcommit.RecoveryOf`) and tells the scheduler
  (`Scheduler.LeadRecovered`, under the scheduler's lock, as the relaunch
  decision is); the next tick puts the decision on the hold
  (`LedgerTamper.Decided`, kept in the state file) and releases the hold
  once the ledger answers that same record (and it is not the baseline).
  A message lost in a crash loses nothing: the verb sent again is
  answered from the ledger (`AlreadyRecovered`) and tells the scheduler
  again. A hold with a slot ignores the message (its slot answers it).
  The lead needs a slot to name: a later refused read of the document
  quarantines it (0c-A), and the lead recovers from that slot.
- **The release resets only the hold's own block.** `ResetIfBlocked`
  takes a match that runs inside the one store mutation; the scheduler's
  (`LedgerTamper.names`) accepts only a `ledger_tampered` block whose
  detail is the alarm's own (`tamperDetail`) naming the hold's slot
  (`seat <h>: quarantined at <slot> (seq `), or, for a hold with no slot,
  exactly the hold's detail. The release's read uses the same check, so a
  tamper block raised meanwhile for another slot, or written by another
  writer (the commit step's refusal words it differently), is left as it
  is and the hold is released. One row's hand-made block named a slot
  (`/q`) the hold never had; it now uses the hold's own detail.
- **The baseline can only withhold a release.** It is decoded from the
  refused bytes without verification (the reviewer accepted this): a
  forged or stale baseline can make `covers` refuse a record, never
  accept one, since `covers` checks the baseline only to say no, and the
  release still needs the exact slot (or, with no slot, the verb's sealed
  decision). The worst a wrong baseline does is keep a seat held until
  the lead decides again.
- **The masked suppression key (P2-2), accepted.** The reviewer accepted
  the reason with its paths and digit runs masked, in place of a fixed
  code, with a row stating the trade-off (`e1fc443c`): two failures whose
  texts differ only in a temp file's name log one line, and one that
  differs in words logs a second. The trade-off, as built: a fixed code
  per kind would also merge failures that say different things (a disk
  full, then a permission refusal), and the lead would see the first one
  only; the masked text keeps those apart at the cost of merging two
  failures that differ only in a path or a number, which are then
  counted, not shown (the full text of the first stays in its line).
- **Where the baseline still decides.** Under slot-only matching the
  baseline refuses a record that names the hold's own slot and was
  already in the ledger at the alarm. On real files I found no path to
  that input: a slot holds the bytes its refusal copied, and the record
  naming the slot is written after the slot exists, in a later body. The
  one way a slot predates its hold is the quarantine's no-op for an
  identical refusal (the same bytes refused again reuse the slot): the
  document recovered from slot X, then the exact refused bytes put back
  (a new hold on X), then the recovered document put back again (it
  verifies and records X). The baseline does not stop that: the refused
  bytes predate the record of X. It needs two deliberate restores of
  whole files by a same-uid process, outside the threat model this note
  states ("Residual risk"); it is listed there for the reviewer. The
  scheduler's baseline row builds its input by hand, and the check stays
  as ruled.
- **A recovery from another slot leaves the hold.** When the document
  changed again after the alarm (a later refused read quarantined it in
  another slot), the recovery must name the later slot (the decision is
  refused for a slot whose bytes are not the live document's), and the
  hold on the first slot is not answered by it. It is not a dead end:
  that recovery makes the document verify, and the verb sent for the
  first slot then takes the verified mode (`recordVerified`: the slot
  exists, the live chain is at or past its seq), whose record names the
  first slot and releases the hold (`bd683f28`'s row drives the pair on
  real files). Listed in "Residual risk".

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
| P1 | A `url.<evil>.insteadOf` of the origin and a `core.sshCommand` with a marker in the seat's config, the task branch at an unpushed commit in `evil`: the seat's own `ls-remote` answers the unpushed commit and an ssh URL runs the planted command (the marker appears; removed); the recovery's check refuses (`recover_entry_unpushed`, "is not on origin's <branch>"), nothing written, the marker absent; the real entry still carries, the marker still absent. | `internal/seatcommit` `TestCarryCheckIgnoresTheSeatsGitConfig` |
| P2 bound | `RecoverTransportTimeout` 500 ms, the push URL an ssh host with `GIT_SSH_COMMAND=sh -c 'sleep 60' --`: the carry is refused `recover_entry_unpushed` naming "(bound 500ms)" and "did not answer within its bound; killed" well inside 10 s, nothing written, and the next recovery (no carry) takes the ledger lock at once and completes at seq N+1. | `internal/seatcommit` `TestStalledOriginEndsTheRecoveryWithinTheBound` |
| P3 compat | A recovered ledger with a carried entry: this binary's decode verifies; the same bytes decoded as a 3.1.2 reader does (no `Recovered`, no `Note`) do not, and `sealchain.Check` on that decode is `ledger_tampered: the tag of commits.<h> link <N> fails`. | `internal/seatcommit` `TestOlderReaderRefusesARecoveredLedgerAsTampered` |
| P2 retry | A recovery decided again for the same slot is `AlreadyRecovered` with the first result's seqs, slot and body digest, the ledger byte-identical; another slot on the recovered ledger is `recover_not_tampered`. | `internal/seatcommit` `TestRecoveryDecidedAgainIsAnsweredFromTheLedger` |
| P2 completion | The decision log's path taken by a directory: the apply step replies `recover_unrecorded`, the chain is written; the next ticks release the seat from the ledger (`ledger_hold_released`, `task_reset`, `dispatched`); the log writable again, the decision sent again is OK as already recovered (`AlreadyRecovered`, seqs 1 and 2), the ledger unchanged, one "had recovered" line. | `internal/schedproc` `TestInterruptedRecoveryCompletesFromTheLedger` |
| P2 restart | The hold in the state file, the chain written, the process gone before any state write: the next incarnation restores the hold and its first tick releases it from the ledger (`ledger_hold_released`, `task_reset`, `dispatched`); the state file no longer holds the seat. An older recovery record (another slot, a lower seq) releases nothing. | `internal/scheduler` `TestRestartedHoldIsReleasedFromTheLedgersRecovery`, `TestRecoveryReleasesTheSeatAndReturnsTheTask` |
| P2 one seat | Two seats held; one recovered: released alone and granted; the other stays in the state file, is restored by the next incarnation and never prepared (one prepare across the restart). | `internal/scheduler` `TestTwoHeldSeatsOneRecovered` |
| P2 per task | Two pending tasks on one failing seat over 200 ticks: one `seat_prepare_failed` and one `seat_prepare_suppressed` per task. B4's end: the same seat passes (dispatched, released by the lead) then fails on the same task: logged again, then counted. | `internal/scheduler` `TestTwoTasksOnOneFailingSeatAreEachLoggedOnce`, `TestPrepareFailureIsLoggedOnceThenCounted` |
| P3 parser | The write-time detail as Go writes it (no `seat H:` prefix) gives the record `T: seat H, quarantined at <path>`, on `tamperRecord` and through `blockWords`. | `pi/test/recovery.test.ts` (the second test) |
| covers | The seat's ledger verifies with an older recovery record (seq 3, its slot) while the hold is from seq 150: 20 ticks release nothing, the task stays blocked, the seat is prepared once; the record of the hold's own slot releases it and the task is dispatched. | `internal/scheduler` `TestAnOlderRecoveryRecordDoesNotReleaseAHold` |
| verified | The session seed made unreadable (mode 0) for one read: the refusal names the seed, the slot is at the head's seq; readable again, the document verifies and `RecoveryOf` answers nothing; a carry is refused `recover_entry_unknown`; the decision is `Verified` at seq N+1 with prev the live link's tag, the record names the slot, the seq and the body digest, the body's entries, base and rest are unchanged, `RecoveryOf` answers the slot, the quarantine copy is unchanged and the document moved on. | `internal/seatcommit` `TestADocumentVerifyingAgainIsReleasedByARecordedDecision` |
| restored | The held seat's document put back (the bytes it was edited from, verifying at the same seq): five ticks release nothing; the lead's decision on the slot is OK and `Verified` at seq 2; the next ticks release the seat (`ledger_hold_released`, `task_reset`, `dispatched`); the decision line says "recorded the release of". | `internal/schedproc` `TestARestoredDocumentIsReleasedByTheLeadsDecision` |
| read error | A hold whose seat's ledger records the recovery, the store's read failing (a seam) for 30 ticks: one `ledger_release_failed`, one `ledger_release_suppressed`, no release, the hold kept and the task blocked; the error cleared, the next tick releases (`ledger_hold_released`, `task_reset`, `dispatched`); a hold naming a task the store no longer has is released with no failure line. | `internal/scheduler` `TestAStoreReadErrorKeepsTheHoldAndLogsOnce` |
| reset fails | The reset fails once (a seam): `ledger_release_failed`, no release, the hold kept and the task blocked; the next tick's retry resets, releases and dispatches. | `internal/scheduler` `TestAFailedResetKeepsTheHoldForTheRetry` |
| child dead | The timeout row's stand-in writes its pid; after the bound's refusal the pid is dead (signal 0, polled up to 5 s), beside the refusal's text, the elapsed time and the lock taken by the next recovery. | `internal/seatcommit` `TestStalledOriginEndsTheRecoveryWithinTheBound` (unix: `processAlive` in `recover_unix_test.go`) |
| P2-1 baseline | A hold on slot seq1 whose alarm carries, as its baseline, a record naming that same slot (link 4; an input built by hand, "The plan reviewer on af1a6124"); the reader answers that same record: 20 ticks release nothing; the state file keeps the baseline; a restarted scheduler restored from it releases nothing over 5 ticks and the task stays blocked; a new record (the lead's decision after the alarm) releases the hold and resets the task. | `internal/scheduler` `TestTheRecoveryPresentAtTheAlarmDoesNotReleaseIt` |
| P2-1 the alarm's baseline | Real files: the lead's verified decision recorded (link 2); the head's seq changed to 1; `CleanSeats.PrepareSeat` refuses with `LedgerTamperedError` at seq 1 whose `Baseline` is the ledger's record; the head put back, `LedgerRecovery` answers a mark equal to the baseline. | `internal/schedproc` `TestThePrepareAlarmCarriesTheLedgersRecordAsTheBaseline` |
| P2-2 the masked key | Three ticks: a failure naming `amq-squad-shadow-4127`, the same naming `amq-squad-shadow-90355`, then one that says `permission denied`: two `seat_prepare_failed` lines; `reasonIdentity` equates the first pair and keeps the words apart. | `internal/scheduler` `TestReasonsDifferingOnlyInATempNameLogOnce` |
| P2-2 temp names | A prepare failure naming a fresh `amq-squad-shadow-<n>` and byte count each tick for 30 ticks: one `seat_prepare_failed` (with the full text) and one `seat_prepare_suppressed`; a failure that says something else (`permission denied`) is logged again. | `internal/scheduler` `TestAFailureNamingAFreshTempFileIsLoggedOnce` |
| P2-3 the conditional reset | Between the release's read and its reset the task is reset and blocked again (`lead_hold`): the new block stays with its reason, no `task_reset`, no dispatch, no failure line, the hold released. The store: `ResetIfBlocked` refuses another reason, a pending task (`ErrBlockChanged`) and an unknown id (`ErrNotFound`), and resets its own reason with the block's fields cleared. | `internal/scheduler` `TestARecoveryNeverClearsABlockRaisedAfterItsRead`; `internal/task` `TestResetIfBlockedResetsOnlyItsReason` |
| P3 the retry's link | The transient alarm at seq 4, a second commit step (live link 7), the verified decision writes link 8; its retry is `AlreadyRecovered`, `Verified`, link 8; the record's `Link` is 8; then the head's seq changed to 1: the quarantine's `Prior` is that record (seq, slot, link, time), and the head put back, `RecoveryOf` answers it. | `internal/seatcommit` `TestAVerifiedRecoveryRetriedReportsItsLink` |
| replay | The lowered-seq replay with no baseline: a hold on slot seq1; the ledger answers an older record (seq 3, its slot): 20 ticks and 5 more after a restart release nothing; the record of slot seq1 releases the hold and resets the task. | `internal/scheduler` `TestALoweredSeqReplayDoesNotReleaseTheHold` |
| another slot | A hold on slot seq150; the ledger records a recovery from slot seq150.2 (the same seq): 10 ticks release nothing, the hold stands, the task stays blocked. | `internal/scheduler` `TestARecoveryForAnotherSlotDoesNotReleaseTheHold` |
| no slot | A hold with no slot (seq 150); a record at seq 150 for 10 ticks releases nothing; the verb's message for another seat, and one with a link the ledger does not record, release nothing; the message naming the record the ledger answers releases the hold, resets the task and dispatches it. | `internal/scheduler` `TestAHoldWithNoSlotIsReleasedOnlyByTheLeadsVerb` |
| no slot, the verb | Real files: a hold with no slot restored from a state file, its task blocked with the alarm's detail; a refused read made slot seq1; the document put back: 5 ticks release nothing; the lead's verb on slot 1 (OK), then the ticks release the hold, reset the task and dispatch it. | `internal/schedproc` `TestTheLeadsVerbReleasesAHoldWithNoSlot` |
| another slot's block | Between the release's read and its reset the task is blocked `ledger_tampered` again for slot seq151 (the alarm's words): that block stays with its detail, no `task_reset`, no dispatch, no failure line, the hold released. The store: a block the caller's match refuses is `ErrBlockChanged` and nothing moves. | `internal/scheduler` `TestARecoveryKeepsATamperBlockOfAnotherSlot`; `internal/task` `TestResetIfBlockedResetsOnlyItsReason` |
| earlier slot | Slot X refused at seq 1, then X.2 after another edit: the decision for X is refused while the live document is X.2's; the recovery from X.2 continues the chain; the decision for X is then `Verified`, names X, at the link after X.2's; `RecoveryOf` answers X with that link. | `internal/seatcommit` `TestAHoldOnAnEarlierSlotIsAnsweredAfterALaterRecovery` |
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

Each run on the committed tree `7ca9da50` (the first list) or `88df5bad`
(the second, Codex's rows): the line flipped, the row run alone, the first
failing line verbatim (the temp-dir prefix as printed), then `git checkout
-- <file>` and the row re-run green. The runner is in the job scratch, not
committed. A flip is a compiling inverse of the line's rule (the one-seat
row's first flip, `covers` made always true, did not fail, since the
unrecovered seat's ledger answers no record at all and `covers` is never
asked; the recorded flip is the whole guard). In the B3c, B6, B2 and retry
lines the fixture's 40- and 64-character hex values (a link tag, digests,
commit ids of the temp repository) are written as `<40 hex>` and `<64
hex>`, so this note holds no hex run.

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


## Revert runs, Codex's rows (`88df5bad`; the P1 and bound rows re-run on `8017ffea` in the third list after the helper move)

### P1 the carry check in the harness's git

```
go test -count=1 -run ^TestCarryCheckIgnoresTheSeatsGitConfig$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:258`
```
	carried, err := carryEntries(o, slot, oldSeq, sh.g)
```
to:
```
	carried, err := carryEntries(o, slot, oldSeq, repo)
```
First failing line (exit 1):
```
recover_test.go:355: not a recovery refusal: <nil>
```
`--- FAIL: TestCarryCheckIgnoresTheSeatsGitConfig (0.60s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.912s`.

### P2 the bound

```
go test -count=1 -run ^TestStalledOriginEndsTheRecoveryWithinTheBound$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:460`
```
	ctx, cancel := context.WithTimeout(context.Background(), RecoverTransportTimeout)
```
to:
```
	ctx, cancel := context.WithCancel(context.Background())
```
First failing line (exit 1):
```
recover_test.go:393: the stalled origin: recover_entry_unpushed: origin's seat/s1-t1 cannot be read: git --heads: exit status 128: fatal: Could not read from remote repository.
```
`--- FAIL: TestStalledOriginEndsTheRecoveryWithinTheBound (120.71s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	2.406s`.

### P2 the reconciliation, the scheduler

```
go test -count=1 -run ^TestRestartedHoldIsReleasedFromTheLedgersRecovery$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:170`
```
	reader, ok := s.Preparer.(RecoveryReader)
	if !ok || len(s.tampered) == 0 {
```
to:
```
	reader, ok := s.Preparer.(RecoveryReader)
	if !ok || len(s.tampered) == 0 || true {
```
First failing line (exit 1):
```
tamper_test.go:356: the restarted scheduler did not release the hold from the ledger: [{Kind:seat_up TaskID: Seat:seat-a Pool:claude:sonnet:medium Reason: Stage: ReasonCode:}]
```
`--- FAIL: TestRestartedHoldIsReleasedFromTheLedgersRecovery (0.13s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.755s`.

### P2 the reconciliation, the process

```
go test -count=1 -run ^TestInterruptedRecoveryCompletesFromTheLedger$ ./internal/schedproc/
```
Reverted `internal/schedproc/prepare.go:83`
```
	r, ok := seatcommit.RecoveryOf(c.Root, handle)
	return r.Seq, r.Slot, ok
```
to:
```
	r, ok := seatcommit.RecoveryOf(c.Root, handle)
	return r.Seq, r.Slot, ok && false
```
First failing line (exit 1):
```
recover_test.go:333: the hold was not released from the ledger: []
```
`--- FAIL: TestInterruptedRecoveryCompletesFromTheLedger (0.90s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	1.331s`.

### P2 the retry

```
go test -count=1 -run ^TestRecoveryDecidedAgainIsAnsweredFromTheLedger$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:185`
```
	case rerr == nil && live.Recovered != nil && live.Recovered.Slot == slotPath:
```
to:
```
	case rerr == nil && live.Recovered != nil && live.Recovered.Slot == slotPath && false:
```
First failing line (exit 1):
```
recover_test.go:427: the retry: recover_not_tampered: the commits ledger of seat s1 verifies; there is nothing to recover {Handle: AlreadyRecovered:false Slot: OldSeq:0 NewSeq:0 Prev: BodySHA256: HeadSHA256: Base: Carried:[]}
```
`--- FAIL: TestRecoveryDecidedAgainIsAnsweredFromTheLedger (0.97s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.885s`.

### P2 one recovery, one seat

```
go test -count=1 -run ^TestTwoHeldSeatsOneRecovered$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:182`
```
		if !ok || !tm.covers(seq, slot) {
			continue
		}
```
to:
```
		if false && ok {
			continue
		}
```
First failing line (exit 1):
```
tamper_test.go:379: both seats were not held once each: map[seat-a:3 seat-b:3] blocked blocked
```
`--- FAIL: TestTwoHeldSeatsOneRecovered (0.25s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.408s`.

### P2 suppression per task

```
go test -count=1 -run ^TestTwoTasksOnOneFailingSeatAreEachLoggedOnce$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:85`
```
func prepareKey(handle, taskID string) string { return handle + "\x00" + taskID }
```
to:
```
func prepareKey(handle, taskID string) string { return handle + "\x00" }
```
First failing line (exit 1):
```
tamper_test.go:321: task t1: 1 failed lines and 0 count lines over 200 ticks, want 1 and 1
```
`--- FAIL: TestTwoTasksOnOneFailingSeatAreEachLoggedOnce (0.12s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.348s`.

### P3 the parser

```
cd pi && node --test --test-reporter=spec test/recovery.test.ts
```
Reverted `pi/extensions/lib/words.ts:344`
```
const TAMPER_DETAIL = /(?:seat|commit ledger) (\S+):.*?quarantined at ([^\s;]+)/;
```
to:
```
const TAMPER_DETAIL = /seat (\S+):.*?quarantined at ([^\s;]+)/;
```
First failing line (exit 1):
```
✖ B9: the typed block reads with the same words, the seat and the path on its record line (0.653291ms)
```
`✖ B9: the typed block reads with the same words, the seat and the path on its record line (0.653291ms); ℹ fail 1`
restored and re-run (exit 0): `ℹ pass 2; ℹ fail 0`.

## Revert runs, the reviewer's rulings (`8017ffea`)

The bound's flip (no deadline) fails on the refusal's text: the ssh stand-in slept its 60 s and git then reported the unreadable remote, so the refusal came, late and without the bound's words.

### P1 the carry check in the harness's shadow

```
go test -count=1 -run ^TestCarryCheckIgnoresTheSeatsGitConfig$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:406`
```
	fenv := append(append([]string(nil), sh.Env...), credEnv...)
```
to:
```
	fenv := append([]string{"GIT_DIR=" + common}, credEnv...) // the seat's git directory and its config
```
First failing line (exit 1):
```
recover_test.go:370: not a recovery refusal: <nil>
```
`--- FAIL: TestCarryCheckIgnoresTheSeatsGitConfig (6.36s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	7.112s`.

### P2 the bound

```
go test -count=1 -run ^TestStalledOriginEndsTheRecoveryWithinTheBound$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:425`
```
			ctx, cancel := context.WithTimeout(context.Background(), RecoverTransportTimeout)
```
to:
```
			ctx, cancel := context.WithCancel(context.Background())
```
First failing line (exit 1):
```
recover_test.go:415: the stalled origin: recover_entry_unpushed: origin's seat/s1-t1 cannot be read (bound 500ms): git ls-remote: exit status 128: fatal: Could not read from remote repository.
```
`--- FAIL: TestStalledOriginEndsTheRecoveryWithinTheBound (123.74s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	2.044s`.

### P3 the compatibility row

```
go test -count=1 -run ^TestOlderReaderRefusesARecoveredLedgerAsTampered$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/ledger.go:77`
```
	Recovered *LedgerRecovery `json:"recovered,omitempty"`
```
to:
```
	Recovered *LedgerRecovery `json:"-"`
```
First failing line (exit 1):
```
recover_test.go:462: this binary's reader: <nil> <nil>
```
`--- FAIL: TestOlderReaderRefusesARecoveredLedgerAsTampered (1.26s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.887s`.

## Revert runs, the reviewer's items on c9826c31 (`ccec9400`; re-run on `c27ad8fc`)

### covers decides

```
go test -count=1 -run ^TestAnOlderRecoveryRecordDoesNotReleaseAHold$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:247`
```
	if tm.Quarantine != "" && slot == tm.Quarantine {
		return true
	}
```
to:
```
	if tm.Quarantine != "" && slot == tm.Quarantine || true {
		return true
	}
```
First failing line (exit 1):
```
tamper_test.go:450: an older record released the hold: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} {Kind:ledger_hold_released TaskID: Seat:seat-a Pool: Reason:the commits ledger of seat seat-a records the lead's recovery from quarantine seq 3 (/root/extensions/amq-squad/quarantine/commits.seat-a.seq3); the seat is grantable again Stage: ReasonCode:} {Kind:seat_prepare_failed TaskID:t1 Seat:seat-a Pool: Reason:seat seat-a: quarantined at /root/extensions/amq-squad/quarantine/commits.seat-a.seq150 (seq 150): the commit ledger of seat seat-a: commit ledger seat-a: ledger_tampered: the tag of commits.seat-a link 150 fails; the refused document is quarantined at /root/extensions/amq-squad/quarantine/commits.seat-a.seq150 Stage: ReasonCode:ledger_tampered} {Kind:blocked TaskID:t1 Seat:seat-a Pool: Reason:ledger_tampered Stage: ReasonCode:}]
```
`--- FAIL: TestAnOlderRecoveryRecordDoesNotReleaseAHold (0.08s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.350s`.

### the verified decision

```
go test -count=1 -run ^TestADocumentVerifyingAgainIsReleasedByARecordedDecision$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:208`
```
		return recordVerified(o, live, slotPath)
```
to:
```
		return out, refuseRecover(RecoverNotTampered, "the commits ledger of seat %s verifies; there is nothing to recover", o.Handle)
```
First failing line (exit 1):
```
recover_test.go:580: a carry on a verifying ledger: recover_not_tampered: the commits ledger of seat s1 verifies; there is nothing to recover
```
`--- FAIL: TestADocumentVerifyingAgainIsReleasedByARecordedDecision (0.48s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.714s`.

## Revert runs, Codex's re-pass on c9826c31 (`01088a5c`; re-run on `c27ad8fc`)

Each of the five below was re-run on `c27ad8fc` (the line numbers are
that tree's), one at a time on a quiet tree: the expression reverted by a
script that asserts it occurs once, the row run, the file restored from
its copy and the row run again.

### only not-found skips a task

```
go test -count=1 -run ^TestAStoreReadErrorKeepsTheHoldAndLogsOnce$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:208`
```
			if errors.Is(err, task.ErrNotFound) {
				continue // gone
			}
```
to:
```
			if err != nil {
				continue // gone
			}
```
First failing line (exit 1):
```
tamper_test.go:489: over 30 ticks of a read error: [{Kind:ledger_hold_released TaskID: Seat:seat-a Pool: Reason:the commits ledger of seat seat-a records the lead's recovery from quarantine seq 150 (/root/extensions/amq-squad/quarantine/commits.seat-a.seq150); the seat is grantable again Stage: ReasonCode:}]
```
`--- FAIL: TestAStoreReadErrorKeepsTheHoldAndLogsOnce (0.06s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.343s`.

### the resets before the delete

```
go test -count=1 -run ^TestAFailedResetKeepsTheHoldForTheRetry$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:205`
```
		kept := false
		for _, id := range tm.Tasks {
```
to:
```
		kept := false
		delete(s.tampered, h) // the hold deleted before the resets
		for _, id := range tm.Tasks {
```
First failing line (exit 1):
```
tamper_test.go:532: the failed reset dropped the hold or returned the task
```
`--- FAIL: TestAFailedResetKeepsTheHoldForTheRetry (0.05s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.405s`.

### the process group killed

```
go test -count=1 -run ^TestStalledOriginEndsTheRecoveryWithinTheBound$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/baseshadow.go:59`
```
	cmd.Cancel = func() error { killGroup(cmd); return nil }
```
to:
```
	cmd.Cancel = func() error { return cmd.Process.Kill() } // git alone
```
First failing line (exit 1):
```
recover_test.go:432: the transport's child 78789 is still alive after the bound
```
`--- FAIL: TestStalledOriginEndsTheRecoveryWithinTheBound (6.09s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	1.448s`.
The reverted run leaves the stand-in alive, as the row says: its `sleep
60` (reparented to pid 1) was killed by hand after the run.

## Revert runs, Codex's re-pass on b250b951 (`8eeeaf24`)

Each on `8eeeaf24`, one at a time, by the same script as above (the
expression asserted to occur once, the row run, the file restored from
its copy and the row run again). A temp root in a failing line is
written `<tmp>`, and a decision list is cut after its first entry.

### P2-1 the baseline check

```
go test -count=1 -run ^TestTheRecoveryPresentAtTheAlarmDoesNotReleaseIt$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:302`
```
	if tm.Baseline != nil && tm.Baseline.same(rec) {
```
to:
```
	if false && tm.Baseline != nil && tm.Baseline.same(rec) {
```
First failing line (exit 1):
```
tamper_test.go:567: the record present at the alarm released the hold: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestTheRecoveryPresentAtTheAlarmDoesNotReleaseIt (0.09s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	1.655s`.

### P2-1 the alarm's baseline

```
go test -count=1 -run ^TestThePrepareAlarmCarriesTheLedgersRecordAsTheBaseline$ ./internal/schedproc/
```
Reverted `internal/schedproc/prepare.go:77`
```
			e.Baseline = &m
```
to:
```
			_ = m
```
First failing line (exit 1):
```
recover_test.go:442: the alarm: the commit ledger of seat seat-t1: commit ledger seat-t1: ledger_tampered: the head of commits.seat-t1 does not decode or its tag fails; the refused document is quarantined at <tmp>/extensions/amq-squad/quarantine/commits.seat-t1.seq1.2 ...
```
`--- FAIL: TestThePrepareAlarmCarriesTheLedgersRecordAsTheBaseline (0.59s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	1.158s`.

### P2-1 the quarantine's prior record

```
go test -count=1 -run ^TestAVerifiedRecoveryRetriedReportsItsLink$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:112`
```
return &QuarantinedError{Handle: handle, Seq: seq, Slot: filepath.Join(dir, slot), Err: refusal, Prior: priorRecovery(raw.body)}
```
to:
```
return &QuarantinedError{Handle: handle, Seq: seq, Slot: filepath.Join(dir, slot), Err: refusal, Prior: nil}
```
First failing line (exit 1):
```
recover_test.go:673: the quarantine's prior record: seq 1 <nil>, want {Seq:4 Slot:<tmp>/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq4 ...}
```
`--- FAIL: TestAVerifiedRecoveryRetriedReportsItsLink (1.20s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	1.973s`.

### P2-2 the masked identity

```
go test -count=1 -run ^TestAFailureNamingAFreshTempFileIsLoggedOnce$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:120`
```
		t.UpdatedAt.UTC().Format(time.RFC3339Nano), reasonIdentity(err.Error()), log)
```
to:
```
		t.UpdatedAt.UTC().Format(time.RFC3339Nano), err.Error(), log)
```
First failing line (exit 1):
```
tamper_test.go:615: over 30 ticks of one failure with a fresh temp name: [{Kind:seat_up TaskID: Seat:seat-a Pool:claude:sonnet:medium Reason: Stage: ReasonCode:} ...]
```
(the list holds a `seat_prepare_failed` for every tick)
`--- FAIL: TestAFailureNamingAFreshTempFileIsLoggedOnce (0.03s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	1.682s`.

### P2-3 the conditional reset, the release

```
go test -count=1 -run ^TestARecoveryNeverClearsABlockRaisedAfterItsRead$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:205`
```
		return st.ResetIfBlocked(id, ReasonLedgerTampered, now)
```
to:
```
		return st.Reset(id, nil, now)
```
First failing line (exit 1):
```
tamper_test.go:659: the release touched the new block: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestARecoveryNeverClearsABlockRaisedAfterItsRead (0.17s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	1.502s`.

### P2-3 the conditional reset, the store

```
go test -count=1 -run ^TestResetIfBlockedResetsOnlyItsReason$ ./internal/task/
```
Reverted `internal/task/reset_if.go:20`
```
		if t.State != StateBlocked || t.BlockReason != reason {
```
to:
```
		if t.State != StateBlocked {
```
First failing line (exit 1):
```
reset_if_test.go:21: another block's reset: <nil>
```
`--- FAIL: TestResetIfBlockedResetsOnlyItsReason (0.06s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/task	1.038s`.

### P3 the retry's link

```
go test -count=1 -run ^TestAVerifiedRecoveryRetriedReportsItsLink$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:195`
```
		newSeq := live.Recovered.Link
```
to:
```
		newSeq := 0
```
First failing line (exit 1):
```
recover_test.go:655: the retry: <nil> {Handle:s1 AlreadyRecovered:true Verified:true Slot:<tmp>/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq4 OldSeq:4 NewSeq:5 ...}, want link 8 verified
```
`--- FAIL: TestAVerifiedRecoveryRetriedReportsItsLink (1.23s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	2.770s`.

### P3 the retry's mode

```
go test -count=1 -run ^TestAVerifiedRecoveryRetriedReportsItsLink$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:199`
```
AlreadyRecovered: true, Verified: live.Recovered.Verified, Slot: slotPath,
```
to:
```
AlreadyRecovered: true, Slot: slotPath,
```
First failing line (exit 1):
```
recover_test.go:655: the retry: <nil> {Handle:s1 AlreadyRecovered:true Verified:false Slot:<tmp>/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq4 OldSeq:4 NewSeq:8 ...}, want link 8 verified
```
`--- FAIL: TestAVerifiedRecoveryRetriedReportsItsLink (1.29s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	1.832s`.

### P3 the link written

```
go test -count=1 -run ^TestAVerifiedRecoveryRetriedReportsItsLink$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:326`
```
l.Recovered = &LedgerRecovery{..., Verified: true, Link: prev.Seq + 1}
```
to:
```
l.Recovered = &LedgerRecovery{..., Verified: true}
```
First failing line (exit 1):
```
recover_test.go:655: the retry: <nil> {Handle:s1 AlreadyRecovered:true Verified:true Slot:<tmp>/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq4 OldSeq:4 NewSeq:5 ...}, want link 8 verified
```
`--- FAIL: TestAVerifiedRecoveryRetriedReportsItsLink (1.18s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	1.630s`.

## Revert runs, the plan reviewer on af1a6124 (`ad3330e0`, `bd683f28`)

Each on `ad3330e0` (the last row's on `bd683f28`, which only adds that
row), one at a time, by the same script (the expression asserted to occur
once, the row run, the file restored from its copy and the row run
again). The seq matching put back is one revert run against the three
rows that the ruling says it must fail. A decision list in a failing line
is cut after its first entry.

### seq matching put back: the lowered-seq replay

```
go test -count=1 -run ^TestALoweredSeqReplayDoesNotReleaseTheHold$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:324`
```
	if tm.Quarantine != "" {
		return rec.Slot == tm.Quarantine
	}
	return tm.Decided != nil && tm.Decided.same(rec)
```
to:
```
	if tm.Quarantine != "" && rec.Slot == tm.Quarantine {
		return true
	}
	var n int
	_, err := fmt.Sscanf(tm.Seq, "%d", &n)
	return err == nil && rec.Seq >= n
```
First failing line (exit 1):
```
tamper_test.go:616: the replayed seq-3 record released the seq-1 hold: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestALoweredSeqReplayDoesNotReleaseTheHold (0.16s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.545s`.

### seq matching put back: another slot of the same seq

```
go test -count=1 -run ^TestARecoveryForAnotherSlotDoesNotReleaseTheHold$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:324`
```
(the same as above)
```
to:
```
(the same as above)
```
First failing line (exit 1):
```
tamper_test.go:653: a recovery for another slot released the hold: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestARecoveryForAnotherSlotDoesNotReleaseTheHold (0.12s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.277s`.

### seq matching put back: the hold with no slot

```
go test -count=1 -run ^TestAHoldWithNoSlotIsReleasedOnlyByTheLeadsVerb$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:324`
```
(the same as above)
```
to:
```
(the same as above)
```
First failing line (exit 1):
```
tamper_test.go:683: a record alone released a hold with no slot: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestAHoldWithNoSlotIsReleasedOnlyByTheLeadsVerb (0.25s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.451s`.

### the verb's decision matched against the ledger

```
go test -count=1 -run ^TestAHoldWithNoSlotIsReleasedOnlyByTheLeadsVerb$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:327`
```
	return tm.Decided != nil && tm.Decided.same(rec)
```
to:
```
	return tm.Decided != nil
```
First failing line (exit 1):
```
tamper_test.go:692: a decision the ledger does not record released the hold: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestAHoldWithNoSlotIsReleasedOnlyByTheLeadsVerb (0.17s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.375s`.

### the apply step tells the scheduler

```
go test -count=1 -run ^TestTheLeadsVerbReleasesAHoldWithNoSlot$ ./internal/schedproc/
```
Reverted `internal/schedproc/recover.go:156`
```
			s.LeadRecovered(req.Seat, recoveryMark(rec))
```
to:
```
			_ = rec
```
First failing line (exit 1):
```
recover_test.go:485: the lead's verb did not release the hold with no slot: []
```
`--- FAIL: TestTheLeadsVerbReleasesAHoldWithNoSlot (0.47s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.849s`.

### the reset's match in the store mutation

```
go test -count=1 -run ^TestARecoveryKeepsATamperBlockOfAnotherSlot$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:209`
```
		return st.ResetIfBlocked(id, ReasonLedgerTampered, match, now)
```
to:
```
		return st.ResetIfBlocked(id, ReasonLedgerTampered, nil, now)
```
First failing line (exit 1):
```
tamper_test.go:804: the release touched the other slot's block: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestARecoveryKeepsATamperBlockOfAnotherSlot (0.16s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.357s`.

### the hold's identity (names)

```
go test -count=1 -run ^TestARecoveryKeepsATamperBlockOfAnotherSlot$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:340`
```
		return strings.HasPrefix(t.BlockDetail, "seat "+tm.Handle+": quarantined at "+tm.Quarantine+" (seq ")
```
to:
```
		return true
```
First failing line (exit 1):
```
tamper_test.go:804: the release touched the other slot's block: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestARecoveryKeepsATamperBlockOfAnotherSlot (0.13s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.305s`.

### the store's match

```
go test -count=1 -run ^TestResetIfBlockedResetsOnlyItsReason$ ./internal/task/
```
Reverted `internal/task/reset_if.go:26`
```
		if match != nil && !match(*t) {
```
to:
```
		if false && match != nil && !match(*t) {
```
First failing line (exit 1):
```
reset_if_test.go:40: a block the match refuses: <nil>
```
`--- FAIL: TestResetIfBlockedResetsOnlyItsReason (0.16s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/task	1.021s`.

### the baseline on the hold's own slot

```
go test -count=1 -run ^TestTheRecoveryPresentAtTheAlarmDoesNotReleaseIt$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:321`
```
	if tm.Baseline != nil && tm.Baseline.same(rec) {
```
to:
```
	if false && tm.Baseline != nil && tm.Baseline.same(rec) {
```
First failing line (exit 1):
```
tamper_test.go:567: the record present at the alarm released the hold: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestTheRecoveryPresentAtTheAlarmDoesNotReleaseIt (0.10s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.328s`.

### the earlier slot answered by the verified mode (`bd683f28`)

```
go test -count=1 -run ^TestAHoldOnAnEarlierSlotIsAnsweredAfterALaterRecovery$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/recover.go:215`
```
		return recordVerified(o, live, slotPath)
```
to:
```
		return out, refuseRecover(RecoverNotTampered, "the commits ledger of seat %s verifies; there is nothing to recover", o.Handle)
```
First failing line (exit 1):
```
recover_test.go:709: the decision for X after X.2's recovery: recover_not_tampered: the commits ledger of seat s1 verifies; there is nothing to recover {Handle: AlreadyRecovered:false Verified:false Slot: ...}
```
`--- FAIL: TestAHoldOnAnEarlierSlotIsAnsweredAfterALaterRecovery (4.59s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	4.184s`.

### the masked key's row (`e1fc443c`)

```
go test -count=1 -run ^TestReasonsDifferingOnlyInATempNameLogOnce$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:124`
```
		t.UpdatedAt.UTC().Format(time.RFC3339Nano), reasonIdentity(err.Error()), log)
```
to:
```
		t.UpdatedAt.UTC().Format(time.RFC3339Nano), err.Error(), log)
```
First failing line (exit 1):
```
tamper_test.go:836: 3 seat_prepare_failed lines, want 2 (the temp name alone is one failure, the words another): [{Kind:seat_up TaskID: Seat:seat-a Pool:claude:sonnet:medium Reason: Stage: ReasonCode:} ...]
```
`--- FAIL: TestReasonsDifferingOnlyInATempNameLogOnce (0.02s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.621s`.

### The re-pass's reverts, run again on `ad3330e0`

The revert runs of "Codex's re-pass on b250b951" above, run again on
`ad3330e0` by the same script (the code they revert is unchanged or moved;
the reset's revert is now the seam's new form). Each exit 1 on its row,
restored exit 0:

| Revert | On `ad3330e0` | First failing line | Restored |
|---|---|---|---|
| the alarm's baseline dropped | `internal/schedproc/prepare.go:77` | `recover_test.go:442: the alarm: ...` | `ok` 0.687s |
| the quarantine's prior record nil | `internal/seatcommit/quarantine.go:112` | `recover_test.go:673: the quarantine's prior record: seq 1 <nil>, ...` | `ok` 1.321s |
| the raw text as the identity | `internal/scheduler/tamper.go:124` | `tamper_test.go:720: over 30 ticks of one failure with a fresh temp name: ...` | `ok` 0.388s |
| an unconditional `Reset` | `internal/scheduler/tamper.go:209` | `tamper_test.go:764: the release touched the new block: ...` | `ok` 0.448s |
| the store's reason not checked | `internal/task/reset_if.go:23` | `reset_if_test.go:22: another block's reset: <nil>` | `ok` 0.633s |
| the retry's link rebuilt | `internal/seatcommit/recover.go:195` | `recover_test.go:655: the retry: ... NewSeq:5 ...` | `ok` 1.266s |
| the retry's mode dropped | `internal/seatcommit/recover.go:199` | `recover_test.go:655: the retry: ... Verified:false ... NewSeq:8 ...` | `ok` 1.251s |
| the link not written | `internal/seatcommit/recover.go:326` | `recover_test.go:655: the retry: ... NewSeq:5 ...` | `ok` 1.460s |

## Gates

The PR lands in two phases (the plan reviewer's changed ruling on the
"waiting on you" gap): phase one is everything above, reported on the
head below; phase two, after t102's PR (the seat's screen task, which also
changes `screen.ts`) merges on v3, merges `origin/v3` into this branch by
hand (never a rebase), re-runs these gates, then adds one commit widening
the declared set to `pi/extensions/lib/screen.ts` and its test: the
"waiting on you" section derived from open gates plus lead-routed blocks,
the tamper block listed there with the rule-7 words, "Nothing is waiting
on you" only when both are empty; its row (a tamper block with no gates
shows under "waiting on you", never the "nothing" line) and revert run go
in this note, and the gates run again on that head.

Phase one, on `e1fc443c` (the masked key's row after `ede2226c`; this
note is the only change after it), each from the worktree root. The
whole-module test, `npm test` and `make ci` ran under the host's gate lock
(`<job tmp>/gate-lock.sh <label> -- <command>`, the plan reviewer's rule,
widened to `npm test`), one at a time, each printing the head
(`git rev-parse --short HEAD`: `e1fc443c`) inside the lock:

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'` (the whole module): exit 0, 56 packages `ok`, no
  `FAIL` (the lock taken after 640 s of waiting).
- `gate-lock.sh 904-pi -- sh -c 'cd pi && npm test'`: exit 0, `ℹ tests
  118`, `ℹ pass 117`, `ℹ fail 0`, `ℹ skipped 1`; `cd pi && node
  scripts/lint.mjs`: exit 0.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: exit 0, 70 `ok` (50 from the cache of
  the whole-module run), no `FAIL`, the same four pi counts.
- The format gate, Go 1.26's gofmt first on PATH, `make fmt-check`: exit
  0, nothing named.
- `GOOS=windows go build ./internal/seatcommit/`: exit 0;
  `go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`:
  exit 0 (on this note).

Every gate passed on its first run here, none repeated.

Before that, phase one on `bd683f28` (the plan reviewer's ruling on `af1a6124`:
`ad3330e0`, `bd683f28`; the note `ede2226c` the only change after it), each from
the worktree root. The whole-module test and `make ci` ran under the
host's gate lock (`<job tmp>/gate-lock.sh <label> -- <command>`, one heavy
gate at a time across the agents on this host, the plan reviewer's rule);
each printed the head (`git rev-parse --short HEAD`: `bd683f28`) inside
the lock. `npm test` did not, yet: the rule was widened to it after these
runs, and its first run's R53 stop below is the case the widening names
(another agent's suite running at the same time).

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'` (the whole module): exit 0, 56 packages `ok`, no
  `FAIL` (the lock taken after 125 s of waiting).
- The pi gate: `cd pi && npm test`: the first run exit 1 on one row
  outside this PR's files, `R53: the owner's yes switches the lead's tools
  to exactly the team lead's eleven and refuses the solo set phase_team`
  (`amq-squad guard failed closed (timed out after 5000 ms)`, `actual:
  'blocked'`, `expected: 'ran'`; 8.57 s), run while another agent's
  `make ci` held the lock; run once more, exit 0: `ℹ tests 118`, `ℹ pass
  117`, `ℹ fail 0`, `ℹ skipped 1`. `cd pi && node scripts/lint.mjs`: exit
  0.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: the first run exit 2, its `go test` on
  the known load flake `internal/seatlaunch`
  `TestPiSeatPlansThroughTheUpstreamAdapter` (`amqpi_test.go:75: outcome
  unsupported (launcher_not_available)`, 5.18 s; already on the 3.2.x
  list); run once more, exit 0: 70 `ok` (55 from the cache of the
  whole-module run), no `FAIL`, `internal/seatlaunch` 21.2 s, the same
  four pi counts.
- The format gate, Go 1.26's gofmt first on PATH, `make fmt-check`: exit
  0, nothing named.
- `GOOS=windows go build ./internal/seatcommit/`: exit 0;
  `go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`:
  exit 0 (on this note).

Before that, phase one on `8eeeaf24` (Codex's re-pass on `b250b951`: `9de43a6c`,
`8eeeaf24`; this note is the only change after it), each from the
worktree root, one at a time. These ran before the gate-lock rule reached
this task: none of them ran under `gate-lock.sh`, so the whole-module run
whose `internal/buzz` row hung shared the host with other agents'
whole-module gates (the load average of 10.8 below), the condition the
rule was made for.

- `go build ./... && go vet ./... && go test -count=1 ./...` (the whole
  module): the first run exit 1, on one row outside this PR's files:
  `internal/buzz` `TestOwnerScriptDryRun` (`--- FAIL: TestOwnerScriptDryRun
  (300.80s)`, `enroll_test.go:447: hung: no signal for "the attach verifies
  the thread answer" (the run's 5m0s budget ran out)`), the owner-script
  dry run against its own local relay on 127.0.0.1, with the host's load
  average at 10.8 from other sessions; every other package `ok` (55). Run
  once more, exit 0: 56 packages `ok`, `internal/buzz` 80.97s, no `FAIL`.
  It did not fail because files changed (nothing was edited during the
  run; the row hung on a wall-clock budget), so it is a load flake and is
  on the 3.2.x list by name ("Residual risk").
- The pi gate: `cd pi && npm test`: exit 0, `ℹ tests 118`, `ℹ pass 117`,
  `ℹ fail 0`, `ℹ skipped 1`; `cd pi && node scripts/lint.mjs`: exit 0.
- `PATH=<job tmp>/pyvenv/bin:$PATH NPM_FLAGS=--offline make ci`: exit 0,
  70 `ok` (26 of them from the cache of the whole-module run above), no
  `FAIL`, the same four pi counts.
- The format gate, Go 1.26's gofmt first on PATH, `make fmt-check`: exit
  0, nothing named.
- `GOOS=windows go build ./internal/seatcommit/`: exit 0;
  `go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`:
  exit 0 (on this note).

Before that, phase one on `c27ad8fc` (the batch on `c9826c31`: `ccec9400`,
`23f315e3`, `01088a5c`, `c27ad8fc`; this note is the only change after
it), each from the worktree root, one at a time on a quiet tree:

- `go build ./... && go vet ./... && go test -count=1 ./...` (the whole
  module): exit 0, 56 packages `ok`, no `FAIL` (`internal/seatlaunch`
  74.7s and `internal/registry` 101.5s among them, both passing).
- The pi gate: `cd pi && npm test`: exit 0, `ℹ tests 118`, `ℹ pass 117`,
  `ℹ fail 0`, `ℹ skipped 1`; `cd pi && node scripts/lint.mjs`: exit 0.
- `PATH=<job tmp>/pyvenv/bin:$PATH NPM_FLAGS=--offline make ci`: exit 0,
  70 `ok`, no `FAIL`, the same four pi counts, every check through
  claude-hooks-test and pi-test. Its `go test` runs without `-count=1`
  and took the packages from the cache the whole-module run above had
  just filled on the same tree; the uncached run is that one.
- The format gate with go.mod's Go 1.26 gofmt first on PATH
  (`PATH=<GOMODCACHE>/golang.org/toolchain@v0.0.1-go1.26.10.darwin-arm64/bin:$PATH
  make fmt-check`): exit 0, nothing named.
- `GOOS=windows go build ./internal/seatcommit/`: exit 0;
  `go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`:
  exit 0.

Before that, phase one on `a692ebca` (the merge of `origin/v3` at
`d522a066`, #905, into `8017ffea`), each from the worktree root:

- `go build ./... && go vet ./... && go test -count=1 ./...` (the whole
  module): exit 0, 56 packages `ok`, no `FAIL`.
- The pi gate: `cd pi && npm test`: exit 0, `ℹ tests 118`, `ℹ pass 117`,
  `ℹ fail 0`, `ℹ skipped 1` (the staged-runtime row, local-only);
  `cd pi && node scripts/lint.mjs`: exit 0.
- `NPM_FLAGS=--offline make ci`: exit 0 (fmt-check included), with the job
  scratch's PyYAML venv first on PATH (`PATH=<job tmp>/pyvenv/bin:$PATH`,
  as the 0c-A note ran it; no network, nothing installed outside the
  scratch): fmt-check, `go vet`, `go test -timeout 20m ./...` (70 `ok`,
  no `FAIL`), the README and docs html checks, skills-check,
  skill-routing-check, skill-command-check, skill-invocation-check,
  release-validator-test, go-files-scope-test, claude-hooks-test and
  pi-test (the same four pi counts).
- Beside the three: `go test -count=1 ./internal/scheduler/
  ./internal/schedproc/ ./internal/seatcommit/ ./internal/cli/
  ./internal/uninstall/`: exit 0, five `ok`; `GOOS=windows go build
  ./internal/seatcommit/`: exit 0; `go run ./scripts/redact-evidence
  -check docs/v3-spike/320-0c-quarantine/`: exit 0.

The same gates ran exit 0 on `8017ffea` before the merge (the whole-module
test through `make ci`; the five packages, the windows build and the
redact check on their own). One earlier `make ci` on `88df5bad` stopped on
`internal/seatlaunch` `TestPiSeatPlansThroughTheUpstreamAdapter`
(`amqpi_test.go:75: outcome unsupported (launcher_not_available)`), the
row classified in "The plan reviewer on c9826c31" above: the #905 kind of
flake under load, on the 3.2.x list by name; the runs on `8017ffea`,
`a692ebca` and `ccec9400` passed it, and no run was repeated beyond that.

The first gates, on `7ca9da50` (the four code commits), were: build and
vet exit 0; the five packages exit 0; `make ci` offline exit 0 (70 `ok`,
`ℹ tests 118`, `ℹ pass 117`, `ℹ fail 0`, `ℹ skipped 1`); the redact check
exit 0; the windows build exit 0.

## Residual risk and what the brief leaves to later

- The screen's "waiting on you" block is derived only from open gates
  (`screen.ts`), so it can say "Nothing is waiting on you" while a
  lead-routed `ledger_tampered` block exists; the tasks block and the seat
  stop line carry the block. The fix (the section from open gates plus
  lead-routed blocks, the tamper block listed there with the rule-7 words,
  the "nothing" line only when both are empty) is this PR's second phase,
  after t102 merges (the plan in "Gates").
- The network: `ls-remote` is the only network operation the recovery
  adds, once per distinct carried branch, none with no carry, under
  `RecoverTransportTimeout`; a transport failure refuses before anything
  is written and releases the lock.
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
  The threat model is unchanged: the recovery offers no protection against
  a same-uid process that moves the worktree HEAD (the recovered base is
  the observed HEAD; the quarantined base, rest, lease, target and
  `gh_path` are never carried).
- What an older reader does (Codex's P3 on #904): a 3.1.2 reader of a
  recovered ledger refuses it as `ledger_tampered`. Its decode drops
  `recovered` and the carried entries' `note` (fields it does not have), so
  the body it re-encodes for the tag differs from the one sealed, and the
  tag fails; continuing the chain at N+1 gives no backward readability
  either. This is the rc.2 rule (0e, before the release that carries
  0c-B): a ledger a newer amq-squad wrote is that distinct refusal, not a
  tamper; until then a downgrade reads a recovered seat as tampered, with
  its evidence kept (0c-A) and this recovery the way back.
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
- For the 3.2.x list, by name: `internal/seatlaunch`
  `TestPiSeatPlansThroughTheUpstreamAdapter`, a 5 s wall-clock row
  (`amqpi_test.go:75: outcome unsupported (launcher_not_available)` when
  AMQ's tmux detection misses its deadline under load); it stopped one
  `make ci` on `88df5bad` and passed every run since. It is outside this
  PR's files.
- For the 3.2.x list, by name: `pi/test` R53's "the owner's yes switches
  the lead's tools to exactly the team lead's eleven" row, its guard's
  5000 ms wall-clock bound (`amq-squad guard failed closed (timed out
  after 5000 ms)`); it stopped one `npm test` on `bd683f28` while another
  agent's `make ci` ran, and passed the run after it. Outside this PR's
  files.
- For the reviewer (`ad3330e0`): a slot reused by the quarantine's no-op
  for an identical refusal can predate its hold, so a record of an earlier
  recovery from that slot, put back by hand after the same refused bytes
  were put back, releases the new hold; the baseline does not stop it
  (the refused bytes predate the record). Two deliberate whole-file
  restores by a same-uid process; the threat model above is unchanged.
  Closing it would take a slot that is never reused once a recovery named
  it, or a release that needs the verb's message for every hold.
- A hold whose document changed again after its alarm is not answered by
  a recovery from the later slot (the recovery must name the slot whose
  bytes are the live document's); the lead then sends the verb for the
  hold's own slot too, which the verified mode records. Two decisions
  where the lead might expect one
  (`TestAHoldOnAnEarlierSlotIsAnsweredAfterALaterRecovery` drives the
  pair).
- For the 3.2.x list, by name: `internal/buzz` `TestOwnerScriptDryRun`,
  a 5 min wall-clock budget over the owner-script dry run on a local relay
  (`enroll_test.go:447: hung: no signal for "the attach verifies the thread
  answer"`); it stopped the first whole-module run on `8eeeaf24` under a
  load average of 10.8 and passed the run after it. Outside this PR's
  files.
