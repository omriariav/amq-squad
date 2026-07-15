//go:build !darwin && !linux

package cli

import "fmt"

func namespaceRenameNoReplace(_, _ string) error {
	return fmt.Errorf("atomic no-replace rename is unsupported on this platform")
}
