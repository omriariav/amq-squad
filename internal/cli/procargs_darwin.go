//go:build darwin

package cli

import "golang.org/x/sys/unix"

// readProcArgsNative reads pid's full command line via the KERN_PROCARGS2
// sysctl — no fork, so it does not fail under fork/exec pressure the way `ps`
// does (#87).
func readProcArgsNative(pid int) (string, bool) {
	buf, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return "", false
	}
	return parseKernProcArgs2(buf)
}
