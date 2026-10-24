package schedproc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/panechain"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Tests of the 2b-2 milestone B fix commit 2 (the second pass on 1b39380f).

// decideServer starts the decision socket with the seams: the sender (this
// test process) is reached by the pane reach answers.
func decideServer(t *testing.T, panes []panechain.Pane, reach func([]panechain.Pane) (*panechain.Pane, bool), peerKnown bool) (*DecisionServer, *[]WidenRequest) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	var applied []WidenRequest
	s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead",
		Apply: func(lead string, req WidenRequest) DecideReply {
			applied = append(applied, req)
			return DecideReply{OK: true, Detail: "applied by " + lead}
		},
		Panes:      func() ([]panechain.Pane, error) { return panes, nil },
		Reaches:    func(int, string, []panechain.Pane) (*panechain.Pane, bool) { return reach(panes) },
		StartToken: func(int) (string, bool) { return "s", true },
		Peer:       func(*net.UnixConn) (int, bool) { return 4242, peerKnown },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, &applied
}

// R4 (task_widen.go:154): the widening trusted the AMQ message's sender,
// which AMQ derives from the caller's AM_ME in a same-UID writable mailbox.
// The decision reaches the scheduler over its own socket and is applied only
// when the kernel-named sender's stamped parent chain reaches the lead's
// pane; another pane is not_lead, an unreadable origin is origin_unknown.
func TestDecisionSocketAcceptsOnlyTheLeadsPaneChain(t *testing.T) {
	req := WidenRequest{Kind: WidenRequestKind, Task: "t1", Files: []string{"b.go"}, Reason: "r"}
	leadPane := []panechain.Pane{{Shell: 80, Start: "s80", Agent: "lead"}, {Shell: 70, Start: "s70", Agent: "s1"}}
	cases := map[string]struct {
		panes     []panechain.Pane
		reach     func([]panechain.Pane) (*panechain.Pane, bool)
		peerKnown bool
		want      string
	}{
		"the lead's pane":     {leadPane, func(p []panechain.Pane) (*panechain.Pane, bool) { return &p[0], true }, true, ""},
		"no pane reached":     {leadPane, func([]panechain.Pane) (*panechain.Pane, bool) { return nil, true }, true, RefusedNotLead},
		"an unreadable chain": {leadPane, func([]panechain.Pane) (*panechain.Pane, bool) { return nil, false }, true, RefusedOriginUnknown},
		"no kernel peer":      {leadPane, func(p []panechain.Pane) (*panechain.Pane, bool) { return &p[0], true }, false, RefusedOriginUnknown},
		"no lead pane":        {leadPane[1:], func(p []panechain.Pane) (*panechain.Pane, bool) { return &p[0], true }, true, RefusedOriginUnknown},
		"two lead panes":      {[]panechain.Pane{leadPane[0], leadPane[0]}, func(p []panechain.Pane) (*panechain.Pane, bool) { return &p[0], true }, true, RefusedOriginUnknown},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, applied := decideServer(t, tc.panes, tc.reach, tc.peerKnown)
			reply, err := SendDecision(s.Path(), req)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" && (!reply.OK || len(*applied) != 1) {
				t.Fatalf("the lead's decision: %+v", reply)
			}
			if tc.want != "" && (reply.Code != tc.want || len(*applied) != 0) {
				t.Fatalf("want %s and nothing applied, got %+v (applied %d)", tc.want, reply, len(*applied))
			}
		})
	}
}

// The same check with the kernel's peer pid and the real stamped chain:
// this test process's own parent stands in for the lead's pane shell.
func TestDecisionSocketWithTheRealPeerAndChain(t *testing.T) {
	ppid := os.Getppid()
	start, ok := procinfo.StartToken(ppid)
	if !ok {
		t.Skip("no start stamps on this host")
	}
	for _, tc := range []struct {
		pane panechain.Pane
		want string
	}{
		{panechain.Pane{Shell: ppid, Start: start, Agent: "lead"}, ""},
		{panechain.Pane{Shell: 999999, Start: "x", Agent: "lead"}, RefusedNotLead},
	} {
		root, _ := filepath.EvalSymlinks(t.TempDir())
		applied := 0
		s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead",
			Apply: func(string, WidenRequest) DecideReply { applied++; return DecideReply{OK: true} },
			Panes: func() ([]panechain.Pane, error) { return []panechain.Pane{tc.pane}, nil }})
		if err != nil {
			t.Fatal(err)
		}
		reply, err := SendDecision(s.Path(), WidenRequest{Kind: WidenRequestKind, Task: "t1"})
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if (tc.want == "" && (!reply.OK || applied != 1)) || (tc.want != "" && (reply.Code != tc.want || applied != 0)) {
			t.Fatalf("pane %d: %+v (applied %d)", tc.pane.Shell, reply, applied)
		}
	}
}

