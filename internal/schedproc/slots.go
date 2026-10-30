package schedproc

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Slots are the seats the scheduler may start: the squad's typed seat list
// (the `launch --seats` file; squad.yaml compiles to it). A request for a
// pool takes the first free seat whose runtime matches, and for Claude and
// Codex its model and effort (a pi seat's model, when the list names one).
// The seat gets its own worktree on its own branch (created from the base
// branch when missing, git hooks off); a seat whose worktree is the
// repository root is never started, because the commit step moves its
// branch.
type Slots struct {
	Squad      seatlaunch.Squad // normalized; Wrapper and AMQPath set
	BaseBranch string           // default main
	Git        string           // default git on PATH
	// Target is the launch's record of the owner's grant for each seat's
	// commit step: repository, base branch and push URL (R2).
	Target seatcommit.Target

	mu     sync.Mutex
	inUse  map[string]string // handle -> request id
	byReq  map[string]string // request id -> handle
	closed map[string]bool   // handles quarantined or otherwise not to be reused
}

func poolMatches(p task.Pool, seat seatlaunch.Seat) bool {
	if p.Runtime != seat.Runtime {
		return false
	}
	if seat.Runtime == "pi" {
		return seat.Model == "" || seat.Model == p.Model
	}
	return seat.Model == p.Model && seat.Effort == p.Effort
}

// Plan reserves a free matching seat for the request and returns the
// one-seat squad to launch (the seatlaunch.Launcher's SeatPlanner).
func (s *Slots) Plan(req scheduler.SeatRequest) (seatlaunch.Squad, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inUse == nil {
		s.inUse, s.byReq, s.closed = map[string]string{}, map[string]string{}, map[string]bool{}
	}
	for _, seat := range s.Squad.Seats {
		if !poolMatches(req.Pool, seat) || s.inUse[seat.Handle] != "" || s.closed[seat.Handle] {
			continue
		}
		seat, err := s.ensureWorktree(seat)
		if err != nil {
			return seatlaunch.Squad{}, err
		}
		seat.RequestID = req.ID
		q := s.Squad
		q.Seats = []seatlaunch.Seat{seat}
		s.inUse[seat.Handle], s.byReq[req.ID] = req.ID, seat.Handle
		return q, nil
	}
	return seatlaunch.Squad{}, fmt.Errorf("no free seat in the squad's seat list for pool %s", req.Pool.Key())
}

// Release frees a seat (closed, or its request cancelled).
func (s *Slots) Release(handle string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id := s.inUse[handle]; id != "" {
		delete(s.byReq, id)
	}
	delete(s.inUse, handle)
}

// ReleaseRequest frees the seat reserved for a request id.
func (s *Slots) ReleaseRequest(id string) {
	s.mu.Lock()
	h := s.byReq[id]
	s.mu.Unlock()
	if h != "" {
		s.Release(h)
	}
}

func (s *Slots) ensureWorktree(seat seatlaunch.Seat) (seatlaunch.Seat, error) {
	root := filepath.Clean(s.Squad.ProjectRoot)
	if _, err := s.checkWorktree(seat); err != nil {
		return seat, err
	}
	if seat.Branch == "" {
		seat.Branch = "seat/" + seat.Handle
	}
	if _, err := os.Stat(seat.Worktree); err == nil {
		return seat, nil
	}
	base := firstNonEmpty(s.BaseBranch, "main")
	// The hardened git (R2): no hook, filter or attribute of the shared
	// repository config runs, and no credential reaches git.
	if err := seatcommit.AddWorktree(s.Git, root, seat.Worktree, seat.Branch, base, nil); err != nil {
		return seat, fmt.Errorf("seat %s worktree: %v", seat.Handle, err)
	}
	return seat, nil
}

// Launcher sends each request to the launcher of its runtime (the AMQ launch
// API for Claude, Codex and, since AMQ v0.116, pi) and
// frees the seat's slot when its request is cancelled or the seat closed.
type Launcher struct {
	Slots     *Slots
	ByRuntime map[string]scheduler.SeatLauncher

	mu     sync.Mutex
	reqRun map[string]string // request id -> runtime
	seatRT map[string]string // handle -> runtime
}

