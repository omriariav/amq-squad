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
asked for; then `c267382d` and `f1edd375`, the notes on it; then
`1cd21fa1`, the baseline removed (the plan reviewer on `f1edd375`, the
section "The baseline removed"), and `c5754be4`, its note; then
`28051cd6`, the alarm id and the decision by alarm (the plan reviewer's
ruling on Codex's delta pass on `c267382d`, the section "The alarm id"),
and `5d2a576a`, its note; then `c83e6ef3`, the row for a decision whose
hold is released before its record is sealed, `254f2c2c` and
`9cf04743`, the notes; then `40254653`, Codex's pass on `254f2c2c` (the
section "Codex on 254f2c2c"), and `16c62fb4`, its note; then `37379e5b`,
the reviewer's rulings on that pass (the section "The reviewer on Codex's
pass on 254f2c2c"), and `ff7b5530`, its note; then `cb4f7d13`, the
refusal words dropped (the reviewer accepted leaving them out), and
`8cffafe7`, its note; then `eff2fea1` and `97a05cc4`, Codex's pass on
`8cffafe7` (the section "The hold release file"), and `50986a71`, its
note; then `a57e9627`, Codex's pass on `50986a71` (the append's
descriptor; the refusal's words), and `a817fed3`, its note; then
`85b5d8df`, the merge of `origin/v3` at `3966afb7`, and `f3d72b7f`, the
anchored prefix (the section "The merge of origin/v3, and the anchored
prefix"); then this note. The helper move of `8017ffea` is between
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
recovery record the ledger carries and the seat's hold release file
(`RecoveryReader`, `CleanSeats` through `seatcommit.RecoveriesOf`, no
quarantine on a refusal; "The hold release file"), and a hold whose exact
slot the ledger's chain record names, or whose alarm a verified line of the
hold release file names, is
released (the seq rule this paragraph first described is gone: "The plan
reviewer on af1a6124" and "The alarm id"): every task
the alarm blocked `ledger_tampered` returns to pending (`task_reset`, "the
commits ledger of seat H was recovered by the lead"), then the hold goes
and `ledger_hold_released` is logged. A clear is not the recovery:
`ledger_tampered` is not in `ClearableWarnings` (refused
`clear_not_a_warning`, as before), and no clear touches `s.tampered`.

`RecoverLedger`, under the seat's ledger lock: the document on disk must
be tampered for the chain continuation (one that verifies takes the
verified mode when the named slot exists and the chain is at or past its
seq, `recordVerified`, `recover.go:333`, and is `recover_not_tampered`
otherwise, `:352`; this paragraph first said every verifying document is
refused; one absent with no head is the launch's to record) and byte-identical to the slot's copies
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
the raw writer with prev `{Seq: N, Tag: <that tag>}`, so it is seq N+1
and links to the quarantined head. Recovery and ordinary updates share
`saveRawLedger`: body bytes, newline, strict raw seal JSON, final newline,
then a raw-sealed head. The writer receives the predecessor selected from
the exact copied bytes and never reopens the head inside `sealchain`; the
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
  quarantine reuses for an identical refusal, is in that section. The
  baseline itself was removed in `1cd21fa1`: "The baseline removed".)
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
  the P2 per task) require to log again. The trade-off: two failures
  that differ only in a path or a digit run count as one (the first is
  logged with its full text, the rest counted); a change of wording logs
  again (`TestReasonsDifferingOnlyInATempNameLogOnce`, `e1fc443c`).
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
  when it names the hold's own slot and is not the baseline (that part
  removed in `1cd21fa1`); the seq
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
  once the ledger answers that same record.
  A message lost in a crash loses nothing: the verb sent again is
  answered from the ledger (`AlreadyRecovered`) and tells the scheduler
  again. A hold with a slot ignores the message (its slot answers it).
  The lead needs a slot to name: a later refused read of the document
  quarantines it (0c-A), and the lead recovers from that slot.
  (Superseded by `28051cd6`, "The alarm id". Two statements here were
  wrong, as Codex's delta pass on `c267382d` found: a refusal that
  quarantined nothing and a ledger that verifies again leave no slot to
  name, and the verified mode needs one (`recover.go:300` at `c267382d`), so such a
  hold stranded; and the apply step answered `recover_unrecorded` before
  it told the scheduler (`schedproc/recover.go:149` returned before
  `:155`), so its "the scheduler releases the seat at its next tick" was
  false for a hold with no slot. `LeadRecovered` and `Decided` are gone;
  the decision by alarm is sealed into the ledger, which the next tick
  reads, so `recover_unrecorded` now releases at the next tick for every
  hold.)
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
- **The baseline can only withhold a release.** (Removed in `1cd21fa1`;
  see "The baseline removed".) It was decoded from the refused bytes
  without verification, and `covers` checked it only to say no.
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
- **The baseline check (withdrawn on `f1edd375`).** The reviewer first
  held it reachable on the slot-reuse path below; on `f1edd375` they
  agreed it is not (a reused slot's bytes predate any record naming it,
  `sameSlot`, `internal/seatcommit/quarantine.go:351`) and withdrew the
  ruling, so `1cd21fa1` removes it ("The baseline removed").
- **The slot-reuse replay, residual risk for rc.2.** The one way a slot
  predates its hold is the quarantine's no-op for an identical refusal:
  the document recovered from slot X, then the exact refused bytes of X
  put back (a new hold, on X again), then the recovered document put back
  (it verifies and records X), which releases the new hold with no new
  decision; the exact-slot rule does not stop it (the record names X, the
  hold's own slot), and no baseline of the earlier record is kept (it
  could not have stopped it either: the refused bytes predate the record
  of X). It needs two deliberate whole-file restores by a
  process running as the same user, which is outside the threat model
  0c-A stated ("The threat line", `A-quarantine.md`: the quarantine is
  evidence against accident and other writers, not a vault against the
  owner's own account). The reviewer accepted it as residual risk for
  rc.2, with the 3.2.x item "a quarantine slot named by a recovery record
  is never reused; identical bytes refused again get a new .k slot"
  ("Residual risk").
- **A recovery from another slot leaves the hold.** When the document
  changed again after the alarm (a later refused read quarantined it in
  another slot), the recovery must name the later slot (the decision is
  refused for a slot whose bytes are not the live document's), and the
  hold on the first slot is not answered by it. That recovery makes the
  document verify, and the verb sent for the first slot takes the
  verified mode only when the chain is at or past the first slot's seq
  (`recordVerified`, `recover.go:333`, its behind-seq refusal `:352`);
  `bd683f28`'s row is that case
  (both slots at seq 1). The claim that this always answers the hold was
  wrong (Codex's delta pass on `c267382d`): when the later slot is at a
  lower seq (the head lowered), its recovery puts the chain behind the
  first slot's seq and the verified mode refuses it. The hold is then
  released by the decision by alarm (`28051cd6`, "The alarm id"), which
  needs no slot and no seq ordering.

## The baseline removed (`1cd21fa1`, the plan reviewer on `f1edd375`)

The reviewer agreed that the baseline check never decides on real files
and withdrew the ruling that kept it; under the rule of no production
code kept alive only by a test, `1cd21fa1` removes it.

- **Why the exact slot subsumes it.** `covers` accepts only a record
  that names the hold's exact slot. A record that predates the alarm
  cannot name a slot the alarm created. The one way a slot predates its
  hold is the quarantine's no-op for an identical refusal, and that reuse
  needs byte-identical bytes (`sameSlot`,
  `internal/seatcommit/quarantine.go:351`, compares the body, the head and
  the stable sidecar), so the bytes that reuse a slot are the slot's own
  copy, made before any record naming the slot could be sealed: they
  never carry a record of that slot, and the baseline they gave could
  never equal one. What the slot reuse does allow, the slot-reuse replay,
  stays the stated same-uid residual for rc.2, closed by the 3.2.x item
  "a quarantine slot named by a recovery record is never reused;
  identical bytes refused again get a new .k slot" ("Residual risk").
- **What went.** The check in `covers`; `LedgerTamper.Baseline` (the
  state file's `baseline` field; a state file written with it still
  decodes, the field ignored) and `LedgerTamperedError.Baseline`;
  `QuarantinedError.Prior`, `priorRecovery` and the prepare alarm's
  mapping (`prepareError`); the rows that only exercised them: the
  hand-built `TestTheRecoveryPresentAtTheAlarmDoesNotReleaseIt`,
  schedproc's `TestThePrepareAlarmCarriesTheLedgersRecordAsTheBaseline`
  and the quarantine's `Prior` half of
  `TestAVerifiedRecoveryRetriedReportsItsLink` (its P3 half, the retry's
  link and mode, stays). The revert runs of those rows above are kept as
  the record of what was run; their code is gone.
- **What stays.** (Superseded by `28051cd6`: `LeadRecovered` and
  `Decided` are gone and `RecoveryMark` is seq, slot and alarm; "The
  alarm id".) `RecoveryMark` keeps its four fields (seq, slot, link,
  time): `covers` reads the slot, and the verb's decision for a hold with
  no slot (`LeadRecovered`, `Decided`) is matched on all four.
  `covers` is now: a hold with a slot is answered by a record that names
  that exact slot; a hold with no slot only by the decision the recover
  verb sealed for it.
- **The covers rows, all passing on `1cd21fa1`** (`go test -count=1
  -v -run ...` by name, each `--- PASS`): `internal/scheduler`
  `TestALoweredSeqReplayDoesNotReleaseTheHold` (the lowered-seq replay),
  `TestARecoveryForAnotherSlotDoesNotReleaseTheHold`,
  `TestAHoldWithNoSlotIsReleasedOnlyByTheLeadsVerb`,
  `TestAnOlderRecoveryRecordDoesNotReleaseAHold`,
  `TestRecoveryReleasesTheSeatAndReturnsTheTask`,
  `TestRestartedHoldIsReleasedFromTheLedgersRecovery`,
  `TestTwoHeldSeatsOneRecovered`, `TestARecoveryKeepsATamperBlockOfAnotherSlot`,
  `TestARecoveryNeverClearsABlockRaisedAfterItsRead`,
  `TestAStoreReadErrorKeepsTheHoldAndLogsOnce`,
  `TestAFailedResetKeepsTheHoldForTheRetry`; `internal/schedproc`
  `TestTheLeadsVerbReleasesAHoldWithNoSlot`,
  `TestARestoredDocumentIsReleasedByTheLeadsDecision`;
  `internal/seatcommit` `TestAHoldOnAnEarlierSlotIsAnsweredAfterALaterRecovery`,
  `TestAVerifiedRecoveryRetriedReportsItsLink`. No revert run for a
  removal.

## The alarm id (`28051cd6`, the plan reviewer's ruling on Codex's delta pass on `c267382d`)

Codex's delta pass on `c267382d` found three P2, each a hold the
exact-slot rule could not release or released wrongly: (1) a transient
read refusal whose quarantine copy failed gives a hold with no slot, and
once the ledger verifies no slot ever appears, while the verified mode
needs one (`recover.go:300`; the line refs of this paragraph are
`c267382d`'s); (2) a hold on slot X at seq 150, the head
then lowered to seq 1 (slot Y): X is refused while the live document is
Y's, Y's recovery seals link 2, and X's verified recovery is refused
because 2 < 150 (`recover.go:316`), so X stays held; (3) a late
`LeadRecovered` attached itself to whatever no-slot hold the seat had at
the next tick (`tamper.go:227`). The reviewer accepted a durable alarm id
per hold as the root fix, with six conditions; as built:

1. **The id.** Eight hex characters from the system's random source
   (`newAlarmID`, `crypto/rand`), never a counter: 32 random bits, so a
   collision across restarts is improbable, not excluded (two holds on one
   seat with the same id would let one's decision answer the other);
   kept in the state file with the hold (`LedgerTamper.Alarm`)
   and restored with it.
2. **Shown to the lead.** The block's detail carries it (`seat H:
   quarantined at PATH (seq N; alarm ID): ...`, or `could not be
   quarantined (seq N; alarm ID)`), so the stop line and the screen's
   seats and tasks blocks read: "seat H's commit record failed its check
   (alarm ID); after investigating, the lead runs amq-squad scheduler
   recover --seat H --alarm ID --reason "REASON"" (`words.ts` `tamperAlarm`, the
   `ledger_tampered` block words, and `decisionWords` reading the
   decision's detail; `screen.ts` unchanged).
3. **The decision by alarm.** (Condition 3 is superseded: by the P2-1
   ruling on `254f2c2c`, then by the hold release file of `97a05cc4`,
   where the decision is a sealed line and the ledger is unchanged, and an
   unverified ledger is copied and refused, never rebuilt; "The hold
   release file".) `scheduler recover --seat H --alarm ID
   --reason R` goes through the decision socket with the lead-pane proof,
   as the verb does. The apply step refuses it `recover_alarm_not_held`
   unless the seat is held by that alarm now (`Scheduler.HeldAlarm`, the
   tick's holds published under the scheduler's lock), and the ledger
   half refuses it `recover_alarm_unverified` unless the ledger verifies
   now (`seatcommit.recordAlarm`). It carries nothing, names no slot and
   no seq, and seals the next link of the same body with the record
   naming the alarm (`LedgerRecovery.Alarm`, `Verified`, `Link`); the
   decision line names it ("the lead L released alarm ID of seat H by
   decision ..."). Sent again while the hold still stands (before the
   next tick reads the record), it is answered from the ledger
   (`AlreadyRecovered`) and the line is recorded then. Once the tick has
   released the hold the socket refuses a repeat
   (`recover_alarm_not_held`), so a decision-log append that failed after
   the seal (`recover_unrecorded`) cannot be repaired by sending it again
   after the release; an earlier version of this item said a resend
   always answers. What stands then: the sealed record (who, why, when,
   the alarm), and the release line, which names the alarm; the missing
   `ledger_recovered` line is not recreated, and the lead tells the
   owner in words (the reply's `recover_unrecorded` says the chain is
   written).
4. **The release names the alarm; no notification.** (Condition 4 is
   superseded by the hold release file of `97a05cc4`: the decision is a
   verified line of that file, not a record in the ledger body; "The hold
   release file".) `covers` releases a
   hold on a record that names its alarm, or its exact slot; a decision
   by alarm for any other alarm answers nothing. The release line ends
   "alarm ID is released and the seat is grantable again", for the slot
   release too. `LeadRecovered` and `Decided` are removed rather than
   given the id: with the alarm in the sealed record the ledger alone
   names the released hold, so the release stays a reconciliation (the
   design since Codex's P2 on #904) and no notification exists to go
   stale. The stale case is refused where it can still arise, at the
   socket: a decision naming an alarm the seat is not held by now is
   `recover_alarm_not_held`, answered to the lead (no tick line is
   needed). This replaces the reviewer's condition 4 (the notification
   carrying the id), and the reviewer accepted it on `5d2a576a` as better
   than adding the id: the sealed `LedgerRecovery.Alarm` makes the release
   a pure reconciliation from the ledger, so no in-process message is left
   to go stale, and a stale id is refused at the socket. The one window the
   socket's check cannot close (the check passes for A, then A's hold is
   released by its slot and B is raised before A's record is sealed) is
   closed by the record itself: it names A, so B stays held
   (`TestADecisionForAReleasedAlarmDoesNotReleaseTheNextHold`, `c83e6ef3`).
5. **Rows**, each with a revert run below: the no-slot hold released by
   its alarm after the ledger verifies (and across a restart); the
   lower-seq slot recovered by its own slot while X releases only by its
   alarm (the scheduler, and the ledger half on real files); the late
   decision for an earlier alarm refused; the decision by alarm refused on
   a ledger that does not verify, and for a wrong or released id.
6. **The note's errors**, corrected where they stand: the two-decision
   claim ("The plan reviewer on af1a6124" and "Residual risk"), "the
   next tick releases" for a hold with no slot (the same section), and
   the "P2 retry" row (`recover_not_tampered` is for a slot that does not
   exist; an existing slot the chain is at or past takes the verified
   mode). The comment above `applyRecoveries` (`tamper.go:251` now) no
   longer describes a seq release, nor does the package comment.

The release's reset now matches the hold by its alarm (`names`: a
`ledger_tampered` block whose detail names `; alarm ID)`), so a tamper
block of another alarm stays (`TestARecoveryKeepsATamperBlockOfAnotherSlot`,
whose other block now names another alarm). A hold an earlier build of
this branch wrote without an id gets one when it is restored; its blocks
name none, so their tasks are reset by the lead by hand (no released
version wrote such a hold).

## Codex on 254f2c2c (`40254653`)

Codex re-read `254f2c2c` (reading only; the gates below are the run
evidence), confirmed the earlier items and found no route to a seat
forging a decision, and found two P2 and a P3:

- **P2-1, no path out.** The body corrupted while the head decodes and the
  quarantine copy failing gives a hold with no slot. With the cause
  repaired the ledger stays corrupted: the decision by alarm refused it as
  unverified (and named a slot it never made), a slot recovery stops at
  `recover_slot_missing`, and a held seat is never prepared, so no read
  makes the copy. Now the decision by alarm on a ledger that does not
  verify makes the lead's quarantine copy of the live bytes (0c-A's
  `quarantine`, the copy a refusing read makes, with the verb `scheduler
  recover --alarm` on the sidecar) and recovers from that slot in the
  same step, under the same lock (`recordAlarm`, `recover.go:387`); the
  record names the slot and the alarm, and the decision line says "the
  decision copied it to quarantine SLOT and recovered from it". It is
  refused `recover_alarm_unverified` only when the copy fails ("... the
  copy of it into the quarantine the decision by alarm recovers from
  could not be made: WHY; fix what the copy names, then send the decision
  again") or the ledger is not a document at all. This changes the
  reviewer's condition 3 ("refused unless the ledger verifies NOW"): an
  unverified ledger is recovered from its fresh copy, a verified one is
  sealed as before; accepted by the reviewer as proposed. In the
  reviewer's words: condition 3 is superseded by the P2-1 ruling.
- **P2-2, a delayed write over a newer recovery.** A's decision passes the
  socket's check (`HeldAlarm(A)`) and waits; A's hold is released, B is
  raised and B's decision sealed; A resumes and its seal replaced B's
  record, so B stayed held. The apply step now hands the ledger half a
  guard (`RecoverOptions.Guard`) that checks again that the request's
  alarm holds the seat, run inside the ledger lock immediately before
  every seal (the alarm's, the chain continuation's and the verified
  mode's); it refuses `recover_alarm_not_held` with nothing written.
  Between the guard and the seal, both under the lock, a tick can still
  change the holds, but a newer hold's own record can only be sealed under
  that same lock, so A's seal never replaces it; at worst a hold raised in
  that instant waits for its own decision.
- **P3, the command as shown.** The screen's words now read "... the lead
  runs amq-squad scheduler recover --seat H --alarm ID --reason
  \"REASON\"": the verb requires `--reason`.
- **Words for the two refusal codes: not added.** Codex asked for
  `words.ts` entries for `recover_alarm_not_held` and
  `recover_alarm_unverified`. No pi surface reads them: a socket refusal
  is the CLI caller's reply only (pi has no recover key, and the decision
  log records accepted recoveries, not refused ones), so entries would be
  read by a test alone, against the reviewer's rule. The words the lead
  reads are the reply's detail, which the CLI prints after the code, and
  both details are written to be acted on (which alarm holds the seat;
  what the copy needs fixed). Put to the reviewer, who accepted it:
  `37379e5b` had added them on the earlier ruling, and `cb4f7d13` drops
  them ("The reviewer on Codex's pass on 254f2c2c").

The rows (each with its revert run below):
`TestTheAlarmVerbRecoversALedgerWhoseCopyFailed` (real files: the copy
refused by the quarantine's mode, the decision refused in words, the mode
repaired, the decision copying and recovering, the seat released) and
`TestADelayedAlarmDecisionNeverSealsOverANewerRecovery` (real writers: A
held at the seam after its check, A released by its slot, B held and
sealed, A resumed and refused, B's record kept and B released; run with
`-race` too). Two rows lose a step that expected a refusal on a tampered
ledger (`TestTheAlarmVerbReleasesAHoldWithNoSlot`,
`TestADecisionByAlarmAnswersAHoldNoSlotCan`): that ledger is now copied
and recovered, which the copy-failure row covers.

## The reviewer on Codex's pass on 254f2c2c (`37379e5b`)

- **P2-1, accepted as built in `40254653`.** The decision by alarm on a
  ledger that does not verify makes the lead's copy through the same code
  a refusing read uses (`quarantine`, which `ReadLedgerAs` calls), then
  recovers from that slot in the same apply; the record names the slot and
  the alarm; it is refused only when the copy fails, in words. The row is
  Codex's sequence (`TestTheAlarmVerbRecoversALedgerWhoseCopyFailed`).
- **P2-2, fixed at the root: one record per alarm.** (Superseded by
  `97a05cc4`: `AlarmRecoveries` is removed and the decisions are lines of
  the hold release file, which no chain continuation touches; "The hold
  release file".) The single
  `Recovered` field let an older alarm's late decision replace a newer
  one's. The ledger now keeps `AlarmRecoveries` (`alarm_recoveries`), one
  record per alarm id: `recordAlarm` appends, never replaces another
  alarm's record nor `Recovered`, and answers a repeat of the same alarm
  from its record. `covers` asks whether any sealed record names this
  hold's alarm (`RecoveryMark.Alarms`, read with the chain's own record in
  one read by `seatcommit.RecoveriesOf`), or whether the chain's own
  record names this hold's exact slot and no alarm (`SlotAlarm` empty). A
  chain continuation starts a new body, which carries only its own
  decision's record (the earlier body is the refused one, so its records
  are not trusted). That dropped a decision sealed but not yet read by a
  tick (Codex on `8cffafe7`), which is why the decisions left the ledger
  body. A's late
  record therefore cannot take B's away, whatever the interleaving. The
  under-lock `HeldAlarm` guard of `40254653` stays on top, so an obsolete
  decision is refused at write time (`recover_alarm_not_held`) instead of
  sealing a record nothing reads.
- **The lock order** (in the code above `covers`): the ledger lock (the
  seat's `flock`, `withLedgerLock`), then the scheduler's mutex `s.mu`.
  The guard reads `HeldAlarm` under `s.mu` while the decision holds the
  ledger lock. The tick never takes them the other way round: every
  `s.mu` section in the scheduler is a copy of one field (`Post`'s
  append, the tick's drain of `pending`, the relaunch ask, `lastRelaunchAt`,
  `publishHolds`, `HeldAlarm`), none calls out, and the tick calls the
  preparer and the reader (which may take the ledger lock) with `s.mu`
  free. The tick changes the holds (`s.tampered`) without the ledger lock;
  a hold that changes between a guard and its seal costs nothing, since
  the record sealed names its own alarm only and every other alarm's
  record stays.
- **P3, accepted.** The command shows `--reason "REASON"` (`40254653`).
  The words entries for the two refusal codes: `37379e5b` added them
  (`RECOVER_REFUSAL_WORDS`) on the first ruling; the reviewer then
  accepted leaving them out, since no pi surface sees those codes (a
  socket refusal is the CLI caller's reply only), and `cb4f7d13` drops the
  table and its row. The words row-7 scan passes without them and needs
  no exemption: it reads the `Reason*`, `Block*` and `Refusal*` constants
  and the block-field literals of `internal/task`, `internal/seatcommit`,
  `internal/scheduler` and `internal/routing`, and the launch files'
  `Code:` literals; `recover_alarm_unverified` is a `Recover*` constant
  (`RecoverAlarmUnverified`) and `recover_alarm_not_held` is in
  `internal/schedproc` (`RefusedRecoverAlarmNotHeld`), both outside what
  it reads.
- **Condition 3 is superseded by the P2-1 ruling** (the reviewer's
  words): the decision by alarm is not refused for a ledger that does not
  verify; it copies it and recovers from the copy, refused only when the
  copy fails.
- **Recovered is no longer the decisions' field.** Every decision by
  alarm is kept per alarm (`AlarmRecoveries`); `Recovered` stays only as
  the chain's own record, the body a slot recovery or a copy-and-recover
  started from, and nothing a decision by alarm writes replaces it or
  another alarm's record.
- **What the sealed ledger holds** changes again (the compatibility item
  in "Residual risk"): the recovery field changes shape,
  `alarm_recoveries`, and `recovered.alarm`.

Rows (each with its revert run below):
`TestAnOlderAlarmsRecordLandingAfterBsLeavesBReleased` (real files: B's
decision by alarm sealed, then an older alarm A's record landing after
it from a writer with no guard; both records kept; B released, and it
stays released); the barrier row
`TestADelayedAlarmDecisionNeverSealsOverANewerRecovery` ends with B
released and A refused, as before; three rows read the alarm's record
from `AlarmRecoveries` now (`TestTheAlarmVerbReleasesAHoldWithNoSlot`,
`TestADelayedAlarmDecisionNeverSealsOverANewerRecovery`,
`TestADecisionByAlarmAnswersAHoldNoSlotCan`, which also checks that Y's
chain record stays beside the alarm's). (The refusal words' row of
`37379e5b` left with the table in `cb4f7d13`.)

## The hold release file (`eff2fea1`, `97a05cc4`, the plan reviewer's ruling on Codex's pass on `8cffafe7`)

Codex's pass on `8cffafe7` found three P2 and a P3: (1) the alarm branch
returned before `RecoverLedger`'s defaults, and `scheduler run` builds its
`Recovery` without Git, so the copy-and-recover path always failed in
production (the row passed only because it set `Git: "git"`); (2) a chain
continuation's fresh body kept only its own record, dropping a decision
sealed but not yet read by a tick; (3) the decision by alarm could
rebuild a ledger that changed between the lead's look and the apply,
unseen; (4) `AlarmRecoveries` grew with the ledger forever. The reviewer
accepted a simplification over patching:

- **(c) The defaults before any branch** (`eff2fea1`): `RecoverLedger`
  sets Git, env and the clock first; `scheduler run` builds its
  `Recovery` through `schedproc.NewRecovery(root, target)`, the
  constructor the rows use (Git omitted).
- **(b) Copy only, never a rebuild** (`eff2fea1`): on a ledger that does
  not verify, the decision by alarm makes only the lead's quarantine copy
  (`quarantine`, the copy a refusing read makes) and refuses
  `recover_alarm_unverified` with "seat H's ledger does not verify; it
  was copied to slot S; recover it with `amq-squad scheduler recover
  --seat H --seq N --reason ...`, which shows what it carries; then, if
  the hold remains, send the alarm decision again" (`--slot K` added when
  the copy is not the first slot of its seq, so the command runs as shown;
  "if the hold remains" since `a57e9627`). The slot recovery comes first.
  When the hold names that exact slot, the slot recovery releases it at
  the next tick and nothing more is needed (an alarm decision sent then is
  `recover_alarm_not_held`, the hold being gone); the decision by alarm is
  needed only when the hold has no matching slot (a refusal that
  quarantined nothing, as in the two-step row, or a slot the chain has
  moved past). An earlier version of this item said recovery is always
  the two steps.
- **(a) The decisions in a file of their own** (`97a05cc4`): the
  decision by alarm is a line of the seat's hold release file,
  `<root>/extensions/amq-squad/commits/<handle>.alarms.jsonl`
  (`seatcommit.AlarmsPath`, `internal/seatcommit/alarms.go`), appended
  under the seat's ledger lock, so the lock order is unchanged, through one
  descriptor (`openAppendNoFollow`, `a57e9627`: on unix
  `O_RDWR|O_APPEND|O_CREAT|O_NOFOLLOW|O_NONBLOCK`, the descriptor fstat'd
  and required to be a regular file, the torn-tail check reading the last
  byte through the same descriptor, beside the quarantine's unix
  descriptor helpers; off unix a link check, then the open). Codex on
  `50986a71` found the first version checked the path and then opened it
  again, so a swap to a symlink between the two appended elsewhere and a
  swap to a FIFO blocked the open under the ledger lock. A line is
  the JSON body (seat, alarm, reason, time, and the lead the socket
  proved), a tab, and the hex HMAC-SHA256 under the session seed's key of
  the domain string "amq-squad scheduler hold release v1", a zero byte
  and the body's bytes exactly as written (`alarmTag`). Readers verify the
  HMAC over the bytes as read and never re-encode (`ReadAlarms`), so a
  body with a field this build does not know verifies, and a later build
  can add fields without an older one refusing the line; that rule is the
  file's comment. No head and no chain: a deleted, truncated or reordered
  line only keeps a hold held (the safe direction); a forged or edited
  line fails the HMAC; a line from another session root fails under its
  different key; a line from another seat's file names another seat; a
  replayed line is idempotent, since it names one alarm. A last line
  with no newline is a torn tail: skipped and reported, the lines before
  it still count, and the next append terminates it first. Each line that
  does not verify reaches the lead once per seat and problem
  (`hold_release_line_skipped`, a stop with words, resolved by the seat's
  release), and releases nothing while the others still count. On a
  ledger that verifies, the decision appends its line and leaves the
  ledger unchanged; a repeat is answered from the file with no new line.
  `covers` releases a hold on a verified line naming its alarm, or on the
  ledger's chain record naming its exact slot. `AlarmRecoveries` is
  removed, and with it `recovered.alarm` and `RecoveryMark.SlotAlarm`.
  The size: one line per decision by alarm on the seat, never pruned.
- **Why not the router's chain.** The router's `decisions.jsonl`
  (`routing.DecisionPath`) is sealed and chained, but `verifyDecisions`
  (`routing/chain.go:154`) sends any line with a `kind` to
  `tamperRecord.verify` (`routing/tamper.go:62`), which admits its four
  tamper kinds only, and its tag is over the re-encoded `tamperRecord`
  struct, not the raw line: a new kind fails on 3.1.2 (`5bd394e6`) and on
  rc.1's base (`fcc0beb7`) after a rollback, as a chain problem
  (`history_tampered`), and reusing a tamper kind would make the router
  act on it (`applyTamper`). And one writer keeps that chain's head in
  memory (`r.decHead`, `routing/durable.go:215`, `routing/tamper.go:97`):
  a second writer would make the router's next line a duplicate seq.
  `internal/routing` is outside #904's declared set as well.
- **No reader of `commits/` takes the file for a ledger or a seat.** The
  choice: the file stays at `commits/<handle>.alarms.jsonl`, beside the
  ledger it belongs to (no rename, no `alarms/` directory), since every
  reader of the directory, in each build, matches exactly or decodes:
  - this branch (`97a05cc4`): `internal/status/collect.go:492`
    (`pushURLMissing` lists only names ending exactly in `.json`; the
    file ends `.jsonl`); `internal/uninstall/plan.go:225` (`commits/` is
    one sealed op, removed whole with `--purge`, so the file goes with
    its ledger) and `:335` (`commits()` plans a worktree only from a file
    that decodes as a ledger naming one; the file's body is followed by a
    tab and a hex string, which does not decode, and names no worktree);
    seatcommit builds every `commits/` path from a handle
    (`ledger.go:342` the ledger, `:557` and `:576` the lock, `:529` the
    write's directory; `alarms.go:49` the hold release file) and never
    lists the directory;
  - 3.1.2 (`5bd394e6`): `internal/status/collect.go:492` (the same exact
    suffix), `internal/uninstall/plan.go:222` and `:330-344` (the same
    sealed op and decode);
  - rc.1's base (`fcc0beb7`): `internal/status/collect.go:492`,
    `internal/uninstall/plan.go:222` and `:330-344` (the same).
  No build derives a seat from a `commits/` file name, and a handle
  cannot hold a dot, so `<h>.alarms.jsonl` is never another seat's
  `<x>.json`; the seat's `<handle>.lock` already sits there, read by none.
- **Condition 3 and condition 4 are superseded** by this shape (the
  sections "The alarm id" and "The reviewer on Codex's pass on 254f2c2c"
  are marked), as is `AlarmRecoveries`.

The rows (each with its revert run below): the seal
(`TestTheHoldReleaseFileReleasesOnlyVerifiedLines`), the problem logged
once (`TestAHoldReleaseFileProblemIsLoggedOnce`), a valid line before a
torn tail (`TestAValidReleaseLineBeforeATornTailReleasesItsAlarm`), B's
decision surviving a later continuation
(`TestAnAlarmDecisionSurvivesALaterContinuation`), a ledger changed
between look and apply (`TestALedgerChangedBeforeTheAlarmDecisionIsCopiedNotRebuilt`),
the two steps through the production constructor
(`TestACopyFailedHoldIsReleasedInTwoSteps`), an older alarm's line after
B's (`TestAnOlderAlarmsRecordLandingAfterBsLeavesBReleased`, now in the
file), and the `commits/` listing
(`TestAlarmsFilesBesideTheLedgerAddNoSeatOrLedger`).

## The merge of origin/v3, and the anchored prefix (`85b5d8df`, `f3d72b7f`)

The reviewer's whole-PR read of `a817fed3` passed, to merge in rc.2 after
0e. #904 then conflicted with v3 (#903, the merge train's re-bind and
`internal/task/bind.go`; #910, the rc.1 bump and notes), and CI stopped
running on the branch. `85b5d8df` merges `origin/v3` at `3966afb7` (a
merge commit; no rebase, no amend). Two files conflicted, each by both
sides adding beside the same line, and both sides were kept:

- `internal/schedproc/decide.go`: `DecideOptions` keeps this branch's
  `Recover` and gains v3's `Rebind`; the proof's kind filter admits
  `RecoverRequestKind` and `RebindRequestKind` (the `apply` switch merged
  cleanly with both cases).
- `pi/extensions/lib/words.ts`: `ROUTINE_DECISIONS` keeps v3's
  `merge_train_rebound` and this branch's `ledger_recovered`,
  `ledger_tampers_restored` and `ledger_hold_released`.

Three files changed on both sides since the merge base `d522a066`
(`git diff --name-only`): the two above, and
`internal/cli/scheduler_run.go`, which merged cleanly (the `Recovery`
from `NewRecovery` and v3's `Rebind` decision both wired, checked by
hand). `internal/task` did not overlap (v3 changed `bind.go`, this branch
added `reset_if.go` and its test), nor did any doc. The merged packages passed before the next
commit (`internal/schedproc`, `internal/task`, `internal/cli`,
`internal/scheduler`; `pi/test/words.test.ts`).

`f3d72b7f`, the reviewer's P3: `names` matched "; alarm ID)" anywhere in
a block's detail, and `tamperDetail` writes the refusal's own error text
after that marker, so text inside an error could name another alarm.
`names` now reads the alarm from the detail's fixed prefix only
(`detailAlarm`: "seat H: quarantined at PATH (seq N; alarm ID): " or
"seat H: the refused document could not be quarantined (seq N; alarm
ID): ", the path matched lazily so the prefix's own marker, which comes
before the error text, is the one read). Row:
`TestAnErrorTextNamingAnotherAlarmIsNotThatAlarmsBlock` (a block
re-raised by alarm B whose refusal text contains "; alarm A)" is left by
A's release; `detailAlarm` reads B from the prefix and nothing from a
detail that does not start with it).

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
| P2 retry | A recovery decided again for the same slot is `AlreadyRecovered` with the first result's seqs, slot and body digest, the ledger byte-identical; a slot that does not exist, on the recovered ledger, is `recover_not_tampered` (an existing slot the chain is at or past would take the verified mode instead, `recordVerified`; an earlier version of this row said every other slot is refused). | `internal/seatcommit` `TestRecoveryDecidedAgainIsAnsweredFromTheLedger` |
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
| P2-2 the masked key | Three ticks: a failure naming `amq-squad-shadow-4127`, the same naming `amq-squad-shadow-90355`, then one that says `permission denied`: two `seat_prepare_failed` lines; `reasonIdentity` equates the first pair and keeps the words apart. | `internal/scheduler` `TestReasonsDifferingOnlyInATempNameLogOnce` |
| P2-2 temp names | A prepare failure naming a fresh `amq-squad-shadow-<n>` and byte count each tick for 30 ticks: one `seat_prepare_failed` (with the full text) and one `seat_prepare_suppressed`; a failure that says something else (`permission denied`) is logged again. | `internal/scheduler` `TestAFailureNamingAFreshTempFileIsLoggedOnce` |
| P2-3 the conditional reset | Between the release's read and its reset the task is reset and blocked again (`lead_hold`): the new block stays with its reason, no `task_reset`, no dispatch, no failure line, the hold released. The store: `ResetIfBlocked` refuses another reason, a pending task (`ErrBlockChanged`) and an unknown id (`ErrNotFound`), and resets its own reason with the block's fields cleared. | `internal/scheduler` `TestARecoveryNeverClearsABlockRaisedAfterItsRead`; `internal/task` `TestResetIfBlockedResetsOnlyItsReason` |
| P3 the retry's link | The transient alarm at seq 4, a second commit step (live link 7), the verified decision writes link 8; its retry is `AlreadyRecovered`, `Verified`, link 8; the record's `Link` is 8. | `internal/seatcommit` `TestAVerifiedRecoveryRetriedReportsItsLink` |
| replay | The lowered-seq replay: a hold on slot seq1; the ledger answers an older record (seq 3, its slot): 20 ticks and 5 more after a restart release nothing; the record of slot seq1 releases the hold and resets the task. | `internal/scheduler` `TestALoweredSeqReplayDoesNotReleaseTheHold` |
| another slot | A hold on slot seq150; the ledger records a recovery from slot seq150.2 (the same seq): 10 ticks release nothing, the hold stands, the task stays blocked. | `internal/scheduler` `TestARecoveryForAnotherSlotDoesNotReleaseTheHold` |
| no slot | A hold with no slot (seq 150): its block names its alarm (`; alarm ID)`); a record naming a slot at the hold's seq, and a decision by alarm for another alarm, release nothing over 10 ticks; the state file keeps the alarm and a restarted scheduler publishes it (`HeldAlarm`); the decision by its own alarm releases the hold, resets the task and dispatches it, and the seat is no longer published as held. | `internal/scheduler` `TestAHoldWithNoSlotIsReleasedOnlyByItsAlarm` |
| lower-seq slot | A hold on slot seq150; the ledger records the recovery of a lower-seq slot (seq1): 10 ticks release nothing; the decision by the hold's alarm releases it, and the release line names the alarm. | `internal/scheduler` `TestALowerSeqSlotLeavesTheHoldToItsAlarm` |
| late decision | A newer hold (alarm B); the ledger records a decision by an earlier alarm A (landing late): 20 ticks release nothing; B's own decision releases it. | `internal/scheduler` `TestALateDecisionForAnEarlierAlarmDoesNotReleaseANewerHold` |
| the anchored prefix | Between A's release read and its reset the task is blocked by alarm B, whose refusal text contains "; alarm A)": no `task_reset`, B's block stays with its detail, A's hold is released; `detailAlarm` reads B from B's prefix and "" from a detail without the prefix. | `internal/scheduler` `TestAnErrorTextNamingAnotherAlarmIsNotThatAlarmsBlock` |
| the append's descriptor | Unix: the hold release file replaced by a symlink before the append: refused naming the link, its target unchanged; replaced by a FIFO: refused at once (within 5 s, never blocked); a regular file again: the append works and its line verifies. | `internal/seatcommit` `TestTheAppendWritesOnlyToTheSeatsOwnRegularFile` |
| two steps | Through `NewRecovery` (Git omitted, the production constructor): the quarantine's mode 0755 refuses the prepare's copy (a hold with no slot, `seq unknown`); the decision by alarm is refused naming the copy's failure; the mode repaired, it makes only the copy and is refused "seat H's ledger does not verify; it was copied to slot S; recover it with `amq-squad scheduler recover --seat H --seq 1 --reason ...`, which shows what it carries; then, if the hold remains, send the alarm decision again", the ledger unchanged, the slot holding the live bytes; step one, the slot recovery, carries nothing and releases nothing (the hold has no slot); step two, the decision by alarm, releases the seat. | `internal/schedproc` `TestACopyFailedHoldIsReleasedInTwoSteps` |
| look, then change | The lead's look sees a ledger that verifies; it changes before the apply; the decision by alarm copies what it found and refuses with the slot and the command; the ledger is left as found, no line is written, no tick releases. | `internal/schedproc` `TestALedgerChangedBeforeTheAlarmDecisionIsCopiedNotRebuilt` |
| the seal | The hold release file: a line from `appendAlarm` releases its alarm; a forged seal, a one-byte edit, a line under another root's seed and a line sealed for another seat release nothing, each one problem by its line number; a body with an unknown field verifies; a valid line before a torn tail releases, the tail one problem; the next append terminates the tail and its line verifies; the file is mode 0600. | `internal/seatcommit` `TestTheHoldReleaseFileReleasesOnlyVerifiedLines` |
| logged once | A problem of the hold release file is one `hold_release_line_skipped` over 10 ticks; a new problem beside a verified line is one more, and the line releases. | `internal/scheduler` `TestAHoldReleaseFileProblemIsLoggedOnce` |
| torn tail | Real files: a valid line, then a torn tail; the next ticks release the hold and log the torn tail once. | `internal/schedproc` `TestAValidReleaseLineBeforeATornTailReleasesItsAlarm` |
| after a continuation | Real files: B's decision by alarm is a line; before any tick the ledger is refused and recovered from its slot (a new body); B's line is still read and B is released. | `internal/schedproc` `TestAnAlarmDecisionSurvivesALaterContinuation` |
| commits/ listing | Uninstall's dry run with `seat-a.alarms.jsonl` and `zz-ghost.alarms.jsonl` in `commits/` is the dry run without them (no seat, worktree or ledger added, no `zz-ghost`); `--write` keeps the file, `--purge` removes it with `commits/`. | `internal/uninstall` `TestAlarmsFilesBesideTheLedgerAddNoSeatOrLedger` |
| older record after B's | Real files: B holds the seat and B's decision by alarm is a line of the hold release file; an older alarm A's line lands after it (no guard); the file keeps both (`RecoveriesOf` answers `[B A]`); the next ticks release B and five more release nothing, B not held again. | `internal/schedproc` `TestAnOlderAlarmsRecordLandingAfterBsLeavesBReleased` |
| delayed write | Real writers with a barrier: A's decision by alarm held after the socket's check; A released by its slot; B held and B's decision sealed; A resumed: `recover_alarm_not_held`, nothing written, the record still names B; the next ticks release B. | `internal/schedproc` `TestADelayedAlarmDecisionNeverSealsOverANewerRecovery` |
| released before the seal | The decision by alarm for A passes the socket's check (`HeldAlarm` is A); before its record is sealed the lead cancels A's task, the ledger records A's slot and the next prepare is refused again: one tick releases A and raises B (a new id), the next task blocked; the record then sealed names A: 20 ticks release nothing, B stands; B's own decision releases it. | `internal/scheduler` `TestADecisionForAReleasedAlarmDoesNotReleaseTheNextHold` |
| the decision by alarm | Real files through the apply step: a hold with no slot (alarm `3fa9c1d2`), its task blocked with its detail; a wrong id is `recover_alarm_not_held` naming the held alarm; the document put back, 5 ticks release nothing; the decision is OK and `Verified`, the hold release file has one line naming the alarm and the lead; the next ticks release, reset and dispatch; the decision line says "released alarm 3fa9c1d2 of seat ..."; the same id once the hold is gone is `recover_alarm_not_held`. | `internal/schedproc` `TestTheAlarmVerbReleasesAHoldWithNoSlot` |
| the lowered head | Real files: the chain at link N; slot X at seq N; the head lowered to seq 1, slot Y; X refused while the live document is Y's; Y's recovery at link 2; X refused again, "behind the slot's seq N"; a decision by alarm with a carry is refused; the decision by alarm appends a line to the hold release file, the ledger unchanged, and sent again it is answered from the file with no new line. | `internal/seatcommit` `TestADecisionByAlarmAnswersAHoldNoSlotCan` |
| the verb's flags | `scheduler recover --alarm` refuses `--seq`, `--slot`, `--carry`, an id that is not eight hex characters and a missing `--reason` before any send; a send carries the seat, the alarm and the reason only, and the output names the alarm and the hold release file. | `internal/cli` `TestSchedulerRecoverSendsTheDecisionOverTheSocket` |
| the words | The prepare's detail with `(seq 150; alarm 3fa9c1d2)`: the stop line "seat dev could not be prepared for t7: seat dev's commit record failed its check (alarm 3fa9c1d2); after investigating, the lead runs amq-squad scheduler recover --seat dev --alarm 3fa9c1d2 --reason \"REASON\"", the same words on the block's line and on the screen's seats and tasks blocks; a hold with no slot reads the same; the write-time refusal names no alarm and keeps its words. | `pi/test/recovery.test.ts` (the third test) |
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

### P2-1 the baseline check (removed in `1cd21fa1`)

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

### P2-1 the alarm's baseline (removed in `1cd21fa1`)

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

### P2-1 the quarantine's prior record (removed in `1cd21fa1`)

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

### the baseline on the hold's own slot (removed in `1cd21fa1`)

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

### the masked key without the reason (the reviewer on `ede2226c`)

```
go test -count=1 -run ^TestReasonsDifferingOnlyInATempNameLogOnce$ ./internal/scheduler/
go test -count=1 -run ^TestAFailureNamingAFreshTempFileIsLoggedOnce$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:124` (on `e1fc443c`), a key that
drops the reason entirely:
```
		t.UpdatedAt.UTC().Format(time.RFC3339Nano), reasonIdentity(err.Error()), log)
```
to:
```
		t.UpdatedAt.UTC().Format(time.RFC3339Nano), "", log)
```
First failing lines (exit 1, each):
```
tamper_test.go:836: 1 seat_prepare_failed lines, want 2 (the temp name alone is one failure, the words another): [{Kind:seat_up TaskID: Seat:seat-a Pool:claude:sonnet:medium Reason: Stage: ReasonCode:} ...]
tamper_test.go:729: a changed reason was not logged: []
```
`--- FAIL: TestReasonsDifferingOnlyInATempNameLogOnce (0.05s)`,
`--- FAIL: TestAFailureNamingAFreshTempFileIsLoggedOnce (0.05s)`;
restored and both re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.412s`.

### The re-pass's reverts, run again on `ad3330e0`

The revert runs of "Codex's re-pass on b250b951" above, run again on
`ad3330e0` by the same script (the code they revert is unchanged or moved;
the reset's revert is now the seam's new form). Each exit 1 on its row,
restored exit 0:

| Revert | On `ad3330e0` | First failing line | Restored |
|---|---|---|---|
| the alarm's baseline dropped (removed in `1cd21fa1`) | `internal/schedproc/prepare.go:77` | `recover_test.go:442: the alarm: ...` | `ok` 0.687s |
| the quarantine's prior record nil (removed in `1cd21fa1`) | `internal/seatcommit/quarantine.go:112` | `recover_test.go:673: the quarantine's prior record: seq 1 <nil>, ...` | `ok` 1.321s |
| the raw text as the identity | `internal/scheduler/tamper.go:124` | `tamper_test.go:720: over 30 ticks of one failure with a fresh temp name: ...` | `ok` 0.388s |
| an unconditional `Reset` | `internal/scheduler/tamper.go:209` | `tamper_test.go:764: the release touched the new block: ...` | `ok` 0.448s |
| the store's reason not checked | `internal/task/reset_if.go:23` | `reset_if_test.go:22: another block's reset: <nil>` | `ok` 0.633s |
| the retry's link rebuilt | `internal/seatcommit/recover.go:195` | `recover_test.go:655: the retry: ... NewSeq:5 ...` | `ok` 1.266s |
| the retry's mode dropped | `internal/seatcommit/recover.go:199` | `recover_test.go:655: the retry: ... Verified:false ... NewSeq:8 ...` | `ok` 1.251s |
| the link not written | `internal/seatcommit/recover.go:326` | `recover_test.go:655: the retry: ... NewSeq:5 ...` | `ok` 1.460s |

## Revert runs, the alarm id (`28051cd6`)

Each on `28051cd6`, one at a time, by the same script (the expression
asserted to occur once, each named row run, the file restored from its
copy and the rows run again). A decision list in a failing line is cut
after its first entry; a temp root is `<tmp>`.

### the release by alarm

```
go test -count=1 -run ^TestAHoldWithNoSlotIsReleasedOnlyByItsAlarm$ ./internal/scheduler/
go test -count=1 -run ^TestALowerSeqSlotLeavesTheHoldToItsAlarm$ ./internal/scheduler/
go test -count=1 -run ^TestTheAlarmVerbReleasesAHoldWithNoSlot$ ./internal/schedproc/
```
Reverted `internal/scheduler/tamper.go:344`
```
	if rec.Alarm != "" {
		return rec.Alarm == tm.Alarm
	}
```
to:
```
	if false && rec.Alarm != "" {
		return rec.Alarm == tm.Alarm
	}
```
First failing line (exit 1, each):
```
tamper_test.go:653: the decision by its alarm did not release the hold: <nil> [{Kind:seat_up TaskID: Seat:seat-a Pool:claude:sonnet:medium Reason: Stage: ReasonCode:}]
tamper_test.go:686: X's alarm did not release its hold: []
recover_test.go:449: the decision by alarm did not release the hold: []
```
restored and re-run (exit 0): `ok` for each (1.024s, 0.653s, 4.280s).

### only the hold's own alarm

```
go test -count=1 -run ^TestALateDecisionForAnEarlierAlarmDoesNotReleaseANewerHold$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:345`
```
		return rec.Alarm == tm.Alarm
```
to:
```
		return true
```
First failing line (exit 1):
```
tamper_test.go:716: an earlier alarm's decision released the newer hold: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
restored and re-run (exit 0): `ok` 0.851s.

### the held-alarm check at the socket

```
go test -count=1 -run ^TestTheAlarmVerbReleasesAHoldWithNoSlot$ ./internal/schedproc/
```
Reverted `internal/schedproc/recover.go:139`
```
			if held != req.Alarm {
```
to:
```
			if false && held != req.Alarm {
```
First failing line (exit 1):
```
recover_test.go:431: a wrong alarm id: {OK:false Code:recover_alarm_unverified Detail:the commits ledger of seat seat-t1 does not verify now (...) ...}
```
restored and re-run (exit 0): `ok` 3.624s.

### the ledger verifies now

```
go test -count=1 -run ^TestADecisionByAlarmAnswersAHoldNoSlotCan$ ./internal/seatcommit/
go test -count=1 -run ^TestTheAlarmVerbReleasesAHoldWithNoSlot$ ./internal/schedproc/
```
Reverted `internal/seatcommit/recover.go:373`
```
	if rerr != nil {
		return out, refuseRecover(RecoverAlarmUnverified, ...
```
to:
```
	if false && rerr != nil {
		return out, refuseRecover(RecoverAlarmUnverified, ...
```
First failing line (exit 1, each):
```
recover_test.go:727: not a recovery refusal: <nil>
recover_test.go:425: a decision by alarm on a tampered ledger: {OK:true Code: Detail:seat seat-t1: alarm 3fa9c1d2 released by decision, sealed at link 2; ...}
```
restored and re-run (exit 0): `ok` for each (4.295s, 1.850s).

### the alarm kept across a restart

```
go test -count=1 -run ^TestAHoldWithNoSlotIsReleasedOnlyByItsAlarm$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:370`
```
		if tm.Alarm == "" {
```
to:
```
		if true || tm.Alarm == "" {
```
First failing line (exit 1):
```
tamper_test.go:645: the restarted hold's alarm: "458c8969" true, want "b74d478d"
```
restored and re-run (exit 0): `ok` 0.343s.

### the reset matches the hold's alarm

```
go test -count=1 -run ^TestARecoveryKeepsATamperBlockOfAnotherSlot$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:357`
```
		strings.Contains(t.BlockDetail, "; alarm "+tm.Alarm+")")
```
to:
```
		true
```
First failing line (exit 1):
```
tamper_test.go:829: the release touched the other slot's block: [{Kind:task_reset TaskID:t1 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
restored and re-run (exit 0): `ok` 0.399s.

### the words read the alarm

```
cd pi && node --test test/recovery.test.ts
```
Reverted `pi/extensions/lib/words.ts:363`
```
	return m ? { seat: m[1], alarm: m[2] } : undefined;
```
to:
```
	return undefined;
```
First failing line (exit 1):
```
not ok 3 - the alarm: the stop line and the block name the hold's alarm and the decision by alarm (Expected values to be strictly deep-equal: + undefined - { alarm: '3fa9c1d2', seat: 'dev' })
```
restored and re-run (exit 0): `ok 1`, `ok 2`, `ok 3`.

### the stop line reads the decision's detail

```
cd pi && node --test test/recovery.test.ts
```
Reverted `pi/extensions/lib/words.ts:579`
```
	const [why, then] = f(code === "ledger_tampered" ? { block_detail: d.reason } : {});
```
to:
```
	const [why, then] = f({});
```
First failing line (exit 1):
```
not ok 3 - ... (Expected values to be strictly equal: + "seat dev could not be prepared for t7: the seat's commit record failed its check; the lead investigates before the seat works again" - "seat dev could not be prepared for t7: seat dev's commit record failed its check (alarm 3fa9c1d2); after investigating, the lead runs amq-squad scheduler recover --seat dev --alarm 3fa9c1d2")
```
restored and re-run (exit 0): `ok 1`, `ok 2`, `ok 3`.

### the verb's flags

```
go test -count=1 -run ^TestSchedulerRecoverSendsTheDecisionOverTheSocket$ ./internal/cli/
```
Reverted `internal/cli/scheduler_recover.go:44`
```
	case byAlarm && (flagWasSet(f.fs, "seq") || flagWasSet(f.fs, "slot") || len(carry) > 0):
```
to:
```
	case false && byAlarm && (flagWasSet(f.fs, "seq") || flagWasSet(f.fs, "slot") || len(carry) > 0):
```
First failing line (exit 1):
```
scheduler_recover_test.go:55: alarm and seq: scheduler recover: no running scheduler for <tmp> (the recovery is the scheduler's to write): ...
```
restored and re-run (exit 0): `ok` 0.424s.

### a record for a released alarm (`c83e6ef3`)

```
go test -count=1 -run ^TestADecisionForAReleasedAlarmDoesNotReleaseTheNextHold$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:345` (a decision by alarm matched
on the seat instead of the alarm):
```
		return rec.Alarm == tm.Alarm
```
to:
```
		return true // any decision by alarm for this seat
```
First failing line (exit 1):
```
tamper_test.go:908: A's late record released B: [{Kind:task_reset TaskID:t2 Seat:seat-a Pool: Reason:the commits ledger of seat seat-a was recovered by the lead; the task returns to pending Stage: ReasonCode:} ...]
```
`--- FAIL: TestADecisionForAReleasedAlarmDoesNotReleaseTheNextHold (0.16s); FAIL`
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.419s`.

### Codex on 254f2c2c (`40254653`)

Each on `40254653`, by the same script.

```
go test -count=1 -run ^TestTheAlarmVerbRecoversALedgerWhoseCopyFailed$ ./internal/schedproc/
```
Reverted `internal/seatcommit/recover.go:390` (no copy on an unverified
ledger):
```
	if errors.Is(rerr, sealchain.ErrTampered) {
```
to:
```
	if false && errors.Is(rerr, sealchain.ErrTampered) {
```
First failing line (exit 1):
```
recover_test.go:497: the decision while the copy fails: {OK:false Code:recover_alarm_unverified Detail:the commits ledger of seat seat-t1 cannot be read as a document (commit ledger seat-t1: ledger_tampered: the tag of commits.seat-t1 link 1 fails); the decision by alarm needs one to verify or to quarantine Data:null}
```
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.726s`.

```
go test -count=1 -run ^TestADelayedAlarmDecisionNeverSealsOverANewerRecovery$ ./internal/schedproc/
```
Reverted `internal/schedproc/recover.go:156` (the check only outside the
lock):
```
				if held, ok := s.HeldAlarm(seat); !ok || held != alarm {
```
to:
```
				if held, ok := s.HeldAlarm(seat); false && (!ok || held != alarm) {
```
First failing line (exit 1):
```
recover_test.go:578: A after B's recovery: {OK:true Code: Detail:seat seat-t1: alarm e90fbe41 released by decision, sealed at link 4; the scheduler releases the seat at its next tick ...}
```
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.724s`.

```
cd pi && node --test test/recovery.test.ts
```
Reverted `pi/extensions/lib/words.ts:174` (the command without
`--reason`):
```
 --alarm ${a.alarm} --reason "REASON"`
```
to:
```
 --alarm ${a.alarm}`
```
First failing line (exit 1):
```
not ok 3 - the alarm: the stop line and the block name the hold's alarm and the decision by alarm (+ "... recover --seat dev --alarm 3fa9c1d2" - "... recover --seat dev --alarm 3fa9c1d2 --reason \"REASON\"")
```
restored and re-run (exit 0): `ok 1`, `ok 2`, `ok 3`, `# pass 3`.

### The reviewer on Codex's pass on 254f2c2c (`37379e5b`)

Each on `37379e5b`, by the same script.

```
go test -count=1 -run ^TestAnOlderAlarmsRecordLandingAfterBsLeavesBReleased$ ./internal/schedproc/
```
Reverted `internal/seatcommit/recover.go:427` (back to the single field:
the alarm's record replaces the list):
```
	l.AlarmRecoveries = append(append([]LedgerRecovery(nil), live.AlarmRecoveries...), LedgerRecovery{...})
```
to:
```
	l.AlarmRecoveries = append([]LedgerRecovery(nil), LedgerRecovery{...})
```
First failing line (exit 1):
```
recover_test.go:617: B was not released after A's record landed: [{Kind:seat_requested TaskID:t1 ...} ...]
```
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.642s`.

Run again on `37379e5b`, each exit 1 on its row, restored exit 0: the
guard only outside the lock (`internal/schedproc/recover.go:156`,
`recover_test.go:579: A after B's recovery: {OK:true ...}`; with the
records per alarm A's late seal no longer strands B, but the row refuses
it at write time), no copy on an unverified ledger
(`internal/seatcommit/recover.go:395`, `recover_test.go:498: the decision
while the copy fails: {OK:false Code:recover_alarm_unverified Detail:...
cannot be read as a document ...}`), and the `recover_alarm_unverified`
entry dropped from `RECOVER_REFUSAL_WORDS` (`pi/extensions/lib/words.ts:721`,
`not ok 4 - ... error: 'recover_alarm_unverified has no words'`; the table
and its row were dropped in `cb4f7d13`).

### The hold release file (`eff2fea1`, `97a05cc4`)

Each by the same script, on the commit named, exit 1 on its row and
restored exit 0:

| Revert | Where | First failing line |
|---|---|---|
| the decision rebuilds from its copy (`eff2fea1`) | `internal/seatcommit/recover.go:405` | `recover_test.go:574: the decision on a changed ledger: {OK:true ... released by decision ...}` |
| no Git default (`eff2fea1`) | `internal/seatcommit/recover.go:165` | `recover_test.go:528: step one, the slot recovery: {OK:false Code:recover_base_unobserved Detail:... exec: no command ...}` |
| the seal not checked (`97a05cc4`) | `internal/seatcommit/alarms.go:122` | `alarms_test.go:101: bbbbbbb2 releases: ...` |
| the seal checked over a re-encoding (`97a05cc4`) | `internal/seatcommit/alarms.go:122` | `alarms_test.go:96: eeeeeee5 does not release: ...` |
| a torn tail fails the whole file (`97a05cc4`) | `internal/seatcommit/alarms.go:108` | `recover_test.go:726: the valid line did not release its alarm: ...` |
| the append replaces the file (`97a05cc4`) | `internal/seatcommit/alarms.go:163` | `recover_test.go:675: B was not released after A's record landed: ...` |
| the problem logged every tick (`97a05cc4`) | `internal/scheduler/tamper.go:276` | `tamper_test.go:952: over 10 ticks of one problem: ...` |

The commands: `go test -count=1 -run ^<row>$ ./internal/<package>/` for
the row named in the table of rows. (The first run of "the seal not
checked" left the key unused and did not compile; it was run again with
the key and the tag kept, and failed on its row as above.)

### Codex on 50986a71 (`a57e9627`)

```
go test -count=1 -run ^TestTheAppendWritesOnlyToTheSeatsOwnRegularFile$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/alarms.go:158` (a plain OpenFile):
```
	f, err := openAppendNoFollow(path)
```
to:
```
	f, err := os.OpenFile(path, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0o600)
```
First failing line (exit 1):
```
alarms_unix_test.go:42: the append through a symlink: <nil>
```
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.297s`.

### The anchored prefix (`f3d72b7f`)

```
go test -count=1 -run ^TestAnErrorTextNamingAnotherAlarmIsNotThatAlarmsBlock$ ./internal/scheduler/
```
Reverted `internal/scheduler/tamper.go:393` (the marker matched anywhere):
```
		detailAlarm(tm.Handle, t.BlockDetail) == tm.Alarm
```
to:
```
		strings.Contains(t.BlockDetail, "; alarm "+tm.Alarm+")")
```
First failing line (exit 1):
```
tamper_test.go:1005: A's release reset B's block: [{Kind:task_reset TaskID:t1 Seat:seat-a ...} ...]
```
restored and re-run (exit 0): `ok  	github.com/omriariav/amq-squad/v3/internal/scheduler	0.342s`.

## On the 0e layout

Task t127 ports reviewed head `921c54ae` (merge base `3966afb7`) onto the
post-0e base. The carry used a squash merge without a commit. Its eight
conflict hunks in six files were resolved as follows:

- `internal/cli/cli.go` (one hunk): kept the 0e `sealchain` version seam and the recovery
  command's `seatcommit` version seam.
- `internal/schedproc/prepare.go` (one hunk): kept strict compatibility classification,
  then wrapped only tamper in `LedgerTamperedError`; no double wrapping.
- `internal/scheduler/scheduler.go` (one hunk): kept `preparedIdleSeat`, routed other
  prepare failures through `prepareFailed`, called `preparePassed`, and made
  tamper and compatibility share the one `seatExcluded` dispatch predicate.
- `pi/extensions/lib/words.ts` (two hunks): kept 0e newer-writer/schema-mismatch words and
  the recovery alarm, suppression, and release words.
- `pi/test/screen.test.ts` (one hunk) and `pi/test/words.test.ts` (two hunks):
  kept the recovery-aware tamper wording while preserving the 0e compatibility
  expectations.

The carried non-merge code patch IDs, in commit order, are:
`33987fed:6fe254c9`, `5ab002d3:ff6c4dd3`, `417a977e:f59324ee`,
`7ca9da50:f5d340f7`, `88df5bad:952d788b`, `8017ffea:a444672d`,
`ccec9400:3bf70e97`, `23f315e3:bee534c1`, `01088a5c:6a468678`,
`c27ad8fc:45b94547`, `9de43a6c:4ba9f328`, `8eeeaf24:619cea44`,
`ad3330e0:b9e8e008`, `bd683f28:d7390ad6`, `e1fc443c:12640a9b`,
`1cd21fa1:c32e1481`, `28051cd6:d62bc10b`, `c83e6ef3:6cd9d61f`,
`40254653:b9a43e32`, `37379e5b:afaff2ca`, `cb4f7d13:59d4da4f`,
`eff2fea1:624dde1b`, `97a05cc4:4abbbc8a`, `a57e9627:529501c0`, and
`f3d72b7f:725a83bb`.

The per-file stable patch IDs below compare #904 (`3966afb7..921c54ae`)
with this staged port (`97282d3b..index`). “Same” means the patch ID is
byte-for-byte-equivalent after patch-id normalization; every “changed” row
names why.

| File | #904 | This port | Result |
|---|---|---|---|
| `docs/v3-spike/320-0c-quarantine/B-recovery.md` | `e5ea95e2` | changed | port, conflict, schema, gate, and round-2 evidence; its own patch ID is intentionally not embedded recursively |
| `internal/cli/cli.go` | `196df36e` | `8306cd93` | changed: kept 0e's version seam |
| `internal/cli/scheduler_recover.go` | `085753aa` | `bdb4a4de` | changed: raw-layout result and `seq_from` words |
| `internal/cli/scheduler_recover_test.go` | `8e51e75c` | `8e51e75c` | same |
| `internal/cli/scheduler_run.go` | `69073b2c` | `69073b2c` | same |
| `internal/schedproc/decide.go` | `169b12ef` | `169b12ef` | same |
| `internal/schedproc/prepare.go` | `438484ef` | `bf833c6c` | changed: 0e compatibility classification precedes tamper wrapping |
| `internal/schedproc/process.go` | `97eb0084` | `97eb0084` | same |
| `internal/schedproc/recover.go` | `52f1b98b` | `c3c5ba58` | changed: decision line carries `seq_from` |
| `internal/schedproc/recover_test.go` | `e8d4e0bc` | `e8d4e0bc` | same |
| `internal/schedproc/slots.go` | `64efb560` | `64efb560` | same |
| `internal/scheduler/scheduler.go` | `13225560` | `a29b0abf` | changed: 0e prepare/exclusion flow retained |
| `internal/scheduler/tamper.go` | `167907c8` | `167907c8` | same |
| `internal/scheduler/tamper_test.go` | `46986e2a` | `b5f3a9ce` | changed: round-2 scheduler recovery assertions |
| `internal/seatcommit/alarms.go` | `8b94c8f4` | `2dc5562e` | changed: appends through `flock.AppendJSONLLine` |
| `internal/seatcommit/alarms_test.go` | `e1802a58` | `e1802a58` | same |
| `internal/seatcommit/alarms_unix_test.go` | `d1d023fa` | `d1d023fa` | same |
| `internal/seatcommit/baseshadow.go` | `9d4816c4` | `9d4816c4` | same |
| `internal/seatcommit/commit.go` | `6cfc59af` | `4ce2a384` | changed: 0e raw writer and evidence counter retained |
| `internal/seatcommit/ledger.go` | `8faac89c` | `dad676d0` | changed: raw layout, migration completion, `seq_from`, and R15 seam |
| `internal/seatcommit/quarantine.go` | `f09d26b0` | `f09d26b0` | same |
| `internal/seatcommit/quarantine_other.go` | `ec9a6c59` | `ec9a6c59` | same |
| `internal/seatcommit/quarantine_unix.go` | `d32c2d2b` | `d32c2d2b` | same |
| `internal/seatcommit/recover.go` | `825ed6d0` | `54d55d0e` | changed: raw-layout recovery plus D1-D5 and R15 |
| `internal/seatcommit/recover_other_test.go` | `3ce7f585` | `3ce7f585` | same |
| `internal/seatcommit/recover_test.go` | `e641128f` | `60b57539` | changed: raw-layout assertions and round-2 rows |
| `internal/seatcommit/recover_unix_test.go` | `b80a2308` | `b80a2308` | same |
| `internal/task/reset_if.go` | `e6226137` | `e6226137` | same |
| `internal/task/reset_if_test.go` | `cf032967` | `cf032967` | same |
| `internal/uninstall/alarms_test.go` | `ae1e1c57` | `ae1e1c57` | same |
| `internal/uninstall/execute.go` | `965fb5f7` | `965fb5f7` | same |
| `internal/uninstall/plan.go` | `c4baf407` | `c4baf407` | same |
| `internal/uninstall/quarantine_test.go` | `7dc70593` | `7dc70593` | same |
| `pi/extensions/lib/words.ts` | `11016a8a` | `030d8f5c` | changed: 0e compatibility words retained |
| `pi/test/recovery.test.ts` | `30942fcd` | `30942fcd` | same |
| `pi/test/screen.test.ts` | `d7ad44e9` | `5f1710ac` | changed: 0e screen expectations retained |
| `pi/test/words.test.ts` | `3729e099` | `6306c3ba` | changed: 0e compatibility expectations retained |

The 0e recovery rows are below. These round-one reversions were not rerun in
the seat during t127; that omission is corrected only for rows explicitly
listed with output in Round 2, and no unrun revert is presented as a result.

| Row | Test |
|---|---|
| R1 | `TestRecoveryContinuesTheChainAfterTheQuarantinedSeq` |
| R2 | `TestRecoveryMigratesALegacyLedgerThroughTheRawWriter` |
| R3 | `TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm` |
| R4 | `TestRecoveryRefusesASlotBodyThatIsNeitherLayout`; `TestRecoveryRefusesANeitherLayoutHeadWithNoBody` |
| R6 | `TestOneTickSeparatesTamperedIncompatibleAndHealthySeats` |
| R7 | `TestRecoveredLedgerRemainsStrictSchemaOne` |
| R8 | `TestRawHeadSwapQuarantinesTheHeadThatWasChecked` |
| R9/P5 | `TestLedgerProductionWritersUseOnlyTheRawPath`; `TestRawWriterGuardListsEveryCallerAndRejectsAThirdRecoveryCaller` |
| R11 | `TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence` |
| R11 max | `TestRecoveryTreatsTheMaximumClaimedSequenceAsUnknown` |
| R12 | `TestInterruptedMigrationRecoveryDedupesTheOriginalDecision` |
| R13 | `TestRetryCompletesARecoveredRawBodyAboveALegacyHead` |
| R14 | `TestRecoveryRefusesWhenEvidenceRefsCannotBeObserved` |
| P1/R5 | `TestRecoveryIsTamperOnlyForCompatibilityRefusals` |
| P3 | `TestRecoveryMigratesALegacyBodyWithNoUsableHead` |
| P4 | `TestPrepareErrorWrapsOnlyTamperingAsALedgerAlarm` |

The schema stays 1: `Ledger.Recovered` and `LedgerCommit.Note` are known
schema-1 fields. Any other authenticated unknown field under seal schema 1 is
`schema_mismatch`; only a higher authenticated seal schema is `newer_writer`.
Recovery is tamper-only for both classifications.

The lock order is the seat ledger lock first, then the scheduler decision
JSONL lock when a legacy-to-raw migration record is appended. No inverse path
was found. Outside `internal/seatcommit`, uninstall's generic extension walk
builds paths that can name the quarantine directory. CLI, schedproc, and
scheduler only describe or pass through the slot identity.

Focused runs on this port passed:

```text
go test -timeout 120s ./internal/seatcommit -run 'Test(Recovery|Recover|Older|Recovered|Alarm|VersionSkew|Legacy|ADecision|Interrupted|CommitCompatibility)' -count=1
ok github.com/omriariav/amq-squad/v3/internal/seatcommit
go test -timeout 120s ./internal/scheduler -run 'Test(SeatExclusion|Compatibility|Tamper|Ledger)' -count=1
ok github.com/omriariav/amq-squad/v3/internal/scheduler
```

`make ci` completed `go vet ./...`, then the whole-module test reached the
sandbox's denied TCP/Unix listener operations (`bind: operation not
permitted`) in `internal/buzz` and the CLI decision/start sockets. The CLI
context rows also saw this live seat's role/rules environment and reported
its recorded-digest warning. The touched focused suites above are green; the
lead must rerun the locked full Go, pi, and `make ci` gates in the host
environment.

## Round 2

Task t130 restores the exact staged tree of attempts 1 and 2 onto the post-#932
base `97282d3b`. The saved patch
`/Users/omri/.claude/jobs/6dd424c9/tmp/t129-staged-attempt1.patch` was 511228
bytes and had SHA-256
`497fcbb66cbd6804d9a3f34fbc72c56fd44729b777adb90667091c0af38d305e`.
After `git apply --index --binary`, `git diff --cached --binary` had that same
SHA-256 and `git write-tree` produced
`fe19decce3c8aafeae0f7bb90825aabac3ecc464`. The one blank-line-at-EOF warning
is the required #904 byte layout. That saved tree already contains the
conflict-free t127 carry. Round 2 fixes six reviewed
defects without changing the quarantine evidence:

- D1: the slot name is checked against the copied head's claimed sequence,
  even when the head tag fails. The continuation tag is the verified head tag
  when available, otherwise the body seal tag found in the slot. The new link
  is one above the highest sequence named by a verified head, claimed head, or
  body seal; `math.MaxInt` is refused. `LedgerRecovery.SeqFrom` and
  `RecoverResult.SeqFrom` record `verified_head`, `claimed_head`, or
  `body_seal`. `OldSeq` remains the sequence in the slot name.
- D2: an interrupted legacy-to-raw migration records its layout decision at
  the already-written raw successor's sequence and writes the recovery as a
  non-migration. `RecordLayoutMigration` therefore deduplicates the present
  and absent-record cases for tamper and verified recovery.
- D3: when a retry reads a recovered raw successor above a legacy head, it
  records the migration, completes that exact raw head, and returns
  `AlreadyRecovered` without writing another ledger link.
- D4: a failed `for-each-ref` now refuses `recover_base_unobserved`; zero is no
  longer substituted for an unobserved evidence counter.
- D5: a quarantine body is raw layout or legacy JSON with a usable embedded
  seal. Plain JSON is neither layout and is refused.
- D6: both compatibility classes remain `recover_incompatible`. A
  `newer_writer` refusal carries the authenticated install command;
  `schema_mismatch` carries 0e's lead-investigates-writer-bug classification,
  for which 0e defines no install command.

The recovery decision line and CLI output carry `seq_from`. `appendAlarm`
keeps its descriptor-anchored no-follow open and now delegates torn-tail
termination plus the single-line append to `flock.AppendJSONLLine`, followed by
the same `Sync`. `pi/test/recovery.test.ts` is byte-identical to #904 again,
including its two blank separator lines.

### Round 2 rows

| Row | Test and location | Revert run |
|---|---|---|
| R1 | `TestRecoveryContinuesTheChainAfterTheQuarantinedSeq`, `internal/seatcommit/recover_test.go:72`; recovered body and head both split as raw and no migration decision exists | round 3 focused suite |
| R2 | `TestRecoveryMigratesALegacyLedgerThroughTheRawWriter`, `internal/seatcommit/raw_compat_test.go:662`; retry is `AlreadyRecovered`, with identical body, head, and one migration line | round 3 focused suite |
| R3 | `TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm`, `internal/seatcommit/raw_compat_test.go:806`; five slot forms assert chosen seq, tag source, migration flag, and migration sequences | round 3 focused suite |
| R4 | `TestRecoveryRefusesASlotBodyThatIsNeitherLayout`, `internal/seatcommit/raw_compat_test.go:535`; `TestRecoveryRefusesANeitherLayoutHeadWithNoBody`, `internal/seatcommit/raw_compat_test.go:562` | round 3 focused suite |
| R6 | `TestOneTickSeparatesTamperedIncompatibleAndHealthySeats`, `internal/scheduler/seat_exclusion_320_test.go:33` | round 3 focused suite |
| R7 | `TestRecoveredLedgerRemainsStrictSchemaOne`, `internal/seatcommit/recover_test.go:520` | round 3 focused suite |
| R8 | `TestRawHeadSwapQuarantinesTheHeadThatWasChecked`, `internal/seatcommit/quarantine_test.go:505` | round 3 focused suite |
| R9/P5 | `TestLedgerProductionWritersUseOnlyTheRawPath`, `internal/seatcommit/quarantine_callers_test.go:118`; `TestRawWriterGuardListsEveryCallerAndRejectsAThirdRecoveryCaller`, line 149 | round 3 focused suite |
| R11 | `TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence`, `internal/seatcommit/raw_compat_test.go:206` | round 3 focused suite |
| R11 max | `TestRecoveryTreatsTheMaximumClaimedSequenceAsUnknown`, `internal/seatcommit/raw_compat_test.go:269`; the wider copied-input bounds are R19 | round 3 focused suite |
| R12 | `TestInterruptedMigrationRecoveryDedupesTheOriginalDecision`, `internal/seatcommit/raw_compat_test.go:744` | round 3 focused suite |
| R13 | `TestRetryCompletesARecoveredRawBodyAboveALegacyHead`, `internal/seatcommit/raw_compat_test.go:776` | round 3 focused suite |
| R14 | `TestRecoveryRefusesWhenEvidenceRefsCannotBeObserved`, `internal/seatcommit/recover_test.go:224` | round 3 focused suite |
| D5/R4 | `TestRecoveryRefusesASlotBodyThatIsNeitherLayout`, `internal/seatcommit/raw_compat_test.go:535` | round 3 focused suite |
| P1/R5 | `TestRecoveryIsTamperOnlyForCompatibilityRefusals`, `internal/seatcommit/raw_compat_test.go:98` | round 3 focused suite |
| P3 | `TestRecoveryMigratesALegacyBodyWithNoUsableHead`, `internal/seatcommit/raw_compat_test.go:695` | round 3 focused suite |
| P4 | `TestPrepareErrorWrapsOnlyTamperingAsALedgerAlarm`, `internal/schedproc/prepare_quarantine_test.go:76` | round 3 focused suite |

R15's crash table is filled from the five subtests, not predicted from the
layout names:

| Slot form | Old head | Recovery choice | Reader case after the crash | Outcome |
|---|---|---|---|---|
| head raw / body raw | raw, verifies | seq 2 after verified-head seq 1, Prev the head tag, `verified_head` | `CheckRaw`, immediate-successor case | committed; retry changes no bytes; the next ordinary write makes the head current |
| head raw / body legacy | raw, verifies | seq 2 after verified-head seq 1, Prev the head tag, `verified_head` | `CheckRaw`, immediate-successor case | committed; retry changes no bytes; the next ordinary write makes the head current |
| head legacy / body raw with a gap | legacy, verifies | seq 4 after body seq 3, Prev the head tag, `body_seal` | `CheckRawMigration` refuses a non-successor | second slot and second decision; final seq is above both slots |
| head legacy / body legacy | legacy, verifies | seq 2 after verified-head seq 1, Prev the head tag, `verified_head` | `CheckRawMigration`, immediate successor | committed; retry completes the raw head and records one migration |
| interrupted migration | legacy, verifies | seq 3 after body seq 2, Prev the body tag, `body_seal` | `CheckRawMigration` refuses seq 3 over head seq 1 | second slot and second decision; final seq is above both slots |

For each admitted form, `RecoveriesOf` exposes the sealed recovery before the
retry, which is the scheduler hold-reconciliation source of truth. The retry is
`AlreadyRecovered` and writes no second body link.
`TestRecoveryReleasesTheSeatAndReturnsTheTask` runs later ticks and asserts the
reconciliation's `ledger_hold_released` line occurs exactly once. For each
refused form, the old decision is `recover_slot_stale`, the named new slot
recovers, and the first slot remains byte-identical.

The writer guard resolves `sealchain` and `atomicfile` by import path, so an
alias does not hide a call. It scans direct ledger-path arguments to
`atomicfile.Write` and the named `os` write calls, plus production
`sealchain.Seal` and `sealchain.WriteHead`. It cannot prove a ledger path that
another package passes through an opaque variable.

The reader guard gained no entry. The inventory command was:

```sh
rg -n 'QuarantineDir|"quarantine"|sealedNames' internal --glob '*.go' --glob '!internal/seatcommit/**' --glob '!**/*_test.go'
```

Production outside seatcommit reaches quarantine paths through
`internal/uninstall/plan.go`: line 82 classifies `quarantine` with the sealed
names; its generic extension walk builds the candidate path at line 203 and
checks the subtree at line 208; line 225 groups a matching directory; and line
256 supplies its removal description. CLI, schedproc, and scheduler
occurrences describe or pass a slot identity and do not build a filesystem
path.

### Schema, changed tests, and limits

`Ledger.Recovered`, `LedgerCommit.Note`, and `LedgerRecovery.SeqFrom` are
schema-1 fields because rc.2 is the first released reader of the raw layout. A
binary built between #925 and this PR classifies a recovered ledger containing
one of the later fields as `schema_mismatch` and excludes that seat. After
rc.2, a new sealed field requires a schema bump.

Against #904, `TestOlderReaderRefusesARecoveredLedgerAsTampered` became
`TestRecoveredLedgerRemainsStrictSchemaOne`, and
`TestADecisionByAlarmAnswersAHoldNoSlotCan` again uses an edited-sequence head
instead of an authentic earlier head. Against 0e, slot readers accept both
layouts but strict authenticated unknown-field classification is unchanged.

A crash between `blockOnTamper`'s task-store write and the tick's state write
can leave a task blocked for an alarm no restored hold carries. The lead uses
`amq-squad task reset ID` for that task; durable repair is a 3.2.x item. A crash
between the recovery's two writes costs a second decision only where the
reader refuses the new body under the old head; where the old head is intact
and the body is its immediate successor, the recovery is already committed
and the retry or the next write finishes it. Under a raw head, the retry
deliberately leaves that head behind. The next ordinary writer derives its
predecessor from the head and writes a replacement N+1: the chain stays
consistent and carries the recovery, but the crashed successor's own sealed
link is never completed. Completing an admitted successor's head before the
next write is a named 3.2.x item. The evidence anchor's later `update-ref`
still lacks an expected-old value; adding one is a 3.2.x item.

### Release note

The release note says a tamper alarm holds the seat once per alarm and that
the lead recovers the exact quarantined slot through `scheduler recover`. The
decision is recorded, recovery writes the raw sealed layout, checked commits
may be carried, and the quarantine remains evidence.

### Round 2 gates in the seat

The gates ran unlocked in the seat sandbox. Focused seatcommit, scheduler,
schedproc, and CLI suites pass; the touched pi tests pass 31/31 and pi lint
passes. Build and vet pass for every touched Go package. The full package and
`make ci` runs do not complete here because macOS denies loopback TCP and Unix
listener creation (`httptest`, scheduler-decision, lead-start, and proof
sockets). CLI context tests also observe this live seat's role/rules digest,
and seat-commit tests correctly refuse from inside a seat. No focused changed
row failed. The lead reruns the locked full gates on the PR head.

## Round 3b

Round 3 starts from the saved t131/#935 patch at
`/Users/omri/.claude/jobs/6dd424c9/tmp/t131-pr935.patch`. Its SHA-256 is
`1f4e2f37d4d9188ece970cc5234d8b7a7eb6aec4f1520375dfc0f5cf5d1fb120`.
The sandbox could read it. `git apply --index --binary` encountered an index
matching refusal even though the tree was clean and `git apply --check`
passed, so the exact equivalent was used: `git apply --binary`, then `git
add` of the declared paths. The staged binary diff had the same SHA-256 and
`git write-tree` printed
`ef4e3a6d3a96a26e323e7624ba9a80b8fee2cd6c`. Thus the applied diff equals
the saved patch before this round's additions.

### Round 3b sequence, layout, and durability rules

A sequence copied from refused head or body bytes is plausible only from 1
through 9007199254740990. This leaves a successor no greater than
9007199254740991, the largest integer carried exactly by interoperating JSON
readers. A verified head is not subject to the copied-input bound. A copied
value above the bound is not used for a slot name or chain link. If neither
copy supplies a usable link, recovery requires `--seq unknown` and refuses
with `recover_seq_unknown`. A real chain whose sequence has passed the bound
can recover a refused copy only with `recover --alarm` while its ledger
verifies.

No released binary wrote a `seq0` or negative-sequence quarantine slot. The
new reader treats such edited claims as unknown. At the platform integer
maximum, raw writers return `sealchain.ErrSeqExhausted` before their save
callback. The seat writer names the seat and says "the ledger's sequence is
exhausted". Verified recovery at that maximum uses D1.c's slot-invalid
refusal before a write.

Recovery records a layout migration only for legacy evidence. A usable head
decides the evidence layout. Without one, the body decides it: a legacy body
migrates and a raw body does not. An interrupted migration retains its
completion rule. A body that is neither known layout is always
`recover_seq_unknown`; a known raw or legacy layout with an implausible copied
sequence can still continue from a usable head.

`seq_from` is `claimed_head` only for a plausible claim decoded from the head
copy, `body_seal` for a copied body seal, and `verified_head` for an
authenticated head. The slot's named sequence remains separate. What
authenticates the new link is the lead's recorded decision, which names the
slot, both hashes and the reason.

The recovery crash seam runs after any required migration decision in the
same pre-head closure. The measured R15 migration sequences are:

| Slot form | After crash | After final decision |
|---|---:|---:|
| raw head / raw body | none | none |
| raw head / legacy body | none | none |
| legacy head / raw body with a gap | 4 | 4, 5 |
| legacy head / legacy body | 2 | 2 |
| interrupted migration | 2 | 2, 4 |

The two refused forms can record migration decisions at different sequences.
This is the measured result submitted for the plan reviewer's ruling;
deduplication remains per ledger kind, seat, and sequence.

The alarm append's open reports whether it created the file. The first append
syncs the file and the ledger directory once each. A later append syncs only
the file. A directory-sync failure is returned through the recovery decision.

### Round 3b rows

| Row | Named test |
|---|---|
| U1 | `TestRenamedSlotMustAgreeWithItsCopiedHeadClaim` |
| U2 | `TestVerifiedRecoveryRefusesASlotAboveTheLiveLink` |
| U3 | `TestRawWriterGuardListsEveryCallerAndRejectsAThirdRecoveryCaller` |
| R16 | `TestRecoveryTreatsNonPositiveHeadClaimsAsUnknownForBothLayouts` |
| R17 | `TestRawWriterRefusesSequenceExhaustionBeforeSave`; `TestOrdinaryWriterRefusesAnExhaustedVerifiedLedgerWithoutChangingEvidence` |
| R18 | `TestVerifiedRecoveryAtMaximumSequenceRefusesBeforeWriting` |
| R19 | `TestCopiedHeadClaimsAboveTheInteroperableBoundUseTheBody`; `TestLargestPlausibleCopiedHeadClaimLeavesOneOrdinaryVerifiedWrite`; `TestImplausibleCopiedBodySequenceNeedsAUsableHead`; `TestKnownRawBodyWithImplausibleSequenceContinuesFromVerifiedHead`; `TestTamperedBodyUnderVerifiedMaximumHeadKeepsD1cRefusal` |
| R20 | `TestRawRecoveryWithGarbageHeadNeverRecordsAMigration`; `TestRecoveryContinuesTheChainAfterTheQuarantinedSeq`; `TestRecoveryMigratesALegacyBodyWithNoUsableHead` |
| R21 | `TestRecoveryDecisionNamesTheActualSequenceSource` |
| R22 | `TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm` |
| R23 | `TestAlarmAppendSyncsNewDirectoryEntryOnce`; `TestAlarmAppendReturnsNewDirectorySyncFailure` |

The R15 test itself supplies both migration columns. The ordinary raw recovery
row checks again after an ordinary write, and the legacy-no-head row requires
exactly one migration record.

### Strengthened round 2 assertions

R11 now checks byte-identical body and head across retry and the persisted
`Recovered.Seq`, `Recovered.Link`, and `SeqFrom`. The refusal rows compare the
live ledger, head, decision log where it exists, and slot contents. R14's
injected error deliberately omits `for-each-ref`; the returned refusal proves
the production wrapper names that command. The checked-entry tests are
`TestRecoveryCarriesOnlyCheckedEntries` and
`TestCarryCheckIgnoresTheSeatsGitConfig`: the first proves origin membership,
tree equality, Note preservation, and unknown-entry refusal; the second proves
seat-controlled Git configuration cannot redirect that check. The R15
scheduler path is exercised by `TestInterruptedRecoveryCompletesFromTheLedger`
using the real ledger on disk. The real preparation classification row is
`TestPreparationClassifiesRealTamperedAndNewerWriterLedgersSeparately`.
Compatibility wording is asserted in
`TestRecoveryIsTamperOnlyForCompatibilityRefusals`. The raw writer guard now
walks all non-test Go under `internal`, not only `internal/seatcommit`.

### Revert evidence

An abbreviated excerpt from the lead's first revert record follows. Round 3c
pastes both source files whole and supersedes this excerpt:

```text
Revert runs on PR #934 head fa6bb69998f48e607ec7a4e76b4f3761e889d53f, the lead's scratch worktree, 2026-11-10 02:47 to 02:57 IST.
Each entry: one string edit asserted to occur once, go test ./internal/seatcommit -run '^(<every Test of recover_test.go, raw_compat_test.go, quarantine_test.go>)$' -count=1 (R15: the five-form test only; P4: go test ./internal/schedproc -count=1), the file restored.

[baseline seatcommit REC] exit 0 ok github.com/omriariav/amq-squad/v3/internal/seatcommit 32.193s
[D1.a name check removed] exit 0; failing: NONE (not caught)
    first: ok github.com/omriariav/amq-squad/v3/internal/seatcommit 34.876s
[D1.a named seq ignores the claimed head seq] exit 1; failing: TestADecisionByAlarmAnswersAHoldNoSlotCan, TestRecoveryRefusesTheMaximumClaimedSequence, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence
    first: raw_compat_test.go:193: recovery = {Handle: AlreadyRecovered:false Verified:false Alarm: Slot: OldSeq:0 NewSeq:0 Prev: SeqFrom: BodySHA256: HeadSHA256: Base: Carried:[]}, recover_slot_invalid: <tmp>/TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutRe
[D1.b claimed seq left out of the max] exit 1; failing: TestRecoveryRefusesTheMaximumClaimedSequence, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence
    first: raw_compat_test.go:193: recovery = {Handle:s1 AlreadyRecovered:false Verified:false Alarm: Slot:<tmp>/quarantine/commits.s1.seq7 OldSeq:7 Ne
[D1.c MaxInt check removed] exit 1; failing: TestRecoveryRefusesTheMaximumClaimedSequence
    first: raw_compat_test.go:213: maximum seq refusal = recover_write_failed: layout migration decision path, ledger kind, and sequence are required
[D2 interrupted migration written as migrating] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration
    first: raw_compat_test.go:419: migration decisions = 2: {"kind":"ledger_layout_migrated","seat":"s1","seq":2}
[D2 interrupted migration record not written] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration
    first: raw_compat_test.go:417: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[D2 no interrupted-migration branch in link] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:412: recovery after interrupted migration = {Seq:3 Prev:429b3a1243b4ea2461c335f205726c637a033d41fcb24237ec2957ee9d9aa400 Tag:be043a193730d0caa5277d5664d4e98cf0e1946bd745d8a0276c85686f78d372 Version:(devel) Schema:1}, <nil>
[D3 retry does not complete the migration] exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRetryCompletesARecoveredRawBodyAboveALegacyHead
    first: raw_compat_test.go:481: completed head = "", <nil>
[D4 evidence seq error ignored] exit 1; failing: TestRecoveryRefusesWhenEvidenceRefsCannotBeObserved
    first: recover_test.go:235: not a recovery refusal: <nil>
[D5 a body of neither layout accepted] exit 1; failing: TestRecoveryRefusesASlotBodyThatIsNeitherLayout
    first: raw_compat_test.go:233: not a recovery refusal: <nil>
[P1 no compat branch in recordAlarm] exit 1; failing: TestRecoveryIsTamperOnlyForCompatibilityRefusals
    first: raw_compat_test.go:117: alarm recovery refusal = recover_alarm_unverified: newer_writer
[P1 no compat case in recoverLedger] exit 1; failing: TestRecoveryIsTamperOnlyForCompatibilityRefusals
    first: raw_compat_test.go:113: recovery refusal = recover_write_failed: newer_writer
[P2 verified recovery of an interrupted migration: seq 0] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision
    first: raw_compat_test.go:446: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[P2 verified recovery of an interrupted migration: -1 (migrating write)] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision
    first: raw_compat_test.go:448: migration decisions = 1 at seq 3
[P2 verified recovery of a legacy body not migrating] exit 1; failing: TestVerifiedLegacyRecoveryAlsoRecordsItsRawMigration
    first: raw_compat_test.go:730: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[P3 legacy body with no usable head not migrating] exit 1; failing: TestRecoveryMigratesALegacyBodyWithNoUsableHead
    first: raw_compat_test.go:391: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[R1 recoverWrite always migrating] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryContinuesTheChainAfterTheQuarantinedSeq
    first: raw_compat_test.go:419: migration decisions = 2 at seq 2
[R1 recoverWrite never migrating] exit 1; failing: TestRecoveryMigratesALegacyBodyWithNoUsableHead, TestRecoveryMigratesALegacyLedgerThroughTheRawWriter, TestRetryCompletesARecoveredRawBodyAboveALegacyHead, TestVerifiedLegacyRecoveryAlsoRecordsItsRawMigration
    first: raw_compat_test.go:357: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[R2 the already-recovered case disabled] exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRecoveryDecidedAgainIsAnsweredFromTheLedger, TestRecoveryMigratesALegacyLedgerThroughTheRawWriter, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence, TestRetryCompletesARecoveredRawBodyAboveALegacyHead
    first: raw_compat_test.go:199: retry was not AlreadyRecovered
[slot-stale comparison removed] exit 1; failing: TestADecisionByAlarmAnswersAHoldNoSlotCan, TestAHoldOnAnEarlierSlotIsAnsweredAfterALaterRecovery, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRecoveryRefusesWhatItCannotCheck
    first: raw_compat_test.go:604: not a recovery refusal: <nil>
[verified: behind-the-slot check removed] exit 0; failing: NONE (not caught)
    first: ok github.com/omriariav/amq-squad/v3/internal/seatcommit 18.539s
[R15 CheckRaw immediate-successor case removed] exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:559: raw successor was not admitted
[P4/R6 prepareError wraps every error as a tamper alarm] exit 1; failing: TestPrepareErrorWrapsOnlyTamperingAsALedgerAlarm
    first: prepare_quarantine_test.go:32: LedgerTamperedError = true, want false: newer_writer
[R9 planted third caller of saveRawLedgerBeforeHead] exit 0 []
```

The lead's runs were on PR #934 head
`fa6bb69998f48e607ec7a4e76b4f3761e889d53f` in its scratch worktree on
2026-11-10 from 02:47 to 02:57 IST. The baseline focused recovery set passed.
The removals for D1.b, D1.c, D2, D3, D4, D5, P1 to P4, R1, R2, the slot-stale
comparison, and the R15 immediate-successor case each produced the named
focused failure in the lead's record. Three lead mutations passed before this
round: removal of the D1.a name check, removal of the verified-behind-slot
check, and planting a third `saveRawLedgerBeforeHead` caller. They are now
pinned by U1, U2, and U3 respectively. The seat ran the named focused tests
and the production gates below; mutation edits themselves were not applied in
the seat sandbox, so the lead reruns every exact mutation at the gate.

### Patch identity and reader inventory corrections

The stable patch-id command is:

```sh
git diff <base> <head> -- <file> | git patch-id --stable
```

For `internal/scheduler/tamper_test.go`, #904 is `46986e2a` and the round-2
port is `b5f3a9ce`; it is changed, not identical. Rechecking
`internal/schedproc/recover_test.go` gives `e8d4e0bc` on both sides. The older
per-file table's tamper-test `same` row is superseded by this correction.

The inventory command remains:

```sh
rg -n 'QuarantineDir|"quarantine"|sealedNames' internal --glob '*.go' --glob '!internal/seatcommit/**' --glob '!**/*_test.go'
```

Outside seatcommit, `internal/uninstall/plan.go` classifies `quarantine` in
`sealedNames`, dispatches it at line 225, and describes it at line 256. Its
generic extension walk also builds `filepath.Join(xpath, name)` at line 203
and checks the opened subtree at line 208, so those lines are readers of paths
that can be the quarantine directory. CLI, schedproc, and scheduler only pass
or describe slot identities.

### Limits and seat gates

The guard detects literal direct ledger paths and the reviewed raw writer
calls. It cannot prove a path assembled from a file-name literal, a path
passed through a local variable, or the semantic effect of `os.OpenFile`
flags. Completing an admitted successor's head before the next ordinary write
is the superseded-successor item on 0e's 3.2.x list. The alarm directory-sync
seam reached creation in this round, so M6 is not deferred.

The seat gates ran unlocked in the seat sandbox. The focused seatcommit,
sealchain, scheduler, schedproc, and CLI tests, build, and vet results are
recorded below. Full package runs that create loopback TCP or Unix listeners
cannot complete here because the sandbox returns `bind: operation not
permitted`; the lead reruns the locked complete gates.

- `go test ./internal/sealchain -count=1`: pass.
- The named recovery, raw-layout, quarantine, alarm, and writer-guard tests in
  `internal/seatcommit`: pass.
- The named real-ledger and source-selection tests in `internal/schedproc` and
  the scheduler recovery/classification tests: pass.
- `go build` and `go vet` for `internal/sealchain`, `internal/seatcommit`,
  `internal/schedproc`, `internal/scheduler`, and `internal/cli`: pass.
- The broader schedproc and CLI runs stop on denied Unix decision sockets.
  `make ci` passes `go vet ./...`, then first fails in `internal/buzz` at
  `listen tcp6 [::1]:0: bind: operation not permitted`. It also observes the
  same denied CLI decision socket and the live seat's expected role/rules
  digest warnings. The run was stopped after those environmental failures
  were established rather than waiting for its 20-minute timeout.

## Round 3c release gate

Round 3b completed its stated rows, but it did not meet A5 at the scheduler
level: it did not drive the real preparer and recovery reader from a crashed
on-disk recovery through release, reset, and dispatch. Round 3c supplies that
row. The omission was a test-evidence gap, not a production block, and the
round 3c results below find no release block.

### Round 3c rows and negative controls

| Row | Test and outcome | Isolated production revert and first failure |
|---|---|---|
| T1, scheduler-level A5 | `TestCrashedRecoveryReleasesOnceFromTheRealLedgerForRawAndLegacyHeads`, `internal/schedproc/recover_test.go:247`; raw and legacy heads, with and without a decision retry, release one hold, reset one task, and dispatch once. The retry is `AlreadyRecovered`, leaves the body unchanged, records one `ledger_recovered`, and later ticks never release again. `RecordTarget` leaves a verifying recovered raw ledger; raw records no migration and legacy records exactly one. | At `internal/scheduler/scheduler.go:654`, replaced `s.applyRecoveries(now, log)` with `error(nil)`. All four forms failed first at `internal/schedproc/recover_test.go:270`: `first tick did not release, reset, and dispatch exactly once: []`. |
| T2, legacy C1b | `TestLegacyBodyWithImplausibleSequenceUsesVerifiedHeadAndChecksCarry`, `internal/seatcommit/raw_compat_test.go:467`; an embedded seal at 2^53 with a tag is ignored under a verified legacy head, unknown carry refuses without changing the ledger, head, slot, or decisions, and the accepted recovery uses N+1, old N, `verified_head`, the exact carried Note, and one migration at N+1. | At `internal/seatcommit/recover.go:638`, replaced `plausibleCopiedSeq(l.Seal.Seq)` with `l.Seal.Seq > 0`. It failed first at `internal/seatcommit/raw_compat_test.go:494`: the recovery chose sequence 9007199254740993 from `body_seal`. |
| T2b, ruling 1 legacy shapes | `TestLegacyShapeWithUnusableSealStillUsesVerifiedHead`, `internal/seatcommit/raw_compat_test.go:506`; both a legacy seal with sequence 1 and no tag and an empty seal object recover at N+1 from `verified_head`, while unknown carry is still refused. The neither-layout contract remains the existing `TestRecoveryRefusesASlotBodyThatIsNeitherLayout`; it was not duplicated. | At `internal/seatcommit/recover.go:637`, moved `bodyKnown = true` inside the usable-seal condition. Both forms failed first at `internal/seatcommit/raw_compat_test.go:520` with `recover_seq_unknown` saying the body was neither layout. |
| T3, persisted verified source | `TestADocumentVerifyingAgainIsReleasedByARecordedDecision` now reads the ledger after recovery and requires `Recovered.SeqFrom == "verified_head"`; `TestRecoveredLedgerRemainsStrictSchemaOne` remains at `internal/seatcommit/recover_test.go:520`. | At `internal/seatcommit/recover.go:408`, removed `SeqFrom: "verified_head"` from the persisted `LedgerRecovery`. It failed first at `internal/seatcommit/recover_test.go:643`, printing the record with an empty `SeqFrom`. |
| T4, thin assertions | The raw implausible-body row now requires N+1, exact Note, and no recovery decision after refusal; every R15 form requires its exact `SeqFrom` and Note; maximum verified recovery requires the whole refusal sentence including the handle; R11 requires raw and legacy migration counts and equality between result, decision, and persisted `seq_from`; `assertNoRecoveryDecision` covers `ledger_recovered`, `ledger_hold_released`, and `ledger_layout_migrated`; `TestAlarmAppendSyncsNewDirectoryEntryOnce` requires file sync before directory sync. | At `internal/seatcommit/alarms.go:178`, swapped the two `errors.Join` operands. `TestAlarmAppendSyncsNewDirectoryEntryOnce` failed first at `internal/seatcommit/alarms_test.go:152`: `first append sync order = [directory file]`. |
| T5, whole-internal writer guard | `TestRawWriterGuardListsEveryCallerAndRejectsAThirdRecoveryCaller`, `internal/seatcommit/quarantine_callers_test.go:149`, walks every production Go file under `internal`, holds the complete allowlist, and tests its parser with a schedproc `WriteRawAfter` caller. | Planted `sealchain.WriteRawAfter` in temporary production file `internal/schedproc/t133_raw_writer_plant.go`. The guard failed first at `internal/seatcommit/quarantine_callers_test.go:189`: `unreviewed raw writer caller schedproc/t133_raw_writer_plant.go:t133RawWriterPlant:sealchain.WriteRawAfter`. The file was then removed. |

Each negative control was applied alone and restored before the next. The final
diff contains no non-test Go file.

The two changed carry rows have distinct reasons. The raw implausible-body row
was strengthened because ignoring an unusable sequence must not skip the
checked-entry membership and origin proof. The legacy implausible-body row was
added because C1b previously covered only the raw representation; the same
sequence-source and carry rules must hold for an embedded legacy seal.

### Ruling measurements and remaining facts

The R15 migration sequences in Round 3 are measurements, not an admission of
the two-sequence result. They remain submitted for the plan reviewer's ruling,
with dedupe keyed by ledger kind, seat, and sequence. Ruling 1 is now measured
by T2b: a recognizable legacy body with an unusable embedded seal still uses a
verified head, while a body of neither layout remains refused by the existing
neither-layout row.

At the maximum interrupted-migration boundary, the lead's measurement starts
with a verified legacy head at MaxInt-1 and a raw successor body at MaxInt. One
ordinary write completes that head and records exactly one migration at
MaxInt. The next ordinary write returns the named sequence-exhaustion error;
the body is unchanged, the ledger still verifies, and no quarantine is
created. This is the measured limit, not a claim that a successor beyond
MaxInt exists.

`evidenceSeqOf`, `internal/seatcommit/recover.go:840`, now returns its
`for-each-ref` observation error instead of substituting zero. The named row is
`TestRecoveryRefusesWhenEvidenceRefsCannotBeObserved`; it requires
`recover_base_unobserved`, the `for-each-ref` command in the refusal, and no
write.

The quarantine inventory command was run as written:

```sh
rg -n 'QuarantineDir|"quarantine"|sealedNames' internal --glob '*.go' --glob '!internal/seatcommit/**' --glob '!**/*_test.go'
```

It finds uninstall's generic extension walk as the only external path builder;
CLI, schedproc, and scheduler only describe or pass a slot identity.

### Round 3c seat verification

The final restored tree passed:

- `go test ./internal/seatcommit ./internal/schedproc ./internal/scheduler
  -count=1`: seatcommit 122.311 s, schedproc 32.949 s, scheduler 22.814 s.
- `go vet ./internal/seatcommit ./internal/schedproc ./internal/scheduler`:
  exit 0.
- The focused T1 test passed all four raw/legacy and retry/no-retry forms.
- The focused T2, T2b, T3, T4, and T5 rows passed after their isolated
  mutations were restored.
- `git diff --check` passed, and the declared Go diff contains test files only.

The seat runs these commands unlocked. The lead reruns the full Go tests, vet,
and every exact negative control under the host gate lock on the final PR head.

### Complete lead revert records supplied to round 3c

The following two blocks are pasted whole from the named files. They are not
abbreviated. The single trailing space after `vet exit 0` in the #936 source
is omitted so the Markdown passes the repository whitespace gate.

#### `gate-934-reverts.txt`

```text
Revert runs on PR #934 head fa6bb69998f48e607ec7a4e76b4f3761e889d53f, worktree /Users/omri/scratch-revert-934, 2026-11-10 02:47 to 02:57 IST.
Script: T/rev934.py (each entry: one string edit asserted to occur once, `go test <pkg> -run <the tests of recover_test.go, raw_compat_test.go, quarantine_test.go> -count=1`, the file restored). The exact edits are in the script.

[baseline seatcommit REC] exit 0 ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	32.193s
[D1.a name check removed] exit 0; failing: NONE (not caught)
    first: ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	34.876s
[D1.a named seq ignores the claimed head seq] exit 1; failing: TestADecisionByAlarmAnswersAHoldNoSlotCan, TestRecoveryRefusesTheMaximumClaimedSequence, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence
    first: raw_compat_test.go:193: recovery = {Handle: AlreadyRecovered:false Verified:false Alarm: Slot: OldSeq:0 NewSeq:0 Prev: SeqFrom: BodySHA256: HeadSHA256: Base: Carried:[]}, recover_slot_invalid: /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutRe
[D1.b claimed seq left out of the max] exit 1; failing: TestRecoveryRefusesTheMaximumClaimedSequence, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence
    first: raw_compat_test.go:193: recovery = {Handle:s1 AlreadyRecovered:false Verified:false Alarm: Slot:<tmp>/quarantine/commits.s1.seq7 OldSeq:7 Ne
[D1.c MaxInt check removed] exit 1; failing: TestRecoveryRefusesTheMaximumClaimedSequence
    first: raw_compat_test.go:213: maximum seq refusal = recover_write_failed: layout migration decision path, ledger kind, and sequence are required
[D2 interrupted migration written as migrating] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration
    first: raw_compat_test.go:419: migration decisions = 2: {"at":"2026-11-10T00:51:56.65212Z","kind":"ledger_layout_migrated","ledger_kind":"commits","seat":"s1","seq":2,"reason":"legacy commit ledger migrated to raw-bytes-v1 at link 2"}
[D2 interrupted migration record not written] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration
    first: raw_compat_test.go:417: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[D2 no interrupted-migration branch in link] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:412: recovery after interrupted migration = {Seq:3 Prev:429b3a1243b4ea2461c335f205726c637a033d41fcb24237ec2957ee9d9aa400 Tag:be043a193730d0caa5277d5664d4e98cf0e1946bd745d8a0276c85686f78d372 Version:(devel) Schema:1}, <nil>
[D3 retry does not complete the migration] exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRetryCompletesARecoveredRawBodyAboveALegacyHead
    first: raw_compat_test.go:481: completed head = "", <nil>
[D4 evidence seq error ignored] exit 1; failing: TestRecoveryRefusesWhenEvidenceRefsCannotBeObserved
    first: recover_test.go:235: not a recovery refusal: <nil>
[D5 a body of neither layout accepted] exit 1; failing: TestRecoveryRefusesASlotBodyThatIsNeitherLayout
    first: raw_compat_test.go:233: not a recovery refusal: <nil>
[P1 no compat branch in recordAlarm] exit 1; failing: TestRecoveryIsTamperOnlyForCompatibilityRefusals
    first: raw_compat_test.go:117: alarm recovery refusal = recover_alarm_unverified: the commits ledger of seat s1 cannot be read as a document (commit ledger s1: newer_writer: the seat's commit record was written by a newer amq-squad (v9.0.0); install it with amq-squad up --binary <that build>: unknown field
[P1 no compat case in recoverLedger] exit 1; failing: TestRecoveryIsTamperOnlyForCompatibilityRefusals
    first: raw_compat_test.go:113: recovery refusal = recover_write_failed: the commits ledger of seat s1 cannot be read: commit ledger s1: newer_writer: the seat's commit record was written by a newer amq-squad (v9.0.0); install it with amq-squad up --binary <that build>: unknown field "future" (writer schema
[P2 verified recovery of an interrupted migration: seq 0] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision
    first: raw_compat_test.go:446: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[P2 verified recovery of an interrupted migration: -1 (migrating write)] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision
    first: raw_compat_test.go:448: migration decisions = 1: {"at":"2026-11-10T00:54:53.006046Z","kind":"ledger_layout_migrated","ledger_kind":"commits","seat":"s1","seq":3,"reason":"legacy commit ledger migrated to raw-bytes-v1 at link 3"}
[P2 verified recovery of a legacy body not migrating] exit 1; failing: TestVerifiedLegacyRecoveryAlsoRecordsItsRawMigration
    first: raw_compat_test.go:730: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[P3 legacy body with no usable head not migrating] exit 1; failing: TestRecoveryMigratesALegacyBodyWithNoUsableHead
    first: raw_compat_test.go:391: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[R1 recoverWrite always migrating] exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryContinuesTheChainAfterTheQuarantinedSeq
    first: raw_compat_test.go:419: migration decisions = 2: {"at":"2026-11-10T00:55:49.379085Z","kind":"ledger_layout_migrated","ledger_kind":"commits","seat":"s1","seq":2,"reason":"legacy commit ledger migrated to raw-bytes-v1 at link 2"}
[R1 recoverWrite never migrating] exit 1; failing: TestRecoveryMigratesALegacyBodyWithNoUsableHead, TestRecoveryMigratesALegacyLedgerThroughTheRawWriter, TestRetryCompletesARecoveredRawBodyAboveALegacyHead, TestVerifiedLegacyRecoveryAlsoRecordsItsRawMigration
    first: raw_compat_test.go:357: open <tmp>/scheduler/decisions.jsonl: no such file or directory
[R2 the already-recovered case disabled] exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRecoveryDecidedAgainIsAnsweredFromTheLedger, TestRecoveryMigratesALegacyLedgerThroughTheRawWriter, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence, TestRetryCompletesARecoveredRawBodyAboveALegacyHead
    first: raw_compat_test.go:199: retry = {Handle:s1 AlreadyRecovered:false Verified:true Alarm: Slot:<tmp>/quarantine/commits.s1.seq7 OldSeq:7 NewSeq:
[slot-stale comparison removed] exit 1; failing: TestADecisionByAlarmAnswersAHoldNoSlotCan, TestAHoldOnAnEarlierSlotIsAnsweredAfterALaterRecovery, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRecoveryRefusesWhatItCannotCheck
    first: raw_compat_test.go:604: not a recovery refusal: <nil>
[verified: behind-the-slot check removed] exit 0; failing: NONE (not caught)
    first: ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	18.539s
[R15 CheckRaw immediate-successor case removed] exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:559: raw successor was not admitted: commit ledger s1: ledger_tampered: commits.s1 is at link 2, its head records link 1; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotFor
[P4/R6 prepareError wraps every error as a tamper alarm] exit 1; failing: TestPrepareErrorWrapsOnlyTamperingAsALedgerAlarm
    first: prepare_quarantine_test.go:32: LedgerTamperedError = true, want false: the commit ledger of seat seat-a: newer_writer
[R9 planted third caller of saveRawLedgerBeforeHead] exit 0 []

```

#### `gate-936-reverts.txt`

```text
Revert runs on PR #936 head adf80543222d6869ede6322f47e9841d559c87ea, the lead's scratch worktree /Users/omri/scratch-revert-936, started 2026-11-10 04:05 IST.
[baseline seatcommit REC+guards+alarms] exit 0 ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	29.930s
[baseline sealchain] exit 0 ok  	github.com/omriariav/amq-squad/v3/internal/sealchain	0.786s
[U1 D1.a name check removed] ./internal/seatcommit exit 1; failing: TestRenamedSlotMustAgreeWithItsCopiedHeadClaim
    first: raw_compat_test.go:467: not a recovery refusal: <nil>
[U2 verified: behind-the-slot check removed] ./internal/seatcommit exit 1; failing: TestVerifiedRecoveryRefusesASlotAboveTheLiveLink
    first: raw_compat_test.go:491: not a recovery refusal: <nil>
[U3 planted third caller of saveRawLedgerBeforeHead in ledger.go] exit 1 ; failing: ['TestRawWriterGuardListsEveryCallerAndRejectsAThirdRecoveryCaller'] ['quarantine_callers_test.go:182: unreviewed raw writer caller ledger.go:zzPlanted:saveRawLedgerBeforeHead']
[U3b plant builds?] vet exit 0
[U3b planted sealchain.WriteRawAfter caller in internal/schedproc (information; outside the ruled row)] exit 0 ; failing: []
[R16/M1 plausibleCopiedSeq lower bound removed] ./internal/seatcommit exit 1; failing: TestCopiedHeadSequenceMustLeaveAPlausibleSuccessor, TestRecoveryTreatsNonPositiveHeadClaimsAsUnknownForBothLayouts
    first: quarantine_test.go:140: headSeq({"seq":0}) = 0, want unknown
[R19/M2.iii plausibleCopiedSeq upper bound removed] ./internal/seatcommit exit 1; failing: TestCopiedHeadClaimsAboveTheInteroperableBoundUseTheBody, TestCopiedHeadSequenceMustLeaveAPlausibleSuccessor, TestImplausibleCopiedBodySequenceNeedsAUsableHead, TestKnownRawBodyWithImplausibleSequenceContinuesFromVerifiedHead, TestKnownRawBodyWithImplausibleSequenceStillChecksCarriedEntries, TestRecoveryTreatsTheMaximumClaimedSequenceAsUnknown
    first: quarantine_test.go:140: headSeq({"seq":9007199254740991}) = 9007199254740991, want unknown
[R19 boundary: < becomes <= (2^53-1 itself plausible)] ./internal/seatcommit exit 1; failing: TestCopiedHeadClaimsAboveTheInteroperableBoundUseTheBody, TestCopiedHeadSequenceMustLeaveAPlausibleSuccessor
    first: quarantine_test.go:140: headSeq({"seq":9007199254740991}) = 9007199254740991, want unknown
[R19 boundary: bound one lower (< maxPlausibleSeq-1)] ./internal/seatcommit exit 1; failing: TestCopiedHeadSequenceMustLeaveAPlausibleSuccessor, TestLargestPlausibleCopiedHeadClaimLeavesOneOrdinaryVerifiedWrite
    first: quarantine_test.go:140: headSeq({"seq":9007199254740990}) = unknown, want 9007199254740990
[C1b raw body: implausible seal seq used] ./internal/seatcommit exit 1; failing: TestImplausibleCopiedBodySequenceNeedsAUsableHead, TestKnownRawBodyWithImplausibleSequenceContinuesFromVerifiedHead, TestKnownRawBodyWithImplausibleSequenceStillChecksCarriedEntries
    first: raw_compat_test.go:367: not a recovery refusal: <nil>
[C1b legacy body: implausible seal seq used] ./internal/seatcommit exit 0; failing: NONE (not caught)
    first: ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	24.317s
[C1a/D5 a body of neither layout accepted] ./internal/seatcommit exit 1; failing: TestRecoveryRefusesASlotBodyThatIsNeitherLayout
    first: raw_compat_test.go:550: not a recovery refusal: <nil>
[C1b no usable head and no body link: the specific refusal removed (falls to the later seq-unknown refusal; expected equivalent in code)] ./internal/seatcommit exit 0; failing: NONE (not caught)
    first: ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	24.357s
[R17 sealchain exhaustion check removed] ./internal/sealchain exit 1; failing: TestRawWriterRefusesSequenceExhaustionBeforeSave
    first: raw_test.go:29: exhaustion = <nil> (<nil>), saved=true
[R17 sealchain exhaustion check removed] ./internal/seatcommit exit 1; failing: TestOrdinaryWriterRefusesAnExhaustedVerifiedLedgerWithoutChangingEvidence
    first: raw_compat_test.go:303: exhausted write = <nil>
[R17 seatcommit words: exhaustion not wrapped] ./internal/seatcommit exit 1; failing: TestOrdinaryWriterRefusesAnExhaustedVerifiedLedgerWithoutChangingEvidence
    first: raw_compat_test.go:303: exhausted write = the ledger's sequence is exhausted at link 9223372036854775807
[R18/M2.ii recordVerified MaxInt refusal removed] ./internal/seatcommit exit 1; failing: TestVerifiedRecoveryAtMaximumSequenceRefusesBeforeWriting
    first: raw_compat_test.go:509: verified maximum = recover_write_failed: commit ledger s1: the ledger's sequence is exhausted: the ledger's sequence is exhausted at link 9223372036854775807
[R18 words: recordVerified MaxInt words changed] ./internal/seatcommit exit 1; failing: TestVerifiedRecoveryAtMaximumSequenceRefusesBeforeWriting
    first: raw_compat_test.go:509: verified maximum = recover_slot_invalid: the commits ledger of seat s1 verifies at the top; no successor can be written
[D1.c tamper-path MaxInt check removed] ./internal/seatcommit exit 1; failing: TestTamperedBodyUnderVerifiedMaximumHeadKeepsD1cRefusal
    first: raw_compat_test.go:526: D1.c refusal = recover_write_failed: commit ledger s1: the ledger's sequence is exhausted: the ledger's sequence is exhausted at link 9223372036854775807
[R20/M3 no usable head: always migrating (round 2 behaviour)] ./internal/seatcommit exit 1; failing: TestRawRecoveryWithGarbageHeadNeverRecordsAMigration
    first: raw_compat_test.go:446: migration decisions after recovery = [2]
[R20/P3 no usable head: never migrating] ./internal/seatcommit exit 1; failing: TestRecoveryMigratesALegacyBodyWithNoUsableHead
    first: raw_compat_test.go:712: open /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryMigratesALegacyBodyWithNoUsableHead3302304544/001/amq/sess/extensions/amq-squad/scheduler/decisions.jsonl: no such file or directory
[R20 usable head: migrating follows the body, not the head] ./internal/seatcommit exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:864: slot link = seq 1 tag 0c6516451dcda9e81c2a1be13e10df63533d6108f4f406dc7d707dbe2dc852de migrating true; want seq 1 tag from head (0c6516451dcda9e81c2a1be13e10df63533d6108f4f406dc7d707dbe2dc852de) migrating false
[R21/M4 body seal reported as claimed_head] ./internal/seatcommit exit 1; failing: TestADecisionByAlarmAnswersAHoldNoSlotCan, TestCopiedHeadClaimsAboveTheInteroperableBoundUseTheBody, TestRawRecoveryWithGarbageHeadNeverRecordsAMigration, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryTreatsNonPositiveHeadClaimsAsUnknownForBothLayouts, TestRecoveryTreatsTheMaximumClaimedSequenceAsUnknown
    first: raw_compat_test.go:269: maximum claim recovery = {Handle:s1 AlreadyRecovered:false Verified:false Alarm: Slot:/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryTreatsTheMaximumClaimedSequenceAsUnknown65083573/001/amq/sess/extensions/amq-squad/quarantine/commits.s1.sequnknown OldSe
[R21 claimed head reported as body_seal] ./internal/seatcommit exit 1; failing: TestLargestPlausibleCopiedHeadClaimLeavesOneOrdinaryVerifiedWrite, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence
    first: raw_compat_test.go:239: recovery = {Handle:s1 AlreadyRecovered:false Verified:false Alarm: Slot:/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence3715768919/001/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq7 OldSeq:7 Ne
[R21 verified recovery seq_from changed] ./internal/seatcommit exit 0; failing: NONE (not caught)
    first: ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	22.256s
[R22/M5 the recovery seam runs before the migration record] ./internal/seatcommit exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:885: migration seqs after crash = [], want [4]
[R23/M6 new alarm file: directory not synced] ./internal/seatcommit exit 1; failing: TestAlarmAppendReturnsNewDirectorySyncFailure, TestAlarmAppendSyncsNewDirectoryEntryOnce
    first: alarms_test.go:154: syncs: file=2 dir=0 path="", want 2, 1, "/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestAlarmAppendSyncsNewDirectoryEntryOnce2219422703/001/extensions/amq-squad/commits"
[R23 directory synced on every append] ./internal/seatcommit exit 1; failing: TestAlarmAppendSyncsNewDirectoryEntryOnce
    first: alarms_test.go:154: syncs: file=2 dir=2 path="/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestAlarmAppendSyncsNewDirectoryEntryOnce313751249/001/extensions/amq-squad/commits", want 2, 1, "/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestAlarmAppendSyncsNewDirectoryEntryOnce313751249/001/ext
[R23 directory sync error dropped] ./internal/seatcommit exit 1; failing: TestAlarmAppendReturnsNewDirectorySyncFailure
    first: alarms_test.go:165: not a recovery refusal: <nil>
[R14 evidence error not named for-each-ref] ./internal/seatcommit exit 1; failing: TestRecoveryRefusesWhenEvidenceRefsCannotBeObserved
    first: recover_test.go:240: evidence refusal = recover_base_unobserved: read the evidence refs: injected observation failure
[D1.a named seq ignores the claimed head seq] ./internal/seatcommit exit 1; failing: TestADecisionByAlarmAnswersAHoldNoSlotCan, TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestKnownRawBodyWithImplausibleSequenceContinuesFromVerifiedHead, TestKnownRawBodyWithImplausibleSequenceStillChecksCarriedEntries, TestLargestPlausibleCopiedHeadClaimLeavesOneOrdinaryVerifiedWrite, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence, TestRenamedSlotMustAgreeWithItsCopiedHeadClaim, TestVerifiedRecoveryRefusesASlotAboveTheLiveLink
    first: raw_compat_test.go:239: recovery = {Handle: AlreadyRecovered:false Verified:false Alarm: Slot: OldSeq:0 NewSeq:0 Prev: SeqFrom: BodySHA256: HeadSHA256: Base: Carried:[]}, recover_slot_invalid: /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutRe
[D1.b claimed seq left out of the max] ./internal/seatcommit exit 1; failing: TestLargestPlausibleCopiedHeadClaimLeavesOneOrdinaryVerifiedWrite, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence, TestVerifiedRecoveryRefusesASlotAboveTheLiveLink
    first: raw_compat_test.go:239: recovery = {Handle:s1 AlreadyRecovered:false Verified:false Alarm: Slot:/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence2701990230/001/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq7 OldSeq:7 Ne
[D2 interrupted migration written as migrating] ./internal/seatcommit exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:740: migration decisions = 2: {"at":"2026-11-10T02:21:32.657887Z","kind":"ledger_layout_migrated","ledger_kind":"commits","seat":"s1","seq":2,"reason":"legacy commit ledger migrated to raw-bytes-v1 at link 2"}
[D2 interrupted migration record not written] ./internal/seatcommit exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:738: open /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryContinuesAfterAnInterruptedRawMigration48399180/001/amq/sess/extensions/amq-squad/scheduler/decisions.jsonl: no such file or directory
[D2 no interrupted-migration branch in link] ./internal/seatcommit exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:733: recovery after interrupted migration = {Seq:3 Prev:94437889d0c47f9088ccd6a76e836a16f922504a9aba1103cda632f80e9f4a8d Tag:2f1f6bcd54d92a2bd0d8a532fd21cc920fcba4368c79a241d77cea5997f48d08 Version:(devel) Schema:1}, <nil>
[D3 retry does not complete the migration] ./internal/seatcommit exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRetryCompletesARecoveredRawBodyAboveALegacyHead
    first: raw_compat_test.go:802: completed head = "", <nil>
[D4 evidence seq error ignored] ./internal/seatcommit exit 1; failing: TestRecoveryRefusesWhenEvidenceRefsCannotBeObserved
    first: recover_test.go:239: not a recovery refusal: <nil>
[P1 no compat branch in recordAlarm] ./internal/seatcommit exit 1; failing: TestRecoveryIsTamperOnlyForCompatibilityRefusals
    first: raw_compat_test.go:119: alarm recovery refusal = recover_alarm_unverified: the commits ledger of seat s1 cannot be read as a document (commit ledger s1: newer_writer: the seat's commit record was written by a newer amq-squad (v9.0.0); install it with amq-squad up --binary <that build>: unknown field
[P1 no compat case in recoverLedger] ./internal/seatcommit exit 1; failing: TestRecoveryIsTamperOnlyForCompatibilityRefusals
    first: raw_compat_test.go:115: recovery refusal = recover_write_failed: the commits ledger of seat s1 cannot be read: commit ledger s1: newer_writer: the seat's commit record was written by a newer amq-squad (v9.0.0); install it with amq-squad up --binary <that build>: unknown field "future" (writer schema
[P2 verified recovery of an interrupted migration: seq 0] ./internal/seatcommit exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision
    first: raw_compat_test.go:767: open /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestInterruptedMigrationRecoveryDedupesTheOriginalDecisionverif820257856/001/amq/sess/extensions/amq-squad/scheduler/decisions.jsonl: no such file or directory
[P2 verified recovery of an interrupted migration: -1 (migrating write)] ./internal/seatcommit exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision
    first: raw_compat_test.go:769: migration decisions = 1: {"at":"2026-11-10T02:24:40.518516Z","kind":"ledger_layout_migrated","ledger_kind":"commits","seat":"s1","seq":3,"reason":"legacy commit ledger migrated to raw-bytes-v1 at link 3"}
[P2 verified recovery of a legacy body not migrating] ./internal/seatcommit exit 1; failing: TestVerifiedLegacyRecoveryAlsoRecordsItsRawMigration
    first: raw_compat_test.go:1139: open /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestVerifiedLegacyRecoveryAlsoRecordsItsRawMigration1087940453/001/amq/sess/extensions/amq-squad/scheduler/decisions.jsonl: no such file or directory
[R1 recoverWrite always migrating] ./internal/seatcommit exit 1; failing: TestInterruptedMigrationRecoveryDedupesTheOriginalDecision, TestKnownRawBodyWithImplausibleSequenceContinuesFromVerifiedHead, TestKnownRawBodyWithImplausibleSequenceStillChecksCarriedEntries, TestRawRecoveryWithGarbageHeadNeverRecordsAMigration, TestRecoveryContinuesAfterAnInterruptedRawMigration, TestRecoveryContinuesTheChainAfterTheQuarantinedSeq, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:387: raw chain recorded migration: {"at":"2026-11-10T02:25:24.108782Z","kind":"ledger_layout_migrated","ledger_kind":"commits","seat":"s1","seq":2,"reason":"legacy commit ledger migrated to raw-bytes-v1 at link 2"}
[R1 recoverWrite never migrating] ./internal/seatcommit exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRecoveryMigratesALegacyBodyWithNoUsableHead, TestRecoveryMigratesALegacyLedgerThroughTheRawWriter, TestRetryCompletesARecoveredRawBodyAboveALegacyHead, TestVerifiedLegacyRecoveryAlsoRecordsItsRawMigration
    first: raw_compat_test.go:678: open /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryMigratesALegacyLedgerThroughTheRawWriter3569391666/001/amq/sess/extensions/amq-squad/scheduler/decisions.jsonl: no such file or directory
[R2 the already-recovered case disabled] ./internal/seatcommit exit 1; failing: TestAVerifiedRecoveryRetriedReportsItsLink, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRecoveryDecidedAgainIsAnsweredFromTheLedger, TestRecoveryMigratesALegacyLedgerThroughTheRawWriter, TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence, TestRetryCompletesARecoveredRawBodyAboveALegacyHead
    first: raw_compat_test.go:250: retry = {Handle:s1 AlreadyRecovered:false Verified:true Alarm: Slot:/private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryUsesTheClaimedTamperedHeadSeqWithoutReusingASequence2715309180/001/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq7 OldSeq:7 NewSeq
[slot-stale comparison removed] ./internal/seatcommit exit 1; failing: TestADecisionByAlarmAnswersAHoldNoSlotCan, TestAHoldOnAnEarlierSlotIsAnsweredAfterALaterRecovery, TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm, TestRecoveryRefusesWhatItCannotCheck
    first: raw_compat_test.go:940: not a recovery refusal: <nil>
[R15 CheckRaw immediate-successor case removed] ./internal/seatcommit exit 1; failing: TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotForm
    first: raw_compat_test.go:895: raw successor was not admitted: commit ledger s1: ledger_tampered: commits.s1 is at link 2, its head records link 1; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRecoveryCrashBeforeTheHeadHasTheRuledOutcomeForEverySlotFor
[P4/R6 prepareError wraps every error as a tamper alarm] ./internal/schedproc exit 1; failing: TestPreparationClassifiesRealTamperedAndNewerWriterLedgersSeparately, TestPrepareErrorWrapsOnlyTamperingAsALedgerAlarm
    first: prepare_quarantine_test.go:63: newer-writer preparation = the commit ledger of seat newer: newer_writer: commit ledger newer: newer_writer: the seat's commit record was written by a newer amq-squad (v9.0.0); install it with amq-squad up --binary <that build>: unknown field "future" (writer schema 2,
finished 04:27 IST
```

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

Phase one, on `f3d72b7f` (the merge of `origin/v3` at `3966afb7`, then
the anchored prefix; this note is the only change after it), each from
the worktree root; the whole-module test, `npm test` with lint, and `make
ci` under the gate lock, one after another, each printing `f3d72b7f`
inside the lock:

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'`: exit 0, 56 packages `ok`, no `FAIL`.
- `gate-lock.sh 904-pi -- sh -c 'cd pi && npm test && node
  scripts/lint.mjs'`: exit 0, `ℹ tests 119`, `ℹ pass 118`, `ℹ fail 0`,
  `ℹ skipped 1`, `lint ok: 30 files`.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: exit 0, 70 `ok` (37 from the cache), no
  `FAIL`.
- `make fmt-check` with Go 1.26's gofmt first on PATH: exit 0;
  `GOOS=windows go build ./internal/seatcommit/`: exit 0; the redact
  check and the skill checks on this note: exit 0.

Every gate passed on its first run, none repeated.

Before that, phase one on `a57e9627` (Codex on `50986a71`; the note
`a817fed3` the only change after it), each from the worktree root; the whole-module test,
`npm test` with lint, and `make ci` under the gate lock, one after
another, each printing `a57e9627` inside the lock:

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'`: exit 0, 56 packages `ok`, no `FAIL`.
- `gate-lock.sh 904-pi -- sh -c 'cd pi && npm test && node
  scripts/lint.mjs'`: exit 0, `ℹ tests 119`, `ℹ pass 118`, `ℹ fail 0`,
  `ℹ skipped 1`, `lint ok: 30 files`.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: exit 0, 70 `ok` (48 from the cache), no
  `FAIL`.
- `make fmt-check` with Go 1.26's gofmt first on PATH: exit 0;
  `GOOS=windows go build ./internal/seatcommit/`: exit 0 (the non-unix
  `openAppendNoFollow` builds); the redact check and the skill checks on
  this note: exit 0.

Every gate passed on its first run, none repeated.

Before that, phase one on `97a05cc4` (the hold release file; the note
`50986a71` the only change after it), each from the worktree root; the whole-module test,
`npm test` with lint, and `make ci` under the gate lock, one after
another, each printing `97a05cc4` inside the lock:

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'`: exit 0, 56 packages `ok`, no `FAIL`.
- `gate-lock.sh 904-pi -- sh -c 'cd pi && npm test && node
  scripts/lint.mjs'`: exit 0, `ℹ tests 119`, `ℹ pass 118`, `ℹ fail 0`,
  `ℹ skipped 1`, `lint ok: 30 files`.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: exit 0, 70 `ok` (34 from the cache), no
  `FAIL`.
- `make fmt-check` with Go 1.26's gofmt first on PATH: exit 0;
  `GOOS=windows go build ./internal/seatcommit/`: exit 0; the redact
  check and the skill checks on this note: exit 0.

Every gate passed on its first run, none repeated. `eff2fea1` (b and c)
ran the whole-module gate and `make ci` under the lock too, both exit 0.

Before that, phase one on `cb4f7d13` (the refusal words dropped; the note
`8cffafe7` the only change after it), each from the worktree root; the whole-module
test, `npm test` and `make ci` under the gate lock, one after another,
each printing `cb4f7d13` inside the lock:

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'`: exit 0, 56 packages `ok`, no `FAIL`.
- `gate-lock.sh 904-pi -- sh -c 'cd pi && npm test'`: exit 0, `ℹ tests
  119`, `ℹ pass 118`, `ℹ fail 0`, `ℹ skipped 1` (the refusal words' test
  gone with its table); `cd pi && node scripts/lint.mjs`: exit 0.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: exit 0, 70 `ok` (51 from the cache), no
  `FAIL`, the pi counts as above.
- `make fmt-check` with Go 1.26's gofmt first on PATH: exit 0;
  `GOOS=windows go build ./internal/seatcommit/`: exit 0; the redact
  check and the skill checks on this note: exit 0.

Every gate passed on its first run, none repeated.

Before that, phase one on `37379e5b` (the reviewer's rulings on Codex's
pass on `254f2c2c`; the note `ff7b5530` the only change after it), each from the
worktree root; the whole-module test, `npm test` and `make ci` under the
gate lock, one after another, each printing `37379e5b` inside the lock:

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'`: exit 0, 56 packages `ok`, no `FAIL`.
- `gate-lock.sh 904-pi -- sh -c 'cd pi && npm test'`: exit 0, `ℹ tests
  120`, `ℹ pass 119`, `ℹ fail 0`, `ℹ skipped 1` (one test more: the
  refusal words); `cd pi && node scripts/lint.mjs`: exit 0.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: exit 0, 70 `ok` (34 from the cache), no
  `FAIL`.
- `make fmt-check` with Go 1.26's gofmt first on PATH: exit 0;
  `GOOS=windows go build ./internal/seatcommit/`: exit 0; the redact
  check and the skill checks on this note: exit 0.

Every gate passed on its first run, none repeated.

Before that, phase one on `40254653` (Codex on `254f2c2c`; the note
`16c62fb4` the only change after it), each from the worktree root; the whole-module test,
`npm test` and `make ci` under the gate lock, one after another, each
printing `40254653` inside the lock:

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'`: exit 0, 56 packages `ok`, no `FAIL`.
- `gate-lock.sh 904-pi -- sh -c 'cd pi && npm test'`: exit 0, `ℹ tests
  119`, `ℹ pass 118`, `ℹ fail 0`, `ℹ skipped 1`; `cd pi && node
  scripts/lint.mjs`: exit 0.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: exit 0, 70 `ok` (35 from the cache), no
  `FAIL`.
- `make fmt-check` with Go 1.26's gofmt first on PATH: exit 0;
  `GOOS=windows go build ./internal/seatcommit/`: exit 0; the redact
  check and the skill checks on this note: exit 0.

Every gate passed on its first run, none repeated. The two new schedproc
rows also passed under `go test -race` before the commit.

On `254f2c2c` (the plan reviewer's gate rule: `make ci` runs fmt-check,
vet and the Go tests over every package, so it reads the changed
`tamper_test.go`), from the worktree root:

- `gate-lock.sh 904-ci-r2 -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'` (printing `254f2c2c` inside the lock):
  exit 0, 70 `ok` (50 from the cache), no `FAIL`, `internal/scheduler`
  run fresh (25.4 s), `ℹ tests 119`, `ℹ pass 118`, `ℹ fail 0`, `ℹ skipped
  1`.
- `PATH=<GOMODCACHE>/golang.org/toolchain@v0.0.1-go1.26.10.darwin-arm64/bin:$PATH
  make fmt-check`: exit 0.
- `npm test` and `GOOS=windows go build ./internal/seatcommit/` stand from
  `28051cd6` (below): `28051cd6..254f2c2c` touches only
  `internal/scheduler/tamper_test.go` and this note (`git diff
  --name-only`), no `.ts` file and nothing in `internal/seatcommit`.

On `c83e6ef3` (one test row after `5d2a576a`; this note the only change
after it), the gates the lead named for a row and a note:
`go test -count=1 ./internal/scheduler/`: exit 0; `gate-lock.sh 904-gates
-- sh -c 'go build ./... && go vet ./... && go test -count=1 ./...'` (the
whole module, under the gate lock, printing `c83e6ef3`): exit 0, 56
packages `ok`, no `FAIL`; `make fmt-check` with Go 1.26's gofmt: exit 0;
the redact check and the skill checks on this note: exit 0. The other
gates stand from `28051cd6` below (no file they read changed).

Phase one, on `28051cd6` (the alarm id; `5d2a576a`'s note the only change
after it), each from the worktree root. The whole-module test, `npm
test` and `make ci` ran under the gate lock, one after another, each
printing the head (`28051cd6`) inside the lock:

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'` (the whole module): exit 0, 56 packages `ok`, no
  `FAIL` (the lock taken after 675 s of waiting).
- `gate-lock.sh 904-pi -- sh -c 'cd pi && npm test'`: exit 0, `ℹ tests
  119`, `ℹ pass 118`, `ℹ fail 0`, `ℹ skipped 1` (one test more: the
  alarm's words); `cd pi && node scripts/lint.mjs`: exit 0.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: exit 0, 70 `ok` (34 from the cache of
  the whole-module run), no `FAIL`, the same four pi counts.
- The format gate, Go 1.26's gofmt first on PATH, `make fmt-check`: exit
  0; `GOOS=windows go build ./internal/seatcommit/`: exit 0;
  `go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`:
  exit 0 and `make skill-command-check skill-invocation-check`: exit 0
  (both on this note).

Every gate passed on its first run, none repeated.

Before that, phase one on `1cd21fa1` (the baseline removed; the note
`c5754be4` the only change after it), each from the worktree root. The whole-module test,
`npm test` and `make ci` ran under the gate lock, one after another, each
printing the head (`1cd21fa1`) inside the lock:

- `gate-lock.sh 904-gates -- sh -c 'go build ./... && go vet ./... && go
  test -count=1 ./...'` (the whole module): exit 0, 56 packages `ok`, no
  `FAIL`.
- `gate-lock.sh 904-pi -- sh -c 'cd pi && npm test'`: exit 0, `ℹ tests
  118`, `ℹ pass 117`, `ℹ fail 0`, `ℹ skipped 1`; `cd pi && node
  scripts/lint.mjs`: exit 0.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: exit 0, 70 `ok` (34 from the cache of
  the whole-module run), no `FAIL`, the same four pi counts.
- The format gate, Go 1.26's gofmt first on PATH, `make fmt-check`: exit
  0; `GOOS=windows go build ./internal/seatcommit/`: exit 0;
  `go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`:
  exit 0 and `make skill-command-check skill-invocation-check`: exit 0
  (both on this note).

Every gate passed on its first run, none repeated.

Before that, phase one on `e1fc443c` (the masked key's row after `ede2226c`; the
notes `c267382d` and `f1edd375` the only changes after it), each from the worktree root. The
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
  'blocked'`, `expected: 'ran'`; 8.57 s). That run did not hold the gate
  lock (the rule did not cover `npm test` yet) and ran while another
  agent's `make ci` held it. Run once more, also outside the lock, exit
  0: `ℹ tests 118`, `ℹ pass 117`, `ℹ fail 0`, `ℹ skipped 1`. (On
  `e1fc443c` above, `npm test` ran under the lock and passed first time.) `cd pi && node scripts/lint.mjs`: exit
  0.
- `gate-lock.sh 904-ci -- sh -c 'PATH=<job tmp>/pyvenv/bin:$PATH
  NPM_FLAGS=--offline make ci'`: the first run, which held the gate lock
  (`904-ci`), exit 2: `make: *** [test] Error 1`, its `go test` stopped on
  the known load flake `internal/seatlaunch`
  `TestPiSeatPlansThroughTheUpstreamAdapter`, failing line
  `amqpi_test.go:75: outcome unsupported (launcher_not_available)`
  (`--- FAIL: TestPiSeatPlansThroughTheUpstreamAdapter (5.18s)`; already
  on the 3.2.x list), with the host's load from package-scoped runs the
  lock does not cover; run once more under the lock, exit 0: 70 `ok` (55 from the cache of the
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
  its evidence kept (0c-A) and this recovery the way back. The decision by
  alarm no longer changes the ledger body (`recovered.alarm` and
  `alarm_recoveries`, which `28051cd6` and `37379e5b` added, are gone in
  `eff2fea1` and `97a05cc4`): it is a line of the seat's hold release file,
  `commits/<handle>.alarms.jsonl`, which no released build opens, sealed
  over its raw bytes so later builds can add fields. A rollback to 3.1.2 or
  rc.1 leaves the file unread and the ledger as a slot recovery left it.
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
- Residual risk for rc.2 (the reviewer's acceptance on `ede2226c`): the
  slot-reuse replay. A slot reused by the quarantine's no-op for an
  identical refusal can predate its hold, so the record of an earlier
  recovery from that slot, put back by hand after the same refused bytes
  were put back, releases the new hold; the baseline does not stop it
  (the refused bytes predate the record). It needs two deliberate
  whole-file restores by a process running as the same user, outside the
  threat model 0c-A stated ("The threat line" in `A-quarantine.md`).
  For the 3.2.x list, by name: "a quarantine slot named by a recovery
  record is never reused; identical bytes refused again get a new .k
  slot".
- A hold whose document changed again after its alarm is not answered by
  a recovery from the later slot (the recovery must name the slot whose
  bytes are the live document's). The lead then releases the hold by its
  alarm (`scheduler recover --seat H --alarm ID`, on the ledger that now
  verifies). The verified mode on the hold's own slot does it only when
  the chain is at or past that slot's seq; an earlier version of this
  note said it always did, which was wrong for a later slot at a lower
  seq (Codex's delta pass on `c267382d`).
- For the 3.2.x list, by name: `internal/buzz` `TestOwnerScriptDryRun`,
  a 5 min wall-clock budget over the owner-script dry run on a local relay
  (`enroll_test.go:447: hung: no signal for "the attach verifies the thread
  answer"`); it stopped the first whole-module run on `8eeeaf24` under a
  load average of 10.8 and passed the run after it. Outside this PR's
  files.
