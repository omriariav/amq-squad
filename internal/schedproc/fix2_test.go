package schedproc

import (
	"context"
	"errors"
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

// decideServer starts the decision socket with the seams: the lead's
// recorded pane is leadPane (an error when nil), and the sender (this test
// process) is reached by the reach answer.
func decideServer(t *testing.T, leadPane *panechain.Pane, reach func(panechain.Pane) (*panechain.Pane, bool), peerKnown bool) (*DecisionServer, *[]WidenRequest) {
	t.Helper()
	root, _ := filepath.EvalSymlinks(t.TempDir())
	var applied []WidenRequest
	s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead",
		Apply: func(lead string, req WidenRequest) DecideReply {
			applied = append(applied, req)
			return DecideReply{OK: true, Detail: "applied by " + lead}
		},
		LeadPane: func(string, string) (panechain.Pane, error) {
			if leadPane == nil {
				return panechain.Pane{}, errors.New("no launch ledger entry for the lead")
			}
			return *leadPane, nil
		},
		Reaches:    func(_ int, _ string, panes []panechain.Pane) (*panechain.Pane, bool) { return reach(panes[0]) },
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
// recorded pane shell; anything else is not_lead, an unreadable origin
// origin_unknown.
func TestDecisionSocketAcceptsOnlyTheLeadsPaneChain(t *testing.T) {
	req := WidenRequest{Kind: WidenRequestKind, Task: "t1", Files: []string{"b.go"}, Reason: "r"}
	lead := &panechain.Pane{Shell: 80, Start: "s80", Agent: "lead"}
	hit := func(p panechain.Pane) (*panechain.Pane, bool) { return &p, true }
	cases := map[string]struct {
		lead      *panechain.Pane
		reach     func(panechain.Pane) (*panechain.Pane, bool)
		peerKnown bool
		want      string
	}{
		"the lead's pane":       {lead, hit, true, ""},
		"no pane reached":       {lead, func(panechain.Pane) (*panechain.Pane, bool) { return nil, true }, true, RefusedNotLead},
		"an unreadable chain":   {lead, func(panechain.Pane) (*panechain.Pane, bool) { return nil, false }, true, RefusedOriginUnknown},
		"no kernel peer":        {lead, hit, false, RefusedOriginUnknown},
		"no recorded lead pane": {nil, hit, true, RefusedOriginUnknown},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s, applied := decideServer(t, tc.lead, tc.reach, tc.peerKnown)
			reply, err := SendDecision(s.Root(), s.Path(), req)
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

// R8 (decide.go:150): the lead's pane was resolved from the mutable
// @amq_pane_agent label, so a seat that relabelled its pane became the lead.
// The pane is the one the launch ledger recorded at the lead's proof (its
// shell pid and start stamp); no label is read: with no tmux at all on PATH
// the recorded lead is still proved, a recorded shell that is gone or reused
// is origin_unknown, and a sender outside it is not_lead. The real kernel
// peer and the real stamped chain; this process's parent stands in for the
// lead's pane shell.
func TestDecisionSocketProvesTheLeadAgainstTheLedger(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no tmux: a label could not even be read
	ppid := os.Getppid()
	start, ok := procinfo.StartToken(ppid)
	self, ok2 := procinfo.StartToken(os.Getpid())
	if !ok || !ok2 {
		t.Skip("no start stamps on this host")
	}
	for _, tc := range []struct {
		entry launch.ProofEntry
		want  string
	}{
		{launch.ProofEntry{Handle: "lead", PID: os.Getpid(), Start: self, PaneShell: ppid, PaneShellStart: start}, ""},
		{launch.ProofEntry{Handle: "lead", PID: os.Getpid(), Start: self, PaneShell: ppid, PaneShellStart: "reused"}, RefusedOriginUnknown},
		{launch.ProofEntry{Handle: "lead", PID: os.Getpid(), Start: self}, RefusedOriginUnknown},
	} {
		root, _ := filepath.EvalSymlinks(t.TempDir())
		if err := launch.WriteProof(root, tc.entry); err != nil {
			t.Fatal(err)
		}
		applied := 0
		s, err := ServeDecisions(DecideOptions{Root: root, Lead: "lead",
			Apply: func(string, WidenRequest) DecideReply { applied++; return DecideReply{OK: true} }})
		if err != nil {
			t.Fatal(err)
		}
		reply, err := SendDecision(s.Root(), s.Path(), WidenRequest{Kind: WidenRequestKind, Task: "t1"})
		s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if (tc.want == "" && (!reply.OK || applied != 1)) || (tc.want != "" && (reply.Code != tc.want || applied != 0)) {
			t.Fatalf("entry %+v: %+v (applied %d)", tc.entry, reply, applied)
		}
	}
}

// P1-6 (slots.go:183): the slot was released at the close while the
// scheduler waited for the unconfirmed seat's seat_gone, so a replacement
// launched under the same handle and worktree before the old provider
// stopped. An unconfirmed close keeps the slot; the seat_gone frees it.
func TestUnconfirmedCloseSlotIsReleasedOnlyAtSeatGone(t *testing.T) {
	q := squadFixture(t)
	slots := &Slots{Squad: q}
	pi := task.Pool{Runtime: "pi", Model: "", Effort: ""}
	l := &Launcher{Slots: slots, ByRuntime: map[string]scheduler.SeatLauncher{"pi": asyncFake{&fakeLauncher{}}}}
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

// Third pass (decide.go:112): the socket accepted unlimited connections, a
// goroutine each before authentication, held 30 s. At most 8 are open at
// once (an excess one is refused and counted), the sender is proved before
// any byte is read, and a sender that sends nothing is cut off at the read
// deadline.
func TestDecisionSocketLimitsConnections(t *testing.T) {
	prev := decisionReadTimeout
	decisionReadTimeout = 500 * time.Millisecond
	t.Cleanup(func() { decisionReadTimeout = prev })
	lead := &panechain.Pane{Shell: 80, Start: "s80", Agent: "lead"}
	s, applied := decideServer(t, lead, func(p panechain.Pane) (*panechain.Pane, bool) { return &p, true }, true)
	var idle []net.Conn
	for i := 0; i < MaxDecisionConns; i++ {
		c, err := net.Dial("unix", s.Path())
		if err != nil {
			t.Fatal(err)
		}
		idle = append(idle, c)
	}
	time.Sleep(100 * time.Millisecond)
	reply, err := SendDecision(s.Root(), s.Path(), WidenRequest{Kind: WidenRequestKind, Task: "t1"})
	if err != nil || reply.Code != RefusedBusy || s.Overflow() != 1 || len(*applied) != 0 {
		t.Fatalf("the ninth connection: %+v %v (overflow %d)", reply, err, s.Overflow())
	}
	// The idle senders are cut off at the read deadline; the socket admits again.
	time.Sleep(decisionReadTimeout + 300*time.Millisecond)
	for _, c := range idle {
		c.Close()
	}
	time.Sleep(100 * time.Millisecond)
	if reply, err := SendDecision(s.Root(), s.Path(), WidenRequest{Kind: WidenRequestKind, Task: "t1"}); err != nil || !reply.OK {
		t.Fatalf("after the deadline: %+v %v", reply, err)
	}
}
