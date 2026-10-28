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
