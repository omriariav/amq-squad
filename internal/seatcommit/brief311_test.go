package seatcommit

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Brief 31-1, row 1 (the commit step's half): a Claude seat that ran (an
// accepted proof) with no hook report since its launch, while the owner's
// global agent config changed during its run, is not quarantined: its exit
// check is OK and its done report commits and pushes. Status shows the
// silent hook as a warning (internal/status's row).
func TestASilentClaudeSeatCommitsAndIsNotQuarantined(t *testing.T) {
	f := newFixture(t)
	if _, err := claudeseat.Write(claudeseat.Dir(f.root, f.handle, "launch-1"), "dontAsk", f.wt, f.now); err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{Handle: f.handle, Root: f.root, Session: "sess", Runtime: "claude", Worktree: f.wt, Branch: f.branch, LaunchID: "launch-1"}
	if err := launch.WriteFile(filepath.Join(launch.StagedDir(f.root, f.handle), launch.FileName), rec); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteProof(f.root, launch.ProofEntry{Handle: f.handle, LaunchID: "launch-1", PID: 4242, Start: "s", At: f.now}); err != nil {
		t.Fatal(err)
	}
	if n, err := claudeseat.ReportsSince(f.root, f.handle, f.now); err != nil || n != 0 {
		t.Fatalf("the fixture's seat has %d hook reports (%v), want a silent one", n, err)
	}
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"changed":"during the run"}`)
	if r := ExitCheck(f.opts(task.Task{ID: "t1"}), "gone"); !r.OK {
		t.Fatalf("the exit check of a silent seat: %+v", r)
	}
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n// the silent seat's work\n")
	tk := f.reported("cmd/")
	if res := f.run(tk); !res.OK || !res.Pushed || res.Commit == "" {
		t.Fatalf("the silent seat's done report: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(task.DirFor(f.root), "quarantine")); !os.IsNotExist(err) {
		t.Fatalf("a quarantine was written: %v", err)
	}
	if l, err := ReadLedger(f.root, f.handle); err != nil || l.Quarantine != nil {
		t.Fatalf("the ledger carries a quarantine: %+v %v", l.Quarantine, err)
	}
}
