package gatestore

import (
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// Accepted reports whether a gate's approval file verifies against the trust
// store and binds the record's gate id, action and digest: the guard rule
// (2a-3), for callers in the core. The full bip340 re-verification for
// consumers that act is gateverify.VerifyAnswerFile.
func (s Store) Accepted(rec Record, trust operatorauth.AuthorizationTrustStore) bool {
	f, err := s.ReadAnswer(rec.GateID)
	if err != nil || f == nil {
		return false
	}
	p := f.Envelope.Payload
	return p.Gate == "gate/"+rec.GateID && p.Action == rec.Class && p.Target == rec.Digest &&
		operatorauth.ValidateAuthorizationPayload(p) == nil && operatorauth.VerifyAuthorizationEnvelope(f.Envelope, trust) == nil
}

// AcceptedFor returns the accepted gate for an action and target string, if
// one exists. Acceptance is final: not_after does not apply to it.
func (s Store) AcceptedFor(action, target string, trust operatorauth.AuthorizationTrustStore) (Record, bool) {
	digest := gatetarget.Digest(action, target)
	ids, err := s.IDs()
	if err != nil {
		return Record{}, false
	}
	for _, id := range ids {
		rec, err := s.Load(id)
		if err == nil && rec.Class == action && rec.Digest == digest && s.Accepted(rec, trust) {
			return rec, true
		}
	}
	return Record{}, false
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
// number `seats` of the session at root is allowed inside the grant; past
// MaxSeats it needs an accepted spawn gate for gatetarget.Grow(root, seats).
// While the owner grants file is insecure every request is the growth gate
// and no gate opens it (3-1 ruling 1).
func (s Store) SeatRequest(r grants.Resolved, root string, seats int, trust operatorauth.AuthorizationTrustStore) GrowthDecision {
	if r.SeatRequest(seats) {
		return GrowthDecision{Allowed: true, Reason: "within_grant"}
	}
	return s.growth(r, gatetarget.Grow(root, seats), trust)
}

// LimitRequest is the growth gate for a widening past any other grant limit
// (2b-2's widening of a task beyond the grant): within is the caller's own
// grant check; past it the request needs an accepted spawn gate for
// gatetarget.GrowLimit(root, limit, value).
func (s Store) LimitRequest(r grants.Resolved, root, limit string, value int, within bool, trust operatorauth.AuthorizationTrustStore) GrowthDecision {
	if within && !r.Insecure {
		return GrowthDecision{Allowed: true, Reason: "within_grant"}
	}
	return s.growth(r, gatetarget.GrowLimit(root, limit, value), trust)
}

func (s Store) growth(r grants.Resolved, target string, trust operatorauth.AuthorizationTrustStore) GrowthDecision {
	d := GrowthDecision{Reason: "gate_grow", Action: "spawn", Target: target, Digest: gatetarget.Digest("spawn", target)}
	if r.Insecure {
		d.Reason = grants.ReasonInsecure
		return d
	}
	if rec, ok := s.AcceptedFor("spawn", target, trust); ok {
		d.Allowed, d.Reason, d.GateID = true, "gate_accepted", rec.GateID
	}
	return d
}
