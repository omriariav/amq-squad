package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func testExternalOrchestratorIdentity(t *testing.T, project, profile, session, handle, pane string) externalOrchestratorIdentity {
	t.Helper()
	scope, err := newExternalOrchestratorScope(project, profile, session, handle)
	if err != nil {
		t.Fatal(err)
	}
	return externalOrchestratorIdentity{
		Scope: scope,
		Runtime: externalOrchestratorRuntimeIdentity{
			TmuxSession: "noc", WindowID: "@1", WindowName: "orchestrator", PaneID: pane, TTY: "/dev/ttys001",
		},
	}
}

func advanceExternalOrchestratorToRegistered(t *testing.T, identity externalOrchestratorIdentity, start time.Time) externalOrchestratorRegistration {
	t.Helper()
	record, replayed, err := beginExternalOrchestratorRegistration(identity, start)
	if err != nil || replayed {
		t.Fatalf("begin registration record=%+v replayed=%t err=%v", record, replayed, err)
	}
	steps := []struct {
		state    externalOrchestratorRegistrationState
		evidence externalOrchestratorTransitionEvidence
	}{
		{externalOrchestratorStateMailboxInvoked, externalOrchestratorTransitionEvidence{AttemptID: "mailbox-1", ReceiptPath: "/receipts/mailbox-1.json"}},
		{externalOrchestratorStateMailboxVerified, externalOrchestratorTransitionEvidence{AttemptID: "mailbox-1", CanonicalRoot: "/mail/s", MailboxPath: "/mail/s/agents/orchestrator", Outcome: "delivered"}},
		{externalOrchestratorStateRuntimeVerified, externalOrchestratorTransitionEvidence{WakePID: 4242, LaunchPath: "/mail/s/agents/orchestrator/launch.json"}},
		{externalOrchestratorStateRegistered, externalOrchestratorTransitionEvidence{Detail: "registration committed"}},
	}
	for i, step := range steps {
		record, replayed, err = transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, step.state, step.evidence, start.Add(time.Duration(i+1)*time.Second))
		if err != nil || replayed {
			t.Fatalf("transition %s record=%+v replayed=%t err=%v", step.state, record, replayed, err)
		}
	}
	return record
}

func TestExternalOrchestratorRegistryScopedIdentityAndGeneration(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 14, 16, 0, 0, 0, time.UTC)
	firstIdentity := testExternalOrchestratorIdentity(t, project, "release", "work-a", "orchestrator", "%18")
	first, replayed, err := beginExternalOrchestratorRegistration(firstIdentity, now)
	if err != nil || replayed {
		t.Fatalf("first begin record=%+v replayed=%t err=%v", first, replayed, err)
	}
	if first.Generation != 1 || first.State != externalOrchestratorStatePlanned || first.Authoritative {
		t.Fatalf("first generation = %+v", first)
	}
	duplicate, replayed, err := beginExternalOrchestratorRegistration(firstIdentity, now.Add(time.Second))
	if err != nil || !replayed || duplicate.ID != first.ID || duplicate.Generation != first.Generation {
		t.Fatalf("duplicate begin record=%+v replayed=%t err=%v", duplicate, replayed, err)
	}

	replacement := firstIdentity
	replacement.Runtime.PaneID = "%19"
	if _, _, err := beginExternalOrchestratorRegistration(replacement, now.Add(2*time.Second)); err == nil || !strings.Contains(err.Error(), "mark it stale/dead") {
		t.Fatalf("live replacement error = %v", err)
	}
	stale, replayed, err := transitionExternalOrchestratorRegistration(firstIdentity.Scope, first.Generation, externalOrchestratorStateStale, externalOrchestratorTransitionEvidence{Detail: "pane disappeared"}, now.Add(3*time.Second))
	if err != nil || replayed || stale.Authoritative {
		t.Fatalf("stale transition=%+v replayed=%t err=%v", stale, replayed, err)
	}
	second, replayed, err := beginExternalOrchestratorRegistration(replacement, now.Add(4*time.Second))
	if err != nil || replayed || second.Generation != 2 || second.ID == first.ID || second.Authoritative {
		t.Fatalf("replacement generation=%+v replayed=%t err=%v", second, replayed, err)
	}
	registry, err := readExternalOrchestratorRegistry(firstIdentity.Scope)
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Registrations) != 2 || registry.Registrations[0].State != externalOrchestratorStateStale || registry.CurrentGeneration != 2 {
		t.Fatalf("registry generations = %+v", registry)
	}

	other := testExternalOrchestratorIdentity(t, project, "release", "work-b", "orchestrator", "%20")
	otherRecord, _, err := beginExternalOrchestratorRegistration(other, now)
	if err != nil || otherRecord.Generation != 1 || externalOrchestratorRegistryPath(other.Scope) == externalOrchestratorRegistryPath(firstIdentity.Scope) {
		t.Fatalf("independent scope record=%+v err=%v", otherRecord, err)
	}
}

