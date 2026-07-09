package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
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

func TestRunStartExternalLeadUnsupported(t *testing.T) {
	err := runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--external-lead"}, "test")
	if err == nil || !strings.Contains(err.Error(), "external-lead mode is not yet supported") {
		t.Fatalf("expected external-lead unsupported error, got %v", err)
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

func TestRunStartDefaultsToDetachedInPreview(t *testing.T) {
	// A fresh project with --roles: preview should describe a detached (hidden)
	// spawn by default and note the deferred spawn validation.
	out, _, err := captureOutput(t, func() error {
		return runRunStart([]string{"-p", t.TempDir(), "-s", "sess", "--roles", "cto"}, "test")
	})
	if err != nil {
		t.Fatalf("preview returned error: %v", err)
	}
	if !strings.Contains(out, "--visibility detached") {
		t.Fatalf("default visibility should be detached:\n%s", out)
	}
	if !strings.Contains(out, "hidden") {
		t.Fatalf("preview should explain hidden spawn:\n%s", out)
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
