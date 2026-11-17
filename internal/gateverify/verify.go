// Package gateverify verifies owner-signed gate answers for the four typed
// classes (plan 7.3, R32), ported from spike S1 (spike/gateverify) onto the
// core's gate store. Every input that decides an outcome (owner pin, body
// pin, DM channel, the stored gate record and question, the clock) is the
// core's own state; the event supplies only what the owner signed.
package gateverify

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// Rejection codes; each negative test asserts one.
const (
	RejectMalformed     = "malformed_event"
	RejectBadID         = "id_mismatch"
	RejectWrongPubkey   = "wrong_pubkey"
	RejectBadSignature  = "bad_signature"
	RejectWrongKind     = "wrong_kind"
	RejectWrongChannel  = "wrong_channel"
	RejectBadTuple      = "bad_tuple"
	RejectGateMismatch  = "gate_mismatch"
	RejectClassMismatch = "class_mismatch"
	RejectDigest        = "digest_mismatch"
	RejectWindow        = "outside_window"
	RejectFuture        = "created_in_future"
	RejectThread        = "not_in_gate_thread"
	RejectWrongThread   = "wrong_gate_thread"
	RejectQuestion      = "bad_gate_question"
	RejectAltered       = "question_altered"
	RejectHistory       = "history_unknown"
	RejectReplayed      = gatestore.RejectReplayed
	RejectGateRecord    = "bad_gate_record"
	RejectSender        = "wrong_amq_sender"
	RejectLabel         = "bad_label"
	RejectFetch         = "fetch_failed"
	// RejectVerifyTimeout is a verification its context ended (a gate lock
	// not taken in time; 5-amq-B fix round 2).
	RejectVerifyTimeout = "verify_timeout"
	// RejectAlteredHistory is a deletion since the question whose target the
	// relay hides (5-amq-B fix round 2, item 3).
	RejectAlteredHistory = "altered_history"
	RejectInsecure       = "owner_grants_insecure"
	// RejectNoSquad: the record carries no squad identity (a record from
	// before amendment 28), or another squad's (4-5 re-pass).
	RejectNoSquad = "no_squad_identity"
	// RejectLapsed is an answer in the gate thread that passed every check
	// but the window: created, or presented, after not_after. The core asks
	// the gate again in its thread with a new window, once (A1-3).
	RejectLapsed = "gate_lapsed"
	// RejectBeforeReask is an answer dated before the gate's window re-ask:
	// it does not approve the re-asked question (A0 row 7).
	RejectBeforeReask = "answered_before_reask"
	// RejectPublish is a window re-ask the relay did not take: reserved, not
	// published; the same answer delivered again retries it (the handler
	// does not record it as handled).
	RejectPublish = "reask_publish_failed"
)

// Rejection is a typed refusal.
type Rejection struct {
	Code   string
	Detail string
}

func (r *Rejection) Error() string { return r.Code + ": " + r.Detail }

