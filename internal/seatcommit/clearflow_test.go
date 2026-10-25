package seatcommit

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The Buzz-signed seat clear's ledger rows (phase 5, the narrow pass F1, F3,
// F4 and the seed): every consumer of a clear verifies it, the sealed ledger
// is the only use mark, a hooks-silent clear holds for its incarnation, and a
// clear of a task-store quarantine leaves a seed entry the launch continues.

// switchTrust is a clear verifier that fails while broken is set: the owner's
// answer cannot be checked (an unreadable gate store).
type switchTrust struct {
	inner  ClearVerifier
	broken *bool
}

func (s switchTrust) Verify(keyID string, payload, sig []byte) error {
	if *s.broken {
		return errors.New("the gate store cannot be read")
	}
	return s.inner.Verify(keyID, payload, sig)
}

// F1: the commit step (dry run included), the exit check and the widen dry
// run all call ClearRecord.Verified and refuse on any failure.
func TestEveryConsumerOfAClearVerifiesIt(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// work\n")
	write(t, filepath.Join(f.wt, "internal", "client", "factory_test.go"), "package client\n// touched\n")
	tk := f.reported("cmd/root.go", "internal/client/factory_test.go")
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":2}`)
	if res := f.run(tk); !res.Quarantine {
		t.Fatalf("no quarantine: %+v", res)
	}
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	if _, err := ClearQuarantine(f.root, f.handle, f.signer, f.now); err != nil {
		t.Fatal(err)
	}
	broken := false
	o := f.opts(tk)
	o.Clear = switchTrust{f.trust(), &broken}
	widen := func() WidenDecision {
		return CheckWiden(WidenOptions{Root: f.root, Task: tk, Added: []string{"cmd/other.go"}, Grants: f.grants, RepoRoot: f.main, Home: f.home, Commit: &o})
	}
	dry := o
	dry.DryRun = true

	broken = true
	if r := ExitCheck(o, "gone"); r.OK || !r.Quarantine || r.ReasonCode != ReasonQuarantineUnverified {
		t.Fatalf("exit check on an unverifiable clear: %+v", r)
	}
	if r := Run(dry); r.OK || r.ReasonCode != ReasonQuarantineUnverified {
		t.Fatalf("dry run on an unverifiable clear: %+v", r)
	}
	if d := widen(); d.Allowed || d.ReasonCode != ReasonQuarantineUnverified {
		t.Fatalf("widen on an unverifiable clear: %+v", d)
	}
	if r := Run(o); r.OK || r.ReasonCode != ReasonQuarantineUnverified || f.originBranch() != "" {
		t.Fatalf("commit on an unverifiable clear: %+v", r)
	}

	broken = false
	if r := ExitCheck(o, "gone"); !r.OK {
		t.Fatalf("exit check on a verified clear: %+v", r)
	}
	if r := Run(dry); !r.OK {
		t.Fatalf("dry run on a verified clear: %+v", r)
	}
	if d := widen(); !d.Allowed {
		t.Fatalf("widen on a verified clear: %+v", d)
	}
	if r := Run(o); !r.OK || !r.Pushed {
		t.Fatalf("commit on a verified clear: %+v", r)
	}
}

// F4: the owner's clear of a hooks-silent condition holds for that launch; a
// new lease is a new launch and is checked fresh.
func TestClearedHooksSilenceIsNotRaisedAgainOnTheSameIncarnation(t *testing.T) {
	f := newFixture(t)
	stage := func(id string, at time.Time) {
		t.Helper()
		w, err := claudeseat.Write(claudeseat.Dir(f.root, f.handle, id), "dontAsk", f.wt, f.now)
		if err != nil {
			t.Fatal(err)
		}
		rec := launch.Record{Handle: f.handle, Root: f.root, Session: "sess", Runtime: "claude", Worktree: f.wt, Branch: f.branch, LaunchID: id, ClaudeConfig: w.Digest}
		if err := launch.WriteFile(filepath.Join(launch.StagedDir(f.root, f.handle), launch.FileName), rec); err != nil {
			t.Fatal(err)
		}
		if err := launch.WriteProof(f.root, launch.ProofEntry{Handle: f.handle, PID: 4242, Start: "s", At: at}); err != nil {
			t.Fatal(err)
		}
	}
	stage("launch-1", f.now)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// work\n")
	tk := f.reported("cmd/")
	o := f.opts(tk)
	if r := ExitCheck(o, "gone"); !r.Quarantine || r.ReasonCode != claudeseat.HooksSilent {
		t.Fatalf("a silent seat: %+v", r)
	}
	l, _ := ReadLedger(f.root, f.handle)
	if l.Quarantine == nil || l.Quarantine.Launch != "launch-1" {
		t.Fatalf("the quarantine does not name its launch: %+v", l.Quarantine)
	}
	if _, err := ClearQuarantine(f.root, f.handle, f.signer, f.now); err != nil {
		t.Fatal(err)
	}
	if r := ExitCheck(o, "gone"); !r.OK {
		t.Fatalf("the cleared silence was raised again on its launch: %+v", r)
	}
	if r := f.run(tk); !r.OK || !r.Pushed {
		t.Fatalf("the commit on the cleared launch: %+v", r)
	}
	if r := ExitCheck(o, "gone"); !r.OK {
		t.Fatalf("the silence came back after the commit lifted the quarantine: %+v", r)
	}
	// a new lease: a new launch, detection runs fresh
	stage("launch-2", f.now.Add(time.Hour))
	if r := ExitCheck(o, "gone"); !r.Quarantine || r.ReasonCode != claudeseat.HooksSilent {
		t.Fatalf("a new lease, silent again: %+v", r)
	}
	if l, _ := ReadLedger(f.root, f.handle); l.Quarantine == nil || l.Quarantine.Launch != "launch-2" || l.Quarantine.ClearedAt != nil || len(l.Cleared) != 1 {
		t.Fatalf("the new incarnation's quarantine: %+v cleared=%d", l.Quarantine, len(l.Cleared))
	}
}

