//go:build linux

package procinfo

import (
	"os"
	"strconv"
)

// readArgsNative reads pid's full command line from /proc/<pid>/cmdline — no
// fork, so it does not fail under fork/exec pressure the way `ps` does (#87).
func readArgsNative(pid int) (string, bool) {
	buf, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	if err != nil {
		return "", false
	}
	return parseProcCmdline(buf)
}
