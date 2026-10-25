package routing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// v3-5-verifier P2-2 of the second pass: an attempt archives its verdict only
// after the seal check. An untagged pass written into a lease, then the
// lease released, archives as invalid, and the report says invalid, never
// pass.
func TestUnsignedPassArchivesAsInvalid(t *testing.T) {
	root := t.TempDir()
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 25, 11, 0, 0, 0, time.UTC)
	a, err := s.Add(task.AddInput{Title: "unsigned", Class: "implement"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(a.ID, "seat-1", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(task.DirFor(root), a.ID+".json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var tk task.Task
	if err := json.Unmarshal(raw, &tk); err != nil {
		t.Fatal(err)
	}
	tk.Lease.Snapshot, tk.Lease.SnapshotCommand = "tree-x", task.CommandDigest("x")
	tk.Lease.Verdict = &task.Verdict{Verifier: task.VerifierTests, Result: task.VerdictPass, Source: "seat commit verifier", Tree: "tree-x", CommandDigest: task.CommandDigest("x"), At: now}
	out, _ := json.Marshal(tk)
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := s.Release(a.ID, "lead", "handing it back", now)
	if err != nil {
		t.Fatal(err)
	}
	at := got.Attempts[len(got.Attempts)-1]
	if at.Verdict == nil || at.Verdict.Result != task.VerdictInvalid {
		t.Fatalf("the archived verdict: %+v", at.Verdict)
	}
	if r := verifierResult(VerifierTests, at); r != VerifierInvalid {
		t.Fatalf("the report's verifier_result: %s, want %s", r, VerifierInvalid)
	}
}
