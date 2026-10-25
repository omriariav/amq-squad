package task

import (
	"fmt"
	"strings"
	"time"
)

// Lease and state transitions (docs/task-store-design.md, "Leases"). Every
// function runs in one locked transaction, so a task never has two holders
// and a seat never holds two leases.

// Blocker is why a pending task cannot get a lease now ("" when it can):
// an unfinished dependency, or a file lock held by an in-progress or
// blocked task.
func Blocker(t Task, all []Task) string {
	byID := map[string]Task{}
	for _, o := range all {
		byID[o.ID] = o
	}
	for _, d := range t.DependsOn {
		if byID[d].State != StateCompleted {
			return WaitDependencies
		}
	}
	if len(t.DeclaredFiles) > 0 {
		for _, o := range all {
			if o.ID != t.ID && (o.State == StateInProgress || o.State == StateBlocked) && FilesOverlap(t.DeclaredFiles, o.DeclaredFiles) {
				return WaitFileLock
			}
		}
	}
	return ""
}

// HeldBy returns the task that keeps the seat busy: the one whose lease it
// holds, or a blocked task whose done report it filed (its worktree diff
// waits for the lead's decision). Nil when the seat is free.
func HeldBy(holder string, all []Task) *Task {
	for i := range all {
		t := &all[i]
		if t.Lease != nil && t.Lease.Holder == holder {
			return t
		}
		if t.State == StateBlocked && t.Report != nil && t.Report.By == holder {
			return t
		}
	}
	return nil
}

func grant(t *Task, all *tasks, holder string, pool *Pool, dur time.Duration, now time.Time) error {
	holder = strings.TrimSpace(holder)
	if holder == "" {
		return fmt.Errorf("a lease needs a holder handle")
	}
	if t.State != StatePending {
		return fmt.Errorf("task %s is %s, not pending", t.ID, t.State)
	}
	if why := Blocker(*t, all.list); why != "" {
		return fmt.Errorf("task %s cannot be leased: %s", t.ID, why)
	}
	if busy := HeldBy(holder, all.list); busy != nil {
		return fmt.Errorf("seat %s already holds task %s", holder, busy.ID)
	}
	if all.dir != "" {
		if err := quarantined(all.dir, holder); err != nil {
			return err
		}
	}
	if dur <= 0 {
		dur = DefaultLeaseDuration
	}
	if pool != nil {
		p := *pool
		t.Pool = &p
	}
	t.State, t.WaitReason, t.Attempt = StateInProgress, "", t.Attempt+1
	t.Lease = &Lease{Holder: holder, GrantedAt: now, ExpiresAt: now.Add(dur)}
	return nil
}

func claim(t *Task, dur time.Duration, now time.Time) {
	if dur <= 0 {
		dur = DefaultLeaseDuration
	}
	at := now
	if t.Lease.ClaimedAt == nil {
		t.Lease.ClaimedAt = &at
	} else {
		t.Lease.Renewals++
	}
	// Monotonic: an older timestamp never moves renewed_at back or shortens
	// an extension another writer already committed.
	if t.Lease.RenewedAt == nil || at.After(*t.Lease.RenewedAt) {
		t.Lease.RenewedAt = &at
	}
	if exp := now.Add(dur); exp.After(t.Lease.ExpiresAt) {
		t.Lease.ExpiresAt = exp
	}
}

// endAttempt closes the current lease into an attempt row.
func endAttempt(t *Task, outcome, detail string, now time.Time) {
	if t.Lease == nil {
		return
	}
	t.Attempts = append(t.Attempts, Attempt{N: t.Attempt, Holder: t.Lease.Holder, Pool: t.Pool, GrantedAt: t.Lease.GrantedAt, EndedAt: now, Outcome: outcome, Detail: detail, Verdict: t.Lease.Verdict})
	t.Lease = nil
}

func requireHolder(t *Task, holder string) error {
	if t.State != StateInProgress || t.Lease == nil {
		return fmt.Errorf("task %s is %s with no lease", t.ID, t.State)
	}
	if t.Lease.Holder != strings.TrimSpace(holder) {
		return fmt.Errorf("task %s is leased to %s, not %q", t.ID, t.Lease.Holder, holder)
	}
	return nil
}

