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
	return c.Store.Raise(spec, c.Identity(), c.now(), c.QuestionSender(ctx))
}

// QuestionSender posts gate questions through this core's relay and body key.
func (c *Core) QuestionSender(ctx context.Context) gatestore.Sender {
	return questionSender{ctx: ctx, c: c}
}

type questionSender struct {
	ctx context.Context
	c   *Core
}

// SignQuestion signs the record's text as a kind 9 in the DM channel with the
// body key, the tags amq-remote serve puts on a row (h, p, NIP-OA grant).
func (s questionSender) SignQuestion(rec gatestore.Record) (gatestore.Question, error) {
	var extra [][]string
	if rec.Squad != "" {
		extra = [][]string{{"squad", rec.Squad}}
	}
	evt, err := s.c.sign(rec.Text, extra)
	if err != nil {
		return gatestore.Question{}, err
	}
	return gatestore.Question{GateID: rec.GateID, EventID: evt.ID, RawEvent: string(evt.Marshal()), Relay: s.c.RelayURL,
		Channel: s.c.Pin.Channel, PostedAt: time.Unix(evt.CreatedAt, 0).UTC().Format(time.RFC3339)}, nil
}

// PublishQuestion publishes the recorded question's exact bytes.
func (s questionSender) PublishQuestion(q gatestore.Question) error {
	if s.c.Relay == nil {
		return errors.New("no relay")
	}
	return s.c.Relay.Publish(s.ctx, []byte(q.RawEvent))
}

