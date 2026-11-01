package schedproc

import (
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// fakePools is the router's roster pools (routing.Router's setter).
type fakePools struct {
	mu    sync.Mutex
	pools []task.Pool
}

func (f *fakePools) SetRosterPools(p []task.Pool) {
	f.mu.Lock()
	f.pools = slices.Clone(p)
	f.mu.Unlock()
}

func (f *fakePools) RosterPools() []task.Pool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.pools)
}

var opusPool = task.Pool{Runtime: "claude", Model: "opus", Effort: "high"}

func reloadSquad(extra ...seatlaunch.Seat) seatlaunch.Squad {
	q := seatlaunch.Squad{ProjectRoot: "/repo", Session: "s", Seats: []seatlaunch.Seat{
		{Handle: "lead", Runtime: "claude", Model: "opus", Effort: "high"},
		{Handle: "c1", Runtime: "claude", Model: "sonnet", Effort: "medium", Worktree: "/w/c1"},
		{Handle: "x1", Runtime: "codex", Model: "gpt-5.4", Effort: "medium", Worktree: "/w/x1"},
	}}
	q.Seats = append(q.Seats, extra...)
	return q
}

func seatHandles(q seatlaunch.Squad) string {
	var hs []string
	for _, s := range q.Seats {
		hs = append(hs, s.Handle)
	}
	return strings.Join(hs, ",")
}

// reloadRun runs the process in roster mode (c1, x1) with a reload over
// slots and pools, sends one relaunch carrying start once the decision
// socket is open, and returns the reply, the log and the scheduler after
// the process stopped (when until reports done).
func reloadRun(t *testing.T, reload *Reload, start string, until func(*rosterLauncher, []scheduler.Decision) bool) (DecideReply, string, *scheduler.Scheduler, *rosterLauncher) {
	t.Helper()
	return reloadRunAt(t, time.Time{}, reload, start, until)
}

// reloadRunAt is reloadRun for a scheduler whose launch start is launch.
func reloadRunAt(t *testing.T, launch time.Time, reload *Reload, start string, until func(*rosterLauncher, []scheduler.Decision) bool) (DecideReply, string, *scheduler.Scheduler, *rosterLauncher) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	l, log := &rosterLauncher{}, &lockedBuf{}
	lead := panechain.Pane{Shell: 80, Start: "s80", Agent: "lead"}
	var once sync.Once
	replies := make(chan DecideReply, 1)
	var got DecideReply
	err := runUntil(t, Options{Root: root, Launcher: l, Log: log, Start: launch,
		Config: scheduler.Config{MaxSeats: 2, Lead: "lead", Roster: []scheduler.RosterSeat{{Handle: "c1", Pool: sonnet, Worktree: "/w/c1"}, {Handle: "x1", Pool: gptPool, Worktree: "/w/x1"}}},
		Decisions: &DecideOptions{Apply: func(string, WidenRequest) DecideReply { return DecideReply{OK: true} }, Reload: reload,
			LeadPane:   func(string, string) (panechain.Pane, error) { return lead, nil },
			Reaches:    func(_ int, _ string, panes []panechain.Pane) (*panechain.Pane, bool) { return &panes[0], true },
			StartToken: func(int) (string, bool) { return "s", true },
			Peer:       func(*net.UnixConn) (int, bool) { return 4242, true }}},
		l, func(ds []scheduler.Decision) bool {
			if rec, _ := ReadLaunchRecord(root); rec.DecisionSocket != "" {
				once.Do(func() {
					go func() {
						reply, err := SendRelaunch(root, rec.DecisionSocket, RelaunchRequest{Reason: "add n1", Start: start})
						if err != nil {
							reply = DecideReply{Code: err.Error()}
						}
						replies <- reply
					}()
				})
			}
			select {
			case got = <-replies:
				replies <- got
			default:
				return false
			}
			return until(l, ds)
		})
	if err != nil {
		t.Fatal(err)
	}
	return <-replies, log.String(), l.s, l
}

