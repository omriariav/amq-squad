package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func snapshotTestTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		switch {
		case info.Mode().IsRegular():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += "\x00" + string(body)
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += "\x00" + target
		}
		out[rel] = value
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot tree %s: %v", root, err)
	}
	return out
}
