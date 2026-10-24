//go:build unix

package seatlaunch

import (
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// Fix 2 item 3: after the signal, a birth time that cannot be read is not a
// close; CloseSeat keeps waiting and reports it at the deadline.
func TestCloseSeatUnreadableBirthTimeIsNotAClose(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	cmd := exec.Command("sh", "-c", `trap "" TERM; sleep 30`) // survives the SIGTERM
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	pid := cmd.Process.Pid
	time.Sleep(100 * time.Millisecond) // let the shell install its trap
	born, ok := procinfo.StartTime(pid)
	if !ok {
		t.Skip("process birth time unavailable on this host")
	}
	publishExecRaw(t, f.q, "cto", &launch.Exec{At: time.Now(), Outcome: "exec", PID: pid, ProcessStart: born})
	var calls atomic.Int32
	prev := startTime
	startTime = func(p int) (time.Time, bool) {
		if calls.Add(1) > 2 { // the two checks before the signal read it; after the signal it is unreadable
			return time.Time{}, false
		}
		return prev(p)
	}
	t.Cleanup(func() { startTime = prev })
	f.l.CloseWait = 300 * time.Millisecond
	err := f.l.CloseSeat("cto", "idle")
	if err == nil || !strings.Contains(err.Error(), "not confirmed closed") || !procinfo.Alive(pid) {
		t.Fatalf("unreadable birth time after the signal: %v (alive %v)", err, procinfo.Alive(pid))
	}
}

// Fix 2 item 4: settle's goroutine and a CancelSeatRequest retry can both
// owe the close; only one CloseSeat runs, the other caller gets "close in
// progress".
func TestCloseOwedRunsOneCloseAtATime(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	var calls atomic.Int32
	release := make(chan struct{})
	f.l.closeSeat = func(handle, reason string) error {
		calls.Add(1)
		<-release // a slow close
		return nil
	}
	r := &request{req: scheduler.SeatRequest{ID: "seat-req-inc1-9"}, state: stateClosing, handle: "cto"}
	f.l.reqs = map[string]*request{r.req.ID: r}
	var wg sync.WaitGroup
	wg.Add(1)
	var first error
	go func() { defer wg.Done(); first = f.l.closeOwed(r) }()
	for calls.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	second := make(chan error, 1)
	go func() { second <- f.l.CancelSeatRequest(r.req) }()
	select {
	case err := <-second:
		if err == nil || !strings.Contains(err.Error(), "close in progress") {
			close(release)
			t.Fatalf("a second close while one is in flight: %v", err)
		}
	case <-time.After(time.Second):
		close(release) // the second caller was blocked inside a second CloseSeat
		<-second
		t.Fatalf("a second CloseSeat ran while one was in flight (calls %d)", calls.Load())
	}
	close(release)
	wg.Wait()
	if first != nil || calls.Load() != 1 {
		t.Fatalf("first close %v, CloseSeat calls %d", first, calls.Load())
	}
	if err := f.l.CancelSeatRequest(r.req); err != nil {
		t.Fatalf("after the close: %v", err)
	}
}
