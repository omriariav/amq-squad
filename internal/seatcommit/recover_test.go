package seatcommit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The rows of 3.2.0-0c-B's ledger half (recover.go, commit.go): the
// recovery continues the chain after the quarantined seq, checks what it
// carries, refuses what it cannot check, and the commit step's write-time
// refusal is the typed block. Every row runs under the fixture's temp root
// with a temp HOME (rule 18).

func (f *fixture) recover(seq string, slot int, carry ...CarryEntry) (RecoverResult, error) {
	f.t.Helper()
	f.t.Setenv("HOME", f.home)
	return RecoverLedger(RecoverOptions{Root: f.root, Handle: f.handle, Seq: seq, Slot: slot, Carry: carry, Target: f.target(),
		Git: gitBin(f.t), Env: f.testGitEnv(), Lead: "cto", Reason: "the edit is understood", Now: f.now})
}

func (f *fixture) headBytes() []byte {
	return readFile(f.t, sealchain.HeadPath(f.root, "commits."+f.handle))
}

// quarantined reads the ledger so the refusal quarantines it and returns
// the slot.
func (f *fixture) quarantined() *QuarantinedError {
	f.t.Helper()
	_, err := ReadLedger(f.root, f.handle)
	var q *QuarantinedError
	if !errors.As(err, &q) {
		f.t.Fatalf("the read did not quarantine: %v", err)
	}
	return q
}

func recoverCode(t *testing.T, err error) string {
	t.Helper()
	var rr *RecoverRefusal
	if !errors.As(err, &rr) {
		t.Fatalf("not a recovery refusal: %v", err)
	}
	return rr.Code
}

