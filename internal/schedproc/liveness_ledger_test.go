package schedproc

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
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

// Codex finding 2 on t17, the process: Run restores the previous
// incarnation's launch failures and blocks from its state file before the
// first tick, so a task blocked on a repeated failure is reset by the
// roster launch at start once its seat comes up. Reverted (no restore), the
// task stays blocked.
func TestRunRestoresTheLaunchFailuresOfThePreviousIncarnation(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	store, _ := task.Open(root)
	tk, _ := store.Add(task.AddInput{Title: "work", Class: "implement", Pool: &gptPool}, time.Now())
	if _, err := store.Block(tk.ID, "", "no_proof", "the launch of seat x1 failed 2 times in a row", task.RouteLead, time.Now()); err != nil {
		t.Fatal(err)
	}
	prev := scheduler.State{UpdatedAt: time.Now(), Seats: []scheduler.SeatView{}, Refused: []scheduler.RefusedSeat{},
		LaunchFailures: []scheduler.LaunchFailure{{Pool: gptPool, Handle: "x1", Stage: "seat_not_up", ReasonCode: "no_proof", Count: 2, LaunchID: "L2", Seq: 2, At: time.Now()}},
		LaunchBlocked:  []scheduler.LaunchBlockedTask{{TaskID: tk.ID, Pool: gptPool.Key(), Seq: 2}}}
	raw, _ := json.Marshal(prev)
	if err := os.MkdirAll(filepath.Dir(scheduler.StatePathFor(root)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(scheduler.StatePathFor(root), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	l, log := &rosterLauncher{}, &lockedBuf{}
	err := runUntil(t, Options{Root: root, Launcher: l, Log: log,
		Config: scheduler.Config{MaxSeats: 2, Roster: []scheduler.RosterSeat{{Handle: "x1", Pool: gptPool, Worktree: "/w/x1"}}}},
		l, func(ds []scheduler.Decision) bool { return decided(ds, "dispatched") })
	if err != nil {
		t.Fatal(err)
	}
	text := log.String()
	for _, want := range []string{`"kind":"launch_failures_restored"`, `"kind":"task_reset","task_id":"` + tk.ID + `"`, `"kind":"dispatched","task_id":"` + tk.ID + `","seat":"x1"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("the log lacks %s:\n%s", want, text)
		}
	}
}

// Codex finding 3 on t17: the proof file read is bounded. A read past the
// bound is unknown (an error, never gone), a second check while it is
// still in flight starts no second read, and the read's result is taken
// once it lands. Reverted (an unbounded read), the check blocks the tick.
func TestLivenessProofReadIsBounded(t *testing.T) {
	root := t.TempDir()
	if err := launch.WriteProof(root, launch.ProofEntry{Handle: "a", RequestID: "req-1", PID: os.Getpid(), Start: "s"}); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	reads := 0
	var mu sync.Mutex
	prev := readProofOf
	readProofOf = func(root, handle string) (launch.ProofEntry, error) {
		mu.Lock()
		reads++
		mu.Unlock()
		<-release
		return prev(root, handle)
	}
	t.Cleanup(func() { readProofOf = prev })
	l := &Liveness{Root: root, ReadBound: 20 * time.Millisecond}
	check := func() (bool, error) {
		type answer struct {
			gone bool
			err  error
		}
		done := make(chan answer, 1)
		go func() { g, _, err := l.SeatProcess("a", "req-1"); done <- answer{g, err} }()
		select {
		case a := <-done:
			return a.gone, a.err
		case <-time.After(2 * time.Second):
			t.Fatal("the liveness check did not return within 2 s: the proof read is unbounded")
			return false, nil
		}
	}
	if gone, err := check(); gone || err == nil || !strings.Contains(err.Error(), "was not read within 20ms") {
		t.Fatalf("a slow read: gone %v err %v", gone, err)
	}
	if gone, err := check(); gone || err == nil || !strings.Contains(err.Error(), "still being read") {
		t.Fatalf("while the read is in flight: gone %v err %v", gone, err)
	}
	// The read lands and is taken: the proof names this process with another
	// stamp ("s"), so the seat is gone (a reused pid). The bound is widened so
	// a loaded machine cannot turn the landing into another timeout.
	close(release)
	l.ReadBound = 2 * time.Second
	if gone, err := check(); !gone || err != nil {
		t.Fatalf("after the read landed: gone %v err %v", gone, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if reads != 1 {
		t.Fatalf("the proof was read %d times, want once", reads)
	}
	if got := ProofReadBound(2*time.Second, 4); got != 250*time.Millisecond {
		t.Fatalf("the tick's share: %s", got)
	}
}
