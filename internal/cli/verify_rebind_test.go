package cli

import (
	"fmt"
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

	evidence := writeReboundMergeEvidence(t, repo, oldHead, newHead, newHead, id)
	stdout, _, err = captureOutput(t, func() error {
		return runVerify([]string{"merge", "--project", repo, "--evidence", evidence, "--json"})
	})
	if err != nil {
		t.Fatalf("verify merge with rebinding: %v\n%s", err, stdout)
	}
	for _, want := range []string{
		`"kind": "verify_merge"`,
		`"ok": true`,
		`"review_rebinding"`,
		`"proof_type": "tree"`,
		`"old_head": "` + oldHead + `"`,
		`"new_head": "` + newHead + `"`,
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("merge JSON missing %q:\n%s", want, stdout)
		}
	}

	staleCI := writeReboundMergeEvidence(t, repo, oldHead, newHead, oldHead, id)
	stdout, _, err = captureOutput(t, func() error {
		return runVerify([]string{"merge", "--project", repo, "--evidence", staleCI, "--json"})
	})
	if err == nil || !strings.Contains(stdout, "ci_stale_sha") {
		t.Fatalf("rebinding must not carry CI forward: err=%v\n%s", err, stdout)
	}
}

func TestVerifyRebindRefusesSemanticDeltaAndWrongDirection(t *testing.T) {
	repo := seedReviewGitRepo(t)
	oldHead := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	mustVerifyRebindGit(t, repo, "commit", "--allow-empty", "-m", "equivalent rebuild")
	equivalentHead := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	id := oldHead + "--" + equivalentHead + ".json"
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

	wrongDirectionEvidence := writeReboundMergeEvidence(t, repo, equivalentHead, oldHead, oldHead, id)
	stdout, _, err := captureOutput(t, func() error {
		return runVerify([]string{"merge", "--project", repo, "--evidence", wrongDirectionEvidence, "--json"})
	})
	if err == nil || !strings.Contains(stdout, "review_rebinding_invalid") || !strings.Contains(stdout, "artifact direction") {
		t.Fatalf("want wrong-direction refusal: err=%v\n%s", err, stdout)
	}
}

func TestVerifyMergeAcceptsPatchIDRebindingOnlyAtRecordedNewBase(t *testing.T) {
	repo := seedReviewGitRepo(t)
	oldBase := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("review me\nfeature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustVerifyRebindGit(t, repo, "commit", "-am", "reviewed feature")
	oldHead := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")

	mustVerifyRebindGit(t, repo, "checkout", "--detach", oldBase)
	if err := os.WriteFile(filepath.Join(repo, "upstream.txt"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustVerifyRebindGit(t, repo, "add", "upstream.txt")
	mustVerifyRebindGit(t, repo, "commit", "-m", "new base")
	newBase := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "tracked.txt"), []byte("review me\nfeature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustVerifyRebindGit(t, repo, "commit", "-am", "rebuilt feature")
	newHead := mustVerifyRebindGit(t, repo, "rev-parse", "HEAD")
	id := oldHead + "--" + newHead + ".json"

	if _, _, err := captureOutput(t, func() error {
		return runVerify([]string{
			"rebind", "--project", repo,
			"--old-head", oldHead,
			"--new-head", newHead,
			"--proof", "patch-id",
			"--old-base", oldBase,
			"--new-base", newBase,
		})
	}); err != nil {
		t.Fatalf("record patch-id rebinding: %v", err)
	}
	evidence := writePatchReboundMergeEvidence(t, repo, oldHead, newHead, newBase, id, "valid")
	stdout, _, err := captureOutput(t, func() error {
		return runVerify([]string{"merge", "--project", repo, "--evidence", evidence, "--json"})
	})
	if err != nil || !strings.Contains(stdout, `"proof_type": "patch_id"`) {
		t.Fatalf("want valid patch-id carry: err=%v\n%s", err, stdout)
	}

	wrongBase := writePatchReboundMergeEvidence(t, repo, oldHead, newHead, oldBase, id, "wrong-base")
	stdout, _, err = captureOutput(t, func() error {
		return runVerify([]string{"merge", "--project", repo, "--evidence", wrongBase, "--json"})
	})
	if err == nil || !strings.Contains(stdout, "review_rebinding_invalid") || !strings.Contains(stdout, "artifact new_base") {
		t.Fatalf("want wrong-base refusal: err=%v\n%s", err, stdout)
	}
}

func TestVerifyMergeRejectsUnnecessaryRebinding(t *testing.T) {
	evidence := verifyMergeEvidence{
		HeadSHA:         "abc123",
		CI:              verifyMergeCheck{State: "success", SHA: "abc123"},
		Review:          verifyMergeCheck{State: "clean", SHA: "abc123"},
		ReviewRebinding: &verifyMergeReviewRebinding{ArtifactID: "anything"},
	}
	result := validateVerifyMergeEvidence(evidence)
	if result.OK || !hasVerifyMergeFailure(result, "review_rebinding_unnecessary") {
		t.Fatalf("want unnecessary rebinding failure, got %#v", result)
	}
}

func writePatchReboundMergeEvidence(t *testing.T, dir, reviewSHA, headSHA, base, id, suffix string) string {
	t.Helper()
	path := filepath.Join(dir, "patch-merge-evidence-"+suffix+".json")
	body := fmt.Sprintf(`{
  "subject": "PR #418",
  "head_sha": %q,
  "base": %q,
  "ci": {"state": "success", "sha": %q, "source": "ci", "checked_at": "2026-07-23T12:00:00Z"},
  "review": {"state": "clean", "sha": %q, "source": "cto", "checked_at": "2026-07-23T11:00:00Z"},
  "review_rebinding": {"artifact_id": %q},
  "exceptions": []
}`, headSHA, base, headSHA, reviewSHA, id)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func hasVerifyMergeFailure(result verifyMergeResult, code string) bool {
	for _, failure := range result.Failures {
		if failure.Code == code {
			return true
		}
	}
	return false
}

func writeReboundMergeEvidence(t *testing.T, dir, reviewSHA, headSHA, ciSHA, id string) string {
	t.Helper()
	path := filepath.Join(dir, "merge-evidence-"+ciSHA[:12]+".json")
	body := fmt.Sprintf(`{
  "subject": "PR #418",
  "head_sha": %q,
  "ci": {"state": "success", "sha": %q, "source": "ci", "checked_at": "2026-07-23T12:00:00Z"},
  "review": {"state": "clean", "sha": %q, "source": "cto", "checked_at": "2026-07-23T11:00:00Z"},
  "review_rebinding": {"artifact_id": %q},
  "exceptions": []
}`, headSHA, ciSHA, reviewSHA, id)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustVerifyRebindGit(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := gitOutput(repo, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(out)
}
