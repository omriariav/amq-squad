package cli

import (
	"bytes"
	"encoding/binary"
	"strings"
)

// parseKernProcArgs2 parses a darwin KERN_PROCARGS2 sysctl buffer into the
// process's argv joined by spaces, matching `ps -o args=`. Layout:
//
//	int32 argc | exec_path\0 | \0 padding | argc x (argv\0) | env...
//
// Returns ok=false on a malformed/short buffer. Kept platform-neutral (no
// syscalls) so it is unit-testable on any OS.
func parseKernProcArgs2(buf []byte) (string, bool) {
	if len(buf) < 4 {
		return "", false
	}
	argc := int(binary.LittleEndian.Uint32(buf[:4])) // darwin is little-endian
	if argc <= 0 {
		return "", false
	}
	p := buf[4:]
	// Skip the exec path up to its NUL, then the run of NUL padding before argv.
	i := bytes.IndexByte(p, 0)
	if i < 0 {
		return "", false
	}
	p = p[i:]
	for len(p) > 0 && p[0] == 0 {
		p = p[1:]
	}
	args := make([]string, 0, argc)
	for n := 0; n < argc && len(p) > 0; n++ {
		j := bytes.IndexByte(p, 0)
		if j < 0 {
			args = append(args, string(p))
			break
		}
		args = append(args, string(p[:j]))
		p = p[j+1:]
	}
	if len(args) == 0 {
		return "", false
	}
	return strings.Join(args, " "), true
}

// parseProcCmdline parses a linux /proc/<pid>/cmdline buffer (NUL-separated
// argv) into a space-joined command line.
func parseProcCmdline(buf []byte) (string, bool) {
	if len(buf) == 0 {
		return "", false
	}
	parts := strings.Split(strings.TrimRight(string(buf), "\x00"), "\x00")
	out := strings.TrimSpace(strings.Join(parts, " "))
	if out == "" {
		return "", false
	}
	return out, true
}
