package seatcommit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
)

// The lead's recovery of a quarantined ledger (3.2.0-0c-B rule 3). After
// 0c-A every refused read quarantines the document and every write is
// refused, so a tampered seat stays stopped: the way back is a recorded
// lead decision, never a cleared warning. The decision reaches the
// scheduler over its decision socket with the lead-pane proof (schedproc,
// as task widen does); the scheduler calls RecoverLedger, which writes the
// new chain with the session key. Nothing in the CLI calls it: a seat that
// can run the CLI could otherwise recover its own tampered ledger, the very
// act the alarm stops.
//
// The chain continues; it does not restart. The new first link takes a seq
// one above every sequence recoverable from the copied head and body. Its
// prev is the last verifiable tag (the head when it verifies, otherwise the
// body's own seal), and its body records the
// quarantined body's sha256 and the slot's path (LedgerRecovery). The
// quarantined document stays in the slot: restored afterwards it reads as
// tampered (its seq is behind the head), so it cannot be replayed. A
// carried entry is checked, not trusted: an entry named by task and commit
// is carried only when that commit exists on origin's branch for the task
// and its tree equals the quarantined entry's tree; it is marked "carried
// from quarantine seq N". The rest of the new body is what a fresh launch
// records: the worktree and branch from the staged launch record, the base
// as the core observes the worktree now, the owner's grant as the caller
// (the scheduler process) read it. The quarantined body's own base, rest,
// lease and target are not carried: a tampered gh path there would be
// executed by the next push.

// RecoverOptions is one recovery.
type RecoverOptions struct {
	Root   string
	Handle string
	// Seq is the quarantined head seq the slot is named by ("150"), or
	// quarantineSeqUnknown ("unknown") for a slot whose head did not
	// decode; Slot is k of commits.<handle>.seq<N>[.k] (0 and 1 are the
	// first slot).
	Seq  string
	Slot int
	// Carry names the quarantined entries to carry, by task and commit.
	Carry []CarryEntry
	// Target is the owner's grant as the launch records it (the push URL
	// the carried entries are checked against).
	Target Target
	Git    string
	Env    []string
	Lead   string
	Reason string
	Now    time.Time
	// Alarm, when set, is a decision by alarm (recordAlarm): the scheduler's
	// alarm id of the hold the lead releases; it names no slot and no seq,
	// and carries nothing.
	Alarm string
	// Guard, when set, runs inside the ledger lock immediately before the
	// seal; an error refuses the recovery with nothing written (the
	// scheduler's check that the request's alarm still holds the seat,
	// Codex on 254f2c2c: a check before the lock can be passed by a request
	// that then waits while its hold is released and another is recovered).
	Guard func() error
}

// CarryEntry names one quarantined commit entry to carry.
type CarryEntry struct {
	Task   string `json:"task"`
	Commit string `json:"commit"`
}

// RecoverResult is a recovery as made, or as found already made
// (AlreadyRecovered: the ledger verifies and its recovery record names the
// slot the decision names; a retry after an interrupted completion, Codex
// on #904).
type RecoverResult struct {
	Handle           string `json:"handle"`
	AlreadyRecovered bool   `json:"already_recovered,omitempty"`
	// Verified: the document verified when the decision came; the record
	// was sealed into the next link of the same body (recordVerified).
	Verified bool `json:"verified,omitempty"`
	// Alarm: a decision by alarm released this alarm id (recordAlarm).
	Alarm      string         `json:"alarm,omitempty"`
	Slot       string         `json:"slot"`
	OldSeq     int            `json:"old_seq"`
	NewSeq     int            `json:"new_seq"`
	Prev       string         `json:"prev"`
	SeqFrom    string         `json:"seq_from,omitempty"`
	BodySHA256 string         `json:"body_sha256,omitempty"`
	HeadSHA256 string         `json:"head_sha256,omitempty"`
	Base       string         `json:"base"`
	Carried    []LedgerCommit `json:"carried,omitempty"`
}

// Refusal codes of a recovery (RecoverRefusal.Code). They are the verb's
// replies, never a task's block reason.
const (
	RecoverSlotMissing       = "recover_slot_missing"
	RecoverSlotInvalid       = "recover_slot_invalid"
	RecoverNotTampered       = "recover_not_tampered"
	RecoverSlotStale         = "recover_slot_stale"
	RecoverSeqUnknown        = "recover_seq_unknown"
	RecoverBaseUnobserved    = "recover_base_unobserved"
	RecoverEntryUnknown      = "recover_entry_unknown"
	RecoverEntryUnpushed     = "recover_entry_unpushed"
	RecoverEntryTreeMismatch = "recover_entry_tree_mismatch"
	RecoverWriteFailed       = "recover_write_failed"
	// RecoverIncompatible is a validly sealed newer-writer or schema
	// mismatch refusal. Recovery is for tamper only; it writes nothing.
	RecoverIncompatible = "recover_incompatible"
	// RecoverAlarmUnverified: a decision by alarm on a ledger that does not
	// verify now (it is recovered from its quarantine slot instead).
	RecoverAlarmUnverified = "recover_alarm_unverified"
)

