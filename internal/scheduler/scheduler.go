package scheduler

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/sealchain"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Defaults (proposals; brief 2b-1 ruling 3 and lead ruling 14, measured in
// docs/v3-spike/2b1-execution.md).
const (
	DefaultIdleTimeout      = 10 * time.Minute
	DefaultSeatStartTimeout = 5 * time.Minute
	DefaultMaxSeats         = 12 // plan 7.2: the session grant's default seat cap
)

// Config is the scheduler's grant-side configuration.
type Config struct {
	MaxSeats         int           // the grant's seat cap (squad.yaml can only narrow it)
	LeaseDuration    time.Duration // task lease length
	IdleTimeout      time.Duration // R18: an idle seat closes after this
	SeatStartTimeout time.Duration // a requested seat that never joins is forgotten
	StatePath        string        // optional: where the status-side view is written
	ExitsPath        string        // optional: the exits ledger, the pending exit checks (N3)
	// MaxCommitDeferrals is how many times in a row a commit step may defer
	// before the task blocks for the lead (default DefaultMaxCommitDeferrals);
	// the count lives in the step's exits entry.
	MaxCommitDeferrals int
	// SealRoot is the AMQ session root whose seed seals the exits ledger and
	// holds its head (the phase 5 re-pass ruling); "" leaves it unsealed.
	SealRoot string
	Lead     string // the lead handle: widening is the lead's decision (2b-2); written to the state file
	// Roster, when set, is roster mode (3.0.1 t16): every seat of it
	// launches in one AMQ apply, idle seats are parked and dispatched to,
	// MaxSeats caps live leases, and a gone seat is replaced only by the
	// lead's relaunch of the whole roster. Empty: seats start on demand.
	Roster []RosterSeat
	// FirstMessage, when set, finds a seat's first AMQ message sent at or
	// after since (brief 31-1, the launch budget's second mark): the tick
	// logs seat_first_message once per launched seat. nil: no such mark.
	FirstMessage func(handle string, since time.Time) (time.Time, bool)
}

// EventKind orders events applied in one tick.
type EventKind int

const (
	SeatUp       EventKind = iota // a requested (or existing) seat joined with its pool
	SeatGone                      // the seat's session ended
	SeatActivity                  // the seat did something (renews its claimed lease)
	// SeatLaunchFailed is a launch of the request's seat that failed or did
	// not resolve (3.0.1 t17): the launcher posts it once per request with
	// the stage, AMQ's reason code and the failure detail.
	SeatLaunchFailed
)

// Event is one input observed between ticks. Producers post them from any
// goroutine; the scheduler applies a tick's events sorted by (At, Kind,
// Handle), so arrival order never changes a decision.
type Event struct {
	Kind   EventKind
	At     time.Time
	Handle string
	Pool   task.Pool
	// RequestID is the SeatRequest.ID a launched seat carries back in its
	// SeatUp (the launcher puts it in the seat's launch record; R52: identity
	// comes from the record only). Arrival matches a reservation by this id,
	// never by pool. A SeatUp without one is unmanaged: refused and closed,
	// never dispatched; counted as closing while its closure is unconfirmed
	// (lead ruling; a recovery path, if one is ever needed, comes behind an
	// explicit flag in phase 5).
	RequestID string
	// LaunchID groups the requests of one launch attempt (SeatLaunchFailed):
	// a roster whose two seats of one pool fail in one apply is one failure
	// of that pool, not a repeat.
	LaunchID string
	// Stage, ReasonCode and Detail: a SeatLaunchFailed's failure (the
	// detail redacted), or a SeatGone's cause (process_exited and what the
	// liveness check saw).
	Stage      string
	ReasonCode string
	Detail     string
}

// Decision is one action the scheduler took; the log is what the
// determinism test compares.
type Decision struct {
	Kind   string `json:"kind"`
	TaskID string `json:"task_id,omitempty"`
	Seat   string `json:"seat,omitempty"`
	Pool   string `json:"pool,omitempty"`
	Reason string `json:"reason,omitempty"`
	// Stage and ReasonCode: a launch failure's stage and AMQ's reason code
	// (seat_launch_failed), or a gone seat's cause (3.0.1 t17).
	Stage      string `json:"stage,omitempty"`
	ReasonCode string `json:"reason_code,omitempty"`
}

type seat struct {
	pool         task.Pool
	requestID    string // the request it joined on: the identity the liveness check reads the launch ledger by
	freeSince    time.Time
	lastActivity time.Time
	unresponsive bool      // held a lease it never claimed; no dispatch until it shows activity
	closing      string    // a close the launcher could not do yet: counted, never dispatched, retried each tick
	awaitGone    bool      // closed, termination not yet confirmed: counted and never dispatched until SeatGone
	launchedAt   time.Time // when this incarnation requested the seat; zero for an adopted seat
	firstSent    bool      // seat_first_message is logged
}

type request struct {
	req SeatRequest
	at  time.Time
	// closing is set at the first failed cancel (2b-2 fix 3c): the launcher
	// could not confirm the request's seat gone. A closing request never
	// leaves closing on a SeatUp; it stays counted and is shown as closing.
	closing string
	since   time.Time
}

