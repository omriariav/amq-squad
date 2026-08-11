package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
)

func TestResolveCLIDrafterUsesWholeBlockPrecedence(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, []byte(`{"drafter":{"backend":"custom","command":["trusted-drafter"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_SQUAD_CONFIG", configPath)

	global, err := resolveCLIDrafter(nil)
	if err != nil {
		t.Fatal(err)
	}
	if global.Source != drafter.SourceGlobal || global.Config == nil || global.Config.EffectiveBackend() != drafter.BackendCustom {
		t.Fatalf("global resolution = %+v", global)
	}

	profile, err := resolveCLIDrafter(&drafter.Config{Backend: drafter.BackendClaude})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Source != drafter.SourceProfile || profile.Config == nil || profile.Config.EffectiveBackend() != drafter.BackendClaude {
		t.Fatalf("profile resolution = %+v", profile)
	}

	if err := os.WriteFile(configPath, []byte(`{"drafter":`), 0o600); err != nil {
		t.Fatal(err)
	}
	profile, err = resolveCLIDrafter(&drafter.Config{Backend: drafter.BackendCodex})
	if err != nil || profile.Source != drafter.SourceProfile {
		t.Fatalf("profile should shadow malformed global config: resolution=%+v err=%v", profile, err)
	}
	if _, err := resolveCLIDrafter(nil); err == nil || !strings.Contains(err.Error(), "read user drafter config") {
		t.Fatalf("unshadowed malformed global error = %v", err)
	}
}

func TestCLIDrafterAttemptEvidencePreservesOrderedChain(t *testing.T) {
	attempts := []drafter.Evidence{
		{Backend: drafter.BackendYoetz, Command: []string{"yoetz", "ask"}, CommandDisplay: "yoetz ask", Failure: "missing credentials"},
		{Backend: drafter.BackendClaude, Command: []string{"claude", "-p"}, CommandDisplay: "claude -p"},
	}
	for _, want := range []string{
		"attempt[1] backend=yoetz", `command="yoetz ask"`, `fall-through="missing credentials"`,
		"attempt[2] backend=claude", `command="claude -p"`,
	} {
		if got := cliDrafterFailureEvidence(attempts, attempts[1]); !strings.Contains(got, want) {
			t.Fatalf("failure evidence missing %q: %s", want, got)
		}
	}
	for _, source := range []string{drafter.SourceProfile, drafter.SourceGlobal} {
		got := cliDrafterErrorEvidence(source, attempts, attempts[1])
		for _, want := range []string{
			"drafter config source: " + source,
			"attempt[1] backend=yoetz",
			"attempt[2] backend=claude",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("%s error evidence missing %q: %s", source, want, got)
			}
		}
	}
	for _, want := range []string{"Drafter attempt (yoetz): yoetz ask", "Fall-through: missing credentials", "Drafter attempt (claude): claude -p"} {
		if got := cliDrafterAttemptsText(attempts, attempts[1]); !strings.Contains(got, want) {
			t.Fatalf("human evidence missing %q: %s", want, got)
		}
	}
	cloned := cloneCLIDrafterAttempts(attempts)
	cloned[0].Command[0] = "changed"
	if attempts[0].Command[0] != "yoetz" {
		t.Fatalf("attempt clone aliased the source command: %+v", attempts)
	}
}