// AlarmIDRe is a scheduler alarm id: eight lowercase hex characters.
var AlarmIDRe = regexp.MustCompile(`^[0-9a-f]{8}$`)

// RecoverRefusal is a recovery refused, with its code.
type RecoverRefusal struct {
	Code   string
	Detail string
}

func (e *RecoverRefusal) Error() string { return e.Code + ": " + e.Detail }

func refuseRecover(code, format string, args ...any) error {
	return &RecoverRefusal{Code: code, Detail: fmt.Sprintf(format, args...)}
}

var (
	recoverSeqRe    = regexp.MustCompile(`^(?:[1-9][0-9]*|` + quarantineSeqUnknown + `)$`)
	recoverCommitRe = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// RecoverSlotName is the slot directory name a recovery names:
// commits.<handle>.seq<N>[.k].
func RecoverSlotName(handle, seq string, k int) string {
	name := quarantineBase(handle, seq)
	if k > 1 {
		name += "." + strconv.Itoa(k)
	}
	return name
}

// RecoverLedger is the recovery (the package comment above). It holds the
// seat's ledger lock for the whole step. It never reads the quarantined
// copies as a ledger (nothing verifies them, nothing moves them), never
// seals them and never removes them.
func RecoverLedger(o RecoverOptions) (RecoverResult, error) {
	var out RecoverResult
	// The defaults before any branch (Codex on 8cffafe7: the alarm branch
	// returned before them, and the scheduler builds its Recovery without
	// Git, schedproc.NewRecovery).
	if o.Git == "" {
		o.Git = "git"
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
	}
	if o.Alarm != "" {
		if o.Handle == "" || !AlarmIDRe.MatchString(o.Alarm) || o.Seq != "" || o.Slot != 0 || len(o.Carry) > 0 {
			return out, refuseRecover(RecoverSlotInvalid, "a decision by alarm names a seat and an alarm id, and no slot, seq or carried entry")
		}
		err := withLedgerLock(o.Root, o.Handle, func() error {
			var err error
			out, err = recordAlarm(o)
			return err
		})
		return out, err
	}
	if o.Handle == "" || !recoverSeqRe.MatchString(o.Seq) || o.Slot < 0 {
		return out, refuseRecover(RecoverSlotInvalid, "a recovery names a seat, a head seq (or %s) and a slot number", quarantineSeqUnknown)
	}
	for _, c := range o.Carry {
		if c.Task == "" || !recoverCommitRe.MatchString(c.Commit) {
			return out, refuseRecover(RecoverEntryUnknown, "a carried entry is named by its task id and its full commit id")
		}
	}
	err := withLedgerLock(o.Root, o.Handle, func() error {
		var err error
		out, err = recoverLedger(o)
		return err
	})
	return out, err
}

// quarantinedSlot is a slot as the recovery read it.
type quarantinedSlot struct {
	name, path       string
	body, head       []byte
	hasBody, hasHead bool
	bodySHA, headSHA string // the sidecar's digests
}

func recoverLedger(o RecoverOptions) (RecoverResult, error) {
	var out RecoverResult
	// The document on disk must be the one the decision names: a document
	// that verifies now has nothing to recover; one that fails but differs
	// from the slot (a later edit, another refusal) is quarantined in its
	// own slot and the decision is refused, so the lead decides on what is
	// there, never on a copy of something else.
	slotPath := filepath.Join(QuarantineDir(o.Root), RecoverSlotName(o.Handle, o.Seq, o.Slot))
	live, raw, rerr := readLedger(o.Root, o.Handle)
	switch {
	case rerr == nil && live.Recovered != nil && live.Recovered.Slot == slotPath:
		if raw.migration != nil {
			key, err := sealchain.SessionKey(o.Root)
			if err != nil {
				return out, refuseRecover(RecoverWriteFailed, "the session seed: %v", err)
			}
			if err := recordLayoutMigration(o.Root, o.Handle, raw.migration.Seq); err != nil {
				return out, refuseRecover(RecoverWriteFailed, "%v", err)
			}
			if _, err := sealchain.CompleteRawHead(o.Root, ledgerHeadName(o.Handle), key, *raw.migration, o.Now.UTC()); err != nil {
				return out, refuseRecover(RecoverWriteFailed, "complete the recovered raw head: %v", err)
			}
		}
		// The decision was applied and its completion interrupted (the
		// decision line not appended, the hold not released): the retry
		// returns the recovery as the sealed ledger records it, so the
		// caller records and releases; nothing is written again.
		// The link and the mode are the record's own (Codex on b250b951: a
		// verified decision at live link 150 wrote 151, not seq+1); a record
		// written before Link falls back to the chain continuation's seq+1.
		newSeq := live.Recovered.Link
		if newSeq == 0 {
			newSeq = live.Recovered.Seq + 1
		}
		out = RecoverResult{Handle: o.Handle, AlreadyRecovered: true, Verified: live.Recovered.Verified, Slot: slotPath, OldSeq: live.Recovered.Seq, NewSeq: newSeq,
			SeqFrom: live.Recovered.SeqFrom, BodySHA256: live.Recovered.BodySHA256, HeadSHA256: live.Recovered.HeadSHA256, Base: live.Base}
		for _, c := range live.Commits {
			if c.Note != "" {
				out.Carried = append(out.Carried, c)
			}
		}
		return out, nil
	case rerr == nil:
		// The document verifies now, with no record of the slot: the alarm
		// was transient (the seed unreadable for a tick) or the document was
		// put back by hand. The chain is not restarted; the lead's decision
		// is recorded as a new link that carries the recovery record
		// (Verified), so the scheduler's reconciliation releases the hold
		// from the sealed ledger, as for every recovery. Without it the hold
		// could never be released: the verb would refuse not_tampered.
		_, _, splitErr := sealchain.SplitRaw(raw.body)
		migrationSeq := 0
		if raw.migration != nil {
			migrationSeq = raw.migration.Seq
		} else if splitErr != nil {
			migrationSeq = -1 // legacy: the new raw link is the migration
		}
		return recordVerified(o, live, slotPath, migrationSeq)
	case errors.Is(rerr, os.ErrNotExist):
		return out, refuseRecover(RecoverNotTampered, "the commits ledger of seat %s is absent with no sealed head; the launch records a new one", o.Handle)
	case errors.Is(rerr, sealchain.ErrNewerWriter), errors.Is(rerr, sealchain.ErrSchemaMismatch):
		return out, incompatibleRecovery(o.Handle, rerr)
	case !errors.Is(rerr, sealchain.ErrTampered):
		return out, refuseRecover(RecoverWriteFailed, "the commits ledger of seat %s cannot be read: %v", o.Handle, rerr)
	}
	slot, err := readSlot(o.Root, o.Handle, RecoverSlotName(o.Handle, o.Seq, o.Slot))
	if err != nil {
		return out, err
	}
	if raw.hasBody != slot.hasBody || !bytes.Equal(raw.body, slot.body) || raw.hasHead != slot.hasHead || !bytes.Equal(raw.head, slot.head) {
		where := "and could not be quarantined"
		if qerr := quarantine(o.Root, o.Handle, "scheduler recover", rerr, raw); qerr != nil {
			var q *QuarantinedError
			if errors.As(qerr, &q) {
				where = "and is quarantined at " + q.Slot
			} else {
				where += ": " + qerr.Error()
			}
		}
		return out, refuseRecover(RecoverSlotStale, "the commits ledger of seat %s on disk is not the document of %s %s; decide on that slot", o.Handle, slot.path, where)
	}
	// N and the quarantined head's link tag: from the head copy, else from
	// the body copy's own seal (a head that did not decode, sequnknown).
	choice, err := slot.link(o.Root, o.Handle)
	if err != nil {
		return out, err
	}
	oldSeq := choice.namedSeq
	if o.Seq != quarantineSeqUnknown && strconv.Itoa(choice.namedSeq) != o.Seq {
		return out, refuseRecover(RecoverSlotInvalid, "%s names seq %s but its copied head names %d", slot.path, o.Seq, choice.namedSeq)
	}
	if choice.prev.Seq == math.MaxInt {
		return out, refuseRecover(RecoverSlotInvalid, "%s names the maximum chain sequence; no successor can be written", slot.path)
	}
	// The new body: what a launch records, observed now.
	rec, err := stagedRecord(o.Root, o.Handle)
	if err != nil {
		return out, refuseRecover(RecoverBaseUnobserved, "%v", err)
	}
	rp, err := worktreePaths(rec.Worktree)
	if err != nil {
		return out, refuseRecover(RecoverBaseUnobserved, "%v", err)
	}
	if rp.HeadRef == "" {
		return out, refuseRecover(RecoverBaseUnobserved, "the worktree %s has a detached HEAD", rec.Worktree)
	}
	henv := harnessEnv(o.Env)
	repo := seatRepo(o.Git, henv, rp)
	base, err := repo.line("rev-parse", "--verify", rp.HeadRef+"^{commit}")
	if err != nil {
		return out, refuseRecover(RecoverBaseUnobserved, "the worktree's HEAD %s: %v", rp.HeadRef, err)
	}
	// The carry checks run in the harness's own shadow git directory on
	// the seat's object store, with the fetch's credentials: the launch's
	// base fetch's pair (BaseGit, NewBaseShadow, FetchCredentials; the
	// plan reviewer on #904). None of the seat repository's config is read:
	// a url.*.insteadOf, a core.sshCommand or a transport setting the seat
	// planted never redirects the launch's push URL or runs in this
	// process.
	carried, err := carryEntries(o, slot, oldSeq, rp.Common)
	if err != nil {
		return out, err
	}
	now := o.Now.UTC()
	recovery := &LedgerRecovery{Seq: oldSeq, Slot: slot.path, BodySHA256: slot.bodySHA, HeadSHA256: slot.headSHA, By: o.Lead, Reason: o.Reason, At: now, Link: choice.prev.Seq + 1, SeqFrom: choice.seqFrom}
	for _, c := range carried {
		recovery.Carried = append(recovery.Carried, c.Task+":"+c.Commit)
	}
	evidenceSeq, err := evidenceSeqOf(repo, o.Handle)
	if err != nil {
		return out, refuseRecover(RecoverBaseUnobserved, "read the evidence refs: %v", err)
	}
	l := Ledger{Schema: LedgerSchema, Handle: o.Handle, Worktree: rec.Worktree, Branch: rec.Branch, Base: base, Commits: carried, At: now,
		Target: o.Target, EvidenceSeq: evidenceSeq, Recovered: recovery}
	if l.Commits == nil {
		l.Commits = []LedgerCommit{}
	}
	if err := guard(o); err != nil {
		return out, err
	}
	if choice.migrationSeq > 0 {
		if err := recordLayoutMigration(o.Root, o.Handle, choice.migrationSeq); err != nil {
			return out, refuseRecover(RecoverWriteFailed, "%v", err)
		}
	}
	link, err := recoverWrite(o.Root, o.Handle, choice.prev, l, choice.migrating && choice.migrationSeq == 0, now)
	if err != nil {
		return out, err
	}
	return RecoverResult{Handle: o.Handle, Slot: slot.path, OldSeq: oldSeq, NewSeq: link.Seq, Prev: choice.prev.Tag, SeqFrom: choice.seqFrom, BodySHA256: slot.bodySHA, HeadSHA256: slot.headSHA, Base: base, Carried: carried}, nil
}

// recordVerified is the recovery of a document that verifies now (the
// plan reviewer on c9826c31, the input where the scheduler's covers check
// decides): the slot must exist and be this seat's, the live chain must be
// at or past every sequence represented by the slot (a restored older
// document would not verify),
// nothing is carried (the entries stand), and the next link seals the same
// body with the recovery record marked Verified.
func recordVerified(o RecoverOptions, live Ledger, slotPath string, migrationSeq int) (RecoverResult, error) {
	var out RecoverResult
	slot, err := readSlot(o.Root, o.Handle, RecoverSlotName(o.Handle, o.Seq, o.Slot))
	var rr *RecoverRefusal
	if errors.As(err, &rr) && rr.Code == RecoverSlotMissing {
		return out, refuseRecover(RecoverNotTampered, "the commits ledger of seat %s verifies and there is no slot %s; there is nothing to recover", o.Handle, RecoverSlotName(o.Handle, o.Seq, o.Slot))
	}
	if err != nil {
		return out, err
	}
	choice, err := slot.link(o.Root, o.Handle)
	if err != nil {
		return out, err
	}
	oldSeq := choice.namedSeq
	at := 0 // a verifying ledger is sealed; the refusal never reads a nil seal
	if live.Seal != nil {
		at = live.Seal.Seq
	}
	if at < choice.prev.Seq {
		return out, refuseRecover(RecoverNotTampered, "the commits ledger of seat %s verifies at link %d, behind the slot's highest sequence %d; nothing to record", o.Handle, at, choice.prev.Seq)
	}
	if live.Seal == nil {
		return out, refuseRecover(RecoverSlotInvalid, "the commits ledger of seat %s verifies without a chain seal; no successor can be written", o.Handle)
	}
	if live.Seal.Seq == math.MaxInt {
		return out, refuseRecover(RecoverSlotInvalid, "the commits ledger of seat %s verifies at the maximum chain sequence; no successor can be written", o.Handle)
	}
	if len(o.Carry) > 0 {
		return out, refuseRecover(RecoverEntryUnknown, "the commits ledger of seat %s verifies and its entries stand; a verified recovery carries nothing", o.Handle)
	}
	now := o.Now.UTC()
	prev := live.Seal
	l := live
	l.Seal = nil
	l.Recovered = &LedgerRecovery{Seq: oldSeq, Slot: slot.path, BodySHA256: slot.bodySHA, HeadSHA256: slot.headSHA, By: o.Lead, Reason: o.Reason, At: now, Verified: true, Link: prev.Seq + 1, SeqFrom: "verified_head"}
	if err := guard(o); err != nil {
		return out, err
	}
	if migrationSeq > 0 {
		if err := recordLayoutMigration(o.Root, o.Handle, migrationSeq); err != nil {
			return out, refuseRecover(RecoverWriteFailed, "%v", err)
		}
	}
	link, err := recoverWrite(o.Root, o.Handle, prev, l, migrationSeq < 0, now)
	if err != nil {
		return out, err
	}
	return RecoverResult{Handle: o.Handle, Verified: true, Slot: slot.path, OldSeq: oldSeq, NewSeq: link.Seq, Prev: prev.Tag, SeqFrom: "verified_head", BodySHA256: slot.bodySHA, HeadSHA256: slot.headSHA, Base: live.Base}, nil
}

// recordAlarm is a decision by alarm (the plan reviewer's ruling on
// Codex's delta pass on c267382d): the lead releases the hold of one
// scheduler alarm, named by its id. It names no slot and no seq, so it
// releases a hold whose refusal quarantined nothing, and a hold whose slot
// the chain has moved past (a later slot at a lower seq recovered first).
// On a ledger that verifies, a line naming the alarm is appended to the
// seat's hold release file (alarms.go), the ledger itself unchanged; the
// same alarm decided again is answered from that file (AlreadyRecovered). On a ledger that does not
// verify (Codex on 254f2c2c: a refusal whose copy failed leaves a hold
// with no slot and no read that makes one), the decision makes only the
// lead's quarantine copy of the live bytes, the copy a refusing read
// makes, and is refused recover_alarm_unverified naming the slot and the
// slot recovery to run (the plan reviewer on 8cffafe7: never a rebuild);
// it is refused in words too when the copy fails or the ledger cannot be
// read as a document. Nothing is carried.
func recordAlarm(o RecoverOptions) (RecoverResult, error) {
	var out RecoverResult
	live, raw, rerr := readLedger(o.Root, o.Handle)
	if errors.Is(rerr, sealchain.ErrTampered) {
		// Copy only, never a rebuild (the plan reviewer's ruling on Codex's
		// pass on 8cffafe7: a rebuild here recovered a ledger the lead had
		// not seen). The lead's quarantine copy is the copy a refusing read
		// makes; the lead then recovers the slot with the slot recovery,
		// which shows what it carries, and sends the decision again.
		qerr := quarantine(o.Root, o.Handle, "scheduler recover --alarm", rerr, raw)
		var q *QuarantinedError
		if !errors.As(qerr, &q) {
			return out, refuseRecover(RecoverAlarmUnverified, "the commits ledger of seat %s does not verify, and the copy of it into the quarantine could not be made: %v; fix what the copy names, then send the decision again", o.Handle, qerr)
		}
		slotFlag := ""
		if k, err := slotIndex(o.Handle, q.Seq, q.Slot); err == nil && k > 1 {
			slotFlag = fmt.Sprintf(" --slot %d", k)
		}
		return out, refuseRecover(RecoverAlarmUnverified, "seat %s's ledger does not verify; it was copied to slot %s; recover it with `amq-squad scheduler recover --seat %s --seq %s%s --reason ...`, which shows what it carries; then, if the hold remains, send the alarm decision again", o.Handle, q.Slot, o.Handle, q.Seq, slotFlag)
	}
	if errors.Is(rerr, sealchain.ErrNewerWriter) || errors.Is(rerr, sealchain.ErrSchemaMismatch) {
		return out, incompatibleRecovery(o.Handle, rerr)
	}
	if rerr != nil {
		return out, refuseRecover(RecoverAlarmUnverified, "the commits ledger of seat %s cannot be read as a document (%v); the decision by alarm needs one to verify or to quarantine", o.Handle, rerr)
	}
	if live.Seal == nil {
		return out, refuseRecover(RecoverAlarmUnverified, "the commits ledger of seat %s has no seal", o.Handle)
	}
	// the decision goes to the seat's hold release file (alarms.go), never
	// into the ledger body; the same alarm decided again is answered from it
	have, err := ReadAlarms(o.Root, o.Handle)
	if err != nil {
		return out, refuseRecover(RecoverWriteFailed, "the hold release file of seat %s cannot be read: %v", o.Handle, err)
	}
	if have.Released(o.Alarm) {
		return RecoverResult{Handle: o.Handle, AlreadyRecovered: true, Verified: true, Alarm: o.Alarm, Base: live.Base}, nil
	}
	if err := guard(o); err != nil {
		return out, err
	}
	if err := appendAlarm(o.Root, AlarmDecision{Seat: o.Handle, Alarm: o.Alarm, Reason: o.Reason, At: o.Now.UTC(), Lead: o.Lead}); err != nil {
		return out, refuseRecover(RecoverWriteFailed, "the hold release file of seat %s: %v", o.Handle, err)
	}
	return RecoverResult{Handle: o.Handle, Verified: true, Alarm: o.Alarm, Base: live.Base}, nil
}

func incompatibleRecovery(handle string, err error) error {
	return refuseRecover(RecoverIncompatible, "the commits ledger of seat %s is not a tamper alarm: %v; scheduler recovery is tamper-only and writes or quarantines nothing", handle, err)
}

// guard runs the options' Guard, inside the ledger lock, immediately
// before a seal.
func guard(o RecoverOptions) error {
	if o.Guard == nil {
		return nil
	}
	return o.Guard()
}

// slotIndex is k of a slot path commits.<handle>.seq<N>[.k] (1 for the
// first, which has no suffix).
func slotIndex(handle, seq, slot string) (int, error) {
	base := quarantineBase(handle, seq)
	name := filepath.Base(slot)
	if name == base {
		return 1, nil
	}
	if !strings.HasPrefix(name, base+".") {
		return 0, fmt.Errorf("%s is not a slot of %s", name, base)
	}
	return strconv.Atoi(strings.TrimPrefix(name, base+"."))
}

// recoverWrite uses the same raw document writer as every ledger update,
// then reads the result back as any reader does. The predecessor came from
// the exact quarantined bytes; neither this function nor sealchain reopens
// the old head to choose it.
func recoverWrite(root, handle string, prev *sealchain.Link, l Ledger, migrating bool, now time.Time) (*sealchain.Link, error) {
	key, err := sealchain.SessionKey(root)
	if err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "the session seed: %v", err)
	}
	seal, err := saveRawLedgerBeforeHead(root, key, nil, prev, l, migrating, beforeRecoveryHeadWrite, now)
	if err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "%v", err)
	}
	if _, _, err := readLedger(root, handle); err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "the recovered ledger does not verify: %v", err)
	}
	return &sealchain.Link{Seq: seal.Seq, Prev: seal.Prev, Tag: seal.Tag}, nil
}

