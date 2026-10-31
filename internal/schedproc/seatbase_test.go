package schedproc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// seatBaseFixture is the squad fixture with a fake remote (a bare repository
// in the temp dir, the grant's push URL) holding main, and a second clone
// that moves the remote's main as another merge would.
func seatBaseFixture(t *testing.T) (q seatlaunch.Squad, remote, other string) {
	t.Helper()
	q = squadFixture(t)
	base := filepath.Dir(q.ProjectRoot)
	remote = filepath.Join(base, "remote.git")
	gitRun(t, base, "init", "-q", "--bare", "-b", "main", remote)
	gitRun(t, q.ProjectRoot, "push", "-q", remote, "main")
	other = filepath.Join(base, "other")
	gitRun(t, base, "clone", "-q", remote, other)
	return q, remote, other
}

func commitFile(t *testing.T, dir, name, body, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", msg)
	return gitRun(t, dir, "rev-parse", "HEAD")
}

var seatBasePool = task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}

// 3.0.2 t22: a relaunched seat whose branch and worktree sit on an old base
// (its first launch's base plus its own commit, the ledger's chain) gets
// its worktree and resting branch at the base branch's tip fetched from the
// grant's push URL (the local main is left behind), on the roster launch
// too, from another branch its worktree was left on; its next commit's
// parent is the tip, and the launch record carries the base.
func TestSeatBaseRelaunchAtTheTip(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	old := gitRun(t, q.ProjectRoot, "rev-parse", "HEAD")
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil || first.Seats[0].Base != old {
		t.Fatalf("first launch: %+v %v", first.Seats, err)
	}
	wt := first.Seats[0].Worktree
	seatCommit := commitFile(t, wt, "t5.txt", "t5\n", "the seat's t5 commit")
	gitRun(t, wt, "checkout", "-q", "-b", "task-t5") // left on another branch
	s.ReleaseRequest("r1")
	tip := commitFile(t, other, "v3.txt", "two days of v3\n", "v3 moves")
	gitRun(t, other, "push", "-q", "origin", "main")

	got, err := s.PlanRoster([]scheduler.SeatRequest{{ID: "r2", Pool: seatBasePool, Handle: "c1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Seats[0].Base != tip {
		t.Fatalf("the launch's base is %s, not the tip %s (old %s, seat commit %s)", got.Seats[0].Base, tip, old, seatCommit)
	}
	if h := gitRun(t, wt, "rev-parse", "HEAD"); h != tip {
		t.Fatalf("the worktree is at %s, not the tip %s", h, tip)
	}
	if b := gitRun(t, q.ProjectRoot, "rev-parse", "refs/heads/seat/c1"); b != tip {
		t.Fatalf("seat/c1 is at %s, not the tip", b)
	}
	if s := gitRun(t, wt, "symbolic-ref", "HEAD"); s != "refs/heads/seat/c1" {
		t.Fatalf("the worktree's HEAD names %s", s)
	}
	if raw, err := os.ReadFile(filepath.Join(wt, "v3.txt")); err != nil || string(raw) != "two days of v3\n" {
		t.Fatalf("the tip's file in the worktree: %q %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join(wt, "t5.txt")); !os.IsNotExist(err) {
		t.Fatalf("the old seat commit's file is still in the worktree: %v", err)
	}
	if st := gitRun(t, wt, "status", "--porcelain"); st != "" {
		t.Fatalf("the moved worktree is not clean:\n%s", st)
	}
	commitFile(t, wt, "t12.txt", "harness\n", "the seat's next commit")
	if p := gitRun(t, wt, "rev-parse", "HEAD^"); p != tip {
		t.Fatalf("the next commit's parent is %s, not the tip %s", p, tip)
	}
	got.ClaudePins = map[string]registry.Pin{"c1": {Path: "/opt/pins/claude", SHA256: strings.Repeat("a", 64)}}
	c, err := seatlaunch.Compile(got, "", func(n string) (string, error) { return "/usr/bin/" + n, nil }, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if c.Records[0].Base != tip {
		t.Fatalf("the launch record's base is %q, not the tip %s", c.Records[0].Base, tip)
	}
}

// With no push URL in the grant, the local base branch's tip is the base,
// and a worktree created at a launch starts on it with a stale seat branch
// reset to it.
func TestSeatBaseLocalBranchWithoutPushURL(t *testing.T) {
	q := squadFixture(t)
	old := gitRun(t, q.ProjectRoot, "rev-parse", "HEAD")
	gitRun(t, q.ProjectRoot, "branch", "seat/c1", old)
	tip := commitFile(t, q.ProjectRoot, "b.txt", "b\n", "main moves")
	s := &Slots{Squad: q}
	got, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	if got.Seats[0].Base != tip || gitRun(t, got.Seats[0].Worktree, "rev-parse", "HEAD") != tip {
		t.Fatalf("a new worktree on a stale seat branch: base %s, want %s", got.Seats[0].Base, tip)
	}
}

// A remote seat branch ahead of the tip and unmerged refuses the launch by
// name (the branch and its commits); nothing is moved. Once merged, the
// launch proceeds and resets the branch to the tip.
func TestSeatBaseRemoteSeatBranchAheadRefuses(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	wt := first.Seats[0].Worktree
	pr := commitFile(t, wt, "pr.txt", "pr\n", "an open PR's commit")
	gitRun(t, wt, "push", "-q", remote, "seat/c1")
	s.ReleaseRequest("r1")
	_, err = s.Plan(scheduler.SeatRequest{ID: "r2", Pool: seatBasePool})
	if err == nil || !strings.Contains(err.Error(), ReasonSeatBranchUnmerged) || !strings.Contains(err.Error(), "seat/c1") || !strings.Contains(err.Error(), pr[:7]) {
		t.Fatalf("an unmerged remote seat branch: %v", err)
	}
	if h := gitRun(t, wt, "rev-parse", "HEAD"); h != pr {
		t.Fatalf("a refused launch moved the worktree to %s", h)
	}
	s.ReleaseRequest("r2")
	gitRun(t, other, "pull", "-q", "--no-rebase", "--no-edit", "origin", "seat/c1")
	gitRun(t, other, "push", "-q", "origin", "main")
	tip := commitFile(t, other, "c.txt", "c\n", "v3 moves on")
	gitRun(t, other, "push", "-q", "origin", "main")
	got, err := s.Plan(scheduler.SeatRequest{ID: "r3", Pool: seatBasePool})
	if err != nil || got.Seats[0].Base != tip || gitRun(t, wt, "rev-parse", "HEAD") != tip {
		t.Fatalf("a merged remote seat branch: %+v %v", got.Seats, err)
	}
}

// A local change the move to the tip would overwrite refuses the launch and
// is kept in the worktree (never lost without evidence).
func TestSeatBaseConflictingLocalChangeRefuses(t *testing.T) {
	q, remote, other := seatBaseFixture(t)
	s := &Slots{Squad: q, Target: seatcommit.Target{PushURL: remote, BaseBranch: "main"}}
	first, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: seatBasePool})
	if err != nil {
		t.Fatal(err)
	}
	wt := first.Seats[0].Worktree
	s.ReleaseRequest("r1")
	commitFile(t, other, "a.txt", "upstream\n", "a changes upstream")
	gitRun(t, other, "push", "-q", "origin", "main")
	if err := os.WriteFile(filepath.Join(wt, "a.txt"), []byte("seat's leftover\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r2", Pool: seatBasePool}); err == nil || !strings.Contains(err.Error(), "to the base tip") {
		t.Fatalf("a conflicting local change: %v", err)
	}
	if raw, _ := os.ReadFile(filepath.Join(wt, "a.txt")); string(raw) != "seat's leftover\n" {
		t.Fatalf("the local change was lost: %q", raw)
	}
}
