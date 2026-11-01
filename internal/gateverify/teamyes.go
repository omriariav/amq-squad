package gateverify

import (
	"regexp"
	"strings"
)

// The Buzz door of R55 (brief 31-3, section 2 (b)): the owner confirms a
// team the lead proposed with a signed Buzz DM `yes <16-hex prefix>`, the
// prefix of the proposal's digest (R32's typed-prefix length). The mailbox
// handler takes it on the typed path a gate answer takes: the event is
// fetched by its id on the verifier's own connection and acted on only when
// it is the owner's (the id recomputes, the owner's pinned key signed it,
// the DM kind, the pinned channel), never on serve's copy. Then the
// confirmer writes the confirmation record with the event id as its proof.

// teamYesRE is the whole message: yes and 16 lowercase hex.
var teamYesRE = regexp.MustCompile(`^yes ([0-9a-f]{16})$`)

// ParseTeamYes returns the prefix of a team confirmation, ok false when
// the message is not one.
func ParseTeamYes(content string) (string, bool) {
	m := teamYesRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(content)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// TeamConfirmer hands the raw bytes of the owner-signed event (as the
// handler fetched them on its own connection) to the lead of the one recent
// proposal whose digest starts with prefix; the lead verifies the bytes
// itself with no connection and witnesses the confirmation. It returns the
// confirmed digest, or a refusal whose text starts with its name.
type TeamConfirmer func(prefix string, raw []byte) (string, error)
