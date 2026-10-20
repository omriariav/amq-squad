package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

func threadedApprove(t *testing.T, f fixture) (Outcome, []byte) {
	t.Helper()
	raw := answer(t, f.owner, testNow.Add(time.Minute), approveText(f.gate), f.reply())
	o, err := Verify(raw, f.gate, f.question, f.tpin, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return o, raw
}

// mustReject asserts err is non-nil and contains want.
func mustReject(t *testing.T, name string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: accepted", name)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Fatalf("%s: rejected for the wrong reason: %v (want %q)", name, err, want)
	}
}

// TestEnvelopeIdentity: the envelope records the issuer identity (signature
// key id, issuer binary) and the owner npub (note, evidence). The verifier
// checks both against the STORED gate record, and checks that the answer id
// the envelope names is the event the evidence bytes are. Every case is
// signed by a key the trust store trusts, so operatorauth alone would accept
// it.
func TestEnvelopeIdentity(t *testing.T) {
	f := newFixture(t)
	o, raw := threadedApprove(t, f)
	other := answer(t, f.owner, testNow.Add(90*time.Second), approveText(f.gate), f.reply())
	oe, _ := ParseEvent(other)

	// Answer id of another (also valid) owner event: payload, evidence
	// identity and wrapper all say "other", the bytes are the first event.
	mis := signedEnvelope(t, f, o, raw, f.signer, func(p *operatorauth.AuthorizationPayload) {
		p.AnswerMessageID = "nostr:" + oe.ID
		for i := range p.Evidence {
			if p.Evidence[i].Kind == evidenceKindEvent {
				p.Evidence[i].Identity = "nostr:" + oe.ID
			}
		}
	})
	mis.Evidence.EventID = oe.ID
	if err := operatorauth.VerifyAuthorizationEnvelope(mis.Envelope, f.trust); err != nil {
		t.Fatalf("setup: %v", err)
	}
	mustReject(t, "mislabelled answer id", VerifyAnswerFile(mis, f.trust, f.tpin, f.trusted(), false), "evidence bytes are event")

	seatNpub, _ := EncodeNpub(f.seat.PublicHex())
	note := signedEnvelope(t, f, o, raw, f.signer, func(p *operatorauth.AuthorizationPayload) {
		p.Note = "scheme bip340; owner " + seatNpub + "; class merge"
	})
	mustReject(t, "note names another owner npub", VerifyAnswerFile(note, f.trust, f.tpin, f.trusted(), false), "owner npub")
	bin := signedEnvelope(t, f, o, raw, f.signer, func(p *operatorauth.AuthorizationPayload) { p.Issuer.Binary = "something-else" })
	mustReject(t, "other issuer binary", VerifyAnswerFile(bin, f.trust, f.tpin, f.trusted(), false), "issuer binary")
	w := signedEnvelope(t, f, o, raw, f.signer, func(*operatorauth.AuthorizationPayload) {})
	w.Evidence.OwnerNpub = seatNpub
	mustReject(t, "wrapper owner npub swapped", VerifyAnswerFile(w, f.trust, f.tpin, f.trusted(), false), "owner other than")
	mustReject(t, "pin other than the record's owner", VerifyAnswerFile(signedEnvelope(t, f, o, raw, f.signer, func(*operatorauth.AuthorizationPayload) {}), f.trust,
		Pin{Owner: f.seat.PublicHex(), Channel: testChannel, RequireThread: true, Body: f.body.PublicHex()}, f.trusted(), false), "owner other than")
}

