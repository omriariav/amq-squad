package gateverify

import (
	"errors"
	"regexp"
	"strings"
)

// The Buzz door of R55 (brief 31-3, section 2 (b)): the owner confirms a
// team the lead proposed with a signed Buzz DM. The mailbox handler takes it
// on the typed path a gate answer takes: the event is fetched by its id on
// the verifier's own connection and acted on only when it is the owner's
// (the id recomputes, the owner's pinned key signed it, the DM kind, the
// pinned channel), never on serve's copy. Then the confirmer writes the
// confirmation record with the event id as its proof.
//
// Two forms. Since 3.1.1 (brief 3.1.1-1) the owner replies a bare yes in
// the thread of the post that relayed the lead's plan (brief 31-4): the
// thread is the binding, as for a gate, and the post's own bytes (the share
// body's signed kind 9, its content naming the proposal's digest) say which
// proposal. The 3.1.0 form, `yes <16-hex prefix>` (R32's typed-prefix
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

// PlanPrefixes are the proposal prefixes a relayed post names: the first 16
// hex of each word of 16 to 64 lowercase hex (a digest, its prefix), once
// each, in order. A post that names none is no plan.
func PlanPrefixes(content string) []string {
	var out []string
	words := strings.FieldsFunc(content, func(r rune) bool {
		return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	})
	for _, w := range words {
		if len(w) < 16 || len(w) > 64 || strings.Trim(w, "0123456789abcdef") != "" {
			continue
		}
		if p := w[:16]; !contains(out, p) {
			out = append(out, p)
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

// ErrNotAPlan is a bare yes whose post names no recent proposal: the yes is
// no team confirmation, and the handler goes on as for any reply to that
// post (it continues the request).
var ErrNotAPlan = errors.New("not_a_plan")

// TeamConfirmer hands the raw bytes of the owner-signed event (as the
// handler fetched them on its own connection) to the lead of the one recent
// proposal it names; the lead verifies the bytes itself with no connection
// and witnesses the confirmation. For the 3.1.0 form prefix is the typed
// prefix and post is nil; for a bare yes prefix is "" and post is the raw
// bytes of the relayed plan post the yes replies to (the handler fetched it
// on the same connection), which the lead verifies too. It returns the
// confirmed digest, or a refusal whose text starts with its name
// (ErrNotAPlan when the post names no recent proposal).
type TeamConfirmer func(prefix string, raw, post []byte) (string, error)
