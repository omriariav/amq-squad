package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// withStubbedTmux swaps orchestrateTmuxRun for a recorder and restores it.
func withStubbedTmux(t *testing.T) *[][]string {
	t.Helper()
	var calls [][]string
	prev := orchestrateTmuxRun
	orchestrateTmuxRun = func(args ...string) error {
		calls = append(calls, append([]string{}, args...))
		return nil
	}
	t.Cleanup(func() { orchestrateTmuxRun = prev })
	return &calls
}

func useFakeTmuxBackend(t *testing.T) *fakeBackend {
	t.Helper()
	backend := &fakeBackend{}
	prev, hadPrev := teamLaunchBackends["tmux"]
	teamLaunchBackends["tmux"] = backend
	t.Cleanup(func() {
		if hadPrev {
			teamLaunchBackends["tmux"] = prev
			return
		}
		delete(teamLaunchBackends, "tmux")
	})
	return backend
}

func stubCurrentRunStartPane(t *testing.T, paneID string) {
	t.Helper()
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", paneID)
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: paneID}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
}

func stubRunStartLeadWake(t *testing.T) {
	t.Helper()
	prev := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		return leadWakeResult{PID: 1234, Started: true, Detail: "ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prev })
}

func TestGlobalStartPreviewDoesNotLaunch(t *testing.T) {
	calls := withStubbedTmux(t)
	out, _, err := captureOutput(t, func() error {
		return runGlobalStart([]string{"--root", t.TempDir()})
	})
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("preview must not launch tmux, got calls: %v", *calls)
	}
	if !strings.Contains(out, "PREVIEW only") {
		t.Fatalf("preview output missing PREVIEW banner:\n%s", out)
	}
	if !strings.Contains(out, "poller mode") {
		t.Fatalf("preview output should describe poller mode:\n%s", out)
	}
}

func TestGlobalStartGoLaunchesTmuxWithAgentArgv(t *testing.T) {
	if strings.TrimSpace(os.Getenv("TMUX")) == "" {
		t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	}
	calls := withStubbedTmux(t)
	root := t.TempDir()
	_, _, err := captureOutput(t, func() error {
		return runGlobalStart([]string{"--root", root, "--agent", "codex", "--model", "gpt-5", "--codex-args", "--enable goals", "--go"})
	})
	// LookPath for "codex"/"tmux" may fail in CI; only assert argv shape when it launched.
	if err != nil {
		if strings.Contains(err.Error(), "not found on PATH") {
			t.Skipf("agent/tmux binary unavailable in this environment: %v", err)
		}
		t.Fatalf("go launch returned error: %v", err)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected exactly one tmux call, got %v", *calls)
	}
	got := strings.Join((*calls)[0], " ")
	for _, want := range []string{"new-window", "-c " + root, "-n global-orch", "codex --model gpt-5 --enable goals"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tmux argv %q missing %q", got, want)
		}
	}
}

func TestGlobalStartRejectsBadAgent(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runGlobalStart([]string{"--agent", "vim", "--root", t.TempDir()})
	})
	if err == nil || !strings.Contains(err.Error(), "--agent must be claude or codex") {
		t.Fatalf("expected agent validation error, got %v", err)
	}
}

func TestGlobalUnknownSubcommand(t *testing.T) {
	err := runGlobal([]string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown 'global' subcommand") {
		t.Fatalf("expected unknown-subcommand error, got %v", err)
	}
}

func TestGlobalDispatchHelpAndEmpty(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return runGlobal([]string{}) })
	if err == nil || !strings.Contains(err.Error(), "global requires a subcommand") {
		t.Fatalf("empty global should require a subcommand, got %v", err)
	}
	_, _, err = captureOutput(t, func() error { return runGlobal([]string{"-h"}) })
	if err != nil {
		t.Fatalf("global -h should not error, got %v", err)
	}
}

func TestRunCmdDispatch(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return runRunCmd([]string{}, "test") })
	if err == nil || !strings.Contains(err.Error(), "run requires a subcommand") {
		t.Fatalf("empty run should require a subcommand, got %v", err)
	}
	_, _, err = captureOutput(t, func() error { return runRunCmd([]string{"bogus"}, "test") })
	if err == nil || !strings.Contains(err.Error(), "unknown 'run' subcommand") {
		t.Fatalf("expected unknown-subcommand error, got %v", err)
	}
	_, _, err = captureOutput(t, func() error { return runRunCmd([]string{"-h"}, "test") })
	if err != nil {
		t.Fatalf("run -h should not error, got %v", err)
	}
}

