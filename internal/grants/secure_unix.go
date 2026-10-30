//go:build unix

package grants

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Insecure reports why an owner file is untrusted, "" when it is trusted or
// cannot be stat'ed (absent). The file and its directory must belong to the
// user and must not be group- or world-writable; the file must not be a
// symlink, so a writable link target cannot stand in for it. doctor prints
// the reason.
func Insecure(path string) string {
	for i, p := range []string{path, filepath.Dir(path)} {
		st, err := os.Lstat(p)
		if err != nil {
			if i == 0 {
				return ""
			}
			return fmt.Sprintf("%s: %v", p, err)
		}
		if i == 0 && st.Mode()&os.ModeSymlink != 0 {
			return p + " is a symlink"
		}
		if st.Mode().Perm()&0o022 != 0 {
			return fmt.Sprintf("%s is group- or world-writable (mode %04o)", p, st.Mode().Perm())
		}
		if sys, ok := st.Sys().(*syscall.Stat_t); ok && int(sys.Uid) != os.Geteuid() {
			return fmt.Sprintf("%s is owned by uid %d, not %d", p, sys.Uid, os.Geteuid())
		}
	}
	return ""
}
