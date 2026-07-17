package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestValidateOperatorLaunchRecordAuthorizationAndRuntimeBinding(t *testing.T) {
	project := t.TempDir()
	memberDir := filepath.Join(project, "member")
	if err := os.MkdirAll(memberDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldCWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(memberDir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCWD) })
	root, pane := filepath.Join(project, ".agent-mail", "s"), "%7"
	member := team.Member{Role: "cto", Handle: "cto", CWD: memberDir}
	base := launch.Record{CWD: memberDir, Role: "cto", Handle: "cto", TeamProfile: "default", Session: "s", Root: root, AgentPID: os.Getpid(), Tmux: &launch.TmuxInfo{PaneID: pane}}
	if err := validateOperatorLaunchRecord(base, member, project, "cto", "cto", "default", "s", root, pane); err != nil {
		t.Fatalf("managed record rejected: %v", err)
	}

	externalUnauthorized := base
	externalUnauthorized.External = true
	externalUnauthorized.AdoptionMode = adoptionModeExternal
	if err := validateOperatorLaunchRecord(externalUnauthorized, member, project, "cto", "cto", "default", "s", root, pane); err == nil {
		t.Fatal("unauthorized generic external adoption accepted")
	}
	externalProjectLead := externalUnauthorized
	externalProjectLead.AdoptionMode = adoptionModeExternalProjectLead
	if err := validateOperatorLaunchRecord(externalProjectLead, member, project, "cto", "cto", "default", "s", root, pane); err != nil {
		t.Fatalf("authorized external project lead rejected: %v", err)
	}
	externalNativeGoal := externalUnauthorized
	externalNativeGoal.Binary = "claude"
	externalNativeGoal.GoalBinding = &launch.GoalBinding{
		Mode: "native_goal", NativeGoal: true, Source: "goal-control", Command: `/goal --goal "ship"`,
		DeliveryState: goalBindingDeliveryDelivered,
	}
	if err := validateOperatorLaunchRecord(externalNativeGoal, member, project, "cto", "cto", "default", "s", root, pane); err != nil {
		t.Fatalf("native-goal external record rejected: %v", err)
	}

	for name, mutate := range map[string]func(*launch.Record){
		"wrong record cwd": func(r *launch.Record) { r.CWD = project },
		"stale pid":        func(r *launch.Record) { r.AgentPID = 99999999 },
		"wrong pane":       func(r *launch.Record) { r.Tmux.PaneID = "%8" },
		"wrong session":    func(r *launch.Record) { r.Session = "other" },
		"wrong root":       func(r *launch.Record) { r.Root = filepath.Join(project, "other") },
	} {
		t.Run(name, func(t *testing.T) {
			rec := base
			tmux := *base.Tmux
			rec.Tmux = &tmux
			mutate(&rec)
			if err := validateOperatorLaunchRecord(rec, member, project, "cto", "cto", "default", "s", root, pane); err == nil {
				t.Fatalf("%s accepted", name)
			}
		})
	}
}

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