func TestRunStartRequiresProjectAndSession(t *testing.T) {
	if err := runRunStart([]string{"-s", "x"}, "test"); err == nil || !strings.Contains(err.Error(), "requires --project") {
		t.Fatalf("expected --project error, got %v", err)
	}
	if err := runRunStart([]string{"-p", t.TempDir()}, "test"); err == nil || !strings.Contains(err.Error(), "requires --session") {
		t.Fatalf("expected --session error, got %v", err)
	}
}

func TestRunStartExternalLeadPreviewExistingProfileIsReadOnlyAndWorkerOnly(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project:      "",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	backend := useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")

	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--external-lead"}, "test")
	})
	if err != nil {
		t.Fatalf("external-lead preview: %v\n%s", err, out)
	}
	if !strings.Contains(out, "orchestrated run (external lead)") || !strings.Contains(out, "Preview OK") {
		t.Fatalf("preview output missing external-lead/ok text:\n%s", out)
	}
	if len(backend.dryRuns) != 1 || len(backend.teams) != 1 {
		t.Fatalf("expected one worker dry-run, got dryRuns=%d teams=%d", len(backend.dryRuns), len(backend.teams))
	}
	if got := backend.teams[0].Members; len(got) != 1 || got[0].Role != "qa" {
		t.Fatalf("worker dry-run members = %+v, want only qa", got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-mail")); !os.IsNotExist(err) {
		t.Fatalf("preview must not create .agent-mail, stat err=%v", err)
	}
	if _, err := launch.Read(filepath.Join(dir, ".agent-mail", "sess", "agents", "cto")); err == nil {
		t.Fatal("preview must not write external lead launch record")
	}
}

func TestRunStartExternalLeadPreviewFreshProfileDoesNotWriteTeam(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	stubCurrentRunStartPane(t, "%42")

	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--roles", "cto,qa", "--external-lead"}, "test")
	})
	if err != nil {
		t.Fatalf("fresh external-lead preview: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Spawn validation is deferred") {
		t.Fatalf("fresh preview should explain deferred worker validation:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".amq-squad")); !os.IsNotExist(err) {
		t.Fatalf("fresh preview must not write .amq-squad, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agent-mail")); !os.IsNotExist(err) {
		t.Fatalf("fresh preview must not write .agent-mail, stat err=%v", err)
	}
}

func TestRunStartExternalLeadGoBindsLeadAndSpawnsOnlyWorkers(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project:      "",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	backend := useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")
	stubRunStartLeadWake(t)

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--external-lead", "--go"}, "test")
	})
	if err != nil {
		t.Fatalf("external-lead --go: %v", err)
	}
	if len(backend.dryRuns) != 1 || len(backend.launches) != 1 || len(backend.teams) != 2 {
		t.Fatalf("expected one worker dry-run and one worker launch, got dryRuns=%d launches=%d teams=%d", len(backend.dryRuns), len(backend.launches), len(backend.teams))
	}
	for _, got := range []team.Team{backend.teams[0], backend.teams[1]} {
		if len(got.Members) != 1 || got.Members[0].Role != "qa" {
			t.Fatalf("worker launch members = %+v, want only qa", got.Members)
		}
	}
	rec, err := launch.Read(filepath.Join(dir, ".agent-mail", "sess", "agents", "cto"))
	if err != nil {
		t.Fatalf("read external lead record: %v", err)
	}
	if !rec.External || rec.AdoptionMode != adoptionModeExternalProjectLead || rec.Tmux == nil || rec.Tmux.PaneID != "%42" {
		t.Fatalf("external lead record = %+v", rec)
	}
}

func TestRunStartExternalLeadAcceptsCaseInsensitiveExplicitLead(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project:      "",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")

	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--external-lead", "--lead", "CTO"}, "test")
	}); err != nil {
		t.Fatalf("expected case-insensitive explicit lead to pass, got %v", err)
	}
}

func TestRunStartExternalLeadExistingProfileLeadMismatchFailsWithGuidance(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project:      "",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	stubCurrentRunStartPane(t, "%42")

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--external-lead", "--lead", "qa"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "team lead set") {
		t.Fatalf("expected lead mismatch guidance, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".agent-mail")); !os.IsNotExist(statErr) {
		t.Fatalf("lead mismatch must not create .agent-mail, stat err=%v", statErr)
	}
}

