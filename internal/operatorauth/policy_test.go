package operatorauth

import (
	"reflect"
	"testing"
)

func TestActionCatalogIsTheAllowedPairAuthority(t *testing.T) {
	want := []ActionCapability{
		{Action: "default_branch_push", GateKind: GateMerge, Aliases: []string{"push_default_branch"}, SelfEligible: true},
		{Action: "destructive_filesystem", GateKind: GateDestructiveFilesystem, Aliases: []string{"destructive_fs"}, HumanOnly: true},
		{Action: "external_send", GateKind: GateExternalSend, Aliases: []string{"external_message"}, HumanOnly: true},
		{Action: "feature_branch_push", GateKind: GateFeatureBranchPush, Aliases: []string{"push_feature_branch"}, SelfEligible: true},
		{Action: "github_release", GateKind: GateRelease, Aliases: []string{"gh_release"}, HumanOnly: true},
		{Action: "package_publish", GateKind: GatePublish, HumanOnly: true},
		{Action: "protected_branch_push", GateKind: GateMerge, Aliases: []string{"push_protected_branch"}, SelfEligible: true},
		{Action: "spawn", GateKind: GateSpawn, HumanOnly: true},
		{Action: "tag", GateKind: GateTag, Aliases: []string{"create_tag", "push_tag", "tag_push"}, HumanOnly: true},
	}
	got := ActionCapabilities()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("action catalog = %#v, want %#v", got, want)
	}
	got[0].Aliases[0] = "mutated"
	if again := ActionCapabilities(); reflect.DeepEqual(got, again) || again[0].Aliases[0] != "push_default_branch" {
		t.Fatalf("ActionCapabilities exposed mutable catalog storage: %#v", again)
	}
}

func TestCatalogValidationRejectsAmbiguityAndInvalidCapabilities(t *testing.T) {
	base := ActionCapability{Action: "one", GateKind: "gate", HumanOnly: true}
	for name, items := range map[string][]ActionCapability{
		"empty catalog":       nil,
		"empty action":        {{GateKind: "gate", HumanOnly: true}},
		"empty gate":          {{Action: "one", HumanOnly: true}},
		"noncanonical action": {{Action: "One Action", GateKind: "gate", HumanOnly: true}},
		"empty alias":         {{Action: "one", GateKind: "gate", Aliases: []string{""}, HumanOnly: true}},
		"duplicate action":    {base, base},
		"duplicate alias":     {base, {Action: "two", GateKind: "gate", Aliases: []string{"one"}, HumanOnly: true}},
		"conflicting modes":   {{Action: "one", GateKind: "gate", HumanOnly: true, SelfEligible: true}},
		"missing mode":        {{Action: "one", GateKind: "gate"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateActionCatalog(items); err == nil {
				t.Fatal("invalid catalog was accepted")
			}
		})
	}
}

