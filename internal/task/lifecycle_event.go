package task

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/namespace"
)

const LifecycleSchemaVersion = 1

type LifecycleEvent string

const (
	LifecycleACK        LifecycleEvent = "ACK"
	LifecycleProgress   LifecycleEvent = "PROGRESS"
	LifecycleCheckpoint LifecycleEvent = "CHECKPOINT"
	LifecycleReview     LifecycleEvent = "REVIEW"
	LifecycleBlock      LifecycleEvent = "BLOCK"
	LifecycleDone       LifecycleEvent = "DONE"
	LifecycleCancel     LifecycleEvent = "CANCEL"
)

type EvidenceRef struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	SHA256 string `json:"sha256"`
}

// GenerationRef is the exact immutable prepared-run identity copied into the
// runtime lifecycle. Runtime treats all four values as opaque correlation data
// and never mutates prepared-run state.
type GenerationRef struct {
	Generation     string `json:"generation"`
	ManifestDigest string `json:"manifest_digest"`
	GoalNamespace  string `json:"goal_namespace"`
	GoalDigest     string `json:"goal_digest"`
}

type LifecycleEnvelope struct {
	SchemaVersion     int            `json:"schema_version"`
	EventID           string         `json:"event_id"`
	TaskID            string         `json:"task_id"`
	Event             LifecycleEvent `json:"event"`
	Actor             string         `json:"actor"`
	Profile           string         `json:"profile"`
	Session           string         `json:"session"`
	NamespaceID       string         `json:"namespace_id"`
	RunGeneration     string         `json:"run_generation"`
	GenerationRef     GenerationRef  `json:"generation_ref"`
	TaskGeneration    string         `json:"task_generation"`
	DispatchMessageID string         `json:"dispatch_message_id"`
	OutboxIntentID    string         `json:"outbox_intent_id"`
	EvidenceRef       *EvidenceRef   `json:"evidence_ref,omitempty"`
	OccurredAt        time.Time      `json:"occurred_at"`
}

type LifecycleEventRecord struct {
	Envelope       LifecycleEnvelope `json:"envelope"`
	EnvelopeSHA256 string            `json:"envelope_sha256"`
}

func ParseLifecycleEvent(raw string) (LifecycleEvent, error) {
	event := LifecycleEvent(strings.ToUpper(strings.TrimSpace(raw)))
	switch event {
	case LifecycleACK, LifecycleProgress, LifecycleCheckpoint, LifecycleReview, LifecycleBlock, LifecycleDone, LifecycleCancel:
		return event, nil
	default:
		return "", fmt.Errorf("unsupported task lifecycle event %q", raw)
	}
}

func (e LifecycleEvent) RequiresEvidence() bool {
	switch e {
	case LifecycleReview, LifecycleBlock, LifecycleDone, LifecycleCancel:
		return true
	default:
		return false
	}
}

func ValidateLifecycleEnvelope(e LifecycleEnvelope) error {
	if e.SchemaVersion != LifecycleSchemaVersion {
		return fmt.Errorf("unsupported task lifecycle schema_version %d", e.SchemaVersion)
	}
	if _, err := ParseLifecycleEvent(string(e.Event)); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"event_id": e.EventID, "task_id": e.TaskID, "actor": e.Actor,
		"profile": e.Profile, "session": e.Session, "namespace_id": e.NamespaceID,
		"run_generation": e.RunGeneration, "task_generation": e.TaskGeneration,
		"dispatch_message_id": e.DispatchMessageID, "outbox_intent_id": e.OutboxIntentID,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("task lifecycle %s must be non-empty canonical single-line text", name)
		}
	}
	if !canonicalTaskID(e.TaskID) {
		return fmt.Errorf("task lifecycle task_id %q is not canonical", e.TaskID)
	}
	if e.Profile != namespace.NormalizeProfile(e.Profile) || e.NamespaceID != namespace.ID(e.Profile, e.Session) {
		return fmt.Errorf("task lifecycle namespace identity is inconsistent")
	}
	if err := ValidateGenerationRef(e.GenerationRef); err != nil {
		return err
	}
	if e.RunGeneration != e.GenerationRef.Generation {
		return fmt.Errorf("task lifecycle run_generation does not match generation_ref.generation")
	}
	if e.GenerationRef.GoalNamespace != e.NamespaceID {
		return fmt.Errorf("task lifecycle generation_ref.goal_namespace does not match namespace_id")
	}
	if e.OccurredAt.IsZero() {
		return fmt.Errorf("task lifecycle occurred_at is required")
	}
	if e.Event.RequiresEvidence() {
		if e.EvidenceRef == nil {
			return fmt.Errorf("task lifecycle %s requires evidence_ref", e.Event)
		}
	}
	if e.EvidenceRef != nil {
		if err := ValidateEvidenceRef(*e.EvidenceRef); err != nil {
			return err
		}
	}
	return nil
}