func TestRunStartExternalLeadRefusesExistingWorkstreamBeforeWrites(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Project:      "",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	stubCurrentRunStartPane(t, "%42")
	if err := os.MkdirAll(filepath.Join(base, "sess"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--external-lead", "--go"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "session \"sess\" already exists") {
		t.Fatalf("expected existing workstream refusal, got %v", err)
	}
	if _, err := launch.Read(filepath.Join(base, "sess", "agents", "cto")); err == nil {
		t.Fatal("existing workstream refusal must not write external lead launch record")
	}
}

func TestRunStartExternalLeadMissingTmuxFailsPreviewAndGoBeforeWrites(t *testing.T) {
	for _, goMode := range []bool{false, true} {
		t.Run(map[bool]string{false: "preview", true: "go"}[goMode], func(t *testing.T) {
			dir := seedTeam(t, team.Team{
				Project:      "",
				Orchestrated: true,
				Lead:         "cto",
				Members: []team.Member{
					{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
					{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
				},
			})
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_PANE", "")

			args := []string{"-p", dir, "-s", "sess", "--external-lead"}
			if goMode {
				args = append(args, "--go")
			}
			_, _, err := captureOutput(t, func() error {
				return runRunStart(args, "test")
			})
			if err == nil || !strings.Contains(err.Error(), "requires the current lead pane") {
				t.Fatalf("expected missing tmux pane refusal, got %v", err)
			}
			if _, statErr := os.Stat(filepath.Join(dir, ".agent-mail")); !os.IsNotExist(statErr) {
				t.Fatalf("missing tmux refusal must not create .agent-mail, stat err=%v", statErr)
			}
		})
	}
}

func TestRunStartExternalLeadFreshLeadValidationBeforeTeamWrite(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	stubCurrentRunStartPane(t, "%42")

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--roles", "qa", "--lead", "cto", "--external-lead", "--go"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "not included in --roles") {
		t.Fatalf("expected pre-write lead validation error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".amq-squad")); !os.IsNotExist(statErr) {
		t.Fatalf("failed fresh external-lead preflight must not write .amq-squad, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".agent-mail")); !os.IsNotExist(statErr) {
		t.Fatalf("failed fresh external-lead preflight must not write .agent-mail, stat err=%v", statErr)
	}
}

func TestRunStartExternalLeadGoalDeliveredToBoundLead(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project:      "",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")
	stubRunStartLeadWake(t)
	var goalCalls [][]string
	stubRunStartGoalDelivery(t,
		func(args []string, version string) error { return nil },
		func(args []string, version string) error {
			goalCalls = append(goalCalls, append([]string{}, args...))
			return nil
		},
		func(project, profile, session, role string) (runStartLeadReadiness, error) {
			return runStartLeadReadiness{Ready: true, Detail: "live"}, nil
		},
		func(time.Duration) {},
		time.Now,
	)

	if _, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--external-lead", "--goal", "ship it", "--go"}, "test")
	}); err != nil {
		t.Fatalf("external-lead --goal --go: %v", err)
	}
	if len(goalCalls) != 1 {
		t.Fatalf("expected one goal call, got %v", goalCalls)
	}
	goal := strings.Join(goalCalls[0], " ")
	for _, want := range []string{"start", "--project " + dir, "--profile default", "--session sess", "--role cto", "--goal ship it", "--yes"} {
		if !strings.Contains(goal, want) {
			t.Fatalf("goal args %q missing %q", goal, want)
		}
	}
	if strings.Contains(goal, "--register-orchestrator") {
		t.Fatalf("external-lead goal delivery must not re-register orchestrator: %q", goal)
	}
}

func TestRunStartExternalLeadLeadOnlyRosterReportsSuccess(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project:      "",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
		},
	})
	backend := useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")
	stubRunStartLeadWake(t)

	_, stderr, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--external-lead", "--go"}, "test")
	})
	if err != nil {
		t.Fatalf("lead-only external-lead --go: %v", err)
	}
	if !strings.Contains(stderr, "lead bound; no remaining workers to spawn") {
		t.Fatalf("lead-only run should report no remaining workers, stderr:\n%s", stderr)
	}
	if len(backend.dryRuns) != 0 || len(backend.launches) != 0 {
		t.Fatalf("lead-only run should not call backend, dryRuns=%d launches=%d", len(backend.dryRuns), len(backend.launches))
	}
	if _, err := launch.Read(filepath.Join(dir, ".agent-mail", "sess", "agents", "cto")); err != nil {
		t.Fatalf("lead-only run should bind current lead: %v", err)
	}
}

