package compoundrelease

import (
	"errors"
	"fmt"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// gateAdapter is the 3-2 seam's fake: the child's gate is a record keyed by
// a deterministic id; a second ask for the same child finds it.
type gateAdapter struct {
	gates map[string]string // id -> target
	calls map[string]int    // role -> ChildGate calls
	fail  error
}

func newGateAdapter() *gateAdapter {
	return &gateAdapter{gates: map[string]string{}, calls: map[string]int{}}
}

func (a *gateAdapter) ChildGate(child operatorauth.ReleaseChildPlan) (string, error) {
	a.calls[child.Role]++
	if a.fail != nil {
		return "", a.fail
	}
	id := "rel-" + child.Role
	if target, ok := a.gates[id]; ok && target != child.Target {
		return id, fmt.Errorf("%w: %s", ErrChildConflict, target)
	}
	a.gates[id] = child.Target
	return id, nil
}

func plannedFixture(t *testing.T) (*Store, Snapshot) {
	t.Helper()
	scope := testScope(t)
	store := openTestStore(t, scope)
	planned, err := store.Create(specForScope(scope))
	if err != nil {
		t.Fatal(err)
	}
	return store, planned
}

// A planned release reconciles to active: both children claimed, their gates
// raised in order (tag first) and adopted, the active manifest naming them.
func TestReconcileRaisesBothChildGatesAndActivates(t *testing.T) {
	store, planned := plannedFixture(t)
	a := newGateAdapter()
	res, err := store.Reconcile(planned.Pointer.GenerationID, a)
	if err != nil || res.Disposition != ReconcileActivated || res.Snapshot.Active == nil {
		t.Fatalf("%v %+v", err, res)
	}
	got := res.Snapshot.Active.Children
	if got[0].QuestionMessageID != "rel-tag" || got[1].QuestionMessageID != "rel-github_release" {
		t.Fatalf("active children %+v", got)
	}
	again, err := store.Reconcile(planned.Pointer.GenerationID, a)
	if err != nil || again.Disposition != ReconcileActivated || a.calls["tag"] != 1 || a.calls["github_release"] != 1 {
		t.Fatalf("second reconcile %v %+v calls %v", err, again, a.calls)
	}
}

// A crash after the claim, after the gate was raised, or before adoption
// converges on the next reconcile with one gate per child.
func TestReconcileFaultSeamsConvergeOnOneGatePerChild(t *testing.T) {
	for _, stage := range []string{"after_claim:0", "after_gate:0", "after_claim:1", "after_gate:1"} {
		t.Run(stage, func(t *testing.T) {
			store, planned := plannedFixture(t)
			a := newGateAdapter()
			old := reconcileFault
			fired := false
			reconcileFault = func(s string) error {
				if s == stage && !fired {
					fired = true
					return errors.New("fault " + s)
				}
				return nil
			}
			t.Cleanup(func() { reconcileFault = old })
			if _, err := store.Reconcile(planned.Pointer.GenerationID, a); err == nil || !fired {
				t.Fatalf("fault did not fire: %v", err)
			}
			reconcileFault = old
			res, err := store.Reconcile(planned.Pointer.GenerationID, a)
			if err != nil || res.Disposition != ReconcileActivated || len(a.gates) != 2 {
				t.Fatalf("recovery %v %+v gates %v", err, res, a.gates)
			}
		})
	}
}

// A transport failure leaves the child sending (ambiguous), and the retry
// asks for the same gate; a gate id that binds another target terminalizes
// the release as a conflict.
func TestReconcileTransportFailureAndForeignGate(t *testing.T) {
	store, planned := plannedFixture(t)
	a := newGateAdapter()
	a.fail = errors.New("relay down")
	res, err := store.Reconcile(planned.Pointer.GenerationID, a)
	if err == nil || res.Disposition != ReconcileAmbiguous {
		t.Fatalf("%v %+v", err, res)
	}
	a.fail = nil
	if res, err = store.Reconcile(planned.Pointer.GenerationID, a); err != nil || res.Disposition != ReconcileActivated {
		t.Fatalf("retry %v %+v", err, res)
	}

	store2, planned2 := plannedFixture(t)
	b := newGateAdapter()
	b.gates["rel-tag"] = "git tag v9 elsewhere"
	res, err = store2.Reconcile(planned2.Pointer.GenerationID, b)
	if err != nil || res.Disposition != ReconcileConflict || res.Snapshot.Pointer.State != operatorauth.ReleaseStateConflict {
		t.Fatalf("foreign gate %v %+v", err, res)
	}
}
