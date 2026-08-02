package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// canonicalPathCasePortable reconstructs an existing absolute path using the
// names returned by its parent directories. Exact spellings take the cheap
// path. When a case-insensitive filesystem accepted another spelling, SameFile
// identifies the actual directory entry without treating an entire OS as
// case-insensitive or conflating distinct names on a case-sensitive volume.
func canonicalPathCasePortable(path string) string {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return path
	}

	rawVolume := filepath.VolumeName(path)
	volume := canonicalPathVolume(rawVolume)
	remainder := strings.TrimPrefix(path, rawVolume)
	remainder = strings.TrimLeftFunc(remainder, func(r rune) bool {
		return r <= 0xff && os.IsPathSeparator(uint8(r))
	})
	parts := strings.FieldsFunc(remainder, func(r rune) bool {
		return r <= 0xff && os.IsPathSeparator(uint8(r))
	})
	current := volume + string(filepath.Separator)
	for i, part := range parts {
		entries, err := os.ReadDir(current)
		if err != nil {
			return filepath.Join(append([]string{current}, parts[i:]...)...)
		}

		actual := part
		exact := false
		for _, entry := range entries {
			if entry.Name() == part {
				exact = true
				break
			}
		}
		if !exact {
			candidateInfo, statErr := os.Stat(filepath.Join(current, part))
			if statErr == nil {
				fallback := ""
				for _, entry := range entries {
					entryInfo, infoErr := entry.Info()
					if infoErr != nil || !os.SameFile(candidateInfo, entryInfo) {
						continue
					}
					if strings.EqualFold(entry.Name(), part) {
						actual = entry.Name()
						fallback = ""
						break
					}
					if fallback == "" {
						fallback = entry.Name()
					}
				}
				if fallback != "" {
					actual = fallback
				}
			}
		}
		current = filepath.Join(current, actual)
	}
	return current
}
