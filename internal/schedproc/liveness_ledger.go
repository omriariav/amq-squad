package schedproc

import (
	"fmt"
	"sync"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// Liveness is the scheduler's per-tick check of each live roster seat
// (3.0.1 t17): nothing else posted SeatGone in production, so a roster seat
// whose process died on its own was unseen until a restart. The seat's
// identity is its accepted proof in the launch ledger (pid and raw start
// stamp, the identity CloseSeat and the restart's adoption use), read once
// per seat request and then held; each check is one start-stamp read of
// that pid (darwin: one sysctl KERN_PROC_PID; Linux: one read of
// /proc/<pid>/stat). A pid whose stamp differs (reused) or that is gone is
// gone; a pid that is alive but whose stamp cannot be read is unknown (an
// error, nothing is posted). Never a bare signal 0: signal 0 only tells a
// gone pid from an unreadable stamp, after the stamp read failed. It is
// read only.
type Liveness struct {
	Root string

	mu   sync.Mutex
	held map[string]launch.ProofEntry // handle -> the proof read for its request
}

// readProofOf and aliveOf are test seams.
var (
	readProofOf = launch.ReadProof
	aliveOf     = procinfo.Alive
)

// SeatProcess is scheduler.SeatLiveness.
func (l *Liveness) SeatProcess(handle, requestID string) (bool, string, error) {
	e, err := l.identity(handle, requestID)
	if err != nil {
		return false, "", err
	}
	if now, ok := startTokenOf(e.PID); ok {
		if now == e.Start {
			return false, "", nil
		}
		return true, fmt.Sprintf("pid %d of the seat's accepted proof now carries another start stamp (the pid was reused)", e.PID), nil
	}
	if !aliveOf(e.PID) {
		return true, fmt.Sprintf("pid %d of the seat's accepted proof is gone", e.PID), nil
	}
	return false, "", fmt.Errorf("pid %d of seat %s is alive but its start stamp cannot be read", e.PID, handle)
}

// identity is the seat's accepted proof, read from the ledger the first
// time a request asks and held for it. A seat known by no request id
// (adopted without one) is read each time.
func (l *Liveness) identity(handle, requestID string) (launch.ProofEntry, error) {
	l.mu.Lock()
	e, ok := l.held[handle]
	l.mu.Unlock()
	if ok && requestID != "" && e.RequestID == requestID {
		return e, nil
	}
	e, err := readProofOf(l.Root, handle)
	if err != nil {
		return e, fmt.Errorf("the launch ledger's proof of %s cannot be read: %v", handle, err)
	}
	switch {
	case e.Handle != handle:
		return e, fmt.Errorf("the launch ledger's proof of %s names %s", handle, e.Handle)
	case e.PID <= 1 || e.Start == "":
		return e, fmt.Errorf("the launch ledger's proof of %s holds no process identity (pid %d)", handle, e.PID)
	case requestID != "" && e.RequestID != requestID:
		return e, fmt.Errorf("the launch ledger's proof of %s is request %s, not the seat's %s", handle, orDash(e.RequestID), requestID)
	}
	if requestID != "" {
		l.mu.Lock()
		if l.held == nil {
			l.held = map[string]launch.ProofEntry{}
		}
		l.held[handle] = e
		l.mu.Unlock()
	}
	return e, nil
}

var _ scheduler.SeatLiveness = (*Liveness)(nil)
