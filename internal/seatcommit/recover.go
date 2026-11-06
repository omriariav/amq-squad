package seatcommit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
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
// The chain continues; it does not restart. The new first link takes seq
// N+1 after the quarantined seq N, its prev is the quarantined head's link
// tag (so it links to the quarantined head), and its body records the
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
	Verified   bool           `json:"verified,omitempty"`
	Slot       string         `json:"slot"`
	OldSeq     int            `json:"old_seq"`
	NewSeq     int            `json:"new_seq"`
	Prev       string         `json:"prev"`
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
)

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
	if o.Handle == "" || !recoverSeqRe.MatchString(o.Seq) || o.Slot < 0 {
		return out, refuseRecover(RecoverSlotInvalid, "a recovery names a seat, a head seq (or %s) and a slot number", quarantineSeqUnknown)
	}
	for _, c := range o.Carry {
		if c.Task == "" || !recoverCommitRe.MatchString(c.Commit) {
			return out, refuseRecover(RecoverEntryUnknown, "a carried entry is named by its task id and its full commit id")
		}
	}
	if o.Git == "" {
		o.Git = "git"
	}
	if o.Env == nil {
		o.Env = os.Environ()
	}
	if o.Now.IsZero() {
		o.Now = time.Now()
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
		// The decision was applied and its completion interrupted (the
		// decision line not appended, the hold not released): the retry
		// returns the recovery as the sealed ledger records it, so the
		// caller records and releases; nothing is written again.
		out = RecoverResult{Handle: o.Handle, AlreadyRecovered: true, Slot: slotPath, OldSeq: live.Recovered.Seq, NewSeq: live.Recovered.Seq + 1,
			BodySHA256: live.Recovered.BodySHA256, HeadSHA256: live.Recovered.HeadSHA256, Base: live.Base}
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
		return recordVerified(o, live, slotPath)
	case errors.Is(rerr, os.ErrNotExist):
		return out, refuseRecover(RecoverNotTampered, "the commits ledger of seat %s is absent with no sealed head; the launch records a new one", o.Handle)
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
	oldSeq, prev, err := slot.link()
	if err != nil {
		return out, err
	}
	if o.Seq != quarantineSeqUnknown && strconv.Itoa(oldSeq) != o.Seq {
		return out, refuseRecover(RecoverSlotInvalid, "%s names seq %s but its head records link %d", slot.path, o.Seq, oldSeq)
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
	recovery := &LedgerRecovery{Seq: oldSeq, Slot: slot.path, BodySHA256: slot.bodySHA, HeadSHA256: slot.headSHA, By: o.Lead, Reason: o.Reason, At: now}
	for _, c := range carried {
		recovery.Carried = append(recovery.Carried, c.Task+":"+c.Commit)
	}
	l := Ledger{Schema: LedgerSchema, Handle: o.Handle, Worktree: rec.Worktree, Branch: rec.Branch, Base: base, Commits: carried, At: now,
		Target: o.Target, EvidenceSeq: evidenceSeqOf(repo, o.Handle), Recovered: recovery}
	if l.Commits == nil {
		l.Commits = []LedgerCommit{}
	}
	link, err := sealWrite(o.Root, o.Handle, &sealchain.Link{Seq: oldSeq, Tag: prev}, l, now)
	if err != nil {
		return out, err
	}
	return RecoverResult{Handle: o.Handle, Slot: slot.path, OldSeq: oldSeq, NewSeq: link.Seq, Prev: prev, BodySHA256: slot.bodySHA, HeadSHA256: slot.headSHA, Base: base, Carried: carried}, nil
}

// recordVerified is the recovery of a document that verifies now (the
// plan reviewer on c9826c31, the input where the scheduler's covers check
// decides): the slot must exist and be this seat's, the live chain must be
// at or past the slot's seq (a restored older document would not verify),
// nothing is carried (the entries stand), and the next link seals the same
// body with the recovery record marked Verified.
func recordVerified(o RecoverOptions, live Ledger, slotPath string) (RecoverResult, error) {
	var out RecoverResult
	slot, err := readSlot(o.Root, o.Handle, RecoverSlotName(o.Handle, o.Seq, o.Slot))
	var rr *RecoverRefusal
	if errors.As(err, &rr) && rr.Code == RecoverSlotMissing {
		return out, refuseRecover(RecoverNotTampered, "the commits ledger of seat %s verifies and there is no slot %s; there is nothing to recover", o.Handle, RecoverSlotName(o.Handle, o.Seq, o.Slot))
	}
	if err != nil {
		return out, err
	}
	oldSeq, _, err := slot.link()
	if err != nil {
		return out, err
	}
	if live.Seal == nil || live.Seal.Seq < oldSeq {
		return out, refuseRecover(RecoverNotTampered, "the commits ledger of seat %s verifies at link %d, behind the slot's seq %d; nothing to record", o.Handle, live.Seal.Seq, oldSeq)
	}
	if len(o.Carry) > 0 {
		return out, refuseRecover(RecoverEntryUnknown, "the commits ledger of seat %s verifies and its entries stand; a verified recovery carries nothing", o.Handle)
	}
	now := o.Now.UTC()
	prev := live.Seal
	l := live
	l.Seal = nil
	l.Recovered = &LedgerRecovery{Seq: oldSeq, Slot: slot.path, BodySHA256: slot.bodySHA, HeadSHA256: slot.headSHA, By: o.Lead, Reason: o.Reason, At: now, Verified: true}
	link, err := sealWrite(o.Root, o.Handle, prev, l, now)
	if err != nil {
		return out, err
	}
	return RecoverResult{Handle: o.Handle, Verified: true, Slot: slot.path, OldSeq: oldSeq, NewSeq: link.Seq, Prev: prev.Tag, BodySHA256: slot.bodySHA, HeadSHA256: slot.headSHA, Base: live.Base}, nil
}

// sealWrite seals l as the link after prev with the session key, writes
// the body and moves the head, and reads the chain back as any reader
// reads it. It is the recovery's own write (never writeLedger, whose
// check it is the way back from).
func sealWrite(root, handle string, prev *sealchain.Link, l Ledger, now time.Time) (*sealchain.Link, error) {
	key, err := sealchain.SessionKey(root)
	if err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "the session seed: %v", err)
	}
	link := sealchain.Seal(key.Key, ledgerLabel, prev, l)
	sealed := l
	sealed.Seal = link
	b, err := json.MarshalIndent(sealed, "", "  ")
	if err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "%v", err)
	}
	if err := os.MkdirAll(LedgerDir(root), 0o700); err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "%v", err)
	}
	if err := atomicfile.Write(ledgerPath(root, handle), append(b, '\n'), 0o600); err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "%v", err)
	}
	if err := sealchain.WriteHead(root, ledgerHeadName(handle), key, link, now); err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "the head: %v", err)
	}
	if _, _, err := readLedger(root, handle); err != nil {
		return nil, refuseRecover(RecoverWriteFailed, "the recovered ledger does not verify: %v", err)
	}
	return link, nil
}

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

