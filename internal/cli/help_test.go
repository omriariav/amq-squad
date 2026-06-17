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
		{"roles", "--help"},
		{"version", "--help"},
		{"completion", "--help"},
		{"doctor", "--help"},
		{"history", "--help"},
		{"brief", "--help"},
		{"brief", "seed", "--help"},
		{"threads", "--help"},
		{"thread", "--help"},
		{"status", "--help"},
		{"console", "--help"},
		{"up", "--help"},
		{"resume", "--help"},
		{"fork", "--help"},
		{"new", "--help"},
		{"new", "team", "--help"},
		{"new", "profile", "--help"},
		{"new", "session", "--help"},
		{"team", "--help"},
		{"team", "init", "--help"},
		{"team", "resume", "--help"},
		{"team", "sync", "--help"},
		{"team", "profiles", "--help"},
		{"team", "rm", "--help"},
		{"team", "delete", "--help"},
		{"team", "rules", "--help"},
		{"team", "rules", "show", "--help"},
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
		{name: "stop --help", args: []string{"stop", "--help"}, want: "amq-squad stop"},
		{name: "status --help", args: []string{"status", "--help"}, want: "amq-squad status"},
		{name: "console --help", args: []string{"console", "--help"}, want: "amq-squad console"},
		{name: "history --help", args: []string{"history", "--help"}, want: "amq-squad history"},
		{name: "brief --help", args: []string{"brief", "--help"}, want: "amq-squad brief"},
		{name: "brief seed --help", args: []string{"brief", "seed", "--help"}, want: "amq-squad brief seed"},
		{name: "threads --help", args: []string{"threads", "--help"}, want: "amq-squad threads"},
		{name: "thread --help", args: []string{"thread", "--help"}, want: "amq-squad thread"},
		{name: "resume --help", args: []string{"resume", "--help"}, want: "amq-squad resume"},
		{name: "fork --help", args: []string{"fork", "--help"}, want: "amq-squad fork"},
		{name: "new --help", args: []string{"new", "--help"}, want: "amq-squad new"},
		{name: "new team --help", args: []string{"new", "team", "--help"}, want: "amq-squad new team"},
		{name: "new profile --help", args: []string{"new", "profile", "--help"}, want: "amq-squad new profile"},
		{name: "new session --help", args: []string{"new", "session", "--help"}, want: "amq-squad new session"},
		{name: "roles --help", args: []string{"roles", "--help"}, want: "amq-squad roles"},
		{name: "team rules show --help", args: []string{"team", "rules", "show", "--help"}, want: "amq-squad team rules show"},
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

func TestNewSessionHelpDocumentsSeededBriefs(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error { return Run([]string{"new", "session", "--help"}, "test") })
	if err != nil {
		t.Fatalf("new session --help: %v", err)
	}
	for _, want := range []string{
		"--profile NAME",
		"--seed-from",
		"file:<path>",
		"issue:<n>",
		"gh:owner/repo#<n>",
		"amq-squad new session --profile review issue-98",
		"amq-squad new session issue-98 --seed-from issue:31",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("new session --help should mention %q, got:\n%s", want, stderr)
		}
	}
}

func TestDoctorHelpDocumentsProfile(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error { return Run([]string{"doctor", "--help"}, "test") })
	if err != nil {
		t.Fatalf("doctor --help: %v", err)
	}
	for _, want := range []string{"--project DIR", "--profile NAME", "--all-profiles", "selected profile", "pointer-sync drift", "amq-squad doctor --project ~/Code/app", "amq-squad doctor --profile review"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("doctor --help should mention %q, got:\n%s", want, stderr)
		}
	}
}

func TestStatusHelpDocumentsProject(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error { return Run([]string{"status", "--help"}, "test") })
	if err != nil {
		t.Fatalf("status --help: %v", err)
	}
	for _, want := range []string{"--project DIR", "amq-squad status --project ~/Code/app"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("status --help should mention %q, got:\n%s", want, stderr)
		}
	}
}

