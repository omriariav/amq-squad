package cli

import (
	"errors"
	"flag"
	"testing"
)

func TestScopedShortFlagAliasesRegistered(t *testing.T) {
	project := t.TempDir()
	scoped := []string{"-p", project, "-P", "review", "-s", "sess", "-h"}
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
		{"doctor", func(args []string) error { return runDoctor(args, "test") }, []string{"-p", project, "-h"}},
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