// Scheduler is one squad's deterministic scheduler (plan 4.5). Tick is the
// only method that decides; it is not safe to call Tick concurrently, Post
// is.
type Scheduler struct {
	Store     task.Store
	Now       func() time.Time
	Launcher  SeatLauncher     // nil: TEST MODE ONLY (expired reservations drop without confirmation); New refuses it
	Doorbell  Doorbell         // nil: no doorbell
	Router    Router           // nil: DefaultRouter
	Committer Committer        // nil: done reports complete at once
	Lead      LeadNotifier     // nil: no message to the lead on a refusal
	Preparer  SeatPreparer     // nil: a seat is dispatched to as it is
	Limits    UsageLimitSource // nil: no usage-limit events
	Exits     ExitChecker      // nil: no exit check off the done path (R3)
	Liveness  SeatLiveness     // nil: no per-tick process check (3.0.1 t17)
	Config    Config
	// Incarnation qualifies every request id (seat-req-<incarnation>-<n>) so
	// an arrival from before a restart never matches a new reservation. New
	// draws a fresh nonce; a literal Scheduler (tests) sets it or runs with
	// the empty incarnation.
	Incarnation string

	mu      sync.Mutex
	pending []Event
	// relaunchAsk is the lead's relaunch decision (its reason, the key
	// press and the reload) waiting for the next tick (RequestRelaunch,
	// RequestRelaunchReload); set under mu.
	relaunchAsk *relaunchAsk

	seats       map[string]*seat
	requests    []request
	requested   int             // request ids issued
	cancelled   map[string]bool // request ids the launcher confirmed cancelled or terminated
	refused     []RefusedSeat   // the last refused arrivals, for status
	limitSince  time.Time
	limitUntil  time.Time
	limitOpen   bool // an event gave no reset time: stopped until restart
	polledTo    time.Time
	exitSeen    map[string]bool // attempts whose exit check is queued or done
	exitQueue   []exitItem
	exitsLoaded bool
	// exitsErr is the exits ledger's last load or write failure: dispatch
	// stops (exits_ledger_unwritable) until a load and a write succeed.
	exitsErr error
	// toRelease are gone seats whose checks were decided: their slots are
	// released once nothing for them is queued and the ledger says so.
	toRelease map[string]bool
	// commitState is each running commit step's last reported state (v3-5-verifier).
	commitState map[string]string
	// commitDeferrals counts deferrals with no exits ledger (tests).
	commitDeferrals map[string]int
	// Roster mode (3.0.1 t16): rosterInit is the first tick's adoption
	// verdict done; rosterOwed a roster launch owed; relaunching the lead's
	// relaunch in progress; heldReason the last dispatch_held logged.
	rosterInit  bool
	rosterOwed  bool
	relaunching bool
	heldReason  string
	// reload is the relaunch's re-compiled squad.yaml, swapped in at the
	// drain's end (brief 31-2 PR 2); seatStart the key press of each seat a
	// reload added, carried by every roster launch's request for it until
	// the seat leaves the roster (Codex P2 2).
	reload    *RosterReload
	reloadAt  time.Time
	seatStart map[string]time.Time
	// unlaunched are the squad.yaml seats the last reload left out, over
	// the cap (written to the state for status and the screen).
	unlaunched []string
	// Launch failures (3.0.1 t17): the last failure per pool, the tasks
	// blocked on a repeated one (task id -> the pool and the failure's
	// sequence number), the requests whose failure was noted while a failed
	// roster launch settles, and the failures' sequence counter.
	launchFailures map[string]*LaunchFailure
	launchBlocked  map[string]launchBlock
	failedReqs     map[string]bool
	failSeq        int
	// livenessNote is the last unknown liveness answer logged per seat.
	livenessNote map[string]string
	// goneQueued are the gone attempts (handle#request) whose exit check
	// was queued: one check per attempt (3.0.1 t17).
	goneQueued map[string]bool
}

// Options configure a production scheduler (New).
type Options struct {
	Store       task.Store
	Now         func() time.Time
	Launcher    SeatLauncher
	Doorbell    Doorbell
	Router      Router
	Committer   Committer
	Lead        LeadNotifier
	Preparer    SeatPreparer
	Limits      UsageLimitSource
	Exits       ExitChecker
	Liveness    SeatLiveness
	Config      Config
	Incarnation string // default: a fresh random nonce
}

// New is the production constructor. A launcher is required: it is what
// confirms a cancelled reservation, so without one the cap could not hold.
// The scheduler gets a fresh incarnation unless one is given.
func New(o Options) (*Scheduler, error) {
	if o.Launcher == nil {
		return nil, errors.New("scheduler: a SeatLauncher is required (a nil launcher is a test mode)")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Incarnation == "" {
		var b [6]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, fmt.Errorf("scheduler incarnation: %w", err)
		}
		o.Incarnation = hex.EncodeToString(b[:])
	}
	if strings.ContainsAny(o.Incarnation, "-") {
		return nil, errors.New("scheduler: the incarnation may not contain '-'")
	}
	return &Scheduler{Store: o.Store, Now: o.Now, Launcher: o.Launcher, Doorbell: o.Doorbell, Router: o.Router,
		Committer: o.Committer, Lead: o.Lead, Preparer: o.Preparer, Limits: o.Limits, Exits: o.Exits, Liveness: o.Liveness, Config: o.Config, Incarnation: o.Incarnation}, nil
}

const requestIDPrefix = "seat-req-"

// RefusedStaleIncarnation is the refusal reason of a SeatUp whose request
// id belongs to a previous scheduler incarnation.
const RefusedStaleIncarnation = "request_stale_incarnation"

// requestPrefix is this incarnation's request id prefix.
func (s *Scheduler) requestPrefix() string {
	return requestIDPrefix + s.Incarnation + "-"
}

// Post queues an event for the next tick.
func (s *Scheduler) Post(ev Event) {
	s.mu.Lock()
	s.pending = append(s.pending, ev)
	s.mu.Unlock()
}

