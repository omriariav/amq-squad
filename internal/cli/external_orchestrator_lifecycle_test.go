package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createExternalOrchestratorMailboxFixture(root, handles string) error {
	agents := strings.Split(handles, ",")
	for _, handle := range agents {
		for _, relative := range []string{"inbox/new", "inbox/cur", "inbox/tmp", "outbox/sent", "receipts", "dlq/new", "dlq/cur", "dlq/tmp"} {
			if err := os.MkdirAll(filepath.Join(root, "agents", handle, filepath.FromSlash(relative)), 0o700); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "meta"), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(struct {
		Version int      `json:"version"`
		Agents  []string `json:"agents"`
	}{Version: 1, Agents: agents})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, "meta", "config.json"), b, 0o600)
}

func TestExternalOrchestratorMailboxRejectsIntermediateSameInodeAliasSwap(t *testing.T) {
	root, err := canonicalExternalOrchestratorPath(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := createExternalOrchestratorMailboxFixture(root, "global-orch"); err != nil {
		t.Fatal(err)
	}
	inbox := filepath.Join(root, "agents", "global-orch", "inbox")
	originalHook := externalOrchestratorMailboxContainmentHook
	swapped := false
	externalOrchestratorMailboxContainmentHook = func(stage, path string) error {
		if !swapped && stage == "after_component_validation" && path == inbox {
			swapped = true
			if err := os.Rename(inbox, inbox+".original"); err != nil {
				return err
			}
			return os.Symlink("inbox.original", inbox)
		}
		return nil
	}
	t.Cleanup(func() { externalOrchestratorMailboxContainmentHook = originalHook })

	err = verifyExternalOrchestratorMailbox(root, "global-orch")
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("same-inode intermediate alias swap error = %v", err)
	}
	if !swapped {
		t.Fatal("deterministic intermediate swap hook was not reached")
	}
}
