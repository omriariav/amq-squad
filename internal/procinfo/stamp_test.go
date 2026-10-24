//go:build darwin || linux

package procinfo

import (
	"os"
	"strings"
	"testing"
)

// The raw start stamp of a live process is stable and in the raw form; the
// parent of this test process is its real parent.
func TestStartTokenIsRawAndStable(t *testing.T) {
	a, ok := StartToken(os.Getpid())
	b, _ := StartToken(os.Getpid())
	if !ok || a != b || !(strings.HasPrefix(a, "darwin-tv:") || strings.HasPrefix(a, "linux-ticks:")) {
		t.Fatalf("StartToken = %q %q %v", a, b, ok)
	}
	if ppid, ok := ParentPID(os.Getpid()); !ok || ppid != os.Getppid() {
		t.Fatalf("ParentPID = %d %v, want %d", ppid, ok, os.Getppid())
	}
}
