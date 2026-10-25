//go:build unix

package cli

import (
	"os"
	"syscall"
)

// statDevice is the device number of a file or directory.
func statDevice(path string) (uint64, bool) {
	st, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(sys.Dev), true
}
