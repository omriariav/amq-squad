//go:build windows

package atomicfile

// SyncDir is a no-op on Windows, which cannot fsync a directory handle; a
// Windows build compiles, and the gate store refuses there anyway
// (internal/flock has no lock off unix).
func SyncDir(string) error { return nil }
