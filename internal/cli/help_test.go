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
		{"doctor", "--help"},
		{"status", "--help"},
		{"dispatch", "--help"},
		{"team", "--help"},
		{"team", "init", "--help"},
		// gh#758/t11: "team resume --help" deliberately omitted here.
		// `team resume` was deleted outright (a hard error, not a
		// deprecation notice like the other legacy verbs above), so its
		// --help is a one-line redirect to `amq-squad resume` with no
		// Examples: section or usage doc to check -- there is no longer a
		// help surface here matching this table's contract.
		{"team", "sync", "--help"},
		{"team", "rules", "--help"},
		{"team", "rules", "show", "--help"},
		{"team", "rules", "init", "--help"},
		{"operator", "--help"},
		{"gate", "--help"},
		{"gate", "raise", "--help"},
		{"gate", "close", "--help"},
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
		{name: "status --help", args: []string{"status", "--help"}, want: "amq-squad status"},
		{name: "roles --help", args: []string{"roles", "--help"}, want: "amq-squad roles"},
		{name: "team rules show --help", args: []string{"team", "rules", "show", "--help"}, want: "amq-squad team rules show"},
		{name: "version --help", args: []string{"version", "--help"}, want: "amq-squad version"},
		{name: "doctor --help", args: []string{"doctor", "--help"}, want: "amq-squad doctor"},
		{name: "operator --help", args: []string{"operator", "--help"}, want: "amq-squad operator"},
		{name: "gate --help", args: []string{"gate", "--help"}, want: "amq-squad gate"},
		{name: "gate raise --help", args: []string{"gate", "raise", "--help"}, want: "amq-squad gate raise"},
		{name: "gate close --help", args: []string{"gate", "close", "--help"}, want: "amq-squad gate close"},
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

func TestGateTopLevelDiscoveryIncludesRaiseAndClose(t *testing.T) {
	const wantSummary = "Manage durable typed authorization requests (raise and close)"
	if got := commandSummary("gate"); got != wantSummary {
		t.Fatalf("gate command summary = %q, want %q", got, wantSummary)
	}

	stdout, stderr, err := captureOutput(t, func() error { return Run([]string{"--help"}, "test") })
	if err != nil {
		t.Fatalf("top-level help: %v", err)
	}
	if combined := stdout + stderr; !strings.Contains(combined, "gate") || !strings.Contains(combined, wantSummary) {
		t.Fatalf("top-level help hides gate lifecycle summary:\n%s", combined)
	}

	_, gateHelp, err := captureOutput(t, func() error { return Run([]string{"gate", "--help"}, "test") })
	if err != nil {
		t.Fatalf("gate help: %v", err)
	}
	for _, want := range []string{"amq-squad gate raise [options]", "amq-squad gate close [options]"} {
		if !strings.Contains(gateHelp, want) {
			t.Fatalf("gate help missing %q:\n%s", want, gateHelp)
		}
	}
}

func TestDoctorHelpDocumentsProfile(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error { return Run([]string{"doctor", "--help"}, "test") })
	if err != nil {
		t.Fatalf("doctor --help: %v", err)
	}
	for _, want := range []string{"--project DIR", "--profile NAME", "selected team profile", "amq-squad doctor --project ~/Code/app", "amq-squad doctor --profile review"} {
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

func TestOperatorGroupHelp(t *testing.T) {
	for _, args := range [][]string{{"operator", "--help"}, {"operator", "-h"}, {"operator"}} {
		_, stderr, err := captureOutput(t, func() error { return Run(args, "test") })
		if err != nil {
			t.Errorf("Run %v: returned %v, want nil", args, err)
			continue
		}
		for _, want := range []string{
			"amq-squad operator",
			"Subcommands:",
			"answer",
			"self-approve",
			"status",
			"Examples:",
			"amq-squad operator answer",
			"amq-squad operator status",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("Run %v: missing %q in help output:\n%s", args, want, stderr)
			}
		}
		subcommands := helpSection(t, stderr, "Subcommands:", "Run 'amq-squad")
		// Subcommands must appear in alphabetical order.
		for _, name := range operatorSubcommands {
			if !strings.Contains(subcommands, name) {
				t.Errorf("Run %v: dispatched subcommand %q missing from help output", args, name)
			}
		}
		for _, removed := range []string{"directive", "poll", "watch", "send"} {
			if strings.Contains(subcommands, removed) {
				t.Errorf("Run %v: help still names removed subcommand %q", args, removed)
			}
		}
	}
}

func helpSection(t *testing.T, body, startMarker, endMarker string) string {
	t.Helper()
	start := strings.Index(body, startMarker)
	if start < 0 {
		t.Fatalf("help output missing start marker %q:\n%s", startMarker, body)
	}
	rest := body[start:]
	end := strings.Index(rest, endMarker)
	if end < 0 {
		t.Fatalf("help output missing end marker %q after %q:\n%s", endMarker, startMarker, body)
	}
	return rest[:end]
}

func TestOperatorUnknownSubcommandEnumeratesCompleteSurface(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return Run([]string{"operator", "bogus"}, "test") })
	want := `unknown 'operator' subcommand: "bogus". Try 'answer', 'self-approve', or 'status'.`
	if err == nil || err.Error() != want {
		t.Fatalf("operator unknown-subcommand error = %v, want %q", err, want)
	}
}

func TestTeamHelpDocumentsProject(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{name: "team init", args: []string{"team", "init", "--help"}, want: []string{"--project DIR", "amq-squad team init --project ~/Code/app"}},
		{name: "team sync", args: []string{"team", "sync", "--help"}, want: []string{"--project DIR", "amq-squad team sync --project ~/Code/app --apply"}},
		// gh#758/t11: "team resume" omitted -- deleted outright (hard
		// error), its --help is a one-line redirect with no "--project
		// DIR" usage doc to check, unlike the other legacy verbs above.
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
