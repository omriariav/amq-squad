package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Brief 3.1.1-3's follow-up (the plan reviewer's read of the notes): 3.1.1
// removed the `/squad` subcommands (the pane door is /confirm, a dialog with
// nothing to type; the actions are /add, /reset, /dismiss, /answer and
// /widen), so no Go source, README.md, user doc (docs/**/*.md) or skill
// source (plugins/skills-src/**) tells anyone to type a `/squad` followed by a
// word. The historical notes (docs/v3-spike/**) and the release notes keep
// what was true when they were written. Reverted (one such string back, the
// owner_only deny's old "(/squad" + " confirm)" say), this fails naming the file and
// line.
func TestNoTextNamesARemovedSquadSubcommand(t *testing.T) {
	root := repoRootForText(t)
	// built at run time, so this file does not carry the form it forbids
	removed := regexp.MustCompile(regexp.QuoteMeta("/"+"squad") + `[ \t]+[a-z]`)
	historical := func(rel string) bool {
		return strings.HasPrefix(rel, "docs/v3-spike/") || strings.Contains(filepath.Base(rel), "release-notes")
	}
	var scanned int
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", ".worktrees", ".claude", ".agent-mail":
				return filepath.SkipDir
			}
			return nil
		}
		inScope := strings.HasSuffix(rel, ".go") || rel == "README.md" ||
			(strings.HasPrefix(rel, "docs/") && strings.HasSuffix(rel, ".md") && !historical(rel)) ||
			strings.HasPrefix(rel, "plugins/skills-src/")
		if !inScope {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		scanned++
		for i, line := range strings.Split(string(raw), "\n") {
			if removed.MatchString(line) {
				t.Errorf("%s:%d names a removed subcommand of `/squad`: %s", rel, i+1, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if scanned < 100 {
		t.Fatalf("scanned only %d files under %s", scanned, root)
	}
}

// repoRootForText is the repository root: the nearest directory up from
// the test's directory with a go.mod.
func repoRootForText(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test")
		}
		dir = parent
	}
}
