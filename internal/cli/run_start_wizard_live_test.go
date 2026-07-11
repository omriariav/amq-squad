package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/team"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

type wizardTestTTY struct {
	reader *bytes.Reader
	writes *bytes.Buffer
}

func (t wizardTestTTY) Read(p []byte) (int, error)  { return t.reader.Read(p) }
func (t wizardTestTTY) Write(p []byte) (int, error) { return t.writes.Write(p) }
func (wizardTestTTY) Close() error                  { return nil }

func withWizardExecutionSeams(t *testing.T) (*[][]string, *[][]string) {
	t.Helper()
	oldProject := runStartWizardProjectExecute
	oldGlobal := runStartWizardGlobalExecute
	oldConfirm := runStartWizardConfirm
	var projectCalls, globalCalls [][]string
	runStartWizardProjectExecute = func(args []string, version string) error {
		projectCalls = append(projectCalls, append([]string(nil), args...))
		return nil
	}
	runStartWizardGlobalExecute = func(args []string) error {
		globalCalls = append(globalCalls, append([]string(nil), args...))
		return nil
	}
	runStartWizardConfirm = promptRunStartWizardLaunch
	t.Cleanup(func() {
		runStartWizardProjectExecute = oldProject
		runStartWizardGlobalExecute = oldGlobal
		runStartWizardConfirm = oldConfirm
	})
	return &projectCalls, &globalCalls
}

func validWizardProjectSpec(t *testing.T) runwizard.Spec {
	t.Helper()
	return runwizard.Spec{
		Scope: "project", Project: t.TempDir(), Profile: "default", Session: "issue-393-slice6",
		Roles: "cto", Binary: "cto=codex", Lead: "cto", LeadMode: "builder",
		Visibility: visibilityDetached, OperatorMode: "separate_terminal", LauncherPane: launcherPaneKeep,
	}
}

func TestFinishWizardProjectPreviewNoIsReadOnly(t *testing.T) {
	projectCalls, _ := withWizardExecutionSeams(t)
	var prompt strings.Builder
	if err := finishRunStartWizard(validWizardProjectSpec(t), "test", strings.NewReader("\n"), &prompt); err != nil {
		t.Fatal(err)
	}
	if len(*projectCalls) != 1 || hasWizardArg((*projectCalls)[0], "--go") {
		t.Fatalf("calls = %v", *projectCalls)
	}
	if !strings.Contains(prompt.String(), "Launch now? [y/N]") {
		t.Fatalf("missing default-No confirmation: %q", prompt.String())
	}
}