func reject(code, format string, args ...any) error {
	return &Rejection{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Code extracts the rejection code, "" for an untyped error.
func Code(err error) string {
	var r *Rejection
	if errors.As(err, &r) {
		return r.Code
	}
	if errors.Is(err, gatestore.ErrConsumed) {
		return RejectReplayed
	}
	if errors.Is(err, gatestore.ErrLockTimeout) {
		return RejectVerifyTimeout
	}
	return ""
}

// Pin is the core's trusted state for one transport binding.
type Pin struct {
	Owner   string // 64 hex: the owner's Buzz key (BIP-340)
	Channel string // the DM channel id (NIP-29 h tag)
	Body    string // 64 hex: the share's body key, the signer of gate questions and re-asks
}

func (p Pin) valid() error {
	if !nostr.IsHex64(p.Owner) || !nostr.IsHex64(p.Body) || p.Channel == "" {
		return reject(RejectGateRecord, "pin needs a 64-hex owner, a 64-hex body and a channel")
	}
	return nil
}

// Outcome is a verified, classified answer.
type Outcome struct {
	Decision string // approve | deny
	Event    nostr.Event
	Answer   gatestore.Answer
}

// Verify is VerifyReasked for a gate with no window re-ask.
func Verify(raw []byte, rec gatestore.Record, q *gatestore.Question, reask *gatestore.Reask, pin Pin, now time.Time) (Outcome, error) {
	return VerifyReasked(raw, rec, q, reask, nil, pin, now)
}

// VerifyReasked checks raw (one NIP-01 event as received) against the pin
// and the stored gate record, question, re-ask and window re-ask (nil when
// none), at now. The answer must reply in the gate thread: its e tags name
// the stored question, the gate's re-ask or its window re-ask (both posted
// in that thread). Clock rules (plan 7.3):
//
//	not_before (= issued_at - 60 s) <= created_at <= not_after
//	created_at - 60 s <= now <= not_after     (now: the verifier's clock)
//
// After a window re-ask (A1-3) the window is the re-ask's: created_at
// strictly after the re-ask's signed created_at, and created_at and now no
// later than its WindowEnd; the event that triggered it never approves.
//
// A valid deny is an Outcome with Decision "deny", not an error; it must be
// owner-signed like an approve, so a model cannot cancel a gate either. The
// thread check runs before the not_after checks, so a not_in_gate_thread
// refusal means every check but not_after passed (the re-ask rule relies
// on it), and a gate_lapsed refusal means every check passed, the thread
// included (the window re-ask relies on it).
func VerifyReasked(raw []byte, rec gatestore.Record, q *gatestore.Question, reask, window *gatestore.Reask, pin Pin, now time.Time) (Outcome, error) {
	if err := rec.Validate(); err != nil {
		return Outcome{}, reject(RejectGateRecord, "%v", err)
	}
	// the answer binds to one squad's record through the question's text
	// and tags; a record without a squad identity is unverifiable
	if rec.Squad == "" {
		return Outcome{}, reject(RejectNoSquad, "gate record %s carries no squad identity; raise it again", rec.GateID)
	}
	if err := pin.valid(); err != nil {
		return Outcome{}, err
	}
	if pin.Owner != rec.OwnerPubKey {
		return Outcome{}, reject(RejectGateRecord, "pinned owner %s is not the gate record's owner %s", pin.Owner, rec.OwnerPubKey)
	}
	e, err := nostr.ParseEvent(raw)
	if err != nil {
		return Outcome{}, reject(RejectMalformed, "%v", err)
	}
	if !e.CheckID() {
		return Outcome{}, reject(RejectBadID, "event id is not the NIP-01 hash of its content")
	}
	if e.PubKey != pin.Owner {
		return Outcome{}, reject(RejectWrongPubkey, "signed by %s, the owner is %s", e.PubKey, pin.Owner)
	}
	if err := e.VerifySignature(); err != nil {
		return Outcome{}, reject(RejectBadSignature, "%v", err)
	}
	if e.Kind != 9 {
		return Outcome{}, reject(RejectWrongKind, "kind %d, want 9", e.Kind)
	}
	if h := nostr.TagValues(e, "h"); len(h) != 1 || h[0] != pin.Channel {
		return Outcome{}, reject(RejectWrongChannel, "h tags %q, want exactly [%q]", h, pin.Channel)
	}
	a, err := gatestore.ParseAnswer(e.Content)
	if err != nil {
		// a bare yes or no (brief 3.1.1-1) names no gate: it is this record's
		// answer only through its thread, checked below like any answer's
		verb, ok := gatestore.ParseBare(e.Content)
		if !ok {
			return Outcome{}, reject(RejectBadTuple, "%v", err)
		}
		a = rec.BareAnswer(verb)
	}
	if a.GateID != rec.GateID {
		return Outcome{}, reject(RejectGateMismatch, "answer names gate %s, verifying gate %s", a.GateID, rec.GateID)
	}
	if a.Class != rec.TypedClass {
		return Outcome{}, reject(RejectClassMismatch, "answer class %s, gate class %s", a.Class, rec.TypedClass)
	}
	// The prefix is compared against the stored record's full digest only.
	if !strings.HasPrefix(strings.TrimPrefix(rec.Digest, "sha256:"), a.DigestPrefix) {
		return Outcome{}, reject(RejectDigest, "prefix %s is not a prefix of %s", a.DigestPrefix, rec.Digest)
	}
	_, nb, na, _ := rec.Window()
	created := time.Unix(e.CreatedAt, 0)
	if created.Before(nb) {
		return Outcome{}, reject(RejectWindow, "created_at %s outside [%s, %s]", created.UTC().Format(time.RFC3339), rec.NotBefore, rec.NotAfter)
	}
	if now.Before(created.Add(-gatestore.ClockSkew)) {
		return Outcome{}, reject(RejectFuture, "created_at %s is more than 60 s after the verifier clock %s", created.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	if err := checkQuestion(q, rec, pin); err != nil {
		return Outcome{}, err
	}
	// The answer's squad identity: the squad tag of the event it threads to
	// (the question or the re-ask, both checked against the record below),
	// and its own squad tag when it carries one, which must be the record's.
	// Buzz thread replies carry only h, e and p tags (S1 g-owner-02), so a
	// tag on the answer is checked, not required; a threaded answer always
	// names one squad's question through its e tag (4-5 re-pass).
	if tags := nostr.TagValues(e, "squad"); len(tags) > 0 && !slices.Equal(tags, []string{rec.Squad}) {
		return Outcome{}, reject(RejectNoSquad, "answer carries squad %v, the record is squad %s", tags, rec.Squad)
	}
	// Every thread reference (e tags marked reply or root, and unmarked ones)
	// must name this gate's thread: its question, or its stored re-ask. One
	// reference to anything else (another gate's question) is a conflicting
	// thread and is refused, whatever the other tags say.
	ours := []string{q.EventID}
	if reask != nil && reask.GateID == rec.GateID && checkReask(reask, q, pin) == nil {
		ours = append(ours, reask.EventID)
	}
	var winAt, winEnd time.Time
	win := windowValid(window, rec, q, pin)
	if win {
		var err error
		if winAt, winEnd, err = window.WindowEnd(rec); err != nil {
			win = false
		} else {
			ours = append(ours, window.EventID)
		}
	}
	refs := nostr.ThreadRefs(e)
	inThread := len(refs) > 0
	for _, ref := range refs {
		inThread = inThread && slices.Contains(ours, ref)
	}
	switch {
	case inThread:
	case len(refs) == 0:
		return Outcome{Decision: a.Verb, Event: e, Answer: a}, reject(RejectThread, "answer does not reply in the thread of gate question %s", q.EventID)
	default:
		return Outcome{}, reject(RejectWrongThread, "answer replies to %v, not to gate question %s", refs, q.EventID)
	}
	o := Outcome{Decision: a.Verb, Event: e, Answer: a}
	if win {
		if e.ID == window.TriggerEventID || !created.After(winAt) {
			return Outcome{}, reject(RejectBeforeReask, "the answer is dated %s, not after the question was asked again at %s (event %s); answer again in the thread",
				created.UTC().Format(time.RFC3339), winAt.UTC().Format(time.RFC3339), window.EventID)
		}
		if created.After(winEnd) || now.After(winEnd) {
			return Outcome{}, reject(RejectWindow, "created_at %s, presented at %s: after the asked-again window closed at %s",
				created.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339), winEnd.UTC().Format(time.RFC3339))
		}
		return o, nil
	}
	if created.After(na) {
		return o, reject(RejectLapsed, "created_at %s, after the gate closed at %s", created.UTC().Format(time.RFC3339), rec.NotAfter)
	}
	if now.After(na) {
		return o, reject(RejectLapsed, "presented at %s, after the gate closed at %s", now.UTC().Format(time.RFC3339), rec.NotAfter)
	}
	return o, nil
}

// checkQuestion proves the stored question is the body-signed kind 9 in the
// DM channel whose content is exactly the record's text. An answer threaded
// under it commits, through its e tag, to the question's id, which is a hash
// of that text with the full digest (S1 1.1).
func checkQuestion(q *gatestore.Question, rec gatestore.Record, pin Pin) error {
	if q == nil {
		return reject(RejectQuestion, "no stored gate question for %s", rec.GateID)
	}
	e, err := nostr.ParseEvent([]byte(q.RawEvent))
	switch {
	case err != nil:
		return reject(RejectQuestion, "%v", err)
	case q.GateID != rec.GateID || e.ID != q.EventID || !e.CheckID():
		return reject(RejectQuestion, "question id does not match its event")
	case e.PubKey != pin.Body:
		return reject(RejectQuestion, "question is not signed by the share body")
	case e.VerifySignature() != nil:
		return reject(RejectQuestion, "question signature does not verify")
	case e.Kind != 9 || !slices.Equal(nostr.TagValues(e, "h"), []string{pin.Channel}):
		return reject(RejectQuestion, "question is not a kind 9 in the DM channel")
	case e.Content != rec.Text:
		return reject(RejectQuestion, "question content is not the gate record's text")
	case !slices.Equal(nostr.TagValues(e, "squad"), []string{rec.Squad}):
		return reject(RejectQuestion, "question does not carry the record's squad identity")
	}
	return nil
}

// checkReask proves the stored re-ask is the body's kind 9 in the gate
// thread.
func checkReask(r *gatestore.Reask, q *gatestore.Question, pin Pin) error {
	e, err := nostr.ParseEvent([]byte(r.RawEvent))
	switch {
	case err != nil || e.ID != r.EventID || !e.CheckID() || e.VerifySignature() != nil:
		return reject(RejectQuestion, "re-ask record does not verify")
	case e.PubKey != pin.Body || e.Kind != 9 || !slices.Equal(nostr.TagValues(e, "h"), []string{pin.Channel}):
		return reject(RejectQuestion, "re-ask is not the body's kind 9 in the DM channel")
	case !slices.Contains(nostr.ThreadRefs(e), q.EventID):
		return reject(RejectQuestion, "re-ask is not in the gate thread")
	case !slices.Equal(nostr.TagValues(e, "squad"), nostr.TagValues(mustEvent(q.RawEvent), "squad")):
		return reject(RejectQuestion, "re-ask does not carry the question's squad identity")
	}
	return nil
}

// windowValid reports whether a stored window re-ask is this gate's: the
// body's kind 9 in the gate thread (checkReask) whose signed content is
// exactly WindowReaskText of the record. Anything else opens no window, is
// never published, and its history is not read.
func windowValid(w *gatestore.Reask, rec gatestore.Record, q *gatestore.Question, pin Pin) bool {
	return w != nil && q != nil && w.GateID == rec.GateID && checkReask(w, q, pin) == nil && mustEvent(w.RawEvent).Content == WindowReaskText(rec)
}

func mustEvent(raw string) nostr.Event {
	e, _ := nostr.ParseEvent([]byte(raw))
	return e
}
