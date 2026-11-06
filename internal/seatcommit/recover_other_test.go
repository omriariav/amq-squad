//go:build !unix

package seatcommit

// Off unix the timeout row has no process group to check.
func processAlive(int) bool { return false }
