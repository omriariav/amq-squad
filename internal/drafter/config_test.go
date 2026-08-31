package drafter

import (
	"reflect"
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
		{name: "ordered-chain", cfg: Config{Chain: []string{BackendYoetz, BackendClaude, BackendCodex}, Model: "fast", Effort: "low"}},
		{name: "chain-with-custom", cfg: Config{Chain: []string{BackendCustom, BackendClaude}, Command: []string{"local-drafter"}}},
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
		{name: "yoetz-no-model", cfg: Config{Backend: BackendYoetz}, want: "model: required for the yoetz preset backend"},
		{name: "yoetz-no-model-chain-hop", cfg: Config{Chain: []string{BackendClaude, BackendYoetz}}, want: "model: required for the yoetz preset backend"},
		{name: "backend-and-chain", cfg: Config{Backend: BackendClaude, Chain: []string{BackendCodex}}, want: "mutually exclusive"},
		{name: "empty-chain-hop", cfg: Config{Chain: []string{BackendClaude, " "}}, want: "chain[1]"},
		{name: "in-session-chain-hop", cfg: Config{Chain: []string{BackendClaude, BackendInSession}}, want: "implicit after chain exhaustion"},
		{name: "duplicate-chain-hop", cfg: Config{Chain: []string{BackendClaude, BackendClaude}}, want: "duplicate backend"},
		{name: "chain-command-without-custom", cfg: Config{Chain: []string{BackendClaude}, Command: []string{"claude"}}, want: "chain has no custom hop"},
		{name: "chain-custom-without-command", cfg: Config{Chain: []string{BackendCustom, BackendClaude}}, want: "required for custom"},
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

func TestResolveUsesWholeBlockPrecedenceAndClones(t *testing.T) {
	profile := &Config{Backend: BackendClaude, Model: "profile-model"}
	global := &Config{Chain: []string{BackendYoetz, BackendCodex}, Model: "global-model"}
	resolved, err := Resolve(profile, global)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Source != SourceProfile || resolved.Config == nil {
		t.Fatalf("resolution = %+v", resolved)
	}
	if resolved.Config.Model != "profile-model" || len(resolved.Config.Chain) != 0 || resolved.Config.Backend != BackendClaude {
		t.Fatalf("profile and global blocks were merged: %+v", resolved.Config)
	}
	resolved.Config.Model = "changed"
	if profile.Model != "profile-model" {
		t.Fatalf("Resolve returned profile alias: %+v", profile)
	}

	resolved, err = Resolve(nil, global)
	if err != nil {
		t.Fatalf("Resolve global: %v", err)
	}
	if resolved.Source != SourceGlobal || !reflect.DeepEqual(resolved.Config.Chain, global.Chain) {
		t.Fatalf("global resolution = %+v", resolved)
	}
	resolved.Config.Chain[0] = BackendClaude
	if global.Chain[0] != BackendYoetz {
		t.Fatalf("Resolve returned global slice alias: %+v", global)
	}

	resolved, err = Resolve(nil, nil)
	if err != nil || resolved.Source != SourceInSession || resolved.Config != nil {
		t.Fatalf("in-session resolution = %+v, %v", resolved, err)
	}
}

func TestValidateProfileRejectsExecutablePolicy(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "command", cfg: Config{Backend: BackendClaude, Command: []string{"attacker-command"}}, want: "only in the user-level global config"},
		{name: "custom-backend", cfg: Config{Backend: BackendCustom}, want: "custom is allowed only"},
		{name: "custom-chain", cfg: Config{Chain: []string{BackendClaude, BackendCustom}, Command: []string{"attacker-command"}}, want: "only in the user-level global config"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateProfile(&tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ValidateProfile error = %v, want %q", err, tc.want)
			}
		})
	}
	if err := ValidateProfile(&Config{Chain: []string{BackendClaude, BackendCodex}, Model: "safe-preset"}); err != nil {
		t.Fatalf("ValidateProfile preset chain: %v", err)
	}
}
