# 3.2.0-0c-A: a tamper alarm preserves its evidence

Brief: `brief-320-0c-A-quarantine.md` (part A of 3.2.0-0c, draft 2 approved).
Branch `v3-320-0c-A`, cut from `origin/v3` at `10449a51`; `origin/v3` was
merged in twice (#887 as `f5712d62`, #891 as `648b8c55`, no conflict in
either; #891's schedproc change is `amq.go`, `slots.go` and
`slots_test.go`, not `prepare.go`). PR #893. Declared files only:
`internal/seatcommit/`, `internal/schedproc/`,
`docs/v3-spike/320-0c-quarantine/`.

The history of the design on #893: the first build (`f6927bd7`) copied
files beside each other with `O_EXCL`; the plan reviewer found two gaps
(a planted link suppressed the copy; a second different body under one
seq was dropped) and then one edge (a head already in a fresh slot);
Codex's P1 (the copy re-read the files after the check) and three more
items (path races, slot-set consistency, concurrent refusers) followed;
the plan reviewer ruled all four into this PR with one design, built in
`9e70e181` and described below. Nothing is released on the earlier
layouts; no migration; 0c-B reads this one.

## Why

On 2026-11-05 the rc.1 scheduler refused the reviewer seat's commit ledger
(`ledger_tampered`, link 150) on every tick for 65 minutes. The cause for
that body is version skew (3.1.1's `attempt` in `LedgerDiscard`, which
rc.1 drops on decode and re-encodes away, so the tag fails). The seq-150
body was overwritten by the next writer's re-seal 2 to 4 s after the
restart and is lost: one seal per document, no history. Whatever a
refusal's cause, its evidence must survive the next writer.

## The rule as built

**The read that refuses quarantines, and copies the bytes it refused.**
`seatcommit.ReadLedger` is `ReadLedgerAs(root, handle, "read")`;
`ReadLedgerAs` runs the check (`readLedger`: the file, its schema and
handle, the seal under the session seed, the signed head through
`sealchain.Check`) and, on `sealchain.ErrTampered`, calls `quarantine`
with a `ledgerRaw`: the body and head bytes the check read, with their
presence, and the note of one it could not read as a regular file. The
quarantine never opens the live files (Codex's P1): a writer that
replaces the body between the refusal and the copy cannot make the alarm
archive its body instead. The body bytes are exactly the checked ones;
the head bytes are read right before `sealchain`'s own read of the head
(`Absent`, `Check`), the closest this package gets without a `sealchain`
change (outside the set). A test seam `afterLedgerRefusal` sits between
the refusal and the copy (row A10).

**The source reads never follow a link.** The ledger and its head are
opened `O_RDONLY|O_NOFOLLOW|O_NONBLOCK` and the descriptor must be a
regular file (`readNoFollow`, `quarantine_unix.go`; `Lstat` then read off
unix). A ledger or head that is a symlink, to the seed or to anything, is
a refusal in its own right, `ledger_tampered: <path> is a symlink`,
before `sealchain` would follow it; nothing is read or copied through it,
and the sidecar says `body: absent (<path> is a symlink (not copied))`
(row A11).

**The layout: a directory per refusal, published by one rename.**

    <root>/extensions/amq-squad/quarantine/commits.<handle>.seq<N>[.k]/
        body.json        the refused body, byte for byte (when present)
        head.json        its head, byte for byte (when present)
        quarantine.txt   the sidecar

N is the head file's `seq`, decoded without verifying the head (its tag
may be what failed); `unknown` when the head does not decode or is
absent, so `commits.<handle>.sequnknown/`. The set is written whole into
a fresh temp directory beside the slots (`.copying-<hex>/`) and published
by one rename to the slot name, so no reader ever sees a partial set. A
rename onto a directory that is not empty fails, which is what makes
publication exclusive.