func (l *Launcher) RequestSeat(req scheduler.SeatRequest) error {
	inner := l.ByRuntime[req.Pool.Runtime]
	if inner == nil {
		return fmt.Errorf("no launcher for runtime %s", req.Pool.Runtime)
	}
	l.mu.Lock()
	if l.reqRun == nil {
		l.reqRun, l.seatRT = map[string]string{}, map[string]string{}
	}
	l.reqRun[req.ID] = req.Pool.Runtime
	l.mu.Unlock()
	err := inner.RequestSeat(req)
	if err != nil {
		l.Slots.ReleaseRequest(req.ID)
	}
	return err
}

func (l *Launcher) CancelSeatRequest(req scheduler.SeatRequest) error {
	l.mu.Lock()
	rt := l.reqRun[req.ID]
	l.mu.Unlock()
	inner := l.ByRuntime[firstNonEmpty(rt, req.Pool.Runtime)]
	if inner == nil {
		return nil
	}
	err := inner.CancelSeatRequest(req)
	if err == nil {
		l.Slots.ReleaseRequest(req.ID)
	}
	return err
}

func (l *Launcher) CloseSeat(handle, reason string) error {
	runtime := ""
	for _, s := range l.Slots.Squad.Seats {
		if s.Handle == handle {
			runtime = s.Runtime
		}
	}
	inner := l.ByRuntime[runtime]
	if inner == nil {
		return nil
	}
	err := inner.CloseSeat(handle, reason)
	if err == nil && !l.CloseUnconfirmed(handle) {
		// A confirmed close frees the slot now; an unconfirmed one (a
		// launcher that is a scheduler.AsyncCloser and says so) only at
		// the seat's seat_gone (P1-6).
		l.Slots.Release(handle)
		if reason == "quarantined" {
			l.Slots.mu.Lock()
			if l.Slots.closed == nil {
				l.Slots.closed = map[string]bool{}
			}
			l.Slots.closed[handle] = true
			l.Slots.mu.Unlock()
		}
	}
	return err
}

// CloseUnconfirmed asks the seat's own launcher (a scheduler.AsyncCloser)
// whether its nil close does not confirm the termination; the scheduler then
// waits for the seat's seat_gone (R3). The AMQ launcher confirms.
func (l *Launcher) CloseUnconfirmed(handle string) bool {
	for _, s := range l.Slots.Squad.Seats {
		if s.Handle == handle {
			a, ok := l.ByRuntime[s.Runtime].(scheduler.AsyncCloser)
			return ok && a.CloseUnconfirmed(handle)
		}
	}
	return false
}

// Reserve holds a seat's slot (N2: a seat a previous incarnation left
// running; N3: a gone seat whose exit check is pending), so no replacement
// launches under its handle and worktree.
func (l *Launcher) Reserve(handle string) {
	l.Slots.mu.Lock()
	defer l.Slots.mu.Unlock()
	if l.Slots.inUse == nil {
		l.Slots.inUse, l.Slots.byReq, l.Slots.closed = map[string]string{}, map[string]string{}, map[string]bool{}
	}
	if l.Slots.inUse[handle] == "" {
		l.Slots.inUse[handle] = "reserved"
	}
}

// SeatGone frees the seat's slot once its termination is reported (P1-6):
// a replacement never launches under the same handle and worktree before
// the old provider stops.
func (l *Launcher) SeatGone(handle string) { l.Slots.Release(handle) }

var (
	_ scheduler.SeatLauncher     = (*Launcher)(nil)
	_ scheduler.AsyncCloser      = (*Launcher)(nil)
	_ scheduler.SeatGoneObserver = (*Launcher)(nil)
	_ scheduler.SeatReserver     = (*Launcher)(nil)
)

func cleanPath(p string) string { return filepath.Clean(p) }

func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, _ := json.MarshalIndent(v, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
