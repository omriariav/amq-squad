package buzz

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// The share's declared target is amq-remote's mailbox adapter (AMQ v0.111.0
// and later, PR #1226; strict replies from the carrier from v0.112.0, PR
// #1230; the floor is v0.116.2). amq-remote serve subscribes to the owner's
// DM channel, checks the DM surface (owner, channel membership, the
// admission window, redelivery) and submits each owner DM to the target:
// one AMQ message to MailboxHandle in the share root, from the carrier
// `buzz`, on thread cockpit/<channel>, with the labels acp, buzz and
// nostr:<event id> (v0.116.2 internal/remote/mailbox/mailbox.go, prompt).
// The handle's newest reply that is not --kind status and has a body is the
// request's result, which serve posts as the body's result row in the DM;
// with no such reply within reply_timeout the request fails (mailbox.go,
// observe and finalReply).
const (
	// MailboxTarget is the manifest target of the share's mailbox adapter.
	MailboxTarget = "amq-squad"
	// MailboxHandle is the AMQ handle in the share root serve delivers the
	// owner's DMs to; the attachment reads its inbox and replies from it.
	// It is neither a carrier inbox (buzz, amq-remote) nor serve's own
	// handle (remote), which the adapter refuses.
	MailboxHandle = "attach"
	// ReplyTimeout is the adapter's reply_timeout (ruling 20 point 3): a
	// verification that hangs ends as the adapter's timeout result, never a
	// blocked mailbox.
	ReplyTimeout = 60 * time.Second
	// VerifyTimeout caps one verification: the connection, the read by
	// event id, the question's history and a re-ask post.
	VerifyTimeout = 10 * time.Second
)

// ServePromptSuffix starts the instructions serve appends to a Buzz DM's
// text (v0.116.2 internal/remote/mailbox/mailbox.go, prompt).
const ServePromptSuffix = "\n\nThis came from the owner's Buzz DM."

// Prompt is one message serve delivered to the handle.
type Prompt struct {
	ID     string
	From   string
	Thread string
	Labels []string
	Body   string
}

// Inbox is the handle's AMQ inbox in the share root.
type Inbox interface {
	// New lists the unread messages, oldest first, without reading them.
	New(ctx context.Context) ([]Prompt, error)
	// Take reads one message and moves it out of new: the take is the
	// once-only claim (at most once, as the retired DM marks were).
	Take(ctx context.Context, id string) (Prompt, error)
	// Reply answers a message: status true sends `--kind status` (never the
	// result), false the one result.
	Reply(ctx context.Context, id, text string, status bool) error
}

// Dialer opens the verifier's own connection for one verification (ruling
// 20 point 1): the relay signed in with the share body, the body key and
// the kind 9 grant for the core's re-ask, and the close. It never subscribes
// to the DM channel.
type Dialer func(ctx context.Context) (relay gateverify.Relay, body nostr.SecretKey, grant []string, closeFn func(), err error)

// Handler takes the owner's DMs serve delivered and acts on each once. For
// every message it fetches the event the label names on the verifier's own
// connection and acts only on that owner-signed event, never on the AMQ body
// (serve's message is a notification, never the authority: R32, amendment
// 20, ruling 20, 5-amq-B B-P1): a gate answer goes to the 3-1 core; any
// other message is delivered into the bound mailbox and, when it names a
// lead, routed on through AMQ. Each message gets exactly one result reply,
// or, when a verification hangs, none (the adapter's reply timeout is then
// the result, and the squad's status document shows a verify_timeout row).
type Handler struct {
	Core *gateverify.Core // Store, Pin, Issuer, Namespace; Relay is set per verification
	// Cores are the gate stores of the other squads (operator mode): an
	// answer goes to the core whose store holds its gate; Core otherwise.
	Cores  []*gateverify.Core
	Dial   Dialer
	Inbox  Inbox
	Mail   Mailer
	Target Target
	// Poster and Threads are the lead reply relay (brief 31-4, RelayStep):
	// the poster signs with the share body in this process; without both
	// the relay is off (the receipt and the record are still written).
	Poster  Poster
	Threads ThreadReader
	// SeenPath is the durable once-only record of handled event ids
	// (B-P7); "" keeps it in memory (tests).
	SeenPath string
	// VerifyTimeout overrides the 10 s cap (tests).
	VerifyTimeout time.Duration
	Now           func() time.Time

	seen *seenSet
}

