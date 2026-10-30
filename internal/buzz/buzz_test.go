package buzz

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/status"
)

func TestDefaultEnrollmentIsEightGrants(t *testing.T) {
	// amendment 19: profile and presence by default; S1 measured 8 grants
	// with the profile (5 base kinds, buzz-dm 9 and 40003, buzz-profile 0)
	if len(DefaultKinds) != 8 || !slices.Contains(DefaultKinds, KindProfile) || !slices.Contains(DefaultKinds, KindDM) || !slices.Contains(DefaultKinds, KindAuthGrant) {
		t.Fatalf("DefaultKinds = %v", DefaultKinds)
	}
}

func TestLoadShare(t *testing.T) {
	buzzDir := buzzHome(t)
	root := filepath.Join(buzzDir, "root")
	owner, body := newThrowaway(t), newBody(t)
	exp := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	writeShare(t, root, "amq-squad", owner, body, DefaultKinds, exp)
	sh, err := LoadShare(root, "amq-squad")
	if err != nil {
		t.Fatal(err)
	}
	if sh.BodyHex() != body.PublicHex() || sh.Owner != owner.pubHex() || sh.GrantCount() != 8 || !sh.Expires().Equal(exp.UTC()) {
		t.Fatalf("share = body %s owner %s grants %d expires %s", sh.BodyHex(), sh.Owner, sh.GrantCount(), sh.Expires())
	}
	if g, err := sh.Grant(KindAuthGrant, time.Now()); err != nil || g[0] != "auth" || g[1] != owner.pubHex() {
		t.Fatalf("auth grant = %v, %v", g, err)
	}
	if _, err := sh.Grant(KindAuthGrant, exp); err == nil {
		t.Fatal("an expired grant was returned")
	}
	if _, err := sh.Grant(31337, time.Now()); err == nil {
		t.Fatal("a kind not enrolled was returned")
	}

	keyPath := filepath.Join(root, "extensions", "remote", "keys", "amq-squad", "body.key")
	t.Run("mode", func(t *testing.T) {
		_ = os.Chmod(keyPath, 0o640)
		defer os.Chmod(keyPath, 0o600)
		if _, err := LoadShare(root, "amq-squad"); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Fatalf("group-readable body key: %v", err)
		}
	})
	t.Run("symlink", func(t *testing.T) {
		other := filepath.Join(buzzDir, t.Name()[len("TestLoadShare/"):])
		writeShare(t, other, "s2", owner, body, DefaultKinds, exp)
		link := filepath.Join(other, "extensions", "remote", "keys", "s3")
		if err := os.Symlink(filepath.Join(other, "extensions", "remote", "keys", "s2"), link); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadShare(other, "s3"); err == nil {
			t.Fatal("a symlinked key directory loaded")
		}
	})
	t.Run("two owners", func(t *testing.T) {
		other := filepath.Join(buzzDir, t.Name()[len("TestLoadShare/"):])
		writeShare(t, other, "s", owner, body, DefaultKinds, exp)
		p := filepath.Join(other, "extensions", "remote", "keys", "s", "share.json")
		raw, _ := os.ReadFile(p)
		var doc struct{ Tags []Tag }
		_ = json.Unmarshal(raw, &doc)
		doc.Tags[1] = newThrowaway(t).grant(t, body.PublicHex(), doc.Tags[1].Kind, exp.Unix())
		raw, _ = json.Marshal(map[string]any{"tags": doc.Tags})
		_ = os.WriteFile(p, raw, 0o600)
		if _, err := LoadShare(other, "s"); err == nil || !strings.Contains(err.Error(), "more than one owner") {
			t.Fatalf("mixed owners: %v", err)
		}
	})
	// review item 2: a share outside ~/.amq-squad/buzz/ (a seat-writable
	// workspace) is refused, even when it is a complete enrollment
	t.Run("outside the buzz dir", func(t *testing.T) {
		for _, other := range []string{filepath.Join(realTempDir(t), "root"), buzzDir + "-evil"} {
			writeShare(t, other, "s", owner, body, DefaultKinds, exp)
			if _, err := LoadShare(other, "s"); err == nil || !strings.Contains(err.Error(), "not under") {
				t.Fatalf("share at %s: %v", other, err)
			}
		}
	})
	t.Run("bad names", func(t *testing.T) {
		for _, c := range [][2]string{{"relative/root", "s"}, {root, ".."}, {root, "a/b"}, {root + "/", "s"}} {
			if _, err := LoadShare(c[0], c[1]); err == nil {
				t.Errorf("LoadShare(%q, %q) loaded", c[0], c[1])
			}
		}
	})
}

