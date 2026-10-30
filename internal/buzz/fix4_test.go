package buzz

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
)

// Amendment 28 b: the verifier compares the answer's squad with the record
// (and the record with its question and its store). A thread reply that
// carries another squad's tag is refused; the re-ask carries the squad tag.
func TestAnswerSquadTagMustMatch(t *testing.T) {
	f, a, b, _ := twoSquads(t)
	ctx := context.Background()
	rec, q, err := a.Raise(ctx, gatestore.Spec{GateID: "rel-t", Action: "tag", Target: "git tag v3.2.0", RaisedBy: "cto", Window: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	prefix := strings.TrimPrefix(rec.Digest, "sha256:")[:16]
	signed := func(tags [][]string) []byte {
		e := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: append([][]string{{"h", a.Pin.Channel}, {"e", q.EventID, "", "reply"}}, tags...), Content: "approve rel-t release " + prefix}
		_ = f.owner.sk.Sign(&e)
		_ = f.relay.Publish(ctx, e.Marshal())
		return e.Marshal()
	}
	if r := a.HandleEvent(ctx, signed([][]string{{"squad", b.Store.Squad()}})); r.Outcome == "approved" || r.Code != gateverify.RejectNoSquad {
		t.Fatalf("an answer tagged with another squad: %+v", r)
	}
	if r := a.HandleEvent(ctx, signed([][]string{{"squad", rec.Squad}})); r.Outcome != "approved" {
		t.Fatalf("an answer tagged with its own squad: %+v", r)
	}
	// the re-ask of a plain answer is threaded to the question and tagged
	rec2, _, _ := a.Raise(ctx, gatestore.Spec{GateID: "rel-u", Action: "tag", Target: "git tag v3.3.0", RaisedBy: "cto", Window: 30 * time.Minute})
	plain := nostr.Event{CreatedAt: time.Now().Unix(), Kind: 9, Tags: [][]string{{"h", a.Pin.Channel}}, Content: "approve rel-u release " + strings.TrimPrefix(rec2.Digest, "sha256:")[:16]}
	_ = f.owner.sk.Sign(&plain)
	if r := a.HandleEvent(ctx, plain.Marshal()); r.Outcome != "reasked" {
		t.Fatalf("plain answer: %+v", r)
	}
	ra, _ := a.Store.ReadReask("rel-u")
	re, _ := nostr.ParseEvent([]byte(ra.RawEvent))
	if !slices.Equal(nostr.TagValues(re, "squad"), []string{rec2.Squad}) {
		t.Fatalf("the re-ask carries squad tags %v", nostr.TagValues(re, "squad"))
	}
}

// Item 2: share.json is held to the body key's rule (0600 or tighter).
func TestShareJSONMustBePrivate(t *testing.T) {
	root := filepath.Join(buzzHome(t), "root")
	writeShare(t, root, "amq-squad", newThrowaway(t), newBody(t), DefaultKinds, time.Now().Add(24*time.Hour))
	p := filepath.Join(root, "extensions", "remote", "keys", "amq-squad", "share.json")
	if err := os.Chmod(p, 0o666); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadShare(root, "amq-squad"); err == nil || !strings.Contains(err.Error(), "0600") {
		t.Fatalf("a world-writable share.json: %v", err)
	}
}

// Medium 3: the lead roles are per squad; squad lib's seat cannot borrow
// squad app's configured lead role.
func TestFleetLeadRolesArePerSquad(t *testing.T) {
	dir := realTempDir(t)
	app, lib := filepath.Join(dir, "app", ".agent-mail", "s"), filepath.Join(dir, "lib", ".agent-mail", "s")
	writeLaunchRecord(t, app, "arch", "architect")
	writeLaunchRecord(t, lib, "dev", "architect")
	reg := FleetLeads{Registry: fleet.Registry{Squads: []fleet.Squad{{Name: "app", Root: app, Lead: "arch", Session: "s"}, {Name: "lib", Root: lib, Lead: "dev", Session: "s"}}},
		Roles: map[string][]string{"app": {"architect"}}}
	if _, err := reg.Lead("/op", "app/arch"); err != nil {
		t.Fatalf("app's configured lead role: %v", err)
	}
	if _, err := reg.Lead("/op", "lib/dev"); err == nil {
		t.Fatal("lib's seat passed with app's configured lead role")
	}
}
