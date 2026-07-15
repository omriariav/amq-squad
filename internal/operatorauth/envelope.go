package operatorauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const AuthorizationEnvelopeSchemaVersion = 1

type AuthorizationActor struct {
	Role         string `json:"role"`
	Handle       string `json:"handle"`
	Source       string `json:"source"`
	SelfApproved bool   `json:"self_approved"`
}

type AuthorizationIssuer struct {
	Binary      string `json:"binary"`
	Version     string `json:"version"`
	BuildCommit string `json:"build_commit"`
}

type AuthorizationCompoundEvidence struct {
	ReleaseID          string `json:"release_id,omitempty"`
	ParentGate         string `json:"parent_gate,omitempty"`
	SeriesID           string `json:"series_id,omitempty"`
	GenerationID       string `json:"generation_id,omitempty"`
	PreparedManifestID string `json:"prepared_manifest_id,omitempty"`
	ActiveManifestID   string `json:"active_manifest_id,omitempty"`
	Role               string `json:"role,omitempty"`
	ManifestSHA256     string `json:"manifest_sha256,omitempty"`
}

type AuthorizationPolicyEvidence struct {
	Revision int64  `json:"revision,omitempty"`
	SHA256   string `json:"sha256,omitempty"`
}

type AuthorizationPreflightEvidence struct {
	Kind   string `json:"kind,omitempty"`
	Path   string `json:"path,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

type AuthorizationEvidence struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
	SHA256   string `json:"sha256"`
}

type AuthorizationPayload struct {
	SchemaVersion     int                            `json:"schema_version"`
	TaxonomyVersion   int                            `json:"taxonomy_version"`
	AuthorizationID   string                         `json:"authorization_id"`
	Decision          string                         `json:"decision"`
	Namespace         NamespaceBinding               `json:"namespace"`
	Gate              string                         `json:"gate"`
	Thread            string                         `json:"thread"`
	GateKind          string                         `json:"gate_kind"`
	Action            string                         `json:"action"`
	Target            string                         `json:"target"`
	Note              string                         `json:"note,omitempty"`
	QuestionMessageID string                         `json:"question_message_id"`
	AnswerMessageID   string                         `json:"answer_message_id"`
	QuestionCreatedAt string                         `json:"question_created_at"`
	AnswerCreatedAt   string                         `json:"answer_created_at"`
	Actor             AuthorizationActor             `json:"actor"`
	Issuer            AuthorizationIssuer            `json:"issuer"`
	Compound          AuthorizationCompoundEvidence  `json:"compound,omitempty"`
	Policy            AuthorizationPolicyEvidence    `json:"policy,omitempty"`
	Preflight         AuthorizationPreflightEvidence `json:"preflight,omitempty"`
	Evidence          []AuthorizationEvidence        `json:"evidence"`
	EvidenceSHA256    string                         `json:"evidence_sha256"`
	VerifiedAt        string                         `json:"verified_at"`
}

type AuthorizationSignature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"key_id"`
	Value     string `json:"value"`
}

type AuthorizationEnvelope struct {
	SchemaVersion    int                    `json:"schema_version"`
	Canonicalization string                 `json:"canonicalization"`
	Payload          AuthorizationPayload   `json:"payload"`
	PayloadSHA256    string                 `json:"payload_sha256"`
	Signature        AuthorizationSignature `json:"signature"`
}