// beforeRecoveryHeadWrite is the R15 crash seam after the recovered body is
// durable and immediately before its head is written.
var beforeRecoveryHeadWrite func(sealchain.RawSeal) error

// readSlot reads one quarantine slot through the same descriptor-anchored,
// no-follow opens the copy uses, and checks it against its sidecar (the
// seat line, the digests of the copies present).
func readSlot(root, handle, name string) (*quarantinedSlot, error) {
	dir := QuarantineDir(root)
	d, err := openDirNoFollow(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, refuseRecover(RecoverSlotMissing, "seat %s has no quarantine (%s is absent)", handle, dir)
	}
	if err != nil {
		return nil, refuseRecover(RecoverSlotInvalid, "%v", err)
	}
	defer d.close()
	s, err := d.open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, refuseRecover(RecoverSlotMissing, "no quarantine slot %s under %s", name, dir)
	}
	if err != nil {
		return nil, refuseRecover(RecoverSlotInvalid, "%v", err)
	}
	defer s.close()
	q := &quarantinedSlot{name: name, path: filepath.Join(dir, name)}
	side, ok, err := s.read(quarantineSidecar)
	if err != nil {
		return nil, refuseRecover(RecoverSlotInvalid, "%v", err)
	}
	if !ok {
		return nil, refuseRecover(RecoverSlotInvalid, "%s is not a quarantine slot (no %s)", q.path, quarantineSidecar)
	}
	if q.body, q.hasBody, err = s.read(quarantineBodyFile); err != nil {
		return nil, refuseRecover(RecoverSlotInvalid, "%v", err)
	}
	if q.head, q.hasHead, err = s.read(quarantineHeadFile); err != nil {
		return nil, refuseRecover(RecoverSlotInvalid, "%v", err)
	}
	seat := ""
	for _, line := range strings.Split(string(side), "\n") {
		switch {
		case strings.HasPrefix(line, "seat: "):
			seat = strings.TrimPrefix(line, "seat: ")
		case strings.HasPrefix(line, "body: "+quarantineBodyFile+" sha256 "):
			q.bodySHA = strings.TrimPrefix(line, "body: "+quarantineBodyFile+" sha256 ")
		case strings.HasPrefix(line, "head: "+quarantineHeadFile+" sha256 "):
			q.headSHA = strings.TrimPrefix(line, "head: "+quarantineHeadFile+" sha256 ")
		}
	}
	switch {
	case seat != handle:
		return nil, refuseRecover(RecoverSlotInvalid, "%s is seat %q's quarantine, not seat %s's", q.path, seat, handle)
	case q.hasBody != (q.bodySHA != ""):
		return nil, refuseRecover(RecoverSlotInvalid, "%s: the sidecar and the body copy disagree on whether a body was copied", q.path)
	case q.hasBody && digestOf(q.body) != q.bodySHA:
		return nil, refuseRecover(RecoverSlotInvalid, "%s: the body copy's sha256 is not the sidecar's", q.path)
	case q.hasHead != (q.headSHA != ""):
		return nil, refuseRecover(RecoverSlotInvalid, "%s: the sidecar and the head copy disagree on whether a head was copied", q.path)
	case q.hasHead && digestOf(q.head) != q.headSHA:
		return nil, refuseRecover(RecoverSlotInvalid, "%s: the head copy's sha256 is not the sidecar's", q.path)
	}
	return q, nil
}