// Handled is what the handler did with one delivered message.
type Handled struct {
	MessageID string             `json:"message_id"`
	EventID   string             `json:"event_id,omitempty"`
	RequestID string             `json:"request_id,omitempty"`
	Kind      string             `json:"kind"` // gate | route | duplicate | attention | error
	Gate      *gateverify.Result `json:"gate,omitempty"`
	Delivery  *Delivery          `json:"delivery,omitempty"`
	Status    []string           `json:"status,omitempty"` // the status lines sent
	Reply     string             `json:"reply,omitempty"`  // the one result reply, "" when none was sent
	Error     string             `json:"error,omitempty"`
}

// RouteWindow is how old a phone message may be when it is routed, and
// FutureSkew how far ahead of this clock it may be dated (serve admits a DM
// within 2 minutes, v0.116.2 buzzio/normalize.go MutationWindow; the reply
// may come later).
const (
	RouteWindow = 10 * time.Minute
	FutureSkew  = 60 * time.Second
)

// RejectNotFetched is the reply code of a label whose event the relay did
// not return.
const RejectNotFetched = "not_fetched"

// Step handles every unread message from the carrier, oldest first. A
// message from another sender is not serve's delivery of a Buzz DM and is
// left unread. An unreadable inbox is an owner alert in the primary squad's
// status document (5-amq-B fix round).
func (h *Handler) Step(ctx context.Context) ([]Handled, error) {
	if h.Core == nil || h.Inbox == nil || h.Dial == nil {
		return nil, errors.New("the handler needs a core, an inbox and a dialer")
	}
	if h.seen == nil {
		h.seen = &seenSet{path: h.SeenPath}
	}
	list, err := h.Inbox.New(ctx)
	if err != nil {
		h.inboxAlert(err)
		return nil, err
	}
	var out []Handled
	for _, p := range list {
		if p.From != CarrierHandle {
			continue
		}
		m, err := h.Inbox.Take(ctx, p.ID)
		if err != nil {
			h.inboxAlert(err)
			return out, fmt.Errorf("read %s: %w (not handled; retried next step)", p.ID, err)
		}
		out = append(out, h.handle(ctx, m))
	}
	clearInboxAlert(h.Core)
	return out, nil
}

func (h *Handler) inboxAlert(err error) {
	if errors.Is(err, ErrInboxUnreadable) {
		_ = raiseInboxAlert(h.Core, err, h.now())
	}
}

