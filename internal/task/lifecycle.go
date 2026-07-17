package task

import (
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/namespace"
)

type ClaimOptions struct {
	Actor          string
	LeaseDuration  time.Duration
	OverrideReason string
	Now            time.Time
}

func ClaimWithOptionsForProfile(projectDir, profile, session, id string, opts ClaimOptions) (Task, error) {
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		return Task{}, fmt.Errorf("--me handle is required to claim a task")
	}
	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = DefaultLeaseDuration
	}
	return mutateForProfile(projectDir, profile, session, id, func(t *Task, all map[string]*Task) error {
		if t.Status != StatusPending {
			return fmt.Errorf("task %s is %s, not pending; only pending tasks can be claimed", id, t.Status)
		}
		switch t.Intent {
		case IntentImplement, IntentLifecycle:
			if actor != strings.TrimSpace(t.Implementer) {
				return fmt.Errorf("task %s %s actor is implementer %q; %q cannot claim mutation authority", id, t.Intent, t.Implementer, actor)
			}
		case IntentReview, IntentAudit:
			if actor != strings.TrimSpace(t.Reviewer) {
				return fmt.Errorf("task %s %s actor is reviewer %q; %q cannot claim it", id, t.Intent, t.Reviewer, actor)
			}
		}
		unmet, err := unmetDependencies(t, all)
		if err != nil {
			return err
		}
		if len(unmet) > 0 {
			reason := strings.TrimSpace(opts.OverrideReason)
			if reason == "" {
				first := unmet[0]
				return fmt.Errorf("task %s is blocked on %s (%s); complete it first or use an explicit audited dependency override", id, first.TaskID, first.Status)
			}
			t.DependencyOverrides = append(t.DependencyOverrides, DependencyOverride{Actor: actor, Reason: reason, At: opts.Now, Unmet: unmet})
		}
		t.Status = StatusInProgress
		t.AssignedTo = actor
		t.Evidence, t.FailureReason, t.BlockReason, t.ResetReason, t.CancelReason = "", "", "", "", ""
		t.Lease = newLease(actor, opts.Now, opts.LeaseDuration)
		t.UpdatedAt = opts.Now
		return nil
	})
}

func unmetDependencies(t *Task, all map[string]*Task) ([]DependencyState, error) {
	var unmet []DependencyState
	for _, dep := range t.DependsOn {
		d := all[dep]
		if d == nil {
			return nil, fmt.Errorf("task %s depends on %s, which does not exist", t.ID, dep)
		}
		if d.Status != StatusCompleted {
			unmet = append(unmet, DependencyState{TaskID: dep, Status: d.Status})
		}
	}
	sort.Slice(unmet, func(i, j int) bool { return unmet[i].TaskID < unmet[j].TaskID })
	return unmet, nil
}

func newLease(owner string, now time.Time, duration time.Duration) *Lease {
	return &Lease{Owner: owner, IssuedAt: now, RenewedAt: now, ExpiresAt: now.Add(duration)}
}

func RenewLeaseForProfile(projectDir, profile, session, id, actor string, duration time.Duration, now time.Time) (Task, error) {
	if duration <= 0 {
		duration = DefaultLeaseDuration
	}
	return mutateForProfile(projectDir, profile, session, id, func(t *Task, _ map[string]*Task) error {
		if t.Status != StatusInProgress && t.Status != StatusCompletedPendingReconcile {
			return fmt.Errorf("task %s is %s, not in_progress; only active claims have leases", id, t.Status)
		}
		if err := requireAssignee(t, actor, "renew"); err != nil {
			return err
		}
		owner := strings.TrimSpace(t.AssignedTo)
		if owner == "" {
			owner = strings.TrimSpace(actor)
		}
		if t.Lease == nil {
			// Explicit renewal is the migration path for a legacy in-progress task.
			t.Lease = newLease(owner, now, duration)
		} else {
			t.Lease.Owner = owner
			t.Lease.RenewedAt = now
			t.Lease.ExpiresAt = now.Add(duration)
			t.Lease.StaleObservedAt = nil
		}
		t.UpdatedAt = now
		return nil
	})
}

type DoneOptions struct {
	Actor                string
	Evidence             string
	FinalHead            string
	CompletionGeneration string
	GateCorrelation      *CompletionGateCorrelation
	DispatchNextID       string
	LeaseDuration        time.Duration
	Notify               bool
	Now                  time.Time
}

type DoneResult struct {
	Task            Task
	ReleasedTaskIDs []string
	Successor       *Task
	Outbox          []OutboxIntent
}

