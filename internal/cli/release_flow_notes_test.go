package cli

import (
	"strings"
	"testing"
)

// 3.0.2: the release body comes from a file; prepare and publish refuse to
// run without --notes-file, before any store or gate is touched.
func TestReleaseRequiresTheNotesFile(t *testing.T) {
	project := t.TempDir()
	t.Setenv("AMQ_SQUAD_SEAT", "")
	for _, verb := range []string{"prepare", "publish"} {
		err := runRelease([]string{verb, "--repository", "owner/fixture", "--tag", "v1.2.0", "--commit", strings.Repeat("ab", 20),
			"--notes", "gate note", "--project", project, "--session", "s"})
		if err == nil || !strings.Contains(err.Error(), "requires --notes-file") {
			t.Fatalf("release %s without --notes-file: %v", verb, err)
		}
	}
}