func TestFinishWizardProjectYesAddsOnlyGoAndRechecks(t *testing.T) {
	projectCalls, _ := withWizardExecutionSeams(t)
	runStartWizardConfirm = func(io.Reader, io.Writer) (bool, error) { return true, nil }
	spec := validWizardProjectSpec(t)
	stdout, _, err := captureOutput(t, func() error {
		return finishRunStartWizard(spec, "test", strings.NewReader(""), io.Discard)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyGoDelta(t, *projectCalls)
	assertPrintedCanonicalPair(t, stdout, "run", "start", (*projectCalls)[0], (*projectCalls)[1])
	for _, prompt := range []string{"Launch now?", "Scope", "Choose [1]"} {
		if strings.Contains(stdout, prompt) {
			t.Fatalf("prompt leaked to stdout: %q in %s", prompt, stdout)
		}
	}
}

func TestFinishWizardPreviewFailureNeverConfirmsOrLaunches(t *testing.T) {
	projectCalls, _ := withWizardExecutionSeams(t)
	runStartWizardProjectExecute = func(args []string, version string) error {
		*projectCalls = append(*projectCalls, append([]string(nil), args...))
		return errors.New("preview failed")
	}
	confirmed := false
	runStartWizardConfirm = func(io.Reader, io.Writer) (bool, error) { confirmed = true; return true, nil }
	err := finishRunStartWizard(validWizardProjectSpec(t), "test", strings.NewReader("y\n"), io.Discard)
	if err == nil || confirmed || len(*projectCalls) != 1 {
		t.Fatalf("err=%v confirmed=%t calls=%v", err, confirmed, *projectCalls)
	}
}

func TestFinishWizardLiveRechecksAndSurfacesNewCollision(t *testing.T) {
	projectCalls, _ := withWizardExecutionSeams(t)
	runStartWizardConfirm = func(io.Reader, io.Writer) (bool, error) { return true, nil }
	runStartWizardProjectExecute = func(args []string, version string) error {
		*projectCalls = append(*projectCalls, append([]string(nil), args...))
		if len(*projectCalls) == 2 {
			return errors.New("workstream already exists")
		}
		return nil
	}
	err := finishRunStartWizard(validWizardProjectSpec(t), "test", strings.NewReader(""), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second canonical check error = %v calls=%v", err, *projectCalls)
	}
	assertOnlyGoDelta(t, *projectCalls)
}

func TestFinishWizardGlobalNoAndYesDelegateExactCanonicalArgs(t *testing.T) {
	_, globalCalls := withWizardExecutionSeams(t)
	spec := runwizard.Spec{Scope: "global", GlobalRoot: "/neutral", GlobalAgent: "codex", GlobalModel: "gpt", GlobalEffort: "high", GlobalCodexArgs: "--search", GlobalWindow: "noc"}
	if err := finishRunStartWizard(spec, "test", strings.NewReader("\n"), io.Discard); err != nil {
		t.Fatal(err)
	}
	if len(*globalCalls) != 1 {
		t.Fatalf("No calls = %v", *globalCalls)
	}
	*globalCalls = nil
	runStartWizardConfirm = func(io.Reader, io.Writer) (bool, error) { return true, nil }
	stdout, _, err := captureOutput(t, func() error {
		return finishRunStartWizard(spec, "test", strings.NewReader(""), io.Discard)
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyGoDelta(t, *globalCalls)
	assertPrintedCanonicalPair(t, stdout, "global", "start", (*globalCalls)[0], (*globalCalls)[1])
	joined := strings.Join((*globalCalls)[0], " ")
	for _, forbidden := range []string{"--roles", "--profile", "--visibility", "--launcher-pane"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("global argv leaked %s: %v", forbidden, (*globalCalls)[0])
		}
	}
}

func TestPromptWizardScopeAndConfirmationDefaults(t *testing.T) {
	var out strings.Builder
	scope, cancelled, err := promptRunStartWizardScope(strings.NewReader("\n"), &out, "")
	if err != nil || cancelled || scope != "project" {
		t.Fatalf("scope=%q cancelled=%t err=%v", scope, cancelled, err)
	}
	yes, err := promptRunStartWizardLaunch(strings.NewReader("\n"), &out)
	if err != nil || yes {
		t.Fatalf("default confirmation yes=%t err=%v", yes, err)
	}
	yes, err = promptRunStartWizardLaunch(strings.NewReader("YES\n"), &out)
	if err != nil || !yes {
		t.Fatalf("affirmative confirmation yes=%t err=%v", yes, err)
	}
}

func TestWizardCustomFlagsRemainInCompletionCatalog(t *testing.T) {
	joined := strings.Join(completionCommonFlags, " ")
	for _, flag := range []string{"--interactive", "--wizard-ui", "--numbered", "--accessible", "--scope"} {
		if !strings.Contains(joined, flag) {
			t.Fatalf("completion missing %s", flag)
		}
	}
}

func TestCollectGlobalWizardDefaultsToExistingGlobalNeutralRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	input := strings.NewReader("\n\n\n\n\n\n")
	spec, err := collectGlobalWizard(input, io.Discard, runwizard.Spec{Scope: "global"})
	if err != nil {
		t.Fatal(err)
	}
	if spec.GlobalRoot != home+"/Code" || spec.GlobalAgent != "claude" || spec.GlobalWindow != "global-orch" {
		t.Fatalf("global defaults = %+v", spec)
	}
}

func TestNumberedGlobalWizardDelegatesPreviewThenDefaultNo(t *testing.T) {
	_, globalCalls := withWizardExecutionSeams(t)
	oldInput, oldOutput := runStartWizardInput, runStartWizardOutput
	t.Cleanup(func() { runStartWizardInput, runStartWizardOutput = oldInput, oldOutput })
	// root, agent, model, effort, Claude args, window, then default-No confirmation.
	runStartWizardInput = strings.NewReader(strings.Repeat("\n", 7))
	var prompts strings.Builder
	runStartWizardOutput = &prompts
	if err := runNumberedRunStartWizard([]string{"--scope", "global", "--root", t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	if len(*globalCalls) != 1 || hasWizardArg((*globalCalls)[0], "--go") {
		t.Fatalf("global calls = %v", *globalCalls)
	}
	for _, want := range []string{"NOC contract", "Launch now? [y/N]"} {
		if !strings.Contains(prompts.String(), want) {
			t.Fatalf("prompts missing %q: %s", want, prompts.String())
		}
	}
}

func TestNumberedProjectWizardScriptedYesPreservesReaderAndAddsOnlyGo(t *testing.T) {
	projectCalls, _ := withWizardExecutionSeams(t)
	oldInput, oldOutput := runStartWizardInput, runStartWizardOutput
	t.Cleanup(func() { runStartWizardInput, runStartWizardOutput = oldInput, oldOutput })
	answers := []string{
		"",             // scope: project
		"", "", "", "", // project, profile, session, roles
		"", "", "", // cto binary/model/effort
		"", "", "", // senior-dev binary/model/effort
		"", "", "", // qa binary/model/effort
		"", "", "", "", "", "", "", "", "", // lead through seed
		"YES", // post-preview live confirmation
	}
	runStartWizardInput = strings.NewReader(strings.Join(answers, "\n") + "\n")
	var prompts strings.Builder
	runStartWizardOutput = &prompts
	project := t.TempDir()
	stdout, _, err := captureOutput(t, func() error {
		return runNumberedRunStartWizard([]string{"--project", project, "--profile", "default", "--session", "issue-393-e2e"}, "test")
	})
	if err != nil {
		t.Fatal(err)
	}
	assertOnlyGoDelta(t, *projectCalls)
	assertPrintedCanonicalPair(t, stdout, "run", "start", (*projectCalls)[0], (*projectCalls)[1])
	for _, want := range []string{"Scope", "Answers are previewed first", "Launch now? [y/N]"} {
		if !strings.Contains(prompts.String(), want) {
			t.Fatalf("prompt channel missing %q:\n%s", want, prompts.String())
		}
	}
	for _, forbidden := range []string{"Scope", "Launch now?", "Project directory"} {
		if strings.Contains(stdout, forbidden) {
			t.Fatalf("prompt %q leaked to stdout:\n%s", forbidden, stdout)
		}
	}
}

func TestNumberedProjectWizardRejectsExplicitNotificationMismatchBeforePreview(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Operator: func() *team.OperatorConfig { op := team.DefaultOperator(); return &op }(),
		Members:  []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	})
	projectCalls, _ := withWizardExecutionSeams(t)
	oldInput, oldOutput := runStartWizardInput, runStartWizardOutput
	t.Cleanup(func() { runStartWizardInput, runStartWizardOutput = oldInput, oldOutput })
	// Scope; project/profile/session; keep member; topology/layout; launcher; goal/seed.
	runStartWizardInput = strings.NewReader(strings.Repeat("\n", 10))
	var prompts strings.Builder
	runStartWizardOutput = &prompts
	err := runNumberedRunStartWizard([]string{"--project", dir, "--profile", "default", "--session", "s", "--operator-notifications"}, "test")
	if err == nil || !strings.Contains(err.Error(), "does not match existing profile") {
		t.Fatalf("mismatch = %v\nprompts:\n%s", err, prompts.String())
	}
	if !strings.Contains(prompts.String(), "Preflight blocked [existing_profile_operator_notifications]") {
		t.Fatalf("structured mismatch missing:\n%s", prompts.String())
	}
	if len(*projectCalls) != 0 {
		t.Fatalf("mismatch reached preview/live execution: %+v", *projectCalls)
	}
}

func TestNumberedWizardScopeCancellationDoesNothing(t *testing.T) {
	projectCalls, globalCalls := withWizardExecutionSeams(t)
	oldInput, oldOutput := runStartWizardInput, runStartWizardOutput
	t.Cleanup(func() { runStartWizardInput, runStartWizardOutput = oldInput, oldOutput })
	runStartWizardInput = strings.NewReader("q\n")
	runStartWizardOutput = io.Discard
	if err := runNumberedRunStartWizard(nil, "test"); err != nil {
		t.Fatal(err)
	}
	if len(*projectCalls) != 0 || len(*globalCalls) != 0 {
		t.Fatalf("cancel calls project=%v global=%v", *projectCalls, *globalCalls)
	}
}

func TestBubbleWizardReturnsFromProgramThenConfirmsOnSameTTY(t *testing.T) {
	projectCalls, _ := withWizardExecutionSeams(t)
	oldOpen := runStartWizardOpenTTY
	oldProgram := runStartWizardBubbleProgram
	t.Cleanup(func() { runStartWizardOpenTTY, runStartWizardBubbleProgram = oldOpen, oldProgram })
	tty := wizardTestTTY{reader: bytes.NewReader([]byte("yes\n")), writes: &bytes.Buffer{}}
	runStartWizardOpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	spec := validWizardProjectSpec(t)
	runStartWizardBubbleProgram = func(in io.Reader, out io.Writer, opts runwizard.NumberedOptions) (runwizard.BubbleResult, error) {
		return runwizard.BubbleResult{Spec: spec}, nil
	}
	if err := runBubbleRunStartWizard([]string{"--scope", "project", "--project", spec.Project}, "test"); err != nil {
		t.Fatal(err)
	}
	assertOnlyGoDelta(t, *projectCalls)
	if !strings.Contains(tty.writes.String(), "Launch now? [y/N]") {
		t.Fatalf("confirmation not written to same tty: %q", tty.writes.String())
	}
}

func TestBubbleWizardHandsRawTTYToProgramNotAWrapper(t *testing.T) {
	// Bubble Tea enables raw mode only when its input asserts to term.File
	// (the tty *os.File itself). Passing any wrapping reader leaves the
	// terminal cooked: keys are line-buffered and enter arrives as ctrl+j,
	// freezing the wizard on its first phase (#421).
	withWizardExecutionSeams(t)
	oldOpen := runStartWizardOpenTTY
	oldProgram := runStartWizardBubbleProgram
	t.Cleanup(func() { runStartWizardOpenTTY, runStartWizardBubbleProgram = oldOpen, oldProgram })
	tty := wizardTestTTY{reader: bytes.NewReader([]byte("\n")), writes: &bytes.Buffer{}}
	runStartWizardOpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	var gotIn io.Reader
	var gotOut io.Writer
	runStartWizardBubbleProgram = func(in io.Reader, out io.Writer, opts runwizard.NumberedOptions) (runwizard.BubbleResult, error) {
		gotIn, gotOut = in, out
		return runwizard.BubbleResult{Cancelled: true}, nil
	}
	if err := runBubbleRunStartWizard([]string{"--scope", "project", "--project", t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	if in, ok := gotIn.(wizardTestTTY); !ok || in != tty {
		t.Fatalf("bubble program input = %T, want the exact tty from runStartWizardOpenTTY", gotIn)
	}
	if out, ok := gotOut.(wizardTestTTY); !ok || out != tty {
		t.Fatalf("bubble program output = %T, want the exact tty from runStartWizardOpenTTY", gotOut)
	}
}

func assertOnlyGoDelta(t *testing.T, calls [][]string) {
	t.Helper()
	if len(calls) != 2 {
		t.Fatalf("calls = %v", calls)
	}
	want := append(append([]string(nil), calls[0]...), "--go")
	if strings.Join(calls[1], "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("live argv delta = %v -> %v, want only --go", calls[0], calls[1])
	}
}

func hasWizardArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}

func assertPrintedCanonicalPair(t *testing.T, stdout, verb, subcommand string, preview, live []string) {
	t.Helper()
	previewCommand := shellCommand("amq-squad", append([]string{verb, subcommand}, preview...)...)
	liveCommand := shellCommand("amq-squad", append([]string{verb, subcommand}, live...)...)
	if !strings.Contains(stdout, previewCommand) || !strings.Contains(stdout, liveCommand) {
		t.Fatalf("stdout did not match executed argv\npreview=%s\nlive=%s\nstdout:\n%s", previewCommand, liveCommand, stdout)
	}
}
