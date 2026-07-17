package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
	runwizard "github.com/omriariav/amq-squad/v2/internal/wizard"
)

// writeMemberLaunchRecord drops a v0.6 launch.json under the fake AMQ base
// for the given session/handle so the resume planner can find it.
func writeMemberLaunchRecord(t *testing.T, base, session, handle string, rec launch.Record) {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec.Handle = handle
	rec.Session = session
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatalf("write launch record: %v", err)
	}
}

func resumeChdir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(old); err != nil {
			t.Errorf("restore cwd: %v", err)
		}
	})
}

func TestRunTeamResumeNoTeamConfigErrors(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)
	_, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err == nil {
		t.Fatal("resume without team.json should fail")
	}
	if !strings.Contains(err.Error(), "no team configured") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunTeamResumeAllRestoreClassifiesEveryMember(t *testing.T) {
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
		CWD: dir, Binary: "codex", Role: "cto", Argv: nil,
		StartedAt: time.Now(),
	})
	writeMemberLaunchRecord(t, base, "issue-96", "fullstack", launch.Record{
		CWD: dir, Binary: "claude", Role: "fullstack",
		Argv:      []string{"--permission-mode", "auto"},
		StartedAt: time.Now(),
	})

	stdout, stderr, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{
		"# amq-squad team resume",
		"# workstream: issue-96",
		"cto",
		"fullstack",
		"restore",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "launch fresh") {
		t.Errorf("all-restore plan should not contain 'launch fresh':\n%s", stdout)
	}
}

func TestRunTeamResumeAllFreshWhenNoRecords(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
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

	stdout, stderr, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, "launch fresh") {
		t.Errorf("no-record team should plan launch fresh:\n%s", stdout)
	}
	for _, want := range []string{
		"--trust approve-for-me",
		"--sandbox workspace-write",
		"--ask-for-approval on-request",
		"approvals_reviewer=\"auto_review\"",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("no-Trust resume output missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "  restore  ") || strings.Contains(stdout, "\trestore\t") {
		t.Errorf("no-record team should not surface restore action:\n%s", stdout)
	}
}

