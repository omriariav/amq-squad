package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// t16 through the verb: `scheduler run` launches the roster, every seat of
// the seat list with a worktree of its own, in one launch at start; a seat
// whose worktree is the repository root is named and left out. Reverted,
// nothing launches until a task asks for a seat.
func TestSchedulerRunLaunchesTheRosterAtStart(t *testing.T) {
	home, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("HOME", home)
	writeTestRegistry(t, home)
	proj, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(proj, ".agent-mail", "s1")
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(proj, "roles", "impl.md"), "# Role: implementer\n\nYou implement tasks.\n")
	seat := func(handle, runtime, model, wt string) map[string]any {
		return map[string]any{"handle": handle, "runtime": runtime, "model": model, "effort": "medium", "role": "implementer", "role_file": filepath.Join(proj, "roles", "impl.md"), "worktree": wt}
	}
	raw, _ := json.Marshal(map[string]any{"project_root": proj, "session": "s1", "seats": []map[string]any{
		seat("impl-1", "claude", "sonnet", filepath.Join(proj, "wt-impl-1")),
		seat("rev-1", "codex", "gpt-5.4", filepath.Join(proj, "wt-rev-1")),
		seat("root-1", "claude", "opus", proj),
		seat("lead", "claude", "opus", filepath.Join(proj, "wt-lead")),
	}})
	seatsFile := filepath.Join(proj, "seats.json")
	mustWrite(t, seatsFile, string(raw))
	for _, wt := range []string{"wt-impl-1", "wt-rev-1"} {
		if err := os.MkdirAll(filepath.Join(proj, wt), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	fake := &cliFakeLauncher{}
	ticks := make(chan []scheduler.Decision, 1000)
	prev := []any{schedulerAMQLauncher, schedulerSendRun, schedulerStarted, schedulerOnTick}
	t.Cleanup(func() {
		schedulerAMQLauncher = prev[0].(func(*schedproc.Slots, string, seatlaunch.Options) scheduler.SeatLauncher)
		schedulerSendRun = prev[1].(func(context.Context, string, []string, []string) ([]byte, error))
		schedulerStarted = prev[2].(func(*scheduler.Scheduler))
		schedulerOnTick = prev[3].(func([]scheduler.Decision))
	})
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher {
		fake.sl = sl
		return fake
	}
	schedulerSendRun = func(context.Context, string, []string, []string) ([]byte, error) { return []byte("{}"), nil }
	schedulerStarted = func(s *scheduler.Scheduler) { fake.mu.Lock(); fake.s = s; fake.mu.Unlock() }
	schedulerOnTick = func(ds []scheduler.Decision) { ticks <- ds }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var stdout string
	go func() {
		out, _, err := captureOutput(t, func() error {
			return runSchedulerRun(ctx, []string{"--seats", seatsFile, "--lead", "lead", "--tick", "20ms"}, os.Stdout)
		})
		stdout = out
		done <- err
	}()
	waitDecision(t, ticks, "roster_launched")
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.reqs) != 2 || fake.reqs[0].Handle != "impl-1" || fake.reqs[1].Handle != "rev-1" {
		t.Fatalf("the roster launch: %+v", fake.reqs)
	}
	if !strings.Contains(stdout, "not in the roster: seat root-1 has no worktree of its own") || !strings.Contains(stdout, "not in the roster: seat lead is the lead") || !strings.Contains(stdout, "roster of 2 seats") {
		t.Fatalf("stdout:\n%s", stdout)
	}
}

// t16: `scheduler relaunch` is the lead's decision over the scheduler's
// socket: the reason travels, an accepted relaunch prints the scheduler's
// answer, a refused one exits with its code, and a scheduler still starting
// refuses it.
func TestSchedulerRelaunchVerb(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	t.Setenv("AM_ROOT", root)
	var sent []schedproc.RelaunchRequest
	reply := schedproc.DecideReply{OK: true, Detail: "accepted: the roster relaunches"}
	prev := schedulerRelaunchSend
	t.Cleanup(func() { schedulerRelaunchSend = prev })
	schedulerRelaunchSend = func(r, path string, req schedproc.RelaunchRequest) (schedproc.DecideReply, error) {
		if r != root || path != "/sock" {
			t.Errorf("sent to %s %s", r, path)
		}
		sent = append(sent, req)
		return reply, nil
	}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: 1, StartedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runScheduler([]string{"relaunch"}) }); ExitCode(err) != ExitCommitRefused || !strings.Contains(err.Error(), schedproc.RefusedStarting) || len(sent) != 0 {
		t.Fatalf("a starting scheduler: %v", err)
	}
	if err := schedproc.WriteLaunchRecord(root, schedproc.LaunchRecord{Lead: "lead", Root: root, PID: 1, StartedAt: time.Now(), DecisionSocket: "/sock"}); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureOutput(t, func() error { return runScheduler([]string{"relaunch", "--reason", "rev-1 is gone"}) })
	if err != nil || !strings.Contains(out, "accepted: the roster relaunches") || len(sent) != 1 || sent[0].Reason != "rev-1 is gone" {
		t.Fatalf("relaunch: %v %q %+v", err, out, sent)
	}
	reply = schedproc.DecideReply{Code: schedproc.RefusedNotLead, Detail: "not the lead"}
	if _, _, err := captureOutput(t, func() error { return runScheduler([]string{"relaunch"}) }); ExitCode(err) != ExitCommitRefused || !strings.Contains(err.Error(), schedproc.RefusedNotLead) {
		t.Fatalf("a refused relaunch: %v", err)
	}
	if _, _, err := captureOutput(t, func() error { return runScheduler([]string{"relaunch", "--me", "x"}) }); err == nil {
		t.Fatal("--me was accepted")
	}
}

