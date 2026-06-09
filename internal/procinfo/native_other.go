//go:build !darwin && !linux

package procinfo

// readArgsNative has no fork-free implementation on this platform; callers fall
// back to ps.
func readArgsNative(pid int) (string, bool) { return "", false }
