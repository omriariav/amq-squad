package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedReviewGitRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "review-test@example.invalid"},
		{"config", "user.name", "Review Test"},
	} {
		if _, err := gitOutput(repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("review me\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked.txt"}, {"commit", "-q", "-m", "seed"}} {
		if _, err := gitOutput(repo, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return repo
}

func cleanupReviewWorktreeForTest(t *testing.T, wt reviewWorktree) {
	t.Helper()
	if _, err := os.Stat(wt.Path); os.IsNotExist(err) {
		return
	}
	if _, err := gitOutput(wt.Manifest.Repository, "worktree", "remove", "--force", wt.Path); err != nil {
		t.Errorf("cleanup review worktree: %v", err)
	}
}

func TestCreateReviewWorktreePinsExactCleanCommitAndManifest(t *testing.T) {
	repo := seedReviewGitRepo(t)
	// A dirty source checkout must not contaminate or block an exact detached
	// commit review: only committed objects are copied into the worktree.
	if err := os.WriteFile(filepath.Join(repo, "source-only.txt"), []byte("dirty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixedNow := time.Date(2026, 7, 12, 12, 34, 56, 789, time.UTC)
	oldNow := reviewWorktreeNow
	reviewWorktreeNow = func() time.Time { return fixedNow }
	t.Cleanup(func() { reviewWorktreeNow = oldNow })

	wt, err := createReviewWorktree(repo, "HEAD", "v2.20.0-test")
	if err != nil {
		t.Fatalf("createReviewWorktree: %v", err)
	}
	t.Cleanup(func() { cleanupReviewWorktreeForTest(t, wt) })

	wantCommit, err := gitOutput(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	wantTree, err := gitOutput(repo, "show", "-s", "--format=%T", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Manifest.Commit != strings.TrimSpace(wantCommit) || wt.Manifest.Tree != strings.TrimSpace(wantTree) {
		t.Fatalf("manifest provenance = %s/%s, want %s/%s", wt.Manifest.Commit, wt.Manifest.Tree, strings.TrimSpace(wantCommit), strings.TrimSpace(wantTree))
	}
	if wt.Manifest.CreatedAt != fixedNow.Format(time.RFC3339Nano) {
		t.Errorf("created_at = %q", wt.Manifest.CreatedAt)
	}
	if wt.Manifest.AMQSquadVersion != "v2.20.0-test" || wt.Manifest.GoVersion == "" || wt.Manifest.AMQVersion == "" {
		t.Errorf("tool versions not fully recorded: %+v", wt.Manifest)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "source-only.txt")); !os.IsNotExist(err) {
		t.Errorf("dirty source file leaked into detached worktree: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(wt.Path, reviewWorktreeManifestName))
	if err != nil {
		t.Fatal(err)
	}
	var disk reviewWorktreeManifest
	if err := json.Unmarshal(data, &disk); err != nil {
		t.Fatal(err)
	}
	if disk != wt.Manifest {
		t.Fatalf("disk manifest differs:\n disk=%+v\n want=%+v", disk, wt.Manifest)
	}
	actualCommit, err := gitOutput(wt.Path, "rev-parse", "HEAD")
	if err != nil || strings.TrimSpace(actualCommit) != wt.Manifest.Commit {
		t.Fatalf("detached HEAD = %q, %v", actualCommit, err)
	}
	status, err := gitOutput(wt.Path, "status", "--porcelain")
	if err != nil || strings.TrimSpace(status) != "?? "+reviewWorktreeManifestName {
		t.Fatalf("status after manifest = %q, %v", status, err)
	}
}

func TestCreateReviewWorktreeRejectsUnsafeAndNonCommitRefs(t *testing.T) {
	repo := seedReviewGitRepo(t)
	for _, ref := range []string{"--help", "does-not-exist"} {
		if _, err := createReviewWorktree(repo, ref, "test"); err == nil {
			t.Errorf("ref %q should be rejected", ref)
		}
	}
}

func TestSanitizedReviewEnvClearsAgentAndTerminalIdentity(t *testing.T) {
	input := []string{
		"PATH=/bin", "HOME=/home/reviewer", "PWD=/source",
		"AM_ROOT=/mail", "AM_BASE_ROOT=/base", "AM_ME=qa",
		"AM_SESSION=issue-415", "AM_WAKE_FD=9", "AM_CUSTOM=also-clear",
		"AMQ_SQUAD_CONFIG=/config", "AMQ_SQUAD_ANYTHING=clear",
		"TMUX=/tmp/tmux", "TMUX_PANE=%7", "TMUX_TMPDIR=/tmp",
	}
	got := sanitizedReviewEnv(input)
	want := []string{"PATH=/bin", "HOME=/home/reviewer"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("sanitized env:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestRunSanitizedReviewProcessUsesWorktreeAndKeepsEvidence(t *testing.T) {
	repo := seedReviewGitRepo(t)
	wt, err := createReviewWorktree(repo, "HEAD", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupReviewWorktreeForTest(t, wt) })
	for key, value := range map[string]string{
		"AM_ROOT":              "/mail",
		"AM_ME":                "qa",
		"AMQ_SQUAD_SENTINEL":   "leak",
		"TMUX_PANE":            "%9",
		"TMUX_REVIEW_SENTINEL": "leak",
	} {
		t.Setenv(key, value)
	}
	command := `pwd > reviewer-pwd.txt; env > reviewer-env.txt; printf evidence > review-evidence.txt`
	if err := runSanitizedReviewProcess(wt.Path, "/bin/sh", []string{"-c", command}); err != nil {
		t.Fatal(err)
	}
	pwd, err := os.ReadFile(filepath.Join(wt.Path, "reviewer-pwd.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(pwd)) != wt.Path {
		t.Errorf("review process pwd = %q, want %q", strings.TrimSpace(string(pwd)), wt.Path)
	}
	envData, err := os.ReadFile(filepath.Join(wt.Path, "reviewer-env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"AM_ROOT=", "AM_ME=", "AMQ_SQUAD_SENTINEL=", "TMUX_PANE=", "TMUX_REVIEW_SENTINEL="} {
		if strings.Contains(string(envData), forbidden) {
			t.Errorf("review environment leaked %q:\n%s", forbidden, envData)
		}
	}
	if _, err := os.Stat(filepath.Join(wt.Path, reviewWorktreeManifestName)); err != nil {
		t.Errorf("manifest did not remain beside evidence: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(wt.Path, "review-evidence.txt")); err != nil || string(got) != "evidence" {
		t.Errorf("evidence = %q, %v", got, err)
	}
}

func TestReviewWorktreeRemoveUsesGitAndNeverRM(t *testing.T) {
	repo := seedReviewGitRepo(t)
	bin := t.TempDir()
	rmMarker := filepath.Join(t.TempDir(), "rm-was-called")
	rmScript := "#!/bin/sh\nprintf called > \"" + rmMarker + "\"\nexit 99\n"
	if err := os.WriteFile(filepath.Join(bin, "rm"), []byte(rmScript), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	wt, err := createReviewWorktree(repo, "HEAD", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupReviewWorktreeForTest(t, wt) })

	if err := runReviewWorktreeRemove([]string{wt.Path}); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Errorf("worktree still exists after git removal: %v", err)
	}
	if _, err := os.Stat(rmMarker); !os.IsNotExist(err) {
		t.Fatalf("rm executable was invoked during cleanup: %v", err)
	}
}

func TestReviewWorktreeRemoveRejectsMissingOrMismatchedManifest(t *testing.T) {
	path, err := os.MkdirTemp("", reviewWorktreeTempPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(path) })
	if _, err := validateReviewWorktreeForRemoval(path); err == nil || !strings.Contains(err.Error(), "valid "+reviewWorktreeManifestName+" is required") {
		t.Fatalf("missing manifest should fail closed, got %v", err)
	}

	repo := seedReviewGitRepo(t)
	wt, err := createReviewWorktree(repo, "HEAD", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupReviewWorktreeForTest(t, wt) })
	bad := wt.Manifest
	bad.Worktree = path
	data, err := json.Marshal(bad)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, reviewWorktreeManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := validateReviewWorktreeForRemoval(wt.Path); err == nil || !strings.Contains(err.Error(), "identity does not match") {
		t.Fatalf("mismatched manifest should fail closed, got %v", err)
	}
}

func TestRunReviewWorktreeExecRequiresCommand(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runReviewWorktree([]string{"exec", "HEAD"}, "test")
	})
	if err == nil || !strings.Contains(err.Error(), "requires COMMAND") {
		t.Fatalf("want missing command usage error, got %v", err)
	}
}