**The staged copy is written through the temp directory's own
descriptor** (Codex's P2 on `6c2b48fc`, commit `90dd2e77`): right after
`Mkdirat` the temp directory is opened `O_DIRECTORY|O_NOFOLLOW` relative
to the held root and checked on its descriptor (a directory, the
caller's uid); every staged file is created (`O_CREAT|O_EXCL|O_NOFOLLOW`,
0600, fsynced) and later unlinked relative to that descriptor, never as
`<tmp>/body.json` relative to the root, where `O_NOFOLLOW` would have
protected the last component only and a same-user swap of the temp
directory for a link would have redirected the writes outside the
quarantine (row A14). The staged directory is fsynced before the rename
and the root after it (the second P2: a crash after a successful publish
could otherwise leave a published slot missing files; row A15 records the
order through a trace seam). A test seam `afterTempMkdir` sits between
the mkdir and the open.

**Identical is a no-op, different is the next k.** Before the rename the
slot is looked at: absent, the rename is tried (and a rename that finds
the slot taken by a concurrent refuser looks once more); present, the
whole published set is compared, the presence of each file and its bytes,
the sidecar without its `time:`, `leftover:` and `writer:` lines.
Identical drops the temp directory and names the published slot (the read
path refuses on every tick: one copy; a prepare and then a writer refusing
the same bytes with the same error are one refusal, and the slot records
the first's verb and time). Different tries `commits.<h>.seq<N>.2`, then
`.3`, up to 100 slots per seq. A slot that is present but is not a
published set (an empty directory, a set without `quarantine.txt`) is a
named copy failure, never compared through, never renamed over (row A9).
Leftovers of a copy that did not finish (`.copying-*` of a crash) are
listed in the next sidecar as `leftover:` lines, never reused, never
removed.

**Descriptors, not paths.** The quarantine directory is created when
absent (`MkdirAll`) and then opened once, `O_RDONLY|O_DIRECTORY|O_NOFOLLOW`
(`golang.org/x/sys/unix`, a dependency already through `atomicfile`);
the checks run on that descriptor's `fstat`: a directory, mode 0700,
owned by the process's uid. Everything after is relative to it:
`Mkdirat`, `Openat` with `O_CREAT|O_EXCL|O_NOFOLLOW` for each file,
`Renameat`, `Unlinkat`, and `Openat` with `O_DIRECTORY|O_NOFOLLOW` for an
existing slot and `O_NOFOLLOW` for each file read back for the compare,
each read-back checked regular on its descriptor. A link anywhere on the
way (the quarantine directory itself, a slot, a staged directory, a file
in a slot) refuses the open; the error words what is there (`is a
symlink`, else `is not a directory`; the `Lstat` behind the wording
decides nothing). The owner check (`ownedByCaller`, the fstat result's
uid against `os.Getuid`) has a mismatch row built from a `Stat_t`
(`TestQuarantineDirectoryOwnedByAnotherUIDIsRefused`), since a directory
of another uid cannot be made on disk without root.

Off unix, `quarantine_other.go` is a compatibility version, stated
plainly: the same steps run by path with an `Lstat` before each open, so
between the `Lstat` and the open the path can change; it has no owner
check and no directory fsync (`sync` is a no-op; the rename is still one
`os.Rename`). It keeps the package building for `GOOS=windows`; 3.0
hosts are macOS and Linux, where the descriptor form runs.

**Every failure is named, never softened.** A copy that cannot be made
is `<refusal>; the quarantine copy failed: <why>`: `the quarantine
directory <dir> is a symlink` / `has mode 0755, want 0700` / `is owned by
uid X, not Y`, `<slot> is a symlink`, `<slot> is not a directory`,
`<slot> is not a quarantine slot (no quarantine.txt)`, `<base> has 100
slots already`. A copy that was made is `<refusal>; the refused document
is quarantined at <dir>/commits.<h>.seq<N>[.k]`. Either way the error
wraps `sealchain.ErrTampered` (`%w`), so every `errors.Is` and every
`ReasonLedgerTampered` mapping is unchanged.

**The sidecar** (`quarantine.txt`):

    amq-squad commits ledger quarantine
    time: <RFC3339Nano, UTC>
    seat: <handle>
    writer: <verb>
    binary: <version>
    error: <the refusal's text>
    body: body.json sha256 <hex>   | body: absent | body: absent (<why, not copied>)
    head: head.json sha256 <hex>   | head: absent | head: absent (<why, not copied>)
    leftover: .copying-<hex> (a copy that did not finish; kept, never reused)   (0..n lines)

**The write re-reads the disk under the lock.** `writeLedger(root, l,
verb)` is still the one point every write passes (each caller holds the
ledger lock). Before it seals, it calls `ReadLedgerAs(root, l.Handle,
verb)` on what is on disk: a refusal (or any read error other than
`os.ErrNotExist`) is returned as is, nothing is written, the head is not
moved. The caller's own earlier read is not enough: it happened before the
caller's git work. `sealchain.Write` keeps its own head check after this.

**The writer verb.** Every writer names itself on the sidecar: `seat
commit --base` (`RecordBase`, `RecordTarget`), `launch record base`
(`RecordWorktreeBase`), `archive attempt`, `clean seat`, `start task`,
`commit step` (the step's reads and writes, `verifyEvidence` included),
`commit step settle`, `exit check`, `scheduler prepare` (the prepare's
read, `internal/schedproc/prepare.go`), and `read` for every other
`ReadLedger` caller (`slots.go`, `uninstall`). A label only; it changes
no behaviour and does not split slots.

**The prepare.** `PrepareSeat` keeps today's behaviour: a read error is
`seat_prepare_failed`, no lease. It reads through `ReadLedgerAs(c.Root,
handle, "scheduler prepare")`, so the refusing read quarantines, and the
decision's reason (`the commit ledger of seat <h>: <err>`) carries the
slot path because the error does.

**What a quarantine is.** Evidence: never sealed, never read as a ledger,
never deleted by any verb of this task. `uninstall` lists an unknown
entry under `extensions/amq-squad/` in the session group (the same group
as the commits ledger), which is the owner's confirmed removal of the
session root; keeping the quarantine out of that plan would change
`internal/uninstall/`, outside the declared set, and is the lead's call.

**The binary version.** `seatcommit.BinaryVersion` is the sidecar's
version when set; the fallback is `runtime/debug`'s module version with
`vcs.revision`, `vcs.modified` and the Go version. The release build's
`-X main.version` is visible only to `main`; threading it into
`seatcommit.BinaryVersion` is one line in `internal/cli/`, outside this
task's set, for part B.

**Not changed.** `internal/sealchain/` (the head's seq is decoded in
`seatcommit` from the head bytes), `internal/cli/`, `internal/seatlaunch/`.
The commit step's handling of a write-time refusal is as before (`defer_`
with the error text, which now names the slot); the typed block raised
once is part B.

## Rows

| Row | What it proves | Test |
|---|---|---|
| A1 | A tampered document (the base hand-edited; valid JSON, the tag fails) followed by each writer of the fixture set, driven through the seatcommit function its caller uses, leaves the slot's `body.json` sha256-equal to the refused body and `head.json` byte-equal to the head; the writer refuses as `ledger_tampered`, its error names the slot, the sidecar names the writer and the body's digest; the document and head are unchanged after; one entry in the quarantine directory. One subtest per writer: `RecordBase`, `RecordWorktreeBase`, `RecordTarget`, `ArchiveAttempt`, `CleanSeat`, `StartTask`, `Run`. | `internal/seatcommit` `TestTamperedLedgerIsQuarantinedByEveryWriter` |
| A1 guard | From the source: every function of `seatcommit` that calls `writeLedger` (`ArchiveAttempt, RecordTarget, StartTask, cleanSeat, commitAndPush, recordBase, settle`), every exported function that reaches one (`ArchiveAttempt, CheckWiden, CleanSeat, RecordBase, RecordTarget, RecordWorktreeBase, Run, StartTask`; `CheckWiden` reaches `Run` as a dry run), and every reference to such an entry in the module's other non-test Go files (read, not changed: `internal/cli/seat_commit.go` RecordBase, RecordTarget, Run; `internal/cli/task_widen.go` ArchiveAttempt, CheckWiden; `internal/schedproc/prepare.go` CleanSeat, ArchiveAttempt; `internal/schedproc/slots.go` StartTask; `internal/seatlaunch/run.go` RecordWorktreeBase) must each map to an A1 row. A new writer fails this test until it has a row. | `internal/seatcommit` `TestEveryLedgerWriterHasAQuarantineRow` |
| A2 | An identical second refusal of the same seq (the same body and head, the read path's every-tick case) names the same slot and leaves its body, head and sidecar byte-identical; the quarantine directory holds that one slot (no `.2`, no leftover). | `internal/seatcommit` `TestIdenticalSecondRefusalKeepsTheFirstCopy` |
| A3 | A head that does not decode (`{not a head`) quarantines under `sequnknown`: the body copy is the document as it is, the head copy is the undecodable head, the sidecar carries the error; the head on disk is unchanged. | `internal/seatcommit` `TestUndecodableHeadQuarantinesUnderSeqUnknown` |
| A4 | The refusing error is `ledger_tampered` and ends with `; the refused document is quarantined at <slot>`, from the read that refuses (`ReadLedgerAs` with the prepare's verb; the sidecar names it). The scheduler half: the prepare on a tampered ledger logs `seat_prepare_failed` whose reason names `quarantined at <slot>`, the slot's body is the refused body, its sidecar says `writer: scheduler prepare`, the task is not leased. | `internal/seatcommit` `TestRefusalNamesTheQuarantinePath`; `internal/schedproc` `TestPrepareNamesTheQuarantineOfATamperedLedger` |
| A5 | The check runs under the lock. The first writer (`ArchiveAttempt`) is paused inside its critical section after its read and snapshot (the `beforeArchiveAttemptWrite` seam); the edit lands then; a second writer (`RecordTarget`, the blocking lock) does not return within 300 ms and no quarantine directory appears while the lock is held. Released, the first writer's `writeLedger` re-reads the disk, refuses and quarantines (it does not re-seal its stale in-memory copy over the edit); the second then refuses naming the same slot; the document and head on disk are the edited ones; one slot. | `internal/seatcommit` `TestWriteCheckRunsUnderTheLedgerLock` |
| A6 | Version skew as the tamper: a document a newer amq-squad wrote. Built from a real ledger with one discard entry: `attempt_note` added to the entry (this binary's `LedgerDiscard` already has 3.1.1's `attempt`, so the row uses a field this binary lacks, built the way `attempt` reached rc.1), sealed with the session's key as the next link over the body with the field (`sealchain.Seal`), the head moved to that link (`sealchain.WriteHead`). The row checks the premise both ways: the link verifies over the body with the field, and does not verify over this binary's decode of the same bytes. `CleanSeat` then refuses with `ledger_tampered: the tag of commits.s1 link <N> fails; the refused document is quarantined at ...`, the body copy is sha256-equal to the skewed bytes, the head copy equals the head, the sidecar carries that error text, the document is unchanged. | `internal/seatcommit` `TestVersionSkewIsQuarantinedLikeATamper` |
| A7 | A planted link never passes as a copy. A symlink where the slot would land (to a directory elsewhere): the refusal is still `ledger_tampered`, its error says `the quarantine copy failed: <slot> is a symlink` and not `quarantined at`, the link's target stays empty, the quarantine directory holds the planted link alone (no leftover). A symlink where the quarantine directory should be: `the quarantine copy failed: the quarantine directory <dir> is a symlink`, and the target directory stays empty. | `internal/seatcommit` `TestPlantedSymlinkIsAQuarantineFailureNotACopy` (`slot_path`, `directory`) |
| A8 | A second, different refused body under one head seq takes the next slot: the error names `commits.<h>.seq<N>.2`, the first slot (body, head, sidecar) is untouched, the second slot's body is the second refused body, its head copy the head, its sidecar names its own body's digest; the same second body again names the second slot again; two slots in all (not three). | `internal/seatcommit` `TestDifferentSecondBodyUnderOneSeqTakesTheNextSlot` |
| A9 | Something planted where the slot would land is a named copy failure, never `quarantined at`, and the refusal stands: an empty directory (`the quarantine copy failed: <slot> is not a quarantine slot (no quarantine.txt)`) and a regular file (`the quarantine copy failed: ...`); nothing is renamed over it, no leftover temp directory remains (the planted entry alone). | `internal/seatcommit` `TestPlantedSlotIsAQuarantineFailureNotACopy` (`empty_directory`, `regular_file`) |
| A10 | The copy is of the refused bytes, never a later re-read: the body is swapped on disk through the `afterLedgerRefusal` seam (a concurrent writer's stand-in) after the refusal and before the copy; the slot's body is the refused body (sha256 equal), its head the checked head, its sidecar carries the refused body's digest, and the swapped body stays on disk untouched. | `internal/seatcommit` `TestQuarantineHoldsTheRefusedBytesNotALaterSwap` |
| A11 | A ledger that is a symlink to the session seed: the refusal is `ledger_tampered: <ledger path> is a symlink` and names the slot; the slot has no `body.json`, its `head.json` is the head, its sidecar says `body: absent (<path> is a symlink (not copied))`; no file of the slot carries the seed's bytes; the planted link is unmoved. | `internal/seatcommit` `TestLedgerSymlinkedToTheSeedIsRefusedAndNeverCopied` |
| A12 | A body-absent refusal (the ledger deleted under a head past link 0) and then a tampered body under the same head are two refusals: two slots, the first with no `body.json` and `body: absent` in its sidecar, the second with the tampered body. | `internal/seatcommit` `TestAbsentBodyThenTamperedBodyUnderOneSeqGiveTwoSlots` |
| A14 | The staged directory swapped for a link after its mkdir (the `afterTempMkdir` seam, a same-user process's stand-in, replaces `.copying-<hex>` with a symlink to a directory elsewhere): the refusal is still `ledger_tampered`, its error says `the quarantine copy failed: the staged copy .copying-<hex>: <path> is a symlink` and not `quarantined at`; the link's target stays empty (no staged write followed the swap); the planted link is left in place. | `internal/seatcommit` `TestStagedDirectorySwappedForALinkIsAQuarantineFailure` |
| A15 | The staged directory is fsynced before it is renamed and the root after: the `quarantineTrace` seam records every `fsync` and `renameat` in order, and the row finds `fsync <quarantine>/.copying-<hex>` before `renameat .copying-<hex> -> commits.<h>.seq<N>` and `fsync <quarantine>` after it. | `internal/seatcommit` `TestStagedDirectoryIsFsyncedBeforeItsRename` |
| owner | The uid check refuses a quarantine directory owned by another uid by name and passes the caller's own, on a `Stat_t` built in the test (no root needed; a mismatch cannot be made on disk without it). | `internal/seatcommit` `TestQuarantineDirectoryOwnedByAnotherUIDIsRefused` (unix) |
| A13 | Eight concurrent identical refusers (readers of one tampered document at once through `ReadLedgerAs`, no lock) all name the one slot; the quarantine directory holds that slot alone (no `.2`, no leftover); its body is the refused body. | `internal/seatcommit` `TestConcurrentIdenticalRefusersPublishOneSlot` |

A6 in one line: a seat whose ledger a newer amq-squad wrote is blocked on
version skew (`ledger_tampered`, with its evidence kept) until the lead's
recovery, and the verifier fix of a later brief turns this case into the
distinct "written by a newer amq-squad" refusal; A6 does not call skew
tampering, it proves the evidence survives whatever the cause.

Rule 18: every row runs under a `t.TempDir` root (the seatcommit
fixture's `amq/sess`; the schedproc live rig's root), nothing touches a
real session root, no network. The seatcommit rows (A1 to A15, the A1
guard, the owner row) each set `HOME` to a temp dir with `t.Setenv`, since the fixture
runs git; the schedproc row (A4's scheduler half, `newLiveRig`) runs no
git and leaves `HOME` as it is.

## Revert runs

Each run on the committed tree `0ab0d35d` (the directory layout plus the
rows' exact slot match): the line flipped, the row run alone, the first
failing line verbatim (the temp-dir prefix as printed), then
`git checkout -- <file>` and the row re-run green. The runner is kept in
the job scratch, not committed. A flip is a compiling inverse of the
line's rule (a removed line that leaves a variable unused is flipped to a
form that compiles; the first attempts that did not compile are not
counted).

### A1

```
go test -count=1 -run ^TestTamperedLedgerIsQuarantinedByEveryWriter$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/ledger.go:386`
```
		err = quarantine(root, handle, verb, err, raw)
```
to:
```
		_ = raw // no quarantine
```
First failing line:
```
quarantine_test.go:181: RecordBase's error does not name the quarantine /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestTamperedLedgerIsQuarantinedByEveryWriterRecordBase2587645549/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1: "commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails"
```
`--- FAIL: TestTamperedLedgerIsQuarantinedByEveryWriter (1.31s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	1.667s`.

### A1 guard

```
go test -count=1 -run ^TestEveryLedgerWriterHasAQuarantineRow$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine_callers_test.go:58`
```
		"StartTask":      "StartTask",
```
(the line removed)
First failing line:
```
quarantine_callers_test.go:63: StartTask calls writeLedger and has no A1 row (quarantineWriters): add one
```
`--- FAIL: TestEveryLedgerWriterHasAQuarantineRow (0.06s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.300s`.

### A2

```
go test -count=1 -run ^TestIdenticalSecondRefusalKeepsTheFirstCopy$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:299`
```
		if ok != wantOK || !bytes.Equal(got, want) {
```
to:
```
		if ok != wantOK || !bytes.Equal(got, want) || true {
```
First failing line:
```
quarantine_test.go:224: second refusal: commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestIdenticalSecondRefusalKeepsTheFirstCopy1739451841/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.2
```
`--- FAIL: TestIdenticalSecondRefusalKeepsTheFirstCopy (0.23s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.445s`.

### A3

```
go test -count=1 -run ^TestUndecodableHeadQuarantinesUnderSeqUnknown$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:329`
```
		return quarantineSeqUnknown
```
to:
```
		return "0"
```
First failing line:
```
quarantine_test.go:552: CleanSeat under an undecodable head: commit ledger s1: ledger_tampered: the head of commits.s1 does not decode or its tag fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestUndecodableHeadQuarantinesUnderSeqUnknown425867487/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq0
```
`--- FAIL: TestUndecodableHeadQuarantinesUnderSeqUnknown (0.20s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.472s`.

### A4 the error

```
go test -count=1 -run ^TestRefusalNamesTheQuarantinePath$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:111`
```
	return fmt.Errorf("%w; the refused document is quarantined at %s", refusal, filepath.Join(dir, slot))
```
to:
```
	return fmt.Errorf("%w; the refused document is quarantined at %s", refusal, filepath.Dir(filepath.Join(dir, slot)))
```
First failing line:
```
quarantine_test.go:583: the error does not end with "; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRefusalNamesTheQuarantinePath2207108605/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1": "commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRefusalNamesTheQuarantinePath2207108605/002/amq/sess/extensions/amq-squad/quarantine"
```
`--- FAIL: TestRefusalNamesTheQuarantinePath (0.21s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.434s`.

### A4 the prepare

```
go test -count=1 -run ^TestPrepareNamesTheQuarantineOfATamperedLedger$ ./internal/schedproc/
```
Reverted `internal/schedproc/prepare.go:41`
```
	if _, err := seatcommit.ReadLedgerAs(c.Root, handle, "scheduler prepare"); os.IsNotExist(err) {
```
to:
```
	if _, err := seatcommit.ReadLedger(c.Root, handle); os.IsNotExist(err) {
```
First failing line:
```
prepare_quarantine_test.go:79: the sidecar does not name the prepare: <nil>
```
`--- FAIL: TestPrepareNamesTheQuarantineOfATamperedLedger (0.12s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.382s`.

### A5

```
go test -count=1 -run ^TestWriteCheckRunsUnderTheLedgerLock$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/ledger.go:482 to :484`
```
	if _, err := ReadLedgerAs(root, l.Handle, verb); err != nil && !os.IsNotExist(err) {
		return err
	}
```
(the line removed)
First failing line:
```
quarantine_test.go:657: the first writer did not refuse the edited body at its write: <nil>
```
`--- FAIL: TestWriteCheckRunsUnderTheLedgerLock (0.72s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.941s`.

### A6

```
go test -count=1 -run ^TestVersionSkewIsQuarantinedLikeATamper$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:185`
```
		set[quarantineBodyFile] = raw.body
```
(the line removed)
First failing line:
```
quarantine_test.go:749: open /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestVersionSkewIsQuarantinedLikeATamper2918242521/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq5/body.json: no such file or directory
```
`--- FAIL: TestVersionSkewIsQuarantinedLikeATamper (0.46s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.744s`.

### A7 the slot path

```
go test -count=1 -run ^TestPlantedSymlinkIsAQuarantineFailureNotACopy/slot_path$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine_unix.go:162`
```
	fd, err := unix.Openat(d.fd(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
```
to:
```
	fd, err := unix.Openat(d.fd(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
```
First failing line:
```
quarantine_test.go:505: the error does not say the copy failed ("the quarantine copy failed: /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedSymlinkIsAQuarantineFailureNotACopyslot_path1175406974/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1 is a symlink"): commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the quarantine copy failed: commits.s1.seq1 is not a quarantine slot (no quarantine.txt)
```
`--- FAIL: TestPlantedSymlinkIsAQuarantineFailureNotACopy (0.18s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.452s`.

### A7 the directory

```
go test -count=1 -run ^TestPlantedSymlinkIsAQuarantineFailureNotACopy/directory$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine_unix.go:91`
```
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
```
to:
```
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
```
First failing line:
```
quarantine_test.go:530: the error does not say the copy failed ("the quarantine copy failed: the quarantine directory /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedSymlinkIsAQuarantineFailureNotACopydirectory3510928665/002/amq/sess/extensions/amq-squad/quarantine is a symlink"): commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the quarantine copy failed: the quarantine directory /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedSymlinkIsAQuarantineFailureNotACopydirectory3510928665/002/amq/sess/extensions/amq-squad/quarantine has mode 0755, want 0700
```
`--- FAIL: TestPlantedSymlinkIsAQuarantineFailureNotACopy (0.20s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.427s`.

### A8

```
go test -count=1 -run ^TestDifferentSecondBodyUnderOneSeqTakesTheNextSlot$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:299`
```
		if ok != wantOK || !bytes.Equal(got, want) {
```
to:
```
		if false && (ok != wantOK || !bytes.Equal(got, want)) {
```
First failing line:
```
quarantine_test.go:261: refusal 1 of the second body does not name the second slot: commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDifferentSecondBodyUnderOneSeqTakesTheNextSlot2013377774/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1
```
`--- FAIL: TestDifferentSecondBodyUnderOneSeqTakesTheNextSlot (0.20s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.469s`.

### A9 the empty directory

```
go test -count=1 -run ^TestPlantedSlotIsAQuarantineFailureNotACopy/empty_directory$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:295`
```
				return false, true, fmt.Errorf("%s is not a quarantine slot (no %s)", slot, quarantineSidecar)
```
to:
```
				return false, false, nil
```
First failing line:
```
quarantine_test.go:305: the error does not say the copy failed ("commits.s1.seq1 is not a quarantine slot (no quarantine.txt)"): commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedSlotIsAQuarantineFailureNotACopyempty_directory2241460127/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1
```
`--- FAIL: TestPlantedSlotIsAQuarantineFailureNotACopy (0.19s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.455s`.

### A9 the regular file

```
go test -count=1 -run ^TestPlantedSlotIsAQuarantineFailureNotACopy/regular_file$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:282 to :285`
```
	if err != nil {
		return false, true, err
	}
	defer s.close()
```
to:
```
	if err != nil {
		return false, false, nil
	}
	defer s.close()
```
First failing line:
```
quarantine_test.go:305: the error does not say the copy failed (""): commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedSlotIsAQuarantineFailureNotACopyregular_file354873858/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.2
```
`--- FAIL: TestPlantedSlotIsAQuarantineFailureNotACopy (0.19s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.453s`.

### A10

```
go test -count=1 -run ^TestQuarantineHoldsTheRefusedBytesNotALaterSwap$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/ledger.go:386`
```
		err = quarantine(root, handle, verb, err, raw)
```
to:
```
		raw.body, raw.hasBody, _ = readNoFollow(ledgerPath(root, handle))
		err = quarantine(root, handle, verb, err, raw)
```
First failing line:
```
quarantine_test.go:345: the quarantine holds the swapped body, not the refused one: sha256 5a999005f6166926c0c786eb2393ae239ce6a6c882a87edd9e1247a9c31634a0, refused 6749cac8262b805b2bccb57cb7a79eea9d610c56f301fdef32158d285601857c, swapped 5a999005f6166926c0c786eb2393ae239ce6a6c882a87edd9e1247a9c31634a0
```
`--- FAIL: TestQuarantineHoldsTheRefusedBytesNotALaterSwap (0.18s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.452s`.

### A11

```
go test -count=1 -run ^TestLedgerSymlinkedToTheSeedIsRefusedAndNeverCopied$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine_unix.go:39`
```
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
```
to:
```
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
```
First failing line:
```
quarantine_test.go:376: a symlinked ledger: ledger_tampered: commit ledger s1 does not decode: invalid character ';' after top-level value; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestLedgerSymlinkedToTheSeedIsRefusedAndNeverCopied1703625184/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1
```
`--- FAIL: TestLedgerSymlinkedToTheSeedIsRefusedAndNeverCopied (0.18s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.498s`.

### A12

```
go test -count=1 -run ^TestAbsentBodyThenTamperedBodyUnderOneSeqGiveTwoSlots$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:299`
```
		if ok != wantOK || !bytes.Equal(got, want) {
```
to:
```
		if false && (ok != wantOK || !bytes.Equal(got, want)) {
```
First failing line:
```
quarantine_test.go:437: the tampered body does not take the second slot: commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestAbsentBodyThenTamperedBodyUnderOneSeqGiveTwoSlots2377327624/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1
```
`--- FAIL: TestAbsentBodyThenTamperedBodyUnderOneSeqGiveTwoSlots (0.20s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.476s`.

### A13

```
go test -count=1 -run ^TestConcurrentIdenticalRefusersPublishOneSlot$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:220`
```
			if !slotTaken(err) {
```
to:
```
			if err != nil {
```
First failing line:
```
quarantine_test.go:470: refuser: commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the quarantine copy failed: directory not empty
```
`--- FAIL: TestConcurrentIdenticalRefusersPublishOneSlot (0.26s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.514s`.


### A14

On `90dd2e77`:
```
go test -count=1 -run ^TestStagedDirectorySwappedForALinkIsAQuarantineFailure$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine_unix.go:171`
```
	fd, err := unix.Openat(d.fd(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
```
to:
```
	fd, err := unix.Openat(d.fd(), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
```
First failing line:
```
quarantine_test.go:516: the error does not say the copy failed ("the quarantine copy failed: the staged copy .copying-c2e92ae178f74c38: /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestStagedDirectorySwappedForALinkIsAQuarantineFailure2593453016/002/amq/sess/extensions/amq-squad/quarantine/.copying-c2e92ae178f74c38 is a symlink"): commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestStagedDirectorySwappedForALinkIsAQuarantineFailure2593453016/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1
```
`--- FAIL: TestStagedDirectorySwappedForALinkIsAQuarantineFailure (0.20s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.503s`.

### A15

On `90dd2e77`:
```
go test -count=1 -run ^TestStagedDirectoryIsFsyncedBeforeItsRename$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:299 to :309`
```
	if err := td.sync(); err != nil {
		removeSlot(d, tmp, td, set)
		td.close()
		return "", nil, err
	}
	if err := d.sync(); err != nil {
		removeSlot(d, tmp, td, set)
		td.close()
		return "", nil, err
	}
	return tmp, td, nil
```
to:
```
	if err := d.sync(); err != nil {
		removeSlot(d, tmp, td, set)
		td.close()
		return "", nil, err
	}
	return tmp, td, nil
```
First failing line:
```
quarantine_test.go:562: the staged directory /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestStagedDirectoryIsFsyncedBeforeItsRename1253614767/002/amq/sess/extensions/amq-squad/quarantine/.copying-8edb386ef6b52380 was not fsynced before its rename: [fsync /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestStagedDirectoryIsFsyncedBeforeItsRename1253614767/002/amq/sess/extensions/amq-squad/quarantine renameat /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestStagedDirectoryIsFsyncedBeforeItsRename1253614767/002/amq/sess/extensions/amq-squad/quarantine/.copying-8edb386ef6b52380 -> /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestStagedDirectoryIsFsyncedBeforeItsRename1253614767/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1 fsync /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestStagedDirectoryIsFsyncedBeforeItsRename1253614767/002/amq/sess/extensions/amq-squad/quarantine]
```
`--- FAIL: TestStagedDirectoryIsFsyncedBeforeItsRename (0.19s)`
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.467s`.

## Gates

On `90dd2e77` (the two P2 fixes; only `internal/seatcommit/` changed since
`0ab0d35d`), each exit 0: `go build ./...`; `go vet ./...`;
`GOOS=windows go build ./internal/seatcommit/`; `go test -count=1
./internal/seatcommit/ ./internal/schedproc/` (both `ok`);
`go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`.

The full gates before that:

On `0ab0d35d` (the directory layout; the head after this note's commit
has the same Go tree), each exit 0, from the worktree root:

- `go build ./...`: exit 0.
- `go vet ./...`: exit 0.
- `go test -count=1 ./...`: exit 0; 56 packages `ok`, no `FAIL`.
- `NPM_FLAGS=--offline make ci`: exit 0, with a scratch PyYAML venv first
  on PATH (`PATH=<job tmp>/pyvenv/bin:$PATH`, made from the pip cache's
  `pyyaml-6.0.3` wheel with `--no-index`, no network, nothing installed
  outside the job's scratch; no python3 on this machine has PyYAML, and
  the 3.1.1 notes ran ci the same way). It ran `go vet`, `go test
  -timeout 20m ./...` (56 `ok`), the README and docs html checks (in
  sync), skills-check, skill-routing-check, skill-command-check,
  skill-invocation-check, release-validator-test, go-files-scope-test,
  claude-hooks-test and pi-test: `cd pi && npm ci --ignore-scripts
  --offline && npm test`, tail `ℹ tests 116`, `ℹ pass 115`, `ℹ fail 0`,
  `ℹ skipped 1` (the staged-runtime row, local-only).
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`:
  exit 0.
- `GOOS=windows go build ./internal/seatcommit/`: exit 0 (the path form
  of the quarantine off unix compiles).

The same gates ran green on the earlier layouts (`f6927bd7` before the
merges, `f5712d62` and `648b8c55` after each merge of `origin/v3`,
`bf26780d` with the first two fixes; `bbaddd49` and `ca90cde6` ran build,
vet, the seatcommit and schedproc tests and the redact check).

## Residual risk and what part B takes

- The head bytes the quarantine copies are read just before `sealchain`
  reads the head for its check; a head replaced inside that gap is copied
  as the later head. Closing it needs a `sealchain` entry that takes the
  head bytes it verified (outside this task's set). The plan reviewer
  carried it to brief 0e, which rewrites the seal path; nothing under
  `internal/sealchain/` changes here.
- Off unix the quarantine is the path-based compatibility version named
  above (no descriptor anchoring, no owner check, no directory fsync).
- Identity across refusals is the body, the head and the error text (and
  the binary line); the writer verb, the time and the leftovers seen are
  not part of it. Up to 100 slots per seq.
- A planted empty directory that appears between the look and the rename
  is replaced by the rename (rename(2) replaces an empty directory); no
  evidence is lost by that, and a planted non-empty directory, file or link
  is refused.
- The commit step still maps a write-time refusal to a deferral note; the
  read-time refusal is `ledger_tampered` as before. The typed block raised
  once, the recorded recovery and the prepare-loop suppression are part B.
- `seatcommit.BinaryVersion` is unset until part B threads the CLI's
  version; the sidecar's fallback names the build's vcs revision.
