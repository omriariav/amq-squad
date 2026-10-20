package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

const testChannel = "dm-channel-s1"

// testNow is the gate's issue time. With the 60 s skew and a 30 minute
// window: not_before = testNow - 60 s, not_after = testNow + 30 min.
var testNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

type fixture struct {
	owner, seat, body SecretKey
	gate, other       GateRecord
	question          *GateQuestion
	pin               Pin // no thread binding
	tpin              Pin // thread binding with the body pin
	root              string
	// The core's issuer key; the gate records name it.
	issuerKey string
	signer    operatorauth.AuthorizationSigner
	trust     operatorauth.AuthorizationTrustStore
	identity  GateIdentity
}

func mustKey(t *testing.T) SecretKey {
	t.Helper()
	k, err := NewSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func testSpec(id string) GateSpec {
	return GateSpec{GateID: id, Class: "merge", Repo: "omriariav/amq-squad", BaseSHA: strings.Repeat("a", 40),
		PRs: []PRRef{{Number: 12, HeadSHA: strings.Repeat("b", 40)}, {Number: 15, HeadSHA: strings.Repeat("c", 40)}}, WindowMinutes: 30}
}

// newFixture renders two gates into a store under a temp AMQ root and posts
// a body-signed question for the first.
func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{owner: mustKey(t), seat: mustKey(t), body: mustKey(t), root: realTempDir(t)}
	f.issuerKey = filepath.Join(f.root, "issuer.pem")
	if _, err := WriteIssuerKey(f.issuerKey, filepath.Join(f.root, "trust.json")); err != nil {
		t.Fatal(err)
	}
	var err error
	if f.signer, err = operatorauth.LoadEd25519Signer(f.issuerKey); err != nil {
		t.Fatal(err)
	}
	if f.trust, err = operatorauth.LoadAuthorizationTrustStore(filepath.Join(f.root, "trust.json")); err != nil {
		t.Fatal(err)
	}
	f.identity = GateIdentity{OwnerPubKey: f.owner.PublicHex(), IssuerKeyID: f.signer.KeyID()}
	store := GateStoreFor(f.root)
	for i, id := range []string{"g-20261021-01", "g-20261021-02"} {
		g, err := Render(testSpec(id), testNow, f.identity)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.Create(g); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.gate = g
		} else {
			f.other = g
		}
	}
	q := Event{CreatedAt: testNow.Unix(), Kind: 9, Tags: [][]string{{"h", testChannel}, {"p", f.owner.PublicHex()}}, Content: f.gate.Text}
	if err := f.body.Sign(&q); err != nil {
		t.Fatal(err)
	}
	f.question = &GateQuestion{GateID: f.gate.GateID, EventID: q.ID, RawEvent: string(q.Marshal())}
	if err := store.PutQuestion(*f.question); err != nil {
		t.Fatal(err)
	}
	f.pin = Pin{Owner: f.owner.PublicHex(), Channel: testChannel}
	f.tpin = Pin{Owner: f.owner.PublicHex(), Channel: testChannel, RequireThread: true, Body: f.body.PublicHex()}
	return f
}

func prefixOf(g GateRecord) string { return strings.TrimPrefix(g.Digest, "sha256:")[:minDigestPrefix] }

// answer signs a kind 9 in the channel with key k, created at, content c.
func answer(t *testing.T, k SecretKey, at time.Time, c string, tags ...[]string) []byte {
	t.Helper()
	e := Event{CreatedAt: at.Unix(), Kind: 9, Tags: append([][]string{{"h", testChannel}}, tags...), Content: c}
	if err := k.Sign(&e); err != nil {
		t.Fatal(err)
	}
	return e.Marshal()
}

func approveText(g GateRecord) string {
	return "approve " + g.GateID + " " + g.Class + " " + prefixOf(g)
}

func (f fixture) reply() []string { return []string{"e", f.question.EventID, "", "reply"} }

func mutate(t *testing.T, raw []byte, fn func(*Event)) []byte {
	t.Helper()
	e, err := ParseEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	fn(&e)
	return e.Marshal()
}

func codeOf(o Outcome, err error) string {
	if err != nil {
		return RejectionCode(err)
	}
	return o.Decision
}

