package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

func TestTeamOperatorResumeRejectsDisabledUnpausedSession(t *testing.T) {
	dir := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}}})
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Operator.SelfOperator = &team.SelfOperatorPolicy{LeadRole: "cto", PolicyRevision: 7, Sessions: map[string]team.SelfOperatorSessionPolicy{"s": {Enabled: false, Paused: false, AllowedGateKinds: []string{"merge"}}}}
	if err := team.Write(dir, cfg); err != nil {
		t.Fatal(err)
	}
	err = runTeamOperator([]string{"self", "resume", "--project", dir, "--session", "s"})
	if err == nil || !strings.Contains(err.Error(), "cannot resume disabled") {
		t.Fatalf("resume error = %v", err)
	}
	after, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	if after.Operator.SelfOperator.PolicyRevision != 7 {
		t.Fatalf("revision changed to %d", after.Operator.SelfOperator.PolicyRevision)
	}
}

func TestTeamOperatorSetPauseResumeRevisionAndModeHistory(t *testing.T) {
	dir := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}, {Role: "qa", Handle: "qa", Binary: "codex", Session: "s"}}})
	if err := runTeamOperator([]string{"set", "--project", dir, "--mode", "self_operator", "--self", "cto", "--session", "s", "--allow", "merge"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ := team.Read(dir)
	if !team.EffectiveSelfOperator(cfg, "s").Enabled || cfg.Operator.SelfOperator.PolicyRevision != 1 {
		t.Fatalf("configured = %+v", cfg.Operator)
	}
	if err := runTeamOperator([]string{"set", "--project", dir, "--mode", "self_operator", "--self", "cto", "--session", "s"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = team.Read(dir)
	if cfg.Operator.SelfOperator.PolicyRevision != 1 {
		t.Fatalf("no-op revision = %d", cfg.Operator.SelfOperator.PolicyRevision)
	}
	if err := runTeamOperator([]string{"self", "pause", "--project", dir, "--session", "s"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = team.Read(dir)
	if !team.EffectiveSelfOperator(cfg, "s").Paused || cfg.Operator.SelfOperator.PolicyRevision != 2 {
		t.Fatalf("paused = %+v", cfg.Operator.SelfOperator)
	}
	if err := runTeamOperator([]string{"self", "resume", "--project", dir, "--session", "s"}); err != nil {
		t.Fatal(err)
	}
	if err := runTeamOperator([]string{"set", "--project", dir, "--mode", "separate_terminal"}); err != nil {
		t.Fatal(err)
	}
	cfg, _ = team.Read(dir)
	if cfg.Operator.SelfOperator == nil || team.EffectiveSelfOperator(cfg, "s").Enabled || cfg.Operator.SelfOperator.PolicyRevision != 4 {
		t.Fatalf("mode-away history = %+v", cfg.Operator)
	}
}

func TestTeamOperatorPolicyMutationRejectsVerifiedRunnableActors(t *testing.T) {
	dir := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "s"}, {Role: "qa", Handle: "qa", Binary: "codex", Session: "s"}}})
	old := resolveVerifiedCurrentPaneActor
	t.Cleanup(func() { resolveVerifiedCurrentPaneActor = old })
	resolveVerifiedCurrentPaneActor = func(_, _, _ string, _ team.Team) (verifiedOperatorActor, error) {
		return verifiedOperatorActor{Role: "cto", Handle: "cto"}, nil
	}
	for _, actor := range []string{"", "spoofed", "cto"} {
		t.Setenv("AM_ME", actor)
		err := runTeamOperator([]string{"set", "--project", dir, "--mode", "self_operator", "--self", "cto", "--session", "s", "--allow", "merge"})
		if err == nil || !strings.Contains(err.Error(), "cannot mutate") {
			t.Fatalf("actor %s mutation = %v", actor, err)
		}
	}
}

func TestTeamOperatorPolicyMutationRejectsActorFromDifferentSession(t *testing.T) {
	dir := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "session-a"}}})
	base := filepath.Join(t.TempDir(), "shared-control-mail")
	root := filepath.Join(base, "session-a")
	// Mutation ownership is intentionally more conservative than action
	// authorization: even a stale/mismatched record CWD must not let a live
	// roster pane edit policy for a different session.
	seedAgentRecord(t, base, "session-a", "cto", launch.Record{CWD: filepath.Join(dir, "wrong-cwd"), Binary: "codex", Role: "cto", Handle: "cto", TeamProfile: "default", Session: "session-a", Root: root, AgentPID: os.Getpid(), Tmux: &launch.TmuxInfo{PaneID: "%actor"}})
	oldBase := resolveOperatorActorBaseRoot
	resolveOperatorActorBaseRoot = func(project string) (string, error) {
		if project != dir {
			t.Fatalf("resolved actor base for project %q, want %q", project, dir)
		}
		return base, nil
	}
	t.Cleanup(func() { resolveOperatorActorBaseRoot = oldBase })
	t.Setenv("TMUX_PANE", "%actor")
	for _, me := range []string{"", "spoofed"} {
		t.Setenv("AM_ME", me)
		err := runTeamOperator([]string{"set", "--project", dir, "--mode", "self_operator", "--self", "cto", "--session", "session-b", "--allow", "merge"})
		if err == nil || !strings.Contains(err.Error(), "cannot mutate") {
			t.Fatalf("AM_ME=%q cross-session mutation = %v", me, err)
		}
	}
}

func TestTeamOperatorMutationOwnershipIgnoresActionAuthorizationMismatches(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*launch.Record, string)
	}{
		{name: "generic external adoption", mutate: func(rec *launch.Record, _ string) { rec.External = true; rec.AdoptionMode = adoptionModeExternal }},
		{name: "mismatched recorded root", mutate: func(rec *launch.Record, dir string) { rec.Root = filepath.Join(dir, "unrelated-root") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "session-a"}}})
			base := filepath.Join(t.TempDir(), "control-mail")
			root := filepath.Join(base, "session-a")
			rec := launch.Record{CWD: filepath.Join(dir, "wrong-cwd"), Binary: "codex", Role: "cto", Handle: "cto", TeamProfile: "default", Session: "session-a", Root: root, AgentPID: os.Getpid(), Tmux: &launch.TmuxInfo{PaneID: "%owner"}}
			tc.mutate(&rec, dir)
			seedAgentRecord(t, base, "session-a", "cto", rec)
			oldBase := resolveOperatorActorBaseRoot
			resolveOperatorActorBaseRoot = func(string) (string, error) { return base, nil }
			t.Cleanup(func() { resolveOperatorActorBaseRoot = oldBase })
			t.Setenv("TMUX_PANE", "%owner")
			t.Setenv("AM_ME", "spoofed")
			err := runTeamOperator([]string{"set", "--project", dir, "--mode", "self_operator", "--self", "cto", "--session", "session-b", "--allow", "merge"})
			if err == nil || !strings.Contains(err.Error(), "cannot mutate") {
				t.Fatalf("mutation mismatch allowed: %v", err)
			}
		})
	}
}
