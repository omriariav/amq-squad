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
	Lead             string        // the lead handle: widening is the lead's decision (2b-2); written to the state file
}

// EventKind orders events applied in one tick.
type EventKind int

const (
	SeatUp       EventKind = iota // a requested (or existing) seat joined with its pool
	SeatGone                      // the seat's session ended
	SeatActivity                  // the seat did something (renews its claimed lease)
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
}

// Decision is one action the scheduler took; the log is what the
// determinism test compares.
type Decision struct {
	Kind   string `json:"kind"`
	TaskID string `json:"task_id,omitempty"`
	Seat   string `json:"seat,omitempty"`
	Pool   string `json:"pool,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type seat struct {
	pool         task.Pool
	freeSince    time.Time
	lastActivity time.Time
	unresponsive bool   // held a lease it never claimed; no dispatch until it shows activity
	closing      string // a close the launcher could not do yet: counted, never dispatched, retried each tick
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
	Config    Config
	// Incarnation qualifies every request id (seat-req-<incarnation>-<n>) so
	// an arrival from before a restart never matches a new reservation. New
	// draws a fresh nonce; a literal Scheduler (tests) sets it or runs with
	// the empty incarnation.
	Incarnation string

	mu      sync.Mutex
	pending []Event

	seats      map[string]*seat
	requests   []request
	requested  int             // request ids issued
	cancelled  map[string]bool // request ids the launcher confirmed cancelled or terminated
	refused    []RefusedSeat   // the last refused arrivals, for status
	limitSince time.Time
	limitUntil time.Time
	limitOpen  bool // an event gave no reset time: stopped until restart
	polledTo   time.Time
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
		Committer: o.Committer, Lead: o.Lead, Preparer: o.Preparer, Limits: o.Limits, Config: o.Config, Incarnation: o.Incarnation}, nil
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

	if err := s.applyEvents(now, cfg, log); err != nil {
		return out, err
	}
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
	}
	list, err := s.Store.List()
	if err != nil {
		return out, err
	}
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
			s.seats[ev.Handle] = &seat{pool: ev.Pool, freeSince: ev.At, lastActivity: ev.At}
			log(Decision{Kind: "seat_up", Seat: ev.Handle, Pool: ev.Pool.Key()})
		case SeatGone:
			if s.seats[ev.Handle] != nil {
				delete(s.seats, ev.Handle)
				log(Decision{Kind: "seat_gone", Seat: ev.Handle})
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
		if _, err := s.Store.Complete(t.ID, now); err != nil {
			return err
		}
		log(Decision{Kind: "completed", TaskID: t.ID, Seat: holder})
		return nil
	}
	res := s.Committer.Commit(holder, t)
	if res.Deferred {
		log(Decision{Kind: "commit_deferred", TaskID: t.ID, Seat: holder, Reason: res.Detail})
		return nil
	}
	if res.OK {
		if _, err := s.Store.Complete(t.ID, now); err != nil {
			return err
		}
		log(Decision{Kind: "completed", TaskID: t.ID, Seat: holder})
		return nil
	}
	refused, err := s.Store.RefuseCommit(t.ID, res.ReasonCode, res.Detail, res.Route, now)
	if err != nil {
		return err
	}
	log(Decision{Kind: "commit_refused", TaskID: t.ID, Seat: holder, Reason: res.ReasonCode})
	if res.Quarantine {
		// No new lease for the seat until the owner clears it, and the seat
		// closes (retried each tick while the close fails).
		if err := s.Store.Quarantine(task.Quarantine{Handle: holder, Reason: res.ReasonCode, Detail: res.Detail, TaskID: t.ID, At: now}); err != nil {
			return err
		}
		log(Decision{Kind: "seat_quarantined", TaskID: t.ID, Seat: holder, Reason: res.ReasonCode})
		if st := s.seats[holder]; st != nil && st.closing == "" {
			st.closing = "quarantined"
		}
	}
	// The store record first, then the lead's message (a hint).
	if s.Lead != nil {
		if err := s.Lead.CommitRefused(refused); err != nil {
			log(Decision{Kind: "lead_notice_failed", TaskID: t.ID, Reason: err.Error()})
		}
	}
	return nil
}

func (s *Scheduler) router() Router {
	if s.Router == nil {
		return DefaultRouter{}
	}
	return s.Router
}

func (s *Scheduler) dispatch(list []task.Task, now time.Time, cfg Config, log func(Decision)) error {
	// A quarantined seat is never dispatched to and is closed (2b-2: the
	// store refuses it any lease until the owner clears it).
	qs, err := task.Quarantined(s.Store.Dir)
	if err != nil {
		return err
	}
	for _, q := range qs {
		if st := s.seats[q.Handle]; st != nil && st.closing == "" {
			st.closing = "quarantined"
			log(Decision{Kind: "seat_quarantined", Seat: q.Handle, Reason: q.Reason})
		}
	}
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
			if s.Preparer != nil {
				if err := s.Preparer.PrepareSeat(handle); err != nil {
					log(Decision{Kind: "seat_prepare_failed", TaskID: t.ID, Seat: handle, Reason: err.Error()})
					continue
				}
			}
			got, err := s.Store.Grant(t.ID, handle, r.Pool, cfg.LeaseDuration, now)
			if err != nil {
				return err
			}
			list = replace(list, got)
			log(Decision{Kind: "dispatched", TaskID: t.ID, Seat: handle, Pool: r.Pool.Key()})
			if s.Doorbell != nil {
				if err := s.Doorbell.Ring(handle, got); err != nil {
					log(Decision{Kind: "doorbell_failed", TaskID: t.ID, Seat: handle, Reason: err.Error()})
				}
			}
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
			if err := s.wait(t, task.WaitSeatStarting, now); err != nil {
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
			if err := s.wait(t, task.WaitSeatStarting, now); err != nil {
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
		if st.pool == p && st.closing == "" && !st.unresponsive && !lapsed[h] && task.HeldBy(h, list) == nil {
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
			// quarantined seat); the seat counts until it works.
			if s.Launcher == nil {
				delete(s.seats, h)
				log(Decision{Kind: "seat_closed", Seat: h, Reason: st.closing})
				continue
			}
			if err := s.Launcher.CloseSeat(h, st.closing); err != nil {
				log(Decision{Kind: "seat_close_failed", Seat: h, Reason: err.Error()})
				continue
			}
			delete(s.seats, h)
			log(Decision{Kind: "seat_closed", Seat: h, Reason: st.closing})
			continue
		}
		if task.HeldBy(h, list) != nil {
			st.freeSince = now
			continue
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
	st := State{UpdatedAt: now, Lead: cfg.Lead, MaxSeats: cfg.MaxSeats, LiveSeats: len(s.seats), RequestedSeats: len(s.requests), Seats: []SeatView{}, Refused: append([]RefusedSeat{}, s.refused...)}
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
