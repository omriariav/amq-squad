package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	deliveryReceiptSchemaVersion = 2

	deliveryStateAmbiguousUnknown       = "ambiguous_unknown"
	deliveryStateCommittedIndeterminate = "committed_indeterminate"
	deliveryStateFailed                 = "delivery_failed"
	deliveryStateDeliveredNotDrained    = "delivered_not_drained"
	deliveryStatePartiallyDrained       = "partially_drained"
	deliveryStateDrained                = "drained"
	deliveryStateReconciledExisting     = "reconciled_existing"
)

var (
	receiptBeforeSecureOpen   = func() {}
	receiptBeforeSecureRename = func() {}
	receiptBeforeRootOpen     = func(string) {}
	persistDeliveryReceipt    = writeDeliveryReceipt
	deliveryAttemptSequence   atomic.Uint64
)

type deliveryReceiptData struct {
	SchemaVersion                     int                     `json:"schema_version"`
	Generation                        uint64                  `json:"generation"`
	AttemptID                         string                  `json:"attempt_id"`
	Kind                              string                  `json:"kind"`
	Method                            string                  `json:"method,omitempty"`
	Status                            string                  `json:"status"`
	Target                            deliveryReceiptTarget   `json:"target"`
	MessageID                         string                  `json:"message_id,omitempty"`
	CommittedPath                     string                  `json:"committed_path,omitempty"`
	ReconciledMessageID               string                  `json:"reconciled_message_id,omitempty"`
	Sender                            string                  `json:"sender,omitempty"`
	Recipient                         string                  `json:"recipient,omitempty"`
	Recipients                        []string                `json:"recipients,omitempty"`
	Consumers                         []deliveryConsumerState `json:"consumers,omitempty"`
	DeliveryState                     string                  `json:"delivery_state"`
	DrainedAt                         *time.Time              `json:"drained_at,omitempty"`
	FailedAt                          *time.Time              `json:"failed_at,omitempty"`
	LastCheckedAt                     *time.Time              `json:"last_checked_at,omitempty"`
	LastCheckError                    string                  `json:"last_check_error,omitempty"`
	NativeStage                       string                  `json:"native_stage,omitempty"`
	EvidenceSource                    string                  `json:"evidence_source,omitempty"`
	AMQInvoked                        bool                    `json:"amq_invoked"`
	TaskID                            string                  `json:"task_id,omitempty"`
	CurrentActorImplementationAllowed *bool                   `json:"current_actor_implementation_allowed,omitempty"`
	LeadImplementationAllowed         *bool                   `json:"lead_implementation_allowed,omitempty"`
	LeadershipEpoch                   *uint64                 `json:"leadership_epoch,omitempty"`
	OutboxIntentID                    string                  `json:"outbox_intent_id,omitempty"`
	LifecycleEventID                  string                  `json:"lifecycle_event_id,omitempty"`
	LifecycleEvent                    string                  `json:"lifecycle_event,omitempty"`
	LifecycleTaskGeneration           string                  `json:"lifecycle_task_generation,omitempty"`
	Root                              string                  `json:"root,omitempty"`
	Thread                            string                  `json:"thread,omitempty"`
	PaneID                            string                  `json:"pane_id,omitempty"`
	Fallback                          bool                    `json:"fallback"`
	Acknowledged                      bool                    `json:"acknowledged"`
	Stages                            []deliveryReceiptStage  `json:"stages"`
	Detail                            string                  `json:"detail,omitempty"`
	Path                              string                  `json:"path,omitempty"`
	CreatedAt                         time.Time               `json:"created_at"`
	PreparedRunGeneration             string                  `json:"prepared_run_generation,omitempty"`
	PreparedRunLaunchAttempt          string                  `json:"prepared_run_launch_attempt,omitempty"`
	PreparedRunDigest                 string                  `json:"prepared_run_digest,omitempty"`
	PreparedRunGoalNamespace          string                  `json:"prepared_run_goal_namespace,omitempty"`
	PreparedRunGoalDigest             string                  `json:"prepared_run_goal_digest,omitempty"`
}

func applyPreparedRunTokenToReceipt(receipt *deliveryReceiptData, token preparedRunToken) {
	if receipt == nil || token.empty() {
		return
	}
	receipt.PreparedRunGeneration = token.Generation
	receipt.PreparedRunLaunchAttempt = token.LaunchAttempt
	receipt.PreparedRunDigest = token.ManifestDigest
	receipt.PreparedRunGoalNamespace = token.GoalNamespace
	receipt.PreparedRunGoalDigest = token.GoalDigest
}

type deliveryConsumerState struct {
	Consumer  string     `json:"consumer"`
	State     string     `json:"state"`
	Stage     string     `json:"stage,omitempty"`
	DrainedAt *time.Time `json:"drained_at,omitempty"`
	FailedAt  *time.Time `json:"failed_at,omitempty"`
}

type deliveryReceiptTarget struct {
	ProjectDir    string `json:"project_dir,omitempty"`
	Profile       string `json:"profile"`
	Session       string `json:"session"`
	NamespaceID   string `json:"namespace_id"`
	Role          string `json:"role,omitempty"`
	Handle        string `json:"handle,omitempty"`
	ExecutionMode string `json:"execution_mode,omitempty"`
}

type deliveryReceiptStage struct {
	State  string    `json:"state"`
	At     time.Time `json:"at"`
	Detail string    `json:"detail,omitempty"`
}

func newDeliveryReceipt(projectDir, profile, session, role, handle, executionMode, kind string) deliveryReceiptData {
	now := time.Now().UTC()
	profile = squadnamespace.NormalizeProfile(profile)
	session = strings.TrimSpace(session)
	return deliveryReceiptData{
		SchemaVersion: deliveryReceiptSchemaVersion,
		AttemptID:     deliveryAttemptID(now, kind, role, handle),
		Kind:          kind,
		Status:        "queued",
		Recipient:     strings.TrimSpace(handle),
		DeliveryState: deliveryStateAmbiguousUnknown,
		Target: deliveryReceiptTarget{
			ProjectDir:    projectDir,
			Profile:       profile,
			Session:       session,
			NamespaceID:   squadnamespace.ID(profile, session),
			Role:          role,
			Handle:        handle,
			ExecutionMode: executionMode,
		},
		CreatedAt: now,
	}
}

func deliveryAttemptID(now time.Time, kind, role, handle string) string {
	seed := strings.Join([]string{kind, role, handle}, "-")
	seed = sanitizeWorkstreamName(seed)
	if seed == "" {
		seed = "delivery"
	}
	return fmt.Sprintf("%s-%s-p%d-%016x", now.Format("20060102T150405.000000000Z"), seed, os.Getpid(), deliveryAttemptSequence.Add(1))
}

func (r *deliveryReceiptData) addStage(state, detail string) {
	if r == nil {
		return
	}
	r.Stages = append(r.Stages, deliveryReceiptStage{
		State:  state,
		At:     time.Now().UTC(),
		Detail: detail,
	})
}

func writeDeliveryReceipt(projectDir, profile, session string, receipt *deliveryReceiptData) error {
	if receipt == nil {
		return nil
	}
	if err := validateDeliveryReceiptCrossFields(*receipt); err != nil {
		return err
	}
	if !safeReceiptAttemptID(receipt.AttemptID) {
		return fmt.Errorf("unsafe delivery receipt attempt id %q", receipt.AttemptID)
	}
	dirRoot, dir, err := openReceiptDirRoot(projectDir, profile, session, true)
	if err != nil {
		return err
	}
	defer dirRoot.Close()
	path := filepath.Join(dir, receipt.AttemptID+".json")
	receipt.Path = path
	lockName := receipt.AttemptID + ".json.lock"
	lockFile, err := openDeliveryReceiptLock(dirRoot, lockName)
	if err != nil {
		return fmt.Errorf("open delivery receipt lock: %w", err)
	}
	defer lockFile.Close()
	return flock.WithFile(lockFile, filepath.Join(dir, lockName), func() error {
		if current, err := readDeliveryReceiptAt(dirRoot, receipt.AttemptID+".json", path); err == nil {
			if receipt.Generation > current.Generation {
				return fmt.Errorf("receipt_corrupt: incoming generation %d is ahead of persisted generation %d", receipt.Generation, current.Generation)
			}
			merged, mergeErr := mergeDeliveryReceipt(current, *receipt)
			if mergeErr != nil {
				return mergeErr
			}
			*receipt = merged
			receipt.Generation = current.Generation + 1
		} else if !os.IsNotExist(err) {
			return err
		} else {
			receipt.Generation = 1
		}
		return writeDeliveryReceiptFile(dirRoot, receipt.AttemptID+".json", path, receipt)
	})
}

