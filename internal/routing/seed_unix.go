//go:build unix

package routing

import (
	"os"
	"syscall"
)

// openNoFollow opens path for reading and fails on a symlink (O_NOFOLLOW).
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}
