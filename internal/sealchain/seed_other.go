//go:build !unix

package sealchain

import (
	"fmt"
	"os"
)

// openNoFollow opens path for reading after refusing a symlink by Lstat
// (Windows has no O_NOFOLLOW; the window between the two calls is the
// platform's limit, as for the task store's lock).
func openNoFollow(path string) (*os.File, error) {
	st, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s is a symlink", path)
	}
	return os.Open(path)
}

// fileID has no device and inode off unix: the head's seed identity check
// is skipped there (zero values match zero values).
func fileID(os.FileInfo) (uint64, uint64) { return 0, 0 }