func ValidateAuthorizationPayload(p AuthorizationPayload) error {
	if p.SchemaVersion != AuthorizationEnvelopeSchemaVersion || p.TaxonomyVersion != ActionTaxonomyVersion || p.Decision != "approved" {
		return fmt.Errorf("authorization payload schema, taxonomy, or decision is invalid")
	}
	for name, value := range map[string]string{
		"authorization_id": p.AuthorizationID, "namespace project": p.Namespace.ProjectDir, "namespace profile": p.Namespace.Profile,
		"namespace session": p.Namespace.Session, "namespace id": p.Namespace.NamespaceID, "namespace generation": p.Namespace.Generation,
		"gate": p.Gate, "thread": p.Thread, "gate kind": p.GateKind, "action": p.Action, "target": p.Target,
		"question message id": p.QuestionMessageID, "answer message id": p.AnswerMessageID,
		"question created at": p.QuestionCreatedAt, "answer created at": p.AnswerCreatedAt,
		"actor role": p.Actor.Role, "actor handle": p.Actor.Handle, "actor source": p.Actor.Source,
		"issuer binary": p.Issuer.Binary, "issuer version": p.Issuer.Version, "evidence sha256": p.EvidenceSHA256, "verified at": p.VerifiedAt,
	} {
		if err := ValidateCanonicalSingleLineField(name, value, true); err != nil {
			return err
		}
	}
	if err := ValidateCanonicalSingleLineField("note", p.Note, false); err != nil {
		return err
	}
	if !filepath.IsAbs(p.Namespace.ProjectDir) || filepath.Clean(p.Namespace.ProjectDir) != p.Namespace.ProjectDir ||
		p.Namespace.NamespaceID != p.Namespace.Profile+"/"+p.Namespace.Session {
		return fmt.Errorf("authorization namespace is not coherent")
	}
	if err := ValidateCanonicalGateThread(p.Gate); err != nil || p.Thread != p.Gate {
		return fmt.Errorf("authorization payload gate/thread is invalid")
	}
	capability, err := ValidateGateAction(p.GateKind, p.Action)
	if err != nil || capability.Action != p.Action || capability.GateKind != p.GateKind {
		return fmt.Errorf("authorization payload action is not canonical")
	}
	questionAt, err := time.Parse(time.RFC3339Nano, p.QuestionCreatedAt)
	if err != nil {
		return fmt.Errorf("authorization question timestamp is invalid")
	}
	answerAt, err := time.Parse(time.RFC3339Nano, p.AnswerCreatedAt)
	if err != nil {
		return fmt.Errorf("authorization answer timestamp is invalid")
	}
	verifiedAt, err := time.Parse(time.RFC3339Nano, p.VerifiedAt)
	if err != nil {
		return fmt.Errorf("authorization verification timestamp is invalid")
	}
	if questionAt.After(answerAt) || answerAt.After(verifiedAt) {
		return fmt.Errorf("authorization timestamps are not chronological")
	}
	if p.Actor.Source != "human" || p.Actor.SelfApproved || p.Actor.Role != "operator" {
		return fmt.Errorf("signed authorization requires human operator authority")
	}
	if err := validateAuthorizationCompoundEvidence(p.Compound); err != nil {
		return err
	}
	if err := validateAuthorizationPolicyEvidence(p.Policy); err != nil {
		return err
	}
	if err := validateAuthorizationPreflightEvidence(p.Preflight); err != nil {
		return err
	}
	if len(p.Evidence) == 0 || len(p.Evidence) > 64 {
		return fmt.Errorf("authorization evidence must contain 1..64 items")
	}
	for i, item := range p.Evidence {
		if err := ValidateCanonicalSingleLineField(fmt.Sprintf("evidence %d kind", i), item.Kind, true); err != nil {
			return err
		}
		if err := ValidateCanonicalSingleLineField(fmt.Sprintf("evidence %d identity", i), item.Identity, true); err != nil {
			return err
		}
		if !validSHA256(item.SHA256) {
			return fmt.Errorf("authorization evidence digest is invalid")
		}
		if i > 0 && (p.Evidence[i-1].Kind > item.Kind || (p.Evidence[i-1].Kind == item.Kind && p.Evidence[i-1].Identity >= item.Identity)) {
			return fmt.Errorf("authorization evidence must be uniquely sorted")
		}
	}
	wantEvidence, err := AuthorizationEvidenceDigest(p.Evidence)
	if err != nil || wantEvidence != p.EvidenceSHA256 {
		return fmt.Errorf("authorization evidence digest mismatch")
	}
	return nil
}

func validateAuthorizationCompoundEvidence(c AuthorizationCompoundEvidence) error {
	values := []string{c.ReleaseID, c.ParentGate, c.SeriesID, c.GenerationID, c.PreparedManifestID, c.ActiveManifestID, c.Role, c.ManifestSHA256}
	present := 0
	for _, value := range values {
		if value != "" {
			present++
		}
	}
	if present == 0 {
		return nil
	}
	if present != len(values) || (c.Role != ReleaseChildTag && c.Role != ReleaseChildGitHubRelease) || !validSHA256(c.ManifestSHA256) {
		return fmt.Errorf("authorization compound evidence must be complete and canonical")
	}
	if err := ValidateCanonicalGateThread(c.ParentGate); err != nil {
		return fmt.Errorf("authorization compound parent gate is invalid")
	}
	for name, value := range map[string]string{"release id": c.ReleaseID, "series id": c.SeriesID, "generation id": c.GenerationID, "prepared manifest id": c.PreparedManifestID, "active manifest id": c.ActiveManifestID} {
		if err := ValidateCanonicalSingleLineField("authorization compound "+name, value, true); err != nil {
			return err
		}
	}
	return nil
}

func validateAuthorizationPolicyEvidence(p AuthorizationPolicyEvidence) error {
	if p.Revision == 0 && p.SHA256 == "" {
		return nil
	}
	if p.Revision <= 0 || !validSHA256(p.SHA256) {
		return fmt.Errorf("authorization policy evidence must be complete")
	}
	return nil
}

func validateAuthorizationPreflightEvidence(p AuthorizationPreflightEvidence) error {
	if p.Kind == "" && p.Path == "" && p.SHA256 == "" {
		return nil
	}
	if err := ValidateCanonicalSingleLineField("authorization preflight kind", p.Kind, true); err != nil {
		return err
	}
	if !filepath.IsAbs(p.Path) || filepath.Clean(p.Path) != p.Path || !validSHA256(p.SHA256) {
		return fmt.Errorf("authorization preflight evidence must be complete and canonical")
	}
	return nil
}

