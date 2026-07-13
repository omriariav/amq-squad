package wizard

import (
	"reflect"
	"strings"
	"testing"
)

func TestSpecSerializesExplicitSelfOperatorPolicy(t *testing.T) {
	args := Spec{OperatorMode: "self_operator", SelfOperatorLead: "cto", SelfOperatorAllow: "merge"}.Args()
	got := strings.Join(args, " ")
	for _, want := range []string{"--operator-mode self_operator", "--self-operator-lead cto", "--self-operator-allow merge"} {
		if !strings.Contains(got, want) {
			t.Fatalf("args %q missing %q", got, want)
		}
	}
}

func TestSpecArgsStableAndPreviewOnly(t *testing.T) {
	s := Spec{
		Project:               "/tmp/my repo",
		Profile:               "review",
		Session:               "issue-393",
		Roles:                 "cto,qa",
		Binary:                "qa=claude",
		Model:                 "cto=gpt-5",
		Effort:                "cto=high,qa=medium",
		OperatorMode:          "separate_terminal",
		OperatorNotifications: true,
		CodexArgs:             "-c model_reasoning_effort=high",
		ClaudeArgs:            "--effort high",
		Lead:                  "cto",
		LeadMode:              "planner",
		Visibility:            "current",
		LayoutPreset:          "lead-left",
		LauncherPane:          "close-after-start",
		ExternalLead:          true,
		Goal:                  "ship it",
		SeedFrom:              "issue:393",
	}
	want := []string{
		"--project", "/tmp/my repo",
		"--profile", "review",
		"--session", "issue-393",
		"--roles", "cto,qa",
		"--binary", "qa=claude",
		"--model", "cto=gpt-5",
		"--effort", "cto=high,qa=medium",
		"--operator-mode", "separate_terminal",
		"--operator-notifications",
		"--codex-args", "-c model_reasoning_effort=high",
		"--claude-args", "--effort high",
		"--lead", "cto",
		"--lead-mode", "planner",
		"--visibility", "current",
		"--layout-preset", "lead-left",
		"--launcher-pane", "close-after-start",
		"--external-lead",
		"--goal", "ship it",
		"--seed-from", "issue:393",
	}
	if got := s.Args(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %#v, want %#v", got, want)
	}
	for _, arg := range s.Args() {
		if arg == "--go" || arg == "--interactive" {
			t.Fatalf("preview-only spec emitted forbidden flag %q", arg)
		}
	}
}

func TestSpecGlobalArgsNeverLeakProjectRunFlags(t *testing.T) {
	s := Spec{
		Scope: "global", GlobalRoot: "/neutral", GlobalAgent: "codex", GlobalModel: "gpt",
		GlobalEffort: "high", GlobalCodexArgs: "--search", GlobalClaudeArgs: "--debug", GlobalWindow: "noc",
		Project: "/project", Profile: "release", Session: "issue-393", Roles: "cto,qa",
		Visibility: "current", LayoutPreset: "lead-left", LauncherPane: "close-after-start",
		OperatorNotifications: true, OperatorNotificationsRequested: true, OperatorNotificationsSet: true,
	}
	got := strings.Join(s.GlobalArgs(), " ")
	for _, want := range []string{"--root /neutral", "--agent codex", "--model gpt", "--codex-args --search -c model_reasoning_effort=high", "--name noc"} {
		if !strings.Contains(got, want) {
			t.Fatalf("global argv %q missing %q", got, want)
		}
	}
	for _, forbidden := range []string{"--project", "--profile", "--session", "--roles", "--visibility", "--layout-preset", "--launcher-pane", "--operator-notifications"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("global argv leaked %q: %s", forbidden, got)
		}
	}
	if strings.Contains(got, "--claude-args") {
		t.Fatalf("inactive native args leaked: %s", got)
	}
}

func TestSpecGlobalClaudeEffortUsesOnlyClaudeNativeArgs(t *testing.T) {
	got := strings.Join((Spec{GlobalRoot: "/n", GlobalAgent: "claude", GlobalEffort: "FutureTier", GlobalCodexArgs: "--search", GlobalClaudeArgs: "--chrome"}).GlobalArgs(), " ")
	if !strings.Contains(got, "--claude-args --chrome --effort FutureTier") || strings.Contains(got, "--codex-args") {
		t.Fatalf("Claude global args = %s", got)
	}
}

func TestSpecArgsOmitsEmptyFields(t *testing.T) {
	got := (Spec{Project: "/repo", Session: "s"}).Args()
	want := []string{"--project", "/repo", "--session", "s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %#v, want %#v", got, want)
	}
}

func TestSpecArgsOmitsLegacyUnspecifiedOperatorMode(t *testing.T) {
	got := (Spec{Project: "/repo", Session: "s", OperatorMode: "unspecified"}).Args()
	want := []string{"--project", "/repo", "--session", "s"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Args() = %#v, want %#v", got, want)
	}
}