func TestExternalOrchestratorRegistryMonotonicTransitionsAndDuplicateReplay(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 14, 16, 10, 0, 0, time.UTC)
	identity := testExternalOrchestratorIdentity(t, project, "default", "work", "orchestrator", "%18")
	record, _, err := beginExternalOrchestratorRegistration(identity, now)
	if err != nil {
		t.Fatal(err)
	}
	invokedEvidence := externalOrchestratorTransitionEvidence{AttemptID: "attempt-1", ReceiptPath: "/receipt/attempt-1.json"}
	record, replayed, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxInvoked, invokedEvidence, now.Add(time.Second))
	if err != nil || replayed {
		t.Fatalf("mailbox invoked record=%+v replayed=%t err=%v", record, replayed, err)
	}
	historyLen := len(record.Transitions)
	replayedRecord, replayed, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxInvoked, invokedEvidence, now.Add(2*time.Second))
	if err != nil || !replayed || len(replayedRecord.Transitions) != historyLen {
		t.Fatalf("duplicate transition record=%+v replayed=%t err=%v", replayedRecord, replayed, err)
	}
	if _, _, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxInvoked, externalOrchestratorTransitionEvidence{AttemptID: "different"}, now.Add(2*time.Second)); err == nil || !strings.Contains(err.Error(), "different evidence") {
		t.Fatalf("different duplicate evidence error = %v", err)
	}
	if _, _, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateRegistered, externalOrchestratorTransitionEvidence{}, now.Add(2*time.Second)); err == nil || !strings.Contains(err.Error(), "not monotonic") {
		t.Fatalf("skipped state error = %v", err)
	}
	uncertainEvidence := externalOrchestratorTransitionEvidence{AttemptID: "attempt-1", Outcome: "delivery_uncertain"}
	record, _, err = transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxUncertain, uncertainEvidence, now.Add(3*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	record, _, err = transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxVerified, externalOrchestratorTransitionEvidence{AttemptID: "attempt-1", Outcome: "delivered"}, now.Add(4*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	record, _, err = transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateRuntimeVerified, externalOrchestratorTransitionEvidence{WakePID: 99}, now.Add(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	record, _, err = transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateRegistered, externalOrchestratorTransitionEvidence{Detail: "committed"}, now.Add(6*time.Second))
	if err != nil || record.Authoritative {
		t.Fatalf("registered record=%+v err=%v", record, err)
	}
	dead, _, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateDead, externalOrchestratorTransitionEvidence{Detail: "verified dead"}, now.Add(7*time.Second))
	if err != nil || dead.Authoritative {
		t.Fatalf("dead record=%+v err=%v", dead, err)
	}
	if _, _, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateRegistered, externalOrchestratorTransitionEvidence{}, now.Add(8*time.Second)); err == nil || !strings.Contains(err.Error(), "not monotonic") {
		t.Fatalf("dead resurrection error = %v", err)
	}
}

