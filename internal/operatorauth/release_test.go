package operatorauth

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func validReleaseSpec() ReleaseSpec {
	return ReleaseSpec{
		SchemaVersion: ReleaseSchemaVersion, TaxonomyVersion: ActionTaxonomyVersion,
		Namespace:  NamespaceBinding{ProjectDir: "/repo", Profile: "default", Session: "issue-414", NamespaceID: "default/issue-414", Generation: "none"},
		ParentGate: "gate/release-414", RequesterHandle: "cto", OperatorHandle: "user",
		TagTarget: "v2.20.1", GitHubReleaseTarget: "release v2.20.1 from exact commit deadbeef",
		Note: ReleaseNote{Summary: "publish the accepted Stage B artifacts"},
	}
}

func observedReleaseMessageIDs(prepared PreparedReleaseManifest) map[string]string {
	result := map[string]string{}
	for _, child := range prepared.Children {
		result[child.Role] = "question-" + child.Role
	}
	return result
}

func TestReleaseSpecStrictDecode(t *testing.T) {
	spec := validReleaseSpec()
	b, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeReleaseSpec(json.RawMessage(b))
	if err != nil || !reflect.DeepEqual(got, spec) {
		t.Fatalf("DecodeReleaseSpec()=(%+v,%v)", got, err)
	}

	cases := map[string]string{
		"unknown":             strings.Replace(string(b), `"schema_version":1`, `"schema_version":1,"extra":true`, 1),
		"trailing":            string(b) + ` {}`,
		"schema":              strings.Replace(string(b), `"schema_version":1`, `"schema_version":2`, 1),
		"taxonomy":            strings.Replace(string(b), `"taxonomy_version":1`, `"taxonomy_version":2`, 1),
		"duplicate top":       strings.Replace(string(b), `"schema_version":1`, `"schema_version":1,"schema_version":1`, 1),
		"duplicate namespace": strings.Replace(string(b), `"project_dir":"/repo"`, `"project_dir":"/repo","project_dir":"/repo"`, 1),
		"duplicate note":      strings.Replace(string(b), `"summary":"publish the accepted Stage B artifacts"`, `"summary":"publish the accepted Stage B artifacts","summary":"other"`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeReleaseSpec(raw); err == nil {
				t.Fatalf("DecodeReleaseSpec(%s) unexpectedly succeeded", name)
			}
		})
	}
}

func TestReleaseSpecRejectsWhitespaceAndControls(t *testing.T) {
	for name, mutate := range map[string]func(*ReleaseSpec){
		"target whitespace": func(s *ReleaseSpec) { s.TagTarget = " v1" },
		"target newline":    func(s *ReleaseSpec) { s.GitHubReleaseTarget = "release\nother" },
		"note separator":    func(s *ReleaseSpec) { s.Note.Summary = "one\u2028two" },
		"handle tab":        func(s *ReleaseSpec) { s.RequesterHandle = "c\tto" },
		"parent traversal":  func(s *ReleaseSpec) { s.ParentGate = "gate/release/../other" },
	} {
		t.Run(name, func(t *testing.T) {
			spec := validReleaseSpec()
			mutate(&spec)
			if err := ValidateReleaseSpec(spec); err == nil {
				t.Fatalf("ValidateReleaseSpec(%s) unexpectedly succeeded", name)
			}
		})
	}
}

