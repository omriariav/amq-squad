package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

var notifyNow = time.Date(2026, 6, 22, 12, 0, 0, 0, time.UTC)

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
	if err := team.WriteProfile(project, team.DefaultProfile, cfg); err != nil {
		t.Fatal(err)
	}
	return project, base, filepath.Join(project, ".amq-squad", "notify-state.json")
}

func seedNotifyLaunch(t *testing.T, project, base, session, handle string) string {
	t.Helper()
	return seedNotifyLaunchProfile(t, project, base, team.DefaultProfile, session, handle)
}

func seedNotifyLaunchProfile(t *testing.T, project, base, profile, session, handle string) string {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := launch.WriteUnderRecordLock(agentDir, launch.Record{
		CWD: project, Binary: "codex", Handle: handle, Role: handle, Session: session,
		Root: filepath.Join(base, session), AgentPID: 42, StartedAt: notifyNow, TeamProfile: profile,
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
	ReplyTo string
	Refs    []string
	Created time.Time
	Context string
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
	context := ""
	if strings.TrimSpace(msg.Context) != "" {
		context = ",\n  \"context\": " + msg.Context
	}
	replyTo := ""
	if strings.TrimSpace(msg.ReplyTo) != "" {
		replyTo = ",\n  \"reply_to\": \"" + msg.ReplyTo + "\""
	}
	refs := ""
	if len(msg.Refs) > 0 {
		encoded, err := json.Marshal(msg.Refs)
		if err != nil {
			t.Fatal(err)
		}
		refs = ",\n  \"refs\": " + string(encoded)
	}
	body := "---json\n{\n" +
		"  \"schema\": 1,\n" +
		"  \"id\": \"" + msg.ID + "\",\n" +
		"  \"from\": \"" + msg.From + "\",\n" +
		"  \"to\": [\"" + msg.To + "\"],\n" +
		"  \"thread\": \"" + msg.Thread + "\",\n" +
		"  \"subject\": \"" + msg.Subject + "\",\n" +
		"  \"created\": \"" + msg.Created.UTC().Format(time.RFC3339Nano) + "\",\n" +
		"  \"kind\": \"" + msg.Kind + "\"" + replyTo + refs + context + "\n" +
		"}\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, msg.ID+".md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func probeForNext() state.Probe {
	return state.Probe{
		PIDAlive:     func(pid int) bool { return true },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
		Now:          func() time.Time { return notifyNow },
	}
}
