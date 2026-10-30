package uninstall

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// schedLock is the scheduler's own lock (scheduler/run.lock), held by a
// writing uninstall for its whole run. run.lock is not in the removal set: the
// run unlinks it as its last action (finish), so no scheduler or second
// uninstall can lock a new inode while the run is still removing things. A
// lock file or directory this run had to create is removed again when the run
// is refused before it executes.
type schedLock struct {
	lock       *flock.Exclusive
	x          *fsanchor.AnchoredDir
	sd         *fsanchor.AnchoredDir
	created    bool
	dirCreated bool
	done       bool
}

// takeSchedulerLock takes the lock. It returns nil, "" when there is no
// extension directory (nothing can run), and a reason when the lock is held
// by a scheduler or another uninstall, or cannot be taken.
func takeSchedulerLock(o Options) (*schedLock, string) {
	x, err := openDir(o.Root, []string{extDir, amqDir})
	if errors.Is(err, errAbsent) {
		return nil, ""
	}
	xp := filepath.Join(o.Root, extDir, amqDir)
	if err != nil {
		return nil, xp + " cannot be opened: " + err.Error()
	}
	h := &schedLock{x: x}
	fail := func(why string) (*schedLock, string) { h.release(); return nil, why }
	sd, err := x.Sub("scheduler")
	if errors.Is(err, fs.ErrNotExist) {
		if sd, err = x.StageMkdir("scheduler", 0o700); err == nil {
			h.dirCreated = true
		}
	}
	if err != nil {
		return fail(filepath.Join(xp, "scheduler") + " cannot be opened: " + err.Error())
	}
	h.sd = sd
	lp := filepath.Join(xp, "scheduler", "run.lock")
	_, exists, kerr := kindOf(sd, "run.lock")
	if kerr != nil {
		return fail(lp + " cannot be opened: " + kerr.Error())
	}
	h.created = !exists
	f, err := sd.OpenLock("run.lock")
	if err != nil {
		return fail(lp + " cannot be opened: " + err.Error())
	}
	lock, acquired, err := flock.TryExclusiveFile(f)
	switch {
	case err != nil:
		return fail(fmt.Sprintf("%s cannot be locked: %v", lp, err))
	case !acquired:
		return fail("a live scheduler process (or another uninstall) holds " + lp)
	}
	h.lock = lock
	return h, ""
}

// release lets go of the lock; a lock file or directory this run created is
// removed again. A run that never executed leaves an existing lock file alone.
func (h *schedLock) release() { h.close(false) }

// finish is the last action of a run that executed: it unlinks run.lock
// (still holding it, so nothing created a second lock in between), then the
// scheduler directory when nothing else is left in it, and lets go.
func (h *schedLock) finish() { h.close(true) }

func (h *schedLock) close(final bool) {
	if h == nil || h.done {
		return
	}
	h.done = true
	if h.sd != nil && (final || h.created) {
		h.sd.StageRemoveAll("run.lock")
	}
	if h.lock != nil {
		h.lock.Close()
	}
	if h.sd != nil {
		if final || h.dirCreated {
			if names, err := h.sd.Names(); err == nil && len(names) == 0 {
				h.x.StageRemoveAll("scheduler")
			}
		}
		h.sd.Close()
	}
	if h.x != nil {
		h.x.Close()
	}
}