// TestIssuerBindingFromStoredRecord is the round 3 regression. A second
// key that the trust store also trusts signs the envelope. In the
// bypass, the attacker also rewrites the unsigned wrapper's issuer id to
// the second key. Neither may authorize the gate: the expected issuer comes
// from the stored record only, and a wrapper that differs from it is
// refused.
func TestIssuerBindingFromStoredRecord(t *testing.T) {
	f := newFixture(t)
	o, raw := threadedApprove(t, f)
	dir := realTempDir(t)
	k2 := filepath.Join(dir, "second.pem")
	if _, err := WriteIssuerKey(k2, filepath.Join(dir, "second.json")); err != nil {
		t.Fatal(err)
	}
	second, _ := operatorauth.LoadEd25519Signer(k2)
	both := trustBoth(t, filepath.Join(f.root, "trust.json"), filepath.Join(dir, "second.json"))
	stored := f.trusted()
	if stored.Gate.IssuerKeyID != f.signer.KeyID() || second.KeyID() == f.signer.KeyID() {
		t.Fatal("setup: issuers")
	}

	// (a) Second key, wrapper untouched: key id is not the stored issuer.
	byOther := signedEnvelope(t, f, o, raw, second, func(*operatorauth.AuthorizationPayload) {})
	if err := operatorauth.VerifyAuthorizationEnvelope(byOther.Envelope, both); err != nil {
		t.Fatalf("setup: the second key must be trusted: %v", err)
	}
	mustReject(t, "second key", VerifyAnswerFile(byOther, both, f.tpin, stored, false), "stored gate record's issuer")

	// (b) The bypass: second key AND the wrapper's issuer id substituted to
	// match it. Every other field is intact; the stored record is unchanged.
	bypass := signedEnvelope(t, f, o, raw, second, func(*operatorauth.AuthorizationPayload) {})
	bypass.Gate.IssuerKeyID = second.KeyID()
	mustReject(t, "second key + substituted wrapper issuer", VerifyAnswerFile(bypass, both, f.tpin, stored, false), "differs from the stored record")
	// The pre-fix bypass passed every check except this one: with the
	// wrapper's issuer as the source, case (b) verified. The stored record
	// is the only source now, and the wrapper never writes back to it.
	if f.trusted().Gate.IssuerKeyID != f.signer.KeyID() {
		t.Fatal("the stored record's issuer changed")
	}

	// (c) The same through the CLI, which loads the record from --root.
	path := filepath.Join(dir, "bypass.json")
	blob, _ := json.Marshal(bypass)
	_ = os.WriteFile(path, blob, 0o600)
	bothPath := filepath.Join(dir, "both.json")
	var out bytes.Buffer
	if rc := run([]string{"envelope-verify", "--answer", path, "--store", bothPath, "--root", f.root,
		"--owner", f.pin.Owner, "--channel", testChannel, "--body", f.body.PublicHex()}, &out, &out); rc != 1 || !strings.Contains(out.String(), "differs from the stored record") {
		t.Fatalf("CLI bypass: rc %d %s", rc, out.String())
	}

	// (d) The genuine issuer still passes, from the store, with the same
	// two-key trust store: the rejection above is the binding, not the store.
	good := signedEnvelope(t, f, o, raw, f.signer, func(*operatorauth.AuthorizationPayload) {})
	if err := VerifyAnswerFile(good, both, f.tpin, stored, false); err != nil {
		t.Fatalf("genuine issuer: %v", err)
	}
	// And BuildAnswerFile refuses to sign with a key the record does not name.
	if _, err := BuildAnswerFile(o, raw, f.gate, f.question, f.tpin, "", Namespace{ProjectDir: "/repo", Profile: "spike", Session: "s1", Generation: "g"}, second, testNow.Add(2*time.Minute)); err == nil {
		t.Fatal("BuildAnswerFile signed with an issuer the record does not name")
	}
}

