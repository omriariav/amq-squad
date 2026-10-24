package fleet

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
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
	Grant        grants.Resolved // the operator session's grant
	RegistryPath string          // fleet.yaml
	Squads       SquadWriter
	Launcher     RosterLauncher
	Cost         CostMeter
	// Live reports whether a registered squad still counts against
	// max_squads (default: every registered squad counts).
	Live func(Squad) bool
	// NeedsProvisioning is the ask 2 check (default AMQProvisioned): a root
	// that the launch API cannot provision gives a proposal, not a launch.
	NeedsProvisioning func(projectPath string) (bool, string)
	Now               func() time.Time
	// Waiting are the starts that wait for a free slot or the next day, in
	// the order they came; Retry starts them when they fit.
	Waiting []StartRequest
}

// Start outcomes.
const (
	OutcomeStarted  = "started"
	OutcomeWaiting  = "waiting"  // inside the grant later: max_squads or cost_per_day
	OutcomeRefused  = "refused"  // never inside this grant
	OutcomeProposed = "proposed" // the owner starts it (ask 2 fallback, manual mode)
)

// Reason codes.
const (
	ReasonNoFleetGrant = "no_fleet_grant"
	ReasonManualMode   = "manual_mode"
	ReasonRepository   = "repository_not_permitted"
	ReasonMaxSquads    = "max_squads"
	ReasonCostPerDay   = "cost_per_day"
	ReasonProvisioning = "root_needs_provisioning"
	ReasonRegistered   = "squad_registered"
)

// StartRequest is a squad the mayor wants to start.
type StartRequest struct {
	Squad     Squad  // name, root, project_path, repository, lead, session
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

// Start runs the checks in order (fleet grant, repository, one squad per
// repository, mode, a free squad slot, the day's cost, provisioning) and only
// then writes squad.yaml, launches and registers. Nothing is written for any
// outcome but started. A waiting start joins Waiting.
func (m *Mayor) Start(ctx context.Context, req StartRequest) (Result, error) {
	r, err := m.start(ctx, req)
	if err == nil && r.Outcome == OutcomeWaiting && !slices.ContainsFunc(m.Waiting, func(w StartRequest) bool { return w.Squad.Name == req.Squad.Name }) {
		m.Waiting = append(m.Waiting, req)
	}
	return r, err
}

// Retry runs the waiting starts in order; each that is no longer waiting
// leaves the queue. It returns the results by squad name.
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

func (m *Mayor) start(ctx context.Context, req StartRequest) (Result, error) {
	now := time.Now()
	if m.Now != nil {
		now = m.Now()
	}
	if !m.Grant.FleetScope() {
		return Result{Outcome: OutcomeRefused, Reason: ReasonNoFleetGrant, Detail: ErrNotFleet.Error()}, nil
	}
	s := req.Squad
	fleet := m.Grant.Fleet
	if !fleet.RepositoryPermitted(s.Repository) {
		return Result{Outcome: OutcomeRefused, Reason: ReasonRepository,
			Detail: fmt.Sprintf("%s is not in the fleet grant's permitted_repositories %v", s.Repository, fleet.PermittedRepositories)}, nil
	}
	reg, err := Read(m.RegistryPath)
	if errors.Is(err, os.ErrNotExist) {
		reg, err = Registry{Schema: Schema}, nil
	}
	if err != nil {
		return Result{}, err
	}
	if _, dup := reg.Find(s.Name); dup || slices.ContainsFunc(reg.Squads, func(x Squad) bool { return x.ProjectPath == s.ProjectPath }) {
		return Result{Outcome: OutcomeRefused, Reason: ReasonRegistered,
			Detail: "the fleet already has a squad with this name or repository root (one squad per repository)"}, nil
	}
	if err := (Registry{Schema: Schema, Squads: []Squad{s}}).Validate(); err != nil {
		return Result{}, err
	}
	if m.Grant.Mode != grants.ModeAuto {
		return m.propose(req, ReasonManualMode, "manual mode: the operator session proposes, the owner starts"), nil
	}
	live := 0
	for _, x := range reg.Squads {
		if m.Live == nil || m.Live(x) {
			live++
		}
	}
	if live >= fleet.MaxSquads {
		return Result{Outcome: OutcomeWaiting, Reason: ReasonMaxSquads,
			Detail: fmt.Sprintf("%d squads live, max_squads %d", live, fleet.MaxSquads)}, nil
	}
	if fleet.CostPerDayUSD > 0 && m.Cost != nil {
		if spent := m.Cost.SpentToday(now); spent >= fleet.CostPerDayUSD {
			return Result{Outcome: OutcomeWaiting, Reason: ReasonCostPerDay,
				Detail: fmt.Sprintf("spent %.2f USD today, cost_per_day_usd %g", spent, fleet.CostPerDayUSD)}, nil
		}
	}
	needs := m.NeedsProvisioning
	if needs == nil {
		needs = AMQProvisioned
	}
	if ok, why := needs(s.ProjectPath); ok {
		return m.propose(req, ReasonProvisioning, why), nil
	}
	if m.Squads == nil || m.Launcher == nil {
		return Result{}, errors.New("the mayor needs a squad.yaml writer and a roster launcher")
	}
	digest, err := m.Squads.Propose(s.ProjectPath, req.SquadYAML)
	if err != nil {
		return Result{}, fmt.Errorf("propose squad.yaml: %w", err)
	}
	if err := m.Squads.Apply(s.ProjectPath, digest); err != nil {
		return Result{}, fmt.Errorf("apply squad.yaml %s: %w", digest, err)
	}
	if err := m.Launcher.Launch(ctx, LaunchRequest{ProjectPath: s.ProjectPath, Root: s.Root, Session: s.SessionName(), SquadYAML: req.SquadYAML, Digest: digest}); err != nil {
		return Result{}, fmt.Errorf("launch %s: %w", s.Name, err)
	}
	if err := Update(m.RegistryPath, func(r *Registry) error {
		if _, dup := r.Find(s.Name); dup {
			return fmt.Errorf("squad %s registered meanwhile", s.Name)
		}
		r.Squads = append(r.Squads, s)
		return nil
	}); err != nil {
		return Result{}, err
	}
	return Result{Outcome: OutcomeStarted, Digest: digest}, nil
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
