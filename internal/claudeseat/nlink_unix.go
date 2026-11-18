//go:build unix

package claudeseat

import (
	"os"
	"syscall"
)

// hardLinked reports a file with more than one name (a hard link elsewhere
// would let a write there change it without naming it).
func hardLinked(st os.FileInfo) bool {
	s, ok := st.Sys().(*syscall.Stat_t)
	return ok && s.Nlink > 1
}
