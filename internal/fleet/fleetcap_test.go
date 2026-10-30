package fleet

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/status"
)

// Reviewer ruling (1): without a running operator session (the one-shot CLI
// path) a start held by max_squads or cost per day is refused with the named
// reason and never queued; only a Session mayor queues and retries.
func TestMayorWithoutSessionRefusesTheFleetCap(t *testing.T) {
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	f.m.Session = false
	if r := mustStart(t, f.m, f.request(t, "app", false)); r.Outcome != OutcomeStarted {
		t.Fatalf("first start: %+v", r)
	}
	r := mustStart(t, f.m, f.request(t, "web", false))
	if r.Outcome != OutcomeRefused || r.Reason != ReasonMaxSquads || len(f.m.Waiting) != 0 {
		t.Fatalf("CLI path past max_squads: %+v, waiting %d; want refused max_squads, not queued", r, len(f.m.Waiting))
	}
	g := newMayor(t, fleetGrant(4, "owner/app"))
	g.m.Session, g.m.Cost, g.grant.Grants.Fleet.CostPerDayUSD = false, spend(99), 50
	if r := mustStart(t, g.m, g.request(t, "app", false)); r.Outcome != OutcomeRefused || r.Reason != ReasonCostPerDay || len(g.m.Waiting) != 0 {
		t.Fatalf("CLI path over cost per day: %+v", r)
	}
	if len(f.launcher.launched) != 1 || len(g.launcher.launched) != 0 {
		t.Fatal("a capped start launched")
	}
}

// Reviewer ruling (2): status --fleet --json shows wait_reason fleet_cap for
// a squad the mayor declined or deferred for the cap, and drops it once the
// squad starts.
func TestFleetStatusShowsFleetCap(t *testing.T) {
	at := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	f := newMayor(t, fleetGrant(1, "owner/app", "owner/web"))
	f.m.Now = func() time.Time { return at }
	mustStart(t, f.m, f.request(t, "app", false))
	web := f.request(t, "web", false)
	if r := mustStart(t, f.m, web); r.Outcome != OutcomeWaiting {
		t.Fatalf("deferred: %+v", r)
	}
	f.m.Now = func() time.Time { return at.Add(time.Hour) }
	mustStart(t, f.m, web) // a second hold keeps the first since
	collect := func(s Squad) status.Status {
		return status.Build(status.Input{Root: s.Root, Session: s.SessionName(), ProjectPath: s.ProjectPath, Now: at})
	}
	reg, err := Read(f.m.RegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	doc := Status(reg, collect, "x", at)
	raw, _ := json.Marshal(doc)
	if err := status.CheckAdditive(golden(t, "status-fleet-v1.golden.json"), raw); err != nil {
		t.Fatalf("fleet document with a waiting squad: %v", err)
	}
	if len(doc.Squads) != 1 || len(doc.Waiting) != 1 {
		t.Fatalf("squads %d waiting %+v", len(doc.Squads), doc.Waiting)
	}
	w := doc.Waiting[0]
	if w.Squad != "web" || w.WaitReason != status.WaitReasonFleetCap || w.Cap != ReasonMaxSquads || !w.Since.Equal(at) || w.Repository != "owner/web" {
		t.Fatalf("waiting row = %+v", w)
	}
	// declined on the CLI path is shown the same way
	f.m.Session = false
	mustStart(t, f.m, web)
	if reg, _ = Read(f.m.RegistryPath); len(reg.Waiting) != 1 || reg.Waiting[0].Cap != ReasonMaxSquads {
		t.Fatalf("declined start not recorded: %+v", reg.Waiting)
	}
	// once a slot frees and it starts, it leaves waiting
	f.m.Session = true
	f.stopped["app"] = true
	if res, err := f.m.Retry(context.Background()); err != nil || res["web"].Outcome != OutcomeStarted {
		t.Fatalf("retry: %+v %v", res, err)
	}
	reg, _ = Read(f.m.RegistryPath)
	if doc := Status(reg, collect, "x", at); len(doc.Waiting) != 0 || len(doc.Squads) != 2 {
		t.Fatalf("after start: squads %d waiting %+v", len(doc.Squads), doc.Waiting)
	}
	if _, err := Parse(Render(reg), "f"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitingRoundTripAndValidation(t *testing.T) {
	r := sampleRegistry()
	r.Waiting = []Waiting{{Name: "web", Repository: "owner/web", ProjectPath: "/Users/owner/code/web", Cap: ReasonCostPerDay,
		Since: "2026-10-24T12:00:00Z", Detail: "spent 60.00 USD today, cost_per_day_usd 50"}}
	got, err := Parse(Render(r), "f")
	if err != nil || got.Validate() != nil || len(got.Waiting) != 1 || got.Waiting[0] != r.Waiting[0] {
		t.Fatalf("round trip: %+v %v\n%s", got.Waiting, err, Render(r))
	}
	bad := r
	bad.Waiting = []Waiting{{Name: "app", Repository: "owner/app", ProjectPath: "/p", Cap: ReasonMaxSquads, Since: "2026-10-24T12:00:00Z"}}
	if bad.Validate() == nil {
		t.Error("a registered squad also listed as waiting validated")
	}
	bad.Waiting = []Waiting{{Name: "web", Repository: "owner/web", ProjectPath: "/p", Cap: "repository_not_permitted", Since: "2026-10-24T12:00:00Z"}}
	if bad.Validate() == nil {
		t.Error("a non-cap reason validated as waiting")
	}
}
