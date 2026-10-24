package mergetrain

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
)

// 4-5's squad identity (gatestore Record.Squad): the train sets it on the
// gates it raises and matches only its own squad's accepted records.
func TestGatesCarryAndMatchTheSquadIdentity(t *testing.T) {
	f := newFixture(t)
	root := filepath.Join(f.dir, "root")
	mine, theirs := SquadOf(root), SquadOf("/other/root")
	f.train.Escalate = StoreEscalator{Store: f.store, Identity: gateIdentity(), Squad: mine}
	f.train.Grants.Merge[0].SizeCapLines = 1
	f.req.PRs = f.req.PRs[:1]
	res, err := f.train.Run(context.Background(), f.req)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := f.store.Load(res.PRs[0].GateID)
	if err != nil || rec.Squad != mine {
		t.Fatalf("raised gate %+v %v", rec, err)
	}
	// another squad's accepted record for the same target lifts nothing
	other := rec
	other.Squad = theirs
	f.train.Gates = InSquad(mine, func(string, string) (gatestore.Record, bool) { return other, true })
	if res, err := f.train.Run(context.Background(), f.req); err != nil || res.PRs[0].Lifted {
		t.Fatalf("another squad's gate lifted the refusal: %v %+v", err, res.PRs[0])
	}
	// a record without a squad identity matches nothing (4-5's verifier on v3)
	bare := rec
	bare.Squad = ""
	f.train.Gates = InSquad(mine, func(string, string) (gatestore.Record, bool) { return bare, true })
	if res, err := f.train.Run(context.Background(), f.req); err != nil || res.PRs[0].Lifted {
		t.Fatalf("a squad-less record lifted the refusal: %v %+v", err, res.PRs[0])
	}
	f.train.Gates = InSquad(mine, func(string, string) (gatestore.Record, bool) { return rec, true })
	if res, err := f.train.Run(context.Background(), f.req); err != nil || !res.PRs[0].Lifted {
		t.Fatalf("this squad's gate did not lift: %v %+v", err, res.PRs[0])
	}
	// drift: another squad's accepted tag gate covers nothing here
	g := newFixture(t)
	groot := filepath.Join(g.dir, "root")
	g.train.ReleaseGates, g.train.Root, g.train.DryRun = &g.store, groot, true
	g.req.PRs = g.req.PRs[:1]
	if _, err := g.train.Run(context.Background(), g.req); err != nil { // baseline
		t.Fatal(err)
	}
	mustGit(t, g.bare, "tag", "v7", "main")
	tagGate, err := gatestore.Render(gatestore.Spec{GateID: "g-v7", Action: "tag", Target: "git tag v7", RaisedBy: "seat", Squad: theirs}, time.Now(), gateIdentity())
	if err != nil || g.store.Create(tagGate) != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(groot, "extensions", "amq-squad", "consumed", "gates", "g-v7"), "approved test\n")
	if res, err := g.train.Run(context.Background(), g.req); err != nil || len(res.Alerts) != 1 {
		t.Fatalf("another squad's tag gate covered v7: %v %+v", err, res.Alerts)
	}
}
