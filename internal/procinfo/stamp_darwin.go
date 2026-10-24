//go:build darwin

package procinfo

import (
	"strconv"

	"golang.org/x/sys/unix"
)

func readStartTokenNative(pid int) (string, bool) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || proc == nil || proc.Proc.P_starttime.Sec <= 0 {
		return "", false
	}
	tv := proc.Proc.P_starttime
	return "darwin-tv:" + strconv.FormatInt(tv.Sec, 10) + "." + strconv.FormatInt(int64(tv.Usec), 10), true
}

func readParentNative(pid int) (int, bool) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || proc == nil {
		return 0, false
	}
	return int(proc.Eproc.Ppid), true
}

func socketPeerNative(fd int) (int, bool) {
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	return pid, err == nil && pid > 0
}
