package role

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func writeTemp(t *testing.T, name, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseMarkdownWithFrontmatter(t *testing.T) {
	p := writeTemp(t, "researcher.md", `---
id: researcher
label: Research Engineer
binary: codex
peers: [cto, qa]
skills:
  - /deep-research
---
# Role: Research Engineer

## Description
Owns investigation.
`)
	d, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if d.ID != "researcher" || d.Label != "Research Engineer" || d.Binary != "codex" {
		t.Fatalf("metadata wrong: %+v", d)
	}
	if !reflect.DeepEqual(d.Peers, []string{"cto", "qa"}) {
		t.Errorf("peers = %v", d.Peers)
	}
	if !reflect.DeepEqual(d.Skills, []string{"/deep-research"}) {
		t.Errorf("skills = %v", d.Skills)
	}
	if !strings.Contains(d.Body, "# Role: Research Engineer") || !strings.Contains(d.Body, "Owns investigation.") {
		t.Errorf("body missing markdown content:\n%s", d.Body)
	}
	// Document() returns the verbatim body when present.
	if d.Document() != d.Body {
		t.Errorf("Document should equal verbatim body")
	}
}

func TestParseMarkdownVerbatimNoFrontmatter(t *testing.T) {
	p := writeTemp(t, "scribe.md", "# Role: Scribe\n\n## Description\nKeeps records.\n")
	d, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if d.ID != "scribe" { // from "# Role: Scribe" heading
		t.Errorf("id = %q, want scribe", d.ID)
	}
	if d.Label != "Scribe" {
		t.Errorf("label = %q, want Scribe", d.Label)
	}
	if d.Binary != "" {
		t.Errorf("binary should be empty (no frontmatter), got %q", d.Binary)
	}
	if !strings.Contains(d.Body, "Keeps records.") {
		t.Errorf("body verbatim missing content:\n%s", d.Body)
	}
}

func TestParseIDFromFilenameFallback(t *testing.T) {
	p := writeTemp(t, "data-scientist.md", "Just some prose with no heading.\n")
	d, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if d.ID != "data-scientist" {
		t.Errorf("id = %q, want data-scientist (from filename)", d.ID)
	}
}

func TestParseYAMLMetadataOnly(t *testing.T) {
	p := writeTemp(t, "sre.yaml", `id: sre
label: Site Reliability Engineer
binary: claude
description: Owns reliability and on-call.
peers:
  - cto
  - backend-dev
`)
	d, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if d.ID != "sre" || d.Binary != "claude" {
		t.Fatalf("metadata wrong: %+v", d)
	}
	if d.Body != "" {
		t.Errorf("yaml-only file should have empty Body, got %q", d.Body)
	}
	// Document() renders a stub from metadata when there is no body.
	doc := d.Document()
	if !strings.Contains(doc, "# Role: Site Reliability Engineer") {
		t.Errorf("rendered doc missing label header:\n%s", doc)
	}
	if !strings.Contains(doc, "Owns reliability and on-call.") {
		t.Errorf("rendered doc missing description:\n%s", doc)
	}
}

func TestParseJSON(t *testing.T) {
	p := writeTemp(t, "analyst.json", `{"id":"analyst","label":"Analyst","binary":"codex","description":"Crunches numbers.","peers":["pm"]}`)
	d, err := ParseFile(p)
	if err != nil {
		t.Fatalf("ParseFile: %v", err)
	}
	if d.ID != "analyst" || d.Binary != "codex" || d.Description != "Crunches numbers." {
		t.Fatalf("metadata wrong: %+v", d)
	}
	if !reflect.DeepEqual(d.Peers, []string{"pm"}) {
		t.Errorf("peers = %v", d.Peers)
	}
}

func TestLooksLikeRoleFile(t *testing.T) {
	files := []string{"./researcher.md", "roles/sre.yaml", "~/x.json", "a.markdown", "x.yml"}
	for _, f := range files {
		if !LooksLikeRoleFile(f) {
			t.Errorf("LooksLikeRoleFile(%q) = false, want true", f)
		}
	}
	slugs := []string{"researcher", "cto", "qa", "senior-dev", "2", "all"}
	for _, s := range slugs {
		if LooksLikeRoleFile(s) {
			t.Errorf("LooksLikeRoleFile(%q) = true, want false", s)
		}
	}
}

func TestEnsureContentWritesAndPreserves(t *testing.T) {
	agentDir := t.TempDir()
	body := "# Role: Custom\n\nbody\n"
	wrote, err := EnsureContent(agentDir, body)
	if err != nil || !wrote {
		t.Fatalf("EnsureContent first write: wrote=%v err=%v", wrote, err)
	}
	got, err := os.ReadFile(Path(agentDir))
	if err != nil || string(got) != body {
		t.Fatalf("staged content = %q (err %v), want %q", got, err, body)
	}
	// Second call must not overwrite existing edits.
	wrote, err = EnsureContent(agentDir, "different")
	if err != nil || wrote {
		t.Fatalf("EnsureContent should not overwrite: wrote=%v err=%v", wrote, err)
	}
}
