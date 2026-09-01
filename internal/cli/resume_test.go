package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// TestResumeAliasPlanIsByteIdenticalToPlan is gh#758's named acceptance
// test: without --exec, resume calls the exact same planPrepare/
// printPlanResult plan.go itself calls on the same resolved coordinates
// (resume.go), not a reimplementation -- so for the same project/profile/
// session, `resume`'s output cannot drift from `plan`'s at all.
func TestResumeAliasPlanIsByteIdenticalToPlan(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRootsForLaunchapiPlan(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	planOut, _, err := captureOutput(t, func() error { return runPlan([]string{"issue-96", "--project", dir}) })
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	resumeOut, _, err := captureOutput(t, func() error { return runResume([]string{"--project", dir, "--session", "issue-96"}) })
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if planOut != resumeOut {
		t.Fatalf("resume's plan-only output diverged from plan's own output.\nplan:\n%s\nresume:\n%s", planOut, resumeOut)
	}
}

// TestRunResumePlanRejectsLaunchOverrideFlags supersedes
// TestRunResumeEffortIsFreshOnlyAndJSONSafe (gh#758/t11): per-invocation
// launch overrides (--effort among them) no longer apply to resume's
// plan-only preview, which is now a thin alias for `plan` with no per-member
// exec command to inject an override into (design note, cto-approved on
// task/t11) -- a relaunch reproduces the profile's canonical configured
// shape. Deleted rather than rewritten: there is no new-shape equivalent of
// "preview shows the overridden effort," the capability itself is gone from
// this path.
func TestRunResumePlanRejectsLaunchOverrideFlags(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"trust", "model", "effort", "codex-args", "claude-args", "no-bootstrap"} {
		args := []string{"--" + flag, "x"}
		if flag == "no-bootstrap" {
			args = []string{"--no-bootstrap"}
		}
		_, _, err := captureOutput(t, func() error { return runResume(args) })
		if err == nil || !strings.Contains(err.Error(), "--"+flag) || !strings.Contains(err.Error(), "no longer applies") {
			t.Fatalf("--%s in plan-only mode should refuse naming the drop, got %v", flag, err)
		}
	}
}

// TestRunResumeEffortRejectsLiveAndMixedActionsBeforeExec is deleted
// (gh#758/t11 slice B): it asserted team_resume.go's own bespoke refusal --
// an --effort/--model/etc. override targeting an already-live role hard-
// blocked before any tmux call, naming the live target explicitly. --exec
// now drives simple_start's shared machinery (runResumeExec, commit 2),
// which has no such refusal: like `start` itself, a launch override simply
// has no effect on a role that is already live and not being respawned --
// it applies only to roles this run actually spawns. This is a deliberate
// behavior change flagged in the slice B report rather than silently
// dropped: the fold trades a hard-refuse-with-named-target for the same
// harmless-no-op-on-live-targets semantics `start --effort` already has,
// which is more consistent with "one implementation, two callers" than
// preserving resume's own bespoke pre-launch validation would have been.

// TestRunResumeEffortPreviewExecCommandParity is deleted (gh#758/t11):
// its whole premise -- the plan-only preview renders the exact same
// per-member command --exec would run, so an --effort override shows up
// identically in both -- no longer holds. The plan-only path renders
// launchapi's PrepareResultV1 (outcome/roster/capabilities), which has no
// per-member command line to compare at all, and --effort itself is
// dropped from that path (TestRunResumePlanRejectsLaunchOverrideFlags).
// --exec's own effort-override behavior is untouched pending slice B's
// fold and remains covered by TestRunResumeEffortRejectsLiveAndMixedActionsBeforeExec.

func TestRunResumeRequiresTeam(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	_, _, err := captureOutput(t, func() error { return runResume(nil) })
	if err == nil || !strings.Contains(err.Error(), "no team configured") {
		t.Fatalf("want 'no team configured', got %v", err)
	}
}

// TestRunResumeMatchesTeamResumePlannerRows proves the top-level verb shares
// the planner with `team resume`: identical inputs produce the same per-member
// plan rows. Headers differ on purpose (top-level says "resume", team resume
// says "team resume"); both now suggest the modern "up" verb in the footer.
// TestRunResumeMatchesTeamResumePlannerRows is deleted (gh#758/t11): its
// whole premise -- top-level resume and `team resume` share one planner, so
// their plan rows must match -- no longer holds once `team resume` is
// deleted outright (redirects to `resume`, slice B) and top-level resume's
// plan-only path no longer uses team_resume.go's planner at all (it calls
// plan's own planPrepare/printPlanResult directly, TestResumeAliasPlanIsByteIdenticalToPlan).
// There is no longer a second planner to stay in parity with.

