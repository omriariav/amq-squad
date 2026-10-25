package uninstall

import (
	"errors"
	"fmt"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// DecisionSocketName is the scheduler's decision socket in the launch
// directory (internal/schedproc.DecisionSocketName; the name is repeated so
// this package stays free of the scheduler's dependencies, and a test holds
// the two together).
const DecisionSocketName = "scheduler-decide.sock"

// busy reports everything that is live: the scheduler process (its lock or
// its decision socket), a live seat (an accepted proof whose process is
// alive), an open proof window (a staged ticket not used up or expired) and
// a commit step in the exits queue (a `why: commit` entry). An unreadable
// state file is reported too: uninstall fails closed.
func busy(o Options) ([]string, error) {
	var live []string
	x, err := openDir(o.Root, []string{extDir, amqDir})
	if errors.Is(err, errAbsent) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer x.Close()
	if sd, ok := subOf(x, "scheduler"); ok {
		live = append(live, schedulerLive(x, sd)...)
		live = append(live, commitSteps(sd)...)
		sd.Close()
	}
	if ld, ok := subOf(x, "launch"); ok {
		live = append(live, seatsLive(ld, o.now())...)
		ld.Close()
	}
	return live, nil
}

func schedulerLive(x, sd *fsanchor.AnchoredDir) []string {
	var live []string
	if f, kind, _, _, err := sd.OpenEntry("run.lock"); err == nil && f != nil {
		if kind == fsanchor.KindRegular {
			lock, acquired, lerr := flock.TryExclusiveFile(f)
			switch {
			case errors.Is(lerr, flock.ErrUnsupported):
			case lerr != nil:
				live = append(live, "the scheduler lock cannot be probed")
			case !acquired:
				live = append(live, "a live scheduler process holds scheduler/run.lock")
			default:
				lock.Close()
			}
		} else {
			f.Close()
		}
	}
	if ld, ok := subOf(x, "launch"); ok {
		defer ld.Close()
		names, _ := ld.Names()
		for _, n := range names {
			if n != DecisionSocketName {
				continue
			}
			var rec struct {
				PID int `json:"pid"`
			}
			if !readJSON(sd, "launch.json", &rec) {
				live = append(live, "the scheduler's decision socket exists and its launch record is unreadable")
			} else if procinfo.Alive(rec.PID) {
				live = append(live, fmt.Sprintf("the scheduler's decision socket is open (pid %d)", rec.PID))
			}
		}
	}
	return live
}

func commitSteps(sd *fsanchor.AnchoredDir) []string {
	if _, ok := kindOf(sd, "exits.json"); !ok {
		return nil
	}
	var q struct {
		Queue []struct {
			Handle string `json:"handle"`
			TaskID string `json:"task_id"`
			Why    string `json:"why"`
		} `json:"queue"`
	}
	if !readJSON(sd, "exits.json", &q) {
		return []string{"the exits queue (scheduler/exits.json) is unreadable, so a commit step cannot be ruled out"}
	}
	var live []string
	for _, it := range q.Queue {
		if it.Why == "commit" {
			live = append(live, fmt.Sprintf("a commit step of seat %s (task %s) is in the exits queue", it.Handle, it.TaskID))
		}
	}
	return live
}

func seatsLive(ld *fsanchor.AnchoredDir, now time.Time) []string {
	var live []string
	for _, h := range sortedNames(ld) {
		hd, ok := subOf(ld, h)
		if !ok {
			continue
		}
		var e launch.ProofEntry
		if readJSON(hd, "proof.json", &e) && e.ClosedAt == nil && e.PID > 1 {
			if tok, ok := procinfo.StartToken(e.PID); ok && tok == e.Start {
				live = append(live, fmt.Sprintf("seat %s is live (pid %d)", h, e.PID))
			}
		}
		var rec launch.Record
		if readJSON(hd, launch.FileName, &rec) && rec.Proof != nil {
			t := rec.Proof
			if t.Token != "" && t.Token != launch.TokenConsumed && t.Token != launch.TokenExpired && now.Before(t.IssuedAt.Add(launch.ProofTTL)) {
				live = append(live, fmt.Sprintf("the proof window of seat %s is open", h))
			}
		}
		hd.Close()
	}
	return live
}
