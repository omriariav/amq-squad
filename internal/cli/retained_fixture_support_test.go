package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

func isolateCanonicalContextTest(t *testing.T, project string) {
	t.Helper()
	resumeChdir(t, project)
	for _, key := range []string{"AM_ROOT", "AM_BASE_ROOT", "AM_SESSION", "AM_ME", "TMUX_PANE"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	previousScan, previousAlive := contextScanLaunchEntries, contextPIDAlive
	previousMatch, previousTTY := contextProcessMatch, contextProcessTTY
	previousStart, previousPaneTitle := contextProcessStartTime, contextPaneTitle
	contextScanLaunchEntries = func(string) ([]launch.Entry, error) { return nil, nil }
	contextPIDAlive = func(int) bool { return false }
	contextProcessMatch = func(int, func(string) bool) bool { return false }
	contextProcessTTY = func(int) (string, bool) { return "", false }
	contextProcessStartTime = func(int) (time.Time, bool) { return time.Time{}, false }
	contextPaneTitle = func(string) (string, bool) { return "", false }
	t.Cleanup(func() {
		contextScanLaunchEntries = previousScan
		contextPIDAlive = previousAlive
		contextProcessMatch = previousMatch
		contextProcessTTY = previousTTY
		contextProcessStartTime = previousStart
		contextPaneTitle = previousPaneTitle
	})
}

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
