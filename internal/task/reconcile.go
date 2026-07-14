package task

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
)

type ReconcileOptions struct {
	Apply bool
	Now   time.Time
}

type ReconcileFinding struct {
	Kind       string `json:"kind"`
	TaskID     string `json:"task_id,omitempty"`
	RelatedID  string `json:"related_id,omitempty"`
	IntentID   string `json:"intent_id,omitempty"`
	Detail     string `json:"detail"`
	Guidance   string `json:"guidance,omitempty"`
	Repairable bool   `json:"repairable,omitempty"`
}

type ReconcileResult struct {
	RecoveredTransactionID string             `json:"recovered_transaction_id,omitempty"`
	Findings               []ReconcileFinding `json:"findings"`
	ChangedTaskIDs         []string           `json:"changed_task_ids,omitempty"`
}

func ReconcileForProfile(projectDir, profile, session string, opts ReconcileOptions) (ReconcileResult, error) {
	var result ReconcileResult
	if opts.Apply && opts.Now.IsZero() {
		return result, fmt.Errorf("reconcile --apply requires a non-zero timestamp")
	}
	dir := DirForProfile(projectDir, profile, session)
	if _, err := os.Stat(dir); err != nil {
		if os.IsNotExist(err) {
			return result, nil
		}
		return result, err
	}
	err := flock.WithLock(filepath.Join(dir, ".lock"), func() error {
		if _, err := os.Stat(filepath.Join(dir, transactionJournalName)); err == nil {
			id, err := recoverCommittedTransaction(dir)
			if err != nil {
				return err
			}
			result.RecoveredTransactionID = id
			result.Findings = append(result.Findings, ReconcileFinding{Kind: "journal_replayed", Detail: "replayed committed transaction " + id})
		} else if !os.IsNotExist(err) {
			return err
		}
		if _, err := os.Stat(filepath.Join(dir, transactionJournalTmp)); err == nil {
			finding := ReconcileFinding{Kind: "abandoned_journal_temp", Detail: "uncommitted journal temp exists; no commit point was crossed", Guidance: "run task reconcile --apply to remove it", Repairable: true}
			result.Findings = append(result.Findings, finding)
			if opts.Apply {
				if err := os.Remove(filepath.Join(dir, transactionJournalTmp)); err != nil && !os.IsNotExist(err) {
					return err
				}
				if err := syncDirectory(dir); err != nil {
					return err
				}
			}
		} else if !os.IsNotExist(err) {
			return err
		}

		tasks, err := readAll(dir)
		if err != nil {
			return err
		}
		all := indexByID(tasks)
		changed := map[string]*Task{}
		for i := range tasks {
			t := all[tasks[i].ID]
			if t == nil {
				continue
			}
			if t.Status == StatusInProgress {
				if t.Lease == nil {
					result.Findings = append(result.Findings, ReconcileFinding{Kind: "legacy_unleased", TaskID: t.ID, Detail: "in_progress legacy task has no lease metadata", Guidance: "the assigned worker must run task renew, or an actor must use task release with an audited reason; ownership is preserved"})
				} else if !opts.Now.IsZero() && !t.Lease.ExpiresAt.After(opts.Now) {
					result.Findings = append(result.Findings, ReconcileFinding{Kind: "stale_lease", TaskID: t.ID, Detail: fmt.Sprintf("lease for %s expired at %s", t.Lease.Owner, t.Lease.ExpiresAt.UTC().Format(time.RFC3339Nano)), Guidance: "renew by the assignee or explicitly release with --me and --reason; reconcile never unclaims it", Repairable: true})
					if opts.Apply && (t.Lease.StaleObservedAt == nil || !t.Lease.StaleObservedAt.Equal(opts.Now)) {
						observed := opts.Now
						t.Lease.StaleObservedAt = &observed
						t.UpdatedAt = opts.Now
						changed[t.ID] = t
					}
				}
			}
			inspectLinks(t, all, opts.Apply, opts.Now, changed, &result.Findings)
			for _, intent := range sortedOutbox(t.Outbox) {
				switch intent.State {
				case OutboxPending:
					result.Findings = append(result.Findings, ReconcileFinding{Kind: "outbox_pending", TaskID: t.ID, IntentID: intent.ID, Detail: "delivery intent is committed but has not begun", Guidance: fmt.Sprintf("task deliver %s --intent %s --me <handle>", t.ID, intent.ID)})
				case OutboxSending, OutboxUncertain:
					result.Findings = append(result.Findings, ReconcileFinding{Kind: "outbox_delivery_uncertain", TaskID: t.ID, IntentID: intent.ID, Detail: "delivery began but no durable outcome was recorded", Guidance: retryGuidance(t.ID, intent.ID, true) + "; never retry unless non-delivery is confirmed"})
				case OutboxFailed:
					result.Findings = append(result.Findings, ReconcileFinding{Kind: "outbox_failed", TaskID: t.ID, IntentID: intent.ID, Detail: "delivery failed: " + intent.LastError, Guidance: retryGuidance(t.ID, intent.ID, false) + "; or task release with an audited reason"})
				}
			}
		}
		for _, t := range tasks {
			if replacementChainCycles(all, t.ID) {
				result.Findings = append(result.Findings, ReconcileFinding{Kind: "replacement_cycle", TaskID: t.ID, RelatedID: t.ReplacedBy, Detail: "replacement links contain a cycle", Guidance: "repair the conflicting links explicitly; reconcile will not guess"})
			}
			if reviewChainCycles(all, t.ID) {
				result.Findings = append(result.Findings, ReconcileFinding{Kind: "review_cycle", TaskID: t.ID, RelatedID: t.ReviewOf, Detail: "review_of links contain a cycle", Guidance: "repair the conflicting links explicitly; reconcile will not guess"})
			}
		}
		if opts.Apply && len(changed) > 0 {
			images := make([]Task, 0, len(changed))
			for id, t := range changed {
				images = append(images, *t)
				result.ChangedTaskIDs = append(result.ChangedTaskIDs, id)
			}
			sort.Strings(result.ChangedTaskIDs)
			if err := commitTasks(dir, images, opts.Now); err != nil {
				return err
			}
		}
		return nil
	})
	sort.Slice(result.Findings, func(i, j int) bool {
		a, b := result.Findings[i], result.Findings[j]
		ak := a.TaskID + "\x00" + a.Kind + "\x00" + a.RelatedID + "\x00" + a.IntentID
		bk := b.TaskID + "\x00" + b.Kind + "\x00" + b.RelatedID + "\x00" + b.IntentID
		return ak < bk
	})
	return result, err
}

