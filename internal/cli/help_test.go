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
		{"notify", "--help"},
		{"notifications", "--help"},
		{"notifications", "doctor", "--help"},
		{"notifications", "probe", "--help"},
		{"notifications", "events", "--help"},
		{"notifications", "history", "--help"},
		{"dispatch", "--help"},
		{"up", "--help"},
		{"resume", "--help"},
		{"fork", "--help"},
		{"review-worktree", "--help"},
		{"review-worktree", "create", "--help"},
		{"review-worktree", "exec", "--help"},
		{"review-worktree", "shell", "--help"},
		{"review-worktree", "remove", "--help"},
		{"tmux-harness", "--help"},
		{"tmux-harness", "exec", "--help"},
		{"tmux-harness", "shell", "--help"},
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
		{"goal", "--help"},
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
		{name: "up --help", args: []string{"up", "--help"}, want: "amq-squad up"},
		{name: "stop --help", args: []string{"stop", "--help"}, want: "amq-squad stop"},
		{name: "status --help", args: []string{"status", "--help"}, want: "amq-squad status"},
		{name: "console --help", args: []string{"console", "--help"}, want: "amq-squad console"},
		{name: "notify --help", args: []string{"notify", "--help"}, want: "amq-squad notify"},
		{name: "notifications --help", args: []string{"notifications", "--help"}, want: "amq-squad notifications"},
		{name: "notifications doctor --help", args: []string{"notifications", "doctor", "--help"}, want: "amq-squad notifications doctor"},
		{name: "notifications probe --help", args: []string{"notifications", "probe", "--help"}, want: "amq-squad notifications probe"},
		{name: "notifications events --help", args: []string{"notifications", "events", "--help"}, want: "amq-squad notifications events"},
		{name: "notifications history --help", args: []string{"notifications", "history", "--help"}, want: "amq-squad notifications history"},
		{name: "history --help", args: []string{"history", "--help"}, want: "amq-squad history"},
		{name: "brief --help", args: []string{"brief", "--help"}, want: "amq-squad brief"},
		{name: "brief seed --help", args: []string{"brief", "seed", "--help"}, want: "amq-squad brief seed"},
		{name: "threads --help", args: []string{"threads", "--help"}, want: "amq-squad threads"},
		{name: "thread --help", args: []string{"thread", "--help"}, want: "amq-squad thread"},
		{name: "resume --help", args: []string{"resume", "--help"}, want: "amq-squad resume"},
		{name: "fork --help", args: []string{"fork", "--help"}, want: "amq-squad fork"},
		{name: "review-worktree --help", args: []string{"review-worktree", "--help"}, want: "amq-squad review-worktree"},
		{name: "tmux-harness --help", args: []string{"tmux-harness", "--help"}, want: "amq-squad tmux-harness"},
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
		{name: "goal --help", args: []string{"goal", "--help"}, want: "amq-squad goal"},
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

func TestGoalGroupHelp(t *testing.T) {
	for _, args := range [][]string{{"goal", "--help"}, {"goal", "-h"}} {
		_, stderr, err := captureOutput(t, func() error { return Run(args, "test") })
		if err != nil {
			t.Errorf("Run %v: returned %v, want nil", args, err)
			continue
		}
		for _, want := range []string{
			"amq-squad goal",
			"Subcommands:",
			"apply",
			"deliver",
			"draft",
			"start",
			"Examples:",
			"amq-squad goal apply",
			"amq-squad goal draft",
			"amq-squad goal deliver",
			"amq-squad goal start",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("Run %v: missing %q in help output:\n%s", args, want, stderr)
			}
		}
		subcommands := helpSection(t, stderr, "Subcommands:", "Run 'amq-squad")
		// Subcommands must appear in alphabetical order.
		applyPos := strings.Index(subcommands, "apply")
		deliverPos := strings.Index(subcommands, "deliver")
		draftPos := strings.Index(subcommands, "draft")
		startPos := strings.Index(subcommands, "start")
		if applyPos < 0 || deliverPos < 0 || draftPos < 0 || startPos < 0 {
			t.Errorf("Run %v: all goal subcommands must appear in help output", args)
		} else if !(applyPos < deliverPos && deliverPos < draftPos && draftPos < startPos) {
			t.Errorf("Run %v: subcommands must be alphabetical (apply, deliver, draft, start)", args)
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
			"directive",
			"poll",
			"status",
			"watch",
			"Examples:",
			"amq-squad operator answer",
			"amq-squad operator directive",
			"amq-squad operator status",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("Run %v: missing %q in help output:\n%s", args, want, stderr)
			}
		}
		subcommands := helpSection(t, stderr, "Subcommands:", "Run 'amq-squad")
		// Subcommands must appear in alphabetical order.
		answerPos := strings.Index(subcommands, "answer")
		directivePos := strings.Index(subcommands, "directive")
		pollPos := strings.Index(subcommands, "poll")
		statusPos := strings.Index(subcommands, "status")
		watchPos := strings.Index(subcommands, "watch")
		if answerPos < 0 || directivePos < 0 || pollPos < 0 || statusPos < 0 || watchPos < 0 {
			t.Errorf("Run %v: all operator subcommands must appear in help output", args)
		} else if !(answerPos < directivePos && directivePos < pollPos && pollPos < statusPos && statusPos < watchPos) {
			t.Errorf("Run %v: subcommands must be alphabetical (answer < directive < poll < status < watch)", args)
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

func TestGoalUnknownSubcommandMentionsHelp(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return Run([]string{"goal", "bogus"}, "test") })
	if err == nil {
		t.Fatal("goal bogus: want error, got nil")
	}
	if !strings.Contains(err.Error(), "--help") {
		t.Errorf("goal unknown subcommand error should mention --help, got: %v", err)
	}
}

func TestOperatorUnknownSubcommandMentionsHelp(t *testing.T) {
	_, _, err := captureOutput(t, func() error { return Run([]string{"operator", "bogus"}, "test") })
	if err == nil {
		t.Fatal("operator bogus: want error, got nil")
	}
	if !strings.Contains(err.Error(), "--help") {
		t.Errorf("operator unknown subcommand error should mention --help, got: %v", err)
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