func TestRenderCarriesOnlyIDsClassDigestAndPRs(t *testing.T) {
	f := newFixture(t)
	for _, leak := range []string{"omriariav", "amq-squad/", strings.Repeat("a", 40), strings.Repeat("b", 40), "main"} {
		if strings.Contains(f.gate.Text, leak) {
			t.Fatalf("gate text leaks %q:\n%s", leak, f.gate.Text)
		}
	}
	for _, want := range []string{"g-20261021-01", "class: merge", f.gate.Digest, "#12 #15", approveText(f.gate)} {
		if !strings.Contains(f.gate.Text, want) {
			t.Fatalf("gate text lacks %q:\n%s", want, f.gate.Text)
		}
	}
	// Reordering PRs, a new head or a new base changes the digest (R34).
	base := testSpec("g-x01")
	d0 := base.TargetDigest()
	re := base
	re.PRs = []PRRef{base.PRs[1], base.PRs[0]}
	head := base
	head.PRs = []PRRef{base.PRs[0], {15, strings.Repeat("d", 40)}}
	nb := base
	nb.BaseSHA = strings.Repeat("e", 40)
	for name, s := range map[string]GateSpec{"reorder": re, "new head": head, "new base": nb} {
		if s.TargetDigest() == d0 {
			t.Fatalf("%s did not change the digest", name)
		}
	}
	bad := base
	bad.Repo = "../etc"
	if _, err := Render(bad, testNow, f.identity); err == nil {
		t.Fatal("render accepted a path-like repo")
	}
}

// TestGateStoreBinding: one gate id is one immutable record, and the
// verifier uses the stored record only.
func TestGateStoreBinding(t *testing.T) {
	f := newFixture(t)
	store := GateStoreFor(f.root)
	// A second record for the same gate id (another target) is refused.
	other := testSpec(f.gate.GateID)
	other.BaseSHA = strings.Repeat("f", 40)
	g2, err := Render(other, testNow, f.identity)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Create(g2); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second record for a gate id: %v", err)
	}
	if err := store.PutQuestion(*f.question); !errors.Is(err, os.ErrExist) {
		t.Fatalf("second question: %v", err)
	}
	// A record edited on disk to name another digest no longer validates:
	// its text is the rendering of its own fields.
	path := filepath.Join(store.Dir, f.gate.GateID+".json")
	var rec GateRecord
	raw, _ := os.ReadFile(path)
	_ = json.Unmarshal(raw, &rec)
	rec.Digest = g2.Digest
	blob, _ := json.Marshal(rec)
	_ = os.WriteFile(path, blob, 0o600)
	if _, err := store.Load(f.gate.GateID); err == nil {
		t.Fatal("edited record loaded")
	}
	// Text edited to match the new digest: the text digest no longer matches.
	rec.Text = gateText(rec.GateID, rec.Class, rec.Digest, rec.PRs)
	blob, _ = json.Marshal(rec)
	_ = os.WriteFile(path, blob, 0o600)
	if _, err := store.Load(f.gate.GateID); err == nil {
		t.Fatal("re-rendered record with a stale text digest loaded")
	}
	if _, err := store.Load("../g-20261021-02"); err == nil {
		t.Fatal("path-like gate id loaded")
	}
}

func TestPositiveApprove(t *testing.T) {
	f := newFixture(t)
	in, at := testNow.Add(time.Minute), testNow.Add(2*time.Minute)
	if o, err := Verify(answer(t, f.owner, in, approveText(f.gate)), f.gate, nil, f.pin, at); err != nil || o.Decision != "approve" {
		t.Fatalf("got %+v, %v", o, err)
	}
	// Phone keyboards capitalize the first word and may add spaces.
	cap := "Approve  " + f.gate.GateID + " MERGE " + strings.ToUpper(prefixOf(f.gate))
	if o, err := Verify(answer(t, f.owner, in, cap), f.gate, nil, f.pin, at); err != nil || o.Decision != "approve" {
		t.Fatalf("case-folded answer: %+v, %v", o, err)
	}
	// Thread binding: the answer replies to the stored body-signed question.
	if o, err := Verify(answer(t, f.owner, in, approveText(f.gate), f.reply()), f.gate, f.question, f.tpin, at); err != nil || o.Decision != "approve" {
		t.Fatalf("threaded: %+v, %v", o, err)
	}
}

