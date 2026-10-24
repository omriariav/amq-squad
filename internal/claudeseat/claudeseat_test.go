package claudeseat

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/plugins"
)

var t0 = time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)

// The launch's config dir: 0700, created fresh (a second write of the same
// launch fails), settings.json with the mode and an empty allow list and the
// plugin enabled, the plugin installed at user scope from the embedded
// files, and Check passes on it.
func TestWriteLaysOutTheSeatConfigDir(t *testing.T) {
	root := t.TempDir()
	dir := Dir(root, "c1", "launch-1")
	w, err := Write(dir, "dontAsk", "/wt/c1", t0)
	if err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 || !strings.HasPrefix(w.Digest, "sha256:") {
		t.Fatalf("dir mode %v, digest %q", st.Mode().Perm(), w.Digest)
	}
	var s struct {
		Permissions struct {
			DefaultMode string   `json:"defaultMode"`
			Allow       []string `json:"allow"`
		} `json:"permissions"`
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if json.Unmarshal(raw, &s) != nil || s.Permissions.DefaultMode != "dontAsk" || s.Permissions.Allow == nil || len(s.Permissions.Allow) != 0 || !s.EnabledPlugins[PluginKey] {
		t.Fatalf("settings.json:\n%s", raw)
	}
	want, _ := plugins.Claude.ReadFile("claude/hooks/amq-squad-hook.sh")
	matches, _ := filepath.Glob(filepath.Join(dir, "plugins", "cache", "amq-squad", "amq-squad", "*", "hooks", "amq-squad-hook.sh"))
	if len(matches) != 1 {
		t.Fatalf("hook script installs: %v", matches)
	}
	got, _ := os.ReadFile(matches[0])
	if st, _ := os.Stat(matches[0]); string(got) != string(want) || st.Mode().Perm() != 0o700 {
		t.Fatalf("the installed hook script is not the embedded one (mode %v)", st.Mode().Perm())
	}
	if code, detail := Check(dir, os.Getuid(), w.Digest, "dontAsk"); code != "" {
		t.Fatalf("check: %s %s", code, detail)
	}
	if _, err := Write(dir, "dontAsk", "/wt/c1", t0); err == nil {
		t.Fatal("a launch's config dir was written twice")
	}
	planted := Dir(root, "c1", "launch-2")
	if err := os.MkdirAll(planted, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(planted, "dontAsk", "/wt/c1", t0); err == nil {
		t.Fatal("a dir that already existed (not created by this launch) was used")
	}
	if len(w.Seeded) != 0 {
		t.Fatalf("seeded %v with an empty Seed", w.Seeded)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude.json")); !os.IsNotExist(err) {
		t.Fatal("an empty Seed wrote .claude.json")
	}
}

// The digest covers settings.json and the plugin files only: Claude Code's
// own state (projects, session files, .claude.json) does not move it; a
// managed byte does.
func TestDigestCoversOnlyTheManagedFiles(t *testing.T) {
	dir := Dir(t.TempDir(), "c1", "l")
	w, err := Write(dir, "dontAsk", "/wt", t0)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"projects/-wt/session.jsonl", ".claude.json", "todos/x.json", "statsig/y"} {
		full := filepath.Join(dir, filepath.FromSlash(p))
		os.MkdirAll(filepath.Dir(full), 0o700)
		os.WriteFile(full, []byte("claude state"), 0o600)
	}
	if d, _ := Digest(dir); d != w.Digest {
		t.Fatal("Claude's own state moved the digest")
	}
	os.WriteFile(filepath.Join(dir, "plugins", "installed_plugins.json"), []byte("{}"), 0o600)
	if d, _ := Digest(dir); d == w.Digest {
		t.Fatal("a managed file changed and the digest did not")
	}
}

// The first-run seed is an explicit list: when set, exactly those keys are
// written and reported.
func TestSeedIsExplicit(t *testing.T) {
	prev := Seed
	t.Cleanup(func() { Seed = prev })
	Seed = map[string]any{"hasCompletedOnboarding": true, "theme": "dark"}
	w, err := Write(Dir(t.TempDir(), "c1", "l"), "dontAsk", "/wt", t0)
	if err != nil || strings.Join(w.Seeded, ",") != "hasCompletedOnboarding,theme" {
		t.Fatalf("seeded %v %v", w.Seeded, err)
	}
}

// Worktree allow rules are listed; an unreadable file counts (fail closed).
func TestWorktreeAllowRules(t *testing.T) {
	wt := t.TempDir()
	if got := WorktreeAllowRules(wt); len(got) != 0 {
		t.Fatalf("none: %v", got)
	}
	os.MkdirAll(filepath.Join(wt, ".claude"), 0o700)
	os.WriteFile(filepath.Join(wt, ".claude", "settings.json"), []byte(`{"permissions":{"allow":["Bash(git:*)"],"deny":["Read(./.env)"]}}`), 0o600)
	os.WriteFile(filepath.Join(wt, ".claude", "settings.local.json"), []byte(`{not json`), 0o600)
	got := WorktreeAllowRules(wt)
	if len(got) != 2 || !strings.Contains(got[0], "Bash(git:*)") || !strings.Contains(got[1], "unreadable") {
		t.Fatalf("rules: %v", got)
	}
}

// P1-10: the digest reads through descriptors with no symlink followed: a
// managed file swapped for a symlink to identical bytes fails, where a
// path-based hash would have matched.
func TestDigestRefusesASymlinkedManagedFile(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	dir := filepath.Join(base, "cfg")
	if _, err := Write(dir, "dontAsk", base, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := Digest(dir); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	os.WriteFile(filepath.Join(base, "copy.json"), raw, 0o600)
	os.Remove(filepath.Join(dir, "settings.json"))
	os.Symlink(filepath.Join(base, "copy.json"), filepath.Join(dir, "settings.json"))
	if _, err := Digest(dir); err == nil {
		t.Fatal("a symlinked settings.json was hashed")
	}
}
