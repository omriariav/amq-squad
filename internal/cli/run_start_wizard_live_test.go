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
	reader io.Reader
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
	oldResume := runStartWizardResumeExecute
	oldResumeConfirm := runStartWizardResumeConfirm
	oldInspect := runStartWizardInspectProject
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
	runStartWizardResumeExecute = runResume
	runStartWizardResumeConfirm = promptRunStartWizardResume
	runStartWizardInspectProject = inspectRunStartWizardProject
	t.Cleanup(func() {
		runStartWizardProjectExecute = oldProject
		runStartWizardGlobalExecute = oldGlobal
		runStartWizardConfirm = oldConfirm
		runStartWizardResumeExecute = oldResume
		runStartWizardResumeConfirm = oldResumeConfirm
		runStartWizardInspectProject = oldInspect
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

func TestFinishWizardNonExecutableExistingRunNeverPreviewsOrConfirms(t *testing.T) {
	for _, state := range []runwizard.RunState{runwizard.RunStateRunning, runwizard.RunStateBlocked} {
		t.Run(string(state), func(t *testing.T) {
			projectCalls, _ := withWizardExecutionSeams(t)
			confirmed, resumeCalls := false, 0
			runStartWizardConfirm = func(io.Reader, io.Writer) (bool, error) { confirmed = true; return true, nil }
			runStartWizardResumeConfirm = func(io.Reader, io.Writer) (bool, error) { confirmed = true; return true, nil }
			runStartWizardResumeExecute = func([]string) error { resumeCalls++; return nil }
			var out strings.Builder
			err := finishRunStartWizard(runwizard.Spec{
				Scope: "project", Project: "/repo", Profile: "release", ProfileBranch: runwizard.ProfileBranchExisting,
				Session: "s", RunState: state, RunExecutable: false,
			}, "test", strings.NewReader("y\n"), &out)
			if err != nil || confirmed || len(*projectCalls) != 0 || resumeCalls != 0 {
				t.Fatalf("err=%v confirmed=%t calls=%v resume=%d", err, confirmed, *projectCalls, resumeCalls)
			}
			if !strings.Contains(out.String(), "read-only") || !strings.Contains(out.String(), "nothing was previewed or launched") {
				t.Fatalf("missing safe nonexec result: %q", out.String())
			}
		})
	}
}

func TestFinishWizardBlockedSuggestedFirstNeverReachesExplicitYes(t *testing.T) {
	projectCalls, _ := withWizardExecutionSeams(t)
	confirmed := false
	runStartWizardConfirm = func(io.Reader, io.Writer) (bool, error) { confirmed = true; return true, nil }
	var out strings.Builder
	err := finishRunStartWizard(runwizard.Spec{
		Scope: "project", Project: "/repo", Profile: "review", ProfileBranch: runwizard.ProfileBranchExisting,
		Session: "issue-444", SessionSource: runwizard.SessionSourceSuggestedFirst, DiscoveryFingerprint: "full-fingerprint",
		Backend: runwizard.BackendRunStart, RunState: runwizard.RunStateBlocked, RunExecutable: false,
	}, "test", strings.NewReader("y\n"), &out)
	if err != nil || confirmed || len(*projectCalls) != 0 {
		t.Fatalf("blocked suggested-first crossed explicit-Yes boundary: err=%v confirmed=%t calls=%v", err, confirmed, *projectCalls)
	}
	if !strings.Contains(out.String(), "nothing was previewed or launched") {
		t.Fatalf("missing blocked deferral: %q", out.String())
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

func resumeWizardFixture(fingerprint string) (runwizard.Spec, runwizard.ProjectContext) {
	members := []runwizard.SessionMemberSummary{{Role: "cto", Binary: "codex", Action: runwizard.MemberActionLive}, {Role: "qa", Binary: "codex", Action: runwizard.MemberActionRestore, SavedLaunchIdentity: "saved", SavedBinary: "codex", SavedModel: "gpt", SavedEffort: "high", SavedNativeArgs: []string{"codex", "resume"}}}
	summary := runwizard.SessionSummary{Name: "s", Source: runwizard.SessionSourceMemberPin, Fingerprint: fingerprint, RecordCount: 1, Members: members, Classification: runwizard.RunClassification{State: runwizard.RunStatePartly, Backend: runwizard.BackendResume, Executable: true, RestoreExisting: true}, Live: 1, Restore: 1}
	spec := runwizard.Spec{Scope: "project", Project: "/repo", Profile: "release", ProfileBranch: runwizard.ProfileBranchExisting, Visibility: "sibling-tabs", LayoutPreset: "one-window-per-agent", OperatorMode: "lead_pane", OperatorNotifications: true, OperatorNotificationsRequested: true, OperatorNotificationsSet: true}
	spec.SelectExistingSession(summary)
	ctx := runwizard.ProjectContext{Project: "/repo", Profiles: []runwizard.ProfileSummary{{Name: "release", MemberCount: 2, Sessions: []runwizard.SessionSummary{summary}}}}
	return spec, ctx
}

func existingRunStartWizardFixture(t *testing.T, fingerprint string) (runwizard.Spec, runwizard.ProjectContext) {
	t.Helper()
	project := t.TempDir()
	if err := team.WriteProfile(project, "release", team.Team{Project: project, Workstream: "s", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}}}); err != nil {
		t.Fatal(err)
	}
	members := []runwizard.SessionMemberSummary{{Role: "cto", Binary: "codex", Action: runwizard.MemberActionFresh}}
	summary := runwizard.SessionSummary{Name: "s", Source: runwizard.SessionSourceSuggestedFirst, Fingerprint: fingerprint, Members: members, Classification: runwizard.RunClassification{State: runwizard.RunStateNotStarted, Backend: runwizard.BackendRunStart, Executable: true}, Fresh: 1}
	spec := runwizard.Spec{Scope: "project", Project: project, Profile: "release", ProfileBranch: runwizard.ProfileBranchExisting, Visibility: visibilityDetached, LayoutPreset: "", LauncherPane: launcherPaneKeep}
	spec.SelectExistingSession(summary)
	ctx := runwizard.ProjectContext{Project: project, Profiles: []runwizard.ProfileSummary{{Name: "release", MemberCount: 1, Sessions: []runwizard.SessionSummary{summary}}}}
	return spec, ctx
}

func TestFinishWizardExistingRunStartFreshnessGates(t *testing.T) {
	t.Run("unchanged twice", func(t *testing.T) {
		projectCalls, _ := withWizardExecutionSeams(t)
		spec, ctx := existingRunStartWizardFixture(t, "A")
		inspections := 0
		runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { inspections++; return ctx, nil }
		runStartWizardConfirm = func(io.Reader, io.Writer) (bool, error) { return true, nil }
		if err := finishRunStartWizard(spec, "test", strings.NewReader(""), io.Discard); err != nil {
			t.Fatal(err)
		}
		if inspections != 2 {
			t.Fatalf("inspections=%d", inspections)
		}
		assertOnlyGoDelta(t, *projectCalls)
	})

	t.Run("delta before preview", func(t *testing.T) {
		projectCalls, _ := withWizardExecutionSeams(t)
		spec, changed := existingRunStartWizardFixture(t, "A")
		changed.Profiles[0].Sessions[0].Fingerprint = "B"
		confirmed := false
		runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { return changed, nil }
		runStartWizardConfirm = func(io.Reader, io.Writer) (bool, error) { confirmed = true; return true, nil }
		err := finishRunStartWizard(spec, "test", strings.NewReader(""), io.Discard)
		var restart *wizardRestartError
		if !errors.As(err, &restart) || confirmed || len(*projectCalls) != 0 {
			t.Fatalf("err=%v confirmed=%t calls=%v", err, confirmed, *projectCalls)
		}
	})

	t.Run("delta after yes", func(t *testing.T) {
		projectCalls, _ := withWizardExecutionSeams(t)
		spec, current := existingRunStartWizardFixture(t, "A")
		_, changed := existingRunStartWizardFixture(t, "B")
		inspections := 0
		runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) {
			inspections++
			if inspections == 1 {
				return current, nil
			}
			return changed, nil
		}
		runStartWizardConfirm = func(io.Reader, io.Writer) (bool, error) { return true, nil }
		err := finishRunStartWizard(spec, "test", strings.NewReader(""), io.Discard)
		var restart *wizardRestartError
		if !errors.As(err, &restart) || len(*projectCalls) != 1 || hasWizardArg((*projectCalls)[0], "--go") {
			t.Fatalf("err=%v calls=%v", err, *projectCalls)
		}
	})

	for _, tc := range []struct{ name, input string }{{name: "no", input: "n\n"}, {name: "eof"}} {
		t.Run(tc.name, func(t *testing.T) {
			projectCalls, _ := withWizardExecutionSeams(t)
			spec, ctx := existingRunStartWizardFixture(t, "A")
			inspections := 0
			runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { inspections++; return ctx, nil }
			runStartWizardConfirm = promptRunStartWizardLaunch
			if err := finishRunStartWizard(spec, "test", strings.NewReader(tc.input), io.Discard); err != nil {
				t.Fatal(err)
			}
			if inspections != 1 || len(*projectCalls) != 1 {
				t.Fatalf("inspections=%d calls=%v", inspections, *projectCalls)
			}
		})
	}
}

func TestFinishWizardResumeFingerprintUnchangedTwicePreviewsAndExecutes(t *testing.T) {
	withWizardExecutionSeams(t)
	spec, ctx := resumeWizardFixture("A")
	inspections, confirms := 0, 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { inspections++; return ctx, nil }
	runStartWizardResumeConfirm = func(io.Reader, io.Writer) (bool, error) { confirms++; return true, nil }
	var calls [][]string
	runStartWizardResumeExecute = func(args []string) error { calls = append(calls, append([]string(nil), args...)); return nil }
	stdout, _, err := captureOutput(t, func() error { return finishRunStartWizard(spec, "test", strings.NewReader(""), io.Discard) })
	if err != nil {
		t.Fatal(err)
	}
	if inspections != 2 || confirms != 1 || len(calls) != 2 {
		t.Fatalf("inspections=%d confirms=%d calls=%v", inspections, confirms, calls)
	}
	assertOnlyExecDelta(t, calls)
	assertPrintedCanonicalPair(t, stdout, "resume", "", calls[0], calls[1])
}

func TestFinishWizardResumeNoOrEOFStopsAfterPreview(t *testing.T) {
	for _, tc := range []struct{ name, input string }{{name: "no", input: "n\n"}, {name: "eof"}} {
		t.Run(tc.name, func(t *testing.T) {
			withWizardExecutionSeams(t)
			spec, ctx := resumeWizardFixture("A")
			inspections := 0
			runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { inspections++; return ctx, nil }
			runStartWizardResumeConfirm = promptRunStartWizardResume
			var calls [][]string
			runStartWizardResumeExecute = func(args []string) error { calls = append(calls, append([]string(nil), args...)); return nil }
			if err := finishRunStartWizard(spec, "test", strings.NewReader(tc.input), io.Discard); err != nil {
				t.Fatal(err)
			}
			if inspections != 1 || len(calls) != 1 || hasWizardArg(calls[0], "--exec") {
				t.Fatalf("inspections=%d calls=%v", inspections, calls)
			}
		})
	}
}

func TestFinishWizardResumePreviewFailureNeverConfirms(t *testing.T) {
	withWizardExecutionSeams(t)
	spec, ctx := resumeWizardFixture("A")
	inspections, confirms := 0, 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { inspections++; return ctx, nil }
	runStartWizardResumeConfirm = func(io.Reader, io.Writer) (bool, error) { confirms++; return true, nil }
	runStartWizardResumeExecute = func([]string) error { return errors.New("preview failed") }
	err := finishRunStartWizard(spec, "test", strings.NewReader("y\n"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "preview failed") || inspections != 1 || confirms != 0 {
		t.Fatalf("err=%v inspections=%d confirms=%d", err, inspections, confirms)
	}
}

func TestFinishWizardResumeFingerprintDeltaBeforePreviewRestarts(t *testing.T) {
	withWizardExecutionSeams(t)
	spec, changed := resumeWizardFixture("A")
	changed.Profiles[0].Sessions[0].Fingerprint = "B"
	inspections, confirms, previews := 0, 0, 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { inspections++; return changed, nil }
	runStartWizardResumeConfirm = func(io.Reader, io.Writer) (bool, error) { confirms++; return true, nil }
	runStartWizardResumeExecute = func([]string) error { previews++; return nil }
	err := finishRunStartWizard(spec, "test", strings.NewReader("y\n"), io.Discard)
	var restart *wizardRestartError
	if !errors.As(err, &restart) || inspections != 1 || confirms != 0 || previews != 0 {
		t.Fatalf("err=%v inspections=%d confirms=%d previews=%d", err, inspections, confirms, previews)
	}
	assertWizardRestartCleared(t, restart.Defaults, spec)
}

func TestFinishWizardResumeFingerprintDeltaAfterYesRestartsWithoutExec(t *testing.T) {
	withWizardExecutionSeams(t)
	spec, current := resumeWizardFixture("A")
	_, changed := resumeWizardFixture("B")
	inspections, confirms := 0, 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) {
		inspections++
		if inspections == 1 {
			return current, nil
		}
		return changed, nil
	}
	runStartWizardResumeConfirm = func(io.Reader, io.Writer) (bool, error) { confirms++; return true, nil }
	var calls [][]string
	runStartWizardResumeExecute = func(args []string) error { calls = append(calls, append([]string(nil), args...)); return nil }
	err := finishRunStartWizard(spec, "test", strings.NewReader("y\n"), io.Discard)
	var restart *wizardRestartError
	if !errors.As(err, &restart) || inspections != 2 || confirms != 1 || len(calls) != 1 || hasWizardArg(calls[0], "--exec") {
		t.Fatalf("err=%v inspections=%d confirms=%d calls=%v", err, inspections, confirms, calls)
	}
	assertWizardRestartCleared(t, restart.Defaults, spec)
}

func TestRefreshWizardExistingSelectionInvalidatesOnGoalEvidenceOnly(t *testing.T) {
	withWizardExecutionSeams(t)
	spec, current := resumeWizardFixture("placeholder")
	currentPlan := runwizard.ResumeGoalPlan{SchemaVersion: 1, Action: "redeliver", Eligible: true, BindingDigest: "sha256:binding", AttemptDigest: "sha256:attempt", ClaimDigest: "sha256:claim-a", TransitionState: "absent"}
	currentFP := runwizard.DiscoveryFingerprint(runwizard.DiscoveryFingerprintInput{Profile: "release", Session: "s", GoalPlan: currentPlan})
	current.Profiles[0].Sessions[0].GoalPlan = currentPlan
	current.Profiles[0].Sessions[0].Fingerprint = currentFP
	spec.SelectExistingSession(current.Profiles[0].Sessions[0])
	spec.RedeliverGoal = true
	changed := current
	changed.Profiles = append([]runwizard.ProfileSummary(nil), current.Profiles...)
	changed.Profiles[0].Sessions = append([]runwizard.SessionSummary(nil), current.Profiles[0].Sessions...)
	changedPlan := currentPlan
	changedPlan.ClaimDigest = "sha256:claim-b"
	changed.Profiles[0].Sessions[0].GoalPlan = changedPlan
	changed.Profiles[0].Sessions[0].Fingerprint = runwizard.DiscoveryFingerprint(runwizard.DiscoveryFingerprintInput{Profile: "release", Session: "s", GoalPlan: changedPlan})
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { return changed, nil }
	err := refreshWizardExistingSelection(spec)
	var restart *wizardRestartError
	if !errors.As(err, &restart) || restart.Defaults.RedeliverGoal || restart.Defaults.ResumeGoalPlan != (runwizard.ResumeGoalPlan{}) {
		t.Fatalf("goal-only evidence delta did not clear stale choice: err=%v defaults=%+v", err, restart)
	}
}

func TestRefreshWizardExistingSelectionFailsClosed(t *testing.T) {
	tests := map[string]func(runwizard.ProjectContext) (runwizard.ProjectContext, error){
		"inspector error": func(runwizard.ProjectContext) (runwizard.ProjectContext, error) {
			return runwizard.ProjectContext{}, errors.New("inspect failed")
		},
		"missing profile": func(ctx runwizard.ProjectContext) (runwizard.ProjectContext, error) {
			ctx.Profiles = nil
			return ctx, nil
		},
		"missing session": func(ctx runwizard.ProjectContext) (runwizard.ProjectContext, error) {
			ctx.Profiles[0].Sessions = nil
			return ctx, nil
		},
		"empty refreshed fingerprint": func(ctx runwizard.ProjectContext) (runwizard.ProjectContext, error) {
			ctx.Profiles[0].Sessions[0].Fingerprint = ""
			return ctx, nil
		},
		"same fp backend mismatch": func(ctx runwizard.ProjectContext) (runwizard.ProjectContext, error) {
			ctx.Profiles[0].Sessions[0].Classification = runwizard.RunClassification{State: runwizard.RunStateRunning}
			return ctx, nil
		},
	}
	for name, result := range tests {
		t.Run(name, func(t *testing.T) {
			withWizardExecutionSeams(t)
			spec, ctx := resumeWizardFixture("A")
			runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { return result(ctx) }
			err := refreshWizardExistingSelection(spec)
			var restart *wizardRestartError
			if !errors.As(err, &restart) {
				t.Fatalf("err=%v", err)
			}
			assertWizardRestartCleared(t, restart.Defaults, spec)
		})
	}
	withWizardExecutionSeams(t)
	spec, _ := resumeWizardFixture("")
	err := refreshWizardExistingSelection(spec)
	var restart *wizardRestartError
	if !errors.As(err, &restart) {
		t.Fatalf("empty reviewed fingerprint err=%v", err)
	}
}

func TestFinishWizardRefreshFailureNeverPreviewsOrConfirms(t *testing.T) {
	tests := []struct {
		name       string
		mutateSpec func(*runwizard.Spec)
		inspect    func(runwizard.ProjectContext) (runwizard.ProjectContext, error)
	}{
		{name: "error", inspect: func(runwizard.ProjectContext) (runwizard.ProjectContext, error) {
			return runwizard.ProjectContext{}, errors.New("inspect failed")
		}},
		{name: "missing profile", inspect: func(ctx runwizard.ProjectContext) (runwizard.ProjectContext, error) {
			ctx.Profiles = nil
			return ctx, nil
		}},
		{name: "missing session", inspect: func(ctx runwizard.ProjectContext) (runwizard.ProjectContext, error) {
			ctx.Profiles[0].Sessions = nil
			return ctx, nil
		}},
		{name: "empty refreshed fingerprint", inspect: func(ctx runwizard.ProjectContext) (runwizard.ProjectContext, error) {
			ctx.Profiles[0].Sessions[0].Fingerprint = ""
			return ctx, nil
		}},
		{name: "empty reviewed fingerprint", mutateSpec: func(spec *runwizard.Spec) { spec.DiscoveryFingerprint = "" }, inspect: func(ctx runwizard.ProjectContext) (runwizard.ProjectContext, error) { return ctx, nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withWizardExecutionSeams(t)
			spec, ctx := resumeWizardFixture("A")
			if tt.mutateSpec != nil {
				tt.mutateSpec(&spec)
			}
			previews, confirms := 0, 0
			runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) { return tt.inspect(ctx) }
			runStartWizardResumeExecute = func([]string) error { previews++; return nil }
			runStartWizardResumeConfirm = func(io.Reader, io.Writer) (bool, error) { confirms++; return true, nil }
			err := finishRunStartWizard(spec, "test", strings.NewReader("y\n"), io.Discard)
			var restart *wizardRestartError
			if !errors.As(err, &restart) || previews != 0 || confirms != 0 {
				t.Fatalf("err=%v previews=%d confirms=%d", err, previews, confirms)
			}
		})
	}
}

