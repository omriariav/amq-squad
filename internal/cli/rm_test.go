package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// rmStateProbe builds an internal/state.Probe with deterministic per-PID
// liveness and binary-match decisions so rm's liveness gate never shells out to
// ps or sends real signals.
func rmStateProbe(alive, match map[int]bool) state.Probe {
	return state.Probe{
		PIDAlive: func(pid int) bool { return alive[pid] },
		ProcessMatch: func(pid int, _ func(args string) bool) bool {
			return match[pid]
		},
		Now: time.Now,
	}
}

// deadStateProbe is the common case: every PID is dead, so no session is live.
func deadStateProbe() state.Probe {
	return rmStateProbe(nil, nil)
}

// seedBrief writes a brief file for (projectDir, session) and returns its path.
func seedBrief(t *testing.T, projectDir, session string) string {
	t.Helper()
	path := briefPath(projectDir, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# "+session+"\nreal brief content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func runRmExec(t *testing.T, e rmExecution) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	e.Out = &buf
	if e.Probe.PIDAlive == nil {
		e.Probe = deadStateProbe()
	}
	err := executeRm(e)
	return buf.String(), err
}

// TestRmDeclinedLeavesFilesUntouched: the confirm gate defaults to NO, and a
// decline (answer "n") must make ZERO filesystem changes.
func TestRmDeclinedLeavesFilesUntouched(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	brief := seedBrief(t, projectDir, "issue-96")
	root := filepath.Join(base, "issue-96")

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeDelete,
		BaseRoot:   base,
		Confirm:    strings.NewReader("n\n"),
	})
	if err != nil {
		t.Fatalf("declined rm should not error: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Errorf("session root must survive a decline: %v", statErr)
	}
	if _, statErr := os.Stat(brief); statErr != nil {
		t.Errorf("brief must survive a decline: %v", statErr)
	}
	if !strings.Contains(out, "aborted") {
		t.Errorf("decline should report abort:\n%s", out)
	}
}

// TestRmEmptyAnswerDeclines: the default answer is NO. An empty line (just
// Enter) must NOT remove anything.
func TestRmEmptyAnswerDeclines(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	root := filepath.Join(base, "issue-96")
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeDelete,
		BaseRoot:   base,
		Confirm:    strings.NewReader("\n"),
	})
	if err != nil {
		t.Fatalf("empty-answer rm should not error: %v", err)
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Errorf("empty answer (default No) must not remove the root: %v", statErr)
	}
	if !strings.Contains(out, "aborted") {
		t.Errorf("empty answer should abort:\n%s", out)
	}
}

// TestRmYesRemovesRootAndBrief: --yes skips the prompt and the root dir + brief
// are gone afterward.
func TestRmYesRemovesRootAndBrief(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	root := filepath.Join(base, "issue-96")
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := seedBrief(t, projectDir, "issue-96")

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeDelete,
		Yes:        true,
		BaseRoot:   base,
	})
	if err != nil {
		t.Fatalf("rm --yes: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Errorf("session root should be gone after rm --yes; stat err = %v", statErr)
	}
	if _, statErr := os.Stat(brief); !os.IsNotExist(statErr) {
		t.Errorf("brief should be gone after rm --yes; stat err = %v", statErr)
	}
	if !strings.Contains(out, "removed "+root) {
		t.Errorf("output should report the removed root:\n%s", out)
	}
}

func TestRunRmProjectTargetsOtherDir(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	project := t.TempDir()
	other := t.TempDir()
	root := filepath.Join(base, "issue-99")
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := seedBrief(t, project, "issue-99")
	chdir(t, other)

	stdout, stderr, err := captureOutput(t, func() error {
		return runRm([]string{"issue-99", "--project", project, "--yes", "--keep-panes"}, rmModeDelete)
	})
	if err != nil {
		t.Fatalf("rm --project: %v\nstderr:\n%s", err, stderr)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("rm --project should remove target root; stat err = %v\nstdout:\n%s", statErr, stdout)
	}
	if _, statErr := os.Stat(brief); !os.IsNotExist(statErr) {
		t.Fatalf("rm --project should remove target brief; stat err = %v\nstdout:\n%s", statErr, stdout)
	}
}

