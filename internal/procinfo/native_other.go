//go:build !darwin && !linux

package procinfo

import (
	"time"
)

// readArgsNative has no fork-free implementation on this platform; callers fall
// back to ps.
func readArgsNative(pid int) (string, bool) { return "", false }

func readStartTimeNative(pid int) (time.Time, bool) { return time.Time{}, false }
