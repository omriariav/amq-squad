package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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

func TestRunResumeEffortRejectsLiveAndMixedActionsBeforeExec(t *testing.T) {
	for _, tc := range []struct {
		name       string
		members    []team.Member
		liveRole   string
		effort     string
		wantTarget string
	}{
		{
			name:       "live target",
			members:    []team.Member{{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"}},
			liveRole:   "qa",
			effort:     "qa=max",
			wantTarget: "qa (live)",
		},
		{
			name: "mixed targets",
			members: []team.Member{
				{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
				{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"},
			},
			liveRole:   "cto",
			effort:     "qa=max,cto=high",
			wantTarget: "cto (live)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			base := setupFakeAMQSessionRoots(t)
			resumeChdir(t, dir)
			if err := team.Write(dir, team.Team{Workstream: "issue-96", Members: tc.members}); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(team.ProfilePath(dir, team.DefaultProfile))
			if err != nil {
				t.Fatal(err)
			}
			agentDir := filepath.Join(base, "issue-96", "agents", tc.liveRole)
			if err := os.MkdirAll(agentDir, 0o755); err != nil {
				t.Fatal(err)
			}
			myPID := os.Getpid()
			writeWakeLock(t, agentDir, wakeLockFile{PID: myPID, Root: filepath.Join(base, "issue-96")})
			oldProbe := defaultDuplicateLaunchProbe
			defaultDuplicateLaunchProbe = duplicateLaunchProbe{
				PIDAlive: func(pid int) bool { return pid == myPID },
				ProcessMatch: func(pid int, predicate func(string) bool) bool {
					return predicate("amq wake --me " + tc.liveRole + " --root " + filepath.Join(base, "issue-96"))
				},
				Now: time.Now,
			}
			oldRun := runTmuxLaunchPlanForResume
			called := false
			runTmuxLaunchPlanForResume = func(tmuxLaunchPlan) error {
				called = true
				return nil
			}
			t.Cleanup(func() {
				defaultDuplicateLaunchProbe = oldProbe
				runTmuxLaunchPlanForResume = oldRun
			})

			_, _, err = captureOutput(t, func() error {
				return runResume([]string{"--exec", "--effort", tc.effort})
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantTarget) || !strings.Contains(err.Error(), "only to launch-fresh") {
				t.Fatalf("effort action rejection = %v", err)
			}
			if called {
				t.Fatal("mixed invalid effort targets reached the tmux executor")
			}
			after, err := os.ReadFile(team.ProfilePath(dir, team.DefaultProfile))
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("rejected resume effort changed the profile")
			}
		})
	}
}

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
	t.Skip("gh#758/t11: deferred to slice C, not deleted or rewritten -- this is exactly the goal-blocked-recovery dormancy already flagged on task/t11 (inherited from t9/gh#761): rec.GoalBinding is never set for a launchapi-launched lead, and the plan-only path no longer reads launch.Record at all regardless. cto's ruling: build an equivalent mechanism on the new resume architecture once t9's real InitialInput shape is available (merged, main 3197386), not before.")
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream:    "issue-447",
		Orchestrated:  true,
		Lead:          "cto",
		ExecutionMode: executionModeProjectLead,
		Members:       []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-447"}},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-447", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
		GoalBinding: &launch.GoalBinding{
			Mode:       "native_goal_blocked",
			NativeGoal: true,
			Source:     "goal-runtime",
			Command:    `/goal --goal "ship"`,
			Detail:     "Goal blocked (/goal resume)",
		},
	})

	plain, _, err := captureOutput(t, func() error { return runResume(nil) })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plain, "Native goal recovery required") || !strings.Contains(plain, "then enter /goal resume manually") || !strings.Contains(plain, "Do not automatically redeliver") {
		t.Fatalf("resume plan did not surface safe blocked-goal recovery:\n%s", plain)
	}

	jsonOut, _, err := captureOutput(t, func() error { return runResume([]string{"--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	env := decodeJSONEnvelope[resumeEnvelopeData](t, jsonOut)
	if len(env.Data.NativeGoalBlockedRecovery) != 1 || env.Data.NativeGoalBlockedRecovery[0].Role != "cto" || !strings.Contains(env.Data.NativeGoalBlockedRecovery[0].Guidance, "/goal resume") {
		t.Fatalf("resume JSON did not surface blocked-goal recovery:\n%s", jsonOut)
	}
}

func TestRunResumeExecSurfacesBlockedGoalRecoveryForMixedRoster(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-447", Orchestrated: true, Lead: "cto", ExecutionMode: executionModeProjectLead,
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-447"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-447"},
			{Role: "fullstack", Binary: "codex", Handle: "fullstack", Session: "issue-447"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct {
		role    string
		binding *launch.GoalBinding
	}{
		{role: "cto", binding: &launch.GoalBinding{Mode: "native_goal_blocked", NativeGoal: true, Detail: "lead blocked"}},
		{role: "qa", binding: &launch.GoalBinding{Mode: "native_goal_blocked", NativeGoal: true, Detail: "worker blocked"}},
		{role: "fullstack", binding: &launch.GoalBinding{Mode: "native_goal", NativeGoal: true, Detail: "delivered"}},
	} {
		writeMemberLaunchRecord(t, base, "issue-447", row.role, launch.Record{CWD: dir, Binary: "codex", Role: row.role, Handle: row.role, Session: "issue-447", StartedAt: time.Now(), GoalBinding: row.binding})
	}
	oldRun, oldVerify, oldReady := runTmuxLaunchPlanForResume, verifyResumeExecLaunchRecordsNow, verifyResumeLeadReadyNow
	runTmuxLaunchPlanForResume = func(tmuxLaunchPlan) error { return nil }
	verifyResumeLeadReadyNow = func(resumeExecLaunchCheck) error { return nil }
	verifyResumeExecLaunchRecordsNow = func(checks []resumeExecLaunchCheck, _ map[string]resumeExecLaunchSnapshot) []resumeExecLaunchResult {
		out := make([]resumeExecLaunchResult, 0, len(checks))
		for _, check := range checks {
			out = append(out, resumeExecLaunchResult{Check: check, State: resumeExecLaunchStateLaunched})
		}
		return out
	}
	t.Cleanup(func() {
		runTmuxLaunchPlanForResume, verifyResumeExecLaunchRecordsNow, verifyResumeLeadReadyNow = oldRun, oldVerify, oldReady
	})
	_, stderr, err := captureOutput(t, func() error { return runResume([]string{"--exec", "--stagger", "0"}) })
	if err != nil {
		t.Fatalf("resume --exec: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Count(stderr, "# Recovery:") != 2 || !strings.Contains(stderr, "cto") || !strings.Contains(stderr, "qa") || strings.Contains(stderr, "fullstack") || !strings.Contains(stderr, "then enter /goal resume manually") {
		t.Fatalf("mixed exec recovery output = %q", stderr)
	}
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

// TestExecResumePlanRefusesBlockedMembersUnlessForced covers the contract
// that resume --exec is not a backdoor around live-agent protection: any
// member in action=blocked aborts the run unless --force-duplicate.
func TestExecResumePlanRefusesBlockedMembersUnlessForced(t *testing.T) {
	t.Run("blocked aborts without force", func(t *testing.T) {
		err := execResumePlan(
			team.Team{Project: t.TempDir(), Members: []team.Member{{Role: "cto"}}},
			team.DefaultProfile,
			"issue-96",
			[]resumePlan{
				{Role: "cto", Action: resumeBlocked, Note: "wake+presence", Command: ""},
			},
			resumeExecOptions{Enabled: true, Terminal: "tmux", Target: "current-window", Layout: "vertical"},
			false,
		)
		if err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Fatalf("blocked member should abort: %v", err)
		}
		if !strings.Contains(err.Error(), "--force-duplicate") {
			t.Errorf("error should mention escape hatch: %v", err)
		}
	})
}

// TestExecResumePlanNothingToLaunch covers the all-live scenario: every
// member is already running, so there is nothing to send through the
// terminal backend. Exit cleanly with a notice rather than opening an
// empty pane.
func TestExecResumePlanNothingToLaunch(t *testing.T) {
	dir := t.TempDir()
	stdout, _, err := captureOutput(t, func() error {
		return execResumePlan(
			team.Team{Project: dir, Members: []team.Member{{Role: "cto"}, {Role: "qa"}}},
			team.DefaultProfile,
			"issue-96",
			[]resumePlan{
				{Role: "cto", Action: resumeLive, Note: "wake"},
				{Role: "qa", Action: resumeLive, Note: "wake+presence"},
			},
			resumeExecOptions{Enabled: true, Terminal: "tmux", Target: "current-window", Layout: "vertical"},
			false,
		)
	})
	if err != nil {
		t.Fatalf("all-live execResumePlan should succeed: %v", err)
	}
	for _, want := range []string{"resume --exec", "nothing to launch", "2 live"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output missing %q:\n%s", want, stdout)
		}
	}
}

func TestRunResumeTmuxPlanStagesLeadBeforeDependentsAcrossTargets(t *testing.T) {
	for _, target := range []string{"current-window", "new-session", "new-window"} {
		t.Run(target, func(t *testing.T) {
			oldRun := runTmuxLaunchPlanForResume
			oldVerify := verifyResumeExecLaunchRecordsNow
			oldReady := verifyResumeLeadReadyNow
			t.Cleanup(func() {
				runTmuxLaunchPlanForResume = oldRun
				verifyResumeExecLaunchRecordsNow = oldVerify
				verifyResumeLeadReadyNow = oldReady
			})

			var events []string
			var submitted []tmuxLaunchPlan
			runTmuxLaunchPlanForResume = func(plan tmuxLaunchPlan) error {
				events = append(events, "run:"+plan.Panes[0].Role)
				submitted = append(submitted, plan)
				return nil
			}
			verifyResumeExecLaunchRecordsNow = func(checks []resumeExecLaunchCheck, _ map[string]resumeExecLaunchSnapshot) []resumeExecLaunchResult {
				events = append(events, "record:"+checks[0].Role)
				out := make([]resumeExecLaunchResult, 0, len(checks))
				for _, check := range checks {
					out = append(out, resumeExecLaunchResult{Check: check, State: resumeExecLaunchStateLaunched})
				}
				return out
			}
			verifyResumeLeadReadyNow = func(check resumeExecLaunchCheck) error {
				events = append(events, "ready:"+check.Role)
				return nil
			}

			checks := []resumeExecLaunchCheck{{Role: "cto", Handle: "cto"}, {Role: "qa", Handle: "qa"}}
			results, err := runResumeTmuxPlanWithLeadGate(
				team.Team{Orchestrated: true, Lead: "cto"}, team.DefaultProfile, "issue-473",
				tmuxLaunchPlan{Session: "squad", Workstream: "issue-473", Target: target, Layout: "tiled", Panes: []teamLaunchPane{
					{Role: "cto", Command: "lead"}, {Role: "qa", Command: "worker"},
				}}, checks, map[string]resumeExecLaunchSnapshot{}, false,
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 2 {
				t.Fatalf("results = %+v", results)
			}
			if got := strings.Join(events, ","); got != "run:cto,record:cto,ready:cto,run:qa,record:qa" {
				t.Fatalf("staging order = %s", got)
			}
			if len(submitted) != 2 || len(submitted[0].Panes) != 1 || len(submitted[1].Panes) != 1 || submitted[1].Panes[0].Role != "qa" {
				t.Fatalf("submitted plans = %+v", submitted)
			}
			if !submitted[1].AllowExistingSession {
				t.Fatalf("dependent %s plan must reuse the lead's terminal topology", target)
			}
		})
	}
}

func TestRunResumeTmuxPlanLeadReadinessFailureLaunchesNoDependentsEvenWithForce(t *testing.T) {
	oldRun := runTmuxLaunchPlanForResume
	oldVerify := verifyResumeExecLaunchRecordsNow
	oldReady := verifyResumeLeadReadyNow
	t.Cleanup(func() {
		runTmuxLaunchPlanForResume = oldRun
		verifyResumeExecLaunchRecordsNow = oldVerify
		verifyResumeLeadReadyNow = oldReady
	})

	var submitted []string
	runTmuxLaunchPlanForResume = func(plan tmuxLaunchPlan) error {
		for _, pane := range plan.Panes {
			submitted = append(submitted, pane.Role)
		}
		return nil
	}
	verifyResumeExecLaunchRecordsNow = func(checks []resumeExecLaunchCheck, _ map[string]resumeExecLaunchSnapshot) []resumeExecLaunchResult {
		return []resumeExecLaunchResult{{Check: checks[0], State: resumeExecLaunchStateLaunched}}
	}
	verifyResumeLeadReadyNow = func(resumeExecLaunchCheck) error { return stubErr("bootstrap mismatch") }

	checks := []resumeExecLaunchCheck{{Role: "cto", Handle: "cto", Force: true}, {Role: "qa", Handle: "qa", Force: true}}
	_, err := runResumeTmuxPlanWithLeadGate(
		team.Team{Orchestrated: true, Lead: "cto"}, team.DefaultProfile, "issue-473",
		tmuxLaunchPlan{Target: "current-window", Panes: []teamLaunchPane{{Role: "cto"}, {Role: "qa"}}},
		checks, map[string]resumeExecLaunchSnapshot{}, false,
	)
	if err == nil || !strings.Contains(err.Error(), "lead readiness failed for cto") || !strings.Contains(err.Error(), "dependent roles were not launched") {
		t.Fatalf("readiness error = %v", err)
	}
	if got := strings.Join(submitted, ","); got != "cto" {
		t.Fatalf("submitted roles = %q, want lead only", got)
	}
}

// #655: --skip-lead-check is the recovery escape hatch — a failing readiness
// gate must not block dependent launches when the operator explicitly bypasses
// it, and the bypass must never silently consult the gate.
func TestRunResumeTmuxPlanSkipLeadCheckLaunchesDependentsDespiteFailingGate(t *testing.T) {
	oldRun := runTmuxLaunchPlanForResume
	oldVerify := verifyResumeExecLaunchRecordsNow
	oldReady := verifyResumeLeadReadyNow
	t.Cleanup(func() {
		runTmuxLaunchPlanForResume = oldRun
		verifyResumeExecLaunchRecordsNow = oldVerify
		verifyResumeLeadReadyNow = oldReady
	})

	var submitted []string
	runTmuxLaunchPlanForResume = func(plan tmuxLaunchPlan) error {
		for _, pane := range plan.Panes {
			submitted = append(submitted, pane.Role)
		}
		return nil
	}
	verifyResumeExecLaunchRecordsNow = func(checks []resumeExecLaunchCheck, _ map[string]resumeExecLaunchSnapshot) []resumeExecLaunchResult {
		out := make([]resumeExecLaunchResult, 0, len(checks))
		for _, check := range checks {
			out = append(out, resumeExecLaunchResult{Check: check, State: resumeExecLaunchStateLaunched})
		}
		return out
	}
	gateConsulted := false
	verifyResumeLeadReadyNow = func(resumeExecLaunchCheck) error {
		gateConsulted = true
		return stubErr("lead pane %217 is not live")
	}

	checks := []resumeExecLaunchCheck{{Role: "cto", Handle: "cto"}, {Role: "qa", Handle: "qa"}}
	results, err := runResumeTmuxPlanWithLeadGate(
		team.Team{Orchestrated: true, Lead: "cto"}, team.DefaultProfile, "issue-473",
		tmuxLaunchPlan{Target: "current-window", Panes: []teamLaunchPane{{Role: "cto"}, {Role: "qa"}}},
		checks, map[string]resumeExecLaunchSnapshot{}, true,
	)
	if err != nil {
		t.Fatalf("skip-lead-check resume failed: %v", err)
	}
	if gateConsulted {
		t.Fatal("--skip-lead-check must bypass the readiness gate entirely")
	}
	if got := strings.Join(submitted, ","); got != "cto,qa" {
		t.Fatalf("submitted roles = %q, want lead then dependents", got)
	}
	if len(results) != 2 {
		t.Fatalf("results = %+v", results)
	}
}

func TestRunResumeTmuxPlanChecksAlreadyLiveLeadBeforePartialWorkerResume(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	tm := team.Team{Project: dir, Orchestrated: true, Lead: "cto", Members: []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-473"},
		{Role: "qa", Handle: "qa", Binary: "codex", Session: "issue-473"},
	}}

	oldRun := runTmuxLaunchPlanForResume
	oldVerify := verifyResumeExecLaunchRecordsNow
	oldReady := verifyResumeLeadReadyNow
	t.Cleanup(func() {
		runTmuxLaunchPlanForResume = oldRun
		verifyResumeExecLaunchRecordsNow = oldVerify
		verifyResumeLeadReadyNow = oldReady
	})
	var events []string
	verifyResumeLeadReadyNow = func(check resumeExecLaunchCheck) error {
		events = append(events, "ready:"+check.Role)
		return nil
	}
	runTmuxLaunchPlanForResume = func(plan tmuxLaunchPlan) error {
		events = append(events, "run:"+plan.Panes[0].Role)
		return nil
	}
	verifyResumeExecLaunchRecordsNow = func(checks []resumeExecLaunchCheck, _ map[string]resumeExecLaunchSnapshot) []resumeExecLaunchResult {
		events = append(events, "record:"+checks[0].Role)
		return []resumeExecLaunchResult{{Check: checks[0], State: resumeExecLaunchStateLaunched}}
	}
	workerChecks, err := buildResumeExecLaunchChecks(tm, []teamLaunchPane{{Role: "qa", CWD: dir}}, team.DefaultProfile, "issue-473", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runResumeTmuxPlanWithLeadGate(tm, team.DefaultProfile, "issue-473", tmuxLaunchPlan{
		Target: "new-window", Panes: []teamLaunchPane{{Role: "qa"}},
	}, workerChecks, map[string]resumeExecLaunchSnapshot{}, false); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(events, ","); got != "ready:cto,run:qa,record:qa" {
		t.Fatalf("partial-resume order = %s", got)
	}
}

func TestInspectResumeLeadReadyIgnoresLegacyBootstrapExpectation(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agents", "cto")
	root := dir
	now := time.Now().UTC()
	rec := launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", Handle: "cto", Session: "issue-473", Root: root,
		AgentPID: 4242, StartedAt: now, TeamProfile: team.DefaultProfile,
		BootstrapExpectation: &bootstrapack.Expectation{Required: true, LaunchID: "legacy-required"},
		Tmux:                 &launch.TmuxInfo{PaneID: "%7", Session: "squad", Target: "new-window"},
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	oldInspect := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{PaneID: id, Title: paneTitleToken("issue-473", "cto")}, id == "%7"
	}
	t.Cleanup(func() { statusPaneInspector = oldInspect })
	probe := duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == 4242 },
		ProcessMatch: func(_ int, predicate func(string) bool) bool {
			return predicate("codex")
		},
		Now: func() time.Time { return now.Add(time.Second) },
	}
	check := resumeExecLaunchCheck{Role: "cto", Handle: "cto", Binary: "codex", CWD: dir, AgentDir: agentDir, Root: root, Workstream: "issue-473", Profile: team.DefaultProfile}
	if ready, detail := inspectResumeLeadReady(check, probe); !ready || !strings.Contains(detail, "role cto live in pane %7") || strings.Contains(detail, "bootstrap") {
		t.Fatalf("legacy bootstrap expectation affected readiness = %t, %q", ready, detail)
	}
}

// #655: after an in-place lead restart (e.g. Claude Code /upgrade re-execs in
// the same pane), the fresh process rewrites the pane's visible title. The
// readiness gate must corroborate the recorded pane through the live agent's
// controlling tty, report ready, and re-stamp the durable discovery token so
// later checks pass on the primary path again.
func TestInspectResumeLeadReadySurvivesInPlaceLeadRestart(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agents", "lead")
	root := dir
	now := time.Now().UTC()
	rec := launch.Record{
		CWD: dir, Binary: "claude", Role: "lead", Handle: "lead", Session: "issue-655", Root: root,
		AgentPID: 184, AgentTTY: "/dev/ttys011", StartedAt: now, TeamProfile: team.DefaultProfile,
		Tmux: &launch.TmuxInfo{PaneID: "%217", Session: "loco", Target: "new-window"},
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	oldInspect := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		// The upgraded CLI set its own title; no @amq_squad_title option yet.
		return tmuxpane.TmuxPane{PaneID: id, Title: "✳ upgraded CLI title"}, id == "%217"
	}
	oldTTY := statusPaneTTYInspector
	paneTTY := "/dev/ttys011"
	statusPaneTTYInspector = func(id string) (string, bool) {
		return paneTTY, id == "%217"
	}
	oldRestamp := restampPaneDiscoveryToken
	var restamped []string
	restampPaneDiscoveryToken = func(paneID, workstream, role string) error {
		restamped = append(restamped, paneID+"|"+workstream+"|"+role)
		return nil
	}
	t.Cleanup(func() {
		statusPaneInspector = oldInspect
		statusPaneTTYInspector = oldTTY
		restampPaneDiscoveryToken = oldRestamp
	})
	probe := duplicateLaunchProbe{
		PIDAlive:     func(pid int) bool { return pid == 184 },
		ProcessMatch: func(_ int, predicate func(string) bool) bool { return predicate("claude") },
		ProcessTTY:   func(int) (string, bool) { return "/dev/ttys011", true },
		Now:          func() time.Time { return now.Add(time.Second) },
	}
	check := resumeExecLaunchCheck{Role: "lead", Handle: "lead", Binary: "claude", CWD: dir, AgentDir: agentDir, Root: root, Workstream: "issue-655", Profile: team.DefaultProfile}
	if ready, detail := inspectResumeLeadReady(check, probe); !ready || !strings.Contains(detail, "role lead live in pane %217") {
		t.Fatalf("in-place restart readiness = %t, %q", ready, detail)
	}
	if want := []string{"%217|issue-655|lead"}; !reflect.DeepEqual(restamped, want) {
		t.Fatalf("restamped = %v, want %v", restamped, want)
	}

	// A pane on a DIFFERENT pty is not the lead's pane (reused pane id after a
	// tmux server restart): the gate must keep refusing.
	restamped = nil
	paneTTY = "/dev/ttys042"
	if ready, detail := inspectResumeLeadReady(check, probe); ready || !strings.Contains(detail, "lead pane %217 is not live") {
		t.Fatalf("mismatched pane tty readiness = %t, %q", ready, detail)
	}
	if len(restamped) != 0 {
		t.Fatalf("refused pane must not be restamped: %v", restamped)
	}
}

// TestExecResumePlanReportsPartialLaunchRecordFailure covers #208's
// current-window failure mode: tmux accepted a multi-role plan, but one
// requested role never published a fresh launch.json. The command must return
// non-zero with role-level detail instead of leaving the operator with only the
// optimistic "Added team panes" notice.
func TestExecResumePlanReportsPartialLaunchRecordFailure(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)

	oldRun := runTmuxLaunchPlanForResume
	oldTimeout := resumeExecLaunchVerifyTimeout
	oldInterval := resumeExecLaunchVerifyInterval
	oldBudget := resumeExecLaunchStartupBudget
	runTmuxLaunchPlanForResume = func(plan tmuxLaunchPlan) error {
		if plan.Target != "current-window" {
			t.Errorf("target = %q, want current-window", plan.Target)
		}
		if len(plan.Panes) != 2 {
			t.Errorf("panes = %d, want 2", len(plan.Panes))
		}
		writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
			CWD:       dir,
			Binary:    "codex",
			Role:      "cto",
			StartedAt: time.Now().UTC(),
			Tmux:      &launch.TmuxInfo{PaneID: "%101", Session: "squad", Target: "current-window"},
		})
		_, _ = os.Stderr.WriteString("Added 2 team pane(s) to current tmux window.\n")
		return nil
	}
	resumeExecLaunchVerifyTimeout = time.Millisecond
	resumeExecLaunchVerifyInterval = time.Millisecond
	// The frontend-dev member never publishes a record, so without a short
	// startup budget this fixture pays the real 30s boot wait (#688).
	resumeExecLaunchStartupBudget = time.Millisecond
	t.Cleanup(func() {
		runTmuxLaunchPlanForResume = oldRun
		resumeExecLaunchVerifyTimeout = oldTimeout
		resumeExecLaunchVerifyInterval = oldInterval
		resumeExecLaunchStartupBudget = oldBudget
	})

	stdout, stderr, err := captureOutput(t, func() error {
		return execResumePlan(
			team.Team{
				Project: dir,
				Members: []team.Member{
					{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
					{Role: "frontend-dev", Binary: "codex", Handle: "frontend-dev", Session: "issue-96"},
				},
			},
			team.DefaultProfile,
			"issue-96",
			[]resumePlan{
				{Role: "cto", Action: resumeFresh, Command: "amq-squad agent up codex --role cto"},
				{Role: "frontend-dev", Action: resumeFresh, Command: "amq-squad agent up codex --role frontend-dev"},
			},
			resumeExecOptions{Enabled: true, Terminal: "tmux", Target: "current-window", Layout: "tiled"},
			false,
		)
	})
	if err == nil {
		t.Fatal("partial launch record failure should return an error")
	}
	if _, ok := err.(*PartialError); !ok {
		t.Fatalf("want *PartialError, got %T: %v", err, err)
	}
	for _, want := range []string{"Added 2 team pane", "partial launch failure", "frontend-dev", "missing", "launch record"} {
		if !strings.Contains(stderr, want) && !strings.Contains(err.Error(), want) {
			t.Errorf("missing %q in stderr/error\nstdout:\n%s\nstderr:\n%s\nerr:\n%v", want, stdout, stderr, err)
		}
	}
}

