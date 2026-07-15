package task

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// CompletionEvidence is the durable audit projection of one explicit AMQ
// evidence apply. FirstPath is historical evidence and is never rewritten;
// CurrentPath is the location observed for this particular apply.
type CompletionEvidence struct {
	MessageID         string    `json:"message_id"`
	FirstPath         string    `json:"first_path"`
	CurrentPath       string    `json:"current_path"`
	ContentSHA256     string    `json:"content_sha256"`
	BindingSHA256     string    `json:"binding_sha256"`
	From              string    `json:"from,omitempty"`
	To                []string  `json:"to,omitempty"`
	Owner             string    `json:"owner,omitempty"`
	CanonicalThread   string    `json:"canonical_thread,omitempty"`
	ExpectedAssignee  string    `json:"expected_assignee,omitempty"`
	ExpectedAMQSender string    `json:"expected_amq_sender,omitempty"`
	ExpectedRecipient string    `json:"expected_recipient,omitempty"`
	ExpectedThread    string    `json:"expected_thread,omitempty"`
	Blockers          []string  `json:"blockers,omitempty"`
	ObservedAt        time.Time `json:"observed_at"`
	AppliedBy         string    `json:"applied_by"`
	AppliedAt         time.Time `json:"applied_at"`
}

// CompletionReconcile preserves the first evidence and every distinct
// explicit apply. CompletedEvidence identifies the exact binding that closed
// the task. Preview never writes this record.
type CompletionReconcile struct {
	FirstEvidence     *CompletionEvidence  `json:"first_evidence,omitempty"`
	Attempts          []CompletionEvidence `json:"attempts,omitempty"`
	CompletedEvidence *CompletionEvidence  `json:"completed_evidence,omitempty"`
}

type CompletionEvidenceApply struct {
	ExpectedTaskSHA256 string
	Evidence           CompletionEvidence
	Exact              bool
	Actor              string
	Now                time.Time
}

type CompletionEvidenceResult struct {
	Task            Task     `json:"task"`
	ReleasedTaskIDs []string `json:"released_task_ids,omitempty"`
	Changed         bool     `json:"changed"`
}

// completionEvidenceMutationSeam is a deterministic test seam invoked under
// the task-store lock immediately before the accepted file digest is checked.
var completionEvidenceMutationSeam func(dir, taskID string) error

