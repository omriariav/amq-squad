package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var notifyNow = time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

func TestNotifyEmitsAndDedupesOperatorGate(t *testing.T) {
	project, base, statePath := seedNotifyProject(t, team.OperatorConfig{Enabled: true, Handle: team.DefaultOperatorHandle})
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
		ID: "m1", From: "cto", To: "user", Thread: "gate/spawn-dev",
		Subject: "APPROVAL: spawn dev", Kind: "question", Created: notifyNow.Add(-10 * time.Minute),
	})

	first := executeNotifyForTest(t, notifyExecution{
		ProjectDir: project, Profile: team.DefaultProfile, BaseRoot: base, StatePath: statePath,
		RenotifyAfter: time.Hour, Now: func() time.Time { return notifyNow },
	})
	for _, want := range []string{"1 operator attention item", "gate/spawn-dev", "APPROVAL: spawn dev", "amq-squad thread", "APPROVED:"} {
		if !strings.Contains(first, want) {
			t.Fatalf("first notify output missing %q:\n%s", want, first)
		}
	}

	second := executeNotifyForTest(t, notifyExecution{
		ProjectDir: project, Profile: team.DefaultProfile, BaseRoot: base, StatePath: statePath,
		RenotifyAfter: time.Hour, Now: func() time.Time { return notifyNow.Add(10 * time.Minute) },
	})
	if !strings.Contains(second, "no new operator attention items") || !strings.Contains(second, "suppressed by throttle") {
		t.Fatalf("second notify should be throttled, got:\n%s", second)
	}
}

func TestNotifyRenotifiesAfterThreshold(t *testing.T) {
	project, base, statePath := seedNotifyProject(t, team.OperatorConfig{Enabled: true, Handle: team.DefaultOperatorHandle})
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
		ID: "m1", From: "cto", To: "user", Thread: "gate/merge",
		Subject: "APPROVAL: merge", Kind: "question", Created: notifyNow.Add(-5 * time.Minute),
	})

	_ = executeNotifyForTest(t, notifyExecution{
		ProjectDir: project, Profile: team.DefaultProfile, BaseRoot: base, StatePath: statePath,
		RenotifyAfter: 30 * time.Minute, Now: func() time.Time { return notifyNow },
	})
	out := executeNotifyForTest(t, notifyExecution{
		ProjectDir: project, Profile: team.DefaultProfile, BaseRoot: base, StatePath: statePath,
		RenotifyAfter: 30 * time.Minute, Now: func() time.Time { return notifyNow.Add(31 * time.Minute) },
	})
	if !strings.Contains(out, "APPROVAL: merge") {
		t.Fatalf("expected stale-threshold re-notification, got:\n%s", out)
	}
}

func TestNotifyUsesCustomOperatorHandle(t *testing.T) {
	project, base, statePath := seedNotifyProject(t, team.OperatorConfig{Enabled: true, Handle: "ops"})
	seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyMessage(t, base, "s", "user", "new", notifyMsg{
		ID: "wrong", From: "cto", To: "user", Thread: "gate/user",
		Subject: "APPROVAL: wrong operator", Kind: "question", Created: notifyNow.Add(-time.Minute),
	})
	seedNotifyMessage(t, base, "s", "ops", "new", notifyMsg{
		ID: "right", From: "cto", To: "ops", Thread: "gate/ops",
		Subject: "APPROVAL: ops decision", Kind: "question", Created: notifyNow.Add(-time.Minute),
	})

	out := executeNotifyForTest(t, notifyExecution{
		ProjectDir: project, Profile: team.DefaultProfile, BaseRoot: base, StatePath: statePath,
		RenotifyAfter: time.Hour, Now: func() time.Time { return notifyNow },
	})
	if !strings.Contains(out, "for ops") || !strings.Contains(out, "gate/ops") {
		t.Fatalf("custom operator output missing ops gate:\n%s", out)
	}
	if strings.Contains(out, "wrong operator") || strings.Contains(out, "gate/user") {
		t.Fatalf("custom operator output included default user gate:\n%s", out)
	}
}

