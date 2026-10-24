package seatcommit

import (
	"crypto/ed25519"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// fixture is a repository with a bare origin, a main checkout and one seat
// worktree on its own branch, an AMQ session root with the seat's staged
// launch record and core-recorded base, and a task store.
type fixture struct {
	t                                 *testing.T
	dir, origin, main, wt, root, home string
	handle, branch, base              string
	store                             task.Store
	now                               time.Time
	grants                            grants.Resolved
	gates                             GateChecker
	signer                            testSigner
}

// testSigner and testTrust are the owner's issuer key and trust store of the
// fixture (a fresh Ed25519 key).
type testSigner struct{ key ed25519.PrivateKey }

func (s testSigner) KeyID() string { return "test-key" }
func (s testSigner) Sign(b []byte) ([]byte, error) {
	return ed25519.Sign(s.key, b), nil
}

type testTrust struct{ pub ed25519.PublicKey }

func (v testTrust) Verify(keyID string, payload, sig []byte) error {
	if keyID != "test-key" || !ed25519.Verify(v.pub, payload, sig) {
		return errors.New("not trusted")
	}
	return nil
}

func (f *fixture) trust() ClearVerifier {
	return testTrust{pub: f.signer.key.Public().(ed25519.PublicKey)}
}

// fixtureRepo is the merge grant's repository the fixture's launch records.
const fixtureRepo = "o/r"

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

func gitBin(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not on PATH")
	}
	return p
}

// sh runs git in dir with a fixed test identity and no inherited GIT_*.
func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(gitBin(t), args...)
	cmd.Dir = dir
	cmd.Env = append(harnessEnv(os.Environ()), "GIT_AUTHOR_NAME=seat", "GIT_AUTHOR_EMAIL=seat@example.invalid", "GIT_COMMITTER_NAME=seat", "GIT_COMMITTER_EMAIL=seat@example.invalid", "HOME="+filepath.Dir(dir))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const claudeMD = "# Project\n\nfree text\n\n" + rules.BeginMarker + "\nmanaged rules\n" + rules.EndMarker + "\n"

func newFixture(t *testing.T) *fixture {
	t.Helper()
	gitBin(t)
	dir := t.TempDir()
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	f := &fixture{t: t, dir: dir, origin: filepath.Join(dir, "origin.git"), main: filepath.Join(dir, "repo"), wt: filepath.Join(dir, "wt", "s1"),
		root: filepath.Join(dir, "amq", "sess"), home: filepath.Join(dir, "home"), handle: "s1", branch: "seat/s1", now: t0}
	for _, d := range []string{f.main, f.root, f.home} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	sh(t, dir, "init", "-q", "--bare", "-b", "main", f.origin)
	// No detached auto-gc after a push: it would outlive the test's TempDir.
	sh(t, f.origin, "config", "receive.autogc", "false")
	sh(t, f.origin, "config", "gc.auto", "0")
	sh(t, f.main, "init", "-q", "-b", "main")
	write(t, filepath.Join(f.main, "README.md"), "readme\n")
	write(t, filepath.Join(f.main, "CLAUDE.md"), claudeMD)
	write(t, filepath.Join(f.main, "cmd", "root.go"), "package cmd\n")
	write(t, filepath.Join(f.main, "internal", "client", "factory_test.go"), "package client\n")
	write(t, filepath.Join(f.main, ".github", "workflows", "ci.yml"), "on: push\n")
	sh(t, f.main, "add", "-A")
	sh(t, f.main, "commit", "-q", "-m", "initial")
	sh(t, f.main, "remote", "add", "origin", f.origin)
	sh(t, f.main, "push", "-q", "origin", "main")
	sh(t, f.main, "worktree", "add", "-q", "-b", f.branch, f.wt, "main")
	f.base = sh(t, f.wt, "rev-parse", "HEAD")
	f.writeRecord("claude")
	_, key, _ := ed25519.GenerateKey(nil)
	f.signer = testSigner{key: key}
	// The launch's record of the owner's grant: the push URL is the bare
	// origin (never read from the seat's repository).
	if _, err := RecordWorktreeBase(gitBin(t), f.root, f.handle, f.wt, f.branch, Target{Repository: fixtureRepo, BaseBranch: "main", PushURL: f.origin}, t0); err != nil {
		t.Fatal(err)
	}
	var err error
	if f.store, err = task.Open(f.root); err != nil {
		t.Fatal(err)
	}
	f.grants = grants.Resolved{Grants: grants.Defaults(), Source: "default"}
	return f
}

func (f *fixture) writeRecord(runtime string) {
	dir := launch.StagedDir(f.root, f.handle)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		f.t.Fatal(err)
	}
	rec := launch.Record{Handle: f.handle, Root: f.root, Session: "sess", Runtime: runtime, Worktree: f.wt, Branch: f.branch}
	if err := launch.WriteFile(filepath.Join(dir, launch.FileName), rec); err != nil {
		f.t.Fatal(err)
	}
}

// reported adds a task with the declared files, leases it to the seat and
// files the seat's done report.
func (f *fixture) reported(files ...string) task.Task {
	f.t.Helper()
	tk, err := f.store.Add(task.AddInput{Title: "the task", Class: "implement", DeclaredFiles: files, ProjectPath: f.main}, f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	if _, err := f.store.Claim(tk.ID, f.handle, nil, time.Hour, f.now); err != nil {
		f.t.Fatal(err)
	}
	tk, err = f.store.Done(tk.ID, f.handle, "go test ok", "", f.now)
	if err != nil {
		f.t.Fatal(err)
	}
	return tk
}

func (f *fixture) opts(tk task.Task) Options {
	return Options{Root: f.root, Handle: f.handle, Task: tk, Grants: f.grants, Home: f.home, Git: gitBin(f.t), Gates: f.gates, Clear: f.trust(), Now: func() time.Time { return f.now }}
}

func (f *fixture) run(tk task.Task) Result {
	f.t.Helper()
	return Run(f.opts(tk))
}

// originBranch is the origin's seat branch tip, "" when absent.
func (f *fixture) originBranch() string {
	cmd := exec.Command(gitBin(f.t), "rev-parse", "--verify", "-q", "refs/heads/"+f.branch)
	cmd.Dir = f.origin
	out, _ := cmd.Output()
	return strings.TrimSpace(string(out))
}

type fakeGates map[string]bool

func (g fakeGates) Accepted(target string) bool { return g[target] }