func TestVerifyResumeExecLaunchRecordsAdoptsStaleRecordByPaneTitle(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	oldStarted := time.Now().Add(-5 * time.Minute).UTC()
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:       dir,
		Binary:    "codex",
		Role:      "cto",
		Handle:    "cto",
		Session:   "issue-96",
		StartedAt: oldStarted,
		Tmux:      &launch.TmuxInfo{PaneID: "%old", Session: "squad", Target: "current-window"},
	})
	checks := []resumeExecLaunchCheck{{
		Role:       "cto",
		CWD:        dir,
		AgentDir:   filepath.Join(base, "issue-96", "agents", "cto"),
		Handle:     "cto",
		Workstream: "issue-96",
		Root:       filepath.Join(base, "issue-96"),
		Binary:     "codex",
		Profile:    team.DefaultProfile,
	}}
	snapshots := snapshotResumeExecLaunchRecords(checks)
	withStubPaneLister(t, []tmuxpane.TmuxPane{{
		Session:    "squad",
		WindowID:   "@9",
		WindowName: "issue-96",
		PaneID:     "%77",
		Title:      paneTitleToken("issue-96", "cto"),
		Command:    "codex",
		CWD:        dir,
	}}, nil)
	oldTimeout := resumeExecLaunchVerifyTimeout
	oldInterval := resumeExecLaunchVerifyInterval
	resumeExecLaunchVerifyTimeout = time.Millisecond
	resumeExecLaunchVerifyInterval = time.Millisecond
	t.Cleanup(func() {
		resumeExecLaunchVerifyTimeout = oldTimeout
		resumeExecLaunchVerifyInterval = oldInterval
	})

	results := verifyResumeExecLaunchRecords(checks, snapshots)
	if len(results) != 1 || results[0].State != resumeExecLaunchStateLaunched {
		t.Fatalf("verify results = %+v, want launched after pane adoption", results)
	}
	rec, err := launch.Read(checks[0].AgentDir)
	if err != nil {
		t.Fatalf("read adopted record: %v", err)
	}
	if rec.Tmux == nil || rec.Tmux.PaneID != "%77" || rec.Tmux.WindowID != "@9" || rec.Tmux.Target != "adopted" {
		t.Fatalf("adopted tmux = %+v", rec.Tmux)
	}
	if !rec.StartedAt.After(oldStarted) {
		t.Fatalf("StartedAt was not refreshed: got %s, old %s", rec.StartedAt, oldStarted)
	}
}

