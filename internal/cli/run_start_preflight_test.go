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
	if len(result.Issues) != 1 || result.Issues[0].Code != runStartPreflightExistingProfileSession || len(result.Issues[0].SuggestedFixes) != 3 {
		t.Fatalf("preflight = %+v", result)
	}
	if !strings.Contains(result.Issues[0].SuggestedFixes[2], "--from-profile") {
		t.Fatalf("third suggested fix should name the clone path explicitly: %+v", result.Issues[0].SuggestedFixes)
	}
}

func TestRunStartPreflightFromProfileClonesIntoNewProfile(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, "release-squad", team.Team{Project: dir, Members: []team.Member{{Role: "cto", Binary: "codex", Session: "v1"}}}); err != nil {
		t.Fatal(err)
	}
	result := runStartPreflight(runStartPreflightInput{Project: dir, Profile: "release-squad-v2", ProfileExplicit: true, Session: "v2", FromProfile: "release-squad", FromProfileSet: true, Visibility: "sibling-tabs"})
	if len(result.Issues) != 0 {
		t.Fatalf("preflight = %+v", result)
	}
	if !result.FreshRoster {
		t.Fatalf("expected FreshRoster=true for a from-profile clone into a new profile, got %+v", result)
	}
	if result.FromProfile != "release-squad" {
		t.Fatalf("expected FromProfile=%q, got %+v", "release-squad", result)
	}
}

func TestRunStartPreflightFromProfileRejectsUnknownSource(t *testing.T) {
	dir := t.TempDir()
	result := runStartPreflight(runStartPreflightInput{Project: dir, Profile: "new-squad", ProfileExplicit: true, Session: "v2", FromProfile: "does-not-exist", FromProfileSet: true, Visibility: "sibling-tabs"})
	if len(result.Issues) != 1 || result.Issues[0].Code != runStartPreflightFromProfileNotFound {
		t.Fatalf("preflight = %+v", result)
	}
}

func TestRunStartPreflightFromProfileRejectsSameProfileName(t *testing.T) {
	dir := t.TempDir()
	// Neither "release-squad" profile exists yet: same-name-vs-target is
	// checked before existence so the operator gets the more specific error.
	result := runStartPreflight(runStartPreflightInput{Project: dir, Profile: "release-squad", ProfileExplicit: true, Session: "v2", FromProfile: "release-squad", FromProfileSet: true, Visibility: "sibling-tabs"})
	if len(result.Issues) != 1 || result.Issues[0].Code != runStartPreflightInvalidProfile {
		t.Fatalf("preflight = %+v", result)
	}
}

func TestRunStartPreflightRejectsRolesAndFromProfileTogether(t *testing.T) {
	dir := t.TempDir()
	result := runStartPreflight(runStartPreflightInput{Project: dir, Profile: "new-squad", ProfileExplicit: true, Session: "v2", Roles: "cto", FromProfile: "release-squad", FromProfileSet: true, Visibility: "sibling-tabs"})
	if len(result.Issues) != 1 || result.Issues[0].Code != runStartPreflightConflictingRosterSource {
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
