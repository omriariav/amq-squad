//go:build unix

package flock

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func unlock(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
