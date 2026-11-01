package gateverify

import (
	"regexp"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// The Buzz door of R55 (brief 31-3, section 2 (b)): the owner confirms a
// team the lead proposed with a signed Buzz DM. The mailbox handler takes it
// on the typed path a gate answer takes: the event is fetched by its id on
// the verifier's own connection and acted on only when it is the owner's
// (the id recomputes, the owner's pinned key signed it, the DM kind, the
// pinned channel), never on serve's copy. Then the confirmer writes the
// confirmation record with the event id as its proof.
//
// Two forms. Since 3.1.1 (brief 3.1.1-1) the owner replies a bare yes to
// the post that relayed the lead's plan (brief 31-4): its direct reply
// target is the binding, as a gate's thread is, and the relay record, which
// marks that post as the plan for one proposal, says which. The 3.1.0 form, `yes <16-hex prefix>` (R32's typed-prefix
// length), keeps verifying, but no text asks for it.

// teamYesRE is the whole message: yes and 16 lowercase hex.
var teamYesRE = regexp.MustCompile(`^yes ([0-9a-f]{16})$`)

// ParseTeamYes returns the prefix of a team confirmation in the 3.1.0 form,
// ok false when the message is not one.
func ParseTeamYes(content string) (string, bool) {
	m := teamYesRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(content)))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// EventRefs are every event id an event's tags name, whatever the marker:
// each e tag (reply, root, mention, unmarked or any other), each E tag
// (NIP-22's root) and each q tag (a quote), when its value is 64 hex. The
// team door's conflict checks read these, not nostr.ThreadRefs, so a
// reference marked "mention" cannot hide (Codex re-check on #838).
func EventRefs(e nostr.Event) []string {
	var out []string
	for _, t := range e.Tags {
		if len(t) >= 2 && (t[0] == "e" || t[0] == "E" || t[0] == "q") && nostr.IsHex64(t[1]) && !contains(out, t[1]) {
			out = append(out, t[1])
		}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TeamPlanLabel prefixes the AMQ label the lead's process puts on the
// cockpit reply that carries its team plan: team-plan:<the proposal's
// content digest, 64 hex>. The relay records the label's digest on the post
// (status.RelayPost.PlanFor); no post text is read for it.
const TeamPlanLabel = "team-plan:"

// PlanPost is the relay's post of the lead's team plan a bare yes replies
// to: its raw bytes, fetched on the verifier's connection, and the proposal
// the relay record marks it with (64 hex).
type PlanPost struct {
	Raw []byte
	For string
}

// TeamConfirmer hands the raw bytes of the owner-signed event (as the
// handler fetched them on its own connection) to the lead of the one recent
// proposal it confirms; the lead verifies the bytes itself with no
// connection and witnesses the confirmation. For the 3.1.0 form prefix is
// the typed prefix and plan is nil; for a bare yes prefix is "" and plan is
// the recorded plan post the yes replies to, which the lead verifies too. It
// returns the confirmed digest, or a refusal whose text starts with its
// name.
type TeamConfirmer func(prefix string, raw []byte, plan *PlanPost) (string, error)
