package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

func runThreadExec(t *testing.T, base, projectDir, session, threadID string, jsonOut bool, run func(threadAMQRequest) ([]byte, error)) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := executeThread(threadExecution{
		ProjectDir:  projectDir,
		Session:     session,
		Thread:      threadID,
		IncludeBody: true,
		Limit:       defaultThreadTranscriptLimit,
		BaseRoot:    base,
		Probe: state.Probe{
			PIDAlive:     func(pid int) bool { return true },
			ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
			Now:          func() time.Time { return threadsNow },
		},
		Out:          &out,
		JSON:         jsonOut,
		RunAMQThread: run,
	})
	return out.String(), err
}

func seedThreadSession(t *testing.T) (base, project string) {
	t.Helper()
	base = t.TempDir()
	project = t.TempDir()
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "codex", Handle: "cto", Role: "cto", Session: "issue-96", AgentPID: 111,
	})
	return base, project
}

func TestRunThreadReadsTranscriptThroughResolvedSessionRoot(t *testing.T) {
	base, project := seedThreadSession(t)
	var got []threadAMQRequest
	out, err := runThreadExec(t, base, project, "issue-96", "p2p/cto__fullstack", false, func(req threadAMQRequest) ([]byte, error) {
		got = append(got, req)
		return []byte("2026-06-01T09:00:00Z  cto  Review\nPlease check this\n---\n"), nil
	})
	if err != nil {
		t.Fatalf("thread: %v\n%s", err, out)
	}
	if len(got) != 1 {
		t.Fatalf("runner calls = %d, want 1", len(got))
	}
	if got[0].Root != base+"/issue-96" || got[0].Thread != "p2p/cto__fullstack" || !got[0].IncludeBody || got[0].Limit != defaultThreadTranscriptLimit || got[0].JSON {
		t.Fatalf("thread request mismatch: %+v", got[0])
	}
	for _, want := range []string{
		"# amq-squad thread",
		"# project: " + project,
		"# session: issue-96",
		"# root: " + base + "/issue-96",
		"# thread: p2p/cto__fullstack",
		"Please check this",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("thread output missing %q:\n%s", want, out)
		}
	}
}

func TestRunThreadJSONEnvelopeWrapsAMQEntries(t *testing.T) {
	base, project := seedThreadSession(t)
	out, err := runThreadExec(t, base, project, "issue-96", "decision/ship", true, func(req threadAMQRequest) ([]byte, error) {
		if !req.JSON {
			t.Fatalf("JSON run should ask AMQ for JSON: %+v", req)
		}
		return []byte(`[{"id":"m1","thread":"decision/ship"}]`), nil
	})
	if err != nil {
		t.Fatalf("thread --json: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[threadEnvelopeData](t, out)
	if env.Kind != "thread" {
		t.Fatalf("kind = %q, want thread", env.Kind)
	}
	if env.Data.ProjectDir != project || env.Data.BaseRoot != base || env.Data.Session != "issue-96" || env.Data.Thread != "decision/ship" {
		t.Fatalf("thread envelope scope mismatch: %+v", env.Data)
	}
	var entries []map[string]string
	if err := json.Unmarshal(env.Data.Entries, &entries); err != nil {
		t.Fatalf("thread entries should decode: %v\n%s", err, env.Data.Entries)
	}
	if len(entries) != 1 || entries[0]["id"] != "m1" || entries[0]["thread"] != "decision/ship" {
		t.Fatalf("thread entries mismatch: %+v", entries)
	}
}

func TestRunThreadRequiresSessionAndID(t *testing.T) {
	base, project := seedThreadSession(t)
	_, err := runThreadExec(t, base, project, "", "decision/ship", false, nil)
	if err == nil || !strings.Contains(err.Error(), "requires --session") {
		t.Fatalf("thread without session error = %v", err)
	}
	_, err = runThreadExec(t, base, project, "issue-96", "", false, nil)
	if err == nil || !strings.Contains(err.Error(), "requires --id") {
		t.Fatalf("thread without id error = %v", err)
	}
}

func TestRunThreadRejectsInvalidAMQJSON(t *testing.T) {
	base, project := seedThreadSession(t)
	_, err := runThreadExec(t, base, project, "issue-96", "decision/ship", true, func(threadAMQRequest) ([]byte, error) {
		return []byte("not json"), nil
	})
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("invalid AMQ JSON error = %v", err)
	}
}
