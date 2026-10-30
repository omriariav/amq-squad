//go:build darwin

package procinfo

import (
	"time"

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

func readStartTimeNative(pid int) (time.Time, bool) {
	proc, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil || proc == nil {
		return time.Time{}, false
	}
	start := proc.Proc.P_starttime
	if start.Sec <= 0 {
		return time.Time{}, false
	}
	return time.Unix(start.Sec, int64(start.Usec)*int64(time.Microsecond)).UTC(), true
}
