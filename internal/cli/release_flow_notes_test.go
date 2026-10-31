package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"

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

// releaseFakeGH writes a gh for the release verb: the commit exists on the
// remote and main contains it; every other call (the tag rulesets) answers
// empty. Each call's arguments are appended to the log.
func releaseFakeGH(t *testing.T, log string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "gh")
	script := `#!/bin/sh
printf '%s\n' "$*" >> '` + log + `'
case "$2" in
*/commits/*) echo "${2##*/}" ;;
*/compare/*) echo behind ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// 3.0.2 (Codex revert gap, round 3): through runRelease itself, the notes
// file's content is the body the flow receives and --notes is the gate note.
// prepare records the note and the file's digest in the spec; publish with
// the same file passes the flow's body check (it stops later, at the tag
// rule the fake gh does not have), and publish with another file's content
// is refused as release_body_mismatch by the CLI's spec check or the flow.
func TestReleaseVerbPassesTheFileAsTheBodyAndNotesAsTheGateNote(t *testing.T) {
	f := newGateCLIFixture(t)
	gateTransport = func(context.Context, gateverify.Binding, gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error) {
		return nil, nostr.SecretKey{}, nil, errors.New("no Buzz transport")
	}
	t.Setenv("AMQ_SQUAD_SEAT", "")
	gitRun := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-C", f.project}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=o@example.com", "GIT_COMMITTER_NAME=owner",
			"GIT_COMMITTER_EMAIL=o@example.com", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	gitRun("init", "-q", "-b", "main")
	gitRun("commit", "-q", "--allow-empty", "-m", "v1")
	commit := gitRun("rev-parse", "HEAD")
	content := "# amq-squad v1.2.0\n\nThe release notes.\n"
	notesFile := filepath.Join(t.TempDir(), "v1.2.0-release-notes.md")
	if err := os.WriteFile(notesFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "gh.log")
	args := func(verb, file string) []string {
		return []string{verb, "--repository", "owner/fixture", "--tag", "v1.2.0", "--commit", commit, "--notes-file", file,
			"--notes", "the gate note", "--gh", releaseFakeGH(t, log), "--project", f.project, "--session", "s", "--me", "cto", "--json"}
	}
	if _, _, err := captureOutput(t, func() error { return runRelease(args("prepare", notesFile)) }); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	want := "the gate note release body sha256:" + releaseflow.BodyDigest(content)
	recorded := false
	_ = filepath.WalkDir(f.project, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && !strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) {
			if b, rerr := os.ReadFile(p); rerr == nil && strings.Contains(string(b), want) {
				recorded = true
			}
		}
		return nil
	})
	if !recorded {
		t.Fatalf("no prepared record carries the note %q", want)
	}
	_, _, err := captureOutput(t, func() error { return runRelease(args("publish", notesFile)) })
	if err == nil || strings.Contains(err.Error(), "release_body_mismatch") || !strings.Contains(err.Error(), "protection_unmet") {
		t.Fatalf("publish with the prepared file: %v (want past the body check, stopped at protection_unmet)", err)
	}
	other := filepath.Join(t.TempDir(), "other.md")
	if err := os.WriteFile(other, []byte(content+"edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runRelease(args("publish", other)) }); err == nil || strings.Contains(err.Error(), "protection_unmet") {
		t.Fatalf("publish with another file reached the flow's later steps: %v", err)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "releases") {
		t.Fatalf("gh was asked to create a release: %s", b)
	}
}