func TestExternalOrchestratorRegistryAtomicFsyncCrashRestartRead(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 14, 16, 20, 0, 0, time.UTC)
	identity := testExternalOrchestratorIdentity(t, project, "default", "work", "orchestrator", "%18")

	originalFileSync := externalOrchestratorRegistryFileSync
	originalDirSync := externalOrchestratorRegistryDirectorySync
	originalFault := externalOrchestratorRegistryFault
	fileSyncs, dirSyncs := 0, 0
	externalOrchestratorRegistryFileSync = func(f *os.File) error {
		fileSyncs++
		return originalFileSync(f)
	}
	externalOrchestratorRegistryDirectorySync = func(dir *os.File) error {
		dirSyncs++
		return originalDirSync(dir)
	}
	t.Cleanup(func() {
		externalOrchestratorRegistryFileSync = originalFileSync
		externalOrchestratorRegistryDirectorySync = originalDirSync
		externalOrchestratorRegistryFault = originalFault
	})

	record, _, err := beginExternalOrchestratorRegistration(identity, now)
	if err != nil {
		t.Fatal(err)
	}
	externalOrchestratorRegistryFault = func(stage string) error {
		if stage == "after_file_sync" {
			return errors.New("crash before publish")
		}
		return nil
	}
	if _, _, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxInvoked, externalOrchestratorTransitionEvidence{AttemptID: "attempt-1"}, now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "crash before publish") {
		t.Fatalf("pre-publish crash error = %v", err)
	}
	restarted, err := readExternalOrchestratorRegistry(identity.Scope)
	if err != nil || restarted.Registrations[0].State != externalOrchestratorStatePlanned {
		t.Fatalf("restart after pre-publish crash registry=%+v err=%v", restarted, err)
	}

	externalOrchestratorRegistryFault = originalFault
	invokedEvidence := externalOrchestratorTransitionEvidence{AttemptID: "attempt-1"}
	record, _, err = transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxInvoked, invokedEvidence, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	externalOrchestratorRegistryFault = func(stage string) error {
		if stage == "after_rename" {
			return errors.New("crash after atomic publish")
		}
		return nil
	}
	verifiedEvidence := externalOrchestratorTransitionEvidence{AttemptID: "attempt-1", Outcome: "delivered"}
	if _, _, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxVerified, verifiedEvidence, now.Add(3*time.Second)); err == nil || !strings.Contains(err.Error(), "crash after atomic publish") {
		t.Fatalf("post-publish crash error = %v", err)
	}
	restarted, err = readExternalOrchestratorRegistry(identity.Scope)
	if err != nil || restarted.Registrations[0].State != externalOrchestratorStateMailboxVerified {
		t.Fatalf("restart after publish registry=%+v err=%v", restarted, err)
	}
	externalOrchestratorRegistryFault = originalFault
	replayedRecord, replayed, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxVerified, verifiedEvidence, now.Add(4*time.Second))
	if err != nil || !replayed || replayedRecord.State != externalOrchestratorStateMailboxVerified {
		t.Fatalf("restart duplicate record=%+v replayed=%t err=%v", replayedRecord, replayed, err)
	}
	if fileSyncs < 3 || dirSyncs == 0 {
		t.Fatalf("durability sync counts file=%d dir=%d", fileSyncs, dirSyncs)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(externalOrchestratorRegistryPath(identity.Scope)), ".registry-crash.tmp"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readExternalOrchestratorRegistry(identity.Scope); err != nil {
		t.Fatalf("orphan temp affected canonical restart read: %v", err)
	}
}

