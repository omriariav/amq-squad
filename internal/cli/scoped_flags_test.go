package cli

import (
	"errors"
	"flag"
	"strings"
	"testing"
)

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

func TestScopedShortFlagAliasesRegistered(t *testing.T) {
	project := t.TempDir()
	scoped := []string{"-p", project, "-P", "review", "-s", "sess", "-h"}
	scopedNoSession := []string{"-p", project, "-P", "review", "-h"}
	tests := []struct {
		name string
		run  func([]string) error
		args []string
	}{
		{"status", func(args []string) error { return runStatusWithVersion(args, "test") }, scoped},
		{"dispatch", runDispatch, scoped},
		{"operator answer", runOperator, append([]string{"answer"}, scoped...)},
		{"operator status", runOperator, append([]string{"status"}, scoped...)},
		{"task add", runTask, append([]string{"add"}, scoped...)},
		{"task list", runTask, append([]string{"list"}, scoped...)},
		{"task show", runTask, append([]string{"show", "t1"}, scoped...)},
		{"task claim", runTask, append([]string{"claim", "t1"}, scoped...)},
		{"task done", runTask, append([]string{"done", "t1"}, scoped...)},
		{"task fail", runTask, append([]string{"fail", "t1"}, scoped...)},
		{"task block", runTask, append([]string{"block", "t1"}, scoped...)},
		{"task reset", runTask, append([]string{"reset", "t1"}, scoped...)},
		{"amq env", runAMQ, append([]string{"env"}, scoped...)},
		{"amq route", runAMQ, append([]string{"route"}, scoped...)},
		{"doctor", func(args []string) error { return runDoctor(args, "test") }, scoped},
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
