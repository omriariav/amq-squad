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
			if !sameResolvedDir(recorded, sub) {
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
	if !sameResolvedDir(project, variant) {
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
	missing := filepath.Join("future", "worktree")
	if canonicalFilesystemPath(filepath.Join(project, missing)) != canonicalFilesystemPath(filepath.Join(variant, missing)) {
		t.Fatal("differently-cased existing ancestors diverged for the same missing descendant")
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
		})
	}

	// sameFilesystemPath is the comparator callers actually use, so pin it too.
	if !sameResolvedDir(filepath.Join(link, "not-created-yet"), filepath.Join(real, "not-created-yet")) {
		t.Fatal("sameFilesystemPath must treat a symlinked prefix and its target as one location for a missing leaf")
	}

	// A path with no existing ancestor at all must still canonicalize, not hang
	// or return empty: the loop has to terminate at the root.
	orphan := canonicalFilesystemPath(filepath.Join(string(filepath.Separator), "no-such-root-dir-amq", "x", "y"))
	if orphan == "" || !filepath.IsAbs(orphan) {
		t.Fatalf("a path with no existing ancestor canonicalized to %q", orphan)
	}
}
