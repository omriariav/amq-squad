package attention

import (
	"time"
)

type Event struct {
	SchemaVersion  int       `json:"schema_version"`
	EventType      string    `json:"event_type"`
	Key            string    `json:"key"`
	Fingerprint    string    `json:"fingerprint,omitempty"`
	ProjectDir     string    `json:"project_dir"`
	Profile        string    `json:"profile"`
	Session        string    `json:"session"`
	Thread         string    `json:"thread,omitempty"`
	Role           string    `json:"role,omitempty"`
	GateKind       string    `json:"gate_kind,omitempty"`
	Actor          string    `json:"actor,omitempty"`
	PolicyRevision int64     `json:"policy_revision,omitempty"`
	Subject        string    `json:"subject,omitempty"`
	Summary        string    `json:"summary"`
	Escalation     string    `json:"escalation,omitempty"`
	Age            string    `json:"age,omitempty"`
	InspectCommand string    `json:"inspect_command"`
	AttentionOnly  bool      `json:"attention_only"`
	ObservedAt     time.Time `json:"observed_at"`
	Cleared        bool      `json:"-"`
}

func GateKey(profile, session, thread string) string {
	return profile + "/" + session + "\x00gate\x00" + thread
}
