package attention

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
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

func SelfApprovedKey(profile, session, thread string) string {
	return profile + "/" + session + "\x00self_approved\x00" + thread
}
func HumanOnlyGateKey(profile, session, thread string) string {
	return profile + "/" + session + "\x00human_only_gate\x00" + thread
}

func GateKey(profile, session, thread string) string {
	return profile + "/" + session + "\x00gate\x00" + thread
}
func LocalInputKey(profile, session, role string) string {
	return profile + "/" + session + "\x00local_input_blocked\x00" + role
}
func LocalInputFingerprint(pane, kind string, destructive bool, summary string) string {
	text := strings.Join([]string{pane, kind, strings.ToLower(strings.TrimSpace(summary)), map[bool]string{true: "1", false: "0"}[destructive]}, "\x00")
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