// TestExecResumePlanRejectsUnknownTerminal makes sure the operator gets a
// clear error rather than a downstream nil-map panic when the terminal
// flag value is wrong.
func TestExecResumePlanRejectsUnknownTerminal(t *testing.T) {
	err := execResumePlan(
		team.Team{Project: t.TempDir(), Members: []team.Member{{Role: "cto"}}},
		team.DefaultProfile,
		"issue-96",
		[]resumePlan{{Role: "cto", Action: resumeRestore, Command: "echo hi"}},
		resumeExecOptions{Enabled: true, Terminal: "screen", Target: "current-window", Layout: "vertical"},
		false,
	)
	if err == nil || !strings.Contains(err.Error(), "unsupported terminal") {
		t.Fatalf("expected unsupported-terminal error; got %v", err)
	}
}

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

// The lead-main arrangement fires ONLY on the mid-run member-add signature:
// orchestrated, current-window, lead already live (not in the plan), AND the
// resume invoked from the lead's own recorded pane. Every other shape —
// lead-only plan, lead being relaunched, a resume issued from an operator or
// worker pane, or a --skip-lead-check bypass — clears the candidate flag
// before the tmux plan runs.
func TestRunResumeTmuxPlanLeadMainOnlyForDependentOnlyLaunch(t *testing.T) {
	oldRun := runTmuxLaunchPlanForResume
	oldVerify := verifyResumeExecLaunchRecordsNow
	oldReady := verifyResumeLeadReadyNow
	oldFromLead := resumeLaunchedFromLeadPaneNow
	t.Cleanup(func() {
		runTmuxLaunchPlanForResume = oldRun
		verifyResumeExecLaunchRecordsNow = oldVerify
		verifyResumeLeadReadyNow = oldReady
		resumeLaunchedFromLeadPaneNow = oldFromLead
	})
	var flags []bool
	runTmuxLaunchPlanForResume = func(plan tmuxLaunchPlan) error {
		flags = append(flags, plan.LeadMainCurrentWindow)
		return nil
	}
	verifyResumeExecLaunchRecordsNow = func(checks []resumeExecLaunchCheck, _ map[string]resumeExecLaunchSnapshot) []resumeExecLaunchResult {
		out := make([]resumeExecLaunchResult, 0, len(checks))
		for _, check := range checks {
			out = append(out, resumeExecLaunchResult{Check: check, State: resumeExecLaunchStateLaunched})
		}
		return out
	}
	verifyResumeLeadReadyNow = func(resumeExecLaunchCheck) error { return nil }
	launchedFromLeadPane := true
	resumeLaunchedFromLeadPaneNow = func(resumeExecLaunchCheck) bool { return launchedFromLeadPane }

	base := tmuxLaunchPlan{Target: "current-window", LeadMainCurrentWindow: true}
	tm := team.Team{Orchestrated: true, Lead: "cto"}

	// Mid-run add: dependents only, invoked from the lead pane — flag survives.
	plan := base
	plan.Panes = []teamLaunchPane{{Role: "researcher"}}
	if _, err := runResumeTmuxPlanWithLeadGate(tm, team.DefaultProfile, "s", plan,
		[]resumeExecLaunchCheck{{Role: "researcher", Handle: "researcher"}, {Role: "cto", Handle: "cto"}},
		map[string]resumeExecLaunchSnapshot{}, false); err != nil {
		t.Fatal(err)
	}

	// Lead-only plan: cleared.
	plan = base
	plan.Panes = []teamLaunchPane{{Role: "cto"}}
	if _, err := runResumeTmuxPlanWithLeadGate(tm, team.DefaultProfile, "s", plan,
		[]resumeExecLaunchCheck{{Role: "cto", Handle: "cto"}},
		map[string]resumeExecLaunchSnapshot{}, false); err != nil {
		t.Fatal(err)
	}

	// Lead + dependents relaunch: cleared on both the lead and dependent plans.
	plan = base
	plan.Panes = []teamLaunchPane{{Role: "cto"}, {Role: "qa"}}
	if _, err := runResumeTmuxPlanWithLeadGate(tm, team.DefaultProfile, "s", plan,
		[]resumeExecLaunchCheck{{Role: "cto", Handle: "cto"}, {Role: "qa", Handle: "qa"}},
		map[string]resumeExecLaunchSnapshot{}, false); err != nil {
		t.Fatal(err)
	}

	// Dependents only, but the resume is NOT running in the lead's recorded
	// pane (operator shell, worker pane): the lead is live somewhere, yet
	// arranging main-vertical here would rearrange the CALLER's window.
	launchedFromLeadPane = false
	plan = base
	plan.Panes = []teamLaunchPane{{Role: "researcher"}}
	if _, err := runResumeTmuxPlanWithLeadGate(tm, team.DefaultProfile, "s", plan,
		[]resumeExecLaunchCheck{{Role: "researcher", Handle: "researcher"}, {Role: "cto", Handle: "cto"}},
		map[string]resumeExecLaunchSnapshot{}, false); err != nil {
		t.Fatal(err)
	}

	// --skip-lead-check: no live lead verified at all, so the flag must not
	// survive even when the pane tie would have matched.
	launchedFromLeadPane = true
	plan = base
	plan.Panes = []teamLaunchPane{{Role: "researcher"}}
	if _, err := runResumeTmuxPlanWithLeadGate(tm, team.DefaultProfile, "s", plan,
		[]resumeExecLaunchCheck{{Role: "researcher", Handle: "researcher"}, {Role: "cto", Handle: "cto"}},
		map[string]resumeExecLaunchSnapshot{}, true); err != nil {
		t.Fatal(err)
	}

	want := []bool{true, false, false, false, false, false}
	if len(flags) != len(want) {
		t.Fatalf("plan runs = %d, want %d (%v)", len(flags), len(want), flags)
	}
	for i, flag := range flags {
		if flag != want[i] {
			t.Fatalf("plan run %d LeadMainCurrentWindow = %t, want %t (%v)", i, flag, want[i], flags)
		}
	}
}

