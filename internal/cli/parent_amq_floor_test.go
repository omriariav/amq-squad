package cli

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-team"),
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
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-dry-run"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor dry-run mutated %s: %v", path, statErr)
		}
	}
}

func TestUpDryRunReportsPreFloorAMQAndRendersCommandsWithoutMutations(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-up-dry-run"}},
	})
	t.Setenv("AMQ_FAKE_VERSION", "0.48.0")

	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"floor-up-dry-run", "--dry-run"})
	})
	if err != nil {
		t.Fatalf("up dry-run: %v", err)
	}
	for _, want := range []string{
		"AMQ FLOOR VIOLATIONS", "role=cto", "observed=0.48.0",
		"required_min=" + doctorMinAMQVersion, "live=refused", "agent up",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("up dry-run report/preview missing %q:\n%s", want, stdout)
		}
	}
	for _, path := range []string{
		filepath.Join(base, "floor-up-dry-run"),
		briefPathForProfile(dir, team.DefaultProfile, "floor-up-dry-run"),
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-up-dry-run"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor up dry-run mutated %s: %v", path, statErr)
		}
	}
}

func TestUpDryRunJSONReportsPreFloorAMQAndRetainsFullPlan(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-up-dry-json"}},
	})
	t.Setenv("AMQ_FAKE_VERSION", "0.48.0")

	stdout, _, err := captureOutput(t, func() error {
		return runUp([]string{"floor-up-dry-json", "--dry-run", "--json"})
	})
	if err != nil {
		t.Fatalf("up dry-run json: %v", err)
	}
	env := decodeJSONEnvelope[teamPlan](t, stdout)
	if env.Kind != "team_plan" || len(env.Data.Plan) != 1 {
		t.Fatalf("dry-run json lost launch plan: kind=%q plan=%+v", env.Kind, env.Data.Plan)
	}
	if len(env.Data.AMQFloorViolations) != 1 {
		t.Fatalf("dry-run json floor violations=%+v, want one", env.Data.AMQFloorViolations)
	}
	violation := env.Data.AMQFloorViolations[0]
	if violation.Role != "cto" || violation.Handle != "cto" ||
		violation.AMQVersion != "0.48.0" || violation.RequiredMin != doctorMinAMQVersion ||
		violation.LiveOutcome != "refused" || !strings.Contains(violation.Detail, "older than required") {
		t.Fatalf("dry-run json floor violation=%+v", violation)
	}
	if !strings.Contains(env.Data.Plan[0].Command, "agent up") {
		t.Fatalf("dry-run json omitted live command: %+v", env.Data.Plan[0])
	}
	for _, path := range []string{
		filepath.Join(base, "floor-up-dry-json"),
		briefPathForProfile(dir, team.DefaultProfile, "floor-up-dry-json"),
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-up-dry-json"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor JSON up dry-run mutated %s: %v", path, statErr)
		}
	}
}

func TestUpResetRejectsPreFloorAMQBeforeDeletingSession(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-reset"}},
	})
	backend := useFakeBackend(t)
	root := filepath.Join(base, "floor-reset")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "must-survive")
	if err := os.WriteFile(sentinel, []byte("unchanged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AMQ_FAKE_VERSION", "0.48.0")

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"floor-reset", "--reset", "--yes", "--terminal", "fake"})
	})
	if err == nil || !strings.Contains(err.Error(), "cto: agent up refused: amq 0.48.0 is older than required "+doctorMinAMQVersion) {
		t.Fatalf("up --reset floor error=%v", err)
	}
	if got, readErr := os.ReadFile(sentinel); readErr != nil || string(got) != "unchanged\n" {
		t.Fatalf("pre-floor reset changed sentinel: body=%q err=%v", got, readErr)
	}
	if len(backend.launches) != 0 {
		t.Fatalf("pre-floor reset opened %d backend launch(es)", len(backend.launches))
	}
	for _, path := range []string{
		briefPathForProfile(dir, team.DefaultProfile, "floor-reset"),
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-reset"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor reset mutated %s: %v", path, statErr)
		}
	}
}

