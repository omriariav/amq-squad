# 3.2.0-0c-A: a tamper alarm preserves its evidence

Brief: `brief-320-0c-A-quarantine.md` (part A of 3.2.0-0c, draft 2 approved).
Branch `v3-320-0c-A`, cut from `origin/v3` at `10449a51` (#885 merged);
the code commit is `f6927bd7`; `origin/v3` then took #887 (the 3.2.0-0
ledger investigation, docs and harness testdata only), merged in as
`f5712d62` (no conflict, no file of this task). Declared files only:
`internal/seatcommit/`, `internal/schedproc/`,
`docs/v3-spike/320-0c-quarantine/`.

## Why

On 2026-11-05 the rc.1 scheduler refused the reviewer seat's commit ledger
(`ledger_tampered`, link 150) on every tick for 65 minutes. The cause for
that body is version skew (3.1.1's `attempt` in `LedgerDiscard`, which
rc.1 drops on decode and re-encodes away, so the tag fails). The seq-150
body was overwritten by the next writer's re-seal 2 to 4 s after the
restart and is lost: one seal per document, no history. Whatever a
refusal's cause, its evidence must survive the next writer.

## The rule as built

**The read that refuses quarantines.** `seatcommit.ReadLedger` is now
`ReadLedgerAs(root, handle, "read")`; `ReadLedgerAs` runs the unchanged
check (`readLedger`: the file, its schema and handle, the seal under the
session seed, the signed head) and, on `sealchain.ErrTampered`, calls
`quarantine` before it returns. The quarantine copies the body and the
head byte for byte (`os.ReadFile`, written as read) under

    <root>/extensions/amq-squad/quarantine/commits.<handle>.seq<N>.json
    <root>/extensions/amq-squad/quarantine/commits.<handle>.seq<N>.head.json
    <root>/extensions/amq-squad/quarantine/commits.<handle>.seq<N>.txt

