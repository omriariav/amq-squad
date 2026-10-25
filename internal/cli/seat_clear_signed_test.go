package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The Buzz-signed seat clear (phase 5): with the owner's Buzz enrolment
// present the pty is only the request half. These rows run it through the
// gate fixture and its in-memory relay.

// signedClearSeat is a quarantined seat s1 in the gate fixture's session,
// the owner's HOME being the fixture's (enrolment present).
func signedClearSeat(t *testing.T) (gateCLIFixture, task.Store, task.Task) {
	t.Helper()
	f := newGateCLIFixture(t)
	mustWrite(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	mustWrite(t, filepath.Join(f.home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	repo, wt := seatRepo(t, f.root)
	s, _ := task.Open(f.root)
	tk, _ := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: repo}, time.Now())
	s.Claim(tk.ID, "s1", nil, time.Hour, time.Now())
	mustWrite(t, filepath.Join(wt, "cmd", "root.go"), "package cmd\n// w\n")
	s.Done(tk.ID, "s1", "ok", "", time.Now())
	mustWrite(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1,"hooks":{"PreToolUse":[]}}`)
	if _, _, err := captureOutput(t, func() error { return runSeatCommit([]string{"--seat", "s1", "--root", f.root}, os.Stdout) }); ExitCode(err) != ExitCommitRefused {
		t.Fatalf("seat commit: %v", err)
	}
	return f, s, tk
}

func signedClear(t *testing.T, f gateCLIFixture) (string, error) {
	t.Helper()
	out, _, err := captureOutput(t, func() error {
		return runSeatClear([]string{"--seat", "s1", "--root", f.root, "--project", f.project, "--session", "s"}, os.Stdout)
	})
	return out, err
}

// clearGateOf is the one open quarantine_clear gate of the session.
func clearGateOf(t *testing.T, f gateCLIFixture) (gatestore.Record, gatestore.Question) {
	t.Helper()
	store, _ := gatestore.Open(f.root)
	ids, _ := store.IDs()
	for _, id := range ids {
		if rec, err := store.Load(id); err == nil && rec.Class == "quarantine_clear" {
			q, err := store.Question(id)
			if err != nil || q == nil {
				t.Fatalf("clear gate %s has no posted question: %v", id, err)
			}
			return rec, *q
		}
	}
	t.Fatal("no quarantine_clear gate in the store")
	return gatestore.Record{}, gatestore.Question{}
}

func ownerApproves(t *testing.T, f gateCLIFixture, rec gatestore.Record, q gatestore.Question) string {
	t.Helper()
	tuple := "approve " + rec.GateID + " clear " + strings.TrimPrefix(rec.Digest, "sha256:")[:16]
	id := f.answer(t, tuple, q.EventID)
	out, _, err := captureOutput(t, func() error {
		return runGate([]string{"answer", "--project", f.project, "--session", "s", "--event", id, "--json"})
	})
	if err != nil || !strings.Contains(out, `"outcome": "approved"`) {
		t.Fatalf("gate answer: %v\n%s", err, out)
	}
	return id
}

// Row: a same-UID shell with a real pty and no signature is clear_unsigned;
// the quarantine stays, and the request waits as an open gate posted over
// Buzz.
func TestSeatClearWithoutTheOwnersSignatureIsClearUnsigned(t *testing.T) {
	f, s, tk := signedClearSeat(t)
	ownerProcess(t, nil, false)
	out, err := signedClear(t, f)
	if !errors.Is(err, errClearUnsigned) || !strings.Contains(err.Error(), "clear_unsigned") {
		t.Fatalf("a pty-only clear in required mode: %v\n%s", err, out)
	}
	l, _ := seatcommit.ReadLedger(f.root, "s1")
	if l.Quarantine == nil || l.Quarantine.ClearedAt != nil || l.Quarantine.Clear != nil {
		t.Fatalf("the ledger quarantine moved: %+v", l.Quarantine)
	}
	if q, _ := task.Quarantined(s.Dir); len(q) != 1 || q[0].Handle != "s1" {
		t.Fatalf("the seat's quarantine was lifted: %+v", q)
	}
	if got, _ := s.Get(tk.ID); got.State != task.StateBlocked {
		t.Fatalf("the task moved: %+v", got)
	}
	rec, q := clearGateOf(t, f)
	if !strings.Contains(rec.Target, "clear s1 nonce=") || !strings.Contains(rec.Text, "approve "+rec.GateID+" clear ") || q.EventID == "" {
		t.Fatalf("the request: %+v %+v", rec, q)
	}
	// running it again waits on the same open gate, never a second request
	if _, err := signedClear(t, f); !errors.Is(err, errClearUnsigned) {
		t.Fatalf("second run: %v", err)
	}
	if store, _ := gatestore.Open(f.root); func() int { ids, _ := store.IDs(); return len(ids) }() != 1 {
		t.Fatal("a second run raised a second gate")
	}
	// a hand-written pty-only record in the ledger is no clear either
	if err := checkClearRecord(f.root, []byte("amq-squad seat clear\n{}")); !errors.Is(err, errClearUnsigned) {
		t.Fatalf("a record without an owner answer counted in required mode: %v", err)
	}
}

// Row: the signed clear record carries the gate id and the answer's event id
// and verifies; the commit step then commits the kept tree.
func TestSignedClearRecordCarriesGateAndEventAndVerifies(t *testing.T) {
	f, s, tk := signedClearSeat(t)
	ownerProcess(t, nil, false)
	if _, err := signedClear(t, f); !errors.Is(err, errClearUnsigned) {
		t.Fatalf("request: %v", err)
	}
	rec, q := clearGateOf(t, f)
	event := ownerApproves(t, f, rec, q)
	if out, err := signedClear(t, f); err != nil {
		t.Fatalf("the clear after the owner's answer: %v\n%s", err, out)
	}
	l, _ := seatcommit.ReadLedger(f.root, "s1")
	c := l.Quarantine.Clear
	if c == nil || c.GateID != rec.GateID || c.EventID != event || c.ConfigDigest == "" {
		t.Fatalf("the signed clear record: %+v", c)
	}
	if !c.Verified("s1", l.Quarantine, ownerTrust{Root: f.root}) {
		t.Fatal("the signed clear record does not verify")
	}
	if c.Verified("s1", l.Quarantine, ownerTrust{}) {
		t.Fatal("a clear that names a gate verified with no gate store")
	}
	if got, _ := s.Get(tk.ID); got.State != task.StateInProgress {
		t.Fatalf("not re-armed: %+v", got)
	}
	// the answer is used once: the ClearRecord on the sealed ledger says so
	if used, err := seatcommit.ClearEventUsed(f.root, "s1", event); err != nil || !used {
		t.Fatalf("the ledger does not hold the used answer: %v %v", used, err)
	}
	if _, err := signedClear(t, f); err == nil {
		t.Fatal("a second clear of a cleared seat succeeded")
	}
	// a record whose gate proof no longer stands in the gate store (the
	// answer file gone) does not count
	store, _ := gatestore.Open(f.root)
	for _, rel := range []string{filepath.Join("answers", rec.GateID+".json")} {
		p := store.Dir(rel)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		os.Remove(p)
		if c.Verified("s1", l.Quarantine, ownerTrust{Root: f.root}) {
			t.Fatalf("the clear verified without %s", rel)
		}
		if err := os.WriteFile(p, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if !c.Verified("s1", l.Quarantine, ownerTrust{Root: f.root}) {
		t.Fatal("the clear stopped verifying once the proof was restored")
	}
}

// Row: the request half still refuses without a real pty and with AM_ME
// set, with the enrolment present (the existing rows stay green).
func TestSignedClearRequestHalfKeepsTheOwnerProof(t *testing.T) {
	f, _, _ := signedClearSeat(t)
	ownerProcess(t, nil, false)
	t.Setenv("AM_ME", "s2")
	if _, err := signedClear(t, f); !errors.Is(err, errNotOwner) {
		t.Fatalf("AM_ME set: %v", err)
	}
	t.Setenv("AM_ME", "")
	openOwnerTTY = func() (ownerTTY, error) { return nil, errNoPTY }
	if _, err := signedClear(t, f); !errors.Is(err, errNotOwner) || !strings.Contains(err.Error(), "not a real pty") {
		t.Fatalf("no pty: %v", err)
	}
	if store, _ := gatestore.Open(f.root); func() int { ids, _ := store.IDs(); return len(ids) }() != 0 {
		t.Fatal("a refused owner proof raised a gate")
	}
}

// Row: a pty_only grant with the enrolment present is grants_invalid; with no
// enrolment it is accepted and the owner's pty alone clears.
func TestPtyOnlyGrantWithAnEnrolmentIsGrantsInvalid(t *testing.T) {
	f, _, _ := signedClearSeat(t)
	ownerProcess(t, nil, false)
	mustWrite(t, filepath.Join(f.home, ".amq-squad", "grants.yaml"), "schema: 1\nclear:\n  signature: pty_only\n")
	if err := os.Chmod(filepath.Join(f.home, ".amq-squad", "grants.yaml"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := signedClear(t, f)
	if err == nil || !strings.Contains(err.Error(), "grants_invalid") || !strings.Contains(err.Error(), "pty_only") {
		t.Fatalf("pty_only beside an enrolment: %v", err)
	}
	if got, _ := clearModeInForce(); got != "" {
		t.Fatalf("mode in force %q", got)
	}
	// without the enrolment the same grant is the owner's choice
	if err := os.Remove(filepath.Join(f.home, ".amq-squad", "gate-binding.json")); err != nil {
		t.Fatal(err)
	}
	if mode, err := clearModeInForce(); err != nil || mode != "pty_only" {
		t.Fatalf("mode without an enrolment: %q %v", mode, err)
	}
	if out, err := signedClear(t, f); err != nil {
		t.Fatalf("a pty_only clear without an enrolment: %v\n%s", err, out)
	}
}

// With the enrolment present and no grant key, required is the mode in force
// and doctor names it, and the open clear and why it waits.
func TestDoctorNamesTheClearModeAndAWaitingClear(t *testing.T) {
	f, _, _ := signedClearSeat(t)
	if mode, err := clearModeInForce(); err != nil || mode != "required" {
		t.Fatalf("default mode with an enrolment: %q %v", mode, err)
	}
	ownerProcess(t, nil, false)
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return nil, nostr.SecretKey{}, nil, errors.New("buzz relay unreachable")
	}
	_, err := signedClear(t, f)
	if !errors.Is(err, errClearUnsigned) {
		t.Fatalf("clear with Buzz down: %v", err)
	}
	checks := doctorClearChecks(f.root)
	if len(checks) != 2 || !strings.HasPrefix(checks[0].Detail, "required:") ||
		!strings.Contains(checks[1].Detail, "never reached Buzz") || !strings.Contains(checks[1].Detail, "no fallback") {
		t.Fatalf("doctor: %+v", checks)
	}
	// Buzz comes back: gate post sends the question and the check moves on
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return f.relay, f.body, nil, nil
	}
	if _, err := signedClear(t, f); !errors.Is(err, errClearUnsigned) {
		t.Fatalf("clear with Buzz back: %v", err)
	}
	if checks := doctorClearChecks(f.root); len(checks) != 2 || !strings.Contains(checks[1].Detail, "waiting for the owner's signed answer") {
		t.Fatalf("doctor after the post: %+v", checks)
	}
}

// A seat quarantined in the task store alone (no ledger record) is found, is
// cleared only through the signed path, and the clear writes a signed
// ClearRecord into a seed entry it creates on the ledger for the handle. The
// quarantine is built the way the scheduler builds it: by the exit check.
func TestTaskStoreOnlyQuarantineClearsThroughTheSignedPathAndLeavesARecord(t *testing.T) {
	f := newGateCLIFixture(t)
	ownerProcess(t, nil, false)
	mustWrite(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	mustWrite(t, filepath.Join(f.home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	mustWrite(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":2}`)
	s, _ := task.Open(f.root)
	r := seatcommit.ExitCheck(seatcommit.Options{Root: f.root, Handle: "s1", Task: task.Task{ID: "t-hs"}, Now: taskNow, Clear: ownerTrust{Root: f.root}}, "gone")
	if !r.Quarantine {
		t.Fatalf("the exit check did not quarantine: %+v", r)
	}
	if err := s.Quarantine(task.Quarantine{Handle: "s1", Reason: r.ReasonCode, Detail: r.Detail, TaskID: "t-hs", At: taskNow()}); err != nil {
		t.Fatal(err)
	}
	if _, err := seatcommit.ReadLedger(f.root, "s1"); !os.IsNotExist(err) {
		t.Fatalf("the exit check left a ledger: %v", err)
	}
	if _, err := signedClear(t, f); !errors.Is(err, errClearUnsigned) {
		t.Fatalf("an unsigned clear: %v", err)
	}
	if q, _ := task.Quarantined(s.Dir); len(q) != 1 {
		t.Fatalf("the quarantine was lifted without the answer: %+v", q)
	}
	if _, err := seatcommit.ReadLedger(f.root, "s1"); !os.IsNotExist(err) {
		t.Fatalf("a refused clear created a ledger: %v", err)
	}
	rec, q := clearGateOf(t, f)
	event := ownerApproves(t, f, rec, q)
	if out, err := signedClear(t, f); err != nil {
		t.Fatalf("the clear after the answer: %v\n%s", err, out)
	}
	l, err := seatcommit.ReadLedger(f.root, "s1")
	if err != nil || !l.Seeded || l.Quarantine == nil || l.Quarantine.Clear == nil {
		t.Fatalf("no seed entry for the handle: %+v %v", l, err)
	}
	c := l.Quarantine.Clear
	if c.GateID != rec.GateID || c.EventID != event || c.Condition != r.ReasonCode || !c.Verified("s1", l.Quarantine, ownerTrust{Root: f.root}) {
		t.Fatalf("the signed record: %+v", c)
	}
	if q, _ := task.Quarantined(s.Dir); len(q) != 0 {
		t.Fatalf("the task-store quarantine stayed: %+v", q)
	}
	// a seat with neither record is not quarantined
	if _, err := signedClear(t, f); err == nil || !strings.Contains(err.Error(), "not quarantined") {
		t.Fatalf("a cleared seat: %v", err)
	}
}

