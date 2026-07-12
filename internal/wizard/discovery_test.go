package wizard

import (
	"encoding/json"
	"testing"
)

func TestClassifyExistingRunPrecedenceAndRestoreGuard(t *testing.T) {
	tests := []struct {
		name        string
		members     int
		records     int
		actions     []MemberAction
		ambiguous   bool
		wantState   RunState
		wantBackend Backend
		wantExec    bool
		wantRestore bool
	}{
		{name: "empty profile", wantState: RunStateBlocked},
		{name: "ambiguous namespace wins", members: 1, actions: []MemberAction{MemberActionLive}, ambiguous: true, wantState: RunStateBlocked},
		{name: "blocked member wins", members: 2, records: 1, actions: []MemberAction{MemberActionLive, MemberActionBlocked}, wantState: RunStateBlocked},
		{name: "all live", members: 2, records: 1, actions: []MemberAction{MemberActionLive, MemberActionLive}, wantState: RunStateRunning},
		{name: "all fresh no records", members: 2, actions: []MemberAction{MemberActionFresh, MemberActionFresh}, wantState: RunStateNotStarted, wantBackend: BackendRunStart, wantExec: true},
		{name: "live fresh no records", members: 2, actions: []MemberAction{MemberActionLive, MemberActionFresh}, wantState: RunStatePartly, wantBackend: BackendResume, wantExec: true},
		{name: "live restore", members: 2, records: 1, actions: []MemberAction{MemberActionLive, MemberActionRestore}, wantState: RunStatePartly, wantBackend: BackendResume, wantExec: true, wantRestore: true},
		{name: "stopped all restore", members: 2, records: 2, actions: []MemberAction{MemberActionRestore, MemberActionRestore}, wantState: RunStateStopped, wantBackend: BackendResume, wantExec: true, wantRestore: true},
		{name: "stopped mixed restore fresh", members: 2, records: 1, actions: []MemberAction{MemberActionRestore, MemberActionFresh}, wantState: RunStateStopped, wantBackend: BackendResume, wantExec: true, wantRestore: true},
		{name: "all restore without records inconsistent", members: 2, actions: []MemberAction{MemberActionRestore, MemberActionRestore}, wantState: RunStateBlocked},
		{name: "planner cardinality mismatch", members: 2, actions: []MemberAction{MemberActionFresh}, wantState: RunStateBlocked},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyExistingRun(tc.members, tc.records, tc.actions, tc.ambiguous)
			if got.State != tc.wantState || got.Backend != tc.wantBackend || got.Executable != tc.wantExec || got.RestoreExisting != tc.wantRestore {
				t.Fatalf("classification = %+v, want state=%s backend=%s exec=%t restore=%t", got, tc.wantState, tc.wantBackend, tc.wantExec, tc.wantRestore)
			}
			if got.State == "" || (got.State == RunStateBlocked && got.Detail == "") {
				t.Fatalf("classification must be exhaustive and diagnostic: %+v", got)
			}
		})
	}
}

func TestDiscoveryFingerprintCanonicalizesSetsButPreservesRosterOrder(t *testing.T) {
	base := fingerprintFixture()
	reorderedSets := cloneFingerprintInput(t, base)
	reorderedSets.RecordIDs[0], reorderedSets.RecordIDs[1] = reorderedSets.RecordIDs[1], reorderedSets.RecordIDs[0]
	reorderedSets.NamespaceConflicts[0], reorderedSets.NamespaceConflicts[1] = reorderedSets.NamespaceConflicts[1], reorderedSets.NamespaceConflicts[0]
	reorderedSets.Operator.SelfAllow[0], reorderedSets.Operator.SelfAllow[1] = reorderedSets.Operator.SelfAllow[1], reorderedSets.Operator.SelfAllow[0]
	reorderedSets.MemberPlans[0].LivenessSignals[0], reorderedSets.MemberPlans[0].LivenessSignals[1] = reorderedSets.MemberPlans[0].LivenessSignals[1], reorderedSets.MemberPlans[0].LivenessSignals[0]
	if got, want := DiscoveryFingerprint(reorderedSets), DiscoveryFingerprint(base); got != want {
		t.Fatalf("set ordering changed fingerprint: got %s want %s", got, want)
	}
	reorderedRoster := cloneFingerprintInput(t, base)
	reorderedRoster.Roster[0], reorderedRoster.Roster[1] = reorderedRoster.Roster[1], reorderedRoster.Roster[0]
	if DiscoveryFingerprint(reorderedRoster) == DiscoveryFingerprint(base) {
		t.Fatal("roster order is a decision fact and must change the fingerprint")
	}
}

