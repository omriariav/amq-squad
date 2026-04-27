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
		"amq-squad for team setup",
		"amq-cli only for raw AMQ debugging",
		"## System Prompt",
		"Use the binary default system behavior",
		"## Priming Template",
		"At launch, amq-squad injects identity",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("role.md missing %q", want)
		}
	}
	if strings.Contains(body, "TODO") {
		t.Fatalf("role.md should not include placeholder TODOs:\n%s", body)
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
	body := string(b)
	if !strings.Contains(body, "No catalog description is configured for this custom role") {
		t.Error("missing description should emit default guidance")
	}
	if !strings.Contains(body, "Use `amq-squad` for team setup") {
		t.Error("missing skills should emit amq-squad guidance")
	}
	if strings.Contains(body, "TODO") {
		t.Fatalf("fallback stub should not include TODO markers:\n%s", body)
	}
}

func TestEnsureStubUpgradesUntouchedLegacyPlaceholder(t *testing.T) {
	dir := t.TempDir()
	stub := Stub{
		Label:       "QA Manager",
		RoleID:      "qa",
		Description: "Owns test strategy.",
		Peers:       []string{"cto", "fullstack"},
	}
	if err := os.WriteFile(Path(dir), []byte(renderLegacyPlaceholder(stub)), 0o600); err != nil {
		t.Fatal(err)
	}

	wrote, err := EnsureStub(dir, stub)
	if err != nil {
		t.Fatalf("EnsureStub: %v", err)
	}
	if !wrote {
		t.Fatal("legacy generated placeholder should be upgraded")
	}
	b, err := os.ReadFile(Path(dir))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	if strings.Contains(body, "TODO") {
		t.Fatalf("upgraded stub should not include TODO markers:\n%s", body)
	}
	if !strings.Contains(body, "No role-specific slash skills are configured") {
		t.Fatalf("upgraded stub missing default skills guidance:\n%s", body)
	}
	if !strings.Contains(body, "Use `amq-squad` for team setup") {
		t.Fatalf("upgraded stub missing amq-squad guidance:\n%s", body)
	}
}