func mergeDeliveryReceipt(current, incoming deliveryReceiptData) (deliveryReceiptData, error) {
	if err := validateDeliveryReceiptCrossFields(current); err != nil {
		return deliveryReceiptData{}, err
	}
	if err := validateDeliveryReceiptCrossFields(incoming); err != nil {
		return deliveryReceiptData{}, err
	}
	if err := validateReceiptMergeIdentity(current, incoming); err != nil {
		return deliveryReceiptData{}, err
	}
	if current.MessageID != "" && incoming.MessageID != "" && current.MessageID != incoming.MessageID {
		return deliveryReceiptData{}, fmt.Errorf("receipt_corrupt: attempt %s maps to conflicting message ids %s and %s", incoming.AttemptID, current.MessageID, incoming.MessageID)
	}
	merged := incoming
	if merged.MessageID == "" {
		merged.MessageID = current.MessageID
	}
	if current.CommittedPath != "" && incoming.CommittedPath != "" && filepath.Clean(current.CommittedPath) != filepath.Clean(incoming.CommittedPath) {
		return deliveryReceiptData{}, fmt.Errorf("receipt_corrupt: attempt %s maps to conflicting committed paths %s and %s", incoming.AttemptID, current.CommittedPath, incoming.CommittedPath)
	}
	merged.CommittedPath = mergeSetOnce(current.CommittedPath, incoming.CommittedPath)
	if current.ReconciledMessageID != "" && incoming.ReconciledMessageID != "" && current.ReconciledMessageID != incoming.ReconciledMessageID {
		return deliveryReceiptData{}, fmt.Errorf("receipt_corrupt: attempt %s maps to conflicting reconciled message ids %s and %s", incoming.AttemptID, current.ReconciledMessageID, incoming.ReconciledMessageID)
	}
	merged.ReconciledMessageID = mergeSetOnce(current.ReconciledMessageID, incoming.ReconciledMessageID)
	if len(merged.Recipients) == 0 {
		merged.Recipients = append([]string(nil), current.Recipients...)
	}
	consumerMap := map[string]deliveryConsumerState{}
	for _, c := range current.Consumers {
		consumerMap[c.Consumer] = c
	}
	for _, next := range incoming.Consumers {
		prev, ok := consumerMap[next.Consumer]
		if !ok {
			consumerMap[next.Consumer] = next
			continue
		}
		combined, err := mergeConsumerState(prev, next)
		if err != nil {
			return deliveryReceiptData{}, err
		}
		consumerMap[next.Consumer] = combined
	}
	merged.Consumers = merged.Consumers[:0]
	for _, recipient := range merged.Recipients {
		if c, ok := consumerMap[recipient]; ok {
			merged.Consumers = append(merged.Consumers, c)
		}
	}
	if current.LastCheckedAt != nil && (merged.LastCheckedAt == nil || current.LastCheckedAt.After(*merged.LastCheckedAt)) {
		v := *current.LastCheckedAt
		merged.LastCheckedAt = &v
		merged.LastCheckError = current.LastCheckError
	}
	merged.Stages = mergeReceiptStages(current.Stages, incoming.Stages)
	merged.Status = mergeReceiptStatus(current, incoming)
	if merged.ReconciledMessageID != "" {
		merged.Status = deliveryStateReconciledExisting
		merged.DeliveryState = deliveryStateReconciledExisting
	}
	if incoming.Generation < current.Generation {
		merged.Method, merged.Detail = current.Method, current.Detail
	}
	merged.Acknowledged = current.Acknowledged || incoming.Acknowledged
	merged.Fallback = current.Fallback || incoming.Fallback
	merged.AMQInvoked = current.AMQInvoked || incoming.AMQInvoked
	merged.TaskID = mergeSetOnce(current.TaskID, incoming.TaskID)
	merged.OutboxIntentID = mergeSetOnce(current.OutboxIntentID, incoming.OutboxIntentID)
	merged.PaneID = mergeSetOnce(current.PaneID, incoming.PaneID)
	if hasTerminalConsumerEvidence(merged.Consumers) {
		merged.EvidenceSource = "amq_recipient_receipt"
	} else if incoming.Generation < current.Generation {
		merged.EvidenceSource = current.EvidenceSource
	}
	if merged.ReconciledMessageID != "" {
		merged.EvidenceSource = deliveryStateReconciledExisting
	}
	merged.NativeStage = aggregateNativeStage(merged.Consumers, mergeSetOnce(current.NativeStage, incoming.NativeStage))
	recomputeAggregateDeliveryState(&merged)
	if err := validateDeliveryReceiptCrossFields(merged); err != nil {
		return deliveryReceiptData{}, err
	}
	return merged, nil
}