func TestConsoleHelpDocumentsProject(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error { return Run([]string{"console", "--help"}, "test") })
	if err != nil {
		t.Fatalf("console --help: %v", err)
	}
	for _, want := range []string{"--project DIR", "--filter EXPR", "amq-squad console --project ~/Code/app --once"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("console --help should mention %q, got:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "amq-noc") || strings.Contains(stderr, "NOC") {
		t.Fatalf("console --help should not mention NOC, got:\n%s", stderr)
	}
}

func TestAgentHelpDocumentsProject(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "agent up", args: []string{"agent", "up", "--help"}, want: []string{"--project DIR", "amq-squad agent up codex --project ~/Code/app"}},
		{name: "agent resume", args: []string{"agent", "resume", "--help"}, want: []string{"--project", "amq-squad agent resume cto --project ~/Code/app"}},
	}
	for _, tc := range cases {
		_, stderr, err := captureOutput(t, func() error { return Run(tc.args, "test") })
		if err != nil {
			t.Fatalf("%s --help: %v", tc.name, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(stderr, want) {
				t.Fatalf("%s --help should mention %q, got:\n%s", tc.name, want, stderr)
			}
		}
	}
}

func TestLifecycleHelpDocumentsProject(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "up", args: []string{"up", "--help"}, want: []string{"--project DIR", "amq-squad up --project ~/Code/app"}},
		{name: "stop", args: []string{"stop", "--help"}, want: []string{"--project DIR", "amq-squad stop --project ~/Code/app"}},
		{name: "resume", args: []string{"resume", "--help"}, want: []string{"--project DIR", "amq-squad resume --project ~/Code/app"}},
		{name: "fork", args: []string{"fork", "--help"}, want: []string{"--project DIR", "amq-squad fork --project ~/Code/app"}},
		{name: "rm", args: []string{"rm", "--help"}, want: []string{"--project DIR", "amq-squad rm issue-96 --project ~/Code/app"}},
		{name: "archive", args: []string{"archive", "--help"}, want: []string{"--project DIR", "amq-squad archive issue-96 --project ~/Code/app"}},
	}
	for _, tc := range cases {
		_, stderr, err := captureOutput(t, func() error { return Run(tc.args, "test") })
		if err != nil {
			t.Fatalf("%s --help: %v", tc.name, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(stderr, want) {
				t.Fatalf("%s --help should mention %q, got:\n%s", tc.name, want, stderr)
			}
		}
	}
}

func TestTeamHelpDocumentsProject(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "team init", args: []string{"team", "init", "--help"}, want: []string{"--project DIR", "amq-squad team init --project ~/Code/app"}},
		{name: "team profiles", args: []string{"team", "profiles", "--help"}, want: []string{"--project DIR", "amq-squad team profiles --project ~/Code/app"}},
		{name: "team rm", args: []string{"team", "rm", "--help"}, want: []string{"--project DIR", "amq-squad team delete review --project ~/Code/app --yes"}},
		{name: "team delete", args: []string{"team", "delete", "--help"}, want: []string{"--project DIR", "Deletes the selected team profile config only"}},
		{name: "team sync", args: []string{"team", "sync", "--help"}, want: []string{"--project DIR", "amq-squad team sync --project ~/Code/app --apply"}},
		{name: "team resume", args: []string{"team", "resume", "--help"}, want: []string{"--project DIR", "amq-squad team resume --project ~/Code/app"}},
		{name: "team rules show", args: []string{"team", "rules", "show", "--help"}, want: []string{"--project DIR", "amq-squad team rules show --project ~/Code/app"}},
		{name: "team rules init", args: []string{"team", "rules", "init", "--help"}, want: []string{"--project DIR", "amq-squad team rules init --project ~/Code/app"}},
	}
	for _, tc := range cases {
		_, stderr, err := captureOutput(t, func() error { return Run(tc.args, "test") })
		if err != nil {
			t.Fatalf("%s --help: %v", tc.name, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(stderr, want) {
				t.Fatalf("%s --help should mention %q, got:\n%s", tc.name, want, stderr)
			}
		}
	}
}
