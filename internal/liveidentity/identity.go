// Package liveidentity defines the fail-closed contract that binds a durable
// squad actor to one launch record, one process, one wake consumer, and one
// terminal target. It deliberately keeps declared, persisted, and observed
// identity separate so callers cannot promote one kind of evidence into
// another by copying fields opportunistically.
package liveidentity

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// SchemaVersion is the additive structured-status/API schema version.
	SchemaVersion = 1
	// RecoveryAction is the single recovery offered for every failed binding.
	// Callers may add context around it, but must not synthesize a different
	// action for individual mismatch classes.
	RecoveryAction = "stop the contradictory runtime, then relaunch the actor from the current prepared generation"
	WakeRequired   = "required"
	WakeDisabled   = "disabled"
)

// Key is the durable authority key shared by every identity layer.
type Key struct {
	Project            string `json:"project"`
	Profile            string `json:"profile"`
	Session            string `json:"session"`
	Handle             string `json:"handle"`
	PreparedGeneration string `json:"prepared_generation"`
	PreparedDigest     string `json:"prepared_digest"`
	LaunchID           string `json:"launch_id"`
}

// Terminal identifies the exact terminal endpoint. PaneID is required for
// tmux; SessionID is required for native terminal backends.
type Terminal struct {
	Backend   string `json:"backend"`
	Target    string `json:"target"`
	Session   string `json:"session,omitempty"`
	WindowID  string `json:"window_id,omitempty"`
	PaneID    string `json:"pane_id,omitempty"`
	TabID     string `json:"tab_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
	TTY       string `json:"tty,omitempty"`
}

// Declared is roster/preparation intent. It is not process evidence.
type Declared struct {
	Key        Key      `json:"key"`
	Role       string   `json:"role"`
	Binary     string   `json:"binary"`
	Model      string   `json:"model"`
	WakePolicy string   `json:"wake_policy"`
	WakeMode   string   `json:"wake_mode"`
	WakeTarget string   `json:"wake_target"`
	Terminal   Terminal `json:"terminal"`
}

// LaunchRecord is immutable persisted launch evidence. PID and wake PID are
// declarations until independently observed alive and matching.
type LaunchRecord struct {
	Key        Key    `json:"key"`
	Role       string `json:"role"`
	Binary     string `json:"binary"`
	Model      string `json:"model"`
	PID        int    `json:"pid"`
	WakePID    int    `json:"wake_pid"`
	WakePolicy string `json:"wake_policy"`
	WakeMode   string `json:"wake_mode"`
	WakeTarget string `json:"wake_target"`
	// WakeRecordID and WakeRecordDigest identify the immutable AMQ wake record
	// used for exact retirement. They must not be inferred from PID alone.
	WakeRecordID     string   `json:"wake_record_id,omitempty"`
	WakeRecordDigest string   `json:"wake_record_digest,omitempty"`
	Terminal         Terminal `json:"terminal"`
}

// WakeConsumer is one independently observed wake process and exact target.
type WakeConsumer struct {
	PID          int    `json:"pid"`
	Handle       string `json:"handle"`
	Target       string `json:"target"`
	RecordID     string `json:"record_id"`
	RecordDigest string `json:"record_digest"`
	LaunchID     string `json:"launch_id"`
}

// Observed is live process/terminal evidence. Consumers must contain exactly
// one entry for a verified identity.
type Observed struct {
	Key           Key            `json:"key"`
	PID           int            `json:"pid"`
	Binary        string         `json:"binary"`
	Model         string         `json:"model"`
	Terminal      Terminal       `json:"terminal"`
	WakeConsumers []WakeConsumer `json:"wake_consumers"`
}

// Verified is emitted only after all three layers agree exactly.
type Verified struct {
	Key              Key      `json:"key"`
	Role             string   `json:"role"`
	Binary           string   `json:"binary"`
	Model            string   `json:"model"`
	PID              int      `json:"pid"`
	WakePID          int      `json:"wake_pid"`
	WakePolicy       string   `json:"wake_policy"`
	WakeMode         string   `json:"wake_mode"`
	WakeTarget       string   `json:"wake_target"`
	WakeRecordID     string   `json:"wake_record_id,omitempty"`
	WakeRecordDigest string   `json:"wake_record_digest,omitempty"`
	Terminal         Terminal `json:"terminal"`
	ConsumerCount    int      `json:"consumer_count"`
}

// Result is safe for structured status. Declared, LaunchRecord, and Observed
// are always retained even when verification fails.
type Result struct {
	SchemaVersion int          `json:"schema_version"`
	Declared      Declared     `json:"declared"`
	LaunchRecord  LaunchRecord `json:"launch_record"`
	Observed      Observed     `json:"observed"`
	Verified      *Verified    `json:"verified,omitempty"`
	Problems      []string     `json:"problems,omitempty"`
	Recovery      string       `json:"recovery,omitempty"`
}

// CanonicalProject resolves aliases and symlinks into one physical project
// identity. A missing path is rejected rather than normalized lexically.
func CanonicalProject(path string) (string, error) {
	abs, err := filepath.Abs(filepath.Clean(strings.TrimSpace(path)))
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("resolve physical project: %w", err)
	}
	return filepath.Clean(real), nil
}

// Verify binds declared, persisted, and observed identity. Any incomplete,
// stale, ambiguous, duplicate, or contradictory field fails closed.
func Verify(declared Declared, launch LaunchRecord, observed Observed) Result {
	result := Result{SchemaVersion: SchemaVersion, Declared: declared, LaunchRecord: launch, Observed: observed}
	problem := func(format string, args ...any) {
		result.Problems = append(result.Problems, fmt.Sprintf(format, args...))
	}

	for label, key := range map[string]Key{"declared": declared.Key, "launch_record": launch.Key, "observed": observed.Key} {
		if missing := missingKeyFields(key); len(missing) != 0 {
			problem("%s identity key missing %s", label, strings.Join(missing, ", "))
		}
	}
	if declared.Key != launch.Key || launch.Key != observed.Key {
		problem("declared, launch-record, and observed authority keys do not match exactly")
	}
	if declared.Role == "" || launch.Role == "" || declared.Role != launch.Role {
		problem("declared and launch-record roles are incomplete or contradictory")
	}
	if declared.Binary == "" || launch.Binary == "" || observed.Binary == "" || declared.Binary != launch.Binary || launch.Binary != observed.Binary {
		problem("declared, launch-record, and observed binaries are incomplete or contradictory")
	}
	if declared.Model == "" || launch.Model == "" || observed.Model == "" || declared.Model != launch.Model || launch.Model != observed.Model {
		problem("declared, launch-record, and observed models are incomplete or contradictory")
	}
	if launch.PID <= 0 || observed.PID <= 0 || launch.PID != observed.PID {
		problem("launch-record and observed process IDs are incomplete or contradictory")
	}
	if declared.WakePolicy == "" || launch.WakePolicy == "" || declared.WakePolicy != launch.WakePolicy || declared.WakePolicy != WakeRequired && declared.WakePolicy != WakeDisabled {
		problem("declared and launch-record wake policies are incomplete, unsupported, or contradictory")
	}
	if declared.WakeMode == "" || launch.WakeMode == "" || declared.WakeMode != launch.WakeMode {
		problem("declared and launch-record wake modes are incomplete or contradictory")
	}
	if !validTerminal(declared.Terminal) || declared.Terminal != launch.Terminal || launch.Terminal != observed.Terminal {
		problem("declared, launch-record, and observed terminal identities are incomplete or contradictory")
	}
	switch declared.WakePolicy {
	case WakeRequired:
		if declared.WakeTarget == "" || launch.WakeTarget == "" || declared.WakeTarget != launch.WakeTarget {
			problem("declared and launch-record wake targets are incomplete or contradictory")
		}
		if endpoint := terminalWakeTarget(declared.Terminal); endpoint == "" || declared.WakeTarget != endpoint {
			problem("pane-injection wake target does not match the exact terminal endpoint")
		}
		strictWakeRecord := launch.WakeRecordID != "" || launch.WakeRecordDigest != ""
		if strictWakeRecord && (launch.WakeRecordID == "" || launch.WakeRecordDigest == "") {
			problem("launch-record wake retirement identity is incomplete")
		}
		if launch.WakePID <= 0 {
			problem("launch-record wake PID is incomplete")
		}
		if len(observed.WakeConsumers) != 1 {
			problem("durable actor has %d live wake consumers; exactly one is required", len(observed.WakeConsumers))
		} else {
			consumer := observed.WakeConsumers[0]
			recordMismatch := strictWakeRecord && (consumer.RecordID != launch.WakeRecordID || consumer.RecordDigest != launch.WakeRecordDigest)
			if consumer.PID <= 0 || consumer.PID != launch.WakePID || consumer.Handle != declared.Key.Handle || consumer.Target != declared.WakeTarget ||
				recordMismatch || consumer.LaunchID != declared.Key.LaunchID {
				problem("observed wake consumer does not match launch PID, durable handle, exact target, record identity, and launch ID")
			}
		}
	case WakeDisabled:
		if declared.WakeMode != WakeDisabled || launch.WakeMode != WakeDisabled || declared.WakeTarget != "" || launch.WakeTarget != "" || launch.WakePID != 0 || launch.WakeRecordID != "" || launch.WakeRecordDigest != "" || len(observed.WakeConsumers) != 0 {
			problem("disabled wake policy carries a target, consumer, PID, record identity, or non-disabled mode")
		}
	}
	if len(result.Problems) != 0 {
		result.Recovery = RecoveryAction
		return result
	}
	result.Verified = &Verified{Key: declared.Key, Role: declared.Role, Binary: declared.Binary, Model: declared.Model, PID: observed.PID,
		WakePID: launch.WakePID, WakePolicy: declared.WakePolicy, WakeMode: declared.WakeMode, WakeTarget: declared.WakeTarget,
		WakeRecordID: launch.WakeRecordID, WakeRecordDigest: launch.WakeRecordDigest, Terminal: declared.Terminal, ConsumerCount: len(observed.WakeConsumers)}
	return result
}

func missingKeyFields(key Key) []string {
	fields := []struct{ name, value string }{
		{"project", key.Project}, {"profile", key.Profile}, {"session", key.Session}, {"handle", key.Handle},
		{"prepared_generation", key.PreparedGeneration}, {"prepared_digest", key.PreparedDigest}, {"launch_id", key.LaunchID},
	}
	var missing []string
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			missing = append(missing, field.name)
		}
	}
	return missing
}

func validTerminal(t Terminal) bool {
	if strings.TrimSpace(t.Backend) == "" || strings.TrimSpace(t.Target) == "" {
		return false
	}
	switch t.Backend {
	case "tmux":
		return strings.TrimSpace(t.Session) != "" && strings.TrimSpace(t.WindowID) != "" && strings.TrimSpace(t.PaneID) != ""
	case "iterm2":
		return strings.TrimSpace(t.SessionID) != "" && strings.TrimSpace(t.TTY) != ""
	default:
		return false
	}
}

func terminalWakeTarget(t Terminal) string {
	if strings.TrimSpace(t.PaneID) != "" {
		return strings.TrimSpace(t.PaneID)
	}
	return strings.TrimSpace(t.SessionID)
}
