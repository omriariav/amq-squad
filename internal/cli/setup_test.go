package cli

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
	"github.com/omriariav/amq-squad/v2/internal/userconfig"
)

func TestSetupNonInteractiveWritesGlobalDrafterAndPreservesOtherSettings(t *testing.T) {
	current := userconfig.Config{
		Model:       "general-default",
		CodexModel:  "codex-default",
		ClaudeModel: "claude-default",
		Models:      map[string]string{"codex": "mapped-default"},
	}
	var written userconfig.Config
	var stdout, stderr bytes.Buffer
	deps := setupDependencies{
		In:  strings.NewReader(""),
		Out: &stdout,
		Err: &stderr,
		LookPath: func(name string) (string, error) {
			if name == drafter.BackendClaude {
				return "/tools/claude", nil
			}
			return "", errors.New("not found")
		},
		Version: func(path string) (string, error) {
			if path != "/tools/claude" {
				t.Fatalf("version path = %q", path)
			}
			return "Claude Code 9.1\n", nil
		},
		ReadConfig: func() (userconfig.Config, error) { return current, nil },
		WriteConfig: func(cfg userconfig.Config) (string, error) {
			written = cfg
			return "/config/amq-squad/config.json", nil
		},
	}
	err := runSetupWithDependencies([]string{
		"--drafter-chain", "yoetz,claude,codex",
		"--drafter-model", "draft-model",
		"--drafter-effort", "high",
		"--drafter-timeout", "75",
		"--drafter-on-failure", "error",
	}, deps)
	if err != nil {
		t.Fatalf("runSetupWithDependencies: %v", err)
	}
	if written.Model != current.Model || written.CodexModel != current.CodexModel || written.ClaudeModel != current.ClaudeModel || !reflect.DeepEqual(written.Models, current.Models) {
		t.Fatalf("setup discarded unrelated config: %+v", written)
	}
	wantChain := []string{drafter.BackendYoetz, drafter.BackendClaude, drafter.BackendCodex}
	if written.Drafter == nil || !reflect.DeepEqual(written.Drafter.Chain, wantChain) || written.Drafter.Model != "draft-model" || written.Drafter.Effort != "high" || written.Drafter.TimeoutSeconds != 75 || written.Drafter.OnFailure != drafter.FailureError {
		t.Fatalf("written drafter = %+v", written.Drafter)
	}
	if !strings.Contains(stdout.String(), "claude: /tools/claude (Claude Code 9.1)") || !strings.Contains(stdout.String(), "Wrote user config: /config/amq-squad/config.json") {
		t.Fatalf("stdout missing probe or write result:\n%s", stdout.String())
	}
	for _, backend := range []string{drafter.BackendYoetz, drafter.BackendCodex} {
		if !strings.Contains(stderr.String(), "warning: configured drafter backend "+backend+" was not found on PATH") {
			t.Fatalf("stderr missing %s warning:\n%s", backend, stderr.String())
		}
	}
}

