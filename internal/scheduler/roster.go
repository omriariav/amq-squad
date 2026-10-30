package scheduler

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The roster launch (3.0.1 t16, reviewer ruling 2026-10-30). AMQ v0.116.2
// refuses a fresh seat into a present binding (internal/launch/reconcile.go:
// a fresh seat is a write, line 1204; a write makes attachSafe false, line
// 374; a present binding without attachSafe is action_required
// binding_present_without_resumable_conversation, line 1358). So a squad
// whose seats start one launch each is single-seat. In roster mode the
// scheduler launches every seat of the roster in one AMQ apply, parks the
// seats that hold no lease, and dispatches to them; a seat that is gone is
// replaced only by relaunching the whole roster, the lead's decision.

// RosterSeat is one seat of the squad's roster: the typed seat list's seats
// that can start (a worktree of their own).
type RosterSeat struct {
	Handle string    `json:"handle"`
	Pool   task.Pool `json:"pool"`
	// Worktree is the seat's worktree in the current seat list: a live seat
	// is adopted at a restart only when its launch record names it.
	Worktree string `json:"worktree,omitempty"`
}

// RosterLauncher is a SeatLauncher that launches the whole roster in one
// AMQ prepare and one apply. LaunchRoster returns when the launch settled:
// each seat that proved its identity has posted its SeatUp with its request
// id; the outcome names the stage and, on a failure, AMQ's reason code and
// the failure detail.
type RosterLauncher interface {
	LaunchRoster(reqs []SeatRequest) RosterOutcome
}

// RosterApplied is the stage of a roster launch AMQ applied.
const RosterApplied = "applied"

// RosterOutcome is a roster launch's result.
type RosterOutcome struct {
	LaunchID   string
	Stage      string // applied | prepare_refused | decisions_pending | resource_present | apply_failed | launch_error
	ReasonCode string // AMQ's reason code (prepare or apply), or the pending actions' kinds
	Detail     string
	Seats      []RosterSeatState // after an applied launch: each seat's state
}

// RosterSeatState is one seat after an applied roster launch.
type RosterSeatState struct {
	Handle     string
	Up         bool
	ReasonCode string
	Detail     string
}

// RosterLaunchError is a roster launch that failed at prepare or apply (or
// before either): the scheduler logged roster_launch_failed and the process
// ends with it.
type RosterLaunchError struct {
	Stage      string
	ReasonCode string
	Detail     string
}

func (e *RosterLaunchError) Error() string {
	return fmt.Sprintf("the roster launch failed at stage %s, reason %s: %s", e.Stage, firstNonEmpty(e.ReasonCode, "none"), firstNonEmpty(e.Detail, "no detail"))
}

const (
	// ReasonSeatRelaunch blocks a task whose pool's roster seats are all
	// gone: a replacement is a relaunch of the roster (the lead's decision).
	ReasonSeatRelaunch = "seat_replacement_needs_relaunch"
	// AMQBindingPresent is the reason AMQ gives a fresh seat launched into
	// the session's present binding.
	AMQBindingPresent = "binding_present_without_resumable_conversation"
	// ReasonResourcePresent refuses a roster launch whose Prepare names a
	// tmux resource of a roster seat that still exists (a pane that outlived
	// its process): AMQ would refuse the Apply.
	ReasonResourcePresent = "seat_replacement_resource_present"
	// RelaunchVerb is the lead's decision that relaunches the roster.
	RelaunchVerb = "amq-squad scheduler relaunch"
	// closeRelaunch is the closing reason of a seat the relaunch closes.
	closeRelaunch = "roster_relaunch"
)

// PoolServes reports whether a roster seat of pool seat serves a task of
// pool want: the slot rule (a pi seat without a model serves any pi model;
// otherwise the model, and for Claude and Codex the effort, match).
func PoolServes(seat, want task.Pool) bool {
	if seat.Runtime != want.Runtime {
		return false
	}
	if seat.Runtime == "pi" {
		return seat.Model == "" || seat.Model == want.Model
	}
	return seat.Model == want.Model && seat.Effort == want.Effort
}

func (s *Scheduler) rosterMode() bool { return len(s.Config.Roster) > 0 }

