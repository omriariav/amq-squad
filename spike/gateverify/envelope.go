package main

// The verified answer as an operatorauth envelope, built with the main
// module's internal/operatorauth exactly as it is (no change in this spike):
//
//   - the envelope is signed by a core Ed25519 issuer key
//     (operatorauth.LoadEd25519Signer + NewAuthorizationEnvelope), which
//     attests "the core verified this answer";
//   - the owner's raw nostr event rides as evidence under the new scheme
//     name "bip340": the envelope's Evidence list holds its digest
//     ({kind: "bip340.nostr-event", identity: "nostr:<id>", sha256}), and the
//     exact bytes travel beside the envelope in this wrapper, because
//     AuthorizationEvidence has no bytes field and DecodeAuthorizationEnvelope
//     refuses unknown fields;
//   - VerifyBIP340Evidence is the phase 3 slot-in: what operatorauth would
//     run, after VerifyAuthorizationEnvelope, for evidence of scheme bip340,
//     so trust in an approve never rests on the issuer key alone.

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
)

const (
	answerFileSchema   = "amq-squad-gate-answer-spike/v1"
	schemeBIP340       = "bip340"
	evidenceKindEvent  = "bip340.nostr-event"
	evidenceKindGate   = "gate.text"
	issuerBinary       = "amq-squad-gateverify-spike"
	issuerVersion      = "0.0.0-spike-s1"
	ownerActorHandle   = "owner"
	ownerActorRole     = "operator"
	ownerActorSource   = "human"
	defaultProfileName = "spike"
)

// classTaxonomy maps a typed gate class onto today's operatorauth taxonomy
// (internal/operatorauth/policy.go:35-43). "credential" has no v2 gate
// kind; phase 3 adds one.
var classTaxonomy = map[string][2]string{
	"merge":   {operatorauth.GateMerge, "default_branch_push"},
	"release": {operatorauth.GateRelease, "github_release"},
	"send":    {operatorauth.GateExternalSend, "external_send"},
	"grow":    {operatorauth.GateSpawn, "spawn"},
}

// BIP340Evidence is the owner's answer as carried beside the envelope.
type BIP340Evidence struct {
	Scheme      string `json:"scheme"`
	EventID     string `json:"event_id"`
	OwnerPubKey string `json:"owner_pubkey"`
	OwnerNpub   string `json:"owner_npub"`
	Channel     string `json:"channel"`
	Relay       string `json:"relay,omitempty"`
	RawEvent    string `json:"raw_event"` // the exact bytes fetched from the relay
}

// AnswerFile is what the spike writes on a verified approve. Gate and
// Question are unsigned copies of the core's stored records. A consumer
// checks them against the signed payload (VerifyBIP340Evidence), so they
// cannot drift.
type AnswerFile struct {
	Schema   string                             `json:"schema"`
	Scheme   string                             `json:"scheme"`
	Envelope operatorauth.AuthorizationEnvelope `json:"envelope"`
	Evidence BIP340Evidence                     `json:"bip340_evidence"`
	Gate     GateRecord                         `json:"gate"`
	Question *GateQuestion                      `json:"question,omitempty"`
}

// Namespace is the squad namespace the envelope binds.
type Namespace struct {
	ProjectDir, Profile, Session, Generation string
}

