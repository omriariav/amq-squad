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

func TestExternalOrchestratorMailboxPreInvokeReceiptResumesSafely(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-456"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	opts, err := resolveGoalDeliveryOptions(dir, "", "issue-456", "", "ship", true, false, true, "goal deliver", namespaceConflictOverrideOptions{})
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := beginExternalOrchestratorLifecycle(opts, "global-orch", "%99", "global", "@1", "orch", "/dev/ttys001", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}

	originalPersist := persistDeliveryReceipt
	persistDeliveryReceipt = func(projectDir, profile, session string, receipt *deliveryReceiptData) error {
		if receipt.AMQInvoked {
			return errors.New("injected crash before AMQ invocation")
		}
		return originalPersist(projectDir, profile, session, receipt)
	}
	t.Cleanup(func() { persistDeliveryReceipt = originalPersist })
	initCalls := 0
	originalRun := runAMQCommand
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		initCalls++
		if err := createExternalOrchestratorMailboxFixture(amqFlagValue(req.Arg, "root"), amqFlagValue(req.Arg, "agents")); err != nil {
			return nil, err
		}
		return []byte("Initialized AMQ root\n"), nil
	}
	t.Cleanup(func() { runAMQCommand = originalRun })

	if _, err := ensureExternalOrchestratorMailbox(opts, lifecycle); err == nil || !strings.Contains(err.Error(), "invocation boundary") {
		t.Fatalf("pre-invoke crash error = %v", err)
	}
	if initCalls != 0 {
		t.Fatalf("AMQ invoked despite failed pre-invoke receipt persistence: %d", initCalls)
	}
	registry, err := readExternalOrchestratorRegistry(lifecycle.Registration.Identity.Scope)
	if err != nil {
		t.Fatal(err)
	}
	current := registry.Registrations[len(registry.Registrations)-1]
	if current.State != externalOrchestratorStateMailboxInvoked {
		t.Fatalf("state after pre-invoke crash = %s", current.State)
	}
	attemptID := current.Transitions[len(current.Transitions)-1].Evidence.AttemptID
	receipt, err := readExternalOrchestratorMailboxReceipt(opts, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if got := classifyExternalOrchestratorMailboxOutcome(&receipt, errors.New("not verified")); got != externalOrchestratorMailboxPreInvokeSafe {
		t.Fatalf("typed outcome = %s, want preinvoke_safe", got)
	}

	persistDeliveryReceipt = originalPersist
	lifecycle, err = beginExternalOrchestratorLifecycle(opts, "global-orch", "%99", "global", "@1", "orch", "/dev/ttys001", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err = ensureExternalOrchestratorMailbox(opts, lifecycle)
	if err != nil {
		t.Fatalf("safe resume: %v", err)
	}
	if initCalls != 1 || lifecycle.Registration.State != externalOrchestratorStateMailboxVerified {
		t.Fatalf("safe resume calls/state = %d/%s", initCalls, lifecycle.Registration.State)
	}
	if got := classifyExternalOrchestratorMailboxOutcome(lifecycle.Receipt, nil); got != externalOrchestratorMailboxVerifiedDelivered {
		t.Fatalf("typed outcome = %s, want verified_delivered", got)
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
	attemptID := current.Transitions[len(current.Transitions)-1].Evidence.AttemptID
	tm, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	opts := goalDeliveryOptions{Project: dir, Profile: team.DefaultProfile, Session: "issue-456", Team: tm}
	receipt, err := readExternalOrchestratorMailboxReceipt(opts, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if got := classifyExternalOrchestratorMailboxOutcome(&receipt, verifyExternalOrchestratorMailbox(filepath.Join(base, "issue-456"), "global-orch")); got != externalOrchestratorMailboxInvokedUnverified {
		t.Fatalf("typed outcome = %s, want invoked_unverified", got)
	}
}

func TestExternalOrchestratorMailboxRejectsIntermediateSameInodeAliasSwap(t *testing.T) {
	root, err := canonicalPathForReceipt(t.TempDir())
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
