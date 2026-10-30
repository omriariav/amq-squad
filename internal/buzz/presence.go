package buzz

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/status"
)

// Presence is what the phone shows for the bound session, derived from the
// status document (plan 3.6; the status contract, docs/status-contract.md).
type Presence struct {
	Handle   string        `json:"handle"`
	Present  bool          `json:"present"`
	State    status.State  `json:"state"` // the bound seat's classifier state, "" when it has no row
	Activity string        `json:"activity"`
	Squads   int           `json:"squads"`
	Counts   status.Counts `json:"counts"`
	Gates    []string      `json:"open_gates"`
	// GeneratedAt is when the status document was produced (the fleet
	// header's, else the newest squad document's); a stale document is a
	// missed status read for the attachment (4-5 re-pass).
	GeneratedAt time.Time `json:"generated_at"`
}

// seatPresent reports whether a seat row is a running session: the row's
// live field (status contract, schema 1), which an open gate or a block
// never overrides, unlike the row's state.
func seatPresent(s status.Seat) bool { return s.Live }

// Documents parses a status document: one squad's schema 1 document, or a
// fleet document (briefs lead ruling 16: a document with `squads` is a
// fleet; anything else is one squad).
func Documents(raw []byte) ([]status.Status, error) {
	var probe struct {
		SchemaVersion int               `json:"schema_version"`
		Squads        []json.RawMessage `json:"squads"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("status document: %w", err)
	}
	if probe.SchemaVersion != status.SchemaVersion {
		return nil, fmt.Errorf("status document: schema_version %d is not %d", probe.SchemaVersion, status.SchemaVersion)
	}
	parts := []json.RawMessage{raw}
	if probe.Squads != nil {
		parts = probe.Squads
	}
	out := make([]status.Status, 0, len(parts))
	for i, p := range parts {
		var st status.Status
		if err := json.NewDecoder(bytes.NewReader(p)).Decode(&st); err != nil {
			return nil, fmt.Errorf("status document squad %d: %w", i, err)
		}
		if st.SchemaVersion != status.SchemaVersion {
			return nil, fmt.Errorf("status document squad %d: schema_version %d is not %d", i, st.SchemaVersion, status.SchemaVersion)
		}
		out = append(out, st)
	}
	return out, nil
}

// FromStatus derives the bound session's presence and the fleet's activity
// line from a status document.
func FromStatus(raw []byte, handle string) (Presence, error) {
	docs, err := Documents(raw)
	if err != nil {
		return Presence{Handle: handle}, err
	}
	// the fleet form carries the operator session's own row (the user
	// handle, which squad documents skip; 4-3)
	var probe struct {
		Operator    *status.Seat `json:"operator"`
		GeneratedAt time.Time    `json:"generated_at"`
	}
	_ = json.Unmarshal(raw, &probe)
	p := derive(docs, probe.Operator, handle)
	p.GeneratedAt = probe.GeneratedAt
	for _, d := range docs {
		if d.GeneratedAt.After(p.GeneratedAt) && probe.GeneratedAt.IsZero() {
			p.GeneratedAt = d.GeneratedAt
		}
	}
	return p, nil
}

func derive(docs []status.Status, operator *status.Seat, handle string) Presence {
	p := Presence{Handle: handle, Squads: len(docs)}
	var seat *status.Seat
	if operator != nil && operator.Handle == handle {
		seat = operator
	}
	for i := range docs {
		d := &docs[i]
		p.Counts.All += d.Counts.All
		p.Counts.NeedsYou += d.Counts.NeedsYou
		p.Counts.Working += d.Counts.Working
		p.Counts.Ready += d.Counts.Ready
		p.Counts.Inactive += d.Counts.Inactive
		for _, g := range d.Gates {
			if g.State == "open" {
				p.Gates = append(p.Gates, g.ID)
			}
		}
		for j := range d.Seats {
			s := &d.Seats[j]
			if s.Handle != handle {
				continue
			}
			// a present row wins over another row of the same handle
			if seat == nil || (!seatPresent(*seat) && seatPresent(*s)) {
				seat = s
			}
		}
	}
	sort.Strings(p.Gates)
	if seat != nil {
		p.State = seat.State
		p.Present = seatPresent(*seat)
	}
	p.Activity = activityLine(p, seat)
	return p
}

// activityLine is the one line the phone gets for /status: the bound
// session, the bucket counts over the fleet, the open gates, and the bound
// seat's last message.
func activityLine(p Presence, seat *status.Seat) string {
	var b strings.Builder
	switch {
	case seat == nil:
		fmt.Fprintf(&b, "%s: no status row", p.Handle)
	case p.Present:
		fmt.Fprintf(&b, "%s: present (%s, %s)", p.Handle, seat.State, seat.PromptState)
	default:
		fmt.Fprintf(&b, "%s: not present (%s)", p.Handle, seat.State)
	}
	squads := ""
	if p.Squads > 1 {
		squads = fmt.Sprintf(" over %d squads", p.Squads)
	}
	fmt.Fprintf(&b, ". needs you %d, working %d, ready %d, inactive %d%s.", p.Counts.NeedsYou, p.Counts.Working, p.Counts.Ready, p.Counts.Inactive, squads)
	if len(p.Gates) > 0 {
		fmt.Fprintf(&b, " open gates: %s.", strings.Join(p.Gates, ", "))
	}
	if seat != nil && seat.LastMessage != "" {
		msg := seat.LastMessage
		if r := []rune(msg); len(r) > 120 {
			msg = string(r[:120]) + "..."
		}
		fmt.Fprintf(&b, " last: %s", strings.Join(strings.Fields(msg), " "))
	}
	return b.String()
}
