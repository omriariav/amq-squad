package schedproc

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func squadFixture(t *testing.T) seatlaunch.Squad {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	repo := filepath.Join(base, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun(t, repo, "add", "-A")
	gitRun(t, repo, "commit", "-q", "-m", "base")
	q := seatlaunch.Squad{ProjectRoot: repo, Session: "s1", Wrapper: "/opt/amq-squad", AMQPath: "/opt/amq", Seats: []seatlaunch.Seat{
		{Handle: "c1", Runtime: "claude", Model: "sonnet", Effort: "medium", Role: "# Role\n\nimplementer\n", RoleName: "implementer", Worktree: filepath.Join(base, "wt", "c1")},
		{Handle: "p1", Runtime: "pi", Role: "# Role\n\npi worker\n", RoleName: "implementer", Worktree: filepath.Join(base, "wt", "p1")},
		{Handle: "root", Runtime: "claude", Model: "opus", Effort: "high", Role: "# Role\n\nlead\n", Worktree: repo},
	}}
	q, err := q.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(q.SessionRoot(), "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	return q
}

// Slots: a request takes a free seat of its pool, whose worktree and branch
// are created from the base branch with hooks off; a seat already in use
// is not taken twice; a seat whose worktree is the repository root is never
// started; a release frees it.
func TestSlotsPlanWorktreesAndRelease(t *testing.T) {
	q := squadFixture(t)
	s := &Slots{Squad: q}
	claude := task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	got, err := s.Plan(scheduler.SeatRequest{ID: "r1", Pool: claude})
	if err != nil || len(got.Seats) != 1 || got.Seats[0].Handle != "c1" || got.Seats[0].Branch != "seat/c1" || got.Seats[0].RequestID != "r1" {
		t.Fatalf("plan: %+v %v", got.Seats, err)
	}
	if b := gitRun(t, got.Seats[0].Worktree, "symbolic-ref", "HEAD"); b != "refs/heads/seat/c1" {
		t.Fatalf("worktree branch %s", b)
	}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r2", Pool: claude}); err == nil {
		t.Fatal("a seat in use was planned twice")
	}
	s.ReleaseRequest("r1")
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r3", Pool: claude}); err != nil {
		t.Fatalf("after the release: %v", err)
	}
	if _, err := s.Plan(scheduler.SeatRequest{ID: "r4", Pool: task.Pool{Runtime: "claude", Model: "opus", Effort: "high"}}); err == nil || !strings.Contains(err.Error(), "no worktree of its own") {
		t.Fatalf("a repository-root seat: %v", err)
	}
}

// External (the pi stand-in on AMQ 0.107.0): the core still stages the
// record and role, records the commit base, and writes the request file
// for the runner; cancel and close leave files the runner obeys.
func TestExternalLauncherStagesAndWritesTheRequest(t *testing.T) {
	q := squadFixture(t)
	root := q.SessionRoot()
	e := External{Slots: &Slots{Squad: q}, Root: root}
	pi := task.Pool{Runtime: "pi", Model: "qwen3.6:27b", Effort: "medium"}
	if err := e.RequestSeat(scheduler.SeatRequest{ID: "seat-req-x-1", Pool: pi, TaskID: "t1"}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(RequestsDir(root), "seat-req-x-1.json"))
	var r Request
	if err != nil || json.Unmarshal(raw, &r) != nil || r.Handle != "p1" || r.TaskID != "t1" || r.Branch != "seat/p1" || r.Session != "s1" {
		t.Fatalf("request: %s %v", raw, err)
	}
	staged, err := os.ReadFile(filepath.Join(launch.StagedDir(root, "p1"), launch.FileName))
	if err != nil || !strings.Contains(string(staged), `"request_id": "seat-req-x-1"`) || !strings.Contains(string(staged), `"launcher": "external"`) {
		t.Fatalf("staged record: %s %v", staged, err)
	}
	if l, err := seatcommit.ReadLedger(root, "p1"); err != nil || l.Base == "" || l.Branch != "seat/p1" {
		t.Fatalf("commit base: %+v %v", l, err)
	}
	if err := e.CancelSeatRequest(scheduler.SeatRequest{ID: "seat-req-x-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(RequestsDir(root), "seat-req-x-1.cancel")); err != nil {
		t.Fatal("no cancel file")
	}
	if err := e.CloseSeat("p1", "idle"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(RequestsDir(root), "close-p1.json")); err != nil {
		t.Fatal("no close file")
	}
}

// The committer passes a deferral through with its reason (the scheduler
// counts deferrals in the step's exits entry, v3-5-verifier P2-4), reruns a
// deferred step at the next call, and passes the quarantine of a
// global_config_changed refusal through.
func TestCommitterDeferralsAndQuarantine(t *testing.T) {
	results := []seatcommit.Result{{Deferred: true, ReasonCode: seatcommit.ReasonVerifierInterrupted, Detail: "push"}, {Deferred: true, Detail: "push"}}
	c := &Committer{Step: func(context.Context, string, task.Task) seatcommit.Result {
		r := results[0]
		results = results[1:]
		return r
	}}
	tk := task.Task{ID: "t1"}
	if r := c.Commit("s1", tk); !r.Deferred || r.ReasonCode != seatcommit.ReasonVerifierInterrupted {
		t.Fatalf("1: %+v", r)
	}
	if r := c.Commit("s1", tk); !r.Deferred || len(results) != 0 {
		t.Fatalf("2: %+v (the deferred step was not run again)", r)
	}
	if scheduler.ReasonVerifierInterrupted != seatcommit.ReasonVerifierInterrupted || scheduler.ReasonCommitFailed != seatcommit.ReasonCommitFailed {
		t.Fatal("the scheduler's limit codes are not seatcommit's")
	}
	q := &Committer{Step: func(context.Context, string, task.Task) seatcommit.Result {
		return seatcommit.Result{ReasonCode: seatcommit.ReasonGlobalConfig, Route: task.RouteOwner, Quarantine: true, Detail: "x"}
	}}
	if r := q.Commit("s1", tk); !r.Quarantine || r.Route != task.RouteOwner {
		t.Fatalf("quarantine: %+v", r)
	}
}

// The doorbell and the lead notice are AMQ sends from the scheduler mailbox
// with the session identity, never the caller's.
func TestDoorbellAndLeadNoticeSend(t *testing.T) {
	t.Setenv("AM_ME", "lead")
	var got [][]string
	s := Sender{AMQ: "/opt/amq", Root: "/r/s1", BaseRoot: "/r", Session: "s1", Run: func(_ context.Context, amq string, args, env []string) ([]byte, error) {
		me, err := oneAMQMe(env)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, append([]string{amq, me}, args...))
		return nil, nil
	}}
	tk := task.Task{ID: "t3", Title: "fix", Class: "implement", DeclaredFiles: []string{"a.go"}, BlockReason: "outside_declared_files", BlockRoute: task.RouteLead, BlockDetail: "b.go"}
	if err := (Doorbell{Sender: s}).Ring("s1", tk); err != nil {
		t.Fatal(err)
	}
	if err := (LeadNotice{Sender: s, Lead: "lead"}).CommitRefused(tk); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0][1] != "AM_ME=scheduler" || strings.Join(got[0][2:8], " ") != "send --me scheduler --to s1 --kind" ||
		got[1][6] != "lead" || !strings.Contains(strings.Join(got[1], " "), "commit refused: t3 outside_declared_files") || !strings.Contains(strings.Join(got[1], " "), "amq-squad task widen t3 --files") {
		t.Fatalf("sends: %q", got)
	}
}
