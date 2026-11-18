//go:build !unix

package claudeseat

import "os"

// hardLinked: link counts are not read on this platform (seats run on unix).
func hardLinked(os.FileInfo) bool { return false }
