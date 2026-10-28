// Package schedproc is the scheduler process of one squad (plan 4.5, 16.5,
// briefs lead ruling 15): `amq-squad scheduler run` constructs 2b-1's
// scheduler.Scheduler with the real hooks (the SeatLauncher of 2b-2, the AMQ
// message as the doorbell, the router, the harness commit step, the
// usage-limit detector of 2b-3) and ticks it until it is stopped. One process
// per squad: a second one for the same session root is refused. Restart
// recovery from the task store is phase 5 (16.4); a restarted process starts
// with a fresh incarnation, so a seat of the previous one is refused.
package schedproc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// DefaultTick is the scheduler's tick interval when none is given.
const DefaultTick = 2 * time.Second

// Flusher is a router that keeps per-tick state (2b-4's routing.Router writes
// a task's last outcome row on Flush); the process calls it once per tick.
type Flusher interface{ Flush() error }

// Options are the process's hooks and configuration.
type Options struct {
	Root        string // the AMQ session root
	Session     string // the squad's session (the launch record)
	ProjectRoot string // the squad's repository (the launch record)
	Launcher    scheduler.SeatLauncher
	Doorbell    scheduler.Doorbell
	Lead        scheduler.LeadNotifier
	Router      scheduler.Router // DefaultRouter until 2b-4 merges; one line to swap
	// Verifiers is the task store's verifier source (grants.TaskVerifier in
	// the verb); nil is strict: every mechanical class needs a verdict.
	Verifiers task.VerifierSource
	Committer scheduler.Committer
	Preparer  scheduler.SeatPreparer // nil: seats are dispatched to as they are
	Limits    scheduler.UsageLimitSource
	Exits     scheduler.ExitChecker // the exit check off the done path (R3); nil: none
	// Admitted runs once the process holds the squad's lock, before anything
	// else (the preflight baseline: a rejected duplicate process never
	// touches it, P1 of the milestone B first pass). An error stops the run.
	Admitted func() error
	// Decisions, when set, serves the lead's decisions over the scheduler's
	// own socket (R4); Root and Lead are filled in by Run.
	Decisions *DecideOptions
	Config    scheduler.Config
	Tick      time.Duration
	// Wake triggers an early tick (an AMQ message, an event file).
	Wake <-chan struct{}
	// Log receives one JSON line per decision; nil writes
	// <root>/extensions/amq-squad/scheduler/decisions.jsonl.
	Log io.Writer
	Now func() time.Time
	// OnTick is called after each tick with its decisions (tests, the R19
	// harness).
	OnTick func([]scheduler.Decision)
	// Started is called once the scheduler exists (its Post receives the
	// launcher's events).
	Started func(*scheduler.Scheduler)
}

// Dir is the process's directory under the session root.
func Dir(root string) string { return filepath.Join(root, "extensions", "amq-squad", "scheduler") }

// ErrRunning is returned when a scheduler process already runs for the root.
var ErrRunning = errors.New("a scheduler process already runs for this squad")

