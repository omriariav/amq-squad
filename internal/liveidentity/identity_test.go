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
	terminal := Terminal{Backend: "tmux", Session: "s", WindowID: "@1", PaneID: "%2"}
	declared := Declared{Key: key, Role: "runtime-dev", Binary: "codex", Model: "gpt-5", WakeMode: "raw", WakeTarget: "%2", Terminal: terminal}
	launch := LaunchRecord{Key: key, Role: "runtime-dev", Binary: "codex", Model: "gpt-5", PID: 101, WakePID: 202, WakeMode: "raw", WakeTarget: "%2", Terminal: terminal}
	observed := Observed{Key: key, PID: 101, Binary: "codex", Model: "gpt-5", Terminal: terminal, WakeConsumers: []WakeConsumer{{PID: 202, Handle: "runtime-dev", Target: "%2"}}}
	return declared, launch, observed
}

func TestVerifyExactIdentity(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	result := Verify(declared, launch, observed)
	if result.Verified == nil || result.Verified.ConsumerCount != 1 || len(result.Problems) != 0 || result.Recovery != "" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestVerifyRejectsDuplicateConsumerWithCanonicalRecovery(t *testing.T) {
	declared, launch, observed := completeFixture(t)
	observed.WakeConsumers = append(observed.WakeConsumers, WakeConsumer{PID: 303, Handle: "runtime-dev", Target: "%2"})
	result := Verify(declared, launch, observed)
	if result.Verified != nil || result.Recovery != RecoveryAction || len(result.Problems) != 1 || !strings.Contains(result.Problems[0], "2 live wake consumers") {
		t.Fatalf("unexpected result: %+v", result)
	}
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