// B2 and B3c: the recovery of a tampered ledger (seq 1) writes a chain
// whose first link is seq 2 with prev = the quarantined head's link tag,
// records the quarantined body's sha256 and the slot's path, the worktree
// and branch of the launch record, the base as the worktree's HEAD and
// the grant; the quarantine copy is byte-identical after; the recovered
// ledger verifies; and the quarantined body copied back over it is refused
// as tampered (its seq is behind the head). Reverted (the chain restarted
// at seq 1 with no prev), the first link does not continue the chain.
func TestRecoveryContinuesTheChainAfterTheQuarantinedSeq(t *testing.T) {
	f := newFixture(t)
	headBefore := f.headBytes()
	var head struct {
		Seq  int    `json:"seq"`
		Link string `json:"link_tag"`
	}
	if err := json.Unmarshal(headBefore, &head); err != nil || head.Seq != 1 || head.Link == "" {
		t.Fatalf("the head before: %v %+v", err, head)
	}
	original := readFile(t, ledgerPath(f.root, f.handle))
	tampered := tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if q.Seq != "1" || !bytes.Equal(readFile(t, filepath.Join(q.Slot, quarantineBodyFile)), tampered) {
		t.Fatalf("the slot: %+v", q)
	}
	slotBefore := map[string][]byte{}
	for _, name := range slotFiles() {
		slotBefore[name] = readFile(t, filepath.Join(q.Slot, name))
	}
	res, err := f.recover("1", 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.OldSeq != 1 || res.NewSeq != 2 || res.Prev != head.Link || res.Slot != q.Slot || res.BodySHA256 != digestOf(tampered) || res.HeadSHA256 != digestOf(headBefore) || res.Base != f.base {
		t.Fatalf("the result: %+v", res)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatalf("the recovered ledger does not verify: %v", err)
	}
	if l.Seal == nil || l.Seal.Seq != 2 || l.Seal.Prev != head.Link {
		t.Fatalf("the first link does not continue the chain after seq 1: %+v", l.Seal)
	}
	if l.Recovered == nil || l.Recovered.Seq != 1 || l.Recovered.Slot != q.Slot || l.Recovered.BodySHA256 != digestOf(tampered) || l.Recovered.By != "cto" || len(l.Recovered.Carried) != 0 {
		t.Fatalf("the recovery record: %+v", l.Recovered)
	}
	if l.Worktree != f.wt || l.Branch != f.branch || l.Base != f.base || l.Target != f.target() || len(l.Commits) != 0 || l.Lease != nil || l.Rest != "" {
		t.Fatalf("the recovered body: %+v", l)
	}
	for _, name := range slotFiles() {
		if !bytes.Equal(readFile(t, filepath.Join(q.Slot, name)), slotBefore[name]) {
			t.Fatalf("the quarantine's %s changed", name)
		}
	}
	// B3c: the quarantined document copied back cannot be replayed (its
	// tag fails), and neither can the seq-1 document it was edited from,
	// whose tag verifies: its seq is behind the head
	recovered := readFile(t, ledgerPath(f.root, f.handle))
	for name, body := range map[string][]byte{"the quarantined body": tampered, "the seq-1 original": original} {
		if err := os.WriteFile(ledgerPath(f.root, f.handle), body, 0o600); err != nil {
			t.Fatal(err)
		}
		_, err = ReadLedger(f.root, f.handle)
		if !errors.Is(err, sealchain.ErrTampered) {
			t.Fatalf("%s copied back was not refused as tampered: %v", name, err)
		}
		if body := original; bytes.Equal(body, original) && name == "the seq-1 original" && !strings.Contains(err.Error(), "is at link 1, its head records link 2") {
			t.Fatalf("%s copied back is not refused for its seq: %v", name, err)
		}
	}
	if err := os.WriteFile(ledgerPath(f.root, f.handle), recovered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLedger(f.root, f.handle); err != nil {
		t.Fatalf("the recovered document put back: %v", err)
	}
	// the next writer continues the chain from the recovery
	if _, err := RecordTarget(f.root, f.handle, f.target()); err != nil {
		t.Fatal(err)
	}
	if l, err = ReadLedger(f.root, f.handle); err != nil || l.Seal.Seq != 3 || l.Recovered == nil {
		t.Fatalf("the write after the recovery: %v %+v", err, l.Seal)
	}
}

// The refusals: a ledger that verifies has nothing to recover; a slot that
// is not there; a slot whose seat is another's; and a document on disk
// that is not the slot's (edited again since) is quarantined in a slot of
// its own and the decision refused as stale, with nothing written.
// Reverted (the disk not compared to the slot), a decision on one body
// recovers over another.
func TestRecoveryRefusesWhatItCannotCheck(t *testing.T) {
	f := newFixture(t)
	if _, err := f.recover("1", 1); recoverCode(t, err) != RecoverNotTampered {
		t.Fatalf("a verifying ledger: %v", err)
	}
	first := tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if _, err := f.recover("7", 1); recoverCode(t, err) != RecoverSlotMissing {
		t.Fatalf("a missing slot: %v", err)
	}
	if _, err := f.recover("1", 2); recoverCode(t, err) != RecoverSlotMissing {
		t.Fatalf("a missing k: %v", err)
	}
	// a slot under this seat's name whose sidecar is another seat's (a
	// planted copy): refused by the sidecar, never by the name
	planted := q.Slot + ".2"
	if err := os.Mkdir(planted, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range slotFiles() {
		b := readFile(t, filepath.Join(q.Slot, name))
		if name == quarantineSidecar {
			b = bytes.Replace(b, []byte("seat: s1\n"), []byte("seat: s2\n"), 1)
		}
		if err := os.WriteFile(filepath.Join(planted, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.recover("1", 2); recoverCode(t, err) != RecoverSlotInvalid {
		t.Fatalf("another seat's sidecar: %v", err)
	}
	if err := os.RemoveAll(planted); err != nil {
		t.Fatal(err)
	}
	// edited again since the slot was made, no reader in between
	second := tamperBase(t, f, strings.Repeat("0", len(f.base)), '1')
	_, err := f.recover("1", 1)
	if recoverCode(t, err) != RecoverSlotStale || !strings.Contains(err.Error(), "quarantined at "+q.Slot+".2") {
		t.Fatalf("a stale slot: %v", err)
	}
	if got := readFile(t, filepath.Join(q.Slot+".2", quarantineBodyFile)); !bytes.Equal(got, second) {
		t.Fatal("the document on disk was not quarantined in its own slot")
	}
	if got := readFile(t, filepath.Join(q.Slot, quarantineBodyFile)); !bytes.Equal(got, first) {
		t.Fatal("the first slot changed")
	}
	if got := readFile(t, ledgerPath(f.root, f.handle)); !bytes.Equal(got, second) {
		t.Fatal("the refused recovery wrote the ledger")
	}
	// the stale decision's slot named, decided on: accepted
	if _, err := f.recover("1", 2); err != nil {
		t.Fatalf("the slot of the document on disk: %v", err)
	}
	if l, err := ReadLedger(f.root, f.handle); err != nil || l.Seal.Seq != 2 || l.Recovered.BodySHA256 != digestOf(second) {
		t.Fatalf("the recovery of the second slot: %v %+v", err, l.Recovered)
	}
}

// B5: entries named by the decision are carried into the new chain, marked
// "carried from quarantine seq N" and pushed; unnamed ones are not; naming
// a commit absent from origin's branch for the task, one whose tree
// differs from the quarantined entry's, or one the quarantined body does
// not hold, is refused and nothing is carried (the ledger stays as it
// was). Reverted (the tree not compared), a forged tree is carried.
func TestRecoveryCarriesOnlyCheckedEntries(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// carried\n")
	tk := f.reported("cmd/")
	res := f.run(tk)
	if !res.OK || !res.Pushed || res.Commit == "" {
		t.Fatalf("the commit step: %+v", res)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil || len(l.Commits) != 1 {
		t.Fatalf("the ledger after the step: %v %+v", err, l.Commits)
	}
	real := l.Commits[0]
	// an unpushed commit of the same tree (a local object, on no origin
	// branch), and a forged tree on the real commit
	unpushed := sh(t, f.wt, "commit-tree", real.Tree, "-p", real.Parent, "-m", "never pushed")
	var doc map[string]any
	raw := readFile(t, ledgerPath(f.root, f.handle))
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	entry := func(taskID, commit, tree string) map[string]any {
		return map[string]any{"task": taskID, "commit": commit, "parent": real.Parent, "tree": tree, "at": real.At, "pushed": true, "branch": real.Branch, "base": real.Base}
	}
	doc["commits"] = append(doc["commits"].([]any), entry(tk.ID, unpushed, real.Tree), entry("t-forged", real.Commit, strings.Repeat("f", 40)))
	tampered, _ := json.MarshalIndent(doc, "", "  ")
	if err := os.WriteFile(ledgerPath(f.root, f.handle), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	seq := q.Seq
	for name, tc := range map[string]struct {
		carry CarryEntry
		code  string
	}{
		"unpushed":      {CarryEntry{Task: tk.ID, Commit: unpushed}, RecoverEntryUnpushed},
		"tree mismatch": {CarryEntry{Task: "t-forged", Commit: real.Commit}, RecoverEntryTreeMismatch},
		"unknown":       {CarryEntry{Task: "nope", Commit: real.Commit}, RecoverEntryUnknown},
	} {
		_, err := f.recover(seq, 1, CarryEntry{Task: tk.ID, Commit: real.Commit}, tc.carry)
		if recoverCode(t, err) != tc.code {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := ReadLedger(f.root, f.handle); !errors.Is(err, sealchain.ErrTampered) {
			t.Fatalf("%s: the refused recovery wrote something: %v", name, err)
		}
	}
	if got := readFile(t, ledgerPath(f.root, f.handle)); !bytes.Equal(got, tampered) {
		t.Fatal("a refused recovery changed the document")
	}
	out, err := f.recover(seq, 1, CarryEntry{Task: tk.ID, Commit: real.Commit})
	if err != nil {
		t.Fatal(err)
	}
	l, err = ReadLedger(f.root, f.handle)
	if err != nil || len(l.Commits) != 1 {
		t.Fatalf("the recovered ledger: %v %+v", err, l.Commits)
	}
	c := l.Commits[0]
	if c.Task != tk.ID || c.Commit != real.Commit || c.Tree != real.Tree || !c.Pushed || c.Note != "carried from quarantine seq "+seq {
		t.Fatalf("the carried entry: %+v", c)
	}
	if len(out.Carried) != 1 || l.Recovered == nil || len(l.Recovered.Carried) != 1 || l.Recovered.Carried[0] != tk.ID+":"+real.Commit {
		t.Fatalf("the recovery record: %+v %+v", out, l.Recovered)
	}
	// the carried commit is on origin's task branch; the step's anchor ref
	// and the evidence counter are what the repository holds
	if f.originRef(real.Branch) != real.Commit {
		t.Fatalf("origin's %s is not at the carried commit", real.Branch)
	}
}

// B6: a write-time refusal at the commit step (the document edited after
// the step's read and before its write, the AfterSnapshot seam) is the
// typed block ledger_tampered on the task, routed to the lead, with the
// quarantine path in its detail; not a deferral. Nothing is pushed.
// Reverted (the deferral note), the step defers and the next tick runs it
// into the same refusal.
func TestWriteTimeRefusalIsTheTypedBlock(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// write-time\n")
	tk := f.reported("cmd/")
	o := f.opts(tk)
	var tampered []byte
	o.AfterSnapshot = func(string) { tampered = tamperBase(t, f, f.base, '0') }
	res := Run(o)
	if res.OK || res.Deferred || res.ReasonCode != ReasonLedgerTampered || res.Route != task.RouteLead {
		t.Fatalf("the write-time refusal is not the typed block for the lead: %+v", res)
	}
	slot := filepath.Join(QuarantineDir(f.root), "commits."+f.handle+".seq1")
	if !strings.Contains(res.Detail, "the commits ledger refused the write") || !strings.Contains(res.Detail, "quarantined at "+slot) {
		t.Fatalf("the detail does not name the write and the slot: %s", res.Detail)
	}
	if got := readFile(t, filepath.Join(slot, quarantineBodyFile)); !bytes.Equal(got, tampered) {
		t.Fatal("the slot does not hold the refused body")
	}
	if f.originBranch() != "" {
		t.Fatal("something was pushed")
	}
	if got := readFile(t, ledgerPath(f.root, f.handle)); !bytes.Equal(got, tampered) {
		t.Fatal("the step moved the document")
	}
}
