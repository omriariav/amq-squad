package cli

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #540 acceptance criterion 2: the recorded identity must be representation
// independent for ".", "./", a relative subpath, a symlinked path, and an
// absolute path.
//
// Representation independence is asserted at the level it is actually promised:
// the RECORDED value is stable and absolute for every spelling of the same
// directory, and the COMPARISON treats a symlink and its target as one location.
func TestProjectFlagRecordsOneRepresentationForEverySpelling(t *testing.T) {
	realDir := t.TempDir()
	sub := filepath.Join(realDir, "nested", "project")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "linked-project")
	if err := os.Symlink(sub, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Every spelling below names `sub`. Run each from inside `sub` so the
	// relative forms have a meaningful base.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(sub); err != nil {
		t.Fatal(err)
	}

	spellings := map[string]string{
		"dot":              ".",
		"dot-slash":        "./",
		"relative-subpath": filepath.Join("..", "project"),
		"absolute":         sub,
		"symlink":          link,
	}
	for name, spelling := range spellings {
		t.Run(name, func(t *testing.T) {
			recorded := recordProjectFlag(t, spelling)
			if !filepath.IsAbs(recorded) {
				t.Fatalf("recorded --project %q as %q, want an absolute path", spelling, recorded)
			}
			if recorded != filepath.Clean(recorded) {
				t.Fatalf("recorded --project %q as unclean %q", spelling, recorded)
			}
			// The comparison must agree that this is the same project as the
			// absolute spelling, whatever was recorded. This is the property the
			// #540 identity check depends on.
			if !sameFilesystemPath(recorded, sub) {
				t.Fatalf("recorded --project %q as %q, which does not compare equal to %q", spelling, recorded, sub)
			}
		})
	}
}

// recordProjectFlag parses --project through the real shared parse helper and
// returns the value a command body would read.
func recordProjectFlag(t *testing.T, value string) string {
	t.Helper()
	fs := flag.NewFlagSet("test-command", flag.ContinueOnError)
	project := fs.String("project", "", "")
	if err := parseFlags(fs, []string{"--project", value}); err != nil {
		t.Fatalf("parseFlags(--project %q): %v", value, err)
	}
	return *project
}

// A relative --project must become absolute at parse time, because that value
// flows straight into prepared records, launch records, and identity tuples.
// This is the exact #540 root cause.
func TestProjectFlagIsAbsoluteBeforeAnyCommandBodyReadsIt(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if got := recordProjectFlag(t, "."); got == "." {
		t.Fatal(`--project "." reached the command body verbatim; it must be absolute before it can enter any record`)
	}
}

// An unset --project must stay unset. Commands distinguish "absent" (default to
// cwd) from "explicitly supplied", so normalization must not materialize a value.
func TestUnsetProjectFlagStaysUnset(t *testing.T) {
	fs := flag.NewFlagSet("test-command", flag.ContinueOnError)
	project := fs.String("project", "", "")
	if err := parseFlags(fs, nil); err != nil {
		t.Fatal(err)
	}
	if *project != "" {
		t.Fatalf("unset --project became %q; it must remain empty", *project)
	}
	if flagWasSet(fs, "project") {
		t.Fatal("unset --project reported as explicitly set")
	}
}

// The `list` command's --project is a comma-separated LIST of directories. It is
// an explicitly named exemption, so assert the exemption holds: single-path
// normalization would corrupt the list.
func TestListCommandProjectFlagIsExemptFromPathNormalization(t *testing.T) {
	if !commaSeparatedProjectFlagCommands["list"] {
		t.Fatal("list must remain a named exemption from --project path normalization")
	}
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	project := fs.String("project", "", "")
	value := "one,two"
	if err := parseFlags(fs, []string{"--project", value}); err != nil {
		t.Fatal(err)
	}
	if *project != value {
		t.Fatalf("list --project was rewritten to %q; the comma-separated list must survive verbatim", *project)
	}
}

