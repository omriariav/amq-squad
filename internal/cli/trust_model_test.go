package cli

import (
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/launch"
)

func TestDefaultChildArgsForBinaryWithTrust(t *testing.T) {
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

func TestTrustModeFromRecordLegacyBypassClassifiesAsTrusted(t *testing.T) {
	rec := launch.Record{
		Binary: "codex",
		Argv:   []string{"--dangerously-bypass-approvals-and-sandbox", "prompt"},
	}
	if got := trustModeFromRecord(rec); got != trustModeTrusted {
		t.Errorf("legacy codex bypass record = %q, want trusted", got)
	}
}

func TestTrustModeFromRecordExplicitTrustWins(t *testing.T) {
	rec := launch.Record{Binary: "codex", Trust: "sandboxed", Argv: []string{"--dangerously-bypass-approvals-and-sandbox"}}
	if got := trustModeFromRecord(rec); got != trustModeSandboxed {
		t.Errorf("explicit Trust should override argv inference: %q", got)
	}
}

func TestTrustModeFromRecordNoDefaultArgsSandboxed(t *testing.T) {
	rec := launch.Record{Binary: "codex", NoDefaultArgs: true, Argv: []string{"--enable", "goals"}}
	if got := trustModeFromRecord(rec); got != trustModeSandboxed {
		t.Errorf("no-default-args codex should classify as sandboxed: %q", got)
	}
}

func TestTrustModeFromRecordClaudeReturnsEmpty(t *testing.T) {
	rec := launch.Record{Binary: "claude", Argv: []string{"--permission-mode", "auto"}}
	if got := trustModeFromRecord(rec); got != "" {
		t.Errorf("claude should not get a trust label: %q", got)
	}
}

func TestLaunchArgsFromRecordModelRoundTrips(t *testing.T) {
	rec := launch.Record{
		CWD:     "/p",
		Binary:  "codex",
		Argv:    []string{"--model", "gpt-5", "--enable", "goals"},
		Session: "s",
		Handle:  "cto",
		Role:    "cto",
		Model:   "gpt-5",
		Trust:   trustModeSandboxed,
	}
	got := launchArgsFromRecord(rec)
	want := []string{
		"--no-bootstrap",
		"--role", "cto",
		"--session", "s",
		"--trust", "sandboxed",
		"--model", "gpt-5",
		"--me", "cto",
		"codex",
		"--",
		"--enable", "goals",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("launchArgsFromRecord = %#v, want %#v", got, want)
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

func TestEmitCommandIncludesModel(t *testing.T) {
	rec := launch.Record{
		CWD:    "/p",
		Binary: "claude",
		Handle: "fullstack",
		Role:   "fullstack",
		Model:  "sonnet",
	}
	cmd := emitCommand(rec)
	if !strings.Contains(cmd, "--model sonnet") {
		t.Errorf("emitCommand missing --model sonnet: %s", cmd)
	}
}