func DoneAtomicForProfile(projectDir, profile, session, id string, opts DoneOptions) (DoneResult, error) {
	id = strings.TrimSpace(id)
	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = DefaultLeaseDuration
	}
	var result DoneResult
	err := withLockForProfile(projectDir, profile, session, func(dir string) error {
		tasks, err := readAll(dir)
		if err != nil {
			return err
		}
		all := indexByID(tasks)
		completed := all[id]
		if completed == nil {
			return fmt.Errorf("task %q not found in workstream %q", id, session)
		}
		generation := strings.TrimSpace(opts.CompletionGeneration)
		if generation == "" {
			generation = intentID(completed.ID, "completion-generation", opts.Now)
		}
		if err := validateCompletionGateCorrelation(completed.ID, profile, session, opts.GateCorrelation); err != nil {
			return err
		}
		if completed.Status == StatusCompleted && completed.CompletionLifecycle != nil {
			lifecycle := completed.CompletionLifecycle
			if lifecycle.Generation == generation && completionGateCorrelationIdentityEqual(lifecycle.Gate, opts.GateCorrelation) {
				if completionGateCorrelationEqual(lifecycle.Gate, opts.GateCorrelation) {
					result.Task = *completed
					return nil
				}
				if lifecycle.Gate != nil && lifecycle.Gate.State != "open_preserved" && opts.GateCorrelation != nil && opts.GateCorrelation.State == "open_preserved" {
					return fmt.Errorf("task %s completion gate reconciliation cannot reopen terminal request %s", id, lifecycle.Gate.RequestMessageID)
				}
				lifecycle.GateHistory = appendCompletionGateAudit(lifecycle.GateHistory, lifecycle.Gate)
				lifecycle.Gate = cloneCompletionGateCorrelation(opts.GateCorrelation)
				lifecycle.GateHistory = appendCompletionGateAudit(lifecycle.GateHistory, lifecycle.Gate)
				completed.UpdatedAt = opts.Now
				if err := commitTasks(dir, []Task{*completed}, opts.Now); err != nil {
					return err
				}
				result.Task = *completed
				return nil
			}
			return fmt.Errorf("task %s is already completed by generation %s", id, lifecycle.Generation)
		}
		if completed.Status != StatusInProgress {
			return fmt.Errorf("task %s is %s, not in_progress; claim it before marking it completed", id, completed.Status)
		}
		if err := requireAssignee(completed, opts.Actor, StatusCompleted); err != nil {
			return err
		}
		completed.Status = StatusCompleted
		completed.Evidence = strings.TrimSpace(opts.Evidence)
		completed.FinalHead = strings.TrimSpace(opts.FinalHead)
		completed.Lease = nil
		completed.UpdatedAt = opts.Now
		completed.CompletionLifecycle = &CompletionLifecycle{
			Generation: generation, Actor: strings.TrimSpace(opts.Actor), CompletedAt: opts.Now,
			Gate: cloneCompletionGateCorrelation(opts.GateCorrelation),
		}
		completed.CompletionLifecycle.GateHistory = appendCompletionGateAudit(nil, completed.CompletionLifecycle.Gate)

		changed := map[string]*Task{completed.ID: completed}
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
				ready := opts.Now
				dependent.ReadyAt = &ready
				dependent.UpdatedAt = opts.Now
				changed[dependent.ID] = dependent
				result.ReleasedTaskIDs = append(result.ReleasedTaskIDs, dependent.ID)
			}
		}
		sort.Strings(result.ReleasedTaskIDs)

		if route, ok := completionRoute(completed); ok {
			if opts.Notify {
				intent := OutboxIntent{
					ID: intentID(completed.ID, "completion", opts.Now), TaskID: completed.ID, Type: "completion", State: OutboxPending,
					From: strings.TrimSpace(opts.Actor), To: route.Sender,
					Thread: route.Thread, Kind: "status",
					Subject: "DONE: " + completed.Title, Body: completionBody(*completed), CreatedAt: opts.Now, UpdatedAt: opts.Now,
				}
				completed.CompletionLifecycle.ReportIntentID = intent.ID
				completed.Outbox = append(completed.Outbox, intent)
				result.Outbox = append(result.Outbox, intent)
			} else {
				completed.NotificationSuppression = &SuppressionAudit{Actor: strings.TrimSpace(opts.Actor), Reason: "explicit --no-notify", At: opts.Now}
			}
		}

		if nextID := strings.TrimSpace(opts.DispatchNextID); nextID != "" {
			successor := all[nextID]
			if successor == nil {
				return fmt.Errorf("successor task %q not found", nextID)
			}
			if !containsString(successor.DependsOn, id) {
				return fmt.Errorf("successor task %s does not directly depend on completed task %s", nextID, id)
			}
			if successor.Status != StatusPending {
				return fmt.Errorf("successor task %s is %s, not pending", nextID, successor.Status)
			}
			if strings.TrimSpace(successor.AssignedTo) == "" {
				return fmt.Errorf("successor task %s has no assigned_to handle for dispatch", nextID)
			}
			if unmet, err := unmetDependencies(successor, all); err != nil {
				return err
			} else if len(unmet) > 0 {
				return fmt.Errorf("successor task %s remains blocked on %s (%s)", nextID, unmet[0].TaskID, unmet[0].Status)
			}
			successor.Status = StatusInProgress
			successor.Lease = newLease(successor.AssignedTo, opts.Now, opts.LeaseDuration)
			successor.UpdatedAt = opts.Now
			intent := OutboxIntent{
				ID: intentID(successor.ID, "dispatch", opts.Now), TaskID: successor.ID, Type: "successor_dispatch", State: OutboxPending,
				From: strings.TrimSpace(opts.Actor), To: strings.TrimSpace(successor.AssignedTo), Kind: "todo",
				Subject: successor.Title, Body: successor.Description, CreatedAt: opts.Now, UpdatedAt: opts.Now,
			}
			successor.Outbox = append(successor.Outbox, intent)
			changed[successor.ID] = successor
			copy := *successor
			result.Successor = &copy
			result.Outbox = append(result.Outbox, intent)
		}

		toWrite := make([]Task, 0, len(changed))
		for _, t := range changed {
			toWrite = append(toWrite, *t)
		}
		if err := commitTasks(dir, toWrite, opts.Now); err != nil {
			return err
		}
		result.Task = *completed
		return nil
	})
	return result, err
}

