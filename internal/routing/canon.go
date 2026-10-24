package routing

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// Path rules (privacy_paths, sensitive_paths) match a task's declared files
// on their canonical repository path, not on the spelling the lead declared
// (Codex pass 1, P1-2): the literal prefix of each declared path is walked
// from the repository root one component at a time without following a
// symlink, each component takes its on-disk name, and on a case-insensitive
// filesystem the match is case-folded. A declared path that passes through
// a symlink may resolve anywhere, inside or outside the repository, so it is
// local-only by default. A component that does not exist yet (a new file)
// keeps its spelling, case-folded where the filesystem folds.

// Canonical is one declared file resolved against the repository.
type Canonical struct {
	Path string // the canonical repository-relative path (declared glob tail kept)
	// Alias names why the path cannot be resolved safely ("symlink at
	// internal/keys"); a non-empty Alias makes the path local-only.
	Alias string
}

// Canonicalize resolves a declared path under root. An empty root (unknown
// repository) leaves the spelling as declared.
func Canonicalize(root, declared string) Canonical {
	if root == "" {
		return Canonical{Path: declared}
	}
	tree := strings.HasSuffix(declared, "/")
	segs := strings.Split(strings.TrimSuffix(declared, "/"), "/")
	fold := CaseInsensitive(root)
	dir := root
	out := make([]string, 0, len(segs))
	for i, seg := range segs {
		if seg == "**" || strings.ContainsAny(seg, "*?[") {
			out = append(out, segs[i:]...)
			break
		}
		name, kind, ok := lookup(dir, seg, fold)
		if !ok {
			out = append(out, segs[i:]...)
			break
		}
		out = append(out, name)
		if kind&fs.ModeSymlink != 0 {
			return Canonical{Path: strings.Join(out, "/"), Alias: "symlink at " + strings.Join(out, "/")}
		}
		if !kind.IsDir() {
			out = append(out, segs[i+1:]...)
			break
		}
		dir = filepath.Join(dir, name)
	}
	p := strings.Join(out, "/")
	if tree {
		p += "/"
	}
	return Canonical{Path: p}
}

// lookup finds seg in dir by its directory entry (never by following a
// link): the exact name, else on a folding filesystem the case-folded one.
func lookup(dir, seg string, fold bool) (string, fs.FileMode, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", 0, false
	}
	for _, e := range entries {
		if e.Name() == seg {
			return e.Name(), e.Type(), true
		}
	}
	if fold {
		for _, e := range entries {
			if strings.EqualFold(e.Name(), seg) {
				return e.Name(), e.Type(), true
			}
		}
	}
	return "", 0, false
}

// CaseInsensitive reports whether the filesystem under root folds case: the
// root's own name with its letters' case swapped names the same directory.
// A root without letters uses the platform default (macOS and Windows fold).
func CaseInsensitive(root string) bool {
	base := filepath.Base(root)
	swapped := []rune(base)
	letters := false
	for i, c := range swapped {
		switch {
		case unicode.IsUpper(c):
			swapped[i], letters = unicode.ToLower(c), true
		case unicode.IsLower(c):
			swapped[i], letters = unicode.ToUpper(c), true
		}
	}
	if !letters {
		return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
	}
	a, err := os.Stat(root)
	if err != nil {
		return runtime.GOOS == "darwin" || runtime.GOOS == "windows"
	}
	b, err := os.Stat(filepath.Join(filepath.Dir(root), string(swapped)))
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	return err == nil && os.SameFile(a, b)
}

// RequiredPrivacyAt is RequiredPrivacy on canonical paths under the
// repository root: local when a declared file passes through a symlink, or
// its canonical path may touch a local glob (case-folded where the
// filesystem folds); else vendor. It returns the class and what made it
// local (the glob, or the alias).
func (t Table) RequiredPrivacyAt(root string, files []string) (string, string) {
	fold := root != "" && CaseInsensitive(root) || root == "" && (runtime.GOOS == "darwin" || runtime.GOOS == "windows")
	canon := make([]string, 0, len(files))
	for _, f := range files {
		c := Canonicalize(root, f)
		if c.Alias != "" && len(t.PrivacyPaths) > 0 {
			return grants.PrivacyLocal, c.Alias
		}
		canon = append(canon, c.Path)
	}
	for _, r := range t.PrivacyPaths {
		if r.Class == grants.PrivacyLocal && mayTouchFold(canon, r.Glob, fold) {
			return grants.PrivacyLocal, r.Glob
		}
	}
	return grants.PrivacyVendor, ""
}

// SensitiveAt is Sensitive on canonical paths; a path through a symlink is
// sensitive when any sensitive path is set.
func (t Table) SensitiveAt(root string, files []string) bool {
	if len(t.SensitivePaths) == 0 {
		return false
	}
	fold := root != "" && CaseInsensitive(root) || root == "" && (runtime.GOOS == "darwin" || runtime.GOOS == "windows")
	for _, f := range files {
		c := Canonicalize(root, f)
		if c.Alias != "" {
			return true
		}
		for _, g := range t.SensitivePaths {
			if mayTouchFold([]string{c.Path}, g, fold) {
				return true
			}
		}
	}
	return false
}

func mayTouchFold(files []string, glob string, fold bool) bool {
	if fold {
		glob = strings.ToLower(glob)
		low := make([]string, len(files))
		for i, f := range files {
			low[i] = strings.ToLower(f)
		}
		files = low
	}
	return mayTouch(files, glob)
}
