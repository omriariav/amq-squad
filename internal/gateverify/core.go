package gateverify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Core is the Go core's gate flow over one transport binding: it raises
// gates (record first, then the body-signed question), and it turns an owner
// event into a verified, committed answer, a re-ask, or a typed refusal. No
// model process holds any of its keys: the body key signs only the core's own
// questions and re-asks, the issuer key only envelopes, and the owner key is
// never here (R33).
type Core struct {
	Store     gatestore.Store
	Pin       Pin
	Relay     Relay
	RelayURL  string
	Body      nostr.SecretKey // the share body key; its public key is Pin.Body
	BodyGrant []string        // the owner's NIP-OA tag for kind 9 posts, nil when the relay needs none
	Issuer    operatorauth.AuthorizationSigner
	Namespace Namespace
	Version   string
	// Insecure is set when the owner grants file is present but untrusted:
	// an approval is then refused (owner_grants_insecure) and not consumed;
	// a deny still closes the gate.
	Insecure bool
	// Carriers are the AMQ handles whose mailbox messages may carry a
	// nostr:<event id> label (the binding's carrier, e.g. buzz).
	Carriers []string
	Now      func() time.Time
}

func (c *Core) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Identity is the record identity this core raises gates with.
func (c *Core) Identity() gatestore.Identity {
	return gatestore.Identity{OwnerPubKey: c.Pin.Owner, IssuerKeyID: c.Issuer.KeyID()}
}

// Raise writes the gate record and then posts the question (plan 7.3).
func (c *Core) Raise(ctx context.Context, spec gatestore.Spec) (gatestore.Record, gatestore.Question, error) {
	return c.Store.Raise(spec, c.Identity(), c.now(), questionSender{ctx: ctx, c: c})
}

type questionSender struct {
	ctx context.Context
	c   *Core
}

// PostQuestion signs the record's text as a kind 9 in the DM channel with the
// body key, the tags amq-remote serve puts on a row (h, p, NIP-OA grant).
func (s questionSender) PostQuestion(rec gatestore.Record) (gatestore.Question, error) {
	evt, err := s.c.post(s.ctx, rec.Text, nil)
	if err != nil {
		return gatestore.Question{}, err
	}
	return gatestore.Question{GateID: rec.GateID, EventID: evt.ID, RawEvent: string(evt.Marshal()), Relay: s.c.RelayURL,
		Channel: s.c.Pin.Channel, PostedAt: time.Unix(evt.CreatedAt, 0).UTC().Format(time.RFC3339)}, nil
}

func (c *Core) post(ctx context.Context, content string, extra [][]string) (nostr.Event, error) {
	if c.Body.PublicHex() != c.Pin.Body {
		return nostr.Event{}, errors.New("the body key is not the pinned body")
	}
	tags := [][]string{{"h", c.Pin.Channel}, {"p", c.Pin.Owner}}
	tags = append(tags, extra...)
	if c.BodyGrant != nil {
		tags = append(tags, c.BodyGrant)
	}
	evt := nostr.Event{CreatedAt: c.now().Unix(), Kind: 9, Tags: tags, Content: content}
	if err := c.Body.Sign(&evt); err != nil {
		return nostr.Event{}, err
	}
	if err := c.Relay.Publish(ctx, evt.Marshal()); err != nil {
		return nostr.Event{}, fmt.Errorf("publish: %w", err)
	}
	return evt, nil
}

// Result is what the core did with one owner event.
type Result struct {
	GateID          string `json:"gate_id,omitempty"`
	EventID         string `json:"event_id,omitempty"`
	Outcome         string `json:"outcome"` // approved | denied | reasked | rejected
	Code            string `json:"code,omitempty"`
	Detail          string `json:"detail,omitempty"`
	AuthorizationID string `json:"authorization_id,omitempty"`
	ReaskEventID    string `json:"reask_event_id,omitempty"`
}

func rejected(gateID, eventID string, err error) Result {
	code := Code(err)
	if code == "" {
		code = "error"
	}
	return Result{GateID: gateID, EventID: eventID, Outcome: "rejected", Code: code, Detail: err.Error()}
}

// ReaskText is the re-post in the gate thread (amendment 19).
func ReaskText(a gatestore.Answer) string {
	return "amq-squad gate " + a.GateID + ": that answer was not in this thread. Reply here to confirm, with one line:\n" +
		strings.Join([]string{a.Verb, a.GateID, a.Class, a.DigestPrefix}, " ")
}

