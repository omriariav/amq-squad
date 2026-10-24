package team

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/grants"
)

func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestWriteReadRoundTrip(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	in := Team{
		Session: "feature-x", Lead: "cto", Orchestrated: true, LeadMode: LeadModePlanner,
		ExecutionMode: ExecutionModeProjectTeam, OperatorInteraction: OperatorInteractionLeadPane,
		Members: []Member{
			{Role: "cto", Binary: "claude", Handle: "cto", Session: "feature-x", Model: "opus", Effort: "high", ActorMode: ActorModeReview},
			{Role: "senior-dev", Binary: "codex", Handle: "dev", Session: "feature-x", CWD: filepath.Join(dir, "wt"), ToolProfile: ToolProfileCoding},
			{Role: "researcher", Binary: "pi", Handle: "scout", Session: "feature-x"},
		},
	}
	if err := Write(dir, in); err != nil {
		t.Fatal(err)
	}
	got, err := Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	got.Squad, in.Squad, in.Project = Squad{}, Squad{}, dir
	if !reflect.DeepEqual(got, in) {
		t.Fatalf("round trip:\n%+v\n%+v", got, in)
	}
	raw, _ := os.ReadFile(SquadPath(dir))
	if !strings.Contains(string(raw), "lead: cto\n") || !strings.Contains(string(raw), "worktree: "+filepath.Join(dir, "wt")) {
		t.Fatalf("squad.yaml:\n%s", raw)
	}
	if _, err := os.Stat(filepath.Join(dir, DirName, "team.json")); !os.IsNotExist(err) {
		t.Fatal("a v2 team.json was written")
	}
}

func TestReadMissingAndNamedProfile(t *testing.T) {
	isolateHome(t)
	dir := t.TempDir()
	if _, err := Read(dir); !IsNotExist(err) || Exists(dir) {
		t.Fatalf("missing squad.yaml = %v", err)
	}
	if _, err := ReadFor(dir, "other"); !errors.Is(err, ErrNamedProfile) || !strings.Contains(err.Error(), "migrate") {
		t.Fatalf("named profile = %v", err)
	}
	if _, err := ReadFor(dir, "default"); !IsNotExist(err) {
		t.Fatalf("default profile = %v", err)
	}
}

func TestWriteRefusesWidening(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".amq-squad"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".amq-squad", "grants.yaml"), []byte("max_seats: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	in := Team{Members: []Member{{Role: "a", Binary: "claude"}, {Role: "b", Binary: "claude"}}}
	if err := Write(dir, in); !errors.Is(err, ErrWidens) {
		t.Fatalf("two seats under a cap of one = %v", err)
	}
	if Exists(dir) {
		t.Fatal("a refused squad.yaml was written")
	}
}

func TestEffectiveActorModeAndOperator(t *testing.T) {
	tm := Team{Lead: "cto", LeadMode: LeadModePlanner}
	if EffectiveActorMode(tm, Member{Role: "cto"}) != ActorModeReview || EffectiveActorMode(tm, Member{Role: "dev"}) != ActorModeImplementation {
		t.Fatal("actor modes")
	}
	op := EffectiveOperator(Team{OperatorInteraction: OperatorInteractionNOC})
	if op.Handle != DefaultOperatorHandle || op.InteractionMode != OperatorInteractionNOC || !op.PollRequired || op.Runnable {
		t.Fatalf("operator = %+v", op)
	}
	if err := ValidateOperatorInteractionMode("self_operator"); err == nil || !strings.Contains(err.Error(), "removed") {
		t.Fatalf("self_operator = %v", err)
	}
	if _, err := ParseSquad("operator_interaction: self_operator\n"+minimalSquad, "s", grants.Defaults()); err == nil {
		t.Fatal("self_operator accepted in squad.yaml")
	}
}

func TestMemberExtraArgs(t *testing.T) {
	cases := map[string][]string{"claude": {"--effort", "high"}, "codex": {"-c", "model_reasoning_effort=high"}, "pi": nil}
	for binary, want := range cases {
		if got := (Member{Binary: binary, Effort: "high"}).ExtraArgs(); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v", binary, got)
		}
	}
}

// Acceptance 6: no reference to the v2 profile model is left in the tree.
func TestNoV2ProfileModelLeft(t *testing.T) {
	removed := []string{
		"team.ReadProfile", "team.WriteProfile", "team.ExistsProfile", "team.ProfilePath", "team.ListProfiles",
		"team.WithProfileLock", "team.WriteProfileUnderLock", "team.ValidateProfileName", "team.DefaultProfile",
		"team.NormalizeForWrite", "team.OperatorConfig", "team.DefaultOperator()", "team.DisabledOperator",
		"team.AutonomousPolicy", "team.CompositionSeeded", "team.CompositionAutonomous", "team.LeadExecution",
		"team.SchemaVersion", "team.FileName", "team.TeamsDirName", "team.EffectiveCapabilities", "team.SupportsOperatorGates",
	}
	root := filepath.Join("..", "..")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "spike" || d.Name() == ".git" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "team_test.go") && strings.Contains(path, "internal/team") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, name := range removed {
			if strings.Contains(string(b), name) {
				t.Errorf("%s still references %s", path, name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