// TestRunResumeOutputUsesTopLevelLabels is deleted (gh#758/t11): its whole
// premise -- resume's plan-only output has its own "# amq-squad resume"
// header/footer, distinct from team_resume.go's "# amq-squad team resume"
// -- no longer holds. The plan-only path renders plan's own generic
// PrepareResultV1 output verbatim (target:/outcome:/roster: lines), which
// carries no resume-specific framing to distinguish at all.

// TestRunResumeProjectTargetsOtherDir is rewritten (gh#758/t11) to assert
// against the plan-only path's actual new output shape (the "target:" line
// this task adds to printPlanResult, plus the roster line) instead of the
// deleted "# team-home:"/"# workstream:" header lines team_resume.go's
// planner used to print.
func TestRunResumeProjectTargetsOtherDir(t *testing.T) {
	setupFakeAMQSessionRootsForLaunchapiPlan(t)
	project := t.TempDir()
	other := t.TempDir()
	if err := team.Write(project, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-99"}},
	}); err != nil {
		t.Fatal(err)
	}
	resumeChdir(t, other)

	stdout, stderr, err := captureOutput(t, func() error {
		return runResume([]string{"--project", project, "--session", "issue-99"})
	})
	if err != nil {
		t.Fatalf("resume --project: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"target: project=" + canonicalFilesystemPath(project), "session=issue-99", "roster: desired=[cto"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("resume --project output missing %q in:\n%s", want, stdout)
		}
	}
}

// extractPlanRows pulls the ROLE/ACTION/WAKE/NOTE table out of resume output
// so parity tests can compare the planner's classification without coupling
// to header/footer wording.
func TestRunResumeRoleFilterSelectsSubset(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRootsForLaunchapiPlan(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error {
		return runResume([]string{"--role", "fullstack,qa"})
	})
	if err != nil {
		t.Fatalf("resume --role: %v", err)
	}
	// gh#758/t11: the plan-only path's roster now comes straight from
	// planPrepareFiltered's own role-filtered PrepareResultV1 (roster:
	// desired=[...]), not team_resume.go's ROLE/ACTION/WAKE/NOTE table.
	roster := extractPlanRosterLine(stdout)
	for _, want := range []string{"fullstack", "qa"} {
		if !strings.Contains(roster, want) {
			t.Fatalf("roster missing selected role %q:\n%s", want, roster)
		}
	}
	if strings.Contains(roster, "cto") {
		t.Fatalf("unselected role cto must not appear in the roster:\n%s", roster)
	}
}

// extractPlanRosterLine pulls the "roster: ..." line out of printPlanResult's
// output.
func extractPlanRosterLine(out string) string {
	const marker = "roster:"
	idx := strings.Index(out, marker)
	if idx < 0 {
		return ""
	}
	rest := out[idx:]
	if end := strings.Index(rest, "\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

func TestRunResumeRoleFilterRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runResume([]string{"--role", "bogus"})
	})
	if err == nil || !strings.Contains(err.Error(), "no team member(s) with role bogus") {
		t.Fatalf("unknown role should fail clearly, got %v", err)
	}
}

func extractPlanRows(out string) string {
	const marker = "ROLE"
	idx := strings.Index(out, marker)
	if idx < 0 {
		return ""
	}
	rest := out[idx:]
	// Stop at the first blank line after the table.
	end := strings.Index(rest, "\n\n")
	if end < 0 {
		return rest
	}
	return rest[:end]
}