// ErrLeaseExpired is returned when the holder acts on a lease at or after
// its expires_at (the same boundary as renewal and ExpireDue): the lease is
// over even before the scheduler's next tick returns it to the pool.
var ErrLeaseExpired = fmt.Errorf("%w: the lease expired", ErrNoLease)

// requireLiveHolder is requireHolder for claim and report operations: an
// expired lease cannot be claimed, extended or reported on.
func requireLiveHolder(t *Task, holder string, now time.Time) error {
	if err := requireHolder(t, holder); err != nil {
		return err
	}
	if !now.Before(t.Lease.ExpiresAt) {
		return fmt.Errorf("task %s: %w at %s", t.ID, ErrLeaseExpired, t.Lease.ExpiresAt.UTC().Format(time.RFC3339))
	}
	return nil
}

// Grant leases a pending task to a seat (the scheduler, or `dispatch`).
// The lease is unclaimed until the seat claims it.
func (s Store) Grant(id, holder string, pool *Pool, dur time.Duration, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, all *tasks) error { return grant(t, all, holder, pool, dur, now) })
}

// Claim claims the holder's lease on id. With an empty id it claims the
// holder's unclaimed lease, or else pulls the next eligible pending task
// (of pool, when given; a task with no pool matches any) and claims it in
// the same transaction.
func (s Store) Claim(id, holder string, pool *Pool, dur time.Duration, now time.Time) (Task, error) {
	holder = strings.TrimSpace(holder)
	var out Task
	err := s.update(now, func(all *tasks) error {
		var t *Task
		if id = strings.TrimSpace(id); id != "" {
			if t = all.get(id); t == nil {
				return fmt.Errorf("%w: %q", ErrNotFound, id)
			}
			if t.State == StatePending {
				if err := grant(t, all, holder, pool, dur, now); err != nil {
					return err
				}
			} else if err := requireLiveHolder(t, holder, now); err != nil {
				return err
			}
		} else if held := HeldBy(holder, all.list); held != nil {
			if held.Lease == nil || held.Lease.ClaimedAt != nil {
				return fmt.Errorf("seat %s already holds task %s", holder, held.ID)
			}
			t = all.get(held.ID)
			if err := requireLiveHolder(t, holder, now); err != nil {
				return err
			}
		} else {
			order := append([]Task(nil), all.list...)
			DispatchOrder(order)
			for _, c := range order {
				if c.State == StatePending && Blocker(c, all.list) == "" && (pool == nil || c.Pool == nil || *c.Pool == *pool) {
					t = all.get(c.ID)
					break
				}
			}
			if t == nil {
				return fmt.Errorf("%w: no eligible pending task for %s", ErrNotFound, holder)
			}
			if err := grant(t, all, holder, pool, dur, now); err != nil {
				return err
			}
		}
		claim(t, dur, now)
		all.touch(t.ID)
		out = *t
		return nil
	})
	return out, err
}

// Renew extends the holder's claimed lease. It writes nothing when the last
// renewal is younger than minInterval (renewed false). An unclaimed lease is
// never renewed (ErrNoLease).
func (s Store) Renew(holder string, dur, minInterval time.Duration, now time.Time) (Task, bool, error) {
	return s.renew(s.update, holder, dur, minInterval, now)
}

// RenewNoWait is Renew for the hook path (evidence ingest): it never waits
// for the store lock and returns ErrBusy when another writer holds it. A
// skipped renewal costs nothing: the next tool result renews.
func (s Store) RenewNoWait(holder string, dur, minInterval time.Duration, now time.Time) (Task, bool, error) {
	return s.renew(s.updateNoWait, holder, dur, minInterval, now)
}

