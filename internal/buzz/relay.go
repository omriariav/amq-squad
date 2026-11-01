package buzz

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"

	"github.com/omriariav/amq-squad/v3/internal/amqexec"
	"github.com/omriariav/amq-squad/v3/internal/atomicfile"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/redact"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// The lead reply relay (brief 31-4; plan 3.1 section 3.2; R33's one added
// purpose, "lead reply relay"). The mailbox adapter posts only the
// handle's one result reply within its 60 s reply_timeout, and a lead turn
// takes longer, so the inbound reply is the receipt (`accepted <request
// id>`) and the lead's answer reaches the owner as a post of its own. The
// lead never holds a key: it replies on the request's cockpit thread
// through AMQ as it does today; this process, the attachment, which holds
// the body key for the question post (ruling 20 point 1), follows each
// cockpit thread it opened and posts the lead's replies to the owner's DM:
// text only, through redact.Block, rate-bounded, never a gate answer's
// shape, never into a gate question's thread. The purpose retires when
// amq-remote offers a post surface on serve's connection (upstream ask 19).

const (
	// ProgressBound is the progress rate bound: at most one progress post
	// per cockpit thread per bound; the newest progress reply held inside
	// it posts when it passes. Answers and results are not bounded, but
	// each lead reply is at most one post (no fan-out).
	ProgressBound = 2 * time.Minute
	// DMLimit is one Buzz DM's text limit in bytes (amq-remote v0.116.2
	// internal/remote/buzzio/carrier.go maxRowText); a longer post is split
	// at paragraph boundaries into at most MaxParts parts.
	DMLimit  = 4000
	MaxParts = 3
	// MinQuestionLine is the shortest normalized line of an open gate's
	// question the substring check matches (16 characters): a short line
	// ("v3", a bare tag) would refuse ordinary replies, and the gate id and
	// the typed tuple carry the binding anyway.
	MinQuestionLine = 16
	// FollowWindow is how long after its newest owner turn a cockpit
	// thread is followed.
	FollowWindow = 24 * time.Hour
	// LabelProgress and LabelResult are the labels the lead puts on its
	// send (`amq reply --labels progress|result`); `--kind status` is
	// progress and `--kind decision` is a result too, so the pi lead's
	// amq_reply (which sets a kind, not labels) can say either.
	LabelProgress = "progress"
	LabelResult   = "result"
	// RefusedGateShape is a lead reply with a line shaped like a gate
	// answer or naming an open gate; RefusedGateThread a post whose
	// thread is a gate question's.
	RefusedGateShape  = status.RelayRefusedGateShape
	RefusedGateThread = status.RelayRefusedGateThread
)

// RequestID names the request an owner DM opens: q- and the first 12 hex of
// its event id.
func RequestID(eventID string) string {
	if len(eventID) > 12 {
		eventID = eventID[:12]
	}
	return "q-" + eventID
}

// CockpitThread is a request's AMQ thread: the question and every later
// owner turn go to the lead on it, and the lead replies on it.
func CockpitThread(requestID string) string { return "cockpit/" + requestID }

// Poster posts one text into the owner's DM channel as the share body, in
// reply to the event replyTo ("" for a top-level post), and returns the
// posted event's id. Only the core's process holds one (BodyPoster). An
// error that wraps ErrNotSent says nothing was written to the relay;
// any other error is ambiguous (the relay may have the event).
type Poster interface {
	Post(ctx context.Context, text, replyTo string) (string, error)
}

// ErrNotSent marks a post failure before anything was written to the
// relay: the connection never opened, or the event was never signed.
var ErrNotSent = errors.New("not sent")

func notSent(err error) error { return fmt.Errorf("%w: %w", ErrNotSent, err) }

// BodyPoster posts through the verifier's per-use connection (ruling 20
// point 1): one connection per post, signed with the share body key, the
// owner's kind 9 grant attached; it never subscribes.
type BodyPoster struct {
	Dial Dialer
	Pin  gateverify.Pin
	Now  func() time.Time
}

