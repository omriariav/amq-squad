//go:build unix

package seatcommit

import (
	"fmt"
	"os"
	"syscall"
)

// ownedByCaller refuses a quarantine directory another user owns (a planted
// directory the copy would land in).
func ownedByCaller(dir string, fi os.FileInfo) error {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if int(st.Uid) != os.Getuid() {
		return fmt.Errorf("the quarantine directory %s is owned by uid %d, not %d", dir, st.Uid, os.Getuid())
	}
	return nil
}