func (s *Scheduler) cfg() Config {
	c := s.Config
	if c.MaxSeats <= 0 {
		c.MaxSeats = DefaultMaxSeats
	}
	if c.LeaseDuration <= 0 {
		c.LeaseDuration = task.DefaultLeaseDuration
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	if c.SeatStartTimeout <= 0 {
		c.SeatStartTimeout = DefaultSeatStartTimeout
	}
	if c.MaxCommitDeferrals <= 0 {
		c.MaxCommitDeferrals = DefaultMaxCommitDeferrals
	}
	return c
}

// UsageLimited reports whether new seats are stopped at now.
func (s *Scheduler) UsageLimited(now time.Time) bool {
	return !s.limitSince.IsZero() && (s.limitOpen || now.Before(s.limitUntil))
}

// Tick runs one deterministic pass: apply the queued events, read the
// usage-limit source, expire leases, run the commit step on done reports,
// dispatch pending tasks to idle seats or request seats inside the grant,
// and close idle seats. It returns the decisions it took, in order.
func (s *Scheduler) Tick() ([]Decision, error) {
	cfg, now := s.cfg(), s.Now()
	if s.seats == nil {
		s.seats = map[string]*seat{}
	}
	var out []Decision
	log := func(d Decision) { out = append(out, d) }

	// The per-tick liveness of the roster's seats (3.0.1 t17): a seat whose
	// process is gone is posted SeatGone and applied in this tick.
	s.checkLiveness(now, log)
	if err := s.applyEvents(now, cfg, log); err != nil {
		return out, err
	}
	s.noteFirstMessages(cfg, log)
	if err := s.pollLimits(now, log); err != nil {
		return out, err
	}
	expired, err := s.Store.ExpireDue(now)
	if err != nil {
		return out, err
	}
	for _, t := range expired {
		a := t.Attempts[len(t.Attempts)-1]
		if st := s.seats[a.Holder]; st != nil && a.Outcome == task.OutcomeUnclaimed {
			st.unresponsive = true
		}
		log(Decision{Kind: "lease_expired", TaskID: t.ID, Seat: a.Holder, Reason: a.Outcome})
		// R3: a seat that claimed and ran past its lease may still be working
		// in its worktree; it is closed through the launcher and forgotten only
		// once its termination is confirmed, so CleanSeat and a new dispatch
		// never touch a live worktree.
		if st := s.seats[a.Holder]; st != nil && a.Outcome == task.OutcomeExpired && st.closing == "" {
			st.closing = closeExpired
			log(Decision{Kind: "seat_closing", TaskID: t.ID, Seat: a.Holder, Reason: closeExpired})
		}
	}
	list, err := s.Store.List()
	if err != nil {
		return out, err
	}
	// R3: the exit check on every terminal path that is not a done report.
	s.loadExits()
	if s.exitsLoaded {
		s.scanAttempts(list, now)
		if err := s.runExits(now, log); err != nil {
			return out, err
		}
	}
	// A commit step whose task no longer waits for it (cancelled) is
	// cancelled and its entry dropped once it ended (P2-1 of the second pass).
	s.sweepCommits(list, log)
	for _, t := range list {
		if t.State == task.StateInProgress && t.Report != nil {
			if err := s.commit(t, now, log); err != nil {
				return out, err
			}
		}
	}
	if list, err = s.Store.List(); err != nil {
		return out, err
	}
	// N3 (b): an exits ledger that cannot be read or written stops dispatch
	// until it can.
	if s.exitsErr != nil {
		if errors.Is(s.exitsErr, sealchain.ErrTampered) {
			// A failed tag or chain: a block routed to the owner, never a
			// silent pass (the ruling); dispatch stays stopped.
			log(Decision{Kind: "ledger_tampered", Reason: "owner: " + s.exitsErr.Error()})
		}
		log(Decision{Kind: "exits_ledger_unwritable", Reason: s.exitsErr.Error()})
		s.closeIdle(list, now, cfg, log)
		return out, s.writeState(now, cfg)
	}
	// N2: the seats a previous incarnation left running are closed and
	// confirmed gone before the first dispatch.
	if s.closeAdopted(log) {
		log(Decision{Kind: "dispatch_held", Reason: "a seat of the previous incarnation is not closed yet"})
		s.closeIdle(list, now, cfg, log)
		return out, s.writeState(now, cfg)
	}
	// Roster mode: the owed roster launch, or the lead's relaunch, before
	// any dispatch (3.0.1 t16).
	hold, err := s.rosterStep(list, now, cfg, log)
	// A relaunch's reload swapped the roster and the lease cap in this tick:
	// dispatch and the state written below read the new ones (Codex P2 3 on
	// 31-2 PR 2), so scheduler.json never names the old roster beside the
	// seats just launched.
	cfg = s.cfg()
	if err != nil {
		return out, err
	} else if hold {
		s.closeIdle(list, now, cfg, log)
		return out, s.writeState(now, cfg)
	}
	s.heldReason = ""
	if list, err = s.Store.List(); err != nil {
		return out, err
	}
	if err := s.dispatch(list, now, cfg, log); err != nil {
		return out, err
	}
	if list, err = s.Store.List(); err != nil {
		return out, err
	}
	s.closeIdle(list, now, cfg, log)
	return out, s.writeState(now, cfg)
}

func (s *Scheduler) applyEvents(now time.Time, cfg Config, log func(Decision)) error {
	s.mu.Lock()
	evs := s.pending
	s.pending = nil
	s.mu.Unlock()
	sort.SliceStable(evs, func(i, j int) bool {
		a, b := evs[i], evs[j]
		if !a.At.Equal(b.At) {
			return a.At.Before(b.At)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Handle < b.Handle
	})
	for _, ev := range evs {
		switch ev.Kind {
		case SeatUp:
			if s.seats[ev.Handle] != nil {
				continue
			}
			reason := ""
			requestedAt := s.requestAt(ev.RequestID)
			switch {
			case ev.RequestID != "" && s.requestClosing(ev.RequestID):
				// The request's cancel is pending: its seat is being closed,
				// never admitted; the close is retried (and the cancel too).
				reason = "request_closing"
			case ev.RequestID != "" && s.dropRequest(ev.RequestID):
				// The seat joined on its own reservation.
			case ev.RequestID != "" && s.cancelled[ev.RequestID]:
				reason = "request_cancelled"
			case strings.HasPrefix(ev.RequestID, requestIDPrefix) && !strings.HasPrefix(ev.RequestID, s.requestPrefix()):
				// A seat a previous scheduler incarnation requested: the stale
				// seat the id check exists to catch.
				reason = RefusedStaleIncarnation
			case ev.RequestID != "":
				reason = "request_unknown"
			default:
				reason = RefusedUnmanaged // no request id: a seat this scheduler did not launch
			}
			if reason != "" {
				// Refused and closed, never dispatched; counted as closing
				// while its closure is unconfirmed (a failed close is retried).
				log(Decision{Kind: "seat_refused", Seat: ev.Handle, Pool: ev.Pool.Key(), Reason: reason})
				s.refused = append(s.refused, RefusedSeat{Handle: ev.Handle, Pool: ev.Pool, Reason: reason, At: ev.At})
				if len(s.refused) > refusedKept {
					s.refused = s.refused[len(s.refused)-refusedKept:]
				}
				if s.Launcher != nil && reason == "request_closing" {
					if err := s.Launcher.CloseSeat(ev.Handle, reason); err != nil {
						log(Decision{Kind: "seat_close_failed", Seat: ev.Handle, Reason: err.Error()})
					}
					continue // the request keeps its place, counted as closing
				}
				if s.Launcher != nil {
					if err := s.Launcher.CloseSeat(ev.Handle, reason); err != nil {
						s.seats[ev.Handle] = &seat{pool: ev.Pool, freeSince: ev.At, lastActivity: ev.At, closing: reason}
						log(Decision{Kind: "seat_close_failed", Seat: ev.Handle, Reason: err.Error()})
					}
				}
				continue
			}
			s.seats[ev.Handle] = &seat{pool: ev.Pool, freeSince: ev.At, lastActivity: ev.At, requestID: ev.RequestID, launchedAt: requestedAt}
			log(Decision{Kind: "seat_up", Seat: ev.Handle, Pool: ev.Pool.Key()})
			s.clearLaunchFailures(ev.Pool)
		case SeatLaunchFailed:
			s.noteLaunchFailure(ev, log)
		case SeatGone:
			// The attempt the gone is for: the request it names, else the
			// live seat's (3.0.1 t17); the exit queue checks one attempt once.
			attempt := ev.RequestID
			if st := s.seats[ev.Handle]; st != nil && attempt == "" {
				attempt = st.requestID
			}
			if s.seats[ev.Handle] != nil {
				delete(s.seats, ev.Handle)
				delete(s.livenessNote, ev.Handle)
				log(Decision{Kind: "seat_gone", Seat: ev.Handle, ReasonCode: ev.ReasonCode, Reason: ev.Detail})
			}
			// R3, P1-9: the exit check at every gone seat, known to this
			// incarnation or not (after a restart the map starts empty), before
			// anything else reuses its handle; the slot is released only now
			// (P1-6).
			held := ""
			if list, err := s.Store.List(); err == nil {
				if t := task.HeldBy(ev.Handle, list); t != nil {
					held = t.ID
				}
			}
			if s.Exits == nil {
				if g, ok := s.Launcher.(SeatGoneObserver); ok {
					g.SeatGone(ev.Handle)
				}
			} else {
				// N3: the slot (and the seat's staged evidence) stays held until
				// this check is decided; runExits releases it.
				if !s.queueExit(exitItem{Handle: ev.Handle, TaskID: held, Why: "gone", Gone: true, At: ev.At, Attempt: attempt}) && attempt != "" {
					log(Decision{Kind: "exit_check_duplicate", Seat: ev.Handle, Reason: "the exit check of attempt " + attempt + " is already queued or done; this gone is dropped"})
				}
			}
		case SeatActivity:
			st := s.seats[ev.Handle]
			if st == nil {
				continue
			}
			if ev.At.After(st.lastActivity) {
				st.lastActivity = ev.At
			}
			st.unresponsive = false
			// Renewal on seat activity (review 1 ruling 3): the claimed lease
			// only; an unclaimed lease is never renewed.
			t, renewed, err := s.Store.Renew(ev.Handle, cfg.LeaseDuration, cfg.LeaseDuration/10, now)
			if err != nil && !errors.Is(err, task.ErrNoLease) {
				return err
			}
			if renewed {
				log(Decision{Kind: "lease_renewed", TaskID: t.ID, Seat: ev.Handle})
			}
		}
	}
	s.mu.Lock()
	ask := s.relaunchAsk
	s.relaunchAsk = nil
	s.mu.Unlock()
	if ask != nil {
		switch {
		case !s.rosterMode():
			log(Decision{Kind: "roster_relaunch_refused", Reason: "the scheduler runs no roster"})
		case !s.relaunching:
			s.relaunching = true
			s.reload, s.reloadAt = ask.reload, ask.start
			log(Decision{Kind: "roster_relaunch_requested", Reason: firstNonEmpty(ask.reason, "the lead's decision")})
		case ask.reload != nil:
			// a relaunch already draining: its swap takes the later
			// squad.yaml and key press (the swap is still to come)
			s.reload, s.reloadAt = ask.reload, ask.start
		}
	}
	return nil
}

// dropRequest releases the reservation with this request id; false when
// there is none.
func (s *Scheduler) requestClosing(id string) bool {
	for _, r := range s.requests {
		if r.req.ID == id {
			return r.closing != ""
		}
	}
	return false
}

func (s *Scheduler) dropRequest(id string) bool {
	for i, r := range s.requests {
		if r.req.ID == id {
			s.requests = append(s.requests[:i], s.requests[i+1:]...)
			return true
		}
	}
	return false
}

func (s *Scheduler) pollLimits(now time.Time, log func(Decision)) error {
	if s.Limits == nil {
		return nil
	}
	evs, err := s.Limits.UsageLimitEvents(s.polledTo)
	if err != nil {
		return fmt.Errorf("usage-limit source: %w", err)
	}
	s.polledTo = now
	for _, ev := range evs {
		if !s.UsageLimited(ev.At) {
			// The first event of a new window (plan 7.2: "after the first
			// usage-limit event in the window no new seat starts").
			s.limitSince, s.limitUntil, s.limitOpen = ev.At, time.Time{}, false
		}
		if ev.ResetAt.IsZero() {
			s.limitOpen = true
		} else if ev.ResetAt.After(s.limitUntil) {
			s.limitUntil = ev.ResetAt
		}
		log(Decision{Kind: "usage_limit", Seat: ev.Seat, Reason: ev.Provider})
	}
	return nil
}

func (s *Scheduler) commit(t task.Task, now time.Time, log func(Decision)) error {
	holder := t.Report.By
	if s.Committer == nil {
		got, err := s.Store.Complete(t.ID, now)
		if err != nil {
			return err
		}
		logAccepted(got, holder, log)
		return nil
	}
	s.noteAdopted(holder, t.ID, log)
	// The step's exits entry is written before the step starts, so a crash
	// in between leaves no step unaccounted.
	s.commitEntry(holder, t.ID, now)
	res := s.Committer.Commit(holder, t)
	if res.Running {
		s.commitRunning(holder, t.ID, res.Detail, now, log)
		return nil
	}
	if res.Deferred {
		n := s.commitDeferred(t.ID)
		log(Decision{Kind: "commit_deferred", TaskID: t.ID, Seat: holder, Reason: res.Detail})
		if n < s.cfg().MaxCommitDeferrals {
			return nil
		}
		// The limit (P2-4 of the second pass): the count lives in the step's
		// exits entry, so it survives a restart; at the limit the task blocks
		// for the lead, and the lead's reset applies.
		code := ReasonCommitFailed
		if res.ReasonCode == ReasonVerifierInterrupted {
			code = ReasonVerifierInterrupted
		}
		res = CommitResult{ReasonCode: code, Route: task.RouteLead, Detail: fmt.Sprintf("the commit step deferred %d times in a row, counted across restarts; last: %s", n, res.Detail)}
		// A decided result like any other: retained until applied, so a failed
		// application never starts another step (the third round's item 5).
		if d, ok := s.Committer.(CommitDecider); ok {
			d.Decide(t.ID, res)
		}
	}
	// The result is applied in the store first; the step and its entry are
	// forgotten only after the transaction committed (P2-3 of the second
	// pass): a failed application is retried at the next tick, never the step.
	if err := s.applyCommit(holder, t, res, now, log); err != nil {
		return err
	}
	if a, ok := s.Committer.(CommitApplier); ok {
		a.Applied(t.ID)
	}
	s.commitEnded(t.ID)
	return nil
}

// applyCommit records a decided commit step's result in the store.
func (s *Scheduler) applyCommit(holder string, t task.Task, res CommitResult, now time.Time, log func(Decision)) error {
	if res.OK {
		// The acceptance and the binding the merge train reads (3-2) are one
		// store transaction (P2 of the milestone B first pass); a PR number
		// another task holds refuses for the lead.
		got, err := s.Store.CompleteBound(t.ID, res.PRRepository, res.PRNumber, res.PRHead, now)
		if errors.Is(err, task.ErrPRBoundElsewhere) {
			res = CommitResult{ReasonCode: task.RefusalPRBoundElsewhere, Detail: err.Error(), Route: task.RouteLead}
		} else if err != nil {
			return err
		} else if got.State == task.StateBlocked {
			logAccepted(got, holder, log)
			return nil
		} else {
			log(Decision{Kind: "completed", TaskID: t.ID, Seat: holder})
			if res.PRNumber > 0 {
				log(Decision{Kind: "pr_bound", TaskID: t.ID, Seat: holder, Reason: fmt.Sprintf("%s pr %d head %s", res.PRRepository, res.PRNumber, res.PRHead)})
			}
			return nil
		}
	}
	refused, err := s.Store.RefuseCommitPaths(t.ID, res.ReasonCode, res.Detail, res.Route, res.RefusedPaths, now)
	if err != nil {
		return err
	}
	log(Decision{Kind: "commit_refused", TaskID: t.ID, Seat: holder, Reason: res.ReasonCode})
	// The store record first, then the lead's message (a hint).
	if s.Lead != nil {
		if err := s.Lead.CommitRefused(refused); err != nil {
			log(Decision{Kind: "lead_notice_failed", TaskID: t.ID, Reason: err.Error()})
		}
	}
	return nil
}

// logAccepted logs an accepted done report: completed, or blocked by the
// store's completion gate (a mechanical verifier with no pass verdict).
func logAccepted(got task.Task, holder string, log func(Decision)) {
	if got.State == task.StateBlocked {
		log(Decision{Kind: "blocked", TaskID: got.ID, Seat: holder, Reason: got.BlockReason})
		return
	}
	log(Decision{Kind: "completed", TaskID: got.ID, Seat: holder})
}

func (s *Scheduler) router() Router {
	if s.Router == nil {
		return DefaultRouter{}
	}
	return s.Router
}

func (s *Scheduler) dispatch(list []task.Task, now time.Time, cfg Config, log func(Decision)) error {
	// A request that never joined is released only after the launcher
	// confirms it is cancelled or terminated; until then it keeps its place
	// under the seat cap, so a late join can never exceed the cap.
	kept := s.requests[:0]
	for _, r := range s.requests {
		switch {
		case now.Sub(r.at) < cfg.SeatStartTimeout:
			kept = append(kept, r)
		case s.Launcher == nil:
			log(Decision{Kind: "seat_request_expired", TaskID: r.req.TaskID, Pool: r.req.Pool.Key()})
		default:
			if err := s.Launcher.CancelSeatRequest(r.req); err != nil {
				if r.closing == "" {
					r.since = now
				}
				r.closing = err.Error()
				kept = append(kept, r)
				log(Decision{Kind: "seat_request_cancel_pending", TaskID: r.req.TaskID, Pool: r.req.Pool.Key(), Reason: err.Error()})
				continue
			}
			if s.cancelled == nil {
				s.cancelled = map[string]bool{}
			}
			s.cancelled[r.req.ID] = true // a later SeatUp with this id is closed, not counted
			log(Decision{Kind: "seat_request_cancelled", TaskID: r.req.TaskID, Pool: r.req.Pool.Key()})
		}
	}
	s.requests = kept
	order := append([]task.Task(nil), list...)
	task.DispatchOrder(order)
	waitingFor := map[task.Pool]int{} // tasks this tick that wait on a requested seat
	for _, c := range order {
		if c.State != task.StatePending {
			continue
		}
		t := current(list, c.ID)
		if why := task.Blocker(t, list); why != "" {
			if err := s.wait(t, why, now); err != nil {
				return err
			}
			continue
		}
		r := s.router().Route(t)
		if r.Block != "" {
			got, err := s.Store.Block(t.ID, "", r.Block, r.Detail, task.RouteLead, now)
			if err != nil {
				return err
			}
			list = replace(list, got) // a blocked task keeps its file lock
			log(Decision{Kind: "blocked", TaskID: t.ID, Reason: r.Block})
			continue
		}
		if r.Pool == nil {
			if err := s.wait(t, firstNonEmpty(r.Wait, task.WaitNoRoute), now); err != nil {
				return err
			}
			continue
		}
		if handle := s.idleSeat(list, t, *r.Pool); handle != "" {
			// Roster mode: the seat cap bounds live leases (3.0.1 t16).
			if s.rosterMode() && leases(list) >= cfg.MaxSeats {
				if err := s.wait(t, task.WaitSeatCap, now); err != nil {
					return err
				}
				continue
			}
			if s.Preparer != nil {
				if err := s.Preparer.PrepareSeat(handle); err != nil {
					log(Decision{Kind: "seat_prepare_failed", TaskID: t.ID, Seat: handle, Reason: err.Error()})
					continue
				}
			}
			got, err := s.Store.Grant(t.ID, handle, r.Pool, cfg.LeaseDuration, now)
			var unverified *task.VerifierUnconfiguredError
			if errors.As(err, &unverified) {
				// The claim path's refusal (v3-5-verifier rule 4): the grant
				// changed since the start; the task blocks for the lead.
				got, err := s.Store.Block(t.ID, "", task.ReasonGrantsInvalid, err.Error(), task.RouteLead, now)
				if err != nil {
					return err
				}
				list = replace(list, got)
				log(Decision{Kind: "blocked", TaskID: t.ID, Reason: task.ReasonGrantsInvalid})
				continue
			}
			if err != nil {
				return err
			}
			list = replace(list, got)
			delete(s.launchBlocked, t.ID)
			log(Decision{Kind: "dispatched", TaskID: t.ID, Seat: handle, Pool: r.Pool.Key()})
			if s.Doorbell != nil {
				if err := s.Doorbell.Ring(handle, got); err != nil {
					var unprepared *LeaseUnpreparedError
					if !errors.As(err, &unprepared) {
						log(Decision{Kind: "doorbell_failed", TaskID: t.ID, Seat: handle, Reason: err.Error()})
						continue
					}
					// 3.0.2 t23: the seat was not rung; the lease goes back
					// and the task blocks for the lead, in one transaction.
					blocked, berr := s.Store.Block(t.ID, handle, ReasonTaskBranchUnprepared, unprepared.Err.Error(), task.RouteLead, now)
					if berr != nil {
						return berr
					}
					list = replace(list, blocked)
					log(Decision{Kind: "blocked", TaskID: t.ID, Seat: handle, Reason: ReasonTaskBranchUnprepared})
				}
			}
			continue
		}
		if s.rosterMode() {
			// No seat is requested in roster mode: the roster is up.
			got, err := s.rosterNoIdleSeat(t, *r.Pool, now, log)
			if err != nil {
				return err
			}
			if got != nil {
				list = replace(list, *got)
			}
			continue
		}
		// A repeated launch failure of the pool blocks the task for the lead
		// with the failure's reason; no seat is requested again (3.0.1 t17).
		if got, err := s.blockOnLaunchFailure(t, *r.Pool, now, log); err != nil {
			return err
		} else if got != nil {
			list = replace(list, *got)
			continue
		}
		requested := 0
		for _, q := range s.requests {
			if q.req.Pool == *r.Pool {
				requested++
			}
		}
		if waitingFor[*r.Pool] < requested {
			waitingFor[*r.Pool]++
			if err := s.waitForSeat(t, *r.Pool, now); err != nil {
				return err
			}
			continue
		}
		switch {
		case s.UsageLimited(now):
			if err := s.wait(t, task.WaitUsageLimit, now); err != nil {
				return err
			}
		case len(s.seats)+len(s.requests) >= cfg.MaxSeats:
			if err := s.wait(t, task.WaitSeatCap, now); err != nil {
				return err
			}
		default:
			s.requested++
			req := SeatRequest{ID: fmt.Sprintf("%s%d", s.requestPrefix(), s.requested), Pool: *r.Pool, TaskID: t.ID}
			if s.Launcher != nil {
				if err := s.Launcher.RequestSeat(req); err != nil {
					log(Decision{Kind: "seat_request_failed", TaskID: t.ID, Pool: r.Pool.Key(), Reason: err.Error()})
					continue
				}
			}
			s.requests = append(s.requests, request{req: req, at: now})
			waitingFor[*r.Pool]++
			log(Decision{Kind: "seat_requested", TaskID: t.ID, Pool: r.Pool.Key()})
			if err := s.waitForSeat(t, *r.Pool, now); err != nil {
				return err
			}
		}
	}
	return nil
}

// idleSeat is the first seat (by handle) of the pool that holds nothing,
// answers, and did not let an earlier attempt of this task lapse.
func (s *Scheduler) idleSeat(list []task.Task, t task.Task, p task.Pool) string {
	lapsed := map[string]bool{}
	for _, a := range t.Attempts {
		if a.Outcome == task.OutcomeUnclaimed || a.Outcome == task.OutcomeExpired {
			lapsed[a.Holder] = true
		}
	}
	var handles []string
	for h, st := range s.seats {
		serves := st.pool == p
		if s.rosterMode() {
			serves = PoolServes(st.pool, p)
		}
		if serves && st.closing == "" && !st.unresponsive && !lapsed[h] && task.HeldBy(h, list) == nil && !s.exitPending(h) {
			handles = append(handles, h)
		}
	}
	sort.Strings(handles)
	if len(handles) == 0 {
		return ""
	}
	return handles[0]
}

func (s *Scheduler) wait(t task.Task, reason string, now time.Time) error {
	if t.WaitReason == reason {
		return nil
	}
	_, err := s.Store.SetWait(t.ID, reason, now)
	return err
}

func (s *Scheduler) closeIdle(list []task.Task, now time.Time, cfg Config, log func(Decision)) {
	var handles []string
	for h := range s.seats {
		handles = append(handles, h)
	}
	sort.Strings(handles)
	for _, h := range handles {
		st := s.seats[h]
		if st.closing != "" {
			// Retry the close that failed (or the first close of a
			// seat refused at its exit); the seat counts until it works.
			if st.awaitGone {
				continue // closed; counted until its SeatGone confirms it
			}
			if s.Launcher == nil {
				delete(s.seats, h)
				log(Decision{Kind: "seat_closed", Seat: h, Reason: st.closing})
				continue
			}
			if err := s.Launcher.CloseSeat(h, st.closing); err != nil {
				log(Decision{Kind: "seat_close_failed", Seat: h, Reason: err.Error()})
				continue
			}
			if (st.closing == closeExpired || st.closing == closeRestart || st.closing == closeRelaunch) && s.closeUnconfirmed(h) {
				st.awaitGone = true
				log(Decision{Kind: "seat_close_requested", Seat: h, Reason: st.closing + "; waiting for its seat_gone"})
				continue
			}
			delete(s.seats, h)
			s.holdForExit(h)
			log(Decision{Kind: "seat_closed", Seat: h, Reason: st.closing})
			continue
		}
		if task.HeldBy(h, list) != nil {
			st.freeSince = now
			continue
		}
		if s.rosterMode() && !st.unresponsive {
			continue // a parked roster seat stays up (3.0.1 t16)
		}
		idle := st.freeSince
		if st.lastActivity.After(idle) {
			idle = st.lastActivity
		}
		if now.Sub(idle) < cfg.IdleTimeout {
			continue
		}
		if s.Launcher != nil {
			if err := s.Launcher.CloseSeat(h, "idle"); err != nil {
				st.closing = "idle"
				log(Decision{Kind: "seat_close_failed", Seat: h, Reason: err.Error()})
				continue
			}
		}
		delete(s.seats, h)
		log(Decision{Kind: "seat_closed", Seat: h, Reason: "idle"})
	}
}

// State is the scheduler-side view that status reads.
type State struct {
	UpdatedAt       time.Time  `json:"updated_at"`
	Lead            string     `json:"lead,omitempty"`
	MaxSeats        int        `json:"max_seats"`
	LiveSeats       int        `json:"live_seats"`
	RequestedSeats  int        `json:"requested_seats"`
	UsageLimitSince *time.Time `json:"usage_limit_since"`
	UsageLimitUntil *time.Time `json:"usage_limit_until"`
	Seats           []SeatView `json:"seats"`
	// Refused are the last refused seat arrivals (request_missing: no request
	// id, an unmanaged seat; request_cancelled; request_unknown).
	Refused []RefusedSeat `json:"refused"`
	// Roster mode (3.0.1 t16): the roster's handles, and whether the lead's
	// relaunch is in progress.
	Roster          []string `json:"roster,omitempty"`
	RelaunchPending bool     `json:"relaunch_pending,omitempty"`
	// Unlaunched are squad.yaml's seats beyond max_seats that the last
	// reload did not launch (brief 31-2 PR 2), in squad.yaml order.
	Unlaunched []string `json:"unlaunched,omitempty"`
	// LaunchFailures are the last launch failure of each pool whose seat
	// has not come up since (3.0.1 t17): status shows the reason on the
	// seat's row in place of seat_starting.
	LaunchFailures []LaunchFailure `json:"launch_failures,omitempty"`
	// LaunchBlocked are the tasks blocked on a repeated launch failure:
	// restored at a restart with LaunchFailures (RestoreLaunchFailures).
	LaunchBlocked []LaunchBlockedTask `json:"launch_blocked,omitempty"`
}

// RefusedUnmanaged is the refusal reason of a SeatUp without a request id.
const RefusedUnmanaged = "request_missing"

const refusedKept = 20

// RefusedSeat is one refused seat arrival.
type RefusedSeat struct {
	Handle string    `json:"handle"`
	Pool   task.Pool `json:"pool"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

// SeatView is one known seat.
type SeatView struct {
	Handle       string    `json:"handle"`
	Pool         task.Pool `json:"pool"`
	Unresponsive bool      `json:"unresponsive"`
	Closing      string    `json:"closing,omitempty"`    // a failed close being retried; still counted
	RequestID    string    `json:"request_id,omitempty"` // a request whose cancel is pending (its seat not confirmed gone)
}

// Occupancy is the scheduler's seat view, what a router's concurrency cap
// counts (phase 5 re-pass, ruling 2): every seat it knows, each once: live
// seats, closing ones (a failed close, or a close whose termination is not
// confirmed yet, as after a lease expiry) and every pending seat request.
// Tick calls the router, so the view is read on the tick's goroutine.
func (s *Scheduler) Occupancy() []SeatView {
	out := []SeatView{}
	for h, v := range s.seats {
		closing := v.closing
		if closing == "" && v.awaitGone {
			closing = "awaiting seat_gone"
		}
		if s.rosterMode() && closing == "" {
			// A roster seat occupies a concurrency pool only while it holds a
			// lease, which the router counts from the task (3.0.1 t16).
			continue
		}
		out = append(out, SeatView{Handle: h, Pool: v.pool, Unresponsive: v.unresponsive, Closing: closing})
	}
	for _, r := range s.requests {
		out = append(out, SeatView{Handle: "request " + r.req.ID, RequestID: r.req.ID, Pool: r.req.Pool, Closing: r.closing})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Handle < out[j].Handle })
	return out
}

// StatePathFor is the scheduler state file under an AMQ session root.
func StatePathFor(root string) string {
	return filepath.Join(root, "extensions", "amq-squad", "scheduler.json")
}

// ReadState reads a state file; a missing file is a zero State.
func ReadState(path string) (State, error) {
	var st State
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err == nil {
		err = json.Unmarshal(raw, &st)
	}
	return st, err
}

func (s *Scheduler) writeState(now time.Time, cfg Config) error {
	if cfg.StatePath == "" {
		return nil
	}
	st := State{UpdatedAt: now, Lead: cfg.Lead, MaxSeats: cfg.MaxSeats, LiveSeats: len(s.seats), RequestedSeats: len(s.requests), Seats: []SeatView{}, Refused: append([]RefusedSeat{}, s.refused...), RelaunchPending: s.relaunching}
	st.Unlaunched = append([]string(nil), s.unlaunched...)
	for _, rs := range cfg.Roster {
		st.Roster = append(st.Roster, rs.Handle)
	}
	if !s.limitSince.IsZero() {
		since := s.limitSince
		st.UsageLimitSince = &since
		if !s.limitOpen {
			until := s.limitUntil
			st.UsageLimitUntil = &until
		}
	}
	for h, v := range s.seats {
		st.Seats = append(st.Seats, SeatView{Handle: h, Pool: v.pool, Unresponsive: v.unresponsive, Closing: v.closing})
	}
	for _, r := range s.requests {
		if r.closing != "" {
			st.Seats = append(st.Seats, SeatView{Handle: "request " + r.req.ID, RequestID: r.req.ID, Pool: r.req.Pool, Closing: r.closing})
		}
	}
	sort.Slice(st.Seats, func(i, j int) bool { return st.Seats[i].Handle < st.Seats[j].Handle })
	st.LaunchFailures = s.launchFailureList()
	st.LaunchBlocked = s.launchBlockedList()
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.StatePath), 0o700); err != nil {
		return err
	}
	tmp := cfg.StatePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, cfg.StatePath)
}

func current(list []task.Task, id string) task.Task {
	for _, t := range list {
		if t.ID == id {
			return t
		}
	}
	return task.Task{}
}

func replace(list []task.Task, t task.Task) []task.Task {
	for i := range list {
		if list[i].ID == t.ID {
			list[i] = t
		}
	}
	return list
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
