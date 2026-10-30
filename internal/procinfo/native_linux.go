//go:build linux

package procinfo

import (
	"os"
	"strconv"
	"strings"
	"time"
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

func readStartTimeNative(pid int) (time.Time, bool) {
	buf, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return time.Time{}, false
	}
	ticks, ok := processStartTicksFromStat(string(buf))
	if !ok {
		return time.Time{}, false
	}
	procStat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, false
	}
	boot, ok := bootTimeFromProcStat(string(procStat))
	if !ok {
		return time.Time{}, false
	}
	// Linux exposes process start time in USER_HZ ticks. USER_HZ is the stable
	// procfs ABI value 100, independent of the kernel's internal timer rate.
	const userHZ = uint64(100)
	seconds := ticks / userHZ
	nanos := (ticks % userHZ) * uint64(time.Second) / userHZ
	return time.Unix(boot+int64(seconds), int64(nanos)).UTC(), true
}

func processStartTicksFromStat(stat string) (uint64, bool) {
	close := strings.LastIndexByte(stat, ')')
	if close < 0 || close+1 >= len(stat) {
		return 0, false
	}
	fields := strings.Fields(stat[close+1:])
	// fields[0] is field 3 (state); field 22 (starttime) is index 19.
	if len(fields) <= 19 {
		return 0, false
	}
	ticks, err := strconv.ParseUint(fields[19], 10, 64)
	return ticks, err == nil
}

func bootTimeFromProcStat(stat string) (int64, bool) {
	for _, line := range strings.Split(stat, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "btime" {
			continue
		}
		seconds, err := strconv.ParseInt(fields[1], 10, 64)
		return seconds, err == nil && seconds > 0
	}
	return 0, false
}
