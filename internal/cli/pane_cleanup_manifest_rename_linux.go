//go:build linux

package cli

import "golang.org/x/sys/unix"

func paneManifestRenameNoReplace(dirFD int, oldName, newName string) error {
	return unix.Renameat2(dirFD, oldName, dirFD, newName, unix.RENAME_NOREPLACE)
}
