package operatorauth

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"strconv"
)

const AuthorizationCanonicalization = "amq-squad-authz-v1-lpbe"

type lpbeEncoder struct{ bytes.Buffer }

func (e *lpbeEncoder) field(name, kind, value string) error {
	for _, part := range []string{name, kind, value} {
		if len(part) > 1<<24 {
			return fmt.Errorf("authorization canonical field is too large")
		}
		if err := binary.Write(&e.Buffer, binary.BigEndian, uint32(len(part))); err != nil {
			return err
		}
		e.WriteString(part)
	}
	return nil
}

func (e *lpbeEncoder) integer(name string, value int64) error {
	return e.field(name, "integer", strconv.FormatInt(value, 10))
}

func (e *lpbeEncoder) boolean(name string, value bool) error {
	return e.field(name, "boolean", strconv.FormatBool(value))
}

// CanonicalAuthorizationPayload encodes the fixed v1 payload without maps,
// floats, platform-dependent formatting, or JSON canonicalization claims.
func CanonicalAuthorizationPayload(p AuthorizationPayload) ([]byte, error) {
	if err := ValidateAuthorizationPayload(p); err != nil {
		return nil, err
	}
	var e lpbeEncoder
	e.WriteString(AuthorizationCanonicalization)
	fields := []struct{ name, value string }{
		{"authorization_id", p.AuthorizationID}, {"decision", p.Decision},
		{"namespace.project_dir", p.Namespace.ProjectDir}, {"namespace.profile", p.Namespace.Profile},
		{"namespace.session", p.Namespace.Session}, {"namespace.namespace_id", p.Namespace.NamespaceID},
		{"namespace.generation", p.Namespace.Generation}, {"gate", p.Gate}, {"thread", p.Thread},
		{"gate_kind", p.GateKind}, {"action", p.Action}, {"target", p.Target}, {"note", p.Note},
		{"question_message_id", p.QuestionMessageID}, {"answer_message_id", p.AnswerMessageID},
		{"question_created_at", p.QuestionCreatedAt}, {"answer_created_at", p.AnswerCreatedAt},
		{"actor.role", p.Actor.Role}, {"actor.handle", p.Actor.Handle}, {"actor.source", p.Actor.Source},
		{"issuer.binary", p.Issuer.Binary}, {"issuer.version", p.Issuer.Version}, {"issuer.build_commit", p.Issuer.BuildCommit},
		{"compound.release_id", p.Compound.ReleaseID}, {"compound.parent_gate", p.Compound.ParentGate},
		{"compound.series_id", p.Compound.SeriesID}, {"compound.generation_id", p.Compound.GenerationID},
		{"compound.prepared_manifest_id", p.Compound.PreparedManifestID}, {"compound.active_manifest_id", p.Compound.ActiveManifestID},
		{"compound.role", p.Compound.Role}, {"compound.manifest_sha256", p.Compound.ManifestSHA256},
		{"policy.revision", strconv.FormatInt(p.Policy.Revision, 10)}, {"policy.sha256", p.Policy.SHA256},
		{"preflight.kind", p.Preflight.Kind}, {"preflight.path", p.Preflight.Path}, {"preflight.sha256", p.Preflight.SHA256},
		{"evidence_sha256", p.EvidenceSHA256}, {"verified_at", p.VerifiedAt},
	}
	if err := e.integer("schema_version", int64(p.SchemaVersion)); err != nil {
		return nil, err
	}
	if err := e.integer("taxonomy_version", int64(p.TaxonomyVersion)); err != nil {
		return nil, err
	}
	for _, field := range fields {
		if err := e.field(field.name, "string", field.value); err != nil {
			return nil, err
		}
	}
	if err := e.boolean("actor.self_approved", p.Actor.SelfApproved); err != nil {
		return nil, err
	}
	if err := e.integer("evidence.count", int64(len(p.Evidence))); err != nil {
		return nil, err
	}
	for i, item := range p.Evidence {
		prefix := "evidence." + strconv.Itoa(i) + "."
		for _, field := range []struct{ name, value string }{{"kind", item.Kind}, {"identity", item.Identity}, {"sha256", item.SHA256}} {
			if err := e.field(prefix+field.name, "string", field.value); err != nil {
				return nil, err
			}
		}
	}
	return append([]byte(nil), e.Bytes()...), nil
}
