//go:build unix

package seatcommit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// restartHelper is what the helper process needs to run the commit step as
// the first incarnation would.
type restartHelper struct {
	Root, Handle, Home, Git, Origin, Command string
	Task                                     task.Task
}

const restartHelperEnv = "AMQ_SQUAD_TEST_VERIFIER_RESTART_HELPER"

// The helper process: the first scheduler incarnation's commit step, killed
// by the test while its verifier runs.
func TestVerifierRestartHelperProcess(t *testing.T) {
	raw := os.Getenv(restartHelperEnv)
	if raw == "" {
		t.Skip("the helper process of TestVerifierInterruptedByARestartRunsOnce")
	}
	var h restartHelper
	if err := json.Unmarshal([]byte(raw), &h); err != nil {
		t.Fatal(err)
	}
	g := grants.Resolved{Grants: grants.Defaults(), Source: "default"}
	g.Merge = []grants.Merge{{Repository: fixtureRepo, BaseBranch: "main", SizeCapLines: grants.DefaultSizeCapLines, PushURL: h.Origin, LocalPushAck: true}}
	g.Verifiers = map[string]string{"implement": h.Command}
	Run(Options{Root: h.Root, Handle: h.Handle, Task: h.Task, Grants: g, Home: h.Home, Git: h.Git, Now: time.Now})
}

// v3-5-verifier, the liveness ruling: a restart during a verifier run (the
// harness killed mid-run) defers the adopted step as verifier_interrupted
// and never runs the verifier twice on the same snapshot; nothing is pushed.
func TestVerifierInterruptedByARestartRunsOnce(t *testing.T) {
	f := newFixture(t)
	runs, pidf := filepath.Join(f.dir, "runs.txt"), filepath.Join(f.dir, "verifier.pid")
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// restart\n")
	f.verifying(f.script("v", "echo run >> "+runs+"\necho $$ > "+pidf+"\nexec /bin/sleep 600\n"))
	tk := f.reportedBare("cmd/**")
	raw, _ := json.Marshal(restartHelper{Root: f.root, Handle: f.handle, Home: f.home, Git: gitBin(t), Origin: f.origin, Command: f.grants.Verifiers["implement"], Task: tk})
	helper := exec.Command(os.Args[0], "-test.run=^TestVerifierRestartHelperProcess$", "-test.count=1")
	helper.Env = append(os.Environ(), restartHelperEnv+"="+string(raw))
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	killVerifier := func() {
		if pid := pidFrom(pidf); pid > 1 {
			syscall.Kill(-pid, syscall.SIGKILL)
		}
	}
	t.Cleanup(func() { helper.Process.Kill(); helper.Wait(); killVerifier() })
	started := false
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline) && !started; time.Sleep(50 * time.Millisecond) {
		b, _ := os.ReadFile(runs)
		started = strings.Count(string(b), "run") == 1 && pidFrom(pidf) > 1
	}
	if !started {
		t.Fatal("the first incarnation's verifier never started")
	}
	// The first incarnation dies mid-run, its verifier with it.
	helper.Process.Kill()
	helper.Wait()
	killVerifier()
	res, _ := f.runWithin(tk, 30*time.Second, pidf)
	if !res.Deferred || res.ReasonCode != ReasonVerifierInterrupted {
		t.Fatalf("the adopted step: deferred %v %s", res.Deferred, res.ReasonCode)
	}
	if b, _ := os.ReadFile(runs); strings.Count(string(b), "run") != 1 {
		t.Fatalf("the verifier ran %d times on the same snapshot", strings.Count(string(b), "run"))
	}
	if f.originBranch() != "" {
		t.Fatal("an interrupted step pushed")
	}
}