func (s Store) renew(update func(time.Time, func(*tasks) error) error, holder string, dur, minInterval time.Duration, now time.Time) (Task, bool, error) {
	list, err := Snapshot(s.Dir) // lock-free pre-check keeps the hook path cheap
	if err != nil {
		return Task{}, false, err
	}
	held := HeldBy(holder, list)
	if held == nil || held.Lease == nil || held.Lease.ClaimedAt == nil {
		return Task{}, false, ErrNoLease
	}
	if held.Lease.RenewedAt != nil && now.Sub(*held.Lease.RenewedAt) < minInterval && now.Before(held.Lease.ExpiresAt) {
		return *held, false, nil
	}
	if renewAfterPrecheck != nil {
		renewAfterPrecheck()
	}
	var out Task
	wrote := false
	err = update(now, func(all *tasks) error {
		t := all.get(held.ID)
		if t == nil {
			return ErrNoLease
		}
		if err := requireHolder(t, holder); err != nil || t.Lease.ClaimedAt == nil {
			return ErrNoLease
		}
		if !now.Before(t.Lease.ExpiresAt) {
			return fmt.Errorf("%w: the lease on %s expired at %s", ErrNoLease, t.ID, t.Lease.ExpiresAt.Format(time.RFC3339))
		}
		out = *t
		// The interval is rechecked against the locked lease: another writer
		// may have renewed since the lock-free pre-check.
		if t.Lease.RenewedAt != nil && now.Sub(*t.Lease.RenewedAt) < minInterval {
			return nil
		}
		claim(t, dur, now)
		all.touch(t.ID)
		out, wrote = *t, true
		return nil
	})
	if wrote {
		out.UpdatedAt = now
	}
	return out, err == nil && wrote, err
}

// renewAfterPrecheck is a test seam between the lock-free pre-check and the
// locked renewal.
var renewAfterPrecheck func()

// Done files the holder's done report. A task with no declared files
// completes at once; one with declared files waits, in progress, for the
// harness commit step (Complete or RefuseCommit). Either way a class with a
// mechanical verifier completes only on the harness's verdict (complete).
func (s Store) Done(id, holder, evidence, finalHead string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if err := requireLiveHolder(t, holder, now); err != nil {
			return err
		}
		if t.Report != nil {
			return fmt.Errorf("task %s already has a done report from %s", t.ID, t.Report.By)
		}
		if t.Lease.ClaimedAt == nil {
			at := now
			t.Lease.ClaimedAt = &at
		}
		t.Report = &Report{By: t.Lease.Holder, At: now, Evidence: strings.TrimSpace(evidence), FinalHead: strings.TrimSpace(finalHead)}
		if len(t.DeclaredFiles) == 0 {
			s.complete(t, now)
		}
		return nil
	})
}

// complete accepts a done report. A class whose verifier command is
// configured (Store.Verifiers: the grant, narrowed by squad.yaml) completes
// only on the harness's recorded pass verdict on this attempt
// (RecordVerdict), never on the seat's report (phase 5 re-pass, ruling 1 and
// its ruling): with no verdict the task blocks for the lead with
// verdict_missing, and the attempt ends blocked, which the router counts as
// a failed attempt. A class with no configured verifier completes on the
// done report. A recorded fail verdict always blocks (verdict_failed). The
// report stays, as on a commit refusal, until the lead's reset.
func (s Store) complete(t *Task, now time.Time) {
	var v *Verdict
	if t.Lease != nil {
		v = t.Lease.Verdict
	}
	if required, why := s.verifierRequired(*t); required || v != nil {
		switch {
		case v == nil:
			endAttempt(t, OutcomeBlocked, BlockVerdictMissing, now)
			_ = block(t, BlockVerdictMissing, fmt.Sprintf("class %s completes only on a recorded %s verdict from the harness; the done report has none%s", t.Class, ClassVerifier(t.Class), why), RouteLead)
			return
		case v.Result != VerdictPass:
			endAttempt(t, OutcomeBlocked, BlockVerdictFailed, now)
			_ = block(t, BlockVerdictFailed, fmt.Sprintf("the %s verdict from %s is %s: %s", v.Verifier, v.Source, v.Result, v.Detail), RouteLead)
			return
		}
	}
	t.Evidence = t.Report.Evidence
	endAttempt(t, OutcomeCompleted, "", now)
	at := now
	t.State, t.ClosedAt, t.BlockReason, t.BlockRoute, t.BlockDetail = StateCompleted, &at, "", "", ""
}

