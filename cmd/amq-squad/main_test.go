package main

import "testing"

func TestResolveVersionPrefersLdflagVersion(t *testing.T) {
	if got := resolveVersion("v1.2.3", "v9.9.9"); got != "v1.2.3" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "v1.2.3")
	}
}

func TestResolveVersionFallsBackToModuleVersion(t *testing.T) {
	if got := resolveVersion("dev", "v1.2.3"); got != "v1.2.3" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "v1.2.3")
	}
}

func TestResolveVersionKeepsDevForDevelBuild(t *testing.T) {
	if got := resolveVersion("dev", "(devel)"); got != "dev" {
		t.Fatalf("resolveVersion() = %q, want %q", got, "dev")
	}
}