func TestRunStartExternalLeadWorkerValidationBeforeBind(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project:      "",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "sess"},
		},
	})
	useFakeTmuxBackend(t)
	stubCurrentRunStartPane(t, "%42")
	wakeCalls := 0
	prev := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		wakeCalls++
		return leadWakeResult{PID: 1234, Started: true, Detail: "ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prev })

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--external-lead", "--model", "missing=gpt-5", "--go"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "--model has unknown role(s): missing") {
		t.Fatalf("expected worker validation error, got %v", err)
	}
	if wakeCalls != 0 {
		t.Fatalf("worker validation failure must happen before wake start, wakeCalls=%d", wakeCalls)
	}
	if _, err := launch.Read(filepath.Join(dir, ".agent-mail", "sess", "agents", "cto")); err == nil {
		t.Fatal("worker validation failure must not write external lead launch record")
	}
}

func TestRunStartNoRolesNoTeamErrors(t *testing.T) {
	err := runRunStart([]string{"-p", t.TempDir(), "-s", "sess"}, "test")
	if err == nil || !strings.Contains(err.Error(), "no team profile") {
		t.Fatalf("expected no-team error, got %v", err)
	}
}

func TestRunStartRejectsBadSession(t *testing.T) {
	err := runRunStart([]string{"-p", t.TempDir(), "-s", "Bad Session!", "--roles", "cto"}, "test")
	if err == nil || !strings.Contains(err.Error(), "invalid --session") {
		t.Fatalf("expected session validation error, got %v", err)
	}
}

func TestRunStartRejectsProfileSessionCollisionBeforeWrite(t *testing.T) {
	dir := t.TempDir()
	err := runRunStart([]string{"-p", dir, "-P", "review", "-s", "review", "--roles", "cto"}, "test")
	if err == nil ||
		!strings.Contains(err.Error(), "run start refused") ||
		!strings.Contains(err.Error(), "colliding AMQ roots") ||
		!strings.Contains(err.Error(), "--profile codex-review --session review") {
		t.Fatalf("expected profile/session collision error, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".amq-squad")); !os.IsNotExist(statErr) {
		t.Fatalf("refused run start must not write .amq-squad; stat err = %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".agent-mail")); !os.IsNotExist(statErr) {
		t.Fatalf("refused run start must not write .agent-mail; stat err = %v", statErr)
	}
}

func TestRunStartDefaultsToSiblingTabsInPreview(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	// A fresh project with --roles: preview should describe a visible sibling-tab
	// spawn by default, stay usable outside tmux, and note the deferred spawn
	// validation.
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto"}, "test")
	})
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if !strings.Contains(out, "--visibility sibling-tabs") {
		t.Fatalf("default visibility should be sibling-tabs:\n%s", out)
	}
	if !strings.Contains(out, "--go with --visibility sibling-tabs requires a visible tmux pane") {
		t.Fatalf("outside-tmux preview should warn about --go requirements:\n%s", out)
	}
	if strings.Contains(out, "hidden") {
		t.Fatalf("default preview should not describe detached hidden spawn:\n%s", out)
	}
}

func TestRunStartGoOutsideTmuxRefusesSiblingTabsDefault(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto", "--go"}, "test")
	})
	if err == nil {
		t.Fatal("expected outside-tmux --go to fail under sibling-tabs default")
	}
	for _, want := range []string{"--visibility sibling-tabs", "Run inside tmux", "--visibility detached"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestRunStartGoInsideTmuxDefaultsToSiblingTabsBackend(t *testing.T) {
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%42")
	backend := &fakeBackend{}
	prev, hadPrev := teamLaunchBackends["tmux"]
	teamLaunchBackends["tmux"] = backend
	t.Cleanup(func() {
		if hadPrev {
			teamLaunchBackends["tmux"] = prev
			return
		}
		delete(teamLaunchBackends, "tmux")
	})

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto", "--go"}, "test")
	})
	if err != nil {
		t.Fatalf("run start --go inside tmux: %v", err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("expected one launch, got %+v", backend.launches)
	}
	got := backend.launches[0]
	if got.Terminal != "tmux" || got.Target != "new-window" || got.Workstream != "sess" {
		t.Fatalf("launch opts = %+v, want tmux new-window for sess", got)
	}
}

