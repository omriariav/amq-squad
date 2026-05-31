package cli

import (
	"strings"
	"testing"
)

// TestHelpSurfacesIncludeExamples is the Step 11F coverage guard: every
// help surface (root + each subcommand + each nested help path) must
// contain an "Examples:" block with at least one literal "amq-squad ..."
// line so users get an executable hint without reading prose.
func TestHelpSurfacesIncludeExamples(t *testing.T) {
	cases := [][]string{
		{"--help"},
		{"help"},
		{"version", "--help"},
		{"completion", "--help"},
		{"doctor", "--help"},
		{"history", "--help"},
		{"status", "--help"},
		{"console", "--help"},
		{"down", "--help"},
		{"up", "--help"},
		{"resume", "--help"},
		{"fork", "--help"},
		{"team", "--help"},
		{"team", "init", "--help"},
		{"team", "resume", "--help"},
		{"team", "sync", "--help"},
		{"team", "profiles", "--help"},
		{"team", "rules", "--help"},
		{"team", "rules", "init", "--help"},
		{"agent", "--help"},
		{"agent", "up", "--help"},
		{"agent", "resume", "--help"},
	}
	for _, args := range cases {
		stdout, stderr, err := captureOutput(t, func() error { return Run(args, "test") })
		if err != nil {
			t.Errorf("Run %v: returned %v, want nil", args, err)
			continue
		}
		combined := stdout + stderr
		if !strings.Contains(combined, "Examples:") {
			t.Errorf("Run %v: missing 'Examples:' section in help output", args)
		}
		// At least one example line should look executable.
		if !strings.Contains(combined, "amq-squad ") {
			t.Errorf("Run %v: examples must reference 'amq-squad ...'", args)
		}
	}
}

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
		{name: "team init --help", args: []string{"team", "init", "--help"}, want: "amq-squad team init"},
		{name: "up --help", args: []string{"up", "--help"}, want: "amq-squad up"},
		{name: "down --help", args: []string{"down", "--help"}, want: "amq-squad down"},
		{name: "status --help", args: []string{"status", "--help"}, want: "amq-squad status"},
		{name: "console --help", args: []string{"console", "--help"}, want: "amq-squad console"},
		{name: "history --help", args: []string{"history", "--help"}, want: "amq-squad history"},
		{name: "resume --help", args: []string{"resume", "--help"}, want: "amq-squad resume"},
		{name: "fork --help", args: []string{"fork", "--help"}, want: "amq-squad fork"},
		{name: "version --help", args: []string{"version", "--help"}, want: "amq-squad version"},
		{name: "completion --help", args: []string{"completion", "--help"}, want: "amq-squad completion"},
		{name: "doctor --help", args: []string{"doctor", "--help"}, want: "amq-squad doctor"},
		{name: "agent --help", args: []string{"agent", "--help"}, want: "amq-squad agent"},
		{name: "agent up --help", args: []string{"agent", "up", "--help"}, want: "amq-squad agent up"},
		{name: "agent resume --help", args: []string{"agent", "resume", "--help"}, want: "amq-squad agent resume"},
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
