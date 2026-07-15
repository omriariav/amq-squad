package compoundrelease

import (
	"fmt"
	"slices"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

const deliveryReceiptSchemaV2 = 2

// releaseDeliveryReceiptV2 mirrors the existing CLI-owned schema-2 durable
// receipt. It lives here only as a strict read contract; path selection and
// secure receipt I/O remain injected through ReconcileAdapter.
type releaseDeliveryReceiptV2 struct {
	SchemaVersion  int                         `json:"schema_version"`
	Generation     uint64                      `json:"generation"`
	AttemptID      string                      `json:"attempt_id"`
	Kind           string                      `json:"kind"`
	Method         string                      `json:"method,omitempty"`
	Status         string                      `json:"status"`
	Target         releaseDeliveryTargetV2     `json:"target"`
	MessageID      string                      `json:"message_id,omitempty"`
	Sender         string                      `json:"sender,omitempty"`
	Recipient      string                      `json:"recipient,omitempty"`
	Recipients     []string                    `json:"recipients,omitempty"`
	Consumers      []releaseDeliveryConsumerV2 `json:"consumers,omitempty"`
	DeliveryState  string                      `json:"delivery_state"`
	DrainedAt      *time.Time                  `json:"drained_at,omitempty"`
	FailedAt       *time.Time                  `json:"failed_at,omitempty"`
	LastCheckedAt  *time.Time                  `json:"last_checked_at,omitempty"`
	LastCheckError string                      `json:"last_check_error,omitempty"`
	NativeStage    string                      `json:"native_stage,omitempty"`
	EvidenceSource string                      `json:"evidence_source,omitempty"`
	AMQInvoked     bool                        `json:"amq_invoked"`
	TaskID         string                      `json:"task_id,omitempty"`
	OutboxIntentID string                      `json:"outbox_intent_id,omitempty"`
	Root           string                      `json:"root,omitempty"`
	Thread         string                      `json:"thread,omitempty"`
	PaneID         string                      `json:"pane_id,omitempty"`
	Fallback       bool                        `json:"fallback"`
	Acknowledged   bool                        `json:"acknowledged"`
	Stages         []releaseDeliveryStageV2    `json:"stages"`
	Detail         string                      `json:"detail,omitempty"`
	Path           string                      `json:"path,omitempty"`
	CreatedAt      time.Time                   `json:"created_at"`
}

type releaseDeliveryTargetV2 struct {
	ProjectDir    string `json:"project_dir,omitempty"`
	Profile       string `json:"profile"`
	Session       string `json:"session"`
	NamespaceID   string `json:"namespace_id"`
	Role          string `json:"role,omitempty"`
	Handle        string `json:"handle,omitempty"`
	ExecutionMode string `json:"execution_mode,omitempty"`
}

type releaseDeliveryConsumerV2 struct {
	Consumer  string     `json:"consumer"`
	State     string     `json:"state"`
	Stage     string     `json:"stage,omitempty"`
	DrainedAt *time.Time `json:"drained_at,omitempty"`
	FailedAt  *time.Time `json:"failed_at,omitempty"`
}

type releaseDeliveryStageV2 struct {
	State  string    `json:"state"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail,omitempty"`
}

type boundReleaseReceiptV2 struct {
	Record releaseDeliveryReceiptV2
	Tuple  *operatorauth.ReleaseDeliveryReceiptTuple
}

func decodeBoundReleaseReceiptV2(raw []byte, scope Scope, child operatorauth.ReleaseChildPlan, expectedPath, root string) (boundReleaseReceiptV2, error) {
	var receipt releaseDeliveryReceiptV2
	if err := operatorauth.DecodeStrictJSON(raw, &receipt); err != nil {
		return boundReleaseReceiptV2{}, fmt.Errorf("strict delivery receipt: %w", err)
	}
	if receipt.SchemaVersion != deliveryReceiptSchemaV2 {
		return boundReleaseReceiptV2{}, fmt.Errorf("unsupported delivery receipt schema %d", receipt.SchemaVersion)
	}
	if receipt.Generation == 0 || receipt.Generation < child.Receipt.MinimumGeneration || receipt.CreatedAt.IsZero() {
		return boundReleaseReceiptV2{}, fmt.Errorf("delivery receipt generation or creation time is invalid")
	}
	if receipt.AttemptID != child.Receipt.AttemptID || receipt.Kind != child.Receipt.Kind || receipt.Sender != child.Receipt.Sender || receipt.Recipient != child.Receipt.Recipient || !slices.Equal(receipt.Recipients, []string{child.Receipt.Recipient}) || receipt.Thread != child.Receipt.Thread || receipt.Path != expectedPath || receipt.Root != root {
		return boundReleaseReceiptV2{}, fmt.Errorf("delivery receipt transport tuple diverges")
	}
	wantTarget := releaseDeliveryTargetV2{
		ProjectDir: scope.ProjectDir, Profile: scope.Profile, Session: scope.Session,
		NamespaceID: child.Receipt.NamespaceID, Role: child.Role, Handle: child.Receipt.Recipient,
		ExecutionMode: "",
	}
	if receipt.Target != wantTarget {
		return boundReleaseReceiptV2{}, fmt.Errorf("delivery receipt target tuple diverges")
	}
	if receipt.Method != "durable_amq" || receipt.EvidenceSource != "amq_send_output" || receipt.PaneID != "" || receipt.Fallback || receipt.TaskID != "" || receipt.OutboxIntentID != "" {
		return boundReleaseReceiptV2{}, fmt.Errorf("delivery receipt carries unsupported release transport provenance")
	}
	bound := boundReleaseReceiptV2{Record: receipt}
	if receipt.MessageID == "" {
		return bound, nil
	}
	if !receipt.AMQInvoked {
		return boundReleaseReceiptV2{}, fmt.Errorf("delivery receipt has a message id before invocation")
	}
	if err := operatorauth.ValidateCanonicalSingleLineField("delivery receipt message id", receipt.MessageID, true); err != nil {
		return boundReleaseReceiptV2{}, err
	}
	tuple := operatorauth.ReleaseDeliveryReceiptTuple{
		AttemptID: receipt.AttemptID, Kind: receipt.Kind, Sender: receipt.Sender,
		Recipients: append([]string(nil), receipt.Recipients...), Thread: receipt.Thread,
		MessageID: receipt.MessageID, Path: receipt.Path, Root: receipt.Root,
		NamespaceID: receipt.Target.NamespaceID, TargetIdentity: child.Receipt.TargetIdentity,
		AdoptedGeneration: receipt.Generation,
	}
	if _, err := operatorauth.ReleaseDeliveryReceiptSHA256(tuple); err != nil {
		return boundReleaseReceiptV2{}, err
	}
	bound.Tuple = &tuple
	return bound, nil
}
