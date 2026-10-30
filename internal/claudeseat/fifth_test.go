package claudeseat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fifth commit, item 1: a trust or permission input of the wrong shape is
// drift and is refused before exec. 2.1.314 tests trust by truthiness, so
// {} or [] on the canonical root's entry would grant it; the digest moves
// and Check refuses claude_config_unexpected naming the key, never the
// value.
func TestMalformedTrustOrPermissionInputs(t *testing.T) {
	for name, tc := range map[string]struct {
		edit func(m map[string]any, repo string)
		key  string
	}{
		"{} trust on the root entry": {func(m map[string]any, repo string) {
			m["projects"].(map[string]any)[repo] = map[string]any{"hasTrustDialogAccepted": map[string]any{}}
		}, "hasTrustDialogAccepted"},
		"[] trust on the root entry": {func(m map[string]any, repo string) {
			m["projects"].(map[string]any)[repo] = map[string]any{"hasTrustDialogAccepted": []any{}}
		}, "hasTrustDialogAccepted"},
		"{} trust on another entry": {func(m map[string]any, _ string) {
			m["projects"].(map[string]any)["/elsewhere"] = map[string]any{"hasTrustDialogAccepted": map[string]any{}}
		}, "hasTrustDialogAccepted"},
		"a string bypass flag":      {func(m map[string]any, _ string) { m["bypassPermissionsModeAccepted"] = "yes" }, "bypassPermissionsModeAccepted"},
		"allowedTools as an object": {func(m map[string]any, _ string) { m["allowedTools"] = map[string]any{} }, "allowedTools"},
		"a non-string tool rule":    {func(m map[string]any, _ string) { m["allowedTools"] = []any{1.0} }, "allowedTools"},
		"mcpServers as a list":      {func(m map[string]any, _ string) { m["mcpServers"] = []any{} }, "mcpServers"},
	} {
		t.Run(name, func(t *testing.T) {
			base, _ := filepath.EvalSymlinks(t.TempDir())
			repo, wt := gitWorktree(t, base)
			dir := Dir(base, "c1", "l")
			w, err := Write(dir, "dontAsk", wt, t0)
			if err != nil {
				t.Fatal(err)
			}
			editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) { tc.edit(m, repo) })
			digest, _ := Digest(dir)
			if digest == w.Digest {
				t.Fatal("a malformed input kept the digest")
			}
			code, detail := Check(dir, os.Getuid(), digest, "dontAsk")
			if code != ConfigUnexpected || !strings.Contains(detail, "malformed: ") || !strings.Contains(detail, tc.key) || strings.Contains(detail, "yes") {
				t.Fatalf("want %s naming malformed %s, got %q %s", ConfigUnexpected, tc.key, code, detail)
			}
		})
	}
	// A default of the right shape still grants nothing and keeps the
	// digest (Claude Code writes such an entry for the root at run time).
	dir := Dir(t.TempDir(), "c1", "l")
	w, _ := Write(dir, "dontAsk", "/wt/c1", t0)
	editJSON(t, filepath.Join(dir, ".claude.json"), func(m map[string]any) {
		m["projects"].(map[string]any)["/repo"] = map[string]any{"hasTrustDialogAccepted": false, "allowedTools": []any{}, "mcpServers": map[string]any{}}
	})
	if d, _ := Digest(dir); d != w.Digest {
		t.Fatal("a defaults entry of the right shape moved the digest")
	}
}

// fifth commit, item 2: the bookkeeping keys with a permission reader in
// 2.1.314 leave the allowance. Check refuses each before exec with any
// content, naming the key; mcpLegacyKeyOwners and the external-includes
// warning state enter the digest (presence and count; the flag), so one
// added at run time moves it. cachedGrowthBookFeatures stays out of the
// digest, because the binary rewrites it after each flag fetch.
func TestPermissionPathBookkeepingIsRefused(t *testing.T) {
	for name, tc := range map[string]struct {
		edit   func(m map[string]any)
		key    string
		digest bool
	}{
		"mcpLegacyKeyOwners, empty": {func(m map[string]any) { m["mcpLegacyKeyOwners"] = map[string]any{} }, "mcpLegacyKeyOwners", true},
		"mcpLegacyKeyOwners":        {func(m map[string]any) { m["mcpLegacyKeyOwners"] = map[string]any{"srv": "owner"} }, "mcpLegacyKeyOwners", true},
		"cachedGrowthBookFeatures": {func(m map[string]any) {
			m["cachedGrowthBookFeatures"] = map[string]any{"tengu_marlin_thicket": true}
		}, "cachedGrowthBookFeatures", false},
		"external-includes warning shown": {func(m map[string]any) {
			project(m, "/wt/c1")["hasClaudeMdExternalIncludesWarningShown"] = true
		}, "hasClaudeMdExternalIncludesWarningShown", true},
	} {
		t.Run(name, func(t *testing.T) {
			dir := Dir(t.TempDir(), "c1", "l")
			w, err := Write(dir, "dontAsk", "/wt/c1", t0)
			if err != nil {
				t.Fatal(err)
			}
			editJSON(t, filepath.Join(dir, ".claude.json"), tc.edit)
			digest, _ := Digest(dir)
			if moved := digest != w.Digest; moved != tc.digest {
				t.Fatalf("digest moved %t, want %t", moved, tc.digest)
			}
			if code, detail := Check(dir, os.Getuid(), digest, "dontAsk"); code != ConfigUnexpected || !strings.Contains(detail, tc.key) {
				t.Fatalf("want %s naming %s, got %q %s", ConfigUnexpected, tc.key, code, detail)
			}
		})
	}
}

