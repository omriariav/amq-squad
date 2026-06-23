package autonomy

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

func autonomousTeam() team.Team {
	return team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Composition:  team.CompositionAutonomous,
		Autonomous: &team.AutonomousPolicy{
			MaxActiveAgents:    3,
			MaxTotalSpawns:     2,
			AllowedRoles:       []string{"worker", "reviewer"},
			AllowedRoleClasses: []string{"review"},
			BudgetTurns:        5,
			IdleReapMinutes:    10,
		},
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto"}},
	}
}

func TestSeededDefaultDeniesAutonomousSpawn(t *testing.T) {
	tm := autonomousTeam()
	tm.Composition = ""
	tm.Autonomous = nil
	d := evaluateDecision(tm, Request{Action: ActionSpawn, Role: "worker", RequestedByRole: "cto"})
	if d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "seeded") {
		t.Fatalf("seeded default should deny autonomous spawn: %+v", d)
	}
}

func TestAutonomousSpawnWithinPolicy(t *testing.T) {
	d := evaluateDecision(autonomousTeam(), Request{Action: ActionSpawn, Role: "worker", RequestedByRole: "cto", Reason: "parallel implementation"})
	if !d.Allowed {
		t.Fatalf("spawn should be allowed: %+v", d)
	}
}

func TestAutonomousSpawnDeniedByMaxAgentRoleAndBudget(t *testing.T) {
	tm := autonomousTeam()
	tm.Members = append(tm.Members,
		team.Member{Role: "a", Binary: "codex"},
		team.Member{Role: "b", Binary: "codex"},
	)
	if d := evaluateDecision(tm, Request{Action: ActionSpawn, Role: "worker", RequestedByRole: "cto"}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "max active") {
		t.Fatalf("max active denial mismatch: %+v", d)
	}

	tm = autonomousTeam()
	if d := evaluateDecision(tm, Request{Action: ActionSpawn, Role: "outside", RequestedByRole: "cto"}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "allowlist") {
		t.Fatalf("role allowlist denial mismatch: %+v", d)
	}

	tm = autonomousTeam()
	tm.Autonomous.State.BudgetTurnsUsed = 5
	if d := evaluateDecision(tm, Request{Action: ActionSpawn, Role: "worker", RequestedByRole: "cto"}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "budget") {
		t.Fatalf("budget denial mismatch: %+v", d)
	}
}

func TestAutonomousDeniesChildAuthorityAndPauseDisable(t *testing.T) {
	tm := autonomousTeam()
	if d := evaluateDecision(tm, Request{Action: ActionSpawn, Role: "worker", RequestedByRole: "worker", SourceIsChild: true}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "child messages are data") {
		t.Fatalf("child authority denial mismatch: %+v", d)
	}

	tm = autonomousTeam()
	tm.Autonomous.Paused = true
	if d := evaluateDecision(tm, Request{Action: ActionSpawn, Role: "worker", RequestedByRole: "cto"}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "paused") {
		t.Fatalf("pause denial mismatch: %+v", d)
	}

	tm = autonomousTeam()
	tm.Autonomous.Disabled = true
	if d := evaluateDecision(tm, Request{Action: ActionPrune, Role: "worker", RequestedByRole: "cto", IdleFor: 20 * time.Minute, TaskLinkageChecked: true}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "disabled") {
		t.Fatalf("disable denial mismatch: %+v", d)
	}
}

func TestAutonomousPruneWithinPolicy(t *testing.T) {
	d := evaluateDecision(autonomousTeam(), Request{Action: ActionPrune, Role: "worker", RequestedByRole: "cto", Reason: "idle", IdleFor: 12 * time.Minute, TaskLinkageChecked: true})
	if !d.Allowed {
		t.Fatalf("prune should be allowed: %+v", d)
	}
}

func TestAutonomousPruneEnforcesIdleReapAndTaskLinkage(t *testing.T) {
	tm := autonomousTeam()
	if d := evaluateDecision(tm, Request{Action: ActionPrune, Role: "worker", RequestedByRole: "cto"}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "measured idle") {
		t.Fatalf("missing idle duration denial mismatch: %+v", d)
	}
	if d := evaluateDecision(tm, Request{Action: ActionPrune, Role: "worker", RequestedByRole: "cto", IdleFor: 5 * time.Minute}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "below") {
		t.Fatalf("idle threshold denial mismatch: %+v", d)
	}
	if d := evaluateDecision(tm, Request{Action: ActionPrune, Role: "worker", RequestedByRole: "cto", IdleFor: 20 * time.Minute}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "task linkage") {
		t.Fatalf("missing task-linkage evidence denial mismatch: %+v", d)
	}
	if d := evaluateDecision(tm, Request{Action: ActionPrune, Role: "worker", RequestedByRole: "cto", IdleFor: 20 * time.Minute, TaskLinkageChecked: true, ActiveTaskIDs: []string{"t1"}}); d.Allowed || !strings.Contains(strings.Join(d.Reasons, " "), "active tasks") {
		t.Fatalf("active task denial mismatch: %+v", d)
	}
}

func TestAuthorizeSpawnPersistsCountersAndAuditBeforeAllowedReturn(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, autonomousTeam()); err != nil {
		t.Fatalf("write team: %v", err)
	}

	out, err := AuthorizeSpawn(dir, team.DefaultProfile, "s1", Request{
		Role:            "worker",
		RequestedByRole: "cto",
		Reason:          "parallel implementation",
		TaskID:          "t1",
		SourceMessageID: "m1",
	})
	if err != nil {
		t.Fatalf("AuthorizeSpawn: %v", err)
	}
	if !out.Decision.Allowed {
		t.Fatalf("spawn should be allowed: %+v", out.Decision)
	}
	got, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if got.Autonomous.State.TotalSpawns != 1 || got.Autonomous.State.BudgetTurnsUsed != 1 {
		t.Fatalf("counters not persisted before allowed return: %+v", got.Autonomous.State)
	}
	audit, err := os.ReadFile(out.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{`"action":"spawn"`, `"allowed":true`, `"task_id":"t1"`, `"source_message_id":"m1"`, `"total_spawns":1`, `"budget_turns_used":1`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit missing %q:\n%s", want, audit)
		}
	}
}

