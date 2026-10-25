//go:build unix

package seatexec

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Phase 5 re-pass P1-1: Codex reads a short option's value attached
// (-cVALUE); a sandbox or approval override in that form is refused like the
// spaced and "=" forms, and an attached -s or -a is read as the sandbox or
// the approval policy.
func TestCodexAttachedShortOptionsAreRead(t *testing.T) {
	ok := []string{"--sandbox", "workspace-write", "--ask-for-approval", "on-request"}
	for _, c := range []struct {
		argv []string
		code string
	}{
		{append([]string{`-csandbox_workspace_write.writable_roots=["/"]`}, ok...), "codex_sandbox_missing"},
		{append(append([]string{}, ok...), "-csandbox_mode=danger-full-access"), "codex_sandbox_missing"},
		{append(append([]string{}, ok...), "-capproval_policy=never"), "codex_approval_missing"},
		{[]string{"--sandbox", "workspace-write", "-sdanger-full-access", "-a", "on-request"}, "codex_sandbox_missing"},
		{[]string{"-s", "workspace-write", "-a", "on-request", "-anever"}, "codex_approval_missing"},
	} {
		if err := CheckCodexSandbox(append([]string{"codex"}, c.argv...)); refusalCode(err) != c.code {
			t.Errorf("codex %q: %v, want %s", c.argv, err, c.code)
		}
	}
	for _, argv := range [][]string{
		{"-sworkspace-write", "-aon-request"},
		{"-s", "read-only", "-a", "on-request", "-cmodel_reasoning_effort=high"},
		{"-s", "workspace-write", "-a", "on-request", "--", "-csandbox_mode=danger-full-access"},
	} {
		if err := CheckCodexSandbox(append([]string{"codex"}, argv...)); err != nil {
			t.Errorf("codex %q refused: %v", argv, err)
		}
	}
}

// Phase 5 re-pass P1-2: a Claude argv that excludes the user settings (where
// the seat's hooks plugin is enabled) or skips hooks is refused before the
// proof and the exec.
func TestClaudeArgvThatDropsTheHooksIsRefused(t *testing.T) {
	for _, extra := range [][]string{
		{"--setting-sources", "project,local"},
		{"--setting-sources=project"},
		{"--setting-sources", ""},
		{"--bare"},
		{"--safe-mode"},
	} {
		f := newFixture(t, "claude")
		f.stage()
		argv := append([]string{filepath.Join(f.bin, "claude"), "--permission-mode", "dontAsk"}, extra...)
		err := f.run(Options{Argv: argv})
		if refusalCode(err) != ClaudeHooksMissing || f.execPath != "" || len(f.proofs) != 0 || f.drainArgs != nil {
			t.Errorf("claude %q: %v (exec %q, proofs %d, amq %v)", extra, err, f.execPath, len(f.proofs), f.drainArgs)
		}
	}
	f := newFixture(t, "claude")
	f.stage()
	argv := []string{filepath.Join(f.bin, "claude"), "--permission-mode", "dontAsk", "--setting-sources", "user,project,local"}
	if err := f.run(Options{Argv: argv}); err != nil || f.execPath == "" {
		t.Fatalf("setting sources with user refused: %v", err)
	}
}

// Phase 5 re-pass P2-4: git's TLS trust switches never reach a seat and may
// not be declared.
func TestGitTLSTrustVariablesNeverReachASeat(t *testing.T) {
	names := []string{"GIT_SSL_NO_VERIFY", "GIT_SSL_CAINFO", "GIT_SSL_CAPATH"}
	var parent []string
	for _, n := range names {
		parent = append(parent, n+"=/x")
	}
	if env, _ := SeatEnv(parent, nil, names, nil); len(env) != 0 {
		t.Fatalf("seat env carries %d of the TLS trust variables", len(env))
	}
	for _, n := range names {
		f := newFixture(t, "claude")
		f.rec.EnvNeeds = []string{n}
		f.stage()
		if err := f.run(Options{Argv: []string{filepath.Join(f.bin, "claude")}}); refusalCode(err) != "env_needs_invalid" || f.execPath != "" {
			t.Errorf("%s in env_needs: %v", n, err)
		}
	}
}

// Phase 5 re-pass P2-5: a descendant of `amq env` that keeps the output pipe
// open never holds the proof past the wait bound: its process group is
// killed and the proof is refused as identity_proof_timeout.
func TestIdentityProofDescendantHoldingThePipesTimesOut(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "descendant.pid")
	amq := filepath.Join(dir, "amq")
	// Prints a valid identity, leaves a sleeper holding stdout, exits 0.
	write(t, amq, "#!/bin/sh\nsleep 30 &\necho $! > "+pidFile+"\nprintf '{\"root\":\"/r/s1\",\"me\":\"cto\",\"session_name\":\"s1\"}\\n'\nexit 0\n", 0o700)
	rec := launch.Record{Handle: "cto", Root: "/r/s1", Session: "s1", AMQPath: amq, Worktree: dir}
	start := time.Now()
	_, err := identityProof(Options{RunAMQ: runAMQ}, rec, []string{"PATH=/usr/bin:/bin"})
	elapsed := time.Since(start)
	if refusalCode(err) != "identity_proof_timeout" || elapsed > 15*time.Second {
		t.Fatalf("proof with a descendant holding the pipe: %v after %s", err, elapsed.Round(time.Second))
	}
	raw, rerr := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	if rerr != nil || pid <= 1 {
		t.Fatalf("descendant pid: %v", rerr)
	}
	for deadline := time.Now().Add(3 * time.Second); syscall.Kill(pid, 0) == nil; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the descendant (pid %d) survived the proof", pid)
		}
	}
}
