package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func seedThreadMessage(t *testing.T, agentDir, box, id, from string, to []string, thread, subject, kind string, created time.Time, bodyOverride ...string) {
	t.Helper()
	dir := filepath.Join(agentDir, "inbox", box)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var recipients []string
	for _, r := range to {
		recipients = append(recipients, fmt.Sprintf("%q", r))
	}
	messageBody := "body for " + id
	if len(bodyOverride) > 0 {
		messageBody = bodyOverride[0]
	}
	body := fmt.Sprintf(`---json
{
  "schema": 1,
  "id": %q,
  "from": %q,
  "to": [%s],
  "thread": %q,
  "subject": %q,
  "created": %q,
  "kind": %q
}
---
%s
`, id, from, strings.Join(recipients, ", "), thread, subject, created.UTC().Format(time.RFC3339Nano), kind, messageBody)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

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

// seedBrief writes a brief file for (projectDir, session) and returns its path.

// TestRmDeclinedLeavesFilesUntouched: the confirm gate defaults to NO, and a
// decline (answer "n") must make ZERO filesystem changes.

func isolateCanonicalContextTest(t *testing.T, project string) {
	t.Helper()
	resumeChdir(t, project)
	for _, key := range []string{"AM_ROOT", "AM_BASE_ROOT", "AM_SESSION", "AM_ME"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}
