package main

// Verification of one owner answer. Every input that decides the outcome
// (owner pin, body pin, DM channel, the stored gate record and question,
// the clock) is the core's own state; the event supplies only what the owner
// signed.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Rejection codes. Each negative test asserts one of these.
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
	RejectQuestion      = "bad_gate_question"
	RejectReplayed      = "already_consumed"
	RejectGateRecord    = "bad_gate_record"
	RejectSender        = "wrong_amq_sender"
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

// RejectionCode extracts the code, or "" for a non-typed error.
func RejectionCode(err error) string {
	var r *Rejection
	if errors.As(err, &r) {
		return r.Code
	}
	return ""
}

// Pin is the core's trusted state for one gate answer.
type Pin struct {
	Owner   string // 64 hex, from the owner npub the core was configured with
	Channel string // the share's DM channel id (NIP-29 h tag)
	// RequireThread demands that the answer reply to the stored gate
	// question, and that this question is the body-signed event whose
	// content is exactly the record's text. Body is then required.
	RequireThread bool
	Body          string // 64 hex, the share's enrolled body pubkey
}

// Outcome is a verified, classified answer.
type Outcome struct {
	Decision string // "approve" or "deny"
	Event    Event
	Answer   Answer
}

// Verify checks raw (one NIP-01 event as received) against the pin, the
// stored gate record and question, at now.
//
// Clock rules (clockSkew = 60 s, gate.go):
//
//	not_before (= issued_at - 60 s) <= created_at <= not_after
//	created_at - 60 s <= now <= not_after
//
// The second line holds the presentation time, which is the verifier's
// clock. It refuses an answer dated more than 60 s ahead of the verifier
// ("created_in_future"), and any presentation after not_after, with no
// grace.
//
// A valid deny is an Outcome with Decision "deny", not an error. It must
// never be mistaken for an approve, and it must still be owner-signed to
// count: a foreign deny is rejected like any forgery, so a model cannot
// cancel a gate in the owner's name either.
func Verify(raw []byte, gate GateRecord, question *GateQuestion, pin Pin, now time.Time) (Outcome, error) {
	if err := gate.validate(); err != nil {
		return Outcome{}, reject(RejectGateRecord, "%v", err)
	}
	if !hex64.MatchString(pin.Owner) || pin.Channel == "" {
		return Outcome{}, reject(RejectGateRecord, "pin needs a 64-hex owner and a channel")
	}
	e, err := ParseEvent(raw)
	if err != nil {
		return Outcome{}, reject(RejectMalformed, "%v", err)
	}
	if !e.CheckID() {
		return Outcome{}, reject(RejectBadID, "event id is not the NIP-01 hash of its content")
	}
	if e.PubKey != pin.Owner {
		return Outcome{}, reject(RejectWrongPubkey, "signed by %s, pinned owner is %s", e.PubKey, pin.Owner)
	}
	if err := e.VerifySignature(); err != nil {
		return Outcome{}, reject(RejectBadSignature, "%v", err)
	}
	if e.Kind != 9 {
		return Outcome{}, reject(RejectWrongKind, "kind %d, want 9", e.Kind)
	}
	if h := tagValues(e, "h"); len(h) != 1 || h[0] != pin.Channel {
		return Outcome{}, reject(RejectWrongChannel, "h tags %q, want exactly [%q]", h, pin.Channel)
	}
	a, err := ParseAnswer(e.Content)
	if err != nil {
		return Outcome{}, reject(RejectBadTuple, "%v", err)
	}
	if a.GateID != gate.GateID {
		return Outcome{}, reject(RejectGateMismatch, "answer names gate %s, verifying gate %s", a.GateID, gate.GateID)
	}
	if a.Class != gate.Class {
		return Outcome{}, reject(RejectClassMismatch, "answer class %s, gate class %s", a.Class, gate.Class)
	}
	// The prefix is compared against the stored record's full digest only.
	if !strings.HasPrefix(strings.TrimPrefix(gate.Digest, "sha256:"), a.DigestPrefix) {
		return Outcome{}, reject(RejectDigest, "prefix %s is not a prefix of %s", a.DigestPrefix, gate.Digest)
	}
	_, nb, na, _ := gate.window()
	created := time.Unix(e.CreatedAt, 0)
	if created.Before(nb) || created.After(na) {
		return Outcome{}, reject(RejectWindow, "created_at %s outside [%s, %s]", created.UTC().Format(time.RFC3339), gate.NotBefore, gate.NotAfter)
	}
	if now.Before(created.Add(-clockSkew)) {
		return Outcome{}, reject(RejectFuture, "created_at %s is more than 60 s after the verifier clock %s", created.UTC().Format(time.RFC3339), now.UTC().Format(time.RFC3339))
	}
	if now.After(na) {
		return Outcome{}, reject(RejectWindow, "presented at %s, after the gate closed at %s", now.UTC().Format(time.RFC3339), gate.NotAfter)
	}
	if pin.RequireThread {
		if err := checkQuestion(question, gate, pin); err != nil {
			return Outcome{}, err
		}
		if !slices.Contains(threadRefs(e), question.EventID) {
			return Outcome{}, reject(RejectThread, "answer does not reply to gate question %s", question.EventID)
		}
	}
	return Outcome{Decision: a.Verb, Event: e, Answer: a}, nil
}

