package fleet

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
)

// The mayor (plan 3.5, 8; R40, R41): the operator session in auto mode. It
// starts a squad on its own inside the fleet grant (maximum squads, cost per
// day, permitted repositories), writes that squad's squad.yaml through the
// same digest flow a human runs, provisions the roster through the launch
// API package, and registers the squad in fleet.yaml. It partitions work by
// repository: one squad per repository root.
//
// The mayor never approves a typed gate. It has no method that answers one,
// it holds no owner key, and the core's verifier rejects any answer not
// signed by the owner key (internal/gateverify); a test in this package
// keeps it so.

// SquadWriter is the squad.yaml digest flow (2b-5: team.NewPlan and
// Plan.Apply; docs/squad-yaml.md). Propose plans the files and writes
// nothing; Apply writes them only for the digest Propose returned. The
// adapter over internal/team is one function once 2b-5 is on v3.
type SquadWriter interface {
	Propose(projectPath, squadYAML string) (digest string, err error)
	Apply(projectPath, digest string) error
}

// RosterLauncher provisions a squad's roster through the launch API package
// (2b-2: the seat launch compiler over AMQ launchapi Prepare and Apply). The
// adapter is one function once 2b-2 is on v3.
type RosterLauncher interface {
	Launch(ctx context.Context, req LaunchRequest) error
}

// LaunchRequest is one roster to provision.
type LaunchRequest struct {
	ProjectPath string
	Root        string
	Session     string
	SquadYAML   string
	Digest      string
}

// CostMeter reports the fleet's spend today in USD. Cost accounting beyond
// the grant check is out of 4-3's scope; nil spends nothing.
type CostMeter interface {
	SpentToday(now time.Time) float64
}

// Mayor starts squads inside the fleet grant.
type Mayor struct {
	// Grant loads the operator session's grant. The mayor calls it on every
	// start and every retry, so a permission the owner removed from
	// grants.yaml stops the next start (Codex review, P1 3).
	Grant        func() (grants.Resolved, error)
	RegistryPath string // fleet.yaml
	Squads       SquadWriter
	Launcher     RosterLauncher
	Cost         CostMeter
	// Live reports whether a registered squad still counts against
	// max_squads (default: every registered squad counts).
	Live func(Squad) bool
	// NeedsProvisioning is the ask 2 check (default AMQProvisioned): a root
	// that the launch API cannot provision gives a proposal, not a launch.
	NeedsProvisioning func(projectPath string) (bool, string)
	// Identify derives the repository identity and the canonical checkout
	// (default RepositoryIdentity). The request's repository label is
	// display only; the grant is checked on this identity (P1 1).
	Identify func(projectPath string, f grants.Fleet) (identity, canonical string, err error)
	// Incarnation identifies this mayor on its reservations (default: a
	// random token on first use); its process id and start time go on each
	// reservation. PhaseWindow is how long a reservation may stay in one
	// phase while its mayor is alive (default PhaseWindow, per phase).
	// IncarnationAlive overrides the liveness check (tests).
	Incarnation      string
	PhaseWindow      time.Duration
	IncarnationAlive func(pid int, start time.Time) Liveness
	Now              func() time.Time
	// Session is true in the running operator session, which queues a start
	// held by the fleet cap and retries it. The one-shot CLI verb leaves it
	// false: such a start is refused, never queued (reviewer ruling).
	Session bool
	// Waiting are the starts that wait for a free slot or the next day, in
	// the order they came; Retry starts them when they fit. Only a Session
	// mayor queues.
	Waiting []StartRequest
}

// StaticGrant is a Grant source that always returns g (tests, and callers
// that resolved the grant themselves).
func StaticGrant(g grants.Resolved) func() (grants.Resolved, error) {
	return func() (grants.Resolved, error) { return g, nil }
}

// Start outcomes.
const (
	OutcomeStarted  = "started"
	OutcomeWaiting  = "waiting"  // inside the grant later: max_squads or cost_per_day
	OutcomeRefused  = "refused"  // not inside this grant
	OutcomeProposed = "proposed" // the owner starts it (ask 2 fallback, manual mode)
)

