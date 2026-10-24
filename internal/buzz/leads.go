package buzz

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// Destination is where a routed phone message goes: a lead's handle in its
// squad's AMQ root, and the AMQ peer project and session to reach that root
// from another one ("" for the same root).
type Destination struct {
	Root, Project, Session, Handle string
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

// FleetLeads is the fleet registry as the lead authority: a handle is a lead
// when a registered squad names it as its lead, and the message goes to that
// squad's root through AMQ cross-project routing.
type FleetLeads struct{ Registry fleet.Registry }

func (f FleetLeads) Lead(root, handle string) (Destination, error) {
	for _, s := range f.Registry.Squads {
		if s.Lead == handle {
			d := Destination{Root: s.Root, Handle: handle}
			if filepath.Clean(s.Root) != filepath.Clean(root) {
				d.Project, d.Session = s.ProjectName(), s.SessionName()
			}
			return d, nil
		}
	}
	return Destination{}, fmt.Errorf("%s is no squad's lead in the fleet registry", handle)
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
