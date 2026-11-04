package seatcommit

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func TestTypedWidenSnapshotIsKeyedByStoppedAttempt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	f.grants.Merge = []grants.Merge{f.merge(1, 0)}
	tk := f.leased("cmd/")
	write(t, filepath.Join(f.wt, "extra.go"), "package extra\n1\n2\n3\n")
	blocked, err := f.store.BlockWithPaths(tk.ID, f.handle, task.ReasonWidenRequested, "", task.RouteLead, []string{"extra.go"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	attempt := blocked.Attempts[len(blocked.Attempts)-1].N
	check := func(n int) WidenDecision {
		return CheckWiden(WidenOptions{Root: f.root, Task: blocked, Added: []string{"extra.go"}, Grants: f.grants, RepoRoot: f.main,
			Commit: &Options{Root: f.root, Handle: f.handle, SnapshotAttempt: n, Now: func() time.Time { return f.now }}})
	}
	if d := check(attempt); d.ReasonCode != ReasonWidenSnapshotMissing || !strings.Contains(d.Detail, "is missing") {
		t.Fatalf("missing snapshot = %+v", d)
	}
	if archived, err := ArchiveAttempt(f.root, f.handle, gitBin(t), nil, tk.ID, attempt, f.now); err != nil || !archived {
		t.Fatalf("scheduler archive = %v, %v", archived, err)
	}
	if archived, err := ArchiveAttempt(f.root, f.handle, gitBin(t), nil, tk.ID, attempt, f.now); err != nil || archived {
		t.Fatalf("duplicate scheduler archive = %v, %v", archived, err)
	}
	if discarded, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now); err != nil || !discarded {
		t.Fatalf("CleanSeat = %v, %v", discarded, err)
	}
	l, err := ReadLedger(f.root, f.handle)
	if err != nil || len(l.Discarded) != 1 || l.Discarded[0].TaskID != tk.ID || l.Discarded[0].Attempt != attempt {
		t.Fatalf("attempt archive = %+v, %v", l.Discarded, err)
	}
	if d := check(attempt + 1); d.ReasonCode != ReasonWidenSnapshotMismatch || !strings.Contains(d.Detail, "does not match stopped attempt") {
		t.Fatalf("mismatched snapshot = %+v", d)
	}
	if d := check(attempt); d.ReasonCode != ReasonGrowthGate || d.DryRun == nil || d.DryRun.Lines <= 1 {
		t.Fatalf("archived snapshot size = %+v", d)
	}
}

func TestCleanSeatArchiveWinsBeforeLaterAttemptArchives(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	tk := f.leased("cmd/")
	write(t, filepath.Join(f.wt, "first.go"), "package first\n")
	blocked, err := f.store.BlockWithPaths(tk.ID, f.handle, task.ReasonWidenRequested, "", task.RouteLead, []string{"first.go"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	attempt := blocked.BlockAttempt
	if discarded, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now); err != nil || !discarded {
		t.Fatalf("CleanSeat first = %v, %v", discarded, err)
	}
	first, err := ReadLedger(f.root, f.handle)
	if err != nil || len(first.Discarded) != 1 {
		t.Fatalf("first archive = %+v, %v", first.Discarded, err)
	}
	write(t, filepath.Join(f.wt, "second.go"), "package second\n// changed after cleanup\n")
	for _, writer := range []string{"scheduler", "on-demand"} {
		if archived, err := ArchiveAttempt(f.root, f.handle, gitBin(t), nil, tk.ID, attempt, f.now.Add(time.Second)); err != nil || archived {
			t.Fatalf("%s archive = %v, %v", writer, archived, err)
		}
	}
	after, err := ReadLedger(f.root, f.handle)
	if err != nil || len(after.Discarded) != 1 || after.Discarded[0].Tree != first.Discarded[0].Tree || after.Discarded[0].Parent != first.Discarded[0].Parent {
		t.Fatalf("first writer changed: before=%+v after=%+v err=%v", first.Discarded, after.Discarded, err)
	}
}
