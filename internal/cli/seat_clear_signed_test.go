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
	"github.com/omriariav/amq-squad/v3/internal/nostr"
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
	// the answer lifted this quarantine once
	store, _ := gatestore.Open(f.root)
	if u, _ := store.ClearUsed(rec.GateID); u == nil || u.EventID != event {
		t.Fatalf("the gate's mark: %+v", u)
	}
	if _, err := signedClear(t, f); err == nil {
		t.Fatal("a second clear of a cleared seat succeeded")
	}
	// a record whose gate proof no longer stands in the gate store (the
	// answer's mark gone, or the answer file itself) does not count
	for _, rel := range []string{filepath.Join("clears", "used", rec.GateID+".json"), filepath.Join("answers", rec.GateID+".json")} {
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
