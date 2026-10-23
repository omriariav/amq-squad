package cli

import (
	"os"
	"path/filepath"
	"strings"
)

// Path normalization (#539, #540): one recorder and one comparator, and every
// path comparison goes through canonicalFilesystemPath; a second normalizer
// re-opens the class of bug where one path is recorded in one representation
// and compared in another.
//
// Two levels on purpose:
//   - RECORD (absoluteFilesystemPath, absoluteFilesystemPathIn): tilde-expanded,
//     absolute, cleaned, NOT symlink-resolved, so recorded paths stay what the
//     operator named (macOS /var stays /var) and digests do not move.
//   - COMPARE (canonicalFilesystemPath): also resolves symlinks and on-disk case,
//     so a symlink and its target compare equal however each was recorded.
//
// The anchored ...In form resolves a relative path against base (the project),
// not the process working directory (#538). When two writers disagree on a
// recorded path, fix their origin; never canonicalize at record time.

// absoluteFilesystemPath is the record normalization. It is total; empty input
// returns "", so callers that tell "unset" from "mismatch" must check for it.
func absoluteFilesystemPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				p = home
			} else {
				p = filepath.Join(home, p[2:])
			}
		}
	}
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return filepath.Clean(p)
}

// canonicalFilesystemPath is the compare normalization. For a missing leaf it
// resolves the longest existing ancestor and rejoins the rest, so /link/x and
// /real/x agree before x exists.
func canonicalFilesystemPath(p string) string {
	p = absoluteFilesystemPath(p)
	if p == "" {
		return ""
	}
	if resolved, err := resolveExistingFilesystemPath(p); err == nil {
		return resolved
	}
	remainder := ""
	dir := p
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return p
		}
		remainder = filepath.Join(filepath.Base(dir), remainder)
		dir = parent
		if resolved, err := resolveExistingFilesystemPath(dir); err == nil {
			return filepath.Clean(filepath.Join(resolved, remainder))
		}
	}
}

// resolveExistingFilesystemPath resolves symlinks and asks the OS for the
// on-disk case (EvalSymlinks keeps the caller's case on case-insensitive macOS).
func resolveExistingFilesystemPath(p string) (string, error) {
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	return filepath.Clean(canonicalPathCase(resolved)), nil
}

// absoluteFilesystemPathIn records a possibly relative path anchored to base
// (#538: a relative --cwd means relative to the project).
func absoluteFilesystemPathIn(base, p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if !filepath.IsAbs(p) && !strings.HasPrefix(p, "~") {
		if base = strings.TrimSpace(base); base != "" {
			p = filepath.Join(base, p)
		}
	}
	return absoluteFilesystemPath(p)
}

// sameResolvedDir reports whether two non-empty paths canonicalize equal
// (symlinked macOS TMPDIR and APFS case aliases included).
func sameResolvedDir(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return canonicalFilesystemPath(a) == canonicalFilesystemPath(b)
}
