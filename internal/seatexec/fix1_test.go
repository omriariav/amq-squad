//go:build unix

package seatexec

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Fix 1 item 5 (amendment 28): for a Codex seat the sandbox enforces the
// global-config denial, so a Codex argv without the workspace-write sandbox
// (or with a bypass) is refused. Acceptance 10 is this refusal plus the
// post-exit preflight diff below (milestone B runs it at every seat exit).
func TestSeatExecRefusesCodexWithoutTheSandbox(t *testing.T) {
	for _, argv := range [][]string{
		{"--model", "m"},
		{"--model", "m", "--sandbox", "danger-full-access"},
		{"--sandbox", "workspace-write", "--dangerously-bypass-approvals-and-sandbox"},
		{"--sandbox", "workspace-write", "-c", "sandbox_mode=danger-full-access"},
	} {
		f := newFixture(t, "codex")
		f.stage()
		err := f.run(Options{Argv: append([]string{filepath.Join(f.bin, "codex")}, argv...)})
		if refusalCode(err) != "codex_sandbox_missing" || f.execPath != "" {
			t.Errorf("codex %q: %v (exec %q)", argv, err, f.execPath)
		}
	}
	f := newFixture(t, "codex")
	f.stage()
	if err := f.run(Options{Argv: []string{filepath.Join(f.bin, "codex"), "--sandbox", "workspace-write", "--ask-for-approval", "on-request"}}); err != nil {
		t.Fatalf("workspace-write: %v", err)
	}
}

func TestPreflightDiffCatchesAGlobalConfigWrite(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	if changes, err := PreflightDiff(f.home); err != nil || len(changes) != 0 {
		t.Fatalf("untouched config: %+v %v", changes, err)
	}
	// What a Claude seat without its hook could do: rewrite the owner's file.
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"hooks":{}}`, 0o600)
	changes, err := PreflightDiff(f.home)
	if err != nil || len(changes) != 1 || !strings.HasSuffix(changes[0].File, filepath.Join(".claude", "settings.json")) {
		t.Fatalf("a seat write went unseen: %+v %v", changes, err)
	}
	// A config file created after the preflight copy is a change too.
	os.MkdirAll(filepath.Join(f.home, ".codex"), 0o700)
	write(t, filepath.Join(f.home, ".codex", "config.toml"), "x = 1\n", 0o600)
	if changes, _ := PreflightDiff(f.home); len(changes) != 2 {
		t.Fatalf("a new codex config went unseen: %+v", changes)
	}
}

// Fix 1 item 6: indirect credential sources go with the keys.
func TestSeatExecScrubsIndirectCredentialSources(t *testing.T) {
	indirect := []string{"AWS_SHARED_CREDENTIALS_FILE=/c", "AWS_CONFIG_FILE=/c", "AWS_WEB_IDENTITY_TOKEN_FILE=/c",
		"GOOGLE_APPLICATION_CREDENTIALS=/c", "KUBECONFIG=/c", "DOCKER_CONFIG=/c", "NPM_CONFIG_USERCONFIG=/c", "GIT_CONFIG_GLOBAL=/c"}
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{Env: f.env(indirect...)}); err != nil {
		t.Fatal(err)
	}
	for _, kv := range indirect {
		name, _, _ := strings.Cut(kv, "=")
		if Getenv(f.execEnv, name) != "" {
			t.Errorf("%s reached the seat", name)
		}
	}
	g := newFixture(t, "claude")
	g.rec.CredNeeds = []string{"KUBECONFIG"}
	g.stage()
	g.run(Options{Env: g.env(indirect...)})
	if Getenv(g.execEnv, "KUBECONFIG") != "/c" {
		t.Fatal("a declared need lost KUBECONFIG")
	}
}

// Fix 1 item 7: role and rules are re-hashed right before exec (after the
// identity proof), and the provider gets 0400 snapshots, not the sources.
func TestSeatExecRechecksRoleAndRulesBeforeExec(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	ext := launch.ExtensionDir(filepath.Join(f.root, "agents", "cto"))
	for name, path := range map[string]string{"role": Getenv(f.execEnv, "AMQ_SQUAD_ROLE_PATH"), "rules": Getenv(f.execEnv, "AMQ_SQUAD_RULES_PATH")} {
		st, err := os.Stat(path)
		if err != nil || filepath.Dir(path) != ext || st.Mode().Perm() != 0o400 {
			t.Fatalf("%s snapshot %s: %v %v", name, path, err, st)
		}
	}
	for _, mutate := range []func(*fixture){
		func(f *fixture) { write(f.t, f.rec.RulesPath, "# rules, changed during the proof\n", 0o600) },
		func(f *fixture) {
			write(f.t, filepath.Join(launch.StagedDir(f.root, "cto"), launch.RoleFileName), "changed during the proof\n", 0o600)
		},
	} {
		g := newFixture(t, "claude")
		g.stage()
		err := g.run(Options{BeforeRecheck: func() { mutate(g) }})
		if code := refusalCode(err); (code != "rules_mismatch" && code != "role_mismatch") || g.execPath != "" {
			t.Errorf("a change during the proof reached exec: %v", err)
		}
	}
}

// Fix 1 item 8 (amendment 28): the identity proof never drains, mail or not.
func TestIdentityProofNeverDrains(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.drainArgs[1:], " ") != "env --me cto --json" {
		t.Fatalf("the proof ran %v", f.drainArgs)
	}
}

// Fix 1 item 9: concurrent preflight writers each use their own temp file.
func TestPreflightCopyConcurrentWriters(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	write(t, filepath.Join(home, ".claude", "settings.json"), strings.Repeat("x", 1<<16), 0o600)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				if err := WritePreflight(home, true); err != nil {
					errs <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent preflight write: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(PreflightDir(home), "claude-settings.json")); len(b) != 1<<16 {
		t.Fatalf("copy has %d bytes", len(b))
	}
}
