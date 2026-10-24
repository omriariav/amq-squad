package buzz

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
// that lead or as `squad/handle`; the seat's launch record in the squad's
// root must still name a lead role (Roles, default DefaultLeadRoles), so a
// registry entry that went stale never routes to an implementer. The
// message goes to the squad's root through AMQ cross-project routing.
type FleetLeads struct {
	Registry fleet.Registry
	Roles    []string
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
	roles := f.Roles
	if len(roles) == 0 {
		roles = DefaultLeadRoles
	}
	if _, err := (LaunchRecordLeads{Roles: roles}).Lead(s.Root, handle); err != nil {
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

// DefaultLeadRoles are the roles that lead a squad: the catalog's generic
// lead and the cto persona the 2.x teams lead with. A team's configured lead
// role is added by the caller.
var DefaultLeadRoles = []string{"lead", "cto"}

// LaunchRecordLeads answers from the launch record the launch wrote for the
// seat (agents/<handle>/ under the root): its role must be one of Roles.
type LaunchRecordLeads struct{ Roles []string }

func (l LaunchRecordLeads) Lead(root, handle string) (Destination, error) {
	if !handleRe.MatchString(handle) {
		return Destination{}, fmt.Errorf("%q is not an AMQ handle", handle)
	}
	rec, err := launch.Read(filepath.Join(root, "agents", handle))
	if errors.Is(err, os.ErrNotExist) {
		return Destination{}, fmt.Errorf("%s has no launch record in this squad, so it is no lead seat", handle)
	}
	if err != nil {
		return Destination{}, fmt.Errorf("%s: launch record unreadable: %v", handle, err)
	}
	if rec.Handle != "" && rec.Handle != handle {
		return Destination{}, fmt.Errorf("%s: the launch record names handle %s", handle, rec.Handle)
	}
	if !slices.Contains(l.Roles, rec.Role) {
		return Destination{}, fmt.Errorf("%s is a %q seat, not a lead (lead roles: %v)", handle, rec.Role, l.Roles)
	}
	return Destination{Root: root, Handle: handle}, nil
}