// Reason codes.
const (
	ReasonGrantInvalid  = "grant_invalid"
	ReasonNoFleetGrant  = "no_fleet_grant"
	ReasonManualMode    = "manual_mode"
	ReasonUnidentified  = "repository_unidentified"
	ReasonRepository    = "repository_not_permitted"
	ReasonMaxSquads     = "max_squads"
	ReasonCostPerDay    = "cost_per_day"
	ReasonProvisioning  = "root_needs_provisioning"
	ReasonRegistered    = "squad_registered"
	ReasonLaunchFailure = "launch_failed"
	ReasonLaunchPartial = "launch_failed_partial"
)

// StartRequest is a squad the mayor wants to start.
type StartRequest struct {
	Squad     Squad  // name, root, project_path, lead, session; repository is a label
	SquadYAML string // the squad.yaml text it proposes
}

// Proposal is what the owner gets instead of a launch.
type Proposal struct {
	Squad  Squad      `json:"squad"`
	Reason string     `json:"reason"`
	Detail string     `json:"detail"`
	Steps  [][]string `json:"steps"` // argv the owner runs, in order
	// SquadYAML is the squad.yaml the mayor would have written.
	SquadYAML string `json:"squad_yaml"`
}

// Result is the outcome of one start.
type Result struct {
	Outcome  string    `json:"outcome"`
	Reason   string    `json:"reason,omitempty"`
	Detail   string    `json:"detail,omitempty"`
	Digest   string    `json:"digest,omitempty"`
	Proposal *Proposal `json:"proposal,omitempty"`
}

// ErrNotFleet is a mayor built from a grant that is not a fleet grant.
var ErrNotFleet = errors.New("the mayor needs a fleet grant (the operator session's), not a squad grant")

// Start checks, in order: the freshly loaded grant is a fleet grant, the
// checkout's derived repository identity is permitted, the squad is valid,
// auto mode, the day's cost and provisioning. Admission then runs inside
// the registry lock: expired reservations are released, one squad per name
// and checkout, a free slot under max_squads (reservations and partial
// launches count whatever Live says), and the slot reserved with this
// mayor's incarnation, a reservation id and an expiry. The adapters are
// checked after admission, so a full fleet reports max_squads. A launch
// with no live seat releases the reservation by its id; a partial launch
// keeps it counted in phase launch_failed_partial.
// A start held by the fleet cap is recorded under fleet.yaml waiting; a
// Session mayor queues it. Nothing else is written for any other outcome.
func (m *Mayor) Start(ctx context.Context, req StartRequest) (Result, error) {
	r, err := m.start(ctx, req)
	if err == nil && m.Session && r.Outcome == OutcomeWaiting && !slices.ContainsFunc(m.Waiting, func(w StartRequest) bool { return w.Squad.Name == req.Squad.Name }) {
		m.Waiting = append(m.Waiting, req)
	}
	return r, err
}

// Retry runs the waiting starts in order, each against the grant as it is
// now; each that is no longer waiting leaves the queue (started, or refused
// with its named reason, its waiting row dropped). It returns the results by
// squad name.
func (m *Mayor) Retry(ctx context.Context) (map[string]Result, error) {
	out := map[string]Result{}
	var still []StartRequest
	for _, req := range m.Waiting {
		r, err := m.start(ctx, req)
		if err != nil {
			return out, err
		}
		out[req.Squad.Name] = r
		if r.Outcome == OutcomeWaiting {
			still = append(still, req)
		}
	}
	m.Waiting = still
	return out, nil
}

func (m *Mayor) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

// refuse is a refusal that is not the cap: the squad no longer waits, so a
// waiting row it had is dropped.
func (m *Mayor) refuse(name, reason, detail string) (Result, error) {
	if err := m.dropWaiting(name); err != nil {
		return Result{}, err
	}
	return Result{Outcome: OutcomeRefused, Reason: reason, Detail: detail}, nil
}

