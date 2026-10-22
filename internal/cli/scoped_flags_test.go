package cli

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestAMQPassthroughScopedShortAliasesSplit(t *testing.T) {
	project, profile, session, me, projectSet, passthrough, _, err := splitAMQPassthroughArgsWithOptions("send", []string{
		"-p", "/tmp/project",
		"-P", "review",
		"-s", "sess",
		"--me", "cto",
		"--subject", "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if project != "/tmp/project" || profile != "review" || session != "sess" || me != "cto" || !projectSet {
		t.Fatalf("scope = project:%q profile:%q session:%q me:%q projectSet:%v", project, profile, session, me, projectSet)
	}
	if got := strings.Join(passthrough, " "); got != "--subject hello" {
		t.Fatalf("passthrough = %q", got)
	}
}

func TestFocusAcceptsScopedShortFlagAliases(t *testing.T) {
	dir := t.TempDir()
	_, _, err := captureOutput(t, func() error {
		return runFocus([]string{"-p", dir, "-P", "review", "-s", "sess"})
	})
	if err == nil {
		t.Fatal("expected missing team error")
	}
	if strings.Contains(err.Error(), "flag provided but not defined") {
		t.Fatalf("short aliases were not accepted: %v", err)
	}
	if !strings.Contains(err.Error(), `profile "review"`) {
		t.Fatalf("expected focus to parse aliases and resolve profile, got %v", err)
	}
}

func TestDoctorExplicitSessionPrintsIgnoreNotice(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error {
		return runDoctor([]string{"-s", "sess", "--help"}, "test")
	})
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("expected help, got %v", err)
	}
	if strings.Contains(stderr, "ignoring --session") {
		t.Fatalf("help should not print runtime ignore notice:\n%s", stderr)
	}

	_, stderr, err = captureOutput(t, func() error {
		return runDoctor([]string{"-s", "sess", "--all-profiles", "--profile", "default"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "--all-profiles cannot be combined with --profile") {
		t.Fatalf("expected all-profiles/profile usage error, got %v", err)
	}
	want := "ignoring --session: doctor checks project/profile health, not one session"
	if !strings.Contains(stderr, want) {
		t.Fatalf("stderr missing ignore notice %q:\n%s", want, stderr)
	}
}