func TestRunRmProfileTargetsNamedNamespace(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	project := t.TempDir()
	other := t.TempDir()
	root := filepath.Join(base, "release", "issue-101")
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := briefPathForProfile(project, "release", "issue-101")
	if err := os.MkdirAll(filepath.Dir(brief), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(brief, []byte("# issue-101\nnamed profile brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	chdir(t, other)

	stdout, stderr, err := captureOutput(t, func() error {
		return runRm([]string{"issue-101", "--project", project, "--profile", "release", "--yes", "--keep-panes"}, rmModeDelete)
	})
	if err != nil {
		t.Fatalf("rm --profile release: %v\nstderr:\n%s", err, stderr)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("rm --profile should remove named root; stat err = %v\nstdout:\n%s", statErr, stdout)
	}
	if _, statErr := os.Stat(brief); !os.IsNotExist(statErr) {
		t.Fatalf("rm --profile should remove named brief; stat err = %v\nstdout:\n%s", statErr, stdout)
	}
	if _, statErr := os.Stat(filepath.Join(base, "issue-101")); !os.IsNotExist(statErr) {
		t.Fatalf("rm --profile must not touch/create default root; stat err = %v", statErr)
	}
}

func TestRunArchiveAcceptsFlagsAfterSession(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	project := t.TempDir()
	other := t.TempDir()
	root := filepath.Join(base, "issue-100")
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	chdir(t, other)

	stdout, stderr, err := captureOutput(t, func() error {
		return runRm([]string{"issue-100", "--project", project, "--yes", "--keep-panes"}, rmModeArchive)
	})
	if err != nil {
		t.Fatalf("archive flags after session: %v\nstderr:\n%s", err, stderr)
	}
	dest := filepath.Join(base, archiveDirName, "issue-100")
	if _, statErr := os.Stat(dest); statErr != nil {
		t.Fatalf("archive flags after session should move target root: %v\nstdout:\n%s", statErr, stdout)
	}
}

// TestArchiveMovesNotDeletes: archive MOVES the root into .archive/<session>
// (gone from the original, present in the archive) and moves the brief
// alongside. Nothing is deleted.
func TestArchiveMovesNotDeletes(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	root := filepath.Join(base, "issue-96")
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}
	// A marker file inside the root proves the contents moved, not just a dir.
	marker := filepath.Join(root, "agents", "cto", "inbox.marker")
	if err := os.WriteFile(marker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	brief := seedBrief(t, projectDir, "issue-96")

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeArchive,
		Yes:        true,
		BaseRoot:   base,
	})
	if err != nil {
		t.Fatalf("archive --yes: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Errorf("original session root should be gone after archive; stat err = %v", statErr)
	}
	dest := filepath.Join(base, archiveDirName, "issue-96")
	if _, statErr := os.Stat(filepath.Join(dest, "agents", "cto", "inbox.marker")); statErr != nil {
		t.Errorf("archived contents should be present under .archive: %v", statErr)
	}
	if _, statErr := os.Stat(brief); !os.IsNotExist(statErr) {
		t.Errorf("brief should have MOVED out of the original location: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dest, "issue-96.md")); statErr != nil {
		t.Errorf("brief should be present in the archive: %v", statErr)
	}
}

// TestRmRefusesLiveSessionWithoutForce: a session with a live agent is refused
// unless --force. The live signal is the repo's own liveness (internal/state)
// via an injected probe.
func TestRmRefusesLiveSessionWithoutForce(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	root := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 4242,
		Root: root, Session: "issue-96",
	})
	// Live: PID alive AND binary matches -> state.LivenessAlive.
	liveProbe := rmStateProbe(map[int]bool{4242: true}, map[int]bool{4242: true})

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeDelete,
		Yes:        true,
		BaseRoot:   base,
		Probe:      liveProbe,
	})
	if err == nil {
		t.Fatalf("rm of a live session must be refused without --force:\n%s", out)
	}
	if !strings.Contains(err.Error(), "live agents") {
		t.Errorf("refusal should mention live agents: %v", err)
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Errorf("refused rm must leave the root intact: %v", statErr)
	}
}