func TestPlanMemberResumeIsolatesForeignProfileLaunchRecord(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	writeMemberLaunchRecord(t, base, "main", "cto", launch.Record{
		CWD:         dir,
		Binary:      "codex",
		Role:        "cto",
		AgentPID:    5555,
		TeamProfile: "product",
		StartedAt:   time.Now(),
	})
	probe := duplicateLaunchProbe{
		PIDAlive:     func(pid int) bool { return pid == 5555 },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return pid == 5555 },
		Now:          time.Now,
	}
	member := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "main"}
	plan, err := planMemberResume(memberPlanInput{
		Member:     member,
		Team:       team.Team{Project: dir, Members: []team.Member{member}},
		Workstream: "main",
		Profile:    "release",
		SquadBin:   "amq-squad",
		Probe:      probe,
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Action != resumeFresh || !strings.Contains(plan.Command, ".agent-mail/release/main") {
		t.Fatalf("foreign-profile launch should be isolated and plan fresh in release namespace, got %+v", plan)
	}
}

func TestPlanMemberResumeFingerprintsExactNewestMatchingRecord(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	older := launch.Record{CWD: dir, Binary: "codex", Role: "cto", Handle: "cto", Session: "s", StartedAt: time.Now().Add(-time.Hour), Conversation: "older"}
	writeMemberLaunchRecord(t, base, "s", "cto", older)
	newer := older
	newer.StartedAt = time.Now()
	newer.Conversation = "newer"
	newer.Model = "saved-model"
	secretPrompt := "TOP-SECRET-PROMPT\n\x1b]52;c;clipboard\x07" + strings.Repeat("x", 2000)
	newer.Argv = []string{"codex", "-c", "model_reasoning_effort=xhigh", secretPrompt}
	newer.CodexArgs = []string{"-c", "model_reasoning_effort=xhigh", "-m", "saved-model", "--search"}
	newerDir := filepath.Join(base, "s", "agents", "cto-newer")
	if err := launch.Write(newerDir, newer); err != nil {
		t.Fatal(err)
	}
	member := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"}
	plan, err := planMemberResume(memberPlanInput{
		Member: member, Team: team.Team{Project: dir, Members: []team.Member{member}}, Workstream: "s",
		Profile: team.DefaultProfile, SquadBin: "amq-squad", Probe: duplicateLaunchProbe{PIDAlive: func(int) bool { return false }, ProcessMatch: func(int, func(string) bool) bool { return false }, Now: time.Now},
	})
	if err != nil {
		t.Fatal(err)
	}
	newerStored, err := launch.Read(newerDir)
	if err != nil {
		t.Fatal(err)
	}
	olderStored, err := launch.Read(filepath.Join(base, "s", "agents", "cto"))
	if err != nil {
		t.Fatal(err)
	}
	want, olderID := resumeSavedLaunchIdentity(newerStored), resumeSavedLaunchIdentity(olderStored)
	if plan.Action != resumeRestore || plan.SavedLaunchIdentity != want || plan.SavedLaunchIdentity == olderID {
		t.Fatalf("planner record evidence = action %s identity %q; want newest %q, not older %q", plan.Action, plan.SavedLaunchIdentity, want, olderID)
	}
	if plan.Saved == nil || plan.Saved.Binary != "codex" || plan.Saved.Model != "saved-model" || plan.Saved.Effort != "xhigh" || !reflect.DeepEqual(plan.Saved.NativeArgs, []string{"--search"}) {
		t.Fatalf("planner structured saved evidence = %+v, record=%+v", plan.Saved, newerStored)
	}
	summary := discoverRunStartWizardSession(team.Team{Project: dir, Members: []team.Member{member}}, team.DefaultProfile, "s", runwizard.SessionSourceMemberPin, []string{"s"}, nil)
	if len(summary.Members) != 1 || !reflect.DeepEqual(summary.Members[0].SavedNativeArgs, []string{"--search"}) {
		t.Fatalf("wizard saved args = %+v", summary.Members)
	}
	renderedSummary := fmt.Sprintf("%+v %s", summary, runwizard.FormatSavedNativeArgs(summary.Members[0].SavedNativeArgs))
	if strings.Contains(renderedSummary, "TOP-SECRET-PROMPT") || strings.ContainsAny(renderedSummary, "\n\x1b\x07") || len(renderedSummary) > 2000 {
		t.Fatalf("saved prompt/control leaked into wizard summary: %q", renderedSummary)
	}
	evidence := runStartWizardDiscoveryMemberPlan(plan, runwizard.MemberActionRestore)
	if evidence.SavedLaunchIdentity != want {
		t.Fatalf("wizard fingerprint evidence = %+v, want selected identity %q", evidence, want)
	}
	selected := runwizard.DiscoveryFingerprint(runwizard.DiscoveryFingerprintInput{MemberPlans: []runwizard.DiscoveryMemberPlan{evidence}})
	evidence.SavedLaunchIdentity = olderID
	if got := runwizard.DiscoveryFingerprint(runwizard.DiscoveryFingerprintInput{MemberPlans: []runwizard.DiscoveryMemberPlan{evidence}}); got == selected {
		t.Fatal("changing the planner-selected record identity did not change the wizard fingerprint")
	}
}

func TestWizardSavedExtraArgsStripsSeparatelyRenderedModelAndEffort(t *testing.T) {
	codex := []string{"-m", "one", "-m=two", "--model", "three", "--model=four", "-c", "model=x", "--config=model=y", "-cmodel=z", "-c=model_reasoning_effort=high", "--config=model_reasoning_effort=low", "-cmodel_reasoning_effort=max", "--search", "secret positional prompt"}
	if got, want := wizardSavedExtraArgs("codex", codex), []string{"--search"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("codex saved extras=%#v want=%#v", got, want)
	}
	claude := []string{"--model", "opus", "--model=sonnet", "--effort", "high", "--effort=medium", "--chrome", "--", "prompt"}
	if got, want := wizardSavedExtraArgs("claude", claude), []string{"--chrome"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("claude saved extras=%#v want=%#v", got, want)
	}
}

func TestRunTeamResumeProjectTargetsOtherDir(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, other)

	if err := team.Write(project, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--project", project})
	})
	if err != nil {
		t.Fatalf("team resume --project: %v", err)
	}
	if !strings.Contains(stdout, "cd "+shellQuote(project)) {
		t.Errorf("team resume --project should emit commands for requested project:\n%s", stdout)
	}
	if strings.Contains(stdout, "cd "+shellQuote(other)) {
		t.Errorf("team resume --project should not emit commands for current cwd:\n%s", stdout)
	}
}

