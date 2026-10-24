//go:build unix

package seatexec

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Fix 3 item 3: a seat whose start stamp cannot be read is refused (the
// identity_incomplete class): CloseSeat could never confirm it.
func TestSeatExecRefusesAnUnreadableStartStamp(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	err := f.run(Options{StartToken: func(int) (string, bool) { return "", false }})
	if refusalCode(err) != IdentityIncomplete || f.execPath != "" {
		t.Fatalf("unreadable start stamp: %v (exec %q)", err, f.execPath)
	}
}

// Fix 3 item 5: seat-exec pins the workspace-write sandbox's own settings
// on the Codex command line (no extra writable roots; no network unless
// declared), and refuses an argv that sets them itself.
func TestCodexSandboxPinsBeatProjectConfig(t *testing.T) {
	f := newFixture(t, "codex")
	f.stage()
	if err := f.run(Options{}); err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(f.bin, "codex"), "-c", "sandbox_workspace_write.writable_roots=[]", "-c", "sandbox_workspace_write.network_access=false"}
	if strings.Join(f.execArgv[:5], " ") != strings.Join(want, " ") {
		t.Fatalf("exec argv %q, want the pins after the executable", f.execArgv)
	}
	g := newFixture(t, "codex")
	g.rec.EnvNeeds = []string{CodexNetworkNeed}
	g.stage()
	if err := g.run(Options{}); err != nil || !strings.Contains(strings.Join(g.execArgv, " "), "sandbox_workspace_write.network_access=true") {
		t.Fatalf("declared network: %v %q", err, g.execArgv)
	}
	h := newFixture(t, "codex")
	h.stage()
	argv := []string{filepath.Join(h.bin, "codex"), "--sandbox", "workspace-write", "-c", `sandbox_workspace_write.writable_roots=["~/.claude"]`}
	if err := h.run(Options{Argv: argv}); refusalCode(err) != "codex_sandbox_missing" {
		t.Fatalf("an argv widening the sandbox passed: %v", err)
	}
}

// Fix 3 item 6: askpass programs are scrubbed; env_needs entries are
// variable names and never scrubbed-class; only credential_needs keeps a
// credential, and never a host-session variable.
func TestAskpassScrubbedAndNeedsValidated(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	if err := f.run(Options{Env: f.env("GIT_ASKPASS=/a", "SSH_ASKPASS=/a")}); err != nil {
		t.Fatal(err)
	}
	if Getenv(f.execEnv, "GIT_ASKPASS") != "" || Getenv(f.execEnv, "SSH_ASKPASS") != "" {
		t.Fatal("an askpass program reached the seat")
	}
	for _, c := range []struct{ env, cred []string }{
		{env: []string{"GH_TOKEN"}}, {env: []string{"lower_case"}}, {env: []string{"GIT_ASKPASS"}},
		{env: []string{"CLAUDECODE"}}, {cred: []string{"CLAUDE_CODE_SESSION_ID"}}, {cred: []string{"bad name"}},
	} {
		g := newFixture(t, "claude")
		g.rec.EnvNeeds, g.rec.CredNeeds = c.env, c.cred
		g.stage()
		if err := g.run(Options{}); refusalCode(err) != "env_needs_invalid" || g.execPath != "" {
			t.Errorf("needs %+v: %v", c, err)
		}
	}
	g := newFixture(t, "claude")
	g.rec.CredNeeds = []string{"GH_TOKEN"}
	g.stage()
	if err := g.run(Options{Env: g.env("GH_TOKEN=t")}); err != nil || Getenv(g.execEnv, "GH_TOKEN") != "t" {
		t.Fatalf("a declared credential need: %v", err)
	}
}

// Fix 3: the sandbox scan stops at "--" (what follows is the prompt).
func TestCodexSandboxScanStopsAtDoubleDash(t *testing.T) {
	argv := []string{"codex", "--sandbox", "danger-full-access", "--", "--sandbox=workspace-write"}
	if err := CheckCodexSandbox(argv); refusalCode(err) != "codex_sandbox_missing" {
		t.Fatalf("a sandbox after -- was honoured: %v", err)
	}
}

// Fix 3: a sandbox refusal is published as the seat's exec record, so the
// launch reports codex_sandbox_missing, not no_exec_record.
func TestSandboxRefusalIsPublished(t *testing.T) {
	f := newFixture(t, "codex")
	f.stage()
	err := f.run(Options{Argv: []string{filepath.Join(f.bin, "codex"), "--model", "m"}})
	if refusalCode(err) != "codex_sandbox_missing" {
		t.Fatalf("no sandbox: %v", err)
	}
	rec, rerr := launch.Read(filepath.Join(f.root, "agents", "cto"))
	if rerr != nil || rec.Exec == nil || rec.Exec.Outcome != "refused" || rec.Exec.ReasonCode != "codex_sandbox_missing" {
		t.Fatalf("published record: %+v %v", rec.Exec, rerr)
	}
}
