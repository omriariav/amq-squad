//go:build !windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func lockCollectJournal(root string) (func(), error) {
	if err := os.MkdirAll(root, collectJournalDirectoryPerm); err != nil {
		return nil, fmt.Errorf("ensure collect journal lock dir: %w", err)
	}
	path := filepath.Join(root, ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, collectJournalFilePerm)
	if err != nil {
		return nil, fmt.Errorf("open collect journal lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("lock collect journal: %w", err)
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