// checkQuestion proves the stored question is the body-signed kind 9 in the
// DM channel whose content is exactly the record's text. An answer threaded
// under it then commits, through its e tag, to the question's id, which is
// a hash of that text with the full digest and the PR list.
func checkQuestion(q *GateQuestion, gate GateRecord, pin Pin) error {
	if q == nil {
		return reject(RejectQuestion, "no stored gate question for %s", gate.GateID)
	}
	if !hex64.MatchString(pin.Body) {
		return reject(RejectQuestion, "thread binding needs the pinned body pubkey")
	}
	e, err := ParseEvent([]byte(q.RawEvent))
	switch {
	case err != nil:
		return reject(RejectQuestion, "%v", err)
	case e.ID != q.EventID || !e.CheckID():
		return reject(RejectQuestion, "question id does not match its event")
	case e.PubKey != pin.Body:
		return reject(RejectQuestion, "question is not signed by the share body")
	case e.VerifySignature() != nil:
		return reject(RejectQuestion, "question signature does not verify")
	case e.Kind != 9 || !slices.Equal(tagValues(e, "h"), []string{pin.Channel}):
		return reject(RejectQuestion, "question is not a kind 9 in the DM channel")
	case e.Content != gate.Text:
		return reject(RejectQuestion, "question content is not the gate record's text")
	}
	return nil
}

// Ledger records consumed answers. Order of a consume (cmdVerify, e2e):
// first verify; refuse if already consumed; write the envelope (fsynced);
// then mark consumed (fsynced). A crash between the two leaves, at worst, an
// unconsumed approval, which re-verifies and is consumed on retry; it never
// leaves a consumed gate without its envelope.
//
// Threat model: the ledger is a directory that same-UID processes can
// write. On the phase 1 host (a single-user Mac, plan Q1), a seat altering
// it is accepted, not fixed. Production moves it under the AMQ root, with
// the Go core as the only writer.
type Ledger struct{ Dir string }

// LedgerFor returns the ledger under an AMQ root.
func LedgerFor(root string) Ledger {
	return Ledger{Dir: filepath.Join(root, "extensions", "amq-squad", "ledger")}
}

func (l Ledger) markers(o Outcome) []string {
	return []string{filepath.Join(l.Dir, "gate-"+o.Answer.GateID), filepath.Join(l.Dir, "event-"+o.Event.ID)}
}

// Consumed reports a replay: the gate already has an answer, or this event
// was used.
func (l Ledger) Consumed(o Outcome) error {
	for _, m := range l.markers(o) {
		if _, err := os.Lstat(m); err == nil {
			return reject(RejectReplayed, "%s was already consumed", filepath.Base(m))
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// Consume marks the answer used: the gate marker first, created
// exclusively, then the event marker, each fsynced with its directory. The
// first verified answer (approve or deny) closes the gate.
func (l Ledger) Consume(o Outcome) error {
	line := []byte(fmt.Sprintf("%s %s %s\n", o.Decision, o.Event.ID, time.Now().UTC().Format(time.RFC3339)))
	for _, m := range l.markers(o) {
		if err := writeOnce(m, line); errors.Is(err, os.ErrExist) {
			return reject(RejectReplayed, "%s was already consumed", filepath.Base(m))
		} else if err != nil {
			return err
		}
	}
	return nil
}
