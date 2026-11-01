package procinfo

import (
	"encoding/binary"
	"os"
	"slices"
	"strings"
	"testing"
)

// Brief 31-3: the binary's under-a-lead check reads a pi ancestor's initial
// environment from the kernel. The darwin buffer is argc, the exec path,
// its NUL padding, argv, then the environment up to an empty string; the
// linux buffer is NUL-separated. A live read of this process finds its own
// PATH.
func TestEnvironReadsTheInitialEnvironment(t *testing.T) {
	var buf []byte
	buf = binary.LittleEndian.AppendUint32(buf, 2)
	buf = append(buf, "/rt/node\x00\x00\x00node\x00cli.js\x00AM_ME=lead\x00HOME=/h\x00\x00executable_path=/x\x00"...)
	if env, ok := parseKernProcArgs2Env(buf); !ok || !slices.Equal(env, []string{"AM_ME=lead", "HOME=/h"}) {
		t.Fatalf("darwin env %q %v", env, ok)
	}
	if _, ok := parseKernProcArgs2Env(buf[:3]); ok {
		t.Fatal("a short buffer parsed")
	}
	if env, ok := parseProcEnviron([]byte("AM_ME=impl-1\x00PATH=/bin\x00")); !ok || !slices.Equal(env, []string{"AM_ME=impl-1", "PATH=/bin"}) {
		t.Fatalf("linux env %q %v", env, ok)
	}
	env, ok := Environ(os.Getpid())
	if !ok {
		t.Skip("no fork-free environment read on this platform")
	}
	if !slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "PATH=") }) {
		t.Fatalf("this process's environment has no PATH: %d entries", len(env))
	}
}
