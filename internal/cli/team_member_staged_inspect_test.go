package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestTeamMemberStagedStatusValidatesLifecycleAndKeepsAbandonedInspectable(t *testing.T) {
	project, _, token, claim := preparedStagedProjectionFixture(t, "codex")
	args := []string{"qa", "--project", project, "--profile", team.DefaultProfile, "--session", "prepared"}

	out, _, err := captureOutput(t, func() error { return runTeamMemberStagedInspect(args, false) })
	if err != nil || !strings.Contains(out, "lifecycle=admitted") {
		t.Fatalf("admitted status output=%q err=%v", out, err)
	}
	if err := abandonPreparedRunStagedClaim(project, team.DefaultProfile, "prepared", token, claim.Role, claim.ClaimID, "inspectable rollback"); err != nil {
		t.Fatal(err)
	}
	out, _, err = captureOutput(t, func() error { return runTeamMemberStagedInspect(args, false) })
	if err != nil || !strings.Contains(out, "lifecycle=abandoned") {
		t.Fatalf("abandoned status output=%q err=%v", out, err)
	}
}

func TestTeamMemberStagedInspectFailsClosedOnMissingOrCorruptLifecycleTransition(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "missing", mutate: func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "corrupt", mutate: func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			project, _, token, claim := preparedStagedProjectionFixture(t, "codex")
			dir := preparedRunStagedTransitionsDir(project, team.DefaultProfile, "prepared", token.Generation, claim.Role, claim.ClaimID)
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("transition entries=%v err=%v", entries, err)
			}
			tc.mutate(t, filepath.Join(dir, entries[0].Name()))
			args := []string{"qa", "--project", project, "--profile", team.DefaultProfile, "--session", "prepared"}
			for _, history := range []bool{false, true} {
				_, _, err := captureOutput(t, func() error { return runTeamMemberStagedInspect(args, history) })
				if err == nil || !strings.Contains(err.Error(), "lacks exact append-only admitted transition") {
					t.Fatalf("history=%t lifecycle validation error=%v", history, err)
				}
			}
		})
	}
}

func TestTeamMemberStagedHistoryRejectsUnexpectedTransitionEntries(t *testing.T) {
	project, _, token, claim := preparedStagedProjectionFixture(t, "claude")
	dir := preparedRunStagedTransitionsDir(project, team.DefaultProfile, "prepared", token.Generation, claim.Role, claim.ClaimID)
	if err := os.Mkdir(filepath.Join(dir, "tampered"), 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{"qa", "--project", project, "--profile", team.DefaultProfile, "--session", "prepared"}
	_, _, err := captureOutput(t, func() error { return runTeamMemberStagedInspect(args, true) })
	if err == nil || !strings.Contains(err.Error(), "unexpected staged transition history entry") {
		t.Fatalf("unexpected transition entry error=%v", err)
	}
}
