//go:build windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"
)

func lockCollectJournal(root string) (func(), error) {
	if err := os.MkdirAll(root, collectJournalDirectoryPerm); err != nil {
		return nil, fmt.Errorf("ensure collect journal lock dir: %w", err)
	}
	path := filepath.Join(root, ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, collectJournalFilePerm)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("collect already running for this profile/session/recipient: %s", path)
		}
		return nil, fmt.Errorf("open collect journal lock: %w", err)
	}
	return func() {
		_ = f.Close()
		_ = os.Remove(path)
	}, nil
}