// TestRunResumeReorientsSeatWithoutConversation pins the PR2 contract at the
// top-level resume verb: a restorable seat with no saved conversation comes
// back as a re-orient (bootstrap re-runs, so no --no-bootstrap in the emitted
// command), while a seat carrying a saved conversation reattaches and keeps
// --no-bootstrap.
func TestRunResumeReorientsSeatWithoutConversation(t *testing.T) {
	t.Skip("gh#758/t11: deferred, not deleted or rewritten -- the plan-only path's new alias to plan/planPrepare has no visibility into launch.Record.Conversation at all (it never reads launch.Record; launchapi's own Observations/RosterDriftV1 is the intended replacement signal per the issue, mirroring the launchapi-minted-conversation work already sequenced for slice C). Reattach-vs-reorient framing needs an equivalent built on that signal, not a quick reformat of this assertion; tracked on task/t11 alongside the two goal-recovery dormancy notes rather than silently dropped.")
	t.Run("no conversation re-orients", func(t *testing.T) {
		dir := t.TempDir()
		base := setupFakeAMQSessionRoots(t)
		resumeChdir(t, dir)
		if err := team.Write(dir, team.Team{
			Workstream: "issue-96",
			Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		}); err != nil {
			t.Fatal(err)
		}
		writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
			CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
		})
		stdout, _, err := captureOutput(t, func() error { return runResume(nil) })
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		if strings.Contains(stdout, "--no-bootstrap") {
			t.Errorf("seat without saved conversation must re-orient (no --no-bootstrap):\n%s", stdout)
		}
		if !strings.Contains(stdout, "re-orient") {
			t.Errorf("plan should describe the restore as a re-orient:\n%s", stdout)
		}
	})
	t.Run("with conversation reattaches", func(t *testing.T) {
		dir := t.TempDir()
		base := setupFakeAMQSessionRoots(t)
		resumeChdir(t, dir)
		if err := team.Write(dir, team.Team{
			Workstream: "issue-96",
			Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
		}); err != nil {
			t.Fatal(err)
		}
		writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
			CWD: dir, Binary: "codex", Role: "cto", Conversation: "cto-thread", StartedAt: time.Now(),
		})
		stdout, _, err := captureOutput(t, func() error { return runResume(nil) })
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		if !strings.Contains(stdout, "--no-bootstrap") {
			t.Errorf("seat with saved conversation must reattach (keep --no-bootstrap):\n%s", stdout)
		}
		if !strings.Contains(stdout, "reattach: saved conversation cto-thread") {
			t.Errorf("plan should name the reattached conversation:\n%s", stdout)
		}
	})
}

func TestRunResumeSurfacesNativeGoalBlockedRecovery(t *testing.T) {
	t.Skip("gh#758/t11: deferred to slice C, not deleted or rewritten -- rec.GoalBinding is never set for a launchapi-launched lead, and the plan-only path no longer reads launch.Record at all regardless. cto's ruling: build an equivalent mechanism on the new resume architecture once t9's real InitialInput shape is available (merged, main 3197386), not before. Body stripped in commit 3: it decoded via resumeEnvelopeData, a runtime_json.go type deleted along with team_resume.go.")
}

func TestRunResumeExecSurfacesBlockedGoalRecoveryForMixedRoster(t *testing.T) {
	t.Skip("gh#758/t11: deferred to slice C, not deleted or rewritten -- this is --exec's own copy of the same goal-blocked-recovery dormancy already flagged on task/t11 for the plan-only path (TestRunResumeSurfacesNativeGoalBlockedRecovery, resume_test.go). --exec now drives runResumeExec/simple_start's shared machinery (commit 2), which does not read launch.Record.GoalBinding or emit '# Recovery:' guidance at all. cto's ruling: build an equivalent mechanism on the new resume architecture once t9's real InitialInput shape is available (merged, main 3197386), not before -- same as the plan-only path's deferral. Body stripped in commit 3: its stubs (runTmuxLaunchPlanForResume/verifyResumeExecLaunchRecordsNow/verifyResumeLeadReadyNow) were team_resume.go internals, now deleted.")
}

func TestRunResumeRejectsFreshFlag(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error { return runResume([]string{"--fresh"}) })
	if err == nil {
		t.Fatal("resume must not accept --fresh at top level")
	}
	if !strings.Contains(err.Error(), "fresh") {
		t.Fatalf("error should name the rejected flag: %v", err)
	}
}

func TestRunResumeHonorsExplicitSession(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRootsForLaunchapiPlan(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-99"}},
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error { return runResume([]string{"--session", "issue-99"}) })
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(stdout, "issue-99") {
		t.Errorf("--session not honored:\n%s", stdout)
	}
	if strings.Contains(stdout, "workstream: issue-96") {
		t.Errorf("explicit --session should override stored workstream:\n%s", stdout)
	}
}