// verifierRequired says whether the task's class needs the harness's
// verdict: a mechanical verifier whose command is configured, or, with no
// source or a source that fails, every mechanical verifier (fail closed).
func (s Store) verifierRequired(t Task) (bool, string) {
	if !MechanicalVerifier(ClassVerifier(t.Class)) {
		return false, ""
	}
	if s.Verifiers == nil {
		return true, ""
	}
	cmd, required, err := s.Verifiers(t)
	if err != nil {
		return true, " (the verifier configuration could not be read: " + err.Error() + ")"
	}
	if required && cmd == "" {
		return true, " (the squad's verifier gate names the class, but the owner grant names no verifier command for it)"
	}
	return required, ""
}

// RecordVerdict records the harness's verdict on the current attempt of a
// task whose class has a mechanical verifier: the commit step's test run or
// the probe's predicate. It is the harness's call, never the seat's; one
// verdict per attempt, of the class's own verifier.
func (s Store) RecordVerdict(id string, v Verdict, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		want := ClassVerifier(t.Class)
		switch {
		case !MechanicalVerifier(want):
			return fmt.Errorf("task %s: class %s has no mechanical verifier", t.ID, t.Class)
		case t.State != StateInProgress || t.Lease == nil:
			return fmt.Errorf("task %s is %s with no attempt to record a verdict on", t.ID, t.State)
		case t.Lease.Verdict != nil:
			return fmt.Errorf("task %s attempt %d already has a %s verdict", t.ID, t.Attempt, t.Lease.Verdict.Result)
		case v.Verifier != want:
			return fmt.Errorf("task %s: verdict of verifier %q, class %s is verified by %s", t.ID, v.Verifier, t.Class, want)
		case v.Result != VerdictPass && v.Result != VerdictFail:
			return fmt.Errorf("verdict result %q: want pass or fail", v.Result)
		case strings.TrimSpace(v.Source) == "":
			return fmt.Errorf("a verdict names the harness step that ran the verifier")
		}
		v.Source, v.Detail, v.At = strings.TrimSpace(v.Source), strings.TrimSpace(v.Detail), now
		t.Lease.Verdict = &v
		return nil
	})
}

// Complete is the commit step's acceptance of a done report.
func (s Store) Complete(id string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if t.State != StateInProgress || t.Report == nil {
			return fmt.Errorf("task %s has no done report awaiting the commit step", t.ID)
		}
		s.complete(t, now)
		return nil
	})
}

// RefuseCommit is the commit step's refusal: the task blocks with the
// refusal's reason code, the report stays (the seat's diff waits), and the
// seat stays reserved until Widen re-arms the commit or Reset discards it.
func (s Store) RefuseCommit(id, reasonCode, detail, route string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if t.State != StateInProgress || t.Report == nil {
			return fmt.Errorf("task %s has no done report awaiting the commit step", t.ID)
		}
		endAttempt(t, OutcomeCommitRefused, reasonCode, now)
		return block(t, reasonCode, detail, route)
	})
}

func block(t *Task, reason, detail, route string) error {
	if route == "" {
		route = RouteLead
	}
	if route != RouteLead && route != RouteOwner {
		return fmt.Errorf("block route %q: want lead or owner", route)
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("a block needs a reason code")
	}
	t.State, t.WaitReason = StateBlocked, ""
	t.BlockReason, t.BlockDetail, t.BlockRoute = strings.TrimSpace(reason), strings.TrimSpace(detail), route
	return nil
}

// Fail ends the holder's attempt as failed; the task goes back to pending,
// where the scheduler's router decides the retry.
func (s Store) Fail(id, holder, reason string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if err := requireLiveHolder(t, holder, now); err != nil {
			return err
		}
		endAttempt(t, OutcomeFailed, strings.TrimSpace(reason), now)
		t.State, t.Report = StatePending, nil
		return nil
	})
}

