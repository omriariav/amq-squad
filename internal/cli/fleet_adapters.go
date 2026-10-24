package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/catalog"
	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// The mayor's two adapters (4-3, after 2b-5 and 2b-2 merged): squad.yaml
// through 2b-5's digest flow (team.ParseSquad, team.NewPlan, Plan.Apply,
// the same planning `team propose` and `team apply` run) and the roster
// through 2b-2's seat launch compiler (team.Squad.LaunchSeats,
// seatlaunch.Launch). Each is one function over those packages.

// teamSquadWriter is fleet.SquadWriter over the digest flow. Propose plans
// and writes nothing; Apply writes the plan Propose made for that
// repository, only for its digest.
type teamSquadWriter struct {
	mu     sync.Mutex
	plans  map[string]team.Plan
	before map[string]map[string]*[]byte // per repository, each planned file before Apply (nil: absent)
}

func (w *teamSquadWriter) Propose(projectPath, squadYAML string) (string, error) {
	p, err := planSquadYAML(projectPath, squadYAML)
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.plans == nil {
		w.plans = map[string]team.Plan{}
	}
	w.plans[projectPath] = p
	return p.Digest, nil
}

func (w *teamSquadWriter) Apply(projectPath, digest string) error {
	w.mu.Lock()
	p, ok := w.plans[projectPath]
	w.mu.Unlock()
	if !ok {
		return fmt.Errorf("no squad.yaml proposal for %s", projectPath)
	}
	before := map[string]*[]byte{}
	for _, f := range p.Files {
		if raw, err := os.ReadFile(f.Path); err == nil {
			before[f.Path] = &raw
		} else if os.IsNotExist(err) {
			before[f.Path] = nil
		} else {
			return err
		}
	}
	if err := p.Apply(digest); err != nil {
		return err
	}
	w.mu.Lock()
	if w.before == nil {
		w.before = map[string]map[string]*[]byte{}
	}
	w.before[projectPath] = before
	w.mu.Unlock()
	return nil
}

// Rollback restores every file Apply wrote for digest: a file that existed
// gets its old bytes back through atomicfile's staged commit, a file Apply
// created is removed (a launch that failed before promotion leaves no squad
// files).
func (w *teamSquadWriter) Rollback(projectPath, digest string) error {
	w.mu.Lock()
	p, ok := w.plans[projectPath]
	before := w.before[projectPath]
	w.mu.Unlock()
	if !ok || p.Digest != digest || before == nil {
		return fmt.Errorf("no applied squad.yaml %s for %s to roll back", digest, projectPath)
	}
	var tx atomicfile.Tx
	defer tx.Abort()
	var created []string
	for path, old := range before {
		if old == nil {
			created = append(created, path)
			continue
		}
		if err := tx.Stage(path, *old, 0o644); err != nil {
			return err
		}
	}
	if err := tx.Commit(nil); err != nil {
		return err
	}
	var errs []error
	for _, path := range created {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			errs = append(errs, err)
		}
	}
	w.mu.Lock()
	delete(w.before, projectPath)
	w.mu.Unlock()
	return errors.Join(errs...)
}

// planSquadYAML is `team propose` for a squad.yaml text: parsed against the
// owner grant, team rules rendered from it, every planned write digested.
func planSquadYAML(projectPath, text string) (team.Plan, error) {
	owner, err := grants.Load("")
	if err != nil {
		return team.Plan{}, err
	}
	s, err := team.ParseSquad(text, team.SquadPath(projectPath), owner.Grants)
	if err != nil {
		return team.Plan{}, err
	}
	t := team.FromSquad(projectPath, s)
	rulesText, err := renderTeamRulesWithTemplateDraft(t, "auto", nil)
	if err != nil {
		return team.Plan{}, err
	}
	return team.NewPlan(projectPath, text, t, rulesText)
}

// seatRoster is fleet.RosterLauncher over 2b-2's seat launch: the squad's
// seats compiled, staged, prepared, applied and waited for. A launch that
// leaves some seats up and others not is a fleet.PartialLaunchError, so the
// mayor keeps the squad's slot counted.
type seatRoster struct {
	Wait time.Duration
}

