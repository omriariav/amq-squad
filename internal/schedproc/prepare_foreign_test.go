package schedproc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Codex's P2 on #909 (cancel_scheduler_test.go:301: R6 archived by hand,
// with no Preparer), and G's PrepareSeat caller: the dispatch path, through
// the real preparer. A seat worktree left on a cancelled task's branch that
// its commit ledger does not name (t86's shape: no lease record; the branch
// was checked out by the seat, never by StartTask), with only an untracked
// file: the tick's dispatch calls CleanSeats.PrepareSeat, which archives
// the file on the ledger and moves the worktree back to the seat branch,
// and the next task is granted to the seat. Reverted (the old refusal of a
// branch the lease does not name), the prepare refuses
// (seat_prepare_failed) and nothing is granted.
func TestDispatchPreparesAForeignBranchWorktreeThroughCleanSeatsAndGrants(t *testing.T) {
	q := squadFixture(t)
	slots := &Slots{Squad: q}
	planned, err := slots.Plan(scheduler.SeatRequest{ID: "setup", Pool: sonnet})
	if err != nil {
		t.Fatal(err)
	}
	seat := planned.Seats[0]
	root := q.SessionRoot()
	now := time.Now().UTC()
	if _, err := seatcommit.RecordWorktreeBase("git", root, seat.Handle, seat.Worktree, seat.Branch, seatcommit.Target{BaseBranch: "main"}, now); err != nil {
		t.Fatal(err)
	}
	staged := launch.StagedDir(root, seat.Handle)
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Handle: seat.Handle, Root: root, Session: q.Session, Runtime: "claude", Worktree: seat.Worktree, Branch: seat.Branch, Base: seat.Base}); err != nil {
		t.Fatal(err)
	}
	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	cancelled, err := store.Add(task.AddInput{Title: "cancelled", Class: "script", Pool: &sonnet}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Claim(cancelled.ID, seat.Handle, &sonnet, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Cancel(cancelled.ID, "lead", "stop", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	taskBranch := seatcommit.TaskBranch(seat.Handle, cancelled.ID)
	gitRun(t, seat.Worktree, "checkout", "-q", "-b", taskBranch)
	scratch := filepath.Join(seat.Worktree, "scratch.txt")
	if err := os.WriteFile(scratch, []byte("untracked cancellation evidence\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if l, err := seatcommit.ReadLedger(root, seat.Handle); err != nil || l.Lease != nil {
		t.Fatalf("the ledger names a lease (want t86's shape, none): %+v, %v", l.Lease, err)
	}
	next, err := store.Add(task.AddInput{Title: "next", Class: "script", Pool: &sonnet}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	cleaner := &CleanSeats{Root: root, Git: "git"}
	cleaner.SetLog(&log, func() time.Time { return now.Add(3 * time.Second) })
	launcher := &fakeLauncher{}
	s, err := scheduler.New(scheduler.Options{Store: store, Now: func() time.Time { return now.Add(3 * time.Second) }, Launcher: launcher, Preparer: cleaner, Config: scheduler.Config{IdleTimeout: time.Hour}, Incarnation: "foreignprepare"})
	if err != nil {
		t.Fatal(err)
	}
	launcher.s = s
	s.AdoptUp(seat.Handle, sonnet, now.Add(3*time.Second))
	decisions, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	dispatched := false
	for _, d := range decisions {
		if d.Kind == "seat_prepare_failed" {
			t.Fatalf("the prepare refused the foreign branch: %+v", d)
		}
		dispatched = dispatched || (d.Kind == "dispatched" && d.TaskID == next.ID && d.Seat == seat.Handle)
	}
	granted, err := store.Get(next.ID)
	if err != nil || !dispatched || granted.Lease == nil || granted.Lease.Holder != seat.Handle {
		t.Fatalf("the next task was not granted to the prepared seat: lease=%+v decisions=%+v err=%v", granted.Lease, decisions, err)
	}
	if got := gitRun(t, seat.Worktree, "symbolic-ref", "--short", "HEAD"); got != seat.Branch {
		t.Fatalf("the worktree is on %s, not the seat branch %s", got, seat.Branch)
	}
	if _, err := os.Stat(scratch); !os.IsNotExist(err) {
		t.Fatalf("scratch.txt remains after the prepare: %v", err)
	}
	if got := gitRun(t, seat.Worktree, "rev-parse", "--verify", "refs/heads/"+taskBranch); got == "" {
		t.Fatal("the prepare deleted the cancelled task's branch")
	}
	ledger, err := seatcommit.ReadLedger(root, seat.Handle)
	if err != nil || len(ledger.Discarded) != 1 {
		t.Fatalf("the prepare's archive: %+v, %v", ledger.Discarded, err)
	}
	if got := gitRun(t, seat.Worktree, "cat-file", "-p", ledger.Discarded[0].Tree+":scratch.txt"); got != "untracked cancellation evidence" {
		t.Fatalf("the archived scratch.txt: %q", got)
	}
	if !strings.Contains(log.String(), `"kind":"seat_cleaned"`) {
		t.Fatalf("the prepare's clean was not logged:\n%s", log.String())
	}
}
