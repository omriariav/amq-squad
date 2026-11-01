package gateverify

import (
	"context"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
)

func TestGrowthApprovalBindsTheResolvedFileAcrossAliases(t *testing.T) {
	f := newFixture(t)
	target := gatetarget.GrowTask("/r/s1", "t1", "protected_paths", `["real/file.go"]`)
	rec, q, err := f.core.Raise(context.Background(), gatestore.Spec{GateID: "widen-t1", Action: "spawn", Target: target, RaisedBy: "lead", Facts: gatestore.SafeFactsWithLinks("task", "", []gatestore.PathLink{{Path: "real/file.go", Alias: "first-link"}})})
	if err != nil {
		t.Fatal(err)
	}
	if r := f.core.HandleEvent(context.Background(), f.answer(f.owner, tuple("approve", rec), q.EventID, testNow.Add(time.Second))); r.Outcome != "approved" {
		t.Fatalf("approve first alias: %+v", r)
	}
	v := Verifier{Trust: f.trust, Pin: f.core.Pin, Namespace: f.core.Namespace}
	if _, ok := v.AcceptedFor(f.store, "spawn", target); !ok {
		t.Fatal("the resolved-file approval did not cover a second alias")
	}
}
