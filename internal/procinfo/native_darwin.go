//go:build darwin

package procinfo

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// readArgsNative reads pid's full command line via the KERN_PROCARGS2 sysctl —
// no fork, so it does not fail under fork/exec pressure the way `ps` does (#87).
func readArgsNative(pid int) (string, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", false
	}
	return parseKernProcArgs2(buf)
}

// readTTYNative resolves the process's controlling-terminal device from the
// fork-free KERN_PROC_PID sysctl, then maps that device to its stable /dev/tty
// path. A missing mapping fails closed rather than shelling out to ps.
func readTTYNative(pid int) (string, bool) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	// Darwin reports NODEV as a negative Tdev when the process has no
	// controlling terminal. Guard it before the uint32 conversion below so the
	// sentinel can never alias a real device number and manufacture authority.
	if err != nil || proc == nil || proc.Eproc.Tdev < 0 {
		return "", false
	}
	entries, err := os.ReadDir("/dev")
	if err != nil {
		return "", false
	}
	want := uint32(proc.Eproc.Tdev)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "tty") {
			continue
		}
		path := filepath.Join("/dev", entry.Name())
		var stat unix.Stat_t
		if unix.Stat(path, &stat) == nil && uint32(stat.Rdev) == want {
			return path, true
		}
	}
	return "", false
}

// parentChildIndex builds a parent-pid -> child-pids map from the KERN_PROC_ALL
// sysctl (no fork), so the tmux resolver can walk a pane's process subtree to a
// recorded agent pid without shelling `ps`.
func parentChildIndex() (map[int][]int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	index := make(map[int][]int, len(procs))
	for i := range procs {
		pid := int(procs[i].Proc.P_pid)
		ppid := int(procs[i].Eproc.Ppid)
		if pid > 0 && ppid > 0 {
			index[ppid] = append(index[ppid], pid)
		}
	}
	return index, nil
}
