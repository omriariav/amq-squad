//go:build unix

package fsanchor

// 2b-4 additions beyond the lifted set (routing's no-follow walk of declared
// trees): kept in their own file so the merge is additive, as 4-3's
// fleet_extra.go. routing_extra_other.go is the non-unix twin.

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// Readlink reads the target of the symlink name relative to the descriptor
// (readlinkat): it never follows the link, and fails when name is not one.
func (d *AnchoredDir) Readlink(name string) (string, error) {
	if !CleanName(name) {
		return "", fmt.Errorf("file name %q is not clean", name)
	}
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(d.fd, name, buf)
	if err != nil {
		return "", err
	}
	return string(buf[:n]), nil
}
