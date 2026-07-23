package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestRunStartCloneRosterProfileWritesRestampedRoster(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, "release-squad", team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Trust:        "approve-for-me",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "v1"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "v1", Model: "sonnet"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	if err := runStartCloneRosterProfile(dir, "release-squad-v2", "release-squad", "v2", "", ""); err != nil {
		t.Fatalf("runStartCloneRosterProfile: %v", err)
	}

	cloned, err := team.ReadProfile(dir, "release-squad-v2")
	if err != nil {
		t.Fatalf("ReadProfile(cloned): %v", err)
	}
	if cloned.Lead != "cto" || !cloned.Orchestrated || cloned.Trust != "approve-for-me" {
		t.Fatalf("cloned roster shape = %+v", cloned)
	}
	if len(cloned.Members) != 2 {
		t.Fatalf("cloned.Members = %+v", cloned.Members)
	}
	for _, m := range cloned.Members {
		if m.Session != "v2" {
			t.Fatalf("member %q kept session %q, want restamped to v2", m.Role, m.Session)
		}
	}

	// Source profile must be untouched.
	source, err := team.ReadProfile(dir, "release-squad")
	if err != nil {
		t.Fatalf("ReadProfile(source): %v", err)
	}
	for _, m := range source.Members {
		if m.Session != "v1" {
			t.Fatalf("clone mutated source member %q session to %q", m.Role, m.Session)
		}
	}
}

func TestRunStartCloneRosterProfileHonorsExplicitLeadOverride(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, "release-squad", team.Team{
		Orchestrated: true, Lead: "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "v1"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "v1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := runStartCloneRosterProfile(dir, "release-squad-v2", "release-squad", "v2", "qa", ""); err != nil {
		t.Fatalf("runStartCloneRosterProfile: %v", err)
	}
	cloned, err := team.ReadProfile(dir, "release-squad-v2")
	if err != nil {
		t.Fatal(err)
	}
	if cloned.Lead != "qa" {
		t.Fatalf("cloned.Lead = %q, want explicit override %q", cloned.Lead, "qa")
	}
}

func TestRunStartFromProfilePreviewDoesNotWriteAnyProfile(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.WriteProfile(dir, "release-squad", team.Team{
		Orchestrated: true, Lead: "cto",
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "v1"}},
	}); err != nil {
		t.Fatal(err)
	}

	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "v2", "-P", "release-squad-v2", "--from-profile", "release-squad"}, "test")
	})
	if err != nil {
		t.Fatalf("preview: %v\n%s", err, out)
	}
	if !strings.Contains(out, "release-squad") {
		t.Fatalf("preview should mention the clone source:\n%s", out)
	}
	if team.ExistsProfile(dir, "release-squad-v2") {
		t.Fatalf("preview must not write the target profile")
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".agent-mail")); !os.IsNotExist(statErr) {
		t.Fatalf("preview must not write .agent-mail, stat err=%v", statErr)
	}
}

func TestRunStartFromProfileRejectsMissingSource(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	err := runRunStart([]string{"-p", dir, "-s", "v2", "-P", "release-squad-v2", "--from-profile", "does-not-exist"}, "test")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected a from-profile-not-found refusal, got %v", err)
	}
	if team.ExistsProfile(dir, "release-squad-v2") {
		t.Fatalf("refused clone must not write the target profile")
	}
}

func TestRunStartFromProfileAndRolesConflict(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	err := runRunStart([]string{"-p", dir, "-s", "v2", "-P", "release-squad-v2", "--from-profile", "release-squad", "--roles", "cto"}, "test")
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("expected a conflicting-roster-source refusal, got %v", err)
	}
}

func TestRunStartExistingProfileSessionMismatchNamesCloneFix(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Session: "v1"}},
	}); err != nil {
		t.Fatal(err)
	}
	err := runRunStart([]string{"-p", dir, "-s", "v2", "--roles", "cto"}, "test")
	if err == nil || !strings.Contains(err.Error(), "--from-profile") {
		t.Fatalf("expected the pinned-session refusal to name the clone path, got %v", err)
	}
}
