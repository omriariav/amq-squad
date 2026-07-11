package cli

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func stubRunStartWizard(t *testing.T, stdinTTY, stderrTTY, ci bool) *[][]string {
	t.Helper()
	prevStdin := runStartWizardStdinIsTerminal
	prevStderr := runStartWizardStderrIsTerminal
	prevCI := runStartWizardInCI
	prevRunner := runStartWizardRunner
	runStartWizardStdinIsTerminal = func() bool { return stdinTTY }
	runStartWizardStderrIsTerminal = func() bool { return stderrTTY }
	runStartWizardInCI = func() bool { return ci }
	var calls [][]string
	runStartWizardRunner = func(args []string, _ string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}
	t.Cleanup(func() {
		runStartWizardStdinIsTerminal = prevStdin
		runStartWizardStderrIsTerminal = prevStderr
		runStartWizardInCI = prevCI
		runStartWizardRunner = prevRunner
	})
	return &calls
}

func TestRunStartNoArgsTTYStartsWizard(t *testing.T) {
	calls := stubRunStartWizard(t, true, true, false)
	if err := runRunStart(nil, "test"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 || len((*calls)[0]) != 0 {
		t.Fatalf("wizard calls = %#v", *calls)
	}
}

func TestRunStartNoArgsNonTTYPreservesUsageError(t *testing.T) {
	calls := stubRunStartWizard(t, false, true, false)
	err := runRunStart(nil, "test")
	if err == nil || !strings.Contains(err.Error(), "requires --project") {
		t.Fatalf("expected canonical usage error, got %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("non-TTY started wizard: %#v", *calls)
	}
}

func TestRunStartExplicitInteractiveRequiresEligibleTTY(t *testing.T) {
	for _, tc := range []struct {
		name      string
		stdinTTY  bool
		stderrTTY bool
		ci        bool
	}{
		{name: "stdin pipe", stdinTTY: false, stderrTTY: true},
		{name: "stderr pipe", stdinTTY: true, stderrTTY: false},
		{name: "ci pseudo tty", stdinTTY: true, stderrTTY: true, ci: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := stubRunStartWizard(t, tc.stdinTTY, tc.stderrTTY, tc.ci)
			err := runRunStart([]string{"--interactive"}, "test")
			if err == nil || !strings.Contains(err.Error(), "requires an interactive terminal") {
				t.Fatalf("expected interactive guard, got %v", err)
			}
			if len(*calls) != 0 {
				t.Fatalf("ineligible environment started wizard: %#v", *calls)
			}
		})
	}
}

func TestRunStartInteractiveCombinedWithGoRejected(t *testing.T) {
	calls := stubRunStartWizard(t, true, true, false)
	err := runRunStart([]string{"--interactive", "--go", "--project", "/repo", "--session", "s"}, "test")
	if err == nil || !strings.Contains(err.Error(), "cannot be combined with --go") {
		t.Fatalf("expected interactive/go rejection, got %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("interactive/go reached wizard: %#v", *calls)
	}
}

func TestRunStartInteractiveHelpDoesNotStartWizard(t *testing.T) {
	calls := stubRunStartWizard(t, true, true, false)
	_, stderr, err := captureOutput(t, func() error {
		return runRunStart([]string{"--interactive", "--help"}, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr, "amq-squad run - create one orchestrated run") {
		t.Fatalf("help output missing run usage:\n%s", stderr)
	}
	if len(*calls) != 0 {
		t.Fatalf("help started wizard: %#v", *calls)
	}
}

func TestRunStartInteractivePassesPrefillFlagsToWizard(t *testing.T) {
	calls := stubRunStartWizard(t, true, true, false)
	args := []string{"--project", "/repo", "--session", "s", "--roles", "cto,qa"}
	withInteractive := append([]string{"--interactive"}, args...)
	if err := runRunStart(withInteractive, "test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*calls, [][]string{args}) {
		t.Fatalf("wizard calls = %#v, want %#v", *calls, [][]string{args})
	}
}

func TestRunStartPartialFlagsWithoutInteractiveStayCanonical(t *testing.T) {
	calls := stubRunStartWizard(t, true, true, false)
	err := runRunStart([]string{"--project", t.TempDir()}, "test")
	if err == nil || !strings.Contains(err.Error(), "requires --session") {
		t.Fatalf("expected canonical session error, got %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("partial canonical flags started wizard: %#v", *calls)
	}
}

func TestWizardAliasUsesInteractiveTrigger(t *testing.T) {
	calls := stubRunStartWizard(t, true, true, false)
	args := []string{"--project", "/repo", "--session", "s"}
	if err := runWizardCmd(args, "test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*calls, [][]string{args}) {
		t.Fatalf("wizard alias calls = %#v", *calls)
	}
}

func TestInteractiveFalseSuppressesZeroArgTTYWizard(t *testing.T) {
	calls := stubRunStartWizard(t, true, true, false)
	err := runRunStart([]string{"--interactive=false"}, "test")
	if err == nil || !strings.Contains(err.Error(), "requires --project") {
		t.Fatalf("expected canonical usage error, got %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("interactive=false started wizard: %#v", *calls)
	}
}

func TestNumberedWizardRunsCanonicalPreviewWithoutMutation(t *testing.T) {
	project := t.TempDir()
	prevInput := runStartWizardInput
	prevOutput := runStartWizardOutput
	runStartWizardInput = strings.NewReader(strings.Repeat("\n", 24))
	var prompts bytes.Buffer
	runStartWizardOutput = &prompts
	t.Cleanup(func() {
		runStartWizardInput = prevInput
		runStartWizardOutput = prevOutput
	})

	stdout, _, err := captureOutput(t, func() error {
		return runNumberedRunStartWizard([]string{
			"--project", project,
			"--profile", "review",
			"--session", "issue-393",
		}, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"Equivalent flag command (preview only)",
		"run start --project " + project,
		"--profile review --session issue-393",
		"--roles cto,senior-dev,qa",
		"PREVIEW",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("preview output missing %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(prompts.String(), "Answers are previewed first") {
		t.Fatalf("prompt output missing preview-only banner:\n%s", prompts.String())
	}
	if team.ExistsProfile(project, "review") {
		t.Fatal("preview-only wizard created a team profile")
	}
}

func TestAdaptiveRunStartWizardSelectsUIAndStripsAdapterFlags(t *testing.T) {
	prevTerm := runStartWizardTerm
	prevAccessible := runStartWizardAccessible
	prevNumbered := runStartNumberedAdapter
	prevBubble := runStartBubbleAdapter
	t.Cleanup(func() {
		runStartWizardTerm = prevTerm
		runStartWizardAccessible = prevAccessible
		runStartNumberedAdapter = prevNumbered
		runStartBubbleAdapter = prevBubble
	})

	var used string
	var gotArgs []string
	runStartNumberedAdapter = func(args []string, _ string) error {
		used, gotArgs = "numbered", append([]string(nil), args...)
		return nil
	}
	runStartBubbleAdapter = func(args []string, _ string) error {
		used, gotArgs = "tui", append([]string(nil), args...)
		return nil
	}

	for _, tc := range []struct {
		name       string
		term       string
		accessible bool
		args       []string
		wantUI     string
	}{
		{name: "default TUI", term: "xterm-256color", args: []string{"--project", "/repo"}, wantUI: "tui"},
		{name: "dumb terminal", term: "dumb", args: []string{"--project", "/repo"}, wantUI: "numbered"},
		{name: "accessibility environment", term: "xterm", accessible: true, args: []string{"--project", "/repo"}, wantUI: "numbered"},
		{name: "explicit numbered", term: "xterm", args: []string{"--wizard-ui", "numbered", "--project", "/repo"}, wantUI: "numbered"},
		{name: "numbered alias", term: "xterm", args: []string{"--numbered", "--project", "/repo"}, wantUI: "numbered"},
		{name: "explicit TUI beats dumb fallback", term: "dumb", accessible: true, args: []string{"--wizard-ui=tui", "--project", "/repo"}, wantUI: "tui"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			used, gotArgs = "", nil
			runStartWizardTerm = func() string { return tc.term }
			runStartWizardAccessible = func() bool { return tc.accessible }
			if err := runAdaptiveRunStartWizard(tc.args, "test"); err != nil {
				t.Fatal(err)
			}
			if used != tc.wantUI {
				t.Fatalf("adapter = %q, want %q", used, tc.wantUI)
			}
			if !reflect.DeepEqual(gotArgs, []string{"--project", "/repo"}) {
				t.Fatalf("adapter args = %#v", gotArgs)
			}
		})
	}
}

func TestAdaptiveRunStartWizardRejectsUnknownUI(t *testing.T) {
	err := runAdaptiveRunStartWizard([]string{"--wizard-ui", "graphical"}, "test")
	if err == nil || !strings.Contains(err.Error(), "unsupported --wizard-ui") {
		t.Fatalf("error = %v", err)
	}
}