func TestAuthorizePrunePersistsBudgetCounterIdleEvidenceAndAudit(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, autonomousTeam()); err != nil {
		t.Fatalf("write team: %v", err)
	}

	out, err := AuthorizePrune(dir, team.DefaultProfile, "s1", Request{
		Role:               "worker",
		RequestedByRole:    "cto",
		Reason:             "idle reap",
		IdleFor:            15 * time.Minute,
		TaskLinkageChecked: true,
	})
	if err != nil {
		t.Fatalf("AuthorizePrune: %v", err)
	}
	if !out.Decision.Allowed {
		t.Fatalf("prune should be allowed: %+v", out.Decision)
	}
	got, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if got.Autonomous.State.TotalSpawns != 0 || got.Autonomous.State.BudgetTurnsUsed != 1 {
		t.Fatalf("prune counters mismatch: %+v", got.Autonomous.State)
	}
	audit, err := os.ReadFile(out.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{`"action":"prune"`, `"allowed":true`, `"idle_for_seconds":900`, `"task_linkage_checked":true`, `"active_task_ids":[]`, `"budget_turns_used":1`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit missing %q:\n%s", want, audit)
		}
	}
}

func TestAuthorizePruneDeniesMissingTaskLinkageEvidenceWithAudit(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, autonomousTeam()); err != nil {
		t.Fatalf("write team: %v", err)
	}

	out, err := AuthorizePrune(dir, team.DefaultProfile, "s1", Request{
		Role:            "worker",
		RequestedByRole: "cto",
		IdleFor:         15 * time.Minute,
	})
	if err != nil {
		t.Fatalf("AuthorizePrune missing linkage audit: %v", err)
	}
	if out.Decision.Allowed {
		t.Fatalf("prune should be denied: %+v", out.Decision)
	}
	got, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if got.Autonomous.State.TotalSpawns != 0 || got.Autonomous.State.BudgetTurnsUsed != 0 {
		t.Fatalf("denied decision mutated counters: %+v", got.Autonomous.State)
	}
	audit, err := os.ReadFile(out.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{`"allowed":false`, `"task_linkage_checked":false`, `"decision_reasons":["prune requires explicit active task linkage check"]`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit missing %q:\n%s", want, audit)
		}
	}
}

func TestAuthorizeDeniedDecisionWritesAuditWithoutCounters(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, autonomousTeam()); err != nil {
		t.Fatalf("write team: %v", err)
	}

	out, err := AuthorizePrune(dir, team.DefaultProfile, "s1", Request{
		Role:               "worker",
		RequestedByRole:    "cto",
		IdleFor:            15 * time.Minute,
		TaskLinkageChecked: true,
		ActiveTaskIDs:      []string{"t1"},
	})
	if err != nil {
		t.Fatalf("AuthorizePrune denied audit: %v", err)
	}
	if out.Decision.Allowed {
		t.Fatalf("prune should be denied: %+v", out.Decision)
	}
	got, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if got.Autonomous.State.TotalSpawns != 0 || got.Autonomous.State.BudgetTurnsUsed != 0 {
		t.Fatalf("denied decision mutated counters: %+v", got.Autonomous.State)
	}
	audit, err := os.ReadFile(out.AuditPath)
	if err != nil {
		t.Fatalf("read audit: %v", err)
	}
	for _, want := range []string{`"allowed":false`, `"active_task_ids":["t1"]`, `"decision_reasons":["cannot prune worker with active tasks"]`} {
		if !strings.Contains(string(audit), want) {
			t.Fatalf("audit missing %q:\n%s", want, audit)
		}
	}
}

func TestAuthorizeInvalidSessionDoesNotReturnAllowedOrMutateCounters(t *testing.T) {
	dir := t.TempDir()
	if err := team.WriteProfile(dir, team.DefaultProfile, autonomousTeam()); err != nil {
		t.Fatalf("write team: %v", err)
	}

	if out, err := AuthorizeSpawn(dir, team.DefaultProfile, "bad/session", Request{Role: "worker", RequestedByRole: "cto"}); err == nil || out.Decision.Allowed {
		t.Fatalf("invalid session should fail before allowed decision, out=%+v err=%v", out, err)
	}
	got, err := team.ReadProfile(dir, team.DefaultProfile)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if got.Autonomous.State.TotalSpawns != 0 || got.Autonomous.State.BudgetTurnsUsed != 0 {
		t.Fatalf("invalid session mutated counters: %+v", got.Autonomous.State)
	}
}

func TestAppendAuditWritesJSONL(t *testing.T) {
	dir := t.TempDir()
	path, err := AppendAudit(dir, "s1", AuditEvent{
		Time:            time.Date(2026, 6, 23, 11, 0, 0, 0, time.UTC),
		Action:          ActionSpawn,
		Role:            "worker",
		TaskID:          "t1",
		Reason:          "parallel implementation",
		RequestedByRole: "cto",
		Allowed:         true,
		Policy:          map[string]any{"max_active_agents": 3},
	})
	if err != nil {
		t.Fatalf("AppendAudit: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"session":"s1"`, `"action":"spawn"`, `"role":"worker"`, `"allowed":true`} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("audit missing %q:\n%s", want, got)
		}
	}
}
