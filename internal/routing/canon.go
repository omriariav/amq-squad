package routing

import (
	"errors"
	"fmt"
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

// MaxTreeEntries bounds the no-follow walk of a declared tree or glob; a
// larger tree is local-only (it could not be checked).
const MaxTreeEntries = 20_000

// ResolveRoot resolves the repository root's own ancestors once (a root
// under a symlinked directory such as macOS's /var), so every later walk
// starts from the real directory and never follows a link (Codex pass 2,
// P1-2). An unresolvable root stays as given.
func ResolveRoot(root string) string {
	if root == "" {
		return ""
	}
	if real, err := filepath.EvalSymlinks(root); err == nil {
		return real
	}
	return root
}

// Canonicalize resolves a declared path under root (already resolved by
// ResolveRoot). An empty root (unknown repository) leaves the spelling as
// declared. A declared tree or glob is expanded through a no-follow walk of
// the literal prefix's directory: a symlink inside it whose target leaves
// that directory makes the path local-only (Codex pass 2, P1-2).
func Canonicalize(root, declared string) Canonical {
	if root == "" {
		return Canonical{Path: declared}
	}
	tree := strings.HasSuffix(declared, "/")
	segs := strings.Split(strings.TrimSuffix(declared, "/"), "/")
	fold := CaseInsensitive(root)
	dir := root
	out := make([]string, 0, len(segs))
	expand := tree // a tree, or a glob below the literal prefix, is expanded
	for i, seg := range segs {
		if seg == "**" || strings.ContainsAny(seg, "*?[") {
			out = append(out, segs[i:]...)
			expand = true
			break
		}
		name, kind, ok := lookup(dir, seg, fold)
		if !ok {
			out = append(out, segs[i:]...)
			expand = false // nothing on disk below a missing component
			break
		}
		out = append(out, name)
		if kind&fs.ModeSymlink != 0 {
			return Canonical{Path: strings.Join(out, "/"), Alias: "symlink at " + strings.Join(out, "/")}
		}
		if !kind.IsDir() {
			out = append(out, segs[i+1:]...)
			expand = false
			break
		}
		dir = filepath.Join(dir, name)
	}
	p := strings.Join(out, "/")
	if tree {
		p += "/"
	}
	if expand {
		if why := treeLeaves(dir); why != "" {
			return Canonical{Path: p, Alias: why}
		}
	}
	return Canonical{Path: p}
}

// treeLeaves walks dir without following links and names the first symlink
// whose target leaves dir, or the walk's limit; "" when every link stays
// inside.
func treeLeaves(dir string) string {
	n := 0
	why := ""
	_ = filepath.WalkDir(dir, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			why = "cannot walk " + p
			return filepath.SkipAll
		}
		if n++; n > MaxTreeEntries {
			why = fmt.Sprintf("tree under %s has more than %d entries", dir, MaxTreeEntries)
			return filepath.SkipAll
		}
		if e.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		target, err := os.Readlink(p)
		if err != nil {
			why = "unreadable symlink at " + p
			return filepath.SkipAll
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		target = filepath.Clean(target)
		if rel, err := filepath.Rel(dir, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			why = "symlink at " + p + " leaves the declared tree"
			return filepath.SkipAll
		}
		return nil
	})
	return why
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
	if len(t.PrivacyPaths) == 0 {
		return grants.PrivacyVendor, ""
	}
	root = ResolveRoot(root)
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
	root = ResolveRoot(root)
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
