//go:build !darwin && !linux

package cli

// readProcArgsNative has no fork-free implementation on this platform; callers
// fall back to ps.
func readProcArgsNative(pid int) (string, bool) { return "", false }