// Run ticks the squad's scheduler until ctx ends. It refuses to start when
// another process holds the squad's scheduler lock.
func Run(ctx context.Context, o Options) error {
	if o.Root == "" || !filepath.IsAbs(o.Root) {
		return fmt.Errorf("scheduler: the session root %q must be absolute", o.Root)
	}
	if err := os.MkdirAll(Dir(o.Root), 0o700); err != nil {
		return err
	}
	lock, acquired, err := flock.TryExclusive(filepath.Join(Dir(o.Root), "run.lock"), true)
	switch {
	case errors.Is(err, flock.ErrUnsupported):
	case err != nil:
		return err
	case !acquired:
		return ErrRunning
	default:
		defer lock.Close()
	}
	// The core's record of the admitted process: the lead whose AMQ
	// decisions it accepts (R1).
	// The decision socket opens only once the start-up barrier is passed
	// (the fourth pass): until then the record names none, and a decision
	// is refused scheduler_starting.
	rec := LaunchRecord{Lead: o.Config.Lead, Root: o.Root, Session: o.Session, ProjectRoot: o.ProjectRoot, PID: os.Getpid(), StartedAt: time.Now().UTC()}
	var srv *DecisionServer
	defer func() {
		if srv != nil {
			srv.Close()
		}
	}()
	if err := WriteLaunchRecord(o.Root, rec); err != nil {
		return err
	}
	if o.Admitted != nil {
		if err := o.Admitted(); err != nil {
			return err
		}
	}
	store, err := task.Open(o.Root)
	if err != nil {
		return err
	}
	store.Verifiers = o.Verifiers
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Tick <= 0 {
		o.Tick = DefaultTick
	}
	if o.Config.StatePath == "" {
		o.Config.StatePath = scheduler.StatePathFor(o.Root)
	}
	if o.Config.ExitsPath == "" {
		o.Config.ExitsPath = filepath.Join(Dir(o.Root), "exits.json")
	}
	if o.Config.SealRoot == "" {
		o.Config.SealRoot = o.Root // the exits ledger is sealed under the session seed
	}
	if o.Router == nil {
		o.Router = scheduler.DefaultRouter{}
	}
	if o.Log == nil {
		f, err := os.OpenFile(filepath.Join(Dir(o.Root), "decisions.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		o.Log = f
	}
	limits := o.Limits
	if limits != nil {
		// The window starts with this process: a limit the provider reported
		// before the start is history, not a stop.
		start := o.Now()
		limits = scheduler.UsageLimitFunc(func(since time.Time) ([]scheduler.UsageLimitEvent, error) {
			if since.Before(start) {
				since = start
			}
			return o.Limits.UsageLimitEvents(since)
		})
	}
	s, err := scheduler.New(scheduler.Options{Store: store, Now: o.Now, Launcher: o.Launcher, Doorbell: o.Doorbell, Router: o.Router,
		Committer: o.Committer, Lead: o.Lead, Preparer: o.Preparer, Limits: limits, Exits: o.Exits, Config: o.Config})
	if err != nil {
		return err
	}
	if p, ok := o.Preparer.(interface {
		SetLog(io.Writer, func() time.Time)
	}); ok {
		p.SetLog(o.Log, o.Now)
	}
	// N2: a restart rebuilds the seat map from the launch ledger before any
	// dispatch; a seat of the previous incarnation that is still alive is
	// closed through the launcher, its exit check after the confirmed close.
	// AdoptClosing loads the exits ledger first (N3 (a)).
	for _, ls := range LiveSeats(o.Root) {
		s.AdoptClosing(ls.Handle, ls.Pool, o.Now())
		logLine(o.Log, o.Now(), scheduler.Decision{Kind: "seat_adopted_closing", Seat: ls.Handle, Reason: "alive from a previous incarnation"})
	}
	if o.Started != nil {
		o.Started(s)
	}
	logLine(o.Log, o.Now(), scheduler.Decision{Kind: "scheduler_started", Reason: "incarnation " + s.Incarnation})
	timer := time.NewTicker(o.Tick)
	defer timer.Stop()
	for {
		drainEvents(o.Root, s, o.Log, o.Now)
		ds, err := s.Tick()
		if f, ok := o.Router.(Flusher); ok {
			if err := f.Flush(); err != nil {
				logLine(o.Log, o.Now(), scheduler.Decision{Kind: "routing_flush_failed", Reason: err.Error()})
			}
		}
		for _, d := range ds {
			logLine(o.Log, o.Now(), d)
		}
		if o.OnTick != nil {
			o.OnTick(ds)
		}
		if err != nil {
			// A tick error is logged and the next tick retries: the store is
			// transactional, so a failed tick left nothing half done.
			logLine(o.Log, o.Now(), scheduler.Decision{Kind: "tick_error", Reason: err.Error()})
		}
		// The start-up barrier (the fourth pass): the decision socket opens
		// after the exits ledger is loaded and every adopted seat's close
		// is confirmed.
		if o.Decisions != nil && srv == nil && s.Ready() {
			d := *o.Decisions
			d.Root, d.Lead = o.Root, o.Config.Lead
			opened, serr := ServeDecisions(d)
			if serr != nil {
				return fmt.Errorf("scheduler: the decision socket: %w", serr)
			}
			srv = opened
			rec.DecisionSocket = srv.Path()
			if werr := WriteLaunchRecord(o.Root, rec); werr != nil {
				return werr
			}
			logLine(o.Log, o.Now(), scheduler.Decision{Kind: "decision_socket_open", Reason: srv.Path()})
		}
		select {
		case <-ctx.Done():
			logLine(o.Log, o.Now(), scheduler.Decision{Kind: "scheduler_stopped"})
			return nil
		case <-timer.C:
		case <-o.Wake:
		}
	}
}

func logLine(w io.Writer, at time.Time, d scheduler.Decision) {
	b, _ := json.Marshal(struct {
		At string `json:"at"`
		scheduler.Decision
	}{at.UTC().Format(time.RFC3339Nano), d})
	fmt.Fprintln(w, string(b))
}

// Event is the file form of a scheduler event posted by an external runner
// (`amq-squad scheduler post`): the stand-in launcher path for seats AMQ
// cannot launch (pi before AMQ v0.116).
type Event struct {
	Kind      string    `json:"kind"` // seat_up | seat_gone | seat_activity
	Handle    string    `json:"handle"`
	Pool      task.Pool `json:"pool"`
	RequestID string    `json:"request_id,omitempty"`
	At        time.Time `json:"at"`
}

var eventKinds = map[string]scheduler.EventKind{"seat_up": scheduler.SeatUp, "seat_gone": scheduler.SeatGone, "seat_activity": scheduler.SeatActivity}

// EventsDir is where external runners drop events.
func EventsDir(root string) string { return filepath.Join(Dir(root), "events") }

// PostEvent writes one event file (a temp file renamed into place, so the
// process never reads half of it).
func PostEvent(root string, ev Event) error {
	if _, ok := eventKinds[ev.Kind]; !ok || ev.Handle == "" {
		return fmt.Errorf("scheduler event: kind %q (seat_up, seat_gone, seat_activity) and a handle are required", ev.Kind)
	}
	if ev.At.IsZero() {
		ev.At = time.Now().UTC()
	}
	dir := EventsDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(ev)
	f, err := os.CreateTemp(dir, ".event-*")
	if err != nil {
		return err
	}
	_, err = f.Write(raw)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(f.Name())
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, fmt.Sprintf("%d-%s.json", time.Now().UnixNano(), ev.Handle)))
}

// drainEvents posts every event file to the scheduler, oldest first, and
// removes it.
func drainEvents(root string, s *scheduler.Scheduler, log io.Writer, now func() time.Time) {
	entries, err := os.ReadDir(EventsDir(root))
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		p := filepath.Join(EventsDir(root), n)
		raw, err := os.ReadFile(p)
		os.Remove(p)
		var ev Event
		if err == nil {
			err = json.Unmarshal(raw, &ev)
		}
		if _, known := eventKinds[ev.Kind]; err != nil || !known {
			logLine(log, now(), scheduler.Decision{Kind: "event_unreadable", Reason: n})
			continue
		}
		s.Post(scheduler.Event{Kind: eventKinds[ev.Kind], At: ev.At, Handle: ev.Handle, Pool: ev.Pool, RequestID: ev.RequestID})
	}
}