func TestSpecCarriesExplicitBackendWithoutAffectingLegacyRunStartArgs(t *testing.T) {
	s := Spec{Backend: BackendResume, Project: "/repo", Profile: "release", Session: "s"}
	if s.Backend != BackendResume {
		t.Fatalf("backend = %q", s.Backend)
	}
	want := []string{"--project", "/repo", "--profile", "release", "--session", "s"}
	if got := s.Args(); !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy run-start args changed while adding explicit backend: %#v", got)
	}
}

func TestResumeArgsCanonicalComposition(t *testing.T) {
	tests := []struct {
		name    string
		records int
		members []SessionMemberSummary
		model   string
		effort  string
		vis     string
		layout  string
		want    []string
	}{
		{name: "all restore", records: 2, members: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore}, {Role: "qa", Action: MemberActionRestore}}, vis: "sibling-tabs", layout: "one-window-per-agent", want: []string{"--project", "/repo", "--profile", "release", "--session", "s", "--restore-existing", "--target", "new-window", "--layout", "tiled"}},
		{name: "restore plus fresh", records: 1, members: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore}, {Role: "qa", Action: MemberActionFresh}}, model: "qa=gpt", effort: "qa=FutureTier", vis: "current", layout: "lead-top", want: []string{"--project", "/repo", "--profile", "release", "--session", "s", "--restore-existing", "--model", "qa=gpt", "--effort", "qa=FutureTier", "--target", "current-window", "--layout", "horizontal"}},
		{name: "live plus fresh no records", members: []SessionMemberSummary{{Role: "cto", Action: MemberActionLive}, {Role: "qa", Action: MemberActionFresh}}, model: "qa=gpt", vis: "detached", want: []string{"--project", "/repo", "--profile", "release", "--session", "s", "--model", "qa=gpt", "--target", "new-session", "--layout", "tiled"}},
		{name: "live plus restore", records: 1, members: []SessionMemberSummary{{Role: "cto", Action: MemberActionLive}, {Role: "qa", Action: MemberActionRestore}}, vis: "current", layout: "even-grid", want: []string{"--project", "/repo", "--profile", "release", "--session", "s", "--restore-existing", "--target", "current-window", "--layout", "tiled"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := Spec{Backend: BackendResume, Project: "/repo", Profile: "release", ProfileBranch: ProfileBranchExisting, Session: "s", RunExecutable: true, RunState: RunStateStopped, RecordCount: tt.records, RestoreExisting: tt.records > 0, DiscoveryFingerprint: "fp", ResumeMembers: tt.members, Model: tt.model, Effort: tt.effort, Visibility: tt.vis, LayoutPreset: tt.layout}
			got, err := s.ResumeArgs()
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ResumeArgs()=%#v want=%#v", got, tt.want)
			}
		})
	}
}

func TestResumeArgsRejectsSemanticLeakage(t *testing.T) {
	base := Spec{Backend: BackendResume, Project: "/repo", Profile: "release", ProfileBranch: ProfileBranchExisting, Session: "s", RunExecutable: true, RunState: RunStatePartly, DiscoveryFingerprint: "fp", ResumeMembers: []SessionMemberSummary{{Role: "cto", Action: MemberActionLive}, {Role: "qa", Action: MemberActionFresh}}, Visibility: "sibling-tabs", LayoutPreset: "one-window-per-agent"}
	tests := map[string]func(*Spec){
		"live model":                   func(s *Spec) { s.Model = "cto=gpt" },
		"unknown model":                func(s *Spec) { s.Model = "other=gpt" },
		"live effort":                  func(s *Spec) { s.Effort = "cto=high" },
		"unknown effort":               func(s *Spec) { s.Effort = "other=high" },
		"native args":                  func(s *Spec) { s.CodexArgs = "--search" },
		"launcher":                     func(s *Spec) { s.LauncherPane = "keep" },
		"goal":                         func(s *Spec) { s.Goal = "replace brief" },
		"record guard mismatch":        func(s *Spec) { s.RecordCount, s.RestoreExisting = 1, false },
		"empty fingerprint":            func(s *Spec) { s.DiscoveryFingerprint = "" },
		"blocked member":               func(s *Spec) { s.ResumeMembers[1].Action = MemberActionBlocked },
		"all live":                     func(s *Spec) { s.ResumeMembers[1].Action = MemberActionLive },
		"non resume":                   func(s *Spec) { s.Backend = BackendRunStart },
		"unsupported placement":        func(s *Spec) { s.Visibility = "unknown" },
		"unsupported current layout":   func(s *Spec) { s.Visibility, s.LayoutPreset = "current", "unknown" },
		"sibling inconsistent layout":  func(s *Spec) { s.LayoutPreset = "lead-left" },
		"detached inconsistent layout": func(s *Spec) { s.Visibility, s.LayoutPreset = "detached", "one-window-per-agent" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s := base.Clone()
			mutate(&s)
			if args, err := s.ResumeArgs(); err == nil {
				t.Fatalf("ResumeArgs()=%v unexpectedly accepted %+v", args, s)
			}
		})
	}
}

