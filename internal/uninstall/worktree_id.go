package uninstall

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// CodeWorktreeReplaced: the directory at a worktree's name is not the one that
// was verified (it was renamed away and something else put in its place);
// nothing is removed.
const CodeWorktreeReplaced = "worktree_replaced"

// verifyWorktreeDir opens the candidate (no symlink followed, a directory),
// reads its .git file through that descriptor, and checks that the gitdir it
// names is, by device and inode, the project's own .git/worktrees entry. It
// returns the opened directory; removal walks from it.
func verifyWorktreeDir(proj string, parent *fsanchor.AnchoredDir, name, wtPath string) (*fsanchor.AnchoredDir, error) {
	wd, err := parent.Sub(name)
	if err != nil {
		return nil, err
	}
	fail := func(format string, a ...any) (*fsanchor.AnchoredDir, error) {
		wd.Close()
		return nil, fmt.Errorf(format, a...)
	}
	raw, err := fsanchor.ReadEntry(wd, ".git")
	if err != nil {
		return fail("its .git cannot be read through the opened directory: %v", err)
	}
	if len(raw) > 4096 {
		return fail("its .git is not a gitdir file")
	}
	gd, ok := strings.CutPrefix(strings.TrimSpace(string(raw)), "gitdir: ")
	if !ok || gd == "" {
		return fail("its .git is not a gitdir file")
	}
	if !filepath.IsAbs(gd) {
		gd = filepath.Join(wtPath, gd)
	}
	gd = filepath.Clean(gd)
	named, err := fsanchor.OpenAnchored(filepath.Dir(gd), []string{filepath.Base(gd)}, false)
	if err != nil {
		return fail("the gitdir its .git names cannot be opened: %v", err)
	}
	defer named.Close()
	own, err := fsanchor.OpenAnchored(proj, []string{".git", "worktrees", filepath.Base(gd)}, false)
	if err != nil {
		return fail("the project has no .git/worktrees/%s: %v", filepath.Base(gd), err)
	}
	defer own.Close()
	same, err := named.SameDir(own)
	if err != nil || !same {
		return fail("the gitdir its .git names is not the project's own .git/worktrees/%s", filepath.Base(gd))
	}
	return wd, nil
}

// removeWorktree removes the verified directory: the identity of the entry at
// the parent is checked against the verified descriptor before anything goes
// and again before the entry itself is unlinked (a plain rmdir, so a
// replacement with content is never removed); the contents go through the
// verified descriptor, never through the name.
func removeWorktree(parent *fsanchor.AnchoredDir, o op) error {
	replaced := func() error {
		return &Error{Code: CodeWorktreeReplaced, Detail: o.display + ": the directory at this name is not the one that was verified; nothing was removed from it"}
	}
	if same, err := sameEntry(parent, o.name, o.wd); err != nil || !same {
		if err != nil {
			return err
		}
		return replaced()
	}
	names, err := o.wd.Names()
	if err != nil {
		return err
	}
	for _, n := range names {
		if err := o.wd.StageRemoveAll(n); err != nil {
			return err
		}
	}
	if same, err := sameEntry(parent, o.name, o.wd); err != nil || !same {
		if err != nil {
			return err
		}
		return replaced()
	}
	return rmdirEntry(parent, o.name)
}
