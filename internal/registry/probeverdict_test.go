package registry

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

func probeCheckout(t *testing.T, files map[string]string, gitJunk bool) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if gitJunk { // the model's to edit: never part of the checkout's tree
		if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".git", "hooks", "pre-commit"), []byte("exit 0\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// v3-5-verifier rule 3 (doctor's record): a probe run's predicate is a
// verdict of verifier r12, pass exactly when the predicate holds, on the
// tree of the probe's checkout (its .git left out), with the digest of the
// predicate version and the prompt; doctor's verdict is unsealed and says so.
func TestProbeRunsRecordTheirPredicateAsAVerdict(t *testing.T) {
	ctx := context.Background()
	edited, err := probeTree(ctx, probeCheckout(t, expectedFiles(), true))
	if err != nil {
		t.Fatal(err)
	}
	clean, err := probeTree(ctx, probeCheckout(t, expectedFiles(), false))
	if err != nil {
		t.Fatal(err)
	}
	pristine, err := probeTree(ctx, probeCheckout(t, probeFixture, false))
	if err != nil {
		t.Fatal(err)
	}
	if edited != clean || edited == pristine || len(edited) < 40 {
		t.Fatalf("the checkout's tree: with .git %s, without %s, the fixture %s", edited, clean, pristine)
	}
	fail := ProbeRun{Tree: edited, Checks: map[string]bool{"final_line_exact": false, "worktree_exact": true}}
	v := fail.PredicateVerdict(ProbeVerifier, ProbeSourceUnsealed)
	if v.Result != task.VerdictFail || v.Verifier != ProbeVerifier || v.Tree != edited || !strings.Contains(v.Detail, "final_line_exact") {
		t.Fatalf("a run whose predicate fails: %+v", v)
	}
	pass := ProbeRun{Pass: true, Tree: edited}
	v = pass.PredicateVerdict(ProbeVerifier, ProbeSourceUnsealed)
	if v.Result != task.VerdictPass || v.CommandDigest != PredicateDigest() || v.CommandDigest != task.CommandDigest(ProbePredicate+"\n"+ProbePrompt) {
		t.Fatalf("a run whose predicate holds: %+v", v)
	}
	if v.Tag != "" || !strings.Contains(v.Source, "unsealed") {
		t.Fatalf("doctor's verdict does not say it is unsealed: %q %q", v.Source, v.Tag)
	}
}

// v3-5-verifier rule 3 (the coordinator's addition): a probe run as a
// scheduled task completes only on the predicate verdict the harness
// recorded on its lease, sealed with the session seed: no verdict blocks
// (verdict_missing), a failed predicate blocks (verdict_failed), a held
// predicate completes.
func TestScheduledProbeTaskCompletesOnlyOnItsPredicateVerdict(t *testing.T) {
	tree, err := probeTree(context.Background(), probeCheckout(t, expectedFiles(), false))
	if err != nil {
		t.Fatal(err)
	}
	s, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.Verifiers = func(tk task.Task) (string, bool, error) { return "r12-probe", tk.Class == "implement", nil }
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	probeTask := func(holder string) task.Task {
		t.Helper()
		a, err := s.Add(task.AddInput{Title: "r12 probe", Class: "implement"}, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.Claim(a.ID, holder, nil, time.Hour, now); err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := probeTask("probe-1")
	if got, err := s.Done(a.ID, "probe-1", "RESULT: PASS", "", now); err != nil || got.BlockReason != task.BlockVerdictMissing {
		t.Fatalf("a probe task's report with no predicate verdict: %s %s %v", got.State, got.BlockReason, err)
	}
	b := probeTask("probe-2")
	if _, err := RecordProbeVerdict(s, b.ID, ProbeRun{Tree: tree, Checks: map[string]bool{"go_test_confirmed": false}}, now); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Done(b.ID, "probe-2", "RESULT: PASS", "", now); err != nil || got.BlockReason != task.BlockVerdictFailed {
		t.Fatalf("a probe task whose predicate failed: %s %s %v", got.State, got.BlockReason, err)
	}
	c := probeTask("probe-3")
	if _, err := RecordProbeVerdict(s, c.ID, ProbeRun{Pass: true, Tree: tree}, now); err != nil {
		t.Fatal(err)
	}
	got, err := s.Done(c.ID, "probe-3", "RESULT: PASS", "", now)
	if err != nil || got.State != task.StateCompleted {
		t.Fatalf("a probe task whose predicate held: %s %s %v", got.State, got.BlockReason, err)
	}
	if v := got.Attempts[0].Verdict; v == nil || v.Tag == "" || v.Tree != tree || v.Source != ProbeSourceScheduled || v.Verifier != task.VerifierTests {
		t.Fatalf("the completed probe task's verdict: %+v", v)
	}
}