type slotLinkChoice struct {
	prev         *sealchain.Link
	namedSeq     int
	seqFrom      string
	migrating    bool
	migrationSeq int
}

// link selects the tag to continue from and a sequence above every copied
// value. The slot name follows the copied head's claimed seq even when its tag
// fails; the lead's decision authenticates this continuation and both hashes.
func (q *quarantinedSlot) link(root, handle string) (slotLinkChoice, error) {
	var out slotLinkChoice
	key, err := sealchain.SessionKey(root)
	if err != nil {
		return out, refuseRecover(RecoverSeqUnknown, "%s: the session seed: %v", q.path, err)
	}
	var head *sealchain.Link
	headLayout := ""
	if q.hasHead {
		if h, layout, herr := sealchain.HeadLink(ledgerHeadName(handle), key, q.head); herr == nil && h.Seq > 0 && h.Tag != "" {
			head, headLayout = &h, layout
		}
	}
	claimedSeq, hasClaimed := copiedHeadSeq(q.head)
	var body *sealchain.Link
	bodyRaw := false
	bodyKnown := false
	if q.hasBody {
		if _, seal, splitErr := sealchain.SplitRaw(q.body); splitErr == nil {
			bodyKnown = true
			if plausibleCopiedSeq(seal.Seq) {
				body = &sealchain.Link{Seq: seal.Seq, Prev: seal.Prev, Tag: seal.Tag}
				bodyRaw = true
			}
		} else {
			var l struct {
				Seal *sealchain.Link `json:"seal"`
			}
			if json.Unmarshal(q.body, &l) == nil && l.Seal != nil {
				bodyKnown = true
				if plausibleCopiedSeq(l.Seal.Seq) && l.Seal.Tag != "" {
					body = l.Seal
				}
			}
		}
	}
	if q.hasBody && !bodyKnown {
		return out, refuseRecover(RecoverSeqUnknown, "%s: its body copy is neither the raw layout nor a legacy ledger with an embedded seal", q.path)
	}
	if q.hasBody && body == nil && head == nil {
		return out, refuseRecover(RecoverSeqUnknown, "%s: neither its head copy nor its body copy names a plausible chain link to continue after", q.path)
	}
	if hasClaimed {
		out.namedSeq = claimedSeq
	} else if body != nil {
		out.namedSeq = body.Seq
	}
	maxSeq := 0
	setMax := func(seq int, from string) {
		if seq > maxSeq {
			maxSeq, out.seqFrom = seq, from
		}
	}
	if head != nil {
		setMax(head.Seq, "verified_head")
		out.prev = &sealchain.Link{Tag: head.Tag}
	}
	if hasClaimed {
		setMax(claimedSeq, "claimed_head")
	}
	if body != nil {
		setMax(body.Seq, "body_seal")
		if out.prev == nil {
			out.prev = &sealchain.Link{Tag: body.Tag}
		}
	}
	if out.prev == nil || maxSeq <= 0 || out.prev.Tag == "" {
		return slotLinkChoice{}, refuseRecover(RecoverSeqUnknown, "%s: neither its head copy nor its body copy names a chain link to continue after", q.path)
	}
	out.prev.Seq = maxSeq
	if head != nil {
		out.migrating = headLayout == ""
	} else {
		out.migrating = body != nil && !bodyRaw
	}
	// A raw immediate successor under a legacy head is an interrupted
	// migration. Its original migration record is keyed by that raw seq.
	if head != nil && headLayout == "" && bodyRaw && body.Seq == head.Seq+1 && body.Prev == head.Tag {
		out.prev.Tag = body.Tag
		out.migrationSeq = body.Seq
	}
	return out, nil
}

