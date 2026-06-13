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

var threadsNow = time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC)

func runThreadsExec(t *testing.T, base, projectDir, session string, limit int, jsonOut bool) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := executeThreads(threadsExecution{
		ProjectDir: projectDir,
		Session:    session,
		Limit:      limit,
		BaseRoot:   base,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return threadsNow },
		},
		Now:  func() time.Time { return threadsNow },
		Out:  &out,
		JSON: jsonOut,
	})
	return out.String(), err
}

func seedThreadMessage(t *testing.T, agentDir, box, id, from string, to []string, thread, subject, kind string, created time.Time) {
	t.Helper()
	dir := filepath.Join(agentDir, "inbox", box)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var recipients []string
	for _, r := range to {
		recipients = append(recipients, fmt.Sprintf("%q", r))
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
body for %s
`, id, from, strings.Join(recipients, ", "), thread, subject, created.UTC().Format(time.RFC3339Nano), kind, id)
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedThreadsFixture(t *testing.T) (base, project string) {
	t.Helper()
	base = t.TempDir()
	project = t.TempDir()
	ctoDir := seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-96", AgentPID: 111,
	})
	seniorDir := seedAgentRecord(t, base, "issue-96", "senior-dev", launch.Record{
		Binary: "codex", Handle: "senior-dev", Role: "senior-dev", Session: "issue-96", AgentPID: 222,
	})
	seedThreadMessage(t, ctoDir, "new", "rr1", "senior-dev", []string{"cto"},
		"p2p/cto__senior-dev", "Review PR", string(state.KindReviewRequest), threadsNow.Add(-2*time.Hour))
	seedThreadMessage(t, seniorDir, "cur", "st1", "cto", []string{"senior-dev"},
		"status/check", "Status update", string(state.KindStatus), threadsNow.Add(-5*time.Minute))
	return base, project
}

func TestRunThreadsHumanListsDerivedThreads(t *testing.T) {
	base, project := seedThreadsFixture(t)
	out, err := runThreadsExec(t, base, project, "issue-96", defaultThreadsLimit, false)
	if err != nil {
		t.Fatalf("threads: %v\n%s", err, out)
	}
	for _, want := range []string{
		"# amq-squad threads",
		"# project: " + project,
		"# session: issue-96",
		"TRIAGE",
		"p2p/cto__senior-dev",
		"Review PR",
		"at-risk",
		"awaiting-reply",
		"cto,senior-dev",
		"cto",
		"status/check",
		"5m ago",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("threads output missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "p2p/cto__senior-dev") > strings.Index(out, "status/check") {
		t.Fatalf("at-risk thread should sort before clear status thread:\n%s", out)
	}
}

func TestRunThreadsJSONEnvelope(t *testing.T) {
	base, project := seedThreadsFixture(t)
	out, err := runThreadsExec(t, base, project, "issue-96", 1, true)
	if err != nil {
		t.Fatalf("threads --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[threadsEnvelopeData](t, out)
	if env.Kind != "threads" {
		t.Fatalf("kind = %q, want threads", env.Kind)
	}
	if env.Data.ProjectDir != project || env.Data.BaseRoot != base || env.Data.Session != "issue-96" {
		t.Fatalf("threads envelope scope mismatch: %+v", env.Data)
	}
	if env.Data.ThreadCount != 2 || env.Data.ReturnedCount != 1 || env.Data.Limit != 1 {
		t.Fatalf("threads count/limit mismatch: %+v", env.Data)
	}
	if len(env.Data.Threads) != 1 || env.Data.Threads[0].ID != "p2p/cto__senior-dev" {
		t.Fatalf("threads JSON rows mismatch: %+v", env.Data.Threads)
	}
	if env.Data.Threads[0].Triage != string(state.TriageAtRisk) || env.Data.Threads[0].Status != string(state.ThreadAwaitingReply) {
		t.Fatalf("thread triage/status mismatch: %+v", env.Data.Threads[0])
	}
}

func TestRunThreadsRequiresSession(t *testing.T) {
	_, err := runThreadsExec(t, t.TempDir(), t.TempDir(), "", defaultThreadsLimit, false)
	if err == nil {
		t.Fatal("threads without --session should fail")
	}
	if !strings.Contains(err.Error(), "requires --session") {
		t.Fatalf("error should mention required session, got %v", err)
	}
}

func TestRunThreadsMissingSession(t *testing.T) {
	base, project := seedThreadsFixture(t)
	_, err := runThreadsExec(t, base, project, "missing", defaultThreadsLimit, false)
	if err == nil {
		t.Fatal("missing session should fail")
	}
	if !strings.Contains(err.Error(), "session \"missing\" not found") {
		t.Fatalf("error should mention missing session, got %v", err)
	}
}
