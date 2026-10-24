//go:build !unix

package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// trustedRoot off unix (3.0 hosts are macOS, plan 8): every component is
// checked with Lstat before the open. This is not race-free.
type trustedRoot struct{ dir string }

func openTrustedRoot(dir string) (*trustedRoot, error) {
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		return nil, fmt.Errorf("runtime root %s is not a directory", dir)
	}
	return &trustedRoot{dir: dir}, nil
}

func (r *trustedRoot) Close() error { return nil }

func (r *trustedRoot) Open(rel string) (*os.File, error) {
	cur := r.dir
	for _, part := range strings.Split(rel, "/") {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("runtime path %q is not clean", rel)
		}
		cur = filepath.Join(cur, part)
		st, err := os.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("runtime path %s: %s is a symlink", rel, cur)
		}
	}
	return os.Open(cur)
}