func TestNotifyIgnoresP2PProseOnlyNeedsYou(t *testing.T) {
	project, base, statePath := seedNotifyProject(t, team.OperatorConfig{Enabled: true, Handle: team.DefaultOperatorHandle})
	ctoDir := seedNotifyLaunch(t, project, base, "s", "cto")
	seedNotifyLaunch(t, project, base, "s", "dev")
	seedNotifyMessageToDir(t, ctoDir, "new", notifyMsg{
		ID: "prose", From: "dev", To: "cto", Thread: "p2p/cto__dev",
		Subject: "waiting for operator approval", Kind: "status", Created: notifyNow.Add(-time.Minute),
	})

	out := executeNotifyForTest(t, notifyExecution{
		ProjectDir: project, Profile: team.DefaultProfile, BaseRoot: base, StatePath: statePath,
		RenotifyAfter: time.Hour, Now: func() time.Time { return notifyNow },
	})
	if strings.Contains(out, "p2p/cto__dev") || strings.Contains(out, "operator approval") {
		t.Fatalf("notify must not emit p2p prose-only needs-you threads:\n%s", out)
	}
	if !strings.Contains(out, "no operator attention items") {
		t.Fatalf("expected no operator attention items, got:\n%s", out)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("notify should still write empty throttle state for enabled profiles: %v", err)
	}
}

func TestNotifyNoOperatorReportsDisabled(t *testing.T) {
	project, base, statePath := seedNotifyProject(t, team.OperatorConfig{Enabled: false})
	seedNotifyLaunch(t, project, base, "s", "cto")

	out := executeNotifyForTest(t, notifyExecution{
		ProjectDir: project, Profile: team.DefaultProfile, BaseRoot: base, StatePath: statePath,
		RenotifyAfter: time.Hour, Now: func() time.Time { return notifyNow },
	})
	if !strings.Contains(out, "operator gates disabled") {
		t.Fatalf("disabled operator output mismatch:\n%s", out)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("disabled notify should not write state, stat err = %v", err)
	}
}

func executeNotifyForTest(t *testing.T, n notifyExecution) string {
	t.Helper()
	var buf bytes.Buffer
	n.Out = &buf
	n.Probe = state.Probe{
		PIDAlive:     func(pid int) bool { return true },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
		Now: func() time.Time {
			if n.Now != nil {
				return n.Now()
			}
			return notifyNow
		},
	}
	if err := executeNotify(n); err != nil {
		t.Fatalf("executeNotify: %v", err)
	}
	return buf.String()
}

func seedNotifyProject(t *testing.T, op team.OperatorConfig) (project, base, statePath string) {
	t.Helper()
	project = t.TempDir()
	base = filepath.Join(project, ".agent-mail")
	cfg := team.Team{
		Project:    project,
		Workstream: "s",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "s"},
		},
	}
	if op.Handle != "" || !op.Enabled {
		cfg.Operator = &op
	}
	if err := team.Write(project, cfg); err != nil {
		t.Fatal(err)
	}
	return project, base, filepath.Join(project, ".amq-squad", "notify-state.json")
}

func seedNotifyLaunch(t *testing.T, project, base, session, handle string) string {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := launch.Write(agentDir, launch.Record{
		CWD: project, Binary: "codex", Handle: handle, Role: handle, Session: session,
		Root: filepath.Join(base, session), AgentPID: 42, StartedAt: notifyNow,
	}); err != nil {
		t.Fatal(err)
	}
	return agentDir
}

type notifyMsg struct {
	ID      string
	From    string
	To      string
	Thread  string
	Subject string
	Kind    string
	Created time.Time
}

func seedNotifyMessage(t *testing.T, base, session, owner, box string, msg notifyMsg) {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", owner)
	seedNotifyMessageToDir(t, agentDir, box, msg)
}

func seedNotifyMessageToDir(t *testing.T, agentDir, box string, msg notifyMsg) {
	t.Helper()
	dir := filepath.Join(agentDir, "inbox", box)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if msg.Created.IsZero() {
		msg.Created = notifyNow
	}
	body := "---json\n{\n" +
		"  \"schema\": 1,\n" +
		"  \"id\": \"" + msg.ID + "\",\n" +
		"  \"from\": \"" + msg.From + "\",\n" +
		"  \"to\": [\"" + msg.To + "\"],\n" +
		"  \"thread\": \"" + msg.Thread + "\",\n" +
		"  \"subject\": \"" + msg.Subject + "\",\n" +
		"  \"created\": \"" + msg.Created.UTC().Format(time.RFC3339Nano) + "\",\n" +
		"  \"kind\": \"" + msg.Kind + "\"\n" +
		"}\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, msg.ID+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
