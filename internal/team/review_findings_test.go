package team

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

// Codex P1-1: apply is all or nothing. A seat directory that turned unwritable
// after the proposal fails the apply before squad.yaml, team-rules.md or the
// root pointer files change.
func TestPlanApplyIsAllOrNothing(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix directory permissions as a non-root user")
	}
	isolateHome(t)
	dir := t.TempDir()
	seatDir := filepath.Join(dir, "seat")
	if err := os.MkdirAll(seatDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text := "schema: 1\nseats:\n  - handle: a\n    runtime: claude\n  - handle: b\n    runtime: codex\n    worktree: seat\n"
	s, err := ParseSquad(text, "squad.yaml", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPlan(dir, text, FromSquad(dir, s), "# Team Rules\n")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(seatDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(seatDir, 0o755) })
	if err := p.Apply(p.Digest); err == nil {
		t.Fatal("apply into an unwritable seat directory succeeded")
	}
	if Exists(dir) {
		t.Fatal("squad.yaml was written before the failure")
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, "team-rules.md")); !os.IsNotExist(err) {
		t.Fatalf("team-rules.md was written before the failure: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); string(b) != "# mine\n" {
		t.Fatalf("CLAUDE.md changed: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md was written before the failure: %v", err)
	}
	// and a file changed after the proposal refuses the whole apply
	_ = os.Chmod(seatDir, 0o755)
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("# edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Apply(p.Digest); err == nil || Exists(dir) {
		t.Fatalf("stale plan applied: %v", err)
	}
}

// Codex P1-2: squad.yaml is written through an exclusive random temp; a
// symlink planted at the old predictable <target>.<pid>.tmp name is never
// followed.
func TestWriteDoesNotFollowAPlantedTempName(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "grants.yaml")
	if err := os.WriteFile(victim, []byte("max_seats: 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, fmt.Sprintf("%s.%d.tmp", SquadPath(dir), os.Getpid())); err != nil {
		t.Fatal(err)
	}
	if err := Write(dir, Team{Members: []Member{{Role: "cto", Binary: "claude"}}}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "max_seats: 2\n" {
		t.Fatalf("the planted symlink target was overwritten: %q", b)
	}
}

// Codex P2-5: the lead is a handle; two seats may share its role.
func TestLeadHandleSurvivesASharedRole(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	text := "schema: 1\nlead: b\nlead_mode: planner\nseats:\n  - handle: a\n    role: cto\n    runtime: claude\n  - handle: b\n    role: cto\n    runtime: claude\n"
	s, err := ParseSquad(text, "squad.yaml", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	tm := FromSquad(dir, s)
	if tm.LeadHandle != "b" || EffectiveActorMode(tm, tm.Members[0]) != ActorModeImplementation || EffectiveActorMode(tm, tm.Members[1]) != ActorModeReview {
		t.Fatalf("lead %q, modes %s/%s", tm.LeadHandle, EffectiveActorMode(tm, tm.Members[0]), EffectiveActorMode(tm, tm.Members[1]))
	}
	if err := Write(dir, tm); err != nil {
		t.Fatal(err)
	}
	back, err := Read(dir)
	if err != nil || back.Squad.Lead != "b" || !IsLead(back, back.Members[1]) || IsLead(back, back.Members[0]) {
		t.Fatalf("after a write the lead is %q (%v)", back.Squad.Lead, err)
	}
}

// Codex P2-6: rendering and parsing share one escaping rule.
func TestRenderRoundTripsQuotesAndBackslashes(t *testing.T) {
	text := "schema: 1\nprotected_paths:\n  - 'a\"b'\n  - 'c\\d'\n  - \"e\\\"f\"\n  - 'it''s'\nseats:\n  - handle: dev\n    runtime: codex\n    worktree: '../w\"t\\x'\n"
	s, err := ParseSquad(text, "squad.yaml", grants.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	want := []string{`a"b`, `c\d`, `e"f`, `it's`}
	if !reflect.DeepEqual(s.Grants.ProtectedPaths, want) || s.Seats[0].Worktree != `../w"t\x` {
		t.Fatalf("parsed paths %q, worktree %q", s.Grants.ProtectedPaths, s.Seats[0].Worktree)
	}
	again, err := ParseSquad(Render(s), "render", grants.Defaults())
	if err != nil || !reflect.DeepEqual(again, s) {
		t.Fatalf("round trip:\n%s\n%+v\n%v", Render(s), again, err)
	}
}
