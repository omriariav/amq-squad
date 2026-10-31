package schedproc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
)

// One lead per root (brief 31-1, plan 7.4): `amq-squad lead --team` takes
// the lead lock beside the scheduler's run.lock before it starts its
// scheduler, and records who holds it in lead.json: the lead's pid, its raw
// start stamp (procinfo.StartToken) and its tmux pane. A second lead on the
// same root is refused lead_running, naming the first's pid and pane. The
// lock is an advisory flock held by the lead's process for its life, so it
// is released when that process ends, however it ends; lead.json may then
// outlive it, and is evidence only (a reader probes the lock for liveness).

// Lead lock file names under Dir(root).
const (
	LeadLockName   = "lead.lock"
	LeadRecordName = "lead.json"
)

// ReasonLeadRunning is the refusal of a second lead on a root whose lead
// lock is held.
const ReasonLeadRunning = "lead_running"

// LeadRecord is the holder of a root's lead lock.
type LeadRecord struct {
	Schema int       `json:"schema"`
	PID    int       `json:"pid"`
	Start  string    `json:"start"`          // the raw start stamp of PID
	Pane   string    `json:"pane,omitempty"` // $TMUX_PANE of the lead, "" outside tmux
	At     time.Time `json:"at"`
}

// LeadRunningError is lead_running: another lead holds the root's lead lock.
// Record is that lead's lead.json, zero when it cannot be read.
type LeadRunningError struct {
	Root   string
	Record LeadRecord
}

func (e *LeadRunningError) Error() string {
	pane := e.Record.Pane
	if pane == "" {
		pane = "none recorded"
	}
	msg := fmt.Sprintf("%s: a lead of %s is running (pid %d, tmux pane %s); one lead per root", ReasonLeadRunning, e.Root, e.Record.PID, pane)
	if e.Record.Pane != "" {
		msg += "; attach to it with: tmux attach -t " + e.Record.Pane
	}
	return msg
}

// LeadLock is a held lead lock; Release drops it.
type LeadLock struct{ lock *flock.Exclusive }

// Release drops the lock (lead.json stays, as evidence).
func (l *LeadLock) Release() {
	if l != nil && l.lock != nil {
		_ = l.lock.Close()
	}
}

// AcquireLeadLock takes the root's lead lock for rec (its schema and time
// are filled in) and writes lead.json. A held lock is *LeadRunningError
// with the holder's record.
func AcquireLeadLock(root string, rec LeadRecord) (*LeadLock, error) {
	if err := os.MkdirAll(Dir(root), 0o700); err != nil {
		return nil, err
	}
	lock, acquired, err := flock.TryExclusive(filepath.Join(Dir(root), LeadLockName), true)
	switch {
	case errors.Is(err, flock.ErrUnsupported):
		// no advisory locks on this platform: no lead lock, as for run.lock
	case err != nil:
		return nil, err
	case !acquired:
		held, _ := ReadLeadRecord(root)
		return nil, &LeadRunningError{Root: root, Record: held}
	}
	rec.Schema = 1
	if rec.At.IsZero() {
		rec.At = time.Now().UTC()
	}
	if err := writeJSON(filepath.Join(Dir(root), LeadRecordName), rec); err != nil {
		if lock != nil {
			_ = lock.Close()
		}
		return nil, err
	}
	return &LeadLock{lock: lock}, nil
}

// ReadLeadRecord reads lead.json (the last lead that took the lock).
func ReadLeadRecord(root string) (LeadRecord, error) {
	var rec LeadRecord
	raw, err := os.ReadFile(filepath.Join(Dir(root), LeadRecordName))
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return rec, err
	}
	if rec.Schema != 1 {
		return rec, fmt.Errorf("lead.json schema %d", rec.Schema)
	}
	return rec, nil
}
