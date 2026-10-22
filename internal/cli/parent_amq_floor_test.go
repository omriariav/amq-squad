package cli

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/tmuxpane"
)

type floorRenderingBackend struct {
	fakeBackend
}

func (*floorRenderingBackend) Name() string { return "floor-rendering" }

func (b *floorRenderingBackend) DryRun(t team.Team, opts teamLaunchOptions) error {
	if err := b.fakeBackend.DryRun(t, opts); err != nil {
		return err
	}
	for _, pane := range buildTeamLaunchPanes(t, opts) {
		if _, err := fmt.Fprintln(os.Stdout, pane.Command); err != nil {
			return err
		}
	}
	return nil
}

func useFloorRenderingBackend(t *testing.T) *floorRenderingBackend {
	t.Helper()
	backend := &floorRenderingBackend{}
	prev, hadPrev := teamLaunchBackends[backend.Name()]
	teamLaunchBackends[backend.Name()] = backend
	t.Cleanup(func() {
		if hadPrev {
			teamLaunchBackends[backend.Name()] = prev
			return
		}
		delete(teamLaunchBackends, backend.Name())
	})
	return backend
}

func TestTeamLaunchRejectsPreFloorAMQBeforeParentMutations(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-team"}},
	})
	backend := useFakeBackend(t)
	t.Setenv("AMQ_FAKE_VERSION", "0.48.0")

	err := executeTeamLaunch(teamLaunchOptions{
		Terminal: "fake", Workstream: "floor-team", Profile: team.DefaultProfile,
		Trust: trustModeApproveForMe, SquadBin: "amq-squad", NoBootstrap: true,
	}, true, true)
	if err == nil || !strings.Contains(err.Error(), "cto: agent up refused: amq 0.48.0 is older than required "+doctorMinAMQVersion) {
		t.Fatalf("team launch floor error=%v", err)
	}
	if len(backend.launches) != 0 {
		t.Fatalf("pre-floor team launch opened %d backend launch(es)", len(backend.launches))
	}
	for _, path := range []string{
		filepath.Join(base, "floor-team"),
		briefPathForProfile(dir, team.DefaultProfile, "floor-team"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor team launch mutated %s: %v", path, statErr)
		}
	}
}

func TestTeamLaunchDryRunReportsPreFloorAMQAndRendersPreviewWithoutMutations(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-dry-run"}},
	})
	backend := useFloorRenderingBackend(t)
	t.Setenv("AMQ_FAKE_VERSION", "0.48.0")

	stdout, _, err := captureOutput(t, func() error {
		return executeTeamLaunch(teamLaunchOptions{
			Terminal: backend.Name(), Workstream: "floor-dry-run", Profile: team.DefaultProfile,
			Trust: trustModeApproveForMe, SquadBin: "amq-squad", NoBootstrap: true, DryRun: true,
		}, true, true)
	})
	if err != nil {
		t.Fatalf("team launch dry-run: %v", err)
	}
	for _, want := range []string{
		"AMQ FLOOR VIOLATIONS", "role=cto", "observed=0.48.0",
		"required_min=" + doctorMinAMQVersion, "live=refused",
		"agent up codex", "--role cto", "--session floor-dry-run",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("pre-floor dry-run report missing %q:\n%s", want, stdout)
		}
	}
	if len(backend.dryRuns) != 1 {
		t.Fatalf("pre-floor dry-run rendered %d backend preview(s), want 1", len(backend.dryRuns))
	}
	if len(backend.launches) != 0 {
		t.Fatalf("pre-floor dry-run opened %d backend launch(es)", len(backend.launches))
	}
	for _, path := range []string{
		filepath.Join(base, "floor-dry-run"),
		briefPathForProfile(dir, team.DefaultProfile, "floor-dry-run"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor dry-run mutated %s: %v", path, statErr)
		}
	}
}

func TestExternalLeadLateMemberPreflightRejectsBeforePaneStampOrLaunchRecord(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-external"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "floor-external"},
		},
	})
	backend := useFakeBackend(t)
	originalPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{
			Session: "tmux-main", WindowID: "@7", WindowName: "shell", PaneID: "%5",
		}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = originalPane })
	t.Setenv("AM_ME", "cto")
	t.Setenv("AM_ROOT", filepath.Join(base, "floor-external"))

	originalResolver := resolveTeamLaunchAMQEnv
	resolution := 0
	resolveTeamLaunchAMQEnv = func(cwd, profile, session, handle string) (amqEnv, error) {
		env, err := originalResolver(cwd, profile, session, handle)
		resolution++
		// Two full floor passes consume calls 1-4. Call 6 is the worker in the
		// complete member-env preflight. Before this ordering fix, the external
		// lead pane and launch.json were already stamped/written at call 5.
		if err == nil && resolution == 6 {
			env.AMQVersion = "0.48.0"
		}
		return env, err
	}
	t.Cleanup(func() { resolveTeamLaunchAMQEnv = originalResolver })

	originalStamp := stampCapturedLaunchPane
	stampCalls := 0
	stampCapturedLaunchPane = func(string, string, string) error {
		stampCalls++
		return nil
	}
	t.Cleanup(func() { stampCapturedLaunchPane = originalStamp })

	err := executeTeamLaunch(teamLaunchOptions{
		Terminal: "fake", Workstream: "floor-external", Profile: team.DefaultProfile,
		Trust: trustModeApproveForMe, SquadBin: "amq-squad", NoBootstrap: true,
	}, true, true)
	if err == nil || !strings.Contains(err.Error(), "older than required "+doctorMinAMQVersion) {
		t.Fatalf("external-lead late floor error=%v (resolution calls=%d)", err, resolution)
	}
	if resolution != 6 {
		t.Fatalf("resolution calls=%d, want refusal at complete preflight call 6", resolution)
	}
	if stampCalls != 0 {
		t.Fatalf("pre-floor external-lead path stamped pane %d time(s)", stampCalls)
	}
	if len(backend.launches) != 0 {
		t.Fatalf("pre-floor external-lead path opened %d backend launch(es)", len(backend.launches))
	}
	agentDir := filepath.Join(base, "floor-external", "agents", "cto")
	if _, err := launch.Read(agentDir); !os.IsNotExist(err) {
		t.Fatalf("pre-floor external-lead path wrote launch record: %v", err)
	}
	for _, path := range []string{
		briefPathForProfile(dir, team.DefaultProfile, "floor-external"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("pre-floor external-lead path mutated %s: %v", path, err)
		}
	}
}

func snapshotTestTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		switch {
		case info.Mode().IsRegular():
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += "\x00" + string(body)
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += "\x00" + target
		}
		out[rel] = value
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot tree %s: %v", root, err)
	}
	return out
}