// sign builds and signs a body kind 9 in the DM channel without publishing.
func (c *Core) sign(content string, extra [][]string) (nostr.Event, error) {
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

// ReaskText is the re-post in the gate thread (amendment 19), in plain words
// (brief 3.1.1-1): one line above the rule, never a tuple to type; the gate
// id below it, for the record.
func ReaskText(a gatestore.Answer) string {
	return "Reply yes or no to this message.\n\n" + gatestore.Rule + "\namq-squad gate " + a.GateID
}

// historyReaskMarker starts the history re-ask (HistoryReaskText).
const historyReaskMarker = "the question's history changed"

// HistoryReaskText is the re-ask of an altered_history refusal: the question
// as raised (the record's text, which the core signed), so what the owner
// answers is what the record holds, and the plain way to answer it.
func HistoryReaskText(rec gatestore.Record, a gatestore.Answer) string {
	return "This is the question as it was asked: " + historyReaskMarker + " (an edit was deleted).\nReply yes or no to this message.\n\n" + rec.Text
}

// IsHistoryReask reports whether a stored re-ask is the history re-ask of
// rec (it shows the record's full text).
func IsHistoryReask(r gatestore.Reask, rec gatestore.Record) bool {
	c := mustEvent(r.RawEvent).Content
	return strings.Contains(c, historyReaskMarker) && strings.Contains(c, rec.Text)
}

// HandleEvent processes one owner event exactly as fetched. In order: the
// tuple names a stored gate; a replay is already_consumed; Verify (pin,
// signature, kind, channel, tuple, digest prefix against the record, window,
// question, thread); a non-thread answer that matches the open gate gets the
// gate's one re-ask in the thread and is not accepted (amendment 19), and a
// second non-thread answer is refused; the question's history (edits,
// deletions) is clean; then the answer file is written and consumed.
// Every gate-lock wait is bounded by ctx (5-amq-B B-P4): a lock another
// process holds past ctx's deadline is a rejection, never a hang.
func (c *Core) HandleEvent(ctx context.Context, raw []byte) Result {
	cc := *c
	cc.Store = c.Store.WithContext(ctx)
	return cc.handleEvent(ctx, raw)
}

func (c *Core) handleEvent(ctx context.Context, raw []byte) Result {
	e, err := nostr.ParseEvent(raw)
	if err != nil {
		return rejected("", "", reject(RejectMalformed, "%v", err))
	}
	a, err := gatestore.ParseAnswer(e.Content)
	if err != nil {
		verb, ok := gatestore.ParseBare(e.Content)
		if !ok {
			return rejected("", e.ID, reject(RejectBadTuple, "%v", err))
		}
		id, berr := c.bareGate(e)
		if berr != nil {
			return rejected("", e.ID, berr)
		}
		a = gatestore.Answer{Verb: verb, GateID: id}
	}
	rec, err := c.Store.Load(a.GateID)
	if err != nil {
		return rejected(a.GateID, e.ID, reject(RejectGateRecord, "%v", err))
	}
	if rec.Squad != c.Store.Squad() {
		return rejected(rec.GateID, e.ID, reject(RejectNoSquad, "gate record %s does not carry this store's squad identity", rec.GateID))
	}
	// repair a half-marked decision before the replay check reads the marks
	if err := c.Store.Recover(rec.GateID); err != nil && Code(err) != RejectReplayed {
		return rejected(rec.GateID, e.ID, err)
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
		if reask != nil && c.Store.ReaskPublished(rec.GateID) {
			return rejected(rec.GateID, e.ID, reject(RejectThread, "the gate was re-asked in its thread (%s); this answer is still outside it", reask.EventID))
		}
		return c.reask(ctx, rec, q, o, now, ReaskText(o.Answer), RejectThread, "the answer was not in the gate thread; re-asked there (reply here to confirm)")
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
	// the hidden-target window starts at the question, or at the history
	// re-ask, which showed the question's text again (5-amq-B round 2)
	since := qe.CreatedAt
	if reask != nil && IsHistoryReask(*reask, rec) {
		since = mustEvent(reask.RawEvent).CreatedAt
	}
	if err := CheckHistory(ctx, c.Relay, qe, e, c.Pin.Owner, since); err != nil {
		if Code(err) == RejectAlteredHistory && reask == nil {
			// a re-ask, not a lockout: the question as raised, once, in
			// its thread; the owner's next thread reply is checked from it
			return c.reask(ctx, rec, q, o, now, HistoryReaskText(rec, o.Answer), RejectAlteredHistory,
				"the question's history changed ("+err.Error()+"); re-asked in the thread with the question as raised")
		}
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

// bareGate is the gate a bare yes or no is for (brief 3.1.1-1): the one gate
// whose stored question or re-ask every thread reference names. A bare
// answer with no thread reference is for the one open gate, which Verify
// then refuses as not in its thread and the core re-asks there; with no open
// gate, or several, it names none. A reference to anything else (another
// gate's question and this one's, a post that is no gate's) names none: the
// thread is the binding.
func (c *Core) bareGate(e nostr.Event) (string, error) {
	ids, err := c.Store.IDs()
	if err != nil {
		return "", reject(RejectGateRecord, "%v", err)
	}
	refs := nostr.ThreadRefs(e)
	now := c.now()
	var open, named []string
	for _, id := range ids {
		rec, err := c.Store.Load(id)
		if err != nil {
			continue
		}
		if len(refs) == 0 {
			if q, _ := c.Store.Question(id); q != nil && c.Store.State(rec, now) == gatestore.StateOpen {
				open = append(open, id)
			}
			continue
		}
		var ours []string
		if q, _ := c.Store.Question(id); q != nil {
			ours = append(ours, q.EventID)
		}
		if r, _ := c.Store.ReadReask(id); r != nil {
			ours = append(ours, r.EventID)
		}
		if slices.ContainsFunc(refs, func(ref string) bool { return slices.Contains(ours, ref) }) {
			named = append(named, id)
		}
	}
	if len(refs) == 0 {
		if len(open) == 1 {
			return open[0], nil
		}
		return "", reject(RejectThread, "a bare answer outside a gate thread names no gate (%d open); reply yes or no in the question's thread", len(open))
	}
	if len(named) != 1 {
		return "", reject(RejectWrongThread, "a bare answer replies to %v, which is not exactly one gate's thread", refs)
	}
	return named[0], nil
}

// reask posts the gate's one "reply here to confirm" in the gate thread. The
// signed re-ask is reserved under the gate lock and fsynced before it is
// published (gatestore.ReserveReask), so two handlers, or a crash between
// publish and persist, can never produce a second re-ask: a retry publishes
// the same event. The answer that triggered it is not accepted.
func (c *Core) reask(ctx context.Context, rec gatestore.Record, q *gatestore.Question, o Outcome, now time.Time, text, code, detail string) Result {
	if c.Store.State(rec, now) != gatestore.StateOpen {
		return rejected(rec.GateID, o.Event.ID, reject(code, "the gate is no longer open; no re-ask"))
	}
	r, _, err := c.Store.ReserveReask(rec.GateID, func() (gatestore.Reask, error) {
		extra := [][]string{{"e", q.EventID, c.RelayURL, "root"}}
		if rec.Squad != "" {
			extra = append(extra, []string{"squad", rec.Squad})
		}
		evt, err := c.sign(text, extra)
		if err != nil {
			return gatestore.Reask{}, err
		}
		return gatestore.Reask{GateID: rec.GateID, TriggerEventID: o.Event.ID, EventID: evt.ID, RawEvent: string(evt.Marshal()), At: now.UTC().Format(time.RFC3339)}, nil
	})
	if err != nil {
		return rejected(rec.GateID, o.Event.ID, fmt.Errorf("re-ask: %w", err))
	}
	if !c.Store.ReaskPublished(rec.GateID) {
		if err := c.Relay.Publish(ctx, []byte(r.RawEvent)); err != nil {
			return rejected(rec.GateID, o.Event.ID, fmt.Errorf("publish the re-ask %s (reserved; the next attempt publishes the same event): %w", r.EventID, err))
		}
		if err := c.Store.MarkReaskPublished(rec.GateID); err != nil {
			return rejected(rec.GateID, o.Event.ID, fmt.Errorf("record the re-ask as published: %w", err))
		}
	}
	return Result{GateID: rec.GateID, EventID: o.Event.ID, Outcome: "reasked", Code: code, ReaskEventID: r.EventID, Detail: detail}
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
	return c.HandleEventID(ctx, id)
}

// HandleEventID fetches one event by id from the relay and runs HandleEvent
// on the bytes the relay sent (an event whose id does not recompute is
// refused: a relay cannot substitute another event).
func (c *Core) HandleEventID(ctx context.Context, id string) Result {
	if !nostr.IsHex64(id) {
		return rejected("", "", reject(RejectLabel, "event id %q is not 64 lowercase hex", id))
	}
	raw, err := FetchEvent(ctx, c.Relay, id)
	if err != nil {
		return rejected("", id, err)
	}
	return c.HandleEvent(ctx, raw)
}

// FetchEvent is the one read by event id of the label path: one REQ for the
// id on r, and the bytes the relay sent only when their id recomputes to id
// (a relay can withhold an event but cannot substitute another). A failure
// is fetch_failed.
func FetchEvent(ctx context.Context, r Relay, id string) ([]byte, error) {
	if !nostr.IsHex64(id) {
		return nil, reject(RejectLabel, "event id %q is not 64 lowercase hex", id)
	}
	if r == nil {
		return nil, reject(RejectFetch, "no relay connection")
	}
	raw, err := r.FetchByID(ctx, id)
	if err != nil {
		return nil, reject(RejectFetch, "%v", err)
	}
	if e, err := nostr.ParseEvent(raw); err != nil || e.ID != id || !e.CheckID() {
		return nil, reject(RejectFetch, "the relay returned another event for %s", id)
	}
	return raw, nil
}