// TestNegative is the S1 step 5 table plus the boundary cases.
func TestNegative(t *testing.T) {
	f := newFixture(t)
	in := testNow.Add(time.Minute)
	at := testNow.Add(2 * time.Minute)
	good := answer(t, f.owner, in, approveText(f.gate))
	threaded := answer(t, f.owner, in, approveText(f.gate), f.reply())
	_, na := testNow.Add(-clockSkew), testNow.Add(30*time.Minute)
	forgedQ := func(mut func(*Event)) *GateQuestion {
		e, _ := ParseEvent([]byte(f.question.RawEvent))
		mut(&e)
		return &GateQuestion{GateID: f.gate.GateID, EventID: e.ID, RawEvent: string(e.Marshal())}
	}
	cases := []struct {
		name string
		raw  []byte
		gate GateRecord
		q    *GateQuestion
		pin  Pin
		now  time.Time
		want string
	}{
		{"wrong pubkey (another key signs a valid tuple)", answer(t, f.seat, in, approveText(f.gate)), f.gate, nil, f.pin, at, RejectWrongPubkey},
		{"wrong pubkey (share body key signs, e.g. a model via amq reply)", answer(t, f.body, in, approveText(f.gate)), f.gate, nil, f.pin, at, RejectWrongPubkey},
		{"altered content, id kept", mutate(t, good, func(e *Event) { e.Content += "x" }), f.gate, nil, f.pin, at, RejectBadID},
		{"altered content, id recomputed", mutate(t, good, func(e *Event) {
			e.Content = "approve " + f.other.GateID + " merge " + prefixOf(f.other)
			id := e.ComputeID()
			e.ID = hexOf(id[:])
		}), f.gate, nil, f.pin, at, RejectBadSignature},
		{"owner pubkey with a foreign signature", mutate(t, answer(t, f.seat, in, approveText(f.gate)), func(e *Event) {
			e.PubKey = f.owner.PublicHex()
			id := e.ComputeID()
			e.ID = hexOf(id[:])
		}), f.gate, nil, f.pin, at, RejectBadSignature},
		{"created before the window", answer(t, f.owner, testNow.Add(-time.Hour), approveText(f.gate)), f.gate, nil, f.pin, at, RejectWindow},
		{"created after the window", answer(t, f.owner, na.Add(time.Second), approveText(f.gate)), f.gate, nil, f.pin, na, RejectWindow},
		{"valid answer presented after the gate closed", good, f.gate, nil, f.pin, na.Add(time.Hour), RejectWindow},
		{"created in the future beyond 60 s", answer(t, f.owner, at.Add(61*time.Second), approveText(f.gate)), f.gate, nil, f.pin, at, RejectFuture},
		{"replay for a different gate id", good, f.other, nil, f.pin, at, RejectGateMismatch},
		{"wrong kind (kind 1 note)", mutate(t, good, func(e *Event) { e.Kind = 1; _ = f.owner.Sign(e) }), f.gate, nil, f.pin, at, RejectWrongKind},
		{"wrong DM channel", mutate(t, good, func(e *Event) { e.Tags = [][]string{{"h", "other"}}; _ = f.owner.Sign(e) }), f.gate, nil, f.pin, at, RejectWrongChannel},
		{"two h tags", mutate(t, good, func(e *Event) { e.Tags = append(e.Tags, []string{"h", "other"}); _ = f.owner.Sign(e) }), f.gate, nil, f.pin, at, RejectWrongChannel},
		{"wrong class", answer(t, f.owner, in, "approve "+f.gate.GateID+" release "+prefixOf(f.gate)), f.gate, nil, f.pin, at, RejectClassMismatch},
		{"wrong digest prefix", answer(t, f.owner, in, "approve "+f.gate.GateID+" merge "+strings.Repeat("0", 16)), f.gate, nil, f.pin, at, RejectDigest},
		{"short digest prefix", answer(t, f.owner, in, "approve "+f.gate.GateID+" merge "+prefixOf(f.gate)[:8]), f.gate, nil, f.pin, at, RejectBadTuple},
		{"bare yes (upstream typed approve) is not a gate answer", answer(t, f.owner, in, "yes"), f.gate, nil, f.pin, at, RejectBadTuple},
		{"extra instruction after the tuple", answer(t, f.owner, in, approveText(f.gate)+" and-merge-all"), f.gate, nil, f.pin, at, RejectBadTuple},
		{"LF inside the tuple", answer(t, f.owner, in, strings.Replace(approveText(f.gate), " merge ", "\nmerge ", 1)), f.gate, nil, f.pin, at, RejectBadTuple},
		{"trailing LF", answer(t, f.owner, in, approveText(f.gate)+"\n"), f.gate, nil, f.pin, at, RejectBadTuple},
		{"leading CR", answer(t, f.owner, in, "\r"+approveText(f.gate)), f.gate, nil, f.pin, at, RejectBadTuple},
		{"unknown event field", []byte(strings.Replace(string(good), `{"id"`, `{"x":1,"id"`, 1)), f.gate, nil, f.pin, at, RejectMalformed},
		{"seat-signed approve under a pin misconfigured to the seat key (record names the owner)", answer(t, f.seat, in, approveText(f.gate)), f.gate, nil,
			Pin{Owner: f.seat.PublicHex(), Channel: testChannel}, at, RejectGateRecord},
		{"deny is a valid deny", answer(t, f.owner, in, "deny "+f.gate.GateID+" merge "+prefixOf(f.gate)), f.gate, nil, f.pin, at, "deny"},
		{"deny from a seat key is rejected, not a deny", answer(t, f.seat, in, "deny "+f.gate.GateID+" merge "+prefixOf(f.gate)), f.gate, nil, f.pin, at, RejectWrongPubkey},
		{"thread required, answer not under the question", good, f.gate, f.question, f.tpin, at, RejectThread},
		{"thread required, no stored question", threaded, f.gate, nil, f.tpin, at, RejectQuestion},
		{"thread required, no body pin", threaded, f.gate, f.question, Pin{Owner: f.pin.Owner, Channel: testChannel, RequireThread: true}, at, RejectQuestion},
		{"question signed by a seat key, not the body", answer(t, f.owner, in, approveText(f.gate), []string{"e", forgedQ(func(e *Event) { _ = f.seat.Sign(e) }).EventID, "", "reply"}),
			f.gate, forgedQ(func(e *Event) { _ = f.seat.Sign(e) }), f.tpin, at, RejectQuestion},
		{"question text differs from the record", answer(t, f.owner, in, approveText(f.gate), []string{"e", forgedQ(func(e *Event) { e.Content += " #99"; _ = f.body.Sign(e) }).EventID, "", "reply"}),
			f.gate, forgedQ(func(e *Event) { e.Content += " #99"; _ = f.body.Sign(e) }), f.tpin, at, RejectQuestion},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := Verify(c.raw, c.gate, c.q, c.pin, c.now)
			got := codeOf(o, err)
			if got != c.want {
				t.Fatalf("got %q (%v), want %q", got, err, c.want)
			}
			if c.want == "deny" && o.Decision == "approve" {
				t.Fatal("a deny verified as an approve")
			}
			t.Logf("%-62s -> %s", c.name, got)
		})
	}
}