func TestRunStartFreshPersistsOperatorMode(t *testing.T) {
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%42")
	backend := useFakeTmuxBackend(t)
	dir := t.TempDir()
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--roles", "cto", "--operator-mode", "separate_terminal", "--go"}, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("launches = %d", len(backend.launches))
	}
	stored, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Operator == nil || stored.Operator.InteractionMode != team.OperatorInteractionSeparateTerminal {
		t.Fatalf("stored operator = %+v", stored.Operator)
	}
}

func TestRunStartExistingOperatorModeValidationDoesNotMutateOrForward(t *testing.T) {
	op := team.DefaultOperator()
	op.InteractionMode = team.OperatorInteractionSeparateTerminal
	dir := seedTeam(t, team.Team{
		Operator: &op, Orchestrated: true, Lead: "cto",
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "sess"}},
	})
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--operator-mode", "separate_terminal"}, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "--operator-mode") {
		t.Fatalf("validation-only mode forwarded to up preview:\n%s", out)
	}
	stored, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Operator.InteractionMode != team.OperatorInteractionSeparateTerminal {
		t.Fatalf("stored mode changed: %+v", stored.Operator)
	}
	_, _, err = captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--operator-mode", "noc"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "never rewrites") {
		t.Fatalf("mismatch error = %v", err)
	}
	stored, readErr := team.Read(dir)
	if readErr != nil || stored.Operator.InteractionMode != team.OperatorInteractionSeparateTerminal {
		t.Fatalf("mismatch mutated profile: operator=%+v err=%v", stored.Operator, readErr)
	}
}

func TestRunStartRejectsUnavailableSelfOperatorMode(t *testing.T) {
	err := runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto", "--operator-mode", "self_operator"}, "test")
	if err == nil || !strings.Contains(err.Error(), "#391") {
		t.Fatalf("self_operator error = %v", err)
	}
}

func TestRunStartGoAcceptsGenericLeadRole(t *testing.T) {
	t.Setenv("TMUX", "/tmp/fake-tmux,1,0")
	t.Setenv("TMUX_PANE", "%42")
	backend := &fakeBackend{}
	prev, hadPrev := teamLaunchBackends["tmux"]
	teamLaunchBackends["tmux"] = backend
	t.Cleanup(func() {
		if hadPrev {
			teamLaunchBackends["tmux"] = prev
			return
		}
		delete(teamLaunchBackends, "tmux")
	})
	dir := t.TempDir()

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--roles", "lead", "--lead", "lead", "--go"}, "test")
	})
	if err != nil {
		t.Fatalf("run start --roles lead --lead lead --go: %v", err)
	}
	if len(backend.launches) != 1 {
		t.Fatalf("expected one launch, got %+v", backend.launches)
	}
	got := backend.launches[0]
	if got.Workstream != "sess" || got.Terminal != "tmux" || got.Target != "new-window" {
		t.Fatalf("launch opts = %+v, want tmux new-window for sess", got)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if !cfg.Orchestrated || cfg.Lead != "lead" {
		t.Fatalf("team orchestration = %v/%q, want true/lead", cfg.Orchestrated, cfg.Lead)
	}
	if len(cfg.Members) != 1 || cfg.Members[0].Role != "lead" || cfg.Members[0].Binary != "codex" {
		t.Fatalf("team members = %+v, want one built-in lead using codex", cfg.Members)
	}
}

func TestRunStartProfileAliasAndExplicitLead(t *testing.T) {
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "-P", "release", "--roles", "cto,qa", "--lead", "qa"}, "test")
	})
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if !strings.Contains(out, "profile: release") {
		t.Fatalf("-P alias should set profile release:\n%s", out)
	}
	if !strings.Contains(out, "lead:    qa") || !strings.Contains(out, "--lead qa") {
		t.Fatalf("explicit --lead qa should be honored, not defaulted to cto:\n%s", out)
	}
}

func TestRunStartPreviewSurfacesPlannerLeadModeForFreshRoster(t *testing.T) {
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto,fullstack", "--lead-mode", "planner"}, "test")
	})
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	for _, want := range []string{
		"lead-mode: planner",
		"--lead-mode planner",
		"# lead-mode: planner",
		"# implementation-allowed: false",
		"# mutable-actor: fullstack",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("run start planner preview missing %q:\n%s", want, out)
		}
	}
}

func TestRunStartLeadModeExistingProfileRequiresExplicitProfileMutation(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto,qa", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}
	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--lead-mode", "planner"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "team lead set") {
		t.Fatalf("expected existing-profile lead-mode error, got %v", err)
	}
}

