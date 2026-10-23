package cli

import (
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
)

// TestTeamOperatorSetRejectsRemovedSelfOperatorMode: 3.0 (2a-2) keeps only the
// lead_pane, separate_terminal and noc modes.
func TestTeamOperatorSetRejectsRemovedSelfOperatorMode(t *testing.T) {
	dir := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "session-a"}}})
	err := runTeamOperator([]string{"set", "--project", dir, "--mode", "self_operator"})
	if err == nil || !strings.Contains(err.Error(), "removed in amq-squad 3.0") {
		t.Fatalf("self_operator mode error = %v", err)
	}
}