func TestRunTeamResumeMixedTeam(t *testing.T) {
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

	stdout, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v", err)
	}
	if !strings.Contains(stdout, "restore") {
		t.Errorf("mixed team should mark cto as restore:\n%s", stdout)
	}
	if !strings.Contains(stdout, "launch fresh") {
		t.Errorf("mixed team should mark fullstack as launch fresh:\n%s", stdout)
	}
}

func TestRunTeamResumeLiveMemberSuppressesCommand(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Plant a wake.lock with our own PID so PIDAlive returns true and the
	// process command matches the wake matcher.
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	myPID := os.Getpid()
	writeWakeLock(t, agentDir, wakeLockFile{PID: myPID, Root: filepath.Join(base, "issue-96")})

	// Substitute the probe so we don't depend on ps args matching.
	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == myPID },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			return predicate("amq wake --me cto --root " + filepath.Join(base, "issue-96"))
		},
		Now: time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	stdout, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v", err)
	}
	if !strings.Contains(stdout, "live") {
		t.Errorf("live wake should mark member as live:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no command") {
		t.Errorf("live member should suppress its launch command:\n%s", stdout)
	}
	// Lock must remain on disk: planning is non-mutating.
	if _, err := os.Stat(wakeLockPath(agentDir)); err != nil {
		t.Errorf("resume must not remove the live wake lock: %v", err)
	}
}

func TestRunTeamResumeLiveMemberWithRelativeAMQRootFromOtherCWD(t *testing.T) {
	project := t.TempDir()
	other := t.TempDir()
	setupFakeAMQRelativeSessionRoots(t)
	resumeChdir(t, other)

	if err := team.Write(project, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	agentDir := filepath.Join(project, ".agent-mail", "issue-96", "agents", "cto")
	myPID := os.Getpid()
	writeWakeLock(t, agentDir, wakeLockFile{PID: myPID, Root: ".agent-mail/issue-96"})

	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == myPID },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			return pid == myPID && predicate("amq wake --me cto --root .agent-mail/issue-96")
		},
		Now: time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--project", project})
	})
	if err != nil {
		t.Fatalf("runTeamResume --project: %v", err)
	}
	if !strings.Contains(stdout, "live") {
		t.Errorf("relative-root live wake should mark member as live:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no command") {
		t.Errorf("live relative-root member should suppress its launch command:\n%s", stdout)
	}
	if strings.Contains(stdout, "launch fresh") {
		t.Errorf("live relative-root member must not classify as launch fresh:\n%s", stdout)
	}
}

func TestRunTeamResumeLiveReplacementPaneSuppressesCommand(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 4242, StartedAt: time.Now(),
	})
	paneCWD := canonicalPath(dir)
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "main", Window: "0", Pane: "3", Command: "codex", CWD: paneCWD},
	}, nil)

	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive:     func(pid int) bool { return false },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool { return false },
		Now:          time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	stdout, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v", err)
	}
	if !strings.Contains(stdout, "live") || !strings.Contains(stdout, "recorded pid dead") {
		t.Errorf("replacement pane should classify as live with re-register note:\n%s", stdout)
	}
	if !strings.Contains(stdout, "no command") {
		t.Errorf("replacement pane should suppress restore command:\n%s", stdout)
	}
	if strings.Contains(stdout, "agent up codex") {
		t.Errorf("replacement pane must not emit a restore command without force:\n%s", stdout)
	}
}

