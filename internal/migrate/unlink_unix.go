//go:build unix

package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/omriariav/amq-squad/v3/internal/fsanchor"
)

// unlinkUnder removes path (inside project) through the descriptor walk of
// internal/fsanchor: the project root is opened once (symlinks in the anchor
// itself are the owner's, resolved first), every directory below it with
// O_NOFOLLOW, and the file is unlinked relative to its parent descriptor. A
// symlink at any directory on the way is migrate_refused_symlink and nothing
// is removed; a file that is already gone is not an error.
func unlinkUnder(project, path string) error {
	r, err := filepath.Rel(project, path)
	if err != nil || strings.HasPrefix(r, "..") {
		return &Refusal{Code: CodeSymlink, Fields: []string{"a path outside the project"}, Detail: "a v2 file is outside the project root; nothing is removed"}
	}
	base, err := filepath.EvalSymlinks(project)
	if err != nil {
		return err
	}
	parts := strings.Split(r, string(filepath.Separator))
	dir, err := fsanchor.OpenAnchored(filepath.Clean(base), parts[:len(parts)-1], false)
	if err != nil {
		switch {
		case errors.Is(err, syscall.ENOENT):
			return nil
		case errors.Is(err, syscall.ELOOP), errors.Is(err, syscall.ENOTDIR):
			return &Refusal{Code: CodeSymlink, Fields: []string{filepath.ToSlash(r)}, Detail: "a symlink on the way to a v2 file; migrate removes nothing through one"}
		}
		return fmt.Errorf("open %s: %w", filepath.Dir(path), err)
	}
	defer dir.Close()
	if err := unix.Unlinkat(dir.Fd(), parts[len(parts)-1], 0); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