func replacementChainCycles(all map[string]*Task, start string) bool {
	seen := map[string]bool{}
	for current := strings.TrimSpace(start); current != ""; {
		if seen[current] {
			return true
		}
		seen[current] = true
		t := all[current]
		if t == nil {
			return false
		}
		current = strings.TrimSpace(t.ReplacedBy)
	}
	return false
}

func reviewChainCycles(all map[string]*Task, start string) bool {
	seen := map[string]bool{}
	for current := strings.TrimSpace(start); current != ""; {
		if seen[current] {
			return true
		}
		seen[current] = true
		t := all[current]
		if t == nil {
			return false
		}
		current = strings.TrimSpace(t.ReviewOf)
	}
	return false
}

func inspectLinks(t *Task, all map[string]*Task, apply bool, now time.Time, changed map[string]*Task, findings *[]ReconcileFinding) {
	if id := strings.TrimSpace(t.ReplacedBy); id != "" {
		target := all[id]
		if target == nil {
			*findings = append(*findings, ReconcileFinding{Kind: "dangling_replaced_by", TaskID: t.ID, RelatedID: id, Detail: "replacement target does not exist"})
		} else if target.Replaces != t.ID {
			repairable := target.Replaces == ""
			*findings = append(*findings, ReconcileFinding{Kind: "asymmetric_replacement", TaskID: t.ID, RelatedID: id, Detail: fmt.Sprintf("%s.replaced_by points to %s but reverse replaces is %q", t.ID, id, target.Replaces), Repairable: repairable})
			if apply && repairable {
				target.Replaces, target.UpdatedAt, changed[target.ID] = t.ID, now, target
			}
		}
	}
	if id := strings.TrimSpace(t.Replaces); id != "" {
		target := all[id]
		if target == nil {
			*findings = append(*findings, ReconcileFinding{Kind: "dangling_replaces", TaskID: t.ID, RelatedID: id, Detail: "replaced task does not exist"})
		} else if target.ReplacedBy != t.ID {
			repairable := target.ReplacedBy == ""
			*findings = append(*findings, ReconcileFinding{Kind: "asymmetric_replacement", TaskID: t.ID, RelatedID: id, Detail: fmt.Sprintf("%s.replaces points to %s but reverse replaced_by is %q", t.ID, id, target.ReplacedBy), Repairable: repairable})
			if apply && repairable {
				target.ReplacedBy, target.UpdatedAt, changed[target.ID] = t.ID, now, target
			}
		}
	}
	if id := strings.TrimSpace(t.ReviewOf); id != "" {
		target := all[id]
		if target == nil {
			*findings = append(*findings, ReconcileFinding{Kind: "dangling_review_of", TaskID: t.ID, RelatedID: id, Detail: "review target does not exist"})
		} else if !containsString(target.ReviewTasks, t.ID) {
			*findings = append(*findings, ReconcileFinding{Kind: "asymmetric_review", TaskID: t.ID, RelatedID: id, Detail: "review target is missing reverse review_tasks link", Repairable: true})
			if apply {
				target.ReviewTasks, target.UpdatedAt, changed[target.ID] = appendUniqueSorted(target.ReviewTasks, t.ID), now, target
			}
		}
	}
	for _, id := range t.ReviewTasks {
		review := all[id]
		if review == nil {
			*findings = append(*findings, ReconcileFinding{Kind: "dangling_review_task", TaskID: t.ID, RelatedID: id, Detail: "review task does not exist"})
		} else if review.ReviewOf != t.ID {
			*findings = append(*findings, ReconcileFinding{Kind: "asymmetric_review", TaskID: t.ID, RelatedID: id, Detail: fmt.Sprintf("review task points to %q instead", review.ReviewOf)})
		}
	}
}

func sortedOutbox(in []OutboxIntent) []OutboxIntent {
	out := append([]OutboxIntent(nil), in...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func retryGuidance(taskID, intentID string, uncertain bool) string {
	command := fmt.Sprintf("task retry-delivery %s --intent %s --me <handle> --reason <why>", taskID, intentID)
	if uncertain {
		command += " --confirm-not-delivered"
	}
	return command
}