func sha256Text(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// expectedNote is the payload note for a gate. It records the owner npub
// from the gate record, so the verifier can compare the owner identity the
// envelope names with the record's.
func expectedNote(gate GateRecord) string {
	npub, _ := EncodeNpub(gate.OwnerPubKey)
	return "scheme " + schemeBIP340 + "; owner " + npub + "; class " + gate.Class
}

// questionMessageID is what the envelope records as the question: the
// posted question's nostr id when there is one, else the gate text digest.
func questionMessageID(gate GateRecord, q *GateQuestion) string {
	if q != nil {
		return "nostr:" + q.EventID
	}
	return "gate-text:" + strings.TrimPrefix(gate.TextSHA256, "sha256:")
}

// answerPayload is the operatorauth payload for a verified approve.
func answerPayload(o Outcome, raw []byte, gate GateRecord, q *GateQuestion, pin Pin, ns Namespace, now time.Time) (operatorauth.AuthorizationPayload, error) {
	if o.Decision != "approve" {
		return operatorauth.AuthorizationPayload{}, errors.New("only an approve becomes an authorization envelope; a deny closes the gate")
	}
	tax, ok := classTaxonomy[gate.Class]
	if !ok {
		return operatorauth.AuthorizationPayload{}, fmt.Errorf("class %q has no operatorauth v1 gate kind yet (phase 3 taxonomy)", gate.Class)
	}
	if pin.Owner != gate.OwnerPubKey || o.Event.PubKey != gate.OwnerPubKey {
		return operatorauth.AuthorizationPayload{}, errors.New("the answer's owner is not the gate record's owner")
	}
	answerAt := time.Unix(o.Event.CreatedAt, 0).UTC()
	_, nb, _, _ := gate.window()
	verifiedAt := now.UTC()
	if verifiedAt.Before(answerAt) {
		// The owner's clock ran ahead, inside the 60 s skew Verify admits.
		// The envelope's chronology rule needs verified >= answered.
		verifiedAt = answerAt
	}
	return operatorauth.AuthorizationPayload{
		Namespace: operatorauth.NamespaceBinding{
			ProjectDir: ns.ProjectDir, Profile: ns.Profile, Session: ns.Session,
			NamespaceID: ns.Profile + "/" + ns.Session, Generation: ns.Generation,
		},
		Gate: "gate/" + gate.GateID, Thread: "gate/" + gate.GateID,
		GateKind: tax[0], Action: tax[1], Target: gate.Digest,
		Note:              expectedNote(gate),
		QuestionMessageID: questionMessageID(gate, q),
		AnswerMessageID:   "nostr:" + o.Event.ID,
		QuestionCreatedAt: nb.UTC().Format(time.RFC3339Nano),
		AnswerCreatedAt:   answerAt.Format(time.RFC3339Nano),
		Actor:             operatorauth.AuthorizationActor{Role: ownerActorRole, Handle: ownerActorHandle, Source: ownerActorSource},
		Issuer:            operatorauth.AuthorizationIssuer{Binary: issuerBinary, Version: issuerVersion},
		Evidence: []operatorauth.AuthorizationEvidence{
			{Kind: evidenceKindEvent, Identity: "nostr:" + o.Event.ID, SHA256: sha256Text(raw)},
			{Kind: evidenceKindGate, Identity: "gate/" + gate.GateID, SHA256: gate.TextSHA256},
		},
		VerifiedAt: verifiedAt.Format(time.RFC3339Nano),
	}, nil
}

// BuildAnswerFile wraps a verified approve.
func BuildAnswerFile(o Outcome, raw []byte, gate GateRecord, q *GateQuestion, pin Pin, relay string, ns Namespace, signer operatorauth.AuthorizationSigner, now time.Time) (AnswerFile, error) {
	if signer.KeyID() != gate.IssuerKeyID {
		return AnswerFile{}, fmt.Errorf("issuer key %s is not the gate record's issuer %s", signer.KeyID(), gate.IssuerKeyID)
	}
	payload, err := answerPayload(o, raw, gate, q, pin, ns, now)
	if err != nil {
		return AnswerFile{}, err
	}
	return signAnswerFile(payload, o, raw, gate, q, pin, relay, signer)
}

func signAnswerFile(payload operatorauth.AuthorizationPayload, o Outcome, raw []byte, gate GateRecord, q *GateQuestion, pin Pin, relay string, signer operatorauth.AuthorizationSigner) (AnswerFile, error) {
	env, err := operatorauth.NewAuthorizationEnvelope(payload, signer)
	if err != nil {
		return AnswerFile{}, fmt.Errorf("operatorauth envelope: %w", err)
	}
	npub, _ := EncodeNpub(pin.Owner)
	return AnswerFile{
		Schema: answerFileSchema, Scheme: schemeBIP340, Envelope: env, Gate: gate, Question: q,
		Evidence: BIP340Evidence{Scheme: schemeBIP340, EventID: o.Event.ID, OwnerPubKey: pin.Owner, OwnerNpub: npub,
			Channel: pin.Channel, Relay: relay, RawEvent: string(raw)},
	}, nil
}

// ErrUnbound refuses an answer file with no stored question, the
// unthreaded form, unless the caller explicitly allows it.
var ErrUnbound = errors.New("answer is not bound to a gate question (no question in the answer file); refused unless explicitly allowed")

// VerifyAnswerFile is the full check a consumer (the merge train) runs:
// first today's unchanged operatorauth verification of the issuer
// signature, then the gate record's issuer identity, then the bip340
// slot-in. It fails closed on thread binding: an answer file with no
// question is refused unless allowUnbound is set.
func VerifyAnswerFile(f AnswerFile, store operatorauth.AuthorizationTrustStore, pin Pin, allowUnbound bool) error {
	if f.Schema != answerFileSchema || f.Scheme != schemeBIP340 {
		return errors.New("answer file schema or scheme is not bip340")
	}
	if f.Question == nil && !allowUnbound {
		return ErrUnbound
	}
	if err := operatorauth.VerifyAuthorizationEnvelope(f.Envelope, store); err != nil {
		return fmt.Errorf("operatorauth: %w", err)
	}
	// Any trusted key verifies above; only the issuer the gate record names
	// may authorize this gate.
	if f.Envelope.Signature.KeyID != f.Gate.IssuerKeyID {
		return fmt.Errorf("envelope signed by %s, the gate record's issuer is %s", f.Envelope.Signature.KeyID, f.Gate.IssuerKeyID)
	}
	return VerifyBIP340Evidence(f.Envelope.Payload, f.Evidence, f.Gate, f.Question, pin)
}

// VerifyBIP340Evidence re-verifies the owner's signature from the raw
// event, independent of the issuer key. It also checks that every
// security-bearing payload field agrees with what the owner signed and with
// the core's records. A trusted issuer therefore cannot mislabel the
// action, and the unsigned wrapper records cannot drift:
//
//   - gate_kind and action are the taxonomy mapping of the gate record's
//     class, which the owner's signed tuple names;
//   - gate and target are the record's gate id and full digest;
//   - the gate.text evidence is the record's text digest, and the record's
//     text is the rendering of its own fields (GateRecord.validate);
//   - question_message_id is the stored question's id, or the text digest
//     when no question was posted;
//   - the bip340 evidence digest is the raw event's, and the raw event
//     re-verifies at the recorded verified_at.
//
// In phase 3 this moves into operatorauth as the verifier for evidence kind
// "bip340.nostr-event". The owner pin then comes from the trust store
// (algorithm "bip340", key id "bip340-sha256:<hex sha256 of the x-only
// pubkey>").
func VerifyBIP340Evidence(p operatorauth.AuthorizationPayload, ev BIP340Evidence, gate GateRecord, q *GateQuestion, pin Pin) error {
	if err := gate.validate(); err != nil {
		return fmt.Errorf("gate record: %w", err)
	}
	recordNpub, _ := EncodeNpub(gate.OwnerPubKey)
	switch {
	case ev.Scheme != schemeBIP340 || ev.Channel != pin.Channel:
		return errors.New("bip340 evidence names another channel or scheme")
	case pin.Owner != gate.OwnerPubKey || ev.OwnerPubKey != gate.OwnerPubKey || ev.OwnerNpub != recordNpub:
		return errors.New("bip340 evidence or pin names an owner other than the gate record's")
	case p.Note != expectedNote(gate):
		return errors.New("envelope note does not record the gate record's owner npub and class")
	case p.Issuer.Binary != issuerBinary:
		return errors.New("envelope issuer binary is not the gate verifier")
	}
	raw := []byte(ev.RawEvent)
	var item, gateItem *operatorauth.AuthorizationEvidence
	for i := range p.Evidence {
		switch p.Evidence[i].Kind {
		case evidenceKindEvent:
			if item != nil {
				return errors.New("more than one bip340 evidence item")
			}
			item = &p.Evidence[i]
		case evidenceKindGate:
			if gateItem != nil {
				return errors.New("more than one gate.text evidence item")
			}
			gateItem = &p.Evidence[i]
		}
	}
	tax, ok := classTaxonomy[gate.Class]
	switch {
	case !ok || p.GateKind != tax[0] || p.Action != tax[1]:
		return fmt.Errorf("envelope action %s/%s is not the mapping of the signed class %s", p.GateKind, p.Action, gate.Class)
	case item == nil:
		return errors.New("envelope carries no bip340 evidence")
	case item.SHA256 != sha256Text(raw):
		return errors.New("raw event bytes do not match the envelope's evidence digest")
	case item.Identity != "nostr:"+ev.EventID || p.AnswerMessageID != "nostr:"+ev.EventID:
		return errors.New("envelope answer id is not the evidence event")
	case p.Gate != "gate/"+gate.GateID || p.Thread != p.Gate || p.Target != gate.Digest:
		return errors.New("envelope gate or target is not the gate record's")
	case gateItem == nil || gateItem.Identity != "gate/"+gate.GateID || gateItem.SHA256 != gate.TextSHA256:
		return errors.New("envelope gate-text evidence is not the gate record's text")
	case q != nil && q.GateID != gate.GateID:
		return errors.New("question record names another gate")
	case p.QuestionMessageID != questionMessageID(gate, q):
		return errors.New("envelope question id is not the stored question")
	}
	answeredAt, err := time.Parse(time.RFC3339Nano, p.AnswerCreatedAt)
	if err != nil {
		return err
	}
	// Re-run the answer checks at the time the answer was verified, so a
	// later audit of an old envelope does not fail on the window.
	verifiedAt, err := time.Parse(time.RFC3339Nano, p.VerifiedAt)
	if err != nil {
		return err
	}
	if q != nil {
		pin.RequireThread = true
	}
	o, err := Verify(raw, gate, q, pin, verifiedAt)
	if err != nil {
		return fmt.Errorf("bip340 re-verification: %w", err)
	}
	// Identity: the re-verified event is the one the envelope names. The
	// payload's answer id and evidence identity were compared with the
	// wrapper's event id above; this ties both to the signed bytes.
	if o.Event.ID != ev.EventID || o.Event.PubKey != gate.OwnerPubKey {
		return fmt.Errorf("envelope names answer %s, the evidence bytes are event %s", ev.EventID, o.Event.ID)
	}
	if o.Decision != "approve" || o.Answer.Class != gate.Class || !time.Unix(o.Event.CreatedAt, 0).Equal(answeredAt) {
		return errors.New("bip340 evidence is not the approve the envelope records")
	}
	return nil
}

// WriteIssuerKey mints the core's Ed25519 issuer key (PKCS#8 PEM, 0600) and
// a matching operatorauth trust store. Scratch only, never the repository.
func WriteIssuerKey(keyPath, storePath string) (string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return "", err
	}
	defer clear(der)
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		return "", err
	}
	keyID := operatorauth.KeyIDForEd25519(pub)
	store := operatorauth.AuthorizationTrustStore{
		SchemaVersion: operatorauth.AuthorizationTrustStoreSchemaVersion,
		Keys:          []operatorauth.AuthorizationTrustKey{{KeyID: keyID, Algorithm: "ed25519", PublicKey: base64.StdEncoding.EncodeToString(pub), Status: "trusted"}},
	}
	raw, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return "", err
	}
	return keyID, os.WriteFile(storePath, append(raw, '\n'), 0o600)
}