func (h *Handler) handle(ctx context.Context, m Prompt) Handled {
	hd := Handled{MessageID: m.ID, Kind: "error"}
	id, err := gateverify.LabelEventID(m.Labels)
	if err != nil || m.From != CarrierHandle {
		hd.Error = "not a Buzz DM delivery: " + errText(err)
		h.final(ctx, &hd, "refused - "+gateverify.RejectLabel)
		return hd
	}
	hd.EventID = id
	if gate, seen, err := h.seen.get(id, h.now()); err != nil {
		hd.Error = "once-only record: " + err.Error()
		h.final(ctx, &hd, "refused - not_recorded")
		return hd
	} else if seen {
		// serve delivered the same event again: handled once
		hd.Kind = "duplicate"
		h.final(ctx, &hd, "refused "+gate+" "+gatestore.RejectReplayed)
		return hd
	}
	h.status(ctx, &hd, "verifying event "+id[:12])
	limit := h.VerifyTimeout
	if limit <= 0 {
		limit = VerifyTimeout
	}
	vctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	relay, body, grant, closeFn, err := h.Dial(vctx)
	if err != nil {
		if vctx.Err() != nil {
			h.hungBeforeFetch(ctx, &hd, m, limit, "connect")
			return hd
		}
		hd.Error = "connect: " + err.Error()
		h.final(ctx, &hd, "refused - "+RejectNotFetched)
		return hd
	}
	closed := false
	closeOnce := func() {
		if !closed {
			closed = true
			closeFn()
		}
	}
	defer closeOnce()
	raw, err := gateverify.FetchEvent(vctx, relay, id)
	if err != nil {
		if vctx.Err() != nil {
			h.hungBeforeFetch(ctx, &hd, m, limit, "fetch")
			return hd
		}
		hd.Error = err.Error()
		h.final(ctx, &hd, "refused - "+RejectNotFetched) // nothing is marked
		return hd
	}
	e, _ := nostr.ParseEvent(raw)
	content := strings.TrimSpace(e.Content)
	gateID := answerGateID(content) // from the signed event, never serve's copy
	if code := h.ownerEvent(e); code != "" {
		hd.Error = "the event is not the owner's DM: " + code
		h.final(ctx, &hd, "refused "+gateID+" "+code) // nothing is marked
		return hd
	}
	// a reply to a relayed post continues its request (brief 31-4): it
	// reaches the lead as text, whatever its shape; the gate path keeps
	// every other answer form, as today
	cont := h.continuation(e)
	if cont == nil && isGateAnswer(content) {
		h.gate(ctx, vctx, &hd, e, raw, gateID, relay, body, grant, limit)
		return hd
	}
	closeOnce() // a route needs nothing more from the relay
	now := h.now()
	if e.CreatedAt < now.Add(-RouteWindow).Unix() || e.CreatedAt > now.Add(FutureSkew).Unix() {
		hd.Error = fmt.Sprintf("created_at %d is outside the routing window", e.CreatedAt)
		h.final(ctx, &hd, "refused - "+gateverify.RejectWindow)
		return hd
	}
	// the once-only mark follows the successful forward, as on the gate
	// path (fix round 2, item 1): a forward that fails leaves no mark and
	// the next delivery retries it
	h.route(ctx, &hd, e, content, now, cont)
	return hd
}

// ownerEvent checks the fetched event is the owner's DM to the body: the id
// recomputes, the signature is the owner's pinned key, kind 9, in the
// pinned DM channel (the channel of exactly the owner and the body). It
// returns gateverify's code, "" when it is.
func (h *Handler) ownerEvent(e nostr.Event) string {
	pin := h.Core.Pin
	switch {
	case !e.CheckID():
		return gateverify.RejectBadID
	case e.PubKey != pin.Owner:
		return gateverify.RejectWrongPubkey
	case e.VerifySignature() != nil:
		return gateverify.RejectBadSignature
	case e.Kind != KindDM:
		return gateverify.RejectWrongKind
	case !slices.Equal(nostr.TagValues(e, "h"), []string{pin.Channel}):
		return gateverify.RejectWrongChannel
	}
	return ""
}

// hung is a verification that did not finish within its cap: a status
// line, no result (the adapter's reply timeout ends the request), and, for a
// gate, a verify_timeout row in that squad's status document. Nothing is
// marked: the owner may answer or send again.
func (h *Handler) hung(ctx context.Context, hd *Handled, core *gateverify.Core, gateID string, limit time.Duration, step string) {
	hd.Kind = "attention"
	hd.Error = fmt.Sprintf("verification of %s did not finish within %s (%s); no result sent, the adapter times the request out and the gate stays open", gateID, limit, step)
	h.status(ctx, hd, "timeout "+gateID+" verify_timeout")
	if core == nil || gateID == "-" {
		return
	}
	if err := raiseVerifyTimeout(core, gateID, limit, h.now()); err != nil {
		hd.Error = joinErr(hd.Error, "attention row: "+err.Error())
	}
}

