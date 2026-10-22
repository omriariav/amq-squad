package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

func ensureLaunchDirectoryTracked(path string) ([]string, error) {
	path = filepath.Clean(path)
	var missing []string
	for current := path; ; current = filepath.Dir(current) {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return missing, fmt.Errorf("prepare AMQ root: %s exists and is not a directory", current)
			}
			break
		}
		if !os.IsNotExist(err) {
			return missing, fmt.Errorf("prepare AMQ root %s: %w", current, err)
		}
		missing = append(missing, current)
		parent := filepath.Dir(current)
		if parent == current {
			return missing, fmt.Errorf("prepare AMQ root %s: no existing ancestor", path)
		}
	}
	if err := os.MkdirAll(path, 0o755); err != nil {
		return missing, fmt.Errorf("prepare AMQ root %s: %w", path, err)
	}
	return missing, nil
}
