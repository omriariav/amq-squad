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

func observedReleaseReceipts(prepared PreparedReleaseManifest) map[string]ReleaseDeliveryReceiptTuple {
	result := map[string]ReleaseDeliveryReceiptTuple{}
	for _, child := range prepared.Children {
		messageID := "question-" + child.Role
		result[child.Role] = ReleaseDeliveryReceiptTuple{
			AttemptID: child.Receipt.AttemptID, Kind: child.Receipt.Kind, Sender: child.Receipt.Sender,
			Recipients: []string{child.Receipt.Recipient}, Thread: child.Receipt.Thread, MessageID: messageID,
			Path: "/repo/.amq-squad/receipts/" + child.Role + ".json", Root: "/repo/.agent-mail/issue-414",
			NamespaceID: child.Receipt.NamespaceID, TargetIdentity: child.Receipt.TargetIdentity, AdoptedGeneration: child.Receipt.MinimumGeneration,
		}
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
		if child.ReleaseChild.RenderedSHA256 != child.RenderedSHA256 || child.ReleaseChild.AttemptID != child.Receipt.AttemptID || child.Receipt.Thread != child.Thread || child.Receipt.Sender != spec.RequesterHandle || child.Receipt.Recipient != spec.OperatorHandle {
			t.Fatalf("child[%d] marker/receipt mismatch: %+v", i, child)
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
		"unknown":                      strings.Replace(string(b), `"schema_version":2`, `"schema_version":2,"unknown":1`, 1),
		"trailing":                     string(b) + ` null`,
		"schema":                       strings.Replace(string(b), `"schema_version":2`, `"schema_version":9`, 1),
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

func TestActiveReleaseAdoptsOnlyExactQuestionsAndOwnedReceipts(t *testing.T) {
	prepared, err := DerivePreparedRelease(validReleaseSpec(), 1)
	if err != nil {
		t.Fatal(err)
	}
	observed := observedReleaseReceipts(prepared)
	active, err := NewActiveRelease(prepared, observed)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateActiveRelease(prepared, active); err != nil {
		t.Fatal(err)
	}
	tagReceipt := observed[ReleaseChildTag]
	tagReceipt.Recipients[0] = "mutated-after-build"
	observed[ReleaseChildTag] = tagReceipt
	if err := ValidateActiveRelease(prepared, active); err != nil {
		t.Fatalf("active manifest retained caller-owned recipient slice: %v", err)
	}
	b, _ := json.Marshal(active)
	if strings.Contains(string(b), "answer") || strings.Contains(string(b), "approval") {
		t.Fatalf("activation contains answer/approval authority: %s", b)
	}
	active.Children[0].Receipt.AttemptID = active.Children[1].Receipt.AttemptID
	if err := ValidateActiveRelease(prepared, active); err == nil {
		t.Fatal("swapped receipt identity unexpectedly valid")
	}
}

func TestReleaseDeliveryReceiptDigestIsValidatedDomainSeparatedAndOrderExact(t *testing.T) {
	prepared, err := DerivePreparedRelease(validReleaseSpec(), 1)
	if err != nil {
		t.Fatal(err)
	}
	receipt := observedReleaseReceipts(prepared)[ReleaseChildTag]
	digest, err := ReleaseDeliveryReceiptSHA256(receipt)
	if err != nil || !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("digest=(%q,%v)", digest, err)
	}
	again, err := ReleaseDeliveryReceiptSHA256(receipt)
	if err != nil || again != digest {
		t.Fatalf("deterministic digest=(%q,%v), want %q", again, err, digest)
	}
	for name, mutate := range map[string]func(*ReleaseDeliveryReceiptTuple){
		"attempt": func(r *ReleaseDeliveryReceiptTuple) {
			r.AttemptID = "release-attempt-v2-" + strings.Repeat("a", 64)
		},
		"kind":       func(r *ReleaseDeliveryReceiptTuple) { r.Kind += "-other" },
		"sender":     func(r *ReleaseDeliveryReceiptTuple) { r.Sender = "other" },
		"recipients": func(r *ReleaseDeliveryReceiptTuple) { r.Recipients = []string{"other"} },
		"thread":     func(r *ReleaseDeliveryReceiptTuple) { r.Thread = "gate/other" },
		"message":    func(r *ReleaseDeliveryReceiptTuple) { r.MessageID += "-other" },
		"path":       func(r *ReleaseDeliveryReceiptTuple) { r.Path += "-other" },
		"root":       func(r *ReleaseDeliveryReceiptTuple) { r.Root += "-other" },
		"namespace":  func(r *ReleaseDeliveryReceiptTuple) { r.NamespaceID = "other/session" },
		"target": func(r *ReleaseDeliveryReceiptTuple) {
			r.TargetIdentity = "release-receipt-target-v1-" + strings.Repeat("a", 64)
		},
		"generation": func(r *ReleaseDeliveryReceiptTuple) { r.AdoptedGeneration++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := receipt
			changed.Recipients = append([]string(nil), receipt.Recipients...)
			mutate(&changed)
			got, err := ReleaseDeliveryReceiptSHA256(changed)
			if err == nil && got == digest {
				t.Fatalf("%s mutation preserved digest", name)
			}
		})
	}
	two := receipt
	two.Recipients = []string{"first", "second"}
	forward, err := ReleaseDeliveryReceiptSHA256(two)
	if err != nil {
		t.Fatal(err)
	}
	two.Recipients = []string{"second", "first"}
	reverse, err := ReleaseDeliveryReceiptSHA256(two)
	if err != nil || forward == reverse {
		t.Fatalf("ordered recipient digests forward=%q reverse=%q err=%v", forward, reverse, err)
	}
	two.Recipients = []string{"same", "same"}
	if _, err := ReleaseDeliveryReceiptSHA256(two); err == nil {
		t.Fatal("duplicate recipients accepted")
	}
	bad := receipt
	bad.Path = "relative/receipt.json"
	if _, err := ReleaseDeliveryReceiptSHA256(bad); err == nil {
		t.Fatal("relative receipt path accepted")
	}
}

func TestActiveReleaseRejectsCrossChildIdentityReuse(t *testing.T) {
	prepared, err := DerivePreparedRelease(validReleaseSpec(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(map[string]ReleaseDeliveryReceiptTuple){
		"question id": func(r map[string]ReleaseDeliveryReceiptTuple) {
			v := r[ReleaseChildGitHubRelease]
			v.MessageID = r[ReleaseChildTag].MessageID
			r[ReleaseChildGitHubRelease] = v
		},
		"attempt id": func(r map[string]ReleaseDeliveryReceiptTuple) {
			v := r[ReleaseChildGitHubRelease]
			v.AttemptID = r[ReleaseChildTag].AttemptID
			r[ReleaseChildGitHubRelease] = v
		},
		"receipt path": func(r map[string]ReleaseDeliveryReceiptTuple) {
			v := r[ReleaseChildGitHubRelease]
			v.Path = r[ReleaseChildTag].Path
			r[ReleaseChildGitHubRelease] = v
		},
	} {
		t.Run(name, func(t *testing.T) {
			receipts := observedReleaseReceipts(prepared)
			mutate(receipts)
			if _, err := NewActiveRelease(prepared, receipts); err == nil {
				t.Fatalf("duplicate %s accepted", name)
			}
		})
	}
	receipts := observedReleaseReceipts(prepared)
	delete(receipts, ReleaseChildTag)
	receipts["unknown"] = receipts[ReleaseChildGitHubRelease]
	if _, err := NewActiveRelease(prepared, receipts); err == nil {
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
	duplicateReceipt := strings.Replace(string(preparedJSON), `"attempt_id":"`, `"attempt_id":"duplicate","attempt_id":"`, 1)
	if err := DecodeStrictJSON([]byte(duplicateReceipt), &decodedPrepared); err == nil {
		t.Fatal("duplicate prepared receipt key accepted")
	}

	active, err := NewActiveRelease(prepared, observedReleaseReceipts(prepared))
	if err != nil {
		t.Fatal(err)
	}
	activeJSON, _ := json.Marshal(active)
	duplicateActiveReceipt := strings.Replace(string(activeJSON), `"path":"`, `"path":"/duplicate","path":"`, 1)
	var decodedActive ActiveReleaseManifest
	if err := DecodeStrictJSON([]byte(duplicateActiveReceipt), &decodedActive); err == nil {
		t.Fatal("duplicate active receipt key accepted")
	}
}
