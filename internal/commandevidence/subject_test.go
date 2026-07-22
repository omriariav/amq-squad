package commandevidence

import (
	"errors"
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
		{name: "go subcommand linked", argv: []string{"go", "test", "--C", linked, "./..."}, target: physicalLinked},
		{name: "go subcommand equals", argv: []string{"go", "env", "-C=" + linked, "GOMOD"}, target: physicalLinked},
		{name: "go grouped subcommand", argv: []string{"go", "mod", "tidy", "--C=" + linked}, target: physicalLinked},
		{name: "go grouped command flag", argv: []string{"go", "mod", "--C", linked}, target: physicalLinked},
		{name: "go telemetry flag", argv: []string{"go", "telemetry", "--C", linked, "off"}, target: physicalLinked},
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

func TestResolveCommandSubjectIgnoresGoChdirOutsideExactCommandBoundary(t *testing.T) {
	repo := subjectRepo(t)
	physical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"go", "telemetry", "off", "--C", t.TempDir()},
		{"go", "mod", "unknown", "--C", t.TempDir()},
		{"go", "test", "./...", "--C", t.TempDir()},
	} {
		subject, err := ResolveCommandSubject(repo, argv)
		if err != nil || subject.SubjectCWD != physical {
			t.Fatalf("go argv %v subject=%+v err=%v", argv, subject, err)
		}
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
		{name: "go missing", argv: []string{"go", "-C"}, want: "missing -C target"},
		{name: "go subcommand missing", argv: []string{"go", "test", "--C"}, want: "missing -C target"},
		{name: "git glued", argv: []string{"git", "-C" + repo, "status"}, want: "unsupported glued -C selector"},
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
	if _, err := ResolveCommandSubject(control, []string{"sh", "-c", "go -C /tmp test ./..."}); err == nil || !strings.Contains(err.Error(), "unsupported command subject executable") {
		t.Fatalf("unexpected wrapper error: %v", err)
	}
}

func TestResolveCommandSubjectStopsAtGitSubcommandBoundary(t *testing.T) {
	repo := subjectRepo(t)
	physical, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"git", "commit", "-C", "HEAD"},
		{"git", "blame", "-C", "go.mod"},
		{"git", "log", "-C"},
		{"git", "-c", "color.ui=false", "status", "-C", "ignored-after-subcommand"},
	} {
		subject, err := ResolveCommandSubject(repo, argv)
		if err != nil || subject.SubjectCWD != physical || subject.Mode != "git-C" {
			t.Fatalf("git subcommand argv %v subject=%+v err=%v", argv, subject, err)
		}
	}
}

func TestResolveCommandSubjectRejectsUnknownWrappers(t *testing.T) {
	repo := subjectRepo(t)
	for _, name := range []string{"timeout", "nohup", "nice", "stdbuf", "doas", "time", "sudo", "xargs"} {
		if _, err := ResolveCommandSubject(repo, []string{name, "go", "test", "./..."}); err == nil || !strings.Contains(err.Error(), "unsupported command subject executable") {
			t.Fatalf("unknown wrapper %s error = %v", name, err)
		}
	}
}

func TestResolveCommandSubjectRejectsExplicitNonGitTarget(t *testing.T) {
	repo := subjectRepo(t)
	nonGit := t.TempDir()
	if _, err := ResolveCommandSubject(repo, []string{"git", "-C", nonGit, "status"}); err == nil || !strings.Contains(err.Error(), "not a Git repository/worktree") {
		t.Fatalf("explicit non-Git target error = %v", err)
	}
}

func TestResolveCommandSubjectSurfacesGitSnapshotFailure(t *testing.T) {
	repo := subjectRepo(t)
	previous := runSubjectGit
	t.Cleanup(func() { runSubjectGit = previous })
	runSubjectGit = func(string, ...string) (string, error) { return "", errors.New("injected git snapshot failure") }
	if _, err := ResolveCommandSubject(repo, []string{"go", "test", "./..."}); err == nil || !strings.Contains(err.Error(), "injected git snapshot failure") {
		t.Fatalf("snapshot failure was swallowed: %v", err)
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