func TestResolveTarget(t *testing.T) {
	tg, err := Resolve("/r", "lead-a", []string{"lead-b"})
	if err != nil || tg.Handle != "lead-a" || !slices.Equal(tg.Leads, []string{"lead-b", "lead-a"}) {
		t.Fatalf("lead target = %+v, %v", tg, err)
	}
	// 4-3 merged: no --lead attaches to the operator's user mailbox
	if op, err := Resolve("/r", "", nil); err != nil || op.Handle != "user" {
		t.Fatalf("no lead = %+v, %v", op, err)
	}
	if _, err := resolve(false, "/r", "", nil); err == nil {
		t.Fatal("lead mode resolved with no lead")
	}
	if _, err := Resolve("/r", "lead-a", []string{"Bad Name"}); err == nil {
		t.Fatal("a bad lead handle resolved")
	}
	// the 4-3 switch: same code path, the mailbox becomes the operator's user handle
	op, err := resolve(true, "/r", "", []string{"lead-a", "lead-b"})
	if err != nil || op.Handle != "user" || !slices.Equal(op.Leads, []string{"lead-a", "lead-b"}) {
		t.Fatalf("operator target = %+v, %v", op, err)
	}
	if !AttachToOperator {
		t.Fatal("AttachToOperator is off after brief 4-3 merged")
	}
}

func TestPresenceFromStatus(t *testing.T) {
	live := statusDoc(t, "lead-a", true)
	p, err := FromStatus(live, "lead-a")
	if err != nil || !p.Present || p.State != status.StateNeedsYou || !slices.Equal(p.Gates, []string{"g-open"}) {
		t.Fatalf("live = %+v, %v", p, err)
	}
	if !strings.Contains(p.Activity, "lead-a: present") || !strings.Contains(p.Activity, "open gates: g-open") || !strings.Contains(p.Activity, "last: merged #12") {
		t.Fatalf("activity = %q", p.Activity)
	}
	stopped := statusDoc(t, "lead-a", false)
	p, err = FromStatus(stopped, "lead-a")
	// an open gate keeps a stopped seat needs_you; it is still not present
	if err != nil || p.Present || p.State != status.StateNeedsYou || !strings.Contains(p.Activity, "lead-a: not present") {
		t.Fatalf("stopped with a gate = %+v, %v", p, err)
	}
	now := time.Now().UTC()
	plain, _ := json.Marshal(status.Build(status.Input{Now: now, Seats: []status.SeatInput{{Handle: "lead-a", HasSignal: true}}}))
	if p, err := FromStatus(plain, "lead-a"); err != nil || p.Present || p.State != status.StateStale {
		t.Fatalf("stopped = %+v, %v", p, err)
	}
	// presence reads the row's live field: a running seat with an open gate
	// and no recorded activity is present (the 5-minute heuristic said not)
	gated, _ := json.Marshal(status.Build(status.Input{Now: now, Seats: []status.SeatInput{{Handle: "lead-a", Live: true, HasSignal: true}},
		Gates: []status.GateInput{{ID: "g", Class: "tag", Digest: "sha256:" + strings.Repeat("0", 64), State: "open", RaisedBy: "lead-a", Since: now}}}))
	if p, _ := FromStatus(gated, "lead-a"); !p.Present || p.State != status.StateNeedsYou {
		t.Fatalf("running seat with a gate = %+v", p)
	}
	if p, _ := FromStatus(live, "nobody"); p.Present || !strings.Contains(p.Activity, "no status row") {
		t.Fatalf("absent handle = %+v", p)
	}
	// ruling 16: a document with squads is a fleet; the bound seat can be in any squad
	var a, b map[string]any
	_ = json.Unmarshal(stopped, &a)
	_ = json.Unmarshal(statusDoc(t, "user", true), &b)
	fleet, _ := json.Marshal(map[string]any{"schema_version": 1, "kind": "fleet", "producer": "amq-squad", "producer_version": "t", "generated_at": time.Now().UTC(), "squads": []any{a, b}})
	p, err = FromStatus(fleet, "user")
	if err != nil || !p.Present || p.Squads != 2 || !strings.Contains(p.Activity, "over 2 squads") {
		t.Fatalf("fleet = %+v, %v", p, err)
	}
	for _, bad := range []string{`{}`, `{"schema_version":2}`, `[`, `{"schema_version":1,"squads":[{"schema_version":2}]}`} {
		if _, err := FromStatus([]byte(bad), "x"); err == nil {
			t.Errorf("FromStatus(%s) accepted", bad)
		}
	}
}

type fakeEndpoint struct {
	running      bool
	starts, stop int
}

