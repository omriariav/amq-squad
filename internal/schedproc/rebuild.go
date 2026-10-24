package schedproc

import (
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
		seat := LiveSeat{Handle: e.Handle}
		if raw, err := os.ReadFile(filepath.Join(launch.StagedDir(root, e.Handle), launch.FileName)); err == nil {
			if rec, err := launch.Decode(raw); err == nil {
				seat.Pool = task.Pool{Runtime: rec.Runtime, Model: rec.Model, Effort: rec.Effort}
			}
		}
		out = append(out, seat)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}
