//go:build linux

package procinfo

import (
	"os"
	"strconv"
	"strings"
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

// parentChildIndex builds a parent-pid -> child-pids map by scanning
// /proc/<pid>/stat (no fork). The comm field (2) is parenthesized and may
// contain spaces, so ppid is read relative to the LAST ')'.
func parentChildIndex() (map[int][]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	index := make(map[int][]int, len(entries))
	for _, e := range entries {
		pid, perr := strconv.Atoi(e.Name())
		if perr != nil || pid <= 0 {
			continue
		}
		buf, rerr := os.ReadFile("/proc/" + e.Name() + "/stat")
		if rerr != nil {
			continue
		}
		ppid, ok := ppidFromStat(string(buf))
		if ok && ppid > 0 {
			index[ppid] = append(index[ppid], pid)
		}
	}
	return index, nil
}

// ppidFromStat extracts the parent pid (field 4) from a /proc/<pid>/stat line,
// parsing after the comm field's closing paren.
func ppidFromStat(stat string) (int, bool) {
	close := strings.LastIndexByte(stat, ')')
	if close < 0 || close+1 >= len(stat) {
		return 0, false
	}
	fields := strings.Fields(stat[close+1:])
	// fields[0] = state, fields[1] = ppid
	if len(fields) < 2 {
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, false
	}
	return ppid, true
}