func validateDeliveryReceiptCrossFields(receipt deliveryReceiptData) error {
	if receipt.CommittedPath != "" {
		if !filepath.IsAbs(receipt.CommittedPath) || filepath.Clean(receipt.CommittedPath) != receipt.CommittedPath || strings.TrimSpace(receipt.MessageID) == "" || !receipt.AMQInvoked {
			return fmt.Errorf("receipt_corrupt: committed-indeterminate evidence is incomplete for attempt %s", receipt.AttemptID)
		}
		if receipt.DeliveryState != deliveryStateCommittedIndeterminate && receipt.DeliveryState != deliveryStateDrained && receipt.DeliveryState != deliveryStateFailed {
			return fmt.Errorf("receipt_corrupt: committed path has inconsistent delivery state %s for attempt %s", receipt.DeliveryState, receipt.AttemptID)
		}
		if err := validateCommittedDeliveryEvidence(receipt, committedDeliveryEvidence{MessageID: receipt.MessageID, FinalPath: receipt.CommittedPath}); err != nil {
			return fmt.Errorf("receipt_corrupt: invalid committed-indeterminate evidence for attempt %s: %w", receipt.AttemptID, err)
		}
	}
	reconciledID := strings.TrimSpace(receipt.ReconciledMessageID)
	if reconciledID == "" {
		if receipt.ReconciledMessageID != "" {
			return fmt.Errorf("receipt_corrupt: reconciled message id is not canonical for attempt %s", receipt.AttemptID)
		}
		if receipt.Status == deliveryStateReconciledExisting || receipt.DeliveryState == deliveryStateReconciledExisting {
			return fmt.Errorf("receipt_corrupt: reconciled state has no reconciled message id for attempt %s", receipt.AttemptID)
		}
		if receipt.EvidenceSource == deliveryStateReconciledExisting {
			return fmt.Errorf("receipt_corrupt: reconciled evidence has no reconciled message id for attempt %s", receipt.AttemptID)
		}
		for _, stage := range receipt.Stages {
			if stage.State == deliveryStateReconciledExisting {
				return fmt.Errorf("receipt_corrupt: reconciled stage has no reconciled message id for attempt %s", receipt.AttemptID)
			}
		}
		for _, consumer := range receipt.Consumers {
			if consumer.State == deliveryStateReconciledExisting {
				return fmt.Errorf("receipt_corrupt: reconciled consumer %s has no reconciled message id for attempt %s", consumer.Consumer, receipt.AttemptID)
			}
		}
		return nil
	}
	if receipt.SchemaVersion != deliveryReceiptSchemaVersion {
		return fmt.Errorf("receipt_corrupt: reconciled message id requires schema %d for attempt %s", deliveryReceiptSchemaVersion, receipt.AttemptID)
	}
	if reconciledID != receipt.ReconciledMessageID || strings.ContainsAny(reconciledID, "\r\n\x00") {
		return fmt.Errorf("receipt_corrupt: reconciled message id is not canonical for attempt %s", receipt.AttemptID)
	}
	if receipt.AMQInvoked || receipt.MessageID != "" || receipt.Status != deliveryStateReconciledExisting || receipt.DeliveryState != deliveryStateReconciledExisting {
		return fmt.Errorf("receipt_corrupt: reconciled receipt has inconsistent invocation or message state for attempt %s", receipt.AttemptID)
	}
	if receipt.EvidenceSource != deliveryStateReconciledExisting || receipt.LastCheckedAt != nil || receipt.LastCheckError != "" || receipt.Acknowledged || receipt.Fallback {
		return fmt.Errorf("receipt_corrupt: reconciled receipt has inconsistent replay or refresh evidence for attempt %s", receipt.AttemptID)
	}
	if len(receipt.Recipients) == 0 || len(receipt.Consumers) != len(receipt.Recipients) {
		return fmt.Errorf("receipt_corrupt: reconciled receipt has inconsistent recipient projection for attempt %s", receipt.AttemptID)
	}
	seenConsumers := make(map[string]bool, len(receipt.Consumers))
	for _, consumer := range receipt.Consumers {
		if consumer.Consumer == "" || seenConsumers[consumer.Consumer] || !slices.Contains(receipt.Recipients, consumer.Consumer) || consumer.State != deliveryStateReconciledExisting || consumer.Stage != "" || consumer.DrainedAt != nil || consumer.FailedAt != nil {
			return fmt.Errorf("receipt_corrupt: reconciled consumer %s has inconsistent delivery evidence for attempt %s", consumer.Consumer, receipt.AttemptID)
		}
		seenConsumers[consumer.Consumer] = true
	}
	if receipt.NativeStage != "" || receipt.DrainedAt != nil || receipt.FailedAt != nil {
		return fmt.Errorf("receipt_corrupt: reconciled receipt has native or terminal delivery evidence for attempt %s", receipt.AttemptID)
	}
	reconciledStage := false
	for _, stage := range receipt.Stages {
		switch stage.State {
		case deliveryStateReconciledExisting:
			reconciledStage = true
		case "amq_invocation_boundary", deliveryStateDeliveredNotDrained, deliveryStatePartiallyDrained, deliveryStateDrained, deliveryStateFailed:
			return fmt.Errorf("receipt_corrupt: reconciled receipt has invocation or delivery stage %s for attempt %s", stage.State, receipt.AttemptID)
		}
	}
	if !reconciledStage {
		return fmt.Errorf("receipt_corrupt: reconciled receipt has no reconciled stage for attempt %s", receipt.AttemptID)
	}
	return nil
}

func validateReceiptMergeIdentity(current, incoming deliveryReceiptData) error {
	checks := []struct {
		name string
		ok   bool
	}{
		{"schema_version", current.SchemaVersion == incoming.SchemaVersion},
		{"attempt_id", current.AttemptID == incoming.AttemptID},
		{"kind", current.Kind == incoming.Kind},
		{"target", current.Target == incoming.Target},
		{"sender", current.Sender == incoming.Sender},
		{"recipient", current.Recipient == incoming.Recipient},
		{"recipients", slices.Equal(current.Recipients, incoming.Recipients)},
		{"root", filepath.Clean(current.Root) == filepath.Clean(incoming.Root)},
		{"thread", current.Thread == incoming.Thread},
		{"path", filepath.Clean(current.Path) == filepath.Clean(incoming.Path)},
		{"created_at", current.CreatedAt.Equal(incoming.CreatedAt)},
		{"prepared_run_generation", current.PreparedRunGeneration == incoming.PreparedRunGeneration},
		{"prepared_run_launch_attempt", current.PreparedRunLaunchAttempt == incoming.PreparedRunLaunchAttempt},
		{"prepared_run_digest", current.PreparedRunDigest == incoming.PreparedRunDigest},
		{"prepared_run_goal_namespace", current.PreparedRunGoalNamespace == incoming.PreparedRunGoalNamespace},
		{"prepared_run_goal_digest", current.PreparedRunGoalDigest == incoming.PreparedRunGoalDigest},
	}
	for _, check := range checks {
		if !check.ok {
			return fmt.Errorf("receipt_corrupt: immutable %s changed for attempt %s", check.name, incoming.AttemptID)
		}
	}
	for _, pair := range [][2]string{{current.TaskID, incoming.TaskID}, {current.OutboxIntentID, incoming.OutboxIntentID}, {current.PaneID, incoming.PaneID}} {
		if pair[0] != "" && pair[1] != "" && pair[0] != pair[1] {
			return fmt.Errorf("receipt_corrupt: linked task/outbox provenance changed for attempt %s", incoming.AttemptID)
		}
	}
	return nil
}

func receiptStatusAt(receipt deliveryReceiptData) time.Time {
	var latest time.Time
	for _, stage := range receipt.Stages {
		if stage.State == receipt.Status && stage.At.After(latest) {
			latest = stage.At
		}
	}
	return latest
}

func mergeReceiptStatus(current, incoming deliveryReceiptData) string {
	if current.Status == incoming.Status {
		return current.Status
	}
	currentRank, incomingRank := receiptStatusRank(current.Status), receiptStatusRank(incoming.Status)
	if currentRank != incomingRank {
		if incomingRank > currentRank {
			return incoming.Status
		}
		return current.Status
	}
	currentAt, incomingAt := receiptStatusAt(current), receiptStatusAt(incoming)
	if incomingAt.After(currentAt) {
		return incoming.Status
	}
	return current.Status
}

func receiptStatusRank(status string) int {
	switch status {
	case "", "queued":
		return 0
	case "written_to_amq", "native_goal_queued":
		return 10
	case "queued_zero_input", "wake_pending", dispatchSubmitQueued:
		return 20
	case dispatchSubmitUnconfirmed, "wake_failed", "pane_failed", "failed_before_id", "failed":
		return 30
	case dispatchSubmitConfirmed, "queued_wake_delivered", "durable_goal_fallback", "native_goal_delivered":
		return 40
	case deliveryStateCommittedIndeterminate:
		return 45
	case deliveryStateReconciledExisting:
		return 50
	default:
		return 15
	}
}

func mergeSetOnce(current, incoming string) string {
	if current != "" {
		return current
	}
	return incoming
}

func aggregateNativeStage(consumers []deliveryConsumerState, fallback string) string {
	var stage string
	var latest time.Time
	for _, consumer := range consumers {
		var at *time.Time
		switch consumer.Stage {
		case "drained":
			at = consumer.DrainedAt
		case "dlq":
			at = consumer.FailedAt
		}
		if at != nil && (stage == "" || at.After(latest)) {
			stage, latest = consumer.Stage, *at
		}
	}
	if stage != "" {
		return stage
	}
	return fallback
}

func hasTerminalConsumerEvidence(consumers []deliveryConsumerState) bool {
	for _, consumer := range consumers {
		if consumer.Stage == "drained" || consumer.Stage == "dlq" {
			return true
		}
	}
	return false
}