func TestReleaseDerivationIsFixedAndDeterministic(t *testing.T) {
	spec := validReleaseSpec()
	a, err := DerivePreparedRelease(spec, 7)
	if err != nil {
		t.Fatal(err)
	}
	b, err := DerivePreparedRelease(spec, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(a, b) {
		t.Fatal("identical derivation differed")
	}
	if len(a.Children) != 2 {
		t.Fatalf("children=%d", len(a.Children))
	}
	want := []struct {
		role, kind, action, target, suffix string
	}{
		{ReleaseChildTag, GateTag, "tag", spec.TagTarget, "/g00000000000000000007/00-tag"},
		{ReleaseChildGitHubRelease, GateRelease, "github_release", spec.GitHubReleaseTarget, "/g00000000000000000007/01-github-release"},
	}
	for i, child := range a.Children {
		if child.Ordinal != i || child.Role != want[i].role || child.GateKind != want[i].kind || child.Action != want[i].action || child.Target != want[i].target || !strings.HasSuffix(child.Thread, want[i].suffix) {
			t.Fatalf("child[%d]=%+v", i, child)
		}
		if child.ReleaseChild.RenderedSHA256 != child.RenderedSHA256 || child.ReleaseChild.Thread != child.Thread || child.ReleaseChild.Target != child.Target {
			t.Fatalf("child[%d] marker mismatch: %+v", i, child)
		}
		if child.ReleaseChild.PreparedManifestID != a.PreparedManifestID {
			t.Fatalf("child[%d] does not bind prepared manifest", i)
		}
	}
	if _, err := CanonicalAction("release"); err == nil {
		t.Fatal("generic release unexpectedly entered atomic catalog")
	}
}

func TestPreparedReleaseMutationFailsExactValidation(t *testing.T) {
	prepared, err := DerivePreparedRelease(validReleaseSpec(), 1)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Children[0], prepared.Children[1] = prepared.Children[1], prepared.Children[0]
	if err := ValidatePreparedRelease(prepared); err == nil {
		t.Fatal("reordered child list unexpectedly valid")
	}
	prepared, _ = DerivePreparedRelease(validReleaseSpec(), 1)
	prepared.Children[0].ReleaseChild.AttemptID = prepared.Children[1].ReleaseChild.AttemptID
	if err := ValidatePreparedRelease(prepared); err == nil {
		t.Fatal("well-formed divergent marker attempt unexpectedly valid")
	}
}

func TestReleaseChildStrictDecode(t *testing.T) {
	prepared, err := DerivePreparedRelease(validReleaseSpec(), 1)
	if err != nil {
		t.Fatal(err)
	}
	marker := prepared.Children[0].ReleaseChild
	b, _ := json.Marshal(marker)
	if got, err := DecodeReleaseChild(json.RawMessage(b)); err != nil || !reflect.DeepEqual(got, marker) {
		t.Fatalf("DecodeReleaseChild()=(%+v,%v)", got, err)
	}
	for name, raw := range map[string]string{
		"unknown":                      strings.Replace(string(b), `"schema_version":3`, `"schema_version":3,"unknown":1`, 1),
		"trailing":                     string(b) + ` null`,
		"schema":                       strings.Replace(string(b), `"schema_version":3`, `"schema_version":9`, 1),
		"taxonomy":                     strings.Replace(string(b), `"taxonomy_version":1`, `"taxonomy_version":9`, 1),
		"ordinal":                      strings.Replace(string(b), `"ordinal":0`, `"ordinal":1`, 1),
		"action":                       strings.Replace(string(b), `"action":"tag"`, `"action":"github_release"`, 1),
		"well formed attempt mismatch": strings.Replace(string(b), marker.AttemptID, "release-attempt-v2-"+strings.Repeat("a", 64), 1),
		"duplicate nested":             strings.Replace(string(b), `"role":"tag"`, `"role":"tag","role":"tag"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeReleaseChild(raw); err == nil {
				t.Fatalf("malformed %s marker accepted", name)
			}
		})
	}
}

func TestActiveReleaseAdoptsOnlyAcceptedQuestionMessageIDs(t *testing.T) {
	prepared, err := DerivePreparedRelease(validReleaseSpec(), 1)
	if err != nil {
		t.Fatal(err)
	}
	observed := observedReleaseMessageIDs(prepared)
	active, err := NewActiveRelease(prepared, observed)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateActiveRelease(prepared, active); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(active)
	if strings.Contains(string(b), "answer") || strings.Contains(string(b), "approval") {
		t.Fatalf("activation contains answer/approval authority: %s", b)
	}
	active.Children[0].QuestionMessageID = active.Children[1].QuestionMessageID
	if err := ValidateActiveRelease(prepared, active); err == nil {
		t.Fatal("duplicate accepted message identity unexpectedly valid")
	}
}

func TestActiveReleaseRejectsCrossChildIdentityReuse(t *testing.T) {
	prepared, err := DerivePreparedRelease(validReleaseSpec(), 1)
	if err != nil {
		t.Fatal(err)
	}
	messageIDs := observedReleaseMessageIDs(prepared)
	messageIDs[ReleaseChildGitHubRelease] = messageIDs[ReleaseChildTag]
	if _, err := NewActiveRelease(prepared, messageIDs); err == nil {
		t.Fatal("duplicate accepted message id was accepted")
	}
	messageIDs = observedReleaseMessageIDs(prepared)
	delete(messageIDs, ReleaseChildTag)
	messageIDs["unknown"] = messageIDs[ReleaseChildGitHubRelease]
	if _, err := NewActiveRelease(prepared, messageIDs); err == nil {
		t.Fatal("unknown/missing role set accepted")
	}
}

func TestStrictReleaseManifestDecodeRejectsNestedDuplicates(t *testing.T) {
	prepared, err := DerivePreparedRelease(validReleaseSpec(), 1)
	if err != nil {
		t.Fatal(err)
	}
	preparedJSON, _ := json.Marshal(prepared)
	duplicateChild := strings.Replace(string(preparedJSON), `"role":"tag"`, `"role":"tag","role":"tag"`, 1)
	var decodedPrepared PreparedReleaseManifest
	if err := DecodeStrictJSON([]byte(duplicateChild), &decodedPrepared); err == nil {
		t.Fatal("duplicate child key accepted")
	}
	duplicateAttempt := strings.Replace(string(preparedJSON), `"attempt_id":"`, `"attempt_id":"duplicate","attempt_id":"`, 1)
	if err := DecodeStrictJSON([]byte(duplicateAttempt), &decodedPrepared); err == nil {
		t.Fatal("duplicate prepared attempt key accepted")
	}

	active, err := NewActiveRelease(prepared, observedReleaseMessageIDs(prepared))
	if err != nil {
		t.Fatal(err)
	}
	activeJSON, _ := json.Marshal(active)
	duplicateActiveMessage := strings.Replace(string(activeJSON), `"question_message_id":"`, `"question_message_id":"duplicate","question_message_id":"`, 1)
	var decodedActive ActiveReleaseManifest
	if err := DecodeStrictJSON([]byte(duplicateActiveMessage), &decodedActive); err == nil {
		t.Fatal("duplicate active message key accepted")
	}
}
