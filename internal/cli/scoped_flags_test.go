package cli

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

func TestScopedShortFlagAliasesRegistered(t *testing.T) {
	project := t.TempDir()
	scoped := []string{"-p", project, "-P", "review", "-s", "sess", "-h"}
	scopedNoSession := []string{"-p", project, "-P", "review", "-h"}
	tests := []struct {
		name string
		run  func([]string) error
		args []string
	}{
		{"focus", runFocus, scoped},
		{"open", runFocus, scoped},
		{"status", func(args []string) error { return runStatusWithVersion(args, "test") }, scoped},
		{"dispatch", runDispatch, scoped},
		{"send", runSend, scoped},
		{"down", runDown, scoped},
		{"resume", runResume, scoped},
		{"agent up", runAgent, append([]string{"up", "codex"}, scoped...)},
		{"agent resume", runAgent, append([]string{"resume", "cto"}, scoped...)},
		{"operator answer", runOperator, append([]string{"answer"}, scoped...)},
		{"operator directive", runOperator, append([]string{"directive"}, scoped...)},
		{"operator status", runOperator, append([]string{"status"}, scoped...)},
		{"operator poll", runOperator, append([]string{"poll"}, scoped...)},
		{"operator watch", runOperator, append([]string{"watch"}, scoped...)},
		{"task add", runTask, append([]string{"add"}, scoped...)},
		{"task list", runTask, append([]string{"list"}, scoped...)},
		{"task show", runTask, append([]string{"show", "t1"}, scoped...)},
		{"task claim", runTask, append([]string{"claim", "t1"}, scoped...)},
		{"task done", runTask, append([]string{"done", "t1"}, scoped...)},
		{"task fail", runTask, append([]string{"fail", "t1"}, scoped...)},
		{"task block", runTask, append([]string{"block", "t1"}, scoped...)},
		{"task reset", runTask, append([]string{"reset", "t1"}, scoped...)},
		{"amq env", runAMQ, append([]string{"env"}, scoped...)},
		{"amq ops", runAMQ, append([]string{"ops"}, scoped...)},
		{"amq route", runAMQ, append([]string{"route"}, scoped...)},
		{"amq who", runAMQ, append([]string{"who"}, scoped...)},
		{"amq presence", runAMQ, append([]string{"presence"}, scoped...)},
		{"amq dlq list", runAMQ, append([]string{"dlq", "list"}, scoped...)},
		{"amq dlq read", runAMQ, append([]string{"dlq", "read"}, scoped...)},
		{"amq dlq retry", runAMQ, append([]string{"dlq", "retry"}, scoped...)},
		{"amq dlq retry-all", runAMQ, append([]string{"dlq", "retry-all"}, scoped...)},
		{"amq dlq purge", runAMQ, append([]string{"dlq", "purge"}, scoped...)},
		{"amq cleanup", runAMQ, append([]string{"cleanup"}, scoped...)},
		{"doctor", func(args []string) error { return runDoctor(args, "test") }, scoped},
		{"lead register", runLead, append([]string{"register"}, scoped...)},
		{"doctor no-session", func(args []string) error { return runDoctor(args, "test") }, scopedNoSession},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := captureOutput(t, func() error {
				return tc.run(tc.args)
			})
			if !errors.Is(err, flag.ErrHelp) {
				t.Fatalf("expected help after parsing scoped short aliases, got %T: %v", err, err)
			}
		})
	}
}

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
