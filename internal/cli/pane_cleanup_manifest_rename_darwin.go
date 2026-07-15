//go:build darwin

package cli

import "golang.org/x/sys/unix"

func paneManifestRenameNoReplace(dirFD int, oldName, newName string) error {
	return unix.RenameatxNp(dirFD, oldName, dirFD, newName, unix.RENAME_EXCL)
}
