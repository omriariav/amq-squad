package seatlaunch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
// posts SeatUp with the request id once the seat proved its identity; the id
// travels in the launch record and AMQ_SQUAD_REQUEST_ID. CancelSeatRequest
// returns nil only when no seat will join for that id: the launch never
// reached AMQ, seat-exec refused, or a started seat was closed and the close
// confirmed. CloseSeat stops the seat's provider process (the pane is AMQ's;
// amq-squad never closes it, R6).
type Launcher struct {
	Plan    SeatPlanner
	Options Options               // Decisions carries the standing answers, if any
	Post    func(scheduler.Event) // usually (*scheduler.Scheduler).Post
	Root    string                // the session root, for CloseSeat
	Now     func() time.Time
	// CloseWait bounds how long CloseSeat waits for the process to exit.
	CloseWait time.Duration
	// PaneGone reports whether an AMQ pane resource no longer exists; ok is
	// false when that cannot be determined. Default: a read-only tmux
	// list-panes query.
	PaneGone func(resource string) (gone, ok bool)
	// closeSeat replaces CloseSeat in closeOwed (tests).
	closeSeat func(handle, reason string) error

	mu    sync.Mutex
	reqs  map[string]*request
	ident map[string]seatIdentity // handle -> the launched process, held by the launcher
	wg    sync.WaitGroup
}

// seatIdentity is the process a launch started: its pid and raw start stamp.
type seatIdentity struct {
	PID   int
	Start string
}

// Request states. unresolved: Apply placed the seat but its exec record did
// not arrive in time; the provider may still start. closing: a close is
// owed and not yet confirmed.
const (
	stateLaunching  = "launching"
	stateUp         = "up"
	stateUnresolved = "unresolved"
	stateClosing    = "closing"
	stateFailed     = "failed"
	stateCancelled  = "cancelled"
)

type request struct {
	req       scheduler.SeatRequest
	state     string
	handle    string
	launchID  string
	resource  string
	cancelled bool
	closing   bool // a close is in flight (closeOwed runs one at a time)
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
	r := &request{req: req, state: stateLaunching, handle: q.Seats[0].Handle}
	l.reqs[req.ID] = r
	l.mu.Unlock()
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		res, err := Launch(context.Background(), q, l.Options)
		l.settle(r, res, err)
	}()
	return nil
}

// settle records a finished launch. A started seat whose request was
// cancelled meanwhile is closed; it becomes cancelled only once the close is
// confirmed, else it stays closing with the error for the next cancel retry.
func (l *Launcher) settle(r *request, res Result, err error) {
	up := err == nil && res.Stage == "applied" && len(res.Seats) == 1 && res.Seats[0].Up
	if up {
		l.holdIdentity(r.handle, res.LaunchID, r.req.ID)
	}
	l.mu.Lock()
	r.launchID, r.err = res.LaunchID, err
	if res.Apply != nil {
		for _, o := range res.Apply.Observations {
			if o.Handle == r.handle {
				r.resource = o.Resource
			}
		}
	}
	switch {
	case up && r.cancelled:
		r.state = stateClosing
	case up:
		r.state = stateUp
	case placed(res, err):
		r.state, r.err = stateUnresolved, launchFailure(res, err)
	default:
		r.state, r.err = stateFailed, launchFailure(res, err)
	}
	state := r.state
	l.mu.Unlock()
	switch state {
	case stateUp:
		l.Post(scheduler.Event{Kind: scheduler.SeatUp, At: l.now(), Handle: r.handle, Pool: r.req.Pool, RequestID: r.req.ID})
	case stateClosing:
		l.closeOwed(r)
	}
}