// seatTwo stages a second seat, s2, in its own worktree with a task reported.
func (f *fixture) seatTwo() (task.Task, string) {
	f.t.Helper()
	wt := filepath.Join(f.dir, "wt", "s2")
	sh(f.t, f.main, "worktree", "add", "-q", "-b", "seat/s2", wt, "main")
	rec := launch.Record{Handle: "s2", Root: f.root, Session: "sess", Runtime: "claude", Worktree: wt, Branch: "seat/s2"}
	if err := os.MkdirAll(launch.StagedDir(f.root, "s2"), 0o700); err != nil {
		f.t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(f.root, "s2"), launch.FileName), rec); err != nil {
		f.t.Fatal(err)
	}
	tk, err := f.store.Add(task.AddInput{Title: "two", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: f.main}, f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.store.Claim(tk.ID, "s2", nil, time.Hour, f.now); err != nil {
		f.t.Fatal(err)
	}
	harnessPass(f.t, f.store, tk.ID, f.now)
	if tk, err = f.store.Done(tk.ID, "s2", "ok", "", f.now); err != nil {
		f.t.Fatal(err)
	}
	write(f.t, filepath.Join(wt, "cmd", "root.go"), "package cmd\n// two\n")
	return tk, wt
}

// The seed: a clear of a quarantine that lives in the task store alone
// creates a typed, sealed entry (no worktree, branch or base). A diff with no
// RecordBase is base_unrecorded; the launch's RecordBase continues the entry,
// the commit completes, and the clear record is still on the entry.
func TestSeedEntryIsContinuedByRecordBaseAndKeepsTheClear(t *testing.T) {
	f := newFixture(t)
	tk, wt := f.seatTwo()
	o := f.opts(tk)
	o.Handle = "s2"
	seed := &LedgerQuarantine{Task: tk.ID, Reason: "claude_hooks_silent", At: f.now.Add(-time.Minute), Config: nil}
	gate := ClearGate{GateID: "clear-0001", EventID: strings.Repeat("e1", 32)}
	l, err := ClearQuarantineSeeded(f.root, "s2", f.signer, f.now, seed, func(*LedgerQuarantine) (ClearGate, error) { return gate, nil })
	if err != nil || !l.Seeded || l.Worktree != "" || l.Base != "" || l.Quarantine == nil || l.Quarantine.Clear == nil {
		t.Fatalf("the seed: %+v %v", l, err)
	}
	if used, err := ClearEventUsed(f.root, "s2", gate.EventID); err != nil || !used {
		t.Fatalf("the seed does not hold the used answer: %v %v", used, err)
	}
	if r := ExitCheck(o, "gone"); !r.OK {
		t.Fatalf("a seed with no base at the exit check: %+v", r)
	}
	if r := Run(o); r.OK || r.ReasonCode != ReasonBaseUnrecorded {
		t.Fatalf("a diff on a seed with no RecordBase: %+v", r)
	}
	if _, err := RecordWorktreeBase(gitBin(t), f.root, "s2", wt, "seat/s2", f.target(), f.now); err != nil {
		t.Fatal(err)
	}
	l, _ = ReadLedger(f.root, "s2")
	if l.Seeded || l.Worktree != wt || l.Base == "" || l.Quarantine == nil || l.Quarantine.Clear == nil || l.Quarantine.Clear.EventID != gate.EventID {
		t.Fatalf("RecordBase did not continue the seed: %+v", l)
	}
	if r := Run(o); !r.OK || !r.Pushed {
		t.Fatalf("the commit on the continued seed: %+v", r)
	}
	l, _ = ReadLedger(f.root, "s2")
	if l.Quarantine != nil || len(l.Cleared) != 1 || l.Cleared[0].Clear == nil || l.Cleared[0].Clear.EventID != gate.EventID {
		t.Fatalf("the clear record left the entry: %+v", l)
	}
	if used, _ := ClearEventUsed(f.root, "s2", gate.EventID); !used {
		t.Fatal("the answer is unused once its quarantine was lifted")
	}
}

