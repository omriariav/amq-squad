package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifyRebindCarriesReviewButNeverCIToIdenticalTree(t *testing.T) {
	repo := seedReviewGitRepo(t)
	oldHead := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	mustVerifyRebindGit(t, repo, "commit", "--allow-empty", "-m", "rebuilt")
	newHead := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	id := oldHead + "--" + newHead + ".json"

	stdout, _, err := captureOutput(t, func() error {
		return runVerify([]string{
			"rebind",
			"--project", repo,
			"--old-head", oldHead,
			"--new-head", newHead,
			"--json",
		})
	})
	if err != nil {
		t.Fatalf("verify rebind: %v", err)
	}
	for _, want := range []string{
		`"kind": "verify_rebind"`,
		`"ok": true`,
		`"proof_type": "tree"`,
		id,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("rebind JSON missing %q:\n%s", want, stdout)
		}
	}
	artifactPath := filepath.Join(repo, ".amq-squad", "reviews", "rebindings", id)
	if info, err := os.Lstat(artifactPath); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("artifact was not recorded as regular file: info=%v err=%v", info, err)
	}

}

func TestVerifyRebindRefusesSemanticDeltaAndWrongDirection(t *testing.T) {
	repo := seedReviewGitRepo(t)
	oldHead := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	mustVerifyRebindGit(t, repo, "commit", "--allow-empty", "-m", "equivalent rebuild")
	equivalentHead := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	if _, _, err := captureOutput(t, func() error {
		return runVerify([]string{"rebind", "--project", repo, "--old-head", oldHead, "--new-head", equivalentHead})
	}); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("semantic change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustVerifyRebindGit(t, repo, "commit", "-am", "semantic change")
	semanticHead := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	if _, _, err := captureOutput(t, func() error {
		return runVerify([]string{
			"rebind", "--project", repo,
			"--old-head", oldHead,
			"--new-head", semanticHead,
			"--proof", "tree",
		})
	}); err == nil || !strings.Contains(err.Error(), "tree proof refused") {
		t.Fatalf("want semantic-delta refusal, got %v", err)
	}
	semanticID := oldHead + "--" + semanticHead + ".json"
	if _, err := os.Stat(filepath.Join(repo, ".amq-squad", "reviews", "rebindings", semanticID)); !os.IsNotExist(err) {
		t.Fatalf("refused proof must not record artifact, stat err=%v", err)
	}

}

func mustVerifyRebindGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := gitOutput(repo, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out)
}
