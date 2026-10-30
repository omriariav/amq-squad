//go:build !unix

package gatestore

import "os"

// fileID off unix: no inode is read (3.0 hosts are macOS, plan 8); the
// resolved path alone is the identity.
func fileID(os.FileInfo) string { return "" }
