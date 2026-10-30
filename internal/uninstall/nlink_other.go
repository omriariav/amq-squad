//go:build !unix

package uninstall

import "os"

// linkCount: this platform cannot count links, so a secret is never zeroed.
func linkCount(*os.File) uint64 { return 2 }
