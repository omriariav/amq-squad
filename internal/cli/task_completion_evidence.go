package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
)

type taskCompletionEvidencePreview struct {
	TaskID          string             `json:"task_id"`
	TaskPath        string             `json:"task_path"`
	TaskNamespace   squadnamespace.Ref `json:"task_namespace"`
	TaskFileSHA256  string             `json:"task_file_sha256"`
	MessageID       string             `json:"message_id"`
	FirstPath       string             `json:"first_evidence_path,omitempty"`
	CurrentPath     string             `json:"current_evidence_path,omitempty"`
	ContentSHA256   string             `json:"content_sha256,omitempty"`
	From            string             `json:"from,omitempty"`
	To              []string           `json:"to,omitempty"`
	Owner           string             `json:"owner,omitempty"`
	CanonicalThread string             `json:"canonical_thread,omitempty"`
	Expected        taskEvidenceExpect `json:"expected"`
	ProposedState   string             `json:"proposed_state"`
	Blockers        []string           `json:"blockers,omitempty"`
	BindingSHA256   string             `json:"binding_sha256,omitempty"`
	ReplicaPaths    []string           `json:"replica_paths,omitempty"`
	Warnings        []state.Warning    `json:"scan_warnings,omitempty"`
	Exact           bool               `json:"exact"`
}

type taskEvidenceExpect struct {
	Assignee string `json:"assignee,omitempty"`
	Sender   string `json:"sender,omitempty"`
	To       string `json:"to,omitempty"`
	Thread   string `json:"canonical_thread,omitempty"`
}

type canonicalTaskMessage struct {
	ID         string   `json:"id"`
	From       string   `json:"from"`
	To         []string `json:"to"`
	RawTo      string   `json:"raw_to"`
	RawThread  string   `json:"raw_thread"`
	RawSubject string   `json:"raw_subject"`
	RawBody    string   `json:"raw_body"`
	RawCreated string   `json:"raw_created"`
	Kind       string   `json:"kind"`
	Lifecycle  any      `json:"task_lifecycle,omitempty"`
}

var taskCompletionMessageScanner = state.ScanSessionMessages

