//go:build !darwin && !linux

package cli

import "errors"

// errNoPTY: stdin is not a real pty.
var errNoPTY = errors.New("stdin is not a terminal")

// The owner proof needs a unix controlling terminal (R45: the squad runs on
// unix hosts).
func openControllingTTY() (ownerTTY, error) {
	return nil, errors.New("no controlling terminal on this platform")
}

func deviceOf(string) (uint64, error) { return 0, errors.New("no tty devices on this platform") }
