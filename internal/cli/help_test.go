package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/team"
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
		{"team", "propose", "--help"},
		{"team", "apply", "--help"},
		{"team", "show", "--help"},
		{"team", "check", "--help"},
		{"team", "draft-rules", "--help"},
		{"operator", "--help"},
		{"gate", "--help"},
		{"gate", "raise", "--help"},
		{"gate", "close", "--help"},
		{"context", "--help"},
		{"guard", "--help"},
		{"evidence", "ingest", "--help"},
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
		{name: "team propose --help", args: []string{"team", "propose", "--help"}, want: "amq-squad team propose"},
		{name: "status --help", args: []string{"status", "--help"}, want: "amq-squad status"},
		{name: "roles --help", args: []string{"roles", "--help"}, want: "amq-squad roles"},
		{name: "team show --help", args: []string{"team", "show", "--help"}, want: "amq-squad team show"},
		{name: "version --help", args: []string{"version", "--help"}, want: "amq-squad version"},
		{name: "doctor --help", args: []string{"doctor", "--help"}, want: "amq-squad doctor"},
		{name: "operator --help", args: []string{"operator", "--help"}, want: "amq-squad operator"},
		{name: "gate --help", args: []string{"gate", "--help"}, want: "amq-squad gate"},
		{name: "gate raise --help", args: []string{"gate", "raise", "--help"}, want: "amq-squad gate raise"},
		{name: "gate close --help", args: []string{"gate", "close", "--help"}, want: "amq-squad gate close"},
		{name: "context --help", args: []string{"context", "--help"}, want: "amq-squad context --me HANDLE"},
		{name: "guard --help", args: []string{"guard", "--help"}, want: "amq-squad guard --me HANDLE --stdin"},
		{name: "evidence ingest --help", args: []string{"evidence", "ingest", "--help"}, want: "amq-squad evidence ingest --me HANDLE"},
		{name: "evidence --help", args: []string{"evidence", "--help"}, want: "amq-squad evidence ingest --me HANDLE"},
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
		for _, removed := range []string{"directive", "poll", "watch", "send", "self-approve"} {
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
	want := `unknown 'operator' subcommand: "bogus". Try 'answer' or 'status'.`
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
		{name: "team propose", args: []string{"team", "propose", "--help"}, want: []string{"--project DIR", "amq-squad team propose --project ~/Code/app"}},
		{name: "team apply", args: []string{"team", "apply", "--help"}, want: []string{"--project DIR", "amq-squad team apply --digest"}},
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

// commandTextSubcommands is what each verb dispatches, for the guards below.
func commandTextSubcommands() map[string][]string {
	return map[string][]string{
		"team":     append(append([]string(nil), teamSubcommandHints...), "resume"),
		"task":     append(append([]string(nil), taskSubcommands...), "ls", "complete"),
		"operator": operatorSubcommands,
		"amq":      {"env", "route"},
	}
}

// assertTextNamesRegisteredCommands fails on any `amq-squad VERB [SUB]` in text
// whose verb is not registered, or whose SUB is not dispatched for verbs that
// take subcommands (2a-2: stale names in kept text).
func assertTextNamesRegisteredCommands(t *testing.T, label, text string) {
	t.Helper()
	registered := map[string]bool{"version": true}
	for _, name := range commandNames("v-test") {
		registered[name] = true
	}
	subs := commandTextSubcommands()
	found := 0
	for _, m := range regexp.MustCompile(`amq-squad ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?`).FindAllStringSubmatch(text, -1) {
		verb, sub := m[1], m[2]
		if !registered[verb] {
			continue // prose ("amq-squad injects ..."), checked by the backtick guard
		}
		found++
		allowed, hasSubs := subs[verb]
		if !hasSubs || sub == "" {
			continue
		}
		ok := false
		for _, a := range allowed {
			ok = ok || a == sub
		}
		if !ok {
			t.Errorf("%s names %q, but %s dispatches %v", label, "amq-squad "+verb+" "+sub, verb, allowed)
		}
	}
	for _, m := range regexp.MustCompile("`amq-squad ([a-z][a-z-]*)").FindAllStringSubmatch(text, -1) {
		if !registered[m[1]] {
			t.Errorf("%s names %q, which is not a registered command", label, "amq-squad "+m[1])
		}
	}
	_ = found
}

func TestGeneratedTeamRulesNameRegisteredCommands(t *testing.T) {
	for _, mode := range []string{"", team.OperatorInteractionLeadPane, team.OperatorInteractionSeparateTerminal, team.OperatorInteractionNOC} {
		opMode := mode
		body, err := renderTeamRules(team.Team{
			Project: t.TempDir(), OperatorInteraction: opMode, Orchestrated: true, Lead: "cto",
			Members: []team.Member{
				{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
				{Role: "dev-1", Binary: "claude", Handle: "dev-1", Session: "s", ActorMode: team.ActorModeImplementation},
				{Role: "dev-2", Binary: "codex", Handle: "dev-2", Session: "s", ActorMode: team.ActorModeImplementation},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		assertTextNamesRegisteredCommands(t, "team rules (mode "+mode+")", body)
		for _, removed := range []string{"tmux", "DIRECTIVE:", "local_input", "wake nudge"} {
			if strings.Contains(body, removed) {
				t.Errorf("team rules (mode %s) still mention removed %q", mode, removed)
			}
		}
	}
}

func TestCommandSummariesAndUsageNameDispatchedSubcommands(t *testing.T) {
	subs := commandTextSubcommands()
	for _, verb := range []string{"team", "task"} {
		summary := commandSummary(verb)
		open, close := strings.Index(summary, "("), strings.LastIndex(summary, ")")
		if open < 0 || close < open {
			t.Fatalf("%s summary lists no subcommands: %q", verb, summary)
		}
		for _, name := range strings.FieldsFunc(summary[open+1:close], func(r rune) bool { return r == ',' || r == ' ' || r == '/' }) {
			ok := false
			for _, a := range subs[verb] {
				ok = ok || a == name
			}
			if !ok {
				t.Errorf("%s summary names %q, which %s does not dispatch", verb, name, verb)
			}
		}
	}
	_, teamUsage, _ := captureOutput(t, func() error { printTeamUsage(); return nil })
	assertTextNamesRegisteredCommands(t, "team usage", teamUsage)
	if strings.Contains(teamUsage, "team operator self") {
		t.Errorf("team usage still names the removed team operator self subcommand:\n%s", teamUsage)
	}
	_, taskUsage, _ := captureOutput(t, func() error { return runTask(nil) })
	assertTextNamesRegisteredCommands(t, "task usage", taskUsage)
	_, dispatchUsage, _ := captureOutput(t, func() error { return runDispatch([]string{"--help"}) })
	assertTextNamesRegisteredCommands(t, "dispatch usage", dispatchUsage)
	if strings.Contains(dispatchUsage, "amq-squad amq send") {
		t.Errorf("dispatch usage still names the removed amq send wrapper")
	}
}
