package gateverify

import (
	"errors"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

// The Buzz-signed seat clear (phase 5, plan 4.3 and 7.3). A clear request is
// a typed gate of action quarantine_clear; the clear completes only on the
// owner's answer, verified exactly as the four typed gates' are (owner key,
// thread binding, squad identity, single use: Verifier.Accepted), and then
// only for the handle, nonce, incarnation and config diff the request names.
const (
	// RejectClearUnsigned: the clear has no accepted owner answer (none yet,
	// a deny, an expired gate, or an answer that does not verify).
	RejectClearUnsigned = "clear_unsigned"
	// RejectClearHandle: the answer is for another seat.
	RejectClearHandle = "clear_wrong_handle"
)

// ClearProof is what a verified answer contributes to the signed clear
// record.
type ClearProof struct {
	GateID  string
	EventID string
}

// namespaceOf is the namespace the stored approval binds. A clear is
// verified by processes that hold only the session root (the commit step,
// the scheduler's exit check), so the namespace is read from the issuer-signed
// envelope and VerifyAnswerFile still checks the signature over it; what
// separates two squads is the squad identity of the root (Verify below).
func namespaceOf(f *gatestore.AnswerFile) Namespace {
	n := f.Envelope.Payload.Namespace
	return Namespace{ProjectDir: n.ProjectDir, Profile: n.Profile, Session: n.Session, Generation: n.Generation}
}

// clearGate loads gateID and checks it is a quarantine_clear gate of this
// store, for handle, naming this quarantine (incarnation, config digest).
func clearGate(store gatestore.Store, handle, gateID, incarnation, configDigest string) (gatestore.Record, string, error) {
	rec, err := store.Load(gateID)
	if err != nil {
		return rec, "", reject(RejectClearUnsigned, "gate %s: %v", gateID, err)
	}
	if rec.Class != "quarantine_clear" {
		return rec, "", reject(RejectClearUnsigned, "gate %s is a %s gate, not a quarantine clear", gateID, rec.Class)
	}
	if rec.Squad == "" || rec.Squad != store.Squad() {
		return rec, "", reject(RejectNoSquad, "gate %s does not carry this store's squad identity", gateID)
	}
	h, nonce, inc, cfg, ok := gatetarget.ParseQuarantineClear(rec.Target)
	switch {
	case !ok:
		return rec, "", reject(RejectClearUnsigned, "gate %s: its target is not a clear request", gateID)
	case h != handle:
		return rec, "", reject(RejectClearHandle, "gate %s is the owner's answer for seat %s, not %s", gateID, h, handle)
	case inc != incarnation || cfg != configDigest:
		return rec, "", reject(gatestore.RejectReplayed, "gate %s names another quarantine of seat %s (incarnation %s, config %s): an answer lifts the quarantine it was asked about", gateID, handle, inc, cfg)
	}
	return rec, nonce, nil
}

// AuthorizeClear decides a clear request: gateID is the gate the caller
// presents for handle's quarantine (incarnation and config digest as the
// ledger holds them now). It returns the proof for the signed clear record.
// In order: the gate is the clear of this seat and this quarantine; its
// nonce is the seat's current request's (an old nonce is a replay); the
// owner's answer verifies in full; and no ClearRecord on the seat's ledger
// carries the answer's event id (used says so).
func (v Verifier) AuthorizeClear(store gatestore.Store, handle, gateID, incarnation, configDigest string, used func(eventID string) (bool, error)) (ClearProof, error) {
	rec, nonce, err := clearGate(store, handle, gateID, incarnation, configDigest)
	if err != nil {
		return ClearProof{}, err
	}
	req, err := store.ReadClearRequest(handle)
	if err != nil {
		return ClearProof{}, reject(RejectClearUnsigned, "%v", err)
	}
	if req == nil || req.GateID != gateID || req.Nonce != nonce {
		return ClearProof{}, reject(gatestore.RejectReplayed, "the answer carries a nonce that is not seat %s's current request's: it is refused as replayed", handle)
	}
	p, err := v.clearAnswer(store, rec)
	if err != nil {
		return ClearProof{}, err
	}
	// "used" is a ClearRecord with this event id on the seat's sealed ledger;
	// a ledger that cannot be read refuses
	if used == nil {
		return ClearProof{}, reject(RejectClearUnsigned, "no use check was given for gate %s", gateID)
	}
	if u, err := used(p.EventID); err != nil {
		return ClearProof{}, reject(RejectClearUnsigned, "the seat's ledger cannot say whether the answer was used: %v", err)
	} else if u {
		return ClearProof{}, reject(gatestore.RejectReplayed, "the answer %s already lifted a quarantine", p.EventID)
	}
	return p, nil
}

// clearAnswer is the full consumer check on the clear gate's approval.
func (v Verifier) clearAnswer(store gatestore.Store, rec gatestore.Record) (ClearProof, error) {
	switch d := store.Decision(rec.GateID); d {
	case gatestore.StateAccepted:
	case gatestore.StateDenied:
		return ClearProof{}, reject(RejectClearUnsigned, "the owner denied gate %s", rec.GateID)
	default:
		return ClearProof{}, reject(RejectClearUnsigned, "gate %s has no owner answer yet (%s): the clear waits as an open gate", rec.GateID, store.State(rec, time.Now()))
	}
	f, err := store.ReadAnswer(rec.GateID)
	if err != nil || f == nil {
		return ClearProof{}, reject(RejectClearUnsigned, "gate %s: %v", rec.GateID, errors.Join(err, errors.New("no approval file")))
	}
	vv := v
	vv.Namespace = namespaceOf(f)
	if err := vv.Accepted(store, rec); err != nil {
		return ClearProof{}, reject(RejectClearUnsigned, "gate %s: %v", rec.GateID, err)
	}
	return ClearProof{GateID: rec.GateID, EventID: f.Evidence.EventID}, nil
}

// CheckClear re-verifies a signed clear record's gate proof (the commit
// step, the exit check): the gate is the clear of this seat and this
// quarantine, the owner's answer verifies in full, and it is the event the
// record names. Nonce currency is not checked here: a later request of the
// seat does not unmake an earlier, completed clear.
func (v Verifier) CheckClear(store gatestore.Store, handle, gateID, eventID, incarnation, configDigest string) error {
	rec, _, err := clearGate(store, handle, gateID, incarnation, configDigest)
	if err != nil {
		return err
	}
	p, err := v.clearAnswer(store, rec)
	if err != nil {
		return err
	}
	if p.EventID != eventID {
		return reject(RejectClearUnsigned, "the clear names event %s, the verified answer is %s", eventID, p.EventID)
	}
	return nil
}