func TestRunStartExistingProfileWithRolesInfersLead(t *testing.T) {
	// Regression: --roles + an EXISTING profile whose lead is not cto must not
	// force cto. new team is skipped, so the run infers the profile's lead.
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto,qa", "--orchestrated", "--lead", "qa"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--roles", "cto,qa", "--goal", "do x"}, "test")
	})
	if err != nil {
		t.Fatalf("preview error: %v", err)
	}
	if strings.Contains(out, "lead:    cto") {
		t.Fatalf("existing qa-led team must not display cto lead:\n%s", out)
	}
	if !strings.Contains(out, "inferred from profile") {
		t.Fatalf("existing team should infer lead:\n%s", out)
	}
	if !strings.Contains(out, "already exists") {
		t.Fatalf("should note the existing profile / skipped roster:\n%s", out)
	}
}

func TestRunStartExistingDefaultProfilePinnedElsewhereFailsFast(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "workspace-cli", "--roles", "cto,fullstack", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "dev", "--roles", "cto", "--lead", "cto", "--binary", "cto=codex", "--go"}, "test")
	})
	if err == nil {
		t.Fatal("run start should fail fast when existing profile has zero members for requested session")
	}
	for _, want := range []string{
		`profile "default"`,
		`pinned to workstream workspace-cli, not "dev"`,
		`no team members would run`,
		`--roles "cto" would be ignored`,
		`amq-squad run start --project ` + shellQuote(dir) + ` --session workspace-cli`,
		`amq-squad run start --project ` + shellQuote(dir) + ` --profile <name> --session dev --roles cto`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(out, "spawning team") || strings.Contains(out, "orchestrated run") {
		t.Fatalf("failure should happen before preview/spawn output, got:\n%s", out)
	}
}

func TestRunStartExistingProfileMixedPinsProceed(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, team.Team{
		Project:      dir,
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "dev"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "workspace-cli"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "dev", "--roles", "cto,qa"}, "test")
	})
	if err != nil {
		t.Fatalf("mixed pins with one runnable member should proceed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "profile default already exists; --roles ignored") {
		t.Fatalf("existing profile should still explain --roles is ignored:\n%s", out)
	}
	if !strings.Contains(out, "Preview OK") {
		t.Fatalf("preview should validate the runnable member:\n%s", out)
	}
}

func TestRunStartExistingProfileEffortOverrideIsLaunchOnly(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Project:      "",
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{{
			Role: "cto", Binary: "codex", Handle: "cto", Session: "sess",
			CodexArgs: []string{"--profile", "fast", "-c", "model_reasoning_effort=low"},
		}},
	})
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--effort", "cto=xhigh"}, "test")
	})
	if err != nil {
		t.Fatalf("preview error: %v\n%s", err, out)
	}
	if !strings.Contains(out, "model_reasoning_effort=xhigh") || strings.Contains(out, "model_reasoning_effort=low") {
		t.Fatalf("preview did not replace stored effort args:\n%s", out)
	}
	stored, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatal(err)
	}
	if got := memberEffort(stored.Members[0]); got != "low" {
		t.Fatalf("stored profile effort changed to %q", got)
	}
}

func TestRunStartGoGoalWaitsForLeadReadiness(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto,qa", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}

	var upCalls [][]string
	var goalCalls [][]string
	var sleeps []time.Duration
	readyChecks := 0
	stubRunStartGoalDelivery(t,
		func(args []string, version string) error {
			upCalls = append(upCalls, append([]string{}, args...))
			return nil
		},
		func(args []string, version string) error {
			goalCalls = append(goalCalls, append([]string{}, args...))
			return nil
		},
		func(project, profile, session, role string) (runStartLeadReadiness, error) {
			readyChecks++
			if readyChecks < 3 {
				return runStartLeadReadiness{Detail: "still starting"}, nil
			}
			return runStartLeadReadiness{Ready: true, Detail: "live"}, nil
		},
		func(d time.Duration) { sleeps = append(sleeps, d) },
		time.Now,
	)

	_, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--visibility", "detached", "--goal", "ship it", "--go"}, "test")
	})
	if err != nil {
		t.Fatalf("run start --go returned error: %v", err)
	}
	if len(upCalls) != 1 {
		t.Fatalf("expected one up call, got %v", upCalls)
	}
	if len(goalCalls) != 1 {
		t.Fatalf("expected one goal call, got %v", goalCalls)
	}
	if readyChecks != 3 || len(sleeps) != 2 {
		t.Fatalf("expected two waits before readiness, checks=%d sleeps=%v", readyChecks, sleeps)
	}
	goal := strings.Join(goalCalls[0], " ")
	for _, want := range []string{"start", "--project " + dir, "--profile default", "--session sess", "--role cto", "--goal ship it", "--yes"} {
		if !strings.Contains(goal, want) {
			t.Fatalf("goal args %q missing %q", goal, want)
		}
	}
}

