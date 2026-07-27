//go:build !windows

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

func lockCollectJournalContext(ctx context.Context, root string) (func(), error) {
	if err := os.MkdirAll(root, collectJournalDirectoryPerm); err != nil {
		return nil, fmt.Errorf("ensure collect journal lock dir: %w", err)
	}
	path := filepath.Join(root, ".lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, collectJournalFilePerm)
	if err != nil {
		return nil, fmt.Errorf("open collect journal lock: %w", err)
	}
	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			_ = f.Close()
			return nil, fmt.Errorf("lock collect journal: %w", err)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			_ = f.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}