func AuthorizationEvidenceDigest(items []AuthorizationEvidence) (string, error) {
	copyItems := append([]AuthorizationEvidence(nil), items...)
	sort.Slice(copyItems, func(i, j int) bool {
		if copyItems[i].Kind == copyItems[j].Kind {
			return copyItems[i].Identity < copyItems[j].Identity
		}
		return copyItems[i].Kind < copyItems[j].Kind
	})
	var e lpbeEncoder
	e.WriteString("amq-squad-authz-evidence-v1-lpbe")
	for _, item := range copyItems {
		for _, field := range []struct{ name, value string }{{"kind", item.Kind}, {"identity", item.Identity}, {"sha256", item.SHA256}} {
			if err := e.field(field.name, "string", field.value); err != nil {
				return "", err
			}
		}
	}
	sum := sha256.Sum256(e.Bytes())
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func NewAuthorizationEnvelope(payload AuthorizationPayload, signer AuthorizationSigner) (AuthorizationEnvelope, error) {
	if signer == nil || signer.KeyID() == "" {
		return AuthorizationEnvelope{}, fmt.Errorf("authorization signer is required")
	}
	payload.SchemaVersion = AuthorizationEnvelopeSchemaVersion
	payload.TaxonomyVersion = ActionTaxonomyVersion
	payload.Decision = "approved"
	evidenceDigest, err := AuthorizationEvidenceDigest(payload.Evidence)
	if err != nil {
		return AuthorizationEnvelope{}, err
	}
	payload.EvidenceSHA256 = evidenceDigest
	payload.AuthorizationID = authorizationIdentity(payload, signer.KeyID())
	canonical, err := CanonicalAuthorizationPayload(payload)
	if err != nil {
		return AuthorizationEnvelope{}, err
	}
	digest := sha256.Sum256(canonical)
	signature, err := signer.Sign(canonical)
	if err != nil {
		return AuthorizationEnvelope{}, err
	}
	return AuthorizationEnvelope{
		SchemaVersion: AuthorizationEnvelopeSchemaVersion, Canonicalization: AuthorizationCanonicalization, Payload: payload,
		PayloadSHA256: "sha256:" + hex.EncodeToString(digest[:]),
		Signature:     AuthorizationSignature{Algorithm: authorizationSignatureAlgorithm, KeyID: signer.KeyID(), Value: base64.StdEncoding.EncodeToString(signature)},
	}, nil
}

func authorizationIdentity(p AuthorizationPayload, keyID string) string {
	parts := strings.Join([]string{p.Namespace.ProjectDir, p.Namespace.Profile, p.Namespace.Session, p.Namespace.NamespaceID, p.Namespace.Generation, p.Gate, p.GateKind, p.Action, p.Target, p.QuestionMessageID, p.AnswerMessageID, p.EvidenceSHA256, keyID}, "\x00")
	sum := sha256.Sum256([]byte(parts))
	return "authz-v1-" + hex.EncodeToString(sum[:])
}

func VerifyAuthorizationEnvelope(envelope AuthorizationEnvelope, store AuthorizationTrustStore) error {
	if envelope.SchemaVersion != AuthorizationEnvelopeSchemaVersion || envelope.Canonicalization != AuthorizationCanonicalization || envelope.Signature.Algorithm != authorizationSignatureAlgorithm {
		return fmt.Errorf("authorization envelope metadata is invalid")
	}
	if envelope.Payload.AuthorizationID != authorizationIdentity(envelope.Payload, envelope.Signature.KeyID) {
		return fmt.Errorf("authorization id mismatch")
	}
	canonical, err := CanonicalAuthorizationPayload(envelope.Payload)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(canonical)
	if envelope.PayloadSHA256 != "sha256:"+hex.EncodeToString(digest[:]) {
		return fmt.Errorf("authorization payload digest mismatch")
	}
	signature, err := base64.StdEncoding.Strict().DecodeString(envelope.Signature.Value)
	if err != nil || len(signature) != 64 {
		return fmt.Errorf("authorization signature encoding is invalid")
	}
	return store.Verify(envelope.Signature.KeyID, canonical, signature)
}

func DecodeAuthorizationEnvelope(raw []byte) (AuthorizationEnvelope, error) {
	var envelope AuthorizationEnvelope
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&envelope); err != nil {
		return AuthorizationEnvelope{}, err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return AuthorizationEnvelope{}, fmt.Errorf("authorization envelope has trailing JSON")
	}
	return envelope, nil
}

func LoadAuthorizationEnvelope(path string) (AuthorizationEnvelope, error) {
	raw, err := secureReadAuthorizationFile(path, false, 4<<20)
	if err != nil {
		return AuthorizationEnvelope{}, fmt.Errorf("load authorization envelope %s: %w", path, err)
	}
	return DecodeAuthorizationEnvelope(raw)
}

// ReadAuthorizationEvidenceFile reads a local authority artifact through the
// same owner-controlled regular-file/no-symlink boundary used for envelopes
// and trust stores.
func ReadAuthorizationEvidenceFile(path string, maxSize int64) ([]byte, error) {
	if maxSize <= 0 {
		return nil, fmt.Errorf("authorization evidence size limit must be positive")
	}
	return secureReadAuthorizationFile(path, false, maxSize)
}

func validSHA256(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
