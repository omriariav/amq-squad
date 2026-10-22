package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

)

func TestDefaultChildArgsForBinaryWithTrust(t *testing.T) {
	wantApprove := []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request", "-c", `approvals_reviewer="auto_review"`}
	if got := defaultChildArgsForBinary("codex"); !reflect.DeepEqual(got, wantApprove) {
		t.Errorf("default codex args = %v, want approve-for-me %v", got, wantApprove)
	}
	if got := defaultChildArgsForBinaryWithTrust("codex", trustModeSandboxed); len(got) != 0 {
		t.Errorf("sandboxed codex defaults = %v, want []", got)
	}
	want := []string{"--dangerously-bypass-approvals-and-sandbox"}
	if got := defaultChildArgsForBinaryWithTrust("codex", trustModeTrusted); !reflect.DeepEqual(got, want) {
		t.Errorf("trusted codex defaults = %v, want %v", got, want)
	}
	wantClaude := []string{"--permission-mode", "auto"}
	if got := defaultChildArgsForBinaryWithTrust("claude", trustModeSandboxed); !reflect.DeepEqual(got, wantClaude) {
		t.Errorf("claude sandboxed defaults = %v, want %v", got, wantClaude)
	}
	if got := defaultChildArgsForBinaryWithTrust("claude", trustModeTrusted); !reflect.DeepEqual(got, wantClaude) {
		t.Errorf("trust does not change claude defaults: %v", got)
	}
}

func TestResolveModelUsesAMQSquadConfigBeforeCodexLocal(t *testing.T) {
	configDir := t.TempDir()
	home := t.TempDir()
	writeFile(t, filepath.Join(configDir, "amq-squad", "config.json"), `{"models":{"codex":"gpt-5.5"}}`)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `model = "gpt-local"`)
	withModelLookupRoots(t, configDir, home, map[string]string{})

	if got := resolveModelForLaunch("codex", "", nil); got != "gpt-5.5" {
		t.Fatalf("resolved model = %q, want gpt-5.5", got)
	}
	if got := resolveModelForLaunch("codex", "gpt-explicit", nil); got != "gpt-explicit" {
		t.Fatalf("explicit model = %q, want gpt-explicit", got)
	}
}

func TestResolveModelUsesCodexProfileConfig(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	writeFile(t, filepath.Join(codexHome, "worker.config.toml"), `model = "gpt-5.5"`)
	writeFile(t, filepath.Join(codexHome, "config.toml"), `model = "gpt-default"`)
	withModelLookupRoots(t, t.TempDir(), home, map[string]string{"CODEX_HOME": codexHome})

	got := resolveModelForLaunch("codex", "", []string{"--profile", "worker"})
	if got != "gpt-5.5" {
		t.Fatalf("profile model = %q, want gpt-5.5", got)
	}
}

func withModelLookupRoots(t *testing.T, configDir, home string, env map[string]string) {
	t.Helper()
	oldConfigDir := modelUserConfigDir
	oldHomeDir := modelUserHomeDir
	oldGetenv := modelGetenv
	modelUserConfigDir = func() (string, error) { return configDir, nil }
	modelUserHomeDir = func() (string, error) { return home, nil }
	modelGetenv = func(key string) string {
		if v, ok := env[key]; ok {
			return v
		}
		return ""
	}
	t.Cleanup(func() {
		modelUserConfigDir = oldConfigDir
		modelUserHomeDir = oldHomeDir
		modelGetenv = oldGetenv
	})
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestModelArgsForBinary(t *testing.T) {
	cases := []struct {
		binary string
		model  string
		want   []string
	}{
		{"codex", "gpt-5", []string{"--model", "gpt-5"}},
		{"claude", "sonnet", []string{"--model", "sonnet"}},
		{"codex", "  ", nil},
		{"node", "x", nil},
		{"/usr/local/bin/codex", "gpt-5", []string{"--model", "gpt-5"}},
	}
	for _, tc := range cases {
		got := modelArgsForBinary(tc.binary, tc.model)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("modelArgsForBinary(%q, %q) = %v, want %v", tc.binary, tc.model, got, tc.want)
		}
	}
}

func TestValidateTrustCombinationRejectsContradictions(t *testing.T) {
	if err := validateTrustCombination(trustModeTrusted, true, true, nil); err == nil {
		t.Fatal("trusted + no-default-args should fail")
	}
	if err := validateTrustCombination(trustModeSandboxed, true, false, map[string][]string{
		"codex": {"--dangerously-bypass-approvals-and-sandbox"},
	}); err == nil {
		t.Fatal("sandboxed + bypass via codex-args should fail")
	}
	// Sandboxed without bypass is fine.
	if err := validateTrustCombination(trustModeSandboxed, true, false, map[string][]string{"codex": {"--enable", "goals"}}); err != nil {
		t.Fatalf("sandboxed + benign codex args should pass, got %v", err)
	}
	// Trusted + no-default-args still fine when both sides flow through naturally.
	if err := validateTrustCombination(trustModeTrusted, true, false, nil); err != nil {
		t.Fatalf("trusted alone should pass, got %v", err)
	}
}

func TestValidateModelOverrideKeysRejectsUnknownRole(t *testing.T) {
	known := map[string]bool{"cto": true, "fullstack": true}
	if err := validateModelOverrideKeys(map[string]string{"ctoo": "gpt-5"}, known); err == nil {
		t.Fatal("expected error for unknown role key")
	}
	if err := validateModelOverrideKeys(map[string]string{"cto": "gpt-5"}, known); err != nil {
		t.Fatalf("known role should not error: %v", err)
	}
	if err := validateModelOverrideKeys(map[string]string{"CTO": "gpt-5"}, known); err != nil {
		t.Fatalf("case-insensitive match expected: %v", err)
	}
}
