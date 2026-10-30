package seatlaunch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	Plan SeatPlanner
	// PlanRoster turns a roster launch's requests (one per roster seat, each
	// naming its handle) into the squad to launch in one apply (3.0.1 t16);
	// nil: the launcher launches no roster.
	PlanRoster func(reqs []scheduler.SeatRequest) (Squad, error)
	Options    Options               // Decisions carries the standing answers, if any
	Post       func(scheduler.Event) // usually (*scheduler.Scheduler).Post
	Root       string                // the session root, for CloseSeat
	Now        func() time.Time
	// CloseWait bounds how long CloseSeat waits for the process to exit.
	CloseWait time.Duration
	// PaneGone reports whether an AMQ pane resource no longer exists; ok is
	// false when that cannot be determined. Default: a read-only tmux
	// list-panes query.
	PaneGone func(resource string) (gone, ok bool)
	// BindingPane reports whether a roster seat's recorded pane resource is
	// still the binding's live pane (its server, session, epoch and agent
	// label; bindingPanePresent); known is false when that cannot be read. A
	// test seam.
	BindingPane func(root, handle, resource string) (present, known bool)
	// PanesFor counts the panes labelled with a handle (@amq_pane_agent);
	// ok is false when that cannot be determined. Default: a read-only tmux
	// list-panes query bounded at 2 s (the fix round's item 3).
	PanesFor func(handle string) (n int, ok bool)
	// Clock is the clock the token window is compared on (default
	// time.Now, monotonic; the proof server's ProofClock in tests).
	Clock func() time.Time
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
	// ambiguous: Apply observed more than one pane for the handle, or a pane
	// for a handle this request does not know (apply_ambiguous); no one
	// pane is the evidence then, only the proof or a confirmed close.
	ambiguous bool
	proof     *proofServer
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
	l.settleAll([]*request{r}, res, err)
}

// settleAll settles every request of one launch (one seat, or the roster's
// seats in one apply): each by its own seat's state. A pane observed for a
// handle no request of the launch knows makes every request ambiguous.
func (l *Launcher) settleAll(rs []*request, res Result, err error) {
	known := map[string]bool{}
	for _, r := range rs {
		known[r.handle] = true
	}
	for _, r := range rs {
		l.settleOne(r, known, res, err)
	}
}

