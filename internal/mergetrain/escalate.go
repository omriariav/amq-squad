package mergetrain

import (
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

// StoreEscalator raises a refusal's gate through 3-1's gate store writer: the
// record is written (O_EXCL, fsynced) before any question, and with a Sender
// the question is posted to the owner's thread. Without one the record waits
// for `amq-squad gate post`.
type StoreEscalator struct {
	Store    gatestore.Store
	Identity gatestore.Identity
	Send     gatestore.Sender
	Now      func() time.Time
	Squad    string // set on every gate this escalator raises (Record.Squad)
}

// SquadOf is a squad's identity for its gate records: 4-5's
// gatestore.SquadID of its AMQ session root (the value Store.Raise fills).
func SquadOf(root string) string { return gatestore.SquadID(root) }

// SameSquad reports whether a record was raised by this squad. With 4-5's
// verifier on v3 a record without a squad identity is unverifiable, so it
// matches nothing here either.
func SameSquad(rec gatestore.Record, squad string) bool {
	return squad == "" || rec.Squad != "" && rec.Squad == squad
}

func (e StoreEscalator) Raise(spec gatestore.Spec) (gatestore.Record, error) {
	if spec.Squad == "" {
		spec.Squad = e.Squad
	}
	if spec.Squad == "" {
		spec.Squad = e.Store.Squad() // as 4-5's Store.Raise fills it
	}
	now := time.Now()
	if e.Now != nil {
		now = e.Now()
	}
	if e.Send != nil {
		rec, _, err := e.Store.Raise(spec, e.Identity, now, e.Send)
		return rec, err
	}
	rec, err := gatestore.Render(spec, now, e.Identity)
	if err != nil {
		return gatestore.Record{}, err
	}
	return rec, e.Store.Create(rec)
}

// AcceptedFunc adapts a verifier's accepted-gate lookup
// (gateverify.Verifier.AcceptedFor) to Gates.
type AcceptedFunc func(action, target string) (gatestore.Record, bool)

func (f AcceptedFunc) Accepted(action, target string) (string, bool) {
	if gatetarget.Class(action) == "" {
		return "", false
	}
	rec, ok := f(action, target)
	return rec.GateID, ok
}

// InSquad wraps a lookup so only this squad's records match.
func InSquad(squad string, find func(action, target string) (gatestore.Record, bool)) AcceptedFunc {
	return func(action, target string) (gatestore.Record, bool) {
		rec, ok := find(action, target)
		return rec, ok && SameSquad(rec, squad)
	}
}
