package task

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The store's rows of the verdict producer (v3-5-verifier).

func gatedStore(t *testing.T) Store {
	t.Helper()
	s := newStore(t)
	s.Verifiers = func(Task) (string, bool, error) { return "/opt/verify", true, nil }
	return s
}

func claimed(t *testing.T, s Store, holder string) Task {
	t.Helper()
	a := mustAdd(t, s, AddInput{Title: "t"})
	got, err := s.Claim(a.ID, holder, nil, time.Minute, t0)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Rule 2: a verdict whose tree is not the lease's snapshot is refused
// (verdict_tree_mismatch), and so is a verdict with no snapshot recorded; a
// snapshot never moves under a recorded verdict.
func TestVerdictTreeMustBeTheLeasesSnapshot(t *testing.T) {
	s := gatedStore(t)
	a := claimed(t, s, "seat-1")
	v := Verdict{Verifier: VerifierTests, Result: VerdictPass, Source: "seat commit verifier", Tree: "tree-a", CommandDigest: CommandDigest("/opt/verify")}
	if _, err := s.RecordVerdict(a.ID, v, t0); !errors.Is(err, ErrVerdictTreeMismatch) {
		t.Fatalf("a verdict with no snapshot recorded: %v", err)
	}
	if _, err := s.RecordSnapshot(a.ID, "tree-b", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(a.ID, v, t0); !errors.Is(err, ErrVerdictTreeMismatch) {
		t.Fatalf("a verdict on another tree than the snapshot: %v", err)
	}
	v.Tree = "tree-b"
	if _, err := s.RecordVerdict(a.ID, v, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordSnapshot(a.ID, "tree-c", t0); !errors.Is(err, ErrVerdictTreeMismatch) {
		t.Fatalf("the snapshot moved under a recorded verdict: %v", err)
	}
	got, err := s.Done(a.ID, "seat-1", "", "", t0)
	if err != nil || got.State != StateCompleted {
		t.Fatalf("a sealed verdict on the snapshot did not complete: %s %s %v", got.State, got.BlockReason, err)
	}
}

// Rule 4, the claim path: a gated class with no configured command is never
// leased; Claim and Grant refuse it as grants_invalid naming the class and
// the missing command, and a pull skips it.
func TestClaimRefusesAGatedClassWithNoCommand(t *testing.T) {
	s := newStore(t)
	s.Verifiers = func(tk Task) (string, bool, error) { return "", tk.Class == "implement", nil }
	impl := mustAdd(t, s, AddInput{Title: "gated", Class: "implement", Priority: 9})
	script := mustAdd(t, s, AddInput{Title: "free", Class: "script"})
	var ue *VerifierUnconfiguredError
	if _, err := s.Claim(impl.ID, "seat-1", nil, time.Minute, t0); !errors.As(err, &ue) || !strings.Contains(err.Error(), ReasonGrantsInvalid) || !strings.Contains(err.Error(), "verifiers.implement") {
		t.Fatalf("a claim of a gated class with no command: %v", err)
	}
	if _, err := s.Grant(impl.ID, "seat-1", nil, time.Minute, t0); !errors.As(err, &ue) {
		t.Fatalf("a grant of a gated class with no command: %v", err)
	}
	got, err := s.Claim("", "seat-2", nil, time.Minute, t0)
	if err != nil || got.ID != script.ID {
		t.Fatalf("a pull took the gated task (priority 9) or nothing: %s %v", got.ID, err)
	}
}

// Rule 11: completion verifies the verdict's seal; a verdict inserted into
// the task file without the session seed's tag, or a sealed one edited
// after, is verdict_missing, and the attempt carries no verdict.
func TestUnsealedVerdictIsVerdictMissing(t *testing.T) {
	s := gatedStore(t)
	edit := func(id string, fn func(*Verdict)) {
		t.Helper()
		p := filepath.Join(s.Dir, id+".json")
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var tk Task
		if err := json.Unmarshal(raw, &tk); err != nil {
			t.Fatal(err)
		}
		if tk.Lease.Verdict == nil {
			tk.Lease.Snapshot = "tree-x"
			tk.Lease.Verdict = &Verdict{Verifier: VerifierTests, Result: VerdictPass, Source: "seat commit verifier", Tree: "tree-x", CommandDigest: CommandDigest("/opt/verify"), At: t0}
		}
		fn(tk.Lease.Verdict)
		out, _ := json.Marshal(tk)
		if err := os.WriteFile(p, out, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(id, holder, what string) {
		t.Helper()
		got, err := s.Done(id, holder, "", "", t0)
		if err != nil || got.State != StateBlocked || got.BlockReason != BlockVerdictMissing {
			t.Fatalf("%s: %s %s %v", what, got.State, got.BlockReason, err)
		}
		if a := got.Attempts[len(got.Attempts)-1]; a.Verdict != nil {
			t.Fatalf("%s: the attempt carries the unsealed verdict", what)
		}
	}
	a := claimed(t, s, "seat-1")
	edit(a.ID, func(*Verdict) {})
	check(a.ID, "seat-1", "a verdict inserted with no tag")

	b := claimed(t, s, "seat-2")
	if _, err := s.RecordSnapshot(b.ID, "tree-y", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(b.ID, Verdict{Verifier: VerifierTests, Result: VerdictFail, Source: "seat commit verifier", Tree: "tree-y", CommandDigest: CommandDigest("/opt/verify")}, t0); err != nil {
		t.Fatal(err)
	}
	edit(b.ID, func(v *Verdict) { v.Result = VerdictPass })
	check(b.ID, "seat-2", "a sealed fail edited to pass")

	c := claimed(t, s, "seat-3")
	if _, err := s.RecordSnapshot(c.ID, "tree-z", t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(c.ID, Verdict{Verifier: VerifierTests, Result: VerdictPass, Source: "seat commit verifier", Tree: "tree-z", CommandDigest: CommandDigest("/opt/verify")}, t0); err != nil {
		t.Fatal(err)
	}
	edit(c.ID, func(v *Verdict) { v.CommandDigest = CommandDigest("/bin/true") })
	check(c.ID, "seat-3", "a sealed verdict with its command digest edited")
}
