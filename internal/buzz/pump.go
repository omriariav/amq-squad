package buzz

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// Backlog is how far back the first step reads the DM channel: a message
// the owner sent while no attachment ran is delivered when one starts, once.
const Backlog = 24 * time.Hour

// Pump reads the owner's DMs to the Buzz agent and acts on each exactly once:
// a gate answer goes to the 3-1 core (verified, committed, or re-asked in the
// gate thread); "/status" is answered in its thread from the status document;
// any other message is delivered into the bound mailbox and, when it names a
// lead, routed on to that lead through AMQ.
type Pump struct {
	Core *gateverify.Core // Relay, Pin, Body and BodyGrant set (Connect)
	// Cores are the gate stores of the other squads (operator mode): an
	// answer goes to the core whose store holds its gate; Core otherwise.
	Cores  []*gateverify.Core
	Mail   Mailer
	Target Target
	Status func() ([]byte, error) // the status document (status --json)
	Marks  string                 // directory of the once-only marks
	Now    func() time.Time

	since       int64
	quarantined map[string]bool
}

// Handled is what the pump did with one owner event.
type Handled struct {
	EventID  string             `json:"event_id"`
	Kind     string             `json:"kind"` // gate | status | route | error | quarantined
	Gate     *gateverify.Result `json:"gate,omitempty"`
	Delivery *Delivery          `json:"delivery,omitempty"`
	Reply    string             `json:"reply,omitempty"`
	Error    string             `json:"error,omitempty"`
}

// MarksDir is the once-only mark directory of a DM channel, under the share
// root: one Buzz agent reads one channel, so a DM is handled once whichever
// mailbox the agent is attached to at the time (a lead now, the operator
// after 4-3). guard protects the tree (~/.amq-squad/buzz/).
func MarksDir(shareRoot, channel string) string {
	return filepath.Join(shareRoot, "attach", "seen", strings.ReplaceAll(channel, ":", "_"))
}

func (p *Pump) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

// Step reads the channel once and handles every owner event it has not
// handled before, oldest first.
func (p *Pump) Step(ctx context.Context) ([]Handled, error) {
	c := p.Core
	if c == nil || c.Relay == nil {
		return nil, errors.New("the pump needs a connected core")
	}
	if p.since == 0 {
		p.since = p.now().Add(-Backlog).Unix()
	}
	if err := os.MkdirAll(p.Marks, 0o700); err != nil {
		return nil, err
	}
	raws, err := c.Relay.Query(ctx, gateverify.Filter{Kinds: []int{KindDM}, Authors: []string{c.Pin.Owner}, Tags: map[string][]string{"h": {c.Pin.Channel}}, Since: p.since})
	if err != nil {
		return nil, err
	}
	type item struct {
		raw []byte
		e   nostr.Event
	}
	var items []item
	for _, raw := range raws {
		e, err := nostr.ParseEvent(raw)
		if err != nil || !e.CheckID() || e.PubKey != c.Pin.Owner || e.Kind != KindDM || e.VerifySignature() != nil {
			continue // a relay can withhold but not forge; a bad event is not the owner's
		}
		if !slicesEqual(nostr.TagValues(e, "h"), []string{c.Pin.Channel}) {
			continue
		}
		items = append(items, item{raw, e})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].e.CreatedAt != items[j].e.CreatedAt {
			return items[i].e.CreatedAt < items[j].e.CreatedAt
		}
		return items[i].e.ID < items[j].e.ID
	})
	var out []Handled
	horizon := p.now().Add(FutureSkew).Unix()
	for _, it := range items {
		// a DM dated more than FutureSkew ahead (a phone clock a day fast) is
		// quarantined: reported once, not handled, and it never moves the
		// cursor, so it cannot hide the messages after it (4-5 review item 5;
		// the verifier's created_at window)
		if it.e.CreatedAt > horizon {
			if p.quarantined == nil {
				p.quarantined = map[string]bool{}
			}
			if !p.quarantined[it.e.ID] {
				p.quarantined[it.e.ID] = true
				out = append(out, Handled{EventID: it.e.ID, Kind: "quarantined", Error: fmt.Sprintf("created_at %d is more than %s ahead of this clock; not handled", it.e.CreatedAt, FutureSkew)})
			}
			continue
		}
		// once only: the mark is taken before acting, so a message is never
		// delivered twice (at most once, like amq-acp's redelivery rule).
		// Only an existing mark is a duplicate; any other failure stops the
		// step before this event and before the cursor passes it (item 4).
		fresh, err := p.mark(it.e.ID)
		if err != nil {
			return out, fmt.Errorf("once-only mark for %s: %w (not handled; retried next step)", it.e.ID, err)
		}
		// the next read starts SinceSlack before the newest event claimed;
		// the marks keep the overlap from being handled twice
		if s := it.e.CreatedAt - int64(SinceSlack/time.Second); s > p.since {
			p.since = s
		}
		if fresh {
			out = append(out, p.handle(ctx, it.raw, it.e))
		}
	}
	return out, nil
}

