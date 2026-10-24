//go:build !unix

package claudeseat

import "os"

func ownerUID(os.FileInfo) (int, bool) { return 0, false }
