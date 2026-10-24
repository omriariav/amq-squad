//go:build unix

package seatexec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Fix 2 item 1: the long config form overrides the sandbox like -c, and the
// short sandbox flag is a legitimate sandbox.
func TestCodexSandboxConfigLongFormAndShortFlag(t *testing.T) {
	for _, argv := range [][]string{
		{"--sandbox", "workspace-write", "--config", "sandbox_mode=danger-full-access"},
		{"--sandbox", "workspace-write", "--config=sandbox_mode=danger-full-access"},
		{"--sandbox", "workspace-write", "-c=sandbox_mode=danger-full-access"},
		{"-s", "danger-full-access"},
	} {
		if err := CheckCodexSandbox(append([]string{"codex"}, argv...)); refusalCode(err) != "codex_sandbox_missing" {
			t.Errorf("codex %q passed: %v", argv, err)
		}
	}
	for _, argv := range [][]string{{"-s", "workspace-write", "-a", "on-request"}, {"-s=workspace-write", "--ask-for-approval=on-request"}, {"-s", "read-only", "--config", "model_reasoning_effort=high", "-a=on-request"}} {
		if err := CheckCodexSandbox(append([]string{"codex"}, argv...)); err != nil {
			t.Errorf("codex %q refused: %v", argv, err)
		}
	}
}

// Fix 2 item 2: an identity check that resolves no session does not prove a
// record that has one.
func TestIdentityProofRefusesAnEmptySession(t *testing.T) {
	f := newFixture(t, "claude")
	f.stage()
	o := Options{}
	o.Seat, o.Argv, o.Env, o.Home, o.UID = "cto", []string{filepath.Join(f.bin, "claude")}, f.env(), f.home, 0
	err := f.runWith(o, func(env []string) []byte {
		return []byte(fmt.Sprintf(`{"root":%q,"me":"cto","session_name":""}`, Getenv(env, "AM_ROOT")))
	})
	if refusalCode(err) != "identity_proof_failed" || !strings.Contains(err.Error(), "amq env resolved no session") || f.execPath != "" {
		t.Fatalf("empty session_name: %v (exec %q)", err, f.execPath)
	}
}

// runWith runs seat-exec with a custom amq env answer.
func (f *fixture) runWith(o Options, answer func(env []string) []byte) error {
	o.RunAMQ = func(_ context.Context, amq string, args, env []string, dir string) ([]byte, error) {
		f.drainArgs, f.drainEnv = append([]string{amq}, args...), env
		return answer(env), nil
	}
	o.Exec = func(path string, argv, env []string) error { f.execPath = path; return nil }
	o.SendProof = func(launch.ProofTicket, launch.ProofMessage) (launch.ProofReply, error) {
		return launch.ProofReply{OK: true}, nil
	}
	if o.UID == 0 {
		o.UID = os.Getuid()
	}
	return Run(o)
}
