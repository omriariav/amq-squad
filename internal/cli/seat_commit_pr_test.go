package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// seat commit --open-pr binds the task to the PR the harness opened and the
// head it pushed (pr_number, pr_head), the binding 3-2's merge train reads.
func TestSeatCommitOpenPRBindsTheTask(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt := seatRepo(t, root)
	gh := filepath.Join(t.TempDir(), "gh")
	mustWrite(t, gh, "#!/bin/sh\nif [ \"$2\" = list ]; then echo '[]'; exit 0; fi\necho https://github.com/o/r/pull/7\n")
	os.Chmod(gh, 0o755)
	prev := seatCommitGh
	seatCommitGh = gh
	t.Cleanup(func() { seatCommitGh = prev })
	s, _ := task.Open(root)
	tk, _ := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: repo}, time.Now())
	s.Claim(tk.ID, "s1", nil, time.Hour, time.Now())
	mustWrite(t, filepath.Join(wt, "cmd", "root.go"), "package cmd\n// pr\n")
	harnessPass(t, s, tk.ID, time.Now())
	s.Done(tk.ID, "s1", "ok", "", time.Now())
	if out, _, err := captureOutput(t, func() error {
		return runSeatCommit([]string{"--seat", "s1", "--root", root, "--open-pr"}, os.Stdout)
	}); err != nil {
		t.Fatalf("seat commit: %v\n%s", err, out)
	}
	got, _ := s.Get(tk.ID)
	// The task's own branch carries the commit (3.0.2 t23); the worktree is
	// back on the resting seat branch, and the commit stays anchored.
	head := gitT(t, wt, "rev-parse", "refs/amq-squad/commits/s1/"+tk.ID)
	if rest := gitT(t, wt, "symbolic-ref", "HEAD"); rest != "refs/heads/seat/s1" || head == gitT(t, wt, "rev-parse", "HEAD") {
		t.Fatalf("after the step the worktree is on %s", rest)
	}
	if got.State != task.StateCompleted || got.PRNumber != 7 || got.PRHead != head {
		t.Fatalf("binding: pr %d head %q (want 7, %s), state %s", got.PRNumber, got.PRHead, head, got.State)
	}
}
