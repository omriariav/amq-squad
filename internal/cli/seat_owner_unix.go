//go:build unix

package cli

import (
	"os"

	"golang.org/x/sys/unix"
)

// controllingTTY is /dev/tty opened read-write: it exists only for a
// process with a controlling terminal.
type controllingTTY struct{ *os.File }

func openControllingTTY() (ownerTTY, error) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	return controllingTTY{f}, nil
}

// Device is the terminal's device number (the pty both /dev/tty and the
// pane's tty path name).
func (t controllingTTY) Device() (uint64, error) {
	var st unix.Stat_t
	if err := unix.Fstat(int(t.Fd()), &st); err != nil {
		return 0, err
	}
	return uint64(st.Rdev), nil
}

// deviceOf is a tty path's device number.
func deviceOf(path string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Rdev), nil
}
