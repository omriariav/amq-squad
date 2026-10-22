package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// TestTeamLaunchDryRunDoesNotCreateBrief covers the team-launch backend
// dry-run path (separate flag set with --dry-run meaning tmux-backend
// preview). It must also stay read-only.
func TestTeamLaunchDryRunDoesNotCreateBrief(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--dry-run", "--session", "issue-96", "--no-bootstrap"})
	}); err != nil {
		t.Fatalf("team launch --dry-run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".amq-squad", "briefs")); err == nil {
		t.Error("team launch --dry-run created briefs dir")
	}
}
