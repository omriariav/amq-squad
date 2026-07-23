package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/worktreeplan"
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
		Schema: team.SchemaVersion,
		Members: []team.Member{{
			Role: "worker", Handle: "worker", Binary: "codex", Session: "s",
			ActorMode: team.ActorModeImplementation,
		}},
	}
	operator := team.DisabledOperator()
	configured.Operator = &operator
	if err := team.Write(project, configured); err != nil {
		t.Fatal(err)
	}
	d := doctorExecution{
		ProjectDir: project,
		Profile:    team.DefaultProfile,
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

func TestStatusWorktreeColumnsAndJSON(t *testing.T) {
	status := &worktreeplan.MemberStatus{
		Role: "worker", State: worktreeplan.StateHandoff,
		Worktree: "/tmp/repo-wt", Branch: "amq-squad/p/s/worker/t1",
		Dirty: true, HandoffSHA: "0123456789abcdef",
	}
	worktree, branch, baseHead, gitState, scope, handoff := statusWorktreeColumns(status)
	if worktree != "handoff:/tmp/repo-wt" || branch != "amq-squad/p/s/worker/t1" ||
		baseHead != "-..-" || gitState != "dirty" || scope != "" || handoff != "0123456789ab" {
		t.Fatalf("columns = %q %q %q %q %q %q", worktree, branch, baseHead, gitState, scope, handoff)
	}
	row := statusRecord{Role: "worker", Worktree: status}
	var out bytes.Buffer
	if err := writeJSONEnvelope(&out, "status_worktree_test", row); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"worktree"`) || !strings.Contains(out.String(), `"handoff_sha"`) {
		t.Fatalf("JSON missing worktree contract: %s", out.String())
	}
}
