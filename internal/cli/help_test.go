package cli

import (
	"strings"
	"testing"
)

// Each --help path must exit 0. flag.ContinueOnError returns flag.ErrHelp
// from fs.Parse when --help is present; that error must be swallowed at the
// dispatch boundary so users do not see a misleading "error: flag: help
// requested" message under a help invocation.
func TestHelpExitsZeroAcrossCommands(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "launch --help", args: []string{"launch", "--help"}, want: "amq-squad launch"},
		{name: "restore --help", args: []string{"restore", "--help"}, want: "amq-squad restore"},
		{name: "list --help", args: []string{"list", "--help"}, want: "amq-squad list"},
		{name: "team init --help", args: []string{"team", "init", "--help"}, want: "amq-squad team init"},
		{name: "team show --help", args: []string{"team", "show", "--help"}, want: "amq-squad team show"},
		{name: "team launch --help", args: []string{"team", "launch", "--help"}, want: "--force-duplicate"},
		{name: "up --help", args: []string{"up", "--help"}, want: "amq-squad up"},
		{name: "down --help", args: []string{"down", "--help"}, want: "amq-squad down"},
		{name: "status --help", args: []string{"status", "--help"}, want: "amq-squad status"},
		{name: "history --help", args: []string{"history", "--help"}, want: "amq-squad history"},
		{name: "resume --help", args: []string{"resume", "--help"}, want: "amq-squad resume"},
		{name: "fork --help", args: []string{"fork", "--help"}, want: "amq-squad fork"},
		{name: "version --help", args: []string{"version", "--help"}, want: "amq-squad version"},
		{name: "completion --help", args: []string{"completion", "--help"}, want: "amq-squad completion"},
	}
	for _, tc := range cases {
		_, stderr, err := captureOutput(t, func() error { return Run(tc.args, "test") })
		if err != nil {
			t.Errorf("%s: Run returned %v (must be nil for --help)", tc.name, err)
		}
		if !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: stderr missing %q in:\n%s", tc.name, tc.want, stderr)
		}
	}
}
