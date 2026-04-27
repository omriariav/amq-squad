package rules

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlanCreateWhenMissing(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n\n- one rule\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plans) != 2 {
		t.Fatalf("got %d plans, want 2", len(plans))
	}
	for _, p := range plans {
		if !p.Creating {
			t.Errorf("%s: Creating = false, want true", p.Basename)
		}
		if p.Unchanged {
			t.Errorf("%s: Unchanged = true, want false", p.Basename)
		}
		if p.Adopting {
			t.Errorf("%s: Adopting = true, want false", p.Basename)
		}
		if !strings.Contains(p.After, "one rule") {
			t.Errorf("%s: After missing rule body", p.Basename)
		}
		if !strings.Contains(p.After, BeginMarker) || !strings.Contains(p.After, EndMarker) {
			t.Errorf("%s: After missing markers", p.Basename)
		}
	}
}

func TestPlanAdoptsExistingContent(t *testing.T) {
	project := t.TempDir()
	existing := "# My Project\n\nUser docs.\n"
	if err := os.WriteFile(filepath.Join(project, ClaudeFile), []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	byName := map[string]SyncPlan{}
	for _, p := range plans {
		byName[p.Basename] = p
	}
	claude := byName[ClaudeFile]
	if !claude.Adopting {
		t.Error("CLAUDE.md: Adopting = false, want true")
	}
	if claude.Creating {
		t.Error("CLAUDE.md: Creating = true for pre-existing file")
	}
	if !strings.Contains(claude.After, "User docs.") {
		t.Error("CLAUDE.md: user content not preserved")
	}
	if !strings.Contains(claude.After, BeginMarker) {
		t.Error("CLAUDE.md: no markers inserted")
	}
	// User content must come before the managed block.
	userIdx := strings.Index(claude.After, "User docs.")
	markerIdx := strings.Index(claude.After, BeginMarker)
	if userIdx >= markerIdx {
		t.Error("CLAUDE.md: managed block not placed after user content")
	}

	agents := byName[AgentsFile]
	if !agents.Creating {
		t.Error("AGENTS.md: should be created fresh")
	}
}

func TestPlanRefreshesManagedBlockOnly(t *testing.T) {
	project := t.TempDir()
	// File already has markers with stale content.
	userPrefix := "# My Project\n\nUser docs.\n\n"
	stale := userPrefix + BeginMarker + "\nOLD RULES\n" + EndMarker + "\n"
	for _, name := range []string{ClaudeFile, AgentsFile} {
		if err := os.WriteFile(filepath.Join(project, name), []byte(stale), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	plans, err := Plan(project, "# Team Rules\n\n- NEW RULE\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	for _, p := range plans {
		if p.Adopting || p.Creating {
			t.Errorf("%s: Adopting=%v Creating=%v, want both false", p.Basename, p.Adopting, p.Creating)
		}
		if p.Unchanged {
			t.Errorf("%s: Unchanged = true when rules differ", p.Basename)
		}
		if strings.Contains(p.After, "OLD RULES") {
			t.Errorf("%s: stale content not removed", p.Basename)
		}
		if !strings.Contains(p.After, "NEW RULE") {
			t.Errorf("%s: new rule missing", p.Basename)
		}
		if !strings.Contains(p.After, "User docs.") {
			t.Errorf("%s: user content outside markers clobbered", p.Basename)
		}
	}
}

func TestPlanUnchangedWhenAlreadyInSync(t *testing.T) {
	project := t.TempDir()
	body := "# Team Rules\n\n- a rule\n"

	// First pass: Plan + Apply to get files into canonical state.
	plans, err := Plan(project, body)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := Apply(plans); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Second pass with same body: all unchanged.
	plans, err = Plan(project, body)
	if err != nil {
		t.Fatalf("Plan second: %v", err)
	}
	for _, p := range plans {
		if !p.Unchanged {
			t.Errorf("%s: Unchanged = false on no-op sync", p.Basename)
		}
	}
}

func TestApplyOnlyWritesChanged(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	n, err := Apply(plans)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if n != 2 {
		t.Errorf("Apply touched %d files, want 2", n)
	}

	// No changes now.
	plans, _ = Plan(project, "# Team Rules\n")
	n, err = Apply(plans)
	if err != nil {
		t.Fatalf("Apply 2: %v", err)
	}
	if n != 0 {
		t.Errorf("Apply touched %d files on no-op, want 0", n)
	}
}

func TestApplyRejectsChangedFileSincePlan(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ClaudeFile), []byte("user edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(plans)
	if err == nil || !strings.Contains(err.Error(), "changed since sync plan") {
		t.Fatalf("Apply error = %v, want stale plan error", err)
	}
}

func TestApplyRejectsNewEmptyFileSincePlan(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ClaudeFile), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = Apply(plans)
	if err == nil || !strings.Contains(err.Error(), "changed since sync plan") {
		t.Fatalf("Apply error = %v, want stale plan error", err)
	}
}

func TestApplyDoesNotLeaveTempFiles(t *testing.T) {
	project := t.TempDir()
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := Apply(plans); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	entries, err := os.ReadDir(project)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("left temp file behind: %s", e.Name())
		}
	}
}

func TestApplyPreservesExistingFileMode(t *testing.T) {
	project := t.TempDir()
	path := filepath.Join(project, ClaudeFile)
	if err := os.WriteFile(path, []byte("# Existing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	plans, err := Plan(project, "# Team Rules\n")
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if _, err := Apply(plans); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func TestEnsureStub(t *testing.T) {
	project := t.TempDir()
	wrote, err := EnsureStub(project)
	if err != nil {
		t.Fatalf("EnsureStub: %v", err)
	}
	if !wrote {
		t.Error("first EnsureStub should write")
	}

	// Second call: don't clobber.
	if err := os.WriteFile(Path(project), []byte("USER EDIT"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrote, err = EnsureStub(project)
	if err != nil {
		t.Fatalf("EnsureStub 2: %v", err)
	}
	if wrote {
		t.Error("EnsureStub wrote over existing file")
	}
	b, _ := os.ReadFile(Path(project))
	if string(b) != "USER EDIT" {
		t.Error("EnsureStub clobbered user content")
	}
}
