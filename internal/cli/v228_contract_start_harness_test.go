package cli

import (
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// Shared hermetic harness for the tranche-2 criteria that drive `start`
// (AC11 legacy-namespace recovery, AC12 roster changes).
//
// Names are deliberately distinct from the tranche-1 crash-injection file's
// v228Crash* helpers: that file is behind the v228seam build tag until step 3
// removes it, and both files must coexist in one package afterward.

func v228StartMembers(session string, roles ...string) []team.Member {
	members := make([]team.Member, 0, len(roles))
	for _, role := range roles {
		members = append(members, team.Member{Role: role, Binary: "codex", Handle: role, Session: session})
	}
	return members
}