// TestRmForceOverridesLiveSession: --force tears down a live session's on-disk
// footprint but must NOT stop the agent or close its pane (the documented
// contract). Requested pane cleanup therefore returns partial and retains the
// exact identity evidence needed for explicit operator recovery.
func TestRmForceOverridesLiveSession(t *testing.T) {
	projectDir, base, _, _, record, _ := completeRmPaneFixture(t, "issue-96", 4242)
	root := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", record)
	liveProbe := rmStateProbe(map[int]bool{4242: true}, map[int]bool{4242: true})

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeDelete,
		Yes:        true,
		Force:      true,
		ClosePanes: true,
		BaseRoot:   base,
		Probe:      liveProbe,
	})
	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("rm --force with requested cleanup should report preserved pane: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Errorf("rm --force should remove the root; stat err = %v", statErr)
	}
	// The live agent's pane is explicitly preserved, and the recovery notice
	// names its retained exact identity without blindly issuing kill-pane.
	for _, want := range []string{"preserved_identity_unconfirmed", "left RUNNING", "cto", "%9", "inspect retained identity evidence"} {
		if !strings.Contains(out, want) {
			t.Fatalf("rm --force notice missing %q in:\n%s", want, out)
		}
	}
}

// TestRmStopAgentsTearsDownLiveSession: --stop-agents is the one-command full
// teardown — it SIGTERMs the live agents, closes their panes, then removes the
// session.
func TestRmStopAgentsTearsDownLiveSession(t *testing.T) {
	projectDir, base, _, _, record, pane := completeRmPaneFixture(t, "issue-96", 4242)
	root := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", record)
	liveProbe := rmStateProbe(map[int]bool{4242: true}, map[int]bool{4242: true})
	term := &recordingTerminator{}
	closeCalls := 0

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeDelete,
		Yes:        true,
		// NB: Force deliberately NOT set — --stop-agents must imply it in the
		// execution path so a live session is not refused.
		StopAgents: true,
		ClosePanes: true,
		Terminator: term,
		BaseRoot:   base,
		Probe:      liveProbe,
		PaneDeps:   rmPaneDeps(pane, &closeCalls),
	})
	if err != nil {
		t.Fatalf("rm --stop-agents should succeed: %v\n%s", err, out)
	}
	if len(term.calls) != 1 || term.calls[0] != 4242 {
		t.Fatalf("expected SIGTERM of pid 4242, got terminate calls %v", term.calls)
	}
	if closeCalls != 1 {
		t.Fatalf("--stop-agents must close the live agent's exact pane once; calls = %d", closeCalls)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Errorf("--stop-agents should remove the root; stat err = %v", statErr)
	}
	if !strings.Contains(out, "agent=stopped") || !strings.Contains(out, "pane=closed") {
		t.Errorf("expected independent stopped/closed outcomes, got:\n%s", out)
	}
	if strings.Contains(out, "left RUNNING") {
		t.Errorf("--stop-agents must not print the left-running notice:\n%s", out)
	}
}

// TestArchiveRefusesLiveSessionWithoutForce mirrors the rm refusal for the
// non-destructive verb: archive must not move a live session aside either.
func TestArchiveRefusesLiveSessionWithoutForce(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	root := filepath.Join(base, "issue-96")
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", AgentPID: 7,
		Root: root, Session: "issue-96",
	})
	liveProbe := rmStateProbe(map[int]bool{7: true}, map[int]bool{7: true})

	_, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeArchive,
		Yes:        true,
		BaseRoot:   base,
		Probe:      liveProbe,
	})
	if err == nil || !strings.Contains(err.Error(), "live agents") {
		t.Fatalf("archive of a live session must be refused without --force: %v", err)
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Errorf("refused archive must leave the root intact: %v", statErr)
	}
}

