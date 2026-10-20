package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

const testChannel = "dm-channel-s1"

var testNow = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

type fixture struct {
	owner, seat, body SecretKey
	gate, other       GateRecord
	pin               Pin
}

func mustKey(t *testing.T) SecretKey {
	t.Helper()
	k, err := NewSecretKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	f := fixture{owner: mustKey(t), seat: mustKey(t), body: mustKey(t)}
	spec := GateSpec{GateID: "g-20261021-01", Class: "merge", Repo: "omriariav/amq-squad", BaseSHA: strings.Repeat("a", 40),
		PRs: []PRRef{{Number: 12, HeadSHA: strings.Repeat("b", 40)}, {Number: 15, HeadSHA: strings.Repeat("c", 40)}}, WindowMinutes: 30}
	var err error
	if f.gate, err = Render(spec, testNow); err != nil {
		t.Fatal(err)
	}
	spec.GateID = "g-20261021-02"
	if f.other, err = Render(spec, testNow); err != nil {
		t.Fatal(err)
	}
	f.pin = Pin{Owner: f.owner.PublicHex(), Channel: testChannel}
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

func mutate(t *testing.T, raw []byte, f func(*Event)) []byte {
	t.Helper()
	e, err := ParseEvent(raw)
	if err != nil {
		t.Fatal(err)
	}
	f(&e)
	return e.Marshal()
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
	base := GateSpec{GateID: "g-x01", Class: "merge", Repo: "o/r", BaseSHA: strings.Repeat("a", 40), WindowMinutes: 5,
		PRs: []PRRef{{12, strings.Repeat("b", 40)}, {15, strings.Repeat("c", 40)}}}
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
	if _, err := Render(bad, testNow); err == nil {
		t.Fatal("render accepted a path-like repo")
	}
}

func TestPositiveApprove(t *testing.T) {
	f := newFixture(t)
	o, err := Verify(answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate)), f.gate, f.pin, testNow.Add(2*time.Minute))
	if err != nil || o.Decision != "approve" {
		t.Fatalf("got %+v, %v", o, err)
	}
	// Phone keyboards capitalize the first word and may add spaces.
	cap := "Approve  " + f.gate.GateID + " MERGE " + strings.ToUpper(prefixOf(f.gate)) + "\n"
	if o, err := Verify(answer(t, f.owner, testNow.Add(time.Minute), cap), f.gate, f.pin, testNow.Add(2*time.Minute)); err != nil || o.Decision != "approve" {
		t.Fatalf("case-folded answer: %+v, %v", o, err)
	}
}