func (p BodyPoster) Post(ctx context.Context, text, replyTo string) (string, error) {
	if p.Dial == nil {
		return "", notSent(errors.New("the poster needs a dialer"))
	}
	relay, body, grant, closeFn, err := p.Dial(ctx)
	if err != nil {
		return "", notSent(err)
	}
	defer closeFn()
	if body.PublicHex() != p.Pin.Body {
		return "", notSent(errors.New("the body key is not the pinned body"))
	}
	tags := [][]string{{"h", p.Pin.Channel}, {"p", p.Pin.Owner}}
	if replyTo != "" {
		tags = append(tags, []string{"e", replyTo, "", "reply"})
	}
	if grant != nil {
		tags = append(tags, grant)
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	evt := nostr.Event{CreatedAt: now().Unix(), Kind: KindDM, Tags: tags, Content: text}
	if err := body.Sign(&evt); err != nil {
		return "", notSent(err)
	}
	// ambiguous from here: the relay writes the event before it awaits the
	// acknowledgment (gateverify WSRelay.Publish), so a timeout or a closed
	// connection may follow an accepted event
	if err := relay.Publish(ctx, evt.Marshal()); err != nil {
		return "", err
	}
	return evt.ID, nil
}

// ThreadEntry is one message of an AMQ thread (`amq thread --json`).
type ThreadEntry struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	Thread  string   `json:"thread"`
	Created string   `json:"created"`
	Kind    string   `json:"kind"`
	Labels  []string `json:"labels"`
	Body    string   `json:"body"`
}

// ThreadReader reads one AMQ thread of a root, oldest first, with bodies.
type ThreadReader interface {
	Thread(ctx context.Context, root, thread string) ([]ThreadEntry, error)
}

// AMQThreads reads with `amq thread --include-body --json` (a read: nothing
// moves out of any inbox).
type AMQThreads struct{ Bin string }

func (a AMQThreads) Thread(ctx context.Context, root, thread string) ([]ThreadEntry, error) {
	bin := a.Bin
	if bin == "" {
		bin = "amq"
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "thread", "--root", root, "--id", thread, "--include-body", "--json")
	cmd.Env = amqexec.NoUpdateCheckEnv(nil)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("amq thread %s: %v: %s", thread, err, strings.TrimSpace(errb.String()))
	}
	var rows []ThreadEntry
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		return nil, fmt.Errorf("amq thread %s: output does not parse: %v", thread, err)
	}
	return rows, nil
}

