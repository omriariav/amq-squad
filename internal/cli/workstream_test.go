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

func TestInferredSharedMemberSessionUsesSharedNonLegacySession(t *testing.T) {
	tm := team.Team{
		Project: "/Users/me/My Project",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Handle: "fullstack", Session: "issue-96"},
		},
	}
	if got := inferredSharedMemberSession(tm); got != "issue-96" {
		t.Fatalf("inferredSharedMemberSession = %q, want issue-96", got)
	}
}

func TestInferredSharedMemberSessionIgnoresLegacyRoleSessions(t *testing.T) {
	tm := team.Team{
		Project: "/Users/me/My Project",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "cto"},
			{Role: "fullstack", Handle: "fullstack", Session: "fullstack"},
		},
	}
	// Legacy role-name sessions are not inferable; the helper returns "" so
	// the caller can fall through to the pin or basename.
	if got := inferredSharedMemberSession(tm); got != "" {
		t.Fatalf("inferredSharedMemberSession = %q, want empty", got)
	}
}

// TestResolveTeamWorkstreamInferenceWinsOverPin asserts the new tier order:
// even when the deprecated pin is present and matches the only member's role,
// a shared non-legacy member session wins silently (no deprecation notice).
func TestResolveTeamWorkstreamInferenceWinsOverPin(t *testing.T) {
	tm := team.Team{
		Project:    "/Users/me/My Project",
		Workstream: "pinned-default",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Handle: "fullstack", Session: "issue-96"},
		},
	}
	var got string
	_, stderr, err := captureOutput(t, func() error {
		var rerr error
		got, rerr = resolveTeamWorkstreamName(tm, "", false)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "issue-96" {
		t.Fatalf("resolveTeamWorkstreamName = %q, want issue-96 (inference wins over pin)", got)
	}
	if strings.Contains(stderr, "deprecated") {
		t.Fatalf("inference path must not emit the deprecation notice; stderr:\n%s", stderr)
	}
}

// TestResolveTeamWorkstreamUsesPinOnlyWhenInferenceYieldsNothing asserts the
// pin shim is the resolved source only when inference yields nothing (here the
// single member's session is a legacy role-name), and that the deprecation
// notice fires on stderr in that case.
func TestResolveTeamWorkstreamUsesPinOnlyWhenInferenceYieldsNothing(t *testing.T) {
	tm := team.Team{
		Project:    "/Users/me/My Project",
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "cto"},
		},
	}
	var got string
	_, stderr, err := captureOutput(t, func() error {
		var rerr error
		got, rerr = resolveTeamWorkstreamName(tm, "", false)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "issue-96" {
		t.Fatalf("resolveTeamWorkstreamName = %q, want issue-96 (pin shim)", got)
	}
	if !strings.Contains(stderr, "deprecated") || !strings.Contains(stderr, "issue-96") {
		t.Fatalf("pin path must emit the deprecation notice naming the session; stderr:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--session") || !strings.Contains(stderr, "2.1") {
		t.Fatalf("notice must mention --session and removal in 2.1; stderr:\n%s", stderr)
	}
}

// TestResolveTeamWorkstreamFallsBackToBasenameAsLastResort asserts that with
// no explicit, no request, no inference, and no pin, the sanitized project
// basename is used and no deprecation notice fires.
func TestResolveTeamWorkstreamFallsBackToBasenameAsLastResort(t *testing.T) {
	tm := team.Team{
		Project: "/Users/me/My Project",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "cto"},
			{Role: "fullstack", Handle: "fullstack", Session: "fullstack"},
		},
	}
	var got string
	_, stderr, err := captureOutput(t, func() error {
		var rerr error
		got, rerr = resolveTeamWorkstreamName(tm, "", false)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-project" {
		t.Fatalf("resolveTeamWorkstreamName = %q, want my-project basename", got)
	}
	if strings.Contains(stderr, "deprecated") {
		t.Fatalf("basename path must not emit the deprecation notice; stderr:\n%s", stderr)
	}
}

// TestResolveTeamWorkstreamExplicitDoesNotEmitNotice asserts that an explicit
// or request-supplied session never triggers the pin's deprecation notice,
// even when a pin is present.
func TestResolveTeamWorkstreamExplicitDoesNotEmitNotice(t *testing.T) {
	tm := team.Team{
		Project:    "/Users/me/My Project",
		Workstream: "pinned-default",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Session: "cto"},
		},
	}
	var got string
	_, stderr, err := captureOutput(t, func() error {
		var rerr error
		got, rerr = resolveTeamWorkstreamName(tm, "explicit-stream", true)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != "explicit-stream" {
		t.Fatalf("resolveTeamWorkstreamName = %q, want explicit-stream", got)
	}
	if strings.Contains(stderr, "deprecated") {
		t.Fatalf("explicit path must not emit the deprecation notice; stderr:\n%s", stderr)
	}
}

// TestResolveTeamWorkstreamValidatesSharedLegacyFallback keeps coverage that an
// inferable-but-invalid shared session (a dotted version like v0.5.0) surfaces
// the session-name validation error rather than silently falling back.
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