// TestRmProceedsAfterCleanStop is the #109 regression: stop's last act writes
// presence.json with status "offline" and a fresh last_seen. That terminal
// write must NOT hold rm's live-agents gate closed — the documented stop→rm
// sequence has to work back-to-back, not after a 90s wait.
func TestRmProceedsAfterCleanStop(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	root := filepath.Join(base, "first-run")
	seedAgentRecord(t, base, "first-run", "copilot", launch.Record{
		Binary: "claude", Handle: "copilot", AgentPID: 4242,
		Root: root, Session: "first-run",
	})
	// Exactly what stop leaves behind: dead agent PID + a seconds-old
	// presence write with status "offline".
	seedBoardPresence(t, base, "first-run", "copilot", "offline", time.Now().Add(-5*time.Second))
	deadPID := rmStateProbe(map[int]bool{4242: false}, map[int]bool{4242: false})

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "first-run",
		Mode:       rmModeDelete,
		Yes:        true,
		BaseRoot:   base,
		Probe:      deadPID,
	})
	if err != nil {
		t.Fatalf("rm right after a clean stop must proceed, got: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Errorf("rm should have removed the root; stat err = %v", statErr)
	}
}

// TestRmRefusalNamesFreshnessWindow: when the only "live" evidence is a fresh
// non-terminal presence write behind a dead PID (a genuine zombie writer), rm
// still refuses — but the error must name the freshness window and suggest the
// non-deprecated stop verb, so the operator knows waiting is an option (#109).
func TestRmRefusalNamesFreshnessWindow(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	root := filepath.Join(base, "first-run")
	seedAgentRecord(t, base, "first-run", "copilot", launch.Record{
		Binary: "claude", Handle: "copilot", AgentPID: 4242,
		Root: root, Session: "first-run",
	})
	// Zombie writer: dead agent PID, but presence still reads "active" and
	// fresh — the dead-mailbox-live case that must keep refusing.
	seedBoardPresence(t, base, "first-run", "copilot", "active", time.Now().Add(-5*time.Second))
	deadPID := rmStateProbe(map[int]bool{4242: false}, map[int]bool{4242: false})

	_, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "first-run",
		Mode:       rmModeDelete,
		Yes:        true,
		BaseRoot:   base,
		Probe:      deadPID,
	})
	if err == nil {
		t.Fatal("a zombie-writer session must still refuse rm without --force")
	}
	if !strings.Contains(err.Error(), "freshness window") {
		t.Errorf("refusal should name the presence freshness window: %v", err)
	}
	if !strings.Contains(err.Error(), "amq-squad stop --all") {
		t.Errorf("refusal should suggest the stop verb, not the deprecated down alias: %v", err)
	}
	if strings.Contains(err.Error(), "amq-squad down") {
		t.Errorf("refusal must not suggest the deprecated down alias: %v", err)
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Errorf("refused rm must leave the root intact: %v", statErr)
	}
}

