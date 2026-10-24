//go:build unix

package seatexec

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// TestSeatExecIdentityProofWithInstalledAMQ runs the drain proof against the
// installed amq in a scratch root. On amq 0.107.0 a drain with AM_SESSION
// unset succeeds (upstream now derives the session from AM_ROOT), so the S6
// finding A refusal is reproduced with a conflicting AM_SESSION pin, which
// amq still refuses (exit 5, "session context mismatch").
func TestSeatExecIdentityProofWithInstalledAMQ(t *testing.T) {
	amq, err := exec.LookPath("amq")
	if err != nil {
		t.Skip("amq is not installed on PATH (CI): the drain proof against the real amq runs on the owner's Mac")
	}
	f := newFixture(t, "codex")
	base := filepath.Dir(f.root)
	os.RemoveAll(base)
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + f.home}
	for _, args := range [][]string{{"init", "--root", base, "--agents", "operator,cto"}, {"session", "create", "s1", "--root", base, "--me", "operator", "--json"}} {
		cmd := exec.Command(amq, args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("amq %v: %v\n%s", args, err, out)
		}
	}
	os.MkdirAll(launch.StagedDir(f.root, "cto"), 0o700)
	filepath.Walk(base, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			os.Chmod(p, info.Mode().Perm()&^0o022)
		}
		return nil
	})
	f.rec.AMQPath = amq
	f.stage()
	run := func(mutate func([]string) []string) error {
		o := Options{Seat: "cto", Argv: []string{filepath.Join(f.bin, "codex"), "--sandbox", "workspace-write"}, Env: append(f.env(), env...), Home: f.home, UID: os.Getuid(), MutateEnv: mutate}
		o.Exec = func(path string, argv, env []string) error { f.execPath = path; return nil }
		return Run(o)
	}
	if err := run(nil); err != nil {
		t.Fatalf("drain proof with the full identity: %v", err)
	}
	f.execPath = ""
	wrongPin := func(env []string) []string {
		var out []string
		for _, kv := range env {
			if !strings.HasPrefix(kv, "AM_SESSION=") {
				out = append(out, kv)
			}
		}
		return append(out, "AM_SESSION=s2")
	}
	err = run(wrongPin)
	if refusalCode(err) != "identity_proof_failed" || f.execPath != "" || !strings.Contains(err.Error(), "session context mismatch") {
		t.Fatalf("a wrong pin must fail the drain and stop the seat: %v", err)
	}
}
