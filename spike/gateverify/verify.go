package main

// Verification of one owner answer. Every input that decides the outcome
// (owner pin, DM channel, gate record, clock) is the core's own state; the
// event supplies only what the owner signed.

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
	RejectThread        = "not_in_gate_thread"
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
	// RequireThread, when set, demands that the answer reply to the gate
	// question event (gate.QuestionEventID) as a Buzz thread reply.
	RequireThread bool
}

// Outcome is a verified, classified answer.
type Outcome struct {
	Decision string // "approve" or "deny"
	Event    Event
	Answer   Answer
}

// Verify checks raw (one NIP-01 event as received) against the pin and the
// gate record at now. A valid deny is an Outcome with Decision "deny", not
// an error: it must never be mistaken for an approve, and it must still be
// owner-signed to count (an unsigned or foreign deny is rejected like any
// other forgery, so a model cannot cancel a gate in the owner's name
// either).
func Verify(raw []byte, gate GateRecord, pin Pin, now time.Time) (Outcome, error) {
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
	if !strings.HasPrefix(strings.TrimPrefix(gate.Digest, "sha256:"), a.DigestPrefix) {
		return Outcome{}, reject(RejectDigest, "prefix %s is not a prefix of %s", a.DigestPrefix, gate.Digest)
	}
	nb, na, _ := gate.window()
	created := time.Unix(e.CreatedAt, 0)
	if created.Before(nb) || created.After(na) {
		return Outcome{}, reject(RejectWindow, "created_at %s outside [%s, %s]", created.UTC().Format(time.RFC3339), gate.NotBefore, gate.NotAfter)
	}
	// The answer must also arrive inside the window: a signed answer held
	// back and replayed after the deadline is refused on the core's clock,
	// not only on the owner's claimed created_at.
	if now.After(na.Add(answerSkew)) {
		return Outcome{}, reject(RejectWindow, "verified at %s, after the gate closed at %s", now.UTC().Format(time.RFC3339), gate.NotAfter)
	}
	if pin.RequireThread {
		if gate.QuestionEventID == "" || !slices.Contains(threadRefs(e), gate.QuestionEventID) {
			return Outcome{}, reject(RejectThread, "answer does not reply to gate question %s", gate.QuestionEventID)
		}
	}
	return Outcome{Decision: a.Verb, Event: e, Answer: a}, nil
}

// Consume records the answer once: one event id, and one answer per gate.
// The first verified answer (approve or deny) closes the gate; a second
// answer, or the same event presented again, is a replay.
func Consume(ledgerDir string, o Outcome) error {
	if err := os.MkdirAll(ledgerDir, 0o700); err != nil {
		return err
	}
	// The gate claim goes first, so a refused second answer leaves no
	// partial record behind.
	for _, name := range []string{"gate-" + o.Answer.GateID, "event-" + o.Event.ID} {
		f, err := os.OpenFile(filepath.Join(ledgerDir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			return reject(RejectReplayed, "%s was already consumed", name)
		}
		if err != nil {
			return err
		}
		_, werr := fmt.Fprintf(f, "%s %s %s\n", o.Decision, o.Event.ID, time.Now().UTC().Format(time.RFC3339))
		cerr := f.Close()
		if werr != nil || cerr != nil {
			return errors.Join(werr, cerr)
		}
	}
	return nil
}
