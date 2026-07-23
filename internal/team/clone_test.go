package team

import (
	"testing"
)

func TestCloneRosterForSessionRestampsSessionAndDropsInstanceState(t *testing.T) {
	source := Team{
		Project:      "/old/project",
		Workstream:   "legacy-shim",
		Trust:        "approve-for-me",
		Orchestrated: true,
		Lead:         "cto",
		LeadMode:     LeadModeBuilder,
		Members: []Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "v1", Model: "gpt-5"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "v1", ClaudeArgs: []string{"--effort", "high"}},
		},
		Operator: &OperatorConfig{
			Enabled: true, Handle: DefaultOperatorHandle, Participant: true,
			SelfOperator: &SelfOperatorPolicy{
				LeadRole: "cto",
				Sessions: map[string]SelfOperatorSessionPolicy{"v1": {}},
			},
		},
	}

	clone, err := CloneRosterForSession(source, "v2")
	if err != nil {
		t.Fatalf("CloneRosterForSession: %v", err)
	}

	if clone.Project != "" {
		t.Fatalf("clone.Project = %q, want empty (reset for the new profile)", clone.Project)
	}
	if clone.Workstream != "" {
		t.Fatalf("clone.Workstream = %q, want empty (deprecated shim never carried forward)", clone.Workstream)
	}
	if !clone.CreatedAt.IsZero() {
		t.Fatalf("clone.CreatedAt = %v, want zero so NormalizeForWrite stamps a fresh time", clone.CreatedAt)
	}
	if clone.Trust != "approve-for-me" || !clone.Orchestrated || clone.Lead != "cto" || clone.LeadMode != LeadModeBuilder {
		t.Fatalf("clone roster shape not preserved: %+v", clone)
	}
	if len(clone.Members) != 2 {
		t.Fatalf("clone.Members = %+v, want 2", clone.Members)
	}
	for _, m := range clone.Members {
		if m.Session != "v2" {
			t.Fatalf("member %q Session = %q, want restamped to v2", m.Role, m.Session)
		}
	}
	if clone.Members[1].ClaudeArgs[0] != "--effort" {
		t.Fatalf("member-scoped ClaudeArgs not preserved: %+v", clone.Members[1])
	}
	if clone.Operator == nil || clone.Operator.SelfOperator != nil {
		t.Fatalf("clone.Operator.SelfOperator = %+v, want nil (session-keyed policy from the old session must not carry forward)", clone.Operator)
	}

	// Source must be untouched by the clone (no aliasing through shared slices).
	if source.Members[0].Session != "v1" || source.Members[1].Session != "v1" {
		t.Fatalf("clone mutated source members: %+v", source.Members)
	}
	if source.Operator.SelfOperator == nil {
		t.Fatalf("clone mutated source operator self-operator policy")
	}
}

func TestCloneRosterForSessionRejectsInvalidSession(t *testing.T) {
	source := Team{Members: []Member{{Role: "cto", Binary: "codex", Session: "v1"}}}
	if _, err := CloneRosterForSession(source, "Not A Valid Session!"); err == nil {
		t.Fatal("expected an error for an invalid session name")
	}
}

func TestCloneRosterForSessionThenWriteProfileValidates(t *testing.T) {
	dir := t.TempDir()
	source := Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "v1", ActorMode: ActorModeImplementation},
		},
	}
	clone, err := CloneRosterForSession(source, "v2")
	if err != nil {
		t.Fatalf("CloneRosterForSession: %v", err)
	}
	if err := WriteProfile(dir, "cloned", clone); err != nil {
		t.Fatalf("WriteProfile(clone): %v", err)
	}
	got, err := ReadProfile(dir, "cloned")
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if len(got.Members) != 1 || got.Members[0].Session != "v2" {
		t.Fatalf("persisted cloned profile = %+v", got)
	}
	if got.CreatedAt.IsZero() {
		t.Fatalf("WriteProfile did not stamp a fresh CreatedAt on the cloned profile")
	}
}