// TestNegative is the S1 step 5 table.
func TestNegative(t *testing.T) {
	f := newFixture(t)
	in := testNow.Add(time.Minute)
	at := testNow.Add(2 * time.Minute)
	good := answer(t, f.owner, in, approveText(f.gate))
	cases := []struct {
		name string
		raw  []byte
		gate GateRecord
		pin  Pin
		now  time.Time
		want string // rejection code, or "deny" for a valid deny
	}{
		{"wrong pubkey (another key signs a valid tuple)", answer(t, f.seat, in, approveText(f.gate)), f.gate, f.pin, at, RejectWrongPubkey},
		{"wrong pubkey (share body key signs, e.g. a model via amq reply)", answer(t, f.body, in, approveText(f.gate)), f.gate, f.pin, at, RejectWrongPubkey},
		{"altered content, id kept", mutate(t, good, func(e *Event) { e.Content = strings.Replace(e.Content, "approve", "approve ", 1) + "x" }), f.gate, f.pin, at, RejectBadID},
		{"altered content, id recomputed", mutate(t, good, func(e *Event) {
			e.Content = "approve " + f.other.GateID + " merge " + prefixOf(f.other)
			id := e.ComputeID()
			e.ID = hexOf(id[:])
		}), f.gate, f.pin, at, RejectBadSignature},
		{"owner pubkey with a foreign signature", mutate(t, answer(t, f.seat, in, approveText(f.gate)), func(e *Event) {
			e.PubKey = f.owner.PublicHex()
			id := e.ComputeID()
			e.ID = hexOf(id[:])
		}), f.gate, f.pin, at, RejectBadSignature},
		{"created before the window", answer(t, f.owner, testNow.Add(-time.Hour), approveText(f.gate)), f.gate, f.pin, at, RejectWindow},
		{"created after the window", answer(t, f.owner, testNow.Add(31*time.Minute), approveText(f.gate)), f.gate, f.pin, testNow.Add(31 * time.Minute), RejectWindow},
		{"valid answer presented after the gate closed", good, f.gate, f.pin, testNow.Add(2 * time.Hour), RejectWindow},
		{"replay for a different gate id", good, f.other, f.pin, at, RejectGateMismatch},
		{"wrong kind (kind 1 note)", mutate(t, good, func(e *Event) { e.Kind = 1; _ = f.owner.Sign(e) }), f.gate, f.pin, at, RejectWrongKind},
		{"wrong DM channel", mutate(t, good, func(e *Event) { e.Tags = [][]string{{"h", "other"}}; _ = f.owner.Sign(e) }), f.gate, f.pin, at, RejectWrongChannel},
		{"two h tags", mutate(t, good, func(e *Event) { e.Tags = append(e.Tags, []string{"h", "other"}); _ = f.owner.Sign(e) }), f.gate, f.pin, at, RejectWrongChannel},
		{"wrong class", answer(t, f.owner, in, "approve "+f.gate.GateID+" release "+prefixOf(f.gate)), f.gate, f.pin, at, RejectClassMismatch},
		{"wrong digest prefix", answer(t, f.owner, in, "approve "+f.gate.GateID+" merge "+strings.Repeat("0", 16)), f.gate, f.pin, at, RejectDigest},
		{"short digest prefix", answer(t, f.owner, in, "approve "+f.gate.GateID+" merge "+prefixOf(f.gate)[:8]), f.gate, f.pin, at, RejectBadTuple},
		{"bare yes (upstream typed approve) is not a gate answer", answer(t, f.owner, in, "yes"), f.gate, f.pin, at, RejectBadTuple},
		{"extra instruction after the tuple", answer(t, f.owner, in, approveText(f.gate)+" and-merge-all"), f.gate, f.pin, at, RejectBadTuple},
		{"unknown event field", []byte(strings.Replace(string(good), `{"id"`, `{"x":1,"id"`, 1)), f.gate, f.pin, at, RejectMalformed},
		{"deny is a valid deny", answer(t, f.owner, in, "deny "+f.gate.GateID+" merge "+prefixOf(f.gate)), f.gate, f.pin, at, "deny"},
		{"deny from a seat key is rejected, not a deny", answer(t, f.seat, in, "deny "+f.gate.GateID+" merge "+prefixOf(f.gate)), f.gate, f.pin, at, RejectWrongPubkey},
		{"thread required, answer not in gate thread", good, withQuestion(f.gate), Pin{Owner: f.pin.Owner, Channel: testChannel, RequireThread: true}, at, RejectThread},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, err := Verify(c.raw, c.gate, c.pin, c.now)
			got := RejectionCode(err)
			if err == nil {
				got = o.Decision
			}
			if got != c.want {
				t.Fatalf("got %q (%v), want %q", got, err, c.want)
			}
			if c.want == "deny" && o.Decision == "approve" {
				t.Fatal("a deny verified as an approve")
			}
			t.Logf("%-60s -> %s", c.name, got)
		})
	}
}

func withQuestion(g GateRecord) GateRecord { g.QuestionEventID = strings.Repeat("f", 64); return g }

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}