func TestExternalOrchestratorRegistryConcurrentDuplicateIsSingleTransition(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 14, 16, 25, 0, 0, time.UTC)
	identity := testExternalOrchestratorIdentity(t, project, "default", "work", "orchestrator", "%18")
	record, _, err := beginExternalOrchestratorRegistration(identity, now)
	if err != nil {
		t.Fatal(err)
	}
	evidence := externalOrchestratorTransitionEvidence{AttemptID: "attempt-1", ReceiptPath: "/receipt/attempt-1.json"}
	type outcome struct {
		replayed bool
		err      error
	}
	const writers = 8
	outcomes := make(chan outcome, writers)
	var ready sync.WaitGroup
	ready.Add(writers)
	start := make(chan struct{})
	for i := 0; i < writers; i++ {
		go func() {
			ready.Done()
			<-start
			_, replayed, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxInvoked, evidence, now.Add(time.Second))
			outcomes <- outcome{replayed: replayed, err: err}
		}()
	}
	ready.Wait()
	close(start)
	winners, replays := 0, 0
	for i := 0; i < writers; i++ {
		got := <-outcomes
		if got.err != nil {
			t.Fatalf("concurrent duplicate transition: %v", got.err)
		}
		if got.replayed {
			replays++
		} else {
			winners++
		}
	}
	if winners != 1 || replays != writers-1 {
		t.Fatalf("concurrent outcomes winners=%d replays=%d", winners, replays)
	}
	registry, err := readExternalOrchestratorRegistry(identity.Scope)
	if err != nil {
		t.Fatal(err)
	}
	current := registry.Registrations[0]
	if current.State != externalOrchestratorStateMailboxInvoked || len(current.Transitions) != 2 {
		t.Fatalf("concurrent duplicate produced extra transition: %+v", current)
	}
}