func assertWizardRestartCleared(t *testing.T, got, original runwizard.Spec) {
	t.Helper()
	if got.Scope != original.Scope || got.Project != original.Project || got.Profile != original.Profile || got.ProfileBranch != original.ProfileBranch {
		t.Fatalf("restart lost upstream selection: got=%+v original=%+v", got, original)
	}
	if got.Session != "" || got.SessionSource != "" || got.Backend != "" || got.RunState != "" || got.RunExecutable || got.RestoreExisting || got.RecordCount != 0 || got.DiscoveryFingerprint != "" || len(got.ResumeMembers) != 0 || got.Model != "" || got.Effort != "" || got.Visibility != "" || got.LayoutPreset != "" || got.OperatorMode != "" || got.OperatorNotifications || got.OperatorNotificationsRequested != original.OperatorNotificationsRequested || got.OperatorNotificationsSet != original.OperatorNotificationsSet {
		t.Fatalf("restart retained stale answers: %+v", got)
	}
}

func assertOnlyExecDelta(t *testing.T, calls [][]string) {
	t.Helper()
	if len(calls) != 2 || len(calls[1]) != len(calls[0])+1 || calls[1][len(calls[1])-1] != "--exec" {
		t.Fatalf("resume calls=%v", calls)
	}
	for i := range calls[0] {
		if calls[0][i] != calls[1][i] {
			t.Fatalf("resume argv differs before --exec: %v", calls)
		}
	}
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

func TestWizardConfirmationDefaults(t *testing.T) {
	var out strings.Builder
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

func TestNumberedGlobalWizardDelegatesPreviewThenDefaultNo(t *testing.T) {
	_, globalCalls := withWizardExecutionSeams(t)
	oldInput, oldOutput := runStartWizardInput, runStartWizardOutput
	t.Cleanup(func() { runStartWizardInput, runStartWizardOutput = oldInput, oldOutput })
	// Scope, root, agent, model, effort, Claude args, and window. EOF is default-No confirmation.
	runStartWizardInput = strings.NewReader(strings.Repeat("\n", 7))
	var prompts strings.Builder
	runStartWizardOutput = &prompts
	if err := runNumberedRunStartWizard([]string{"--scope", "global", "--root", t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	if len(*globalCalls) != 1 || hasWizardArg((*globalCalls)[0], "--go") {
		t.Fatalf("global calls = %v", *globalCalls)
	}
	for _, want := range []string{"What do you want to run?", "Review", "Preview command:", "Live command:", "NOC contract", "Launch now? [y/N]"} {
		if !strings.Contains(prompts.String(), want) {
			t.Fatalf("prompts missing %q: %s", want, prompts.String())
		}
	}
}

func TestNumberedGlobalWizardWithoutScopeNeverDiscoversProject(t *testing.T) {
	_, globalCalls := withWizardExecutionSeams(t)
	oldInput, oldOutput := runStartWizardInput, runStartWizardOutput
	t.Cleanup(func() { runStartWizardInput, runStartWizardOutput = oldInput, oldOutput })
	inspections := 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) {
		inspections++
		return runwizard.ProjectContext{}, errors.New("project discovery must stay behind Project scope")
	}
	// Global scope; root, agent, model, effort, native args, window; default-No confirmation.
	runStartWizardInput = strings.NewReader("2\n\n\n\n\n\n\n\n")
	var prompts strings.Builder
	runStartWizardOutput = &prompts
	if err := runNumberedRunStartWizard([]string{"--root", t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	if inspections != 0 || len(*globalCalls) != 1 || hasWizardArg((*globalCalls)[0], "--go") {
		t.Fatalf("inspections=%d global calls=%v", inspections, *globalCalls)
	}
	for _, want := range []string{"What do you want to run?", "Review", "Preview command:", "Live command:", "owns no wake mailbox"} {
		if !strings.Contains(prompts.String(), want) {
			t.Fatalf("global Review missing %q:\n%s", want, prompts.String())
		}
	}
}

func TestNumberedGlobalWizardOverridesProjectPrefillWithoutDiscovery(t *testing.T) {
	_, globalCalls := withWizardExecutionSeams(t)
	oldInput, oldOutput := runStartWizardInput, runStartWizardOutput
	t.Cleanup(func() { runStartWizardInput, runStartWizardOutput = oldInput, oldOutput })
	inspections := 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) {
		inspections++
		return runwizard.ProjectContext{}, errors.New("project-prefilled scope must remain only a default row")
	}
	runStartWizardInput = strings.NewReader("2\n\n\n\n\n\n\n\n")
	var prompts strings.Builder
	runStartWizardOutput = &prompts
	if err := runNumberedRunStartWizard([]string{"--scope", "project", "--project", "/must-not-inspect", "--root", t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	if inspections != 0 || len(*globalCalls) != 1 || hasWizardArg((*globalCalls)[0], "--go") {
		t.Fatalf("inspections=%d global calls=%v", inspections, *globalCalls)
	}
	for _, want := range []string{"What do you want to run?", "Review", "Preview command:", "Live command:"} {
		if !strings.Contains(prompts.String(), want) {
			t.Fatalf("project-prefilled global Review missing %q:\n%s", want, prompts.String())
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
	for _, want := range []string{"What do you want to run?", "Answers are previewed first", "Launch now? [y/N]"} {
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

func TestBubbleGlobalWizardStaysInProgramThroughReview(t *testing.T) {
	_, globalCalls := withWizardExecutionSeams(t)
	oldOpen, oldProgram := runStartWizardOpenTTY, runStartWizardBubbleProgram
	t.Cleanup(func() { runStartWizardOpenTTY, runStartWizardBubbleProgram = oldOpen, oldProgram })
	tty := wizardTestTTY{reader: bytes.NewReader([]byte("\n")), writes: &bytes.Buffer{}}
	runStartWizardOpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	programs := 0
	runStartWizardBubbleProgram = func(_ io.Reader, _ io.Writer, opts runwizard.NumberedOptions) (runwizard.BubbleResult, error) {
		programs++
		if opts.Defaults.Scope != "global" || opts.Defaults.GlobalRoot == "" || opts.Defaults.GlobalWindow != "global-orch" {
			t.Fatalf("global prefills did not reach Bubble: %+v", opts.Defaults)
		}
		return runwizard.BubbleResult{Spec: runwizard.Spec{
			Scope: "global", Backend: runwizard.BackendGlobalStart, GlobalRoot: opts.Defaults.GlobalRoot,
			GlobalAgent: "claude", GlobalWindow: opts.Defaults.GlobalWindow,
		}}, nil
	}
	if err := runBubbleRunStartWizard([]string{"--scope", "global", "--root", t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	if programs != 1 || len(*globalCalls) != 1 || hasWizardArg((*globalCalls)[0], "--go") {
		t.Fatalf("global Bubble routing programs=%d calls=%v", programs, *globalCalls)
	}
}

func TestBubbleGlobalWizardWithoutScopeStartsBeforeProjectDiscovery(t *testing.T) {
	_, globalCalls := withWizardExecutionSeams(t)
	oldOpen, oldProgram := runStartWizardOpenTTY, runStartWizardBubbleProgram
	t.Cleanup(func() { runStartWizardOpenTTY, runStartWizardBubbleProgram = oldOpen, oldProgram })
	tty := wizardTestTTY{reader: bytes.NewReader([]byte("\n")), writes: &bytes.Buffer{}}
	runStartWizardOpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	inspections, programs := 0, 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) {
		inspections++
		return runwizard.ProjectContext{}, errors.New("project discovery must stay behind Project scope")
	}
	runStartWizardBubbleProgram = func(_ io.Reader, _ io.Writer, opts runwizard.NumberedOptions) (runwizard.BubbleResult, error) {
		programs++
		if inspections != 0 || opts.Defaults.Scope != "" {
			t.Fatalf("Bubble started after premature discovery: inspections=%d defaults=%+v", inspections, opts.Defaults)
		}
		return runwizard.BubbleResult{Spec: runwizard.Spec{
			Scope: "global", Backend: runwizard.BackendGlobalStart, GlobalRoot: opts.Defaults.GlobalRoot,
			GlobalAgent: "claude", GlobalWindow: opts.Defaults.GlobalWindow,
		}}, nil
	}
	if err := runBubbleRunStartWizard([]string{"--root", t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	if inspections != 0 || programs != 1 || len(*globalCalls) != 1 || hasWizardArg((*globalCalls)[0], "--go") {
		t.Fatalf("inspections=%d programs=%d global calls=%v", inspections, programs, *globalCalls)
	}
}

func TestBubbleGlobalWizardOverridesProjectPrefillWithoutDiscovery(t *testing.T) {
	_, globalCalls := withWizardExecutionSeams(t)
	oldOpen, oldProgram := runStartWizardOpenTTY, runStartWizardBubbleProgram
	t.Cleanup(func() { runStartWizardOpenTTY, runStartWizardBubbleProgram = oldOpen, oldProgram })
	tty := wizardTestTTY{reader: bytes.NewReader([]byte("\n")), writes: &bytes.Buffer{}}
	runStartWizardOpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	inspections, programs := 0, 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) {
		inspections++
		return runwizard.ProjectContext{}, errors.New("project-prefilled scope must remain only a default row")
	}
	runStartWizardBubbleProgram = func(_ io.Reader, _ io.Writer, opts runwizard.NumberedOptions) (runwizard.BubbleResult, error) {
		programs++
		if inspections != 0 || opts.Defaults.Scope != "project" || opts.Defaults.Project != "/must-not-inspect" {
			t.Fatalf("project prefill was inspected or lost before Bubble: inspections=%d defaults=%+v", inspections, opts.Defaults)
		}
		return runwizard.BubbleResult{Spec: runwizard.Spec{
			Scope: "global", Backend: runwizard.BackendGlobalStart, GlobalRoot: opts.Defaults.GlobalRoot,
			GlobalAgent: "claude", GlobalWindow: opts.Defaults.GlobalWindow,
		}}, nil
	}
	if err := runBubbleRunStartWizard([]string{"--scope", "project", "--project", "/must-not-inspect", "--root", t.TempDir()}, "test"); err != nil {
		t.Fatal(err)
	}
	if inspections != 0 || programs != 1 || len(*globalCalls) != 1 || hasWizardArg((*globalCalls)[0], "--go") {
		t.Fatalf("inspections=%d programs=%d global calls=%v", inspections, programs, *globalCalls)
	}
}

func TestBubbleWizardFingerprintDeltaBeforePreviewRestartsAtProfile(t *testing.T) {
	withWizardExecutionSeams(t)
	oldOpen, oldProgram := runStartWizardOpenTTY, runStartWizardBubbleProgram
	t.Cleanup(func() { runStartWizardOpenTTY, runStartWizardBubbleProgram = oldOpen, oldProgram })
	spec, _ := resumeWizardFixture("A")
	_, changed := resumeWizardFixture("B")
	tty := wizardTestTTY{reader: bytes.NewReader([]byte("\n")), writes: &bytes.Buffer{}}
	runStartWizardOpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	inspections, programs, confirms, previews := 0, 0, 0, 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) {
		inspections++
		return changed, nil
	}
	runStartWizardBubbleProgram = func(_ io.Reader, _ io.Writer, opts runwizard.NumberedOptions) (runwizard.BubbleResult, error) {
		programs++
		if programs == 1 {
			return runwizard.BubbleResult{Spec: spec}, nil
		}
		if !opts.StartAtProfile || opts.Defaults.Session != "" || opts.Defaults.Backend != "" || opts.RestartMessage == "" {
			t.Fatalf("stale retry did not return to Profile & run: %+v", opts)
		}
		return runwizard.BubbleResult{Cancelled: true}, nil
	}
	runStartWizardResumeConfirm = func(io.Reader, io.Writer) (bool, error) { confirms++; return true, nil }
	runStartWizardResumeExecute = func([]string) error { previews++; return nil }
	if err := runBubbleRunStartWizard([]string{"--scope", "project", "--project", "/repo"}, "test"); err != nil {
		t.Fatal(err)
	}
	if inspections != 1 || programs != 2 || confirms != 0 || previews != 0 {
		t.Fatalf("inspections=%d programs=%d confirms=%d previews=%d", inspections, programs, confirms, previews)
	}
}

func TestBubbleWizardFingerprintDeltaAfterYesRestartsAtProfile(t *testing.T) {
	withWizardExecutionSeams(t)
	oldOpen, oldProgram := runStartWizardOpenTTY, runStartWizardBubbleProgram
	t.Cleanup(func() { runStartWizardOpenTTY, runStartWizardBubbleProgram = oldOpen, oldProgram })
	spec, current := resumeWizardFixture("A")
	refreshedSpec, changed := resumeWizardFixture("B")
	tty := wizardTestTTY{reader: &wizardLineReader{lines: []string{"yes\nYES\n", "n\n"}}, writes: &bytes.Buffer{}}
	runStartWizardOpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	inspections, programs := 0, 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) {
		inspections++
		if inspections <= 1 {
			return current, nil
		}
		return changed, nil
	}
	runStartWizardBubbleProgram = func(_ io.Reader, _ io.Writer, opts runwizard.NumberedOptions) (runwizard.BubbleResult, error) {
		programs++
		if programs == 1 {
			return runwizard.BubbleResult{Spec: spec}, nil
		}
		if !opts.StartAtProfile || opts.Defaults.Session != "" || opts.Defaults.Backend != "" {
			t.Fatalf("post-Yes retry did not return to Profile & run: %+v", opts)
		}
		return runwizard.BubbleResult{Spec: refreshedSpec}, nil
	}
	runStartWizardResumeConfirm = promptRunStartWizardResume
	var calls [][]string
	runStartWizardResumeExecute = func(args []string) error {
		calls = append(calls, append([]string(nil), args...))
		if hasWizardArg(args, "--exec") {
			t.Fatalf("stale Bubble Yes reached execution: %v", args)
		}
		return nil
	}
	if err := runBubbleRunStartWizard([]string{"--scope", "project", "--project", "/repo"}, "test"); err != nil {
		t.Fatal(err)
	}
	if inspections != 3 || programs != 2 || len(calls) != 2 {
		t.Fatalf("inspections=%d programs=%d calls=%v", inspections, programs, calls)
	}
}

func TestNumberedWizardPostYesDeltaDiscardsYesAndRestartsAtProfile(t *testing.T) {
	withWizardExecutionSeams(t)
	oldInput, oldOutput := runStartWizardInput, runStartWizardOutput
	t.Cleanup(func() { runStartWizardInput, runStartWizardOutput = oldInput, oldOutput })
	spec, current := resumeWizardFixture("A")
	_, changed := resumeWizardFixture("B")
	// The first confirmation chunk intentionally contains a second affirmative
	// line. The post-Yes drift restart must discard that buffered consent; the
	// retry reaches a real prompt and consumes the later explicit No.
	runStartWizardInput = &wizardLineReader{lines: []string{"\n", "\n", "\n", "\n", "\n", "yes\nYES\n", "\n", "\n", "\n", "n\n"}}
	runStartWizardOutput = io.Discard
	inspections, previews, confirms := 0, 0, 0
	runStartWizardInspectProject = func(string) (runwizard.ProjectContext, error) {
		inspections++
		if inspections <= 2 {
			return current, nil
		}
		return changed, nil
	}
	var calls [][]string
	runStartWizardResumeExecute = func(args []string) error {
		previews++
		calls = append(calls, append([]string(nil), args...))
		if hasWizardArg(args, "--exec") {
			t.Fatalf("stale Yes reached execution: %v", args)
		}
		return nil
	}
	runStartWizardResumeConfirm = func(in io.Reader, out io.Writer) (bool, error) {
		confirms++
		return promptRunStartWizardResume(in, out)
	}
	if err := runNumberedRunStartWizard([]string{"--scope", "project", "--project", spec.Project, "--profile", spec.Profile}, "test"); err != nil {
		t.Fatal(err)
	}
	if inspections != 5 || previews != 2 || confirms != 2 || len(calls) != 2 {
		t.Fatalf("inspections=%d previews=%d confirms=%d calls=%v", inspections, previews, confirms, calls)
	}
}

type wizardLineReader struct {
	lines []string
}

func (r *wizardLineReader) Read(p []byte) (int, error) {
	if len(r.lines) == 0 {
		return 0, io.EOF
	}
	line := r.lines[0]
	r.lines = r.lines[1:]
	return copy(p, line), nil
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

func TestBubbleWizardProgramOwnsScopeInputBeforeLaunchConfirmation(t *testing.T) {
	projectCalls, _ := withWizardExecutionSeams(t)
	oldOpen := runStartWizardOpenTTY
	oldProgram := runStartWizardBubbleProgram
	t.Cleanup(func() { runStartWizardOpenTTY, runStartWizardBubbleProgram = oldOpen, oldProgram })
	tty := wizardTestTTY{reader: &wizardLineReader{lines: []string{"1\n", "yes\n"}}, writes: &bytes.Buffer{}}
	runStartWizardOpenTTY = func() (io.ReadWriteCloser, error) { return tty, nil }
	spec := validWizardProjectSpec(t)
	runStartWizardBubbleProgram = func(in io.Reader, out io.Writer, opts runwizard.NumberedOptions) (runwizard.BubbleResult, error) {
		line, err := readWizardLine(in)
		if err != nil || strings.TrimSpace(line) != "1" {
			t.Fatalf("scope input did not reach Bubble first: line=%q err=%v", line, err)
		}
		return runwizard.BubbleResult{Spec: spec}, nil
	}
	if err := runBubbleRunStartWizard([]string{"--project", spec.Project}, "test"); err != nil {
		t.Fatal(err)
	}
	assertOnlyGoDelta(t, *projectCalls)
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
	prefix := []string{verb}
	if subcommand != "" {
		prefix = append(prefix, subcommand)
	}
	previewCommand := shellCommand("amq-squad", append(append([]string(nil), prefix...), preview...)...)
	liveCommand := shellCommand("amq-squad", append(append([]string(nil), prefix...), live...)...)
	if !strings.Contains(stdout, previewCommand) || !strings.Contains(stdout, liveCommand) {
		t.Fatalf("stdout did not match executed argv\npreview=%s\nlive=%s\nstdout:\n%s", previewCommand, liveCommand, stdout)
	}
}