func mergeConsumerState(a, b deliveryConsumerState) (deliveryConsumerState, error) {
	if a.Consumer != b.Consumer {
		return deliveryConsumerState{}, fmt.Errorf("receipt_corrupt: cannot merge different consumers")
	}
	if a.Stage != "" && b.Stage != "" && a.Stage != b.Stage {
		return deliveryConsumerState{}, fmt.Errorf("receipt_corrupt: consumer %s has conflicting terminal stages %s and %s", a.Consumer, a.Stage, b.Stage)
	}
	if a.Stage != "" {
		return a, nil
	}
	if b.Stage != "" {
		return b, nil
	}
	if a.State == deliveryStateReconciledExisting {
		return a, nil
	}
	if b.State == deliveryStateReconciledExisting {
		return b, nil
	}
	if a.State == deliveryStateDeliveredNotDrained && b.State == deliveryStateAmbiguousUnknown {
		return a, nil
	}
	if a.State == deliveryStateCommittedIndeterminate && (b.State == deliveryStateAmbiguousUnknown || b.State == deliveryStateDeliveredNotDrained) {
		return a, nil
	}
	if b.State == deliveryStateCommittedIndeterminate && (a.State == deliveryStateAmbiguousUnknown || a.State == deliveryStateDeliveredNotDrained) {
		return b, nil
	}
	return b, nil
}

func mergeReceiptStages(a, b []deliveryReceiptStage) []deliveryReceiptStage {
	seen := map[string]bool{}
	out := make([]deliveryReceiptStage, 0, len(a)+len(b))
	for _, stage := range append(append([]deliveryReceiptStage(nil), a...), b...) {
		key := stage.State + "\x00" + stage.At.UTC().Format(time.RFC3339Nano) + "\x00" + stage.Detail
		if !seen[key] {
			seen[key] = true
			out = append(out, stage)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].At.Before(out[j].At) })
	return out
}

func writeDeliveryReceiptFile(dirRoot *os.Root, name, path string, receipt *deliveryReceiptData) error {
	if receipt == nil {
		return fmt.Errorf("receipt_corrupt: nil delivery receipt")
	}
	if err := validateDeliveryReceiptCrossFields(*receipt); err != nil {
		return err
	}
	b, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal delivery receipt: %w", err)
	}
	var tmp *os.File
	var tmpName string
	for attempt := 0; attempt < 10; attempt++ {
		tmpName = fmt.Sprintf(".receipt-%d-%d-%d.tmp", os.Getpid(), time.Now().UnixNano(), attempt)
		tmp, err = dirRoot.OpenFile(tmpName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			break
		}
		if !os.IsExist(err) {
			return fmt.Errorf("create delivery receipt temp file: %w", err)
		}
	}
	if tmp == nil {
		return fmt.Errorf("create delivery receipt temp file: exhausted unique names")
	}
	defer dirRoot.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod delivery receipt temp file: %w", err)
	}
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write delivery receipt temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync delivery receipt temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close delivery receipt temp file: %w", err)
	}
	receiptBeforeSecureRename()
	if err := dirRoot.Rename(tmpName, name); err != nil {
		return fmt.Errorf("write delivery receipt: %w", err)
	}
	if d, err := dirRoot.Open("."); err == nil {
		if syncErr := d.Sync(); syncErr != nil {
			_ = d.Close()
			return fmt.Errorf("sync delivery receipt directory: %w", syncErr)
		}
		_ = d.Close()
	}
	return nil
}

func updateDeliveryReceiptLocked(projectDir, profile, session, attemptID string, fn func(*deliveryReceiptData) error) (deliveryReceiptData, error) {
	if !safeReceiptAttemptID(attemptID) {
		return deliveryReceiptData{}, fmt.Errorf("unsafe delivery receipt attempt id %q", attemptID)
	}
	dirRoot, dir, err := openReceiptDirRoot(projectDir, profile, session, false)
	if err != nil {
		return deliveryReceiptData{}, err
	}
	defer dirRoot.Close()
	path := filepath.Join(dir, attemptID+".json")
	var updated deliveryReceiptData
	lockName := attemptID + ".json.lock"
	lockFile, err := openDeliveryReceiptLock(dirRoot, lockName)
	if err != nil {
		return deliveryReceiptData{}, fmt.Errorf("open delivery receipt lock: %w", err)
	}
	defer lockFile.Close()
	err = flock.WithFile(lockFile, filepath.Join(dir, lockName), func() error {
		current, err := readDeliveryReceiptAt(dirRoot, attemptID+".json", path)
		if err != nil {
			return err
		}
		if current.AttemptID != attemptID || filepath.Clean(current.Path) != filepath.Clean(path) {
			return fmt.Errorf("receipt attempt/path mismatch at %s", path)
		}
		if err := fn(&current); err != nil {
			return err
		}
		current.Path = path
		current.Generation++
		if err := writeDeliveryReceiptFile(dirRoot, attemptID+".json", path, &current); err != nil {
			return err
		}
		updated = current
		return nil
	})
	return updated, err
}

// openDeliveryReceiptLock creates a missing sidecar exclusively, then opens an
// existing sidecar without O_CREATE. On Darwin, concurrent openat calls using
// O_CREATE|O_NOFOLLOW for the same missing name can spuriously return ENOENT.
// Splitting create from open also keeps the secure os.Root no-symlink boundary:
// O_EXCL never follows a link, and the existing-file open retains O_NOFOLLOW.
func openDeliveryReceiptLock(dirRoot *os.Root, name string) (*os.File, error) {
	lockFile, err := dirRoot.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o644)
	if err == nil {
		return lockFile, nil
	}
	if !os.IsExist(err) {
		return nil, err
	}
	return dirRoot.OpenFile(name, os.O_RDWR, 0)
}

func openReceiptDirRoot(projectDir, profile, session string, create bool) (*os.Root, string, error) {
	profile = squadnamespace.NormalizeProfile(profile)
	if profile != team.DefaultProfile {
		if err := team.ValidateProfileName(profile); err != nil {
			return nil, "", err
		}
	}
	if err := team.ValidateSessionName(strings.TrimSpace(session)); err != nil {
		return nil, "", err
	}
	rel := filepath.Join(team.DirName, "receipts")
	if profile != team.DefaultProfile {
		rel = filepath.Join(rel, profile)
	}
	rel = filepath.Join(rel, strings.TrimSpace(session))
	return openContainedReceiptRoot(projectDir, rel, create)
}

func openReceiptBaseRoot(projectDir, profile string) (*os.Root, string, error) {
	profile = squadnamespace.NormalizeProfile(profile)
	if profile != team.DefaultProfile {
		if err := team.ValidateProfileName(profile); err != nil {
			return nil, "", err
		}
	}
	rel := filepath.Join(team.DirName, "receipts")
	if profile != team.DefaultProfile {
		rel = filepath.Join(rel, profile)
	}
	return openContainedReceiptRoot(projectDir, rel, false)
}

func openContainedReceiptRoot(projectDir, rel string, create bool) (*os.Root, string, error) {
	projectAbs, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, "", err
	}
	projectReal, err := filepath.EvalSymlinks(projectAbs)
	if err != nil {
		return nil, "", fmt.Errorf("resolve receipt project root: %w", err)
	}
	projectRoot, err := os.OpenRoot(projectReal)
	if err != nil {
		return nil, "", fmt.Errorf("open receipt project root: %w", err)
	}
	dirRoot, err := openReceiptComponentsNoSymlink(projectRoot, rel, create)
	if err != nil {
		return nil, "", fmt.Errorf("open contained delivery receipt dir: %w", err)
	}
	return dirRoot, filepath.Join(projectReal, rel), nil
}

