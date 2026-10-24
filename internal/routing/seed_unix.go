//go:build unix

package routing

import (
	"os"
	"syscall"
)

// openNoFollow opens path for reading without following a symlink
// (O_NOFOLLOW) and without blocking (O_NONBLOCK), so a FIFO at the path
// cannot hang the caller before the fstat type check (Codex pass 4; the
// shape of the registry's runtime open). The caller requires a regular file
// from fstat before it reads.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
