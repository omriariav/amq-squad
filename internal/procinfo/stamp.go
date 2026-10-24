package procinfo

// StartToken returns the operating system's raw process start stamp as an
// opaque string: on Linux the starttime clock ticks since boot from
// /proc/<pid>/stat (field 22), on darwin the kinfo p_starttime timeval. It
// is never a wall-clock reconstruction (StartTime derives Linux times from
// btime, which a clock step moves), so compare tokens for equality only
// (2b-2 fix 3).
func StartToken(pid int) (string, bool) {
	if pid <= 0 {
		return "", false
	}
	return readStartTokenNative(pid)
}

// ParentPID returns pid's parent process id.
func ParentPID(pid int) (int, bool) {
	if pid <= 0 {
		return 0, false
	}
	return readParentNative(pid)
}
