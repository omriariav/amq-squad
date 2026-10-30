package operatorauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// Decisions an envelope payload can carry.
const (
	DecisionApproved = "approved"
	DecisionDenied   = "denied"
)

// DenialCanonicalization is the signing domain of a deny record. It differs
// from AuthorizationCanonicalization, so a deny signature never verifies as
// an approval and an approval never as a deny (S1 open question 5).
const DenialCanonicalization = "amq-squad-authz-deny-v1-lpbe"

// ValidateDenialPayload checks a deny record payload: the same fields and
// rules as an approval, with decision "denied".
func ValidateDenialPayload(p AuthorizationPayload) error {
	return validatePayload(p, DecisionDenied)
}

// NewDenialEnvelope signs a verified owner deny. It closes the gate in the
// gate store's ledger; nothing ever reads it as an authorization.
func NewDenialEnvelope(payload AuthorizationPayload, signer AuthorizationSigner) (AuthorizationEnvelope, error) {
	if signer == nil || signer.KeyID() == "" {
		return AuthorizationEnvelope{}, fmt.Errorf("authorization signer is required")
	}
	payload.SchemaVersion = AuthorizationEnvelopeSchemaVersion
	payload.TaxonomyVersion = ActionTaxonomyVersion
	payload.Decision = DecisionDenied
	evidenceDigest, err := AuthorizationEvidenceDigest(payload.Evidence)
	if err != nil {
		return AuthorizationEnvelope{}, err
	}
	payload.EvidenceSHA256 = evidenceDigest
	payload.AuthorizationID = authorizationIdentity(payload, signer.KeyID())
	if err := ValidateDenialPayload(payload); err != nil {
		return AuthorizationEnvelope{}, err
	}
	canonical, err := canonicalPayload(payload, DenialCanonicalization)
	if err != nil {
		return AuthorizationEnvelope{}, err
	}
	digest := sha256.Sum256(canonical)
	signature, err := signer.Sign(canonical)
	if err != nil {
		return AuthorizationEnvelope{}, err
	}
	return AuthorizationEnvelope{
		SchemaVersion: AuthorizationEnvelopeSchemaVersion, Canonicalization: DenialCanonicalization, Payload: payload,
		PayloadSHA256: "sha256:" + hex.EncodeToString(digest[:]),
		Signature:     AuthorizationSignature{Algorithm: authorizationSignatureAlgorithm, KeyID: signer.KeyID(), Value: base64.StdEncoding.EncodeToString(signature)},
	}, nil
}

// VerifyDenialEnvelope verifies a deny record against the trust store.
func VerifyDenialEnvelope(envelope AuthorizationEnvelope, store AuthorizationTrustStore) error {
	if envelope.SchemaVersion != AuthorizationEnvelopeSchemaVersion || envelope.Canonicalization != DenialCanonicalization || envelope.Signature.Algorithm != authorizationSignatureAlgorithm {
		return fmt.Errorf("denial envelope metadata is invalid")
	}
	if err := ValidateDenialPayload(envelope.Payload); err != nil {
		return err
	}
	if envelope.Payload.AuthorizationID != authorizationIdentity(envelope.Payload, envelope.Signature.KeyID) {
		return fmt.Errorf("authorization id mismatch")
	}
	canonical, err := canonicalPayload(envelope.Payload, DenialCanonicalization)
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
