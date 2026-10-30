package schedproc

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

var gptPool = task.Pool{Runtime: "codex", Model: "gpt-5.4", Effort: "medium"}

// rosterLauncher is the process tests' launcher of a roster: each seat
// joins with its request id, or the set fail outcome is returned.
type rosterLauncher struct {
	fakeLauncher
	launches [][]scheduler.SeatRequest
	fail     scheduler.RosterOutcome
}

func (f *rosterLauncher) LaunchRoster(reqs []scheduler.SeatRequest) scheduler.RosterOutcome {
	f.mu.Lock()
	f.launches = append(f.launches, reqs)
	s, fail := f.s, f.fail
	f.mu.Unlock()
	if fail.Stage != "" {
		return fail
	}
	out := scheduler.RosterOutcome{Stage: scheduler.RosterApplied}
	for _, r := range reqs {
		s.Post(scheduler.Event{Kind: scheduler.SeatUp, At: time.Now(), Handle: r.Handle, Pool: r.Pool, RequestID: r.ID})
		out.Seats = append(out.Seats, scheduler.RosterSeatState{Handle: r.Handle, Up: true})
	}
	return out
}

func (f *rosterLauncher) launched() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.launches)
}

// liveSeat stages a launch record and an accepted proof for a seat whose
// process the startTokenOf seam reports alive.
func liveSeat(t *testing.T, root, handle string, pid int, p task.Pool) {
	t.Helper()
	if err := os.MkdirAll(launch.StagedDir(root, handle), 0o700); err != nil {
		t.Fatal(err)
	}
	launch.WriteFile(filepath.Join(launch.StagedDir(root, handle), launch.FileName), launch.Record{Handle: handle, Root: root, Session: "s", Runtime: p.Runtime, Model: p.Model, Effort: p.Effort, Worktree: "/w/" + handle, Branch: "seat/" + handle})
	if err := launch.WriteProof(root, launch.ProofEntry{Handle: handle, PID: pid, Start: "live"}); err != nil {
		t.Fatal(err)
	}
}

func seamLive(t *testing.T, pids ...int) {
	prev := startTokenOf
	startTokenOf = func(pid int) (string, bool) {
		for _, p := range pids {
			if p == pid {
				return "live", true
			}
		}
		return "", false
	}
	t.Cleanup(func() { startTokenOf = prev })
}

// runUntil runs the process until until() reports done on a tick's
// decisions (or 10 s pass), then stops it and returns its error.
func runUntil(t *testing.T, o Options, l *rosterLauncher, until func([]scheduler.Decision) bool) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ticks := make(chan []scheduler.Decision, 1000)
	o.Tick = 10 * time.Millisecond
	o.Started = func(s *scheduler.Scheduler) { l.mu.Lock(); l.s = s; l.mu.Unlock() }
	o.OnTick = func(ds []scheduler.Decision) { ticks <- ds }
	done := make(chan error, 1)
	go func() { done <- Run(ctx, o) }()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case err := <-done:
			return err
		case ds := <-ticks:
			if until(ds) {
				cancel()
				return <-done
			}
		case <-deadline:
			cancel()
			<-done
			t.Fatal("timed out")
		}
	}
}

func decided(ds []scheduler.Decision, kind string) bool {
	for _, d := range ds {
		if d.Kind == kind {
			return true
		}
	}
	return false
}

