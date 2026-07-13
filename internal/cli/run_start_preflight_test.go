package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunStartPreflightReturnsStructuredCollision(t *testing.T) {
	dir := t.TempDir()
	result := runStartPreflight(runStartPreflightInput{
		Project: dir, Profile: "review", ProfileExplicit: true, Session: "review", Roles: "cto", Visibility: "sibling-tabs",
	})
	if len(result.Issues) != 1 || result.Issues[0].Code != runStartPreflightNamespaceCollision || len(result.Issues[0].SuggestedFixes) == 0 {
		t.Fatalf("preflight = %+v", result)
	}
	if err := result.Err(); err == nil || !strings.Contains(err.Error(), "colliding AMQ roots") {
		t.Fatalf("formatted error = %v", err)
	}
}

func TestRunStartPreflightReturnsStructuredPinnedSessionMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{Project: dir, Members: []team.Member{{Role: "cto", Binary: "codex", Session: "existing"}}}); err != nil {
		t.Fatal(err)
	}
	result := runStartPreflight(runStartPreflightInput{Project: dir, Session: "new", Roles: "cto", Visibility: "sibling-tabs"})
	if len(result.Issues) != 1 || result.Issues[0].Code != runStartPreflightExistingProfileSession || len(result.Issues[0].SuggestedFixes) != 2 {
		t.Fatalf("preflight = %+v", result)
	}
}

func TestRunStartPreflightExistingProfileEffortIsLaunchOnlyAndValid(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{Project: dir, Members: []team.Member{{Role: "cto", Binary: "codex", Session: "sess"}}}); err != nil {
		t.Fatal(err)
	}
	var result runStartPreflightResult
	_, stderr, err := captureOutput(t, func() error {
		result = runStartPreflight(runStartPreflightInput{Project: dir, Session: "sess", Visibility: "sibling-tabs", Effort: "cto=FutureTier", EffortSet: true})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Issues) != 0 {
		t.Fatalf("preflight = %+v", result)
	}
	if strings.Contains(stderr, "not in the merged catalog") {
		t.Fatalf("preflight must validate quietly so the command surface warns once: %q", stderr)
	}
	stored, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if got := memberEffort(stored.Members[0]); got != effortAutomatic {
		t.Fatalf("preflight mutated stored effort to %q", got)
	}
}

func TestRunStartPreflightAcceptsCurrentAndCustomEffortForSupportedBinary(t *testing.T) {
	dir := t.TempDir()
	for _, effort := range []string{"xhigh", "max", "FutureTier"} {
		result := runStartPreflight(runStartPreflightInput{
			Project: dir, Session: "sess", Roles: "qa", Binary: "qa=claude", Visibility: "sibling-tabs", Effort: "qa=" + effort, EffortSet: true,
		})
		if len(result.Issues) != 0 {
			t.Fatalf("effort %q preflight = %+v", effort, result)
		}
	}
}