// SinceSlack is how far before the newest handled event the next read
// starts, for events a relay stores late.
const SinceSlack = 5 * time.Minute

// FutureSkew is how far ahead of this clock an owner DM may be dated: the
// verifier's presentation window (created_at - 60 s <= now, plan 7.3).
const FutureSkew = 60 * time.Second

// mark claims an event: true for a new claim, false for an existing mark,
// an error for anything else (a full disk, a permission change).
func (p *Pump) mark(id string) (bool, error) {
	f, err := os.OpenFile(filepath.Join(p.Marks, id), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := f.Close(); err != nil {
		return false, err
	}
	return true, nil
}

func (p *Pump) handle(ctx context.Context, raw []byte, e nostr.Event) Handled {
	h := Handled{EventID: e.ID}
	content := strings.TrimSpace(e.Content)
	switch {
	case isGateAnswer(content):
		h.Kind = "gate"
		r := p.coreFor(content).HandleEvent(ctx, raw)
		h.Gate = &r
		switch r.Outcome {
		case "approved", "denied":
			h.Reply = fmt.Sprintf("amq-squad gate %s: %s, verified by the core.", r.GateID, r.Outcome)
		case "rejected":
			h.Reply = fmt.Sprintf("amq-squad gate %s: answer not accepted (%s).", r.GateID, r.Code)
		}
	case strings.EqualFold(content, "/status"):
		h.Kind = "status"
		h.Reply = p.activity()
	default:
		h.Kind = "route"
		d, err := Deliver(ctx, p.Mail, p.Target, e.ID, content)
		if err != nil {
			// the detail (paths, AMQ errors) stays on this machine; the DM is
			// clear text on the relay
			h.Kind, h.Error = "error", err.Error()
			h.Reply = "not delivered; send it again, or see amq-squad remote attach on the Mac."
			break
		}
		h.Delivery = &d
		h.Reply = "delivered to " + d.Mailbox
		if d.Lead != "" {
			h.Reply += ", routed to " + d.Lead
		}
		if d.Refused != "" {
			h.Reply += "; not routed on: the name is not a lead seat"
		}
	}
	if h.Reply != "" {
		if err := p.reply(ctx, e.ID, h.Reply); err != nil && h.Error == "" {
			h.Error = "reply: " + err.Error()
		}
	}
	return h
}

// coreFor is the core whose gate store holds the answer's gate: in operator
// mode each squad's gates live in that squad's root.
func (p *Pump) coreFor(content string) *gateverify.Core {
	a, err := gatestore.ParseAnswer(content)
	if err != nil {
		return p.Core
	}
	for _, c := range append([]*gateverify.Core{p.Core}, p.Cores...) {
		if _, err := c.Store.Load(a.GateID); err == nil {
			return c
		}
	}
	return p.Core
}

// isGateAnswer reports whether a message is meant as a gate answer; the core
// decides everything else about it.
// A message whose first word is approve or deny always goes to the core, so
// a malformed answer is refused there (bad_tuple) and never routed to a lead.
func isGateAnswer(content string) bool {
	if _, err := gatestore.ParseAnswer(content); err == nil {
		return true
	}
	first, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(content)), " ")
	return first == "approve" || first == "deny"
}

func (p *Pump) activity() string {
	if p.Status == nil {
		return "no status document"
	}
	raw, err := p.Status()
	if err != nil {
		return "status unavailable: " + err.Error()
	}
	pr, err := FromStatus(raw, p.Target.Handle)
	if err != nil {
		return "status unavailable: " + err.Error()
	}
	return pr.Activity
}

// reply posts a body-signed kind 9 into the owner's event's thread.
func (p *Pump) reply(ctx context.Context, to, text string) error {
	c := p.Core
	if c.Body.PublicHex() != c.Pin.Body {
		return errors.New("the body key is not the pinned body")
	}
	tags := [][]string{{"h", c.Pin.Channel}, {"p", c.Pin.Owner}, {"e", to, "", "reply"}}
	if c.BodyGrant != nil {
		tags = append(tags, c.BodyGrant)
	}
	evt := nostr.Event{CreatedAt: p.now().Unix(), Kind: KindDM, Tags: tags, Content: text}
	if err := c.Body.Sign(&evt); err != nil {
		return err
	}
	return c.Relay.Publish(ctx, evt.Marshal())
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