// fifth commit, item 3: a CLAUDE.md the seat loads that imports a file
// outside its worktree keeps the startup screens open
// (claude_external_includes_pending: StartupScreensClosed false, Check
// refuses), whether the import is in the worktree's CLAUDE.md, nested in an
// import inside the worktree, in an ancestor's CLAUDE.md, under ~/, or a
// linked rules file. Imports inside the worktree, in code, in an HTML
// comment, of a missing file, or of a non-path word close nothing.
func TestExternalIncludesKeepTheScreensOpen(t *testing.T) {
	prev := managedMemory
	t.Cleanup(func() { managedMemory = prev })
	for name, tc := range map[string]struct {
		setup   func(t *testing.T, base, wt, outside string)
		pending bool
	}{
		"no imports": {func(t *testing.T, base, wt, outside string) {
			write(t, filepath.Join(wt, "CLAUDE.md"), "# rules\n@./notes.md\n")
			write(t, filepath.Join(wt, "notes.md"), "inside\n")
		}, false},
		"imports in code, comments, missing files and plain words": {func(t *testing.T, base, wt, outside string) {
			write(t, filepath.Join(wt, "CLAUDE.md"), "```\n@"+outside+"\n```\nuse `@"+outside+"` here\n<!-- @"+outside+" -->\nask @nobody\n@../missing.md\n")
		}, false},
		"an absolute import outside": {func(t *testing.T, base, wt, outside string) {
			write(t, filepath.Join(wt, "CLAUDE.md"), "see @"+outside+"\n")
		}, true},
		"a relative import outside": {func(t *testing.T, base, wt, outside string) {
			write(t, filepath.Join(wt, ".claude", "CLAUDE.md"), "@../../shared/rules.md\n")
		}, true},
		"nested in an import inside": {func(t *testing.T, base, wt, outside string) {
			write(t, filepath.Join(wt, "CLAUDE.md"), "@docs/a.md\n")
			write(t, filepath.Join(wt, "docs", "a.md"), "@"+outside+"\n")
		}, true},
		"in an ancestor's CLAUDE.md": {func(t *testing.T, base, wt, outside string) {
			write(t, filepath.Join(base, "CLAUDE.md"), "@shared/rules.md\n")
		}, true},
		"under ~/": {func(t *testing.T, base, wt, outside string) {
			t.Setenv("HOME", filepath.Join(base, "shared"))
			write(t, filepath.Join(wt, "CLAUDE.local.md"), "@~/rules.md\n")
		}, true},
		"a linked rules file": {func(t *testing.T, base, wt, outside string) {
			os.MkdirAll(filepath.Join(wt, ".claude", "rules"), 0o700)
			if err := os.Symlink(outside, filepath.Join(wt, ".claude", "rules", "shared.md")); err != nil {
				t.Fatal(err)
			}
		}, true},
		"the managed CLAUDE.md": {func(t *testing.T, base, wt, outside string) {
			managedMemory = filepath.Join(base, "managed", "CLAUDE.md")
			write(t, managedMemory, "@"+outside+"\n")
		}, true},
	} {
		t.Run(name, func(t *testing.T) {
			managedMemory = filepath.Join(t.TempDir(), "none", "CLAUDE.md")
			base, _ := filepath.EvalSymlinks(t.TempDir())
			wt := filepath.Join(base, "wt")
			outside := filepath.Join(base, "shared", "rules.md")
			write(t, outside, "shared rules\n")
			os.MkdirAll(wt, 0o700)
			tc.setup(t, base, wt, outside)
			dir := Dir(base, "c1", "l")
			w, err := Write(dir, "dontAsk", wt, t0)
			if err != nil {
				t.Fatal(err)
			}
			ok, why := StartupScreensClosed(dir)
			code, _ := Check(dir, os.Getuid(), w.Digest, "dontAsk")
			if tc.pending {
				if ok || why != ExternalIncludesPending || code != ExternalIncludesPending {
					t.Fatalf("want %s, got %t %q (Check %q) for %v", ExternalIncludesPending, ok, why, code, ExternalIncludes(wt))
				}
			} else if !ok || why != "" || code != "" {
				t.Fatalf("want closed, got %t %q (Check %q) for %v", ok, why, code, ExternalIncludes(wt))
			}
		})
	}
}

func write(t *testing.T, p, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