func TestRunTeamResumeDoesNotSuppressEveryStaleRoleWithOneReplacementPane(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "codex", Handle: "qa", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	for i, role := range []string{"cto", "qa"} {
		writeMemberLaunchRecord(t, base, "issue-96", role, launch.Record{
			CWD: dir, Binary: "codex", Role: role, Handle: role, AgentPID: 4242 + i, StartedAt: time.Now(),
		})
	}
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "main", Window: "0", Pane: "3", Command: "codex", CWD: canonicalPath(dir)},
	}, nil)

	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = livenessProbe(map[int]bool{}, map[int]bool{}, time.Now())
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	stdout, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v", err)
	}
	if got := strings.Count(stdout, "recorded pid dead"); got != 1 {
		t.Fatalf("replacement-live notes = %d, want 1:\n%s", got, stdout)
	}
	if got := strings.Count(stdout, "- no command"); got != 1 {
		t.Fatalf("suppressed commands = %d, want 1:\n%s", got, stdout)
	}
	if !strings.Contains(stdout, "amq-squad agent up codex") {
		t.Fatalf("the unassigned stale role must retain a restore command:\n%s", stdout)
	}
}

func TestRunTeamResumeForceDuplicateReplacementPaneEmitsRestoreCommand(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", AgentPID: 4242, StartedAt: time.Now(),
	})
	paneCWD := canonicalPath(dir)
	withStubPaneLister(t, []tmuxpane.TmuxPane{
		{Session: "main", Window: "0", Pane: "3", Command: "codex", CWD: paneCWD},
	}, nil)

	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive:     func(pid int) bool { return false },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool { return false },
		Now:          time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--force-duplicate"})
	})
	if err != nil {
		t.Fatalf("runTeamResume --force-duplicate: %v", err)
	}
	if !strings.Contains(stdout, "force-duplicate: recorded pid dead") {
		t.Errorf("forced replacement pane should preserve warning note:\n%s", stdout)
	}
	if !strings.Contains(stdout, "amq-squad agent up codex --force-duplicate") {
		t.Errorf("forced replacement pane should emit forced restore command:\n%s", stdout)
	}
}

func TestRunTeamResumeFreshIgnoresRestoreRecords(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: ""},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--fresh", "--session", "issue-97"})
	})
	if err != nil {
		t.Fatalf("runTeamResume --fresh: %v", err)
	}
	if !strings.Contains(stdout, "launch fresh") {
		t.Errorf("--fresh should plan launch fresh, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "restore") {
		// The 'restore' substring will appear in the printed --restore-existing
		// help text or other parts; check that the action column does not say
		// restore. The action column is between two tabs in the planning row.
		if strings.Contains(stdout, "\trestore\t") {
			t.Errorf("--fresh should not classify member as restore:\n%s", stdout)
		}
	}
}

func TestRunTeamResumeRestoreExistingRequiresRecords(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	_, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--restore-existing"})
	})
	if err == nil {
		t.Fatal("--restore-existing without records should error")
	}
	if !strings.Contains(err.Error(), "restorable") {
		t.Fatalf("error should mention restorable: %v", err)
	}
}

