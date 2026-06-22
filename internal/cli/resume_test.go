package cli

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

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

// TestExecResumePlanRejectsUnknownTerminal makes sure the operator gets a
// clear error rather than a downstream nil-map panic when the terminal
// flag value is wrong.
func TestExecResumePlanRejectsUnknownTerminal(t *testing.T) {
	err := execResumePlan(
		team.Team{Project: t.TempDir(), Members: []team.Member{{Role: "cto"}}},
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
