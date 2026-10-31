package schedproc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// 3.0.2 t23, the reviewer's ruling on the base: it refreshes at the grant.
// A seat launched on tip A; a merge on the fake remote moves its base branch
// to B; the next grant's doorbell Start (Slots.TaskStart) fetches B, cuts
// seat/c1-t1 from it and records B as the task's base on the ledger; the
// task's commit has B as its parent and the ledger's commit says base B.
// The local main stays at A. Fails when the grant keeps the launch's base.
func TestGrantBasesTheTaskOnTheRemoteTipFetchedAtTheGrant(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	a := gitRun(t, q.ProjectRoot, "rev-parse", "HEAD")
	target := seatcommit.Target{Repository: "o/r", BaseBranch: "main", PushURL: remote, LocalPushAck: true}
	s := &Slots{Squad: q, BaseBranch: "main", Target: target}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil || first.Seats[0].Base != a {
		t.Fatalf("the launch: %+v %v", first.Seats, err)
	}
	wt := first.Seats[0].Worktree
	root := filepath.Join(filepath.Dir(q.ProjectRoot), "amq", "s1")
	staged := launch.StagedDir(root, "c1")
	if err := os.MkdirAll(staged, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Handle: "c1", Root: root, Session: "s1", Runtime: "claude", Worktree: wt, Branch: "seat/c1", Base: a}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if _, err := seatcommit.RecordWorktreeBase("", root, "c1", wt, "seat/c1", target, now); err != nil {
		t.Fatal(err)
	}
	b := commitFile(t, other, "merged.txt", "merged\n", "a merge on the remote")
	gitRun(t, other, "push", "-q", "origin", "main")

	store, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tk, err := store.Add(task.AddInput{Title: "work", Class: "summarize", DeclaredFiles: []string{"work.txt"}, ProjectPath: q.ProjectRoot}, now)
	if err != nil {
		t.Fatal(err)
	}
	if tk, err = store.Grant(tk.ID, "c1", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if err := s.TaskStart(root, func() time.Time { return now })("c1", tk); err != nil {
		t.Fatal(err)
	}
	if h, ref := gitRun(t, wt, "rev-parse", "HEAD"), gitRun(t, wt, "symbolic-ref", "HEAD"); h != b || ref != "refs/heads/seat/c1-"+tk.ID {
		t.Fatalf("the granted worktree is on %s at %s, not seat/c1-%s at the remote tip %s (launch base %s)", ref, h, tk.ID, b, a)
	}
	if l, _ := seatcommit.ReadLedger(root, "c1"); l.Lease == nil || l.Lease.Base != b {
		t.Fatalf("the ledger's task base: %+v", l.Lease)
	}
	if err := os.WriteFile(filepath.Join(wt, "work.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Claim(tk.ID, "c1", nil, time.Hour, now); err != nil {
		t.Fatal(err)
	}
	if tk, err = store.Done(tk.ID, "c1", "ok", "", now); err != nil {
		t.Fatal(err)
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	g := grants.Resolved{Grants: grants.Defaults(), Source: "default"}
	g.Merge = []grants.Merge{{Repository: "o/r", BaseBranch: "main", PushURL: remote, LocalPushAck: true}}
	res := seatcommit.Run(seatcommit.Options{Root: root, Handle: "c1", Task: tk, Grants: g, Home: home, Now: func() time.Time { return now }})
	l, _ := seatcommit.ReadLedger(root, "c1")
	if !res.OK || res.Parent != b || res.Branch != "seat/c1-"+tk.ID || len(l.Commits) != 1 || l.Commits[0].Base != b || l.Commits[0].Parent != b {
		t.Fatalf("the task's commit: %+v\nledger %+v", res, l.Commits)
	}
	if got := gitRun(t, remote, "rev-parse", "refs/heads/seat/c1-"+tk.ID); got != res.Commit {
		t.Fatalf("the remote's task branch is at %s, not %s", got, res.Commit)
	}
	if local := gitRun(t, q.ProjectRoot, "rev-parse", "main"); local != a {
		t.Fatalf("the local main moved: %s", local)
	}
}

// A failed fetch at the grant is the unprepared path: the doorbell rings
// nothing and returns scheduler.LeaseUnpreparedError (the scheduler then
// releases the lease and blocks the task as task_branch_unprepared); no
// lease start is written and the worktree stays on its resting branch.
func TestGrantBaseFetchFailureRingsNothing(t *testing.T) {
	q, remote, _ := seatBaseFixture(t)
	target := seatcommit.Target{Repository: "o/r", BaseBranch: "main", PushURL: remote, LocalPushAck: true}
	s := &Slots{Squad: q, BaseBranch: "main", Target: target}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	wt := first.Seats[0].Worktree
	root := filepath.Join(filepath.Dir(q.ProjectRoot), "amq", "s1")
	staged := launch.StagedDir(root, "c1")
	os.MkdirAll(staged, 0o700)
	if err := launch.WriteFile(filepath.Join(staged, launch.FileName), launch.Record{Handle: "c1", Root: root, Session: "s1", Runtime: "claude", Worktree: wt, Branch: "seat/c1", Base: first.Seats[0].Base}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	if _, err := seatcommit.RecordWorktreeBase("", root, "c1", wt, "seat/c1", target, now); err != nil {
		t.Fatal(err)
	}
	s.Target.PushURL = filepath.Join(filepath.Dir(remote), "gone.git") // the remote cannot be fetched
	sent := 0
	bell := Doorbell{Sender: Sender{AMQ: "/opt/amq", Root: root, Session: "s1", Run: func(_ context.Context, _ string, _, _ []string) ([]byte, error) { sent++; return nil, nil }},
		Start: s.TaskStart(root, func() time.Time { return now })}
	err = bell.Ring("c1", task.Task{ID: "t1", Title: "work", Class: "summarize"})
	var unprepared *scheduler.LeaseUnpreparedError
	if !errors.As(err, &unprepared) || sent != 0 {
		t.Fatalf("a failed fetch: %v, sends %d", err, sent)
	}
	if l, _ := seatcommit.ReadLedger(root, "c1"); l.Lease != nil {
		t.Fatalf("a lease start was written: %+v", l.Lease)
	}
	if ref := gitRun(t, wt, "symbolic-ref", "HEAD"); ref != "refs/heads/seat/c1" {
		t.Fatalf("the worktree moved to %s", ref)
	}
}