func validateCompletionGateCorrelation(taskID, profile, session string, correlation *CompletionGateCorrelation) error {
	if correlation == nil {
		return nil
	}
	if strings.TrimSpace(correlation.TaskID) != taskID || strings.TrimSpace(correlation.Profile) != namespace.NormalizeProfile(profile) ||
		strings.TrimSpace(correlation.Session) != strings.TrimSpace(session) || strings.TrimSpace(correlation.NamespaceID) != namespace.ID(profile, session) {
		return fmt.Errorf("completion gate correlation does not match exact task namespace")
	}
	for name, value := range map[string]string{
		"namespace_generation": correlation.NamespaceGeneration,
		"thread":               correlation.Thread,
		"request_message_id":   correlation.RequestMessageID,
		"request_sha256":       correlation.RequestSHA256,
	} {
		if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("completion gate correlation %s must be non-empty and trim-canonical", name)
		}
	}
	if len(correlation.RequestSHA256) != 64 || strings.ToLower(correlation.RequestSHA256) != correlation.RequestSHA256 {
		return fmt.Errorf("completion gate correlation request_sha256 must be exactly 64 lowercase hex characters")
	}
	if _, err := hex.DecodeString(correlation.RequestSHA256); err != nil {
		return fmt.Errorf("completion gate correlation request_sha256 must be exactly 64 lowercase hex characters")
	}
	if correlation.Reason == "" || correlation.Reason != strings.TrimSpace(correlation.Reason) || strings.ContainsAny(correlation.Reason, "\r\n\x00") {
		return fmt.Errorf("completion gate correlation reason must be non-empty canonical single-line text")
	}
	if !strings.HasPrefix(correlation.Thread, "gate/") || correlation.ObservedAt.IsZero() {
		return fmt.Errorf("completion gate correlation thread and observation are required")
	}
	switch correlation.State {
	case "answered", "closed", "withdrawn", "superseded":
		if !correlation.Suppressed {
			return fmt.Errorf("terminal completion gate correlation must suppress completed attention")
		}
	case "open_preserved":
		if correlation.Suppressed {
			return fmt.Errorf("unresolved completion gate correlation must remain unsuppressed")
		}
	default:
		return fmt.Errorf("unsupported completion gate correlation state %q", correlation.State)
	}
	return nil
}

func cloneCompletionGateCorrelation(in *CompletionGateCorrelation) *CompletionGateCorrelation {
	if in == nil {
		return nil
	}
	copy := *in
	return &copy
}

func completionGateCorrelationEqual(a, b *CompletionGateCorrelation) bool {
	return completionGateCorrelationIdentityEqual(a, b) && (a == nil || a.State == b.State && a.Suppressed == b.Suppressed)
}

