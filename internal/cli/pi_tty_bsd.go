//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package cli

import "golang.org/x/sys/unix"

// isTerminal is the termios ioctl that golang.org/x/term's IsTerminal makes
// (review item 5): /dev/null and other character devices that are not a
// terminal answer ENOTTY, so `amq-squad </dev/null` does not start the lead.
func isTerminal(fd int) bool {
	_, err := unix.IoctlGetTermios(fd, unix.TIOCGETA)
	return err == nil
}