// placed reports whether AMQ may have started the seat: Apply began and did
// not report a clean refusal, and seat-exec did not refuse. An error or a
// missing ApplyResult after Apply began is unknown, so placed: AMQ can create
// the pane and then exit without valid JSON (fix 3 item 1).
func placed(res Result, err error) bool {
	if !res.ApplyStarted {
		return false
	}
	if err != nil || res.Apply == nil {
		return true
	}
	if d := string(res.Apply.Disposition); d == "not_applied" || d == "rolled_back" {
		return false
	}
	for _, s := range res.Seats {
		if s.State == "refused" {
			return false // seat-exec exited before exec: nothing runs
		}
	}
	return true
}

// closeOwed runs a close for a closing request and settles it. One close
// runs per request at a time: settle's goroutine and a CancelSeatRequest
// retry can both owe it, and a second caller gets "close in progress" (an
// error, so the scheduler keeps the reservation and retries).
func (l *Launcher) closeOwed(r *request) error {
	l.mu.Lock()
	if r.closing {
		l.mu.Unlock()
		return fmt.Errorf("seat request %s: close in progress", r.req.ID)
	}
	r.closing = true
	l.mu.Unlock()
	closeFn := l.CloseSeat
	if l.closeSeat != nil {
		closeFn = l.closeSeat
	}
	err := closeFn(r.handle, "request cancelled")
	l.mu.Lock()
	defer l.mu.Unlock()
	r.closing = false
	if err != nil {
		r.err = err
		return err
	}
	r.state, r.err = stateCancelled, nil
	return nil
}

// CancelSeatRequest confirms (nil) that no seat will join for req.ID. It
// returns an error, and the reservation keeps its place, while that is not
// confirmed: a launch in flight, a placed seat whose pane may still start, or
// a close that has not succeeded.
func (l *Launcher) CancelSeatRequest(req scheduler.SeatRequest) error {
	l.mu.Lock()
	r, ok := l.reqs[req.ID]
	if !ok {
		l.mu.Unlock()
		return nil
	}
	r.cancelled = true
	state := r.state
	l.mu.Unlock()
	switch state {
	case stateFailed, stateCancelled:
		return nil
	case stateLaunching:
		return fmt.Errorf("seat request %s: launch in flight; the seat is closed when it comes up", req.ID)
	case stateUp:
		// The seat's SeatUp is posted (or about to be). Confirming the cancel
		// makes the scheduler refuse that SeatUp for a cancelled id and close
		// the seat through CloseSeat, so a closing seat is never counted
		// (fix 3 item 2).
		return nil
	case stateClosing:
		return l.closeOwed(r)
	}
	// unresolved: look again at what seat-exec published for this launch.
	rec, err := launch.Read(filepath.Join(l.Root, "agents", r.handle))
	if err == nil && rec.Exec != nil && rec.LaunchID == r.launchID && rec.RequestID == req.ID {
		if rec.Exec.Outcome == "refused" {
			l.mu.Lock()
			r.state = stateFailed
			l.mu.Unlock()
			return nil
		}
		l.mu.Lock()
		r.state = stateClosing
		l.mu.Unlock()
		l.holdIdentity(r.handle, r.launchID, req.ID)
		return l.closeOwed(r)
	}
	gone, known := l.paneGone(r.resource)
	if known && gone {
		l.mu.Lock()
		r.state = stateCancelled
		l.mu.Unlock()
		return nil
	}
	return fmt.Errorf("seat request %s: AMQ placed the seat (%s) and its termination is not confirmed", req.ID, orNone(r.resource))
}

