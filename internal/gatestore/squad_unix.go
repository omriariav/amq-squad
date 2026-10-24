//go:build unix

package gatestore

import (
	"fmt"
	"os"
	"syscall"
)

// fileID is the directory's device and inode.
func fileID(info os.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("%d:%d", uint64(st.Dev), uint64(st.Ino))
	}
	return ""
}