func TestExternalOrchestratorRegistryRejectsExistingAncestorSymlink(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	writeExternalOrchestratorOutsideSentinel(t, outside)
	if err := os.Symlink(outside, filepath.Join(project, team.DirName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	identity := testExternalOrchestratorIdentity(t, project, "default", "work", "orchestrator", "%18")
	if _, _, err := beginExternalOrchestratorRegistration(identity, time.Date(2026, 7, 14, 16, 26, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("existing ancestor symlink error = %v", err)
	}
	assertExternalOrchestratorOutsideSentinel(t, outside)
}

func TestExternalOrchestratorRegistryRejectsValidationOpenAncestorSwap(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	writeExternalOrchestratorOutsideSentinel(t, outside)
	identity := testExternalOrchestratorIdentity(t, project, "default", "work", "orchestrator", "%18")
	ancestor := filepath.Join(identity.Scope.ProjectDir, team.DirName)
	if err := os.Mkdir(ancestor, 0o700); err != nil {
		t.Fatal(err)
	}
	originalHook := externalOrchestratorRegistryContainmentHook
	swapped := false
	externalOrchestratorRegistryContainmentHook = func(stage, path string) error {
		if !swapped && stage == "after_component_validation" && path == ancestor {
			swapped = true
			if err := os.Rename(ancestor, ancestor+".original"); err != nil {
				return err
			}
			return os.Symlink(outside, ancestor)
		}
		return nil
	}
	t.Cleanup(func() { externalOrchestratorRegistryContainmentHook = originalHook })
	if _, _, err := beginExternalOrchestratorRegistration(identity, time.Date(2026, 7, 14, 16, 27, 0, 0, time.UTC)); err == nil || !strings.Contains(err.Error(), "contained registry") {
		t.Fatalf("validation/open ancestor swap error = %v", err)
	}
	if !swapped {
		t.Fatal("validation/open swap hook was not reached")
	}
	assertExternalOrchestratorOutsideSentinel(t, outside)
}

func TestExternalOrchestratorRegistryRejectsTargetSwapBeforeRename(t *testing.T) {
	project := t.TempDir()
	outside := t.TempDir()
	writeExternalOrchestratorOutsideSentinel(t, outside)
	now := time.Date(2026, 7, 14, 16, 28, 0, 0, time.UTC)
	identity := testExternalOrchestratorIdentity(t, project, "default", "work", "orchestrator", "%18")
	record, _, err := beginExternalOrchestratorRegistration(identity, now)
	if err != nil {
		t.Fatal(err)
	}
	target := externalOrchestratorRegistryPath(identity.Scope)
	originalHook := externalOrchestratorRegistryContainmentHook
	swapped := false
	externalOrchestratorRegistryContainmentHook = func(stage, path string) error {
		if !swapped && stage == "before_target_rename" && path == target {
			swapped = true
			if err := os.Remove(target); err != nil {
				return err
			}
			return os.Symlink(filepath.Join(outside, "sentinel"), target)
		}
		return nil
	}
	t.Cleanup(func() { externalOrchestratorRegistryContainmentHook = originalHook })
	if _, _, err := transitionExternalOrchestratorRegistration(identity.Scope, record.Generation, externalOrchestratorStateMailboxInvoked, externalOrchestratorTransitionEvidence{AttemptID: "attempt-1"}, now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "target identity changed") {
		t.Fatalf("target swap error = %v", err)
	}
	if !swapped {
		t.Fatal("target swap hook was not reached")
	}
	assertExternalOrchestratorOutsideSentinel(t, outside)
}

func writeExternalOrchestratorOutsideSentinel(t *testing.T, outside string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("outside-unchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertExternalOrchestratorOutsideSentinel(t *testing.T, outside string) {
	t.Helper()
	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "sentinel" {
		t.Fatalf("outside directory was mutated: %+v", entries)
	}
	b, err := os.ReadFile(filepath.Join(outside, "sentinel"))
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "outside-unchanged\n" {
		t.Fatalf("outside sentinel was mutated: %q", b)
	}
}

func TestExternalOrchestratorRegistryStaleDeadRecordsRemainNonAuthoritative(t *testing.T) {
	project := t.TempDir()
	now := time.Date(2026, 7, 14, 16, 30, 0, 0, time.UTC)
	firstIdentity := testExternalOrchestratorIdentity(t, project, "default", "work", "orchestrator", "%18")
	first := advanceExternalOrchestratorToRegistered(t, firstIdentity, now)
	if first.Authoritative {
		t.Fatal("registered external orchestrator became authoritative")
	}
	first, _, err := transitionExternalOrchestratorRegistration(firstIdentity.Scope, first.Generation, externalOrchestratorStateStale, externalOrchestratorTransitionEvidence{Detail: "heartbeat expired"}, now.Add(10*time.Second))
	if err != nil || first.Authoritative {
		t.Fatalf("stale first=%+v err=%v", first, err)
	}
	// A verified stale generation is historical even if tmux later reuses the
	// exact same pane identity; beginning again must allocate a new generation,
	// never replay the terminal record as current authority.
	secondIdentity := firstIdentity
	second, _, err := beginExternalOrchestratorRegistration(secondIdentity, now.Add(11*time.Second))
	if err != nil || second.Generation != 2 {
		t.Fatalf("second generation=%+v err=%v", second, err)
	}
	second, _, err = transitionExternalOrchestratorRegistration(secondIdentity.Scope, second.Generation, externalOrchestratorStateDead, externalOrchestratorTransitionEvidence{Detail: "pane dead"}, now.Add(12*time.Second))
	if err != nil || second.Authoritative {
		t.Fatalf("dead second=%+v err=%v", second, err)
	}
	registry, err := readExternalOrchestratorRegistry(firstIdentity.Scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range registry.Registrations {
		if record.Authoritative {
			t.Fatalf("retained external record became authoritative: %+v", record)
		}
	}

	path := externalOrchestratorRegistryPath(firstIdentity.Scope)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &registry); err != nil {
		t.Fatal(err)
	}
	registry.Registrations[0].Authoritative = true
	b, _ = json.MarshalIndent(registry, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readExternalOrchestratorRegistry(firstIdentity.Scope); err == nil || !strings.Contains(err.Error(), "cannot be authoritative") {
		t.Fatalf("authoritative corruption error = %v", err)
	}
}
