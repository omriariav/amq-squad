package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func executeNotifyForTest(t *testing.T, n notifyExecution) string {
	t.Helper()
	raw := executeNotifyJSONForTest(t, n)
	data := decodeJSONEnvelope[notifyEnvelopeData](t, raw).Data
	return renderNotifyForTest(data)
}

func executeNotifyJSONForTest(t *testing.T, n notifyExecution) string {
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

func renderNotifyForTest(data notifyEnvelopeData) string {
	if !data.OperatorGates {
		return "amq-squad notify: " + data.Message + ".\n"
	}
	var out strings.Builder
	if len(data.Notifications) == 0 {
		if data.Suppressed > 0 {
			fmt.Fprintf(&out, "amq-squad notify: no new operator attention items (%d suppressed by throttle).\n", data.Suppressed)
		} else {
			fmt.Fprintln(&out, "amq-squad notify: no operator attention items.")
		}
		return out.String()
	}
	fmt.Fprintf(&out, "amq-squad notify: %d operator attention %s for %s\n", len(data.Notifications), pluralize(len(data.Notifications), "item", "items"), data.Operator.Handle)
	for _, n := range data.Notifications {
		reason := string(n.Reason)
		if reason == "" {
			reason = "generic"
		}
		escalation := ""
		if n.Escalation != "" {
			escalation = ", " + n.Escalation
		}
		fmt.Fprintf(&out, "- %s %s %s (%s%s, age %s)\n", n.Session, n.Thread, n.Subject, reason, escalation, n.Age)
		fmt.Fprintf(&out, "  inspect: %s\n", n.Inspect)
		fmt.Fprintf(&out, "  respond: %s\n", n.Respond)
	}
	if data.Suppressed > 0 {
		fmt.Fprintf(&out, "%d unchanged %s suppressed by throttle.\n", data.Suppressed, pluralize(data.Suppressed, "item", "items"))
	}
	return out.String()
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
	return seedNotifyLaunchProfile(t, project, base, team.DefaultProfile, session, handle)
}

func seedNotifyLaunchProfile(t *testing.T, project, base, profile, session, handle string) string {
	t.Helper()
	agentDir := filepath.Join(base, session, "agents", handle)
	if err := launch.Write(agentDir, launch.Record{
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

func collectCallVerbs(calls []amqCommandRequest) []string {
	var verbs []string
	for _, c := range calls {
		if len(c.Arg) > 0 {
			verbs = append(verbs, c.Arg[0])
		}
	}
	return verbs
}

func probeForNext() state.Probe {
	return state.Probe{
		PIDAlive:     func(pid int) bool { return true },
		ProcessMatch: func(pid int, _ func(args string) bool) bool { return true },
		Now:          func() time.Time { return notifyNow },
	}
}
