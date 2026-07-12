package cli

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
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

func TestCreateReviewWorktreeRequiresRunningVersion(t *testing.T) {
	repo := seedReviewGitRepo(t)
	if _, err := createReviewWorktree(repo, "HEAD", ""); err == nil || !strings.Contains(err.Error(), "running amq-squad version is required") {
		t.Fatalf("missing running version should fail before creation, got %v", err)
	}
}

func TestCreateReviewWorktreeDegradesWhenAMQVersionUnavailable(t *testing.T) {
	repo := seedReviewGitRepo(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "amq"), []byte("#!/bin/sh\necho unavailable >&2\nexit 23\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	wt, err := createReviewWorktree(repo, "HEAD", "test")
	if err != nil {
		t.Fatalf("AMQ version failure must degrade, not abort creation: %v", err)
	}
	t.Cleanup(func() { cleanupReviewWorktreeForTest(t, wt) })
	if !strings.HasPrefix(wt.Manifest.AMQVersion, "unavailable: ") || !strings.Contains(wt.Manifest.AMQVersion, "unavailable") {
		t.Fatalf("AMQ version = %q, want explicit degraded value", wt.Manifest.AMQVersion)
	}
	if wt.Manifest.GoVersion == "" || wt.Manifest.AMQSquadVersion != "test" {
		t.Fatalf("required versions were not preserved: %+v", wt.Manifest)
	}
}

func TestCreateReviewWorktreeDegradesWhenAMQIsAbsent(t *testing.T) {
	repo := seedReviewGitRepo(t)
	bin := t.TempDir()
	for _, name := range []string{"git", "go"} {
		path, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)

	wt, err := createReviewWorktree(repo, "HEAD", "test")
	if err != nil {
		t.Fatalf("missing AMQ must degrade, not abort creation: %v", err)
	}
	t.Cleanup(func() { cleanupReviewWorktreeForTest(t, wt) })
	if !strings.HasPrefix(wt.Manifest.AMQVersion, "unavailable: ") || !strings.Contains(wt.Manifest.AMQVersion, "executable file not found") {
		t.Fatalf("AMQ version = %q, want missing-executable degraded value", wt.Manifest.AMQVersion)
	}
}

func TestSanitizedReviewEnvClearsAgentAndTerminalIdentity(t *testing.T) {
	input := []string{
		"PATH=/bin", "HOME=/home/reviewer", "PWD=/source",
		"GOCACHE=/cache", "TMPDIR=/tmp/reviews",
		"AM_ROOT=/mail", "AM_BASE_ROOT=/base", "AM_ME=qa",
		"AM_SESSION=issue-415", "AM_WAKE_FD=9", "AM_CUSTOM=also-clear",
		"AMQ_SQUAD_CONFIG=/config", "AMQ_SQUAD_ANYTHING=clear",
		"TMUX=/tmp/tmux", "TMUX_PANE=%7", "TMUX_TMPDIR=/tmp",
		"GIT_DIR=/hostile/.git", "GIT_WORK_TREE=/hostile",
		"GIT_COMMON_DIR=/hostile/.git", "GIT_INDEX_FILE=/hostile/index",
		"GIT_OBJECT_DIRECTORY=/hostile/objects", "GIT_ALTERNATE_OBJECT_DIRECTORIES=/other",
		"GIT_NAMESPACE=hostile", "GIT_NO_REPLACE_OBJECTS=1", "GIT_CONFIG_COUNT=1",
	}
	got := sanitizedReviewEnv(input)
	want := []string{"PATH=/bin", "HOME=/home/reviewer", "GOCACHE=/cache", "TMPDIR=/tmp/reviews"}
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
		"GIT_DIR":              "/hostile/.git",
		"GIT_WORK_TREE":        "/hostile",
		"GIT_NAMESPACE":        "hostile",
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
	for _, forbidden := range []string{"AM_ROOT=", "AM_ME=", "AMQ_SQUAD_SENTINEL=", "TMUX_PANE=", "TMUX_REVIEW_SENTINEL=", "GIT_DIR=", "GIT_WORK_TREE=", "GIT_NAMESPACE="} {
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

func TestReviewWorktreeHostileGitEnvironmentCannotRedirectLifecycle(t *testing.T) {
	repo := seedReviewGitRepo(t)
	hostileRepo := seedReviewGitRepo(t)
	resolvedRepo, err := gitOutput(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{
		"GIT_DIR":                          filepath.Join(hostileRepo, ".git"),
		"GIT_WORK_TREE":                    hostileRepo,
		"GIT_COMMON_DIR":                   filepath.Join(hostileRepo, ".git"),
		"GIT_INDEX_FILE":                   filepath.Join(hostileRepo, ".git", "index"),
		"GIT_OBJECT_DIRECTORY":             filepath.Join(hostileRepo, ".git", "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES": filepath.Join(hostileRepo, ".git", "objects"),
		"GIT_NAMESPACE":                    "hostile",
		"GIT_NO_REPLACE_OBJECTS":           "1",
	} {
		t.Setenv(key, value)
	}

	wt, err := createReviewWorktree(repo, "HEAD", "test")
	if err != nil {
		t.Fatalf("hostile Git env redirected creation: %v", err)
	}
	t.Cleanup(func() { cleanupReviewWorktreeForTest(t, wt) })
	if wt.Manifest.Repository != strings.TrimSpace(resolvedRepo) {
		t.Fatalf("manifest repository = %q, want requested repo %q", wt.Manifest.Repository, strings.TrimSpace(resolvedRepo))
	}
	wantCommit, err := gitOutput(repo, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if wt.Manifest.Commit != strings.TrimSpace(wantCommit) {
		t.Fatalf("manifest commit = %s, want requested repo commit %s", wt.Manifest.Commit, strings.TrimSpace(wantCommit))
	}
	command := `git rev-parse --show-toplevel > reviewer-repo.txt; env > reviewer-env.txt`
	if err := runSanitizedReviewProcess(wt.Path, "/bin/sh", []string{"-c", command}); err != nil {
		t.Fatal(err)
	}
	reviewerRepo, err := os.ReadFile(filepath.Join(wt.Path, "reviewer-repo.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(reviewerRepo)) != wt.Path {
		t.Fatalf("reviewer Git repo = %q, want isolated worktree %q", strings.TrimSpace(string(reviewerRepo)), wt.Path)
	}
	reviewerEnv, err := os.ReadFile(filepath.Join(wt.Path, "reviewer-env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reviewerEnv), "GIT_") {
		t.Fatalf("reviewer environment retained hostile Git controls:\n%s", reviewerEnv)
	}
	if err := runReviewWorktreeRemove([]string{wt.Path}); err != nil {
		t.Fatalf("hostile Git env redirected removal: %v", err)
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

func TestCreateReviewWorktreeAddFailureRemovesOnlyEmptyTempDirectory(t *testing.T) {
	repo := seedReviewGitRepo(t)
	bin := t.TempDir()
	rmMarker := filepath.Join(t.TempDir(), "rm-was-called")
	if err := os.WriteFile(filepath.Join(bin, "rm"), []byte("#!/bin/sh\nprintf called > \""+rmMarker+"\"\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	oldAdd := reviewWorktreeAdd
	var allocated string
	reviewWorktreeAdd = func(_, path, _ string) error {
		allocated = path
		return errors.New("injected add failure")
	}
	t.Cleanup(func() { reviewWorktreeAdd = oldAdd })

	if _, err := createReviewWorktree(repo, "HEAD", "test"); err == nil || !strings.Contains(err.Error(), "injected add failure") {
		t.Fatalf("want injected add failure, got %v", err)
	}
	if allocated == "" {
		t.Fatal("test did not observe allocated temp path")
	}
	if _, err := os.Stat(allocated); !os.IsNotExist(err) {
		t.Fatalf("failed add leaked temp directory %q: %v", allocated, err)
	}
	if _, err := os.Stat(rmMarker); !os.IsNotExist(err) {
		t.Fatalf("rm executable was invoked after failed add: %v", err)
	}
}

func TestReviewWorktreeRemoveRejectsMissingOrMismatchedManifest(t *testing.T) {
	path, err := os.MkdirTemp("", reviewWorktreeTempPrefix)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	if _, _, err := validateReviewWorktreeForRemoval(path); err == nil || !strings.Contains(err.Error(), "valid "+reviewWorktreeManifestName+" is required") {
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
	if _, _, err := validateReviewWorktreeForRemoval(wt.Path); err == nil || !strings.Contains(err.Error(), "identity does not match") {
		t.Fatalf("mismatched manifest should fail closed, got %v", err)
	}
}

func TestReviewWorktreeRemoveRejectsManifestCommitAndTreeMismatch(t *testing.T) {
	repo := seedReviewGitRepo(t)
	wt, err := createReviewWorktree(repo, "HEAD", "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cleanupReviewWorktreeForTest(t, wt) })

	for name, mutate := range map[string]func(*reviewWorktreeManifest){
		"commit": func(m *reviewWorktreeManifest) { m.Commit = strings.Repeat("0", len(m.Commit)) },
		"tree":   func(m *reviewWorktreeManifest) { m.Tree = strings.Repeat("0", len(m.Tree)) },
	} {
		t.Run(name, func(t *testing.T) {
			bad := wt.Manifest
			mutate(&bad)
			data, err := json.Marshal(bad)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(wt.Path, reviewWorktreeManifestName), data, 0o644); err != nil {
				t.Fatal(err)
			}
			_, _, err = validateReviewWorktreeForRemoval(wt.Path)
			if err == nil || !strings.Contains(err.Error(), "does not match manifest "+name) {
				t.Fatalf("%s mismatch should fail clearly, got %v", name, err)
			}
		})
	}
}

func TestReviewWorktreeRemoveRejectsForgedManifestOnRegisteredWorktree(t *testing.T) {
	repo := seedReviewGitRepo(t)
	otherRepo := seedReviewGitRepo(t)
	path, err := os.MkdirTemp("", reviewWorktreeTempPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(repo, "worktree", "add", "--detach", path, "HEAD"); err != nil {
		_ = os.Remove(path)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = gitOutput(repo, "worktree", "remove", "--force", path)
	})
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := gitOutput(path, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := gitOutput(path, "show", "-s", "--format=%T", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	otherRoot, err := gitOutput(otherRepo, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	forged := reviewWorktreeManifest{
		SchemaVersion: 1,
		Commit:        strings.TrimSpace(commit),
		Tree:          strings.TrimSpace(tree),
		Repository:    strings.TrimSpace(otherRoot),
		Worktree:      path,
	}
	data, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, reviewWorktreeManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateReviewWorktreeForRemoval(path); err == nil || !strings.Contains(err.Error(), "common Git directory") {
		t.Fatalf("forged cross-repository manifest should fail closed, got %v", err)
	}
}

func TestReviewWorktreeRemoveRejectsAttachedRegisteredWorktree(t *testing.T) {
	repo := seedReviewGitRepo(t)
	root, err := gitOutput(repo, "rev-parse", "--show-toplevel")
	if err != nil {
		t.Fatal(err)
	}
	path, err := os.MkdirTemp("", reviewWorktreeTempPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gitOutput(repo, "worktree", "add", "-b", "forged-review-branch", path, "HEAD"); err != nil {
		_ = os.Remove(path)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = gitOutput(repo, "worktree", "remove", "--force", path)
	})
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	commit, err := gitOutput(path, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tree, err := gitOutput(path, "show", "-s", "--format=%T", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	manifest := reviewWorktreeManifest{
		SchemaVersion: 1,
		Commit:        strings.TrimSpace(commit),
		Tree:          strings.TrimSpace(tree),
		Repository:    strings.TrimSpace(root),
		Worktree:      path,
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, reviewWorktreeManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := validateReviewWorktreeForRemoval(path); err == nil || !strings.Contains(err.Error(), "expected detached HEAD") {
		t.Fatalf("attached registered worktree should fail closed, got %v", err)
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
