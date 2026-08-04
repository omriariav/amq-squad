package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestLiveIdentityRecoveryNamesRegisteredExecutableCommands(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Workstream:   "audit",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: "audit"},
		},
	})
	chdir(t, dir)
	for _, displayed := range []string{"amq-squad status --json", "amq-squad team resume"} {
		if !strings.Contains(liveidentity.RecoveryAction, "'"+displayed+"'") {
			t.Fatalf("live-identity recovery lacks %q: %s", displayed, liveidentity.RecoveryAction)
		}
		argv := strings.Fields(displayed)
		if _, _, err := captureOutput(t, func() error { return Run(argv[1:], "test") }); err != nil {
			t.Fatalf("the live-identity recovery command %q did not execute: %v", displayed, err)
		}
	}
	if strings.Contains(liveidentity.RecoveryAction, "<") {
		t.Fatalf("live-identity recovery must not print unresolved placeholders: %s", liveidentity.RecoveryAction)
	}
}

func TestNextMissingTeamUsesSharedActionableError(t *testing.T) {
	dir := t.TempDir()
	err := runNext([]string{"--project", dir, "--profile", "review", "--session", "audit"})
	if err == nil {
		t.Fatal("next unexpectedly accepted an unconfigured profile")
	}
	for _, want := range []string{
		`no team configured for profile "review"`,
		"Run 'amq-squad new profile review' first.",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("next missing-team error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "read team:") {
		t.Fatalf("next leaked the raw storage failure instead of the shared remedy: %v", err)
	}
}
