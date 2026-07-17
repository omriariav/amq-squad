package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/bootstrapack"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func TestRunResumeEffortIsFreshOnlyAndJSONSafe(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{{
			Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96",
			ClaudeArgs: []string{"--chrome", "--effort", "low"},
		}},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runResume([]string{"--json", "--effort", "qa=FutureTier"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(stdout)) {
		t.Fatalf("resume JSON is polluted:\n%s", stdout)
	}
	if !strings.Contains(stdout, "FutureTier") || strings.Contains(stdout, "--effort low") {
		t.Fatalf("fresh command did not replace stored effort exactly:\n%s", stdout)
	}
	if strings.Count(stderr, "not in the merged catalog") != 1 {
		t.Fatalf("warning count/stderr = %q", stderr)
	}
	stored, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(stored.Members[0].ClaudeArgs, " "); got != "--chrome --effort low" {
		t.Fatalf("resume preview mutated profile args: %q", got)
	}

	writeMemberLaunchRecord(t, base, "issue-96", "qa", launch.Record{
		CWD: dir, Binary: "claude", Role: "qa", StartedAt: time.Now(),
	})
	_, _, err = captureOutput(t, func() error {
		return runResume([]string{"--effort", "qa=max"})
	})
	if err == nil || !strings.Contains(err.Error(), "qa (restore)") || !strings.Contains(err.Error(), "only to launch-fresh") {
		t.Fatalf("restore-target effort error = %v", err)
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

func TestRunResumeEffortPreviewExecCommandParity(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{{
			Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96",
			ClaudeArgs: []string{"--chrome", "--effort", "low"},
		}},
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(team.ProfilePath(dir, team.DefaultProfile))
	if err != nil {
		t.Fatal(err)
	}
	preview, _, err := captureOutput(t, func() error {
		return runResume([]string{"--effort", "qa=max"})
	})
	if err != nil {
		t.Fatal(err)
	}

	oldRun := runTmuxLaunchPlanForResume
	oldVerify := verifyResumeExecLaunchRecordsNow
	var executed tmuxLaunchPlan
	runTmuxLaunchPlanForResume = func(plan tmuxLaunchPlan) error {
		executed = plan
		return nil
	}
	verifyResumeExecLaunchRecordsNow = func(checks []resumeExecLaunchCheck, _ map[string]resumeExecLaunchSnapshot) []resumeExecLaunchResult {
		results := make([]resumeExecLaunchResult, 0, len(checks))
		for _, check := range checks {
			results = append(results, resumeExecLaunchResult{Check: check, State: resumeExecLaunchStateLaunched})
		}
		return results
	}
	t.Cleanup(func() {
		runTmuxLaunchPlanForResume = oldRun
		verifyResumeExecLaunchRecordsNow = oldVerify
	})
	if _, _, err := captureOutput(t, func() error {
		return runResume([]string{"--exec", "--stagger", "0", "--effort", "qa=max"})
	}); err != nil {
		t.Fatal(err)
	}
	if len(executed.Panes) != 1 {
		t.Fatalf("executed panes = %+v", executed.Panes)
	}
	command := executed.Panes[0].Command
	if !strings.Contains(command, "--chrome") || !strings.Contains(command, "--effort max") || strings.Contains(command, "--effort low") {
		t.Fatalf("executed effort command = %q", command)
	}
	if !strings.Contains(preview, command) {
		t.Fatalf("preview/exec command mismatch:\npreview:\n%s\nexec: %s", preview, command)
	}
	after, err := os.ReadFile(team.ProfilePath(dir, team.DefaultProfile))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("resume preview/exec effort override persisted to the profile")
	}
}

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
func TestRunResumeMatchesTeamResumePlannerRows(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "fullstack", Binary: "claude", Handle: "fullstack", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})
	teamOut, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("team resume: %v", err)
	}
	resumeOut, _, err := captureOutput(t, func() error { return runResume(nil) })
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if extractPlanRows(teamOut) != extractPlanRows(resumeOut) {
		t.Fatalf("top-level resume diverged from team resume on the plan rows.\nteam resume:\n%s\nresume:\n%s", teamOut, resumeOut)
	}
}

