//go:build unix

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Codex's re-check on #837, item 1: runProcess hands the spawned process
// the lead's nonce over an inherited pipe at fd 3, never in its
// environment; without a nonce no descriptor is passed.
func TestRunProcessHandsTheNonceOverDescriptorThreeNotTheEnvironment(t *testing.T) {
	dir := t.TempDir()
	out, envOut := filepath.Join(dir, "fd3"), filepath.Join(dir, "env")
	prev := piSpawnNonce
	t.Cleanup(func() { piSpawnNonce = prev })
	piSpawnNonce = strings.Repeat("c3", 32)
	script := "cat <&3 > " + out + "; env > " + envOut
	if code, err := runProcess("/bin/sh", []string{"sh", "-c", script}, os.Environ()); err != nil || code != 0 {
		t.Fatalf("runProcess = %d, %v", code, err)
	}
	got, _ := os.ReadFile(out)
	env, _ := os.ReadFile(envOut)
	if strings.TrimSpace(string(got)) != piSpawnNonce {
		t.Fatalf("fd 3 carried %q", got)
	}
	if strings.Contains(string(env), piSpawnNonce) {
		t.Fatal("the nonce is in the child's environment")
	}
	piSpawnNonce = ""
	if code, _ := runProcess("/bin/sh", []string{"sh", "-c", "cat <&3 > " + out + " 2>/dev/null"}, os.Environ()); code == 0 {
		t.Fatal("a spawn with no nonce passed a descriptor 3")
	}
}
