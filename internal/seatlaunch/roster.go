package seatlaunch

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

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
	opts.BeforeApply = l.resourcesGone(q.SessionRoot(), reqs)
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
func (l *Launcher) resourcesGone(root string, reqs []scheduler.SeatRequest) func(launchapi.PrepareResultV1) (string, string) {
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
			if live, known := l.bindingPane(root, o.Handle, o.Resource); !known || live {
				present = append(present, o.Handle+" ("+o.Resource+")")
			}
		}
		if len(present) == 0 {
			return "", ""
		}
		return scheduler.ReasonResourcePresent, "the binding's resource is still present for " + strings.Join(present, ", ") + "; AMQ would refuse the Apply until it ends (a pane that outlives its process stays until AMQ can replace a seat in place, ask 20)"
	}
}

// bindingPane reports whether a seat's recorded pane still exists as the
// binding's resource (Launcher.BindingPane, default bindingPanePresent).
func (l *Launcher) bindingPane(root, handle, resource string) (present, known bool) {
	if l.BindingPane != nil {
		return l.BindingPane(root, handle, resource)
	}
	return bindingPanePresent(root, handle, resource, l.Options.Env)
}

// The pane check bound to the binding (3.0.1 t16 round 7, Codex 4): a tmux
// pane id (%N) is unique only within one server's life, so after a tmux
// server restart an unrelated pane can carry the old id. A recorded pane is
// present only when the binding's own server (its socket, the binding's
// instance identity) lists it in the binding's session (id and name), with
// the seat's agent label, and with the server pid the binding recorded as
// its epoch (AMQ's tmux:v1:epoch resource, the server incarnation).

// amqBinding is the part of AMQ's binding record (<session root>/meta/
// launch/binding.json, AMQ internal/launch/binding.go) the check reads.
type amqBinding struct {
	Backend          string `json:"backend"`
	InstanceIdentity string `json:"instance_identity"`
	Resources        struct {
		Resources []struct {
			OpaqueID string `json:"opaque_id"`
			Agent    string `json:"agent"`
		} `json:"resources"`
	} `json:"resources"`
}

// bindingPanePresent reads the binding record and asks the binding's own
// tmux server for its panes (read-only, bounded at 2 s). No server on the
// binding's socket is gone; anything that cannot be read is unknown.
func bindingPanePresent(root, handle, resource string, env []string) (present, known bool) {
	raw, err := os.ReadFile(filepath.Join(root, "meta", "launch", "binding.json"))
	if err != nil {
		return false, false
	}
	var b amqBinding
	if json.Unmarshal(raw, &b) != nil || b.Backend != "tmux" {
		return false, false
	}
	socket, ok := strings.CutPrefix(b.InstanceIdentity, "tmux-socket:")
	if !ok || socket == "" {
		return false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "-S", socket, "list-panes", "-a", "-F", "#{pid}\t#{session_id}\t#{session_name}\t#{pane_id}\t#{@amq_pane_agent}")
	cmd.WaitDelay = 500 * time.Millisecond
	if env != nil {
		cmd.Env = env
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := stderr.String()
		if ctx.Err() == nil && (strings.Contains(msg, "no server running") || strings.Contains(msg, "error connecting to")) {
			return false, true // no server on the binding's socket: every pane of it is gone
		}
		return false, false
	}
	return paneInBinding(b, handle, resource, string(out))
}

// paneInBinding decides from one server listing ("pid\tsession_id\t
// session_name\tpane_id\tagent" per line) whether the recorded pane is the
// binding's live resource for the handle. A binding without a session
// resource, or a resource that is not a tmux pane, is unknown.
func paneInBinding(b amqBinding, handle, resource, listing string) (present, known bool) {
	pane, ok := strings.CutPrefix(resource, "tmux:v1:pane:")
	if !ok || pane == "" {
		return false, false
	}
	var sessionID, sessionName, epoch string
	for _, r := range b.Resources.Resources {
		if r.Agent != "" {
			continue
		}
		if rest, ok := strings.CutPrefix(r.OpaqueID, "tmux:v1:session:"); ok {
			sessionID, sessionName, _ = strings.Cut(rest, ":")
		}
		if rest, ok := strings.CutPrefix(r.OpaqueID, "tmux:v1:epoch:"); ok {
			epoch = rest
		}
	}
	if sessionID == "" || sessionName == "" {
		return false, false
	}
	for _, line := range strings.Split(listing, "\n") {
		f := strings.Split(line, "\t")
		if len(f) != 5 {
			continue
		}
		if f[3] == pane && f[4] == handle && f[1] == sessionID && f[2] == sessionName && (epoch == "" || f[0] == epoch) {
			return true, true
		}
	}
	return false, true
}

// rosterOutcome is the scheduler's view of a roster launch: the stage, AMQ's
// reason code and the failure detail, or each seat's state once applied.
func rosterOutcome(res Result, err error) scheduler.RosterOutcome {
	out := scheduler.RosterOutcome{LaunchID: res.LaunchID, Stage: res.Stage, Detail: res.FailureDetail}
	switch {
	case err != nil && res.ApplyStarted:
		out.Stage, out.Detail = "apply_failed", err.Error()
	case err != nil:
		out.Stage, out.Detail = "launch_error", err.Error()
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