// gate runs the core's checks on the fetched event, with the verifier's
// connection lent for the call (the question's history and the re-ask go on
// it), bounded by the verification's context (gate locks included).
func (h *Handler) gate(ctx, vctx context.Context, hd *Handled, e nostr.Event, raw []byte, gateID string, relay gateverify.Relay, body nostr.SecretKey, grant []string, limit time.Duration) {
	hd.Kind = "gate"
	core, holders := h.coreFor(e)
	if core == nil {
		hd.Error = "the gate id is open in more than one squad: " + strings.Join(holders, ", ")
		h.final(ctx, hd, "refused "+gateID+" ambiguous_gate")
		return
	}
	prevRelay, prevBody, prevGrant := core.Relay, core.Body, core.BodyGrant
	core.Relay, core.Body, core.BodyGrant = relay, body, grant
	defer func() { core.Relay, core.Body, core.BodyGrant = prevRelay, prevBody, prevGrant }()
	r := core.HandleEvent(vctx, raw)
	hd.Gate = &r
	if r.GateID != "" {
		gateID = r.GateID
	}
	if r.Outcome == "rejected" && vctx.Err() != nil {
		h.hung(ctx, hd, core, gateID, limit, "verify")
		return
	}
	clearVerifyTimeout(core, gateID) // a verification of the gate finished
	// the owner's verified event is handled: a redelivery is a duplicate
	if err := h.seen.put(e.ID, gateID, h.now()); err != nil {
		hd.Error = joinErr(hd.Error, "once-only record: "+err.Error())
	}
	switch r.Outcome {
	case "approved", "denied":
		class := "-"
		if rec, err := core.Store.Load(r.GateID); err == nil {
			class = rec.Class
		}
		h.final(ctx, hd, "accepted "+gateID+" "+class)
	case "reasked":
		h.final(ctx, hd, "refused "+gateID+" "+r.Code+"\n"+reaskLine(r.ReaskEventID))
	default:
		text := "refused " + gateID + " " + r.Code
		if r.Code == gateverify.RejectThread || r.Code == gateverify.RejectWrongThread {
			if ra, err := core.Store.ReadReask(gateID); err == nil && ra != nil {
				text += "\n" + reaskLine(ra.EventID)
			} else if q, err := core.Store.Question(gateID); err == nil && q != nil {
				text += "\n" + reaskLine(q.EventID)
			}
		}
		h.final(ctx, hd, text)
	}
}

// reaskLine points the owner at the gate thread by the id prefix of the
// event the thread hangs from: ids only, never the question's text.
func reaskLine(eventID string) string {
	if len(eventID) > 12 {
		eventID = eventID[:12]
	}
	return "re-ask: answer in the gate thread (event " + eventID + ")"
}

// route delivers the owner-signed event's own content (never the AMQ body)
// on the request's cockpit thread: a new request for a fresh DM, the same
// request for a continuation. The one result reply is the receipt,
// `accepted <request id>` and the routing; the lead's answer reaches the
// owner later as a relay post (RelayStep).
func (h *Handler) route(ctx context.Context, hd *Handled, e nostr.Event, content string, now time.Time, cont *status.RelayRecord) {
	hd.Kind = "route"
	id := e.ID
	rid := RequestID(id)
	var via *Route
	if cont != nil {
		rid, via = cont.RequestID, &Route{Lead: cont.Route}
	}
	hd.RequestID = rid
	// the record first (Codex P2-6): no record, no question to the lead,
	// and the DM stays unhandled so serve's redelivery works
	rec, undo, err := h.openTurn(e, rid, cont, now)
	if err != nil {
		hd.Kind, hd.Error = "error", "relay record: "+err.Error()
		h.final(ctx, hd, "refused - "+RelayRecordFailed)
		return
	}
	d, err := DeliverOn(ctx, h.Mail, h.Target, id, content, CockpitThread(rid), via)
	if err != nil {
		// the detail (paths, AMQ errors) stays on this machine; the reply is
		// clear text on the relay
		hd.Kind, hd.Error = "error", err.Error()
		if uerr := undo(); uerr != nil {
			// named, never ignored; the relay's next step takes the
			// undelivered turn back
			hd.Error = joinErr(hd.Error, RelayRollbackFailed+": "+uerr.Error())
		}
		h.final(ctx, hd, "refused - not_delivered")
		return
	}
	hd.Delivery = &d
	// the real lead and the delivered mark before the DM is marked handled
	// (Codex re-check #4): if that write fails, the DM stays unhandled so
	// serve's redelivery works, and the relay takes the turn back
	if err := h.finishTurn(rec, cont == nil, d, now); err != nil {
		hd.Kind, hd.Error = "error", "relay record: "+err.Error()
		h.final(ctx, hd, "refused - "+RelayRecordFailed)
		return
	}
	if err := h.seen.put(id, "-", now); err != nil {
		hd.Error = "once-only record: " + err.Error()
	}
	dest := d.Mailbox
	if d.Lead != "" {
		dest = d.Lead
	}
	text := "accepted " + rid + " routed " + dest
	switch {
	case len(d.Ambiguous) > 0:
		text += " not_forwarded ambiguous_lead"
	case d.Refused != "":
		text += " not_forwarded not_a_lead"
	}
	h.final(ctx, hd, text)
}

