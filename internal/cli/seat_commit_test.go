package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(sanitizedReviewEnv(os.Environ()), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// seatRepo makes a repository with a bare origin and a seat worktree on
// seat/s1, stages the seat's launch record under root and records its base.
func seatRepo(t *testing.T, root string) (repo, wt string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt = filepath.Join(dir, "repo"), filepath.Join(dir, "wt-s1")
	origin := filepath.Join(dir, "origin.git")
	gitT(t, dir, "init", "-q", "--bare", "-b", "main", origin)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, repo, "init", "-q", "-b", "main")
	mustWrite(t, filepath.Join(repo, "cmd", "root.go"), "package cmd\n")
	mustWrite(t, filepath.Join(repo, ".claude", "settings.json"), "{}\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "initial")
	gitT(t, repo, "remote", "add", "origin", origin)
	gitT(t, repo, "push", "-q", "origin", "main")
	gitT(t, repo, "worktree", "add", "-q", "-b", "seat/s1", wt, "main")
	staged := launch.StagedDir(root, "s1")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Handle: "s1", Root: root, Session: "s", Runtime: "claude", Worktree: wt, Branch: "seat/s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := seatcommit.RecordWorktreeBase("", root, "s1", wt, "seat/s1", time.Now()); err != nil {
		t.Fatal(err)
	}
	return repo, wt
}

// Acceptance 7 through the verbs: seat commit refuses a protected path
// (the implicit .claude/settings*.json) and records the refusal for the
// lead; task widen onto it is refused by the core, raises the growth gate
// in the gate store and routes the task to the owner; after the owner's
// signed answer the same widen goes through and seat commit pushes.
func TestSeatCommitAndWidenThroughTheGrowthGate(t *testing.T) {
	f := newGateCLIFixture(t)
	repo, wt := seatRepo(t, f.root)
	s, _ := task.Open(f.root)
	tk, err := s.Add(task.AddInput{Title: "tune settings", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: repo}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(tk.ID, "s1", nil, time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(wt, "cmd", "root.go"), "package cmd\n// tuned\n")
	mustWrite(t, filepath.Join(wt, ".claude", "settings.json"), `{"permissions":{"allow":["Bash(*)"]}}`+"\n")
	if _, err := s.Done(tk.ID, "s1", "ok", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	scoped := []string{"--project", f.project, "--session", "s"}
	_, _, err = captureOutput(t, func() error { return runSeatCommit(append([]string{"--seat", "s1"}, scoped...), os.Stdout) })
	if ExitCode(err) != ExitCommitRefused {
		t.Fatalf("seat commit: %v", err)
	}
	got, _ := s.Get(tk.ID)
	if got.State != task.StateBlocked || got.BlockReason != seatcommit.ReasonProtected || got.BlockRoute != task.RouteLead {
		t.Fatalf("after the refusal: %+v", got)
	}
	widen := append([]string{tk.ID, "--me", "lead", "--files", ".claude/settings.json", "--reason", "the seat needs the permission", "--json"}, scoped...)
	out, _, err := captureOutput(t, func() error { return runTaskWiden(widen, os.Stdout) })
	if ExitCode(err) != ExitCommitRefused {
		t.Fatalf("widen onto a protected path: %v\n%s", err, out)
	}
	var env struct {
		Data taskWidenData `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || len(env.Data.GateIDs) != 1 {
		t.Fatalf("widen output: %v\n%s", err, out)
	}
	target := seatcommit.ProtectedTarget(f.root, tk.ID, []string{".claude/settings.json"})
	store, _ := gatestore.Open(f.root)
	rec, err := store.Load(env.Data.GateIDs[0])
	if err != nil || rec.Class != "spawn" || rec.Target != target || rec.RaisedBy != "lead" {
		t.Fatalf("growth gate record = %+v, %v", rec, err)
	}
	got, _ = s.Get(tk.ID)
	if got.BlockRoute != task.RouteOwner || got.BlockReason != seatcommit.ReasonGrowthGate {
		t.Fatalf("the task did not go to the owner: %+v", got)
	}
	// The owner answers in the gate thread.
	q, _ := store.Question(rec.GateID)
	if q == nil {
		t.Fatal("no question posted")
	}
	tuple := "approve " + rec.GateID + " grow " + strings.TrimPrefix(rec.Digest, "sha256:")[:16]
	ev := f.answer(t, tuple, q.EventID)
	if _, _, err := captureOutput(t, func() error {
		return runGate(append([]string{"answer", "--from", "buzz", "--label", "nostr:" + ev}, scoped...))
	}); err != nil {
		t.Fatalf("gate answer: %v", err)
	}
	if out, _, err := captureOutput(t, func() error { return runTaskWiden(widen, os.Stdout) }); err != nil {
		t.Fatalf("widen after the accepted gate: %v\n%s", err, out)
	}
	got, _ = s.Get(tk.ID)
	if got.State != task.StateInProgress || len(got.ScopeChanges) != 1 || got.ScopeChanges[0].Gates[0] != target {
		t.Fatalf("after the widening: %+v", got)
	}
	if out, _, err := captureOutput(t, func() error { return runSeatCommit(append([]string{"--seat", "s1", "--json"}, scoped...), os.Stdout) }); err != nil {
		t.Fatalf("seat commit after the gate: %v\n%s", err, out)
	}
	if got, _ = s.Get(tk.ID); got.State != task.StateCompleted {
		t.Fatalf("task = %+v", got)
	}
}

// task widen refuses an actor that is not the scheduler's lead, and a --me
// that differs from the environment's AM_ME (a seat acting as the lead).
func TestTaskWidenRefusesANonLead(t *testing.T) {
	root := t.TempDir()
	s, _ := task.Open(root)
	tk, _ := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"a.go"}}, time.Now())
	t.Setenv("AM_ME", "s1")
	_, _, err := captureOutput(t, func() error {
		return runTaskWiden([]string{tk.ID, "--root", root, "--me", "lead", "--files", "b.go", "--reason", "r"}, os.Stdout)
	})
	if err == nil || !strings.Contains(err.Error(), "differs from this environment's AM_ME") {
		t.Fatalf("a seat's environment widened as the lead: %v", err)
	}
	t.Setenv("AM_ME", "")
	statePath := filepath.Join(root, "extensions", "amq-squad", "scheduler.json")
	mustWrite(t, statePath, `{"lead":"cto"}`)
	_, _, err = captureOutput(t, func() error {
		return runTaskWiden([]string{tk.ID, "--root", root, "--me", "lead", "--files", "b.go", "--reason", "r"}, os.Stdout)
	})
	if ExitCode(err) != ExitCommitRefused || !strings.Contains(err.Error(), "not_lead") {
		t.Fatalf("widen by a non-lead: %v", err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runTaskWiden([]string{tk.ID, "--root", root, "--me", "cto", "--files", "b.go", "--reason", "r"}, os.Stdout)
	}); err != nil {
		t.Fatalf("widen by the lead: %v", err)
	}
}
