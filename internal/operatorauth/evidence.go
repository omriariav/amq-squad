package operatorauth

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

const ApprovalSchemaVersion = 1

type ApprovalContext struct {
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

func DecodeApproval(raw any) (ApprovalContext, error) {
	b, err := json.Marshal(raw)
	if err != nil {
		return ApprovalContext{}, err
	}
	var approval ApprovalContext
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&approval); err != nil {
		return ApprovalContext{}, fmt.Errorf("approval context: %w", err)
	}
	if approval.SchemaVersion != ApprovalSchemaVersion {
		return ApprovalContext{}, fmt.Errorf("approval context: unsupported schema_version %d", approval.SchemaVersion)
	}
	return approval, nil
}

func DecodeStrictEvidence(r io.Reader, dst any) ([]byte, string, error) {
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, "", err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return nil, "", err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, "", fmt.Errorf("evidence must contain exactly one JSON value")
	}
	sum := sha256.Sum256(b)
	return b, "sha256:" + hex.EncodeToString(sum[:]), nil
}

func ValidateApproval(a ApprovalContext) error {
	if a.SchemaVersion != ApprovalSchemaVersion {
		return fmt.Errorf("unsupported approval schema")
	}
	if a.Source != "human" && a.Source != "self_operator" {
		return fmt.Errorf("unsupported approval source %q", a.Source)
	}
	if (a.Source == "self_operator") != a.SelfApproved {
		return fmt.Errorf("approval source/self_approved mismatch")
	}
	if _, err := NormalizeGateKind(a.GateKind); err != nil {
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
