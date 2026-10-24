package gateverify

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

const (
	SchemeBIP340      = "bip340"
	EvidenceKindEvent = "bip340.nostr-event"
	EvidenceKindGate  = "gate.text"
	IssuerBinary      = "amq-squad"
)

// Namespace is the squad namespace an envelope binds.
type Namespace struct {
	ProjectDir, Profile, Session, Generation string
}

func (ns Namespace) binding() operatorauth.NamespaceBinding {
	return operatorauth.NamespaceBinding{ProjectDir: ns.ProjectDir, Profile: ns.Profile, Session: ns.Session,
		NamespaceID: ns.Profile + "/" + ns.Session, Generation: ns.Generation}
}

func sha256Text(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// expectedNote records the owner and the typed class the owner signed.
func expectedNote(rec gatestore.Record) string {
	return "scheme " + SchemeBIP340 + "; owner " + rec.OwnerPubKey + "; class " + rec.TypedClass
}

// payload is the operatorauth payload for a verified answer of either
// decision.
func payload(o Outcome, raw []byte, rec gatestore.Record, q gatestore.Question, ns Namespace, version string, now time.Time) (operatorauth.AuthorizationPayload, error) {
	if o.Event.PubKey != rec.OwnerPubKey {
		return operatorauth.AuthorizationPayload{}, errors.New("the answer's signer is not the gate record's owner")
	}
	issued, _, _, err := rec.Window()
	if err != nil {
		return operatorauth.AuthorizationPayload{}, err
	}
	answerAt := time.Unix(o.Event.CreatedAt, 0).UTC()
	questionAt := issued
	if answerAt.Before(questionAt) { // the owner's clock ran behind, inside the 60 s skew
		questionAt = answerAt
	}
	verifiedAt := now.UTC()
	if verifiedAt.Before(answerAt) { // the owner's clock ran ahead, inside the 60 s skew
		verifiedAt = answerAt
	}
	return operatorauth.AuthorizationPayload{
		Namespace: ns.binding(),
		Gate:      "gate/" + rec.GateID, Thread: "gate/" + rec.GateID,
		GateKind: rec.GateKind, Action: rec.Class, Target: rec.Digest,
		Note:              expectedNote(rec),
		QuestionMessageID: "nostr:" + q.EventID,
		AnswerMessageID:   "nostr:" + o.Event.ID,
		QuestionCreatedAt: questionAt.Format(time.RFC3339Nano),
		AnswerCreatedAt:   answerAt.Format(time.RFC3339Nano),
		Actor:             operatorauth.AuthorizationActor{Role: "operator", Handle: "owner", Source: "human"},
		Issuer:            operatorauth.AuthorizationIssuer{Binary: IssuerBinary, Version: version},
		Evidence: []operatorauth.AuthorizationEvidence{
			{Kind: EvidenceKindEvent, Identity: "nostr:" + o.Event.ID, SHA256: sha256Text(raw)},
			{Kind: EvidenceKindGate, Identity: "gate/" + rec.GateID, SHA256: rec.TextSHA256},
		},
		VerifiedAt: verifiedAt.Format(time.RFC3339Nano),
	}, nil
}

// BuildAnswerFile wraps a verified answer: an approve becomes an
// operatorauth authorization envelope, a deny a signed deny record. The
// issuer key must be the one the gate record names.
func BuildAnswerFile(o Outcome, raw []byte, rec gatestore.Record, q gatestore.Question, pin Pin, relay string, ns Namespace, version string, signer operatorauth.AuthorizationSigner, now time.Time) (gatestore.AnswerFile, error) {
	if signer == nil || signer.KeyID() != rec.IssuerKeyID {
		return gatestore.AnswerFile{}, fmt.Errorf("the issuer key is not the gate record's issuer %s", rec.IssuerKeyID)
	}
	p, err := payload(o, raw, rec, q, ns, version, now)
	if err != nil {
		return gatestore.AnswerFile{}, err
	}
	var env operatorauth.AuthorizationEnvelope
	decision := operatorauth.DecisionApproved
	switch o.Decision {
	case "approve":
		env, err = operatorauth.NewAuthorizationEnvelope(p, signer)
	case "deny":
		decision = operatorauth.DecisionDenied
		env, err = operatorauth.NewDenialEnvelope(p, signer)
	default:
		return gatestore.AnswerFile{}, errors.New("unknown decision")
	}
	if err != nil {
		return gatestore.AnswerFile{}, fmt.Errorf("operatorauth envelope: %w", err)
	}
	qq := q
	return gatestore.AnswerFile{
		Schema: gatestore.AnswerSchema, Scheme: SchemeBIP340, Decision: decision, Envelope: env, Gate: rec, Question: &qq,
		Evidence: gatestore.Evidence{Scheme: SchemeBIP340, EventID: o.Event.ID, OwnerPubKey: o.Event.PubKey, Channel: pin.Channel, Relay: relay, RawEvent: string(raw)},
	}, nil
}

func sameJSON(a, b any) bool {
	ja, ea := json.Marshal(a)
	jb, eb := json.Marshal(b)
	return ea == nil && eb == nil && string(ja) == string(jb)
}

// VerifyAnswerFile is the full check a consumer (the release step, the merge
// train's escalation) runs before acting on an approval: it never rests on
// the issuer key alone (S1 section 6, the phase 3 slot-in).
//
//   - the record and question come from the store, never from the file; the
//     file's unsigned copies must equal them;
//   - the operatorauth envelope verifies against the trust store, signed by
//     the issuer the stored record names;
//   - every security-bearing payload field agrees with the record and what
//     the owner signed (gate, thread, action, kind, target, note, question,
//     gate-text evidence, event evidence digest of the raw bytes);
//   - the raw owner event re-verifies (BIP-340, tuple, thread) at the
//     recorded verified_at, and it is the approve the envelope names;
//   - the signed namespace is exactly the caller's (project, profile,
//     session, generation): an approval copied into another session's store
//     never counts there.
func VerifyAnswerFile(f gatestore.AnswerFile, store operatorauth.AuthorizationTrustStore, pin Pin, rec gatestore.Record, q *gatestore.Question, reask *gatestore.Reask, ns Namespace) error {
	if f.Schema != gatestore.AnswerSchema || f.Scheme != SchemeBIP340 || f.Decision != operatorauth.DecisionApproved {
		return errors.New("answer file is not a bip340 approval")
	}
	if err := rec.Validate(); err != nil {
		return fmt.Errorf("stored gate record: %w", err)
	}
	if q == nil {
		return errors.New("the stored gate has no question: an approval is never unbound")
	}
	if !sameJSON(f.Gate, rec) || !sameJSON(f.Question, q) {
		return errors.New("the answer file's record or question differs from the store")
	}
	if err := operatorauth.ValidateAuthorizationPayload(f.Envelope.Payload); err != nil {
		return fmt.Errorf("operatorauth: %w", err)
	}
	if err := operatorauth.VerifyAuthorizationEnvelope(f.Envelope, store); err != nil {
		return fmt.Errorf("operatorauth: %w", err)
	}
	if got, want := f.Envelope.Payload.Namespace, ns.binding(); got != want {
		return fmt.Errorf("the approval binds namespace %s/%s generation %s in %s, not the caller's %s/%s generation %s in %s",
			got.Profile, got.Session, got.Generation, got.ProjectDir, want.Profile, want.Session, want.Generation, want.ProjectDir)
	}
	if f.Envelope.Signature.KeyID != rec.IssuerKeyID {
		return fmt.Errorf("envelope signed by %s, the stored record's issuer is %s", f.Envelope.Signature.KeyID, rec.IssuerKeyID)
	}
	p, ev := f.Envelope.Payload, f.Evidence
	var item, gateItem *operatorauth.AuthorizationEvidence
	for i := range p.Evidence {
		switch p.Evidence[i].Kind {
		case EvidenceKindEvent:
			if item != nil {
				return errors.New("more than one bip340 evidence item")
			}
			item = &p.Evidence[i]
		case EvidenceKindGate:
			if gateItem != nil {
				return errors.New("more than one gate.text evidence item")
			}
			gateItem = &p.Evidence[i]
		}
	}
	raw := []byte(ev.RawEvent)
	switch {
	case ev.Scheme != SchemeBIP340 || ev.Channel != pin.Channel || ev.OwnerPubKey != rec.OwnerPubKey || pin.Owner != rec.OwnerPubKey:
		return errors.New("bip340 evidence names another scheme, channel or owner")
	case p.Note != expectedNote(rec) || p.Issuer.Binary != IssuerBinary:
		return errors.New("envelope note or issuer is not the gate verifier's")
	case p.GateKind != rec.GateKind || p.Action != rec.Class:
		return fmt.Errorf("envelope action %s/%s is not the record's %s/%s", p.GateKind, p.Action, rec.GateKind, rec.Class)
	case p.Gate != "gate/"+rec.GateID || p.Thread != p.Gate || p.Target != rec.Digest:
		return errors.New("envelope gate or target is not the record's")
	case item == nil || item.SHA256 != sha256Text(raw) || item.Identity != "nostr:"+ev.EventID || p.AnswerMessageID != "nostr:"+ev.EventID:
		return errors.New("envelope event evidence is not the raw event")
	case gateItem == nil || gateItem.Identity != "gate/"+rec.GateID || gateItem.SHA256 != rec.TextSHA256:
		return errors.New("envelope gate-text evidence is not the record's text")
	case p.QuestionMessageID != "nostr:"+q.EventID:
		return errors.New("envelope question id is not the stored question")
	}
	verifiedAt, err := time.Parse(time.RFC3339Nano, p.VerifiedAt)
	if err != nil {
		return err
	}
	o, err := Verify(raw, rec, q, reask, pin, verifiedAt)
	if err != nil {
		return fmt.Errorf("bip340 re-verification: %w", err)
	}
	if o.Event.ID != ev.EventID || o.Decision != "approve" || p.AnswerCreatedAt != time.Unix(o.Event.CreatedAt, 0).UTC().Format(time.RFC3339Nano) {
		return errors.New("the bip340 evidence is not the approve the envelope records")
	}
	return nil
}
