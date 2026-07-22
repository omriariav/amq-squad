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
	tests := []struct {
		name   string
		argv   []string
		target string
	}{
		{name: "git regular", argv: []string{"git", "-C", repo, "status", "--short"}, target: physicalRepo},
		{name: "git linked", argv: []string{"git", "-C", linked, "status", "--short"}, target: physicalLinked},
		{name: "make regular", argv: []string{"make", "-C", repo, "test"}, target: physicalRepo},
		{name: "make linked", argv: []string{"make", "-C", linked, "test"}, target: physicalLinked},
		{name: "go regular", argv: []string{"go", "-C", repo, "test", "./..."}, target: physicalRepo},
		{name: "go linked", argv: []string{"go", "-C", linked, "test", "./..."}, target: physicalLinked},
		{name: "env assignments go linked", argv: []string{"/usr/bin/env", "GOCACHE=/tmp/cache", "GOTMPDIR=/tmp/work", "go", "-C", linked, "test", "./..."}, target: physicalLinked},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, err := ResolveCommandSubject(repo, tt.argv)
			if err != nil {
				t.Fatalf("ResolveCommandSubject(%v): %v", tt.argv, err)
			}
			if subject.SubjectCWD != tt.target || subject.GitTopLevel != tt.target || subject.ControlCWD != physicalRepo || subject.GitHead == "" || subject.GitTree == "" {
				t.Fatalf("wrong subject for %v: %+v", tt.argv, subject)
			}
		})
	}
}

func TestResolveCommandSubjectRejectsInvalidSelectors(t *testing.T) {
	repo := subjectRepo(t)
	tests := []struct {
		name string
		argv []string
		want string
	}{
		{name: "git missing", argv: []string{"git", "-C"}, want: "missing -C target"},
		{name: "make missing", argv: []string{"make", "--directory"}, want: "missing -C target"},
		{name: "go missing", argv: []string{"go", "-C"}, want: "missing or duplicate -C target"},
		{name: "go duplicate", argv: []string{"go", "-C", repo, "-C=" + repo, "test"}, want: "duplicate -C target"},
		{name: "git conflicting git dir", argv: []string{"git", "-C", repo, "--git-dir=.git", "status"}, want: "conflicting repository selector"},
		{name: "git conflicting work tree", argv: []string{"git", "--work-tree", repo, "status"}, want: "conflicting repository selector"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ResolveCommandSubject(repo, tt.argv)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ResolveCommandSubject(%v) error = %v, want %q", tt.argv, err, tt.want)
			}
		})
	}
}

func TestResolveCommandSubjectEnvWrapperIsBounded(t *testing.T) {
	repo := subjectRepo(t)
	for _, argv := range [][]string{
		{"/usr/bin/env", "--", "GOCACHE=/tmp/cache", "go", "-C", repo, "test", "./..."},
		{"/usr/bin/env", "EMPTY=", "VALUE=contains=equals", "go", "-C", repo, "test", "./..."},
	} {
		if _, err := ResolveCommandSubject(repo, argv); err != nil {
			t.Fatalf("deterministic env wrapper %v: %v", argv, err)
		}
	}
	for _, argv := range [][]string{
		{"/usr/bin/env", "-i", "go", "-C", repo, "test", "./..."},
		{"/usr/bin/env", "9INVALID=value", "go", "-C", repo, "test", "./..."},
		{"/usr/bin/env", "GOCACHE=/tmp/cache"},
	} {
		if _, err := ResolveCommandSubject(repo, argv); err == nil || !strings.Contains(err.Error(), "ambiguous env wrapper") {
			t.Fatalf("ambiguous env wrapper %v error = %v", argv, err)
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