// TestClockBoundaries pins the two clock rules at their exact edges:
// not_before <= created_at <= not_after and created_at - 60 s <= now <=
// not_after. Times are whole seconds, as created_at is.
func TestClockBoundaries(t *testing.T) {
	f := newFixture(t)
	nb, na := testNow.Add(-clockSkew), testNow.Add(30*time.Minute)
	cases := []struct {
		name         string
		created, now time.Time
		want         string
	}{
		{"created == not_before", nb, testNow, "approve"},
		{"created == not_before - 1 s", nb.Add(-time.Second), testNow, RejectWindow},
		{"created == not_after, now == not_after", na, na, "approve"},
		{"created == not_after + 1 s", na.Add(time.Second), na, RejectWindow},
		{"now == not_after", testNow, na, "approve"},
		{"now == not_after + 1 s (no grace)", testNow, na.Add(time.Second), RejectWindow},
		{"created == now + 60 s", testNow.Add(2 * time.Minute), testNow.Add(time.Minute), "approve"},
		{"created == now + 61 s", testNow.Add(2*time.Minute + time.Second), testNow.Add(time.Minute), RejectFuture},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := codeOf(Verify(answer(t, f.owner, c.created, approveText(f.gate)), f.gate, nil, f.pin, c.now))
			if got != c.want {
				t.Fatalf("got %q, want %q", got, c.want)
			}
		})
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