// link is the quarantined head's seq and link tag: from the head copy when
// it decodes, else from the body copy's own seal.
func (q *quarantinedSlot) link() (seq int, prev string, err error) {
	if q.hasHead {
		var h struct {
			Seq  *int   `json:"seq"`
			Link string `json:"link_tag"`
		}
		if json.Unmarshal(q.head, &h) == nil && h.Seq != nil && *h.Seq > 0 && h.Link != "" {
			return *h.Seq, h.Link, nil
		}
	}
	if q.hasBody {
		var l struct {
			Seal *sealchain.Link `json:"seal"`
		}
		if json.Unmarshal(q.body, &l) == nil && l.Seal != nil && l.Seal.Seq > 0 && l.Seal.Tag != "" {
			return l.Seal.Seq, l.Seal.Tag, nil
		}
	}
	return 0, "", refuseRecover(RecoverSeqUnknown, "%s: neither its head copy nor its body copy names a chain link to continue after", q.path)
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
	if !slot.hasBody || json.Unmarshal(slot.body, &body) != nil {
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
func evidenceSeqOf(repo git, handle string) int {
	out, err := repo.run(nil, nil, "for-each-ref", "--format=%(refname)", "refs/amq-squad/evidence/"+refPart(handle)+"/")
	if err != nil {
		return 0
	}
	max := 0
	for _, ref := range strings.Fields(string(out)) {
		if n, err := strconv.Atoi(ref[strings.LastIndexByte(ref, '/')+1:]); err == nil && n > max {
			max = n
		}
	}
	return max
}
