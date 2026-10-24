//go:build windows

package gatestore

// syncDir is a no-op on Windows, which cannot fsync a directory handle; a
// Windows build compiles but the gate store's Commit refuses there anyway
// (internal/flock has no lock off unix).
func syncDir(string) error { return nil }