// Regression for #27 P1: a sibling repo with the same role/handle/session
// must not leak its restore command into this team's plan. The newer foreign
// record must be ignored in favor of the cwd-matching one (or fall through
// to launch fresh when no cwd-matching record exists).
func TestRunTeamResumeIgnoresForeignRepoRecordWithSameRoleHandleSession(t *testing.T) {
	dir := t.TempDir()
	foreignDir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Older record from THIS repo.
	older := time.Now().Add(-1 * time.Hour)
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: older,
	})
	// Newer record from a foreign repo, same role/handle/session. Even
	// though it is the most recent, planMemberResume must reject it on
	// cwd grounds and fall back to the cwd-matching older record.
	foreignAgentDir := filepath.Join(base, "issue-96", "agents", "cto-foreign")
	if err := os.MkdirAll(foreignAgentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := launch.Write(foreignAgentDir, launch.Record{
		CWD: foreignDir, Binary: "codex", Role: "cto", Handle: "cto", Session: "issue-96",
		StartedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v", err)
	}
	if !strings.Contains(stdout, dir) {
		t.Errorf("plan should reference current cwd %q, got:\n%s", dir, stdout)
	}
	if strings.Contains(stdout, foreignDir) {
		t.Errorf("plan must not reference foreign cwd %q, got:\n%s", foreignDir, stdout)
	}
}

// Regression for #27 P2: when no launch record exists, the wake-health
// fallback must not report pid:N for a stale lock whose PID was reused by
// an unrelated process.
func TestWakeHealthForMemberRejectsForeignPIDReuse(t *testing.T) {
	agentDir := t.TempDir()
	expectedRoot := "/abs/proj/.agent-mail/issue-96"
	writeWakeLock(t, agentDir, wakeLockFile{PID: 4321, Root: expectedRoot})

	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == 4321 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			// Unrelated process: a node script that happens to have pid 4321.
			return predicate("/usr/local/bin/node /path/to/some/server.js")
		},
		Now: time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	got := wakeHealthForMember(agentDir, expectedRoot, "cto", launch.Record{}, false)
	if got != "stale" {
		t.Errorf("PID-reused lock with unrelated command must classify as stale, got %q", got)
	}
}

// Regression for #27 P2 (continued): a foreign-root wake (real `amq wake`
// process but wrong workstream) must also classify as stale.
func TestWakeHealthForMemberRejectsForeignRootWake(t *testing.T) {
	agentDir := t.TempDir()
	expectedRoot := "/abs/proj/.agent-mail/issue-96"
	writeWakeLock(t, agentDir, wakeLockFile{PID: 7777, Root: expectedRoot})

	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == 7777 },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			return predicate("amq wake --me cto --root /other/proj/.agent-mail/issue-96")
		},
		Now: time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	got := wakeHealthForMember(agentDir, expectedRoot, "cto", launch.Record{}, false)
	if got != "stale" {
		t.Errorf("foreign-root wake must classify as stale, got %q", got)
	}
}

// Regression for #27 P3: a member whose AMQ env cannot be resolved must
// classify as blocked, not silently 'launch fresh'. We trigger env failure
// by pointing PATH at an empty dir so 'amq env --json' is unfindable.
func TestRunTeamResumeBlockedOnEnvFailure(t *testing.T) {
	dir := t.TempDir()
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())

	stdout, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v", err)
	}
	if !strings.Contains(stdout, "blocked") {
		t.Errorf("env failure should classify as blocked, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "\tlaunch fresh\t") {
		t.Errorf("env failure must not silently classify as launch fresh:\n%s", stdout)
	}
}

// Regression for #27 P1 (addendum): --fresh --session <existing> must
// reject when the workstream already has restorable launch records, unless
// --force-duplicate is set. Without this guard, fresh would silently
// overwrite saved launch.json metadata.
func TestRunTeamResumeFreshRejectsExistingWorkstreamUnlessForced(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	_, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--fresh", "--session", "issue-96"})
	})
	if err == nil {
		t.Fatal("--fresh into existing workstream should error without --force-duplicate")
	}
	if !strings.Contains(err.Error(), "force-duplicate") {
		t.Fatalf("error should mention --force-duplicate, got %v", err)
	}

	// With --force-duplicate, the same call should succeed and emit fresh.
	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--fresh", "--session", "issue-96", "--force-duplicate"})
	})
	if err != nil {
		t.Fatalf("--fresh --force-duplicate should succeed: %v", err)
	}
	if !strings.Contains(stdout, "launch fresh") {
		t.Errorf("--fresh --force-duplicate should still classify as launch fresh, got:\n%s", stdout)
	}
}