// The record/compare split is load-bearing (see pathnorm.go). Recording must NOT
// resolve symlinks -- doing so rewrites the operator-chosen path and changes
// every digest computed over it -- while comparison MUST resolve them.
func TestRecordingKeepsSymlinkAndComparisonResolvesIt(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := absoluteFilesystemPath(link); got != filepath.Clean(link) {
		t.Fatalf("absoluteFilesystemPath resolved the symlink to %q; recording must preserve %q", got, link)
	}
	if absoluteFilesystemPath(link) == absoluteFilesystemPath(target) {
		t.Fatal("recording collapsed a symlink onto its target; that is the comparison's job, not the recorder's")
	}
	if canonicalFilesystemPath(link) != canonicalFilesystemPath(target) {
		t.Fatalf("canonicalFilesystemPath(%q)=%q did not resolve to the same location as %q=%q",
			link, canonicalFilesystemPath(link), target, canonicalFilesystemPath(target))
	}
}

// #617: macOS can resolve two differently-cased path spellings to the same
// directory while filepath.EvalSymlinks preserves the spelling it was given.
// Every comparison seam must converge on the on-disk spelling so pane CWD,
// launch-record and runtime identity checks agree.
func TestCanonicalPathUsesOnDiskCaseOnCaseInsensitiveFilesystem(t *testing.T) {
	project := filepath.Join(t.TempDir(), "CaseProbe")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	variant, ok := differentlyCasedExistingPath(project)
	if !ok {
		t.Skip("test filesystem is case-sensitive")
	}

	want := canonicalFilesystemPath(project)
	if got := canonicalFilesystemPath(variant); got != want {
		t.Fatalf("same directory canonicalized with different case:\n canonical: %q\n variant:   %q\n got:       %q", project, variant, got)
	}
	if !sameFilesystemPath(project, variant) {
		t.Fatal("sameFilesystemPath rejected differently-cased aliases of one directory")
	}
	if !sameResolvedDir(project, variant) {
		t.Fatal("sameResolvedDir rejected differently-cased aliases of one directory")
	}
	canonicalDirProject, err := canonicalDir(project)
	if err != nil {
		t.Fatal(err)
	}
	canonicalDirVariant, err := canonicalDir(variant)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalDirProject != canonicalDirVariant {
		t.Fatalf("canonicalDir preserved input case: %q != %q", canonicalDirProject, canonicalDirVariant)
	}
	if canonicalPath(project) != canonicalPath(variant) {
		t.Fatal("launch/resume canonicalPath rejected differently-cased aliases")
	}
	if canonicalContextComparisonPath(project) != canonicalContextComparisonPath(variant) {
		t.Fatal("context comparison rejected differently-cased aliases")
	}
	if !rootsMatch(filepath.Join(project, ".agent-mail"), filepath.Join(variant, ".agent-mail")) {
		t.Fatal("AMQ root comparison rejected differently-cased aliases")
	}
	if got := canonicalFilesystemPaths([]string{project, variant}); len(got) != 1 {
		t.Fatalf("one directory produced %d canonical set entries: %v", len(got), got)
	}
	missing := filepath.Join("future", "worktree")
	if canonicalFilesystemPath(filepath.Join(project, missing)) != canonicalFilesystemPath(filepath.Join(variant, missing)) {
		t.Fatal("differently-cased existing ancestors diverged for the same missing descendant")
	}
}

func TestPortableCanonicalPathCasePreservesOnDiskSpelling(t *testing.T) {
	project := filepath.Join(t.TempDir(), "MixedCaseProject")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := canonicalPathCasePortable(project); got != project {
		t.Fatalf("portable path-case fallback rewrote exact on-disk spelling: got %q want %q", got, project)
	}
	if variant, ok := differentlyCasedExistingPath(project); ok {
		if got := canonicalPathCasePortable(variant); got != project {
			t.Fatalf("portable path-case fallback did not recover on-disk spelling: got %q want %q", got, project)
		}
	}
}

func differentlyCasedExistingPath(path string) (string, bool) {
	want, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	for i := len(path) - 1; i >= 0; i-- {
		var replacement byte
		switch {
		case path[i] >= 'a' && path[i] <= 'z':
			replacement = path[i] - ('a' - 'A')
		case path[i] >= 'A' && path[i] <= 'Z':
			replacement = path[i] + ('a' - 'A')
		default:
			continue
		}
		candidate := path[:i] + string(replacement) + path[i+1:]
		got, statErr := os.Stat(candidate)
		if statErr == nil && os.SameFile(got, want) {
			return candidate, true
		}
	}
	return "", false
}