// P1-6 (slots.go:183): the slot was released at the close while the
// scheduler waited for the external seat's seat_gone, so a replacement
// launched under the same handle and worktree before the old provider
// stopped. An unconfirmed close keeps the slot; the seat_gone frees it.
func TestExternalSlotIsReleasedOnlyAtSeatGone(t *testing.T) {
	q := squadFixture(t)
	slots := &Slots{Squad: q}
	pi := task.Pool{Runtime: "pi", Model: "", Effort: ""}
	l := &Launcher{Slots: slots, ByRuntime: map[string]scheduler.SeatLauncher{"pi": External{Slots: slots, Root: q.SessionRoot()}}}
	if _, err := slots.Plan(scheduler.SeatRequest{ID: "r1", Pool: pi}); err != nil {
		t.Fatal(err)
	}
	if err := l.CloseSeat("p1", "lease_expired"); err != nil {
		t.Fatal(err)
	}
	if _, err := slots.Plan(scheduler.SeatRequest{ID: "r2", Pool: pi}); err == nil {
		t.Fatal("a replacement took the slot before the old seat's seat_gone")
	}
	l.SeatGone("p1")
	if got, err := slots.Plan(scheduler.SeatRequest{ID: "r3", Pool: pi}); err != nil || got.Seats[0].Handle != "p1" {
		t.Fatalf("after seat_gone: %+v %v", got.Seats, err)
	}
}

// N2 (scheduler.go:644 after a restart): the empty seat map of a restarted
// process skipped the close of a seat still running from the previous
// incarnation. The map is rebuilt from the launch ledger before any
// dispatch: such a seat is counted, closed through the launcher, and never
// dispatched to.
func TestRestartAdoptsALiveSeatAndClosesItBeforeAnyDispatch(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	prev := startTokenOf
	startTokenOf = func(pid int) (string, bool) { return "live", pid == 4242 }
	t.Cleanup(func() { startTokenOf = prev })
	sonnetPool := task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	if err := os.MkdirAll(launch.StagedDir(root, "c1"), 0o700); err != nil {
		t.Fatal(err)
	}
	launch.WriteFile(filepath.Join(launch.StagedDir(root, "c1"), launch.FileName), launch.Record{Handle: "c1", Root: root, Session: "s", Runtime: "claude", Model: "sonnet", Effort: "medium", Worktree: "/w", Branch: "seat/c1"})
	if err := launch.WriteProof(root, launch.ProofEntry{Handle: "c1", PID: 4242, Start: "live"}); err != nil {
		t.Fatal(err)
	}
	if err := launch.WriteProof(root, launch.ProofEntry{Handle: "c2", PID: 5555, Start: "gone"}); err != nil {
		t.Fatal(err)
	}
	if got := LiveSeats(root); len(got) != 1 || got[0].Handle != "c1" || got[0].Pool != sonnetPool {
		t.Fatalf("live seats: %+v", got)
	}
	store, _ := task.Open(root)
	store.Add(task.AddInput{Title: "work", Class: "implement", Pool: &sonnetPool}, time.Now())
	l := &fakeLauncher{}
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan []scheduler.Decision, 100)
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{Root: root, Launcher: l, Log: &lockedBuf{}, Tick: 10 * time.Millisecond,
			Started: func(s *scheduler.Scheduler) { l.mu.Lock(); l.s = s; l.mu.Unlock() },
			OnTick:  func(ds []scheduler.Decision) { ticks <- ds }})
	}()
	first := <-ticks
	cancel()
	<-done
	for _, d := range first {
		if d.Kind == "dispatched" && d.Seat == "c1" {
			t.Fatalf("the adopted seat got a lease: %+v", first)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.closed) == 0 || l.closed[0] != "c1" {
		t.Fatalf("the live seat of the previous incarnation was not closed: %v", l.closed)
	}
}
