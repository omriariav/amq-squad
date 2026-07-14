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
	externalNativeGoal.GoalBinding = &launch.GoalBinding{Mode: "native_goal", NativeGoal: true, Source: "goal-control", Command: `/goal --goal "ship"`}
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