// answerGateID is the gate id an answer names, "-" when it names none.
func answerGateID(content string) string {
	if a, err := gatestore.ParseAnswer(content); err == nil {
		return a.GateID
	}
	return "-"
}

// final sends the one result reply. Every reply passes redact.Block.
func (h *Handler) final(ctx context.Context, hd *Handled, text string) {
	text = redact.Block(text)
	if err := h.Inbox.Reply(ctx, hd.MessageID, text, false); err != nil {
		hd.Error = joinErr(hd.Error, "reply: "+err.Error())
		return
	}
	hd.Reply = text
}

// status sends one intermediate line as --kind status: never the result.
func (h *Handler) status(ctx context.Context, hd *Handled, text string) {
	text = redact.Block(text)
	if err := h.Inbox.Reply(ctx, hd.MessageID, text, true); err != nil {
		hd.Error = joinErr(hd.Error, "status: "+err.Error())
		return
	}
	hd.Status = append(hd.Status, text)
}

func joinErr(a, b string) string {
	if a == "" {
		return b
	}
	return a + "; " + b
}

func errText(err error) string {
	if err == nil {
		return "sender is not the carrier"
	}
	return err.Error()
}

// coreFor resolves a gate answer to the one core that may verify it: the
// only store holding its gate id, else the only store whose stored question
// or re-ask the answer's thread names. nil and the holders' names when the
// answer is ambiguous. An answer for a gate no store holds goes to Core
// (which refuses it).
func (h *Handler) coreFor(e nostr.Event) (*gateverify.Core, []string) {
	a, err := gatestore.ParseAnswer(strings.TrimSpace(e.Content))
	if err != nil {
		return h.Core, nil
	}
	var holders []*gateverify.Core
	for _, c := range append([]*gateverify.Core{h.Core}, h.Cores...) {
		if _, err := c.Store.Load(a.GateID); err == nil {
			holders = append(holders, c)
		}
	}
	switch len(holders) {
	case 0:
		return h.Core, nil
	case 1:
		return holders[0], nil
	}
	refs := nostr.ThreadRefs(e)
	var match []*gateverify.Core
	var names []string
	for _, c := range holders {
		names = append(names, squadName(c))
		q, _ := c.Store.Question(a.GateID)
		r, _ := c.Store.ReadReask(a.GateID)
		if (q != nil && slices.Contains(refs, q.EventID)) || (r != nil && slices.Contains(refs, r.EventID)) {
			match = append(match, c)
		}
	}
	if len(match) == 1 {
		return match[0], nil
	}
	return nil, names
}

// squadName names a core's squad for the owner: the project directory's
// base name and the session.
func squadName(c *gateverify.Core) string {
	if c.Namespace.ProjectDir != "" {
		return filepath.Base(c.Namespace.ProjectDir) + "/" + c.Namespace.Session
	}
	return filepath.Base(c.Store.Root)
}

// isGateAnswer reports whether a message is meant as a gate answer; the core
// decides everything else about it, on the event it fetched itself.
// A message whose first word is approve or deny always goes to the core, so
// a malformed answer is refused there (bad_tuple) and never routed to a lead.
func isGateAnswer(content string) bool {
	if _, err := gatestore.ParseAnswer(content); err == nil {
		return true
	}
	first, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(content)), " ")
	return first == "approve" || first == "deny"
}
