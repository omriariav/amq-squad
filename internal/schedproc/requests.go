package schedproc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// The lead's decisions reach the scheduler as AMQ messages (R1 of the 2b-2
// milestone B first pass): a lead-only verb never takes identity from a
// flag or the environment. `task widen` sends the decision to the
// scheduler's mailbox; the scheduler process drains it, checks the sender
// against the lead handle of its own launch record (written by the core at
// admission) and the task against its squad, and only then applies it.

// LaunchRecordName is the scheduler process's launch record under Dir.
const LaunchRecordName = "launch.json"

// LaunchRecord is what the core records when a scheduler process is
// admitted: the squad it schedules and the lead whose decisions it accepts.
type LaunchRecord struct {
	Schema      int       `json:"schema"`
	Lead        string    `json:"lead"`
	Root        string    `json:"root"`
	Session     string    `json:"session,omitempty"`
	ProjectRoot string    `json:"project_root,omitempty"`
	PID         int       `json:"pid"`
	StartedAt   time.Time `json:"started_at"`
}

// WriteLaunchRecord writes the record (0600 in the 0700 scheduler dir).
func WriteLaunchRecord(root string, rec LaunchRecord) error {
	rec.Schema = 1
	return writeJSON(filepath.Join(Dir(root), LaunchRecordName), rec)
}

// ReadLaunchRecord reads the admitted process's record.
func ReadLaunchRecord(root string) (LaunchRecord, error) {
	var rec LaunchRecord
	raw, err := os.ReadFile(filepath.Join(Dir(root), LaunchRecordName))
	if err != nil {
		return rec, err
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		return rec, err
	}
	if rec.Schema != 1 || rec.Lead == "" || rec.Root != root {
		return rec, fmt.Errorf("the scheduler launch record is not this squad's")
	}
	return rec, nil
}

// Message is one AMQ message drained from the scheduler's mailbox.
type Message struct {
	ID      string    `json:"id"`
	From    string    `json:"from"`
	To      []string  `json:"to"`
	Thread  string    `json:"thread"`
	Subject string    `json:"subject"`
	Kind    string    `json:"kind"`
	Body    string    `json:"body"`
	Created time.Time `json:"created"`
}

// Drain moves the scheduler's new messages to cur and returns them (amq
// drain: a receipt per message; quiet when empty).
func (s Sender) Drain() ([]Message, error) {
	run := s.Run
	if run == nil {
		run = defaultRun
	}
	env := append(redact.StripEnv(scrubAMQ(os.Environ())), "AM_ME="+Handle, "AM_ROOT="+s.Root, "AMQ_NO_UPDATE_CHECK=1")
	if s.BaseRoot != "" {
		env = append(env, "AM_BASE_ROOT="+s.BaseRoot)
	}
	if s.Session != "" {
		env = append(env, "AM_SESSION="+s.Session)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := run(ctx, firstNonEmpty(s.AMQ, "amq"), []string{"drain", "--me", Handle, "--json", "--include-body", "--limit", "50"}, env)
	if err != nil {
		return nil, fmt.Errorf("amq drain: %v: %s", err, strings.TrimSpace(string(out)))
	}
	text := strings.TrimSpace(string(out))
	if text == "" {
		return nil, nil
	}
	if i := strings.IndexByte(text, '{'); i > 0 {
		text = text[i:]
	}
	var got struct {
		Drained []Message `json:"drained"`
	}
	if err := json.Unmarshal([]byte(text), &got); err != nil {
		return nil, fmt.Errorf("amq drain returned no message list")
	}
	return got.Drained, nil
}

// Reply sends a status message to a handle (the decision on a request).
func (s Sender) Reply(to, subject, body string) error { return s.send(to, "status", subject, body) }

// WidenRequestKind marks a widening in a message body.
const WidenRequestKind = "amq-squad.widen.v1"

// WidenRequest is the body of the lead's widening message.
type WidenRequest struct {
	Kind   string   `json:"kind"`
	Task   string   `json:"task"`
	Files  []string `json:"files"`
	Reason string   `json:"reason"`
}

// ParseWidenRequest reads a widening from a message body ("" kind: not a
// widening).
func ParseWidenRequest(body string) (WidenRequest, bool) {
	var r WidenRequest
	if json.Unmarshal([]byte(strings.TrimSpace(body)), &r) != nil || r.Kind != WidenRequestKind {
		return r, false
	}
	return r, true
}
