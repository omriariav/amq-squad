//go:build unix

package uninstall

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// The two secret files hold these placeholders; no test ever formats them
// into a message, it only checks that the verb's output lacks them.
const (
	tokenBody   = "placeholder-token-body-not-a-secret"
	bindingBody = "placeholder-binding-body-not-a-secret"
)

type fixture struct {
	t       *testing.T
	home    string
	proj    string
	root    string // the AMQ session root
	x       string // <root>/extensions/amq-squad
	wt      string // the seat worktree
	ref     string // the evidence ref the sealed commits ledger records
	outside string // a directory no uninstall may touch
	opts    Options
}

func realDir(t *testing.T) string {
	t.Helper()
	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	argv := append([]string{"-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "-c", "protocol.file.allow=never"}, args...)
	cmd := exec.Command("git", argv...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

func write(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	write(t, path, string(b), 0o600)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{t: t, home: realDir(t), proj: realDir(t), outside: realDir(t)}
	f.root = filepath.Join(f.proj, ".agent-mail", "sess")
	f.x = filepath.Join(f.root, "extensions", "amq-squad")
	f.wt = filepath.Join(f.proj, ".claude", "worktrees", "seat-a")

	git(t, f.proj, "init", "-q", "-b", "main")
	write(t, filepath.Join(f.proj, "README"), "x\n", 0o644)
	git(t, f.proj, "add", "README")
	git(t, f.proj, "commit", "-q", "-m", "init")
	git(t, f.proj, "worktree", "add", "-q", "-b", "seat/a", f.wt)
	// a sealed commits ledger with a real evidence ref: the core's own calls
	head := git(t, f.proj, "rev-parse", "HEAD")
	writeJSON(t, filepath.Join(f.x, "launch", "seat-a", launch.FileName), launch.Record{Schema: 1, Handle: "seat-a", Worktree: f.wt, Branch: "seat/a"})
	if _, err := seatcommit.RecordBase(f.root, "seat-a", f.wt, "seat/a", head, time.Now()); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.wt, "left-behind.txt"), "work\n", 0o644)
	if ok, err := seatcommit.CleanSeat(f.root, "seat-a", "git", nil, time.Now()); err != nil || !ok {
		t.Fatalf("clean seat: %v %v", ok, err)
	}
	l, err := seatcommit.ReadLedger(f.root, "seat-a")
	if err != nil || len(l.EvidenceRefs()) != 1 {
		t.Fatalf("the sealed ledger: %v %v", err, l.EvidenceRefs())
	}
	f.ref = l.EvidenceRefs()[0]
	write(t, filepath.Join(f.x, "ledger", "seat-a.jsonl"), "{}\n", 0o600)
	write(t, filepath.Join(f.x, "routing", "outcomes.jsonl"), "{}\n", 0o600)
	write(t, filepath.Join(f.x, "scheduler", "run.lock"), "", 0o644)
	write(t, filepath.Join(f.x, "scheduler", "decisions.jsonl"), "{}\n", 0o600)
	writeJSON(t, filepath.Join(f.x, "scheduler", "launch.json"), map[string]any{"schema": 1, "lead": "lead", "root": f.root, "project_root": f.proj, "pid": 999999999})
	f.writeExits(`{"schema":1,"queue":[{"handle":"seat-a","task_id":"t0","why":"dispatch"}]}`)
	writeJSON(t, filepath.Join(f.x, "launch", "seat-a", launch.FileName), launch.Record{Schema: 1, Handle: "seat-a", Worktree: f.wt, Branch: "seat/a",
		Proof: &launch.ProofTicket{Token: launch.TokenConsumed, IssuedAt: time.Now().Add(-time.Hour)}})
	closed := time.Now().Add(-time.Minute)
	writeJSON(t, filepath.Join(f.x, "launch", "seat-a", "proof.json"), launch.ProofEntry{Handle: "seat-a", PID: 999999999, Start: "none", ClosedAt: &closed})
	write(t, filepath.Join(f.x, "launch", "seat-a", "claude-config", "L1", "settings.json"), "{}\n", 0o600)
	write(t, filepath.Join(f.x, "tools", "gh"), "gh-copy\n", 0o700)
	write(t, filepath.Join(f.x, "tools", "claude"), "claude-copy\n", 0o700)
	writeJSON(t, filepath.Join(f.x, "tasks", "t1.json"), map[string]any{"id": "t1", "project_path": f.proj, "attempts": []map[string]any{{"holder": "seat-a"}}})
	write(t, filepath.Join(f.x, "verifiers", "gomodcache", "mod", "m.go"), "package m\n", 0o444)
	if err := os.Chmod(filepath.Join(f.x, "verifiers", "gomodcache", "mod"), 0o555); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(f.x, "gates", "g1.question.json"), "{}\n", 0o600)
	write(t, filepath.Join(f.x, "clears", "seat-a.json"), "{}\n", 0o600)

	// owner files and the owner's global agent config
	for name, body := range map[string]string{"claude-token": tokenBody, "gate-binding.json": bindingBody, "registry.yaml": "r: 1\n", "grants.yaml": "g: 1\n"} {
		write(t, filepath.Join(f.home, ".amq-squad", name), body, 0o600)
	}
	write(t, filepath.Join(f.home, ".amq-squad", "fleet.yaml"), "f: 1\n", 0o600)
	write(t, filepath.Join(f.home, ".claude", "settings.json"), `{"enabledPlugins":{"amq-squad@amq-squad":true}}`+"\n", 0o600)
	write(t, filepath.Join(f.home, ".claude", "plugins", "cache", "amq-squad", "hooks.json"), "{}\n", 0o600)
	write(t, filepath.Join(f.home, ".codex", "config.toml"), "[projects."+quote(f.wt)+"]\ntrust_level = \"trusted\"\n", 0o600)
	write(t, filepath.Join(f.outside, "sentinel"), "keep\n", 0o600)

	f.opts = Options{Root: f.root, Home: f.home, Now: time.Now}
	t.Cleanup(func() { f.unlock() })
	return f
}

func quote(s string) string { return `"` + s + `"` }

func (f *fixture) writeExits(body string) {
	write(f.t, filepath.Join(f.x, "scheduler", "exits.json"), body, 0o600)
}

// unlock makes every directory writable again so TempDir cleanup works.
func (f *fixture) unlock() {
	for _, top := range []string{f.root, f.outside} {
		filepath.WalkDir(top, func(p string, d os.DirEntry, err error) error {
			if err == nil && d.IsDir() {
				os.Chmod(p, 0o700)
			}
			return nil
		})
	}
}

func (f *fixture) all() Options {
	o := f.opts
	o.Write, o.OwnerFiles, o.Purge = true, true, true
	return o
}

func (f *fixture) writeOnly() Options {
	o := f.opts
	o.Write = true
	return o
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// snap records dev, inode, mtime, mode and size of every entry under paths.
func snap(t *testing.T, paths ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range paths {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			fi, err := os.Lstat(p)
			if err != nil {
				return nil
			}
			st := fi.Sys().(*syscall.Stat_t)
			out[p] = strings.Join([]string{itoa(int64(st.Dev)), itoa(int64(st.Ino)), itoa(fi.ModTime().UnixNano()), fi.Mode().String(), itoa(fi.Size())}, ":")
			return nil
		})
	}
	return out
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func sameSnap(t *testing.T, before, after map[string]string, what string) {
	t.Helper()
	for p, v := range before {
		if after[p] != v {
			t.Errorf("%s: %s changed or was removed", what, p)
		}
	}
	for p := range after {
		if _, ok := before[p]; !ok {
			t.Errorf("%s: %s appeared", what, p)
		}
	}
}

func refExists(t *testing.T, repo, ref string) bool {
	t.Helper()
	cmd := exec.Command("git", "-C", repo, "rev-parse", "--verify", "-q", ref)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
	return cmd.Run() == nil
}

func liveToken(t *testing.T) string {
	tok, ok := procinfo.StartToken(os.Getpid())
	if !ok {
		t.Skip("no process start token on this host")
	}
	return tok
}
