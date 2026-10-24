package gateverify

import (
	"errors"
	"fmt"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Verifier is the core's trusted state for deciding from the gate store: the
// owner's trust store, the binding's pin, and the caller's namespace.
type Verifier struct {
	Trust     operatorauth.AuthorizationTrustStore
	Pin       Pin
	Namespace Namespace
}

// Accepted is the full consumer check on one gate: the store's recovered
// decision is accepted, and its approval file passes VerifyAnswerFile (the
// owner's BIP-340 signature re-verified, the record's issuer, the caller's
// namespace). It never rests on the issuer envelope alone.
func (v Verifier) Accepted(store gatestore.Store, rec gatestore.Record) error {
	if d := store.Decision(rec.GateID); d != gatestore.StateAccepted {
		return fmt.Errorf("gate %s has no accepted decision (%q)", rec.GateID, d)
	}
	f, err := store.ReadAnswer(rec.GateID)
	if err != nil || f == nil {
		return errors.Join(err, errors.New("no approval file"))
	}
	q, err := store.Question(rec.GateID)
	if err != nil {
		return err
	}
	re, err := store.ReadReask(rec.GateID)
	if err != nil {
		return err
	}
	return VerifyAnswerFile(*f, v.Trust, v.Pin, rec, q, re, v.Namespace)
}

// AcceptedFor returns the gate accepted for exactly this action and target.
func (v Verifier) AcceptedFor(store gatestore.Store, action, target string) (gatestore.Record, bool) {
	digest := gatetarget.Digest(action, target)
	ids, err := store.IDs()
	if err != nil {
		return gatestore.Record{}, false
	}
	for _, id := range ids {
		rec, err := store.Load(id)
		if err == nil && rec.Class == action && rec.Digest == digest && v.Accepted(store, rec) == nil {
			return rec, true
		}
	}
	return gatestore.Record{}, false
}

// GrowthDecision is the growth gate's answer to a request past a grant limit.
type GrowthDecision struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason"` // within_grant | gate_accepted | gate_grow | owner_grants_insecure
	Action  string `json:"action,omitempty"`
	Target  string `json:"target,omitempty"`
	Digest  string `json:"digest,omitempty"`
	GateID  string `json:"gate_id,omitempty"`
}

// SeatRequest is the growth gate for the scheduler (R30, plan 7.2): seat
// number `seats` of the session at root starts inside the grant; past
// MaxSeats it needs a gate accepted, by the full owner-signature check, for
// gatetarget.Grow(root, seats). While the owner grants file is insecure every
// request is the growth gate and nothing opens it (3-1 ruling 1).
func (v Verifier) SeatRequest(store gatestore.Store, r grants.Resolved, root string, seats int) GrowthDecision {
	if r.SeatRequest(seats) {
		return GrowthDecision{Allowed: true, Reason: "within_grant"}
	}
	return v.growth(store, r, gatetarget.Grow(root, seats))
}

// LimitRequest is the growth gate for a widening past another grant limit
// (2b-2): within is the caller's own grant check; past it the request needs
// an accepted gate for gatetarget.GrowLimit(root, limit, value).
func (v Verifier) LimitRequest(store gatestore.Store, r grants.Resolved, root, limit string, value int, within bool) GrowthDecision {
	if within && !r.Insecure {
		return GrowthDecision{Allowed: true, Reason: "within_grant"}
	}
	return v.growth(store, r, gatetarget.GrowLimit(root, limit, value))
}

func (v Verifier) growth(store gatestore.Store, r grants.Resolved, target string) GrowthDecision {
	d := GrowthDecision{Reason: "gate_grow", Action: "spawn", Target: target, Digest: gatetarget.Digest("spawn", target)}
	if r.Insecure {
		d.Reason = grants.ReasonInsecure
		return d
	}
	if rec, ok := v.AcceptedFor(store, "spawn", target); ok {
		d.Allowed, d.Reason, d.GateID = true, "gate_accepted", rec.GateID
	}
	return d
}
