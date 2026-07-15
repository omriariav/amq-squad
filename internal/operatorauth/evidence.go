package operatorauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	GateRequestSchemaVersion = 1
	ApprovalSchemaVersionV1  = 1
	ApprovalSchemaVersion    = 2
	ReceiptSchemaVersionV1   = 1
	ReceiptSchemaVersion     = 2
)

type NamespaceBinding struct {
	ProjectDir  string `json:"project_dir"`
	Profile     string `json:"profile"`
	Session     string `json:"session"`
	NamespaceID string `json:"namespace_id"`
	Generation  string `json:"generation"`
}

// GateRequestContext is the durable, atomic question binding. Note is not part
// of action matching, but remains integrity-bearing typed context.
type GateRequestContext struct {
	SchemaVersion   int              `json:"schema_version"`
	TaxonomyVersion int              `json:"taxonomy_version"`
	Gate            string           `json:"gate"`
	Thread          string           `json:"thread"`
	Namespace       NamespaceBinding `json:"namespace"`
	GateKind        string           `json:"gate_kind"`
	Action          string           `json:"action"`
	Target          string           `json:"target"`
	Note            string           `json:"note,omitempty"`
}

type ApprovalContext struct {
	SchemaVersion     int    `json:"schema_version"`
	TaxonomyVersion   int    `json:"taxonomy_version,omitempty"`
	Source            string `json:"source"`
	SelfApproved      bool   `json:"self_approved"`
	GateKind          string `json:"gate_kind"`
	Action            string `json:"action"`
	Target            string `json:"target"`
	Note              string `json:"note,omitempty"`
	QuestionMessageID string `json:"question_message_id"`
	AnsweredByRole    string `json:"answered_by_role"`
	AnsweredByHandle  string `json:"answered_by_handle"`
	PolicyRevision    int64  `json:"policy_revision,omitempty"`
	PolicyHash        string `json:"policy_hash,omitempty"`
	PreflightKind     string `json:"preflight_kind,omitempty"`
	PreflightSHA256   string `json:"preflight_sha256,omitempty"`
	PreflightPath     string `json:"preflight_path,omitempty"`
	VerifiedAt        string `json:"verified_at"`
}

type approvalContextV1 struct {
	SchemaVersion     int    `json:"schema_version"`
	Source            string `json:"source"`
	SelfApproved      bool   `json:"self_approved"`
	GateKind          string `json:"gate_kind"`
	Action            string `json:"action"`
	Target            string `json:"target"`
	QuestionMessageID string `json:"question_message_id"`
	AnsweredByRole    string `json:"answered_by_role"`
	AnsweredByHandle  string `json:"answered_by_handle"`
	PolicyRevision    int64  `json:"policy_revision,omitempty"`
	PolicyHash        string `json:"policy_hash,omitempty"`
	PreflightKind     string `json:"preflight_kind,omitempty"`
	PreflightSHA256   string `json:"preflight_sha256,omitempty"`
	PreflightPath     string `json:"preflight_path,omitempty"`
	VerifiedAt        string `json:"verified_at"`
}

type PreflightReceipt struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256"`
	Path   string `json:"path,omitempty"`
	OK     bool   `json:"ok"`
}

type Receipt struct {
	SchemaVersion     int              `json:"schema_version"`
	TaxonomyVersion   int              `json:"taxonomy_version,omitempty"`
	Gate              string           `json:"gate"`
	GateKind          string           `json:"gate_kind"`
	Action            string           `json:"action"`
	Target            string           `json:"target"`
	Note              string           `json:"note,omitempty"`
	Decision          string           `json:"decision"`
	ApprovalSource    string           `json:"approval_source"`
	SelfApproved      bool             `json:"self_approved"`
	QuestionMessageID string           `json:"question_message_id"`
	AnswerMessageID   string           `json:"answer_message_id"`
	AnsweredBy        string           `json:"answered_by"`
	PolicyRevision    int64            `json:"policy_revision,omitempty"`
	PolicyHash        string           `json:"policy_hash,omitempty"`
	Preflight         PreflightReceipt `json:"preflight"`
}

