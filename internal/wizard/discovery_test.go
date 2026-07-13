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
		{name: "negative record count", members: 1, records: -1, actions: []MemberAction{MemberActionFresh}, wantState: RunStateBlocked},
		{name: "ambiguous namespace wins", members: 1, actions: []MemberAction{MemberActionLive}, ambiguous: true, wantState: RunStateBlocked},
		{name: "blocked member wins", members: 2, records: 1, actions: []MemberAction{MemberActionLive, MemberActionBlocked}, wantState: RunStateBlocked},
		{name: "all live", members: 2, records: 1, actions: []MemberAction{MemberActionLive, MemberActionLive}, wantState: RunStateRunning},
		{name: "all fresh no records", members: 2, actions: []MemberAction{MemberActionFresh, MemberActionFresh}, wantState: RunStateNotStarted, wantBackend: BackendRunStart, wantExec: true},
		{name: "live fresh no records", members: 2, actions: []MemberAction{MemberActionLive, MemberActionFresh}, wantState: RunStatePartly, wantBackend: BackendResume, wantExec: true},
		{name: "live restore", members: 2, records: 1, actions: []MemberAction{MemberActionLive, MemberActionRestore}, wantState: RunStatePartly, wantBackend: BackendResume, wantExec: true, wantRestore: true},
		{name: "live restore zero records", members: 2, actions: []MemberAction{MemberActionLive, MemberActionRestore}, wantState: RunStatePartly, wantBackend: BackendResume, wantExec: true},
		{name: "partly live restore fresh", members: 3, records: 2, actions: []MemberAction{MemberActionLive, MemberActionRestore, MemberActionFresh}, wantState: RunStatePartly, wantBackend: BackendResume, wantExec: true, wantRestore: true},
		{name: "stopped all restore", members: 2, records: 2, actions: []MemberAction{MemberActionRestore, MemberActionRestore}, wantState: RunStateStopped, wantBackend: BackendResume, wantExec: true, wantRestore: true},
		{name: "stopped all fresh with records", members: 2, records: 1, actions: []MemberAction{MemberActionFresh, MemberActionFresh}, wantState: RunStateStopped, wantBackend: BackendResume, wantExec: true, wantRestore: true},
		{name: "stopped mixed restore fresh", members: 2, records: 1, actions: []MemberAction{MemberActionRestore, MemberActionFresh}, wantState: RunStateStopped, wantBackend: BackendResume, wantExec: true, wantRestore: true},
		{name: "all restore without records inconsistent", members: 2, actions: []MemberAction{MemberActionRestore, MemberActionRestore}, wantState: RunStateBlocked},
		{name: "unknown action", members: 1, records: 1, actions: []MemberAction{"unknown"}, wantState: RunStateBlocked},
		{name: "mixed known unknown", members: 2, records: 1, actions: []MemberAction{MemberActionFresh, "unknown"}, wantState: RunStateBlocked},
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
	reorderedSets.Operator.Notifications.Events[0], reorderedSets.Operator.Notifications.Events[1] = reorderedSets.Operator.Notifications.Events[1], reorderedSets.Operator.Notifications.Events[0]
	reorderedSets.Operator.Notifications.Sinks[0], reorderedSets.Operator.Notifications.Sinks[1] = reorderedSets.Operator.Notifications.Sinks[1], reorderedSets.Operator.Notifications.Sinks[0]
	reorderedSets.MatchingHistorySessions[0], reorderedSets.MatchingHistorySessions[1] = reorderedSets.MatchingHistorySessions[1], reorderedSets.MatchingHistorySessions[0]
	reorderedSets.NamespaceFacts[0], reorderedSets.NamespaceFacts[1] = reorderedSets.NamespaceFacts[1], reorderedSets.NamespaceFacts[0]
	reorderedSets.MemberPlans[0].LivenessSignals[0], reorderedSets.MemberPlans[0].LivenessSignals[1] = reorderedSets.MemberPlans[0].LivenessSignals[1], reorderedSets.MemberPlans[0].LivenessSignals[0]
	if got, want := DiscoveryFingerprint(reorderedSets), DiscoveryFingerprint(base); got != want {
		t.Fatalf("set ordering changed fingerprint: got %s want %s", got, want)
	}
	reorderedRoster := cloneFingerprintInput(t, base)
	reorderedRoster.Roster[0], reorderedRoster.Roster[1] = reorderedRoster.Roster[1], reorderedRoster.Roster[0]
	if DiscoveryFingerprint(reorderedRoster) == DiscoveryFingerprint(base) {
		t.Fatal("roster order is a decision fact and must change the fingerprint")
	}
	reorderedPlans := cloneFingerprintInput(t, base)
	reorderedPlans.MemberPlans[0], reorderedPlans.MemberPlans[1] = reorderedPlans.MemberPlans[1], reorderedPlans.MemberPlans[0]
	if DiscoveryFingerprint(reorderedPlans) == DiscoveryFingerprint(base) {
		t.Fatal("member-plan order is a decision fact and must change the fingerprint")
	}
	reorderedArgs := cloneFingerprintInput(t, base)
	reorderedArgs.Roster[0].NativeArgs = []string{"high", "--effort"}
	if DiscoveryFingerprint(reorderedArgs) == DiscoveryFingerprint(base) {
		t.Fatal("native-argument order is a decision fact and must change the fingerprint")
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
		"stored model":     func(v *DiscoveryFingerprintInput) { v.Roster[0].Model = "new-model" },
		"stored effort":    func(v *DiscoveryFingerprintInput) { v.Roster[0].Effort = "xhigh" },
		"lead":             func(v *DiscoveryFingerprintInput) { v.Lead = "qa" },
		"lead mode":        func(v *DiscoveryFingerprintInput) { v.LeadMode = "builder" },
		"operator enabled": func(v *DiscoveryFingerprintInput) { v.Operator.Enabled = false },
		"operator mode":    func(v *DiscoveryFingerprintInput) { v.Operator.InteractionMode = "separate_terminal" },
		"notification enabled": func(v *DiscoveryFingerprintInput) {
			v.Operator.Notifications.Enabled = false
		},
		"notification semantics": func(v *DiscoveryFingerprintInput) {
			v.Operator.Notifications.DeliverySemantics = "delivery"
		},
		"notification event": func(v *DiscoveryFingerprintInput) {
			v.Operator.Notifications.Events = append(v.Operator.Notifications.Events, "new-event")
		},
		"notification sink id": func(v *DiscoveryFingerprintInput) {
			v.Operator.Notifications.Sinks[0].ID = "other"
		},
		"notification sink type": func(v *DiscoveryFingerprintInput) {
			v.Operator.Notifications.Sinks[0].Type = "command"
		},
		"notification sink argv": func(v *DiscoveryFingerprintInput) {
			v.Operator.Notifications.Sinks[0].Argv = append(v.Operator.Notifications.Sinks[0].Argv, "--urgent")
		},
		"notification sink timeout": func(v *DiscoveryFingerprintInput) {
			v.Operator.Notifications.Sinks[0].Timeout = "20s"
		},
		"session":        func(v *DiscoveryFingerprintInput) { v.Session = "other" },
		"session source": func(v *DiscoveryFingerprintInput) { v.SessionSource = "history" },
		"history session set": func(v *DiscoveryFingerprintInput) {
			v.MatchingHistorySessions = append(v.MatchingHistorySessions, "history-c")
		},
		"brief identity":     func(v *DiscoveryFingerprintInput) { v.Brief.Path = "/other" },
		"brief content":      func(v *DiscoveryFingerprintInput) { v.Brief.ContentDigest = "changed" },
		"namespace conflict": func(v *DiscoveryFingerprintInput) { v.NamespaceConflicts = append(v.NamespaceConflicts, "legacy-root") },
		"namespace fact profile": func(v *DiscoveryFingerprintInput) {
			v.NamespaceFacts[0].Profile = "other"
		},
		"namespace fact session": func(v *DiscoveryFingerprintInput) {
			v.NamespaceFacts[0].Session = "other"
		},
		"namespace fact root": func(v *DiscoveryFingerprintInput) {
			v.NamespaceFacts[0].AMQRoot = "/other"
		},
		"namespace fact durable state": func(v *DiscoveryFingerprintInput) {
			v.NamespaceFacts[0].DurableState = !v.NamespaceFacts[0].DurableState
		},
		"namespace fact profile pin": func(v *DiscoveryFingerprintInput) {
			v.NamespaceFacts[0].ProfilePinsSession = !v.NamespaceFacts[0].ProfilePinsSession
		},
		"record identity":  func(v *DiscoveryFingerprintInput) { v.RecordIDs[0] = "record-x" },
		"record count":     func(v *DiscoveryFingerprintInput) { v.RecordCount++ },
		"member action":    func(v *DiscoveryFingerprintInput) { v.MemberPlans[0].Action = MemberActionRestore },
		"liveness verdict": func(v *DiscoveryFingerprintInput) { v.MemberPlans[0].LivenessStatus = "stale" },
		"liveness signal": func(v *DiscoveryFingerprintInput) {
			v.MemberPlans[0].LivenessSignals = append(v.MemberPlans[0].LivenessSignals, "presence")
		},
		"saved launch identity": func(v *DiscoveryFingerprintInput) { v.MemberPlans[0].SavedLaunchIdentity = "launch-x" },
		"saved launch target":   func(v *DiscoveryFingerprintInput) { v.MemberPlans[0].SavedTarget = "new-session" },
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

func TestDiscoveryFingerprintCanonicalizesNilAndEmptyCollections(t *testing.T) {
	tests := []struct {
		name string
		a    DiscoveryFingerprintInput
		b    DiscoveryFingerprintInput
	}{
		{name: "roster", a: DiscoveryFingerprintInput{Roster: nil}, b: DiscoveryFingerprintInput{Roster: []DiscoveryMember{}}},
		{name: "native args", a: DiscoveryFingerprintInput{Roster: []DiscoveryMember{{NativeArgs: nil}}}, b: DiscoveryFingerprintInput{Roster: []DiscoveryMember{{NativeArgs: []string{}}}}},
		{name: "self allow", a: DiscoveryFingerprintInput{Operator: DiscoveryOperator{SelfAllow: nil}}, b: DiscoveryFingerprintInput{Operator: DiscoveryOperator{SelfAllow: []string{}}}},
		{name: "notification events", a: DiscoveryFingerprintInput{Operator: DiscoveryOperator{Notifications: DiscoveryNotificationPolicy{Events: nil}}}, b: DiscoveryFingerprintInput{Operator: DiscoveryOperator{Notifications: DiscoveryNotificationPolicy{Events: []string{}}}}},
		{name: "notification sinks", a: DiscoveryFingerprintInput{Operator: DiscoveryOperator{Notifications: DiscoveryNotificationPolicy{Sinks: nil}}}, b: DiscoveryFingerprintInput{Operator: DiscoveryOperator{Notifications: DiscoveryNotificationPolicy{Sinks: []DiscoveryNotificationSink{}}}}},
		{name: "notification sink argv", a: DiscoveryFingerprintInput{Operator: DiscoveryOperator{Notifications: DiscoveryNotificationPolicy{Sinks: []DiscoveryNotificationSink{{ID: "x", Argv: nil}}}}}, b: DiscoveryFingerprintInput{Operator: DiscoveryOperator{Notifications: DiscoveryNotificationPolicy{Sinks: []DiscoveryNotificationSink{{ID: "x", Argv: []string{}}}}}}},
		{name: "history sessions", a: DiscoveryFingerprintInput{MatchingHistorySessions: nil}, b: DiscoveryFingerprintInput{MatchingHistorySessions: []string{}}},
		{name: "namespace conflicts", a: DiscoveryFingerprintInput{NamespaceConflicts: nil}, b: DiscoveryFingerprintInput{NamespaceConflicts: []string{}}},
		{name: "namespace facts", a: DiscoveryFingerprintInput{NamespaceFacts: nil}, b: DiscoveryFingerprintInput{NamespaceFacts: []DiscoveryNamespaceFact{}}},
		{name: "record ids", a: DiscoveryFingerprintInput{RecordIDs: nil}, b: DiscoveryFingerprintInput{RecordIDs: []string{}}},
		{name: "member plans", a: DiscoveryFingerprintInput{MemberPlans: nil}, b: DiscoveryFingerprintInput{MemberPlans: []DiscoveryMemberPlan{}}},
		{name: "liveness signals", a: DiscoveryFingerprintInput{MemberPlans: []DiscoveryMemberPlan{{LivenessSignals: nil}}}, b: DiscoveryFingerprintInput{MemberPlans: []DiscoveryMemberPlan{{LivenessSignals: []string{}}}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got, want := DiscoveryFingerprint(tc.a), DiscoveryFingerprint(tc.b); got != want {
				t.Fatalf("nil/empty mismatch: got %s want %s", got, want)
			}
		})
	}
}

func fingerprintFixture() DiscoveryFingerprintInput {
	return DiscoveryFingerprintInput{
		Profile: "release",
		Roster: []DiscoveryMember{
			{Role: "cto", Handle: "cto", Binary: "codex", CWD: "/repo", Session: "s", NativeArgs: []string{"--effort", "high"}, Model: "gpt", Effort: "high"},
			{Role: "qa", Handle: "qa", Binary: "claude", CWD: "/repo", Session: "s", NativeArgs: []string{"--chrome"}, Model: "sonnet", Effort: "medium"},
		},
		Lead: "cto", LeadMode: "planner",
		Operator: DiscoveryOperator{Enabled: true, InteractionMode: "lead_pane", Handle: "user", SelfLead: "cto", SelfAllow: []string{"merge", "spawn"}, SelfRevision: 2, Notifications: DiscoveryNotificationPolicy{Enabled: true, DeliverySemantics: "attention_only", Events: []string{"gate", "local_input_blocked"}, Sinks: []DiscoveryNotificationSink{{ID: "desktop", Type: "desktop", Timeout: "10s"}, {ID: "audit", Type: "command", Argv: []string{"notify", "--json"}, Timeout: "5s"}}}},
		Session:  "s", SessionSource: "member_pin", MatchingHistorySessions: []string{"history-b", "history-a"},
		Brief:              DiscoveryBrief{Path: "/repo/.amq-squad/briefs/s.md", Source: "seed", Goal: "ship it", Provenance: "issue:431", ContentDigest: "abc"},
		NamespaceConflicts: []string{"b", "a"}, RecordIDs: []string{"record-2", "record-1"}, RecordCount: 2,
		NamespaceFacts: []DiscoveryNamespaceFact{{Profile: "release", Session: "s", AMQRoot: "/repo/.agent-mail/release/s", DurableState: true, ProfilePinsSession: true}, {Profile: "default", Session: "s", AMQRoot: "/repo/.agent-mail/s", DurableState: true}},
		MemberPlans: []DiscoveryMemberPlan{
			{Role: "cto", Action: MemberActionLive, LivenessStatus: "live", LivenessSignals: []string{"wake", "pid"}, SavedLaunchIdentity: "launch-cto", SavedTarget: "current-window"},
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
