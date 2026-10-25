package fleet

import (
	"sort"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/status"
)

// WarningRootUnavailable prefixes the warning a fleet squad document
// carries when its registered root is gone (Codex review, P2 7).
const WarningRootUnavailable = "root_unavailable"

// Collector builds one squad's schema 1 status document (the single-squad
// producer: internal/cli's status contract over the squad's root).
type Collector func(Squad) status.Status

// Status is `status --fleet --json` (plan 16.3, ruling 16): one schema 1
// document per squad of the registry, in registry order, unchanged except
// that fleet.yaml's lead handle fills lead_handle when the squad's own
// producer left it null.
func Status(r Registry, collect Collector, version string, now time.Time) status.Fleet {
	squads := make([]status.Status, 0, len(r.Squads))
	for _, s := range r.Squads {
		st := collect(s)
		if st.LeadHandle == nil && s.Lead != "" {
			lead := s.Lead
			st.LeadHandle = &lead
		}
		squads = append(squads, st)
	}
	var waiting []status.FleetWaiting
	for _, w := range r.Waiting {
		since, _ := time.Parse(time.RFC3339, w.Since) // Validate checked it
		reason := status.WaitReasonFleetCap
		if w.Cap == ReasonCostUnmetered {
			reason = status.WaitReasonCostUnmetered // the status says why: the cap has no meter
		}
		waiting = append(waiting, status.FleetWaiting{Squad: w.Name, Repository: w.Repository, ProjectPath: w.ProjectPath,
			WaitReason: reason, Cap: w.Cap, Since: since.UTC(), Detail: w.Detail})
	}
	return status.BuildFleet(version, now, squads, waiting)
}

// Gate is one open gate of the fleet with its squad.
type Gate struct {
	Squad string      `json:"squad"`
	Root  string      `json:"root"`
	Gate  status.Gate `json:"gate"`
	Since time.Time   `json:"since"`
}

// Question is one lead question to the owner (an AMQ thread that waits on
// the owner) with its squad.
type Question struct {
	Squad     string           `json:"squad"`
	Attention status.Attention `json:"attention"`
	// Reply is the relay argv: the operator's answer to the asking seat over
	// AMQ cross-project routing; the renderer appends the body.
	Reply []string `json:"reply"`
}

// Inbox is the manual-mode view (plan 3.5): every open gate of every squad in
// one list, and the lead questions to relay or answer. A typed gate carries
// no answer action: only the owner answers it, with a signature (R41).
type Inbox struct {
	Gates     []Gate     `json:"gates"`
	Questions []Question `json:"questions"`
}

// GroupInbox builds the inbox from a fleet document of r. Gates sort oldest
// first, then by squad and id.
func GroupInbox(r Registry, f status.Fleet) Inbox {
	in := Inbox{Gates: []Gate{}, Questions: []Question{}}
	for i, st := range f.Squads {
		name := squadName(r, st, i)
		since := map[string]time.Time{}
		for _, a := range st.Attention {
			if a.Kind == "gate" {
				since[a.GateID] = a.Since
			}
			if a.Kind == "question" {
				q := Question{Squad: name, Attention: a}
				if s, ok := r.Find(name); ok && r.Operator.Root != "" {
					q.Reply = RelayArgv(r.Operator, s, a.Seat, threadOf(a), "")
				}
				in.Questions = append(in.Questions, q)
			}
		}
		for _, g := range st.Gates {
			if g.State == "open" {
				in.Gates = append(in.Gates, Gate{Squad: name, Root: st.Root, Gate: g, Since: since[g.ID]})
			}
		}
	}
	sort.SliceStable(in.Gates, func(i, j int) bool {
		a, b := in.Gates[i], in.Gates[j]
		if !a.Since.Equal(b.Since) {
			return a.Since.Before(b.Since)
		}
		if a.Squad != b.Squad {
			return a.Squad < b.Squad
		}
		return a.Gate.ID < b.Gate.ID
	})
	return in
}

// squadName finds the registry name of a fleet squad by root, else by
// position.
func squadName(r Registry, st status.Status, i int) string {
	for _, s := range r.Squads {
		if s.Root == st.Root {
			return s.Name
		}
	}
	if i < len(r.Squads) {
		return r.Squads[i].Name
	}
	return st.Session
}

func threadOf(a status.Attention) string {
	const prefix = "thread:"
	if len(a.ID) > len(prefix) && a.ID[:len(prefix)] == prefix {
		return a.ID[len(prefix):]
	}
	return ""
}
