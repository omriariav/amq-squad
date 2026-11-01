//go:build unix

package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Codex on #837, item 5: a host-fact command that hangs (a git that never
// returns, its child holding the output open) is killed with its whole
// process group at the deadline and its fact reads missing; an amq that
// prints more than the cap is no fact either. context --host returns.
func TestContextHostKillsAHangingCommandsGroupAndNamesTheFactMissing(t *testing.T) {
	f := newHookFixture(t)
	bin := t.TempDir()
	pidfile := filepath.Join(bin, "child.pid")
	mustWrite(t, filepath.Join(bin, "git"), "#!/bin/sh\nsleep 30 &\necho $! > "+pidfile+"\nwait\n")
	mustWrite(t, filepath.Join(bin, "amq"), "#!/bin/sh\nhead -c 10000 /dev/zero | tr '\\0' 'v'\n")
	for _, n := range []string{"git", "amq"} {
		if err := os.Chmod(filepath.Join(bin, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AMQ_SQUAD_PROJECT", f.project)
	t.Setenv("AMQ_SQUAD_AMQ", filepath.Join(bin, "amq"))
	prev := contextHostDeadline
	t.Cleanup(func() { contextHostDeadline = prev })
	contextHostDeadline = 300 * time.Millisecond
	type result struct {
		out string
		err error
	}
	done := make(chan result, 1)
	go func() {
		out, _, err := captureOutput(t, func() error { return Run([]string{"context", "--json", "--host"}, "test") })
		done <- result{out, err}
	}()
	var r result
	select {
	case r = <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("context --host did not return: the hanging git was not killed")
	}
	if r.err != nil {
		t.Fatal(r.err)
	}
	var env struct {
		Data struct {
			Host map[string]any `json:"host"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(r.out), &env); err != nil {
		t.Fatalf("context json: %v\n%s", err, r.out)
	}
	if h := env.Data.Host; h["branch"] != "" || h["tip"] != "" || h["amq_version"] != "" || h["repository"] != f.project {
		t.Fatalf("host with a hanging git and a flooding amq = %v", h)
	}
	raw, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the hanging git's child %d outlived the deadline (its group was not killed)", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
