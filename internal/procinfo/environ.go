package procinfo

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// Environ returns pid's initial environment (the one it was exec'd with),
// each entry its own NAME=VALUE string, from the fork-free read only
// (darwin KERN_PROCARGS2, linux /proc/<pid>/environ); ok=false means it
// could not be read (another user's process, a process gone). A process
// cannot rewrite its ancestors' records, so `team confirm` reads its
// ancestors' AM_ME here (brief 31-3, door (a)).
func Environ(pid int) ([]string, bool) {
	if pid <= 0 {
		return nil, false
	}
	return readEnvironNative(pid)
}

// SessionID returns pid's session id (getsid(2)): the session leader's pid.
func SessionID(pid int) (int, bool) {
	if pid < 0 {
		return 0, false
	}
	return readSessionNative(pid)
}

// parseKernProcArgs2Env parses the environment that follows argv in a
// darwin KERN_PROCARGS2 buffer: NUL-terminated strings up to the first
// empty one (the apple[] strings follow it and are not environment).
func parseKernProcArgs2Env(buf []byte) ([]string, bool) {
	if len(buf) < 4 {
		return nil, false
	}
	argc := int(binary.LittleEndian.Uint32(buf[:4]))
	if argc <= 0 {
		return nil, false
	}
	p := buf[4:]
	i := bytes.IndexByte(p, 0)
	if i < 0 {
		return nil, false
	}
	p = p[i:]
	for len(p) > 0 && p[0] == 0 {
		p = p[1:]
	}
	for n := 0; n < argc; n++ {
		j := bytes.IndexByte(p, 0)
		if j < 0 {
			return nil, false
		}
		p = p[j+1:]
	}
	var env []string
	for len(p) > 0 {
		j := bytes.IndexByte(p, 0)
		if j <= 0 {
			break
		}
		env = append(env, string(p[:j]))
		p = p[j+1:]
	}
	return env, true
}

// parseProcEnviron parses a linux /proc/<pid>/environ buffer.
func parseProcEnviron(buf []byte) ([]string, bool) {
	trimmed := strings.TrimRight(string(buf), "\x00")
	if trimmed == "" {
		return []string{}, true
	}
	return strings.Split(trimmed, "\x00"), true
}

// HasTTY reports whether pid has a controlling terminal, from the kernel's
// process record (darwin kinfo_proc e_tdev, linux /proc/<pid>/stat tty_nr);
// ok=false when it cannot be read. The solo lead reads it for a peer of its
// start socket (the shell door), never for itself.
func HasTTY(pid int) (has bool, ok bool) {
	if pid <= 0 {
		return false, false
	}
	return readTTYNative(pid)
}

// ttyFromStat reads tty_nr (field 7) of a linux /proc/<pid>/stat line.
func ttyFromStat(stat string) (bool, bool) {
	close := strings.LastIndexByte(stat, ')')
	if close < 0 {
		return false, false
	}
	f := strings.Fields(stat[close+1:])
	if len(f) < 5 {
		return false, false
	}
	return f[4] != "0", true
}