func openReceiptComponentsNoSymlink(root *os.Root, rel string, create bool) (*os.Root, error) {
	current := root
	for _, component := range strings.Split(filepath.Clean(rel), string(os.PathSeparator)) {
		if component == "" || component == "." || component == ".." {
			current.Close()
			return nil, fmt.Errorf("unsafe receipt path component %q", component)
		}
		info, err := current.Lstat(component)
		if os.IsNotExist(err) && create {
			if mkdirErr := current.Mkdir(component, 0o755); mkdirErr != nil && !os.IsExist(mkdirErr) {
				current.Close()
				return nil, mkdirErr
			}
			info, err = current.Lstat(component)
		}
		if err != nil {
			current.Close()
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			current.Close()
			return nil, fmt.Errorf("refusing non-directory or symlink receipt ancestor %q", component)
		}
		receiptBeforeRootOpen(component)
		next, err := current.OpenRoot(component)
		if err != nil {
			current.Close()
			return nil, err
		}
		opened, openErr := next.Open(".")
		if openErr != nil {
			next.Close()
			current.Close()
			return nil, openErr
		}
		openedInfo, statErr := opened.Stat()
		opened.Close()
		if statErr != nil || !os.SameFile(info, openedInfo) {
			next.Close()
			current.Close()
			return nil, fmt.Errorf("receipt ancestor %q changed while opening", component)
		}
		visibleInfo, visibleErr := current.Lstat(component)
		if visibleErr != nil || visibleInfo.Mode()&os.ModeSymlink != 0 || !visibleInfo.IsDir() || !os.SameFile(visibleInfo, openedInfo) {
			next.Close()
			current.Close()
			return nil, fmt.Errorf("receipt ancestor %q changed or became a symlink while opening", component)
		}
		current.Close()
		current = next
	}
	return current, nil
}

func deliveryReceiptDir(projectDir, profile, session string) string {
	base := filepath.Join(projectDir, team.DirName, "receipts")
	if squadnamespace.NormalizeProfile(profile) != team.DefaultProfile {
		base = filepath.Join(base, squadnamespace.NormalizeProfile(profile))
	}
	return filepath.Join(base, strings.TrimSpace(session))
}

type nativeAMQReceipt struct {
	MsgID     string `json:"msg_id"`
	Consumer  string `json:"consumer"`
	Stage     string `json:"stage"`
	EmittedAt string `json:"emitted_at"`
}

type nativeAMQReceiptList struct {
	Receipts []nativeAMQReceipt `json:"receipts"`
}

