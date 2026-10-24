package operatorauth

import (
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

type testSigner struct {
	id  string
	key ed25519.PrivateKey
}

func (s testSigner) KeyID() string                 { return s.id }
func (s testSigner) Sign(b []byte) ([]byte, error) { return ed25519.Sign(s.key, b), nil }
func (s testSigner) Destroy()                      {}

func newTestSignerAndStore(t *testing.T) (testSigner, AuthorizationTrustStore) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	id := KeyIDForEd25519(pub)
	store := AuthorizationTrustStore{SchemaVersion: 1, Keys: []AuthorizationTrustKey{{KeyID: id, Algorithm: "ed25519", PublicKey: base64.StdEncoding.EncodeToString(pub), Status: "trusted"}},
		keys: map[string]trustedAuthorizationKey{id: {public: pub, status: "trusted"}}}
	return testSigner{id, priv}, store
}

func gatePayload(action, kind string) AuthorizationPayload {
	ts := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	return AuthorizationPayload{
		Namespace: NamespaceBinding{ProjectDir: "/p", Profile: "default", Session: "s1", NamespaceID: "default/s1", Generation: "g1"},
		Gate:      "gate/g1", Thread: "gate/g1", GateKind: kind, Action: action, Target: "sha256:" + strings.Repeat("ab", 32),
		QuestionMessageID: "nostr:q", AnswerMessageID: "nostr:a", QuestionCreatedAt: ts, AnswerCreatedAt: ts, VerifiedAt: ts,
		Actor:    AuthorizationActor{Role: "operator", Handle: "owner", Source: "human"},
		Issuer:   AuthorizationIssuer{Binary: "amq-squad", Version: "test"},
		Evidence: []AuthorizationEvidence{{Kind: "gate.text", Identity: "gate/g1", SHA256: "sha256:" + "0000000000000000000000000000000000000000000000000000000000000000"}},
	}
}

func TestCredentialChangeIsATypedHumanOnlyAction(t *testing.T) {
	c, err := ValidateGateAction(GateCredential, "credential_change")
	if err != nil || !c.HumanOnly || c.SelfEligible {
		t.Fatalf("credential_change = %+v, %v", c, err)
	}
	if !HumanOnlyGateKind(GateCredential) {
		t.Fatal("credential gate kind is self-eligible")
	}
	signer, store := newTestSignerAndStore(t)
	env, err := NewAuthorizationEnvelope(gatePayload("credential_change", GateCredential), signer)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAuthorizationEnvelope(env, store); err != nil {
		t.Fatalf("credential_change approval does not verify: %v", err)
	}
}

func TestDenialEnvelopeNeverVerifiesAsApproval(t *testing.T) {
	signer, store := newTestSignerAndStore(t)
	deny, err := NewDenialEnvelope(gatePayload("tag", GateTag), signer)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDenialEnvelope(deny, store); err != nil {
		t.Fatalf("deny does not verify: %v", err)
	}
	if deny.Payload.Decision != DecisionDenied || deny.Canonicalization != DenialCanonicalization {
		t.Fatalf("deny = %+v", deny)
	}
	if err := ValidateAuthorizationPayload(deny.Payload); err == nil {
		t.Fatal("deny payload validates as an approval")
	}
	if err := VerifyAuthorizationEnvelope(deny, store); err == nil {
		t.Fatal("deny verifies as an approval")
	}
	// relabelled to the approval domain and decision, the signature fails
	relabel := deny
	relabel.Canonicalization = AuthorizationCanonicalization
	relabel.Payload.Decision = DecisionApproved
	relabel.Payload.AuthorizationID = authorizationIdentity(relabel.Payload, relabel.Signature.KeyID)
	if err := VerifyAuthorizationEnvelope(relabel, store); err == nil {
		t.Fatal("relabelled deny verifies as an approval")
	}
	approve, err := NewAuthorizationEnvelope(gatePayload("tag", GateTag), signer)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyDenialEnvelope(approve, store); err == nil {
		t.Fatal("approval verifies as a deny")
	}
	approve.Canonicalization = DenialCanonicalization
	approve.Payload.Decision = DecisionDenied
	approve.Payload.AuthorizationID = authorizationIdentity(approve.Payload, approve.Signature.KeyID)
	if err := VerifyDenialEnvelope(approve, store); err == nil {
		t.Fatal("relabelled approval verifies as a deny")
	}
	tampered := deny
	tampered.Payload.Target = "sha256:" + "1111111111111111111111111111111111111111111111111111111111111111"
	if err := VerifyDenialEnvelope(tampered, store); err == nil {
		t.Fatal("tampered deny verifies")
	}
}
