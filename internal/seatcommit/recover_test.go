package seatcommit

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

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

func rawDocumentBody(t *testing.T, raw []byte) []byte {
	t.Helper()
	if body, _, err := sealchain.SplitRaw(raw); err == nil {
		return body
	}
	return raw
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
	key, err := sealchain.SessionKey(f.root)
	if err != nil {
		t.Fatal(err)
	}
	head, _, err := sealchain.HeadLink("commits."+f.handle, key, headBefore)
	if err != nil || head.Seq != 1 || head.Tag == "" {
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
	if res.OldSeq != 1 || res.NewSeq != 2 || res.Prev != head.Tag || res.Slot != q.Slot || res.BodySHA256 != digestOf(tampered) || res.HeadSHA256 != digestOf(headBefore) || res.Base != f.base {
		t.Fatalf("the result: %+v", res)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatalf("the recovered ledger does not verify: %v", err)
	}
	if l.Seal == nil || l.Seal.Seq != 2 || l.Seal.Prev != head.Tag {
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
	if _, _, err := sealchain.SplitRaw(recovered); err != nil {
		t.Fatalf("recovered document is not raw layout: %v", err)
	}
	if _, _, err := sealchain.SplitRaw(f.headBytes()); err != nil {
		t.Fatalf("recovered head is not raw layout: %v", err)
	}
	if decisions, err := os.ReadFile(filepath.Join(f.root, "extensions", "amq-squad", "scheduler", "decisions.jsonl")); err == nil && bytes.Contains(decisions, []byte(`"kind":"ledger_layout_migrated"`)) {
		t.Fatalf("raw-to-raw recovery recorded a migration: %s", decisions)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
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

func TestRecoveryRefusesWhenEvidenceRefsCannotBeObserved(t *testing.T) {
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	bodyBefore := readFile(t, ledgerPath(f.root, f.handle))
	headBefore := f.headBytes()
	want := errors.New("for-each-ref failed")
	old := readEvidenceRefs
	readEvidenceRefs = func(git, string) ([]byte, error) { return nil, want }
	t.Cleanup(func() { readEvidenceRefs = old })
	_, err := f.recover(q.Seq, 1)
	if recoverCode(t, err) != RecoverBaseUnobserved || !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("evidence refusal = %v", err)
	}
	if !bytes.Equal(bodyBefore, readFile(t, ledgerPath(f.root, f.handle))) || !bytes.Equal(headBefore, f.headBytes()) {
		t.Fatal("evidence-ref refusal changed the ledger")
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
	_, oldSeal, err := sealchain.SplitRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawDocumentBody(t, raw), &doc); err != nil {
		t.Fatal(err)
	}
	entry := func(taskID, commit, tree string) map[string]any {
		return map[string]any{"task": taskID, "commit": commit, "parent": real.Parent, "tree": tree, "at": real.At, "pushed": true, "branch": real.Branch, "base": real.Base}
	}
	doc["commits"] = append(doc["commits"].([]any), entry(tk.ID, unpushed, real.Tree), entry("t-forged", real.Commit, strings.Repeat("f", 40)))
	tamperedBody, _ := json.MarshalIndent(doc, "", "  ")
	tampered, _ := sealchain.JoinRaw(tamperedBody, oldSeal)
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

// Codex's P1 on #904: the carry check ran ls-remote through the seat
// repository's git, whose config the seat controls: a url.<evil>.insteadOf
// of the launch's push URL redirected the check to a repository holding
// the unpushed commit, and it passed; a core.sshCommand there would run a
// seat-chosen command in the scheduler's process. The check runs in the
// harness's own shadow git directory with the fetch's credentials (the
// launch's base fetch's pair): the planted redirect moves the seat's own
// git (shown) and not the recovery, which reads the real origin and
// refuses the unpushed entry, and the planted ssh command never runs.
// Reverted (the seat's git), the forged branch is carried.
func TestCarryCheckIgnoresTheSeatsGitConfig(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// carried\n")
	tk := f.reported("cmd/")
	res := f.run(tk)
	if !res.OK || !res.Pushed {
		t.Fatalf("the commit step: %+v", res)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil || len(l.Commits) != 1 {
		t.Fatal(err)
	}
	real := l.Commits[0]
	unpushed := sh(t, f.wt, "commit-tree", real.Tree, "-p", real.Parent, "-m", "never pushed to origin")
	// the evil repository holds the task branch at the unpushed commit,
	// and the seat's config sends the origin's URL there
	evil := filepath.Join(f.dir, "evil.git")
	sh(t, f.dir, "init", "-q", "--bare", evil)
	sh(t, f.wt, "push", "-q", evil, unpushed+":refs/heads/"+real.Branch)
	sh(t, f.wt, "config", "url."+evil+".insteadOf", f.origin)
	marker := filepath.Join(f.dir, "ssh-ran")
	sh(t, f.wt, "config", "core.sshCommand", "sh -c 'touch "+marker+"; exit 1' --")
	if got := sh(t, f.wt, "ls-remote", "--heads", f.origin, "refs/heads/"+real.Branch); !strings.HasPrefix(got, unpushed) {
		t.Fatalf("the planted redirect does not move the seat's own git: %q", got)
	}
	// the planted command is live in the seat's git: an ssh URL runs it
	if out, err := runGit(t, f.wt, "--git-dir="+sh(t, f.wt, "rev-parse", "--path-format=absolute", "--git-common-dir"), "ls-remote", "ssh://planted.invalid/x.git", "refs/heads/main"); err == nil {
		t.Fatalf("the planted ssh command did not refuse: %q", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the planted ssh command is not live in the seat's git: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	raw := readFile(t, ledgerPath(f.root, f.handle))
	_, oldSeal, err := sealchain.SplitRaw(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(rawDocumentBody(t, raw), &doc); err != nil {
		t.Fatal(err)
	}
	doc["commits"] = append(doc["commits"].([]any), map[string]any{"task": tk.ID, "commit": unpushed, "parent": real.Parent, "tree": real.Tree, "at": real.At, "pushed": true, "branch": real.Branch, "base": real.Base})
	tamperedBody, _ := json.MarshalIndent(doc, "", "  ")
	tampered, _ := sealchain.JoinRaw(tamperedBody, oldSeal)
	if err := os.WriteFile(ledgerPath(f.root, f.handle), tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	_, err = f.recover(q.Seq, 1, CarryEntry{Task: tk.ID, Commit: unpushed})
	if recoverCode(t, err) != RecoverEntryUnpushed || !strings.Contains(err.Error(), "is not on origin's "+real.Branch) {
		t.Fatalf("the redirected check: %v", err)
	}
	if _, err := ReadLedger(f.root, f.handle); !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("the refused recovery wrote something: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the recovery ran the seat's planted ssh command")
	}
	// the real entry still carries through the harness's git
	if _, err := f.recover(q.Seq, 1, CarryEntry{Task: tk.ID, Commit: real.Commit}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the carry ran the seat's planted ssh command")
	}
}

// Codex's P2 on #904: an origin that does not answer (the ssh plumbing
// stands in with a sleep) ends in a refusal within RecoverTransportTimeout,
// its process group killed (the stand-in's own pid, which it writes, is
// dead after; Codex on c9826c31: killing git alone would leave it), with
// nothing written and the ledger lock released: the next recovery takes
// the lock at once (and, carrying nothing, completes). Reverted (no
// bound), the recovery waits for git; reverted (git killed alone), the
// stand-in lives on.
func TestStalledOriginEndsTheRecoveryWithinTheBound(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// stalled\n")
	tk := f.reported("cmd/")
	res := f.run(tk)
	if !res.OK || !res.Pushed {
		t.Fatalf("the commit step: %+v", res)
	}
	l, _ := ReadLedger(f.root, f.handle)
	real := l.Commits[0]
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	prev := RecoverTransportTimeout
	RecoverTransportTimeout = 500 * time.Millisecond
	t.Cleanup(func() { RecoverTransportTimeout = prev })
	target := f.target()
	target.PushURL = "ssh://stalled.invalid/x.git"
	pidFile := filepath.Join(f.dir, "stand-in.pid")
	start := time.Now()
	_, err := RecoverLedger(RecoverOptions{Root: f.root, Handle: f.handle, Seq: q.Seq, Slot: 1, Carry: []CarryEntry{{Task: tk.ID, Commit: real.Commit}}, Target: target,
		Git: gitBin(t), Env: append(f.testGitEnv(), "GIT_SSH_COMMAND=sh -c 'echo $$ > "+pidFile+"; sleep 60' --"), Lead: "cto", Reason: "r", Now: f.now})
	took := time.Since(start)
	pidRaw, perr := os.ReadFile(pidFile)
	if perr != nil {
		t.Fatalf("the stand-in did not write its pid: %v", perr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidRaw)))
	alive := pid > 0
	for i := 0; i < 50 && alive; i++ {
		alive = processAlive(pid)
		if alive {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if alive {
		t.Fatalf("the transport's child %d is still alive after the bound", pid)
	}
	if recoverCode(t, err) != RecoverEntryUnpushed || !strings.Contains(err.Error(), "(bound 500ms)") || !strings.Contains(err.Error(), "did not answer within its bound; killed") {
		t.Fatalf("the stalled origin: %v", err)
	}
	if took > 10*time.Second {
		t.Fatalf("the refusal took %s", took)
	}
	if _, err := ReadLedger(f.root, f.handle); !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("the refused recovery wrote something: %v", err)
	}
	// the lock is free: the next recovery takes it at once
	type outcome struct {
		r   RecoverResult
		err error
	}
	done := make(chan outcome, 1)
	go func() { r, err := f.recover(q.Seq, 1); done <- outcome{r, err} }()
	select {
	case got := <-done:
		if got.err != nil || got.r.NewSeq != got.r.OldSeq+1 {
			t.Fatalf("the recovery after the refusal: %v %+v", got.err, got.r)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the ledger lock is still held")
	}
}

// On the 0e layout Recovered and Note remain known schema-1 fields. The raw
// tag covers their exact bytes, and an actually unknown field under schema 1
// is schema_mismatch rather than tamper or newer_writer.
func TestRecoveredLedgerRemainsStrictSchemaOne(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// carried\n")
	tk := f.reported("cmd/")
	if res := f.run(tk); !res.OK {
		t.Fatalf("the commit step: %+v", res)
	}
	l, _ := ReadLedger(f.root, f.handle)
	real := l.Commits[0]
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	if _, err := f.recover(q.Seq, 1, CarryEntry{Task: tk.ID, Commit: real.Commit}); err != nil {
		t.Fatal(err)
	}
	recovered, err := ReadLedger(f.root, f.handle)
	if err != nil || recovered.Recovered == nil || recovered.Commits[0].Note == "" {
		t.Fatalf("this binary's reader: %v %+v", err, recovered.Recovered)
	}
	key, ok, err := sealchain.ReadKey(f.root)
	if err != nil || !ok {
		t.Fatal(err)
	}
	body, seal, err := sealchain.SplitRaw(readFile(t, ledgerPath(f.root, f.handle)))
	if err != nil || seal.Schema != LedgerSchema || !seal.Verify(key.Key, ledgerLabel, body) {
		t.Fatalf("raw schema/tag: schema=%d err=%v", seal.Schema, err)
	}
	if !bytes.Contains(body, []byte(`"recovered"`)) || !bytes.Contains(body, []byte(`"note"`)) {
		t.Fatal("the schema-1 raw body does not carry Recovered and Note")
	}
	var older struct {
		Schema int    `json:"schema"`
		Handle string `json:"handle"`
	}
	err = sealchain.DecodeStrict("the seat's commit record", body, seal, LedgerSchema, &older)
	if !errors.Is(err, sealchain.ErrSchemaMismatch) || errors.Is(err, sealchain.ErrNewerWriter) || errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("a schema-1 unknown field classification: %v", err)
	}
	writeUnknownCommitField(t, f, "v3.2.0-rc.2", LedgerSchema)
	if _, err := ReadLedger(f.root, f.handle); !errors.Is(err, sealchain.ErrSchemaMismatch) || errors.Is(err, sealchain.ErrNewerWriter) || errors.Is(err, sealchain.ErrTampered) || !strings.Contains(err.Error(), "future") {
		t.Fatalf("ledger reader's schema-1 unknown field classification: %v", err)
	}
}

// Codex's P2 on #904, the retry: a recovery already in the sealed ledger,
// decided again for the same slot, is answered as made (AlreadyRecovered,
// the record's seqs, the carried entries) with the ledger unchanged; the
// same slot named for another seq is not it. Reverted (the retry refused
// recover_not_tampered), an interrupted completion can never be finished.
func TestRecoveryDecidedAgainIsAnsweredFromTheLedger(t *testing.T) {
	f := newFixture(t)
	tamperBase(t, f, f.base, '0')
	q := f.quarantined()
	first, err := f.recover("1", 1)
	if err != nil {
		t.Fatal(err)
	}
	written := readFile(t, ledgerPath(f.root, f.handle))
	again, err := f.recover("1", 1)
	if err != nil || !again.AlreadyRecovered || again.OldSeq != first.OldSeq || again.NewSeq != first.NewSeq || again.Slot != q.Slot || again.BodySHA256 != first.BodySHA256 {
		t.Fatalf("the retry: %v %+v", err, again)
	}
	if !bytes.Equal(readFile(t, ledgerPath(f.root, f.handle)), written) {
		t.Fatal("the retry wrote the ledger")
	}
	if _, err := f.recover("1", 2); recoverCode(t, err) != RecoverNotTampered {
		t.Fatalf("another slot on a recovered ledger: %v", err)
	}
}

// The plan reviewer on c9826c31, the reachable input: a transient alarm
// (the session seed unreadable for one read) quarantines the document and
// the scheduler holds the seat; the seed readable again, the document
// verifies with no record of the slot, and a recovery would have refused
// not_tampered, so the hold could never have been released. The decision
// is recorded as the next link of the same body (Verified), nothing
// restarted and nothing carried, and RecoveryOf then answers the slot so
// the scheduler's reconciliation releases the hold; the quarantine copy
// is unchanged. A carry on a verifying ledger is refused. Reverted (the
// verifying document refused), the hold is a dead end.
func TestADocumentVerifyingAgainIsReleasedByARecordedDecision(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a mode-0 seed")
	}
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// transient\n")
	tk := f.reported("cmd/")
	if res := f.run(tk); !res.OK {
		t.Fatalf("the commit step: %+v", res)
	}
	before, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatal(err)
	}
	seed := sealchain.SeedPath(f.root)
	if err := os.Chmod(seed, 0); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	if err := os.Chmod(seed, 0o600); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Err.Error(), "the session seed") || q.Seq != strconv.Itoa(before.Seal.Seq) {
		t.Fatalf("the transient alarm: %v (seq %s)", q.Err, q.Seq)
	}
	if _, err := ReadLedger(f.root, f.handle); err != nil {
		t.Fatalf("the document does not verify again: %v", err)
	}
	if _, ok := RecoveryOf(f.root, f.handle); ok {
		t.Fatal("a record before the decision")
	}
	slotBody := readFile(t, filepath.Join(q.Slot, quarantineBodyFile))
	if _, err := f.recover(q.Seq, 1, CarryEntry{Task: tk.ID, Commit: before.Commits[0].Commit}); recoverCode(t, err) != RecoverEntryUnknown {
		t.Fatalf("a carry on a verifying ledger: %v", err)
	}
	res, err := f.recover(q.Seq, 1)
	if err != nil || !res.Verified || res.OldSeq != before.Seal.Seq || res.NewSeq != before.Seal.Seq+1 || res.Slot != q.Slot {
		t.Fatalf("the verified recovery: %v %+v", err, res)
	}
	after, err := ReadLedger(f.root, f.handle)
	if err != nil || after.Seal.Seq != before.Seal.Seq+1 || after.Seal.Prev != before.Seal.Tag {
		t.Fatalf("the next link: %v %+v", err, after.Seal)
	}
	if after.Recovered == nil || !after.Recovered.Verified || after.Recovered.Slot != q.Slot || after.Recovered.Seq != before.Seal.Seq || after.Recovered.BodySHA256 != digestOf(slotBody) {
		t.Fatalf("the record: %+v", after.Recovered)
	}
	if len(after.Commits) != 1 || after.Commits[0].Commit != before.Commits[0].Commit || after.Commits[0].Note != "" || after.Base != before.Base || after.Rest != before.Rest {
		t.Fatalf("the body changed: %+v", after)
	}
	if rec, ok := RecoveryOf(f.root, f.handle); !ok || rec.Slot != q.Slot {
		t.Fatalf("RecoveryOf after the decision: %v %+v", ok, rec)
	}
	if !bytes.Equal(readFile(t, filepath.Join(q.Slot, quarantineBodyFile)), slotBody) {
		t.Fatal("the quarantine copy changed")
	}
	// the slot holds the refused bytes; the document moved on to the new link
	if bytes.Equal(slotBody, readFile(t, ledgerPath(f.root, f.handle))) {
		t.Fatal("the document did not move to the new link")
	}
}

// Codex on b250b951 (the P3): a verified decision on a slot whose seq is
// behind the live chain (the transient alarm at seq N, then another commit
// step) writes the link after the live one, and a retry of the same
// decision reports that link and Verified from the record (Link), never
// the slot's seq plus one. Reverted (the retry's link rebuilt from the
// slot's seq), the retry reports N+1.
func TestAVerifiedRecoveryRetriedReportsItsLink(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads a mode-0 seed")
	}
	f := newFixture(t)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// one\n")
	one := f.reported("cmd/")
	if res := f.run(one); !res.OK {
		t.Fatalf("the first commit step: %+v", res)
	}
	if _, err := f.store.Complete(one.ID, f.now); err != nil {
		t.Fatal(err)
	}
	seed := sealchain.SeedPath(f.root)
	if err := os.Chmod(seed, 0); err != nil {
		t.Fatal(err)
	}
	q := f.quarantined()
	if err := os.Chmod(seed, 0o600); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// two\n")
	if res := f.run(f.reported("cmd/")); !res.OK {
		t.Fatalf("the second commit step: %+v", res)
	}
	live, err := ReadLedger(f.root, f.handle)
	if err != nil || strconv.Itoa(live.Seal.Seq) == q.Seq {
		t.Fatalf("the live chain is not ahead of the slot: %v %+v (slot seq %s)", err, live.Seal, q.Seq)
	}
	first, err := f.recover(q.Seq, 1)
	if err != nil || !first.Verified || first.NewSeq != live.Seal.Seq+1 {
		t.Fatalf("the verified decision: %v %+v (live %d)", err, first, live.Seal.Seq)
	}
	again, err := f.recover(q.Seq, 1)
	if err != nil || !again.AlreadyRecovered || !again.Verified || again.NewSeq != first.NewSeq {
		t.Fatalf("the retry: %v %+v, want link %d verified", err, again, first.NewSeq)
	}
	rec, ok := RecoveryOf(f.root, f.handle)
	if !ok || rec.Link != first.NewSeq {
		t.Fatalf("the record's link: %v %+v", ok, rec)
	}
}

// The plan reviewer on af1a6124, a recovery from another slot: the
// document refused at seq 1 (slot X), then refused again after another
// edit (slot X.2, whose bytes are the live document's). The decision for X
// is refused while the live document is X.2's; the recovery from X.2
// continues the chain and names X.2; the document now verifies, and the
// decision for X is then recorded in the verified mode, naming X: here
// X.2 is at X's own seq, so the chain is at or past X's. When the later
// slot is at a lower seq the verified mode refuses X and only the decision
// by alarm answers its hold (TestADecisionByAlarmAnswersAHoldNoSlotCan).
// Reverted (the verifying document refused), X stays unanswerable.
func TestAHoldOnAnEarlierSlotIsAnsweredAfterALaterRecovery(t *testing.T) {
	f := newFixture(t)
	original := readFile(t, ledgerPath(f.root, f.handle))
	tamperBase(t, f, f.base, '0')
	x := f.quarantined()
	write(t, ledgerPath(f.root, f.handle), string(original))
	tamperBase(t, f, f.base, '1')
	y := f.quarantined()
	if x.Seq != "1" || y.Seq != "1" || y.Slot != x.Slot+".2" {
		t.Fatalf("the slots: %s %s / %s %s", x.Seq, x.Slot, y.Seq, y.Slot)
	}
	if _, err := f.recover("1", 1); err == nil {
		t.Fatal("the decision for X was taken while the live document is X.2's")
	}
	later, err := f.recover("1", 2)
	if err != nil || later.Slot != y.Slot || later.Verified {
		t.Fatalf("the recovery from X.2: %v %+v", err, later)
	}
	first, err := f.recover("1", 1)
	if err != nil || !first.Verified || first.Slot != x.Slot || first.NewSeq != later.NewSeq+1 {
		t.Fatalf("the decision for X after X.2's recovery: %v %+v", err, first)
	}
	if rec, ok := RecoveryOf(f.root, f.handle); !ok || rec.Slot != x.Slot || !rec.Verified || rec.Link != first.NewSeq {
		t.Fatalf("the record: %v %+v", ok, rec)
	}
}

// Codex's delta pass on c267382d (2), on real files, and the decision by
// alarm's ledger half (the plan reviewer's ruling): the chain at link N
// (two commit steps); the document refused at seq N (slot X); its head then lowered to seq 1
// and refused again (slot Y, a lower seq). X is refused while the live
// document is Y's; Y's recovery continues the chain at link 2; X is then
// refused again, its seq past the chain (no verified recovery can answer
// it), so a hold on X is released only by the decision naming its alarm.
// On the ledger that verifies it appends a sealed line naming the alarm to
// the seat's hold release file, the ledger unchanged; sent again it is
// answered from that file with no new line; a carry with it is refused.
func TestADecisionByAlarmAnswersAHoldNoSlotCan(t *testing.T) {
	f := newFixture(t)
	for _, line := range []string{"// one\n", "// two\n"} {
		write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n"+line)
		tk := f.reported("cmd/")
		if res := f.run(tk); !res.OK {
			t.Fatalf("the commit step: %+v", res)
		}
		if _, err := f.store.Complete(tk.ID, f.now); err != nil {
			t.Fatal(err)
		}
	}
	live, err := ReadLedger(f.root, f.handle)
	if err != nil || live.Seal.Seq < 3 {
		t.Fatalf("the chain: %v %+v", err, live.Seal)
	}
	n := strconv.Itoa(live.Seal.Seq)
	original := readFile(t, ledgerPath(f.root, f.handle))
	tamperBase(t, f, f.base, '0')
	x := f.quarantined()
	write(t, ledgerPath(f.root, f.handle), string(original))
	editCopiedHeadSeq(t, f, 1)
	y := f.quarantined()
	if x.Seq != n || y.Seq != "1" {
		t.Fatalf("the slots: X seq %s, Y seq %s", x.Seq, y.Seq)
	}
	if _, err := f.recover(n, 1); err == nil {
		t.Fatal("X was recovered while the live document is Y's")
	}
	ry, err := f.recover("1", 1)
	if err != nil || ry.Slot != y.Slot || ry.NewSeq != live.Seal.Seq+1 || ry.SeqFrom != "body_seal" {
		t.Fatalf("Y's recovery: %v %+v", err, ry)
	}
	if _, err := RecoverLedger(RecoverOptions{Root: f.root, Handle: f.handle, Alarm: "3fa9c1d2", Carry: []CarryEntry{{Task: "t1", Commit: strings.Repeat("a", 40)}}, Lead: "cto", Reason: "r", Now: f.now}); recoverCode(t, err) != RecoverSlotInvalid {
		t.Fatalf("a decision by alarm with a carry: %v", err)
	}
	before, err := ReadLedger(f.root, f.handle)
	if err != nil {
		t.Fatal(err)
	}
	res, err := RecoverLedger(RecoverOptions{Root: f.root, Handle: f.handle, Alarm: "3fa9c1d2", Lead: "cto", Reason: "X's alarm, investigated", Now: f.now})
	if err != nil || !res.Verified || res.Alarm != "3fa9c1d2" {
		t.Fatalf("the decision by alarm: %v %+v", err, res)
	}
	// the decision is a line of the hold release file; the ledger is unchanged
	after, err := ReadLedger(f.root, f.handle)
	if err != nil || after.Seal.Seq != before.Seal.Seq || after.Seal.Tag != before.Seal.Tag {
		t.Fatalf("the ledger moved: %v %+v", err, after.Seal)
	}
	log, err := ReadAlarms(f.root, f.handle)
	if err != nil || len(log.Problems) != 0 || len(log.Decisions) != 1 {
		t.Fatalf("the hold release file: %v %+v", err, log)
	}
	if d := log.Decisions[0]; d.Alarm != "3fa9c1d2" || d.Seat != f.handle || d.Lead != "cto" || d.Reason != "X's alarm, investigated" || !d.At.Equal(f.now.UTC()) {
		t.Fatalf("the line: %+v", d)
	}
	// the chain's own record (Y's recovery) stays; nothing replaces it
	if after.Recovered == nil || after.Recovered.Slot != y.Slot {
		t.Fatalf("Y's record: %+v", after.Recovered)
	}
	if len(after.Commits) != len(before.Commits) || after.Base != before.Base {
		t.Fatalf("the body changed: %+v", after)
	}
	again, err := RecoverLedger(RecoverOptions{Root: f.root, Handle: f.handle, Alarm: "3fa9c1d2", Lead: "cto", Reason: "again", Now: f.now})
	if err != nil || !again.AlreadyRecovered {
		t.Fatalf("the same decision again: %v %+v", err, again)
	}
	if log, _ := ReadAlarms(f.root, f.handle); len(log.Decisions) != 1 {
		t.Fatalf("the repeat added a line: %+v", log)
	}
}