func completionGateCorrelationIdentityEqual(a, b *CompletionGateCorrelation) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.TaskID == b.TaskID && a.Profile == b.Profile && a.Session == b.Session &&
		a.NamespaceID == b.NamespaceID && a.NamespaceGeneration == b.NamespaceGeneration &&
		a.Thread == b.Thread && a.RequestMessageID == b.RequestMessageID && a.RequestSHA256 == b.RequestSHA256
}

func appendCompletionGateAudit(history []CompletionGateCorrelation, gate *CompletionGateCorrelation) []CompletionGateCorrelation {
	if gate == nil {
		return history
	}
	if len(history) > 0 && completionGateCorrelationEqual(&history[len(history)-1], gate) {
		return history
	}
	return append(history, *cloneCompletionGateCorrelation(gate))
}

func completionBody(t Task) string {
	if strings.TrimSpace(t.Evidence) != "" {
		return strings.TrimSpace(t.Evidence)
	}
	return fmt.Sprintf("Task %s completed.", t.ID)
}

func completionRoute(t *Task) (Dispatch, bool) {
	if t.Dispatch != nil && strings.TrimSpace(t.Dispatch.Sender) != "" {
		return Dispatch{Sender: strings.TrimSpace(t.Dispatch.Sender), Thread: strings.TrimSpace(t.Dispatch.Thread)}, true
	}
	// A task-backed dispatch commits its route in the outbox before AMQ send.
	// That route remains authoritative if legacy dispatch metadata was not yet
	// finalized, and successor_dispatch intentionally has no separate link step.
	for i := len(t.Outbox) - 1; i >= 0; i-- {
		intent := t.Outbox[i]
		if (intent.Type == "task_dispatch" || intent.Type == "successor_dispatch") && strings.TrimSpace(intent.From) != "" {
			return Dispatch{Sender: strings.TrimSpace(intent.From), Thread: strings.TrimSpace(intent.Thread)}, true
		}
	}
	return Dispatch{}, false
}

func intentID(taskID, kind string, now time.Time) string {
	return fmt.Sprintf("%s-%s-%d", taskID, kind, now.UnixNano())
}

func containsString(in []string, value string) bool {
	for _, item := range in {
		if item == value {
			return true
		}
	}
	return false
}

func CancelForProfile(projectDir, profile, session, id, actor, reason, replacementID string, now time.Time) (Task, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Task{}, fmt.Errorf("--reason is required to cancel a task")
	}
	var out Task
	err := withLockForProfile(projectDir, profile, session, func(dir string) error {
		tasks, err := readAll(dir)
		if err != nil {
			return err
		}
		all := indexByID(tasks)
		cancelled := all[strings.TrimSpace(id)]
		if cancelled == nil {
			return fmt.Errorf("task %q not found in workstream %q", id, session)
		}
		if cancelled.Status == StatusCompleted || cancelled.Status == StatusCancelled || cancelled.Status == StatusCompletedPendingReconcile {
			return fmt.Errorf("task %s is %s and cannot be cancelled", id, cancelled.Status)
		}
		if cancelled.Status == StatusInProgress {
			if err := requireAssignee(cancelled, actor, "cancel"); err != nil {
				return err
			}
		}
		changed := []*Task{cancelled}
		if replacementID = strings.TrimSpace(replacementID); replacementID != "" {
			replacement := all[replacementID]
			if replacement == nil {
				return fmt.Errorf("replacement task %q does not exist", replacementID)
			}
			if replacementID == cancelled.ID || replacementChainReaches(all, replacementID, cancelled.ID) {
				return fmt.Errorf("replacement link %s -> %s would create a cycle", cancelled.ID, replacementID)
			}
			if replacement.Replaces != "" && replacement.Replaces != cancelled.ID {
				return fmt.Errorf("replacement task %s already replaces %s", replacement.ID, replacement.Replaces)
			}
			cancelled.ReplacedBy = replacement.ID
			replacement.Replaces = cancelled.ID
			replacement.UpdatedAt = now
			changed = append(changed, replacement)
		}
		cancelled.Status = StatusCancelled
		cancelled.CancelReason = reason
		cancelled.Lease = nil
		cancelled.UpdatedAt = now
		images := make([]Task, 0, len(changed))
		for _, t := range changed {
			images = append(images, *t)
		}
		if err := commitTasks(dir, images, now); err != nil {
			return err
		}
		out = *cancelled
		return nil
	})
	return out, err
}

