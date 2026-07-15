package operatorauth

import (
	"encoding/json"
	"strings"
	"testing"
)

func validGateRequest() GateRequestContext {
	return GateRequestContext{
		SchemaVersion: GateRequestSchemaVersion, TaxonomyVersion: ActionTaxonomyVersion,
		Gate: "gate/merge-pr-414", Thread: "gate/merge-pr-414",
		Namespace: NamespaceBinding{ProjectDir: "/repo", Profile: "default", Session: "stage-a", NamespaceID: "ns-1", Generation: "gen-1"},
		GateKind:  GateMerge, Action: "protected_branch_push", Target: "PR #414 head abcdef0 into main", Note: "reviewed",
	}
}

func TestGateRequestRequiresStrictCanonicalBinding(t *testing.T) {
	if err := ValidateGateRequest(validGateRequest()); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*GateRequestContext){
		func(r *GateRequestContext) { r.Gate = "merge-pr-414" },
		func(r *GateRequestContext) { r.Gate = "gate//merge-pr-414"; r.Thread = r.Gate },
		func(r *GateRequestContext) { r.Gate = "gate/merge pr-414"; r.Thread = r.Gate },
		func(r *GateRequestContext) { r.Thread = "gate/other" },
		func(r *GateRequestContext) { r.Action = "push_protected_branch" },
		func(r *GateRequestContext) { r.Target = " target " },
		func(r *GateRequestContext) { r.Note = " note " },
	} {
		r := validGateRequest()
		mutate(&r)
		if err := ValidateGateRequest(r); err == nil {
			t.Errorf("accepted malformed request: %+v", r)
		}
	}
}

func TestCanonicalSingleLineFieldsRejectInjectionAndNormalization(t *testing.T) {
	bad := []string{
		" leading", "trailing ", "line\rbreak", "line\nbreak", "nul\x00byte", "c0\x01byte", "unit\x1fseparator",
		"delete\x7fbyte", "c1\u0085byte", "unicode\u2028line", "unicode\u2029paragraph", string([]byte{'b', 'a', 'd', 0xff}),
	}
	for _, value := range bad {
		if err := ValidateCanonicalSingleLineField("target", value, true); err == nil {
			t.Errorf("accepted unsafe field %q", value)
		}
	}
	for _, value := range []string{"safe prose", "two  internal spaces", "café / שלום"} {
		if err := ValidateCanonicalSingleLineField("target", value, true); err != nil {
			t.Errorf("rejected safe field %q: %v", value, err)
		}
	}
}

func TestCanonicalGateRejectsPathAliasesAndControls(t *testing.T) {
	for _, gate := range []string{
		"gate/", "gate//x", "gate/./x", "gate/../x", "gate/x/.", "gate/x/..", `gate/a\b`,
		"gate/x\nother", " gate/x", "gate/x ", "gate/x\u2028other",
	} {
		if _, err := CanonicalGateThread(gate); err == nil {
			t.Errorf("accepted unsafe gate %q", gate)
		}
	}
	for _, input := range []string{"safe", "gate/safe", "gate/a/b-c_1"} {
		if _, err := CanonicalGateThread(input); err != nil {
			t.Errorf("rejected safe gate %q: %v", input, err)
		}
	}
}

func TestTypedRenderedBindingIsExact(t *testing.T) {
	r := validGateRequest()
	valid := "Gate-Kind: merge\nAction: protected_branch_push\nTarget: PR #414 head abcdef0 into main\nNote: reviewed\nReason: safe prose"
	if err := ValidateTypedRenderedBinding(valid, r); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		strings.Replace(valid, "\nNote: reviewed", "", 1),
		valid + "\nTarget: other",
		strings.Replace(valid, "Note: reviewed", "Note: reviewed ", 1),
		strings.Replace(valid, "Target: PR #414 head abcdef0 into main", "Target: alias", 1),
	} {
		if err := ValidateTypedRenderedBinding(body, r); err == nil {
			t.Errorf("accepted malformed rendered binding %q", body)
		}
	}
	r.Note = ""
	withoutNote := "Gate-Kind: merge\nAction: protected_branch_push\nTarget: PR #414 head abcdef0 into main"
	if err := ValidateTypedRenderedBinding(withoutNote, r); err != nil {
		t.Fatal(err)
	}
	if err := ValidateTypedRenderedBinding(withoutNote+"\nNote: surprise", r); err == nil {
		t.Fatal("accepted unexpected Note line")
	}
}