// resumeLaunchedFromLeadPane ties the arrangement to REAL identity evidence:
// the lead launch record's pane id must equal the invoking $TMUX_PANE, and
// every missing side fails closed.
func TestResumeLaunchedFromLeadPaneComparesRecordedPane(t *testing.T) {
	dir := t.TempDir()
	writeRecord := func(paneID string) {
		rec := launch.Record{Handle: "cto", Role: "cto", Tmux: &launch.TmuxInfo{PaneID: paneID}}
		data, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		path := launch.Path(dir)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	check := resumeExecLaunchCheck{Role: "cto", Handle: "cto", AgentDir: dir}

	writeRecord("%7")
	t.Setenv("TMUX_PANE", "%7")
	if !resumeLaunchedFromLeadPane(check) {
		t.Fatal("matching recorded pane and TMUX_PANE not recognized")
	}

	t.Setenv("TMUX_PANE", "%9")
	if resumeLaunchedFromLeadPane(check) {
		t.Fatal("mismatched TMUX_PANE accepted as the lead pane")
	}

	t.Setenv("TMUX_PANE", "")
	if resumeLaunchedFromLeadPane(check) {
		t.Fatal("resume outside tmux accepted as the lead pane")
	}

	t.Setenv("TMUX_PANE", "%7")
	writeRecord("")
	if resumeLaunchedFromLeadPane(check) {
		t.Fatal("record without a pane id accepted as the lead pane")
	}

	if resumeLaunchedFromLeadPane(resumeExecLaunchCheck{Role: "cto", Handle: "cto", AgentDir: filepath.Join(dir, "missing")}) {
		t.Fatal("missing launch record accepted as the lead pane")
	}
}
