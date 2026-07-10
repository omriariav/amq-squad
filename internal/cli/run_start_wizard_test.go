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
	runStartWizardInput = strings.NewReader(strings.Repeat("\n", 9))
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
	if !strings.Contains(prompts.String(), "Preview only: this flow cannot launch agents") {
		t.Fatalf("prompt output missing preview-only banner:\n%s", prompts.String())
	}
	if team.ExistsProfile(project, "review") {
		t.Fatal("preview-only wizard created a team profile")
	}
}
