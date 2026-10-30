package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/status"
)

// Phase 5 re-pass of 4-3, P2-1: a session root is one squad's: two rows on
// one root, or on a root and its symlink alias, fail validation, and the
// locked admission refuses a start on a registered root (squad_registered).
func TestRootIdentityIsUniqueAcrossTheRegistry(t *testing.T) {
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "app", ".agent-mail", "app")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	row := func(name, r string) Squad {
		return Squad{Name: name, Root: r, ProjectPath: filepath.Join(base, name), Repository: "owner/" + name, Lead: "lead"}
	}
	for _, second := range []string{root, alias} {
		reg := Registry{Schema: Schema, Squads: []Squad{row("app", root), row("web", second)}}
		if err := reg.Validate(); err == nil || !strings.Contains(err.Error(), "share the session root") {
			t.Errorf("roots %s and %s validated: %v", root, second, err)
		}
	}
	if err := (Registry{Schema: Schema, Squads: []Squad{row("app", root), row("web", filepath.Join(base, "web", ".agent-mail", "web"))}}).Validate(); err != nil {
		t.Fatalf("distinct roots: %v", err)
	}

	f := newMayor(t, fleetGrant(4, "owner/app", "owner/web"))
	app := f.request(t, "app", false)
	if r := mustStart(t, f.m, app); r.Outcome != OutcomeStarted {
		t.Fatalf("app: %+v", r)
	}
	os.MkdirAll(app.Squad.Root, 0o700)
	webAlias := filepath.Join(f.dir, "web-root-alias")
	if err := os.Symlink(app.Squad.Root, webAlias); err != nil {
		t.Fatal(err)
	}
	web := f.request(t, "web", false)
	web.Squad.Root = webAlias
	if r := mustStart(t, f.m, web); r.Outcome != OutcomeRefused || r.Reason != ReasonRegistered {
		t.Fatalf("a start on the alias of a registered root: %+v", r)
	}
	if names := f.registered(t); len(names) != 1 {
		t.Fatalf("registered %v", names)
	}
}

// Phase 5 re-pass of 4-3, ruling: a cost_per_day_usd cap with no meter that
// can read the outcome log holds every start with wait_reason
// cost_unmetered, which the fleet status shows; removing the cap proceeds.
func TestCostCapWithoutAMeterHoldsEveryStart(t *testing.T) {
	f := newMayor(t, fleetGrant(4, "owner/app"))
	f.m.Cost, f.grant.Grants.Fleet.CostPerDayUSD = nil, 50
	r := mustStart(t, f.m, f.request(t, "app", false))
	if r.Outcome != OutcomeWaiting || r.Reason != ReasonCostUnmetered || len(f.launcher.launched) != 0 {
		t.Fatalf("an unmetered cap: %+v (launched %d)", r, len(f.launcher.launched))
	}
	reg, err := Read(f.m.RegistryPath)
	if err != nil {
		t.Fatal(err)
	}
	st := Status(reg, func(Squad) status.Status { return status.Status{} }, "test", time.Now())
	if len(st.Waiting) != 1 || st.Waiting[0].WaitReason != status.WaitReasonCostUnmetered || st.Waiting[0].Cap != ReasonCostUnmetered {
		t.Fatalf("fleet status waiting: %+v", st.Waiting)
	}
	f.grant.Grants.Fleet.CostPerDayUSD = 0 // the owner removes the cap
	if r := mustStart(t, f.m, f.request(t, "app", false)); r.Outcome != OutcomeStarted {
		t.Fatalf("with the cap removed: %+v", r)
	}
}
