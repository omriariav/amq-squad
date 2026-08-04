package cli

import (
	"fmt"
	"os"
	"strings"
)

func historyProjectDirs(projectFlag string) ([]string, error) {
	if projectFlag == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("getwd: %w", err)
		}
		return []string{cwd}, nil
	}
	var out []string
	for _, d := range strings.Split(projectFlag, ",") {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		dir, err := resolveProjectDirFlag("", d, true)
		if err != nil {
			return nil, err
		}
		out = append(out, dir)
	}
	if len(out) == 0 {
		return nil, usageErrorf("--project requires at least one directory")
	}
	return out, nil
}
