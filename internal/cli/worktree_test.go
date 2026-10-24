package cli

import (
	"strings"
	"testing"
)

func TestWorktreeCommandRegisteredAndMaterializeRequiresConfirmation(t *testing.T) {
	if _, ok := lookupCommand("worktree", "dev"); !ok {
		t.Fatal("worktree command is not registered")
	}
	err := runWorktree([]string{
		"materialize", "--role", "worker", "--task", "t1", "--base", "HEAD",
		"--scope", "internal/**", "--session", "s",
	})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("materialize confirmation error = %v", err)
	}
}
