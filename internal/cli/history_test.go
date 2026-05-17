package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/internal/launch"
)

func TestRunHistoryScansCurrentProject(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	chdir(t, dir)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96",
		CWD: dir, StartedAt: time.Now().Add(-1 * time.Hour),
	})
	stdout, _, err := captureOutput(t, func() error {
		return runHistory(nil)
	})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	for _, want := range []string{"ROLE", "cto", "codex", "issue-96"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("history output missing %q in:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "WAKE") {
		t.Errorf("history must not emit wake column: %s", stdout)
	}
}

func TestRunHistoryJSONOmitsWakeField(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	chdir(t, dir)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96",
		CWD: dir, StartedAt: time.Now().Add(-1 * time.Hour),
	})
	stdout, _, err := captureOutput(t, func() error {
		return runHistory([]string{"--json"})
	})
	if err != nil {
		t.Fatalf("history --json: %v", err)
	}
	env := decodeJSONEnvelope[historyEnvelopeData](t, stdout)
	if env.Kind != "history" {
		t.Errorf("envelope kind = %q, want history", env.Kind)
	}
	if env.SchemaVersion != JSONSchemaVersion {
		t.Errorf("schema_version = %d, want %d", env.SchemaVersion, JSONSchemaVersion)
	}
	if len(env.Data.Records) != 1 || env.Data.Records[0].Role != "cto" {
		t.Fatalf("records = %+v, want one cto entry", env.Data.Records)
	}
	if strings.Contains(stdout, `"wake"`) {
		t.Errorf("history JSON leaked wake field:\n%s", stdout)
	}
}

func TestRunHistoryHonorsProjectFlag(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := t.TempDir()
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96",
		CWD: dir, StartedAt: time.Now().Add(-1 * time.Hour),
	})
	// chdir into an unrelated empty dir so default cwd has no records.
	empty := t.TempDir()
	chdir(t, empty)
	stdout, _, err := captureOutput(t, func() error {
		return runHistory([]string{"--project", dir})
	})
	if err != nil {
		t.Fatalf("history --project: %v", err)
	}
	if !strings.Contains(stdout, "cto") {
		t.Errorf("history --project did not scan target dir:\n%s", stdout)
	}
}