// F1 through the gate store: after a completed clear, the exit check, the
// commit step (a dry run included) and the widen dry run each verify the
// owner's answer and refuse when it is gone or the gate store is unreadable.
func TestEveryConsumerRefusesAClearWhoseAnswerIsGone(t *testing.T) {
	f, s, tk := signedClearSeat(t)
	ownerProcess(t, nil, false)
	if _, err := signedClear(t, f); !errors.Is(err, errClearUnsigned) {
		t.Fatalf("request: %v", err)
	}
	rec, q := clearGateOf(t, f)
	ownerApproves(t, f, rec, q)
	if out, err := signedClear(t, f); err != nil {
		t.Fatalf("the clear: %v\n%s", err, out)
	}
	cur, _ := s.Get(tk.ID)
	g, _ := grants.Load(cur.ProjectPath)
	o := seatcommit.Options{Root: f.root, Handle: "s1", Task: cur, Grants: g, Now: taskNow, Clear: ownerTrust{Root: f.root}}
	dry := o
	dry.DryRun = true
	consumers := map[string]func() string{
		"exit check": func() string { return seatcommit.ExitCheck(o, "gone").ReasonCode },
		"commit dry": func() string { return seatcommit.Run(dry).ReasonCode },
		"widen": func() string {
			return seatcommit.CheckWiden(seatcommit.WidenOptions{Root: f.root, Task: cur, Added: []string{"cmd/other.go"}, Grants: g, RepoRoot: cur.ProjectPath, Commit: &o}).ReasonCode
		},
	}
	for name, run := range consumers {
		if got := run(); got == seatcommit.ReasonQuarantineUnverified {
			t.Fatalf("%s refused a verified clear", name)
		}
	}
	store, _ := gatestore.Open(f.root)
	answer := store.Dir("answers", rec.GateID+".json")
	raw, err := os.ReadFile(answer)
	if err != nil {
		t.Fatal(err)
	}
	gates := store.Dir("gates")
	breakages := map[string]struct{ break_, restore func() }{
		"answer file deleted":   {func() { os.Remove(answer) }, func() { os.WriteFile(answer, raw, 0o600) }},
		"gate store unreadable": {func() { os.Chmod(gates, 0) }, func() { os.Chmod(gates, 0o700) }},
	}
	for what, b := range breakages {
		b.break_()
		for name, run := range consumers {
			if got := run(); got != seatcommit.ReasonQuarantineUnverified {
				t.Errorf("%s with the %s: reason %q", name, what, got)
			}
		}
		b.restore()
	}
}