func replacementChainReaches(all map[string]*Task, start, target string) bool {
	seen := map[string]bool{}
	for current := strings.TrimSpace(start); current != "" && !seen[current]; {
		if current == target {
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

func ReleaseForProfile(projectDir, profile, session, id, actor, reason string, now time.Time) (Task, error) {
	actor, reason = strings.TrimSpace(actor), strings.TrimSpace(reason)
	if actor == "" || reason == "" {
		return Task{}, fmt.Errorf("--me and --reason are required for an audited release")
	}
	return mutateForProfile(projectDir, profile, session, id, func(t *Task, _ map[string]*Task) error {
		if t.Status != StatusInProgress {
			return fmt.Errorf("task %s is %s, not in_progress", id, t.Status)
		}
		t.Releases = append(t.Releases, ReleaseAudit{Actor: actor, Reason: reason, At: now})
		t.Status = StatusPending
		t.AssignedTo = ""
		t.Lease = nil
		t.UpdatedAt = now
		return nil
	})
}

func BeginOutboxDeliveryForProfile(projectDir, profile, session, taskID, intentID string, now time.Time) (OutboxIntent, error) {
	var out OutboxIntent
	_, err := mutateForProfile(projectDir, profile, session, taskID, func(t *Task, _ map[string]*Task) error {
		intent, err := taskOutboxIntent(t, intentID)
		if err != nil {
			return err
		}
		if intent.State != OutboxPending {
			return fmt.Errorf("outbox intent %s is %s, not pending", intentID, intent.State)
		}
		intent.State = OutboxSending
		intent.UpdatedAt = now
		out = *intent
		t.UpdatedAt = now
		return nil
	})
	return out, err
}

// AttachOutboxReceiptForProfile links a pre-send durable receipt projection to
// an already-sending outbox intent. The receipt is evidence; State remains the
// transport lifecycle owned by the outbox transaction.
func AttachOutboxReceiptForProfile(projectDir, profile, session, taskID, intentID, attemptID, receiptPath string, now time.Time) (OutboxIntent, error) {
	attemptID, receiptPath = strings.TrimSpace(attemptID), strings.TrimSpace(receiptPath)
	if attemptID == "" || receiptPath == "" {
		return OutboxIntent{}, fmt.Errorf("outbox receipt attempt id and path are required")
	}
	var out OutboxIntent
	_, err := mutateForProfile(projectDir, profile, session, taskID, func(t *Task, _ map[string]*Task) error {
		intent, err := taskOutboxIntent(t, intentID)
		if err != nil {
			return err
		}
		if intent.State != OutboxSending {
			return fmt.Errorf("outbox intent %s is %s, not sending", intentID, intent.State)
		}
		if intent.ReceiptAttemptID != "" && len(intent.ReceiptAttempts) == 0 {
			intent.ReceiptAttempts = []ReceiptLink{{AttemptID: intent.ReceiptAttemptID, Path: intent.ReceiptPath}}
		}
		if intent.ReceiptAttemptID != "" && (intent.ReceiptAttemptID != attemptID || intent.ReceiptPath != receiptPath) {
			for _, historical := range intent.ReceiptAttempts {
				if historical.AttemptID == attemptID || historical.Path == receiptPath {
					return fmt.Errorf("outbox intent %s cannot replay historical receipt attempt or path %s", intentID, attemptID)
				}
			}
			// A replacement consumes the newest retry audit exactly once. Earlier
			// audits may legitimately have no receipt when that retry failed during
			// resolution before a receipt attempt could be reserved.
			if len(intent.RetryAudits) == 0 {
				return fmt.Errorf("outbox intent %s cannot replace receipt %s without a pending audited retry", intentID, intent.ReceiptAttemptID)
			}
			audit := &intent.RetryAudits[len(intent.RetryAudits)-1]
			if audit.ReceiptAttemptID != "" || audit.ReceiptPath != "" {
				return fmt.Errorf("outbox intent %s latest audited retry already links receipt %s", intentID, audit.ReceiptAttemptID)
			}
			if (audit.PreviousState == OutboxSending || audit.PreviousState == OutboxUncertain) && !audit.ConfirmedNotDelivered {
				return fmt.Errorf("outbox intent %s uncertain retry audit lacks confirmed non-delivery", intentID)
			}
			audit.ReceiptAttemptID, audit.ReceiptPath = attemptID, receiptPath
		}
		intent.ReceiptAttemptID = attemptID
		intent.ReceiptPath = receiptPath
		link := ReceiptLink{AttemptID: intent.ReceiptAttemptID, Path: intent.ReceiptPath}
		if !containsReceiptLink(intent.ReceiptAttempts, link) {
			intent.ReceiptAttempts = append(intent.ReceiptAttempts, link)
		}
		intent.UpdatedAt = now
		t.UpdatedAt = now
		out = *intent
		return nil
	})
	return out, err
}

func PendingOutboxIntentForProfile(projectDir, profile, session, taskID, intentID string) (OutboxIntent, error) {
	t, err := ShowForProfile(projectDir, profile, session, taskID)
	if err != nil {
		return OutboxIntent{}, err
	}
	intent, err := taskOutboxIntent(&t, intentID)
	if err != nil {
		return OutboxIntent{}, err
	}
	if intent.State != OutboxPending {
		return OutboxIntent{}, fmt.Errorf("outbox intent %s is %s, not pending; use retry-delivery only for failed or confirmed-undelivered uncertain intents", intentID, intent.State)
	}
	return *intent, nil
}

type DispatchIntentOptions struct {
	From             string
	Assignee         string
	Thread           string
	Kind             string
	Subject          string
	Body             string
	ReceiptAttemptID string
	ReceiptPath      string
	LeaseDuration    time.Duration
	Now              time.Time
	Create           *AddInput
	Leadership       LeadershipExpectation
}

// LeadershipExpectation is checked while the task-store lock is held, in the
// same critical section that commits task creation/claim and the outbox intent.
// EpochSpecified distinguishes an omitted backward-compatible epoch 0 from an
// explicit value after a durable leadership handoff exists.
type LeadershipExpectation struct {
	Sender         string
	ExpectedEpoch  uint64
	EpochSpecified bool
}

type DispatchPrepareResult struct {
	Task            Task
	Intent          OutboxIntent
	DidClaim        bool
	LeadershipEpoch *uint64
}

// PrepareDispatchForProfile commits a task-backed dispatch intent (and the
// pending -> in_progress claim when needed) before the caller may send AMQ.
func PrepareDispatchForProfile(projectDir, profile, session, taskID string, opts DispatchIntentOptions) (DispatchPrepareResult, error) {
	if opts.LeaseDuration <= 0 {
		opts.LeaseDuration = DefaultLeaseDuration
	}
	var result DispatchPrepareResult
	err := withLockForProfile(projectDir, profile, session, func(dir string) error {
		epoch, err := authorizeLeadershipUnderLock(dir, opts.Leadership)
		if err != nil {
			return err
		}
		tasks, err := readAll(dir)
		if err != nil {
			return err
		}
		changed := map[string]Task{}
		if opts.Create != nil {
			if strings.TrimSpace(taskID) != "" {
				return fmt.Errorf("dispatch cannot create and select an existing task in the same transaction")
			}
			created, createdChanges, err := addTaskUnderLock(tasks, *opts.Create, opts.Now)
			if err != nil {
				return err
			}
			taskID = created.ID
			tasks = append(tasks, created)
			for _, candidate := range createdChanges {
				changed[candidate.ID] = candidate
			}
		}
		all := indexByID(tasks)
		t := all[strings.TrimSpace(taskID)]
		if t == nil {
			return fmt.Errorf("task %q not found in workstream %q", taskID, session)
		}
		assignee := strings.TrimSpace(opts.Assignee)
		if authority := AuthorityActor(*t); authority != "" && assignee != authority {
			return fmt.Errorf("task %s %s authority actor is %s; dispatch target uses handle %s", t.ID, t.Intent, authority, assignee)
		}
		if assigned := strings.TrimSpace(t.AssignedTo); assigned != "" && assigned != assignee {
			return fmt.Errorf("task %s is assigned to %s; dispatch target uses handle %s", t.ID, assigned, assignee)
		}
		switch t.Status {
		case StatusPending:
			unmet, err := unmetDependencies(t, all)
			if err != nil {
				return err
			}
			if len(unmet) > 0 {
				return fmt.Errorf("task %s is blocked on %s (%s); complete it before dispatch", t.ID, unmet[0].TaskID, unmet[0].Status)
			}
			t.Status = StatusInProgress
			t.AssignedTo = assignee
			t.Lease = newLease(assignee, opts.Now, opts.LeaseDuration)
			result.DidClaim = true
		case StatusInProgress:
			if strings.TrimSpace(t.AssignedTo) != assignee {
				return fmt.Errorf("task %s is in_progress for %s, not %s", t.ID, t.AssignedTo, assignee)
			}
		case StatusCompletedPendingReconcile, StatusCompleted, StatusFailed, StatusBlocked, StatusCancelled:
			return fmt.Errorf("task %s is %s; dispatch requires pending or in_progress", t.ID, t.Status)
		default:
			return fmt.Errorf("task %s has unknown status %q; dispatch requires pending or in_progress", t.ID, t.Status)
		}
		intent := OutboxIntent{
			ID: nextIntentID(t, "dispatch", opts.Now), TaskID: t.ID, Type: "task_dispatch", State: OutboxPending,
			From: strings.TrimSpace(opts.From), To: assignee, Thread: strings.TrimSpace(opts.Thread), Kind: strings.TrimSpace(opts.Kind),
			Subject: strings.TrimSpace(opts.Subject), Body: opts.Body,
			ReceiptAttemptID: strings.TrimSpace(opts.ReceiptAttemptID), ReceiptPath: strings.TrimSpace(opts.ReceiptPath),
			CreatedAt: opts.Now, UpdatedAt: opts.Now,
		}
		if epoch != nil {
			value := *epoch
			intent.LeadershipEpoch = &value
			result.LeadershipEpoch = &value
		}
		if intent.ReceiptAttemptID != "" {
			intent.ReceiptAttempts = []ReceiptLink{{AttemptID: intent.ReceiptAttemptID, Path: intent.ReceiptPath}}
		}
		t.Outbox = append(t.Outbox, intent)
		t.UpdatedAt = opts.Now
		changed[t.ID] = *t
		toCommit := make([]Task, 0, len(changed))
		for _, candidate := range changed {
			toCommit = append(toCommit, candidate)
		}
		if err := commitTasks(dir, toCommit, opts.Now); err != nil {
			return err
		}
		result.Task, result.Intent = *t, intent
		return nil
	})
	return result, err
}

func authorizeLeadershipUnderLock(dir string, expectation LeadershipExpectation) (*uint64, error) {
	state, err := readLeadership(dir)
	if err != nil {
		return nil, fmt.Errorf("read leadership authority during dispatch transaction: %w", err)
	}
	sender := strings.TrimSpace(expectation.Sender)
	if state.Epoch == 0 {
		if expectation.EpochSpecified && expectation.ExpectedEpoch != 0 {
			return nil, fmt.Errorf("dispatch refused: no durable leadership handoff exists; expected backward-compatible epoch 0")
		}
		return nil, nil
	}
	if !expectation.EpochSpecified || expectation.ExpectedEpoch != state.Epoch {
		return nil, fmt.Errorf("dispatch refused: leadership epoch is %d; recover the current record and authorize that exact epoch", state.Epoch)
	}
	if sender != state.CurrentLead {
		return nil, fmt.Errorf("dispatch refused: sender %q is stale at leadership epoch %d; current lead is %q", sender, state.Epoch, state.CurrentLead)
	}
	epoch := state.Epoch
	return &epoch, nil
}

func nextIntentID(t *Task, kind string, now time.Time) string {
	base := intentID(t.ID, kind, now)
	used := map[string]bool{}
	for _, intent := range t.Outbox {
		used[intent.ID] = true
	}
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s-%d", base, n)
		if !used[candidate] {
			return candidate
		}
	}
}

func FinishDispatchForProfile(projectDir, profile, session, taskID, intentID string, dispatch Dispatch, outcome DeliveryOutcome, now time.Time) (Task, OutboxIntent, error) {
	var out Task
	var outIntent OutboxIntent
	err := withLockForProfile(projectDir, profile, session, func(dir string) error {
		tasks, err := readAll(dir)
		if err != nil {
			return err
		}
		all := indexByID(tasks)
		t := all[strings.TrimSpace(taskID)]
		if t == nil {
			return fmt.Errorf("task %q not found in workstream %q", taskID, session)
		}
		intent, err := taskOutboxIntent(t, intentID)
		if err != nil {
			return err
		}
		if intent.State != OutboxSending {
			return fmt.Errorf("outbox intent %s is %s, not sending", intentID, intent.State)
		}
		if err := validateDeliveryOutcome(outcome); err != nil {
			return err
		}
		intent.MessageID = strings.TrimSpace(outcome.MessageID)
		intent.UpdatedAt = now
		switch outcome.State {
		case DeliveryDelivered:
			intent.State, intent.LastError = OutboxDelivered, ""
		case DeliveryUncertain:
			intent.State, intent.LastError = OutboxUncertain, strings.TrimSpace(outcome.Error)
		case DeliveryFailedBeforeInvoke:
			intent.State, intent.LastError = OutboxFailed, strings.TrimSpace(outcome.Error)
		}
		dispatch.Sender = strings.TrimSpace(dispatch.Sender)
		dispatch.Assignee = strings.TrimSpace(dispatch.Assignee)
		dispatch.Thread = strings.TrimSpace(dispatch.Thread)
		dispatch.Kind = strings.TrimSpace(dispatch.Kind)
		dispatch.Subject = strings.TrimSpace(dispatch.Subject)
		dispatch.MessageID = strings.TrimSpace(outcome.MessageID)
		if dispatch.DispatchedAt.IsZero() {
			dispatch.DispatchedAt = now
		}
		t.Dispatch = &dispatch
		t.UpdatedAt = now
		if err := commitTasks(dir, []Task{*t}, now); err != nil {
			return err
		}
		out, outIntent = *t, *intent
		return nil
	})
	return out, outIntent, err
}

func FinishOutboxDeliveryForProfile(projectDir, profile, session, taskID, intentID string, outcome DeliveryOutcome, now time.Time) (OutboxIntent, error) {
	var out OutboxIntent
	_, err := mutateForProfile(projectDir, profile, session, taskID, func(t *Task, _ map[string]*Task) error {
		intent, err := taskOutboxIntent(t, intentID)
		if err != nil {
			return err
		}
		if intent.State != OutboxSending {
			return fmt.Errorf("outbox intent %s is %s, not sending", intentID, intent.State)
		}
		if err := validateDeliveryOutcome(outcome); err != nil {
			return err
		}
		intent.MessageID = strings.TrimSpace(outcome.MessageID)
		intent.UpdatedAt = now
		switch outcome.State {
		case DeliveryDelivered:
			intent.State = OutboxDelivered
			intent.LastError = ""
		case DeliveryUncertain:
			intent.State = OutboxUncertain
			intent.LastError = strings.TrimSpace(outcome.Error)
		case DeliveryFailedBeforeInvoke:
			intent.State = OutboxFailed
			intent.LastError = strings.TrimSpace(outcome.Error)
		}
		out = *intent
		t.UpdatedAt = now
		return nil
	})
	return out, err
}

func PrepareOutboxRetryForProfile(projectDir, profile, session, taskID, intentID, actor, reason string, confirmNotDelivered bool, now time.Time) (OutboxIntent, error) {
	actor, reason = strings.TrimSpace(actor), strings.TrimSpace(reason)
	if actor == "" || reason == "" {
		return OutboxIntent{}, fmt.Errorf("--me and --reason are required to retry delivery")
	}
	var out OutboxIntent
	_, err := mutateForProfile(projectDir, profile, session, taskID, func(t *Task, _ map[string]*Task) error {
		intent, err := taskOutboxIntent(t, intentID)
		if err != nil {
			return err
		}
		switch intent.State {
		case OutboxFailed:
		case OutboxSending, OutboxUncertain:
			if !confirmNotDelivered {
				return fmt.Errorf("outbox intent %s is delivery-uncertain; retry requires --confirm-not-delivered", intentID)
			}
		default:
			return fmt.Errorf("outbox intent %s is %s; retry requires failed or delivery-uncertain", intentID, intent.State)
		}
		previousState := intent.State
		intent.RetryAudits = append(intent.RetryAudits, RetryAudit{Actor: actor, Reason: reason, At: now, PreviousState: previousState, ConfirmedNotDelivered: confirmNotDelivered})
		intent.State = OutboxPending
		intent.MessageID, intent.LastError = "", ""
		intent.UpdatedAt = now
		out = *intent
		t.UpdatedAt = now
		return nil
	})
	return out, err
}

func validateDeliveryOutcome(outcome DeliveryOutcome) error {
	outcome.MessageID = strings.TrimSpace(outcome.MessageID)
	switch outcome.State {
	case DeliveryDelivered:
		if outcome.MessageID == "" {
			return fmt.Errorf("delivered outcome requires a stable message id")
		}
	case DeliveryUncertain:
		if outcome.MessageID != "" {
			return fmt.Errorf("delivery-uncertain outcome cannot carry a stable message id")
		}
	case DeliveryFailedBeforeInvoke:
		if outcome.MessageID != "" {
			return fmt.Errorf("pre-invocation failure cannot carry a stable message id")
		}
	default:
		return fmt.Errorf("unknown delivery outcome %q", outcome.State)
	}
	return nil
}

func containsReceiptLink(links []ReceiptLink, want ReceiptLink) bool {
	for _, link := range links {
		if link.AttemptID == want.AttemptID && link.Path == want.Path {
			return true
		}
	}
	return false
}

func taskOutboxIntent(t *Task, id string) (*OutboxIntent, error) {
	for i := range t.Outbox {
		if t.Outbox[i].ID == strings.TrimSpace(id) {
			return &t.Outbox[i], nil
		}
	}
	return nil, fmt.Errorf("outbox intent %q not found on task %s", id, t.ID)
}
