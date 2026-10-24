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
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
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

// twoSquads is one owner, relay, body and channel over two squad stores.
func twoSquads(t *testing.T) (coreFixture, *gateverify.Core, *gateverify.Core, *Pump) {
	t.Helper()
	f, p, _, _ := pumpFixture(t)
	fixed := time.Now().Truncate(time.Second)
	a := f.core
	a.Now = func() time.Time { return fixed }
	b := *a
	store, err := gatestore.Open(filepath.Join(realTempDir(t), "squad-b"))
	if err != nil {
		t.Fatal(err)
	}
	b.Store = store
	p.Cores = []*gateverify.Core{&b}
	return f, a, &b, p
}

// The amendment 28 ruling: two squads raising the same gate id, action and
// target in the same second post different questions (the squad identity is
// in the text and the tags), so the owner's reply to squad A's question can
// never verify against squad B's record.
func TestSquadIdentitySeparatesTheQuestions(t *testing.T) {
	f, a, b, p := twoSquads(t)
	ctx := context.Background()
	spec := gatestore.Spec{GateID: "rel-1", Action: "tag", Target: "git tag v3.0.0", RaisedBy: "cto", Window: 30 * time.Minute}
	recA, qA, err := a.Raise(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	recB, qB, err := b.Raise(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if qA.EventID == qB.EventID || recA.Squad == "" || recA.Squad == recB.Squad || !strings.Contains(recA.Text, "squad: "+recA.Squad) {
		t.Fatalf("the two squads' questions are not distinct: %s %s, squads %q %q", qA.EventID, qB.EventID, recA.Squad, recB.Squad)
	}
	prefix := strings.TrimPrefix(recA.Digest, "sha256:")[:16]
	replyA := f.ownerDM(t, "approve rel-1 release "+prefix, qA.EventID)
	// squad B's core refuses the reply to A's question
	raw, _ := a.Relay.FetchByID(ctx, replyA)
	if r := b.HandleEvent(ctx, raw); r.Outcome == "approved" {
		t.Fatalf("squad B committed an answer to squad A's question: %+v", r)
	}
	got, err := p.Step(ctx)
	if err != nil || len(got) != 1 || got[0].Gate == nil || got[0].Gate.Outcome != "approved" {
		t.Fatalf("the reply to A's question: %+v, %v", got, err)
	}
	if ans, _ := b.Store.ReadAnswer("rel-1"); ans != nil {
		t.Fatal("squad B holds an answer for squad A's question")
	}
	// a record without a squad identity is unverifiable
	old, err := gatestore.Render(gatestore.Spec{GateID: "rel-old", Action: "tag", Target: "git tag v2", RaisedBy: "cto"}, time.Now(), a.Identity())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gateverify.Verify([]byte(`{}`), old, nil, nil, a.Pin, time.Now()); gateverify.Code(err) != gateverify.RejectNoSquad {
		t.Fatalf("a record without squad identity: %v", err)
	}
}

// Re-pass medium 1: an answer whose gate id is open in two squads and whose
// thread names neither question is ambiguous: refused once, not consumed,
// and the owner's later thread reply is the answer.
func TestPumpAmbiguousAnswerIsNotConsumed(t *testing.T) {
	f, a, b, p := twoSquads(t)
	ctx := context.Background()
	spec := gatestore.Spec{GateID: "rel-2", Action: "tag", Target: "git tag v3.1.0", RaisedBy: "cto", Window: 30 * time.Minute}
	recA, _, _ := a.Raise(ctx, spec)
	_, qB, _ := b.Raise(ctx, spec)
	prefix := strings.TrimPrefix(recA.Digest, "sha256:")[:16]
	plain := f.ownerDM(t, "approve rel-2 release "+prefix, "")
	got, err := p.Step(ctx)
	if err != nil || len(got) != 1 || got[0].EventID != plain || got[0].Kind != "ambiguous" || !strings.Contains(got[0].Reply, "more than one squad") {
		t.Fatalf("ambiguous answer: %+v, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(p.Marks, plain)); err == nil {
		t.Fatal("the ambiguous answer was consumed")
	}
	if ra, _ := a.Store.ReadReask("rel-2"); ra != nil {
		t.Fatal("the ambiguous answer was re-asked in squad A")
	}
	if got, _ := p.Step(ctx); len(got) != 0 {
		t.Fatalf("the refusal was repeated: %+v", got)
	}
	f.ownerDM(t, "approve rel-2 release "+prefix, qB.EventID)
	got, err = p.Step(ctx)
	if err != nil || len(got) != 1 || got[0].Gate == nil || got[0].Gate.Outcome != "approved" {
		t.Fatalf("the thread reply to B: %+v, %v", got, err)
	}
	if ans, _ := b.Store.ReadAnswer("rel-2"); ans == nil {
		t.Fatal("squad B has no answer")
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

// Re-pass medium 5: channels that differ only where the old name mapping
// collided (a:b and a_b) get their own marks directory.
func TestMarksDirIsPerFullChannelID(t *testing.T) {
	if MarksDir("/s", "a:b") == MarksDir("/s", "a_b") || MarksDir("/s", "a:b") != MarksDir("/s", "a:b") {
		t.Fatalf("marks dirs: %s %s", MarksDir("/s", "a:b"), MarksDir("/s", "a_b"))
	}
}
