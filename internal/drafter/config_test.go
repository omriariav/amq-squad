package drafter

import (
	"strings"
	"testing"
)

func TestValidateDefaultsToInSession(t *testing.T) {
	if err := Validate(nil); err != nil {
		t.Fatalf("Validate(nil): %v", err)
	}
	cfg := Config{}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("Validate(empty): %v", err)
	}
	if got := cfg.EffectiveBackend(); got != BackendInSession {
		t.Fatalf("EffectiveBackend = %q, want %q", got, BackendInSession)
	}
}

func TestValidatePresetAndCustomConfigs(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "yoetz", cfg: Config{Backend: BackendYoetz, Model: "gemini/flash"}},
		{name: "claude", cfg: Config{Backend: BackendClaude, Model: "fable", Effort: "low", TimeoutSeconds: 30}},
		{name: "codex", cfg: Config{Backend: BackendCodex, Model: "gpt-5.6-luna", Effort: "medium", OnFailure: FailureError}},
		{name: "custom-stdio", cfg: Config{Backend: BackendCustom, Command: []string{"local-drafter", "--schema", `{"type":"string"}`}}},
		{name: "custom-files", cfg: Config{Backend: BackendCustom, Command: []string{"local-drafter", "--in={prompt}", "--out={out}", "--model={model}", "--effort={effort}"}, Model: "fast", Effort: "low"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(&tc.cfg); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestValidateRejectsInvalidConfig(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "backend", cfg: Config{Backend: "remote"}, want: "unsupported drafter backend"},
		{name: "custom-command", cfg: Config{Backend: BackendCustom}, want: "required for custom"},
		{name: "timeout-negative", cfg: Config{Backend: BackendClaude, TimeoutSeconds: -1}, want: "timeout_seconds"},
		{name: "timeout-large", cfg: Config{Backend: BackendClaude, TimeoutSeconds: MaxTimeoutSeconds + 1}, want: "timeout_seconds"},
		{name: "failure-mode", cfg: Config{Backend: BackendClaude, OnFailure: "ignore"}, want: "on_failure"},
		{name: "in-session-options", cfg: Config{Backend: BackendInSession, Model: "x"}, want: "cannot set"},
		{name: "empty-arg", cfg: Config{Backend: BackendCustom, Command: []string{"tool", ""}}, want: "command[1]"},
		{name: "unknown-token", cfg: Config{Backend: BackendCustom, Command: []string{"tool", "{input}"}}, want: "unsupported template token"},
		{name: "missing-model-token", cfg: Config{Backend: BackendCustom, Command: []string{"tool"}, Model: "x"}, want: "must include {model}"},
		{name: "missing-model-value", cfg: Config{Backend: BackendCustom, Command: []string{"tool", "{model}"}}, want: "required when command contains {model}"},
		{name: "yoetz-effort", cfg: Config{Backend: BackendYoetz, Effort: "low"}, want: "yoetz preset has no effort"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(&tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate error = %v, want %q", err, tc.want)
			}
		})
	}
}
