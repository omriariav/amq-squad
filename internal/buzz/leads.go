package buzz

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Destination is where a routed phone message goes: a lead's handle in its
// squad's AMQ root, and the AMQ peer project and session to reach that root
// from another one ("" for the same root).
type Destination struct {
	Root, Project, Session, Handle string
	Squad                          string // the fleet squad, "" in single-squad mode
}

// LeadRegistry is the authority on which handles lead a squad: a phone
// message is routed only to a handle it names as a lead (4-5 review item
// 3). FleetLeads answers from 4-3's fleet registry; LaunchRecordLeads from
// the seats' launch records (single-squad mode).
type LeadRegistry interface {
	// Lead returns the destination when handle is a lead, else the reason
	// it is not. root is the attached mailbox's root.
	Lead(root, handle string) (Destination, error)
}

// FleetLeads is the fleet registry as the lead authority: a destination is
// a squad's registered lead, named as `handle` when exactly one squad has
// that lead or as `squad/handle`; the lead's launch record in the squad's
// root must still carry lead authority (LaunchRecordLeads: its start's
// record, squad.yaml's current lead), so a registry entry that went stale
// never routes to an implementer or a former lead. The message goes to the
// squad's root through AMQ cross-project routing.
type FleetLeads struct {
	Registry fleet.Registry
	Current  CurrentLead
}

// AmbiguousLeadError is a lead handle that more than one squad has.
type AmbiguousLeadError struct {
	Handle string
	Squads []string
}

func (e *AmbiguousLeadError) Error() string {
	return fmt.Sprintf("%s leads more than one squad (%s); name it as squad/%s", e.Handle, strings.Join(e.Squads, ", "), e.Handle)
}

func (f FleetLeads) Lead(root, name string) (Destination, error) {
	squad, handle, qualified := strings.Cut(name, "/")
	if !qualified {
		squad, handle = "", name
	}
	var found []fleet.Squad
	for _, s := range f.Registry.Squads {
		if s.Lead == handle && (squad == "" || s.Name == squad) {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 0:
		return Destination{}, fmt.Errorf("%s is no squad's lead in the fleet registry", name)
	case 1:
	default:
		e := &AmbiguousLeadError{Handle: handle}
		for _, s := range found {
			e.Squads = append(e.Squads, s.Name)
		}
		return Destination{}, e
	}
	s := found[0]
	if _, err := (LaunchRecordLeads{Current: f.Current}).Lead(s.Root, handle); err != nil {
		return Destination{}, fmt.Errorf("the fleet registry names %s the lead of %s, but the seat says otherwise: %w", handle, s.Name, err)
	}
	d := Destination{Root: s.Root, Handle: handle, Squad: s.Name}
	if filepath.Clean(s.Root) != filepath.Clean(root) {
		d.Project, d.Session = s.ProjectName(), s.SessionName()
	}
	return d, nil
}

// FleetDestinations are the names a phone message may use for the fleet's
// leads: squad/handle for every squad, and the bare handle where only one
// squad has it.
func FleetDestinations(r fleet.Registry) []string {
	count := map[string]int{}
	for _, s := range r.Squads {
		if s.Lead != "" {
			count[s.Lead]++
		}
	}
	var out []string
	for _, s := range r.Squads {
		if s.Lead == "" {
			continue
		}
		out = append(out, s.Name+"/"+s.Lead)
		if count[s.Lead] == 1 {
			out = append(out, s.Lead)
		}
	}
	return out
}

// CurrentLead names the lead a squad's squad.yaml names now, read from the
// repository the lead's staged record names (launch.Record.SquadProject),
// at every check.
type CurrentLead func(project string) (string, error)

// LaunchRecordLeads answers from the record the core staged under
// <root>/extensions/amq-squad/launch/<handle>/ (which no seat can write;
// never a record in the seat's own agent directory, C5a): lead authority is
// the record the lead's start staged (SquadLead) for the handle squad.yaml
// names its lead now (3.3.1; launch.Record.HasLeadAuthority). A role id
// ("cto", "lead") alone grants nothing; without Current nothing leads.
type LaunchRecordLeads struct{ Current CurrentLead }

func (l LaunchRecordLeads) Lead(root, handle string) (Destination, error) {
	if !handleRe.MatchString(handle) {
		return Destination{}, fmt.Errorf("%q is not an AMQ handle", handle)
	}
	rec, err := launch.ReadStaged(root, handle)
	if errors.Is(err, os.ErrNotExist) {
		return Destination{}, fmt.Errorf("%s has no launch record in this squad, so it is no lead seat", handle)
	}
	if err != nil {
		return Destination{}, fmt.Errorf("%s: launch record unreadable: %v", handle, err)
	}
	if !rec.SquadLead || rec.SquadProject == "" {
		return Destination{}, fmt.Errorf("%s is a %q seat, not the squad's lead: no lead start staged its record", handle, rec.Role)
	}
	if l.Current == nil {
		return Destination{}, fmt.Errorf("%s: no squad.yaml to name the squad's current lead", handle)
	}
	cur, err := l.Current(rec.SquadProject)
	if err != nil {
		return Destination{}, fmt.Errorf("%s: the squad's current lead: %v", handle, err)
	}
	if !rec.HasLeadAuthority(cur) {
		return Destination{}, fmt.Errorf("%s is no longer the squad's lead: %s names %s", handle, filepath.Join(rec.SquadProject, "squad.yaml"), cur)
	}
	return Destination{Root: root, Handle: handle}, nil
}
