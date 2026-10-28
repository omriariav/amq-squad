//go:build !unix

package piruntime

import (
	"errors"
	"os"
)

// ownedByMe: the shim is a POSIX sh script and pi seats run on unix hosts
// only (seat-exec is unix-only); elsewhere ownership is not checked.
func ownedByMe(os.FileInfo) bool { return true }

// openNoFollow: pi seats run on unix hosts only.
func openNoFollow(string) (*os.File, error) {
	return nil, errors.New("the pi shim is supported on unix hosts only")
}