func TestResumeArgsEmitsOnlyRedeliverGoalIntent(t *testing.T) {
	s := Spec{
		Backend: BackendResume, Project: "/repo", Profile: "release", ProfileBranch: ProfileBranchExisting,
		Session: "s", RunExecutable: true, RunState: RunStateStopped, RecordCount: 1, RestoreExisting: true,
		DiscoveryFingerprint: "fp", ResumeMembers: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore}},
		Visibility: "sibling-tabs", LayoutPreset: "one-window-per-agent",
		ResumeGoalPlan: ResumeGoalPlan{SchemaVersion: 1, Eligible: true, Goal: "secret goal text"}, RedeliverGoal: true,
	}
	args, err := s.ResumeArgs()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(args, " "); !strings.Contains(got, "--redeliver-goal") || strings.Contains(got, s.ResumeGoalPlan.Goal) {
		t.Fatalf("resume args leaked or omitted goal intent: %q", got)
	}
	s.ResumeGoalPlan.Eligible = false
	s.ResumeGoalPlan.Reason = "claim missing"
	if _, err := s.ResumeArgs(); err == nil || !strings.Contains(err.Error(), "claim missing") {
		t.Fatalf("ineligible selected redelivery accepted: %v", err)
	}
}

func TestResumeArgsPreservesEligibleNoWithoutGoalText(t *testing.T) {
	s := Spec{Backend: BackendResume, Project: "/repo", Profile: "default", ProfileBranch: ProfileBranchExisting, Session: "s", RunExecutable: true, RunState: RunStateStopped, RecordCount: 1, RestoreExisting: true, DiscoveryFingerprint: "fp", ResumeMembers: []SessionMemberSummary{{Role: "cto", Action: MemberActionRestore}}, Visibility: "sibling-tabs", LayoutPreset: "one-window-per-agent", ResumeGoalPlan: ResumeGoalPlan{SchemaVersion: 1, Eligible: true, Goal: "secret\n\x1b[31mgoal"}}
	args, err := s.ResumeArgs()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--no-redeliver-goal-prompt") || strings.Contains(joined, "secret") || strings.Contains(joined, "--redeliver-goal") {
		t.Fatalf("eligible No args=%#v", args)
	}
	preview, live, err := s.CommandForms()
	if err != nil || strings.Count(preview, "--no-redeliver-goal-prompt") != 1 || strings.Count(live, "--no-redeliver-goal-prompt") != 1 || strings.Count(live, "--exec") != 1 {
		t.Fatalf("forms preview=%q live=%q err=%v", preview, live, err)
	}
}

func TestDiscoveryFingerprintIgnoresGoalSelectionButTracksEvidence(t *testing.T) {
	base := DiscoveryFingerprintInput{Profile: "default", Session: "s", GoalPlan: ResumeGoalPlan{SchemaVersion: 1, Action: "redeliver", Eligible: true, BindingDigest: "sha256:a", AttemptDigest: "sha256:b", ClaimDigest: "sha256:c", TransitionState: "absent"}}
	want := DiscoveryFingerprint(base)
	selected := base
	selected.GoalPlan.Selected = true
	if got := DiscoveryFingerprint(selected); got != want {
		t.Fatalf("downstream Selected changed discovery fingerprint: %s != %s", got, want)
	}
	mutated := base
	mutated.GoalPlan.ClaimDigest = "sha256:changed"
	if got := DiscoveryFingerprint(mutated); got == want {
		t.Fatal("raw goal evidence mutation did not invalidate fingerprint")
	}
}

func TestGoalExcerptStripsTerminalControlsAndBounds(t *testing.T) {
	got := GoalExcerpt("safe\x1b[31mRED\x1b[0m\x00\nsecond\nthird" + strings.Repeat("x", 300))
	if strings.ContainsRune(got, '\x1b') || strings.ContainsRune(got, '\x00') || strings.Contains(got, "[31m") || len([]rune(got)) > maxGoalExcerptRunes || strings.Count(got, "\n") > 1 {
		t.Fatalf("unsafe/unbounded excerpt: %q", got)
	}
}

func TestResumeGoalChoiceIsDownstreamAndClearedOnInvalidation(t *testing.T) {
	s := Spec{RedeliverGoal: true}
	s.SelectExistingSession(SessionSummary{Name: "s", Fingerprint: "fp", Classification: RunClassification{Backend: BackendResume, Executable: true}, GoalPlan: ResumeGoalPlan{Eligible: true, Selected: true, BindingDigest: "sha256:evidence"}})
	if s.ResumeGoalPlan.Selected || s.RedeliverGoal {
		t.Fatalf("authoritative session imported downstream intent: %+v", s)
	}
	s.RedeliverGoal = true
	s.InvalidateExistingRun()
	if s.RedeliverGoal || s.ResumeGoalPlan != (ResumeGoalPlan{}) {
		t.Fatalf("stale goal choice survived invalidation: %+v", s)
	}
}
