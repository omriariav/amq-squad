package procinfo

import (
	"encoding/binary"
	"os"
	"runtime"
	"slices"
	"testing"
)

// Argv keeps each argument whole (brief 31-1's second PR: up compares a
// scheduler's argv exactly, so a path holding a space is one argument).
func TestArgvKeepsEachArgumentWhole(t *testing.T) {
	want := []string{"/usr/local/bin/amq-squad", "scheduler", "run", "--project", "/repos/my app"}
	buf := binary.LittleEndian.AppendUint32(nil, uint32(len(want)))
	buf = append(buf, "/usr/local/bin/amq-squad\x00\x00\x00"...)
	for _, a := range want {
		buf = append(append(buf, a...), 0)
	}
	buf = append(buf, "HOME=/h\x00"...)
	if got, ok := parseKernProcArgs2Argv(buf); !ok || !slices.Equal(got, want) {
		t.Fatalf("darwin argv %q %v", got, ok)
	}
	var cmdline []byte
	for _, a := range want {
		cmdline = append(append(cmdline, a...), 0)
	}
	if got, ok := parseProcCmdlineArgv(cmdline); !ok || !slices.Equal(got, want) {
		t.Fatalf("linux argv %q %v", got, ok)
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		if got, ok := Argv(os.Getpid()); !ok || !slices.Equal(got, os.Args) {
			t.Fatalf("Argv(self) %q %v, want %q", got, ok, os.Args)
		}
	}
}
