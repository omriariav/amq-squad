package procinfo

import (
	"encoding/binary"
	"os"
	"slices"
	"testing"
)

// Brief 31-3, door (a): team confirm reads its ancestors' initial
// environment from the kernel. The darwin buffer is argc, the exec path,
// its NUL padding, argv, then the environment up to an empty string (the
// apple[] strings after it are not environment); the linux buffer is
// NUL-separated. A live read of this process finds its own variable, and
// its session id is readable.
func TestEnvironReadsTheInitialEnvironment(t *testing.T) {
	var buf []byte
	buf = binary.LittleEndian.AppendUint32(buf, 2)
	buf = append(buf, "/usr/local/bin/amq-squad\x00\x00\x00amq-squad\x00lead\x00AM_ME=lead\x00HOME=/h\x00\x00executable_path=/x\x00"...)
	env, ok := parseKernProcArgs2Env(buf)
	if !ok || !slices.Equal(env, []string{"AM_ME=lead", "HOME=/h"}) {
		t.Fatalf("darwin env %q %v", env, ok)
	}
	if _, ok := parseKernProcArgs2Env(buf[:3]); ok {
		t.Fatal("a short buffer parsed")
	}
	if env, ok := parseProcEnviron([]byte("AM_ME=impl-1\x00PATH=/bin\x00")); !ok || !slices.Equal(env, []string{"AM_ME=impl-1", "PATH=/bin"}) {
		t.Fatalf("linux env %q %v", env, ok)
	}
	if _, ok := readEnvironNative(os.Getpid()); !ok {
		t.Skip("no fork-free environment read on this platform")
	}
	env, _ = Environ(os.Getpid())
	if !slices.ContainsFunc(env, func(kv string) bool { return len(kv) > 5 && kv[:5] == "PATH=" }) {
		t.Fatalf("this process's environment has no PATH: %d entries", len(env))
	}
	if sid, ok := SessionID(os.Getpid()); !ok || sid <= 0 {
		t.Fatalf("session id %d %v", sid, ok)
	}
}
