//go:build linux

package procinfo

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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

func readTTYNative(pid int) (string, bool) {
	buf, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", false
	}
	want, ok := ttyDeviceFromStat(string(buf))
	if !ok {
		return "", false
	}
	var candidates []string
	if path, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/fd/0"); err == nil {
		candidates = append(candidates, path)
	}
	for _, pattern := range []string{"/dev/tty*", "/dev/pts/*"} {
		matches, _ := filepath.Glob(pattern)
		candidates = append(candidates, matches...)
	}
	return ttyPathForDevice(want, candidates, linuxDeviceNumber)
}

// ttyDeviceFromStat parses tty_nr (field 7) relative to the closing paren of
// comm, whose contents may contain spaces and parentheses. tty_nr=0 means the
// process has no controlling terminal.
func ttyDeviceFromStat(stat string) (uint64, bool) {
	close := strings.LastIndexByte(stat, ')')
	if close < 0 || close+1 >= len(stat) {
		return 0, false
	}
	fields := strings.Fields(stat[close+1:])
	// fields[0]=state, [1]=ppid, [2]=pgrp, [3]=session, [4]=tty_nr.
	if len(fields) < 5 {
		return 0, false
	}
	encoded, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil || encoded == 0 {
		return 0, false
	}
	return uint64(uint32(encoded)), true
}

func linuxDeviceNumber(path string) (uint64, bool) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || !strings.HasPrefix(clean, "/dev/") || strings.HasSuffix(path, " (deleted)") {
		return 0, false
	}
	info, err := os.Stat(clean)
	if err != nil {
		return 0, false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return stat.Rdev, true
}

func ttyPathForDevice(want uint64, candidates []string, deviceNumber func(string) (uint64, bool)) (string, bool) {
	if want == 0 || deviceNumber == nil {
		return "", false
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidates {
		clean := filepath.Clean(candidate)
		if !filepath.IsAbs(clean) || !strings.HasPrefix(clean, "/dev/") {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		if got, ok := deviceNumber(clean); ok && got == want {
			return clean, true
		}
	}
	return "", false
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