// Relayed is what the relay did with one lead reply.
type Relayed struct {
	RequestID string   `json:"request_id"`
	ReplyID   string   `json:"reply_id"`
	Kind      string   `json:"kind"`    // answer | progress | result
	Outcome   string   `json:"outcome"` // posted | refused | held | superseded
	Reason    string   `json:"reason,omitempty"`
	Events    []string `json:"events,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// RelayStep follows every open cockpit thread of the target's root and
// posts the lead's new replies. It does nothing without a poster and a
// thread reader. A post that fails is retried on the next step; the record
// is written after every reply handled, so a restarted attachment neither
// posts a reply again nor skips one.
func (h *Handler) RelayStep(ctx context.Context) ([]Relayed, error) {
	if h.Poster == nil || h.Threads == nil {
		return nil, nil
	}
	now := h.now()
	recs, problems := loadRelayRecords(status.RelayDir(h.Target.Root))
	var errs []string
	errs = append(errs, problems...)
	if len(recs) == 0 {
		return nil, joinErrs(errs)
	}
	g, err := h.gateView(now)
	if err != nil {
		// fail closed: nothing is posted while the gate stores are unread
		return nil, fmt.Errorf("relay: gate store: %w", err)
	}
	var out []Relayed
	for _, rec := range recs {
		got, err := h.relayRecord(ctx, rec, g, now)
		out = append(out, got...)
		if err != nil {
			errs = append(errs, rec.RequestID+": "+err.Error())
		}
	}
	return out, joinErrs(errs)
}

func joinErrs(errs []string) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}

// replyKind classifies a lead reply by what the lead put on its send.
func replyKind(e ThreadEntry) string {
	switch {
	case slices.Contains(e.Labels, LabelResult) || e.Kind == "decision":
		return "result"
	case slices.Contains(e.Labels, LabelProgress) || e.Kind == "status":
		return "progress"
	}
	return "answer"
}

func (h *Handler) relayRecord(ctx context.Context, rec *status.RelayRecord, g gateView, now time.Time) ([]Relayed, error) {
	if rec.Closed && rec.Pending == "" && rec.Posting == nil {
		return nil, nil
	}
	if n := len(rec.Turns); n > 0 && now.Sub(rec.Turns[n-1].At) > FollowWindow && rec.Posting == nil {
		return nil, nil
	}
	entries, err := h.Threads.Thread(ctx, h.Target.Root, rec.Thread)
	if err != nil {
		return nil, err
	}
	dir := status.RelayDir(h.Target.Root)
	// every change is written before the relay goes on; a write that fails
	// stops this request until the next one succeeds (no durable intent, no
	// publish)
	save := func() error { return writeRelayRecord(dir, rec, now) }
	var out []Relayed
	byID := map[string]ThreadEntry{}
	for _, e := range entries {
		byID[e.ID] = e
	}
	// a reply left in flight (a crash, a failed part) resumes first
	if rec.Posting != nil {
		r, err := h.resume(ctx, rec, byID, g, now, save)
		out = append(out, r)
		if err != nil {
			return out, err
		}
	}
	for _, e := range entries {
		if e.From != rec.Lead || slices.Contains(rec.Seen, e.ID) {
			continue
		}
		kind := replyKind(e)
		if kind == "progress" && rec.ProgressAt != nil && now.Sub(*rec.ProgressAt) < ProgressBound {
			// inside the bound: held; the newest held progress wins
			if rec.Pending != "" {
				out = append(out, Relayed{RequestID: rec.RequestID, ReplyID: rec.Pending, Kind: "progress", Outcome: "superseded"})
			}
			rec.Pending = e.ID
			markSeen(rec, e.ID)
			if err := save(); err != nil {
				return out, err
			}
			out = append(out, Relayed{RequestID: rec.RequestID, ReplyID: e.ID, Kind: kind, Outcome: "held"})
			continue
		}
		if kind != "progress" && rec.Pending != "" {
			// an answer or a result is newer than the held progress
			out = append(out, Relayed{RequestID: rec.RequestID, ReplyID: rec.Pending, Kind: "progress", Outcome: "superseded"})
			rec.Pending = ""
		}
		r, err := h.relayReply(ctx, rec, e, kind, g, now, save)
		out = append(out, r)
		if err != nil {
			return out, err // not marked: the next step resumes it from the record
		}
	}
	if rec.Pending != "" && (rec.ProgressAt == nil || now.Sub(*rec.ProgressAt) >= ProgressBound) {
		e, ok := byID[rec.Pending]
		if !ok {
			rec.Pending = ""
			return out, save()
		}
		r, err := h.relayReply(ctx, rec, e, "progress", g, now, save)
		out = append(out, r)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func markSeen(rec *status.RelayRecord, id string) {
	if !slices.Contains(rec.Seen, id) {
		rec.Seen = append(rec.Seen, id)
	}
	rec.LastReply = id
}

func timePtr(t time.Time) *time.Time {
	u := t.UTC()
	return &u
}

// relayAfterPublish and relayRecordWrite are test seams: a crash between a
// publish and its record, a record store that fails.
var (
	relayAfterPublish func() error
	relayRecordWrite  func() error
)

// refused records a reply the relay does not post (by name, the sha256 of
// the line or part that caused it) and marks it handled.
func refused(rec *status.RelayRecord, r Relayed, reason, text string, now time.Time, save func() error) (Relayed, error) {
	rec.Refusals = append(rec.Refusals, status.RelayRefusal{ReplyID: r.ReplyID, Reason: reason, SHA256: sha256Hex(text), At: now.UTC()})
	rec.Posting = nil
	if rec.Pending == r.ReplyID {
		rec.Pending = ""
	}
	markSeen(rec, r.ReplyID)
	r.Outcome, r.Reason = "refused", reason
	return r, save()
}

// relayReply posts one lead reply, or refuses it and records why.
func (h *Handler) relayReply(ctx context.Context, rec *status.RelayRecord, e ThreadEntry, kind string, g gateView, now time.Time, save func() error) (Relayed, error) {
	r := Relayed{RequestID: rec.RequestID, ReplyID: e.ID, Kind: kind}
	body := strings.TrimSpace(e.Body)
	if g.threads[rec.PostTo] {
		return refused(rec, r, RefusedGateThread, body, now, save)
	}
	parts, line, bad := g.assemble(rec.RequestID, rec.Thread, body)
	if bad {
		return refused(rec, r, RefusedGateShape, line, now, save)
	}
	p := &status.RelayPosting{ReplyID: e.ID, Kind: kind, Events: []string{}}
	for _, x := range parts {
		p.Parts = append(p.Parts, sha256Hex(x))
	}
	rec.Posting = p
	return h.publish(ctx, rec, parts, now, save)
}

// resume continues the reply in flight from the record: the parts are
// assembled again from the thread and must be the ones the intent names.
func (h *Handler) resume(ctx context.Context, rec *status.RelayRecord, byID map[string]ThreadEntry, g gateView, now time.Time, save func() error) (Relayed, error) {
	p := rec.Posting
	r := Relayed{RequestID: rec.RequestID, ReplyID: p.ReplyID, Kind: p.Kind}
	next := p.Parts[min(len(p.Events), len(p.Parts)-1)]
	e, ok := byID[p.ReplyID]
	if !ok {
		return refused(rec, r, status.RelayPostUnconfirmed, next, now, save)
	}
	if g.threads[rec.PostTo] {
		return refused(rec, r, RefusedGateThread, strings.TrimSpace(e.Body), now, save)
	}
	parts, line, bad := g.assemble(rec.RequestID, rec.Thread, strings.TrimSpace(e.Body))
	if bad {
		return refused(rec, r, RefusedGateShape, line, now, save)
	}
	same := len(parts) == len(p.Parts)
	for i := 0; same && i < len(parts); i++ {
		same = sha256Hex(parts[i]) == p.Parts[i]
	}
	if !same {
		return refused(rec, r, status.RelayPostUnconfirmed, next, now, save)
	}
	return h.publish(ctx, rec, parts, now, save)
}

// publish posts the parts of the reply in flight not yet confirmed. Before
// each publish the intent is written (a write that fails publishes
// nothing); after it, the event id. A part whose intent was already tried
// (a crash between publish and record) is published once more at most,
// then recorded relay_post_unconfirmed and never tried again. The reply is
// handled only when every part has its event id.
func (h *Handler) publish(ctx context.Context, rec *status.RelayRecord, parts []string, now time.Time, save func() error) (Relayed, error) {
	p := rec.Posting
	r := Relayed{RequestID: rec.RequestID, ReplyID: p.ReplyID, Kind: p.Kind}
	for i := len(p.Events); i < len(parts); i++ {
		if p.Tries >= 2 {
			return refused(rec, r, status.RelayPostUnconfirmed, p.Parts[i], now, save)
		}
		p.Tries++
		if err := save(); err != nil {
			return r, fmt.Errorf("the intent for %s part %d of %d is not recorded, nothing published: %w", p.ReplyID, i+1, len(parts), err)
		}
		id, err := h.Poster.Post(ctx, parts[i], rec.PostTo)
		if err != nil {
			// a failed post keeps its try: the relay may have accepted the
			// event before the failure, so an ambiguous failure gets the one
			// retry and then relay_post_unconfirmed. Only a failure before
			// anything was written gives the try back.
			if errors.Is(err, ErrNotSent) {
				p.Tries--
				_ = save()
			}
			return r, fmt.Errorf("post %s part %d of %d: %w", p.ReplyID, i+1, len(parts), err)
		}
		if relayAfterPublish != nil {
			if err := relayAfterPublish(); err != nil {
				return r, err
			}
		}
		p.Events, p.Tries = append(p.Events, id), 0
		if err := save(); err != nil {
			return r, err
		}
	}
	rec.Posts = append(rec.Posts, status.RelayPost{ReplyID: p.ReplyID, Kind: p.Kind, Events: p.Events, SHA256: sha256Hex(strings.Join(parts, "\n")), At: now.UTC()})
	rec.RelayedAt = timePtr(now)
	rec.Posting = nil
	markSeen(rec, p.ReplyID)
	switch p.Kind {
	case "progress":
		rec.ProgressAt = timePtr(now)
		if rec.Pending == p.ReplyID {
			rec.Pending = ""
		}
	case "result":
		rec.Closed = true
	}
	r.Outcome, r.Events = "posted", p.Events
	return r, save()
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// assemble is the reply's post parts as they will be published, in five
// steps: redact.Block over the whole body and the header (a value the split
// would cut is redacted whole), the split, then per part the three refusal
// checks, redact.Block, and the checks again. Every check runs on the bytes
// that leave. One refused line in any part refuses the whole reply (a split
// that would start or end a part with an answer-form line is refused, never
// re-split around it).
func (g gateView) assemble(requestID, thread, body string) ([]string, string, bool) {
	parts := splitPost(requestID, thread, redact.Block(body))
	for i, p := range parts {
		if line, bad := g.refuse(p); bad {
			return nil, line, true
		}
		parts[i] = redact.Block(p)
		if line, bad := g.refuse(parts[i]); bad {
			return nil, line, true
		}
	}
	return parts, "", false
}

// splitPost is the post's text: the first line `re <request id>`, then the
// (redacted) reply. A text over DMLimit is split at paragraph boundaries
// into parts marked (1/n), at most MaxParts; what does not fit is replaced
// by one line naming the cockpit thread.
func splitPost(requestID, thread, body string) []string {
	head := redact.Block("re " + requestID)
	if len(head)+1+len(body) <= DMLimit {
		return []string{head + "\n" + body}
	}
	tail := "(the rest is on the cockpit thread " + thread + ")"
	budget := DMLimit - len(head) - len(" (3/3)\n") - len("\n\n") - len(tail)
	var paras []string
	for _, p := range strings.Split(body, "\n\n") {
		for len(p) > budget {
			cut := cutAt(p, budget)
			paras = append(paras, strings.TrimRight(p[:cut], "\n "))
			p = strings.TrimLeft(p[cut:], "\n ")
		}
		if strings.TrimSpace(p) != "" {
			paras = append(paras, p)
		}
	}
	var packed []string
	cur := ""
	for _, p := range paras {
		switch {
		case cur == "":
			cur = p
		case len(cur)+2+len(p) <= budget:
			cur += "\n\n" + p
		default:
			packed = append(packed, cur)
			cur = p
		}
	}
	if cur != "" {
		packed = append(packed, cur)
	}
	rest := len(packed) > MaxParts
	if rest {
		packed = packed[:MaxParts]
	}
	out := make([]string, 0, len(packed))
	for i, p := range packed {
		s := fmt.Sprintf("%s (%d/%d)\n%s", head, i+1, len(packed), p)
		if rest && i == len(packed)-1 {
			s += "\n\n" + tail
		}
		out = append(out, s)
	}
	return out
}

// cutAt is where a too-long paragraph is cut at or before n bytes: the last
// line break, else the last space, else a rune boundary.
func cutAt(s string, n int) int {
	if i := strings.LastIndex(s[:n], "\n"); i > n/2 {
		return i + 1
	}
	if i := strings.LastIndex(s[:n], " "); i > n/2 {
		return i + 1
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return n
}

// gateView is what the relay knows of the gates of every squad the
// attachment serves.
type gateView struct {
	open    map[string]bool // open gate ids (and any id whose record does not read)
	known   map[string]bool // every gate id with a record
	lines   []string        // the lines of every open gate's question text
	threads map[string]bool // the event ids of every stored gate question and re-ask
}

func (h *Handler) gateView(now time.Time) (gateView, error) {
	g := gateView{open: map[string]bool{}, known: map[string]bool{}, threads: map[string]bool{}}
	for _, c := range append([]*gateverify.Core{h.Core}, h.Cores...) {
		if c == nil {
			continue
		}
		ids, err := c.Store.IDs()
		if err != nil {
			return g, err
		}
		for _, id := range ids {
			key := normalizeLine(id) // the ids compared, normalized as the lines are
			g.known[key] = true
			if q, err := c.Store.Question(id); err == nil && q != nil {
				g.threads[q.EventID] = true
			}
			if ra, err := c.Store.ReadReask(id); err == nil && ra != nil {
				g.threads[ra.EventID] = true
			}
			rec, err := c.Store.Load(id)
			if err != nil {
				g.open[key] = true // fail closed on the id
				continue
			}
			if c.Store.State(rec, now) != gatestore.StateOpen {
				continue
			}
			g.open[key] = true
			for _, l := range strings.Split(rec.Text, "\n") {
				if l = strings.TrimSpace(normalizeLine(l)); l != "" {
					g.lines = append(g.lines, l)
				}
			}
		}
	}
	return g, nil
}

// refuse runs the gate-shape check over every line of a reply and returns
// the first line it refuses, trimmed (its sha256 is the refusal's).
func (g gateView) refuse(body string) (string, bool) {
	for _, line := range strings.Split(body, "\n") {
		if g.gateShaped(line) {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

// gateShaped is the refusal narrowed to the risk (brief 31-4, number 2): a
// line that is, as a whole, an answer form; a line naming an open gate's
// id; a line carrying a line of an open gate's question. A sentence that
// merely contains yes or no is not refused.
func (g gateView) gateShaped(line string) bool {
	t := strings.TrimSpace(normalizeLine(line))
	if t == "" {
		return false
	}
	if answerForm(t, g.known) {
		return true
	}
	for _, tok := range idTokens(t) {
		if g.open[tok] {
			return true
		}
	}
	for _, q := range g.lines {
		if utf8.RuneCountInString(q) >= MinQuestionLine && strings.Contains(t, q) {
			return true
		}
	}
	return false
}

// normalizeLine is the one normalization of the three checks (Codex P2-4
// and its re-check #3), applied to the candidate line and to the gate ids
// and question lines it is compared with: NFKC; every format character
// (Unicode Cf: zero-width spaces, joiners, bidi controls) and every
// nonspacing or enclosing mark (Mn, Me: variation selectors, combining
// marks) removed, every other control made a space; case folding; then the
// confusables fold below.
func normalizeLine(s string) string {
	s = norm.NFKC.String(s)
	s = strings.Map(func(r rune) rune {
		switch {
		case unicode.In(r, unicode.Cf, unicode.Mn, unicode.Me):
			return -1
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, s)
	return strings.Map(func(r rune) rune {
		if l, ok := confusables[r]; ok {
			return l
		}
		return r
	}, cases.Fold().String(s))
}

// confusables folds the Cyrillic and Greek lowercase letters that read as
// Latin letters to those letters (a hand list for the letters of the answer
// words and gate ids: a c e o p s x y i k h n m t v u; after case folding,
// so the capitals are covered). Applied on both sides of every comparison.
var confusables = map[rune]rune{
	// Cyrillic
	'а': 'a', 'с': 'c', 'е': 'e', 'о': 'o', 'р': 'p', 'ѕ': 's', 'х': 'x', 'у': 'y', 'і': 'i', 'ј': 'j',
	'к': 'k', 'һ': 'h', 'п': 'n', 'м': 'm', 'т': 't', 'ѵ': 'v', 'ԁ': 'd', 'ԛ': 'q', 'ԝ': 'w',
	// Greek
	'α': 'a', 'ϲ': 'c', 'ε': 'e', 'ο': 'o', 'ρ': 'p', 'χ': 'x', 'γ': 'y', 'ι': 'i', 'κ': 'k',
	'η': 'n', 'τ': 't', 'ν': 'v', 'υ': 'u', 'μ': 'u',
}

var (
	answerVerbs = map[string]bool{"yes": true, "no": true, "approve": true, "deny": true}
	// gateIDShape is a gate id as the squads mint them: rel-..., gate-...,
	// or <kind>-<20 hex>
	gateIDShape = regexp.MustCompile(`^((rel|gate)-[a-z0-9._-]+|[a-z][a-z0-9]*-[0-9a-f]{20})$`)
	hexPrefixRe = regexp.MustCompile(`^[0-9a-f]{4,64}$`)
)

// answerForm reports whether a whole line is an answer form: gateverify's
// inbound parser (gatestore.ParseAnswer) accepts it, or, after trimming
// punctuation and case, it is yes, no, approve or deny alone or followed by
// a gate id and the rest of the typed tuple.
func answerForm(line string, known map[string]bool) bool {
	if _, err := gatestore.ParseAnswer(line); err == nil {
		return true
	}
	var w []string
	for _, f := range strings.Fields(strings.ToLower(line)) {
		if f = strings.TrimFunc(f, func(r rune) bool { return unicode.IsPunct(r) || unicode.IsSymbol(r) }); f != "" {
			w = append(w, f)
		}
	}
	if len(w) == 0 || len(w) > 4 || !answerVerbs[w[0]] {
		return false
	}
	rest := w[1:]
	switch {
	case len(rest) == 0:
		return true
	case !known[rest[0]] && !gateIDShape.MatchString(rest[0]):
		return false
	case len(rest) >= 2 && !typedClass(rest[1]):
		return false
	case len(rest) == 3 && !hexPrefixRe.MatchString(rest[2]):
		return false
	}
	return true
}

func typedClass(c string) bool {
	switch c {
	case gatetarget.ClassRelease, gatetarget.ClassSend, gatetarget.ClassCredential, gatetarget.ClassGrow:
		return true
	}
	return false
}

// idTokens are the gate-id-shaped words of a line.
func idTokens(line string) []string {
	f := strings.FieldsFunc(strings.ToLower(line), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-')
	})
	for i, t := range f {
		f[i] = strings.TrimRight(t, ".-_")
	}
	return f
}

// The relay records.

func relayRecordPath(dir, requestID string) string { return filepath.Join(dir, requestID+".json") }

// readRelayRecord reads one record strictly: every field its exact shape
// (status.DecodeRelayRecord); anything else is relay_record_invalid and the
// relay refuses the record whole.
func readRelayRecord(dir, requestID string) (*status.RelayRecord, error) {
	raw, err := os.ReadFile(relayRecordPath(dir, requestID))
	if err != nil {
		return nil, err
	}
	return status.DecodeRelayRecord(raw, requestID)
}

func loadRelayRecords(dir string) ([]*status.RelayRecord, []string) {
	paths, _ := filepath.Glob(filepath.Join(dir, "*.json"))
	var out []*status.RelayRecord
	var problems []string
	for _, p := range paths {
		r, err := readRelayRecord(dir, strings.TrimSuffix(filepath.Base(p), ".json"))
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		out = append(out, r)
	}
	return out, problems
}

func writeRelayRecord(dir string, r *status.RelayRecord, now time.Time) error {
	r.Schema, r.UpdatedAt = status.RelayRecordSchema, now.UTC()
	if r.Seen == nil {
		r.Seen = []string{}
	}
	if r.Turns == nil {
		r.Turns = []status.RelayTurn{}
	}
	if r.Posts == nil {
		r.Posts = []status.RelayPost{}
	}
	if r.Refusals == nil {
		r.Refusals = []status.RelayRefusal{}
	}
	if err := r.Validate(r.RequestID); err != nil {
		return err
	}
	if relayRecordWrite != nil {
		if err := relayRecordWrite(); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return atomicfile.Write(relayRecordPath(dir, r.RequestID), append(raw, '\n'), 0o600)
}

// continuation is the request a DM continues: the record one of whose
// relay posts the event replies to. A DM in a gate question's thread is
// never a continuation (the gate path keeps it, as today).
func (h *Handler) continuation(e nostr.Event) *status.RelayRecord {
	refs := nostr.ThreadRefs(e)
	if len(refs) == 0 {
		return nil
	}
	g, err := h.gateView(h.now())
	if err != nil {
		return nil
	}
	for _, ref := range refs {
		if g.threads[ref] {
			return nil
		}
	}
	recs, _ := loadRelayRecords(status.RelayDir(h.Target.Root))
	for _, r := range recs {
		for _, ref := range refs {
			if slices.ContainsFunc(r.Posts, func(p status.RelayPost) bool { return slices.Contains(p.Events, ref) }) {
				return r
			}
		}
	}
	return nil
}

// RelayRecordFailed is the receipt's code when the request's record
// cannot be written: the question is not posted to the lead and the DM is
// not marked handled, so serve's redelivery works.
const RelayRecordFailed = "relay_record_failed"

// openTurn writes the request's record with the owner's new turn before
// the question goes to the lead (Codex P2-6): a fresh request's record, or
// a continuation's with one more turn. undo takes the turn back when the
// delivery fails.
func (h *Handler) openTurn(e nostr.Event, requestID string, cont *status.RelayRecord, now time.Time) (*status.RelayRecord, func(), error) {
	dir := status.RelayDir(h.Target.Root)
	rec := cont
	if rec == nil {
		rec = &status.RelayRecord{RequestID: requestID, Thread: CockpitThread(requestID), Lead: h.Target.Handle, RootEvent: e.ID, PostTo: e.ID}
		// the owner's DM in a gate thread: the relay posts top level, never
		// into the gate question's thread
		if g, err := h.gateView(now); err != nil {
			rec.PostTo = ""
		} else {
			for _, ref := range nostr.ThreadRefs(e) {
				if g.threads[ref] {
					rec.PostTo = ""
				}
			}
		}
	}
	wasClosed := rec.Closed
	rec.Turns = append(rec.Turns, status.RelayTurn{Event: e.ID, At: now.UTC()})
	rec.Closed = false
	// the owner's next turn clears an unconfirmed post (the owner may have
	// received it; it is never published again, and the conversation goes on)
	var cleared []int
	for i := range rec.Refusals {
		if rec.Refusals[i].Reason == status.RelayPostUnconfirmed && rec.Refusals[i].ClearedAt == nil {
			rec.Refusals[i].ClearedAt = timePtr(now)
			cleared = append(cleared, i)
		}
	}
	if err := writeRelayRecord(dir, rec, now); err != nil {
		return nil, nil, err
	}
	undo := func() {
		if cont == nil {
			_ = os.Remove(relayRecordPath(dir, requestID))
			return
		}
		rec.Turns, rec.Closed = rec.Turns[:len(rec.Turns)-1], wasClosed
		for _, i := range cleared {
			rec.Refusals[i].ClearedAt = nil
		}
		_ = writeRelayRecord(dir, rec, now)
	}
	return rec, undo, nil
}

// finishTurn records where the turn went: the AMQ message, and for a fresh
// request the lead it was forwarded to.
func (h *Handler) finishTurn(rec *status.RelayRecord, fresh bool, d Delivery, now time.Time) error {
	if fresh && d.LeadID != "" {
		rec.Lead, rec.Route = d.LeadHandle, d.Lead
	}
	rec.Turns[len(rec.Turns)-1].MessageID = firstNonEmpty(d.LeadID, d.MailboxID)
	return writeRelayRecord(status.RelayDir(h.Target.Root), rec, now)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
