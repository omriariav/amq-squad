//go:build !darwin && !linux

package procinfo

import "errors"

// readArgsNative has no fork-free implementation on this platform; callers fall
// back to ps.
func readArgsNative(pid int) (string, bool) { return "", false }

func readTTYNative(pid int) (string, bool) { return "", false }

// parentChildIndex has no fork-free implementation on this platform.
func parentChildIndex() (map[int][]int, error) {
	return nil, errors.New("procinfo: no fork-free process table on this platform")
}
