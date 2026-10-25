package task

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// harnessPass records the harness's pass verdict on the task's current
// attempt, as the commit step's test run or the probe would.
func harnessPass(t testing.TB, s Store, id string, now time.Time) {
	t.Helper()
	got, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if !MechanicalVerifier(ClassVerifier(got.Class)) {
		return
	}
	// The harness records the tree first, then the sealed verdict on it.
	if _, err := s.RecordSnapshot(id, "test-tree", CommandDigest("test"), now); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(id, Verdict{Verifier: ClassVerifier(got.Class), Result: VerdictPass, Source: "test harness", Tree: "test-tree", CommandDigest: CommandDigest("test")}, now); err != nil {
		t.Fatal(err)
	}
}

// Phase 5 re-pass, ruling 1: a class with a mechanical verifier completes
// only on a recorded verdict from the harness, never on the seat's report. An
// implement task claimed and reported done with empty evidence blocks with
// verdict_missing, and its attempt ends blocked (a failure for escalation);
// with declared files the commit step's acceptance blocks the same way; a
// fail verdict blocks with verdict_failed; a pass verdict completes and the
// attempt carries it. A class with no verifier still completes on its report.
func TestRepassMechanicalClassCompletesOnlyOnAHarnessVerdict(t *testing.T) {
	s := newStore(t)
	pool := Pool{Runtime: "claude", Model: "haiku", Effort: "low"}
	a := mustAdd(t, s, AddInput{Title: "smol implement", Pool: &pool})
	if _, err := s.Claim(a.ID, "seat-1", &pool, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	d, err := s.Done(a.ID, "seat-1", "", "", t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if d.State != StateBlocked || d.BlockReason != BlockVerdictMissing || d.BlockRoute != RouteLead ||
		len(d.Attempts) != 1 || d.Attempts[0].Outcome != OutcomeBlocked || d.Attempts[0].Detail != BlockVerdictMissing {
		t.Fatalf("a done report with no verdict completed: state %s reason %s attempts %+v", d.State, d.BlockReason, d.Attempts)
	}

	b := mustAdd(t, s, AddInput{Title: "with files", DeclaredFiles: []string{"x.go"}})
	if _, err := s.Claim(b.ID, "seat-2", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Done(b.ID, "seat-2", "tests passed, trust me", "", t0); err != nil {
		t.Fatal(err)
	}
	if c, err := s.Complete(b.ID, t0.Add(time.Second)); err != nil || c.State != StateBlocked || c.BlockReason != BlockVerdictMissing {
		t.Fatalf("the commit step completed with no verdict: %s %s %v", c.State, c.BlockReason, err)
	}

	c := mustAdd(t, s, AddInput{Title: "red", Class: "script"})
	if _, err := s.Claim(c.ID, "seat-3", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(c.ID, Verdict{Verifier: VerifierTests, Result: VerdictPass, Source: "x"}, t0); err == nil {
		t.Fatal("a verdict of another verifier was recorded")
	}
	if _, err := s.RecordSnapshot(c.ID, "test-tree", CommandDigest("test"), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(c.ID, Verdict{Verifier: VerifierExitCode, Result: VerdictFail, Source: "probe", Detail: "exit 1", Tree: "test-tree", CommandDigest: CommandDigest("test")}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(c.ID, Verdict{Verifier: VerifierExitCode, Result: VerdictPass, Source: "probe", Tree: "test-tree", CommandDigest: CommandDigest("test")}, t0); err == nil {
		t.Fatal("a second verdict replaced the first")
	}
	if got, err := s.Done(c.ID, "seat-3", "", "", t0); err != nil || got.State != StateBlocked || got.BlockReason != BlockVerdictFailed {
		t.Fatalf("a fail verdict completed: %s %s %v", got.State, got.BlockReason, err)
	}

	e := mustAdd(t, s, AddInput{Title: "green", Class: "extract"})
	if _, err := s.Claim(e.ID, "seat-4", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	harnessPass(t, s, e.ID, t0)
	got, err := s.Done(e.ID, "seat-4", "", "", t0)
	if err != nil || got.State != StateCompleted || got.Attempts[0].Verdict == nil || got.Attempts[0].Verdict.Result != VerdictPass {
		t.Fatalf("a pass verdict did not complete: %+v %v", got, err)
	}

	p := mustAdd(t, s, AddInput{Title: "plan", Class: "plan"})
	if _, err := s.Claim(p.ID, "seat-5", nil, time.Minute, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(p.ID, Verdict{Verifier: VerifierNone, Result: VerdictPass, Source: "x"}, t0); err == nil || !strings.Contains(err.Error(), "no mechanical verifier") {
		t.Fatalf("a verdict on a class with no verifier: %v", err)
	}
	if got, err := s.Done(p.ID, "seat-5", "", "", t0); err != nil || got.State != StateCompleted {
		t.Fatalf("a plan task did not complete on its report: %s %v", got.State, err)
	}
}

// Ruling on ruling 1, the store side: the completion gate applies only to a
// class in the verifier gate (Store.Verifiers: the grant's commands, plus
// the classes a squad.yaml adds); a class outside it completes on the done
// report. A gated class with no grant command blocks, and the message names
// the missing command. A source that cannot be read fails closed.
func TestRepassGateOnlyForAConfiguredVerifier(t *testing.T) {
	s := newStore(t)
	cmd, gated, srcErr := "", false, error(nil)
	s.Verifiers = func(Task) (string, bool, error) { return cmd, cmd != "" || gated, srcErr }
	var duringLease func() // a configuration change while the seat holds the lease
	run := func(title string) Task {
		t.Helper()
		a := mustAdd(t, s, AddInput{Title: title})
		if _, err := s.Claim(a.ID, "seat-"+a.ID, nil, time.Minute, t0); err != nil {
			t.Fatal(err)
		}
		if duringLease != nil {
			duringLease()
			duringLease = nil
		}
		got, err := s.Done(a.ID, "seat-"+a.ID, "", "", t0)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := run("none configured"); got.State != StateCompleted {
		t.Fatalf("no verifier configured: %s %s", got.State, got.BlockReason)
	}
	cmd = "go test ./..."
	if got := run("configured"); got.State != StateBlocked || got.BlockReason != BlockVerdictMissing {
		t.Fatalf("a configured verifier: %s %s", got.State, got.BlockReason)
	}
	// The squad's gate adds the class and the grant names no command: such a
	// task is never dispatched (v3-5-verifier rule 4), so the done-report gate
	// fires on a configuration change during the lease.
	duringLease = func() { cmd, gated = "", true }
	if got := run("gated, no command"); got.State != StateBlocked || got.BlockReason != BlockVerdictMissing || !strings.Contains(got.BlockDetail, "names no verifier command") {
		t.Fatalf("a gated class with no grant command: %s %s %q", got.State, got.BlockReason, got.BlockDetail)
	}
	cmd, gated, srcErr = "", false, errors.New("owner grants file unreadable")
	if got := run("unreadable"); got.State != StateBlocked || !strings.Contains(got.BlockDetail, "could not be read") {
		t.Fatalf("an unreadable source: %s %s", got.State, got.BlockDetail)
	}
}