func (l *Launcher) settleOne(r *request, known map[string]bool, res Result, err error) {
	var seat *SeatState
	for i := range res.Seats {
		if res.Seats[i].Handle == r.handle {
			seat = &res.Seats[i]
		}
	}
	up := err == nil && res.Stage == "applied" && seat != nil && seat.Up
	if up {
		l.hold(r.handle, seatIdentity{PID: seat.PID, Start: seat.Start})
	}
	l.mu.Lock()
	r.launchID, r.err, r.proof = res.LaunchID, err, res.proof
	mine := 0
	if res.Apply != nil {
		for _, o := range res.Apply.Observations {
			if o.Handle != r.handle {
				if !known[o.Handle] {
					r.ambiguous = true // a pane for a handle this launch does not know
				}
				continue
			}
			if mine++; mine > 1 {
				r.ambiguous = true // two panes for one handle: neither is the evidence
			}
			r.resource = o.Resource
		}
	}
	switch {
	case up && r.cancelled:
		r.state = stateClosing
	case up:
		r.state = stateUp
	case r.ambiguous:
		// The proof or a confirmed close settles it (CancelSeatRequest).
		r.state, r.err = stateUnresolved, fmt.Errorf("apply_ambiguous: AMQ observed %d pane(s) for %s and %d other(s); %v", mine, r.handle, len(res.Apply.Observations)-mine, seatFailure(res, err, seat))
	case placedSeat(res, err, seat):
		r.state, r.err = stateUnresolved, seatFailure(res, err, seat)
	default:
		r.state, r.err = stateFailed, seatFailure(res, err, seat)
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

// placedSeat reports whether AMQ may have started this request's seat:
// Apply began and did not report a clean refusal, and this seat's seat-exec
// did not refuse. An error or a missing ApplyResult after Apply began is
// unknown, so placed: AMQ can create the pane and then exit without valid
// JSON (fix 3 item 1). The refusal is the seat's own (3.0.1 t16, Codex
// finding 2): in a roster launch another seat's refusal says nothing about
// this one, whose pane may still start.
func placedSeat(res Result, err error, seat *SeatState) bool {
	if !res.ApplyStarted {
		return false
	}
	if err != nil || res.Apply == nil {
		return true
	}
	if d := string(res.Apply.Disposition); d == "not_applied" || d == "rolled_back" {
		return false
	}
	if seat != nil && seat.State == "refused" {
		return false // seat-exec exited before exec: nothing runs
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
		// A seat that is up cannot be cancelled away: cancelling it is a
		// close, and only a confirmed close (the process gone by its stamp)
		// releases the reservation. Until then the error keeps the slot
		// counted, so the scheduler cannot request a replacement past the cap
		// while a seat that resists SIGTERM still runs (fix 3b item 2).
		l.mu.Lock()
		r.state = stateClosing
		l.mu.Unlock()
		return l.closeOwed(r)
	case stateClosing:
		return l.closeOwed(r)
	}
	if r.ambiguous {
		return l.cancelAmbiguous(r)
	}
	// unresolved: the proof socket closed with the launch, so a seat that
	// starts now cannot prove and seat-exec refuses it; the pane is the
	// evidence that it is gone (the seat's record is never read here).
	gone, known := l.paneGone(r.resource)
	if known && gone {
		l.mu.Lock()
		r.state = stateCancelled
		l.mu.Unlock()
		return nil
	}
	return fmt.Errorf("seat request %s: AMQ placed the seat (%s) and its termination is not confirmed", req.ID, orNone(r.resource))
}

// cancelAmbiguous settles the cancel of an apply_ambiguous request (phase 5
// re-pass P1-3). No pane check answers it: with two observations for the
// handle, or one for a handle the request does not know, the pane checked
// could be the wrong one while the live one proves. A seat that proved over
// the launch's socket is closed by its proved identity, and only that
// confirmed close releases the request; without a proof the request stays
// unresolved.
func (l *Launcher) cancelAmbiguous(r *request) error {
	l.mu.Lock()
	srv, handle, id := r.proof, r.handle, r.req.ID
	l.mu.Unlock()
	if srv != nil {
		if up, pid, start, _, _ := srv.state(handle); up {
			l.hold(handle, seatIdentity{PID: pid, Start: start})
			l.mu.Lock()
			r.state = stateClosing
			l.mu.Unlock()
			return l.closeOwed(r)
		}
	}
	// After the token window plus the close grace no seat-exec can prove
	// any more (the socket is closed): a request with no pane for its handle
	// is abandoned and its slot released; a pane that exists stays held
	// until it is closed (the fix round's item 3, ruling).
	if srv != nil {
		if end := srv.windowEnd(); !end.IsZero() && !l.clock().Before(end.Add(l.closeWait())) {
			if n, known := l.panesFor(handle); known && n == 0 {
				l.mu.Lock()
				r.state, r.err = stateCancelled, fmt.Errorf("apply_abandoned: no proof within the token window and no pane for %s", handle)
				l.mu.Unlock()
				return nil
			}
		}
	}
	return fmt.Errorf("seat request %s: apply_ambiguous: AMQ observed more than one pane for %s, or a pane for an unknown handle; only its proof, a confirmed close, or (after the token window) a confirmed absence of its pane settles it", id, handle)
}

func (l *Launcher) clock() time.Time {
	if l.Clock != nil {
		return l.Clock()
	}
	return time.Now()
}

func (l *Launcher) closeWait() time.Duration {
	if l.CloseWait > 0 {
		return l.CloseWait
	}
	return closeGrace
}

func (l *Launcher) panesFor(handle string) (int, bool) {
	if l.PanesFor != nil {
		return l.PanesFor(handle)
	}
	return tmuxPanesFor(handle, l.Options.Env)
}

// tmuxPanesFor counts the panes AMQ labelled with the handle, from a
// read-only `tmux list-panes -a` bounded at 2 s; no tmux server is no pane;
// any other failure is unknown.
func tmuxPanesFor(handle string, env []string) (int, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{@amq_pane_agent}")
	cmd.WaitDelay = 500 * time.Millisecond
	if env != nil {
		cmd.Env = env
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == nil && strings.Contains(stderr.String(), "no server running") {
			return 0, true
		}
		return 0, false
	}
	n := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == handle {
			n++
		}
	}
	return n, true
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
		// The core-owned launch ledger (an accepted proof), never the seat's
		// own record.
		if e, err := launch.ReadProof(l.Root, handle); err == nil && e.PID > 0 {
			id, held = seatIdentity{PID: e.PID, Start: e.Start}, true
		}
	}
	if !held {
		return fmt.Errorf("close %s (%s): the launcher holds no process identity for it; termination cannot be confirmed", handle, reason)
	}
	grace := l.CloseWait
	if grace == 0 {
		grace = closeGrace
	}
	escalated, err := closeProcess(id.PID, id.Start, grace)
	if err != nil {
		return fmt.Errorf("close %s (%s): %w", handle, reason, err)
	}
	if escalated {
		// The ledger records that the seat had to be killed (fix 3c item 2a).
		e, rerr := launch.ReadProof(l.Root, handle)
		if rerr != nil || e.PID != id.PID {
			e = launch.ProofEntry{Handle: handle, PID: id.PID, Start: id.Start}
		}
		at := l.now().UTC()
		e.CloseEscalated, e.ClosedAt = true, &at
		if werr := launch.WriteProof(l.Root, e); werr != nil {
			return fmt.Errorf("close %s (%s): the seat was killed but the ledger could not record it: %w", handle, reason, werr)
		}
	}
	return nil
}

