//go:build amqpi

package cli

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLiveR7FirstRun is acceptance 7 against the real embedded pi (reviewer
// ruling after the jiti cache finding). It needs a build with the payload and
// a local model, so it runs only with the amqpi tag and
// AMQ_SQUAD_LIVE_BIN=<amq-squad built by scripts/build-pi-runtime.sh>
// (AMQ_SQUAD_LIVE_MODEL, default ollama/qwen2.5:32b):
//
//	AMQ_SQUAD_LIVE_BIN=$PWD/build/pi/amq-squad go test -tags amqpi -run TestLiveR7FirstRun ./internal/cli/
//
// Fresh HOME and TMPDIR; the runtime is unpacked first; then one full lead
// turn with a tool call (so context, guard and evidence ingest all run).
// After it: git status is clean and the repository is unchanged, every file
// under HOME is under ~/.amq-squad, TMPDIR is empty, no jiti cache was
// written (TMPDIR/jiti, /tmp/jiti), and the runtime tree has no new file and
// no changed mtime.
func TestLiveR7FirstRun(t *testing.T) {
	bin := os.Getenv("AMQ_SQUAD_LIVE_BIN")
	if bin == "" {
		t.Skip("set AMQ_SQUAD_LIVE_BIN to an amq-squad built with the payload")
	}
	model := firstNonEmptyStr(os.Getenv("AMQ_SQUAD_LIVE_MODEL"), "ollama/qwen2.5:32b")
	home, tmpdir, repo := t.TempDir(), t.TempDir(), t.TempDir()
	path := "/usr/bin:/bin:/opt/homebrew/bin"
	env := []string{"HOME=" + home, "TMPDIR=" + tmpdir, "PATH=" + path, "TERM=xterm", "AMQ_NO_UPDATE_CHECK=1"}
	run := func(dir string, name string, args ...string) string {
		t.Helper()
		cmd := exec.Command(name, args...)
		cmd.Dir, cmd.Env = dir, append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %v: %v\n%s", name, args, err, out)
		}
		return string(out)
	}
	run(repo, "git", "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(repo, "git", "add", "-A")
	run(repo, "git", "-c", "commit.gpgsign=false", "commit", "-qm", "init")
	before := snapshot(t, repo)

	// Unpack first, so the turn below is measured against a settled tree.
	run(repo, bin, "pi", "path")
	rtBase := filepath.Join(home, ".amq-squad", "runtime")
	runtimeBefore := mtimes(t, rtBase)
	jitiBefore := mtimes(t, "/tmp/jiti")
	start := time.Now()

	cmd := exec.Command(bin, "lead", "--model", model, "--", "--mode", "json", "--no-session", "--thinking", "off",
		"-p", "Use your bash tool to run exactly: echo r7-live. Then reply with the word done.")
	cmd.Dir, cmd.Env = repo, env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("lead turn: %v", err)
	}
	toolEnded := false
	sc := bufio.NewScanner(strings.NewReader(string(out)))
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var ev struct {
			Type     string `json:"type"`
			ToolName string `json:"toolName"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Type == "tool_execution_end" && ev.ToolName == "bash" {
			toolEnded = true
		}
	}
	if !toolEnded {
		t.Fatalf("the lead turn made no bash call; the hooks were not exercised:\n%s", out)
	}

	if st := run(repo, "git", "status", "--porcelain", "--untracked-files=all", "--ignored"); strings.TrimSpace(st) != "" {
		t.Fatalf("git status not clean:\n%s", st)
	}
	if after := snapshot(t, repo); !equalSets(before, after) {
		t.Fatalf("the turn wrote inside the repository: %v", diffSets(before, after))
	}
	var outside []string
	_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if rel, _ := filepath.Rel(home, p); !strings.HasPrefix(rel, ".amq-squad"+string(filepath.Separator)) {
				outside = append(outside, rel)
			}
		}
		return nil
	})
	if len(outside) > 0 {
		t.Fatalf("writes under HOME outside ~/.amq-squad: %v", outside)
	}
	if ents, _ := os.ReadDir(tmpdir); len(ents) > 0 {
		var names []string
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("the caller's TMPDIR received files (the seat's own TMPDIR should have): %v", names)
	}
	for p, mt := range mtimes(t, "/tmp/jiti") {
		if old, ok := jitiBefore[p]; !ok || !old.Equal(mt) {
			if mt.After(start) {
				t.Fatalf("jiti cache written during the turn: %s", p)
			}
		}
	}
	runtimeAfter := mtimes(t, rtBase)
	for p, mt := range runtimeAfter {
		old, ok := runtimeBefore[p]
		if !ok {
			t.Fatalf("new file in the runtime tree: %s", p)
		}
		if !old.Equal(mt) {
			t.Fatalf("runtime file changed during the turn: %s", p)
		}
	}
	if len(runtimeAfter) != len(runtimeBefore) {
		t.Fatalf("runtime tree lost files: %d -> %d", len(runtimeBefore), len(runtimeAfter))
	}
}

// TestLiveNodeOptionsRequireNeverRuns is review item 1 live: a parent
// NODE_OPTIONS=--require=<file> (and NODE_PATH, NODE_REPL_EXTERNAL_MODULE)
// never reaches the verified node; the planted file never runs.
func TestLiveNodeOptionsRequireNeverRuns(t *testing.T) {
	bin := os.Getenv("AMQ_SQUAD_LIVE_BIN")
	if bin == "" {
		t.Skip("set AMQ_SQUAD_LIVE_BIN to an amq-squad built with the payload")
	}
	home, dir := t.TempDir(), t.TempDir()
	marker := filepath.Join(dir, "evil-ran")
	evil := filepath.Join(dir, "evil.cjs")
	if err := os.WriteFile(evil, []byte("require('fs').writeFileSync("+strconvQuote(marker)+", 'ran');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Control: the planted file does run under a plain node with this env,
	// so its absence below is the launcher's doing.
	if node, err := exec.LookPath("node"); err == nil {
		c := exec.Command(node, "-e", "0")
		c.Env = []string{"PATH=/usr/bin:/bin", "NODE_OPTIONS=--require=" + evil}
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("control node: %v %s", err, out)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("control: --require did not run under a plain node; the test proves nothing")
		}
		_ = os.Remove(marker)
	}
	cmd := exec.Command(bin, "pi", "run", "--no-package", "--seat", "probe", "--", "--version")
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "TERM=xterm",
		"NODE_OPTIONS=--require=" + evil, "NODE_PATH=" + dir, "NODE_REPL_EXTERNAL_MODULE=" + evil}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("pi --version: %v\n%s", err, out)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the NODE_OPTIONS --require file ran before pi")
	}
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// mtimes maps every path under dir (files and directories) to its mtime.
func mtimes(t *testing.T, dir string) map[string]time.Time {
	t.Helper()
	out := map[string]time.Time{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if fi, err := os.Lstat(p); err == nil {
			out[p] = fi.ModTime()
		}
		return nil
	})
	return out
}