func TestCatalogValidationSortsActionsAndAliases(t *testing.T) {
	got, err := validateActionCatalog([]ActionCapability{
		{Action: "z", GateKind: "gate", Aliases: []string{"z_two", "z_one"}, HumanOnly: true},
		{Action: "a", GateKind: "gate", SelfEligible: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Action != "a" || !reflect.DeepEqual(got[1].Aliases, []string{"z_one", "z_two"}) {
		t.Fatalf("catalog was not deterministic: %#v", got)
	}
}

func TestActionCatalogAliasesAndExcludedActions(t *testing.T) {
	for alias, canonical := range map[string]string{
		"push default branch":   "default_branch_push",
		"push-feature-branch":   "feature_branch_push",
		"push_protected_branch": "protected_branch_push",
		"GH-RELEASE":            "github_release",
		"create tag":            "tag",
	} {
		got, err := CanonicalAction(alias)
		if err != nil || got != canonical {
			t.Errorf("CanonicalAction(%q) = %q, %v; want %q", alias, got, err, canonical)
		}
	}
	for _, action := range []string{"force_push_feature_branch", "force-push-feature-branch", "force_push", "release", "unknown"} {
		if _, err := CanonicalAction(action); err == nil {
			t.Errorf("excluded action %q was accepted", action)
		}
	}
}

func TestImmutableExclusionsAndBindings(t *testing.T) {
	for _, kind := range []string{GateSpawn, GateRelease, GateTag, GatePublish, GateExternalSend, GateDestructiveFilesystem, "unknown"} {
		if _, err := ValidateAllowlist([]string{kind}); err == nil {
			t.Fatalf("allowlist accepted human-only %q", kind)
		}
	}
	if err := Evaluate(GateMerge, "protected_branch_push", []string{GateMerge}); err != nil {
		t.Fatal(err)
	}
	if err := Evaluate(GateFeatureBranchPush, "feature_branch_push", []string{GateFeatureBranchPush}); err != nil {
		t.Fatal(err)
	}
	if err := Evaluate(GateMerge, "default_branch_push", []string{GateMerge}); err != nil {
		t.Fatalf("default branch push is not an eligible merge capability: %v", err)
	}
	for _, tc := range []struct{ kind, action string }{{GateSpawn, "github_release"}, {GatePublish, "default_branch_push"}, {GateFeatureBranchPush, "protected_branch_push"}} {
		if err := Evaluate(tc.kind, tc.action, []string{tc.kind}); err == nil {
			t.Fatalf("accepted gate/action mismatch %+v", tc)
		}
	}
}

func TestHumanPrecedence(t *testing.T) {
	facts := []MessageFact{{ID: "s", From: "cto", Bound: true, Decision: "approved", After: true}, {ID: "h", From: "user", Bound: true, Decision: "denied", After: true}}
	got := ResolvePrecedence(facts, "user", "cto")
	if got.Source != "human" || got.Decision != "denied" {
		t.Fatalf("precedence = %+v", got)
	}
	facts[1] = MessageFact{ID: "h", From: "user", After: true}
	if got := ResolvePrecedence(facts, "user", "cto"); !got.Barrier {
		t.Fatalf("human intervention did not create barrier: %+v", got)
	}
}

func TestHumanPrecedenceIsOrderIndependent(t *testing.T) {
	facts := []MessageFact{{ID: "s2", From: "cto", Bound: true, Decision: "approved", After: true}, {ID: "h1", From: "user", After: true}, {ID: "h3", From: "user", Bound: true, Decision: "approved", After: true}, {ID: "h2", From: "user", Bound: true, Decision: "denied", After: true}}
	for _, ordered := range [][]MessageFact{facts, {facts[3], facts[1], facts[0], facts[2]}} {
		got := ResolvePrecedence(ordered, "user", "cto")
		if got.Decision != "denied" || got.MessageID != "h2" {
			t.Fatalf("order-dependent precedence: %+v", got)
		}
	}
}

func TestHumanPrecedenceCompleteOrderingAndSameTimestamp(t *testing.T) {
	facts := []MessageFact{
		{ID: "s9", From: "cto", Bound: true, Decision: "approved", After: true, Order: 99},
		{ID: "h2", From: "user", Bound: false, After: true, Order: 20},
		{ID: "h3", From: "user", Bound: true, Decision: "approved", After: true, Order: 10},
		{ID: "h1", From: "user", Bound: true, Decision: "denied", After: true, Order: 1},
	}
	if got := ResolvePrecedence(facts, "user", "cto"); got.Decision != "denied" || got.MessageID != "h1" {
		t.Fatalf("denial did not outrank all: %+v", got)
	}
	if got := ResolvePrecedence(facts[:3], "user", "cto"); got.Decision != "approved" || got.MessageID != "h3" {
		t.Fatalf("approval did not outrank barrier/self: %+v", got)
	}
	if got := ResolvePrecedence(facts[:2], "user", "cto"); !got.Barrier || got.MessageID != "h2" {
		t.Fatalf("barrier did not outrank self: %+v", got)
	}
	same := []MessageFact{{ID: "a", From: "user", Bound: true, Decision: "denied", After: true, Order: 7}, {ID: "b", From: "user", Bound: true, Decision: "denied", After: true, Order: 7}}
	if got := ResolvePrecedence(same, "user", "cto"); got.MessageID != "b" {
		t.Fatalf("same timestamp ID tiebreak = %+v", got)
	}
}
