package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The global_config_changed block through the verbs: seat commit keeps the
// tree uncommitted, blocks the task to the owner and quarantines the seat (a
// lease for it is refused); seat clear refreshes the preflight copy, lifts
// the quarantine and re-arms the task; seat commit then pushes the kept tree.
func TestSeatCommitQuarantineAndOwnerClear(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1}`)
	mustWrite(t, filepath.Join(home, ".amq-squad", "preflight", "claude-settings.json"), `{"a":1}`)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	repo, wt := seatRepo(t, root)
	s, _ := task.Open(root)
	tk, _ := s.Add(task.AddInput{Title: "x", Class: "implement", DeclaredFiles: []string{"cmd/"}, ProjectPath: repo}, time.Now())
	if _, err := s.Claim(tk.ID, "s1", nil, time.Hour, time.Now()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(wt, "cmd", "root.go"), "package cmd\n// w\n")
	if _, err := s.Done(tk.ID, "s1", "ok", "", time.Now()); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(home, ".claude", "settings.json"), `{"a":1,"hooks":{"PreToolUse":[]}}`)
	_, _, err := captureOutput(t, func() error { return runSeatCommit([]string{"--seat", "s1", "--root", root}, os.Stdout) })
	if ExitCode(err) != ExitCommitRefused {
		t.Fatalf("seat commit: %v", err)
	}
	got, _ := s.Get(tk.ID)
	if got.BlockReason != seatcommit.ReasonGlobalConfig || got.BlockRoute != task.RouteOwner {
		t.Fatalf("task: %+v", got)
	}
	other, _ := s.Add(task.AddInput{Title: "y", Class: "implement"}, time.Now())
	if _, err := s.Grant(other.ID, "s1", nil, time.Minute, time.Now()); err == nil {
		t.Fatal("a quarantined seat got a lease")
	}
	l, _ := seatcommit.ReadLedger(root, "s1")
	if l.Quarantine == nil || l.Quarantine.Tree == "" || l.Head != "" {
		t.Fatalf("ledger: %+v", l)
	}
	if _, _, err := captureOutput(t, func() error { return runSeatClear([]string{"--seat", "s1", "--root", root, "--me", "s1"}, os.Stdout) }); err == nil {
		t.Fatal("a seat cleared its own quarantine")
	}
	if _, _, err := captureOutput(t, func() error { return runSeatClear([]string{"--seat", "s1", "--root", root, "--me", "user"}, os.Stdout) }); err != nil {
		t.Fatalf("seat clear: %v", err)
	}
	if got, _ := s.Get(tk.ID); got.State != task.StateInProgress || got.Report == nil {
		t.Fatalf("not re-armed: %+v", got)
	}
	if out, _, err := captureOutput(t, func() error { return runSeatCommit([]string{"--seat", "s1", "--root", root}, os.Stdout) }); err != nil {
		t.Fatalf("seat commit after the clear: %v\n%s", err, out)
	}
	if got, _ := s.Get(tk.ID); got.State != task.StateCompleted {
		t.Fatalf("task after the clear: %+v", got)
	}
}