// ApplyCompletionEvidenceForProfile atomically records an explicit evidence
// apply. Exact evidence completes and releases dependencies without queuing a
// completion outbox. Mismatched evidence can only place in-progress work into
// completed_pending_reconcile and retains its assignee and lease.
func ApplyCompletionEvidenceForProfile(projectDir, profile, session, id string, in CompletionEvidenceApply) (CompletionEvidenceResult, error) {
	id = strings.TrimSpace(id)
	if !canonicalTaskID(id) {
		return CompletionEvidenceResult{}, fmt.Errorf("invalid task id %q: expected canonical t<N> leaf", id)
	}
	if id == "" || strings.TrimSpace(in.ExpectedTaskSHA256) == "" || strings.TrimSpace(in.Actor) == "" {
		return CompletionEvidenceResult{}, fmt.Errorf("task id, expected task digest, and apply actor are required")
	}
	if in.Now.IsZero() {
		return CompletionEvidenceResult{}, fmt.Errorf("completion evidence apply requires a non-zero timestamp")
	}
	if err := validateCompletionEvidence(in.Evidence, in.Exact); err != nil {
		return CompletionEvidenceResult{}, err
	}
	if strings.TrimSpace(in.Actor) != strings.TrimSpace(in.Evidence.AppliedBy) {
		return CompletionEvidenceResult{}, fmt.Errorf("completion evidence apply actor %q does not match durable evidence actor %q", in.Actor, in.Evidence.AppliedBy)
	}
	var result CompletionEvidenceResult
	err := withLockForProfile(projectDir, profile, session, func(dir string) error {
		if completionEvidenceMutationSeam != nil {
			if err := completionEvidenceMutationSeam(dir, id); err != nil {
				return err
			}
		}
		tasks, err := readAll(dir)
		if err != nil {
			return err
		}
		all := indexByID(tasks)
		t := all[id]
		if t == nil {
			return fmt.Errorf("task %q not found in workstream %q", id, session)
		}
		if t.Status == StatusCompleted {
			if t.CompletionReconcile != nil && t.CompletionReconcile.CompletedEvidence != nil &&
				t.CompletionReconcile.CompletedEvidence.BindingSHA256 == in.Evidence.BindingSHA256 {
				result.Task = *t
				return nil
			}
			return fmt.Errorf("task %s is already completed by different evidence", id)
		}
		if t.Status == StatusCompletedPendingReconcile && completionAttemptExists(t.CompletionReconcile, in.Evidence.BindingSHA256) && !in.Exact {
			result.Task = *t
			return nil
		}
		if t.Status != StatusInProgress && t.Status != StatusCompletedPendingReconcile {
			return fmt.Errorf("task %s is %s; only in_progress evidence may enter reconciliation", id, t.Status)
		}
		digest, err := taskFileSHA256(dir, id)
		if err != nil {
			return err
		}
		if digest != strings.TrimSpace(in.ExpectedTaskSHA256) {
			return fmt.Errorf("task %s changed before completion evidence commit: accepted digest %s, current digest %s", id, in.ExpectedTaskSHA256, digest)
		}
		if t.CompletionReconcile == nil {
			t.CompletionReconcile = &CompletionReconcile{}
		}
		appendCompletionEvidence(t.CompletionReconcile, in.Evidence)
		changed := map[string]*Task{t.ID: t}
		if !in.Exact {
			if t.Status == StatusInProgress {
				t.Status = StatusCompletedPendingReconcile
			}
			t.UpdatedAt = in.Now
			if err := commitTaskMap(dir, changed, in.Now); err != nil {
				return err
			}
			result.Task, result.Changed = *t, true
			return nil
		}

		t.Status = StatusCompleted
		t.Evidence = "accepted AMQ " + in.Evidence.MessageID
		t.Lease = nil
		t.UpdatedAt = in.Now
		accepted := in.Evidence
		t.CompletionReconcile.CompletedEvidence = &accepted
		for _, candidate := range tasks {
			dependent := all[candidate.ID]
			if dependent == nil || dependent.ReadyAt != nil || !containsString(dependent.DependsOn, id) {
				continue
			}
			unmet, err := unmetDependencies(dependent, all)
			if err != nil {
				return err
			}
			if len(unmet) == 0 {
				ready := in.Now
				dependent.ReadyAt = &ready
				dependent.UpdatedAt = in.Now
				changed[dependent.ID] = dependent
				result.ReleasedTaskIDs = append(result.ReleasedTaskIDs, dependent.ID)
			}
		}
		sort.Strings(result.ReleasedTaskIDs)
		if err := commitTaskMap(dir, changed, in.Now); err != nil {
			return err
		}
		result.Task, result.Changed = *t, true
		return nil
	})
	return result, err
}

func canonicalTaskID(id string) bool {
	if id == "" || filepath.IsAbs(id) || filepath.Base(id) != id || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return false
	}
	n, ok := parseTaskNum(id)
	return ok && n > 0 && fmt.Sprintf("t%d", n) == id
}

func validateCompletionEvidence(e CompletionEvidence, exact bool) error {
	if strings.TrimSpace(e.MessageID) == "" || strings.TrimSpace(e.FirstPath) == "" || strings.TrimSpace(e.CurrentPath) == "" ||
		strings.TrimSpace(e.ContentSHA256) == "" || strings.TrimSpace(e.BindingSHA256) == "" || strings.TrimSpace(e.AppliedBy) == "" || e.AppliedAt.IsZero() {
		return fmt.Errorf("completion evidence identity is incomplete")
	}
	if exact && len(e.Blockers) != 0 {
		return fmt.Errorf("exact completion evidence cannot carry blockers")
	}
	if !exact && len(e.Blockers) == 0 {
		return fmt.Errorf("pending completion evidence requires deterministic blockers")
	}
	return nil
}

func appendCompletionEvidence(state *CompletionReconcile, evidence CompletionEvidence) {
	if state.FirstEvidence == nil {
		first := evidence
		state.FirstEvidence = &first
	}
	for _, existing := range state.Attempts {
		if existing.BindingSHA256 == evidence.BindingSHA256 && existing.ContentSHA256 == evidence.ContentSHA256 {
			return
		}
	}
	state.Attempts = append(state.Attempts, evidence)
}

func completionAttemptExists(state *CompletionReconcile, binding string) bool {
	if state == nil {
		return false
	}
	for _, attempt := range state.Attempts {
		if attempt.BindingSHA256 == binding {
			return true
		}
	}
	return false
}

func taskFileSHA256(dir, id string) (string, error) {
	b, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		return "", fmt.Errorf("read task %s for digest: %w", id, err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func commitTaskMap(dir string, changed map[string]*Task, now time.Time) error {
	images := make([]Task, 0, len(changed))
	for _, t := range changed {
		images = append(images, *t)
	}
	return commitTasks(dir, images, now)
}
