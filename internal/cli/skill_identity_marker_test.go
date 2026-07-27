package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #534 moved the skill identity out of a "Skill version: X.Y.Z" body preamble and into
// frontmatter stamped by scripts/generate-plugin-skills.py. These tests protect the two
// properties that move had to preserve: the shipped bundle still verifies, and a bundle
// that cannot be verified still WARNS rather than passing quietly.

// TestNoShippedSkillCarriesThePreamble asserts the deleted preamble is absent from EVERY
// SKILL.md, in both mirrors and in the sources they are generated from.
//
// The doctor-acceptance test below covers only the amq-squad router, because that is the
// single file the binary reads. Nothing else covers the other 13: the release validator
// requires the frontmatter version to be PRESENT, the drift gate checks that named
// commands exist, and `generate-plugin-skills.py --check` only proves mirrors match their
// sources. A preamble pasted back into one skill's source and faithfully mirrored would
// pass all three, and agents reading that skill would resume announcing a version from the
// body -- the exact behaviour #534 removed.
//
// Sources are checked as well as mirrors. Mirrors alone would be sufficient (a synced
// mirror reveals the source, and an unsynced one fails --check), but naming the source
// file points the reader at the file they would actually edit.
func TestNoShippedSkillCarriesThePreamble(t *testing.T) {
	const marker = "Skill version:"
	roots := []string{
		filepath.Join("..", "..", "plugins", "skills-src"),
		filepath.Join("..", "..", "plugins", "claude", "skills"),
		filepath.Join("..", "..", "plugins", "codex", "skills"),
	}

	checked := 0
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() || filepath.Base(path) != "SKILL.md" {
				return nil
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			checked++
			if strings.Contains(string(raw), marker) {
				t.Errorf("%s carries the deleted %q preamble; #534 moves identity to frontmatter",
					filepath.ToSlash(path), marker)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}

	// Anti-vacuity: 7 skills across a source tree and two mirrors. A walk that found
	// nothing would otherwise report a clean pass having read no files at all.
	if checked < 21 {
		t.Fatalf("inspected only %d SKILL.md files across sources and both mirrors; want >= 21", checked)
	}
}

// TestDoctorAcceptsTheRealShippedBundle reads the actual mirror rather than a fixture.
// A synthetic string proves the regex matches something the test author wrote; only the
// real file proves the binary agrees with what the generator emits.
//
// Scoped to the amq-squad router deliberately: that is the one file the binary reads.
// Preamble absence across all 14 is TestNoShippedSkillCarriesThePreamble's job.
func TestDoctorAcceptsTheRealShippedBundle(t *testing.T) {
	for _, mirror := range []string{"claude", "codex"} {
		t.Run(mirror, func(t *testing.T) {
			skillPath := filepath.Join("..", "..", "plugins", mirror, "skills", "amq-squad", "SKILL.md")
			raw, err := os.ReadFile(skillPath)
			if err != nil {
				// Not skipped: the mirror is committed, so an unreadable one is a
				// broken repository rather than an absent optional fixture.
				t.Fatalf("read shipped bundle: %v", err)
			}
			body := string(raw)

			m := skillVersionFrontmatterRE.FindStringSubmatch(body)
			if m == nil {
				t.Fatalf("%s: frontmatter version not readable by the binary's own pattern", skillPath)
			}

			got := doctorCheckSkillVersion(doctorExecution{
				RunningVersion: "v" + m[1],
				SkillMDContent: func(string) (string, string, bool) { return body, skillPath, true },
			})
			if got.Status != doctorOK {
				t.Errorf("doctor on the real shipped bundle = %q, want OK\ndetail: %s", got.Status, got.Detail)
			}
		})
	}
}

// TestNoGoCodeReadsTheDeletedMarker is an enumeration test derived from the tree rather
// than from a maintained list, so a NEW consumer of the deleted marker cannot be added
// without this failing. Three production consumers existed when the preamble was removed
// (doctor.go, version_alignment.go, team_rules.go); nothing should reintroduce a fourth.
//
// Comment lines are ignored: prose explaining what was removed is documentation, not a
// consumer. Excluding them by rule rather than by an exemption list keeps this test from
// needing maintenance of its own.
//
// ACCEPTED RESIDUALS, recorded rather than chased. Any substring test can be evaded by
// string concatenation ("Skill ver" + "sion:") or by a raw string literal whose line
// happens to start with //. Both are residuals of substring matching itself, not gaps
// this test can close, and neither is a plausible accident. The failure mode this test
// exists to catch is a consumer written NORMALLY, which it does catch.
//
// Note also that block-comment INTERIORS are not excluded, so a marker inside /* */
// produces a false positive. That direction is deliberate: it fails loud rather than
// hiding a real consumer.
func TestNoGoCodeReadsTheDeletedMarker(t *testing.T) {
	const marker = "Skill version:"
	// Consumers 6 and 7 (.amq-squad/team-rules.md and RELEASING.md) hid OUTSIDE internal/,
	// so an internal-only walk could not see them. The roots below are the tracked
	// surfaces that instruct agents or releasers; scripts/ is deliberately excluded
	// because check-release-version.py is #558-owned and legitimately still pins the old
	// sentence until that PR lands.
	roots := []string{
		filepath.Join("..", "..", "internal"),
		filepath.Join("..", "..", ".amq-squad"),
	}
	extraFiles := []string{filepath.Join("..", "..", "RELEASING.md")}

	var offenders []string
	walk := func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		// .md is walked as well as .go: internal/cli/bootstrap.md is an EMBEDDED
		// PRODUCTION PROMPT, so a marker instruction there ships to every launched
		// agent. A .go-only walk missed it, and review found it -- the gap is why
		// this covers both extensions rather than only source code.
		if info.IsDir() {
			return nil
		}
		isGo := strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go")
		isMarkdown := strings.HasSuffix(path, ".md") && !strings.Contains(filepath.ToSlash(path), "/testdata/")
		if !isGo && !isMarkdown {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for i, line := range strings.Split(string(raw), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if strings.Contains(line, marker) {
				offenders = append(offenders, filepath.ToSlash(path)+":"+itoa(i+1)+": "+strings.TrimSpace(line))
			}
		}
		return nil
	}
	for _, root := range roots {
		if _, statErr := os.Stat(root); statErr != nil {
			continue
		}
		if err := filepath.Walk(root, walk); err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	for _, file := range extraFiles {
		info, statErr := os.Stat(file)
		if statErr != nil {
			// Not skipped silently: RELEASING.md is tracked, so an absent one is a
			// broken checkout rather than an optional file.
			t.Fatalf("stat %s: %v", file, statErr)
		}
		if err := walk(file, info, nil); err != nil {
			t.Fatalf("walk %s: %v", file, err)
		}
	}

	// Anti-vacuity: prove the walk actually inspected the files it claims to cover. An
	// empty result from a walk that visited nothing would otherwise read as a pass.
	if got := countGoFiles(t, roots[0]); got < 50 {
		t.Fatalf("walk inspected only %d non-test .go files; the traversal is broken", got)
	}

	if len(offenders) != 0 {
		t.Errorf("production Go still reads the deleted %q marker in %d place(s):\n  %s\n"+
			"Read the frontmatter version via skillVersionFrontmatterRE instead.",
			marker, len(offenders), strings.Join(offenders, "\n  "))
	}
}

func countGoFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			n++
		}
		return nil
	}); err != nil {
		t.Fatalf("count walk: %v", err)
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
