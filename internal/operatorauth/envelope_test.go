package operatorauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func authorizationCryptoFixture(t *testing.T) (AuthorizationSigner, string, ed25519.PublicKey) {
	t.Helper()
	dir, err := os.MkdirTemp("/private/tmp", "amq-authz-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(dir, "signer.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := LoadEd25519Signer(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return signer, dir, public
}

func testAuthorizationPayload(t *testing.T) AuthorizationPayload {
	t.Helper()
	evidence := []AuthorizationEvidence{{Kind: "approval_receipt", Identity: "/repo/.amq-squad/evidence/receipt.json", SHA256: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	return AuthorizationPayload{
		Namespace: NamespaceBinding{ProjectDir: "/repo", Profile: "default", Session: "release", NamespaceID: "default/release", Generation: "generation-1"},
		Gate:      "gate/release", Thread: "gate/release", GateKind: GateRelease, Action: "github_release", Target: "publish v2.21.0", Note: "accepted candidate",
		QuestionMessageID: "question-1", AnswerMessageID: "answer-1",
		QuestionCreatedAt: time.Date(2026, 7, 15, 1, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		AnswerCreatedAt:   time.Date(2026, 7, 15, 1, 1, 0, 0, time.UTC).Format(time.RFC3339Nano),
		Actor:             AuthorizationActor{Role: "operator", Handle: "user", Source: "human"},
		Issuer:            AuthorizationIssuer{Binary: "amq-squad", Version: "v2.21.0-test", BuildCommit: "deadbeef"},
		Evidence:          evidence, VerifiedAt: time.Date(2026, 7, 15, 1, 2, 0, 0, time.UTC).Format(time.RFC3339Nano),
	}
}

func writeAuthorizationTrustFixture(t *testing.T, dir string, public ed25519.PublicKey, status string) string {
	t.Helper()
	store := AuthorizationTrustStore{SchemaVersion: AuthorizationTrustStoreSchemaVersion, Keys: []AuthorizationTrustKey{{KeyID: KeyIDForEd25519(public), Algorithm: authorizationSignatureAlgorithm, PublicKey: base64.StdEncoding.EncodeToString(public), Status: status}}}
	raw, err := json.Marshal(store)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "trust.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSignedAuthorizationEnvelopeRoundTripAndTamper(t *testing.T) {
	signer, dir, public := authorizationCryptoFixture(t)
	envelope, err := NewAuthorizationEnvelope(testAuthorizationPayload(t), signer)
	if err != nil {
		t.Fatal(err)
	}
	store, err := LoadAuthorizationTrustStore(writeAuthorizationTrustFixture(t, dir, public, "trusted"))
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyAuthorizationEnvelope(envelope, store); err != nil {
		t.Fatal(err)
	}
	canonical1, err := CanonicalAuthorizationPayload(envelope.Payload)
	if err != nil {
		t.Fatal(err)
	}
	canonical2, err := CanonicalAuthorizationPayload(envelope.Payload)
	if err != nil || string(canonical1) != string(canonical2) {
		t.Fatalf("canonical payload is not deterministic: %v", err)
	}
	mutated := envelope
	mutated.Payload.Target = "publish v2.21.1"
	if err := VerifyAuthorizationEnvelope(mutated, store); err == nil {
		t.Fatal("mutated signed payload verified")
	}
}

func TestAuthorizationTrustStoreRevocationAndSignerPathPolicy(t *testing.T) {
	_, dir, public := authorizationCryptoFixture(t)
	store, err := LoadAuthorizationTrustStore(writeAuthorizationTrustFixture(t, dir, public, "revoked"))
	if err == nil || len(store.keys) != 0 {
		t.Fatalf("revoked-only trust store accepted: %+v %v", store, err)
	}
	public2, private2, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(private2)
	weak := filepath.Join(dir, "world-readable.pem")
	if err := os.WriteFile(weak, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEd25519Signer(weak); err == nil {
		t.Fatal("world-readable signing key accepted")
	}
	good := filepath.Join(dir, "good.pem")
	if err := os.WriteFile(good, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.pem")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEd25519Signer(link); err == nil {
		t.Fatal("symlink signing key accepted")
	}
	_ = public2
}

func TestAuthorizationEnvelopeRejectsMalformedAuthorityInvariants(t *testing.T) {
	signer, _, _ := authorizationCryptoFixture(t)
	defer signer.Destroy()
	compound := AuthorizationCompoundEvidence{
		ReleaseID: "release-v1-" + strings.Repeat("a", 64), ParentGate: "gate/release", SeriesID: "series-v1-" + strings.Repeat("b", 64),
		GenerationID: "release-generation-v1-" + strings.Repeat("c", 64), PreparedManifestID: "release-prepared-v1-" + strings.Repeat("d", 64),
		ActiveManifestID: "release-active-v1-" + strings.Repeat("e", 64), Role: ReleaseChildTag,
		ManifestSHA256: "sha256:" + strings.Repeat("f", 64),
	}
	for _, tc := range []struct {
		name   string
		mutate func(*AuthorizationPayload)
	}{
		{name: "self authority", mutate: func(p *AuthorizationPayload) { p.Actor.Source, p.Actor.SelfApproved = "self_operator", true }},
		{name: "non operator role", mutate: func(p *AuthorizationPayload) { p.Actor.Role = "cto" }},
		{name: "namespace id drift", mutate: func(p *AuthorizationPayload) { p.Namespace.NamespaceID = "other/release" }},
		{name: "relative project", mutate: func(p *AuthorizationPayload) { p.Namespace.ProjectDir = "repo" }},
		{name: "empty evidence", mutate: func(p *AuthorizationPayload) { p.Evidence = nil }},
		{name: "partial compound", mutate: func(p *AuthorizationPayload) {
			p.Compound = AuthorizationCompoundEvidence{ReleaseID: compound.ReleaseID}
		}},
		{name: "partial policy", mutate: func(p *AuthorizationPayload) { p.Policy = AuthorizationPolicyEvidence{Revision: 1} }},
		{name: "partial preflight", mutate: func(p *AuthorizationPayload) { p.Preflight = AuthorizationPreflightEvidence{Kind: "merge"} }},
		{name: "answer before question", mutate: func(p *AuthorizationPayload) {
			p.AnswerCreatedAt = time.Date(2026, 7, 15, 0, 59, 0, 0, time.UTC).Format(time.RFC3339Nano)
		}},
		{name: "verified before answer", mutate: func(p *AuthorizationPayload) {
			p.VerifiedAt = time.Date(2026, 7, 15, 1, 0, 30, 0, time.UTC).Format(time.RFC3339Nano)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := testAuthorizationPayload(t)
			tc.mutate(&payload)
			if _, err := NewAuthorizationEnvelope(payload, signer); err == nil {
				t.Fatal("malformed authorization payload was signed")
			}
		})
	}
	payload := testAuthorizationPayload(t)
	payload.Compound = compound
	if _, err := NewAuthorizationEnvelope(payload, signer); err != nil {
		t.Fatalf("complete compound evidence rejected: %v", err)
	}
}

func TestAuthorizationTrustRotationAndRevocation(t *testing.T) {
	oldSigner, dir, oldPublic := authorizationCryptoFixture(t)
	defer oldSigner.Destroy()
	newPublic, newPrivate, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(newPrivate)
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(dir, "new.pem")
	if err := os.WriteFile(newPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	newSigner, err := LoadEd25519Signer(newPath)
	if err != nil {
		t.Fatal(err)
	}
	defer newSigner.Destroy()
	oldEnvelope, err := NewAuthorizationEnvelope(testAuthorizationPayload(t), oldSigner)
	if err != nil {
		t.Fatal(err)
	}
	newEnvelope, err := NewAuthorizationEnvelope(testAuthorizationPayload(t), newSigner)
	if err != nil {
		t.Fatal(err)
	}
	writeStore := func(oldStatus string) AuthorizationTrustStore {
		store := AuthorizationTrustStore{SchemaVersion: AuthorizationTrustStoreSchemaVersion, Keys: []AuthorizationTrustKey{
			{KeyID: KeyIDForEd25519(oldPublic), Algorithm: authorizationSignatureAlgorithm, PublicKey: base64.StdEncoding.EncodeToString(oldPublic), Status: oldStatus},
			{KeyID: KeyIDForEd25519(newPublic), Algorithm: authorizationSignatureAlgorithm, PublicKey: base64.StdEncoding.EncodeToString(newPublic), Status: "trusted"},
		}}
		raw, _ := json.Marshal(store)
		path := filepath.Join(dir, "rotation-"+oldStatus+".json")
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		loaded, err := LoadAuthorizationTrustStore(path)
		if err != nil {
			t.Fatal(err)
		}
		return loaded
	}
	overlap := writeStore("trusted")
	if err := VerifyAuthorizationEnvelope(oldEnvelope, overlap); err != nil {
		t.Fatal(err)
	}
	if err := VerifyAuthorizationEnvelope(newEnvelope, overlap); err != nil {
		t.Fatal(err)
	}
	rotated := writeStore("revoked")
	if err := VerifyAuthorizationEnvelope(oldEnvelope, rotated); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Fatalf("revoked old envelope error=%v", err)
	}
	if err := VerifyAuthorizationEnvelope(newEnvelope, rotated); err != nil {
		t.Fatalf("new envelope rejected after rotation: %v", err)
	}
}

func TestAuthorizationSignerRejectsAmbiguousPEM(t *testing.T) {
	_, dir, _ := authorizationCryptoFixture(t)
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(private)
	good := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{name: "multiple blocks", raw: append(append([]byte(nil), good...), good...)},
		{name: "encrypted label", raw: pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: der})},
		{name: "trailing prose", raw: append(append([]byte(nil), good...), []byte("not pem\n")...)},
		{name: "raw bytes", raw: der},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, strings.ReplaceAll(tc.name, " ", "-")+".pem")
			if err := os.WriteFile(path, tc.raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadEd25519Signer(path); err == nil {
				t.Fatal("ambiguous signing material accepted")
			}
		})
	}
}
