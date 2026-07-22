package liveidentity

import (
	"path/filepath"
	"strings"
	"testing"
)

func completeFixture(t *testing.T) (Declared, LaunchRecord, Observed) {
	t.Helper()
	project, err := CanonicalProject(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	key := Key{Project: project, Profile: "release", Session: "v2-23-1", Handle: "runtime-dev", PreparedGeneration: "g1", PreparedDigest: "d1", LaunchID: "l1"}
	terminal := Terminal{Backend: "tmux", Target: "new-window", Session: "s", WindowID: "@1", PaneID: "%2"}
	declared := Declared{Key: key, Role: "runtime-dev", Binary: "codex", Model: "gpt-5", WakePolicy: WakeRequired, WakeMode: "raw", WakeTarget: "%2", Terminal: terminal}
	launch := LaunchRecord{Key: key, Role: "runtime-dev", Binary: "codex", Model: "gpt-5", PID: 101, WakePID: 202, WakePolicy: WakeRequired, WakeMode: "raw", WakeTarget: "%2", WakeRecordID: "record-1", WakeRecordDigest: "sha256:record-1", Terminal: terminal}
	observed := Observed{Key: key, PID: 101, Binary: "codex", Model: "gpt-5", Terminal: terminal, WakeConsumers: []WakeConsumer{{PID: 202, Handle: "runtime-dev", Target: "%2", RecordID: "record-1", RecordDigest: "sha256:record-1", LaunchID: "l1"}}}
	return declared, launch, observed
}

func TestVerifyExactIdentity(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	result := Verify(declared, launch, observed)
	if result.Verified == nil || result.Verified.ConsumerCount != 1 || result.Verified.WakeRecordDigest != launch.WakeRecordDigest || len(result.Problems) != 0 || result.Recovery != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyRejectsDuplicateConsumerWithCanonicalRecovery(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	observed.WakeConsumers = append(observed.WakeConsumers, WakeConsumer{PID: 303, Handle: "runtime-dev", Target: "%2", RecordID: "record-2", RecordDigest: "sha256:record-2", LaunchID: "l1"})
	result := Verify(declared, launch, observed)
	if result.Verified != nil || result.Recovery != RecoveryAction || len(result.Problems) != 1 || !strings.Contains(result.Problems[0], "2 live wake consumers") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyRejectsConsistentlyWrongWakePane(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	declared.WakeTarget = "%wrong"
	launch.WakeTarget = "%wrong"
	observed.WakeConsumers[0].Target = "%wrong"
	result := Verify(declared, launch, observed)
	if result.Verified != nil || result.Recovery != RecoveryAction || !containsProblem(result.Problems, "exact terminal endpoint") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyRejectsContradictoryTerminalTargetStrategy(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	observed.Terminal.Target = "current-window"
	result := Verify(declared, launch, observed)
	if result.Verified != nil || !containsProblem(result.Problems, "terminal identities") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyNativeTerminalRequiresAndCarriesTTY(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	native := Terminal{Backend: "iterm2", Target: "new-window", Session: "s", WindowID: "101", TabID: "tab-1", SessionID: "session-1", TTY: "/dev/ttys001"}
	declared.Terminal, launch.Terminal, observed.Terminal = native, native, native
	declared.WakePolicy, declared.WakeMode, declared.WakeTarget = WakeDisabled, WakeDisabled, ""
	launch.WakePolicy, launch.WakeMode, launch.WakeTarget, launch.WakePID = WakeDisabled, WakeDisabled, "", 0
	launch.WakeRecordID, launch.WakeRecordDigest, observed.WakeConsumers = "", "", nil
	result := Verify(declared, launch, observed)
	if result.Verified == nil || result.Verified.Terminal.TTY != native.TTY {
		t.Fatalf("native result = %+v", result)
	}
	observed.Terminal.TTY = ""
	if result = Verify(declared, launch, observed); result.Verified != nil || !containsProblem(result.Problems, "terminal identities") {
		t.Fatalf("native identity without observed TTY verified: %+v", result)
	}
}

func TestVerifyRejectsUnsupportedTerminalBackend(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	synthetic := Terminal{Backend: "synthetic", Target: "new-window", SessionID: "session-1", TTY: "/dev/ttys001"}
	declared.Terminal, launch.Terminal, observed.Terminal = synthetic, synthetic, synthetic
	result := Verify(declared, launch, observed)
	if result.Verified != nil || !containsProblem(result.Problems, "terminal identities") {
		t.Fatalf("unsupported backend verified: %+v", result)
	}
}

func TestVerifyRejectsWakeRecordAliasAndLaunchMismatch(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	observed.WakeConsumers[0].RecordID = "sibling-record"
	observed.WakeConsumers[0].LaunchID = "old-launch"
	result := Verify(declared, launch, observed)
	if result.Verified != nil || !containsProblem(result.Problems, "record identity") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyExplicitNoWakeIdentity(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	declared.WakePolicy, declared.WakeMode, declared.WakeTarget = WakeDisabled, WakeDisabled, ""
	launch.WakePolicy, launch.WakeMode, launch.WakeTarget = WakeDisabled, WakeDisabled, ""
	launch.WakePID, launch.WakeRecordID, launch.WakeRecordDigest = 0, "", ""
	observed.WakeConsumers = nil
	result := Verify(declared, launch, observed)
	if result.Verified == nil || result.Verified.ConsumerCount != 0 || result.Verified.WakePolicy != WakeDisabled {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyDoesNotPromoteImplicitNoWakeIdentity(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	declared.WakePolicy, declared.WakeMode, declared.WakeTarget = "", WakeDisabled, ""
	launch.WakePolicy, launch.WakeMode, launch.WakeTarget = "", WakeDisabled, ""
	launch.WakePID, launch.WakeRecordID, launch.WakeRecordDigest = 0, "", ""
	observed.WakeConsumers = nil
	result := Verify(declared, launch, observed)
	if result.Verified != nil || !containsProblem(result.Problems, "wake policies") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func containsProblem(problems []string, needle string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, needle) {
			return true
		}
	}
	return false
}

func TestVerifyRejectsEveryIdentityLayerMismatch(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	launch.Key.PreparedGeneration = "stale"
	observed.PID++
	observed.Terminal.PaneID = "%wrong"
	observed.WakeConsumers[0].Target = "%wrong"
	result := Verify(declared, launch, observed)
	if result.Verified != nil || result.Recovery != RecoveryAction || len(result.Problems) < 4 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCanonicalProjectResolvesPhysicalPath(t *testing.T) {
	real := t.TempDir()
	alias := filepath.Join(t.TempDir(), "alias")
	if err := symlinkDir(real, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	got, err := CanonicalProject(alias)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(real)
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
