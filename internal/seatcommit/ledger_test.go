package seatcommit

import (
	"errors"
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

// The ledger lock makes the first archive decision stand. While the first
// writer is paused between its read and write, a second writer must not write
// its distinct snapshot into that window.
func TestArchiveAttemptKeepsTheFirstWriterDuringTheLedgerCriticalSection(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newFixture(t)
	tk := f.leased("cmd/")
	write(t, filepath.Join(f.wt, "first.go"), "package first\n")
	blocked, err := f.store.BlockWithPaths(tk.ID, f.handle, task.ReasonWidenRequested, "", task.RouteLead, []string{"first.go"}, f.now)
	if err != nil {
		t.Fatal(err)
	}
	expectedParent, expectedFirstTree := expectedArchiveSnapshot(t, f)
	once, entered, release := make(chan struct{}, 1), make(chan struct{}, 1), make(chan struct{})
	releaseHook := closeOnce(release)
	once <- struct{}{}
	beforeArchiveAttemptWrite = func(string, string) {
		select {
		case <-once:
			entered <- struct{}{}
			waitForArchiveRelease(release)
		default:
		}
	}
	cleanupArchiveHook(t, releaseHook)
	firstDone := make(chan struct {
		archived bool
		err      error
	}, 1)
	go func() {
		archived, err := ArchiveAttempt(f.root, f.handle, gitBin(t), nil, tk.ID, blocked.BlockAttempt, f.now)
		firstDone <- struct {
			archived bool
			err      error
		}{archived, err}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first archive did not reach its critical section")
	}
	write(t, filepath.Join(f.wt, "second.go"), "package second\n")
	sh(t, f.wt, "add", "-A")
	sh(t, f.wt, "commit", "-q", "-m", "different parent")
	secondTree := sh(t, f.wt, "rev-parse", "HEAD^{tree}")
	if secondTree == expectedFirstTree {
		t.Fatalf("second tree equals the independently captured first tree: %s", secondTree)
	}
	secondDone := make(chan error, 1)
	go func() {
		_, err := ArchiveAttempt(f.root, f.handle, gitBin(t), nil, tk.ID, blocked.BlockAttempt, f.now.Add(time.Second))
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		if !errors.Is(err, ErrLedgerBusy) {
			t.Fatalf("second archive entered the first writer's window: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second archive did not finish")
	}
	releaseHook()
	select {
	case got := <-firstDone:
		if got.err != nil || !got.archived {
			t.Fatalf("first archive = %+v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("first archive did not finish")
	}
	ledger, err := ReadLedger(f.root, f.handle)
	if err != nil || len(ledger.Discarded) != 1 || ledger.Discarded[0].Parent != expectedParent || ledger.Discarded[0].Tree != expectedFirstTree || ledger.Discarded[0].Tree == secondTree {
		t.Fatalf("surviving discard = %+v, expected parent/tree=(%s,%s), second tree=%s, err=%v", ledger.Discarded, expectedParent, expectedFirstTree, secondTree, err)
	}
	// The surviving tree's own content, independent of the captured tree (the
	// plan reviewer on #879: without the comparison above, a snapshot that
	// fell back to the parent tree passed): it is not the parent's tree, it
	// carries the first writer's first.go with its content, and it does not
	// carry the second writer's second.go.
	survivor := ledger.Discarded[0].Tree
	if parentTree := sh(t, f.wt, "rev-parse", expectedParent+"^{tree}"); survivor == parentTree {
		t.Fatalf("surviving discard's tree %s is the parent's tree: the snapshot did not record the first writer's change", survivor)
	}
	if got := sh(t, f.wt, "cat-file", "-p", survivor+":first.go"); got != "package first" {
		t.Fatalf("surviving discard's first.go = %q, want the first writer's", got)
	}
	if names := sh(t, f.wt, "ls-tree", "-r", "--name-only", survivor); strings.Contains("\n"+names+"\n", "\nsecond.go\n") {
		t.Fatalf("surviving discard carries the second writer's second.go: %s", names)
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

// Codex's B on #898 (prepare.go:97): the clean never removes bytes the
// ledger does not hold. An attempt archived once, then edited again under
// the same task and attempt, gets a second discard at the clean (the tree
// differs); the same tree is not archived twice.
func TestCleanSeatDoesNotTreatSameAttemptWithDifferentTreeAsAlreadyArchived(t *testing.T) {
	f := newFixture(t)
	tk := f.leased("first.go", "second.go")
	write(t, filepath.Join(f.wt, "first.go"), "package first\n")
	if archived, err := ArchiveAttemptReason(f.root, f.handle, gitBin(t), nil, tk.ID, 1, task.OutcomeCancelled, f.now); err != nil || !archived {
		t.Fatalf("first archive = %v, %v", archived, err)
	}
	if _, err := f.store.Cancel(tk.ID, "lead", "stop", f.now); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "second.go"), "package second\n")
	if discarded, err := CleanSeat(f.root, f.handle, gitBin(t), nil, f.now.Add(time.Second)); err != nil || !discarded {
		t.Fatalf("changed-tree clean = %v, %v", discarded, err)
	}
	ledger, err := ReadLedger(f.root, f.handle)
	if err != nil || len(ledger.Discarded) != 2 || ledger.Discarded[0].Tree == ledger.Discarded[1].Tree {
		t.Fatalf("clean did not append the changed tree: %+v, %v", ledger.Discarded, err)
	}
	if got := sh(t, f.wt, "cat-file", "-p", ledger.Discarded[1].Tree+":second.go"); got != "package second" {
		t.Fatalf("changed clean tree's second.go = %q", got)
	}
}
