package piruntime

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// fixture stages a tiny runtime tree (an executable "node", a pi entry, the
// package, fd and rg) and packs it.
func fixture(t *testing.T) Embedded {
	t.Helper()
	tree := t.TempDir()
	files := map[string]struct {
		body string
		exec bool
	}{
		"node/bin/node":               {"#!/bin/sh\necho node\n", true},
		"pi/node_modules/pi/cli.js":   {"console.log('pi')\n", false},
		"pi/node_modules/pi/chunk.js": {"export const x = 1\n", false},
		"pi/package.json":             {`{"name":"@amq-squad/pi"}`, false},
		"pi/extensions/amq-squad.ts":  {"export default function () {}\n", false},
		"tools/fd":                    {"#!/bin/sh\necho fd\n", true},
		"tools/rg":                    {"#!/bin/sh\necho rg\n", true},
	}
	for rel, f := range files {
		p := filepath.Join(tree, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if f.exec {
			mode = 0o755
		}
		if err := os.WriteFile(p, []byte(f.body), mode); err != nil {
			t.Fatal(err)
		}
	}
	packed, err := Pack(tree, PackSpec{
		Version: "pi-test", Platform: runtime.GOOS + "-" + runtime.GOARCH, PiVersion: "1.3.0", NodeVersion: "22.21.1",
		Executable: "node/bin/node", Args: []string{"pi/node_modules/pi/cli.js"}, Package: "pi",
		Tools: map[string]string{"fd": "tools/fd", "rg": "tools/rg"},
	})
	if err != nil {
		t.Fatal(err)
	}
	e, err := ParseEmbedded(packed.Manifest, packed.Digest, func() ([]byte, error) { return packed.Payload, nil })
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestColdThenWarm(t *testing.T) {
	e := fixture(t)
	base := tempHome(t)
	rt, tm, err := Ensure(base, e)
	if err != nil || !tm.Cold {
		t.Fatalf("cold: %v %+v", err, tm)
	}
	if tm.Files != len(e.Manifest.Files) {
		t.Fatalf("cold hashed %d files, want %d", tm.Files, len(e.Manifest.Files))
	}
	rt2, tm2, err := Ensure(base, e)
	if err != nil || tm2.Cold || rt2.Dir != rt.Dir {
		t.Fatalf("warm: %v %+v", err, tm2)
	}
	if tm2.Files != len(e.Manifest.Files) {
		t.Fatalf("warm launch re-hashed %d files, want every file (%d)", tm2.Files, len(e.Manifest.Files))
	}
	if got := rt.PiArgv(); len(got) != 2 || !strings.HasSuffix(got[1], "pi/node_modules/pi/cli.js") || !filepath.IsAbs(got[0]) {
		t.Fatalf("PiArgv = %v", got)
	}
	if b, _ := os.ReadFile(filepath.Join(rt.Dir, ManifestName)); !bytes.Equal(b, e.Raw) {
		t.Fatal("on-disk manifest is not the embedded manifest")
	}
}

// The trust root: the embedded manifest digest must be present and match.
func TestEmbeddedDigestIsTheTrustRoot(t *testing.T) {
	e := fixture(t)
	load := func() ([]byte, error) { return nil, nil }
	if _, err := ParseEmbedded(e.Raw, nil, load); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing digest: %v", err)
	}
	if _, err := ParseEmbedded(e.Raw, []byte(strings.Repeat("0", 64)), load); err == nil || !strings.Contains(err.Error(), "mismatch") {
		t.Fatalf("mismatched digest: %v", err)
	}
	if _, err := ParseEmbedded(e.Raw, []byte("zz"), load); err == nil {
		t.Fatal("malformed digest accepted")
	}
	changed := bytes.Replace(e.Raw, []byte(`"pi-test"`), []byte(`"pi-tesu"`), 1)
	if _, err := ParseEmbedded(changed, []byte(e.Digest), load); err == nil {
		t.Fatal("a changed manifest passed the pinned digest")
	}
}

func TestPayloadDigestCheckedBeforeAnyWrite(t *testing.T) {
	e := fixture(t)
	e.Payload = func() ([]byte, error) { return []byte("not the payload"), nil }
	base := filepath.Join(t.TempDir(), "runtime")
	if _, _, err := Ensure(base, e); err == nil {
		t.Fatal("a payload that does not match the manifest was installed")
	}
	if _, err := os.Stat(base); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime base written before the digest check: %v", err)
	}
}

