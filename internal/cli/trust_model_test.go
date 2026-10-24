package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
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
