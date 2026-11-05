//go:build !unix

package seatcommit

import "os"

// ownedByCaller has no owner to check off unix.
func ownedByCaller(string, os.FileInfo) error { return nil }
