//go:build !darwin

package cli

// canonicalPathCase uses the portable entry/inode walk when the platform has no
// cheaper API for retrieving an existing path's on-disk spelling.
func canonicalPathCase(path string) string {
	return canonicalPathCasePortable(path)
}