// rosterFor is the roster seats that serve a pool.
func (s *Scheduler) rosterFor(p task.Pool) []RosterSeat {
	var out []RosterSeat
	for _, rs := range s.Config.Roster {
		if PoolServes(rs.Pool, p) {
			out = append(out, rs)
		}
	}
	return out
}

// AdoptUp takes a live roster seat of a previous incarnation as up (restart
// adopts): the caller proved it as a launch does (the launch ledger's
// accepted proof, pid and raw start stamp, matches the live process) and
// checked that its handle and pool are the roster's. Call before the first
// Tick.
func (s *Scheduler) AdoptUp(handle string, pool task.Pool, at time.Time) {
	s.AdoptUpRequest(handle, "", pool, at)
}

// AdoptUpRequest is AdoptUp naming the request the seat's accepted proof
// carries: the per-tick liveness check reads the launch ledger by it
// (3.0.1 t17).
func (s *Scheduler) AdoptUpRequest(handle, requestID string, pool task.Pool, at time.Time) {
	s.loadExits()
	if s.seats == nil {
		s.seats = map[string]*seat{}
	}
	if s.seats[handle] == nil {
		s.seats[handle] = &seat{pool: pool, freeSince: at, lastActivity: at, requestID: requestID}
	}
}

// RequestRelaunch is the lead's relaunch decision, posted from any
// goroutine (the decision socket); the next tick applies it.
func (s *Scheduler) RequestRelaunch(reason string) {
	s.mu.Lock()
	s.relaunchAsk = &reason
	s.mu.Unlock()
}

// holdOnce logs dispatch_held when its reason changes.
func (s *Scheduler) holdOnce(reason string, log func(Decision)) {
	if s.heldReason != reason {
		s.heldReason = reason
		log(Decision{Kind: "dispatch_held", Reason: reason})
	}
}

// rosterStep runs the roster's launch work of a tick, after the start-up
// barrier and before dispatch: the first tick's adoption verdict, the
// relaunch's drain and closes, and the owed roster launch. hold stops this
// tick's dispatch; an error is a *RosterLaunchError.
func (s *Scheduler) rosterStep(list []task.Task, now time.Time, cfg Config, log func(Decision)) (bool, error) {
	if !s.rosterMode() {
		return false, nil
	}
	if !s.rosterInit {
		s.rosterInit = true
		var live, missing []string
		for _, rs := range s.Config.Roster {
			if st := s.seats[rs.Handle]; st != nil && st.closing == "" {
				live = append(live, rs.Handle)
			} else {
				missing = append(missing, rs.Handle)
			}
		}
		switch {
		case len(live) == 0:
			s.rosterOwed = true
		case len(missing) == 0:
			log(Decision{Kind: "roster_adopted", Seat: strings.Join(live, ","), Reason: "every roster seat is live from the previous incarnation; no launch"})
		default:
			log(Decision{Kind: "roster_incomplete", Seat: strings.Join(missing, ","), Reason: fmt.Sprintf("adopted %s; no launch: AMQ refuses a fresh seat into the present binding (%s), so a task for a missing seat's pool blocks for the lead's relaunch (%s)",
				strings.Join(live, ","), AMQBindingPresent, RelaunchVerb)})
		}
	}
	if s.relaunching {
		if !s.drainForRelaunch(list, log) {
			return true, nil
		}
		s.relaunching, s.rosterOwed = false, true
	}
	if !s.rosterOwed {
		return false, nil
	}
	// N3: a roster seat whose exit check (or commit step) is pending keeps
	// its worktree; the launch waits for it, as a replacement always did.
	var pending []string
	for _, rs := range s.Config.Roster {
		if s.exitPending(rs.Handle) {
			pending = append(pending, rs.Handle)
		}
	}
	if len(pending) > 0 {
		s.holdOnce("roster launch: waiting for the pending exit check or commit step of "+strings.Join(pending, ", "), log)
		return true, nil
	}
	if err := s.launchRoster(now, log); err != nil {
		return true, err
	}
	s.rosterOwed = false
	// The seats that proved posted their SeatUp during the launch: they are
	// admitted in this tick.
	if err := s.applyEvents(now, cfg, log); err != nil {
		return true, err
	}
	return false, s.resetRelaunchBlocked(now, log)
}

