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
	// the pattern and the allowlist, pinned on fixtures first
	pinRemovedSquadForms(t)
	root := repoRootForText(t)
	removed, historical := removedSquadForm, squadTextHistorical
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

// removedSquadForm is the scan's one pattern: the subcommands 3.1.1 removed
// (Codex on #878: not any word after /squad, so a line like "/squad prints
// the commands" is no hit), built at run time so this file does not carry
// the form it forbids. The walk and the pins both use it.
var removedSquadForm = regexp.MustCompile(regexp.QuoteMeta("/"+"squad") + `[ \t]+(add|reset|clear|answer|confirm)\b`)

// squadReleaseNotes is the release notes the scan allows: from 3.1.0 on,
// when /squad's subcommands came and went, by an anchored path.
var squadReleaseNotes = regexp.MustCompile(`^docs/v3\.[1-9][0-9]*\.[0-9]+(-[a-z0-9.]+)?-release-notes\.md$`)

// squadTextHistorical is the scan's one allowlist, by exact path: the
// historical notes and the release notes from 3.1.0 on, never a file whose
// name merely carries "release-notes". The walk and the pins both use it.
func squadTextHistorical(rel string) bool {
	return strings.HasPrefix(rel, "docs/v3-spike/") || squadReleaseNotes.MatchString(rel)
}

// pinRemovedSquadForms checks the scan's own pattern and allowlist, the
// same definitions the walk uses (Codex's re-check on #878: pins on copies
// stayed green when the scanner loosened): each removed subcommand is a hit
// and a plain word after /squad is not; only the historical notes and the
// anchored 3.1.0-and-later release notes are allowed.
func pinRemovedSquadForms(t *testing.T) {
	t.Helper()
	removed := removedSquadForm
	for _, sub := range []string{"add", "reset", "clear", "answer", "confirm"} {
		if !removed.MatchString("type /" + "squad " + sub + " here") {
			t.Errorf("the removed /squad %s is no hit", sub)
		}
	}
	for _, line := range []string{"/" + "squad prints the commands", "the /" + "squad addendum", "`/" + "squad` lists"} {
		if removed.MatchString(line) {
			t.Errorf("a line that names no removed subcommand is a hit: %q", line)
		}
	}
	for _, rel := range []string{"docs/v3.1.0-release-notes.md", "docs/v3.1.1-release-notes.md", "docs/v3.2.0-release-notes.md", "docs/v3-spike/311-evidence/311-squad-ux.md"} {
		if !squadTextHistorical(rel) {
			t.Errorf("%s is not allowed", rel)
		}
	}
	for _, rel := range []string{"docs/x-release-notes-guide.md", "docs/release-notes.md", "docs/squad-yaml.md", "docs/sub/v3.1.0-release-notes.md", "docs/v3.0.2-release-notes.md", "docs/v2.31.0-release-notes.md", "docs/v3-spikes.md", "README.md"} {
		if squadTextHistorical(rel) {
			t.Errorf("%s is allowed", rel)
		}
	}
}
