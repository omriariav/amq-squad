package seatlaunch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// SeatPlanner turns a scheduler seat request into a one-seat squad: the
// handle, role, worktree and branch for the request's pool. squad.yaml (2b-5)
// and the registry (2b-3) supply it; the launcher only launches.
type SeatPlanner func(req scheduler.SeatRequest) (Squad, error)

// Launcher is the scheduler's SeatLauncher (2b-1 contract, docs/task-store-
// design.md "Scheduler hooks"). RequestSeat launches asynchronously and
// posts SeatUp with the request id once the seat proved its drain; the id
// travels in the launch record and AMQ_SQUAD_REQUEST_ID. CancelSeatRequest
// returns nil only when no seat will join for that id. CloseSeat stops the
// seat's provider process (the pane is AMQ's; amq-squad never closes it, R6).
type Launcher struct {
	Plan    SeatPlanner
	Options Options               // Decisions carries the standing answers, if any
	Post    func(scheduler.Event) // usually (*scheduler.Scheduler).Post
	Root    string                // the session root, for CloseSeat
	Now     func() time.Time
	// CloseWait bounds how long CloseSeat waits for the process to exit.
	CloseWait time.Duration

	mu   sync.Mutex
	reqs map[string]*request
	wg   sync.WaitGroup
}

type request struct {
	req       scheduler.SeatRequest
	state     string // launching | up | failed | cancelled
	handle    string
	cancelled bool
	err       error
}

// RequestSeat plans the seat and starts its launch in the background.
func (l *Launcher) RequestSeat(req scheduler.SeatRequest) error {
	if req.ID == "" {
		return errors.New("seat request without an id")
	}
	q, err := l.Plan(req)
	if err != nil {
		return fmt.Errorf("plan seat for %s: %w", req.ID, err)
	}
	if len(q.Seats) != 1 {
		return fmt.Errorf("plan seat for %s: want one seat, got %d", req.ID, len(q.Seats))
	}
	q.Seats[0].RequestID = req.ID
	l.mu.Lock()
	if l.reqs == nil {
		l.reqs = map[string]*request{}
	}
	if _, dup := l.reqs[req.ID]; dup {
		l.mu.Unlock()
		return fmt.Errorf("seat request %s is already known", req.ID)
	}
	r := &request{req: req, state: "launching", handle: q.Seats[0].Handle}
	l.reqs[req.ID] = r
	l.mu.Unlock()
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		res, err := Launch(context.Background(), q, l.Options)
		up := err == nil && res.Stage == "applied" && len(res.Seats) == 1 && res.Seats[0].Up
		l.mu.Lock()
		cancelled := r.cancelled
		switch {
		case up && !cancelled:
			r.state = "up"
		case up:
			r.state = "cancelled"
		default:
			r.state, r.err = "failed", launchFailure(res, err)
		}
		l.mu.Unlock()
		if up && cancelled {
			_ = l.CloseSeat(r.handle, "request cancelled")
			return
		}
		if up {
			l.Post(scheduler.Event{Kind: scheduler.SeatUp, At: l.now(), Handle: r.handle, Pool: req.Pool, RequestID: req.ID})
		}
	}()
	return nil
}

// CancelSeatRequest confirms (nil) that no seat will join for req.ID: the
// launch failed, was cancelled and closed, or never existed here. A launch
// still in flight is marked and refused (the seat is closed when it comes
// up); a seat that already joined is closed first.
func (l *Launcher) CancelSeatRequest(req scheduler.SeatRequest) error {
	l.mu.Lock()
	r, ok := l.reqs[req.ID]
	if !ok {
		l.mu.Unlock()
		return nil
	}
	r.cancelled = true
	state, handle := r.state, r.handle
	l.mu.Unlock()
	switch state {
	case "launching":
		return fmt.Errorf("seat request %s: launch in flight; the seat is closed when it comes up", req.ID)
	case "up":
		if err := l.CloseSeat(handle, "request cancelled"); err != nil {
			return err
		}
		l.mu.Lock()
		r.state = "cancelled"
		l.mu.Unlock()
	}
	return nil
}

// CloseSeat stops the seat's provider: SIGTERM to the pid seat-exec recorded
// (the provider runs in seat-exec's process, exec'd in place), after checking
// that the pid is alive and was not started after the exec (pid reuse). A
// seat with no running provider is already closed (nil).
func (l *Launcher) CloseSeat(handle, reason string) error {
	rec, err := launch.Read(filepath.Join(l.Root, "agents", handle))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("close %s: %w", handle, err)
	}
	if rec.Exec == nil || rec.Exec.Outcome != "exec" || rec.Exec.PID <= 0 || !procinfo.Alive(rec.Exec.PID) {
		return nil
	}
	if started, ok := procinfo.StartTime(rec.Exec.PID); ok && started.After(rec.Exec.At.Add(2*time.Second)) {
		return nil // the pid was reused; the seat's process is gone
	}
	p, err := os.FindProcess(rec.Exec.PID)
	if err == nil {
		err = p.Signal(syscall.SIGTERM)
	}
	if err != nil {
		return fmt.Errorf("close %s (%s): %w", handle, reason, err)
	}
	wait := l.CloseWait
	if wait == 0 {
		wait = 5 * time.Second
	}
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		if !procinfo.Alive(rec.Exec.PID) {
			return nil
		}
	}
	return fmt.Errorf("close %s (%s): pid %d still running after %s", handle, reason, rec.Exec.PID, wait)
}

// Wait blocks until every background launch has finished (tests, shutdown).
func (l *Launcher) Wait() { l.wg.Wait() }

func (l *Launcher) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func launchFailure(res Result, err error) error {
	if err != nil {
		return err
	}
	if len(res.Seats) == 1 && !res.Seats[0].Up {
		return fmt.Errorf("seat %s %s: %s %s", res.Seats[0].Handle, res.Seats[0].State, res.Seats[0].ReasonCode, res.Seats[0].Detail)
	}
	return fmt.Errorf("launch stopped at %s", res.Stage)
}

var _ scheduler.SeatLauncher = (*Launcher)(nil)
