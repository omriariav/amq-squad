package registry

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain runs the package's tests with TMPDIR at its real path: on macOS
// the default temp dir is under /var, a symlink to /private/var, and the
// fsanchor anchor (the runtime re-hash, the probe's agent dir) is opened
// from / one component at a time with O_NOFOLLOW (4-1 rebase), so a test
// HOME reached through that symlink would be refused like any other.
func TestMain(m *testing.M) {
	if real, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		_ = os.Setenv("TMPDIR", real)
	}
	os.Exit(m.Run())
}