func TestRunStartGoGoalFailurePrintsQuotedRetryCommand(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}

	now := time.Unix(100, 0)
	goal := "ship 'quotes'\nand $stuff"
	goalCalled := false
	stubRunStartGoalDelivery(t,
		func(args []string, version string) error { return nil },
		func(args []string, version string) error {
			goalCalled = true
			return nil
		},
		func(project, profile, session, role string) (runStartLeadReadiness, error) {
			return runStartLeadReadiness{Detail: "pane not live yet"}, nil
		},
		func(d time.Duration) { now = now.Add(d) },
		func() time.Time { return now },
	)
	prevTimeout := runStartLeadReadyTimeout
	runStartLeadReadyTimeout = time.Millisecond
	t.Cleanup(func() { runStartLeadReadyTimeout = prevTimeout })

	_, stderr, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--visibility", "detached", "--goal", goal, "--go"}, "test")
	})
	if err == nil {
		t.Fatal("expected readiness timeout error")
	}
	if goalCalled {
		t.Fatal("goal delivery must not run when readiness never succeeds")
	}
	wantCmd := "amq-squad goal start --project " + shellQuote(dir) +
		" --profile default --session sess --role cto --goal " + shellQuote(goal) + " --yes"
	if !strings.Contains(stderr, wantCmd) {
		t.Fatalf("stderr missing quoted retry command\nwant: %s\nstderr:\n%s", wantCmd, stderr)
	}
}

func TestRunStartGoGoalDeliveryFailureAfterReadyPrintsRetryCommand(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}

	goalErr := errors.New("pane rejected paste")
	stubRunStartGoalDelivery(t,
		func(args []string, version string) error { return nil },
		func(args []string, version string) error { return goalErr },
		func(project, profile, session, role string) (runStartLeadReadiness, error) {
			return runStartLeadReadiness{Ready: true, Detail: "live"}, nil
		},
		func(time.Duration) {},
		time.Now,
	)

	_, stderr, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--visibility", "detached", "--goal", "ship it", "--go"}, "test")
	})
	if !errors.Is(err, goalErr) || !strings.Contains(err.Error(), "goal delivery failed after lead became ready") {
		t.Fatalf("expected wrapped delivery error, got %v", err)
	}
	wantCmd := "amq-squad goal start --project " + shellQuote(dir) +
		" --profile default --session sess --role cto --goal 'ship it' --yes"
	if !strings.Contains(stderr, wantCmd) {
		t.Fatalf("stderr missing retry command\nwant: %s\nstderr:\n%s", wantCmd, stderr)
	}
}