// Brief 31-2 PR 2, changes 1 and 4 (row A and row 4, the process's half):
// a relaunch over the decision socket compiles squad.yaml again; at the
// drain's end the slots' squad, the router's roster pools and the
// scheduler's roster all name the added seat n1, the relaunch launches it,
// and its request carries the key press as its start, while c1 and x1
// carry none; the lease cap is the reload's (3). Reverted (the relaunch
// without the reload), n1 is never launched and nothing changes.
func TestARelaunchReloadsSquadYAMLAndLaunchesTheAddedSeat(t *testing.T) {
	slots := &Slots{Squad: reloadSquad()}
	pools := &fakePools{pools: []task.Pool{sonnet, gptPool}}
	added := reloadSquad(seatlaunch.Seat{Handle: "n1", Runtime: "claude", Model: "opus", Effort: "high", Worktree: "/w/n1"})
	keyPress := time.Now().Add(-3 * time.Second).UTC()                              // in range: at most StartSkew ahead
	capOf := func(q seatlaunch.Squad) (int, error) { return len(q.Seats) - 1, nil } // one lease per roster seat
	reply, text, s, l := reloadRun(t, &Reload{Compile: func() (seatlaunch.Squad, error) { return added, nil }, Cap: capOf, Slots: slots, Pools: pools, Lead: "lead"},
		keyPress.Format(time.RFC3339Nano), func(l *rosterLauncher, ds []scheduler.Decision) bool { return l.launched() == 2 })
	if !reply.OK {
		t.Fatalf("the relaunch: %+v", reply)
	}
	if !strings.Contains(text, `"kind":"roster_reloaded","seat":"n1"`) {
		t.Fatalf("log:\n%s", text)
	}
	if seatHandles(slots.Squad) != "lead,c1,x1,n1" || !slices.Equal(pools.RosterPools(), []task.Pool{sonnet, gptPool, opusPool}) || len(s.Config.Roster) != 3 || s.Config.Roster[2].Handle != "n1" || s.Config.MaxSeats != 3 {
		t.Fatalf("after the swap: slots %s, pools %+v, roster %+v, cap %d", seatHandles(slots.Squad), pools.RosterPools(), s.Config.Roster, s.Config.MaxSeats)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	second := l.launches[1]
	if len(second) != 3 || second[2].Handle != "n1" || !second[2].Start.Equal(keyPress) || !second[0].Start.IsZero() || !second[1].Start.IsZero() {
		t.Fatalf("the relaunch's requests: %+v", second)
	}
}

// Brief 31-2 PR 2, change 2 (row B): a squad.yaml that does not compile
// refuses the relaunch squad_invalid by name before the drain begins: no
// relaunch is requested, no seat closes, and the roster, the slots' squad
// and the router's pools are as they were. A malformed start is refused
// the same way. Reverted (the compile after the request, or its error
// ignored), the relaunch drains.
func TestARelaunchWhoseSquadDoesNotCompileIsRefusedBeforeTheDrain(t *testing.T) {
	for _, tc := range []struct {
		name, start, code string
		compile           func() (seatlaunch.Squad, error)
	}{
		{"squad_invalid", "", RefusedSquadInvalid, func() (seatlaunch.Squad, error) {
			return seatlaunch.Squad{}, errors.New("squad.yaml: seat n1: runtime missing")
		}},
		{"no seat but the lead", "", RefusedSquadInvalid, func() (seatlaunch.Squad, error) {
			return seatlaunch.Squad{Seats: []seatlaunch.Seat{{Handle: "lead", Runtime: "claude"}}}, nil
		}},
		{"malformed start", "yesterday", RefusedRelaunchMalformed, func() (seatlaunch.Squad, error) { return reloadSquad(), nil }},
		{"no cap", "", RefusedSquadInvalid, func() (seatlaunch.Squad, error) {
			return reloadSquad(seatlaunch.Seat{Handle: "nocap", Runtime: "claude", Model: "opus", Effort: "high"}), nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slots := &Slots{Squad: reloadSquad()}
			pools := &fakePools{pools: []task.Pool{sonnet, gptPool}}
			ticks := 0
			capOf := func(q seatlaunch.Squad) (int, error) {
				for _, seat := range q.Seats {
					if seat.Handle == "nocap" {
						return 0, errors.New("the grant admits no seat (max_seats 0)")
					}
				}
				return 3, nil
			}
			reply, text, s, l := reloadRun(t, &Reload{Compile: tc.compile, Cap: capOf, Slots: slots, Pools: pools, Lead: "lead"}, tc.start,
				func(*rosterLauncher, []scheduler.Decision) bool { ticks++; return ticks > 5 })
			if reply.OK || reply.Code != tc.code {
				t.Fatalf("reply %+v", reply)
			}
			if strings.Contains(text, "roster_relaunch_requested") || strings.Contains(text, `"reason":"roster_relaunch"`) || l.launched() != 1 {
				t.Fatalf("the refused relaunch drained: launches %d\n%s", l.launched(), text)
			}
			if seatHandles(slots.Squad) != "lead,c1,x1" || !slices.Equal(pools.RosterPools(), []task.Pool{sonnet, gptPool}) || len(s.Config.Roster) != 2 || s.Config.MaxSeats != 2 {
				t.Fatalf("changed: slots %s, pools %+v, roster %+v, cap %d", seatHandles(slots.Squad), pools.RosterPools(), s.Config.Roster, s.Config.MaxSeats)
			}
		})
	}
}

// Brief 31-2 PR 2, change 2 (row B, the half-applied row): a failure
// injected between the swap's writes (after the slots' squad; after the
// router's pools) is undone: the slots' squad, the router's pools and the
// scheduler's roster are all the old ones, the failure is a decision, and
// the relaunch launches the running roster unchanged. Reverted (no undo,
// or the roster written before the swap), the slots or the pools keep the
// new squad while the roster is the old one.
func TestAHalfAppliedReloadIsNeverObserved(t *testing.T) {
	for _, step := range []string{"pools", "roster"} {
		t.Run(step, func(t *testing.T) {
			prev := reloadFault
			t.Cleanup(func() { reloadFault = prev })
			reloadFault = func(at string) error {
				if at == step {
					return errors.New("injected at " + at)
				}
				return nil
			}
			slots := &Slots{Squad: reloadSquad()}
			pools := &fakePools{pools: []task.Pool{sonnet, gptPool}}
			added := reloadSquad(seatlaunch.Seat{Handle: "n1", Runtime: "claude", Model: "opus", Effort: "high", Worktree: "/w/n1"})
			reply, text, s, l := reloadRun(t, &Reload{Compile: func() (seatlaunch.Squad, error) { return added, nil }, Cap: func(seatlaunch.Squad) (int, error) { return 3, nil }, Slots: slots, Pools: pools, Lead: "lead"}, "",
				func(l *rosterLauncher, _ []scheduler.Decision) bool { return l.launched() == 2 })
			if !reply.OK || !strings.Contains(text, `"kind":"roster_reload_failed"`) || strings.Contains(text, "roster_reloaded") {
				t.Fatalf("reply %+v\n%s", reply, text)
			}
			if seatHandles(slots.Squad) != "lead,c1,x1" || !slices.Equal(pools.RosterPools(), []task.Pool{sonnet, gptPool}) || len(s.Config.Roster) != 2 || s.Config.MaxSeats != 2 {
				t.Fatalf("half applied: slots %s, pools %+v, roster %+v, cap %d", seatHandles(slots.Squad), pools.RosterPools(), s.Config.Roster, s.Config.MaxSeats)
			}
			l.mu.Lock()
			defer l.mu.Unlock()
			if second := l.launches[1]; len(second) != 2 || second[0].Handle != "c1" || second[1].Handle != "x1" {
				t.Fatalf("the relaunch after the failure: %+v", second)
			}
		})
	}
}

// Codex P2 1 on 31-2 PR 2: the relaunch's start is bounded at the socket. A
// stamp more than 5 s in the future, or older than the scheduler's own
// launch start, refuses start_out_of_range by name before any drain: no
// relaunch is requested, nothing closes or launches, and squad, pools and
// roster are as they were; a stamp within the bounds is accepted. Reverted
// (the stamp only parsed), both out-of-range stamps drain.
func TestARelaunchStartOutOfRangeIsRefusedBeforeTheDrain(t *testing.T) {
	launch := time.Now().Add(-time.Hour).UTC()
	for _, tc := range []struct {
		name  string
		start time.Time
		ok    bool
	}{
		{"in the future", time.Now().Add(time.Minute), false},
		{"before the launch start", launch.Add(-time.Second), false},
		{"in range", time.Now().Add(-time.Second), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			slots := &Slots{Squad: reloadSquad()}
			pools := &fakePools{pools: []task.Pool{sonnet, gptPool}}
			added := reloadSquad(seatlaunch.Seat{Handle: "n1", Runtime: "claude", Model: "opus", Effort: "high", Worktree: "/w/n1"})
			ticks := 0
			reply, text, s, l := reloadRunAt(t, launch, &Reload{Compile: func() (seatlaunch.Squad, error) { return added, nil }, Slots: slots, Pools: pools, Lead: "lead"},
				tc.start.Format(time.RFC3339Nano), func(l *rosterLauncher, _ []scheduler.Decision) bool { ticks++; return l.launched() == 2 || ticks > 5 })
			if tc.ok {
				if !reply.OK || l.launched() != 2 {
					t.Fatalf("an in-range start: %+v, launches %d", reply, l.launched())
				}
				return
			}
			if reply.OK || reply.Code != RefusedStartOutOfRange {
				t.Fatalf("reply %+v", reply)
			}
			if strings.Contains(text, "roster_relaunch_requested") || l.launched() != 1 || seatHandles(slots.Squad) != "lead,c1,x1" || len(pools.RosterPools()) != 2 || len(s.Config.Roster) != 2 {
				t.Fatalf("the refused relaunch drained or swapped: launches %d, slots %s\n%s", l.launched(), seatHandles(slots.Squad), text)
			}
		})
	}
}
