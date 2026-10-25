//go:build unix

package seatcommit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func pidFrom(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return pid
}

func alivePIDFile(path string) bool {
	pid := pidFrom(path)
	return pid > 1 && syscall.Kill(pid, 0) == nil
}

// Rule 12: the verifier's process group is killed whole at the bound; an
// escaped descendant (a new session of its own) holding stdout does not
// extend it, and a child still in the group goes with it.
func TestVerifierProcessGroupKilledWholeAtTheBound(t *testing.T) {
	f := newFixture(t)
	pidf, child := filepath.Join(f.dir, "escaped.pid"), filepath.Join(f.dir, "child.pid")
	t.Cleanup(func() { killPIDFile(pidf); killPIDFile(child) })
	write(t, filepath.Join(f.wt, "cmd", "root.go"), "package cmd\n\n// hold\n")
	f.verifying(f.script("v", "/usr/bin/perl -e 'use POSIX; POSIX::setsid(); open(my $f, \">\", \""+pidf+"\"); print $f $$; close $f; exec \"/bin/sleep\", \"600\"' &\n"+
		"/bin/sleep 600 &\necho $! > "+child+"\nexec /bin/sleep 600\n"))
	f.grants.VerifierTimeout = 2 * time.Second
	tk := f.reportedBare("cmd/**")
	start := time.Now()
	res, ok := f.runWithin(tk, 30*time.Second, pidf)
	if !ok {
		t.Fatal("an escaped descendant holding stdout extended the verifier past its bound")
	}
	if res.Verifier == nil || res.Verifier.Reason != ReasonVerifierTimeout || time.Since(start) > 20*time.Second {
		t.Fatalf("the bound: %+v after %s", res.Verifier, time.Since(start).Round(time.Second))
	}
	if alivePIDFile(child) {
		t.Fatal("a child in the verifier's process group survived the bound")
	}
}
