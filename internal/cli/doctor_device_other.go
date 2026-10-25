//go:build !unix

package cli

// statDevice is unknown off unix: the same-filesystem note is skipped.
func statDevice(string) (uint64, bool) { return 0, false }
