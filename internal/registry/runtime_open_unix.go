//go:build unix

package registry

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// trustedRoot opens files below a runtime root one component at a time
// with O_NOFOLLOW (openat-style), so no symlink at any depth is followed and
// a component swapped after a check cannot redirect the open: the caller
// hashes the descriptor it was handed.
type trustedRoot struct{ fd int }

func openTrustedRoot(dir string) (*trustedRoot, error) {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("runtime root %s: %w (a symlink or not a directory)", dir, err)
	}
	return &trustedRoot{fd: fd}, nil
}

func (r *trustedRoot) Close() error { return unix.Close(r.fd) }

// Open opens rel (slash separated) for reading without following a symlink
// in any component.
func (r *trustedRoot) Open(rel string) (*os.File, error) {
	parts := strings.Split(rel, "/")
	dirfd := r.fd
	for i, part := range parts {
		if part == "" || part == "." || part == ".." {
			return nil, fmt.Errorf("runtime path %q is not clean", rel)
		}
		last := i == len(parts)-1
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC
		if !last {
			flags |= unix.O_DIRECTORY
		}
		fd, err := unix.Openat(dirfd, part, flags, 0)
		if dirfd != r.fd {
			unix.Close(dirfd)
		}
		if err != nil {
			return nil, fmt.Errorf("runtime path %s: %w (a symlinked or missing component)", rel, err)
		}
		if last {
			return os.NewFile(uintptr(fd), rel), nil
		}
		dirfd = fd
	}
	return nil, fmt.Errorf("runtime path %q is empty", rel)
}
