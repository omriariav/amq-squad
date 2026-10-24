package cli

import (
	"strings"
	"testing"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/worktreeplan"
)

func TestWorktreeCommandRegisteredAndMaterializeRequiresConfirmation(t *testing.T) {
	if _, ok := lookupCommand("worktree", "dev"); !ok {
		t.Fatal("worktree command is not registered")
	}
	err := runWorktree([]string{
		"materialize", "--role", "worker", "--task", "t1", "--base", "HEAD",
		"--scope", "internal/**", "--session", "s",
	})
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Fatalf("materialize confirmation error = %v", err)
	}
}

func TestDoctorWorktreeRowsAreStableAndTyped(t *testing.T) {
	project := t.TempDir()
	configured := team.Team{
		Members: []team.Member{{
			Role: "worker", Handle: "worker", Binary: "codex", Session: "s",
			ActorMode: team.ActorModeImplementation,
		}},
	}
	if err := team.Write(project, configured); err != nil {
		t.Fatal(err)
	}
	d := doctorExecution{
		ProjectDir: project,
		Profile:    squadnamespace.DefaultProfile,
		WorktreeDiagnostics: func(team.Team, string, string) ([]worktreeplan.Diagnostic, error) {
			out := make([]worktreeplan.Diagnostic, 0, len(worktreeDiagnosticKinds))
			for _, kind := range worktreeDiagnosticKinds {
				status := worktreeplan.DiagnosticOK
				if kind == "worktree-plan-drift" {
					status = worktreeplan.DiagnosticFail
				}
				out = append(out, worktreeplan.Diagnostic{Kind: kind, Status: status, Detail: "detail"})
			}
			return out, nil
		},
	}
	checks := doctorCheckWorktrees(d, "s")
	if len(checks) != len(worktreeDiagnosticKinds) {
		t.Fatalf("checks = %#v", checks)
	}
	for i, kind := range worktreeDiagnosticKinds {
		if checks[i].Name != "worktree/"+kind || checks[i].Kind != kind {
			t.Fatalf("check[%d] = %+v", i, checks[i])
		}
	}
	if checks[3].Status != doctorFail {
		t.Fatalf("drift status = %s", checks[3].Status)
	}
}
