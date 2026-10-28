//go:build unix

package piruntime

import (
	"os"
	"syscall"
)

// ownedByMe reports a file owned by this process's user.
func ownedByMe(fi os.FileInfo) bool {
	st, ok := fi.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid()
}

// openNoFollow opens path read-only and refuses a symlink in its last
// component (O_NOFOLLOW) without blocking on a FIFO (O_NONBLOCK; the caller
// refuses a non-regular file from the fstat), so the checks and the read
// share one descriptor.
func openNoFollow(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
}