func TestSetupInteractiveDefaultsToDetectedChain(t *testing.T) {
	current := userconfig.Config{Model: "keep-me"}
	var written userconfig.Config
	var stdout, stderr bytes.Buffer
	deps := setupDependencies{
		In:  strings.NewReader("\ndraft-model\nmedium\n90\nerror\n"),
		Out: &stdout,
		Err: &stderr,
		LookPath: func(name string) (string, error) {
			switch name {
			case drafter.BackendYoetz, drafter.BackendCodex:
				return "/tools/" + name, nil
			default:
				return "", errors.New("not found")
			}
		},
		Version:    func(path string) (string, error) { return path + " v1", nil },
		ReadConfig: func() (userconfig.Config, error) { return current, nil },
		WriteConfig: func(cfg userconfig.Config) (string, error) {
			written = cfg
			return "/config.json", nil
		},
	}
	if err := runSetupWithDependencies(nil, deps); err != nil {
		t.Fatalf("interactive setup: %v\nstderr:\n%s", err, stderr.String())
	}
	wantChain := []string{drafter.BackendYoetz, drafter.BackendCodex}
	if written.Model != "keep-me" || written.Drafter == nil || !reflect.DeepEqual(written.Drafter.Chain, wantChain) {
		t.Fatalf("written config = %+v", written)
	}
	if written.Drafter.Model != "draft-model" || written.Drafter.Effort != "medium" || written.Drafter.TimeoutSeconds != 90 || written.Drafter.OnFailure != drafter.FailureError {
		t.Fatalf("interactive drafter = %+v", written.Drafter)
	}
	for _, want := range []string{
		"Drafter chain (yoetz,claude,codex or in_session) [yoetz,codex]:",
		"Drafter timeout seconds [180]:",
		"Configured drafter: yoetz,codex",
	} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("stdout missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestSetupInteractiveWithoutDetectedBackendsUsesInSession(t *testing.T) {
	var written userconfig.Config
	var stdout, stderr bytes.Buffer
	deps := setupDependencies{
		In:       strings.NewReader("\n"),
		Out:      &stdout,
		Err:      &stderr,
		LookPath: func(string) (string, error) { return "", errors.New("not found") },
		Version:  func(string) (string, error) { t.Fatal("unexpected version probe"); return "", nil },
		ReadConfig: func() (userconfig.Config, error) {
			return userconfig.Config{Model: "preserved"}, nil
		},
		WriteConfig: func(cfg userconfig.Config) (string, error) {
			written = cfg
			return "/config.json", nil
		},
	}
	if err := runSetupWithDependencies(nil, deps); err != nil {
		t.Fatalf("interactive in-session setup: %v", err)
	}
	if written.Model != "preserved" || written.Drafter != nil {
		t.Fatalf("written config = %+v, want preserved settings with nil drafter", written)
	}
	if !strings.Contains(stdout.String(), "none (in_session remains available)") || !strings.Contains(stdout.String(), "Configured drafter: in_session") {
		t.Fatalf("stdout missing in-session result:\n%s", stdout.String())
	}
}

func TestSetupNonInteractiveUpdatesExistingTrustedCustomConfigWithoutDiscardingCommand(t *testing.T) {
	current := userconfig.Config{Drafter: &drafter.Config{
		Chain:   []string{drafter.BackendCustom, drafter.BackendClaude},
		Command: []string{"local-drafter", "--out", "{out}"},
	}}
	var written userconfig.Config
	deps := setupDependencies{
		In:  strings.NewReader(""),
		Out: &bytes.Buffer{},
		Err: &bytes.Buffer{},
		LookPath: func(name string) (string, error) {
			if name == drafter.BackendClaude {
				return "/tools/claude", nil
			}
			return "", errors.New("not found")
		},
		Version:    func(string) (string, error) { return "v1", nil },
		ReadConfig: func() (userconfig.Config, error) { return current, nil },
		WriteConfig: func(cfg userconfig.Config) (string, error) {
			written = cfg
			return "/config.json", nil
		},
	}
	if err := runSetupWithDependencies([]string{"--drafter-timeout", "60"}, deps); err != nil {
		t.Fatalf("update custom config: %v", err)
	}
	if written.Drafter == nil || written.Drafter.TimeoutSeconds != 60 || !reflect.DeepEqual(written.Drafter.Command, current.Drafter.Command) || !reflect.DeepEqual(written.Drafter.Chain, current.Drafter.Chain) {
		t.Fatalf("custom config was not preserved: %+v", written.Drafter)
	}
	if current.Drafter.TimeoutSeconds != 0 {
		t.Fatalf("setup mutated input config: %+v", current.Drafter)
	}
}

func TestSetupRejectsInvalidNonInteractiveChainBeforeWrite(t *testing.T) {
	writes := 0
	deps := setupDependencies{
		In:         strings.NewReader(""),
		Out:        &bytes.Buffer{},
		Err:        &bytes.Buffer{},
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
		Version:    func(string) (string, error) { return "", nil },
		ReadConfig: func() (userconfig.Config, error) { return userconfig.Config{}, nil },
		WriteConfig: func(userconfig.Config) (string, error) {
			writes++
			return "/config.json", nil
		},
	}
	err := runSetupWithDependencies([]string{"--drafter-chain", "claude,claude"}, deps)
	if err == nil || !strings.Contains(err.Error(), "duplicate backend") {
		t.Fatalf("setup error = %v, want duplicate backend", err)
	}
	if writes != 0 {
		t.Fatalf("invalid setup performed %d writes", writes)
	}
}

func TestSetupIsPublicAndCompletable(t *testing.T) {
	if _, ok := lookupCommand("setup", "v-test"); !ok {
		t.Fatal("setup is not dispatchable")
	}
	if !containsString(commandNames("v-test"), "setup") || !containsString(completionTopCommands, "setup") {
		t.Fatal("setup is missing from public command or completion catalog")
	}
	for _, flag := range []string{"--drafter-chain", "--drafter-model", "--drafter-effort", "--drafter-timeout", "--drafter-on-failure"} {
		if !containsString(completionCommonFlags, flag) {
			t.Fatalf("completion flags missing %s", flag)
		}
	}
}
