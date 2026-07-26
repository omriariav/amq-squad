//go:build !darwin && !linux

package procinfo

import (
	"errors"
	"time"
)

// readArgsNative has no fork-free implementation on this platform; callers fall
// back to ps.
func readArgsNative(pid int) (string, bool) { return "", false }

func readTTYNative(pid int) (string, bool) { return "", false }

func readStartTimeNative(pid int) (time.Time, bool) { return time.Time{}, false }

// parentChildIndex has no fork-free implementation on this platform.
func parentChildIndex() (map[int][]int, error) {
	return nil, errors.New("procinfo: no fork-free process table on this platform")
}