func TestRunStartLeadReadinessTransientErrorKeepsPolling(t *testing.T) {
	dir := t.TempDir()
	now := time.Unix(100, 0)
	calls := 0
	stubRunStartGoalDelivery(t,
		func(args []string, version string) error { return nil },
		func(args []string, version string) error { return nil },
		func(project, profile, session, role string) (runStartLeadReadiness, error) {
			calls++
			if calls == 1 {
				return runStartLeadReadiness{}, errors.New("profile temporarily unreadable")
			}
			return runStartLeadReadiness{Ready: true, Detail: "live"}, nil
		},
		func(d time.Duration) { now = now.Add(d) },
		func() time.Time { return now },
	)

	err := waitForRunStartLeadReady(runStartGoalDeliveryOptions{
		Project: dir,
		Profile: "default",
		Session: "sess",
		Role:    "cto",
	})
	if err != nil {
		t.Fatalf("transient readiness error should be retried, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("readiness calls = %d, want 2", calls)
	}
}

func stubRunStartGoalDelivery(
	t *testing.T,
	up func([]string, string) error,
	goal func([]string, string) error,
	ready func(project, profile, session, role string) (runStartLeadReadiness, error),
	sleep func(time.Duration),
	now func() time.Time,
) {
	t.Helper()
	prevUp := runStartUpWithVersion
	prevGoal := runStartGoalWithVersion
	prevReady := runStartLeadReadyCheck
	prevSleep := runStartLeadReadySleep
	prevNow := runStartLeadReadyNow
	runStartUpWithVersion = up
	runStartGoalWithVersion = goal
	runStartLeadReadyCheck = ready
	runStartLeadReadySleep = sleep
	runStartLeadReadyNow = now
	t.Cleanup(func() {
		runStartUpWithVersion = prevUp
		runStartGoalWithVersion = prevGoal
		runStartLeadReadyCheck = prevReady
		runStartLeadReadySleep = prevSleep
		runStartLeadReadyNow = prevNow
	})
}

func TestStripFlagValue(t *testing.T) {
	got, had := stripFlagValue([]string{"sess", "--project", "p", "--seed-from", "issue:9", "--visibility", "detached"}, "--seed-from")
	if !had {
		t.Fatal("expected had=true when flag present")
	}
	if strings.Join(got, " ") != "sess --project p --visibility detached" {
		t.Fatalf("unexpected strip result: %q", strings.Join(got, " "))
	}
	if _, had := stripFlagValue([]string{"sess", "--project", "p"}, "--seed-from"); had {
		t.Fatal("expected had=false when flag absent")
	}
}

func TestRunStartPreviewSeedFromValidatesRealSpawn(t *testing.T) {
	// With --seed-from, the validation dry-run must strip it (else up --dry-run
	// returns brief-only and skips roster/session validation). Existing team is
	// pinned to sess, so the real validation passes and the seed note appears.
	dir := t.TempDir()
	if _, _, err := captureOutput(t, func() error {
		return runNew([]string{"team", "--project", dir, "--session", "sess", "--roles", "cto,qa", "--orchestrated", "--lead", "cto"})
	}); err != nil {
		t.Fatalf("setup new team: %v", err)
	}
	brief := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(brief, []byte("# brief\nwork on it\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", dir, "-s", "sess", "--seed-from", "file:" + brief}, "test")
	})
	if err != nil {
		t.Fatalf("preview error: %v", err)
	}
	if !strings.Contains(out, "Preview OK") {
		t.Fatalf("expected Preview OK for a valid pinned team:\n%s", out)
	}
	if !strings.Contains(out, "--seed-from brief is written at --go") {
		t.Fatalf("expected seed-from note:\n%s", out)
	}
}

func TestRunStartRejectsBadVisibility(t *testing.T) {
	err := runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto", "--visibility", "bogus"}, "test")
	if err == nil || !strings.Contains(err.Error(), "unsupported visibility") {
		t.Fatalf("expected visibility validation error, got %v", err)
	}
	err = runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto", "--visibility", "plan"}, "test")
	if err == nil || !strings.Contains(err.Error(), "not valid for run start") {
		t.Fatalf("expected plan-rejection error, got %v", err)
	}
}

func TestAppendPassthroughArgs(t *testing.T) {
	got := appendPassthroughArgs([]string{"up"}, "cto=gpt-5", "--enable goals", "")
	want := "up --model cto=gpt-5 --codex-args --enable goals"
	if strings.Join(got, " ") != want {
		t.Fatalf("appendPassthroughArgs = %q, want %q", strings.Join(got, " "), want)
	}
	if joined := strings.Join(appendPassthroughArgs([]string{"up"}, "", "", ""), " "); joined != "up" {
		t.Fatalf("empty passthrough should be a no-op, got %q", joined)
	}
}

func TestCompletionCoversGlobalRunSubcommands(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out, _, err := captureOutput(t, func() error { return runCompletion([]string{shell}) })
		if err != nil {
			t.Fatalf("%s completion error: %v", shell, err)
		}
		for _, verb := range []string{"global", "run"} {
			if !strings.Contains(out, verb) {
				t.Errorf("%s completion missing top command %q", shell, verb)
			}
		}
		// Each verb's sole subcommand is "start"; assert the script wires it.
		if strings.Count(out, "start") == 0 {
			t.Errorf("%s completion does not surface the start subcommand:\n%s", shell, out)
		}
	}
}

func TestGlobalAndRunRegistered(t *testing.T) {
	for _, name := range []string{"global", "run"} {
		if _, ok := lookupCommand(name, "test"); !ok {
			t.Fatalf("command %q not registered", name)
		}
		if commandSummary(name) == "" {
			t.Fatalf("command %q missing catalog summary", name)
		}
	}
}