// trustBoth merges two single-key trust stores into <dir of b>/both.json.
func trustBoth(t *testing.T, a, b string) operatorauth.AuthorizationTrustStore {
	t.Helper()
	var sa, sb operatorauth.AuthorizationTrustStore
	ra, _ := os.ReadFile(a)
	rb, _ := os.ReadFile(b)
	if json.Unmarshal(ra, &sa) != nil || json.Unmarshal(rb, &sb) != nil {
		t.Fatal("trust stores unreadable")
	}
	sa.Keys = append(sa.Keys, sb.Keys...)
	for _, k := range sa.Keys {
		if _, err := base64.StdEncoding.DecodeString(k.PublicKey); err != nil {
			t.Fatal(err)
		}
	}
	out, _ := json.Marshal(sa)
	p := filepath.Join(filepath.Dir(b), "both.json")
	_ = os.WriteFile(p, out, 0o600)
	ts, err := operatorauth.LoadAuthorizationTrustStore(p)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// TestEnvelopeUnboundFailsClosed: thread binding fails closed, judged from
// the STORED question, with the plain (non-strict) pin, which has no thread
// flag and no body pin.
func TestEnvelopeUnboundFailsClosed(t *testing.T) {
	f := newFixture(t)
	o, raw := threadedApprove(t, f)
	bound := signedEnvelope(t, f, o, raw, f.signer, func(*operatorauth.AuthorizationPayload) {})
	stored := f.trusted()
	if stored.Question == nil {
		t.Fatal("setup: the first gate has a stored question")
	}

	// Absent question: the wrapper drops it while the store has one.
	absent := bound
	absent.Question = nil
	for _, allow := range []bool{false, true} {
		mustReject(t, "absent question", VerifyAnswerFile(absent, f.trust, f.pin, stored, allow), "differs from the stored question")
	}

	// Issuer-signed downgrade of a bound gate: a trusted issuer signs the
	// threaded answer in the text-digest form, with no question. The store
	// has the question, so it is refused, even with allowUnbound.
	ns := Namespace{ProjectDir: "/repo", Profile: "spike", Session: "s1", Generation: "gen-1"}
	p, err := answerPayload(o, raw, f.gate, nil, f.pin, ns, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	down, err := signAnswerFile(p, o, raw, f.gate, nil, f.pin, "", f.signer)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(down.Envelope.Payload.QuestionMessageID, "gate-text:") {
		t.Fatal("setup: not a text-digest downgrade")
	}
	for _, allow := range []bool{false, true} {
		mustReject(t, "issuer-signed downgrade of a bound gate", VerifyAnswerFile(down, f.trust, f.pin, stored, allow), "differs from the stored question")
	}

	// A gate that genuinely has no stored question (the second gate):
	// refused by default, accepted only with the explicit flag.
	og := f.other
	ounbound := answer(t, f.owner, testNow.Add(time.Minute), approveText(og))
	oo, err := Verify(ounbound, og, nil, f.pin, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	po, err := answerPayload(oo, ounbound, og, nil, f.pin, ns, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	unb, err := signAnswerFile(po, oo, ounbound, og, nil, f.pin, "", f.signer)
	if err != nil {
		t.Fatal(err)
	}
	ostored, err := LoadTrustedGate(f.root, og.GateID)
	if err != nil || ostored.Question != nil {
		t.Fatalf("setup: second gate has no stored question: %v", err)
	}
	if err := VerifyAnswerFile(unb, f.trust, f.pin, ostored, false); !errors.Is(err, ErrUnbound) {
		t.Fatalf("unbound gate without the flag: %v", err)
	}
	if err := VerifyAnswerFile(unb, f.trust, f.pin, ostored, true); err != nil {
		t.Fatalf("unbound gate with the flag: %v", err)
	}
	// Presented against the other (bound) gate's stored state: refused.
	mustReject(t, "envelope for one gate against another", VerifyAnswerFile(unb, f.trust, f.pin, stored, true), "not the trusted gate")

	// The same through the CLI, which loads the stored state from --root.
	dir := realTempDir(t)
	write := func(name string, v any) string {
		pth := filepath.Join(dir, name)
		blob, _ := json.Marshal(v)
		_ = os.WriteFile(pth, blob, 0o600)
		return pth
	}
	cli := func(path string, extra ...string) (int, string) {
		var out bytes.Buffer
		args := append([]string{"envelope-verify", "--answer", path, "--store", filepath.Join(f.root, "trust.json"), "--root", f.root,
			"--owner", f.pin.Owner, "--channel", testChannel}, extra...)
		rc := run(args, &out, &out)
		return rc, out.String()
	}
	unbPath, downPath, boundPath := write("unb.json", unb), write("down.json", down), write("bound.json", bound)
	if rc, out := cli(unbPath); rc != 1 || !strings.Contains(out, "not bound") {
		t.Fatalf("CLI unbound without flag: rc %d %s", rc, out)
	}
	if rc, out := cli(unbPath, "--allow-unbound"); rc != 0 {
		t.Fatalf("CLI unbound with flag: rc %d %s", rc, out)
	}
	if rc, out := cli(downPath, "--allow-unbound"); rc != 1 || !strings.Contains(out, "differs from the stored question") {
		t.Fatalf("CLI downgrade with flag: rc %d %s", rc, out)
	}
	if rc, out := cli(boundPath, "--body", f.body.PublicHex()); rc != 0 {
		t.Fatalf("CLI bound: rc %d %s", rc, out)
	}
	if rc, _ := cli(boundPath[:len(boundPath)-5] + "-missing.json"); rc == 0 {
		t.Fatal("CLI accepted a missing answer file")
	}
}

// TestRenderNeedsIdentity: the CLI refuses to render without the owner and
// the issuer key, and Render refuses malformed identities.
func TestRenderNeedsIdentity(t *testing.T) {
	f := newFixture(t)
	spec := filepath.Join(f.root, "spec.json")
	blob, _ := json.Marshal(testSpec("g-render-01"))
	_ = os.WriteFile(spec, blob, 0o600)
	var out bytes.Buffer
	if rc := run([]string{"render", "--root", f.root, "--spec", spec, "--owner", f.pin.Owner}, &out, &out); rc != 2 {
		t.Fatalf("render without issuer: rc %d", rc)
	}
	if rc := run([]string{"render", "--root", f.root, "--spec", spec, "--issuer-key", f.issuerKey}, &out, &out); rc != 2 {
		t.Fatalf("render without owner: rc %d", rc)
	}
	if _, err := Render(testSpec("g-render-02"), testNow, GateIdentity{OwnerPubKey: f.pin.Owner, IssuerKeyID: "ed25519-sha256:xyz"}); err == nil {
		t.Fatal("malformed issuer key id accepted")
	}
	out.Reset()
	if rc := run([]string{"render", "--root", f.root, "--spec", spec, "--owner", f.pin.Owner, "--issuer-key", f.issuerKey}, &out, &out); rc != 0 {
		t.Fatalf("render: rc %d %s", rc, out.String())
	}
	g, err := GateStoreFor(f.root).Load("g-render-01")
	if err != nil || g.OwnerPubKey != f.pin.Owner || g.IssuerKeyID != f.signer.KeyID() {
		t.Fatalf("stored identity: %+v %v", g, err)
	}
}