func copiedHeadSeq(raw []byte) (int, bool) {
	if body, _, err := sealchain.SplitRaw(raw); err == nil {
		raw = body
	}
	var h struct {
		Seq *int `json:"seq"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &h) != nil || h.Seq == nil || !plausibleCopiedSeq(*h.Seq) {
		return 0, false
	}
	return *h.Seq, true
}

func plausibleCopiedSeq(seq int) bool {
	return seq >= 1 && seq < maxPlausibleSeq
}

// carryEntries checks each named entry against the quarantined body and
// origin: the entry must be in the body by task and commit, the commit
// must be on origin's branch for the task (the entry's branch, else the
// task's branch), and its tree must equal the entry's. Any failure refuses
// the whole recovery: nothing is carried.
func carryEntries(o RecoverOptions, slot *quarantinedSlot, oldSeq int, common string) ([]LedgerCommit, error) {
	if len(o.Carry) == 0 {
		return nil, nil
	}
	var body Ledger
	bodyBytes, ok := slot.documentBody()
	if !ok || json.Unmarshal(bodyBytes, &body) != nil {
		return nil, refuseRecover(RecoverEntryUnknown, "%s holds no body that decodes; nothing can be carried from it", slot.path)
	}
	if o.Target.PushURL == "" {
		return nil, refuseRecover(RecoverEntryUnpushed, "the launch's grant records no push URL, so no entry is on origin; nothing can be carried")
	}
	cred, credEnv, _, why := FetchCredentials(o.Target, o.Env)
	if why != "" {
		return nil, refuseRecover(RecoverEntryUnpushed, "origin cannot be read: %s", why)
	}
	g := BaseGit{Bin: o.Git, Env: o.Env}
	sh, err := NewBaseShadow(g, common)
	if err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "the harness git directory: %v", err)
	}
	defer os.RemoveAll(sh.Dir)
	fg := BaseGit{Bin: o.Git, Pre: cred, Env: o.Env} // ls-remote only
	fenv := append(append([]string(nil), sh.Env...), credEnv...)
	repo := func(args ...string) (string, error) { return g.Run(sh.Dir, sh.Env, args...) }
	var out []LedgerCommit
	tips := map[string]string{}
	for _, c := range o.Carry {
		at := -1
		for i, e := range body.Commits {
			if e.Task == c.Task && e.Commit == c.Commit {
				at = i
			}
		}
		if at < 0 {
			return nil, refuseRecover(RecoverEntryUnknown, "the quarantined body of %s has no entry of task %s with commit %s", slot.path, c.Task, c.Commit)
		}
		e := body.Commits[at]
		branch := firstNonEmpty(e.Branch, TaskBranch(o.Handle, c.Task))
		tip, ok := tips[branch]
		if !ok {
			// the one network call, once per distinct branch, under the bound
			ctx, cancel := context.WithTimeout(context.Background(), RecoverTransportTimeout)
			line, err := fg.RunBounded(ctx, sh.Dir, fenv, "ls-remote", "--heads", "--", o.Target.PushURL, "refs/heads/"+branch)
			cancel()
			if err != nil {
				return nil, refuseRecover(RecoverEntryUnpushed, "origin's %s cannot be read (bound %s): %v", branch, RecoverTransportTimeout, err)
			}
			tip, _, _ = strings.Cut(line, "\t")
			tip = strings.TrimSpace(tip)
			tips[branch] = tip
		}
		if tip == "" {
			return nil, refuseRecover(RecoverEntryUnpushed, "origin has no branch %s, so commit %s of task %s is not on it; nothing is carried", branch, c.Commit, c.Task)
		}
		if _, err := repo("cat-file", "-e", tip+"^{commit}"); err != nil {
			return nil, refuseRecover(RecoverEntryUnpushed, "origin's %s is at %s, which the seat repository does not hold; nothing is carried", branch, tip)
		}
		if _, err := repo("merge-base", "--is-ancestor", c.Commit, tip); err != nil {
			return nil, refuseRecover(RecoverEntryUnpushed, "commit %s of task %s is not on origin's %s (at %s); nothing is carried", c.Commit, c.Task, branch, tip)
		}
		tree, err := repo("rev-parse", "--verify", "-q", c.Commit+"^{tree}")
		if err != nil || tree != e.Tree {
			return nil, refuseRecover(RecoverEntryTreeMismatch, "commit %s of task %s has tree %q, not the quarantined entry's %s; nothing is carried", c.Commit, c.Task, tree, e.Tree)
		}
		e.Pushed = true
		e.Note = "carried from quarantine seq " + strconv.Itoa(oldSeq)
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out, nil
}

func (q *quarantinedSlot) documentBody() ([]byte, bool) {
	if !q.hasBody {
		return nil, false
	}
	if body, _, err := sealchain.SplitRaw(q.body); err == nil {
		return body, true
	}
	var legacy struct {
		Seal *sealchain.Link `json:"seal"`
	}
	if json.Unmarshal(q.body, &legacy) != nil || legacy.Seal == nil || legacy.Seal.Seq <= 0 || legacy.Seal.Tag == "" {
		return nil, false
	}
	return q.body, true
}

// RecoveriesOf is what the scheduler's tick releases a hold from: the
// ledger's own chain record, read without a quarantine (nil when the
// ledger does not verify or records none), the alarm ids of the verified
// lines of the seat's hold release file (alarms.go), and that file's
// problems (the lines that release nothing, for the lead); ok when either
// names anything.
func RecoveriesOf(root, handle string) (slot *LedgerRecovery, alarms []string, problems []string, ok bool) {
	if l, _, err := readLedger(root, handle); err == nil {
		slot = l.Recovered
	}
	log, err := ReadAlarms(root, handle)
	if err != nil {
		problems = append(problems, "the hold release file cannot be read: "+err.Error())
	}
	for _, d := range log.Decisions {
		alarms = append(alarms, d.Alarm)
	}
	problems = append(problems, log.Problems...)
	return slot, alarms, problems, slot != nil || len(alarms) > 0 || len(problems) > 0
}

// RecoveryOf is the recovery a seat's ledger records, read without a
// quarantine: ok when the ledger verifies and carries one. The scheduler's
// tick reconciles a held seat against it (the sealed ledger is the source
// of truth of a completed recovery; a decision line lost or a crash before
// the state write loses nothing).
func RecoveryOf(root, handle string) (LedgerRecovery, bool) {
	l, _, err := readLedger(root, handle)
	if err != nil || l.Recovered == nil {
		return LedgerRecovery{}, false
	}
	return *l.Recovered, true
}

// evidenceSeqOf is the highest evidence ref number the seat repository
// holds for this seat (the counter the new chain continues from, so a
// later anchor never overwrites a ref an earlier chain made).
func evidenceSeqOf(repo git, handle string) (int, error) {
	out, err := readEvidenceRefs(repo, handle)
	if err != nil {
		return 0, fmt.Errorf("git for-each-ref evidence refs: %w", err)
	}
	max := 0
	for _, ref := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(ref[strings.LastIndexByte(ref, '/')+1:]); err == nil && n > max {
			max = n
		}
	}
	return max, nil
}

var readEvidenceRefs = func(repo git, handle string) ([]byte, error) {
	return repo.run(nil, nil, "for-each-ref", "--format=%(refname)", "refs/amq-squad/evidence/"+refPart(handle)+"/")
}
