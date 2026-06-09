//go:build darwin

package procinfo

import "golang.org/x/sys/unix"

// readArgsNative reads pid's full command line via the KERN_PROCARGS2 sysctl —
// no fork, so it does not fail under fork/exec pressure the way `ps` does (#87).
func readArgsNative(pid int) (string, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", false
	}
	return parseKernProcArgs2(buf)
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
