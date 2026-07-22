// Package procinfo is the single, shared process-liveness probe used by every
// amq-squad surface (internal/cli status/resume/doctor/preflight and
// internal/state's status board + NOC snapshots). Centralizing it guarantees
// those surfaces can never disagree about whether a PID is alive.
//
// Liveness is read WITHOUT forking where possible: PID liveness via signal-0,
// and the process command line via the darwin KERN_PROCARGS2 sysctl or linux
// /proc/<pid>/cmdline. The previous fork-based `ps` read returned EAGAIN under
// fork/exec pressure (a large process table), which demoted a LIVE agent/wake
// to stale on some surfaces but not others (#87). `ps -ww` remains a fallback.
package procinfo

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// psArgsAttempts bounds how many times the ps FALLBACK re-reads when it fails to
// RUN (a transient fork/resource error under load) before giving up.
const psArgsAttempts = 3

// argsNative reads a process's full command line WITHOUT forking. It is a
// package var so tests can stub it; ok=false means the native path is
// unavailable or failed, and Args then falls back to ps.
var argsNative = readArgsNative

// TTY returns the exact controlling terminal endpoint for pid without
// spawning a helper process. ok=false means the platform cannot observe one
// or the process has no controlling terminal.
func TTY(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	return readTTYNative(pid)
}

// Alive reports whether pid is a live process via signal-0. EPERM means the
// process EXISTS but is owned by another user (POSIX guarantees the target
// exists when EPERM is returned), so it counts as alive; only ESRCH/other mean
// gone. Treating EPERM as dead previously mislabeled live cross-user/sandboxed
// agents as stale (#87).
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return signalErrMeansAlive(proc.Signal(syscall.Signal(0)))
}

func signalErrMeansAlive(err error) bool {
	return err == nil || errors.Is(err, syscall.EPERM)
}

// Match reads pid's command line (fork-free where possible) and applies the
// predicate. Returns false when pid is invalid, the predicate is nil, the args
// cannot be read at all, or the predicate rejects them.
func Match(pid int, predicate func(args string) bool) bool {
	if pid <= 0 || predicate == nil {
		return false
	}
	args, ok := Args(pid)
	if !ok {
		return false
	}
	return predicate(args)
}

// ChildrenIndex takes one fork-free snapshot of the process table and returns a
// function mapping a pid to its IMMEDIATE child pids (suitable as the pidTree
// seam for tmux pane resolution: walking a pane's pane_pid down to an agent
// pid). The returned function is a closure over the snapshot, so repeated
// lookups do not re-read the table. err is non-nil when the table cannot be
// read (e.g. unsupported platform); callers should treat that as "no pid tree"
// and degrade to non-pid resolution rather than failing.
func ChildrenIndex() (func(pid int) []int, error) {
	index, err := parentChildIndex()
	if err != nil {
		return nil, err
	}
	return func(pid int) []int { return index[pid] }, nil
}

// Args returns pid's full command line, fork-free where possible (darwin sysctl
// / linux /proc), falling back to ps (with retry on transient fork failures)
// otherwise. ok=false means it could not be read at all.
func Args(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	if args, ok := argsNative(pid); ok {
		return args, true
	}
	return readArgsWithRetry(pid, readPSArgs)
}

// readPSArgs reads pid's command line via ps. -ww disables column truncation.
// ran is true when ps actually executed (so a nil-args/non-zero result is a
// DEFINITIVE "no such pid"); ran is false when ps could not be started (a
// transient condition worth retrying).
func readPSArgs(pid int) (args string, ran bool, err error) {
	out, e := exec.Command("ps", "-ww", "-o", "args=", "-p", fmt.Sprintf("%d", pid)).Output()
	if e == nil {
		return strings.TrimSpace(string(out)), true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(e, &exitErr) {
		return "", true, e // ps ran and exited non-zero: the pid does not exist.
	}
	return "", false, e // ps could not be started (e.g. fork: EAGAIN).
}

// readArgsWithRetry retries the reader only when it could not RUN (transient);
// a definitive "ran but pid absent" is not retried.
func readArgsWithRetry(pid int, read func(int) (string, bool, error)) (string, bool) {
	for attempt := 0; attempt < psArgsAttempts; attempt++ {
		args, ran, err := read(pid)
		if err == nil {
			return args, true
		}
		if ran {
			return "", false
		}
	}
	return "", false
}