func TestUpResetRejectsNewlyPreFloorResolutionBeforeByteIdenticalSessionDeletion(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-reset-late"}},
	})
	_ = useFakeBackend(t)
	root := filepath.Join(base, "floor-reset-late")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "must-survive"), []byte{0, 1, 2, '\n'}, 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotTestTree(t, root)

	originalResolver := resolveTeamLaunchAMQEnv
	resolution := 0
	resolveTeamLaunchAMQEnv = func(cwd, profile, session, handle string) (amqEnv, error) {
		env, err := originalResolver(cwd, profile, session, handle)
		resolution++
		// The initial and under-admission checks pass. The complete pinned
		// preflight then observes a newly pre-floor version; this used to occur
		// only inside executeTeamLaunch after --reset deleted the old session.
		if err == nil && resolution == 3 {
			env.AMQVersion = "0.48.0"
		}
		return env, err
	}
	t.Cleanup(func() { resolveTeamLaunchAMQEnv = originalResolver })

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"floor-reset-late", "--reset", "--yes", "--terminal", "fake"})
	})
	if err == nil || !strings.Contains(err.Error(), "older than required "+doctorMinAMQVersion) {
		t.Fatalf("late up --reset floor error=%v (resolution calls=%d)", err, resolution)
	}
	if resolution != 3 {
		t.Fatalf("resolution calls=%d, want refusal at pinned preflight call 3", resolution)
	}
	after := snapshotTestTree(t, root)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("pre-floor reset changed existing session tree:\nbefore=%#v\nafter=%#v", before, after)
	}
	if _, err := os.Stat(briefPathForProfile(dir, team.DefaultProfile, "floor-reset-late")); !os.IsNotExist(err) {
		t.Fatalf("pre-floor reset wrote brief: %v", err)
	}
}

func TestUpResetExternalLeadConsumesPinnedAMQSnapshotAfterDeletion(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-reset-external"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "floor-reset-external"},
		},
	})
	backend := useFakeBackend(t)
	root := filepath.Join(base, "floor-reset-external")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(root, "deleted-by-reset")
	if err := os.WriteFile(sentinel, []byte("old session\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	originalPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{
			Session: "tmux-main", WindowID: "@7", WindowName: "shell", PaneID: "%5",
		}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = originalPane })
	t.Setenv("AM_ME", "cto")
	t.Setenv("AM_BASE_ROOT", base)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_SESSION", "floor-reset-external")

	originalResolver := resolveTeamLaunchAMQEnv
	resolution := 0
	resolveTeamLaunchAMQEnv = func(cwd, profile, session, handle string) (amqEnv, error) {
		env, err := originalResolver(cwd, profile, session, handle)
		resolution++
		// Calls 1-4 are the two complete floor passes. Calls 5-6 build the
		// complete pinned snapshot before reset. A seventh call would be the
		// historical post-deletion external-lead re-resolution.
		if err == nil && resolution >= 7 {
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

	_, _, err := captureOutput(t, func() error {
		return runUp([]string{"floor-reset-external", "--reset", "--yes", "--terminal", "fake"})
	})
	if err != nil {
		t.Fatalf("up --reset with pinned external lead: %v (resolution calls=%d)", err, resolution)
	}
	if resolution != 6 {
		t.Fatalf("resolution calls=%d, want only six pre-reset roster resolutions", resolution)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatalf("reset did not cross the destructive boundary: %v", err)
	}
	if stampCalls != 1 {
		t.Fatalf("external lead pane stamps=%d, want 1", stampCalls)
	}
	if len(backend.launches) != 1 || len(backend.teams) != 1 ||
		len(backend.teams[0].Members) != 1 || backend.teams[0].Members[0].Role != "qa" {
		t.Fatalf("post-reset worker launch=%d teams=%+v", len(backend.launches), backend.teams)
	}
	rec, err := launch.Read(filepath.Join(root, "agents", "cto"))
	if err != nil {
		t.Fatalf("read pinned external lead record: %v", err)
	}
	if !rec.External || rec.Handle != "cto" || rec.Session != "floor-reset-external" {
		t.Fatalf("pinned external lead record=%+v", rec)
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
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-external"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("pre-floor external-lead path mutated %s: %v", path, err)
		}
	}
}

func TestResumeExecRejectsPreFloorAMQBeforeParentMutations(t *testing.T) {
	// gh#758/t11 slice B commit 3: rewritten against runResumeExec
	// (resumeExecRequest), the fold's own entry point, replacing the
	// deleted team_resume.go executeResume/resumeExecution call. The
	// property under test is unchanged and still real: buildSimpleStartPlan
	// (simple_start.go) calls validateLaunchAMQVersion per member before
	// runSimpleStartWithRequest ever reaches a mutation (AMQ root creation,
	// brief write, notifier registration, launch), so a stale AMQ version
	// must still refuse closed pre-mutation on the new path.
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "floor-resume"}},
	})
	t.Setenv("AMQ_FAKE_VERSION", "0.48.0")

	err := runResumeExec(resumeExecRequest{
		ProjectDir: dir, Profile: team.DefaultProfile, Session: "floor-resume",
		Target: "current-window", Layout: "vertical",
	}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "cto: agent up refused: amq 0.48.0 is older than required "+doctorMinAMQVersion) {
		t.Fatalf("resume --exec floor error=%v", err)
	}
	for _, path := range []string{
		filepath.Join(base, "floor-resume"),
		briefPathForProfile(dir, team.DefaultProfile, "floor-resume"),
		notificationWatcherRuntimePath(dir, team.DefaultProfile, "floor-resume"),
	} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("pre-floor resume mutated %s: %v", path, statErr)
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
