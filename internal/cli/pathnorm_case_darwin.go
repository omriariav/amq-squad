//go:build darwin

package cli

import (
	"unsafe"

	"golang.org/x/sys/unix"
)

// canonicalPathCase returns the filesystem's spelling for an existing path.
// Darwin's filepath.EvalSymlinks preserves input case even when APFS resolves a
// differently-cased alias. F_GETPATH returns the canonical path attached to the
// opened object without changing the process working directory.
func canonicalPathCase(path string) string {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return canonicalPathCasePortable(path)
	}
	defer unix.Close(fd)

	var buf [unix.PathMax]byte
	_, _, errno := unix.Syscall(unix.SYS_FCNTL, uintptr(fd), unix.F_GETPATH, uintptr(unsafe.Pointer(&buf[0])))
	if errno != 0 {
		return canonicalPathCasePortable(path)
	}
	canonical := unix.ByteSliceToString(buf[:])
	if canonical == "" {
		return canonicalPathCasePortable(path)
	}
	return canonical
}