// launchRoster issues one request id per roster seat and launches them in
// one apply.
func (s *Scheduler) launchRoster(now time.Time, log func(Decision)) error {
	rl, ok := s.Launcher.(RosterLauncher)
	if !ok {
		e := &RosterLaunchError{Stage: "launch_error", Detail: "the seat launcher cannot launch a roster"}
		log(Decision{Kind: "roster_launch_failed", Reason: e.Error()})
		return e
	}
	reqs := make([]SeatRequest, 0, len(s.Config.Roster))
	for _, rs := range s.Config.Roster {
		s.requested++
		req := SeatRequest{ID: fmt.Sprintf("%s%d", s.requestPrefix(), s.requested), Pool: rs.Pool, Handle: rs.Handle}
		reqs = append(reqs, req)
		s.requests = append(s.requests, request{req: req, at: now})
	}
	out := rl.LaunchRoster(reqs)
	if out.Stage != RosterApplied {
		e := &RosterLaunchError{Stage: firstNonEmpty(out.Stage, "launch_error"), ReasonCode: out.ReasonCode, Detail: redact.Block(out.Detail)}
		log(Decision{Kind: "roster_launch_failed", Reason: e.Error()})
		// Each seat's failure is a decision too, and the state is written
		// before the process ends (3.0.1 t17).
		s.settleRosterFailure(reqs, out, e, now, log)
		return e
	}
	log(Decision{Kind: "roster_launched", Reason: fmt.Sprintf("launch %s: %d seats in one apply", firstNonEmpty(out.LaunchID, "-"), len(reqs))})
	for _, st := range out.Seats {
		if !st.Up {
			log(Decision{Kind: "seat_not_up", Seat: st.Handle, Reason: redact.Block(strings.TrimSpace(firstNonEmpty(st.ReasonCode, "not_started") + ": " + st.Detail))})
		}
	}
	return nil
}

// resetRelaunchBlocked returns the tasks blocked for a relaunch to pending
// once the roster launched.
func (s *Scheduler) resetRelaunchBlocked(now time.Time, log func(Decision)) error {
	list, err := s.Store.List()
	if err != nil {
		return err
	}
	for _, t := range list {
		_, onFailure := s.launchBlocked[t.ID]
		if t.State != task.StateBlocked || (t.BlockReason != ReasonSeatRelaunch && !onFailure) {
			continue
		}
		// A task blocked on a repeated launch failure is pending again
		// too: the launch is the lead's retry (3.0.1 t17); if its pool's
		// seat failed again, dispatch blocks it again with the reason.
		if _, err := s.Store.Reset(t.ID, nil, now); err != nil {
			return err
		}
		log(Decision{Kind: "task_reset", TaskID: t.ID, Reason: "the roster was relaunched"})
	}
	return nil
}

// drainForRelaunch holds dispatch until no seat holds anything, then closes
// every seat and cancels every reservation; true once all are gone.
func (s *Scheduler) drainForRelaunch(list []task.Task, log func(Decision)) bool {
	busy := map[string]bool{}
	for _, t := range list {
		if t.Lease != nil {
			busy[t.Lease.Holder] = true
		}
	}
	for h := range s.seats {
		if task.HeldBy(h, list) != nil || s.commitPending(h) {
			busy[h] = true
		}
	}
	if len(busy) > 0 {
		s.holdOnce("roster relaunch: waiting for "+strings.Join(sortedKeys(busy), ", ")+" to finish what it holds", log)
		return false
	}
	handles := make([]string, 0, len(s.seats))
	for h := range s.seats {
		handles = append(handles, h)
	}
	sort.Strings(handles)
	for _, h := range handles {
		st := s.seats[h]
		if st.closing != "" {
			continue // a close already owed: closeIdle retries it
		}
		if s.Launcher == nil {
			delete(s.seats, h)
			log(Decision{Kind: "seat_closed", Seat: h, Reason: closeRelaunch})
			continue
		}
		st.closing = closeRelaunch
		if err := s.Launcher.CloseSeat(h, closeRelaunch); err != nil {
			log(Decision{Kind: "seat_close_failed", Seat: h, Reason: err.Error()})
			continue
		}
		if s.closeUnconfirmed(h) {
			st.awaitGone = true
			log(Decision{Kind: "seat_close_requested", Seat: h, Reason: closeRelaunch + "; waiting for its seat_gone"})
			continue
		}
		delete(s.seats, h)
		s.holdForExit(h)
		log(Decision{Kind: "seat_closed", Seat: h, Reason: closeRelaunch})
	}
	kept := s.requests[:0]
	for _, r := range s.requests {
		if s.Launcher != nil {
			if err := s.Launcher.CancelSeatRequest(r.req); err != nil {
				kept = append(kept, r)
				log(Decision{Kind: "seat_request_cancel_pending", TaskID: r.req.TaskID, Pool: r.req.Pool.Key(), Reason: err.Error()})
				continue
			}
		}
		if s.cancelled == nil {
			s.cancelled = map[string]bool{}
		}
		s.cancelled[r.req.ID] = true
		log(Decision{Kind: "seat_request_cancelled", Seat: r.req.Handle, Pool: r.req.Pool.Key(), Reason: closeRelaunch})
	}
	s.requests = kept
	if len(s.seats) > 0 || len(s.requests) > 0 {
		s.holdOnce("roster relaunch: waiting for every seat's close to be confirmed", log)
		return false
	}
	return true
}

