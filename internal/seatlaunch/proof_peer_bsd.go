//go:build unix && !darwin && !linux

package seatlaunch

// The kernel does not report the peer pid here; the claimed pid and its
// start stamp and the pane chain still apply.
func socketPeerPID(fd int) (int, bool) { return 0, false }