func (f *fakeEndpoint) Running() bool { return f.running }
func (f *fakeEndpoint) Start() error  { f.running = true; f.starts++; return nil }
func (f *fakeEndpoint) Stop() error   { f.running = false; f.stop++; return nil }

func TestSupervisorFollowsPresence(t *testing.T) {
	ep := &fakeEndpoint{}
	s := Supervisor{Endpoint: ep}
	steps := []struct {
		present bool
		want    string
	}{{false, ""}, {true, "started"}, {true, ""}, {false, "stopped"}, {false, ""}, {true, "started"}}
	for i, st := range steps {
		got, err := s.Apply(Presence{Present: st.present})
		if err != nil || got != st.want {
			t.Fatalf("step %d: %q, %v, want %q", i, got, err, st.want)
		}
	}
	if ep.starts != 2 || ep.stop != 1 {
		t.Fatalf("starts %d stops %d", ep.starts, ep.stop)
	}
}

func TestParseRoute(t *testing.T) {
	leads := []string{"lead-a", "lead-b"}
	for _, c := range []struct{ in, lead, text string }{
		{"@lead-b please rebase #12", "lead-b", "please rebase #12"},
		{"@Lead-B: ship it", "lead-b", "ship it"},
		{"lead-a: what is blocked?", "lead-a", "what is blocked?"},
		{"@lead-c hello", "", "@lead-c hello"},
		{"@lead-a", "", "@lead-a"},
		{"note: lead-a is slow", "", "note: lead-a is slow"},
		{"plain message", "", "plain message"},
		{"  lead-b:  trimmed  ", "lead-b", "trimmed"},
	} {
		r := ParseRoute(c.in, leads)
		if r.Lead != c.lead || r.Text != c.text {
			t.Errorf("ParseRoute(%q) = %+v, want lead %q text %q", c.in, r, c.lead, c.text)
		}
	}
}

func TestDeliverRoutesToANamedLead(t *testing.T) {
	m := &fakeMailer{}
	tg := Target{Root: "/r", Handle: "lead-a", Leads: []string{"lead-a", "lead-b"}, Registry: fakeLeads{"lead-a": true, "lead-b": true}}
	id := strings.Repeat("ab", 32)
	d, err := Deliver(context.Background(), m, tg, id, "@lead-b please rebase")
	if err != nil || d.Mailbox != "lead-a" || d.Lead != "lead-b" || len(m.sent) != 2 {
		t.Fatalf("deliver = %+v, %v, sent %d", d, err, len(m.sent))
	}
	first, fwd := m.sent[0], m.sent[1]
	if first.From != "buzz" || first.To != "lead-a" || first.Body != "@lead-b please rebase" || !slices.Contains(first.Labels, "nostr:"+id) {
		t.Fatalf("mailbox copy = %+v", first)
	}
	if fwd.To != "lead-b" || fwd.Body != "please rebase" || !slices.Equal(fwd.Refs, []string{"msg-1"}) || !slices.Contains(fwd.Labels, "routed") || !slices.Contains(fwd.Labels, "nostr:"+id) {
		t.Fatalf("forward = %+v", fwd)
	}
	m2 := &fakeMailer{}
	d, err = Deliver(context.Background(), m2, tg, id, "lead-a: just you")
	if err != nil || d.Lead != "" || len(m2.sent) != 1 {
		t.Fatalf("self route = %+v, %v, %d", d, err, len(m2.sent))
	}
	if _, err := Deliver(context.Background(), &fakeMailer{err: errors.New("down")}, tg, id, "x"); err == nil {
		t.Fatal("a failed send was not reported")
	}
}

type fakeService struct {
	st  ServiceState
	err error
}

func (f fakeService) Status(context.Context, string) (ServiceState, error) { return f.st, f.err }

func TestServiceWarning(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		st   ServiceState
		err  error
		want string
	}{
		{ServiceState{State: "current", Root: "/m", Running: true}, nil, ""},
		{ServiceState{State: "missing", Root: "/m"}, nil, "absent"},
		{ServiceState{State: "current", Root: "/m"}, nil, "not running"},
		{ServiceState{State: "stale", Root: "/m"}, nil, "another amq-remote binary"},
		{ServiceState{State: "other_root", Root: "/m", InstalledRoot: "/x"}, nil, "runs root /x"},
		{ServiceState{}, errors.New("no launchctl"), "unknown"},
	} {
		got := ServiceWarning(ctx, fakeService{c.st, c.err}, "")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("state %+v err %v: warning %q, want %q", c.st, c.err, got, c.want)
		}
	}
	if !strings.Contains(ServiceWarning(ctx, nil, ""), "not checked") {
		t.Fatal("no service checker gave no warning")
	}
}