func (l seatRoster) Launch(ctx context.Context, req fleet.LaunchRequest) error {
	q, err := rosterSquad(req)
	if err != nil {
		return err
	}
	if q.Wrapper, err = os.Executable(); err == nil {
		q.Wrapper, err = filepath.EvalSymlinks(q.Wrapper)
	}
	if err != nil {
		return fmt.Errorf("resolve the amq-squad executable: %w", err)
	}
	if q.AMQPath, err = exec.LookPath("amq"); err != nil {
		return fmt.Errorf("the installed amq is required on PATH (R51): %w", err)
	}
	wait := l.Wait
	if wait == 0 {
		wait = 60 * time.Second
	}
	res, err := seatlaunch.Launch(ctx, q, seatlaunch.Options{Wait: wait})
	return launchOutcome(res, err)
}

// Close stops every seat of the launched roster through 2b-2's CloseSeat
// (the provider pid with its recorded birth time), and returns nil only when
// every close is confirmed.
func (l seatRoster) Close(_ context.Context, req fleet.LaunchRequest) error {
	q, err := rosterSquad(req)
	if err != nil {
		return err
	}
	closer := &seatlaunch.Launcher{Root: req.Root}
	var errs []error
	for _, s := range q.Seats {
		if err := closer.CloseSeat(s.Handle, fleet.ReleaseLaunchFenced); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// rosterSquad is the launch request's squad.yaml as 2b-2's typed squad.
func rosterSquad(req fleet.LaunchRequest) (seatlaunch.Squad, error) {
	owner, err := grants.Load("")
	if err != nil {
		return seatlaunch.Squad{}, err
	}
	s, err := team.ParseSquad(req.SquadYAML, team.SquadPath(req.ProjectPath), owner.Grants)
	if err != nil {
		return seatlaunch.Squad{}, err
	}
	seats, err := s.LaunchSeats(req.ProjectPath, func(id string) (string, error) { return roleText(req.ProjectPath, id) })
	if err != nil {
		return seatlaunch.Squad{}, err
	}
	// the launch root is the registered root: its parent is the base, its
	// name the session (a separate session flag never moves the launch)
	return seatlaunch.Squad{ProjectRoot: req.ProjectPath, BaseRoot: filepath.Dir(req.Root), Session: filepath.Base(req.Root), Seats: seats}, nil
}

// roleText is a seat's role.md: the repository's staged custom role, else
// the catalog role's profile and description.
func roleText(projectPath, id string) (string, error) {
	if raw, err := os.ReadFile(team.CustomRolePath(projectPath, id)); err == nil {
		return string(raw), nil
	}
	r := catalog.Lookup(id)
	if r == nil {
		return "", fmt.Errorf("role %q is neither a catalog role nor staged at %s", id, team.CustomRolePath(projectPath, id))
	}
	return fmt.Sprintf("# %s\n\n%s\n\n%s\n", r.Label, r.Profile, r.Description), nil
}

// launchOutcome maps a seat launch to the mayor's terms. Success is only
// stage applied with at least one seat and every seat up. Stage
// decisions_pending is a fleet.DecisionsPendingError carrying the required
// actions for the owner; some seats up is a partial launch; none up is a
// plain error.
func launchOutcome(res seatlaunch.Result, err error) error {
	if err == nil && res.Stage == "decisions_pending" {
		var actions []string
		for _, a := range res.Pending {
			var choices []string
			for _, c := range a.AllowedDecisions {
				choices = append(choices, string(c))
			}
			actions = append(actions, fmt.Sprintf("%s: %s %s (%s)", a.ActionID, a.Kind, a.ReasonCode, strings.Join(choices, "|")))
		}
		return &fleet.DecisionsPendingError{Actions: actions}
	}
	if err == nil && res.Stage == "applied" && len(res.Seats) > 0 && !res.Failed() {
		return nil
	}
	var up []string
	for _, s := range res.Seats {
		if s.Up {
			up = append(up, s.Handle)
		}
	}
	if err == nil {
		err = fmt.Errorf("not every seat came up (stage %s)", res.Stage)
	}
	if len(up) > 0 {
		return &fleet.PartialLaunchError{Live: up, Err: err}
	}
	return errors.New(strings.TrimSpace(err.Error()))
}