// Regression: --fresh --session <existing-root> must reject when the
// workstream's AMQ root already contains mailbox state, even if no
// member has a matching launch record. Matches `team launch --fresh`
// semantics so a fresh resume cannot silently reuse mailbox dirs.
func TestRunTeamResumeFreshRejectsExistingWorkstreamRootWithoutRecords(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	// Pre-create the workstream root with mailbox state but NO matching
	// launch.json so the recordCount guard alone would not trip.
	if err := os.MkdirAll(filepath.Join(base, "issue-96", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}

	_, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--fresh", "--session", "issue-96"})
	})
	if err == nil {
		t.Fatal("--fresh into existing workstream root should error without --force-duplicate")
	}
	if !strings.Contains(err.Error(), "force-duplicate") {
		t.Fatalf("error should mention --force-duplicate, got %v", err)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error should mention existing workstream root, got %v", err)
	}

	// With --force-duplicate, the same call must succeed.
	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--fresh", "--session", "issue-96", "--force-duplicate"})
	})
	if err != nil {
		t.Fatalf("--fresh --force-duplicate should succeed: %v", err)
	}
	if !strings.Contains(stdout, "launch fresh") {
		t.Errorf("forced resume should still classify member as launch fresh:\n%s", stdout)
	}
}

// Regression for #27 P2 (addendum): --restore-existing must check for
// restorable record existence, not whether the final action came out as
// restore. A live member that matches a record still satisfies the
// contract because the record exists and would replay if the live
// instance went away.
func TestRunTeamResumeRestoreExistingPassesWhenLiveMemberHasRecord(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	// Plant a live wake for this member so the final action is 'live',
	// not 'restore'. The record still exists; --restore-existing must pass.
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	myPID := os.Getpid()
	writeWakeLock(t, agentDir, wakeLockFile{PID: myPID, Root: filepath.Join(base, "issue-96")})
	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == myPID },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			return predicate("amq wake --me cto --root " + filepath.Join(base, "issue-96"))
		},
		Now: time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--restore-existing"})
	})
	if err != nil {
		t.Fatalf("--restore-existing with a recorded-but-live member should not fail: %v", err)
	}
	if !strings.Contains(stdout, "live") {
		t.Errorf("plan should still classify the member as live:\n%s", stdout)
	}
}

// Regression for #27 P1 (force-duplicate restore command must inject the
// flag): when a member has both a restorable record and a live wake, the
// emitted restore command under --force-duplicate must contain
// --force-duplicate so it bypasses launch-time preflight on replay.
func TestRunTeamResumeForceDuplicateRestoreCommandHasFlag(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	myPID := os.Getpid()
	writeWakeLock(t, agentDir, wakeLockFile{PID: myPID, Root: filepath.Join(base, "issue-96")})
	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == myPID },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			return predicate("amq wake --me cto --root " + filepath.Join(base, "issue-96"))
		},
		Now: time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--force-duplicate"})
	})
	if err != nil {
		t.Fatalf("runTeamResume --force-duplicate: %v", err)
	}
	if !strings.Contains(stdout, "force-duplicate:") {
		t.Errorf("plan note should mark forced override:\n%s", stdout)
	}
	// The forced restore command MUST carry --force-duplicate so its
	// own preflight does not refuse on replay against the same live agent.
	// The record has no saved conversation, so this is a re-orient resume:
	// --no-bootstrap must be ABSENT and the agent re-runs bootstrap.
	if !strings.Contains(stdout, "amq-squad agent up codex --force-duplicate") {
		t.Errorf("forced restore command must inject --force-duplicate in modern agent up shape, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "--no-bootstrap") {
		t.Errorf("re-orient restore (no saved conversation) must not emit --no-bootstrap, got:\n%s", stdout)
	}
}

