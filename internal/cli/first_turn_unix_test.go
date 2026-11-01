//go:build unix

package cli

import (
	"context"
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
// process group at the deadline and its fact reads missing. context --host
// returns.
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
	// wide enough for a slow shell on a loaded host to start its child and
	// write its pid (1.5 s was not, twice in 65 runs)
	contextHostDeadline = 4 * time.Second
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
	case <-time.After(20 * time.Second):
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
		t.Fatalf("host with a hanging git = %v", h)
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

// Codex's re-check on #837, item 2: the host facts share one budget, and
// every group the read started is killed when it ends. A git whose leader
// returns but leaves a child behind, then a git that hangs: the read ends
// within the one budget (not a fresh deadline per command), the first fact
// stands, the rest read missing, and no process the read started is left.
func TestContextHostSharesOneBudgetAndLeavesNoProcessBehind(t *testing.T) {
	f := newHookFixture(t)
	bin := t.TempDir()
	pids := filepath.Join(bin, "pids")
	mustWrite(t, filepath.Join(bin, "git"), "#!/bin/sh\ncase \"$2\" in\n"+
		"--abbrev-ref) sleep 30 >/dev/null 2>&1 & echo $! >> "+pids+"; sleep 0.1; echo main ;;\n"+
		"*) sleep 30 >/dev/null 2>&1 & echo $! >> "+pids+"; sleep 30 ;;\nesac\n")
	mustWrite(t, filepath.Join(bin, "amq"), "#!/bin/sh\nsleep 30 &\necho $! >> "+pids+"\nwait\n")
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
	// one budget: the read ends near 4 s; a fresh deadline for each of the
	// hanging git and the hanging amq would take at least 8 s
	contextHostDeadline = 4 * time.Second
	began := time.Now()
	h := hostFacts()
	took := time.Since(began)
	if took > 7*time.Second {
		t.Fatalf("the host read took %s with a %s budget: each command had its own deadline", took, contextHostDeadline)
	}
	if h.Branch != "main" || h.Tip != "" || h.AMQVersion != "" {
		t.Fatalf("host = %+v", h)
	}
	raw, err := os.ReadFile(pids)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, l := range strings.Fields(string(raw)) {
		pid, _ := strconv.Atoi(l)
		for pid > 1 && syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				t.Fatalf("pid %d, started by the host read, outlived it", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}

// Codex on #837, item 5: a host-fact command that prints more than the cap
// is no fact.
func TestContextHostRefusesAFloodingCommand(t *testing.T) {
	bin := t.TempDir()
	amq := filepath.Join(bin, "amq")
	mustWrite(t, amq, "#!/bin/sh\nhead -c 10000 /dev/zero | tr '\\0' 'v'\n")
	if err := os.Chmod(amq, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := &hostCall{ctx: ctx}
	defer c.sweep()
	if out, err := contextHostRun(c, bin, amq, "--version"); err == nil || out != "" {
		t.Fatalf("a flooding amq = %q, %v", out, err)
	}
}
