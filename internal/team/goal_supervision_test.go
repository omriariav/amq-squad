package team

import (
	"strings"
	"testing"
)

func TestEffectiveGoalSupervisionPolicyDefaultsToManual(t *testing.T) {
	got := EffectiveGoalSupervisionPolicy(Team{})
	if got.Mode != GoalSupervisionManual || got.Revision != 1 || got.Source != "compatibility-default" {
		t.Fatalf("default policy = %+v, want compatibility manual revision 1", got)
	}

	got = EffectiveGoalSupervisionPolicy(Team{
		GoalSupervision: &GoalSupervisionPolicy{Mode: GoalSupervisionSafeAuto, Revision: 7},
	})
	if got.Mode != GoalSupervisionSafeAuto || got.Revision != 7 || got.Source != "profile" {
		t.Fatalf("explicit policy = %+v, want profile safe-auto revision 7", got)
	}

	got = EffectiveGoalSupervisionPolicy(Team{GoalSupervision: &GoalSupervisionPolicy{}})
	if got.Mode != GoalSupervisionManual || got.Revision != 1 || got.Source != "profile" {
		t.Fatalf("empty explicit policy = %+v, want profile manual revision 1", got)
	}
}

func TestValidateGoalSupervisionPolicyModes(t *testing.T) {
	for _, mode := range []string{"", GoalSupervisionManual, GoalSupervisionNotifyOnly, GoalSupervisionSafeAuto} {
		if err := validateGoalSupervisionPolicy(&GoalSupervisionPolicy{Mode: mode}); err != nil {
			t.Fatalf("mode %q: %v", mode, err)
		}
	}
	if err := validateGoalSupervisionPolicy(&GoalSupervisionPolicy{Mode: "always"}); err == nil ||
		!strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("invalid mode error = %v, want invalid mode", err)
	}
	if err := validateGoalSupervisionPolicy(&GoalSupervisionPolicy{Mode: GoalSupervisionManual, Revision: -1}); err == nil ||
		!strings.Contains(err.Error(), "cannot be negative") {
		t.Fatalf("negative revision error = %v, want cannot be negative", err)
	}
}

func TestCloneRosterForSessionDropsGoalSupervisionPolicy(t *testing.T) {
	source := Team{
		GoalSupervision: &GoalSupervisionPolicy{Mode: GoalSupervisionSafeAuto, Revision: 3},
		Members:         []Member{{Role: "cto", Binary: "codex", Session: "old"}},
	}
	clone, err := CloneRosterForSession(source, "new")
	if err != nil {
		t.Fatalf("CloneRosterForSession: %v", err)
	}
	if clone.GoalSupervision != nil {
		t.Fatalf("clone goal supervision = %+v, want nil fail-safe reset", clone.GoalSupervision)
	}
	if source.GoalSupervision == nil || source.GoalSupervision.Mode != GoalSupervisionSafeAuto {
		t.Fatalf("clone mutated source policy: %+v", source.GoalSupervision)
	}
}

func TestGoalSupervisionPolicyWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := Team{
		GoalSupervision: &GoalSupervisionPolicy{Mode: GoalSupervisionNotifyOnly, Revision: 4},
		Members: []Member{{
			Role: "cto", Binary: "codex", Handle: "cto", Session: "release",
			ActorMode: ActorModeReview,
		}},
	}
	if err := WriteProfile(dir, "supervised", in); err != nil {
		t.Fatalf("WriteProfile: %v", err)
	}
	got, err := ReadProfile(dir, "supervised")
	if err != nil {
		t.Fatalf("ReadProfile: %v", err)
	}
	if got.GoalSupervision == nil ||
		got.GoalSupervision.Mode != GoalSupervisionNotifyOnly ||
		got.GoalSupervision.Revision != 4 {
		t.Fatalf("round-trip policy = %+v, want notify-only revision 4", got.GoalSupervision)
	}
}