// F3: the sealed ClearRecord is the only use mark: a failure between the
// authorization and the one ledger write leaves the answer usable, and an
// answer whose event id is on the ledger is used, from the ledger alone.
func TestClearRecordOnTheLedgerIsTheOnlyUseMark(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// work\n")
	tk := f.reported("cmd/")
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":2}`)
	if res := f.run(tk); !res.Quarantine {
		t.Fatalf("no quarantine: %+v", res)
	}
	event := strings.Repeat("ab", 32)
	authorize := func(*LedgerQuarantine) (ClearGate, error) {
		return ClearGate{GateID: "clear-0002", EventID: event}, nil
	}
	// the crash: authorization passed, the signature fails
	if _, err := ClearQuarantineWith(f.root, f.handle, failingSigner{}, f.now, authorize); err == nil {
		t.Fatal("a failed signature cleared")
	}
	if used, err := ClearEventUsed(f.root, f.handle, event); err != nil || used {
		t.Fatalf("the answer was burned by a failed clear: %v %v", used, err)
	}
	if l, _ := ReadLedger(f.root, f.handle); l.Quarantine == nil || l.Quarantine.ClearedAt != nil {
		t.Fatalf("the quarantine moved: %+v", l.Quarantine)
	}
	// the next run completes with the same answer
	if _, err := ClearQuarantineWith(f.root, f.handle, f.signer, f.now, authorize); err != nil {
		t.Fatal(err)
	}
	if used, _ := ClearEventUsed(f.root, f.handle, event); !used {
		t.Fatal("the completed clear does not mark its answer used on the ledger")
	}
	if used, _ := ClearEventUsed(f.root, f.handle, strings.Repeat("ff", 32)); used {
		t.Fatal("another event reads as used")
	}
}

type failingSigner struct{}

func (failingSigner) KeyID() string { return "test-key" }
func (failingSigner) Sign([]byte) ([]byte, error) {
	return nil, errors.New("crash before the ledger write")
}

// P2: a widening is allowed only on an explicitly clean dry run; every other
// refusal of the dry run refuses the widening with its own code.
func TestWidenRefusesWhatTheDryRunRefuses(t *testing.T) {
	f := newFixture(t)
	tk, _ := f.seatTwo()
	o := f.opts(tk)
	o.Handle = "s2"
	widen := func(o Options) WidenDecision {
		return CheckWiden(WidenOptions{Root: f.root, Task: tk, Added: []string{"cmd/other.go"}, Grants: f.grants, RepoRoot: f.main, Home: f.home, Commit: &o})
	}
	// a seeded lifted quarantine with no base
	seed := &LedgerQuarantine{Task: tk.ID, Reason: "claude_hooks_silent", At: f.now.Add(-time.Minute)}
	if _, err := ClearQuarantineSeeded(f.root, "s2", f.signer, f.now, seed, func(*LedgerQuarantine) (ClearGate, error) {
		return ClearGate{GateID: "clear-0003", EventID: strings.Repeat("c3", 32)}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if d := widen(o); d.Allowed || d.ReasonCode != ReasonBaseUnrecorded {
		t.Fatalf("widen on a seed with no base: %+v", d)
	}
	// a ledger edited without resealing
	g := newFixture(t)
	gtk := g.reported("cmd/")
	raw, err := os.ReadFile(ledgerPath(g.root, g.handle))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ledgerPath(g.root, g.handle), []byte(strings.Replace(string(raw), `"seat/s1"`, `"seat/s1x"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	go_ := g.opts(gtk)
	d := CheckWiden(WidenOptions{Root: g.root, Task: gtk, Added: []string{"cmd/other.go"}, Grants: g.grants, RepoRoot: g.main, Home: g.home, Commit: &go_})
	if d.Allowed || d.ReasonCode != ReasonLedgerTampered {
		t.Fatalf("widen on a tampered ledger: %+v", d)
	}
}

// P4: two callers that authorized the same answer outside the lock: the
// first writes the one record, the second finds the quarantine lifted and
// reports already_cleared, never success.
func TestConcurrentClearsWriteOneRecordAndOneIsAlreadyCleared(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// work\n")
	tk := f.reported("cmd/")
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":2}`)
	if res := f.run(tk); !res.Quarantine {
		t.Fatalf("no quarantine: %+v", res)
	}
	authorize := func(*LedgerQuarantine) (ClearGate, error) {
		return ClearGate{GateID: "clear-0004", EventID: strings.Repeat("d4", 32)}, nil
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = ClearQuarantineWith(f.root, f.handle, f.signer, f.now, authorize)
		}()
	}
	wg.Wait()
	ok, already := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrAlreadyCleared):
			already++
		default:
			t.Fatalf("unexpected: %v", err)
		}
	}
	if ok != 1 || already != 1 {
		t.Fatalf("successes %d, already_cleared %d (%v)", ok, already, errs)
	}
	if l, _ := ReadLedger(f.root, f.handle); l.Quarantine == nil || l.Quarantine.Clear == nil || len(l.Cleared) != 0 {
		t.Fatalf("not exactly one record: %+v", l)
	}
}
