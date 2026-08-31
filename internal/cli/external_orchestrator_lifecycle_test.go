package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
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

func TestExternalOrchestratorMailboxFailedInvocationIsUncertainAndNotReplayed(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-456"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	opts, err := resolveGoalTargetOptions(dir, "", "issue-456", "", true, false, true, "goal")
	if err != nil {
		t.Fatal(err)
	}
	opts.Goal = "ship"
	lifecycle, err := beginExternalOrchestratorLifecycle(opts, "global-orch", "%99", "global", "@1", "orch", "/dev/ttys001", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	initCalls := 0
	originalRun := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		initCalls++
		return nil, errors.New("injected AMQ interruption")
	}
	t.Cleanup(func() { runAMQCommand = originalRun })

	if _, err := ensureExternalOrchestratorMailbox(opts, lifecycle); err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("failed invocation error = %v", err)
	}
	if initCalls != 1 {
		t.Fatalf("AMQ invocation count = %d, want 1", initCalls)
	}
	registry, err := readExternalOrchestratorRegistry(lifecycle.Registration.Identity.Scope)
	if err != nil {
		t.Fatal(err)
	}
	current := registry.Registrations[len(registry.Registrations)-1]
	if current.State != externalOrchestratorStateMailboxUncertain {
		t.Fatalf("state after failed invocation = %s", current.State)
	}
	evidence := current.Transitions[len(current.Transitions)-1].Evidence
	if evidence.AttemptID == "" || evidence.CanonicalRoot != lifecycle.Root || evidence.Outcome != "uncertain" {
		t.Fatalf("uncertain transition evidence = %+v", evidence)
	}

	lifecycle, err = beginExternalOrchestratorLifecycle(opts, "global-orch", "%99", "global", "@1", "orch", "/dev/ttys001", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ensureExternalOrchestratorMailbox(opts, lifecycle); err == nil || !strings.Contains(err.Error(), "explicit repair") {
		t.Fatalf("uncertain replay error = %v", err)
	}
	if initCalls != 1 {
		t.Fatalf("uncertain replay invoked AMQ again: %d", initCalls)
	}
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
