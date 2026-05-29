package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
	"github.com/omriariav/amq-squad/internal/team"
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
	if strings.Contains(stdout, "  restore  ") || strings.Contains(stdout, "\trestore\t") {
		t.Errorf("no-record team should not surface restore action:\n%s", stdout)
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

func TestRunTeamResumeFreshIgnoresRestoreRecords(t *testing.T) {
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
	if strings.Contains(stdout, "team launch --session") {
		t.Errorf("footer alternative must be suppressed when any row is restore:\n%s", stdout)
	}
	if !strings.Contains(stdout, "not equivalent to the per-member plan") {
		t.Errorf("footer should explain that team launch is not equivalent:\n%s", stdout)
	}
}

// Combined regression for the live+forced+recorded case: action stays
// 'live' but the emitted command is a forced restore. The footer must
// still be suppressed because 'team launch' would re-emit fresh commands
// from team intent and not reproduce the restore semantics above.
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
	// Footer must be suppressed: 'team launch --session ...' would re-emit
	// fresh from team intent and not reproduce the restore semantics.
	if strings.Contains(stdout, "team launch --session") {
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
	if !strings.Contains(stdout, "team launch --session 'issue-96' --force-duplicate") &&
		!strings.Contains(stdout, "team launch --session issue-96 --force-duplicate") {
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
	if !strings.Contains(stdout, "reattach conversation cto-thread") {
		t.Errorf("plan Note should name the reattached conversation:\n%s", stdout)
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
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("team resume --help missing %q in:\n%s", want, stderr)
		}
	}
}