func safeReceiptAttemptID(id string) bool {
	if strings.TrimSpace(id) == "" {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// parseSentMessageID accepts both the AMQ v0.42.1 JSON contract and its legacy
// human confirmation line. Combined stdout/stderr may put a timeout diagnostic
// before the JSON object, so extracting the stable id cannot require the whole
// byte stream to be a single JSON document.
func parseSentMessageID(out string) string {
	var native struct {
		ID string `json:"id"`
	}
	if payload := firstJSONObject([]byte(out)); len(payload) > 0 && json.Unmarshal(payload, &native) == nil && strings.TrimSpace(native.ID) != "" {
		return strings.TrimSpace(native.ID)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "Sent ")
		if !ok {
			rest, ok = strings.CutPrefix(line, "Replied ")
		}
		if ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

func markDeliverySendResult(receipt *deliveryReceiptData, out []byte, sendErr error) {
	if receipt == nil || receipt.ReconciledMessageID != "" {
		return
	}
	if evidence, ok := parseCommittedDeliveryEvidence(string(out), sendErr); ok && validateCommittedDeliveryEvidence(*receipt, evidence) == nil {
		receipt.MessageID = evidence.MessageID
		receipt.CommittedPath = evidence.FinalPath
		receipt.Status = deliveryStateCommittedIndeterminate
		receipt.DeliveryState = deliveryStateCommittedIndeterminate
		receipt.EvidenceSource = "amq_committed_delivery_error"
		receipt.Detail = sendErr.Error()
		for i := range receipt.Consumers {
			receipt.Consumers[i].State = deliveryStateCommittedIndeterminate
		}
		receipt.addStage(deliveryStateCommittedIndeterminate, "AMQ exposed a stable message id and final path after the visible commit, but directory-sync durability is indeterminate; do not resend")
		return
	}
	receipt.MessageID = parseSentMessageID(string(out))
	if receipt.MessageID == "" {
		if sendErr != nil {
			receipt.DeliveryState = deliveryStateAmbiguousUnknown
			receipt.Detail = sendErr.Error()
			receipt.addStage(deliveryStateAmbiguousUnknown, "AMQ was invoked but returned no stable message id: "+sendErr.Error()+"; confirm non-delivery before retry")
		}
		return
	}
	receipt.DeliveryState = deliveryStateDeliveredNotDrained
	for i := range receipt.Consumers {
		receipt.Consumers[i].State = deliveryStateDeliveredNotDrained
	}
	receipt.addStage(deliveryStateDeliveredNotDrained, "AMQ exposed a stable message id; no recipient drain receipt is recorded yet")
	if native, ok := nativeReceiptFromSendOutput(out, receipt.MessageID, receipt.Recipient); ok {
		if err := applyNativeReceipt(receipt, native); err != nil {
			receipt.DeliveryState = deliveryStateAmbiguousUnknown
			receipt.LastCheckError = err.Error()
		}
	}
}

type committedDeliveryEvidence struct {
	MessageID string
	FinalPath string
}

func parseCommittedDeliveryEvidence(out string, sendErr error) (committedDeliveryEvidence, bool) {
	if sendErr == nil {
		return committedDeliveryEvidence{}, false
	}
	text := out
	text += "\n" + sendErr.Error()
	const idSuffix = " has a committed delivery;"
	const pathPrefix = " committed at "
	const pathSuffix = ", but durability is indeterminate:"
	for _, line := range strings.Split(text, "\n") {
		idEnd := strings.Index(line, idSuffix)
		if idEnd < 0 {
			continue
		}
		idStart := strings.LastIndex(line[:idEnd], "message ")
		if idStart < 0 {
			continue
		}
		messageID := strings.TrimSpace(line[idStart+len("message ") : idEnd])
		pathStart := strings.Index(line[idEnd+len(idSuffix):], pathPrefix)
		if !safeCommittedMessageID(messageID) || pathStart < 0 {
			continue
		}
		pathStart += idEnd + len(idSuffix) + len(pathPrefix)
		pathEnd := strings.Index(line[pathStart:], pathSuffix)
		if pathEnd < 0 {
			continue
		}
		finalPath := strings.TrimSpace(line[pathStart : pathStart+pathEnd])
		if !filepath.IsAbs(finalPath) || filepath.Clean(finalPath) != finalPath {
			continue
		}
		return committedDeliveryEvidence{MessageID: messageID, FinalPath: finalPath}, true
	}
	return committedDeliveryEvidence{}, false
}

func safeCommittedMessageID(id string) bool {
	return safeReceiptAttemptID(id) && id != "." && id != ".." && !strings.HasPrefix(id, ".") && !strings.Contains(id, "..") && filepath.Base(id) == id
}

func validateCommittedDeliveryEvidence(receipt deliveryReceiptData, evidence committedDeliveryEvidence) error {
	root := strings.TrimSpace(receipt.Root)
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return fmt.Errorf("committed delivery root is not canonical absolute")
	}
	if len(receipt.Recipients) != 1 || len(receipt.Consumers) != 1 {
		return fmt.Errorf("committed delivery requires exactly one intended recipient and consumer")
	}
	recipient := strings.TrimSpace(receipt.Recipients[0])
	if recipient == "" || receipt.Recipient != recipient || receipt.Target.Handle != recipient || receipt.Consumers[0].Consumer != recipient {
		return fmt.Errorf("committed delivery recipient projection is ambiguous")
	}
	expected := filepath.Join(root, "agents", recipient, "inbox", "new", evidence.MessageID+".md")
	if filepath.Clean(evidence.FinalPath) != expected || evidence.FinalPath != expected {
		return fmt.Errorf("committed delivery path does not match exact recipient inbox and message id")
	}
	return nil
}

func markDeliveryFailedBeforeID(projectDir, profile, session string, receipt *deliveryReceiptData, cause error) {
	if receipt == nil || cause == nil {
		return
	}
	now := time.Now().UTC()
	receipt.Status = "failed_before_id"
	receipt.DeliveryState = deliveryStateFailed
	receipt.FailedAt = &now
	receipt.Detail = cause.Error()
	receipt.addStage("failed_before_id", "definite pre-send failure: "+cause.Error())
	_ = writeDeliveryReceipt(projectDir, profile, session, receipt)
}

func nativeReceiptFromSendOutput(out []byte, msgID, recipient string) (nativeAMQReceipt, bool) {
	var envelope struct {
		Wait struct {
			Event   string            `json:"event"`
			Receipt *nativeAMQReceipt `json:"receipt"`
		} `json:"wait"`
	}
	if payload := firstJSONObject(out); len(payload) > 0 && json.Unmarshal(payload, &envelope) == nil && envelope.Wait.Receipt != nil {
		r := *envelope.Wait.Receipt
		if r.MsgID == msgID && (recipient == "" || r.Consumer == recipient) {
			return r, true
		}
	}
	return nativeAMQReceipt{}, false
}

func firstJSONObject(data []byte) []byte {
	start := bytes.IndexByte(data, '{')
	if start < 0 {
		return nil
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(data); i++ {
		c := data[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return data[start : i+1]
			}
		}
	}
	return nil
}

func applyNativeReceipt(receipt *deliveryReceiptData, native nativeAMQReceipt) error {
	if receipt != nil && receipt.ReconciledMessageID != "" {
		return fmt.Errorf("receipt_corrupt: reconciled existing receipt cannot adopt native delivery evidence")
	}
	if receipt == nil || native.MsgID != receipt.MessageID || !containsString(receipt.Recipients, native.Consumer) {
		return fmt.Errorf("native receipt provenance does not match message/consumer")
	}
	var consumer *deliveryConsumerState
	for i := range receipt.Consumers {
		if receipt.Consumers[i].Consumer == native.Consumer {
			consumer = &receipt.Consumers[i]
			break
		}
	}
	if consumer == nil {
		return fmt.Errorf("native receipt consumer %s is not projected", native.Consumer)
	}
	if native.Stage != "drained" && native.Stage != "dlq" {
		return fmt.Errorf("receipt_corrupt: unsupported native stage %q", native.Stage)
	}
	if consumer.Stage != "" && consumer.Stage != native.Stage {
		receipt.DeliveryState = deliveryStateAmbiguousUnknown
		receipt.LastCheckError = fmt.Sprintf("conflicting native receipt stages for %s: %s and %s", native.Consumer, consumer.Stage, native.Stage)
		receipt.addStage(deliveryStateAmbiguousUnknown, receipt.LastCheckError)
		return fmt.Errorf("%s", receipt.LastCheckError)
	}
	when, err := time.Parse(time.RFC3339Nano, native.EmittedAt)
	if err != nil {
		return fmt.Errorf("receipt_corrupt: invalid emitted_at %q for %s/%s", native.EmittedAt, native.Consumer, native.Stage)
	}
	when = when.UTC()
	receipt.NativeStage = native.Stage
	receipt.EvidenceSource = "amq_recipient_receipt"
	switch native.Stage {
	case "drained":
		consumer.State, consumer.Stage, consumer.DrainedAt = deliveryStateDrained, native.Stage, &when
		receipt.addStage(deliveryStateDrained, fmt.Sprintf("recipient %s emitted drained receipt at %s", native.Consumer, when.Format(time.RFC3339Nano)))
	case "dlq":
		consumer.State, consumer.Stage, consumer.FailedAt = deliveryStateFailed, native.Stage, &when
		receipt.addStage(deliveryStateFailed, fmt.Sprintf("recipient %s emitted DLQ receipt at %s", native.Consumer, when.Format(time.RFC3339Nano)))
	}
	recomputeAggregateDeliveryState(receipt)
	return nil
}

func recomputeAggregateDeliveryState(receipt *deliveryReceiptData) {
	if receipt == nil || receipt.ReconciledMessageID != "" || len(receipt.Consumers) == 0 || receipt.DeliveryState == deliveryStateAmbiguousUnknown && receipt.LastCheckError != "" {
		return
	}
	drained, failed := 0, 0
	var latestDrain, latestFailure *time.Time
	for i := range receipt.Consumers {
		c := &receipt.Consumers[i]
		switch c.State {
		case deliveryStateDrained:
			drained++
			if c.DrainedAt != nil && (latestDrain == nil || c.DrainedAt.After(*latestDrain)) {
				v := *c.DrainedAt
				latestDrain = &v
			}
		case deliveryStateFailed:
			failed++
			if c.FailedAt != nil && (latestFailure == nil || c.FailedAt.After(*latestFailure)) {
				v := *c.FailedAt
				latestFailure = &v
			}
		}
	}
	if receipt.CommittedPath != "" && drained == 0 && failed == 0 {
		receipt.DeliveryState = deliveryStateCommittedIndeterminate
		return
	}
	receipt.DrainedAt, receipt.FailedAt = nil, nil
	switch {
	case failed > 0:
		receipt.DeliveryState, receipt.FailedAt = deliveryStateFailed, latestFailure
	case drained == len(receipt.Consumers):
		receipt.DeliveryState, receipt.DrainedAt = deliveryStateDrained, latestDrain
	case drained > 0:
		receipt.DeliveryState = deliveryStatePartiallyDrained
	case receipt.MessageID != "":
		receipt.DeliveryState = deliveryStateDeliveredNotDrained
	}
}

type durableSendOptions struct {
	ProjectDir     string
	Profile        string
	Session        string
	Role           string
	ExecutionMode  string
	Kind           string
	TaskID         string
	OutboxIntentID string
	Receipt        *deliveryReceiptData
	Invocation     durableInvocationBoundary
	WaitPosture    waitPostureRequest
}

type durableInvocationDisposition string

const (
	durableInvocationInvoked            durableInvocationDisposition = "invoked"
	durableInvocationReconciledExisting durableInvocationDisposition = "reconciled_existing"
)

type durableInvocationResult struct {
	disposition         durableInvocationDisposition
	reconciledMessageID string
}

func newDurableInvokedResult() durableInvocationResult {
	return durableInvocationResult{disposition: durableInvocationInvoked}
}

func newDurableReconciledExistingResult(messageID string) (durableInvocationResult, error) {
	result := durableInvocationResult{disposition: durableInvocationReconciledExisting, reconciledMessageID: messageID}
	if err := result.validate(); err != nil {
		return durableInvocationResult{}, err
	}
	return result, nil
}

func (r durableInvocationResult) Disposition() durableInvocationDisposition {
	return r.disposition
}

func (r durableInvocationResult) ReconciledMessageID() string {
	return r.reconciledMessageID
}

func (r durableInvocationResult) validate() error {
	switch r.disposition {
	case durableInvocationInvoked:
		if r.reconciledMessageID != "" {
			return fmt.Errorf("durable invocation result cannot bind a reconciled message id when invoked")
		}
		return nil
	case durableInvocationReconciledExisting:
		if strings.TrimSpace(r.reconciledMessageID) == "" || strings.TrimSpace(r.reconciledMessageID) != r.reconciledMessageID || strings.ContainsAny(r.reconciledMessageID, "\r\n\x00") {
			return fmt.Errorf("durable invocation reconciled message id is required and must be canonical")
		}
		return nil
	default:
		return fmt.Errorf("durable invocation result disposition %q is invalid", r.disposition)
	}
}

type durableInvocationBoundary struct {
	run func(func() error) (durableInvocationResult, error)
}

func newDurableInvocationBoundary(run func(func() error) (durableInvocationResult, error)) (durableInvocationBoundary, error) {
	if run == nil {
		return durableInvocationBoundary{}, fmt.Errorf("durable invocation boundary callback is required")
	}
	return durableInvocationBoundary{run: run}, nil
}

func (b durableInvocationBoundary) Run(invoke func() error) (durableInvocationResult, error) {
	if b.run == nil || invoke == nil {
		return durableInvocationResult{}, fmt.Errorf("durable invocation boundary and callback are required")
	}
	return b.run(invoke)
}

type durableInvocationPhase string

const (
	durableInvocationNotStarted            durableInvocationPhase = "not_started"
	durableInvocationCallbackEntered       durableInvocationPhase = "callback_entered"
	durableInvocationPreflightFailed       durableInvocationPhase = "preflight_failed"
	durableInvocationBoundaryPersistFailed durableInvocationPhase = "boundary_persist_failed"
	durableInvocationBoundaryPersisted     durableInvocationPhase = "boundary_persisted"
	durableInvocationSubprocessEntered     durableInvocationPhase = "subprocess_entered"
	durableInvocationSubprocessReturned    durableInvocationPhase = "subprocess_returned"
)

type durableInvocationBoundaryPersistError struct {
	AttemptID string
	Cause     error
}

type durableFinalReceiptPersistError struct {
	AttemptID           string
	MessageID           string
	ReconciledMessageID string
	Cause               error
}

func (e *durableFinalReceiptPersistError) Error() string {
	if e == nil {
		return "durable final receipt persistence failed"
	}
	return fmt.Sprintf("durable final receipt persistence failed for attempt %s (message_id=%s reconciled_message_id=%s): %v", e.AttemptID, e.MessageID, e.ReconciledMessageID, e.Cause)
}

func (e *durableFinalReceiptPersistError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func (e *durableInvocationBoundaryPersistError) Error() string {
	if e == nil {
		return "AMQ invocation-boundary persistence failed"
	}
	return fmt.Sprintf("AMQ invocation-boundary persistence failed for receipt %s: %v", e.AttemptID, e.Cause)
}

func (e *durableInvocationBoundaryPersistError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func isDurableInvocationBoundaryPersistError(err error) bool {
	var target *durableInvocationBoundaryPersistError
	return errors.As(err, &target)
}

func directDurableInvocationBoundary() durableInvocationBoundary {
	boundary, _ := newDurableInvocationBoundary(func(invoke func() error) (durableInvocationResult, error) {
		if err := invoke(); err != nil {
			return durableInvocationResult{}, err
		}
		return newDurableInvokedResult(), nil
	})
	return boundary
}

func validateDurableInvocationBoundaryResult(phase durableInvocationPhase, callbackCount int, result durableInvocationResult, boundaryErr error, receipt deliveryReceiptData) error {
	zero := durableInvocationResult{}
	if result == zero {
		switch {
		case callbackCount == 0 && phase == durableInvocationNotStarted && boundaryErr != nil && !isDurableInvocationBoundaryPersistError(boundaryErr) && !receipt.AMQInvoked && receipt.MessageID == "" && receipt.ReconciledMessageID == "":
			return nil
		case callbackCount == 1 && phase == durableInvocationPreflightFailed && boundaryErr != nil && !receipt.AMQInvoked && receipt.MessageID == "" && receipt.ReconciledMessageID == "":
			return nil
		case callbackCount == 1 && phase == durableInvocationBoundaryPersistFailed && isDurableInvocationBoundaryPersistError(boundaryErr) && !receipt.AMQInvoked && receipt.MessageID == "" && receipt.ReconciledMessageID == "":
			return nil
		default:
			return fmt.Errorf("durable invocation boundary returned an invalid empty result (phase=%s callbacks=%d)", phase, callbackCount)
		}
	}
	if err := result.validate(); err != nil {
		return err
	}
	switch result.Disposition() {
	case durableInvocationInvoked:
		if callbackCount != 1 || phase != durableInvocationSubprocessReturned || !receipt.AMQInvoked || receipt.MessageID != "" || receipt.ReconciledMessageID != "" || isDurableInvocationBoundaryPersistError(boundaryErr) {
			return fmt.Errorf("durable invocation boundary reported invoked with inconsistent callback or subprocess evidence (phase=%s callbacks=%d)", phase, callbackCount)
		}
	case durableInvocationReconciledExisting:
		if callbackCount != 0 || phase != durableInvocationNotStarted || receipt.AMQInvoked || receipt.MessageID != "" || receipt.ReconciledMessageID != "" || isDurableInvocationBoundaryPersistError(boundaryErr) {
			return fmt.Errorf("durable invocation boundary reconciled result has inconsistent callback or invocation evidence (phase=%s callbacks=%d)", phase, callbackCount)
		}
	}
	return nil
}

// runOwnedDurableSend is the one amq-squad-owned send boundary. It reserves a
// crash-visible projection before invoking AMQ, captures output even on a
// nonzero exit, persists any stable id before returning, and treats an exit-0
// response without an id as ambiguous rather than successful.
func runOwnedDurableSend(opts durableSendOptions, req amqCommandRequest) ([]byte, *deliveryReceiptData, error) {
	from, to := amqFlagValue(req.Arg, "me"), amqFlagValue(req.Arg, "to")
	if from == "" {
		from = amqFlagValue(req.Arg, "from")
	}
	receipt := newDeliveryReceipt(opts.ProjectDir, opts.Profile, opts.Session, opts.Role, to, opts.ExecutionMode, opts.Kind)
	if opts.Receipt != nil {
		receipt = *opts.Receipt
	}
	receipt.Sender = strings.TrimSpace(from)
	if parsedRecipients := splitReceiptRecipients(to); len(parsedRecipients) > 0 {
		receipt.Recipients = parsedRecipients
		receipt.Recipient = ""
		if len(receipt.Recipients) == 1 {
			receipt.Recipient = receipt.Recipients[0]
		}
		receipt.Consumers = make([]deliveryConsumerState, 0, len(receipt.Recipients))
		for _, consumer := range receipt.Recipients {
			receipt.Consumers = append(receipt.Consumers, deliveryConsumerState{Consumer: consumer, State: deliveryStateAmbiguousUnknown})
		}
	}
	receipt.Root = strings.TrimSpace(amqFlagValue(req.Arg, "root"))
	if thread := strings.TrimSpace(amqFlagValue(req.Arg, "thread")); thread != "" {
		receipt.Thread = thread
	}
	if receipt.Thread == "" && len(receipt.Recipients) == 1 && receipt.Sender != "" {
		receipt.Thread = receiptCanonicalP2P(receipt.Sender, receipt.Recipients[0])
	}
	if taskID := strings.TrimSpace(opts.TaskID); taskID != "" {
		receipt.TaskID = taskID
	}
	if intentID := strings.TrimSpace(opts.OutboxIntentID); intentID != "" {
		receipt.OutboxIntentID = intentID
	}
	receipt.Method = "durable_amq"
	receipt.EvidenceSource = "amq_send_output"
	receipt.addStage(deliveryStateAmbiguousUnknown, "send attempt reserved before invoking AMQ; do not retry if this process stops before reconciliation")
	if len(receipt.Recipients) == 0 || receipt.Root == "" {
		return nil, &receipt, fmt.Errorf("durable send requires recipient and root provenance (attempt_id=%s state=%s)", receipt.AttemptID, receipt.DeliveryState)
	}
	if err := persistDeliveryReceipt(opts.ProjectDir, opts.Profile, opts.Session, &receipt); err != nil {
		return nil, &receipt, err
	}
	boundary := opts.Invocation
	if boundary.run == nil {
		boundary = directDurableInvocationBoundary()
	}
	phase := durableInvocationNotStarted
	callbackCount := 0
	var out []byte
	var sendErr error
	var result durableInvocationResult
	var boundaryErr error
	var boundaryPanic any
	func() {
		defer func() { boundaryPanic = recover() }()
		result, boundaryErr = boundary.Run(func() error {
			callbackCount++
			if phase != durableInvocationNotStarted {
				return fmt.Errorf("durable invocation callback is single-use (phase=%s)", phase)
			}
			phase = durableInvocationCallbackEntered
			if err := guardOwnedWait(opts.WaitPosture); err != nil {
				phase = durableInvocationPreflightFailed
				return err
			}
			invoked := receipt
			invoked.AMQInvoked = true
			invoked.addStage("amq_invocation_boundary", "receipt persisted immediately before invoking AMQ; an interruption after this point is delivery-uncertain")
			if err := persistDeliveryReceipt(opts.ProjectDir, opts.Profile, opts.Session, &invoked); err != nil {
				phase = durableInvocationBoundaryPersistFailed
				return &durableInvocationBoundaryPersistError{AttemptID: receipt.AttemptID, Cause: err}
			}
			receipt = invoked
			phase = durableInvocationBoundaryPersisted
			phase = durableInvocationSubprocessEntered
			out, sendErr = runAMQCommand(req)
			phase = durableInvocationSubprocessReturned
			return nil
		})
	}()
	defer func() {
		if boundaryPanic != nil {
			panic(boundaryPanic)
		}
	}()
	contractErr := validateDurableInvocationBoundaryResult(phase, callbackCount, result, boundaryErr, receipt)
	if result.Disposition() == durableInvocationReconciledExisting && contractErr == nil {
		receipt.ReconciledMessageID = result.ReconciledMessageID()
		receipt.Status = deliveryStateReconciledExisting
		receipt.DeliveryState = deliveryStateReconciledExisting
		receipt.EvidenceSource = deliveryStateReconciledExisting
		for i := range receipt.Consumers {
			receipt.Consumers[i].State = deliveryStateReconciledExisting
			receipt.Consumers[i].Stage = ""
			receipt.Consumers[i].DrainedAt = nil
			receipt.Consumers[i].FailedAt = nil
		}
		receipt.addStage(deliveryStateReconciledExisting, "durable invocation reconciled an existing stable message without invoking AMQ")
		if err := persistDeliveryReceipt(opts.ProjectDir, opts.Profile, opts.Session, &receipt); err != nil {
			persistErr := &durableFinalReceiptPersistError{AttemptID: receipt.AttemptID, ReconciledMessageID: receipt.ReconciledMessageID, Cause: err}
			return nil, &receipt, errors.Join(boundaryErr, contractErr, persistErr)
		}
		return nil, &receipt, boundaryErr
	}
	if phase == durableInvocationNotStarted || phase == durableInvocationCallbackEntered || phase == durableInvocationPreflightFailed || phase == durableInvocationBoundaryPersistFailed {
		cause := errors.Join(boundaryErr, contractErr)
		if cause == nil {
			cause = fmt.Errorf("durable invocation boundary ended before AMQ invocation")
		}
		markDeliveryFailedBeforeID(opts.ProjectDir, opts.Profile, opts.Session, &receipt, cause)
		return nil, &receipt, cause
	}
	markDeliverySendResult(&receipt, out, sendErr)
	if sendErr == nil && receipt.MessageID == "" {
		sendErr = fmt.Errorf("AMQ exited successfully without a parseable stable message id")
		receipt.DeliveryState = deliveryStateAmbiguousUnknown
		receipt.Detail = sendErr.Error()
		receipt.addStage(deliveryStateAmbiguousUnknown, sendErr.Error()+"; inspect the recipient mailbox before any retry")
	}
	if err := persistDeliveryReceipt(opts.ProjectDir, opts.Profile, opts.Session, &receipt); err != nil {
		persistErr := &durableFinalReceiptPersistError{AttemptID: receipt.AttemptID, MessageID: receipt.MessageID, Cause: err}
		cause := errors.Join(boundaryErr, contractErr, sendErr, persistErr)
		return out, &receipt, &durableSendError{Cause: cause, Receipt: receipt}
	}
	finalErr := errors.Join(boundaryErr, contractErr, sendErr)
	if finalErr != nil {
		return out, &receipt, &durableSendError{Cause: finalErr, Receipt: receipt}
	}
	return out, &receipt, nil
}

func taskDeliveryOutcome(receipt *deliveryReceiptData, sendErr error) taskstore.DeliveryOutcome {
	if receipt != nil && strings.TrimSpace(receipt.MessageID) != "" {
		return taskstore.DeliveryOutcome{State: taskstore.DeliveryDelivered, MessageID: strings.TrimSpace(receipt.MessageID), Error: deliveryErrorString(sendErr)}
	}
	if receipt != nil && receipt.AMQInvoked {
		return taskstore.DeliveryOutcome{State: taskstore.DeliveryUncertain, Error: deliveryErrorString(sendErr)}
	}
	return taskstore.DeliveryOutcome{State: taskstore.DeliveryFailedBeforeInvoke, Error: deliveryErrorString(sendErr)}
}

func deliveryErrorString(err error) string {
	if err == nil {
		return "AMQ returned without a stable message id"
	}
	return err.Error()
}

func splitReceiptRecipients(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part != "" && !seen[part] {
			seen[part] = true
			out = append(out, part)
		}
	}
	sort.Strings(out)
	return out
}

func receiptCanonicalP2P(a, b string) string {
	a, b = strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if a <= b {
		return "p2p/" + a + "__" + b
	}
	return "p2p/" + b + "__" + a
}

type durableSendError struct {
	Cause   error
	Receipt deliveryReceiptData
}

func (e *durableSendError) Error() string {
	return fmt.Sprintf("%v (message_id=%s attempt_id=%s state=%s receipt=%s)", e.Cause, e.Receipt.MessageID, e.Receipt.AttemptID, e.Receipt.DeliveryState, e.Receipt.Path)
}

func (e *durableSendError) Unwrap() error { return e.Cause }

func readDeliveryReceiptAt(root *os.Root, name, path string) (deliveryReceiptData, error) {
	b, err := readDeliveryReceiptRawAt(root, name, path)
	if err != nil {
		return deliveryReceiptData{}, err
	}
	return decodeDeliveryReceipt(b, path)
}

// readDeliveryReceiptRawAt is the descriptor-confined read seam shared by the
// ordinary receipt decoder and read-only compound-release inspection. Keeping
// the raw bytes lets the latter apply its own strict immutable schema decoder.
func readDeliveryReceiptRawAt(root *os.Root, name, path string) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || migrationLinkCount(info) != 1 {
		return nil, fmt.Errorf("receipt path is not a regular file: %s", path)
	}
	receiptBeforeSecureOpen()
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || migrationLinkCount(opened) != 1 || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("receipt path changed while opening: %s", path)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func decodeDeliveryReceipt(b []byte, path string) (deliveryReceiptData, error) {
	var receipt deliveryReceiptData
	if err := json.Unmarshal(b, &receipt); err != nil {
		return deliveryReceiptData{}, err
	}
	if receipt.SchemaVersion == 0 {
		receipt.SchemaVersion = 1
	}
	if receipt.SchemaVersion < 1 || receipt.SchemaVersion > deliveryReceiptSchemaVersion {
		return deliveryReceiptData{}, fmt.Errorf("unsupported delivery receipt schema %d at %s", receipt.SchemaVersion, path)
	}
	if err := validateDeliveryReceiptCrossFields(receipt); err != nil {
		return deliveryReceiptData{}, err
	}
	if receipt.Recipient == "" {
		receipt.Recipient = strings.TrimSpace(receipt.Target.Handle)
	}
	if len(receipt.Recipients) == 0 && receipt.Recipient != "" {
		receipt.Recipients = []string{receipt.Recipient}
	}
	if len(receipt.Consumers) == 0 {
		for _, consumer := range receipt.Recipients {
			receipt.Consumers = append(receipt.Consumers, deliveryConsumerState{Consumer: consumer, State: receipt.DeliveryState})
		}
	}
	if err := validateDeliveryReceiptCrossFields(receipt); err != nil {
		return deliveryReceiptData{}, err
	}
	return receipt, nil
}
