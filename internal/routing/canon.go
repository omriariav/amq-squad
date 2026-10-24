package routing

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
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
	// Refuse names why the path could not be checked at all (a tree over
	// MaxTreeEntries, a walk error); the router blocks the task with it.
	Refuse string
}

// BlockTreeUnchecked blocks a task whose declared tree could not be checked
// for links (Codex pass 3, ruling 6): the lead narrows the declared files.
const BlockTreeUnchecked = "declared_tree_unchecked"

// Unchecked names the first declared path whose tree could not be checked
// under the table's path rules, "" when every path was checked or no path
// rule exists.
func (t Table) Unchecked(root string, files []string) string {
	if len(t.PrivacyPaths) == 0 && len(t.SensitivePaths) == 0 {
		return ""
	}
	root = ResolveRoot(root)
	for _, f := range files {
		if c := Canonicalize(root, f); c.Refuse != "" {
			return f + ": " + c.Refuse
		}
	}
	return ""
}

// MaxTreeEntries bounds the no-follow walk of a declared tree or glob; a
// larger tree is local-only (it could not be checked).
var MaxTreeEntries = 20_000

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
// declared. The walk runs on internal/fsanchor at the rebase (Codex pass 2,
// P1-2): the root is opened from / one component at a time with O_NOFOLLOW,
// and each component through its parent's descriptor; a symlink never
// opens, and readlinkat tells it from an unreadable entry. A declared tree
// or glob is expanded the same way below its literal prefix: a symlink
// inside it whose target leaves that directory makes the path local-only.
func Canonicalize(root, declared string) Canonical {
	if root == "" {
		return Canonical{Path: declared}
	}
	tree := strings.HasSuffix(declared, "/")
	segs := strings.Split(strings.TrimSuffix(declared, "/"), "/")
	fold := CaseInsensitive(root)
	d, err := fsanchor.OpenAnchored(root, nil, false)
	if err != nil {
		return Canonical{Path: declared, Refuse: "cannot open the repository root without following a link: " + err.Error()}
	}
	defer func() { d.Close() }()
	dirAbs := root
	out := make([]string, 0, len(segs))
	expand := tree // a tree, or a glob below the literal prefix, is expanded
	for i, seg := range segs {
		if seg == "**" || strings.ContainsAny(seg, "*?[") {
			out = append(out, segs[i:]...)
			expand = true
			break
		}
		name, ok, err := lookup(d, seg, fold)
		if err != nil {
			return Canonical{Path: declared, Refuse: "cannot list " + dirAbs + ": " + err.Error()}
		}
		if !ok {
			out = append(out, segs[i:]...)
			expand = false // nothing on disk below a missing component
			break
		}
		out = append(out, name)
		f, kind, _, _, oerr := d.OpenEntry(name)
		if oerr != nil {
			if _, lerr := d.Readlink(name); lerr == nil {
				return Canonical{Path: strings.Join(out, "/"), Alias: "symlink at " + strings.Join(out, "/")}
			}
			return Canonical{Path: declared, Refuse: "cannot open " + filepath.Join(dirAbs, name) + ": " + oerr.Error()}
		}
		if kind != fsanchor.KindDir {
			f.Close()
			out = append(out, segs[i+1:]...)
			expand = false
			break
		}
		sub, err := fsanchor.AsDir(f)
		if err != nil {
			return Canonical{Path: declared, Refuse: "cannot open " + filepath.Join(dirAbs, name) + ": " + err.Error()}
		}
		d.Close()
		d, dirAbs = sub, filepath.Join(dirAbs, name)
	}
	p := strings.Join(out, "/")
	if tree {
		p += "/"
	}
	if expand {
		n := 0
		alias, refuse := treeLeaves(d, dirAbs, dirAbs, &n)
		if alias != "" || refuse != "" {
			return Canonical{Path: p, Alias: alias, Refuse: refuse}
		}
	}
	return Canonical{Path: p}
}

// treeLeaves walks d (the directory at abs, inside the declared tree top)
// through descriptors without following links, and names the first symlink
// whose target leaves top, or why the walk could not finish; "" and "" when
// every link stays inside. n counts entries against MaxTreeEntries.
func treeLeaves(d *fsanchor.AnchoredDir, abs, top string, n *int) (alias, refuse string) {
	names, err := d.Names()
	if err != nil {
		return "", "cannot walk " + abs + ": " + err.Error()
	}
	sort.Strings(names)
	for _, name := range names {
		// A tree that cannot be checked is a refusal with its reason, never a
		// silent local-only (Codex pass 3, ruling 6).
		if *n++; *n > MaxTreeEntries {
			return "", fmt.Sprintf("the declared tree %s has more than %d entries; declare narrower paths", top, MaxTreeEntries)
		}
		p := filepath.Join(abs, name)
		f, kind, _, _, oerr := d.OpenEntry(name)
		if oerr != nil {
			target, lerr := d.Readlink(name)
			if lerr != nil {
				return "", "cannot walk " + p + ": " + oerr.Error()
			}
			if !filepath.IsAbs(target) {
				target = filepath.Join(abs, target)
			}
			target = filepath.Clean(target)
			if rel, err := filepath.Rel(top, target); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return "symlink at " + p + " leaves the declared tree", ""
			}
			continue
		}
		if kind != fsanchor.KindDir {
			f.Close()
			continue
		}
		sub, err := fsanchor.AsDir(f)
		if err != nil {
			return "", "cannot walk " + p + ": " + err.Error()
		}
		alias, refuse = treeLeaves(sub, p, top, n)
		sub.Close()
		if alias != "" || refuse != "" {
			return alias, refuse
		}
	}
	return "", ""
}

// lookup finds seg among d's entries, listed through the descriptor: the
// exact name, else on a folding filesystem the case-folded one.
func lookup(d *fsanchor.AnchoredDir, seg string, fold bool) (string, bool, error) {
	names, err := d.Names()
	if err != nil {
		return "", false, err
	}
	for _, name := range names {
		if name == seg {
			return name, true, nil
		}
	}
	if fold {
		for _, name := range names {
			if strings.EqualFold(name, seg) {
				return name, true, nil
			}
		}
	}
	return "", false, nil
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
