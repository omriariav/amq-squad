package schedproc

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// LiveSeat is a seat a previous scheduler incarnation launched whose process
// is still alive: the core-owned launch ledger (the accepted proof's pid and
// raw start stamp, internal/launch.ProofEntry) names it, and the process with
// that pid carries that stamp now (N2 of the 2b-2 milestone B second pass).
type LiveSeat struct {
	Handle string
	Pool   task.Pool
	// The proof's and the staged record's identities and the record's
	// worktree (3.0.1 t16: an adoption takes the seat only when the record
	// is the proof's own).
	ProofLaunchID, ProofRequestID   string
	RecordLaunchID, RecordRequestID string
	Worktree                        string
}

// startTokenOf is a test seam.
var startTokenOf = procinfo.StartToken

// LiveSeats reads the session root's launch ledger. A seat with no proof
// entry (the external stand-in's) is not listed: its runner reports it.
func LiveSeats(root string) []LiveSeat {
	dirs, _ := os.ReadDir(filepath.Join(root, "extensions", "amq-squad", "launch"))
	var out []LiveSeat
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		e, err := launch.ReadProof(root, d.Name())
		if err != nil || e.PID <= 1 || e.ClosedAt != nil || e.Handle != d.Name() {
			continue
		}
		if now, ok := startTokenOf(e.PID); !ok || now != e.Start {
			continue // gone, or the pid reused
		}
		seat := LiveSeat{Handle: e.Handle, ProofLaunchID: e.LaunchID, ProofRequestID: e.RequestID}
		if raw, err := os.ReadFile(filepath.Join(launch.StagedDir(root, e.Handle), launch.FileName)); err == nil {
			if rec, err := launch.Decode(raw); err == nil {
				seat.Pool = task.Pool{Runtime: rec.Runtime, Model: rec.Model, Effort: rec.Effort}
				seat.RecordLaunchID, seat.RecordRequestID, seat.Worktree = rec.LaunchID, rec.RequestID, rec.Worktree
			}
		}
		out = append(out, seat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}

// LiveSeatsStrict is LiveSeats for a caller that must not guess (uninstall):
// a seat whose entry cannot be read or parsed, whose entry names another
// handle, or whose process is alive but whose start-token probe fails is
// reported in problems, naming the pid, instead of being skipped as not
// live. probe is procinfo.StartToken when nil.
func LiveSeatsStrict(root string, probe func(pid int) (string, bool)) (live []LiveSeat, problems []string) {
	if probe == nil {
		probe = procinfo.StartToken
	}
	base := filepath.Join(root, "extensions", "amq-squad", "launch")
	dirs, err := os.ReadDir(base)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, []string{base + " cannot be listed: " + err.Error()}
	}
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		e, err := launch.ReadProof(root, d.Name())
		if err != nil {
			if !os.IsNotExist(err) {
				problems = append(problems, filepath.Join(base, d.Name(), "proof.json")+" cannot be read: "+err.Error())
			}
			continue
		}
		if e.PID <= 1 || e.ClosedAt != nil {
			continue
		}
		if e.Handle != d.Name() {
			problems = append(problems, filepath.Join(base, d.Name(), "proof.json")+" names another seat ("+e.Handle+")")
			continue
		}
		if !procinfo.Alive(e.PID) {
			continue
		}
		now, ok := probe(e.PID)
		if !ok {
			problems = append(problems, fmt.Sprintf("seat %s: pid %d is alive but its start stamp cannot be read", d.Name(), e.PID))
			continue
		}
		if now != e.Start {
			continue // the pid was reused
		}
		live = append(live, LiveSeat{Handle: e.Handle})
	}
	sort.Slice(live, func(i, j int) bool { return live[i].Handle < live[j].Handle })
	return live, problems
}