type receiptV1 struct {
	SchemaVersion     int              `json:"schema_version,omitempty"`
	Gate              string           `json:"gate"`
	GateKind          string           `json:"gate_kind"`
	Action            string           `json:"action"`
	Target            string           `json:"target"`
	Decision          string           `json:"decision"`
	ApprovalSource    string           `json:"approval_source"`
	SelfApproved      bool             `json:"self_approved"`
	QuestionMessageID string           `json:"question_message_id"`
	AnswerMessageID   string           `json:"answer_message_id"`
	AnsweredBy        string           `json:"answered_by"`
	PolicyRevision    int64            `json:"policy_revision,omitempty"`
	PolicyHash        string           `json:"policy_hash,omitempty"`
	Preflight         PreflightReceipt `json:"preflight"`
}

var canonicalGateThreadPattern = regexp.MustCompile(`^gate/[^/\s]+(/[^/\s]+)*$`)

// ValidateCanonicalSingleLineField rejects values that could escape their
// rendered field or be normalized differently by a reader. Optional fields
// may be empty; required fields may not.
func ValidateCanonicalSingleLineField(name, value string, required bool) error {
	if !utf8.ValidString(value) {
		return fmt.Errorf("%s must be valid UTF-8", name)
	}
	if value != strings.TrimSpace(value) {
		return fmt.Errorf("%s must be trim-canonical", name)
	}
	if required && value == "" {
		return fmt.Errorf("%s must be non-empty", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return fmt.Errorf("%s must be a single line without control characters", name)
		}
	}
	return nil
}