// Acceptance 2: a changed byte anywhere (an executable or not), an extra
// file, a missing file, a symlink on the entrypoint path or a replaced
// on-disk manifest makes the launch refuse; nothing is repaired.
func TestLaunchRefusesTamper(t *testing.T) {
	cases := map[string]func(t *testing.T, dir string){
		"executable byte": func(t *testing.T, dir string) { flipByte(t, filepath.Join(dir, "node/bin/node")) },
		"tool byte":       func(t *testing.T, dir string) { flipByte(t, filepath.Join(dir, "tools/rg")) },
		"js chunk byte":   func(t *testing.T, dir string) { flipByte(t, filepath.Join(dir, "pi/node_modules/pi/chunk.js")) },
		"package byte":    func(t *testing.T, dir string) { flipByte(t, filepath.Join(dir, "pi/extensions/amq-squad.ts")) },
		"extra file": func(t *testing.T, dir string) {
			chmodDir(t, filepath.Join(dir, "pi"))
			write(t, filepath.Join(dir, "pi/extensions/evil.ts"), "x")
		},
		"missing file": func(t *testing.T, dir string) {
			chmodDir(t, filepath.Join(dir, "pi/node_modules/pi"))
			if err := os.Remove(filepath.Join(dir, "pi/node_modules/pi/chunk.js")); err != nil {
				t.Fatal(err)
			}
		},
		"symlinked directory on the entry path": func(t *testing.T, dir string) {
			src := filepath.Join(dir, "pi/node_modules/pi")
			moved := filepath.Join(t.TempDir(), "pi")
			chmodDir(t, filepath.Join(dir, "pi/node_modules"))
			if err := os.Rename(src, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, src); err != nil {
				t.Fatal(err)
			}
		},
		"symlinked entry file": func(t *testing.T, dir string) {
			p := filepath.Join(dir, "pi/node_modules/pi/cli.js")
			copyTo := filepath.Join(t.TempDir(), "cli.js")
			b, _ := os.ReadFile(p)
			write(t, copyTo, string(b))
			chmodDir(t, filepath.Dir(p))
			_ = os.Remove(p)
			if err := os.Symlink(copyTo, p); err != nil {
				t.Fatal(err)
			}
		},
		"replaced on-disk manifest": func(t *testing.T, dir string) {
			p := filepath.Join(dir, ManifestName)
			_ = os.Chmod(p, 0o644)
			write(t, p, "{}")
		},
		"marker for another payload": func(t *testing.T, dir string) {
			p := filepath.Join(dir, MarkerName)
			_ = os.Chmod(p, 0o644)
			write(t, p, strings.Repeat("a", 64))
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			e := fixture(t)
			base := tempHome(t)
			rt, _, err := Ensure(base, e)
			if err != nil {
				t.Fatal(err)
			}
			tamper(t, rt.Dir)
			_, _, err = Ensure(base, e)
			var rej *Reject
			if !errors.As(err, &rej) {
				t.Fatalf("tampered runtime launched: %v", err)
			}
			if _, statErr := os.Lstat(rt.Dir); statErr != nil {
				t.Fatalf("the launcher removed the rejected runtime: %v", statErr)
			}
		})
	}
}