func (m *Mayor) dropWaiting(name string) error {
	reg, err := Read(m.RegistryPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(reg.Waiting, func(w Waiting) bool { return w.Name == name }) {
		return nil
	}
	return Update(m.RegistryPath, func(r *Registry) error {
		r.Waiting = slices.DeleteFunc(r.Waiting, func(w Waiting) bool { return w.Name == name })
		return nil
	})
}

// recordWaiting upserts the squad's waiting row, keeping the first hold's
// time.
func recordWaiting(r *Registry, w Waiting) {
	if i := slices.IndexFunc(r.Waiting, func(x Waiting) bool { return x.Name == w.Name }); i >= 0 {
		w.Since = r.Waiting[i].Since
		r.Waiting[i] = w
		return
	}
	r.Waiting = append(r.Waiting, w)
}

// capResult is a start held back by the fleet cap. In a running operator
// session (Session) it waits and Start queues it; on the one-shot CLI path
// there is no session to retry it, so it is refused with the same named
// reason. Either way the caller records it under fleet.yaml waiting, so
// status --fleet shows wait_reason fleet_cap.
func (m *Mayor) capResult(s Squad, reason, detail string, now time.Time) (Result, Waiting) {
	outcome := OutcomeWaiting
	if !m.Session {
		outcome, detail = OutcomeRefused, detail+"; no running operator session to queue it"
	}
	w := Waiting{Name: s.Name, Repository: s.Repository, ProjectPath: s.ProjectPath, Cap: reason, Since: now.UTC().Format(time.RFC3339), Detail: detail}
	return Result{Outcome: outcome, Reason: reason, Detail: detail}, w
}

var errDuplicate = errors.New("duplicate")

func (m *Mayor) start(ctx context.Context, req StartRequest) (Result, error) {
	now := m.now()
	s := req.Squad
	if m.Grant == nil {
		return Result{}, errors.New("the mayor needs a grant source")
	}
	g, err := m.Grant()
	if err != nil {
		return m.refuse(s.Name, ReasonGrantInvalid, err.Error())
	}
	if !g.FleetScope() {
		return m.refuse(s.Name, ReasonNoFleetGrant, ErrNotFleet.Error())
	}
	identify := m.Identify
	if identify == nil {
		identify = RepositoryIdentity
	}
	label := s.Repository
	id, canonical, err := identify(s.ProjectPath, g.Fleet)
	if err != nil {
		return m.refuse(s.Name, ReasonUnidentified, err.Error())
	}
	s.Repository, s.ProjectPath = id, canonical
	fleet := g.Fleet
	if !fleet.RepositoryPermitted(id) {
		detail := fmt.Sprintf("the checkout %s is %s, not in the fleet grant's permitted_repositories %v", canonical, id, fleet.PermittedRepositories)
		if label != "" && !strings.EqualFold(label, id) {
			detail += fmt.Sprintf(" (the request's label %q is display only)", label)
		}
		return m.refuse(s.Name, ReasonRepository, detail)
	}
	if err := (Registry{Schema: Schema, Squads: []Squad{s}}).Validate(); err != nil {
		return Result{}, err
	}
	req.Squad = s
	if g.Mode != grants.ModeAuto {
		return m.propose(req, ReasonManualMode, "manual mode: the operator session proposes, the owner starts"), nil
	}
	if fleet.CostPerDayUSD > 0 && m.Cost != nil {
		if spent := m.Cost.SpentToday(now); spent >= fleet.CostPerDayUSD {
			res, w := m.capResult(s, ReasonCostPerDay, fmt.Sprintf("spent %.2f USD today, cost_per_day_usd %g", spent, fleet.CostPerDayUSD), now)
			return res, Update(m.RegistryPath, func(r *Registry) error { recordWaiting(r, w); return nil })
		}
	}
	needs := m.NeedsProvisioning
	if needs == nil {
		needs = AMQProvisioned
	}
	if ok, why := needs(s.ProjectPath); ok {
		return m.propose(req, ReasonProvisioning, why), nil
	}

	// admission, inside the registry lock: release stale reservations
	// (incarnation gone, or a phase stalled for a window), refuse a
	// duplicate, hold at the cap, else reserve the slot with this mayor's
	// incarnation (P1 2)
	resv := Squad{}
	var held *Result
	inc := m.incarnation()
	err = Update(m.RegistryPath, func(r *Registry) error {
		r.ReleaseStale(now, m.PhaseWindow, m.incarnationAlive)
		if _, dup := r.Find(s.Name); dup || slices.ContainsFunc(r.Squads, func(x Squad) bool { return x.ProjectPath == s.ProjectPath }) {
			return errDuplicate
		}
		taken := 0
		for _, x := range r.Squads {
			if x.Counts(m.Live) {
				taken++
			}
		}
		if taken >= fleet.MaxSquads {
			res, w := m.capResult(s, ReasonMaxSquads, fmt.Sprintf("%d of max_squads %d taken", taken, fleet.MaxSquads), now)
			held = &res
			recordWaiting(r, w)
			return nil
		}
		resv = s
		resv.Phase, resv.Reservation = PhaseReserved, newToken()
		resv.Incarnation, resv.MayorPID, resv.MayorStart = inc.token, strconv.Itoa(inc.pid), inc.start.UTC().Format(time.RFC3339Nano)
		resv.PhaseSince = now.UTC().Format(time.RFC3339)
		r.Squads = append(r.Squads, resv)
		r.Waiting = slices.DeleteFunc(r.Waiting, func(w Waiting) bool { return w.Name == s.Name })
		return nil
	})
	if errors.Is(err, errDuplicate) {
		return m.refuse(s.Name, ReasonRegistered, "the fleet already has a squad with this name or checkout (one squad per repository)")
	}
	if err != nil {
		return Result{}, err
	}
	if held != nil {
		return *held, nil
	}
	// adapters after admission (P2 6): a full fleet reports max_squads
	if m.Squads == nil || m.Launcher == nil {
		err := errors.New("the mayor needs a squad.yaml writer and a roster launcher")
		return Result{}, errors.Join(err, m.setPhase(resv, "", ""))
	}
	digest, err := m.launch(ctx, req, resv)
	if err == nil {
		if err := m.setPhase(resv, PhaseRunning, ""); err != nil {
			return Result{}, err
		}
		return Result{Outcome: OutcomeStarted, Digest: digest}, nil
	}
	// a partial launch keeps its slot until the operator resolves it; a
	// launch with no live seat releases the reservation, by its id (new P1,
	// new P2)
	var partial *PartialLaunchError
	if errors.As(err, &partial) && len(partial.Live) > 0 {
		if perr := m.setPhase(resv, PhaseFailedPartial, strings.Join(partial.Live, ",")); perr != nil {
			return Result{}, errors.Join(err, perr)
		}
		return Result{Outcome: OutcomeRefused, Reason: ReasonLaunchPartial, Detail: err.Error()}, nil
	}
	if rerr := m.setPhase(resv, "", ""); rerr != nil {
		return Result{}, errors.Join(err, fmt.Errorf("release the reservation of %s: %w", s.Name, rerr))
	}
	return Result{Outcome: OutcomeRefused, Reason: ReasonLaunchFailure, Detail: err.Error()}, nil
}

// PhaseRunning is setPhase's target for a launched squad: the row loses its
// reservation fields and becomes a running squad.
const PhaseRunning = "running"

// errReservationGone is a reservation the registry no longer holds (it
// expired and was released, or was replaced).
var errReservationGone = errors.New("the reservation is no longer in the registry")

// setPhase changes the row of reservation resv, found by its reservation
// id, never by name: "" deletes it, PhaseRunning makes it a running squad,
// any other phase sets that phase (with the live seats of a partial launch).
func (m *Mayor) setPhase(resv Squad, phase, liveSeats string) error {
	return Update(m.RegistryPath, func(r *Registry) error {
		i := slices.IndexFunc(r.Squads, func(x Squad) bool { return x.Reservation != "" && x.Reservation == resv.Reservation })
		if i < 0 {
			if phase == "" {
				return nil
			}
			return fmt.Errorf("squad %s: %w", resv.Name, errReservationGone)
		}
		switch phase {
		case "":
			r.Squads = slices.Delete(r.Squads, i, i+1)
		case PhaseRunning:
			x := &r.Squads[i]
			x.Phase, x.Reservation, x.Incarnation, x.MayorPID, x.MayorStart, x.PhaseSince, x.LiveSeats = "", "", "", "", "", "", ""
		default:
			// a new phase restarts its window
			r.Squads[i].Phase, r.Squads[i].LiveSeats = phase, liveSeats
			r.Squads[i].PhaseSince = m.now().UTC().Format(time.RFC3339)
		}
		return nil
	})
}

// PartialLaunchError is a launch that left some seats live: the launcher
// returns it so the mayor keeps the squad's slot (phase
// launch_failed_partial) until the operator resolves it.
type PartialLaunchError struct {
	Live []string // the live seats' handles
	Err  error
}

func (e *PartialLaunchError) Error() string {
	return fmt.Sprintf("partial launch, live seats %s: %v", strings.Join(e.Live, ","), e.Err)
}

func (e *PartialLaunchError) Unwrap() error { return e.Err }

// incarnation is this mayor on its reservations: a token, its process id
// and that process's start time (a reused pid has another start time).
type incarnation struct {
	token string
	pid   int
	start time.Time
}

func (m *Mayor) incarnation() incarnation {
	if m.Incarnation == "" {
		m.Incarnation = newToken()
	}
	pid := os.Getpid()
	start, ok := pidStartTime(pid)
	if !ok {
		start = processStart
	}
	return incarnation{token: m.Incarnation, pid: pid, start: start}
}

// processStart stands in for the start time where the OS gives none.
var processStart = time.Now()

// The process probes, package variables so tests can stub them.
var (
	pidAlive     = procinfo.Alive
	pidStartTime = procinfo.StartTime
)

// incarnationAlive is the liveness of a reservation's mayor, checked
// under the registry lock: its pid is a live process with the recorded
// start time (a foreign pid's start time within 1 s; our own pid's
// exactly). A live pid whose start time cannot be read is unknown, which
// counts as alive: a read error never releases a reservation and admits a
// squad past the cap (default procinfo; Mayor.IncarnationAlive overrides it).
func (m *Mayor) incarnationAlive(pid int, start time.Time) Liveness {
	if m.IncarnationAlive != nil {
		return m.IncarnationAlive(pid, start)
	}
	if pid <= 0 || !pidAlive(pid) {
		return LivenessGone
	}
	if pid == os.Getpid() {
		if start.Equal(m.incarnation().start) {
			return LivenessAlive
		}
		return LivenessGone
	}
	got, ok := pidStartTime(pid)
	switch {
	case !ok:
		return LivenessUnknown
	case got.Sub(start).Abs() < time.Second:
		return LivenessAlive
	}
	return LivenessGone
}

func newToken() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand does not fail on supported platforms
	}
	return hex.EncodeToString(b[:])
}