// CloseSeat stops the seat's provider: SIGTERM to the pid the launcher holds
// for the handle (the provider runs in seat-exec's process, exec'd in
// place), only when the pid's raw start stamp equals the one held, checked
// again immediately before the signal. A pid that is not alive, or alive
// with another start stamp (reused), means the seat's process is gone (nil).
// Termination is confirmed only by process state: a handle the launcher
// holds no identity for, or a stamp that is missing or unreadable, fails
// closed (no signal, an error; the caller keeps the seat closing). The
// seat's own record is never consulted, so deleting it confirms nothing.
func (l *Launcher) CloseSeat(handle, reason string) error {
	l.mu.Lock()
	id, held := l.ident[handle]
	l.mu.Unlock()
	if !held {
		return fmt.Errorf("close %s (%s): the launcher holds no process identity for it; termination cannot be confirmed", handle, reason)
	}
	pid, born := id.PID, id.Start
	same := func() (bool, error) {
		if !procinfo.Alive(pid) {
			return false, nil
		}
		if born == "" {
			return false, fmt.Errorf("close %s (%s): no start stamp is held for pid %d; not signalled", handle, reason, pid)
		}
		now, ok := startToken(pid)
		if !ok {
			return false, fmt.Errorf("close %s (%s): the start stamp of pid %d is unavailable; not signalled", handle, reason, pid)
		}
		return now == born, nil
	}
	if ok, err := same(); err != nil || !ok {
		return err
	}
	p, err := os.FindProcess(pid)
	if err == nil {
		if ok, serr := same(); serr != nil || !ok { // re-check immediately before the signal
			return serr
		}
		err = p.Signal(syscall.SIGTERM)
	}
	if err != nil {
		return fmt.Errorf("close %s (%s): %w", handle, reason, err)
	}
	wait := l.CloseWait
	if wait == 0 {
		wait = 5 * time.Second
	}
	// Closed only when the process is confirmed gone (not alive, or alive
	// with another birth time). An unreadable birth time is not a close: keep
	// waiting, and report it at the deadline.
	var last error
	for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		ok, err := same()
		if err == nil && !ok {
			return nil
		}
		last = err
	}
	if last != nil {
		return fmt.Errorf("close %s (%s): pid %d not confirmed closed after %s: %w", handle, reason, pid, wait, last)
	}
	return fmt.Errorf("close %s (%s): pid %d still running after %s", handle, reason, pid, wait)
}

// Wait blocks until every background launch has finished (tests, shutdown).
func (l *Launcher) Wait() { l.wg.Wait() }

func (l *Launcher) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

func (l *Launcher) paneGone(resource string) (bool, bool) {
	if l.PaneGone != nil {
		return l.PaneGone(resource)
	}
	return tmuxPaneGone(resource, l.Options.Env)
}

// tmuxPaneGone answers from a read-only `tmux list-panes -a` (R6: amq-squad
// never creates, moves or closes a pane; reading the list is none of those).
func tmuxPaneGone(resource string, env []string) (bool, bool) {
	pane, ok := strings.CutPrefix(resource, "tmux:v1:pane:")
	if !ok || pane == "" {
		return false, false
	}
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{pane_id}")
	if env != nil {
		cmd.Env = env
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		// No tmux server: every pane is gone. Anything else: unknown.
		return strings.Contains(string(out), "no server running"), strings.Contains(string(out), "no server running")
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == pane {
			return false, true
		}
	}
	return true, true
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

// startToken is procinfo.StartToken: the raw start stamp (Linux starttime
// ticks, darwin kinfo timeval), never a wall-clock reconstruction, so a clock
// step cannot make a live process look reused (fix 3). A test seam.
var startToken = procinfo.StartToken

var _ scheduler.SeatLauncher = (*Launcher)(nil)

// holdIdentity takes the started process's pid and start stamp into the
// launcher, from the seat's record for this launch (once, at the moment the
// seat is reported up). Commit 2 replaces the source with the proof socket.
func (l *Launcher) holdIdentity(handle, launchID, requestID string) {
	rec, err := launch.Read(filepath.Join(l.Root, "agents", handle))
	if err != nil || rec.Exec == nil || rec.Exec.Outcome != "exec" || rec.LaunchID != launchID || rec.RequestID != requestID {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ident == nil {
		l.ident = map[string]seatIdentity{}
	}
	l.ident[handle] = seatIdentity{PID: rec.Exec.PID, Start: rec.Exec.ProcessStart}
}
