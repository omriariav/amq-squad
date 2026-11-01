package schedproc

import (
	"fmt"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The roster launch (3.0.1 t16): the scheduler launches every seat of the
// typed seat list in one AMQ apply and dispatches to the parked seats; the
// slots govern dispatch, not launch.

// SeatPool is the pool a seat of the seat list serves as a roster seat.
func SeatPool(seat seatlaunch.Seat) task.Pool {
	return task.Pool{Runtime: seat.Runtime, Model: seat.Model, Effort: seat.Effort}
}

// Roster is the squad's roster: every seat of the seat list but the lead's
// own (the roster's tmux session holds seats only: a lead inside it keeps
// the binding present, and every relaunch is refused), which is named in
// excluded with the reason. squad.yaml derives every seat's worktree (brief
// 31-1), so no seat is left out for its worktree.
func (s *Slots) Roster(lead string) (roster []scheduler.RosterSeat, excluded []string) {
	for _, seat := range s.Squad.Seats {
		if lead != "" && seat.Handle == lead {
			excluded = append(excluded, fmt.Sprintf("seat %s is the lead (squad.yaml's lead); the lead runs outside the roster's tmux session", seat.Handle))
			continue
		}
		roster = append(roster, scheduler.RosterSeat{Handle: seat.Handle, Pool: SeatPool(seat), Worktree: cleanPath(seat.Worktree)})
	}
	return roster, excluded
}

// PlanRoster is the squad to launch for a roster launch: each request's
// seat by handle, its worktree ensured, its request id set, in the order of
// the requests (the seatlaunch.Launcher's PlanRoster).
func (s *Slots) PlanRoster(reqs []scheduler.SeatRequest) (seatlaunch.Squad, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.inUse == nil {
		s.inUse, s.byReq = map[string]string{}, map[string]string{}
	}
	q := s.Squad
	q.Seats = nil
	for _, r := range reqs {
		var found *seatlaunch.Seat
		for i := range s.Squad.Seats {
			if s.Squad.Seats[i].Handle == r.Handle {
				found = &s.Squad.Seats[i]
			}
		}
		if found == nil {
			return seatlaunch.Squad{}, fmt.Errorf("the roster names %s, which is not in the squad's seat list", r.Handle)
		}
		if !scheduler.PoolServes(SeatPool(*found), r.Pool) {
			return seatlaunch.Squad{}, fmt.Errorf("seat %s does not serve pool %s", r.Handle, r.Pool.Key())
		}
		seat, err := s.ensureWorktree(*found)
		if err != nil {
			return seatlaunch.Squad{}, err
		}
		seat.RequestID = r.ID
		q.Seats = append(q.Seats, seat)
	}
	for _, seat := range q.Seats {
		s.inUse[seat.Handle], s.byReq[seat.RequestID] = seat.RequestID, seat.Handle
	}
	return q, nil
}

// checkWorktree is ensureWorktree's refusal without creating anything.
func (s *Slots) checkWorktree(seat seatlaunch.Seat) (seatlaunch.Seat, error) {
	root := cleanPath(s.Squad.ProjectRoot)
	if seat.Worktree == "" || cleanPath(seat.Worktree) == root {
		return seat, fmt.Errorf("seat %s has no worktree of its own (the repository root is never a seat worktree: the commit step moves the seat branch)", seat.Handle)
	}
	return seat, nil
}

// LaunchRoster sends the roster to the one launcher that runs every seat of
// it in one apply (in production the AMQ launcher, for every runtime). A
// roster whose runtimes go to different launchers cannot share one apply
// and is refused.
func (l *Launcher) LaunchRoster(reqs []scheduler.SeatRequest) scheduler.RosterOutcome {
	var inner scheduler.SeatLauncher
	for _, r := range reqs {
		got := l.ByRuntime[r.Pool.Runtime]
		if got == nil {
			return scheduler.RosterOutcome{Stage: "launch_error", Detail: fmt.Sprintf("no launcher for runtime %s (seat %s)", r.Pool.Runtime, r.Handle)}
		}
		if inner != nil && got != inner {
			return scheduler.RosterOutcome{Stage: "launch_error", Detail: "the roster's runtimes go to different launchers, which cannot share one apply"}
		}
		inner = got
	}
	rl, ok := inner.(scheduler.RosterLauncher)
	if !ok {
		return scheduler.RosterOutcome{Stage: "launch_error", Detail: "the seat launcher cannot launch a roster"}
	}
	l.mu.Lock()
	if l.reqRun == nil {
		l.reqRun, l.seatRT = map[string]string{}, map[string]string{}
	}
	for _, r := range reqs {
		l.reqRun[r.ID] = r.Pool.Runtime
	}
	l.mu.Unlock()
	return rl.LaunchRoster(reqs)
}

var _ scheduler.RosterLauncher = (*Launcher)(nil)

// adoptLive adopts the live seats a previous incarnation left (the launch
// ledger's accepted proof: pid and raw start stamp match the live process,
// the identity a launch's close uses). Without a roster each is closed, as
// before. With one, a seat of the roster whose launch record names the
// roster's pool is taken as up (seat_adopted); any other live seat is
// closed, with the reason (seat_adopted_closing).
func adoptLive(s *scheduler.Scheduler, root string, roster []scheduler.RosterSeat, now time.Time, log func(scheduler.Decision)) {
	seats := map[string]scheduler.RosterSeat{}
	for _, rs := range roster {
		seats[rs.Handle] = rs
	}
	for _, ls := range LiveSeats(root) {
		rs, in := seats[ls.Handle]
		pool := rs.Pool
		reason := "alive from a previous incarnation"
		switch {
		case len(roster) == 0:
		case !in:
			reason += "; not a seat of the roster"
		// Codex finding 1: the record must be the one the proof belongs to.
		// A later launch that overwrote the staged record and failed at
		// Apply leaves the old provider alive under a record that is not
		// its own; its pool and worktree say nothing about that process.
		case ls.ProofLaunchID == "" || ls.ProofLaunchID != ls.RecordLaunchID || ls.ProofRequestID != ls.RecordRequestID:
			reason += fmt.Sprintf("; its proof (launch %s, request %s) is not its launch record's (launch %s, request %s)",
				orDash(ls.ProofLaunchID), orDash(ls.ProofRequestID), orDash(ls.RecordLaunchID), orDash(ls.RecordRequestID))
		case ls.Pool != pool:
			reason += fmt.Sprintf("; its launch record's pool %s is not the roster's %s", ls.Pool.Key(), pool.Key())
		case cleanPath(ls.Worktree) != rs.Worktree:
			reason += fmt.Sprintf("; its launch record's worktree %s is not the roster's %s", orDash(ls.Worktree), orDash(rs.Worktree))
		default:
			s.AdoptUpRequest(ls.Handle, ls.ProofRequestID, pool, now)
			log(scheduler.Decision{Kind: "seat_adopted", Seat: ls.Handle, Pool: pool.Key(), Reason: reason + "; its accepted proof's pid and start stamp match the live process"})
			continue
		}
		s.AdoptClosing(ls.Handle, ls.Pool, now)
		log(scheduler.Decision{Kind: "seat_adopted_closing", Seat: ls.Handle, Reason: reason})
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// RefusedSquadInvalid: the relaunch's reload could not compile squad.yaml;
// nothing drains and the running roster is untouched (brief 31-2 PR 2).
const RefusedSquadInvalid = "squad_invalid"

// RefusedRelaunchMalformed: the relaunch's start is not an RFC3339 stamp.
const RefusedRelaunchMalformed = "relaunch_malformed"

// RefusedStartOutOfRange: the relaunch's start (the key press) is more than
// StartSkew after the relaunch decision, or older than the start of the
// launch running now (the last relaunch's, else the scheduler's launch
// start); nothing drains (Codex P2 1 on 31-2 PR 2, measured against the
// relaunch since the plan reviewer's settled reading).
const RefusedStartOutOfRange = "start_out_of_range"

// StartSkew is how far in the future a relaunch's start may lie (clocks of
// the lead's process and the scheduler's are the same host's).
const StartSkew = 5 * time.Second

// CheckRelaunchStart bounds a relaunch's start: not after now (the
// relaunch decision) plus StartSkew, and not before running, the start of
// the launch running now (zero: no lower bound, a scheduler started by
// hand). "" is in range.
func CheckRelaunchStart(start, running, now time.Time) string {
	switch {
	case start.After(now.Add(StartSkew)):
		return fmt.Sprintf("the relaunch's start %s is %s after the relaunch decision (at most %s)", start.UTC().Format(time.RFC3339Nano), start.Sub(now).Round(time.Millisecond), StartSkew)
	case !running.IsZero() && start.Before(running):
		return fmt.Sprintf("the relaunch's start %s is before the start of the launch running now, %s", start.UTC().Format(time.RFC3339Nano), running.UTC().Format(time.RFC3339Nano))
	}
	return ""
}

// RunningLaunchStart is the start of the launch running now: the last
// relaunch's own start when one ran, else the scheduler's launch start.
func RunningLaunchStart(launchStart, lastRelaunch time.Time) time.Time {
	if lastRelaunch.After(launchStart) {
		return lastRelaunch
	}
	return launchStart
}

// RosterPools is the router's side of a reload: its roster pools, replaced
// under its lock (routing.Router.SetRosterPools, RosterPools).
type RosterPools interface {
	SetRosterPools([]task.Pool)
	RosterPools() []task.Pool
}

// Reload is the relaunch's reload seam (brief 31-2 PR 2): Compile reads
// squad.yaml again as scheduler run reads it at its start (team.LoadSquad,
// Squad.LaunchSeats, roleText; the running squad's wrapper and amq path),
// and the swap writes the result into Slots and Pools at the relaunch's
// drain's end, with the scheduler's roster. Lead is squad.yaml's lead,
// never in the roster.
type Reload struct {
	Compile func() (seatlaunch.Squad, error)
	// Cap is the lease cap for the compiled squad: the grant's max_seats,
	// read again, bounding the new seat count (and scheduler run's
	// --max-seats narrowing); an error is squad_invalid. nil keeps the
	// running cap.
	Cap   func(seatlaunch.Squad) (int, error)
	Slots *Slots
	Pools RosterPools
	Lead  string
}

// reloadFault is a test seam: an error injected at a named step between
// the swap's writes ("pools", after the slots' squad; "roster", after the
// router's pools), which the swap must undo.
var reloadFault func(step string) error

// prepare compiles squad.yaml and returns the reload the scheduler swaps
// in at the drain's end; an error is squad_invalid, before any drain.
func (r *Reload) prepare() (*scheduler.RosterReload, error) {
	if r.Compile == nil || r.Slots == nil {
		return nil, fmt.Errorf("the scheduler has no reload")
	}
	q, err := r.Compile()
	if err != nil {
		return nil, err
	}
	roster, _ := (&Slots{Squad: q}).Roster(r.Lead)
	if len(roster) == 0 {
		return nil, fmt.Errorf("squad.yaml has no seat but the lead")
	}
	maxSeats := 0
	if r.Cap != nil {
		if maxSeats, err = r.Cap(q); err != nil {
			return nil, err
		}
	}
	next := &scheduler.RosterReload{Roster: roster, MaxSeats: maxSeats, Swap: func() error { return r.swap(q, roster) }}
	if r.Cap != nil {
		// the cap above is the early refusal; the one installed is read
		// again at the drain's end, in the swap's step
		next.Cap = func() (int, error) { return r.Cap(q) }
	}
	return next, nil
}

// swap writes the slots' squad (under Slots.mu) and the router's roster
// pools, the reload's two halves the scheduler does not hold; the scheduler
// writes its roster only once this returns nil. A failure at any step
// restores what was written, so a half-applied reload is never observed.
func (r *Reload) swap(q seatlaunch.Squad, roster []scheduler.RosterSeat) error {
	r.Slots.mu.Lock()
	oldSquad := r.Slots.Squad
	r.Slots.Squad = q
	r.Slots.mu.Unlock()
	undoSquad := func() {
		r.Slots.mu.Lock()
		r.Slots.Squad = oldSquad
		r.Slots.mu.Unlock()
	}
	if err := faultAt("pools"); err != nil {
		undoSquad()
		return err
	}
	var oldPools []task.Pool
	if r.Pools != nil {
		oldPools = r.Pools.RosterPools()
		pools := make([]task.Pool, 0, len(roster))
		for _, rs := range roster {
			pools = append(pools, rs.Pool)
		}
		r.Pools.SetRosterPools(pools)
	}
	if err := faultAt("roster"); err != nil {
		if r.Pools != nil {
			r.Pools.SetRosterPools(oldPools)
		}
		undoSquad()
		return err
	}
	return nil
}

func faultAt(step string) error {
	if reloadFault == nil {
		return nil
	}
	return reloadFault(step)
}

// relaunchDecision takes the lead's proved relaunch: the scheduler applies
// it at its next tick (it drains, closes and relaunches over several ticks,
// so the reply is the acceptance; the decisions log carries the outcome).
// With a reload, squad.yaml is compiled here, before anything drains: a
// squad.yaml that does not compile refuses the relaunch squad_invalid and
// leaves the running roster as it is. The request's start (the key press)
// goes with it.
func relaunchDecision(s *scheduler.Scheduler, reload *Reload, launchStart time.Time) func(string, RelaunchRequest) DecideReply {
	return func(lead string, req RelaunchRequest) DecideReply {
		// the relaunch decision's time: the relaunch's own start (the plan
		// reviewer's settled reading on 31-2 PR 2)
		now := time.Now()
		if s.Now != nil {
			now = s.Now()
		}
		var start time.Time
		if req.Start != "" {
			t, err := time.Parse(time.RFC3339Nano, req.Start)
			if err != nil {
				return DecideReply{Code: RefusedRelaunchMalformed, Detail: fmt.Sprintf("the relaunch's start %q is not an RFC3339 stamp; nothing drains", req.Start)}
			}
			if why := CheckRelaunchStart(t, RunningLaunchStart(launchStart, s.LastRelaunchAt()), now); why != "" {
				return DecideReply{Code: RefusedStartOutOfRange, Detail: why + "; nothing drains"}
			}
			start = t
		}
		var next *scheduler.RosterReload
		if reload != nil {
			got, err := reload.prepare()
			if err != nil {
				return DecideReply{Code: RefusedSquadInvalid, Detail: "squad.yaml does not compile: " + err.Error() + "; nothing drains, and the running roster is untouched"}
			}
			next = got
		}
		s.RequestRelaunchAt(req.Reason, start, now, next)
		return DecideReply{OK: true, Detail: "accepted: the seats finish what they hold, then every seat closes and the roster relaunches in one apply; the outcome is in the decisions log (roster_launched or roster_launch_failed)"}
	}
}
