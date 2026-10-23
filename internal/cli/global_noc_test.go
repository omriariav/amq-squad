package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobalNOCRegistryRejectsSymlinkedMetadataDirectory(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, ".amq-squad")); err != nil {
		t.Fatal(err)
	}
	err := writeGlobalNOCRegistry(root, func(*globalNOCRegistry) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "non-symlink directory") {
		t.Fatalf("symlink registry error = %v", err)
	}
}