// t16 row 4: a restart with two live roster seats adopts both as up: no
// seat_closed scheduler_restart, no close, no launch; the adopted seat gets
// the pending task. Reverted, both seats are closed before any dispatch and
// the squad has no seat AMQ will let it start.
func TestRestartAdoptsTheLiveRosterSeats(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	seamLive(t, 4242, 4343)
	liveSeat(t, root, "c1", 4242, sonnet)
	liveSeat(t, root, "x1", 4343, gptPool)
	store, _ := task.Open(root)
	tk, _ := store.Add(task.AddInput{Title: "work", Class: "implement", Pool: &sonnet}, time.Now())
	l, log := &rosterLauncher{}, &lockedBuf{}
	err := runUntil(t, Options{Root: root, Launcher: l, Log: log,
		Config: scheduler.Config{MaxSeats: 2, Roster: []scheduler.RosterSeat{{Handle: "c1", Pool: sonnet}, {Handle: "x1", Pool: gptPool}}}},
		l, func(ds []scheduler.Decision) bool { return decided(ds, "dispatched") })
	if err != nil {
		t.Fatal(err)
	}
	text := log.String()
	for _, want := range []string{`"kind":"seat_adopted","seat":"c1"`, `"kind":"seat_adopted","seat":"x1"`, `"kind":"roster_adopted"`, `"kind":"dispatched","task_id":"` + tk.ID + `","seat":"c1"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the log lacks %s:\n%s", want, text)
		}
	}
	for _, not := range []string{"seat_adopted_closing", "scheduler_restart", `"kind":"seat_closed"`} {
		if strings.Contains(text, not) {
			t.Fatalf("the log has %s:\n%s", not, text)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.closed) != 0 || len(l.launches) != 0 {
		t.Fatalf("closed %v, launches %d", l.closed, len(l.launches))
	}
}

// t16 rule 2: a live seat outside the roster, or whose launch record names
// another pool than the roster's, fails the adoption and is closed with the
// reason; with no roster seat live the roster launches.
func TestRestartClosesALiveSeatTheRosterDoesNotProve(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	seamLive(t, 4242, 4343)
	liveSeat(t, root, "c1", 4242, sonnet)
	liveSeat(t, root, "x1", 4343, task.Pool{Runtime: "codex", Model: "gpt-old", Effort: "medium"})
	l, log := &rosterLauncher{}, &lockedBuf{}
	err := runUntil(t, Options{Root: root, Launcher: l, Log: log,
		Config: scheduler.Config{MaxSeats: 2, Roster: []scheduler.RosterSeat{{Handle: "x1", Pool: gptPool}}}},
		l, func(ds []scheduler.Decision) bool { return decided(ds, "roster_launched") })
	if err != nil {
		t.Fatal(err)
	}
	text := log.String()
	if !strings.Contains(text, `"seat":"c1","reason":"alive from a previous incarnation; not a seat of the roster"`) ||
		!strings.Contains(text, `launch record's pool codex:gpt-old:medium is not the roster's codex:gpt-5.4:medium`) || strings.Contains(text, `"kind":"seat_adopted",`) {
		t.Fatalf("log:\n%s", text)
	}
	if l.launched() != 1 {
		t.Fatalf("launches %d", l.launched())
	}
}

// t16 row 6, the process: a roster launch that fails at apply is one
// decision with its stage, AMQ's reason and the detail, and the process
// ends with it (scheduler run prints it). Reverted, the failure stayed in
// the launcher and the process ticked on with no seat.
func TestFailedRosterLaunchEndsTheProcess(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	l, log := &rosterLauncher{fail: scheduler.RosterOutcome{Stage: "apply_failed", ReasonCode: scheduler.AMQBindingPresent, Detail: "the session's tmux binding is present"}}, &lockedBuf{}
	err := runUntil(t, Options{Root: root, Launcher: l, Log: log,
		Config: scheduler.Config{MaxSeats: 1, Roster: []scheduler.RosterSeat{{Handle: "c1", Pool: sonnet}}}},
		l, func([]scheduler.Decision) bool { return false })
	var failed *scheduler.RosterLaunchError
	if !errors.As(err, &failed) || !strings.Contains(err.Error(), scheduler.AMQBindingPresent) || !strings.Contains(err.Error(), "apply_failed") {
		t.Fatalf("the process error: %v", err)
	}
	text := log.String()
	if !strings.Contains(text, `"kind":"roster_launch_failed"`) || !strings.Contains(text, "the session's tmux binding is present") || !strings.Contains(text, `"kind":"scheduler_stopped"`) {
		t.Fatalf("log:\n%s", text)
	}
}

// t16 rule 3: the lead's relaunch reaches the scheduler over its decision
// socket, proved like a widening; the scheduler closes the parked seats and
// launches the roster again. A socket with no roster refuses it.
func TestLeadsRelaunchReachesTheSchedulerOverItsSocket(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	l, log := &rosterLauncher{}, &lockedBuf{}
	lead := panechain.Pane{Shell: 80, Start: "s80", Agent: "lead"}
	var once sync.Once
	replies := make(chan DecideReply, 1)
	err := runUntil(t, Options{Root: root, Launcher: l, Log: log,
		Config: scheduler.Config{MaxSeats: 2, Lead: "lead", Roster: []scheduler.RosterSeat{{Handle: "c1", Pool: sonnet}, {Handle: "x1", Pool: gptPool}}},
		Decisions: &DecideOptions{Apply: func(string, WidenRequest) DecideReply { return DecideReply{OK: true} },
			LeadPane:   func(string, string) (panechain.Pane, error) { return lead, nil },
			Reaches:    func(_ int, _ string, panes []panechain.Pane) (*panechain.Pane, bool) { return &panes[0], true },
			StartToken: func(int) (string, bool) { return "s", true },
			Peer:       func(*net.UnixConn) (int, bool) { return 4242, true }}},
		l, func(ds []scheduler.Decision) bool {
			if rec, _ := ReadLaunchRecord(root); rec.DecisionSocket != "" {
				once.Do(func() {
					go func() {
						reply, err := SendRelaunch(root, rec.DecisionSocket, RelaunchRequest{Reason: "c1 is gone"})
						if err != nil {
							reply = DecideReply{Code: err.Error()}
						}
						replies <- reply
					}()
				})
			}
			return l.launched() == 2
		})
	if err != nil {
		t.Fatal(err)
	}
	if reply := <-replies; !reply.OK {
		t.Fatalf("the relaunch reply: %+v", reply)
	}
	text := log.String()
	if !strings.Contains(text, `"kind":"roster_relaunch_requested","reason":"c1 is gone"`) || strings.Count(text, `"reason":"roster_relaunch"`) != 2 || strings.Count(text, `"kind":"roster_launched"`) != 2 {
		t.Fatalf("log:\n%s", text)
	}
	s, _ := decideServer(t, &lead, func(p panechain.Pane) (*panechain.Pane, bool) { return &p, true }, true)
	if reply, err := SendRelaunch(s.Root(), s.Path(), RelaunchRequest{}); err != nil || reply.Code != RefusedRelaunchUnsupported {
		t.Fatalf("a relaunch to a scheduler with no roster: %+v %v", reply, err)
	}
}

// t16 rule 1: the roster is every seat of the seat list with a worktree of
// its own (a repository-root seat is named and left out); the roster plan
// takes each request's seat by handle, creates its worktree and carries
// its request id; a handle outside the seat list or a pool the seat does not
// serve is refused.
func TestSlotsRosterAndItsPlan(t *testing.T) {
	q := squadFixture(t)
	s := &Slots{Squad: q}
	roster, excluded := s.Roster("")
	if len(roster) != 2 || roster[0].Handle != "c1" || roster[1].Handle != "p1" || roster[0].Pool != sonnet || len(excluded) != 1 || !strings.Contains(excluded[0], "seat root has no worktree of its own") {
		t.Fatalf("roster %+v excluded %v", roster, excluded)
	}
	got, err := s.PlanRoster([]scheduler.SeatRequest{{ID: "r1", Handle: "c1", Pool: roster[0].Pool}, {ID: "r2", Handle: "p1", Pool: roster[1].Pool}})
	if err != nil || len(got.Seats) != 2 || got.Seats[0].RequestID != "r1" || got.Seats[1].RequestID != "r2" || got.Seats[1].Handle != "p1" {
		t.Fatalf("plan %+v %v", got.Seats, err)
	}
	if b := gitRun(t, got.Seats[0].Worktree, "symbolic-ref", "HEAD"); b != "refs/heads/seat/c1" {
		t.Fatalf("worktree branch %s", b)
	}
	if _, err := s.PlanRoster([]scheduler.SeatRequest{{ID: "r3", Handle: "nobody", Pool: sonnet}}); err == nil {
		t.Fatal("a handle outside the seat list was planned")
	}
	if _, err := s.PlanRoster([]scheduler.SeatRequest{{ID: "r4", Handle: "c1", Pool: gptPool}}); err == nil {
		t.Fatal("a seat was planned for a pool it does not serve")
	}
}

// t16: the roster goes to one launcher for one apply; runtimes that go to
// different launchers are refused.
func TestLauncherSendsTheRosterToOneLauncher(t *testing.T) {
	a, b := &rosterLauncher{}, &rosterLauncher{}
	reqs := []scheduler.SeatRequest{{ID: "r1", Handle: "c1", Pool: sonnet}, {ID: "r2", Handle: "x1", Pool: gptPool}}
	split := &Launcher{Slots: &Slots{}, ByRuntime: map[string]scheduler.SeatLauncher{"claude": a, "codex": b}}
	if out := split.LaunchRoster(reqs); out.Stage != "launch_error" || !strings.Contains(out.Detail, "different launchers") || a.launched()+b.launched() != 0 {
		t.Fatalf("split roster: %+v", out)
	}
	a.fail = scheduler.RosterOutcome{Stage: "prepare_refused", ReasonCode: "x"}
	one := &Launcher{Slots: &Slots{}, ByRuntime: map[string]scheduler.SeatLauncher{"claude": a, "codex": a}}
	if out := one.LaunchRoster(reqs); out.Stage != "prepare_refused" || a.launched() != 1 || len(a.launches[0]) != 2 {
		t.Fatalf("one launcher: %+v", out)
	}
}

// t16 ruling 1: a scheduler started by the lead's team mode takes its own
// parent (the team-mode lead process, outside the roster's tmux session) as
// the lead: a decision from that process or its descendants is proved over
// the real socket, with the real kernel peer and parent chain; a parent
// that is gone or reused is origin_unknown. Reverted, a lead with no proof
// ledger entry (one outside the roster's binding) could send no decision.
func TestSupervisingLeadProvesTheTeamModeLead(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	me := os.Getpid()
	start, ok := procinfo.StartToken(me)
	if !ok {
		t.Skip("no start stamp for this process")
	}
	var relaunched int
	serve := func(lead func(root, lead string) (panechain.Pane, error)) *DecisionServer {
		s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead", LeadPane: lead,
			Apply:    func(string, WidenRequest) DecideReply { return DecideReply{OK: true} },
			Relaunch: func(string, RelaunchRequest) DecideReply { relaunched++; return DecideReply{OK: true} }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(s.Close)
		return s
	}
	s := serve(SupervisingLead(me, start, procinfo.StartToken))
	if reply, err := SendRelaunch(s.Root(), s.Path(), RelaunchRequest{Reason: "x"}); err != nil || !reply.OK || relaunched != 1 {
		t.Fatalf("the team-mode lead: %+v %v", reply, err)
	}
	s.Close()
	s = serve(SupervisingLead(me, start+"-reused", procinfo.StartToken))
	if reply, err := SendRelaunch(s.Root(), s.Path(), RelaunchRequest{}); err != nil || reply.Code != RefusedOriginUnknown || relaunched != 1 {
		t.Fatalf("a reused parent: %+v %v", reply, err)
	}
}
