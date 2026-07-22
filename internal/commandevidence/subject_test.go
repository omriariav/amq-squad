package commandevidence

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func subjectRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.test/subject\n\ngo 1.25\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", dir, "add", "go.mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v: %s", err, out)
	}
	cmd = exec.Command("git", "-C", dir, "commit", "-m", "initial")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, out)
	}
	return dir
}

func subjectWorktree(t *testing.T, repo string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "linked")
	cmd := exec.Command("git", "-C", repo, "worktree", "add", "--detach", dir, "HEAD")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git worktree: %v: %s", err, out)
	}
	return dir
}

func TestResolveCommandSubjectAlternateTargets(t *testing.T) {
	repo := subjectRepo(t)
	linked := subjectWorktree(t, repo)
	physicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	physicalLinked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"git", "-C", linked, "status", "--short"},
		{"make", "-C", linked, "test"},
		{"go", "-C", linked, "test", "./..."},
		{"/usr/bin/env", "go", "-C", linked, "test", "./..."},
	} {
		subject, err := ResolveCommandSubject(repo, argv)
		if err != nil {
			t.Fatalf("ResolveCommandSubject(%v): %v", argv, err)
		}
		if subject.SubjectCWD != physicalLinked || subject.GitTopLevel != physicalLinked || subject.ControlCWD != physicalRepo || subject.GitHead == "" || subject.GitTree == "" {
			t.Fatalf("wrong subject for %v: %+v", argv, subject)
		}
	}
}

func TestResolveCommandSubjectRejectsUnrelatedRepoAndAmbiguousWrapper(t *testing.T) {
	control := subjectRepo(t)
	unrelated := subjectRepo(t)
	if _, err := ResolveCommandSubject(control, []string{"go", "-C", unrelated, "test", "./..."}); err == nil || !strings.Contains(err.Error(), "differs from the task control repository") {
		t.Fatalf("unexpected unrelated-repo error: %v", err)
	}
	if _, err := ResolveCommandSubject(control, []string{"sh", "-c", "go -C /tmp test ./..."}); err == nil || !strings.Contains(err.Error(), "nested wrapper") {
		t.Fatalf("unexpected wrapper error: %v", err)
	}
}

func TestVerifyCommandSubjectDetectsPostReceiptMutation(t *testing.T) {
	repo := subjectRepo(t)
	expected, err := ResolveCommandSubject(repo, []string{"go", "test", "./..."})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "changed.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = VerifyCommandSubject(expected, []string{"go", "test", "./..."})
	if err == nil || !strings.Contains(err.Error(), "changed after receipt") {
		t.Fatalf("unexpected mutation error: %v", err)
	}
}
