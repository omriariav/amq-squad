package cli

import (
	"reflect"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

func TestPreparedPinnedLaunchAdmissionRereadExcludesStagedMembers(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated:  true,
		Lead:          "cto",
		LeadMode:      team.LeadModePlanner,
		ExecutionMode: executionModeProjectLead,
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: "prepared", ActorMode: team.ActorModeReview},
			{Role: "qa", Handle: "qa", Binary: "claude", Session: "prepared", ActorMode: team.ActorModeReview},
		},
	})
	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{
			"--project", dir, "--profile", team.DefaultProfile, "--session", "prepared",
			"--launch-shape", runwizard.LaunchShapeLeadOnlyStaged,
			"--staged-roles", "qa",
			"--goal", "Launch only the immutable initial roster",
			"--visibility", visibilityDetached, "--prepare",
		}, "test")
	}); err != nil {
		t.Fatalf("prepare staged run: %v", err)
	}
	manifest, digest, err := readPreparedRunManifestSnapshot(dir, team.DefaultProfile, "prepared")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest.InitialRoster, []string{"cto"}) || !reflect.DeepEqual(manifest.StagedRoster, []string{"qa"}) {
		t.Fatalf("prepared rosters initial=%v staged=%v", manifest.InitialRoster, manifest.StagedRoster)
	}
	token := preparedRunTokenFromSnapshot(manifest, digest)
	token.LaunchAttempt, err = reservePreparedRunLaunch(dir, team.DefaultProfile, "prepared", token)
	if err != nil {
		t.Fatal(err)
	}

	backend := useFakeTmuxBackend(t)
	var result teamLaunchResult
	err = executeTeamLaunch(teamLaunchOptions{
		Terminal: "tmux", Target: "new-session", Layout: "vertical", Workstream: "prepared",
		Profile: team.DefaultProfile, Trust: trustModeApproveForMe, SquadBin: "amq-squad",
		NoBootstrap: true, PreparedRunToken: token,
		ResultSink: func(got teamLaunchResult) { result = got },
	}, true, true)
	if err != nil {
		t.Fatalf("pinned launch: %v", err)
	}
	if len(backend.teams) != 1 {
		t.Fatalf("backend launches=%d want=1", len(backend.teams))
	}
	if got := teamMemberRoles(backend.teams[0].Members); !reflect.DeepEqual(got, []string{"cto"}) {
		t.Fatalf("terminal backend received roles=%v, want only immutable initial roster", got)
	}
	if len(result.Panes) != 1 || result.Panes[0].Role != "cto" {
		t.Fatalf("launch result leaked staged panes: %+v", result)
	}
}
