package buzz

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/omriariav/amq-squad/v3/internal/launch"
)

// LeadRegistry is the authority on which handles lead a squad: a phone
// message is routed only to a handle it names as a lead (4-5 review item
// 3). 4-3's fleet registry implements it once merged; until then
// LaunchRecordLeads reads the seats' roles from their launch records.
type LeadRegistry interface {
	// Lead returns nil when handle is a lead seat in root, else the reason
	// it is not.
	Lead(root, handle string) error
}

// DefaultLeadRoles are the roles that lead a squad: the catalog's generic
// lead and the cto persona the 2.x teams lead with. A team's configured lead
// role is added by the caller.
var DefaultLeadRoles = []string{"lead", "cto"}

// LaunchRecordLeads answers from the launch record the launch wrote for the
// seat (agents/<handle>/ under the root): its role must be one of Roles.
type LaunchRecordLeads struct{ Roles []string }

func (l LaunchRecordLeads) Lead(root, handle string) error {
	if !handleRe.MatchString(handle) {
		return fmt.Errorf("%q is not an AMQ handle", handle)
	}
	rec, err := launch.Read(filepath.Join(root, "agents", handle))
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s has no launch record in this squad, so it is no lead seat", handle)
	}
	if err != nil {
		return fmt.Errorf("%s: launch record unreadable: %v", handle, err)
	}
	if rec.Handle != "" && rec.Handle != handle {
		return fmt.Errorf("%s: the launch record names handle %s", handle, rec.Handle)
	}
	if !slices.Contains(l.Roles, rec.Role) {
		return fmt.Errorf("%s is a %q seat, not a lead (lead roles: %v)", handle, rec.Role, l.Roles)
	}
	return nil
}
