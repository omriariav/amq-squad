package procinfo

import (
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

// #87: a live process owned by another user returns EPERM from kill(0); that
// means it EXISTS, so it must read as alive (previously mislabeled dead).
func TestSignalErrMeansAlive(t *testing.T) {
	if !signalErrMeansAlive(nil) {
		t.Error("nil error (signalable) must be alive")
	}
	if !signalErrMeansAlive(syscall.EPERM) {
		t.Error("EPERM means the process exists but is unsignalable: must be alive")
	}
	if signalErrMeansAlive(syscall.ESRCH) {
		t.Error("ESRCH means no such process: must be dead")
	}
}

// #87: Match reads the command line fork-free (argsNative) and never forks ps
// when that succeeds — so it can't fail under fork pressure.
func TestMatchUsesNativeFastPath(t *testing.T) {
	orig := argsNative
	t.Cleanup(func() { argsNative = orig })

	isCodex := func(args string) bool {
		f := strings.Fields(args)
		return len(f) > 0 && f[0] == "codex"
	}
	argsNative = func(int) (string, bool) { return "codex app-server", true }
	if !Match(99999, isCodex) {
		t.Error("native args matching the predicate must match (no fork)")
	}
	argsNative = func(int) (string, bool) { return "node /x/foo.js", true }
	if Match(99999, isCodex) {
		t.Error("native args for a different binary must not match")
	}
	if Match(-1, isCodex) {
		t.Error("invalid pid must not match")
	}
	if Match(1, nil) {
		t.Error("nil predicate must not match")
	}
}

// #87: a transient ps fork failure (couldn't RUN) must be retried, not treated
// as the process being absent.
func TestReadArgsRetriesTransientFailure(t *testing.T) {
	calls := 0
	read := func(int) (string, bool, error) {
		calls++
		if calls < 3 {
			return "", false, errors.New("fork: resource temporarily unavailable") // could not run
		}
		return "amq wake --me cto --root /p", true, nil
	}
	args, ok := readArgsWithRetry(1234, read)
	if !ok || !strings.Contains(args, "amq wake") {
		t.Errorf("must read args after transient failures are retried: ok=%v args=%q", ok, args)
	}
	if calls != 3 {
		t.Errorf("expected 2 retries then success (3 reads), got %d", calls)
	}
}

// A definitive "pid absent" (ps ran, non-zero) must NOT be retried.
func TestReadArgsDefinitiveAbsentNoRetry(t *testing.T) {
	calls := 0
	read := func(int) (string, bool, error) {
		calls++
		return "", true, errors.New("exit status 1") // ps ran, pid gone
	}
	if _, ok := readArgsWithRetry(1234, read); ok {
		t.Error("a definitively absent pid must be not-ok")
	}
	if calls != 1 {
		t.Errorf("a definitive result must not retry, got %d reads", calls)
	}
}

// Persistent transient failure exhausts the retries and ends not-ok.
func TestReadArgsExhaustsRetries(t *testing.T) {
	calls := 0
	read := func(int) (string, bool, error) { calls++; return "", false, errors.New("fork fail") }
	if _, ok := readArgsWithRetry(1234, read); ok {
		t.Error("persistent failure must be not-ok")
	}
	if calls != psArgsAttempts {
		t.Errorf("expected %d attempts, got %d", psArgsAttempts, calls)
	}
}

// parseKernProcArgs2 reconstructs argv from the darwin sysctl buffer layout.
func TestParseKernProcArgs2(t *testing.T) {
	var b []byte
	b = append(b, 2, 0, 0, 0)                            // argc = 2 (little-endian)
	b = append(b, []byte("/usr/local/bin/codex\x00")...) // exec path
	b = append(b, 0, 0, 0)                               // NUL padding
	b = append(b, []byte("codex\x00app-server\x00")...)  // argv[0..1]
	b = append(b, []byte("HOME=/Users/x\x00")...)        // env (ignored)
	got, ok := parseKernProcArgs2(b)
	if !ok || got != "codex app-server" {
		t.Fatalf("parseKernProcArgs2 = %q, %v; want %q, true", got, ok, "codex app-server")
	}
	if _, ok := parseKernProcArgs2([]byte{1, 2}); ok {
		t.Error("a short buffer must be not-ok")
	}
	if _, ok := parseKernProcArgs2(append([]byte{0, 0, 0, 0}, []byte("x\x00")...)); ok {
		t.Error("argc=0 must be not-ok")
	}
}

func TestParseProcCmdline(t *testing.T) {
	got, ok := parseProcCmdline([]byte("claude\x00--permission-mode\x00auto\x00"))
	if !ok || got != "claude --permission-mode auto" {
		t.Fatalf("parseProcCmdline = %q, %v", got, ok)
	}
	if _, ok := parseProcCmdline(nil); ok {
		t.Error("empty cmdline must be not-ok")
	}
}

// ChildrenIndex must build a real, fork-free parent->children map: this test
// process is a child of its own parent (os.Getppid).
func TestChildrenIndexFindsRealChild(t *testing.T) {
	ci, err := ChildrenIndex()
	if err != nil {
		t.Skipf("no fork-free process table on this platform: %v", err)
	}
	ppid := os.Getppid()
	self := os.Getpid()
	found := false
	for _, c := range ci(ppid) {
		if c == self {
			found = true
		}
	}
	if !found {
		t.Errorf("ChildrenIndex(%d) did not include this process %d; children=%v", ppid, self, ci(ppid))
	}
	// A pid with no children returns an empty slice, not a panic.
	if got := ci(-1); len(got) != 0 {
		t.Errorf("ci(-1) = %v, want empty", got)
	}
}