// Regression for #27 P2 (footer must be option-aware or suppressed): when
// the plan emits restore commands, the team-launch alternative footer
// would NOT be equivalent (it always re-emits fresh from team intent), so
// the footer must be suppressed and replaced with a clarifying note.
func TestRunTeamResumeFooterSuppressedWhenAnyRestore(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	stdout, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v", err)
	}
	if strings.Contains(stdout, "up --session") {
		t.Errorf("footer alternative must be suppressed when any row is restore:\n%s", stdout)
	}
	if !strings.Contains(stdout, "not equivalent to the per-member plan") {
		t.Errorf("footer should explain that the up alternative is not equivalent:\n%s", stdout)
	}
}

// Combined regression for the live+forced+recorded case: action stays
// 'live' but the emitted command is a forced restore. The footer must
// still be suppressed because 'up' would re-emit fresh commands from team
// intent and not reproduce the restore semantics above.
func TestRunTeamResumeFooterSuppressedForLiveForcedRestore(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	myPID := os.Getpid()
	writeWakeLock(t, agentDir, wakeLockFile{PID: myPID, Root: filepath.Join(base, "issue-96")})
	original := defaultDuplicateLaunchProbe
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(pid int) bool { return pid == myPID },
		ProcessMatch: func(pid int, predicate func(args string) bool) bool {
			return predicate("amq wake --me cto --root " + filepath.Join(base, "issue-96"))
		},
		Now: time.Now,
	}
	t.Cleanup(func() { defaultDuplicateLaunchProbe = original })

	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--force-duplicate"})
	})
	if err != nil {
		t.Fatalf("runTeamResume --force-duplicate: %v", err)
	}
	// Sanity: the emitted command must be the forced restore. The record has
	// no saved conversation, so this is a re-orient resume: --no-bootstrap is
	// absent and bootstrap re-runs.
	if !strings.Contains(stdout, "amq-squad agent up codex --force-duplicate") {
		t.Errorf("forced live restore must include --force-duplicate in modern agent up shape, got:\n%s", stdout)
	}
	if strings.Contains(stdout, "--no-bootstrap") {
		t.Errorf("re-orient live+forced restore (no saved conversation) must not emit --no-bootstrap, got:\n%s", stdout)
	}
	// Footer must be suppressed: 'up --session ...' would re-emit fresh from
	// team intent and not reproduce the restore semantics.
	if strings.Contains(stdout, "up --session") {
		t.Errorf("footer must be suppressed for live+forced+recorded restore:\n%s", stdout)
	}
	if !strings.Contains(stdout, "not equivalent to the per-member plan") {
		t.Errorf("footer note should still explain the non-equivalence:\n%s", stdout)
	}
}

// Regression: when all rows are fresh and --force-duplicate was passed,
// the footer alternative must propagate --force-duplicate.
func TestRunTeamResumeFooterCarriesForceDuplicateOnAllFresh(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, _, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--force-duplicate"})
	})
	if err != nil {
		t.Fatalf("runTeamResume --force-duplicate: %v", err)
	}
	if !strings.Contains(stdout, "up --session 'issue-96' --force-duplicate") &&
		!strings.Contains(stdout, "up --session issue-96 --force-duplicate") {
		t.Errorf("footer should carry --force-duplicate, got:\n%s", stdout)
	}
}

// TestRunTeamResumeReorientWhenNoSavedConversation pins the PR2 planner
// contract: a restorable record with NO saved conversation produces a
// re-orient restore -- the emitted command re-runs bootstrap (no
// --no-bootstrap) and the plan Note explains the agent re-reads its brief
// and drains AMQ history rather than coming up blank.
func TestRunTeamResumeReorientWhenNoSavedConversation(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", StartedAt: time.Now(),
	})

	stdout, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v", err)
	}
	if !strings.Contains(stdout, "restore") {
		t.Errorf("recorded member should classify as restore:\n%s", stdout)
	}
	if strings.Contains(stdout, "--no-bootstrap") {
		t.Errorf("re-orient restore (no saved conversation) must not emit --no-bootstrap:\n%s", stdout)
	}
	if !strings.Contains(stdout, "re-orient") {
		t.Errorf("plan Note should mark the restore as a re-orient:\n%s", stdout)
	}
}

