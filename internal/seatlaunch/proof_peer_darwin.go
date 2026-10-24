//go:build darwin

package seatlaunch

import "golang.org/x/sys/unix"

func socketPeerPID(fd int) (int, bool) {
	pid, err := unix.GetsockoptInt(fd, unix.SOL_LOCAL, unix.LOCAL_PEERPID)
	return pid, err == nil && pid > 0
}
