package uninstall

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
)

// DecisionSocketName is the scheduler's decision socket in the launch
// directory (internal/schedproc.DecisionSocketName; the name is repeated so
// the check reads the directory without the scheduler's constants, and a
// test holds the two together).
const DecisionSocketName = "scheduler-decide.sock"

// busy reports everything that is live: the scheduler process (its lock or
// its decision socket), a live seat, an open proof window and a commit step
// in the exits queue. It fails closed: every read, open, list or probe error
// is reported as busy with the path named, and unsupported locking is busy.
// held is true when this run already holds the scheduler lock itself.
func busy(o Options, held bool) []string {
	x, err := openDir(o.Root, []string{extDir, amqDir})
	if errors.Is(err, errAbsent) {
		return nil
	}
	xp := filepath.Join(o.Root, extDir, amqDir)
	if err != nil {
		return []string{xp + " cannot be opened: " + err.Error()}
	}
	defer x.Close()
	var live []string
	sd, serr := x.Sub("scheduler")
	switch {
	case serr == nil:
		live = append(live, schedulerLive(xp, x, sd, held)...)
		live = append(live, commitSteps(xp, sd)...)
		sd.Close()
	case !errors.Is(serr, fs.ErrNotExist):
		live = append(live, filepath.Join(xp, "scheduler")+" cannot be opened: "+serr.Error())
	}
	ld, lerr := x.Sub("launch")
	switch {
	case lerr == nil:
		live = append(live, seatsLive(xp, o.Root, ld, o.now())...)
		ld.Close()
	case !errors.Is(lerr, fs.ErrNotExist):
		live = append(live, filepath.Join(xp, "launch")+" cannot be opened: "+lerr.Error())
	}
	return live
}

func schedulerLive(xp string, x, sd *fsanchor.AnchoredDir, held bool) []string {
	var live []string
	lockPath := filepath.Join(xp, "scheduler", "run.lock")
	if !held {
		f, kind, _, _, err := sd.OpenEntry("run.lock")
		switch {
		case err != nil && errors.Is(err, fs.ErrNotExist):
		case err != nil:
			live = append(live, lockPath+" cannot be opened: "+err.Error())
		case kind != fsanchor.KindRegular:
			if f != nil {
				f.Close()
			}
			live = append(live, lockPath+" is not a regular file")
		default:
			lock, acquired, lerr := flock.TryExclusiveFile(f)
			switch {
			case lerr != nil:
				live = append(live, lockPath+" cannot be probed: "+lerr.Error())
			case !acquired:
				live = append(live, "a live scheduler process (or another uninstall) holds "+lockPath)
			default:
				lock.Close()
			}
		}
	}
	ld, err := x.Sub("launch")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return live
		}
		return append(live, filepath.Join(xp, "launch")+" cannot be opened: "+err.Error())
	}
	defer ld.Close()
	names, err := ld.Names()
	if err != nil {
		return append(live, filepath.Join(xp, "launch")+" cannot be listed: "+err.Error())
	}
	for _, n := range names {
		if n != DecisionSocketName {
			continue
		}
		var rec struct {
			PID int `json:"pid"`
		}
		raw, rerr := fsanchor.ReadEntry(sd, "launch.json")
		if rerr != nil || json.Unmarshal(raw, &rec) != nil {
			live = append(live, "the scheduler's decision socket exists and "+filepath.Join(xp, "scheduler", "launch.json")+" is unreadable")
		} else if procinfo.Alive(rec.PID) {
			live = append(live, fmt.Sprintf("the scheduler's decision socket is open (pid %d)", rec.PID))
		}
	}
	return live
}

func commitSteps(xp string, sd *fsanchor.AnchoredDir) []string {
	path := filepath.Join(xp, "scheduler", "exits.json")
	raw, err := fsanchor.ReadEntry(sd, "exits.json")
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return []string{path + " cannot be read, so a commit step cannot be ruled out: " + err.Error()}
	}
	var q struct {
		Queue []struct {
			Handle string `json:"handle"`
			TaskID string `json:"task_id"`
			Why    string `json:"why"`
		} `json:"queue"`
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		return []string{path + " cannot be parsed, so a commit step cannot be ruled out"}
	}
	var live []string
	for _, it := range q.Queue {
		if it.Why == "commit" {
			live = append(live, fmt.Sprintf("a commit step of seat %s (task %s) is in the exits queue", it.Handle, it.TaskID))
		}
	}
	return live
}

// seatsLive reads every seat's proof entry and staged record strictly (an
// error is busy) and asks the scheduler's own LiveSeats, with its pid and
// start-stamp match, which seats are alive.
func seatsLive(xp, root string, ld *fsanchor.AnchoredDir, now time.Time) []string {
	var live []string
	names, err := ld.Names()
	if err != nil {
		return []string{filepath.Join(xp, "launch") + " cannot be listed: " + err.Error()}
	}
	for _, h := range names {
		if h == DecisionSocketName {
			continue
		}
		hd, err := ld.Sub(h)
		if err != nil {
			if errors.Is(err, syscall.ENOTDIR) {
				continue // a file in the launch directory, not a seat
			}
			live = append(live, filepath.Join(xp, "launch", h)+" cannot be opened: "+err.Error())
			continue
		}
		hp := filepath.Join(xp, "launch", h)
		var e launch.ProofEntry
		if raw, err := fsanchor.ReadEntry(hd, "proof.json"); err == nil {
			if json.Unmarshal(raw, &e) != nil {
				live = append(live, hp+"/proof.json cannot be parsed, so the seat cannot be ruled live")
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			live = append(live, hp+"/proof.json cannot be read: "+err.Error())
		}
		if raw, err := fsanchor.ReadEntry(hd, launch.FileName); err == nil {
			var rec launch.Record
			if json.Unmarshal(raw, &rec) != nil {
				live = append(live, hp+"/"+launch.FileName+" cannot be parsed, so its proof window cannot be ruled out")
			} else if t := rec.Proof; t != nil && t.Token != "" && t.Token != launch.TokenConsumed && t.Token != launch.TokenExpired && now.Before(t.IssuedAt.Add(launch.ProofTTL)) {
				live = append(live, fmt.Sprintf("the proof window of seat %s is open", h))
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			live = append(live, hp+"/"+launch.FileName+" cannot be read: "+err.Error())
		}
		hd.Close()
	}
	for _, s := range schedproc.LiveSeats(root) {
		live = append(live, fmt.Sprintf("seat %s is live", s.Handle))
	}
	return live
}
