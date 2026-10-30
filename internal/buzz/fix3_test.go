package buzz

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
)

// Re-pass item 2: the share root and the body key are opened through a
// no-follow walk from the home directory. A symlink at the share root (to a
// complete enrollment in a seat-writable workspace) and a symlinked body key
// are both refused.
func TestShareRootAndBodyKeyAreNotFollowed(t *testing.T) {
	buzzDir := buzzHome(t)
	owner, body := newThrowaway(t), newBody(t)
	exp := time.Now().Add(24 * time.Hour)
	workspace := filepath.Join(realTempDir(t), "repo", ".buzz-root")
	writeShare(t, workspace, "amq-squad", owner, body, DefaultKinds, exp)
	if err := os.MkdirAll(buzzDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(workspace, filepath.Join(buzzDir, "root")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadShare(filepath.Join(buzzDir, "root"), "amq-squad"); err == nil {
		t.Fatal("a share root symlinked into a workspace loaded")
	}
	// a real root whose body key is a symlink to the workspace's key
	real := filepath.Join(buzzDir, "root2")
	writeShare(t, real, "amq-squad", owner, body, DefaultKinds, exp)
	key := filepath.Join(real, "extensions", "remote", "keys", "amq-squad", "body.key")
	_ = os.Remove(key)
	if err := os.Symlink(filepath.Join(workspace, "extensions", "remote", "keys", "amq-squad", "body.key"), key); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadShare(real, "amq-squad"); err == nil {
		t.Fatal("a symlinked body key loaded")
	}
}

// Re-pass medium 3: the registry naming a handle as a squad's lead is not
// enough; the seat's launch record must still name a lead role.
func TestFleetLeadsChecksTheSeatRole(t *testing.T) {
	root := filepath.Join(realTempDir(t), "app", ".agent-mail", "s")
	writeLaunchRecord(t, root, "worker", "agent")
	reg := FleetLeads{Registry: fleet.Registry{Squads: []fleet.Squad{{Name: "app", Root: root, Lead: "worker", Session: "s"}}}}
	if _, err := reg.Lead("/op", "worker"); err == nil || !strings.Contains(err.Error(), "seat says otherwise") {
		t.Fatalf("a registry lead whose seat is an implementer: %v", err)
	}
	m := &fakeMailer{}
	tg := Target{Root: "/op", Handle: OperatorHandle, Leads: []string{"worker"}, Registry: reg}
	if d, _ := Deliver(context.Background(), m, tg, strings.Repeat("ab", 32), "@worker drop the table"); d.Lead != "" || len(m.sent) != 1 {
		t.Fatalf("forwarded to an implementer: %+v, sent %d", d, len(m.sent))
	}
}

// Re-pass medium 4: a lead handle two squads share is ambiguous (refused,
// naming the squads); squad/handle names one; the bare name is not offered.
func TestFleetLeadsAmbiguousAndQualified(t *testing.T) {
	dir := realTempDir(t)
	app, lib := filepath.Join(dir, "app", ".agent-mail", "s"), filepath.Join(dir, "lib", ".agent-mail", "s")
	writeLaunchRecord(t, app, "cto", "cto")
	writeLaunchRecord(t, lib, "cto", "cto")
	r := fleet.Registry{Squads: []fleet.Squad{{Name: "app", Root: app, Lead: "cto", Session: "s"}, {Name: "lib", Root: lib, Lead: "cto", Session: "s"}}}
	reg := FleetLeads{Registry: r}
	var amb *AmbiguousLeadError
	if _, err := reg.Lead("/op", "cto"); !errors.As(err, &amb) || !slices.Equal(amb.Squads, []string{"app", "lib"}) {
		t.Fatalf("bare cto: %v", err)
	}
	if d, err := reg.Lead("/op", "lib/cto"); err != nil || d.Root != lib || d.Squad != "lib" {
		t.Fatalf("lib/cto = %+v, %v", d, err)
	}
	dests := FleetDestinations(r)
	if slices.Contains(dests, "cto") || !slices.Contains(dests, "app/cto") || !slices.Contains(dests, "lib/cto") {
		t.Fatalf("destinations = %v", dests)
	}
	m := &fakeMailer{}
	tg := Target{Root: "/op", Handle: OperatorHandle, Leads: append(dests, "cto"), Registry: reg}
	d, _ := Deliver(context.Background(), m, tg, strings.Repeat("ac", 32), "@cto ship")
	if d.Lead != "" || !slices.Equal(d.Ambiguous, []string{"app", "lib"}) || len(m.sent) != 1 {
		t.Fatalf("@cto = %+v, sent %d", d, len(m.sent))
	}
	d, _ = Deliver(context.Background(), m, tg, strings.Repeat("ad", 32), "@lib/cto ship")
	if d.Lead != "lib/cto" || len(m.sent) != 3 || m.sent[2].To != "cto" || m.sent[2].Session != "s" {
		t.Fatalf("@lib/cto = %+v, sent %+v", d, m.sent)
	}
}