// launch writes squad.yaml through the digest flow and provisions the
// roster; the reservation moves to launching before the roster starts.
func (m *Mayor) launch(ctx context.Context, req StartRequest, resv Squad) (string, error) {
	s := req.Squad
	if err := m.setPhase(resv, PhaseApplying, ""); err != nil {
		return "", err
	}
	digest, err := m.Squads.Propose(s.ProjectPath, req.SquadYAML)
	if err != nil {
		return "", fmt.Errorf("propose squad.yaml: %w", err)
	}
	if err := m.Squads.Apply(s.ProjectPath, digest); err != nil {
		return "", fmt.Errorf("apply squad.yaml %s: %w", digest, err)
	}
	if err := m.setPhase(resv, PhaseWaitingSeats, ""); err != nil {
		return "", err
	}
	if err := m.Launcher.Launch(ctx, LaunchRequest{ProjectPath: s.ProjectPath, Root: s.Root, Session: s.SessionName(), SquadYAML: req.SquadYAML, Digest: digest}); err != nil {
		return "", fmt.Errorf("launch %s: %w", s.Name, err)
	}
	return digest, nil
}

// propose is the ask 2 fallback (R40): "the mayor proposes, the owner
// starts." The steps are argv for the owner; the mayor runs none of them.
func (m *Mayor) propose(req StartRequest, reason, detail string) Result {
	s := req.Squad
	p := &Proposal{Squad: s, Reason: reason, Detail: detail, SquadYAML: req.SquadYAML}
	// For root_needs_provisioning the owner first gives the root its AMQ
	// project config (.amqrc); Detail says so. The rest is the human flow.
	p.Steps = append(p.Steps,
		[]string{"amq-squad", "team", "propose", "--project", s.ProjectPath},
		[]string{"amq-squad", "fleet", "add", "--name", s.Name, "--root", s.Root, "--project-path", s.ProjectPath, "--repository", s.Repository, "--lead", s.Lead})
	return Result{Outcome: OutcomeProposed, Reason: reason, Detail: detail, Proposal: p}
}

// AMQProvisioned is the default ask 2 check: the launch API provisions an
// unknown session under a project root only when that root has an .amqrc
// (docs/v3-upstream-asks.md, ask 2, AMQ v0.102.0); without one it is
// refused, so the root needs the owner.
func AMQProvisioned(projectPath string) (bool, string) {
	info, err := os.Stat(filepath.Join(projectPath, ".amqrc"))
	if err == nil && info.Mode().IsRegular() {
		return false, ""
	}
	if _, err := os.Stat(projectPath); err != nil {
		return true, "the repository root " + projectPath + " does not exist"
	}
	return true, "the repository root has no .amqrc; the launch API refuses to provision a session under it (upstream ask 2)"
}