// canonicalFilesystemPath must be total: a comparator has to canonicalize a
// recorded location that no longer exists (a removed worktree) without failing.
func TestCanonicalPathHandlesMissingLocation(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone", "worktree")
	got := canonicalFilesystemPath(missing)
	if got == "" {
		t.Fatalf("canonicalFilesystemPath(%q) returned empty; it must be total", missing)
	}
	if !filepath.IsAbs(got) || got != filepath.Clean(got) {
		t.Fatalf("canonicalFilesystemPath(%q)=%q, want a clean absolute path", missing, got)
	}
	// Stable: canonicalizing twice must not move.
	if again := canonicalFilesystemPath(got); again != got {
		t.Fatalf("canonicalFilesystemPath is not idempotent: %q -> %q", got, again)
	}
	if canonicalFilesystemPath("   ") != "" {
		t.Fatal("blank input must canonicalize to the empty string")
	}
}

// Second-review finding 5: EvalSymlinks fails outright when the LEAF is missing,
// even if an ancestor is a symlink. Taking the absolute form in that case would
// make /link/x and /real/x canonicalize differently for a not-yet-created x, so
// two records naming the same future location would compare as drift.
//
// The comparison resolves the longest EXISTING ancestor and rejoins the
// remainder, so both spellings agree whether or not the leaf exists yet.
func TestCanonicalPathResolvesSymlinkPrefixForMissingLeaf(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// Several depths of not-yet-existing descendant.
	for _, leaf := range []string{"x", filepath.Join("a", "b"), filepath.Join("a", "b", "c", "d")} {
		t.Run(leaf, func(t *testing.T) {
			viaLink := canonicalFilesystemPath(filepath.Join(link, leaf))
			viaReal := canonicalFilesystemPath(filepath.Join(real, leaf))
			if viaLink != viaReal {
				t.Fatalf("same missing location canonicalized differently:\n via link: %q\n via real: %q", viaLink, viaReal)
			}
			// And the set comparison must therefore see them as one entry.
			set := canonicalFilesystemPaths([]string{filepath.Join(link, leaf), filepath.Join(real, leaf)})
			if len(set) != 1 {
				t.Fatalf("two spellings of one missing location produced %d set entries: %v", len(set), set)
			}
		})
	}

	// sameFilesystemPath is the comparator callers actually use, so pin it too.
	if !sameFilesystemPath(filepath.Join(link, "not-created-yet"), filepath.Join(real, "not-created-yet")) {
		t.Fatal("sameFilesystemPath must treat a symlinked prefix and its target as one location for a missing leaf")
	}

	// A path with no existing ancestor at all must still canonicalize, not hang
	// or return empty: the loop has to terminate at the root.
	orphan := canonicalFilesystemPath(filepath.Join(string(filepath.Separator), "no-such-root-dir-amq", "x", "y"))
	if orphan == "" || !filepath.IsAbs(orphan) {
		t.Fatalf("a path with no existing ancestor canonicalized to %q", orphan)
	}
}

// A path set persisted with RELATIVE entries must be anchored to the project it
// was recorded against, not to the process working directory. This is the
// out-of-repo invocation case: `amq-squad ... --project /path/to/repo` run from
// somewhere else must reach the same verdict as the same command run inside it.
func TestRelativePathSetAnchorsToProjectNotProcessCWD(t *testing.T) {
	project := t.TempDir()
	claudeDir := filepath.Join(project, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(claudeDir, "settings.local.json")
	if err := os.WriteFile(settings, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	legacyRelative := []string{filepath.Join(".claude", "settings.local.json")}
	absolute := []string{settings}

	elsewhere := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(cwd) })

	for _, from := range []struct{ name, dir string }{
		{"cwd inside the project", project},
		{"cwd outside the project", elsewhere},
	} {
		t.Run(from.name, func(t *testing.T) {
			if err := os.Chdir(from.dir); err != nil {
				t.Fatal(err)
			}
			got := canonicalFilesystemPathsIn(project, legacyRelative)
			want := canonicalFilesystemPathsIn(project, absolute)
			if len(got) != 1 || len(want) != 1 || got[0] != want[0] {
				t.Fatalf("legacy relative entry anchored to %v, absolute anchored to %v; they must agree from any cwd", got, want)
			}
			if !strings.HasPrefix(got[0], canonicalFilesystemPath(project)) {
				t.Fatalf("relative entry resolved to %q, which is outside the project %q", got[0], project)
			}
		})
	}
}