// writeSessionPresence drops a presence.json for handle under session's
// launch record so the status-board activity signal resolveLastSessionForProfile
// relies on has a real, orderable timestamp instead of a zero time.
func writeSessionPresence(t *testing.T, base, session, handle string, lastSeen time.Time) {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"schema": 1, "handle": handle, "status": "online", "last_seen": lastSeen,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "presence.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestRunResumeLastPicksTheOnlyLiveSession is regression coverage for the
// single-session case of gh#722's 'resume --last' shorthand.
func TestRunResumeLastPicksTheOnlyLiveSession(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRootsForLaunchapiPlan(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})
	writeSessionPresence(t, base, "issue-96", "cto", time.Now())

	stdout, stderr, err := captureOutput(t, func() error { return runResume([]string{"--last"}) })
	if err != nil {
		t.Fatalf("resume --last: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, `--last picked session "issue-96"`) {
		t.Errorf("--last did not report the picked session:\n%s", stderr)
	}
	if !strings.Contains(stdout, "session=issue-96") {
		t.Errorf("--last did not resume the only live session:\n%s", stdout)
	}
}

// TestRunResumeLastPicksMostRecentAmongMultipleLiveSessions is the HIGH
// regression from senior-dev's review of PR #723: with two live sessions
// for the same profile, resolveCanonicalContext's own session resolution
// used to run first and return an ambiguous-session usage error before
// --last's newest-session pick ever ran. Both sessions here are genuinely
// live (a launch record + a fresh presence heartbeat each), so this must
// succeed and pick the more recently active one, not error out.
func TestRunResumeLastPicksMostRecentAmongMultipleLiveSessions(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRootsForLaunchapiPlan(t)
	resumeChdir(t, dir)
	// Members are not session-pinned (Session left empty), matching a
	// profile that has been resumed into more than one workstream over
	// time -- exactly the case that used to trip the ambiguous-session
	// error before --last's own resolution ever ran.
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}); err != nil {
		t.Fatal(err)
	}
	older := time.Now().Add(-time.Hour)
	newer := time.Now()
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: older,
	})
	writeSessionPresence(t, base, "issue-96", "cto", older)
	writeMemberLaunchRecord(t, base, "issue-97", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: newer,
	})
	writeSessionPresence(t, base, "issue-97", "cto", newer)

	stdout, stderr, err := captureOutput(t, func() error { return runResume([]string{"--last"}) })
	if err != nil {
		t.Fatalf("resume --last with multiple live sessions: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stderr, `--last picked session "issue-97"`) {
		t.Errorf("--last did not pick the more recently active session:\n%s", stderr)
	}
	if !strings.Contains(stdout, "session=issue-97") {
		t.Errorf("--last did not resume the picked session:\n%s", stdout)
	}
}

func TestRunResumeLastRejectsExplicitSession(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := captureOutput(t, func() error { return runResume([]string{"--last", "--session", "issue-96"}) }); err == nil {
		t.Fatal("want --last + --session to be rejected")
	}
}

