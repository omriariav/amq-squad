package role

import (
	"os"
	"strings"
	"testing"
)

func TestEnsureStubWritesAllSections(t *testing.T) {
	dir := t.TempDir()
	stub := Stub{
		Label:       "Product Designer",
		RoleID:      "designer",
		Description: "Designs the product surface.",
		Skills:      []string{"/frontend-design", "/canvas-design"},
		Peers:       []string{"cpo", "fullstack"},
	}
	wrote, err := EnsureStub(dir, stub)
	if err != nil {
		t.Fatalf("EnsureStub: %v", err)
	}
	if !wrote {
		t.Fatal("EnsureStub returned false on first call")
	}

	b, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, want := range []string{
		"# Role: Product Designer",
		"## Description",
		"Designs the product surface.",
		"## Peers",
		"- cpo",
		"- fullstack",
		"## Skills",
		"/frontend-design",
		"/canvas-design",
		"## System Prompt",
		"## Priming Template",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("role.md missing %q", want)
		}
	}
}

func TestEnsureStubNoClobber(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(Path(dir), []byte("USER EDIT"), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote, err := EnsureStub(dir, Stub{RoleID: "cpo", Label: "CPO"})
	if err != nil {
		t.Fatalf("EnsureStub: %v", err)
	}
	if wrote {
		t.Error("EnsureStub wrote over existing role.md")
	}
	b, _ := os.ReadFile(Path(dir))
	if string(b) != "USER EDIT" {
		t.Error("existing content clobbered")
	}
}

func TestEnsureStubFallsBackToRoleID(t *testing.T) {
	dir := t.TempDir()
	if _, err := EnsureStub(dir, Stub{RoleID: "unknown"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(Path(dir))
	if !strings.Contains(string(b), "# Role: unknown") {
		t.Error("stub should fall back to RoleID when Label is empty")
	}
	// Missing optional fields should emit TODO markers.
	if !strings.Contains(string(b), "TODO: describe") {
		t.Error("missing description should emit TODO")
	}
}