func TestSymlinkedRuntimeDirRefused(t *testing.T) {
	e := fixture(t)
	real := tempHome(t)
	rt, _, err := Ensure(real, e)
	if err != nil {
		t.Fatal(err)
	}
	home := tempHome(t)
	if err := os.MkdirAll(DefaultBase(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rt.Dir, filepath.Join(DefaultBase(home), e.Manifest.DirName())); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Ensure(home, e); err == nil {
		t.Fatal("a symlinked runtime directory was accepted")
	}
}

// Review item 3: ~/.amq-squad as a symlink to a directory that holds a
// valid runtime is refused (every component below HOME is opened with
// O_NOFOLLOW through its parent's descriptor), and so is a symlinked
// ~/.amq-squad/runtime.
func TestSymlinkedAncestorRefused(t *testing.T) {
	e := fixture(t)
	elsewhere := tempHome(t)
	if _, _, err := Ensure(elsewhere, e); err != nil {
		t.Fatal(err)
	}
	for name, plant := range map[string]func(home string) error{
		"~/.amq-squad": func(home string) error {
			return os.Symlink(filepath.Join(elsewhere, ".amq-squad"), filepath.Join(home, ".amq-squad"))
		},
		"~/.amq-squad/runtime": func(home string) error {
			if err := os.Mkdir(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
				return err
			}
			return os.Symlink(DefaultBase(elsewhere), DefaultBase(home))
		},
	} {
		t.Run(name, func(t *testing.T) {
			home := tempHome(t)
			if err := plant(home); err != nil {
				t.Fatal(err)
			}
			var rej *Reject
			_, _, err := Ensure(home, e)
			if err == nil {
				t.Fatal("a runtime behind a symlinked ancestor was accepted")
			}
			if !errors.As(err, &rej) && !strings.Contains(err.Error(), "symlink") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// Review item 2 (a): a file of the launch set renamed or rewritten between
// the re-hash and exec is caught by CheckLaunch.
func TestCheckLaunchCatchesSwapAfterVerification(t *testing.T) {
	for name, swap := range map[string]func(t *testing.T, rt Runtime){
		"entry renamed over with a verified copy of other bytes": func(t *testing.T, rt Runtime) {
			entry := filepath.Join(rt.Dir, "pi/node_modules/pi/cli.js")
			chmodDir(t, filepath.Dir(entry))
			evil := entry + ".evil"
			write(t, evil, "console.log('evil')\n")
			if err := os.Rename(evil, entry); err != nil {
				t.Fatal(err)
			}
		},
		"node rewritten in place, same size": func(t *testing.T, rt Runtime) { flipByte(t, rt.Exe()) },
		// Review 2, item 2 (a): same inode, same size, mtime restored: only
		// the content hash catches it.
		"node rewritten in place, mtime restored": func(t *testing.T, rt Runtime) {
			fi, err := os.Stat(rt.Exe())
			if err != nil {
				t.Fatal(err)
			}
			flipByte(t, rt.Exe())
			if err := os.Chtimes(rt.Exe(), fi.ModTime(), fi.ModTime()); err != nil {
				t.Fatal(err)
			}
		},
		"extension rewritten in place, mtime restored": func(t *testing.T, rt Runtime) {
			p := filepath.Join(rt.Dir, "pi/extensions/amq-squad.ts")
			fi, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			flipByte(t, p)
			if err := os.Chtimes(p, fi.ModTime(), fi.ModTime()); err != nil {
				t.Fatal(err)
			}
		},
		"package file renamed over": func(t *testing.T, rt Runtime) {
			p := filepath.Join(rt.Dir, "pi/extensions/amq-squad.ts")
			chmodDir(t, filepath.Dir(p))
			b, _ := os.ReadFile(p)
			write(t, p+".x", string(b)) // same bytes, another inode
			if err := os.Rename(p+".x", p); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := fixture(t)
			rt, _, err := Ensure(tempHome(t), e)
			if err != nil {
				t.Fatal(err)
			}
			if err := rt.CheckLaunch(); err != nil {
				t.Fatalf("clean launch refused: %v", err)
			}
			time.Sleep(10 * time.Millisecond) // a distinct mtime on rewrite
			swap(t, rt)
			if err := rt.CheckLaunch(); err == nil {
				t.Fatal("a swap after verification passed the launch check")
			}
		})
	}
	if err := (Runtime{}).CheckLaunch(); err == nil {
		t.Fatal("an unverified runtime passed the launch check")
	}
}

func TestConcurrentInstallUnpacksOnce(t *testing.T) {
	e := fixture(t)
	base := tempHome(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	cold := 0
	errs := make([]error, 4)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, tm, err := Ensure(base, e)
			errs[i] = err
			if tm.Cold {
				mu.Lock()
				cold++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if cold != 1 {
		t.Fatalf("%d cold installs, want 1", cold)
	}
}

func TestPackIsDeterministic(t *testing.T) {
	a, b := fixture(t), fixture(t)
	if a.Digest != b.Digest || a.Manifest.SHA256 != b.Manifest.SHA256 {
		t.Fatal("the same tree packed to different bytes")
	}
	for _, f := range a.Manifest.Files {
		if (f.Path == "node/bin/node" || strings.HasPrefix(f.Path, "tools/")) != (f.Mode == 0o555) {
			t.Fatalf("%s mode %o", f.Path, f.Mode)
		}
	}
}

func TestPackRefusesSymlink(t *testing.T) {
	tree := t.TempDir()
	write(t, filepath.Join(tree, "node/bin/node"), "x")
	if err := os.Symlink("node", filepath.Join(tree, "link")); err != nil {
		t.Fatal(err)
	}
	if _, err := Pack(tree, PackSpec{Version: "v", Platform: "p", Executable: "node/bin/node"}); err == nil {
		t.Fatal("a symlink was packed")
	}
}

func TestPruneKeepsLastTwoAndCurrent(t *testing.T) {
	base := tempHome(t)
	mk := func(name string, age time.Duration) {
		dir := filepath.Join(base, name)
		write(t, filepath.Join(dir, MarkerName), "x")
		at := time.Now().Add(-age)
		if err := os.Chtimes(filepath.Join(dir, MarkerName), at, at); err != nil {
			t.Fatal(err)
		}
	}
	mk("pi-a", 4*time.Hour)
	mk("pi-b", 3*time.Hour)
	mk("pi-c", 2*time.Hour)
	mk("pi-d", 1*time.Hour)
	if err := os.MkdirAll(filepath.Join(base, "not-a-runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	removed, err := Prune(base, "pi-a", KeepRuntimes)
	if err != nil {
		t.Fatal(err)
	}
	// Current pi-a stays first; pi-d is the newest other one; pi-b and pi-c go.
	if strings.Join(removed, ",") != "pi-c,pi-b" {
		t.Fatalf("removed %v", removed)
	}
	for _, keep := range []string{"pi-a", "pi-d", "not-a-runtime"} {
		if _, err := os.Stat(filepath.Join(base, keep)); err != nil {
			t.Fatalf("%s removed: %v", keep, err)
		}
	}
}

func TestPruneRemovesReadOnlyRuntime(t *testing.T) {
	e := fixture(t)
	base := tempHome(t)
	rt, _, err := Ensure(base, e)
	if err != nil {
		t.Fatal(err)
	}
	removed, err := Prune(DefaultBase(base), "other", 0)
	if err != nil || len(removed) != 1 {
		t.Fatalf("prune: %v %v", removed, err)
	}
	if _, err := os.Stat(rt.Dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime still present: %v", err)
	}
}

func TestPrepareScrubsAndRestricts(t *testing.T) {
	e := fixture(t)
	rt, _, err := Ensure(tempHome(t), e)
	if err != nil {
		t.Fatal(err)
	}
	home := tempHome(t)
	parent := []string{"PATH=/usr/bin:/bin", "GEMINI_API_KEY=g", "OPENAI_API_KEY=o", "GH_TOKEN=t", "BASH_ENV=/home/x/.bashrc",
		"PI_TELEMETRY=1", "PI_CODING_AGENT_SESSION_DIR=/tmp/elsewhere", "SSH_AUTH_SOCK=/tmp/agent", "KEEP=1", "ANTHROPIC_API_KEY=a"}
	l, err := Prepare(Spec{
		Runtime: rt, Home: home, Seat: "pi-1", Parent: parent, EnvNeeds: []string{"ANTHROPIC_API_KEY"}, Package: true,
		Models:   []Model{{Provider: "ollama", ID: "qwen3.6:27b", BaseURL: "http://localhost:11434/v1", API: "openai-completions", APIKey: "ollama", ContextWindow: 131072, Reasoning: true}},
		Thinking: "medium", Args: []string{"--session-id", "n1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(l.Env, "\n")
	for _, gone := range []string{"GEMINI_API_KEY", "OPENAI_API_KEY", "GH_TOKEN", "BASH_ENV", "SSH_AUTH_SOCK", "PI_CODING_AGENT_SESSION_DIR"} {
		if strings.Contains(env, gone+"=") {
			t.Fatalf("%s reached the pi environment", gone)
		}
	}
	for _, want := range []string{"PI_TELEMETRY=0", "PI_OFFLINE=1", "PI_SKIP_VERSION_CHECK=1", "KEEP=1", "ANTHROPIC_API_KEY=a",
		"PI_CODING_AGENT_DIR=" + filepath.Join(home, ".amq-squad", "pi-agent", "pi-1"), "AMQ_SQUAD_MODELS=ollama/qwen3.6:27b"} {
		if !strings.Contains(env, want) {
			t.Fatalf("missing %s in the pi environment", want)
		}
	}
	if l.Path != rt.Exe() || l.Argv[0] != rt.Exe() || l.Argv[2] != "-e" || l.Argv[3] != rt.PackageDir() || l.Argv[len(l.Argv)-1] != "n1" {
		t.Fatalf("argv %v", l.Argv)
	}
	models, _ := os.ReadFile(filepath.Join(l.AgentDir, "models.json"))
	settings, _ := os.ReadFile(filepath.Join(l.AgentDir, "settings.json"))
	if !strings.Contains(string(models), `"qwen3.6:27b"`) || strings.Count(string(models), `"id"`) != 1 {
		t.Fatalf("models.json %s", models)
	}
	for _, want := range []string{`"shellPath": "/bin/bash"`, `"enabledModels"`, `"defaultThinkingLevel": "medium"`, `"enableInstallTelemetry": false`} {
		if !strings.Contains(string(settings), want) {
			t.Fatalf("settings.json lacks %s: %s", want, settings)
		}
	}
	if target, err := os.Readlink(filepath.Join(l.AgentDir, "bin", "rg")); err != nil || target != filepath.Join(rt.Dir, "tools", "rg") {
		t.Fatalf("rg link %q %v", target, err)
	}
	if _, err := Prepare(Spec{Runtime: rt, Home: home, Seat: ".."}); err == nil {
		t.Fatal("seat .. accepted")
	}
}

func TestSessionLayoutAndResume(t *testing.T) {
	agent := t.TempDir()
	cwd := "/Users/me/code/repo"
	if got := SessionDir(agent, cwd); got != filepath.Join(agent, "sessions", "--Users-me-code-repo--") {
		t.Fatalf("SessionDir = %s", got)
	}
	root := t.TempDir()
	ledger := LedgerPath(root, "pi-1")
	argv := []string{"pi", "--session-id", "amqn-1"}
	// No ledger: a seat that never took a turn resumes (create-if-missing).
	if err := CheckResume(ledger, agent, cwd, argv); err != nil {
		t.Fatalf("fresh seat refused: %v", err)
	}
	write(t, ledger, `{"schema":1,"event":"before_agent_start"}`+"\n")
	if err := CheckResume(ledger, agent, cwd, argv); err != nil {
		t.Fatalf("no tool result yet, refused: %v", err)
	}
	write(t, ledger, `{"schema":1,"event":"tool_result","tool":"bash","outcome":"ok"}`+"\n")
	err := CheckResume(ledger, agent, cwd, argv)
	var rr *ResumeRefusal
	if !errors.As(err, &rr) || rr.Code != "pi_resume_context_lost" {
		t.Fatalf("lost context not refused: %v", err)
	}
	// A file for another id that contains this id does not count.
	write(t, filepath.Join(SessionDir(agent, cwd), "2026-10-24T10-00-00-000Z_xamqn-1.jsonl"), "{}")
	if err := CheckResume(ledger, agent, "/Users/me/code/repo", argv); err == nil {
		t.Fatal("a substring match counted as the session file")
	}
	write(t, filepath.Join(SessionDir(agent, cwd), "2026-10-24T10-00-00-000Z_amqn-1.jsonl"), "{}")
	if err := CheckResume(ledger, agent, cwd, argv); err != nil {
		t.Fatalf("present session refused: %v", err)
	}
	if err := CheckResume(ledger, agent, cwd, []string{"pi"}); err == nil {
		t.Fatal("argv without --session-id accepted")
	}
	if SessionIDArg([]string{"--session-id=abc"}) != "abc" {
		t.Fatal("--session-id= form")
	}
}

func flipByte(t *testing.T, p string) {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(p, 0o644)
	b, _ := os.ReadFile(p)
	b[0] ^= 1
	write(t, p, string(b))
	_ = os.Chmod(p, fi.Mode().Perm())
}

func chmodDir(t *testing.T, d string) {
	t.Helper()
	if err := os.Chmod(d, 0o755); err != nil {
		t.Fatal(err)
	}
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
