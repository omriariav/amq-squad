package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func shortTestTempDir(t *testing.T, pattern string) string {
	t.Helper()
	root := os.TempDir()
	if resolved, err := filepath.EvalSymlinks("/tmp"); err == nil {
		root = resolved
	}
	dir, err := os.MkdirTemp(root, pattern)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
