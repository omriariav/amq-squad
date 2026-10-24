//go:build darwin || linux

package cli

import (
	"errors"
	"fmt"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

// The owner's terminal (R9 of the 2b-2 milestone B third pass) is the real
// pty of stdin: fstat of fd 0 (on Linux also named through /proc/self/fd/0),
// never /dev/tty, whose device on Linux is the alias device every
// controlling terminal shares. A stdin that is not a terminal is no owner
// terminal; the confirmation is read from it with echo off.

// errNoPTY: stdin is not a real pty.
var errNoPTY = errors.New("stdin is not a terminal")

// stdinTTY is the owner's terminal on fd 0, echo off while it is open.
type stdinTTY struct {
	dev  uint64
	name string
	old  *unix.Termios
}

func openControllingTTY() (ownerTTY, error) {
	var st unix.Stat_t
	if err := unix.Fstat(0, &st); err != nil {
		return nil, fmt.Errorf("stdin cannot be read: %v", err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFCHR {
		return nil, errNoPTY
	}
	old, err := unix.IoctlGetTermios(0, getTermios)
	if err != nil {
		return nil, errNoPTY
	}
	name := ""
	if runtime.GOOS == "linux" {
		if name, err = os.Readlink("/proc/self/fd/0"); err != nil {
			return nil, fmt.Errorf("the pty of stdin cannot be named: %v", err)
		}
		var nst unix.Stat_t
		if err := unix.Stat(name, &nst); err != nil || uint64(nst.Rdev) != uint64(st.Rdev) {
			return nil, fmt.Errorf("the pty of stdin %s is not the device fd 0 names", name)
		}
	}
	quiet := *old
	quiet.Lflag &^= unix.ECHO
	if err := unix.IoctlSetTermios(0, setTermios, &quiet); err != nil {
		return nil, fmt.Errorf("echo cannot be turned off: %v", err)
	}
	return &stdinTTY{dev: uint64(st.Rdev), name: name, old: old}, nil
}

func (t *stdinTTY) Read(b []byte) (int, error)  { return os.Stdin.Read(b) }
func (t *stdinTTY) Write(b []byte) (int, error) { return os.Stderr.Write(b) }

// Close restores echo.
func (t *stdinTTY) Close() error {
	err := unix.IoctlSetTermios(0, setTermios, t.old)
	os.Stderr.Write([]byte("\n"))
	return err
}

// Device is the real pty's device number.
func (t *stdinTTY) Device() (uint64, error) { return t.dev, nil }

// deviceOf is a tty path's device number.
func deviceOf(path string) (uint64, error) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Rdev), nil
}
