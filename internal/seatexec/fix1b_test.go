//go:build unix

package seatexec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// Milestone B first pass, P1 (seatexec_unix.go:128 and :209): the Claude
// config dir was checked before the identity proof and the recheck covered
// role and rules only, so a change during the proof reached the provider.
// The recheck immediately before exec now covers the config dir too.
func TestClaudeConfigDirIsRecheckedImmediatelyBeforeExec(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	dir := claudeseat.Dir(f.root, "cto", "launch-1")
	err := f.run(Options{BeforeRecheck: func() {
		// After the first check and the proof: an allow rule lands in the
		// seat's settings.json.
		write(t, filepath.Join(dir, "settings.json"), `{"permissions":{"defaultMode":"dontAsk","allow":["Bash(*)"]},"enabledPlugins":{"amq-squad@amq-squad":true}}`, 0o600)
	}})
	if refusalCode(err) != claudeseat.PermissionMismatch || f.execPath != "" {
		t.Fatalf("want %s and no exec, got %v (exec %q)", claudeseat.PermissionMismatch, err, f.execPath)
	}
	if !strings.Contains(err.Error(), "changed before exec") {
		t.Fatalf("the refusal does not name the recheck: %v", err)
	}
}

// Milestone B first pass, P2 (seatexec_unix.go:184, :341): the provider
// environment, with the seat token and the provider keys the runtime
// declares, went to the AMQ identity-proof subprocess. The proof now gets
// the seat's identity and no credential; the provider still gets its own.
// The canary asserts on the AMQ child's environment, not only its argv.
func TestIdentityProofChildGetsNoCredential(t *testing.T) {
	f := newFixture(t, "claude")
	const provider = "AIzaSyCANARYprovider2b2bfix1b000000"
	f.rec.CredNeeds = []string{"GEMINI_API_KEY"}
	f.stage()
	if err := f.run(Options{Env: f.env("GEMINI_API_KEY=" + provider)}); err != nil {
		t.Fatal(err)
	}
	if Getenv(f.execEnv, claudeseat.TokenVar) != canaryToken || Getenv(f.execEnv, "GEMINI_API_KEY") != provider {
		t.Fatal("the provider lost its token or its declared key")
	}
	if len(f.drainEnv) == 0 || Getenv(f.drainEnv, "AM_ME") != "cto" {
		t.Fatal("the identity proof did not run with the seat's identity")
	}
	for _, kv := range f.drainEnv {
		name, _, _ := strings.Cut(kv, "=")
		if redact.CredentialName(name) || strings.Contains(kv, canaryToken) || strings.Contains(kv, provider) {
			t.Fatalf("the AMQ identity-proof child got the credential %s", name)
		}
	}
}

// The canary check of the token row extends to the AMQ child (reviewer
// condition 2 of the token file, asserted on the environment as well).
func TestClaudeTokenCanaryNeverReachesTheAMQChild(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	for _, kv := range append(append([]string{}, f.drainArgs...), f.drainEnv...) {
		if strings.Contains(kv, canaryToken) {
			name, _, _ := strings.Cut(kv, "=")
			t.Fatalf("the AMQ child carries the token in %s", name)
		}
	}
	if _, err := os.Stat(claudeseat.TokenPath(f.home)); err != nil {
		t.Fatal(err)
	}
}