func TestRunResumeRestoreExistingPropagates(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRootsForLaunchapiPlan(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	// No restorable records -> --restore-existing must fail.
	_, _, err := captureOutput(t, func() error { return runResume([]string{"--restore-existing"}) })
	if err == nil || !strings.Contains(err.Error(), "--restore-existing") {
		t.Fatalf("want --restore-existing failure, got %v", err)
	}
}

// resumePlanDoesNotMutateDisk is a sanity check: the planner promises plan-
// only behavior. We exercise it from the top-level verb and confirm no new
// files appear under the AMQ root.
func TestRunResumeDoesNotMutateAMQRoot(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRootsForLaunchapiPlan(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})
	before := fileTreeFingerprint(t, base)
	if _, _, err := captureOutput(t, func() error { return runResume(nil) }); err != nil {
		t.Fatalf("resume: %v", err)
	}
	after := fileTreeFingerprint(t, base)
	if before != after {
		t.Fatalf("resume mutated AMQ root.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

func fileTreeFingerprint(t *testing.T, root string) string {
	t.Helper()
	var lines []string
	err := walkFiles(root, func(path string, mode os.FileMode, size int64) {
		lines = append(lines, path)
	})
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func walkFiles(root string, visit func(path string, mode os.FileMode, size int64)) error {
	return walkDir(root, func(path string, info os.FileInfo) error {
		visit(path, info.Mode(), info.Size())
		return nil
	})
}

// TestRunResumeRejectsExecWithDryRun guards the mutually-exclusive surface
// so the operator does not get a silent no-op when they pass both.
func TestRunResumeRejectsExecWithDryRun(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error { return runResume([]string{"--exec", "--dry-run"}) })
	if err == nil {
		t.Fatal("--exec --dry-run together should be a usage error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}

// gh#758/t11 slice B commit 3: TestExecResumePlanRefusesBlockedMembersUnlessForced,
// TestExecResumePlanNothingToLaunch, TestRunResumeTmuxPlanStagesLeadBeforeDependentsAcrossTargets,
// TestRunResumeTmuxPlanLeadReadinessFailureLaunchesNoDependentsEvenWithForce,
// TestRunResumeTmuxPlanSkipLeadCheckLaunchesDependentsDespiteFailingGate,
// TestRunResumeTmuxPlanChecksAlreadyLiveLeadBeforePartialWorkerResume,
// TestInspectResumeLeadReadyIgnoresLegacyBootstrapExpectation,
// TestInspectResumeLeadReadySurvivesInPlaceLeadRestart,
// TestExecResumePlanReportsPartialLaunchRecordFailure,
// TestVerifyResumeExecLaunchRecordsAdoptsStaleRecordByPaneTitle, and
// TestExecResumePlanRejectsUnknownTerminal all deleted. Every one of them
// exercised team_resume.go's own per-member launch orchestration internals
// -- execResumePlan, resumePlan/resumeExecOptions/resumeBlocked/resumeLive/
// resumeFresh/resumeRestore, runTmuxLaunchPlanForResume,
// verifyResumeExecLaunchRecords(Now), inspectResumeLeadReady,
// runResumeTmuxPlanWithLeadGate, buildResumeExecLaunchChecks,
// snapshotResumeExecLaunchRecords -- all deleted along with the classifier,
// no live equivalent under these names. --exec now drives
// runResumeExec/buildSimpleStartPlan/runSimpleStartWithRequest (commit 2)
// instead, which has its own, already-covered verification machinery
// (verifySimpleStartRecords, validateCompleteTeamLaunchResult in
// simple_start_test.go) and deliberately does NOT replicate the old
// classifier's two-phase lead-then-dependents staged launch sequencing (a
// launch-mechanics detail resume_exec.go's own doc comment calls out as
// orthogonal to the new required-action lead gate, resolveResumeLeadGate,
// which TestResumeExecRoleFilterDigestIgnoresLivenessOutsideSubset and the
// team_lead_test.go lead-gate tests cover instead).

// TestRunResumePositionalSessionHonored verifies that `resume <session>`
// treats the positional as the session name, fixing #177's secondary finding.
func TestRunResumePositionalSessionHonored(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRootsForLaunchapiPlan(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "go-dev", Binary: "claude", Handle: "go-dev", Session: "beta"},
			{Role: "architect", Binary: "codex", Handle: "architect", Session: "alpha"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := captureOutput(t, func() error { return runResume([]string{"beta"}) })
	if err != nil {
		t.Fatalf("resume beta: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "beta") {
		t.Errorf("positional session not honored; got:\n%s", stdout)
	}
	if !strings.Contains(stderr, "skipping architect") {
		t.Errorf("stderr missing skip notice for cross-session member:\n%s", stderr)
	}
}

// TestRunResumePositionalAndFlagIsError verifies that passing session both
// positionally and via --session is rejected.
func TestRunResumePositionalAndFlagIsError(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "beta"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runResume([]string{"--session", "beta", "beta"})
	})
	if err == nil || !strings.Contains(err.Error(), "positionally or via --session, not both") {
		t.Fatalf("expected both-session error; got %v", err)
	}
}

// TestRunResumeTooManyPositionalsIsError verifies that more than one positional
// is rejected cleanly.
func TestRunResumeTooManyPositionalsIsError(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "beta"}},
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runResume([]string{"beta", "extra"})
	})
	if err == nil || !strings.Contains(err.Error(), "at most one session positional") {
		t.Fatalf("expected too-many-positionals error; got %v", err)
	}
}

// walkDir is a tiny wrapper around filepath.Walk used by the disk-mutation
// fingerprint test. Kept local so the existing helpers stay focused on the
// planner inputs.
func walkDir(root string, fn func(path string, info os.FileInfo) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		full := root + string(os.PathSeparator) + e.Name()
		info, err := os.Stat(full)
		if err != nil {
			continue
		}
		if err := fn(full, info); err != nil {
			return err
		}
		if info.IsDir() {
			if err := walkDir(full, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

// gh#758/t11 slice B commit 3: TestRunResumeTmuxPlanLeadMainOnlyForDependentOnlyLaunch
// and TestResumeLaunchedFromLeadPaneComparesRecordedPane deleted. Both tested
// team_resume.go's own lead-main-current-window arrangement heuristic
// (runResumeTmuxPlanWithLeadGate's LeadMainCurrentWindow flag,
// resumeLaunchedFromLeadPane/resumeLaunchedFromLeadPaneNow) -- all deleted
// with the classifier, no live equivalent. resume --exec's new shared-
// machinery fold (commit 2) does not replicate this arrangement heuristic at
// all: runSimpleStartWithRequest has no notion of "lead already live,
// dependents launching from the lead's own pane" driving a different tmux
// window layout: see resume_exec.go's own scope note on not replicating
// "launch-mechanics detail orthogonal to" the new required-action gate.
