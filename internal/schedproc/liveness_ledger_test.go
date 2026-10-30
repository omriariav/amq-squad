package schedproc

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

func goneOf(ds []scheduler.Decision) []scheduler.Decision {
	var out []scheduler.Decision
	for _, d := range ds {
		if d.Kind == "seat_gone" {
			out = append(out, d)
		}
	}
	return out
}

// t17 row 2: the scheduler's tick checks each live roster seat against the
// launch ledger's accepted proof, with real processes and the real start
// stamp: a seat whose recorded pid is gone is posted gone within one tick
// of its death; a pid that is alive but carries another start stamp (the
// pid reused by another process) is gone, not alive; a live seat is
// untouched. Each proof is read once per seat request (the cost is one
// start-stamp read per seat per tick). Reverted (no Liveness wired, or a
// bare signal 0), the dead seat stays parked and the reused pid counts as
// alive.
func TestLivenessFromTheLaunchLedgerSeesAGoneSeatWithinOneTick(t *testing.T) {
	root := t.TempDir()
	child := exec.Command("sleep", "30")
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { child.Process.Kill(); child.Wait() })
	stamp := func(pid int) string {
		s, ok := procinfo.StartToken(pid)
		if !ok {
			t.Fatalf("no start stamp for pid %d", pid)
		}
		return s
	}
	proofs := []launch.ProofEntry{
		{Handle: "dead", LaunchID: "L1", RequestID: "req-dead", PID: child.Process.Pid, Start: stamp(child.Process.Pid)},
		{Handle: "live", LaunchID: "L1", RequestID: "req-live", PID: os.Getpid(), Start: stamp(os.Getpid())},
		// This test's own pid with another stamp: a reused pid.
		{Handle: "reused", LaunchID: "L1", RequestID: "req-reused", PID: os.Getpid(), Start: stamp(os.Getpid()) + "0"},
	}
	for _, e := range proofs {
		if err := launch.WriteProof(root, e); err != nil {
			t.Fatal(err)
		}
	}
	reads := map[string]int{}
	prev := readProofOf
	readProofOf = func(root, handle string) (launch.ProofEntry, error) { reads[handle]++; return prev(root, handle) }
	t.Cleanup(func() { readProofOf = prev })
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pool := task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}
	var roster []scheduler.RosterSeat
	for _, e := range proofs {
		roster = append(roster, scheduler.RosterSeat{Handle: e.Handle, Pool: pool})
	}
	s, err := scheduler.New(scheduler.Options{Store: store, Launcher: &rosterLauncher{}, Liveness: &Liveness{Root: root},
		Config: scheduler.Config{MaxSeats: 3, IdleTimeout: time.Hour, Roster: roster}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range proofs {
		s.AdoptUpRequest(e.Handle, e.RequestID, pool, time.Now())
	}
	ds, err := s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if g := goneOf(ds); len(g) != 1 || g[0].Seat != "reused" || g[0].ReasonCode != scheduler.GoneProcessExited || !strings.Contains(g[0].Reason, "another start stamp") {
		t.Fatalf("first tick: %+v", ds)
	}
	child.Process.Kill()
	child.Wait()
	ds, err = s.Tick()
	if err != nil {
		t.Fatal(err)
	}
	if g := goneOf(ds); len(g) != 1 || g[0].Seat != "dead" || g[0].ReasonCode != scheduler.GoneProcessExited || !strings.Contains(g[0].Reason, "is gone") {
		t.Fatalf("the tick after the death: %+v", ds)
	}
	for i := 0; i < 3; i++ {
		if ds, err = s.Tick(); err != nil || len(goneOf(ds)) != 0 {
			t.Fatalf("the live seat: %+v %v", ds, err)
		}
	}
	if reads["live"] != 1 {
		t.Fatalf("the live seat's proof was read %d times over five ticks, want once", reads["live"])
	}
}

// t17: what is not known is not gone: a pid alive whose stamp cannot be
// read, a proof of another request, a proof that cannot be read; each is
// an error (the scheduler posts nothing). A seat known by no request id is
// read each time.
func TestLivenessUnknownIsNotGone(t *testing.T) {
	root := t.TempDir()
	if err := launch.WriteProof(root, launch.ProofEntry{Handle: "a", RequestID: "req-1", PID: 4242, Start: "s1"}); err != nil {
		t.Fatal(err)
	}
	prevTok, prevAlive := startTokenOf, aliveOf
	t.Cleanup(func() { startTokenOf, aliveOf = prevTok, prevAlive })
	startTokenOf = func(int) (string, bool) { return "", false }
	aliveOf = func(int) bool { return true }
	l := &Liveness{Root: root}
	if gone, _, err := l.SeatProcess("a", "req-1"); gone || err == nil || !strings.Contains(err.Error(), "start stamp cannot be read") {
		t.Fatalf("unreadable stamp: gone %v err %v", gone, err)
	}
	if gone, _, err := l.SeatProcess("a", "req-2"); gone || err == nil || !strings.Contains(err.Error(), "not the seat's req-2") {
		t.Fatalf("another request's proof: gone %v err %v", gone, err)
	}
	if gone, _, err := l.SeatProcess("b", ""); gone || err == nil || !strings.Contains(err.Error(), "cannot be read") {
		t.Fatalf("no proof: gone %v err %v", gone, err)
	}
	aliveOf = func(int) bool { return false }
	if gone, detail, err := l.SeatProcess("a", ""); !gone || err != nil || !strings.Contains(detail, "pid 4242") {
		t.Fatalf("gone: %v %q %v", gone, detail, err)
	}
}

// askedLiveness records which request each seat was checked by.
type askedLiveness struct{ asked []string }

func (a *askedLiveness) SeatProcess(handle, requestID string) (bool, string, error) {
	a.asked = append(a.asked, handle+"@"+requestID)
	return false, "", nil
}

// t17: a seat adopted at a restart is checked by the request its accepted
// proof carries, so its identity is read once, not every tick. Reverted
// (adopted with no request id), the check asks with "".
func TestAdoptedRosterSeatIsCheckedByItsProofsRequest(t *testing.T) {
	root := t.TempDir()
	seamLive(t, 4242)
	liveSeat(t, root, "a", 4242, gptPool)
	store, err := task.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lv := &askedLiveness{}
	roster := []scheduler.RosterSeat{{Handle: "a", Pool: gptPool, Worktree: "/w/a"}}
	s, err := scheduler.New(scheduler.Options{Store: store, Launcher: &rosterLauncher{}, Liveness: lv, Config: scheduler.Config{MaxSeats: 1, Roster: roster}})
	if err != nil {
		t.Fatal(err)
	}
	adoptLive(s, root, roster, time.Now(), func(scheduler.Decision) {})
	if _, err := s.Tick(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lv.asked, ",") != "a@req-L1-a" {
		t.Fatalf("asked %v", lv.asked)
	}
}