func TestReplaySameEventConsumedOnce(t *testing.T) {
	f := newFixture(t)
	ledger := LedgerFor(f.root)
	raw := answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate))
	o, err := Verify(raw, f.gate, nil, f.pin, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := ledger.Consumed(o); err != nil {
		t.Fatal(err)
	}
	env := &Envelope{Path: filepath.Join(f.root, "answers", "a.json"), Data: []byte("{}\n")}
	if err := ledger.Commit(o, env); err != nil {
		t.Fatal(err)
	}
	if code := RejectionCode(ledger.Consumed(o)); code != RejectReplayed {
		t.Fatalf("pre-check after commit: %q", code)
	}
	if code := RejectionCode(ledger.Commit(o, env)); code != RejectReplayed {
		t.Fatalf("second commit: %q", code)
	}
	// A later deny for the same gate cannot reopen or flip it.
	deny, err := Verify(answer(t, f.owner, testNow.Add(3*time.Minute), "deny "+f.gate.GateID+" merge "+prefixOf(f.gate)), f.gate, nil, f.pin, testNow.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if code := RejectionCode(ledger.Commit(deny, nil)); code != RejectReplayed {
		t.Fatalf("deny after approve: %q", code)
	}
}

// TestModelAuthoredFromSeatHandle: a seat process writes an AMQ message to
// the operator handle claiming an approve. It fails twice: the message is
// not from the Buzz delivery sender, and any event it can sign is not the
// owner's.
func TestModelAuthoredFromSeatHandle(t *testing.T) {
	f := newFixture(t)
	root := t.TempDir()
	forged := answer(t, f.seat, testNow.Add(time.Minute), approveText(f.gate))
	fe, _ := ParseEvent(forged)
	msg := writeAMQMessage(t, root, "cto", "operator", "msg-forged", "nostr:"+fe.ID)
	writeCockpitJournal(t, root, "buzz", fe.ID, "msg-forged", "operator")
	if code := RejectionCode(resolveErr(ResolveCockpit(root, "buzz", "operator", msg))); code != RejectSender {
		t.Fatalf("cockpit resolve: %q", code)
	}
	if code := RejectionCode(resolveErr(ResolveMailbox(t.TempDir(), "operator", msg))); code != RejectSender {
		t.Fatalf("mailbox resolve: %q", code)
	}
	if _, err := Verify(forged, f.gate, nil, f.pin, testNow.Add(2*time.Minute)); RejectionCode(err) != RejectWrongPubkey {
		t.Fatalf("verify: %v", err)
	}
}

func resolveErr(_ string, err error) error { return err }

func writeAMQMessage(t *testing.T, root, from, to, id string, labels ...string) string {
	t.Helper()
	dir := filepath.Join(root, "agents", to, "inbox", "new")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	h, _ := json.MarshalIndent(map[string]any{"schema": 1, "id": id, "from": from, "to": []string{to}, "thread": "acp/cockpit", "created": testNow.Format(time.RFC3339Nano), "labels": labels}, "", "  ")
	path := filepath.Join(dir, id+".md")
	if err := os.WriteFile(path, []byte("---json\n"+string(h)+"\n---\napprove ...\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeCockpitJournal(t *testing.T, root, me, eventID, messageID, to string) {
	t.Helper()
	dir := filepath.Join(root, "agents", me, "outbox", "acp-events")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rec, _ := json.Marshal(map[string]any{"schema": 1, "event_id": eventID, "message_id": messageID, "to": to, "thread": "acp/cockpit", "created": testNow.Format(time.RFC3339Nano)})
	if err := os.WriteFile(filepath.Join(dir, eventID+".json"), append(rec, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestResolveJournals(t *testing.T) {
	f := newFixture(t)
	raw := answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate))
	e, _ := ParseEvent(raw)
	root := t.TempDir()
	// 1. cockpit journal (internal/acp/event.go:98) with the nostr label.
	msg := writeAMQMessage(t, root, "buzz", "operator", "2026-10-21T12-01-00.000Z_pid1_abcd", "acp", "cockpit", "nostr:"+e.ID)
	writeCockpitJournal(t, root, "buzz", e.ID, "2026-10-21T12-01-00.000Z_pid1_abcd", "operator")
	if id, err := ResolveCockpit(root, "buzz", "operator", msg); err != nil || id != e.ID {
		t.Fatalf("cockpit: %s %v", id, err)
	}
	// A label that disagrees with the journal refuses.
	bad := writeAMQMessage(t, root, "buzz", "operator", "2026-10-21T12-01-00.000Z_pid1_abcd", "nostr:"+strings.Repeat("0", 64))
	if _, err := ResolveCockpit(root, "buzz", "operator", bad); err == nil {
		t.Fatal("label/journal disagreement accepted")
	}
	// 2. binding-mode mailbox claim (internal/acp/mailbox.go:389-419).
	state := t.TempDir()
	agentDir := filepath.Join(state, "remote-events", strings.Repeat("e", 64))
	_ = os.MkdirAll(agentDir, 0o700)
	claim, _ := json.Marshal(map[string]string{"message_id": "m-2", "created": testNow.Format(time.RFC3339Nano), "thread": "t", "channel": testChannel})
	_ = os.WriteFile(filepath.Join(agentDir, e.ID+".mailbox.json"), claim, 0o600)
	mb := writeAMQMessage(t, root, "buzz", "operator", "m-2", "acp", "buzz")
	if id, err := ResolveMailbox(state, "operator", mb); err != nil || id != e.ID {
		t.Fatalf("mailbox: %s %v", id, err)
	}
	// 3. amq-remote serve ingress claim (buzzio/ledger.go:41-91, relay_dm.go:128).
	// A missing ingress directory is an empty result, not an error.
	if claims, skipped, err := ResolveIngress(root, "s1gate", f.pin.Owner, testChannel, 0); err != nil || len(claims)+len(skipped) != 0 {
		t.Fatalf("missing ingress dir: %v %v %v", claims, skipped, err)
	}
	ing := filepath.Join(root, "extensions", "remote", "shares", "s1gate", "buzz", "ingress")
	_ = os.MkdirAll(ing, 0o700)
	c, _ := json.Marshal(IngressClaim{EventID: e.ID, Owner: f.pin.Owner, Body: f.body.PublicHex(), Channel: testChannel, DMChannel: testChannel, CreatedAt: e.CreatedAt})
	_ = os.WriteFile(filepath.Join(ing, e.ID+".json"), c, 0o600)
	// Bad candidates are skipped and reported, never fatal.
	_ = os.WriteFile(filepath.Join(ing, strings.Repeat("1", 64)+".json"), []byte("{not json"), 0o600)
	_ = os.WriteFile(filepath.Join(ing, strings.Repeat("2", 64)+".json"), []byte(`{"event_id":"`+strings.Repeat("3", 64)+`"}`), 0o600)
	_ = os.WriteFile(filepath.Join(ing, e.ID+".settled.json"), []byte(`{"op":"submit"}`), 0o600)
	claims, skipped, err := ResolveIngress(root, "s1gate", f.pin.Owner, testChannel, testNow.Unix())
	if err != nil || len(claims) != 1 || claims[0].EventID != e.ID || len(skipped) != 2 {
		t.Fatalf("ingress: %+v skipped %v %v", claims, skipped, err)
	}
}

// signedEnvelope builds an answer file whose payload is mutated before a
// TRUSTED issuer signs it: the issuer-mislabel cases.
func signedEnvelope(t *testing.T, f fixture, o Outcome, raw []byte, signer operatorauth.AuthorizationSigner, mut func(*operatorauth.AuthorizationPayload)) AnswerFile {
	t.Helper()
	ns := Namespace{ProjectDir: "/repo", Profile: "spike", Session: "s1", Generation: "gen-1"}
	p, err := answerPayload(o, raw, f.gate, f.question, f.tpin, ns, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	mut(&p)
	af, err := signAnswerFile(p, o, raw, f.gate, f.question, f.tpin, "wss://relay.example", signer)
	if err != nil {
		t.Fatal(err)
	}
	return af
}

func TestEnvelopeRoundTripAndTamper(t *testing.T) {
	f := newFixture(t)
	signer, ts := f.signer, f.trust
	raw := answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate), f.reply())
	o, err := Verify(raw, f.gate, f.question, f.tpin, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	af := signedEnvelope(t, f, o, raw, signer, func(*operatorauth.AuthorizationPayload) {})
	if err := VerifyAnswerFile(af, ts, f.tpin, false); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	envRaw, _ := json.Marshal(af.Envelope)
	if _, err := operatorauth.DecodeAuthorizationEnvelope(envRaw); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	// Trusted issuer, mislabelled payload: operatorauth accepts the
	// signature, the bip340 slot-in must refuse the semantics.
	mislabel := map[string]func(*operatorauth.AuthorizationPayload){
		"action of another class": func(p *operatorauth.AuthorizationPayload) {
			p.GateKind, p.Action = operatorauth.GateRelease, "github_release"
		},
		"action of the same kind but another action": func(p *operatorauth.AuthorizationPayload) { p.Action = "protected_branch_push" },
		"gate-text hash not the record's": func(p *operatorauth.AuthorizationPayload) {
			for i := range p.Evidence {
				if p.Evidence[i].Kind == evidenceKindGate {
					p.Evidence[i].SHA256 = "sha256:" + strings.Repeat("7", 64)
				}
			}
		},
		"question id not the stored question": func(p *operatorauth.AuthorizationPayload) { p.QuestionMessageID = "nostr:" + strings.Repeat("5", 64) },
		"question id downgraded to text digest": func(p *operatorauth.AuthorizationPayload) {
			p.QuestionMessageID = "gate-text:" + strings.TrimPrefix(f.gate.TextSHA256, "sha256:")
		},
		"target not the record's digest": func(p *operatorauth.AuthorizationPayload) { p.Target = "sha256:" + strings.Repeat("9", 64) },
	}
	for name, mut := range mislabel {
		af := signedEnvelope(t, f, o, raw, signer, mut)
		if err := operatorauth.VerifyAuthorizationEnvelope(af.Envelope, ts); err != nil {
			t.Fatalf("%s: setup: issuer signature should verify: %v", name, err)
		}
		if err := VerifyAnswerFile(af, ts, f.tpin, false); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// Unsigned wrapper drift.
	wrapper := map[string]func(*AnswerFile){
		"raw event swapped for a seat-signed one": func(a *AnswerFile) {
			a.Evidence.RawEvent = string(answer(t, f.seat, testNow.Add(time.Minute), approveText(f.gate), f.reply()))
		},
		"evidence re-pointed at another owner":  func(a *AnswerFile) { a.Evidence.OwnerPubKey = f.seat.PublicHex() },
		"wrapper gate swapped for another gate": func(a *AnswerFile) { a.Gate = f.other },
		"wrapper gate text edited":              func(a *AnswerFile) { a.Gate.Text += " " },
		"wrapper question dropped":              func(a *AnswerFile) { a.Question = nil },
		"payload target edited after signing":   func(a *AnswerFile) { a.Envelope.Payload.Target = "sha256:" + strings.Repeat("9", 64) },
	}
	for name, mut := range wrapper {
		c := af
		mut(&c)
		if err := VerifyAnswerFile(c, ts, f.tpin, false); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// An envelope from an issuer key the core does not trust fails at
	// operatorauth.
	dir2 := realTempDir(t)
	key2 := filepath.Join(dir2, "rogue.pem")
	if _, err := WriteIssuerKey(key2, filepath.Join(dir2, "rogue.json")); err != nil {
		t.Fatal(err)
	}
	rogue, _ := operatorauth.LoadEd25519Signer(key2)
	forged := signedEnvelope(t, f, o, raw, rogue, func(*operatorauth.AuthorizationPayload) {})
	if err := VerifyAnswerFile(forged, ts, f.tpin, false); err == nil || !strings.Contains(err.Error(), "operatorauth") {
		t.Fatalf("untrusted issuer: %v", err)
	}
	// A deny never becomes an envelope.
	deny, _ := Verify(answer(t, f.owner, testNow.Add(time.Minute), "deny "+f.gate.GateID+" merge "+prefixOf(f.gate)), f.gate, nil, f.pin, testNow.Add(2*time.Minute))
	if _, err := BuildAnswerFile(deny, raw, f.gate, nil, f.pin, "", Namespace{ProjectDir: "/repo", Profile: "spike", Session: "s1", Generation: "g"}, signer, testNow.Add(2*time.Minute)); err == nil {
		t.Fatal("deny produced an envelope")
	}
}

// realTempDir resolves macOS's /var -> /private/var symlink: operatorauth's
// secure file reads refuse a symlinked ancestor.
func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
