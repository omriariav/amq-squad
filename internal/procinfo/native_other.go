//go:build !darwin && !linux

package procinfo

import "time"

// No fork-free reads on this platform; Args falls back to ps.
func readArgsNative(pid int) (string, bool) { return "", false }

func readStartTimeNative(pid int) (time.Time, bool) { return time.Time{}, false }
