//go:build unix

package flock

import (
	"os"

	"golang.org/x/sys/unix"
)

func lockExclusive(f *os.File) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_EX)
}

func lockExclusiveRequired(f *os.File) error { return lockExclusive(f) }

func tryLockExclusive(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == unix.EWOULDBLOCK || err == unix.EAGAIN {
		return false, nil
	}
	return false, err
}

func tryLockShared(f *os.File) (bool, error) {
	err := unix.Flock(int(f.Fd()), unix.LOCK_SH|unix.LOCK_NB)
	if err == nil {
		return true, nil
	}
	if err == unix.EWOULDBLOCK || err == unix.EAGAIN {
		return false, nil
	}
	return false, err
}

func unlock(f *os.File) {
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