func TestGateRequestNamespaceFieldsAreTrimCanonical(t *testing.T) {
	for _, mutate := range []func(*NamespaceBinding){
		func(n *NamespaceBinding) { n.ProjectDir = " /repo" },
		func(n *NamespaceBinding) { n.Profile = "default " },
		func(n *NamespaceBinding) { n.Session = "\tstage-a" },
		func(n *NamespaceBinding) { n.NamespaceID = "" },
		func(n *NamespaceBinding) { n.Generation = " gen-1 " },
	} {
		r := validGateRequest()
		mutate(&r.Namespace)
		if err := ValidateGateRequest(r); err == nil {
			t.Errorf("accepted non-canonical namespace: %+v", r.Namespace)
		}
	}
}

func TestDecodeGateRequestRejectsUnknownFields(t *testing.T) {
	raw, err := json.Marshal(validGateRequest())
	if err != nil {
		t.Fatal(err)
	}
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	request["unknown"] = true
	if _, err := DecodeGateRequest(request); err == nil {
		t.Fatal("unknown authorization request field accepted")
	}
}

func TestDecodeReceiptReadsLegacyAndExplicitV1(t *testing.T) {
	const body = `"gate":"gate/x","gate_kind":"merge","action":"protected_branch_push","target":"PR #1 head abcdef0 into main","decision":"approved","approval_source":"human","self_approved":false,"question_message_id":"q","answer_message_id":"a","answered_by":"user","preflight":{"kind":"","sha256":"","ok":false}`
	for name, raw := range map[string]string{
		"legacy without schema": `{` + body + `}`,
		"explicit v1":           `{"schema_version":1,` + body + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeReceipt([]byte(raw))
			if err != nil {
				t.Fatal(err)
			}
			if got.SchemaVersion != ReceiptSchemaVersionV1 || got.Gate != "gate/x" || got.Action != "protected_branch_push" {
				t.Fatalf("decoded receipt = %+v", got)
			}
		})
	}
}

func TestStrictEvidenceRejectsUnknownFields(t *testing.T) {
	var approval ApprovalContext
	_, _, err := DecodeStrictEvidence(strings.NewReader(`{"schema_version":1,"source":"human","unknown":true}`), &approval)
	if err == nil {
		t.Fatal("unknown evidence field accepted")
	}
}

func TestStageAEvidenceRejectsRecursiveDuplicateKeysAndInvalidUTF8(t *testing.T) {
	b, err := json.Marshal(validGateRequest())
	if err != nil {
		t.Fatal(err)
	}
	duplicate := strings.Replace(string(b), `"project_dir":"/repo"`, `"project_dir":"/repo","project_dir":"/other"`, 1)
	if _, err := DecodeGateRequest(json.RawMessage(duplicate)); err == nil || !strings.Contains(err.Error(), "duplicate JSON object key") {
		t.Fatalf("duplicate nested Stage A key error=%v", err)
	}
	invalid := append([]byte(`{"schema_version":1,"taxonomy_version":1,"gate":"gate/x","thread":"gate/x","namespace":{"project_dir":"/repo","profile":"default","session":"s","namespace_id":"default/s","generation":"none"},"gate_kind":"tag","action":"tag","target":"`), 0xff)
	invalid = append(invalid, []byte(`"}`)...)
	if _, err := DecodeGateRequest(json.RawMessage(invalid)); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("invalid UTF-8 Stage A evidence error=%v", err)
	}
}