func ValidateGenerationRef(ref GenerationRef) error {
	for name, value := range map[string]string{
		"generation": ref.Generation, "manifest_digest": ref.ManifestDigest,
		"goal_namespace": ref.GoalNamespace, "goal_digest": ref.GoalDigest,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("task lifecycle generation_ref.%s must be non-empty canonical single-line text", name)
		}
	}
	return nil
}

func ValidateEvidenceRef(ref EvidenceRef) error {
	if strings.TrimSpace(ref.Kind) == "" || ref.Kind != strings.TrimSpace(ref.Kind) ||
		strings.TrimSpace(ref.ID) == "" || ref.ID != strings.TrimSpace(ref.ID) {
		return fmt.Errorf("task lifecycle evidence_ref kind and id are required and trim-canonical")
	}
	if len(ref.SHA256) != 64 || strings.ToLower(ref.SHA256) != ref.SHA256 {
		return fmt.Errorf("task lifecycle evidence_ref sha256 must be 64 lowercase hexadecimal characters")
	}
	if _, err := hex.DecodeString(ref.SHA256); err != nil {
		return fmt.Errorf("task lifecycle evidence_ref sha256 must be 64 lowercase hexadecimal characters")
	}
	return nil
}

func LifecycleEnvelopeSHA256(e LifecycleEnvelope) (string, error) {
	if err := ValidateLifecycleEnvelope(e); err != nil {
		return "", err
	}
	b, err := json.Marshal(e)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func DecodeLifecycleEnvelope(context map[string]any) (LifecycleEnvelope, bool, error) {
	if context == nil {
		return LifecycleEnvelope{}, false, nil
	}
	raw, ok := context["amq_squad"]
	if !ok {
		return LifecycleEnvelope{}, false, nil
	}
	root, ok := raw.(map[string]any)
	if !ok {
		return LifecycleEnvelope{}, true, fmt.Errorf("amq_squad context must be an object")
	}
	raw, ok = root["task_lifecycle"]
	if !ok {
		return LifecycleEnvelope{}, false, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return LifecycleEnvelope{}, true, err
	}
	var envelope LifecycleEnvelope
	if err := json.Unmarshal(b, &envelope); err != nil {
		return LifecycleEnvelope{}, true, fmt.Errorf("decode task lifecycle envelope: %w", err)
	}
	if err := ValidateLifecycleEnvelope(envelope); err != nil {
		return envelope, true, err
	}
	return envelope, true, nil
}

func LifecycleContextJSON(e LifecycleEnvelope) (string, error) {
	if err := ValidateLifecycleEnvelope(e); err != nil {
		return "", err
	}
	b, err := json.Marshal(map[string]any{"amq_squad": map[string]any{"task_lifecycle": e}})
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func newTaskGeneration(taskID, actor string, now time.Time, previous string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{taskID, actor, now.UTC().Format(time.RFC3339Nano), previous}, "\x00")))
	return hex.EncodeToString(sum[:])
}