// P1: a commits ledger removed after the clear used an answer is
// ledger_tampered (its sealed head says it existed), never "no ledger": the
// task-store fallback cannot recreate the seed and re-authorize the answer.
func TestDeletedLedgerIsTamperedNotUnused(t *testing.T) {
	f := newGateCLIFixture(t)
	ownerProcess(t, nil, false)
	mustWrite(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":1}`)
	mustWrite(t, filepath.Join(f.home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	mustWrite(t, filepath.Join(f.home, ".claude", "settings.json"), `{"a":2}`)
	s, _ := task.Open(f.root)
	r := seatcommit.ExitCheck(seatcommit.Options{Root: f.root, Handle: "s1", Task: task.Task{ID: "t-hs"}, Now: taskNow, Clear: ownerTrust{Root: f.root}}, "gone")
	at := taskNow()
	if err := s.Quarantine(task.Quarantine{Handle: "s1", Reason: r.ReasonCode, Detail: r.Detail, TaskID: "t-hs", At: at}); err != nil {
		t.Fatal(err)
	}
	if _, err := signedClear(t, f); !errors.Is(err, errClearUnsigned) {
		t.Fatalf("request: %v", err)
	}
	rec, q := clearGateOf(t, f)
	event := ownerApproves(t, f, rec, q)
	if out, err := signedClear(t, f); err != nil {
		t.Fatalf("the clear: %v\n%s", err, out)
	}
	// stop before the task-store clear: the quarantine is back, the ledger is deleted
	if err := s.Quarantine(task.Quarantine{Handle: "s1", Reason: r.ReasonCode, Detail: r.Detail, TaskID: "t-hs", At: at}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(seatcommit.LedgerDir(f.root), "s1.json")); err != nil {
		t.Fatal(err)
	}
	if used, err := seatcommit.ClearEventUsed(f.root, "s1", event); err == nil || used || !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("a deleted ledger reads as %v, %v", used, err)
	}
	if _, err := signedClear(t, f); !errors.Is(err, sealchain.ErrTampered) {
		t.Fatalf("the retry of the used answer after the ledger was deleted: %v", err)
	}
	if q, _ := task.Quarantined(s.Dir); len(q) != 1 {
		t.Fatalf("the quarantine was lifted: %+v", q)
	}
}

// F2: quarantine_clear is raised only by seat clear, after the pty proof; the
// generic gate raise refuses it, whoever runs it.
func TestGenericGateRaiseRefusesTheReservedClearAction(t *testing.T) {
	f := newGateCLIFixture(t)
	_, _, err := captureOutput(t, func() error {
		return runGate([]string{"raise", "--project", f.project, "--session", "s", "--me", "cto", "--gate", "spam-1", "--action", "quarantine_clear", "--target", "clear s1 nonce=a incarnation=b config=c"})
	})
	if err == nil || !strings.Contains(err.Error(), "action_reserved") {
		t.Fatalf("gate raise of a reserved action: %v", err)
	}
	store, _ := gatestore.Open(f.root)
	if ids, _ := store.IDs(); len(ids) != 0 {
		t.Fatalf("a gate was written: %v", ids)
	}
}
