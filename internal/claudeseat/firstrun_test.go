package claudeseat

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 3.0.1 t6: a fresh seat config dir written by the core carries the three
// facts under the names Claude Code 2.1.314 reads: hasCompletedOnboarding in
// .claude.json and a theme in settings.json (onboarding), the amq-squad
// plugin enabled in settings.json with its files in the dir (hooks), and
// projects[<worktree>].hasTrustDialogAccepted in .claude.json (trust). The
// plugin files are copies, never links into another config dir. Fails when
// any writer is reverted.
func TestWriteBearsTheFirstRunFacts(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	wt := filepath.Join(base, "wt")
	os.MkdirAll(wt, 0o700)
	dir := Dir(base, "c1", "launch-1")
	w, err := Write(dir, "dontAsk", wt, t0)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	raw, err := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if err != nil || json.Unmarshal(raw, &cfg) != nil {
		t.Fatalf(".claude.json: %v\n%s", err, raw)
	}
	if cfg["hasCompletedOnboarding"] != true {
		t.Fatalf("onboarding not complete:\n%s", raw)
	}
	projects, _ := cfg["projects"].(map[string]any)
	entry, _ := projects[wt].(map[string]any)
	if entry["hasTrustDialogAccepted"] != true {
		t.Fatalf("the worktree %s is not trusted:\n%s", wt, raw)
	}
	var settings struct {
		Theme          string          `json:"theme"`
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	raw, _ = os.ReadFile(filepath.Join(dir, "settings.json"))
	if json.Unmarshal(raw, &settings) != nil || settings.Theme != "dark" || !settings.EnabledPlugins["amq-squad@amq-squad"] {
		t.Fatalf("settings.json lacks the theme or the plugin:\n%s", raw)
	}
	want := []string{"hasCompletedOnboarding", `projects["` + wt + `"].hasTrustDialogAccepted`}
	if strings.Join(w.Seeded, "|") != strings.Join(want, "|") {
		t.Fatalf("seeded %q, want %q", w.Seeded, want)
	}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink != 0 {
			t.Errorf("%s is a symlink; the seat's config is written by value", p)
		}
		return nil
	})
}

// A worktree reached through a symlink is trusted under both keys, the path
// as recorded and its resolved form; the digest reads the recorded keys.
func TestWriteTrustsTheResolvedWorktreeToo(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	real := filepath.Join(base, "real-wt")
	os.MkdirAll(real, 0o700)
	link := filepath.Join(base, "wt")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	dir := Dir(base, "c1", "l")
	w, err := Write(dir, "dontAsk", link, t0)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Projects map[string]struct {
			Trusted bool `json:"hasTrustDialogAccepted"`
		} `json:"projects"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, ".claude.json"))
	if json.Unmarshal(raw, &cfg) != nil || !cfg.Projects[link].Trusted || !cfg.Projects[real].Trusted || len(w.Seeded) != 3 {
		t.Fatalf("trust keys %s, seeded %v", raw, w.Seeded)
	}
}

func editJSON(t *testing.T, p string, edit func(map[string]any)) {
	t.Helper()
	m := map[string]any{}
	raw, err := os.ReadFile(p)
	if err != nil || json.Unmarshal(raw, &m) != nil {
		t.Fatalf("%s: %v", p, err)
	}
	edit(m)
	out, _ := json.Marshal(m)
	if err := os.WriteFile(p, out, 0o600); err != nil {
		t.Fatal(err)
	}
}
