package seatlaunch

import (
	"context"
	"fmt"
	"strings"

	"github.com/avivsinai/agent-message-queue/launchapi"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
)

// LaunchRoster launches the whole roster in one AMQ prepare and one apply
// (3.0.1 t16): AMQ v0.116.2 refuses a fresh seat into a present binding
// (binding_present_without_resumable_conversation), so seats launched one
// apply each leave a squad with one live seat. Each request names its roster
// seat; each seat carries its request id into its launch record, and every
// seat that proves posts its SeatUp with that id, as a single launch does.
// Each request keeps the single launch's state machine (settle), so a seat
// that did not prove is cancelled or closed through CancelSeatRequest like
// any request. It returns once the launch settled.
func (l *Launcher) LaunchRoster(reqs []scheduler.SeatRequest) scheduler.RosterOutcome {
	fail := func(format string, a ...any) scheduler.RosterOutcome {
		return scheduler.RosterOutcome{Stage: "launch_error", Detail: fmt.Sprintf(format, a...)}
	}
	if l.PlanRoster == nil {
		return fail("this launcher has no roster planner")
	}
	if len(reqs) == 0 {
		return fail("the roster is empty")
	}
	for _, r := range reqs {
		if r.ID == "" || r.Handle == "" {
			return fail("a roster request needs an id and a handle (%q, %q)", r.ID, r.Handle)
		}
	}
	q, err := l.PlanRoster(reqs)
	if err != nil {
		return fail("plan the roster: %v", err)
	}
	if len(q.Seats) != len(reqs) {
		return fail("plan the roster: want %d seats, got %d", len(reqs), len(q.Seats))
	}
	for i := range q.Seats {
		if q.Seats[i].Handle != reqs[i].Handle {
			return fail("plan the roster: seat %d is %s, the request names %s", i, q.Seats[i].Handle, reqs[i].Handle)
		}
		q.Seats[i].RequestID = reqs[i].ID
	}
	l.mu.Lock()
	if l.reqs == nil {
		l.reqs = map[string]*request{}
	}
	for _, r := range reqs {
		if _, dup := l.reqs[r.ID]; dup {
			l.mu.Unlock()
			return fail("seat request %s is already known", r.ID)
		}
	}
	rs := make([]*request, len(reqs))
	for i, r := range reqs {
		rs[i] = &request{req: r, state: stateLaunching, handle: r.Handle}
		l.reqs[r.ID] = rs[i]
	}
	l.mu.Unlock()
	opts := l.Options
	opts.BeforeApply = l.resourcesGone(reqs)
	res, err := Launch(context.Background(), q, opts)
	l.settleAll(rs, res, err)
	return rosterOutcome(res, err)
}

// resourcesGone is the roster launch's check before Apply (3.0.1 t16, Codex
// finding 3): AMQ applies a fresh roster only into a binding whose tmux
// resources are gone, and a pane that outlived its seat's process (a
// remain-on-exit pane, a pane of another process) fails the Apply and
// stops the scheduler. Prepare names, per handle, the resource the session's
// binding record holds (prepare.go applyBindingResources: the record's, not
// a live inspection; AMQ keeps the record after the session ends), so each
// named pane is asked whether it still exists through the launcher's
// read-only pane check. A pane that exists, or whose state cannot be read,
// refuses the launch before Apply with seat_replacement_resource_present.
func (l *Launcher) resourcesGone(reqs []scheduler.SeatRequest) func(launchapi.PrepareResultV1) (string, string) {
	mine := map[string]bool{}
	for _, r := range reqs {
		mine[r.Handle] = true
	}
	return func(p launchapi.PrepareResultV1) (string, string) {
		var present []string
		for _, o := range p.Observations {
			if !mine[o.Handle] || o.Resource == "" || o.Resource == "none" {
				continue
			}
			if gone, known := l.paneGone(o.Resource); !known || !gone {
				present = append(present, o.Handle+" ("+o.Resource+")")
			}
		}
		if len(present) == 0 {
			return "", ""
		}
		return scheduler.ReasonResourcePresent, "the binding's resource is still present for " + strings.Join(present, ", ") + "; AMQ would refuse the Apply until it ends (a pane that outlives its process stays until AMQ can replace a seat in place, ask 20)"
	}
}

// rosterOutcome is the scheduler's view of a roster launch: the stage, AMQ's
// reason code and the failure detail, or each seat's state once applied.
func rosterOutcome(res Result, err error) scheduler.RosterOutcome {
	out := scheduler.RosterOutcome{LaunchID: res.LaunchID, Stage: res.Stage, Detail: amqDetail(res.FailureDetail)}
	switch {
	case err != nil && res.ApplyStarted:
		out.Stage, out.Detail = "apply_failed", amqDetail(err.Error())
	case err != nil:
		out.Stage, out.Detail = "launch_error", amqDetail(err.Error())
	case res.Stage == "prepare_refused" && res.Prepare != nil:
		out.ReasonCode = res.Prepare.Reason
	case res.Stage == "decisions_pending":
		var kinds []string
		for _, a := range res.Pending {
			kinds = append(kinds, string(a.Kind))
		}
		out.ReasonCode = strings.Join(kinds, ",")
		out.Detail = strings.Join(res.NextActions, "; ")
	case res.Stage == "apply_failed" && res.Apply != nil:
		out.ReasonCode = res.Apply.ReasonCode
	case res.Stage == "resource_present":
		out.ReasonCode = scheduler.ReasonResourcePresent
	case res.Stage == "applied":
		for _, s := range res.Seats {
			out.Seats = append(out.Seats, scheduler.RosterSeatState{Handle: s.Handle, Up: s.Up, ReasonCode: s.ReasonCode, Detail: s.Detail})
		}
	}
	return out
}

var _ scheduler.RosterLauncher = (*Launcher)(nil)