func assessTaskCompletionEvidence(selected taskSelection, evidenceID string, now time.Time) (taskCompletionEvidencePreview, error) {
	evidenceID = strings.TrimSpace(evidenceID)
	if evidenceID == "" || strings.ContainsAny(evidenceID, "\r\n\x00") {
		return taskCompletionEvidencePreview{}, usageErrorf("--evidence-id requires one exact AMQ message id")
	}
	expected := taskEvidenceExpect{Assignee: strings.TrimSpace(selected.Task.AssignedTo)}
	if dispatch, ok := taskstore.CanonicalDispatch(selected.Task); ok {
		expected.Sender = strings.TrimSpace(dispatch.Assignee)
		expected.To = strings.TrimSpace(dispatch.Sender)
		expected.Thread = statusCanonicalThread(dispatch.Thread)
	}
	preview := taskCompletionEvidencePreview{
		TaskID:         selected.Task.ID,
		TaskPath:       selected.TaskPath,
		TaskNamespace:  selected.Namespace,
		TaskFileSHA256: selected.FileSHA256,
		MessageID:      evidenceID,
		Expected:       expected,
		ProposedState:  selected.Task.Status,
	}
	messages, warnings := taskCompletionMessageScanner(selected.Namespace.AMQRoot, func() time.Time { return now })
	preview.Warnings = warnings
	var matches []state.Message
	for _, message := range messages {
		if strings.TrimSpace(message.ID) == evidenceID {
			matches = append(matches, message)
		}
	}
	if len(matches) == 0 {
		preview.Blockers = []string{"evidence_id_not_found"}
		return preview, nil
	}

	type digested struct {
		message state.Message
		digest  string
	}
	groups := map[string][]state.Message{}
	for _, message := range matches {
		digest, err := canonicalTaskMessageSHA256(message)
		if err != nil {
			return taskCompletionEvidencePreview{}, err
		}
		groups[digest] = append(groups[digest], message)
	}
	if len(groups) != 1 {
		preview.Blockers = []string{"conflicting_same_id_content"}
		for _, message := range matches {
			preview.ReplicaPaths = append(preview.ReplicaPaths, message.Path)
		}
		sort.Strings(preview.ReplicaPaths)
		return preview, nil
	}
	var contentDigest string
	var replicas []state.Message
	for digest, same := range groups {
		contentDigest, replicas = digest, same
	}
	sort.Slice(replicas, func(i, j int) bool { return replicas[i].Path < replicas[j].Path })
	chosen := replicas[0]
	for _, replica := range replicas {
		preview.ReplicaPaths = append(preview.ReplicaPaths, replica.Path)
		if strings.TrimSpace(replica.Owner) == expected.To {
			chosen = replica
		}
	}
	preview.CurrentPath = chosen.Path
	preview.FirstPath = chosen.Path
	preview.ContentSHA256 = contentDigest
	preview.From = strings.TrimSpace(chosen.From)
	preview.To = append([]string(nil), chosen.To...)
	preview.Owner = strings.TrimSpace(chosen.Owner)
	preview.CanonicalThread = statusCanonicalThread(chosen.Thread)
	var blockers []string
	if record := selected.Task.CompletionReconcile; record != nil && record.FirstEvidence != nil && record.FirstEvidence.MessageID == evidenceID {
		preview.FirstPath = record.FirstEvidence.FirstPath
		if record.FirstEvidence.ContentSHA256 != contentDigest {
			blockers = append(blockers, "recorded_evidence_content_mismatch")
		}
	}

	if !chosen.SchemaOK {
		blockers = append(blockers, "degraded_message_schema")
	}
	if !chosen.AuthorityRaw {
		blockers = append(blockers, "untrusted_raw_authority")
	}
	if !chosen.ToPresent || !chosen.ToArrayValid || len(chosen.To) != 1 {
		blockers = append(blockers, "invalid_recipient_envelope")
	}
	if chosen.RawThread == "" || chosen.RawThread != preview.CanonicalThread || chosen.RawThread != expected.Thread {
		blockers = append(blockers, "repaired_or_noncanonical_thread")
	}
	envelope, lifecyclePresent, lifecycleErr := taskstore.DecodeLifecycleEnvelope(chosen.Context)
	if lifecycleErr != nil {
		blockers = append(blockers, "invalid_structured_lifecycle")
	} else if !lifecyclePresent {
		blockers = append(blockers, "missing_structured_lifecycle")
	}
	if chosen.Kind != state.KindStatus || !lifecyclePresent || lifecycleErr != nil || envelope.Event != taskstore.LifecycleDone {
		blockers = append(blockers, "not_done_completion_evidence")
	}
	if lifecyclePresent && lifecycleErr == nil {
		blockers = append(blockers, lifecycleCorrelationBlockers(selected, chosen, envelope)...)
		prepared, preparedErr := currentPreparedGenerationRef(selected.ProjectDir, selected.Profile, selected.Session)
		if preparedErr != nil {
			return taskCompletionEvidencePreview{}, preparedErr
		}
		if prepared == nil || envelope.GenerationRef != *prepared {
			blockers = append(blockers, "current_prepared_generation_mismatch")
		}
	}
	if expected.Assignee == "" || expected.Sender == "" || expected.Assignee != expected.Sender {
		blockers = append(blockers, "task_dispatch_assignee_mismatch")
	}
	if preview.From == "" || preview.From != expected.Assignee || preview.From != expected.Sender {
		blockers = append(blockers, "sender_assignee_mismatch")
	}
	if expected.To == "" || len(preview.To) != 1 || strings.TrimSpace(preview.To[0]) != expected.To {
		blockers = append(blockers, "recipient_mismatch")
	}
	if preview.Owner != expected.To {
		blockers = append(blockers, "owner_mismatch")
	}
	if expected.Thread == "" || preview.CanonicalThread != expected.Thread {
		blockers = append(blockers, "thread_mismatch")
	}
	if !taskEvidencePathAllowed(selected.Namespace.AMQRoot, preview.CurrentPath, preview.Owner) {
		blockers = append(blockers, "evidence_path_outside_namespace")
	}
	preview.Blockers = sortedUniqueStrings(blockers)
	preview.BindingSHA256 = taskEvidenceBindingSHA256(selected, preview)
	preview.Exact = len(preview.Blockers) == 0
	if preview.Exact {
		if selected.Task.Status == taskstore.StatusCompleted && selected.Task.CompletionReconcile != nil &&
			selected.Task.CompletionReconcile.CompletedEvidence != nil && selected.Task.CompletionReconcile.CompletedEvidence.BindingSHA256 == preview.BindingSHA256 {
			preview.ProposedState = taskstore.StatusCompleted
		} else {
			preview.ProposedState = taskstore.StatusCompleted
		}
	} else if evidenceCanEnterPending(preview.Blockers) &&
		(selected.Task.Status == taskstore.StatusInProgress || selected.Task.Status == taskstore.StatusCompletedPendingReconcile) {
		preview.ProposedState = taskstore.StatusCompletedPendingReconcile
	}
	return preview, nil
}

