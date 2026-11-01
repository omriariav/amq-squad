package procinfo

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// Environ returns pid's initial environment (the one it was exec'd with),
// each entry its own NAME=VALUE string, from the fork-free read only
// (darwin KERN_PROCARGS2, linux /proc/<pid>/environ); ok=false means it
// could not be read (another user's process, a process gone). The binary's
// under-a-lead check reads a pi ancestor's AMQ identity here (brief 31-3).
func Environ(pid int) ([]string, bool) {
	if pid <= 0 {
		return nil, false
	}
	return readEnvironNative(pid)
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