// TestRmConfinedToSessionRoot is the highest-risk property: deleting session X
// must leave a sibling session Y (and the brief for Y) completely intact.
func TestRmConfinedToSessionRoot(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	rootX := filepath.Join(base, "issue-96")
	rootY := filepath.Join(base, "issue-97")
	for _, r := range []string{rootX, rootY} {
		if err := os.MkdirAll(filepath.Join(r, "agents", "cto"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	seedBrief(t, projectDir, "issue-96")
	briefY := seedBrief(t, projectDir, "issue-97")

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeDelete,
		Yes:        true,
		BaseRoot:   base,
	})
	if err != nil {
		t.Fatalf("rm: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(rootX); !os.IsNotExist(statErr) {
		t.Errorf("target session X should be gone; stat err = %v", statErr)
	}
	if _, statErr := os.Stat(rootY); statErr != nil {
		t.Errorf("sibling session Y must be untouched: %v", statErr)
	}
	if _, statErr := os.Stat(briefY); statErr != nil {
		t.Errorf("sibling brief Y must be untouched: %v", statErr)
	}
}

// TestRmRejectsTraversalSession proves a name that tries to escape the base
// root is rejected by name validation before any path is touched.
func TestRmRejectsTraversalSession(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	// A sibling that an unguarded "../" could reach.
	sibling := filepath.Join(filepath.Dir(base), "victim")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../victim", "/etc", "a/b", "Foo"} {
		_, err := runRmExec(t, rmExecution{
			ProjectDir: projectDir,
			Session:    name,
			Mode:       rmModeDelete,
			Yes:        true,
			BaseRoot:   base,
		})
		if err == nil {
			t.Errorf("session name %q must be rejected", name)
		}
	}
	if _, statErr := os.Stat(sibling); statErr != nil {
		t.Errorf("traversal target must remain untouched: %v", statErr)
	}
}

// TestRmNonExistentSessionErrors: a session with no root and no brief is a
// clean error, never a panic.
func TestRmNonExistentSessionErrors(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "ghost",
		Mode:       rmModeDelete,
		Yes:        true,
		BaseRoot:   base,
	})
	if err == nil {
		t.Fatalf("non-existent session should error:\n%s", out)
	}
	if !strings.Contains(err.Error(), "nothing to remove") {
		t.Errorf("error should explain nothing to remove: %v", err)
	}
}

// TestRmPreviewListsPaths: the preview always lists the resolved paths and the
// agent count before any confirmation.
func TestRmPreviewListsPaths(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	root := filepath.Join(base, "issue-96")
	for _, h := range []string{"cto", "fullstack"} {
		if err := os.MkdirAll(filepath.Join(root, "agents", h), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	brief := seedBrief(t, projectDir, "issue-96")

	// Decline so the preview is the only effect.
	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeDelete,
		BaseRoot:   base,
		Confirm:    strings.NewReader("n\n"),
	})
	if err != nil {
		t.Fatalf("preview rm: %v", err)
	}
	for _, want := range []string{"preview", "session:  issue-96", "agents:   2", root, brief, "Remove session issue-96? [y/N]"} {
		if !strings.Contains(out, want) {
			t.Errorf("preview missing %q:\n%s", want, out)
		}
	}
}

// TestArchivePreviewShowsDestination proves the archive preview names the move
// destination so the operator sees where the session lands.
func TestArchivePreviewShowsDestination(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	root := filepath.Join(base, "issue-96")
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o755); err != nil {
		t.Fatal(err)
	}

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "issue-96",
		Mode:       rmModeArchive,
		BaseRoot:   base,
		Confirm:    strings.NewReader("n\n"),
	})
	if err != nil {
		t.Fatalf("archive preview: %v", err)
	}
	dest := filepath.Join(base, archiveDirName, "issue-96")
	for _, want := range []string{"MOVE", root, dest} {
		if !strings.Contains(out, want) {
			t.Errorf("archive preview missing %q:\n%s", want, out)
		}
	}
}

// TestRmBriefOnlySession proves rm works when only a brief exists (no AMQ root):
// the brief is removed and the command succeeds.
func TestRmBriefOnlySession(t *testing.T) {
	base := t.TempDir()
	projectDir := t.TempDir()
	brief := seedBrief(t, projectDir, "orphan")

	out, err := runRmExec(t, rmExecution{
		ProjectDir: projectDir,
		Session:    "orphan",
		Mode:       rmModeDelete,
		Yes:        true,
		BaseRoot:   base,
	})
	if err != nil {
		t.Fatalf("rm brief-only: %v\n%s", err, out)
	}
	if _, statErr := os.Stat(brief); !os.IsNotExist(statErr) {
		t.Errorf("brief-only rm should remove the brief; stat err = %v", statErr)
	}
}

// TestRunRmRequiresSession proves the dispatcher-level flag parse rejects a
// missing session argument as a usage error.
func TestRunRmRequiresSession(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runRm(nil, rmModeDelete)
	})
	if err == nil {
		t.Fatal("rm with no session should be a usage error")
	}
	if _, ok := err.(UsageError); !ok {
		t.Fatalf("want UsageError, got %T: %v", err, err)
	}
}