func canonicalTaskMessageSHA256(message state.Message) (string, error) {
	to := append([]string(nil), message.To...)
	for i := range to {
		to[i] = strings.TrimSpace(to[i])
	}
	sort.Strings(to)
	canonical := canonicalTaskMessage{
		ID: strings.TrimSpace(message.ID), From: strings.TrimSpace(message.From), To: to,
		RawTo: message.ToRaw, RawThread: message.RawThread, RawSubject: message.RawSubject,
		RawBody: message.RawBody, RawCreated: message.RawCreated, Kind: string(message.Kind), Lifecycle: taskLifecycleContextProjection(message.Context),
	}
	b, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func taskLifecycleContextProjection(context map[string]any) any {
	root, _ := context["amq_squad"].(map[string]any)
	return root["task_lifecycle"]
}

func lifecycleCorrelationBlockers(selected taskSelection, message state.Message, envelope taskstore.LifecycleEnvelope) []string {
	var blockers []string
	t := selected.Task
	if envelope.TaskID != t.ID {
		blockers = append(blockers, "task_identity_mismatch")
	}
	if envelope.Actor != strings.TrimSpace(t.AssignedTo) {
		blockers = append(blockers, "lifecycle_actor_mismatch")
	}
	if envelope.Profile != selected.Profile || envelope.Session != selected.Session || envelope.NamespaceID != selected.Namespace.ID {
		blockers = append(blockers, "lifecycle_namespace_mismatch")
	}
	if t.LifecycleGenerationRef == nil {
		blockers = append(blockers, "task_generation_ref_missing")
	} else if envelope.GenerationRef != *t.LifecycleGenerationRef {
		blockers = append(blockers, "run_generation_mismatch")
	}
	if strings.TrimSpace(t.LifecycleTaskGeneration) == "" || envelope.TaskGeneration != t.LifecycleTaskGeneration {
		blockers = append(blockers, "task_generation_mismatch")
	}
	dispatch, dispatchOK := taskstore.CanonicalDispatch(t)
	if !dispatchOK || envelope.DispatchMessageID != strings.TrimSpace(dispatch.MessageID) {
		blockers = append(blockers, "dispatch_message_mismatch")
	}
	var matched *taskstore.OutboxIntent
	for i := range t.Outbox {
		if t.Outbox[i].ID == envelope.OutboxIntentID {
			matched = &t.Outbox[i]
			break
		}
	}
	if matched == nil {
		blockers = append(blockers, "outbox_intent_mismatch")
	} else {
		matchedDigest, matchedErr := lifecycleEnvelopeDigest(matched.Lifecycle)
		envelopeDigest, envelopeErr := taskstore.LifecycleEnvelopeSHA256(envelope)
		if matchedErr != nil || envelopeErr != nil || matchedDigest != envelopeDigest {
			blockers = append(blockers, "outbox_lifecycle_mismatch")
		}
		if strings.TrimSpace(matched.MessageID) == "" || matched.MessageID != strings.TrimSpace(message.ID) {
			blockers = append(blockers, "outbox_message_mismatch")
		}
	}
	if envelope.EvidenceRef == nil {
		blockers = append(blockers, "lifecycle_evidence_missing")
	} else if err := taskstore.ValidateLifecycleEvidenceRef(selected.ProjectDir, selected.Profile, selected.Session, t, *envelope.EvidenceRef); err != nil {
		blockers = append(blockers, "lifecycle_evidence_mismatch")
	}
	return blockers
}

func lifecycleEnvelopeDigest(envelope *taskstore.LifecycleEnvelope) (string, error) {
	if envelope == nil {
		return "", fmt.Errorf("missing lifecycle envelope")
	}
	return taskstore.LifecycleEnvelopeSHA256(*envelope)
}

func taskEvidenceBindingSHA256(selected taskSelection, preview taskCompletionEvidencePreview) string {
	identity := struct {
		Project, Profile, Session, TaskID, TaskPath string
		EvidenceID, ContentSHA256                   string
		Assignee, Sender, To, Thread                string
	}{
		Project: filepath.Clean(selected.ProjectDir), Profile: selected.Profile, Session: selected.Session,
		TaskID: selected.Task.ID, TaskPath: filepath.Clean(selected.TaskPath), EvidenceID: preview.MessageID,
		ContentSHA256: preview.ContentSHA256, Assignee: preview.Expected.Assignee, Sender: preview.Expected.Sender,
		To: preview.Expected.To, Thread: preview.Expected.Thread,
	}
	b, _ := json.Marshal(identity)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func completionDONEOnly(subject string) bool {
	subject = strings.ToUpper(strings.TrimSpace(subject))
	return subject == "DONE" || strings.HasPrefix(subject, "DONE:") || strings.HasPrefix(subject, "DONE ")
}

func messageContainsExactTaskID(message state.Message, taskID string) bool {
	text := message.Subject + "\n" + message.Body
	if message.AuthorityRaw {
		text = message.RawSubject + "\n" + message.RawBody
	}
	for offset := 0; ; {
		i := strings.Index(text[offset:], taskID)
		if i < 0 {
			return false
		}
		i += offset
		beforeOK := i == 0 || !taskIDWordByte(text[i-1])
		after := i + len(taskID)
		afterOK := after == len(text) || !taskIDWordByte(text[after])
		if beforeOK && afterOK {
			return true
		}
		offset = i + 1
	}
}

func taskIDWordByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '-' || b == '_'
}

func taskEvidencePathAllowed(root, path, owner string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) >= 5 && parts[0] == "agents" && parts[1] == owner && parts[2] == "inbox" && (parts[3] == "new" || parts[3] == "cur")
}

