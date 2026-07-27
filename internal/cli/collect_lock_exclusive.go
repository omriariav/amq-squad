package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	collectJournalExclusiveLockWait  = time.Second
	collectJournalExclusiveLockRetry = 10 * time.Millisecond
)

// lockCollectJournalExclusiveContext implements the Windows collect lock with
// an O_EXCL sentinel. Unlike a kernel-owned flock, that sentinel can survive a
// killed collector, so even context.Background callers need a bounded wait.
// The explicit maxWait argument keeps the stale-sentinel failure input fast and
// deterministic in platform-independent tests.
func lockCollectJournalExclusiveContext(ctx context.Context, root string, maxWait time.Duration) (func(), error) {
	if err := os.MkdirAll(root, collectJournalDirectoryPerm); err != nil {
		return nil, fmt.Errorf("ensure collect journal lock dir: %w", err)
	}
	path := filepath.Join(root, ".lock")
	deadline := time.Now().Add(maxWait)
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, collectJournalFilePerm)
		if err == nil {
			return func() {
				_ = f.Close()
				_ = os.Remove(path)
			}, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("open collect journal lock: %w", err)
		}
		remaining := time.Until(deadline)
		if maxWait <= 0 || remaining <= 0 {
			return nil, fmt.Errorf("collect already running for this profile/session/recipient or stale lock persisted beyond %s: %s", maxWait, path)
		}
		delay := collectJournalExclusiveLockRetry
		if remaining < delay {
			delay = remaining
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}
