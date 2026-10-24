//go:build unix

package seatlaunch

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// launcherFixture runs Launch against a fake amq whose Apply "starts" the
// seat: it publishes seat-exec's exec block for a real sleeping process.
type launcherFixture struct {
	t      *testing.T
	q      Squad
	l      *Launcher
	mu     sync.Mutex
	events []scheduler.Event
	gate   chan struct{} // when set, Apply waits for it
	procs  []*exec.Cmd
}

func newLauncherFixture(t *testing.T, applyOutcome string) *launcherFixture {
	f := &launcherFixture{t: t, q: testSquad(t)}
	fake := &fakeAMQ{t: t, prepare: preparedResult(), apply: launchapi.ApplyResultV1{ResultVersion: 1, Outcome: applyOutcome, Disposition: "committed"}}
	fake.onApply = func() {
		if f.gate != nil {
			<-f.gate
		}
		if applyOutcome != "applied" {
			return
		}
		cmd := exec.Command("sleep", "30")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		f.procs = append(f.procs, cmd)
		go cmd.Wait()
		dir := filepath.Join(f.q.SessionRoot(), "agents", "cto")
		launch.WriteUnderRecordLock(dir, launch.Record{Handle: "cto", Exec: &launch.Exec{At: time.Now(), Outcome: "exec", DrainProof: "amq drain", PID: cmd.Process.Pid}})
	}
	t.Cleanup(func() {
		for _, c := range f.procs {
			c.Process.Kill()
		}
	})
	opts := launchOpts(t, fake, map[string]string{"sha256:a1": "trust_exact_subject"})
	f.l = &Launcher{
		Options: opts, Root: f.q.SessionRoot(), CloseWait: 2 * time.Second,
		Plan: func(req scheduler.SeatRequest) (Squad, error) {
			q := f.q
			q.Seats = q.Seats[:1]
			return q, nil
		},
		Post: func(ev scheduler.Event) { f.mu.Lock(); f.events = append(f.events, ev); f.mu.Unlock() },
	}
	return f
}

var poolA = task.Pool{Runtime: "claude", Model: "sonnet", Effort: "medium"}

func TestLauncherPostsSeatUpWithTheRequestID(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	req := scheduler.SeatRequest{ID: "seat-req-inc1-1", Pool: poolA, TaskID: "t1"}
	if err := f.l.RequestSeat(req); err != nil {
		t.Fatal(err)
	}
	f.l.Wait()
	if len(f.events) != 1 || f.events[0].Kind != scheduler.SeatUp || f.events[0].RequestID != req.ID || f.events[0].Handle != "cto" || f.events[0].Pool != poolA {
		t.Fatalf("events %+v", f.events)
	}
	staged, _ := os.ReadFile(filepath.Join(launch.StagedDir(f.q.SessionRoot(), "cto"), launch.FileName))
	if !strings.Contains(string(staged), `"request_id": "seat-req-inc1-1"`) {
		t.Fatalf("the request id is not in the launch record:\n%s", staged)
	}
	if err := f.l.RequestSeat(req); err == nil {
		t.Fatal("a repeated request id was accepted")
	}
	if err := f.l.RequestSeat(scheduler.SeatRequest{Pool: poolA}); err == nil {
		t.Fatal("a request without an id was accepted")
	}
}

func TestLauncherCancelInFlightClosesTheSeatAndPostsNothing(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	f.gate = make(chan struct{})
	req := scheduler.SeatRequest{ID: "seat-req-inc1-2", Pool: poolA}
	if err := f.l.RequestSeat(req); err != nil {
		t.Fatal(err)
	}
	// Apply is in flight: not confirmed.
	time.Sleep(50 * time.Millisecond)
	if err := f.l.CancelSeatRequest(req); err == nil {
		t.Fatal("cancel of an in-flight launch was confirmed")
	}
	close(f.gate)
	f.l.Wait()
	if len(f.events) != 0 {
		t.Fatalf("a cancelled request posted %+v", f.events)
	}
	if pid := f.procs[0].Process.Pid; procinfo.Alive(pid) {
		t.Fatalf("the cancelled seat (pid %d) still runs", pid)
	}
	if err := f.l.CancelSeatRequest(req); err != nil {
		t.Fatalf("after the close the cancel is confirmed: %v", err)
	}
}

func TestLauncherCancelConfirmsFailedAndUnknownRequests(t *testing.T) {
	f := newLauncherFixture(t, "action_required")
	req := scheduler.SeatRequest{ID: "seat-req-inc1-3", Pool: poolA}
	if err := f.l.RequestSeat(req); err != nil {
		t.Fatal(err)
	}
	f.l.Wait()
	if len(f.events) != 0 {
		t.Fatalf("a failed launch posted %+v", f.events)
	}
	if err := f.l.CancelSeatRequest(req); err != nil {
		t.Fatalf("failed launch: %v", err)
	}
	if err := f.l.CancelSeatRequest(scheduler.SeatRequest{ID: "seat-req-other-9"}); err != nil {
		t.Fatalf("unknown id: %v", err)
	}
}

func TestLauncherCloseSeatStopsTheProvider(t *testing.T) {
	f := newLauncherFixture(t, "applied")
	if err := f.l.RequestSeat(scheduler.SeatRequest{ID: "seat-req-inc1-4", Pool: poolA}); err != nil {
		t.Fatal(err)
	}
	f.l.Wait()
	pid := f.procs[0].Process.Pid
	if err := f.l.CloseSeat("cto", "idle"); err != nil || procinfo.Alive(pid) {
		t.Fatalf("close: %v, alive %v", err, procinfo.Alive(pid))
	}
	if err := f.l.CloseSeat("cto", "idle"); err != nil {
		t.Fatalf("closing a closed seat: %v", err)
	}
	if err := f.l.CloseSeat("nobody", "idle"); err != nil {
		t.Fatalf("closing an unknown seat: %v", err)
	}
}
