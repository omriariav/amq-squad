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
	for _, displayed := range []string{"amq-squad status --json", "amq-squad resume"} {
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
