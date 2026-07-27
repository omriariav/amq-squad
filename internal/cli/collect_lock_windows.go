//go:build windows

package cli

import "context"

func lockCollectJournalContext(ctx context.Context, root string) (func(), error) {
	return lockCollectJournalExclusiveContext(ctx, root, collectJournalExclusiveLockWait)
}