// closeProcess sends SIGTERM to pid only while its raw start stamp equals
// born, checked again immediately before every signal, and confirms the
// close by process state: gone, or alive with another stamp (a reused pid).
// A seat still running after the grace is killed (SIGKILL) and the close
// reports escalated (fix 3c item 2a). A stamp that is missing or unreadable
// fails closed: no signal, an error; an unreadable stamp after the signal is
// never escalated on (the process is not known to be the seat) and is
// reported at the deadline.
func closeProcess(pid int, born string, grace time.Duration) (escalated bool, err error) {
	same := func() (bool, error) {
		if !procinfo.Alive(pid) {
			return false, nil
		}
		if born == "" {
			return false, fmt.Errorf("no start stamp is held for pid %d; not signalled", pid)
		}
		now, ok := startToken(pid)
		if !ok {
			return false, fmt.Errorf("the start stamp of pid %d is unavailable; not signalled", pid)
		}
		return now == born, nil
	}
	signal := func(sig syscall.Signal) error {
		p, err := os.FindProcess(pid)
		if err != nil {
			return err
		}
		if ok, serr := same(); serr != nil || !ok { // re-check immediately before the signal
			if serr != nil {
				return serr
			}
			return errGone
		}
		return p.Signal(sig)
	}
	gone := func(wait time.Duration) (bool, error) {
		var last error
		for deadline := time.Now().Add(wait); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			ok, err := same()
			if err == nil && !ok {
				return true, nil
			}
			last = err
		}
		return false, last
	}
	if ok, err := same(); err != nil || !ok {
		return false, err
	}
	if err := signal(syscall.SIGTERM); err != nil {
		if err == errGone {
			return false, nil
		}
		return false, err
	}
	done, last := gone(grace)
	if done {
		return false, nil
	}
	if last != nil {
		return false, fmt.Errorf("pid %d not confirmed closed after %s: %w", pid, grace, last)
	}
	if err := signal(syscall.SIGKILL); err != nil {
		if err == errGone {
			return false, nil
		}
		return false, err
	}
	if done, last = gone(killWait); done {
		return true, nil
	}
	if last != nil {
		return true, fmt.Errorf("pid %d not confirmed closed after SIGKILL: %w", pid, last)
	}
	return true, fmt.Errorf("pid %d still running after SIGKILL", pid)
}

var errGone = errors.New("the process is gone")

// closeGrace is how long a seat has after SIGTERM before SIGKILL; killWait
// bounds the wait after SIGKILL.
var (
	closeGrace = 10 * time.Second
	killWait   = 5 * time.Second
)

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

// seatFailure is why one seat of a launch did not come up.
func seatFailure(res Result, err error, seat *SeatState) error {
	if err != nil {
		return err
	}
	if seat != nil && !seat.Up {
		return fmt.Errorf("seat %s %s: %s %s", seat.Handle, seat.State, seat.ReasonCode, seat.Detail)
	}
	return fmt.Errorf("launch stopped at %s", res.Stage)
}

// startToken is procinfo.StartToken: the raw start stamp (Linux starttime
// ticks, darwin kinfo timeval), never a wall-clock reconstruction, so a clock
// step cannot make a live process look reused (fix 3). A test seam.
var startToken = procinfo.StartToken

var _ scheduler.SeatLauncher = (*Launcher)(nil)

// hold keeps the identity of an accepted proof (pid and raw start stamp)
// for CloseSeat; the launch ledger holds the same entry on disk.
func (l *Launcher) hold(handle string, id seatIdentity) {
	if id.PID <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.ident == nil {
		l.ident = map[string]seatIdentity{}
	}
	l.ident[handle] = id
}
