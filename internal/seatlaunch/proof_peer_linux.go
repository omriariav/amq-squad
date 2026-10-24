//go:build linux

package seatlaunch

import "golang.org/x/sys/unix"

func socketPeerPID(fd int) (int, bool) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	return int(cred.Pid), err == nil && cred.Pid > 0
}
