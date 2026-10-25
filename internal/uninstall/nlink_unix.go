//go:build unix

package uninstall

import (
	"os"
	"syscall"
)

// linkCount is how many names the open file has (1 when unknown).
func linkCount(f *os.File) uint64 {
	st, err := f.Stat()
	if err != nil {
		return 2 // unknown: treat as shared, never zero a file we cannot count
	}
	if s, ok := st.Sys().(*syscall.Stat_t); ok {
		return uint64(s.Nlink)
	}
	return 2
}
