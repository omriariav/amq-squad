package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/internal/team"
)

func TestDefaultWorkstreamNameSanitizesProjectDir(t *testing.T) {
	got := defaultWorkstreamName("/Users/me/My Project:API")
	want := "my-project-api"
	if got != want {
		t.Fatalf("defaultWorkstreamName = %q, want %q", got, want)
	}
}

func TestCanonicalP2PThreadSortsHandles(t *testing.T) {
	got := canonicalP2PThread("fullstack", "cto")
	want := "p2p/cto__fullstack"
	if got != want {
		t.Fatalf("canonicalP2PThread = %q, want %q", got, want)
	}
}

func TestValidateWorkstreamNameRejectsUnsafeNames(t *testing.T) {
	for _, name := range []string{"", "Feature", "feature/api", "feature.api"} {
		if err := validateWorkstreamName(name); err == nil {
			t.Fatalf("validateWorkstreamName(%q) succeeded, want error", name)
		}
	}
	if err := validateWorkstreamName("v0.5.0"); err == nil || !strings.Contains(err.Error(), "replace dots") {
		t.Fatalf("validateWorkstreamName dot error = %v, want replacement guidance", err)
	}
}

func TestDefaultTeamWorkstreamUsesStoredSharedNonLegacySession(t *testing.T) {
	tm := team.Team{
		Project: "/Users/me/My Project",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Handle: "fullstack", Session: "issue-96"},
		},
	}
	if got := defaultTeamWorkstreamName(tm); got != "issue-96" {
		t.Fatalf("defaultTeamWorkstreamName = %q, want issue-96", got)
	}
}

func TestResolveTeamWorkstreamUsesExplicitTeamDefaultEvenWhenNameMatchesOnlyMember(t *testing.T) {
	tm := team.Team{
		Project:    "/Users/me/My Project",
		Workstream: "cto",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "cto"},
		},
	}
	got, err := resolveTeamWorkstreamName(tm, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if got != "cto" {
		t.Fatalf("resolveTeamWorkstreamName = %q, want cto", got)
	}
}

func TestDefaultTeamWorkstreamIgnoresLegacyRoleSessions(t *testing.T) {
	tm := team.Team{
		Project: "/Users/me/My Project",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "cto"},
			{Role: "fullstack", Handle: "fullstack", Session: "fullstack"},
		},
	}
	if got := defaultTeamWorkstreamName(tm); got != "my-project" {
		t.Fatalf("defaultTeamWorkstreamName = %q, want my-project", got)
	}
}

func TestResolveTeamWorkstreamValidatesSharedLegacyFallback(t *testing.T) {
	tm := team.Team{
		Project: "/repo",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "v0.5.0"},
			{Role: "fullstack", Handle: "fullstack", Session: "v0.5.0"},
		},
	}
	_, err := resolveTeamWorkstreamName(tm, "", false)
	if err == nil || !strings.Contains(err.Error(), "replace dots") {
		t.Fatalf("resolveTeamWorkstreamName error = %v, want invalid session guidance", err)
	}
}