// HandleEvent processes one owner event exactly as fetched. In order: the
// tuple names a stored gate; a replay is already_consumed; Verify (pin,
// signature, kind, channel, tuple, digest prefix against the record, window,
// question, thread); a non-thread answer that matches the open gate gets the
// gate's one re-ask in the thread and is not accepted (amendment 19), and a
// second non-thread answer is refused; the question's history (edits,
// deletions) is clean; then the answer file is written and consumed.
func (c *Core) HandleEvent(ctx context.Context, raw []byte) Result {
	e, err := nostr.ParseEvent(raw)
	if err != nil {
		return rejected("", "", reject(RejectMalformed, "%v", err))
	}
	a, err := gatestore.ParseAnswer(e.Content)
	if err != nil {
		return rejected("", e.ID, reject(RejectBadTuple, "%v", err))
	}
	rec, err := c.Store.Load(a.GateID)
	if err != nil {
		return rejected(a.GateID, e.ID, reject(RejectGateRecord, "%v", err))
	}
	if err := c.Store.Replayed(rec.GateID, e.ID); err != nil {
		return rejected(rec.GateID, e.ID, err)
	}
	q, err := c.Store.Question(rec.GateID)
	if err != nil {
		return rejected(rec.GateID, e.ID, reject(RejectQuestion, "%v", err))
	}
	reask, err := c.Store.ReadReask(rec.GateID)
	if err != nil {
		return rejected(rec.GateID, e.ID, reject(RejectQuestion, "%v", err))
	}
	now := c.now()
	o, err := Verify(raw, rec, q, reask, c.Pin, now)
	if Code(err) == RejectThread {
		if reask != nil {
			return rejected(rec.GateID, e.ID, reject(RejectThread, "the gate was re-asked in its thread (%s); this answer is still outside it", reask.EventID))
		}
		return c.reask(ctx, rec, q, o, now)
	}
	if err != nil {
		return rejected(rec.GateID, e.ID, err)
	}
	if o.Decision == "approve" && c.Insecure {
		return rejected(rec.GateID, e.ID, reject(RejectInsecure, "the owner grants file is present but untrusted; an approval is not accepted until it is fixed"))
	}
	qe, err := nostr.ParseEvent([]byte(q.RawEvent))
	if err != nil {
		return rejected(rec.GateID, e.ID, reject(RejectQuestion, "%v", err))
	}
	if err := CheckHistory(ctx, c.Relay, qe, c.Pin.Owner); err != nil {
		return rejected(rec.GateID, e.ID, err)
	}
	f, err := BuildAnswerFile(o, raw, rec, *q, c.Pin, c.RelayURL, c.Namespace, c.Version, c.Issuer, now)
	if err != nil {
		return rejected(rec.GateID, e.ID, err)
	}
	if err := c.Store.Commit(f); err != nil {
		return rejected(rec.GateID, e.ID, err)
	}
	return Result{GateID: rec.GateID, EventID: e.ID, Outcome: f.Decision, AuthorizationID: f.Envelope.Payload.AuthorizationID}
}

// reask posts the gate's one "reply here to confirm" in the gate thread and
// records it. The answer that triggered it is not accepted.
func (c *Core) reask(ctx context.Context, rec gatestore.Record, q *gatestore.Question, o Outcome, now time.Time) Result {
	if c.Store.State(rec, now) != gatestore.StateOpen {
		return rejected(rec.GateID, o.Event.ID, reject(RejectThread, "the answer is outside the gate thread and the gate is no longer open"))
	}
	evt, err := c.post(ctx, ReaskText(o.Answer), [][]string{{"e", q.EventID, c.RelayURL, "root"}})
	if err != nil {
		return rejected(rec.GateID, o.Event.ID, fmt.Errorf("re-ask: %w", err))
	}
	r := gatestore.Reask{GateID: rec.GateID, TriggerEventID: o.Event.ID, EventID: evt.ID, RawEvent: string(evt.Marshal()), At: now.UTC().Format(time.RFC3339)}
	if err := c.Store.PutReask(r); err != nil {
		return rejected(rec.GateID, o.Event.ID, fmt.Errorf("record the re-ask: %w", err))
	}
	return Result{GateID: rec.GateID, EventID: o.Event.ID, Outcome: "reasked", Code: RejectThread, ReaskEventID: evt.ID,
		Detail: "the answer was not in the gate thread; re-asked there (reply here to confirm)"}
}

// MailboxMessage is the part of an AMQ mailbox message the label path reads.
type MailboxMessage struct {
	ID     string
	From   string
	Labels []string
}

// LabelEventID returns the one nostr:<event id> label of a message.
func LabelEventID(labels []string) (string, error) {
	var ids []string
	for _, l := range labels {
		if id, ok := strings.CutPrefix(l, "nostr:"); ok {
			ids = append(ids, id)
		}
	}
	if len(ids) != 1 || !nostr.IsHex64(ids[0]) {
		return "", reject(RejectLabel, "want exactly one nostr:<64 hex> label, got %q", ids)
	}
	return ids[0], nil
}

// HandleLabel is the mailbox-binding path (amendment 20e, AMQ #1155): the
// message's nostr:<event id> label is only a pointer. The core fetches that
// event from the relay and runs the same checks as for any owner event
// (HandleEvent: pinned owner and body key, DM channel, gate thread, window,
// edit and delete history, single use). A message from a handle that is not
// the binding's carrier is refused before any fetch.
func (c *Core) HandleLabel(ctx context.Context, m MailboxMessage) Result {
	if !slices.Contains(c.Carriers, m.From) {
		return rejected("", "", reject(RejectSender, "message %s is from %q, not a carrier (%v)", m.ID, m.From, c.Carriers))
	}
	id, err := LabelEventID(m.Labels)
	if err != nil {
		return rejected("", "", err)
	}
	raw, err := c.Relay.FetchByID(ctx, id)
	if err != nil {
		return rejected("", id, reject(RejectFetch, "%v", err))
	}
	if e, err := nostr.ParseEvent(raw); err != nil || e.ID != id || !e.CheckID() {
		return rejected("", id, reject(RejectFetch, "the relay returned another event for %s", id))
	}
	return c.HandleEvent(ctx, raw)
}
