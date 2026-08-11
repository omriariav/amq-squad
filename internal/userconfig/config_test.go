package userconfig

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
)

func TestPathUsesCanonicalHomeAndExplicitOverride(t *testing.T) {
	t.Setenv(ConfigEnv, "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	path, err := Path()
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := filepath.Join(home, ".config", "amq-squad", "config.json")
	if path != want {
		t.Fatalf("Path = %q, want %q", path, want)
	}

	override := filepath.Join(t.TempDir(), "ci-config.json")
	t.Setenv(ConfigEnv, override)
	path, err = Path()
	if err != nil || path != override {
		t.Fatalf("override Path = %q, %v, want %q", path, err, override)
	}
}

func TestReadMissingConfigIsEmpty(t *testing.T) {
	t.Setenv(ConfigEnv, filepath.Join(t.TempDir(), "missing.json"))
	cfg, err := Read()
	if err != nil {
		t.Fatalf("Read missing: %v", err)
	}
	if !reflect.DeepEqual(cfg, Config{}) {
		t.Fatalf("Read missing = %+v, want empty", cfg)
	}
}

func TestReadPreservesExistingSettingsAndAcceptsTrustedCustomCommand(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{
  "models": {"codex": "gpt-default"},
  "drafter": {
    "chain": ["custom", "claude"],
    "command": ["local-drafter", "--out", "{out}"],
    "timeout_seconds": 30
  }
}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if cfg.Models["codex"] != "gpt-default" || cfg.Drafter == nil {
		t.Fatalf("config = %+v", cfg)
	}
	if !reflect.DeepEqual(cfg.Drafter.Chain, []string{drafter.BackendCustom, drafter.BackendClaude}) {
		t.Fatalf("chain = %v", cfg.Drafter.Chain)
	}
	if !reflect.DeepEqual(cfg.Drafter.Command, []string{"local-drafter", "--out", "{out}"}) {
		t.Fatalf("command = %v", cfg.Drafter.Command)
	}
}

func TestReadFailsClosedOnMalformedOrInvalidDrafter(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "malformed", body: `{`, want: "parse user config"},
		{name: "unknown-top-level", body: `{"draftre":{"chain":["claude"]}}`, want: `unknown field "draftre"`},
		{name: "unknown-nested-drafter", body: `{"drafter":{"chian":["claude"]}}`, want: `unknown field "chian"`},
		{name: "multiple-json-values", body: `{} {}`, want: "multiple JSON values"},
		{name: "invalid-chain", body: `{"drafter":{"chain":["claude","in_session"]}}`, want: "implicit after chain exhaustion"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := ReadFile(path)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ReadFile error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestWriteRoundTripsPrivateConfigAndPreservesOtherSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	t.Setenv(ConfigEnv, path)
	cfg := Config{
		Model:       "general-default",
		CodexModel:  "codex-default",
		ClaudeModel: "claude-default",
		Models:      map[string]string{"codex": "mapped-default"},
		Drafter: &drafter.Config{
			Chain:          []string{drafter.BackendYoetz, drafter.BackendClaude},
			Model:          "draft-model",
			Effort:         "low",
			TimeoutSeconds: 45,
			OnFailure:      drafter.FailureError,
		},
	}
	written, err := Write(cfg)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if written != path {
		t.Fatalf("Write path = %q, want %q", written, path)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat written config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("config mode = %o, want 600", info.Mode().Perm())
	}
	got, err := Read()
	if err != nil {
		t.Fatalf("Read written config: %v", err)
	}
	if !reflect.DeepEqual(got, cfg) {
		t.Fatalf("round trip = %+v, want %+v", got, cfg)
	}
}

func TestWriteRejectsInvalidDrafterBeforeReplacingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := []byte("{\"model\":\"keep\"}\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	err := WriteFile(path, Config{Drafter: &drafter.Config{
		Chain: []string{drafter.BackendClaude, drafter.BackendClaude},
	}})
	if err == nil || !strings.Contains(err.Error(), "duplicate backend") {
		t.Fatalf("WriteFile error = %v, want duplicate backend", err)
	}
	body, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("read original after rejected write: %v", readErr)
	}
	if string(body) != string(original) {
		t.Fatalf("rejected write changed config: %q", body)
	}
}
