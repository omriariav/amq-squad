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

// TestEnvelopeIdentity: the envelope records the issuer identity (signature
// key id, issuer binary) and the owner npub (note, evidence). The verifier
// checks both against the gate record, and checks that the answer id it
// names is the event the evidence bytes are. Each case is signed by a
// TRUSTED issuer, so operatorauth alone would accept it.
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
	if err := VerifyAnswerFile(mis, f.trust, f.tpin, false); err == nil || !strings.Contains(err.Error(), "evidence bytes are event") {
		t.Fatalf("mislabelled answer id: %v", err)
	}

	// The note names another owner npub.
	seatNpub, _ := EncodeNpub(f.seat.PublicHex())
	note := signedEnvelope(t, f, o, raw, f.signer, func(p *operatorauth.AuthorizationPayload) {
		p.Note = "scheme bip340; owner " + seatNpub + "; class merge"
	})
	if err := VerifyAnswerFile(note, f.trust, f.tpin, false); err == nil {
		t.Fatal("note with another owner npub accepted")
	}
	// Another issuer binary.
	bin := signedEnvelope(t, f, o, raw, f.signer, func(p *operatorauth.AuthorizationPayload) { p.Issuer.Binary = "something-else" })
	if err := VerifyAnswerFile(bin, f.trust, f.tpin, false); err == nil {
		t.Fatal("other issuer binary accepted")
	}
	// Wrapper owner npub swapped.
	w := signedEnvelope(t, f, o, raw, f.signer, func(*operatorauth.AuthorizationPayload) {})
	w.Evidence.OwnerNpub = seatNpub
	if err := VerifyAnswerFile(w, f.trust, f.tpin, false); err == nil {
		t.Fatal("wrapper owner npub swap accepted")
	}
	// A pin that is not the gate record's owner.
	if err := VerifyAnswerFile(signedEnvelope(t, f, o, raw, f.signer, func(*operatorauth.AuthorizationPayload) {}), f.trust,
		Pin{Owner: f.seat.PublicHex(), Channel: testChannel, RequireThread: true, Body: f.body.PublicHex()}, false); err == nil {
		t.Fatal("pin other than the record's owner accepted")
	}

	// A second key that the trust store also trusts, but that the gate record
	// does not name, cannot authorize this gate.
	dir := realTempDir(t)
	k2 := filepath.Join(dir, "second.pem")
	if _, err := WriteIssuerKey(k2, filepath.Join(dir, "second.json")); err != nil {
		t.Fatal(err)
	}
	second, _ := operatorauth.LoadEd25519Signer(k2)
	both := trustBoth(t, filepath.Join(f.root, "trust.json"), filepath.Join(dir, "second.json"))
	byOther := signedEnvelope(t, f, o, raw, second, func(*operatorauth.AuthorizationPayload) {})
	if err := operatorauth.VerifyAuthorizationEnvelope(byOther.Envelope, both); err != nil {
		t.Fatalf("setup: second key should be trusted: %v", err)
	}
	if err := VerifyAnswerFile(byOther, both, f.tpin, false); err == nil || !strings.Contains(err.Error(), "gate record's issuer") {
		t.Fatalf("second trusted issuer: %v", err)
	}
	if _, err := BuildAnswerFile(o, raw, f.gate, f.question, f.tpin, "", Namespace{ProjectDir: "/repo", Profile: "spike", Session: "s1", Generation: "g"}, second, testNow.Add(2*time.Minute)); err == nil {
		t.Fatal("BuildAnswerFile signed with an issuer the record does not name")
	}
}

// trustBoth merges two single-key trust stores into one file.
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

// TestEnvelopeUnboundFailsClosed: an answer file with no question is
// refused unless explicitly allowed, including when a trusted issuer
// downgrades a threaded answer to the text-digest form. Both run with the
// plain (non-strict) pin, which carries no thread binding and no body pin.
func TestEnvelopeUnboundFailsClosed(t *testing.T) {
	f := newFixture(t)
	o, raw := threadedApprove(t, f)
	bound := signedEnvelope(t, f, o, raw, f.signer, func(*operatorauth.AuthorizationPayload) {})

	// Absent question: the wrapper simply drops it.
	absent := bound
	absent.Question = nil
	if err := VerifyAnswerFile(absent, f.trust, f.pin, false); !errors.Is(err, ErrUnbound) {
		t.Fatalf("absent question: %v", err)
	}
	// Even when allowed, the signed payload still names the question, so it
	// cannot pass.
	if err := VerifyAnswerFile(absent, f.trust, f.pin, true); err == nil {
		t.Fatal("dropped question accepted with allowUnbound: the payload names one")
	}

	// Issuer-signed downgrade: a trusted issuer signs the threaded answer as
	// unbound (question id = text digest, no question in the wrapper).
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
	if err := VerifyAnswerFile(down, f.trust, f.pin, false); !errors.Is(err, ErrUnbound) {
		t.Fatalf("issuer-signed downgrade: %v", err)
	}
	// With the explicit flag it is accepted: that is what the flag means.
	if err := VerifyAnswerFile(down, f.trust, f.pin, true); err != nil {
		t.Fatalf("explicitly allowed unbound answer: %v", err)
	}

	// The same through the CLI: refused without --allow-unbound.
	dir := realTempDir(t)
	path := filepath.Join(dir, "down.json")
	blob, _ := json.Marshal(down)
	_ = os.WriteFile(path, blob, 0o600)
	var out bytes.Buffer
	base := []string{"envelope-verify", "--answer", path, "--store", filepath.Join(f.root, "trust.json"), "--owner", f.pin.Owner, "--channel", testChannel}
	if rc := run(base, &out, &out); rc != 1 || !strings.Contains(out.String(), "not bound") {
		t.Fatalf("CLI without --allow-unbound: rc %d %s", rc, out.String())
	}
	out.Reset()
	if rc := run(append(base, "--allow-unbound"), &out, &out); rc != 0 {
		t.Fatalf("CLI with --allow-unbound: rc %d %s", rc, out.String())
	}
	// And the bound one passes through the CLI with the body pin.
	bpath := filepath.Join(dir, "bound.json")
	blob, _ = json.Marshal(bound)
	_ = os.WriteFile(bpath, blob, 0o600)
	out.Reset()
	if rc := run([]string{"envelope-verify", "--answer", bpath, "--store", filepath.Join(f.root, "trust.json"), "--owner", f.pin.Owner, "--channel", testChannel, "--body", f.body.PublicHex()}, &out, &out); rc != 0 {
		t.Fatalf("CLI bound: rc %d %s", rc, out.String())
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