func TestDiscoveryFingerprintChangesForEveryDecisionInputClass(t *testing.T) {
	base := fingerprintFixture()
	want := DiscoveryFingerprint(base)
	tests := map[string]func(*DiscoveryFingerprintInput){
		"roster identity": func(v *DiscoveryFingerprintInput) { v.Roster[0].Handle = "other" },
		"native args": func(v *DiscoveryFingerprintInput) {
			v.Roster[0].NativeArgs = append(v.Roster[0].NativeArgs, "--search")
		},
		"stored model":       func(v *DiscoveryFingerprintInput) { v.Roster[0].Model = "new-model" },
		"stored effort":      func(v *DiscoveryFingerprintInput) { v.Roster[0].Effort = "xhigh" },
		"lead":               func(v *DiscoveryFingerprintInput) { v.Lead = "qa" },
		"lead mode":          func(v *DiscoveryFingerprintInput) { v.LeadMode = "builder" },
		"operator":           func(v *DiscoveryFingerprintInput) { v.Operator.InteractionMode = "separate_terminal" },
		"notifications":      func(v *DiscoveryFingerprintInput) { v.Operator.Notifications = false },
		"session":            func(v *DiscoveryFingerprintInput) { v.Session = "other" },
		"session source":     func(v *DiscoveryFingerprintInput) { v.SessionSource = "history" },
		"brief identity":     func(v *DiscoveryFingerprintInput) { v.Brief.Path = "/other" },
		"brief content":      func(v *DiscoveryFingerprintInput) { v.Brief.ContentDigest = "changed" },
		"namespace conflict": func(v *DiscoveryFingerprintInput) { v.NamespaceConflicts = append(v.NamespaceConflicts, "legacy-root") },
		"record identity":    func(v *DiscoveryFingerprintInput) { v.RecordIDs[0] = "record-x" },
		"record count":       func(v *DiscoveryFingerprintInput) { v.RecordCount++ },
		"member action":      func(v *DiscoveryFingerprintInput) { v.MemberPlans[0].Action = MemberActionRestore },
		"liveness verdict":   func(v *DiscoveryFingerprintInput) { v.MemberPlans[0].LivenessStatus = "stale" },
		"liveness signal": func(v *DiscoveryFingerprintInput) {
			v.MemberPlans[0].LivenessSignals = append(v.MemberPlans[0].LivenessSignals, "presence")
		},
		"saved launch identity": func(v *DiscoveryFingerprintInput) { v.MemberPlans[0].SavedLaunchIdentity = "launch-x" },
		"member blocker":        func(v *DiscoveryFingerprintInput) { v.MemberPlans[0].Blocker = "conflict" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			changed := cloneFingerprintInput(t, base)
			mutate(&changed)
			if got := DiscoveryFingerprint(changed); got == want {
				t.Fatalf("%s delta did not change fingerprint %s", name, got)
			}
		})
	}
}

func fingerprintFixture() DiscoveryFingerprintInput {
	return DiscoveryFingerprintInput{
		Profile: "release",
		Roster: []DiscoveryMember{
			{Role: "cto", Handle: "cto", Binary: "codex", CWD: "/repo", Session: "s", NativeArgs: []string{"--search"}, Model: "gpt", Effort: "high"},
			{Role: "qa", Handle: "qa", Binary: "claude", CWD: "/repo", Session: "s", NativeArgs: []string{"--chrome"}, Model: "sonnet", Effort: "medium"},
		},
		Lead: "cto", LeadMode: "planner",
		Operator: DiscoveryOperator{InteractionMode: "lead_pane", Handle: "user", Delivery: "durable_amq", SelfLead: "cto", SelfAllow: []string{"merge", "spawn"}, SelfRevision: 2, Notifications: true, NotificationSem: "attention_only"},
		Session:  "s", SessionSource: "member_pin",
		Brief:              DiscoveryBrief{Path: "/repo/.amq-squad/briefs/s.md", Source: "seed", Provenance: "issue:431", ContentDigest: "abc"},
		NamespaceConflicts: []string{"b", "a"}, RecordIDs: []string{"record-2", "record-1"}, RecordCount: 2,
		MemberPlans: []DiscoveryMemberPlan{
			{Role: "cto", Action: MemberActionLive, LivenessStatus: "live", LivenessSignals: []string{"wake", "pid"}, SavedLaunchIdentity: "launch-cto"},
			{Role: "qa", Action: MemberActionRestore, LivenessStatus: "stale", SavedLaunchIdentity: "launch-qa"},
		},
	}
}

func cloneFingerprintInput(t *testing.T, input DiscoveryFingerprintInput) DiscoveryFingerprintInput {
	t.Helper()
	b, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var out DiscoveryFingerprintInput
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