// t16 rule 3: status shows a task blocked for the roster relaunch: its row
// carries the reason and the attention queue names it.
func TestStatusShowsTheRelaunchBlock(t *testing.T) {
	root := t.TempDir()
	chdir(t, root)
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	prevNow, prevAlive := statusNow, statusPIDAlive
	statusNow, statusPIDAlive = func() time.Time { return now }, func(int) bool { return true }
	t.Cleanup(func() { statusNow, statusPIDAlive = prevNow, prevAlive })
	store, _ := task.Open(root)
	pool := task.Pool{Runtime: "codex", Model: "gpt-5.4", Effort: "medium"}
	tk, err := store.Add(task.AddInput{Title: "review the parser", Class: "implement", Pool: &pool}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Block(tk.ID, "", scheduler.ReasonSeatRelaunch, "every roster seat of pool codex:gpt-5.4:medium is gone (rev-1); the lead's decision: "+scheduler.RelaunchVerb, task.RouteLead, now); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ROOT", root)
	text, _, err := captureOutput(t, func() error { return runStatusWithVersion(nil, "3.0.1-test") })
	if err != nil || !strings.Contains(text, "blocked") || !strings.Contains(text, tk.ID+" blocked ("+scheduler.ReasonSeatRelaunch+")") {
		t.Fatalf("status (%v):\n%s", err, text)
	}
}

// refusingRoster is a launcher whose roster launch AMQ refuses.
type refusingRoster struct {
	cliFakeLauncher
	out scheduler.RosterOutcome
}

func (f *refusingRoster) LaunchRoster([]scheduler.SeatRequest) scheduler.RosterOutcome { return f.out }

// t16 ruling 4: without --trust, a roster launch whose Prepare asks for the
// trust confirmation is refused at start, once: scheduler run exits with
// the refusal code (which the lead's supervisor does not restart) and a
// reason that names the flag. A launch error that is not AMQ's refusal
// keeps the crash path.
func TestSchedulerRunRefusesAtStartNamingTrust(t *testing.T) {
	_, _, seatsFile := schedulerFixture(t)
	f := &refusingRoster{out: scheduler.RosterOutcome{Stage: "decisions_pending", ReasonCode: "trust_confirmation", Detail: "answer trust_confirmation"}}
	schedulerAMQLauncher = func(sl *schedproc.Slots, _ string, _ seatlaunch.Options) scheduler.SeatLauncher { f.sl = sl; return f }
	_, _, err := captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--seats", seatsFile, "--lead", "lead", "--tick", "20ms"}, os.Stdout)
	})
	if ExitCode(err) != ExitSchedulerRefused || !strings.Contains(err.Error(), "amq-squad lead --team --scheduler-seats FILE --trust") || !strings.Contains(err.Error(), "trust_confirmation") {
		t.Fatalf("the refusal: %v (exit %d)", err, ExitCode(err))
	}
	f.out = scheduler.RosterOutcome{Stage: "launch_error", Detail: "the probe failed"}
	_, _, err = captureOutput(t, func() error {
		return runSchedulerRun(context.Background(), []string{"--seats", seatsFile, "--lead", "lead", "--tick", "20ms"}, os.Stdout)
	})
	if err == nil || ExitCode(err) == ExitSchedulerRefused {
		t.Fatalf("a launch error took the refusal exit: %v", err)
	}
}

// t16 ruling 1: started by the lead's team mode, the decision socket's lead
// is the scheduler's own parent (the team-mode lead outside the roster's
// tmux session); otherwise the launch ledger's lead, as before.
func TestSchedulerTakesItsTeamModeParentAsTheLead(t *testing.T) {
	me := os.Getpid()
	start, _ := procinfo.StartToken(me)
	prevTeam, prevParent := startedByLeadTeam, schedulerLeadParent
	t.Cleanup(func() { startedByLeadTeam, schedulerLeadParent = prevTeam, prevParent })
	schedulerLeadParent = func() (int, string, bool) { return me, start, true }
	startedByLeadTeam = func() bool { return true }
	d := decisionOptions(storeSelection{}, t.TempDir())
	gone, err := teamModeLead(d)
	if err != nil || d.LeadPane == nil || gone == nil {
		t.Fatalf("no team-mode lead: %v", err)
	}
	if p, err := d.LeadPane("/r", "lead"); err != nil || p.Shell != me || p.Start != start || p.Agent != "lead" {
		t.Fatalf("lead pane %+v %v", p, err)
	}
	if st, _ := gone(); st != schedproc.LeadAlive {
		t.Fatalf("a live lead reported %v", st)
	}
	schedulerLeadParent = func() (int, string, bool) { return me, "", false }
	if _, err := teamModeLead(decisionOptions(storeSelection{}, t.TempDir())); err == nil {
		t.Fatal("a team-mode lead whose stamp cannot be read was accepted")
	}
	startedByLeadTeam = func() bool { return false }
	d = decisionOptions(storeSelection{}, t.TempDir())
	if gone, err := teamModeLead(d); err != nil || gone != nil || d.LeadPane != nil {
		t.Fatal("a scheduler outside team mode took its parent as the lead")
	}
}