func TestThreadReplyAccepted(t *testing.T) {
	f := newFixture(t)
	g := withQuestion(f.gate)
	raw := answer(t, f.owner, testNow.Add(time.Minute), approveText(g), []string{"e", g.QuestionEventID, "", "reply"})
	if _, err := Verify(raw, g, Pin{Owner: f.pin.Owner, Channel: testChannel, RequireThread: true}, testNow.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestReplaySameEventConsumedOnce(t *testing.T) {
	f := newFixture(t)
	dir := t.TempDir()
	raw := answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate))
	o, err := Verify(raw, f.gate, f.pin, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := Consume(dir, o); err != nil {
		t.Fatal(err)
	}
	if code := RejectionCode(Consume(dir, o)); code != RejectReplayed {
		t.Fatalf("second consume: %q", code)
	}
	// A later deny for the same gate cannot reopen or flip it.
	deny, err := Verify(answer(t, f.owner, testNow.Add(3*time.Minute), "deny "+f.gate.GateID+" merge "+prefixOf(f.gate)), f.gate, f.pin, testNow.Add(4*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if code := RejectionCode(Consume(dir, deny)); code != RejectReplayed {
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
	if _, err := Verify(forged, f.gate, f.pin, testNow.Add(2*time.Minute)); RejectionCode(err) != RejectWrongPubkey {
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
	ing := filepath.Join(root, "extensions", "remote", "shares", "s1gate", "buzz", "ingress")
	_ = os.MkdirAll(ing, 0o700)
	c, _ := json.Marshal(IngressClaim{EventID: e.ID, Owner: f.pin.Owner, Body: f.body.PublicHex(), Channel: testChannel, DMChannel: testChannel, CreatedAt: e.CreatedAt})
	_ = os.WriteFile(filepath.Join(ing, e.ID+".json"), c, 0o600)
	claims, err := ResolveIngress(root, "s1gate", f.pin.Owner, testChannel, testNow.Unix())
	if err != nil || len(claims) != 1 || claims[0].EventID != e.ID {
		t.Fatalf("ingress: %+v %v", claims, err)
	}
}

func TestEnvelopeRoundTripAndTamper(t *testing.T) {
	f := newFixture(t)
	dir := realTempDir(t)
	key, store := filepath.Join(dir, "issuer.pem"), filepath.Join(dir, "trust.json")
	if _, err := WriteIssuerKey(key, store); err != nil {
		t.Fatal(err)
	}
	signer, err := operatorauth.LoadEd25519Signer(key)
	if err != nil {
		t.Fatal(err)
	}
	ts, err := operatorauth.LoadAuthorizationTrustStore(store)
	if err != nil {
		t.Fatal(err)
	}
	raw := answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate))
	o, err := Verify(raw, f.gate, f.pin, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ns := Namespace{ProjectDir: "/repo", Profile: "spike", Session: "s1", Generation: "gen-1"}
	af, err := BuildAnswerFile(o, raw, f.gate, f.pin, "wss://relay.example", ns, signer, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAnswerFile(af, ts, f.pin); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	// The envelope JSON decodes with operatorauth's own strict decoder.
	envRaw, _ := json.Marshal(af.Envelope)
	if _, err := operatorauth.DecodeAuthorizationEnvelope(envRaw); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	tamper := map[string]func(*AnswerFile){
		"raw event swapped for a seat-signed one": func(a *AnswerFile) {
			a.Evidence.RawEvent = string(answer(t, f.seat, testNow.Add(time.Minute), approveText(f.gate)))
		},
		"payload target changed":               func(a *AnswerFile) { a.Envelope.Payload.Target = "sha256:" + strings.Repeat("9", 64) },
		"answer re-pointed at another gate":    func(a *AnswerFile) { a.Gate = f.other },
		"evidence re-pointed at another owner": func(a *AnswerFile) { a.Evidence.OwnerPubKey = f.seat.PublicHex() },
	}
	for name, mut := range tamper {
		c := af
		c.Envelope.Payload.Evidence = append([]operatorauth.AuthorizationEvidence(nil), af.Envelope.Payload.Evidence...)
		mut(&c)
		if err := VerifyAnswerFile(c, ts, f.pin); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	// A forged envelope over seat-signed evidence, issued by a key the core
	// does not trust, fails at operatorauth.
	dir2 := realTempDir(t)
	key2 := filepath.Join(dir2, "rogue.pem")
	if _, err := WriteIssuerKey(key2, filepath.Join(dir2, "rogue.json")); err != nil {
		t.Fatal(err)
	}
	rogue, _ := operatorauth.LoadEd25519Signer(key2)
	forged, err := BuildAnswerFile(o, raw, f.gate, f.pin, "", ns, rogue, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAnswerFile(forged, ts, f.pin); err == nil || !strings.Contains(err.Error(), "operatorauth") {
		t.Fatalf("untrusted issuer: %v", err)
	}
	// A deny never becomes an envelope.
	deny, _ := Verify(answer(t, f.owner, testNow.Add(time.Minute), "deny "+f.gate.GateID+" merge "+prefixOf(f.gate)), f.gate, f.pin, testNow.Add(2*time.Minute))
	if _, err := BuildAnswerFile(deny, raw, f.gate, f.pin, "", ns, signer, testNow.Add(2*time.Minute)); err == nil {
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