func evidenceCanEnterPending(blockers []string) bool {
	if len(blockers) == 0 {
		return false
	}
	for _, blocker := range blockers {
		switch blocker {
		case "sender_assignee_mismatch", "recipient_mismatch", "owner_mismatch", "thread_mismatch", "task_dispatch_assignee_mismatch":
		default:
			return false
		}
	}
	return true
}

func sortedUniqueStrings(in []string) []string {
	set := map[string]struct{}{}
	for _, item := range in {
		set[item] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for item := range set {
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func completionEvidenceRecord(preview taskCompletionEvidencePreview, actor string, now time.Time) taskstore.CompletionEvidence {
	return taskstore.CompletionEvidence{
		MessageID: preview.MessageID, FirstPath: preview.FirstPath, CurrentPath: preview.CurrentPath,
		ContentSHA256: preview.ContentSHA256, BindingSHA256: preview.BindingSHA256,
		From: preview.From, To: append([]string(nil), preview.To...), Owner: preview.Owner,
		CanonicalThread: preview.CanonicalThread, ExpectedAssignee: preview.Expected.Assignee,
		ExpectedAMQSender: preview.Expected.Sender, ExpectedRecipient: preview.Expected.To,
		ExpectedThread: preview.Expected.Thread, Blockers: append([]string(nil), preview.Blockers...),
		ObservedAt: now, AppliedBy: strings.TrimSpace(actor), AppliedAt: now,
	}
}

func validateCompletionApplyPreview(preview taskCompletionEvidencePreview) error {
	if preview.CurrentPath == "" || preview.ContentSHA256 == "" || preview.BindingSHA256 == "" {
		return fmt.Errorf("completion evidence %s cannot be applied: %s", preview.MessageID, strings.Join(preview.Blockers, ", "))
	}
	if !preview.Exact && !evidenceCanEnterPending(preview.Blockers) {
		return fmt.Errorf("completion evidence %s is not accepted DONE evidence: %s", preview.MessageID, strings.Join(preview.Blockers, ", "))
	}
	return nil
}
