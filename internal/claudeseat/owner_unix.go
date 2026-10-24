//go:build unix

package claudeseat

import (
	"os"
	"syscall"
)

func ownerUID(st os.FileInfo) (int, bool) {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(sys.Uid), true
}
