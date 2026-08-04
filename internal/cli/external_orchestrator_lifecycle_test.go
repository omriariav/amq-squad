package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func TestExternalOrchestratorMailboxFailedInvocationIsUncertainAndNotReplayed(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-456"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	opts, err := resolveGoalDeliveryOptions(dir, "", "issue-456", "", "ship", true, false, true, "goal deliver")
	if err != nil {
		t.Fatal(err)
	}
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

func TestGoalRegisterOrchestratorInvokedUnverifiedBlocksWakeAndGoal(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-456"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-456", "cto", launch.Record{CWD: dir, Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-456", AgentPID: 42, Tmux: &launch.TmuxInfo{PaneID: "%7"}})

	originalPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "global", WindowID: "@1", WindowName: "orch", PaneID: "%99"}, nil
	}
	originalRun := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		if len(req.Arg) > 0 && req.Arg[0] == "init" {
			return nil, errors.New("injected AMQ interruption")
		}
		return originalRun(req)
	}
	originalWake := leadWakeStarter
	wakeCalls := 0
	leadWakeStarter = func(leadWakeOptions) (leadWakeResult, error) {
		wakeCalls++
		return leadWakeResult{}, nil
	}
	originalSend := sendPromptToPane
	goalCalls := 0
	sendPromptToPane = func(string, string) error {
		goalCalls++
		return nil
	}
	t.Cleanup(func() {
		currentPaneIdentity = originalPane
		runAMQCommand = originalRun
		leadWakeStarter = originalWake
		sendPromptToPane = originalSend
	})

	_, _, err := captureOutput(t, func() error {
		return runGoal([]string{"deliver", "--project", dir, "--session", "issue-456", "--goal", "ship", "--register-orchestrator=global-orch", "--json"})
	})
	if err == nil || !strings.Contains(err.Error(), "uncertain") {
		t.Fatalf("invoked-unverified error = %v", err)
	}
	if wakeCalls != 0 || goalCalls != 0 {
		t.Fatalf("uncertain mailbox crossed external boundary: wake=%d goal=%d", wakeCalls, goalCalls)
	}
	scope, err := newExternalOrchestratorScope(dir, team.DefaultProfile, "issue-456", "global-orch")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := readExternalOrchestratorRegistry(scope)
	if err != nil {
		t.Fatal(err)
	}
	current := registry.Registrations[len(registry.Registrations)-1]
	if current.State != externalOrchestratorStateMailboxUncertain {
		t.Fatalf("registry state = %s, want mailbox_uncertain", current.State)
	}
	evidence := current.Transitions[len(current.Transitions)-1].Evidence
	if evidence.AttemptID == "" || evidence.Outcome != "uncertain" || !strings.Contains(evidence.Detail, "injected AMQ interruption") {
		t.Fatalf("uncertain transition evidence = %+v", evidence)
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
