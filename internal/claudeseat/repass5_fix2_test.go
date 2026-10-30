//go:build unix

package claudeseat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func probeSeatDir(t *testing.T) (string, string) {
	t.Helper()
	base, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(base, "cfg", "launch-1")
	if _, err := Write(dir, "dontAsk", filepath.Join(base, "wt"), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	return base, dir
}

func fakeClaude(t *testing.T, base, body string) func(args ...string) *exec.Cmd {
	t.Helper()
	p := filepath.Join(base, "fake-claude")
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return func(args ...string) *exec.Cmd { return exec.Command(p, args...) }
}

// The narrow pass's item 4: a descendant that escaped the probe's group
// (a new session of its own) and holds the stream open never keeps the probe
// past its deadline.
func TestProbeDeadlineHoldsAgainstAnEscapedDescendant(t *testing.T) {
	base, dir := probeSeatDir(t)
	pidf := filepath.Join(base, "escaped.pid")
	run := fakeClaude(t, base, "perl -e 'use POSIX; POSIX::setsid(); open(my $f, \">\", \""+pidf+"\"); print $f $$; close $f; exec \"sleep\", \"600\"' &\nsleep 1\nexit 0\n")
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidf); err == nil {
			if pid, _ := strconv.Atoi(strings.TrimSpace(string(raw))); pid > 1 {
				syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	prev := ProbeTimeout
	ProbeTimeout = 2 * time.Second
	t.Cleanup(func() { ProbeTimeout = prev })
	start := time.Now()
	done := make(chan error, 1)
	go func() {
		_, err := ProbeMode(context.Background(), run, dir, "dontAsk")
		done <- err
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(8 * time.Second):
		t.Fatal("an escaped descendant held the probe past its deadline")
	}
	if err == nil || time.Since(start) > 6*time.Second {
		t.Fatalf("an escaped descendant holding the stream: %v after %s", err, time.Since(start).Round(time.Millisecond))
	}
}

// The narrow pass's item 6: the probed claude is the leader of a new
// session (Setsid), so it has no controlling terminal to prompt on.
func TestProbeChildIsASessionLeaderWithNoTTY(t *testing.T) {
	base, dir := probeSeatDir(t)
	pidf, ttyf, ack := filepath.Join(base, "child.pid"), filepath.Join(base, "tty"), filepath.Join(base, "ack")
	// The stand-in writes its pid and waits for the test's ack (the test has
	// read its session id meanwhile), then prints the init event.
	run := fakeClaude(t, base, "echo $$ > "+pidf+"\nif (exec 3</dev/tty) 2>/dev/null; then echo opened > "+ttyf+"; else echo refused > "+ttyf+"; fi\n"+
		"while [ ! -e "+ack+" ]; do sleep 0.05; done\n"+
		"echo '{\"type\":\"system\",\"subtype\":\"init\",\"permissionMode\":\"dontAsk\",\"claude_code_version\":\"fake\"}'\n")
	prev := ProbeTimeout
	ProbeTimeout = 60 * time.Second // a loaded host; the 5 s bound is its own row
	t.Cleanup(func() { ProbeTimeout = prev })
	sid := make(chan int, 1)
	go func() {
		defer os.WriteFile(ack, nil, 0o600)
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			raw, err := os.ReadFile(pidf)
			if err != nil {
				continue
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
			if pid <= 1 {
				continue
			}
			s, err := unix.Getsid(pid)
			switch {
			case err != nil:
				sid <- 0
			case s == pid:
				sid <- 1
			default:
				sid <- -1
			}
			return
		}
		sid <- 0
	}()
	if _, err := ProbeMode(context.Background(), run, dir, "dontAsk"); err != nil {
		t.Fatal(err)
	}
	if got := <-sid; got != 1 {
		t.Fatalf("the probe child is not a session leader (%d)", got)
	}
	if raw, _ := os.ReadFile(ttyf); strings.TrimSpace(string(raw)) != "refused" {
		t.Fatalf("the probe child opened /dev/tty: %q", raw)
	}
}

// The narrow pass's item 5: the probe kind carries the pin's digest and the
// copied config dir (plugins included).
func TestProbeKindTracksThePinAndThePlugin(t *testing.T) {
	_, dir := probeSeatDir(t)
	a, _ := ProbeKind(dir, "dontAsk", "sha-a")
	b, _ := ProbeKind(dir, "dontAsk", "sha-b")
	if a == b {
		t.Fatal("two pins share a probe kind")
	}
	version, _ := pluginVersion()
	hooks := filepath.Join(dir, "plugins", "cache", "amq-squad", "amq-squad", version, "hooks", "hooks.json")
	raw, err := os.ReadFile(hooks)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(hooks, append(raw, ' '), 0o600)
	if c, _ := ProbeKind(dir, "dontAsk", "sha-a"); c == a {
		t.Fatal("a plugin change kept the probe kind")
	}
}
