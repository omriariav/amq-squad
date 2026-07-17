package task

import (
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func seedLeadershipTeam(t *testing.T, dir string) {
	t.Helper()
	if err := team.WriteProfile(dir, "release", team.Team{
		Orchestrated: true, Lead: "cto",
		Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLeadershipHandoffPersistsEpochForRecovery(t *testing.T) {
	dir := t.TempDir()
	seedLeadershipTeam(t, dir)
	first, err := HandoffLeadershipForProfile(dir, "release", "s", LeadershipHandoffInput{
		ExpectedEpoch: 0, From: "cto", To: "cto-recovery", Reason: "lead pane lost",
		Evidence: "thread:p2p/cto__cto-recovery", Now: fixedNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Epoch != 1 || first.CurrentLead != "cto-recovery" || len(first.Handoffs) != 1 {
		t.Fatalf("first handoff = %+v", first)
	}
	recovered, err := ReadLeadershipForProfile(dir, "release", "s")
	if err != nil || recovered.Epoch != 1 || recovered.Handoffs[0].Evidence == "" {
		t.Fatalf("recovered leadership = %+v err=%v", recovered, err)
	}

	second, err := HandoffLeadershipForProfile(dir, "release", "s", LeadershipHandoffInput{
		ExpectedEpoch: 1, From: "cto-recovery", To: "cto-2", Reason: "planned rotation", Now: fixedNow.Add(time.Minute),
	})
	if err != nil || second.Epoch != 2 || second.CurrentLead != "cto-2" {
		t.Fatalf("second handoff = %+v err=%v", second, err)
	}
}

func TestLeadershipHandoffRejectsStaleEpochAndActorWithoutMutation(t *testing.T) {
	dir := t.TempDir()
	seedLeadershipTeam(t, dir)
	_, err := HandoffLeadershipForProfile(dir, "release", "s", LeadershipHandoffInput{
		ExpectedEpoch: 0, From: "cto", To: "recovery", Reason: "recover", Now: fixedNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []LeadershipHandoffInput{
		{ExpectedEpoch: 0, From: "cto", To: "stale", Reason: "stale", Now: fixedNow.Add(time.Minute)},
		{ExpectedEpoch: 1, From: "cto", To: "wrong actor", Reason: "stale actor", Now: fixedNow.Add(time.Minute)},
	} {
		if _, err := HandoffLeadershipForProfile(dir, "release", "s", in); err == nil || (!strings.Contains(err.Error(), "epoch") && !strings.Contains(err.Error(), "stale")) {
			t.Fatalf("stale handoff err=%v", err)
		}
	}
	state, err := ReadLeadershipForProfile(dir, "release", "s")
	if err != nil || state.Epoch != 1 || state.CurrentLead != "recovery" || len(state.Handoffs) != 1 {
		t.Fatalf("rejected handoff mutated state: %+v err=%v", state, err)
	}
}

func TestLeadershipEpochZeroRejectsNonConfiguredIncumbent(t *testing.T) {
	dir := t.TempDir()
	seedLeadershipTeam(t, dir)
	_, err := HandoffLeadershipForProfile(dir, "release", "s", LeadershipHandoffInput{
		ExpectedEpoch: 0, From: "attacker", To: "recovery", Reason: "invented", Now: fixedNow,
	})
	if err == nil || !strings.Contains(err.Error(), "configured lead") {
		t.Fatalf("non-incumbent epoch establishment err=%v", err)
	}
	state, err := ReadLeadershipForProfile(dir, "release", "s")
	if err != nil || state.Epoch != 0 || state.CurrentLead != "" {
		t.Fatalf("rejected initial handoff mutated state: %+v err=%v", state, err)
	}
}
