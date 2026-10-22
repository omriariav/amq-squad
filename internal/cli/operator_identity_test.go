package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

func TestValidateOperatorLaunchRecordRejectsCurrentWorkingDirectoryMismatch(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	oldCWD, _ := os.Getwd()
	if err := os.Chdir(other); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	root := filepath.Join(project, ".agent-mail", "s")
	rec := launch.Record{CWD: project, Role: "qa", Handle: "qa", Session: "s", Root: root, AgentPID: os.Getpid(), Tmux: &launch.TmuxInfo{PaneID: "%9"}}
	err := validateOperatorLaunchRecord(rec, team.Member{Role: "qa", Handle: "qa"}, project, "qa", "qa", "default", "s", root, "%9")
	if err == nil || !strings.Contains(err.Error(), "working directory") {
		t.Fatalf("wrong current CWD = %v", err)
	}
}

func TestDefaultVerifiedCurrentPaneActorRejectsAmbiguousLiveRosterRecordsDeterministically(t *testing.T) {
	for _, tc := range []struct {
		name  string
		order []string
		amMe  string
	}{
		{name: "cto scanned first with qa mailbox selected", order: []string{"cto", "qa"}, amMe: "qa"},
		{name: "qa scanned first with cto mailbox selected", order: []string{"qa", "cto"}, amMe: "cto"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := newAmbiguousCurrentPaneFixture(t, tc.order)
			t.Setenv("AM_ME", tc.amMe)
			actor, err := defaultVerifiedCurrentPaneActor(fx.dir, team.DefaultProfile, "s", fx.cfg)
			if err == nil {
				t.Fatalf("ambiguous pane resolved to %+v", actor)
			}
			got := err.Error()
			for _, want := range []string{"ambiguous", "cto/cto", "qa/qa", `profile="default"`, `session="s"`, `pane="%ambiguous"`} {
				if !strings.Contains(got, want) {
					t.Fatalf("ambiguity error missing %q: %v", want, err)
				}
			}
			if strings.Index(got, "cto/cto") > strings.Index(got, "qa/qa") {
				t.Fatalf("ambiguity tuples are not deterministic: %v", err)
			}
		})
	}
}

type ambiguousCurrentPaneFixture struct {
	dir, root string
	cfg       team.Team
}

func newAmbiguousCurrentPaneFixture(t *testing.T, scanOrder []string) ambiguousCurrentPaneFixture {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, ".agent-mail")
	root := filepath.Join(base, "s")
	members := make([]team.Member, 0, len(scanOrder))
	for i, role := range scanOrder {
		members = append(members, team.Member{Role: role, Handle: role, Binary: "codex", Session: "s"})
		agentDir := filepath.Join(root, "agents", string(rune('a'+i))+"-"+role)
		rec := launch.Record{
			CWD: dir, Binary: "codex", Role: role, Handle: role, TeamProfile: team.DefaultProfile,
			Session: "s", Root: root, TeamHome: dir, AgentPID: os.Getpid(),
			Tmux: &launch.TmuxInfo{PaneID: "%ambiguous"},
		}
		if err := launch.Write(agentDir, rec); err != nil {
			t.Fatalf("write %s launch record: %v", role, err)
		}
	}
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionLeadPane
	cfg := team.Team{Project: dir, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectTeam, Operator: &op, Members: members}
	if err := team.Write(dir, cfg); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX_PANE", "%ambiguous")
	previousBaseRoot := resolveOperatorActorBaseRoot
	resolveOperatorActorBaseRoot = func(string) (string, error) { return base, nil }
	t.Cleanup(func() { resolveOperatorActorBaseRoot = previousBaseRoot })
	return ambiguousCurrentPaneFixture{dir: dir, root: root, cfg: cfg}
}
