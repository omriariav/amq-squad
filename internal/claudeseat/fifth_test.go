package claudeseat

import (
	"os"
	"path/filepath"
	"testing"
)

// fifth commit, item 3: a CLAUDE.md the seat loads that imports a file
// outside its worktree keeps the startup screens open
// (claude_external_includes_pending: StartupScreensClosed false, Check
// refuses), whether the import is in the worktree's CLAUDE.md, nested in an
// import inside the worktree, in an ancestor's CLAUDE.md, under ~/, or a
// linked rules file. Imports inside the worktree, in code, in an HTML
// comment, of a missing file, or of a non-path word close nothing.
func TestExternalIncludesAreDetected(t *testing.T) {
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
			if got := ExternalIncludes(wt); (len(got) > 0) != tc.pending {
				t.Fatalf("pending %t, external includes %v", tc.pending, got)
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
