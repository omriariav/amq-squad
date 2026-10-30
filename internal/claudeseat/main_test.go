package claudeseat

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain points the managed CLAUDE.md at a path that does not exist, so
// no row reads the host's (a row that needs one sets its own).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "claudeseat-managed-")
	if err != nil {
		panic(err)
	}
	managedMemory = filepath.Join(dir, "absent", "CLAUDE.md")
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