// Block stops a pending or in-progress task for a decision. On an
// in-progress task only the holder may block it.
func (s Store) Block(id, actor, reason, detail, route string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		switch t.State {
		case StateInProgress:
			if err := requireLiveHolder(t, actor, now); err != nil {
				return err
			}
			endAttempt(t, OutcomeBlocked, reason, now)
			t.Report = nil
		case StatePending:
		default:
			return fmt.Errorf("task %s is %s; only pending or in-progress tasks block", t.ID, t.State)
		}
		return block(t, reason, detail+byActor(actor), route)
	})
}

func byActor(actor string) string {
	if actor = strings.TrimSpace(actor); actor == "" {
		return ""
	}
	return " (by " + actor + ")"
}

// Release returns a leased task to the pool (the holder or the lead).
func (s Store) Release(id, actor, reason string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if t.State != StateInProgress || t.Lease == nil {
			return fmt.Errorf("task %s is %s with no lease", t.ID, t.State)
		}
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("release needs a reason")
		}
		endAttempt(t, OutcomeReleased, strings.TrimSpace(reason)+byActor(actor), now)
		t.State, t.Report = StatePending, nil
		return nil
	})
}

// Reset moves a blocked task back to pending (the lead), optionally on a
// new pool. A refused done report is discarded.
func (s Store) Reset(id string, pool *Pool, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if t.State != StateBlocked {
			return fmt.Errorf("task %s is %s; reset takes a blocked task", t.ID, t.State)
		}
		t.State, t.Report, t.BlockReason, t.BlockRoute, t.BlockDetail = StatePending, nil, "", "", ""
		if pool != nil {
			p := *pool
			t.Pool = &p
		}
		return nil
	})
}

// Set changes the priority or the pool of a task that is not terminal.
func (s Store) Set(id string, priority *int, pool *Pool, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if Terminal(t.State) {
			return fmt.Errorf("task %s is %s", t.ID, t.State)
		}
		if priority != nil {
			t.Priority = *priority
		}
		if pool != nil {
			p := *pool
			t.Pool = &p
		}
		return nil
	})
}

// Cancel closes a task that is not terminal (the lead).
func (s Store) Cancel(id, actor, reason string, now time.Time) (Task, error) {
	return s.mutate(id, now, func(t *Task, _ *tasks) error {
		if Terminal(t.State) {
			return fmt.Errorf("task %s is already %s", t.ID, t.State)
		}
		if strings.TrimSpace(reason) == "" {
			return fmt.Errorf("cancel needs a reason")
		}
		endAttempt(t, OutcomeCancelled, "", now)
		at := now
		t.State, t.ClosedAt, t.Report, t.WaitReason = StateCancelled, &at, nil, ""
		t.CancelReason = strings.TrimSpace(reason) + byActor(actor)
		return nil
	})
}

// SetWait records why a pending task waits; it writes only on a change.
func (s Store) SetWait(id, reason string, now time.Time) (bool, error) {
	changed := false
	err := s.update(now, func(all *tasks) error {
		t := all.get(id)
		if t == nil || t.State != StatePending || t.WaitReason == reason {
			return nil
		}
		t.WaitReason, changed = reason, true
		all.touch(t.ID)
		return nil
	})
	return changed, err
}

// ExpireDue ends every lease whose expiry is at or before now, except a
// lease whose done report waits for the commit step. An unclaimed lease ends
// as unclaimed, a claimed one as expired; the task goes back to pending.
// It returns the tasks it changed, after the change.
func (s Store) ExpireDue(now time.Time) ([]Task, error) {
	var out []Task
	err := s.update(now, func(all *tasks) error {
		for i := range all.list {
			t := &all.list[i]
			if t.State != StateInProgress || t.Lease == nil || t.Report != nil || now.Before(t.Lease.ExpiresAt) {
				continue
			}
			outcome := OutcomeExpired
			if t.Lease.ClaimedAt == nil {
				outcome = OutcomeUnclaimed
			}
			endAttempt(t, outcome, "lease expired at "+t.Lease.ExpiresAt.UTC().Format(time.RFC3339), now)
			t.State = StatePending
			all.touch(t.ID)
			out = append(out, *t)
		}
		return nil
	})
	return out, err
}