N is the head file's `seq`, decoded without verifying the head (its tag
may be what failed); `unknown` when the head does not decode or is absent,
so the name is then `commits.<handle>.sequnknown.json` (the brief's
`seq<N>` with N = unknown, taken literally). Each file is created with
`O_WRONLY|O_CREATE|O_EXCL`, 0600, fsynced with its directory, so nothing
is ever overwritten. Under one head seq (the plan reviewer on #893,
commit `bf26780d`): a refusal whose body and head are byte-identical to
an existing copy is a no-op (the read path refuses on every tick; one
copy), and a refusal with a different body or head takes the next free
slot, `commits.<handle>.seq<N>.2.json` with its own `.2.head.json` and
`.2.txt`, then `.3`, up to 100 slots per seq. The slot's primary file (the
body; the head when the body is absent) claims it with the exclusive
create; an existing primary is compared byte for byte, with the head copy
beside it. In a slot the primary just claimed, a head file already there
(a crash's leftover, a planted file) is this refusal's only when it is
byte-identical; with other content the copy is the named failure `the
quarantine copy failed: <head path> exists with other content` (commit
`bbaddd49`, the reviewer's re-read of `bf26780d`), so the sidecar never
names a copy whose digest is not the head's.

A planted link never passes as a copy (the same review): after `MkdirAll`
(which follows a symlinked directory) the quarantine directory is
`Lstat`ed and must be a directory itself, mode 0700, owned by the
process's uid (unix; no owner check off unix); and a path that exists
already counts as an existing copy only when `Lstat` says regular file.
A symlink or any other type at either place is a named failure in the
error, `the quarantine copy failed: <path> is a symlink` or `the
quarantine copy failed: the quarantine directory <dir> is a symlink`,
never `quarantined at`; the refusal stands either way. The sidecar carries

    amq-squad commits ledger quarantine
    time: <RFC3339Nano, UTC>
    seat: <handle>
    writer: <verb>
    binary: <version>
    error: <the refusal's text>
    body: commits.<handle>.seq<N>.json sha256 <hex>      (or: body: absent)
    head: commits.<handle>.seq<N>.head.json sha256 <hex> (or: head: absent)

The refusing error is the refusal with the path appended:
`commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the
refused document is quarantined at <root>/extensions/amq-squad/quarantine/commits.s1.seq1.json`.
It still wraps `sealchain.ErrTampered` (`%w`), so every `errors.Is` and
every `ReasonLedgerTampered` mapping is unchanged. A quarantine that
cannot be written does not soften the refusal: the error then says `could
not be quarantined under <dir>: <why>`.

Why at the read and not only at the write: in the incident the refusing
binary (rc.1) never wrote the ledger; the body was re-sealed by a binary
that accepted it. A write-side check in the accepting binary passes. Only
the refusing reader holds the evidence at the moment it matters, so the
copy is taken there, through the one function every reader and writer
calls. The prepare's refusal therefore quarantines on its first refusing
tick (the incident's first alarm), before any writer can touch the body.

**The write re-reads the disk under the lock.** `writeLedger(root, l,
verb)` is still the one point every write passes (each caller holds the
ledger lock). Before it seals, it calls `ReadLedgerAs(root, l.Handle,
verb)` on what is on disk: a refusal (or any read error other than
`os.ErrNotExist`) is returned as is, nothing is written, the head is not
moved. The caller's own earlier read is not enough: it happened before the
caller's git work, and a same-seat edit (or another binary's re-seal) can
land in between. `sealchain.Write` keeps its own head check after this.

**The writer verb.** Every writer names itself on the sidecar: `seat
commit --base` (`RecordBase`, `RecordTarget`), `launch record base`
(`RecordWorktreeBase`), `archive attempt`, `clean seat`, `start task`,
`commit step` (the step's reads and writes, `verifyEvidence` included),
`commit step settle`, `exit check`, `scheduler prepare` (the prepare's read,
`internal/schedproc/prepare.go`), and `read` for every other
`ReadLedger` caller (`slots.go`, `uninstall`). The verb is only a sidecar
label; it changes no behaviour.

**The prepare.** `PrepareSeat` keeps today's behaviour: a read error is
`seat_prepare_failed`, no lease. It reads through `ReadLedgerAs(c.Root,
handle, "scheduler prepare")`, so the refusing read quarantines, and the
decision's reason (`the commit ledger of seat <h>: <err>`) carries the
quarantine path because the error does.

**What a quarantine file is.** Evidence: never sealed, never read as a
ledger, never deleted by any verb of this task. `uninstall` lists an
unknown entry under `extensions/amq-squad/` in the session group (the
same group as the commits ledger), which is the owner's confirmed removal
of the session root; keeping the quarantine out of that plan would change
`internal/uninstall/`, outside the declared set, and is the lead's call.

**The binary version.** `seatcommit.BinaryVersion` is the sidecar's
version when set; the fallback is `runtime/debug`'s module version with
`vcs.revision`, `vcs.modified` and the Go version (a `make build` binary
reports `(devel) vcs.revision=<sha> vcs.modified=<bool> go1.x`). The
release build's `-X main.version` is visible only to `main`; threading it
into `seatcommit.BinaryVersion` is one line in `internal/cli/`, outside
this task's set, for part B.

**Not changed.** `internal/sealchain/` (the head's seq is decoded in
`seatcommit` from the head file's JSON), `internal/cli/`,
`internal/seatlaunch/`. The commit step's handling of a write-time
refusal is as before (`defer_` with the error text, which now names the
copy); the typed block raised once is part B.

## Rows

| Row | What it proves | Test |
|---|---|---|
| A1 | A tampered document (the base hand-edited; valid JSON, the tag fails) followed by each writer of the fixture set, driven through the seatcommit function its caller uses, leaves the quarantine body sha256-equal to the refused body and the head copy byte-equal to the head; the writer refuses as `ledger_tampered`, its error names the quarantine path, the sidecar names the writer; the document and head are unchanged after. One subtest per writer: `RecordBase`, `RecordWorktreeBase`, `RecordTarget`, `ArchiveAttempt`, `CleanSeat`, `StartTask`, `Run`. | `internal/seatcommit` `TestTamperedLedgerIsQuarantinedByEveryWriter` |
| A1 guard | From the source: every function of `seatcommit` that calls `writeLedger` (`ArchiveAttempt, RecordTarget, StartTask, cleanSeat, commitAndPush, recordBase, settle`), every exported function that reaches one (`ArchiveAttempt, CheckWiden, CleanSeat, RecordBase, RecordTarget, RecordWorktreeBase, Run, StartTask`; `CheckWiden` reaches `Run` as a dry run), and every reference to such an entry in the module's other non-test Go files (read, not changed: `internal/cli/seat_commit.go:106` RecordBase, `:111` RecordTarget, `:217` Run; `internal/cli/task_widen.go:249` ArchiveAttempt, `:264` and `:334` CheckWiden; `internal/schedproc/prepare.go:46` CleanSeat, `:60` ArchiveAttempt; `internal/schedproc/slots.go:323` StartTask; `internal/seatlaunch/run.go:302` RecordWorktreeBase) must each map to an A1 row. A new writer fails this test until it has a row. | `internal/seatcommit` `TestEveryLedgerWriterHasAQuarantineRow` |
| A2 | An identical second refusal of the same seq (the same body and head, the read path's every-tick case) names the same path and leaves the first body, head and sidecar byte-identical; the directory holds three files. | `internal/seatcommit` `TestIdenticalSecondRefusalKeepsTheFirstCopy` |
| A3 | A head that does not decode (`{not a head`) quarantines under `sequnknown`: the body copy is the document as it is, the head copy is the undecodable head, the sidecar carries the error; the head on disk is unchanged. | `internal/seatcommit` `TestUndecodableHeadQuarantinesUnderSeqUnknown` |
| A4 | The refusing error is `ledger_tampered` and ends with `; the refused document is quarantined at <path>`, from the read that refuses (`ReadLedgerAs` with the prepare's verb; the sidecar names it). The scheduler half: the prepare on a tampered ledger logs `seat_prepare_failed` whose reason names `quarantined at <path>`, the copy is the refused body, the sidecar says `writer: scheduler prepare`, the task is not leased. | `internal/seatcommit` `TestRefusalNamesTheQuarantinePath`; `internal/schedproc` `TestPrepareNamesTheQuarantineOfATamperedLedger` |
| A5 | The check runs under the lock. The first writer (`ArchiveAttempt`) is paused inside its critical section after its read and snapshot (the `beforeArchiveAttemptWrite` seam); the edit lands then; a second writer (`RecordTarget`, the blocking lock) does not return within 300 ms and no quarantine directory appears while the lock is held. Released, the first writer's `writeLedger` re-reads the disk, refuses and quarantines (it does not re-seal its stale in-memory copy over the edit); the second then refuses naming the same path; the document and head on disk are the edited ones; one copy (three files). | `internal/seatcommit` `TestWriteCheckRunsUnderTheLedgerLock` |
| A7 | A planted link never passes as a copy. A symlink at the body path (to a file elsewhere): the refusal is still `ledger_tampered`, its error says `the quarantine copy failed: <body path> is a symlink` and not `quarantined at`, the link's target is unchanged, the directory holds the planted link alone. A symlink where the quarantine directory should be (to a directory elsewhere): the error says `the quarantine copy failed: the quarantine directory <dir> is a symlink`, and the target directory stays empty. | `internal/seatcommit` `TestPlantedSymlinkIsAQuarantineFailureNotACopy` (`body_path`, `directory`) |
| A8 | A second, different refused body under one head seq takes the next slot: the error names `commits.<h>.seq<N>.2.json`, the first copy (body, head, sidecar) is untouched, the second slot's body is the second refused body, its head copy the head, its sidecar names its own body and digest; the same second body again names the second slot again; six files in all (two copies, not three). | `internal/seatcommit` `TestDifferentSecondBodyUnderOneSeqTakesTheNextSlot` |
| A9 | A head file planted in an empty slot (the quarantine directory made as the code makes it, 0700; `{"seq": 999}` at `commits.<h>.seq<N>.head.json`, not the head) makes the copy a named failure: the refusal is still `ledger_tampered`, its error says `the quarantine copy failed: <head path> exists with other content` and not `quarantined at`; the planted file is unchanged; no sidecar is written beside it; the body copy (the slot's claim) is there. | `internal/seatcommit` `TestPlantedHeadInAnEmptySlotIsAQuarantineFailure` |
| A6 | Version skew as the tamper: a document a newer amq-squad wrote. Built from a real ledger with one discard entry: `attempt_note` added to the entry (this binary's `LedgerDiscard` already has 3.1.1's `attempt`, so the row uses a field this binary lacks, built the way `attempt` reached rc.1), sealed with the session's key as the next link over the body with the field (`sealchain.Seal`), the head moved to that link (`sealchain.WriteHead`). The row checks the premise both ways: the link verifies over the body with the field, and does not verify over this binary's decode of the same bytes. `CleanSeat` then refuses with `ledger_tampered: the tag of commits.s1 link <N> fails; the refused document is quarantined at ...`, the body copy is sha256-equal to the skewed bytes, the head copy equals the head, the sidecar carries that error text, the document is unchanged. | `internal/seatcommit` `TestVersionSkewIsQuarantinedLikeATamper` |

A6 in one line: a seat whose ledger a newer amq-squad wrote is blocked on
version skew (`ledger_tampered`, with its evidence kept) until the lead's
recovery, and the verifier fix of a later brief turns this case into the
distinct "written by a newer amq-squad" refusal; A6 does not call skew
tampering, it proves the evidence survives whatever the cause.

Rule 18: every row runs under `t.TempDir` roots (the seatcommit fixture's
`amq/sess`, the schedproc live rig's root) with `HOME` set to a temp dir;
nothing touches a real session root; no network.

## Revert runs

Each run: the line flipped on the committed tree (`f6927bd7`), the row
run alone, the first failing line verbatim, then `git checkout -- <file>`
and the row re-run green. The runner is
`revert-runs.py` beside this note's job scratch (not committed); its
output is reproduced here unedited apart from the temp-dir prefix, which
is kept as printed.

### A1

```
go test -count=1 -run ^TestTamperedLedgerIsQuarantinedByEveryWriter$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/ledger.go:382`, the line removed:
```
		err = quarantine(root, handle, verb, err)
```
First failing line:
```
quarantine_test.go:148: RecordBase's error does not name the quarantine /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestTamperedLedgerIsQuarantinedByEveryWriterRecordBase3655239131/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.json: "commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails"
```
`--- FAIL: TestTamperedLedgerIsQuarantinedByEveryWriter (1.42s)`; restored
and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	1.658s`.

### A1 guard

```
go test -count=1 -run ^TestEveryLedgerWriterHasAQuarantineRow$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine_callers_test.go:58`, the line removed
(the row's entry for one writer):
```
		"StartTask":      "StartTask",
```
First failing line:
```
quarantine_callers_test.go:63: StartTask calls writeLedger and has no A1 row (quarantineWriters): add one
```
`--- FAIL: TestEveryLedgerWriterHasAQuarantineRow (0.07s)`; restored and
re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.321s`.

### A2

On `bf26780d` (the reworded row):
```
go test -count=1 -run ^TestIdenticalSecondRefusalKeepsTheFirstCopy$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:193`:
```
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
```
to
```
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
```
First failing line:
```
quarantine_test.go:191: the second refusal changed /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestIdenticalSecondRefusalKeepsTheFirstCopy675128202/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.txt
```
`--- FAIL: TestIdenticalSecondRefusalKeepsTheFirstCopy (0.20s)`; restored
and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.466s`.
(The earlier form of this row, `TestSecondRefusalOfTheSameSeqKeepsTheFirstCopy`
on `f6927bd7` with a different second edit, had the same revert at
`quarantine.go:113` and failed at `quarantine_test.go:195: the second
refusal changed .../commits.s1.seq1.json`; that behaviour is now A8's.)

### A3

```
go test -count=1 -run ^TestUndecodableHeadQuarantinesUnderSeqUnknown$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:105`:
```
		return quarantineSeqUnknown
```
to
```
		return "0"
```
First failing line:
```
quarantine_test.go:221: CleanSeat under an undecodable head: commit ledger s1: ledger_tampered: the head of commits.s1 does not decode or its tag fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestUndecodableHeadQuarantinesUnderSeqUnknown1984214931/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq0.json
```
`--- FAIL: TestUndecodableHeadQuarantinesUnderSeqUnknown (0.19s)`;
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.424s`.

### A4, the error

```
go test -count=1 -run ^TestRefusalNamesTheQuarantinePath$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:94`:
```
	return fmt.Errorf("%w; the refused document is quarantined at %s", refusal, named)
```
to
```
	return fmt.Errorf("%w; the refused document is quarantined at %s", refusal, filepath.Dir(named))
```
First failing line:
```
quarantine_test.go:252: the error does not end with "; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRefusalNamesTheQuarantinePath3620956397/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.json": "commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestRefusalNamesTheQuarantinePath3620956397/002/amq/sess/extensions/amq-squad/quarantine"
```
`--- FAIL: TestRefusalNamesTheQuarantinePath (0.18s)`; restored and
re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.451s`.

### A4, the prepare

```
go test -count=1 -run ^TestPrepareNamesTheQuarantineOfATamperedLedger$ ./internal/schedproc/
```
Reverted `internal/schedproc/prepare.go:41`:
```
	if _, err := seatcommit.ReadLedgerAs(c.Root, handle, "scheduler prepare"); os.IsNotExist(err) {
```
to
```
	if _, err := seatcommit.ReadLedger(c.Root, handle); os.IsNotExist(err) {
```
First failing line:
```
prepare_quarantine_test.go:79: the sidecar does not name the prepare: <nil>
```
`--- FAIL: TestPrepareNamesTheQuarantineOfATamperedLedger (0.11s)`;
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/schedproc	0.346s`.
(With `ReadLedger` the reason still names the path and the copy is still
made, under the verb `read`: the row's earlier assertions pass and the
sidecar's verb is what fails.)

### A5

```
go test -count=1 -run ^TestWriteCheckRunsUnderTheLedgerLock$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/ledger.go:444` to `:446`, the three lines
removed (the write's check):
```
	if _, err := ReadLedgerAs(root, l.Handle, verb); err != nil && !os.IsNotExist(err) {
		return err
	}
```
First failing line:
```
quarantine_test.go:325: the first writer did not refuse the edited body at its write: <nil>
```
`--- FAIL: TestWriteCheckRunsUnderTheLedgerLock (0.73s)`; restored and
re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.952s`.
(Without the check the first writer re-seals its stale copy over the edit
and returns no error: the incident's loss, reproduced.)

### A6

```
go test -count=1 -run ^TestVersionSkewIsQuarantinedLikeATamper$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:81`, the line removed (the
body copy):
```
		errs = append(errs, writeOnce(bodyPath, body))
```
First failing line:
```
quarantine_test.go:417: open /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestVersionSkewIsQuarantinedLikeATamper499087049/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq5.json: no such file or directory
```
(`quarantine_test.go:417` is the row's read of the body copy for the
sha256 comparison; `readFile` is a `t.Helper`, so the line is the row's.)
`--- FAIL: TestVersionSkewIsQuarantinedLikeATamper (0.45s)`; restored and
re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.732s`.

### A7, the body path

On `bf26780d`:
```
go test -count=1 -run ^TestPlantedSymlinkIsAQuarantineFailureNotACopy/body_path$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:195` (the existing-copy read
follows the link again):
```
		existing, err = regularFileContent(path)
```
to
```
		existing, err = os.ReadFile(path)
```
First failing line:
```
quarantine_test.go:272: the error does not say the copy failed ("the quarantine copy failed: /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedSymlinkIsAQuarantineFailureNotACopybody_path3387825039/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.json is a symlink"): commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedSymlinkIsAQuarantineFailureNotACopybody_path3387825039/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.2.json
```
`--- FAIL: TestPlantedSymlinkIsAQuarantineFailureNotACopy (0.19s)`;
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.442s`.
(Without the check the link's target is read as "the existing copy",
differs, and the copy lands in slot 2 while the error names a path beside
the planted link.)

### A7, the directory

```
go test -count=1 -run ^TestPlantedSymlinkIsAQuarantineFailureNotACopy/directory$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:176` to `:178`, the three
lines removed:
```
	if fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("the quarantine directory %s is a symlink", dir)
	}
```
First failing line:
```
quarantine_test.go:297: the error does not say the copy failed ("the quarantine copy failed: the quarantine directory /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedSymlinkIsAQuarantineFailureNotACopydirectory1967638678/002/amq/sess/extensions/amq-squad/quarantine is a symlink"): commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the quarantine copy failed: the quarantine directory /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedSymlinkIsAQuarantineFailureNotACopydirectory1967638678/002/amq/sess/extensions/amq-squad/quarantine is not a directory
```
`--- FAIL: TestPlantedSymlinkIsAQuarantineFailureNotACopy (0.17s)`;
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.444s`.
(The `IsDir` check behind it still refuses, with a different sentence;
the row wants the link named.)

### A8

```
go test -count=1 -run ^TestDifferentSecondBodyUnderOneSeqTakesTheNextSlot$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:136` (any existing regular
file counts as the same copy, the pre-fix rule):
```
			if !bytes.Equal(existing, pdata) {
```
to
```
			if existing == nil {
```
First failing line:
```
quarantine_test.go:225: refusal 1 of the second body does not name the second slot: commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestDifferentSecondBodyUnderOneSeqTakesTheNextSlot2139270044/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.json
```
`--- FAIL: TestDifferentSecondBodyUnderOneSeqTakesTheNextSlot (0.20s)`;
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.481s`.
(A first attempt removed the three lines of the compare; that does not
compile, `existing` unused, so the flip above is the recorded run.)

### A9

On `bbaddd49`:
```
go test -count=1 -run ^TestPlantedHeadInAnEmptySlotIsAQuarantineFailure$ ./internal/seatcommit/
```
Reverted `internal/seatcommit/quarantine.go:158` (a pre-existing head
counts as this refusal's whatever it holds):
```
			if !created && !bytes.Equal(existing, sdata) {
```
to
```
			if !created && existing == nil {
```
First failing line:
```
quarantine_test.go:274: the error does not say the copy failed ("the quarantine copy failed: /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedHeadInAnEmptySlotIsAQuarantineFailure352317724/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.head.json exists with other content"): commit ledger s1: ledger_tampered: the tag of commits.s1 link 1 fails; the refused document is quarantined at /private/var/folders/gx/wg9xtfn96wx9z4gkb9mp_hxr0000gn/T/TestPlantedHeadInAnEmptySlotIsAQuarantineFailure352317724/002/amq/sess/extensions/amq-squad/quarantine/commits.s1.seq1.json
```
`--- FAIL: TestPlantedHeadInAnEmptySlotIsAQuarantineFailure (0.17s)`;
restored and re-run: `ok  	github.com/omriariav/amq-squad/v3/internal/seatcommit	0.445s`.

## Gates

On `bbaddd49` (the A9 fix; only `internal/seatcommit/` changed since
`bf26780d`, so the full `make ci` was not run again, as the lead said),
each exit 0: `go build ./...`; `go vet ./...`; `go test -count=1
./internal/seatcommit/ ./internal/schedproc/` (both `ok`);
`go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`.

On `bf26780d` (the #893 fixes: the symlink-safe copy and the per-body
slots; the head after this note's commit has the same Go tree), each
exit 0: `go build ./...`; `go vet ./...`; `go test -count=1 ./...` (56
`ok`, no `FAIL`); `NPM_FLAGS=--offline make ci` with the scratch PyYAML
venv on PATH (pi: `cd pi && npm ci --ignore-scripts --offline && npm
test`, `ℹ tests 116`, `ℹ pass 115`, `ℹ fail 0`, `ℹ skipped 1`);
`go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`.

The earlier runs, kept for the record:

On the merged head `f5712d62` (the code commit `f6927bd7` plus #887's
docs; the Go tree is the code commit's), each from the worktree root:

- `go build ./...`: exit 0.
- `go vet ./...`: exit 0.
- `go test -count=1 ./...`: exit 0; 56 packages `ok`, no `FAIL`.
- `NPM_FLAGS=--offline make ci`: exit 0, with a scratch PyYAML venv first
  on PATH (`PATH=<job tmp>/pyvenv/bin:$PATH`, made from the pip cache's
  `pyyaml-6.0.3` wheel with `--no-index`, no network, nothing installed
  outside the job's scratch). It ran `go vet`, `go test -timeout 20m ./...`
  (56 `ok`), the README and docs html checks (in sync), skills-check,
  skill-routing-check, skill-command-check (`skills name 48 command(s);
  all verb/subcommand paths resolve, 130 of 164 flag(s) verified`),
  skill-invocation-check, release-validator-test, go-files-scope-test,
  claude-hooks-test and pi-test: `cd pi && npm ci --ignore-scripts
  --offline && npm test`, tail `ℹ tests 114`, `ℹ pass 113`, `ℹ fail 0`,
  `ℹ skipped 1` (the staged-runtime row, local-only, as the 3.1.2 note
  says).
- `go run ./scripts/redact-evidence -check docs/v3-spike/320-0c-quarantine/`:
  exit 0.

After #891 (the lease-renewal fix) merged, `origin/v3` `8302baf1` was
merged in as `648b8c55` (a merge, no conflict; #891's schedproc change is
`amq.go`, `slots.go` and `slots_test.go`, not `prepare.go`, so the merged
`PrepareSeat` is this task's). The same gates ran again on `648b8c55`, each
exit 0: `go build ./...`; `go vet ./...`; `go test -count=1 ./...` (56
`ok`); `NPM_FLAGS=--offline make ci` with the venv on PATH (pi: `ℹ tests
116`, `ℹ pass 115`, `ℹ fail 0`, `ℹ skipped 1`); the redact check. The
commit that records this paragraph changes this note only; its Go tree is
`648b8c55`'s.

The same Go gates ran once before the merges on `f6927bd7` (exit 0, 56
`ok`); that run's `make ci` stopped at skills-check with `PyYAML is
required for skill generation: pip3 install pyyaml` (no python3 on this
machine has PyYAML; the 3.1.1 notes ran ci the same way, with a PyYAML
venv on PATH), which is why the merged-head run carries the venv.

## Residual risk and what part B takes

- One copy per distinct body and head under a seq, up to 100 slots; an
  identical refusal records nothing new, so the sidecar carries the first
  refusal's time and error.
- The copy is taken at the refusing read, outside the lock for readers that
  hold none (`slots.go`, `uninstall`, the commit step's first read). The
  writer-side check is under the lock, as the brief requires (A5).
- The commit step still maps a write-time refusal to a deferral note; the
  read-time refusal is `ledger_tampered` as before. The typed block raised
  once, the recorded recovery and the prepare-loop suppression are part B.
- `seatcommit.BinaryVersion` is unset until part B threads the CLI's
  version; the sidecar's fallback names the build's vcs revision.