// CanonicalGateThread accepts a topic with or without the gate/ prefix and
// returns its exact canonical thread. Dot path segments are forbidden because
// gate topics are also used in durable path derivation.
func CanonicalGateThread(raw string) (string, error) {
	if err := ValidateCanonicalSingleLineField("gate", raw, true); err != nil {
		return "", err
	}
	topic := strings.TrimPrefix(raw, "gate/")
	if topic == "" || strings.HasPrefix(topic, "/") || strings.HasSuffix(topic, "/") {
		return "", fmt.Errorf("gate must contain a canonical topic")
	}
	for _, segment := range strings.Split(topic, "/") {
		if segment == "" || segment == "." || segment == ".." || strings.Contains(segment, `\`) || strings.IndexFunc(segment, unicode.IsSpace) >= 0 {
			return "", fmt.Errorf("gate contains invalid path segment %q", segment)
		}
	}
	gate := "gate/" + topic
	if !canonicalGateThreadPattern.MatchString(gate) {
		return "", fmt.Errorf("gate must contain a canonical topic")
	}
	return gate, nil
}

func ValidateCanonicalGateThread(gate string) error {
	canonical, err := CanonicalGateThread(gate)
	if err != nil {
		return err
	}
	if canonical != gate {
		return fmt.Errorf("gate must include the canonical gate/ prefix")
	}
	return nil
}

// ValidateTypedRenderedBinding verifies the exact human-readable mirror of a
// typed request. Unknown prose fields (for example Reason) are non-binding,
// while every security-bearing field must occur exactly once in writer form.
func ValidateTypedRenderedBinding(text string, request GateRequestContext) error {
	want := map[string]string{
		"gate-kind": "Gate-Kind: " + request.GateKind,
		"action":    "Action: " + request.Action,
		"target":    "Target: " + request.Target,
	}
	if request.Note != "" {
		want["note"] = "Note: " + request.Note
	}
	counts := map[string]int{}
	for _, line := range strings.Split(text, "\n") {
		key, _, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		expected, securityBearing := want[key]
		if key == "note" && request.Note == "" {
			return fmt.Errorf("typed binding must not contain Note when request note is empty")
		}
		if !securityBearing {
			continue
		}
		counts[key]++
		if line != expected {
			return fmt.Errorf("typed binding field %s is not exact canonical writer form", key)
		}
	}
	for key := range want {
		if counts[key] != 1 {
			return fmt.Errorf("typed binding requires exactly one canonical %s field", key)
		}
	}
	return nil
}

func DecodeGateRequest(raw any) (GateRequestContext, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return GateRequestContext{}, err
	}
	var request GateRequestContext
	if err := decodeStrictJSON(b, &request); err != nil {
		return GateRequestContext{}, fmt.Errorf("authorization request context: %w", err)
	}
	if err := ValidateGateRequest(request); err != nil {
		return GateRequestContext{}, fmt.Errorf("authorization request context: %w", err)
	}
	return request, nil
}

func DecodeApproval(raw any) (ApprovalContext, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return ApprovalContext{}, err
	}
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return ApprovalContext{}, fmt.Errorf("approval context: %w", err)
	}
	switch probe.SchemaVersion {
	case ApprovalSchemaVersionV1:
		var old approvalContextV1
		if err := decodeStrictJSON(b, &old); err != nil {
			return ApprovalContext{}, fmt.Errorf("approval context: %w", err)
		}
		return ApprovalContext{SchemaVersion: old.SchemaVersion, Source: old.Source, SelfApproved: old.SelfApproved, GateKind: old.GateKind, Action: old.Action, Target: old.Target, QuestionMessageID: old.QuestionMessageID, AnsweredByRole: old.AnsweredByRole, AnsweredByHandle: old.AnsweredByHandle, PolicyRevision: old.PolicyRevision, PolicyHash: old.PolicyHash, PreflightKind: old.PreflightKind, PreflightSHA256: old.PreflightSHA256, PreflightPath: old.PreflightPath, VerifiedAt: old.VerifiedAt}, nil
	case ApprovalSchemaVersion:
		var approval ApprovalContext
		if err := decodeStrictJSON(b, &approval); err != nil {
			return ApprovalContext{}, fmt.Errorf("approval context: %w", err)
		}
		return approval, nil
	default:
		return ApprovalContext{}, fmt.Errorf("approval context: unsupported schema_version %d", probe.SchemaVersion)
	}
}

func DecodeReceipt(b []byte) (Receipt, error) {
	var probe struct {
		SchemaVersion int `json:"schema_version"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return Receipt{}, fmt.Errorf("receipt: %w", err)
	}
	if probe.SchemaVersion == 0 || probe.SchemaVersion == ReceiptSchemaVersionV1 {
		var old receiptV1
		if err := decodeStrictJSON(b, &old); err != nil {
			return Receipt{}, fmt.Errorf("receipt: %w", err)
		}
		return Receipt{SchemaVersion: ReceiptSchemaVersionV1, Gate: old.Gate, GateKind: old.GateKind, Action: old.Action, Target: old.Target, Decision: old.Decision, ApprovalSource: old.ApprovalSource, SelfApproved: old.SelfApproved, QuestionMessageID: old.QuestionMessageID, AnswerMessageID: old.AnswerMessageID, AnsweredBy: old.AnsweredBy, PolicyRevision: old.PolicyRevision, PolicyHash: old.PolicyHash, Preflight: old.Preflight}, nil
	}
	if probe.SchemaVersion != ReceiptSchemaVersion {
		return Receipt{}, fmt.Errorf("receipt: unsupported schema_version %d", probe.SchemaVersion)
	}
	var receipt Receipt
	if err := decodeStrictJSON(b, &receipt); err != nil {
		return Receipt{}, fmt.Errorf("receipt: %w", err)
	}
	return receipt, nil
}

func decodeStrictJSON(b []byte, dst any) error {
	return DecodeStrictJSON(b, dst)
}

// DecodeStrictJSON rejects duplicate object keys recursively before applying
// unknown-field and trailing-value checks. encoding/json otherwise silently
// accepts the last duplicate, which is unsafe for authority-bearing data.
func DecodeStrictJSON(b []byte, dst any) error {
	if err := ValidateUnambiguousJSON(b); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return fmt.Errorf("must contain exactly one JSON value")
	}
	return nil
}

// ValidateUnambiguousJSON validates only representation-level safety: valid
// UTF-8, no duplicate object keys at any depth, and exactly one JSON value.
// It intentionally allows unknown fields so durable envelope parsers can keep
// their existing forward-compatible field policy.
func ValidateUnambiguousJSON(b []byte) error {
	if !utf8.Valid(b) {
		return fmt.Errorf("JSON must be valid UTF-8")
	}
	return rejectDuplicateJSONKeys(b)
}

