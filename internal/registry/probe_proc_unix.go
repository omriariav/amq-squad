//go:build unix

package registry

import (
	"context"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"golang.org/x/sys/unix"
)

// ownProcessGroup starts the probe's pi as the leader of a new session (and
// so of a new process group), so its id names the session the model's
// processes start in.
func ownProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// procWatch tracks every process descended from the probe's pi: by parent
// chain (polled while the model runs, because an orphan is reparented and
// loses the chain) and by the probe's session id. A descendant that calls
// setsid leaves the group and the session but is still on the chain.
type procWatch struct {
	root  int
	mu    sync.Mutex
	known map[int]time.Time // pid -> start time when first seen (guards pid reuse)
	stop  chan struct{}
	done  chan struct{}
}

// pollInterval bounds the window in which a double fork plus setsid can
// leave the parent chain unseen.
const pollInterval = 20 * time.Millisecond

func watchDescendants(cmd *exec.Cmd) *procWatch {
	w := &procWatch{root: cmd.Process.Pid, known: map[int]time.Time{}, stop: make(chan struct{}), done: make(chan struct{})}
	w.add(w.root)
	go func() {
		defer close(w.done)
		t := time.NewTicker(pollInterval)
		defer t.Stop()
		for {
			w.scan()
			select {
			case <-w.stop:
				return
			case <-t.C:
			}
		}
	}()
	return w
}

func (w *procWatch) add(pid int) {
	if _, ok := w.known[pid]; ok {
		return
	}
	start, _ := procinfo.StartTime(pid)
	w.known[pid] = start
}

// scan adds every process whose parent is known, to a fixpoint, and every
// process in the probe's session.
func (w *procWatch) scan() {
	table := processTable()
	w.mu.Lock()
	defer w.mu.Unlock()
	for changed := true; changed; {
		changed = false
		for pid, ppid := range table {
			if _, ok := w.known[pid]; ok {
				continue
			}
			sid, _ := unix.Getsid(pid)
			if _, parentKnown := w.known[ppid]; parentKnown || sid == w.root {
				w.add(pid)
				changed = true
			}
		}
	}
}

// processTable is pid -> ppid for every process, from ps.
func processTable() map[int]int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "ps", "-A", "-o", "pid=,ppid=").Output()
	if err != nil {
		return nil
	}
	table := map[int]int{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 == nil && err2 == nil {
			table[pid] = ppid
		}
	}
	return table
}

// endProcessGroup stops the watch and kills every tracked descendant and
// every member of the probe's session, until none is alive. A pid whose
// start time changed since it was first seen is a reused pid and is left
// alone. Residual (same UID): a process that double-forks and calls setsid
// within one poll interval, after its parent exited, is not on the chain;
// and any process of the same user can reach the user's files.
func endProcessGroup(cmd *exec.Cmd, w *procWatch) error {
	if cmd.Process == nil {
		return nil
	}
	if w != nil {
		close(w.stop)
		<-w.done
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		alive := 0
		if w != nil {
			w.scan()
			w.mu.Lock()
			for pid, start := range w.known {
				if pid == cmd.Process.Pid {
					continue // reaped by Wait
				}
				if now, ok := procinfo.StartTime(pid); ok && !start.IsZero() && !now.Equal(start) {
					continue // the pid was reused
				}
				if syscall.Kill(pid, 0) == nil {
					alive++
					_ = syscall.Kill(pid, syscall.SIGKILL)
				}
			}
			w.mu.Unlock()
		}
		if err := syscall.Kill(-cmd.Process.Pid, 0); errors.Is(err, syscall.ESRCH) && alive == 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("the probe's processes did not all exit")
		}
		time.Sleep(pollInterval)
	}
}
