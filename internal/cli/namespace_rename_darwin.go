//go:build darwin

package cli

import "golang.org/x/sys/unix"

func namespaceRenameNoReplace(source, target string) error {
	return unix.RenameatxNp(unix.AT_FDCWD, source, unix.AT_FDCWD, target, unix.RENAME_EXCL)
}
