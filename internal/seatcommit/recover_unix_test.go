//go:build unix

package seatcommit

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid is alive (signal 0): the timeout row's
// check that the transport's children died with their process group.
func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
