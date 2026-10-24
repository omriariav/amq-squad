package gatestore

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

func testIssuer(t *testing.T) (operatorauth.AuthorizationSigner, operatorauth.AuthorizationTrustStore) {
	t.Helper()
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	pub, priv, _ := ed25519.GenerateKey(nil)
	der, _ := x509.MarshalPKCS8PrivateKey(priv)
	key, trustPath := filepath.Join(dir, "k.pem"), filepath.Join(dir, "t.json")
	_ = os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600)
	raw, _ := json.Marshal(operatorauth.AuthorizationTrustStore{SchemaVersion: 1, Keys: []operatorauth.AuthorizationTrustKey{{KeyID: operatorauth.KeyIDForEd25519(pub), Algorithm: "ed25519", PublicKey: base64.StdEncoding.EncodeToString(pub), Status: "trusted"}}})
	_ = os.WriteFile(trustPath, raw, 0o600)
	signer, err := operatorauth.LoadEd25519Signer(key)
	if err != nil {
		t.Fatal(err)
	}
	trust, err := operatorauth.LoadAuthorizationTrustStore(trustPath)
	if err != nil {
		t.Fatal(err)
	}
	return signer, trust
}

// approve commits a signed approval for a gate (the shape the core writes).
func approve(t *testing.T, s Store, rec Record, signer operatorauth.AuthorizationSigner, event string) {
	t.Helper()
	ts := testNow.Format(time.RFC3339Nano)
	env, err := operatorauth.NewAuthorizationEnvelope(operatorauth.AuthorizationPayload{
		Namespace: operatorauth.NamespaceBinding{ProjectDir: "/p", Profile: "default", Session: "s1", NamespaceID: "default/s1", Generation: "g"},
		Gate:      "gate/" + rec.GateID, Thread: "gate/" + rec.GateID, GateKind: rec.GateKind, Action: rec.Class, Target: rec.Digest,
		QuestionMessageID: "nostr:q", AnswerMessageID: "nostr:" + event, QuestionCreatedAt: ts, AnswerCreatedAt: ts, VerifiedAt: ts,
		Actor:    operatorauth.AuthorizationActor{Role: "operator", Handle: "owner", Source: "human"},
		Issuer:   operatorauth.AuthorizationIssuer{Binary: "amq-squad", Version: "test"},
		Evidence: []operatorauth.AuthorizationEvidence{{Kind: "gate.text", Identity: "gate/" + rec.GateID, SHA256: rec.TextSHA256}},
	}, signer)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Commit(AnswerFile{Schema: AnswerSchema, Scheme: "bip340", Decision: operatorauth.DecisionApproved, Envelope: env, Evidence: Evidence{EventID: event}, Gate: rec}); err != nil {
		t.Fatal(err)
	}
}

// The growth gate the scheduler and seat-exec call (R30): inside the grant a
// seat starts; past it only an accepted spawn gate for exactly that target
// opens it; an insecure owner grants file keeps every request gated.
func TestGrowthGate(t *testing.T) {
	s := testStore(t)
	signer, trust := testIssuer(t)
	ident := Identity{OwnerPubKey: testIdent.OwnerPubKey, IssuerKeyID: signer.KeyID()}
	r := grants.Resolved{Grants: grants.Defaults()}
	r.MaxSeats = 2
	root := "/r/s1"
	if d := s.SeatRequest(r, root, 2, trust); !d.Allowed || d.Reason != "within_grant" {
		t.Fatalf("within: %+v", d)
	}
	d := s.SeatRequest(r, root, 3, trust)
	if d.Allowed || d.Reason != "gate_grow" || d.Target != gatetarget.Grow(root, 3) || d.Digest != gatetarget.Digest("spawn", d.Target) {
		t.Fatalf("past the cap: %+v", d)
	}
	rec, _, err := s.Raise(Spec{GateID: "grow-3", Action: "spawn", Target: d.Target, RaisedBy: "lead"}, ident, testNow, fileSender{s.Root})
	if err != nil {
		t.Fatal(err)
	}
	if d := s.SeatRequest(r, root, 3, trust); d.Allowed {
		t.Fatalf("an unanswered gate opened growth: %+v", d)
	}
	approve(t, s, rec, signer, strings.Repeat("0c", 32))
	if d := s.SeatRequest(r, root, 3, trust); !d.Allowed || d.Reason != "gate_accepted" || d.GateID != "grow-3" {
		t.Fatalf("accepted spawn gate: %+v", d)
	}
	if d := s.SeatRequest(r, root, 4, trust); d.Allowed {
		t.Fatalf("seat 4 opened by the seat-3 gate: %+v", d)
	}
	// another trust store does not accept the envelope
	_, otherTrust := testIssuer(t)
	if d := s.SeatRequest(r, root, 3, otherTrust); d.Allowed {
		t.Fatalf("an untrusted issuer opened growth: %+v", d)
	}
	// insecure owner grants: every request is gated and nothing opens it
	insecure := grants.Resolved{Grants: grants.Defaults(), Insecure: true, ReasonCode: grants.ReasonInsecure}
	if d := s.SeatRequest(insecure, root, 3, trust); d.Allowed || d.Reason != grants.ReasonInsecure {
		t.Fatalf("insecure: %+v", d)
	}
	// widening past another grant limit (2b-2)
	if d := s.LimitRequest(r, root, "files", 12, true, trust); !d.Allowed {
		t.Fatalf("widening inside the grant: %+v", d)
	}
	if d := s.LimitRequest(r, root, "files", 40, false, trust); d.Allowed || d.Target != "grow /r/s1 files=40" {
		t.Fatalf("widening past the grant: %+v", d)
	}
}