func TestRunResumeOutputUsesTopLevelLabels(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members:    []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	}); err != nil {
		t.Fatal(err)
	}
	stdout, _, err := captureOutput(t, func() error { return runResume(nil) })
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if !strings.Contains(stdout, "# amq-squad resume") {
		t.Errorf("top-level resume header missing 'amq-squad resume':\n%s", stdout)
	}
	if strings.Contains(stdout, "amq-squad team resume") {
		t.Errorf("top-level resume must not surface 'amq-squad team resume':\n%s", stdout)
	}
	if strings.Contains(stdout, "team launch") {
		t.Errorf("top-level resume must not suggest 'team launch':\n%s", stdout)
	}
	if !strings.Contains(stdout, "plan-only") || !strings.Contains(stdout, "amq-squad resume --exec --session issue-96") {
		t.Errorf("top-level resume should show the exec follow-up:\n%s", stdout)
	}
}

func TestRunResumeProjectTargetsOtherDir(t *testing.T) {
	setupFakeAMQSessionRoots(t)
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
	for _, want := range []string{"# amq-squad resume", "# team-home:  " + project, "# workstream: issue-99"} {
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
	setupFakeAMQSessionRoots(t)
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
	rows := extractPlanRows(stdout)
	for _, want := range []string{"fullstack", "qa"} {
		if !strings.Contains(rows, want) {
			t.Fatalf("plan rows missing selected role %q:\n%s", want, rows)
		}
	}
	if strings.Contains(rows, "cto") {
		t.Fatalf("unselected role cto must not appear in the plan rows:\n%s", rows)
	}
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
	setupFakeAMQSessionRoots(t)
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

func TestRunResumeRestoreExistingPropagates(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
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
				}}, checks, map[string]resumeExecLaunchSnapshot{},
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
		checks, map[string]resumeExecLaunchSnapshot{},
	)
	if err == nil || !strings.Contains(err.Error(), "lead readiness failed for cto") || !strings.Contains(err.Error(), "dependent roles were not launched") {
		t.Fatalf("readiness error = %v", err)
	}
	if got := strings.Join(submitted, ","); got != "cto" {
		t.Fatalf("submitted roles = %q, want lead only", got)
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
	}, workerChecks, map[string]resumeExecLaunchSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(events, ","); got != "ready:cto,run:qa,record:qa" {
		t.Fatalf("partial-resume order = %s", got)
	}
}

func TestInspectResumeLeadReadyRequiresLivePaneAndMatchingBootstrapWhenRequired(t *testing.T) {
	dir := t.TempDir()
	agentDir := filepath.Join(dir, "agents", "cto")
	root := dir
	now := time.Now().UTC()
	expect, err := bootstrapack.NewExpectation(true, now)
	if err != nil {
		t.Fatal(err)
	}
	rec := launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", Handle: "cto", Session: "issue-473", Root: root,
		AgentPID: 4242, StartedAt: now, TeamProfile: team.DefaultProfile, BootstrapExpectation: &expect,
		Tmux: &launch.TmuxInfo{PaneID: "%7", Session: "squad", Target: "new-window"},
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	oldInspect := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		return tmuxpane.TmuxPane{PaneID: id}, id == "%7"
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
	if ready, detail := inspectResumeLeadReady(check, probe); ready || !strings.Contains(detail, "bootstrap acknowledgement pending") {
		t.Fatalf("unacknowledged readiness = %t, %q", ready, detail)
	}
	if err := bootstrapack.Write(agentDir, bootstrapack.Marker{
		LaunchID: expect.LaunchID, PromptVersion: expect.PromptVersion, AcknowledgedAt: now.Add(time.Second),
		Handle: "cto", Role: "cto", Profile: team.DefaultProfile, Session: "issue-473", Root: root,
		SkillVersion: "2.21.0", Steps: append([]string(nil), bootstrapack.RequiredSteps...),
	}); err != nil {
		t.Fatal(err)
	}
	if ready, detail := inspectResumeLeadReady(check, probe); !ready {
		t.Fatalf("acknowledged readiness = %t, %q", ready, detail)
	}

	noBootstrap, err := bootstrapack.NewExpectation(false, now)
	if err != nil {
		t.Fatal(err)
	}
	rec.BootstrapExpectation = &noBootstrap
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	if ready, detail := inspectResumeLeadReady(check, probe); !ready || !strings.Contains(detail, "bootstrap=not_required") {
		t.Fatalf("no-bootstrap readiness = %t, %q", ready, detail)
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
	t.Cleanup(func() {
		runTmuxLaunchPlanForResume = oldRun
		resumeExecLaunchVerifyTimeout = oldTimeout
		resumeExecLaunchVerifyInterval = oldInterval
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
	setupFakeAMQSessionRoots(t)
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
