package claudeseat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A worktree path in decomposed Unicode is trusted under its NFC form, the
// key 2.1.314's trust walk compares (it normalizes the cwd to NFC); the
// decomposed form is not written, and Check passes on the dir.
func TestTrustKeysAreNFC(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	decomposed := filepath.Join(base, "café")
	composed := filepath.Join(base, "café")
	if err := os.MkdirAll(decomposed, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := Dir(base, "c1", "l")
	w, err := Write(dir, "dontAsk", decomposed, t0)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]any{}
	editJSON(t, filepath.Join(dir, ".claude.json"), func(c map[string]any) {
		for k, v := range c {
			m[k] = v
		}
	})
	projects, _ := m["projects"].(map[string]any)
	if _, ok := projects[composed]; !ok || len(projects) != 1 {
		t.Fatalf("trust keys %v, want only the NFC %q", projects, composed)
	}
	if strings.Join(w.Seeded, "|") != `hasCompletedOnboarding|projects["`+composed+`"].hasTrustDialogAccepted` {
		t.Fatalf("seeded %q", w.Seeded)
	}
}

// gitWorktree lays out a main repository and a linked worktree of it the way
// git does (no git run): <repo>/.git/worktrees/wt1 with commondir and
// gitdir, and <wt>/.git naming that gitdir.
func gitWorktree(t *testing.T, base string) (repo, wt string) {
	t.Helper()
	repo, wt = filepath.Join(base, "repo"), filepath.Join(base, "wt")
	admin := filepath.Join(repo, ".git", "worktrees", "wt1")
	for _, d := range []string{admin, wt} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(admin, "commondir"), []byte("../..\n"), 0o600)
	os.WriteFile(filepath.Join(admin, "gitdir"), []byte(filepath.Join(wt, ".git")+"\n"), 0o600)
	os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: "+admin+"\n"), 0o600)
	return repo, wt
}

// Reviewer condition (b): every launch is born fresh. A launch's dir that
// Claude Code has used (a canonical-root entry, defaults only or trusted,
// and its own keys) is never written or reused again: a second Write to it
// fails, and Check on it refuses the root entry by name. The next launch of
// the same seat gets a new dir with only the seed, which passes.
func TestEveryLaunchIsBornFresh(t *testing.T) {
	for name, root := range map[string]map[string]any{
		"root entry, defaults only": {"allowedTools": []any{}, "hasTrustDialogAccepted": false, "lastSessionId": "s"},
		"root entry, trusted":       {"hasTrustDialogAccepted": true},
	} {
		t.Run(name, func(t *testing.T) {
			base, _ := filepath.EvalSymlinks(t.TempDir())
			repo, wt := gitWorktree(t, base)
			first := Dir(base, "c1", "launch-1")
			if _, err := Write(first, "dontAsk", wt, t0); err != nil {
				t.Fatal(err)
			}
			editJSON(t, filepath.Join(first, ".claude.json"), func(m map[string]any) {
				m["userID"], m["numStartups"] = "u", 1
				m["projects"].(map[string]any)[repo] = root
			})
			if _, err := Write(first, "dontAsk", wt, t0); err == nil {
				t.Fatal("a used launch dir was written again")
			}
			second := Dir(base, "c1", "launch-2")
			if _, err := Write(second, "dontAsk", wt, t0); err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(filepath.Join(second, ".claude.json"))
			if strings.Contains(string(raw), `"userID"`) || strings.Contains(string(raw), `"`+repo+`"`) {
				t.Fatalf("the next launch's dir is not fresh:\n%s", raw)
			}
		})
	}
}