// TestRunTeamResumeReattachWhenSavedConversation pins the other direction:
// a restorable record WITH a saved conversation truly reattaches -- the
// emitted command keeps --no-bootstrap and the plan Note names the thread.
func TestRunTeamResumeReattachWhenSavedConversation(t *testing.T) {
	dir := t.TempDir()
	base := setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Workstream: "issue-96",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	writeMemberLaunchRecord(t, base, "issue-96", "cto", launch.Record{
		CWD: dir, Binary: "codex", Role: "cto", Conversation: "cto-thread",
		StartedAt: time.Now(),
	})

	stdout, _, err := captureOutput(t, func() error { return runTeamResume(nil) })
	if err != nil {
		t.Fatalf("runTeamResume: %v", err)
	}
	if !strings.Contains(stdout, "--no-bootstrap") {
		t.Errorf("reattach restore (saved conversation) must keep --no-bootstrap:\n%s", stdout)
	}
	if !strings.Contains(stdout, "reattach: saved conversation cto-thread") {
		t.Errorf("plan Note should name the reattached conversation:\n%s", stdout)
	}
}

func TestRunTeamResumeMixedSessionFiltersOutCrossSessionMembers(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "go-dev", Binary: "claude", Handle: "go-dev", Session: "v2-3-0"},
			{Role: "architect", Binary: "codex", Handle: "architect", Session: "v2-3-0"},
			{Role: "pm-copilot", Binary: "claude", Handle: "pm-copilot", Session: "pm-copilot"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--session", "v2-3-0"})
	})
	if err != nil {
		t.Fatalf("team resume: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "pm-copilot") {
		t.Errorf("resume plan should not include cross-session member pm-copilot:\n%s", stdout)
	}
	for _, want := range []string{"go-dev", "architect"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("resume plan missing session member %q:\n%s", want, stdout)
		}
	}
	if !strings.Contains(stderr, "skipping pm-copilot") {
		t.Errorf("stderr missing skip notice for pm-copilot:\n%s", stderr)
	}
}

func TestRunTeamResumeMixedSessionIncludesUnpinnedMembers(t *testing.T) {
	dir := t.TempDir()
	setupFakeAMQSessionRoots(t)
	resumeChdir(t, dir)

	if err := team.Write(dir, team.Team{
		Members: []team.Member{
			{Role: "go-dev", Binary: "claude", Handle: "go-dev", Session: "v2-3-0"},
			{Role: "shared-bot", Binary: "claude", Handle: "shared-bot", Session: ""},
			{Role: "pm-copilot", Binary: "claude", Handle: "pm-copilot", Session: "pm-copilot"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamResume([]string{"--session", "v2-3-0"})
	})
	if err != nil {
		t.Fatalf("team resume: %v\nstderr:\n%s", err, stderr)
	}
	for _, want := range []string{"go-dev", "shared-bot"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("resume plan missing %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "pm-copilot") {
		t.Errorf("resume plan should not include cross-session member pm-copilot:\n%s", stdout)
	}
}

func TestRunTeamResumeHelpListsActions(t *testing.T) {
	_, stderr, err := captureOutput(t, func() error { return Run([]string{"team", "resume", "--help"}, "test") })
	if err != nil {
		t.Fatalf("team resume --help: %v", err)
	}
	for _, want := range []string{
		"amq-squad team resume",
		"live",
		"restore",
		"launch fresh",
		"blocked",
		"--restore-existing",
		"--fresh",
		"--project DIR",
		"amq-squad team resume --project ~/Code/app",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("team resume --help missing %q in:\n%s", want, stderr)
		}
	}
}