func rejectDuplicateJSONKeys(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	var walk func() error
	walk = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]struct{}{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("JSON object key is not a string")
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("duplicate JSON object key %q", key)
				}
				seen[key] = struct{}{}
				if err := walk(); err != nil {
					return err
				}
			}
			closeToken, err := dec.Token()
			if err != nil || closeToken != json.Delim('}') {
				if err != nil {
					return err
				}
				return fmt.Errorf("malformed JSON object")
			}
		case '[':
			for dec.More() {
				if err := walk(); err != nil {
					return err
				}
			}
			closeToken, err := dec.Token()
			if err != nil || closeToken != json.Delim(']') {
				if err != nil {
					return err
				}
				return fmt.Errorf("malformed JSON array")
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter %q", delim)
		}
		return nil
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		if err != nil {
			return err
		}
		return fmt.Errorf("must contain exactly one JSON value")
	}
	return nil
}

func DecodeStrictEvidence(r io.Reader, dst any) ([]byte, string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}
	if err := decodeStrictJSON(b, dst); err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(b)
	return b, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ValidateGateRequest(r GateRequestContext) error {
	if r.SchemaVersion != GateRequestSchemaVersion {
		return fmt.Errorf("unsupported schema_version %d", r.SchemaVersion)
	}
	if r.TaxonomyVersion != ActionTaxonomyVersion {
		return fmt.Errorf("unsupported taxonomy_version %d", r.TaxonomyVersion)
	}
	item, err := ValidateGateAction(r.GateKind, r.Action)
	if err != nil {
		return err
	}
	if r.GateKind != item.GateKind || r.Action != item.Action {
		return fmt.Errorf("gate kind and action must be canonical")
	}
	if err := ValidateCanonicalGateThread(r.Gate); err != nil || r.Gate != r.Thread {
		return fmt.Errorf("gate and thread must carry the same exact canonical binding")
	}
	if err := ValidateCanonicalSingleLineField("target", r.Target, true); err != nil {
		return err
	}
	if err := ValidateCanonicalSingleLineField("note", r.Note, false); err != nil {
		return err
	}
	for name, value := range map[string]string{
		"project_dir":  r.Namespace.ProjectDir,
		"profile":      r.Namespace.Profile,
		"session":      r.Namespace.Session,
		"namespace_id": r.Namespace.NamespaceID,
		"generation":   r.Namespace.Generation,
	} {
		if value == "" || value != strings.TrimSpace(value) {
			return fmt.Errorf("namespace %s must be non-empty and trim-canonical", name)
		}
	}
	return nil
}

func ValidateApproval(a ApprovalContext) error {
	if a.SchemaVersion != ApprovalSchemaVersionV1 && a.SchemaVersion != ApprovalSchemaVersion {
		return fmt.Errorf("unsupported approval schema")
	}
	if a.Source != "human" && a.Source != "self_operator" {
		return fmt.Errorf("unsupported approval source %q", a.Source)
	}
	if (a.Source == "self_operator") != a.SelfApproved {
		return fmt.Errorf("approval source/self_approved mismatch")
	}
	if a.SchemaVersion == ApprovalSchemaVersion {
		if a.TaxonomyVersion != ActionTaxonomyVersion {
			return fmt.Errorf("unsupported approval taxonomy")
		}
		item, err := ValidateGateAction(a.GateKind, a.Action)
		if err != nil {
			return err
		}
		if a.GateKind != item.GateKind || a.Action != item.Action {
			return fmt.Errorf("approval gate kind and action must be canonical")
		}
		if err := ValidateCanonicalSingleLineField("approval target", a.Target, true); err != nil {
			return err
		}
		if err := ValidateCanonicalSingleLineField("approval note", a.Note, false); err != nil {
			return err
		}
	} else if _, err := NormalizeGateKind(a.GateKind); err != nil {
		return err
	}
	if strings.TrimSpace(a.Action) == "" || strings.TrimSpace(a.Target) == "" || strings.TrimSpace(a.QuestionMessageID) == "" || strings.TrimSpace(a.AnsweredByHandle) == "" {
		return fmt.Errorf("approval context is missing required binding")
	}
	if _, err := time.Parse(time.RFC3339Nano, a.VerifiedAt); err != nil {
		return fmt.Errorf("approval verified_at: %w", err)
	}
	return nil
}
