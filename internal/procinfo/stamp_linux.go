//go:build linux

package procinfo

import (
	"os"
	"strconv"
	"strings"
)

func readStartTokenNative(pid int) (string, bool) {
	buf, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	ticks, ok := processStartTicksFromStat(string(buf))
	if !ok {
		return "", false
	}
	return "linux-ticks:" + strconv.FormatUint(ticks, 10), true
}

func readParentNative(pid int) (int, bool) {
	buf, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0, false
	}
	return parentFromStat(string(buf))
}

// parentFromStat reads field 4 (ppid) after the parenthesized comm.
func parentFromStat(stat string) (int, bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 2 { // fields[0] is the state, fields[1] the ppid
		return 0, false
	}
	ppid, err := strconv.Atoi(fields[1])
	return ppid, err == nil
}
