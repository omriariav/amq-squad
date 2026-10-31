package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/compoundrelease"
	"github.com/omriariav/amq-squad/v3/internal/releaseflow"
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

// 3.0.2 (Codex revert gap): the notes file's content is the body the flow
// receives, and its digest is the one the spec records; the gate note is
// neither.
func TestReleaseNotesFileIsTheFlowsBody(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, "v1.2.0-release-notes.md")
	content := "# amq-squad v1.2.0\n\nThe release notes.\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	body, err := releaseBody("prepare", path)
	if err != nil {
		t.Fatal(err)
	}
	flow := &releaseflow.Flow{Lead: "lead"}
	scope := compoundrelease.Scope{ProjectDir: project, Profile: "default", Session: "s", NamespaceGeneration: "none", ParentGate: "gate/release-v1.2.0"}
	spec, err := releaseSpecWithBody(flow, scope, "owner/fixture", "v1.2.0", strings.Repeat("ab", 20), "gate note", body)
	if err != nil {
		t.Fatal(err)
	}
	if flow.Body != content {
		t.Fatalf("the flow's body %q is not the file's content", flow.Body)
	}
	if want := "gate note release body sha256:" + releaseflow.BodyDigest(content); spec.Note.Summary != want {
		t.Fatalf("the recorded note %q, want %q", spec.Note.Summary, want)
	}
}
