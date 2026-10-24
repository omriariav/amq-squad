//go:build linux

package cli

import "golang.org/x/sys/unix"

// isTerminal is the termios ioctl that golang.org/x/term's IsTerminal makes.
func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	return err == nil
}
