package schedproc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/ledger"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	// Isolated from the owner's git: a temp HOME, no global or system config.
	cmd.Env = append(os.Environ(), "HOME="+t.TempDir(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@x.invalid", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@x.invalid")
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
	// Every git under test, the code's own included, runs off the owner's
	// git: a temp HOME, no global or system config, no ambient redirection.
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for _, k := range []string{"GIT_DIR", "GIT_WORK_TREE", "GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS"} {
		t.Setenv(k, "") // restored at cleanup
		os.Unsetenv(k)
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

// The committer passes a deferral through with its reason (the scheduler
// counts deferrals in the step's exits entry, v3-5-verifier P2-4), reruns a
// deferred step at the next call, and passes the quarantine of a
// global_config_changed refusal through.
func TestCommitterDeferrals(t *testing.T) {
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
	// 3.0.2 t23: the doorbell puts the worktree on the task's branch before
	// the seat is rung; a failure there rings nothing and comes back as the
	// scheduler's LeaseUnpreparedError.
	var started []string
	start := func(h string, st task.Task) error {
		started = append(started, h+" "+st.ID+" before "+fmt.Sprint(len(got))+" sends")
		return errors.New("busy ledger")
	}
	err := (Doorbell{Sender: s, Start: start}).Ring("s1", tk)
	var unprepared *scheduler.LeaseUnpreparedError
	if !errors.As(err, &unprepared) || !strings.Contains(err.Error(), "busy ledger") || len(started) != 1 || started[0] != "s1 t3 before 0 sends" || len(got) != 0 {
		t.Fatalf("start: %v %q, sends %d", err, started, len(got))
	}
	if err := (Doorbell{Sender: s, Start: func(string, task.Task) error { return nil }}).Ring("s1", tk); err != nil || len(got) != 1 {
		t.Fatalf("a prepared seat is rung: %v, sends %d", err, len(got))
	}
	got = nil
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

func TestCodexLaunchAdmissionControlsTheDoorbellManualRenewFallback(t *testing.T) {
	root := t.TempDir()
	launch := time.Date(2026, 11, 10, 20, 0, 0, 0, time.UTC)
	slots := &Slots{Root: root, Squad: seatlaunch.Squad{Seats: []seatlaunch.Seat{
		{Handle: "codex", Runtime: "codex"}, {Handle: "trusted", Runtime: "codex"}, {Handle: "other", Runtime: "codex"}, {Handle: "claude", Runtime: "claude"},
	}}}
	if !slots.NeedsManualRenew("codex") || slots.NeedsManualRenew("claude") {
		t.Fatal("an unadmitted Codex seat alone needs the fallback")
	}
	slots.AdmitCodexActivity("codex", true, launch)
	if slots.NeedsManualRenew("codex") {
		t.Fatal("a trusted Codex launch kept the manual fallback")
	}
	slots.AdmitCodexActivity("codex", false, launch)
	appendActivityRow := func(pathHandle, rowHandle string, at time.Time) {
		t.Helper()
		if _, err := ledger.Append(ledger.Path(root, pathHandle), func(seq uint64) any {
			return map[string]any{"schema": ledger.LineSchema, "seq": seq, "at": at.Format(time.RFC3339Nano), "handle": rowHandle, "event": "PostToolUse", "tool": "Bash", "outcome": "ok"}
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendActivity := func(handle string, at time.Time) { appendActivityRow(handle, handle, at) }
	appendActivityRow("codex", "other", launch.Add(time.Second))
	appendActivity("other", launch.Add(time.Second))
	if !slots.NeedsManualRenew("codex") {
		t.Fatal("another handle's ingest supplied Codex activity admission")
	}
	appendActivity("codex", launch.Add(time.Second))
	if slots.NeedsManualRenew("codex") {
		t.Fatal("this launch's own activity did not supply admission")
	}
	slots.AdmitCodexActivity("codex", false, launch.Add(2*time.Second))
	if !slots.NeedsManualRenew("codex") {
		t.Fatal("a relaunch retained the previous launch's activity admission")
	}
	appendActivity("codex", launch.Add(3*time.Second))
	if slots.NeedsManualRenew("codex") {
		t.Fatal("activity after the relaunch did not supply admission")
	}
	slots.AdmitCodexActivity("trusted", true, launch)

	var bodies []string
	s := Sender{AMQ: "/opt/amq", Root: "/r/s1", BaseRoot: "/r", Session: "s1", Run: func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		for i := range args {
			if args[i] == "--body" && i+1 < len(args) {
				bodies = append(bodies, args[i+1])
			}
		}
		return nil, nil
	}}
	callRule := "A command that runs longer than the lease (" + task.DefaultLeaseDuration.String() + ") loses it: run long work in the background and poll it."
	manual := `This Codex seat's activity hooks are not trusted, so run amq-squad task renew --me "$AM_ME" after every tool batch and at least every 5 minutes while you hold the task; stop and block if renewal is refused.`
	checks := []struct {
		handle string
		manual bool
	}{
		{"trusted", false},
		{"other", true},
		{"claude", false},
	}
	for _, check := range checks {
		if err := (Doorbell{Sender: s, NeedsManualRenew: slots.NeedsManualRenew}).Ring(check.handle, task.Task{ID: "t1", Title: "work", Class: "implement"}); err != nil {
			t.Fatal(err)
		}
		body := bodies[len(bodies)-1]
		if strings.Count(body, callRule) != 1 || strings.Count(body, manual) != btoi(check.manual) {
			t.Fatalf("%s doorbell = %q", check.handle, body)
		}
		if check.manual && strings.Index(body, callRule) > strings.Index(body, manual) {
			t.Fatalf("call rule followed manual fallback: %q", body)
		}
	}
}

func TestCodexStopDoesNotProveThePostToolHook(t *testing.T) {
	root := t.TempDir()
	launch := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	slots := &Slots{Root: root, Squad: seatlaunch.Squad{Seats: []seatlaunch.Seat{{Handle: "codex", Runtime: "codex"}}}}
	slots.AdmitCodexActivity("codex", false, launch)
	appendRow := func(event string) {
		t.Helper()
		if _, err := ledger.Append(ledger.Path(root, "codex"), func(seq uint64) any {
			return map[string]any{"schema": ledger.LineSchema, "seq": seq, "at": launch.Add(time.Second).Format(time.RFC3339Nano), "handle": "codex", "event": event, "tool": "Bash", "outcome": "ok"}
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendRow("Stop")
	if !slots.NeedsManualRenew("codex") {
		t.Fatal("S1 Stop alone removed the manual fallback")
	}
	var bodies []string
	sender := Sender{AMQ: "/opt/amq", Root: "/r/s1", BaseRoot: "/r", Session: "s1", Run: func(_ context.Context, _ string, args, _ []string) ([]byte, error) {
		for i := range args {
			if args[i] == "--body" && i+1 < len(args) {
				bodies = append(bodies, args[i+1])
			}
		}
		return nil, nil
	}}
	manual := `This Codex seat's activity hooks are not trusted, so run amq-squad task renew --me "$AM_ME" after every tool batch and at least every 5 minutes while you hold the task; stop and block if renewal is refused.`
	if err := (Doorbell{Sender: sender, NeedsManualRenew: slots.NeedsManualRenew}).Ring("codex", task.Task{ID: "t1", Title: "work", Class: "implement"}); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || strings.Count(bodies[0], manual) != 1 {
		t.Fatalf("S1 Stop doorbell = %q", bodies)
	}
	appendRow("PostToolUse")
	if slots.NeedsManualRenew("codex") {
		t.Fatal("S1 PostToolUse did not remove the manual fallback")
	}
	if err := (Doorbell{Sender: sender, NeedsManualRenew: slots.NeedsManualRenew}).Ring("codex", task.Task{ID: "t2", Title: "work", Class: "implement"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(bodies[1], manual) {
		t.Fatalf("S1 PostToolUse doorbell kept fallback = %q", bodies[1])
	}
}

func TestCodexLedgerReadErrorKeepsTheManualFallback(t *testing.T) {
	root := t.TempDir()
	launch := time.Date(2026, 11, 11, 0, 0, 0, 0, time.UTC)
	path := ledger.Path(root, "codex")
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if active, err := ledger.HasActivitySince(root, "codex", launch); err == nil {
		t.Fatalf("read-error fixture returned active=%v, err=nil", active)
	}
	slots := &Slots{Root: root, Squad: seatlaunch.Squad{Seats: []seatlaunch.Seat{{Handle: "codex", Runtime: "codex"}}}}
	slots.AdmitCodexActivity("codex", false, launch)
	if !slots.NeedsManualRenew("codex") {
		t.Fatal("read error removed the manual fallback")
	}
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}
