package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// gh#758/t11 slice B commit 3: eleven tests deleted from this file, all
// testing team_resume.go's now-deleted goal-redelivery machinery
// (buildResumeGoalPlan, executeResume's --json goal-plan path,
// resumeNativeGoalBlockedRecoveries/writeResumeNativeGoalBlockedRecoveries,
// writeResumeJSONWithGoal, deliverResumeGoalAfterLaunch,
// goalManualDeliveryCommand, writeResumeGoalPlan) -- every one of these
// functions' sole caller was the classifier this commit deletes, and
// resume's own goal-redelivery step is not wired into the new
// runResumeExec/simple_start path yet (--redeliver-goal/
// --no-redeliver-goal-prompt are refused outright, see resume.go and t11's
// slice B PR body). This is a real, further behavior tightening beyond
// gh#761/t9's original "print retirement notice, never fail" contract
// (TestDeliverResumeGoalAfterLaunchPrintsRetirementNoticeAndNeverFails and
// TestResumeGoalRecoveryGuidanceNeverPrintsRemovedSubcommands both asserted
// that exact contract, which no longer exists): resume --exec now refuses
// the flags entirely rather than accepting and silently no-op'ing them.
// Deleted: TestResumeGoalPlanRejectsSavedTeamHomeAndAdoptedTarget,
// TestVerifyResumeGoalPostBaselineReadyUsesExactLeadAndRefusesBeforeResend,
// TestResumeJSONSelectedGoalPlanIsReadOnly,
// TestResumeGoalPlanEligibleUsesExactSettledEvidence,
// TestResumeGoalPlanReattachSkipsButFingerprintsBinding,
// TestResumeSurfacesNativeGoalBlockedRecoveryWithoutReactivation,
// TestResumeNativeGoalBlockedRecoveryCoversMixedRosterWithoutFalsePositives,
// TestResumeGoalPlanUnclaimedBlocksWithoutCreatingAttempt,
// TestResumeGoalPlanRejectsNonGeneratedRawCommand,
// TestResumeGoalPlanRejectsCorruptOrMismatchedPromptBindingWithoutMutation,
// TestDeliverResumeGoalAfterLaunchPrintsRetirementNoticeAndNeverFails,
// TestResumeGoalRecoveryGuidanceNeverPrintsRemovedSubcommands.
//
// TestResumeGoalAttemptIdentityIsExact is rewritten below (not deleted):
// what it actually verifies -- validateResumeGoalAttempt's exact-identity
// rejection -- is still live, real, in-scope code, unrelated to the
// classifier. It no longer needs the deleted seededResumeGoalPlan/
// buildResumeGoalPlan fixture chain to construct its inputs; the attempt
// record and the values validateResumeGoalAttempt compares against are
// built directly here instead.

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustGoalAttemptPath(t *testing.T, project, profile, session, attemptID string) string {
	t.Helper()
	path, err := goalAttemptPath(project, profile, session, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResumeGoalAttemptIdentityIsExact(t *testing.T) {
	project := t.TempDir()
	const (
		profile   = team.DefaultProfile
		session   = "issue-447"
		role      = "cto"
		handle    = "cto"
		attemptID = "attempt-original"
	)
	goal := "ship literal --attempt-id fake\nwith \"quotes\""
	ns := squadnamespace.Resolve(project, profile, session)
	attempt := goalAttemptRecord{
		SchemaVersion: 1, AttemptID: attemptID, Goal: goal,
		Project: project, Profile: profile, Session: session, Namespace: ns,
		Role: role, Handle: handle,
	}
	path := mustGoalAttemptPath(t, project, profile, session, attemptID)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, path, attempt)
	got, err := readGoalAttempt(path, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if got != attempt {
		t.Fatalf("round-tripped attempt = %+v, want %+v", got, attempt)
	}

	mutations := map[string]func(*goalAttemptRecord){
		"role case":         func(a *goalAttemptRecord) { a.Role = "CTO" },
		"handle case":       func(a *goalAttemptRecord) { a.Handle = "CTO" },
		"namespace display": func(a *goalAttemptRecord) { a.Namespace.Display = "changed" },
		"namespace path":    func(a *goalAttemptRecord) { a.Namespace.Paths.Brief = "/other" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			mutated := got
			mutate(&mutated)
			if err := validateResumeGoalAttempt(mutated, project, profile, session, role, handle, goal, attemptID, ns); err == nil {
				t.Fatalf("exact identity mutation accepted: %+v", mutated)
			}
		})
	}
}

func TestParseGeneratedGoalBindingIsQuoteAware(t *testing.T) {
	tm := team.Team{ExecutionMode: executionModeProjectLead}
	wantGoal := "say --attempt-id fake and \"quoted\"\nsecond line"
	command := nativeGoalControlPrompt(wantGoal, tm, "default", "issue-447", "cto", "real-attempt")
	goal, attemptID, err := parseGeneratedGoalBinding(command)
	if err != nil || attemptID != "real-attempt" || goal != wantGoal || strings.Contains(command, `quoted\nsecond`) {
		t.Fatalf("parse = goal %q attempt %q err %v", goal, attemptID, err)
	}
	if _, _, err := parseGeneratedGoalBinding(command + " --attempt-id duplicate"); err == nil {
		t.Fatal("duplicate attempt flag must fail")
	}
}

func TestRestoreGoalBindingIsMetadataNotChildArg(t *testing.T) {
	tm := team.Team{Project: "/tmp/project", Lead: "cto", ExecutionMode: executionModeProjectLead}
	command := codexGoalControlPrompt("ship\nsecond line", tm, team.DefaultProfile, "s", "cto", "a1")
	rec := launch.Record{CWD: "/tmp/project", Binary: "codex", Role: "cto", Handle: "cto", Session: "s", Argv: []string{"--enable", "goals", command}, GoalBinding: &launch.GoalBinding{Mode: "prompt_goal", NativeGoal: false, Source: "goal-control", Command: command, Goal: "ship\nsecond line", AttemptID: "a1", Detail: "reserved"}}
	args := launchArgsFromRecord(rec)
	var metadata string
	dash := len(args)
	for i, arg := range args {
		if arg == "--restore-goal-binding" && i+1 < len(args) {
			metadata = args[i+1]
		}
		if arg == "--" {
			dash = i
			break
		}
	}
	if metadata == "" {
		t.Fatalf("restore args omitted binding metadata: %#v", args)
	}
	for _, arg := range args[dash:] {
		if arg == command {
			t.Fatalf("saved goal leaked into child argv: %#v", args)
		}
	}
	var decoded launch.GoalBinding
	if err := json.Unmarshal([]byte(metadata), &decoded); err != nil || decoded != *rec.GoalBinding {
		t.Fatalf("metadata round trip = %+v, %v", decoded, err)
	}
	if emitted := emitCommand(rec); !strings.Contains(emitted, "--restore-goal-binding") {
		t.Fatalf("copy-paste restore omitted binding metadata: %s", emitted)
	}
}

func TestRunLaunchRestoresGoalBindingMetadataWithoutActivation(t *testing.T) {
	project := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-447"}}, Orchestrated: true, Lead: "cto"})
	setupFakeAMQ(t)
	tm := team.Team{Project: project, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-447"}}, Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead}
	goal := "ship\nsecond line"
	command := codexGoalControlPrompt(goal, tm, team.DefaultProfile, "issue-447", "cto", "a1")
	binding := &launch.GoalBinding{Mode: "prompt_goal", NativeGoal: false, Source: "goal-control", Command: command, Goal: goal, AttemptID: "a1", Detail: "reserved bytes\nunchanged"}
	rec := launch.Record{CWD: project, Binary: "codex", Role: "cto", Handle: "cto", Session: "issue-447", SharedWorkstream: true, TeamHome: project, Argv: []string{"--enable", "goals", binding.Command}, GoalBinding: binding}
	var observed launch.Record
	var child []string
	oldObserver := launchPlanObserver
	launchPlanObserver = func(got launch.Record, args []string) { observed, child = got, append([]string(nil), args...) }
	t.Cleanup(func() { launchPlanObserver = oldObserver })
	args := append([]string{"--dry-run", "--project", project}, launchArgsFromRecord(rec)...)
	if _, _, err := captureOutput(t, func() error { return runLaunch(args) }); err != nil {
		t.Fatalf("restore launch: %v", err)
	}
	if observed.GoalBinding == nil || *observed.GoalBinding != *binding {
		t.Fatalf("restored binding changed: got=%+v want=%+v", observed.GoalBinding, binding)
	}
	for _, arg := range child {
		if arg == binding.Command {
			t.Fatalf("restored binding activated through child argv: %v", child)
		}
	}
	payload, err := json.Marshal(binding)
	if err != nil {
		t.Fatal(err)
	}
	conflict := []string{"--dry-run", "--project", project, "--session", "issue-447", "--role", "cto", "--restore-goal-binding", string(payload), "codex", "--", binding.Command}
	if _, _, err := captureOutput(t, func() error { return runLaunch(conflict) }); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("metadata/prompt-goal conflict accepted: %v", err)
	}
}
