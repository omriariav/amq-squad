//go:build !unix

package piruntime

import "os"

// ownedByMe: the shim is a POSIX sh script and pi seats run on unix hosts
// only (seat-exec is unix-only); elsewhere ownership is not checked.
func ownedByMe(os.FileInfo) bool { return true }
