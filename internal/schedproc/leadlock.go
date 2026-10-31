package schedproc

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// One lead per root (brief 31-1, plan 7.4): `amq-squad lead --team` takes
// the lead lock beside the scheduler's run.lock before it starts its
// scheduler, and records who holds it in lead.json: the lead's pid, its raw
// start stamp (procinfo.StartToken) and its tmux pane. A second lead on the
// same root is refused lead_running, naming the first's pid and pane. The
// lock is an advisory flock held by the lead's process for its life, so it
// is released when that process ends, however it ends: the flock is the
// pid's liveness, and a dead lead leaves no stale lock to clear. lead.json
// may outlive it and is evidence only. The holder writes lead.json after it
// takes the lock (never before: two starts could both publish), so a
// competing start that finds the lock held and no fresh record (one whose
// pid is alive with its recorded start stamp) waits briefly for it, then
// refuses lead_running as "starting, pid unknown". Whether the lead's tmux
// pane still exists is `up`'s check (brief 31-1's second PR).

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
	// Starting: the lock is held but the holder had not published its
	// record within the wait (it is starting); Record is then zero.
	Starting bool
}

func (e *LeadRunningError) Error() string {
	if e.Starting {
		return fmt.Sprintf("%s: a lead of %s is starting, pid unknown; one lead per root", ReasonLeadRunning, e.Root)
	}
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
		return nil, runningLead(root)
	}
	if afterLeadLock != nil {
		afterLeadLock()
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

// Seams for tests: how long a competing start waits for the holder's
// record, and the liveness reads that make a record fresh.
var (
	leadRecordWait = 2 * time.Second
	leadAlive      = procinfo.Alive
	leadStamp      = procinfo.StartToken
	// afterLeadLock runs between taking the lock and publishing lead.json:
	// the window a competing start can fall into.
	afterLeadLock func()
)

// runningLead is the refusal of a start that found the lock held: the
// holder's fresh record, waited for briefly, else "starting".
func runningLead(root string) error {
	deadline := time.Now().Add(leadRecordWait)
	for {
		if rec, err := ReadLeadRecord(root); err == nil && rec.PID > 0 && rec.Start != "" && leadAlive(rec.PID) {
			if stamp, ok := leadStamp(rec.PID); ok && stamp == rec.Start {
				return &LeadRunningError{Root: root, Record: rec}
			}
		}
		if time.Now().After(deadline) {
			return &LeadRunningError{Root: root, Starting: true}
		}
		time.Sleep(20 * time.Millisecond)
	}
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
