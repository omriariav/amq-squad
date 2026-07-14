package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	deliveryReceiptSchemaVersion = 2

	deliveryStateAmbiguousUnknown    = "ambiguous_unknown"
	deliveryStateFailed              = "delivery_failed"
	deliveryStateDeliveredNotDrained = "delivered_not_drained"
	deliveryStatePartiallyDrained    = "partially_drained"
	deliveryStateDrained             = "drained"
)

var (
	receiptBeforeSecureOpen   = func() {}
	receiptBeforeSecureRename = func() {}
	receiptBeforeRootOpen     = func(string) {}
	persistDeliveryReceipt    = writeDeliveryReceipt
)

type deliveryReceiptData struct {
	SchemaVersion  int                     `json:"schema_version"`
	Generation     uint64                  `json:"generation"`
	AttemptID      string                  `json:"attempt_id"`
	Kind           string                  `json:"kind"`
	Method         string                  `json:"method,omitempty"`
	Status         string                  `json:"status"`
	Target         deliveryReceiptTarget   `json:"target"`
	MessageID      string                  `json:"message_id,omitempty"`
	Sender         string                  `json:"sender,omitempty"`
	Recipient      string                  `json:"recipient,omitempty"`
	Recipients     []string                `json:"recipients,omitempty"`
	Consumers      []deliveryConsumerState `json:"consumers,omitempty"`
	DeliveryState  string                  `json:"delivery_state"`
	DrainedAt      *time.Time              `json:"drained_at,omitempty"`
	FailedAt       *time.Time              `json:"failed_at,omitempty"`
	LastCheckedAt  *time.Time              `json:"last_checked_at,omitempty"`
	LastCheckError string                  `json:"last_check_error,omitempty"`
	NativeStage    string                  `json:"native_stage,omitempty"`
	EvidenceSource string                  `json:"evidence_source,omitempty"`
	AMQInvoked     bool                    `json:"amq_invoked"`
	TaskID         string                  `json:"task_id,omitempty"`
	OutboxIntentID string                  `json:"outbox_intent_id,omitempty"`
	Root           string                  `json:"root,omitempty"`
	Thread         string                  `json:"thread,omitempty"`
	PaneID         string                  `json:"pane_id,omitempty"`
	Fallback       bool                    `json:"fallback"`
	Acknowledged   bool                    `json:"acknowledged"`
	Stages         []deliveryReceiptStage  `json:"stages"`
	Detail         string                  `json:"detail,omitempty"`
	Path           string                  `json:"path,omitempty"`
	CreatedAt      time.Time               `json:"created_at"`
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
	return fmt.Sprintf("%s-%s", now.Format("20060102T150405.000000000Z"), seed)
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
	lockFile, err := dirRoot.OpenFile(lockName, os.O_CREATE|os.O_RDWR, 0o644)
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
	merged.NativeStage = aggregateNativeStage(merged.Consumers, mergeSetOnce(current.NativeStage, incoming.NativeStage))
	recomputeAggregateDeliveryState(&merged)
	return merged, nil
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
	if a.State == deliveryStateDeliveredNotDrained && b.State == deliveryStateAmbiguousUnknown {
		return a, nil
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
	lockFile, err := dirRoot.OpenFile(lockName, os.O_CREATE|os.O_RDWR, 0o644)
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
	if receipt == nil {
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
	if receipt == nil || len(receipt.Consumers) == 0 || receipt.DeliveryState == deliveryStateAmbiguousUnknown && receipt.LastCheckError != "" {
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
	invoked := receipt
	invoked.AMQInvoked = true
	invoked.addStage("amq_invocation_boundary", "receipt persisted immediately before invoking AMQ; an interruption after this point is delivery-uncertain")
	if err := persistDeliveryReceipt(opts.ProjectDir, opts.Profile, opts.Session, &invoked); err != nil {
		cause := fmt.Errorf("persist AMQ invocation boundary for receipt %s: %w", receipt.AttemptID, err)
		markDeliveryFailedBeforeID(opts.ProjectDir, opts.Profile, opts.Session, &receipt, cause)
		return nil, &receipt, cause
	}
	receipt = invoked
	out, sendErr := runAMQCommand(req)
	markDeliverySendResult(&receipt, out, sendErr)
	if sendErr == nil && receipt.MessageID == "" {
		sendErr = fmt.Errorf("AMQ exited successfully without a parseable stable message id")
		receipt.DeliveryState = deliveryStateAmbiguousUnknown
		receipt.Detail = sendErr.Error()
		receipt.addStage(deliveryStateAmbiguousUnknown, sendErr.Error()+"; inspect the recipient mailbox before any retry")
	}
	if err := persistDeliveryReceipt(opts.ProjectDir, opts.Profile, opts.Session, &receipt); err != nil {
		if sendErr != nil {
			return out, &receipt, fmt.Errorf("%v; persist durable receipt %s for message %s: %w", sendErr, receipt.AttemptID, receipt.MessageID, err)
		}
		return out, &receipt, fmt.Errorf("AMQ send exposed message %s but receipt %s update failed: %w", receipt.MessageID, receipt.AttemptID, err)
	}
	if sendErr != nil {
		return out, &receipt, &durableSendError{Cause: sendErr, Receipt: receipt}
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
	info, err := root.Lstat(name)
	if err != nil {
		return deliveryReceiptData{}, err
	}
	if !info.Mode().IsRegular() {
		return deliveryReceiptData{}, fmt.Errorf("receipt path is not a regular file: %s", path)
	}
	receiptBeforeSecureOpen()
	f, err := root.Open(name)
	if err != nil {
		return deliveryReceiptData{}, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return deliveryReceiptData{}, fmt.Errorf("receipt path changed while opening: %s", path)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return deliveryReceiptData{}, err
	}
	return decodeDeliveryReceipt(b, path)
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
	return receipt, nil
}