// Absolute entries must be unaffected by anchoring, and a set must be
// order-independent: the #539 symptom included a leading "." sorting differently
// from the absolute form and making an identical three-file set compare unequal.
func TestPathSetComparisonIsOrderAndRepresentationIndependent(t *testing.T) {
	project := t.TempDir()
	a := filepath.Join(project, ".claude", "settings.local.json")
	b := filepath.Join(project, ".mcp.json")
	c := "/etc/hosts"

	recordedOneOrder := canonicalFilesystemPathsIn(project, []string{c, a, b})
	recordedOtherOrder := canonicalFilesystemPathsIn(project, []string{b, c, a})
	mixedRepresentation := canonicalFilesystemPathsIn(project, []string{
		filepath.Join(".claude", "settings.local.json"), c, ".mcp.json",
	})
	if len(recordedOneOrder) != 3 {
		t.Fatalf("expected 3 canonical entries, got %v", recordedOneOrder)
	}
	for i := range recordedOneOrder {
		if recordedOneOrder[i] != recordedOtherOrder[i] {
			t.Fatalf("set comparison is order dependent: %v vs %v", recordedOneOrder, recordedOtherOrder)
		}
		if recordedOneOrder[i] != mixedRepresentation[i] {
			t.Fatalf("set comparison is representation dependent: %v vs %v", recordedOneOrder, mixedRepresentation)
		}
	}
}

// Second-review MUST-FIX 2: every command whose --project is a comma-separated
// directory LIST must be exempt from single-path normalization.
func TestCommaSeparatedProjectFlagCommandsSurviveVerbatim(t *testing.T) {
	for _, command := range []string{"list", "restore"} {
		t.Run(command, func(t *testing.T) {
			fs := flag.NewFlagSet(command, flag.ContinueOnError)
			project := fs.String("project", "", "comma-separated project directories to scan (default: cwd)")
			value := "~,/repo,./rel"
			if err := parseFlags(fs, []string{"--project", value}); err != nil {
				t.Fatal(err)
			}
			if *project != value {
				t.Fatalf("%s --project was rewritten to %q; the comma-separated list must survive verbatim", command, *project)
			}
		})
	}
}

// The exemption map is load-bearing, so it must police itself rather than rely on
// someone remembering. This enumerates the actual flag registrations in the
// package and fails when a comma-separated --project is not registered as exempt,
// so the NEXT such command cannot be silently missed.
func TestEveryCommaSeparatedProjectFlagIsExempt(t *testing.T) {
	entries, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	// A --project registration whose usage string says "comma-separated" is a
	// directory list by construction.
	registration := regexp.MustCompile(`fs\.String\("project",\s*""\s*,\s*"comma-separated`)
	flagSetName := regexp.MustCompile(`flag\.NewFlagSet\("([^"]+)"`)

	found := map[string]string{}
	for _, path := range entries {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		src := string(body)
		loc := registration.FindStringIndex(src)
		if loc == nil {
			continue
		}
		// The nearest preceding NewFlagSet declares the command this belongs to.
		names := flagSetName.FindAllStringSubmatch(src[:loc[0]], -1)
		if len(names) == 0 {
			t.Fatalf("%s registers a comma-separated --project but declares no flag set name", path)
		}
		command := names[len(names)-1][1]
		found[command] = path
	}
	if len(found) == 0 {
		t.Fatal("no comma-separated --project registrations found; this guard has stopped guarding anything")
	}
	for command, path := range found {
		if !commaSeparatedProjectFlagCommands[command] {
			t.Fatalf("%s registers --project as a comma-separated list for command %q, but it is not in commaSeparatedProjectFlagCommands; normalization will corrupt that flag", path, command)
		}
	}
	// And no stale entries: an exemption for a command that no longer takes a
	// list would silently skip normalization it should be doing.
	for command := range commaSeparatedProjectFlagCommands {
		if _, ok := found[command]; !ok {
			t.Fatalf("commaSeparatedProjectFlagCommands exempts %q, but no comma-separated --project registration was found for it; the exemption is stale and skips normalization", command)
		}
	}
}