// rosterNoIdleSeat decides a pending task of a pool with no idle roster
// seat: a pool no roster seat serves is refused as an on-demand request
// was; a reservation of the pool not up yet is waited for; a usage-limit
// window waits; a pool whose live seats are all busy waits seat_cap; a pool
// whose roster seats are all gone blocks for the lead's relaunch.
// A blocked task is returned (the dispatch pass replaces it in its list).
func (s *Scheduler) rosterNoIdleSeat(t task.Task, p task.Pool, now time.Time, log func(Decision)) (*task.Task, error) {
	serving := s.rosterFor(p)
	if len(serving) == 0 {
		log(Decision{Kind: "seat_request_failed", TaskID: t.ID, Pool: p.Key(), Reason: fmt.Sprintf("no free seat in the squad's seat list for pool %s (no roster seat serves it)", p.Key())})
		return nil, nil
	}
	live := false
	for _, rs := range serving {
		if st := s.seats[rs.Handle]; st != nil && st.closing == "" {
			live = true
		}
	}
	if !live {
		// No live seat of the pool and its launch failed twice with one
		// reason: the lead's block with that reason (3.0.1 t17).
		if got, err := s.blockOnLaunchFailure(t, p, now, log); err != nil || got != nil {
			return got, err
		}
	}
	for _, q := range s.requests {
		if PoolServes(q.req.Pool, p) {
			return nil, s.waitForSeat(t, p, now)
		}
	}
	if s.UsageLimited(now) {
		return nil, s.wait(t, task.WaitUsageLimit, now)
	}
	var gone []string
	for _, rs := range serving {
		if st := s.seats[rs.Handle]; st != nil && st.closing == "" {
			return nil, s.wait(t, task.WaitSeatCap, now) // a live seat of the pool is busy
		}
		gone = append(gone, rs.Handle)
	}
	log(Decision{Kind: ReasonSeatRelaunch, TaskID: t.ID, Seat: strings.Join(gone, ","), Pool: p.Key(), Reason: AMQBindingPresent})
	detail := fmt.Sprintf("every roster seat of pool %s is gone (%s); AMQ refuses a fresh seat into the session's present binding (%s) until it can join a fresh participant (ask 20); the lead's decision: %s (the remaining seats finish what they hold and close, then the roster relaunches in one apply)",
		p.Key(), strings.Join(gone, ", "), AMQBindingPresent, RelaunchVerb)
	if f := s.failureFor(p); f != nil {
		detail += fmt.Sprintf("; the pool's last launch failed at stage %s with reason %s: %s", f.Stage, f.Code(), firstNonEmpty(f.Detail, "no detail"))
	}
	got, err := s.Store.Block(t.ID, "", ReasonSeatRelaunch, detail, task.RouteLead, now)
	if err != nil {
		return nil, err
	}
	log(Decision{Kind: "blocked", TaskID: t.ID, Reason: ReasonSeatRelaunch})
	return &got, nil
}

// leases counts the tasks that hold a lease: in roster mode the seat cap
// bounds them (max_seats caps live leases, not live seats).
func leases(list []task.Task) int {
	n := 0
	for _, t := range list {
		if t.Lease != nil {
			n++
		}
	}
	return n
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
