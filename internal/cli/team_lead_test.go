package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func TestTeamLeadSetShowClear(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"},
		},
	})

	if _, _, err := captureOutput(t, func() error {
		return runTeamLead([]string{"set", "cto"})
	}); err != nil {
		t.Fatalf("team lead set: %v", err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if !cfg.Orchestrated || cfg.Lead != "cto" {
		t.Fatalf("lead config = orchestrated:%v lead:%q, want true/cto", cfg.Orchestrated, cfg.Lead)
	}
	out, _, err := captureOutput(t, func() error {
		return runTeamLead([]string{"show", "--json"})
	})
	if err != nil {
		t.Fatalf("team lead show: %v", err)
	}
	env := decodeJSONEnvelope[teamLeadData](t, out)
	if !env.Data.Orchestrated || env.Data.Lead != "cto" || env.Data.LeadHandle != "cto" {
		t.Fatalf("team_lead data = %+v", env.Data)
	}

	if _, _, err := captureOutput(t, func() error {
		return runTeamLead([]string{"clear"})
	}); err != nil {
		t.Fatalf("team lead clear: %v", err)
	}
	cfg, err = team.Read(dir)
	if err != nil {
		t.Fatalf("read cleared team: %v", err)
	}
	if cfg.Orchestrated || cfg.Lead != "" || cfg.LeadMode != "" {
		t.Fatalf("cleared lead config = orchestrated:%v lead:%q lead_mode:%q, want false/empty/empty", cfg.Orchestrated, cfg.Lead, cfg.LeadMode)
	}
}

func TestTeamLeadSetPlannerLeadMode(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-350"},
			{Role: "fullstack", Binary: "codex", Handle: "fullstack", Session: "issue-350"},
		},
	})

	if _, _, err := captureOutput(t, func() error {
		return runTeamLead([]string{"set", "cto", "--lead-mode", "planner"})
	}); err != nil {
		t.Fatalf("team lead set --lead-mode planner: %v", err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if cfg.LeadMode != team.LeadModePlanner {
		t.Fatalf("lead_mode = %q, want planner", cfg.LeadMode)
	}
	out, _, err := captureOutput(t, func() error {
		return runTeamLead([]string{"show", "--json"})
	})
	if err != nil {
		t.Fatalf("team lead show: %v", err)
	}
	env := decodeJSONEnvelope[teamLeadData](t, out)
	if env.Data.LeadMode != team.LeadModePlanner {
		t.Fatalf("team_lead data = %+v, want planner mode", env.Data)
	}
}

func TestTeamLeadSetPreservesPlannerLeadModeWhenFlagOmitted(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-350"},
			{Role: "architect", Binary: "codex", Handle: "architect", Session: "issue-350"},
		},
		Orchestrated: true,
		Lead:         "cto",
		LeadMode:     team.LeadModePlanner,
	})

	if _, _, err := captureOutput(t, func() error {
		return runTeamLead([]string{"set", "architect"})
	}); err != nil {
		t.Fatalf("team lead set without lead-mode: %v", err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if cfg.Lead != "architect" || cfg.LeadMode != team.LeadModePlanner {
		t.Fatalf("lead config = lead:%q lead_mode:%q, want architect/planner", cfg.Lead, cfg.LeadMode)
	}
}

func TestTeamLeadClearRemovesStaleLeadMode(t *testing.T) {
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-350"}},
		Orchestrated: true,
		Lead:         "cto",
		LeadMode:     team.LeadModePlanner,
	})

	if _, _, err := captureOutput(t, func() error {
		return runTeamLead([]string{"clear"})
	}); err != nil {
		t.Fatalf("team lead clear: %v", err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if cfg.LeadMode != "" {
		t.Fatalf("lead_mode after clear = %q, want empty", cfg.LeadMode)
	}
}

func TestLeadRegisterWritesExternalRecordAndSetsLead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	var wakeOpts []leadWakeOptions
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		wakeOpts = append(wakeOpts, opts)
		return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	out, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
	})
	if err != nil {
		t.Fatalf("lead register: %v\n%s", err, out)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if !cfg.Orchestrated || cfg.Lead != "cto" {
		t.Fatalf("lead register config = orchestrated:%v lead:%q, want true/cto", cfg.Orchestrated, cfg.Lead)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "cto"))
	if err != nil {
		t.Fatalf("read external launch record: %v", err)
	}
	if !rec.External || rec.AgentPID != 0 || rec.Tmux == nil || rec.Tmux.PaneID != "%5" || rec.Tmux.Target != "external" {
		t.Fatalf("external record = %+v", rec)
	}
	if rec.AdoptionMode != adoptionModeExternalProjectLead {
		t.Fatalf("AdoptionMode = %q, want %q", rec.AdoptionMode, adoptionModeExternalProjectLead)
	}
	wantAgentDir := filepath.Join(base, "issue-96", "agents", "cto")
	if rec.WakePID != 2222 || filepath.Base(rec.WakeRecordID) != ".wake.lock" || !sameResolvedDir(filepath.Dir(rec.WakeRecordID), wantAgentDir) || rec.WakeRecordDigest == "" {
		t.Fatalf("wake binding = pid:%d id:%q digest:%q", rec.WakePID, rec.WakeRecordID, rec.WakeRecordDigest)
	}
	if len(wakeOpts) != 1 {
		t.Fatalf("wake start calls = %d, want 1", len(wakeOpts))
	}
	if wakeOpts[0].Root != filepath.Join(base, "issue-96") || wakeOpts[0].Handle != "cto" || !wakeOpts[0].Require {
		t.Fatalf("wake opts = %+v", wakeOpts[0])
	}
	if !strings.Contains(out, "wake: ready") {
		t.Fatalf("lead register output should report wake readiness:\n%s", out)
	}
}

func TestLeadRegisterRejectsGlobalPaneAsProjectLead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: goalOrchestratorRole, Binary: "codex", Handle: "orchestrator", Session: "issue-96"},
		},
	})
	seedAgentRecord(t, base, "issue-96", "orchestrator", launch.Record{
		CWD:          dir,
		Binary:       "codex",
		Handle:       "orchestrator",
		Role:         goalOrchestratorRole,
		Session:      "issue-96",
		Root:         filepath.Join(base, "issue-96"),
		TeamProfile:  team.DefaultProfile,
		External:     true,
		AdoptionMode: adoptionModeExternal,
		Tmux:         &launch.TmuxInfo{Session: "tmux-main", WindowID: "@7", WindowName: "noc", PaneID: "%99", Target: "external"},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "noc", PaneID: "%99"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		t.Fatal("wake must not start for rejected adoption")
		return leadWakeResult{}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	_, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
	})
	if err == nil || !strings.Contains(err.Error(), "pane already has launch identity") || !strings.Contains(err.Error(), goalOrchestratorRole) {
		t.Fatalf("lead register global pane err = %v, want boundary rejection", err)
	}
	if _, err := launch.Read(filepath.Join(base, "issue-96", "agents", "cto")); err == nil {
		t.Fatal("rejected adoption must not write cto launch record")
	}
}

func TestLeadRegisterProjectLeadNoWakeRequiresCompatReason(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })

	_, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead", "--no-wake"})
	})
	if err == nil || !strings.Contains(err.Error(), "--compat-no-wake --reason") {
		t.Fatalf("lead register --no-wake err = %v, want compat reason rejection", err)
	}
}

func TestLeadRegisterGlobalOrchestratorNoWakeAllowed(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Orchestrated:  true,
		Lead:          goalOrchestratorRole,
		ExecutionMode: executionModeGlobalOrchestrator,
		Members:       []team.Member{{Role: goalOrchestratorRole, Binary: "codex", Handle: "orchestrator", Session: "issue-96"}},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "noc", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	called := false
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		called = true
		return leadWakeResult{}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	out, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", goalOrchestratorRole, "--session", "issue-96", "--no-wake"})
	})
	if err != nil {
		t.Fatalf("global orchestrator --no-wake should pass: %v\n%s", err, out)
	}
	if called {
		t.Fatal("global orchestrator --no-wake must not start wake")
	}
	if !strings.Contains(out, "wake: skipped") {
		t.Fatalf("output should report skipped wake:\n%s", out)
	}
}

// TestLeadRegisterStartsDrainInjectingWakeSidecar proves the #283 plumbing for
// the lead-register caller: the wake sidecar is started with the standard drain
// inject-cmd and that instruction is persisted on the launch record as durable
// evidence (resume repair is via re-register; see the re-register test).
func TestLeadRegisterStartsDrainInjectingWakeSidecar(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	var wakeOpts []leadWakeOptions
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		wakeOpts = append(wakeOpts, opts)
		return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
	}); err != nil {
		t.Fatalf("lead register: %v", err)
	}
	if len(wakeOpts) != 1 || wakeOpts[0].WakeInjectCmd != wakeDrainInject() {
		t.Fatalf("wake sidecar must be started with the drain inject-cmd: %+v", wakeOpts)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "cto"))
	if err != nil {
		t.Fatalf("read external launch record: %v", err)
	}
	if rec.WakeInjectCmd != wakeDrainInject() {
		t.Fatalf("launch record must persist WakeInjectCmd as resume-repair evidence, got %q", rec.WakeInjectCmd)
	}
}

// TestLeadRegisterReapplyDrainInjectOnReRegister proves the #283 resume-repair
// path: re-running lead register (the way an external lead is brought back)
// reapplies the drain inject-cmd on the new wake start and keeps it persisted.
// This is the resume mechanism for the external wake path; coop-exec restore
// deliberately does not carry inject-cmd (see restore_test.go).
func TestLeadRegisterReapplyDrainInjectOnReRegister(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	var wakeOpts []leadWakeOptions
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		wakeOpts = append(wakeOpts, opts)
		return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	register := func() error {
		_, _, err := captureOutput(t, func() error {
			return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
		})
		return err
	}
	if err := register(); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := register(); err != nil {
		t.Fatalf("re-register (resume repair): %v", err)
	}
	if len(wakeOpts) != 2 {
		t.Fatalf("expected two wake starts, got %d", len(wakeOpts))
	}
	for i, o := range wakeOpts {
		if o.WakeInjectCmd != wakeDrainInject() {
			t.Fatalf("wake start %d must reapply drain inject-cmd, got %q", i, o.WakeInjectCmd)
		}
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "cto"))
	if err != nil {
		t.Fatalf("read external launch record: %v", err)
	}
	if rec.WakeInjectCmd != wakeDrainInject() {
		t.Fatalf("re-register must keep WakeInjectCmd persisted, got %q", rec.WakeInjectCmd)
	}
}

func TestLeadRegisterPreservesExistingNativeGoalBinding(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members:      []team.Member{{Role: "cto", Binary: "claude", Handle: "cto", Session: "issue-96"}},
		Orchestrated: true,
		Lead:         "cto",
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary: "claude", Handle: "cto", Role: "cto", AgentPID: 4242,
		GoalBinding: &launch.GoalBinding{
			Mode:       "native_goal",
			NativeGoal: true,
			Source:     "goal-control",
			Command:    `/goal --goal "ship"`,
			Detail:     "native /goal delivered as a first-class claim-once control action",
		},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	prevWake := leadWakeStarter
	prevSignal := externalLeadWakeProcessGroupSignal
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
	}
	externalLeadWakeProcessGroupSignal = func(int, syscall.Signal) error { return syscall.ESRCH }
	t.Cleanup(func() { leadWakeStarter, externalLeadWakeProcessGroupSignal = prevWake, prevSignal })
	prevInspector := statusPaneInspector
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		if id == "%5" {
			return tmuxpane.TmuxPane{PaneID: "%5"}, true
		}
		return tmuxpane.TmuxPane{}, false
	}
	t.Cleanup(func() { statusPaneInspector = prevInspector })

	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
	}); err != nil {
		t.Fatalf("lead register: %v", err)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "cto"))
	if err != nil {
		t.Fatalf("read external launch record: %v", err)
	}
	if rec.GoalBinding == nil || !rec.GoalBinding.NativeGoal || rec.GoalBinding.Source != "goal-control" || rec.GoalBinding.Command == "" {
		t.Fatalf("goal binding was not preserved: %+v", rec.GoalBinding)
	}
	if !rec.External || rec.Tmux == nil || rec.Tmux.Target != "external" {
		t.Fatalf("external registration fields = %+v", rec)
	}
	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		JSON:             true,
		Probe:            statusProbe(map[int]bool{}, map[int]bool{}, time.Now()),
	})
	if err != nil {
		t.Fatalf("status after lead register: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	if env.Data.GoalBinding.Mode != "native_goal" || !env.Data.GoalBinding.Verified || env.Data.GoalBinding.NativeSource != "goal-control" {
		t.Fatalf("status goal binding after lead register = %+v, want preserved native goal", env.Data.GoalBinding)
	}
	if len(env.Data.Records) != 1 || !env.Data.Records[0].OperatorVisible || env.Data.Records[0].AdoptionMode != adoptionModeExternalProjectLead {
		t.Fatalf("status record after lead register = %+v, want operator-visible external lead", env.Data.Records)
	}
}

func TestPreserveExternalGoalBindingRejectsInvalidClaudeBinding(t *testing.T) {
	validLegacy := launch.Record{
		Binary: "claude", Role: "cto", Session: "issue-460",
		GoalBinding: &launch.GoalBinding{
			Mode: "native_goal", NativeGoal: true, Source: "goal-control", Command: `/goal --goal "ship"`,
			Detail: "native /goal delivered as a first-class claim-once control action",
		},
	}
	if !preserveExternalGoalBinding(validLegacy, nil, "cto", "issue-460") {
		t.Fatal("valid legacy Claude binding was not preserved")
	}
	for name, binding := range map[string]*launch.GoalBinding{
		"corrupt":        {Mode: "native_goal", NativeGoal: true, Command: `/goal --goal "ship" --unknown value`},
		"typed mismatch": {Mode: "native_goal", NativeGoal: true, Command: `/goal --goal "ship"`, Goal: "other"},
	} {
		t.Run(name, func(t *testing.T) {
			rec := validLegacy
			rec.GoalBinding = binding
			if preserveExternalGoalBinding(rec, nil, "cto", "issue-460") {
				t.Fatalf("invalid Claude binding was preserved: %+v", binding)
			}
		})
	}
}

func TestLeadRegisterWriteFailurePreservesTeamLeadConfig(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "qa",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"},
		},
	})
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir fake base: %v", err)
	}
	agentDir := filepath.Join(base, "issue-96", "agents", "cto")
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatalf("mkdir fake agent dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "extensions"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed blocking launch extension path: %v", err)
	}
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	prevWake := leadWakeStarter
	prevSignal := externalLeadWakeProcessGroupSignal
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
	}
	externalLeadWakeProcessGroupSignal = func(int, syscall.Signal) error { return syscall.ESRCH }
	t.Cleanup(func() { leadWakeStarter, externalLeadWakeProcessGroupSignal = prevWake, prevSignal })

	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
	}); err == nil || !strings.Contains(err.Error(), "write external launch record") {
		t.Fatalf("lead register err = %v, want launch record write failure", err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if !cfg.Orchestrated || cfg.Lead != "qa" {
		t.Fatalf("team config after failed register = orchestrated:%v lead:%q, want preserved true/qa", cfg.Orchestrated, cfg.Lead)
	}
}

func TestLeadRegisterPostWriteRollbackCASGatesWakeOwnership(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replace     bool
		wantSignals bool
	}{
		{name: "applied rollback stops owned wake", wantSignals: true},
		{name: "concurrent replacement preserves record and wake", replace: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := setupFakeAMQSessionRoots(t)
			seedTeam(t, team.Team{Orchestrated: true, Lead: "cto", Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}})
			prevPane := currentPaneIdentity
			currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
				return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
			}
			t.Cleanup(func() { currentPaneIdentity = prevPane })
			prevWake, prevSignal := leadWakeStarter, externalLeadWakeProcessGroupSignal
			leadWakeStarter = func(leadWakeOptions) (leadWakeResult, error) {
				return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
			}
			var signals []syscall.Signal
			externalLeadWakeProcessGroupSignal = func(pgid int, signal syscall.Signal) error {
				if pgid != 2222 {
					t.Fatalf("wake pgid=%d want=2222", pgid)
				}
				signals = append(signals, signal)
				if signal == 0 {
					return syscall.ESRCH
				}
				return nil
			}
			t.Cleanup(func() { leadWakeStarter, externalLeadWakeProcessGroupSignal = prevWake, prevSignal })
			prevAfterWrite := externalLeadAfterRecordWrite
			var replacement launch.Record
			externalLeadAfterRecordWrite = func(agentDir string, written launch.Record) error {
				if tc.replace {
					replacement = written
					replacement.Conversation = "concurrent-replacement"
					if err := launch.Write(agentDir, replacement); err != nil {
						return err
					}
				}
				return fmt.Errorf("forced post-write failure")
			}
			t.Cleanup(func() { externalLeadAfterRecordWrite = prevAfterWrite })

			_, _, err := captureOutput(t, func() error {
				return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
			})
			if err == nil || !strings.Contains(err.Error(), "forced post-write failure") {
				t.Fatalf("post-write failure=%v", err)
			}
			agentDir := filepath.Join(base, "issue-96", "agents", "cto")
			stored, readErr := launch.Read(agentDir)
			if tc.replace {
				if readErr != nil || !reflect.DeepEqual(stored, replacement) {
					t.Fatalf("replacement preservation err=%v got=%+v want=%+v", readErr, stored, replacement)
				}
				if len(signals) != 0 {
					t.Fatalf("concurrent replacement wake was signaled: %v", signals)
				}
				return
			}
			if !os.IsNotExist(readErr) {
				t.Fatalf("applied rollback left record: %v %+v", readErr, stored)
			}
			if !reflect.DeepEqual(signals, []syscall.Signal{syscall.SIGTERM, 0}) {
				t.Fatalf("applied rollback signals=%v", signals)
			}
		})
	}
}

func TestLeadRegisterNoWakeSkipsWakeStarter(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	prevPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prevPane })
	called := false
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		called = true
		return leadWakeResult{}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	out, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead", "--no-wake", "--compat-no-wake", "--reason", "polling via NOC cadence"})
	})
	if err != nil {
		t.Fatalf("lead register --no-wake: %v\n%s", err, out)
	}
	if called {
		t.Fatal("--no-wake must not start amq wake")
	}
	if !strings.Contains(out, "wake: skipped") {
		t.Fatalf("output should report skipped wake:\n%s", out)
	}
	rec, err := launch.Read(filepath.Join(os.Getenv("AMQ_FAKE_BASE"), "issue-96", "agents", "cto"))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if rec.NoWakeReason != "polling via NOC cadence" {
		t.Fatalf("NoWakeReason = %q", rec.NoWakeReason)
	}
}

func TestLeadRegisterExplicitWakeStartsWakeStarter(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	prevPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prevPane })
	var got leadWakeOptions
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		got = opts
		return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	out, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--wake", "--adopt-project-lead"})
	})
	if err != nil {
		t.Fatalf("lead register --wake: %v\n%s", err, out)
	}
	if got.Root != filepath.Join(base, "issue-96") || got.Handle != "cto" || !got.Require {
		t.Fatalf("--wake opts = %+v", got)
	}
	if !strings.Contains(out, "wake: ready") {
		t.Fatalf("output should report wake readiness:\n%s", out)
	}
}

func TestLeadRegisterWakeAndNoWakeConflict(t *testing.T) {
	_, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--wake", "--no-wake"})
	})
	if err == nil || !strings.Contains(err.Error(), "--wake and --no-wake are mutually exclusive") {
		t.Fatalf("lead register --wake --no-wake err = %v", err)
	}
}

func TestLeadRegisterWakeFailureCanBeNonRequired(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	prevPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prevPane })
	var got leadWakeOptions
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		got = opts
		return leadWakeResult{Detail: "wake not ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	out, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead", "--no-require-wake", "--wake-inject-via", "/opt/inject", "--wake-inject-arg", "--pane", "--wake-inject-arg", "%5"})
	})
	if err != nil {
		t.Fatalf("lead register --no-require-wake: %v\n%s", err, out)
	}
	if got.Require {
		t.Fatalf("--no-require-wake should pass Require=false: %+v", got)
	}
	if got.WakeInjectVia != "/opt/inject" || strings.Join(got.WakeInjectArgs, ",") != "--pane,%5" {
		t.Fatalf("wake inject opts = %+v", got)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "cto"))
	if err != nil {
		t.Fatalf("read record: %v", err)
	}
	if !rec.NoRequireWake || rec.WakeInjectVia != "/opt/inject" {
		t.Fatalf("record wake fields = %+v", rec)
	}
	if rec.WakePID != 0 {
		t.Fatalf("WakePID = %d, want 0 for non-live wake failure", rec.WakePID)
	}
}

func TestLeadRegisterWakeInjectModeNoneIsZeroInput(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.42.0")
	setupFakeExternalWakeBinder(t)
	base := os.Getenv("AMQ_FAKE_ROOT")
	seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}})
	prevPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	var got []leadWakeOptions
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		got = append(got, opts)
		return leadWakeResult{PID: 1234, Started: true}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prevPane; leadWakeStarter = prevWake })
	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead", "--wake-inject-mode", "none"})
	}); err != nil {
		t.Fatalf("lead register none: %v", err)
	}
	if len(got) != 1 || got[0].WakeInjectMode != "none" || got[0].WakeInjectCmd != "" || got[0].WakeInjectVia != "" || len(got[0].WakeInjectArgs) != 0 {
		t.Fatalf("zero-input wake options = %+v", got)
	}
	rec, err := launch.Read(filepath.Join(base, "agents", "cto"))
	if err != nil || rec.WakeInjectMode != "none" || rec.WakeInjectCmd != "" {
		t.Fatalf("zero-input wake record = %+v, %v", rec, err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
	}); err != nil {
		t.Fatalf("lead repair without mode: %v", err)
	}
	if len(got) != 2 || got[1].WakeInjectMode != "none" || got[1].WakeInjectCmd != "" {
		t.Fatalf("repair must inherit none mode: %+v", got)
	}
	rec, err = launch.Read(filepath.Join(base, "agents", "cto"))
	if err != nil || rec.WakeInjectMode != "none" || rec.WakeInjectCmd != "" {
		t.Fatalf("repair reset zero-input record = %+v, %v", rec, err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead", "--wake-inject-mode", "raw"})
	}); err != nil {
		t.Fatalf("lead repair explicit raw: %v", err)
	}
	if len(got) != 3 || got[2].WakeInjectMode != "raw" || got[2].WakeInjectCmd != wakeDrainInject() {
		t.Fatalf("explicit raw must override inherited none: %+v", got)
	}
	rec, err = launch.Read(filepath.Join(base, "agents", "cto"))
	if err != nil || rec.WakeInjectMode != "raw" || rec.WakeInjectCmd != wakeDrainInject() {
		t.Fatalf("explicit raw record = %+v, %v", rec, err)
	}
	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead", "--wake-inject-mode", "none", "--wake-inject-via", "/opt/inject"})
	}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("none + injector must fail closed, got %v", err)
	}
}

func TestLeadRegisterDefaultsManagedBinaryWakeModeToRaw(t *testing.T) {
	setupFakeAMQWithVersion(t, "0.42.0")
	setupFakeExternalWakeBinder(t)
	base := os.Getenv("AMQ_FAKE_ROOT")
	seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}}})
	prevPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	var got []leadWakeOptions
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		got = append(got, opts)
		return leadWakeResult{PID: 1234, Started: true}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prevPane; leadWakeStarter = prevWake })
	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
	}); err != nil {
		t.Fatalf("lead register default raw: %v", err)
	}
	if len(got) != 1 || got[0].WakeInjectMode != "raw" || got[0].WakeInjectCmd != wakeDrainInject() {
		t.Fatalf("managed external lead wake options = %+v", got)
	}
	rec, err := launch.Read(filepath.Join(base, "agents", "cto"))
	if err != nil || rec.Binary != "codex" || rec.WakeInjectMode != "raw" {
		t.Fatalf("managed external lead record = %+v, %v", rec, err)
	}
}

func TestResolveExternalWakeInjectConfigMigratesLegacyManagedModesAndPreservesOverrides(t *testing.T) {
	for _, test := range []struct {
		name, stored, requested, binary, want string
		explicit                              bool
	}{
		{"legacy-blank-codex", "", "", "codex", "raw", false},
		{"legacy-auto-claude", "auto", "", "claude", "raw", false},
		{"explicit-none", "auto", "none", "codex", "none", true},
		{"explicit-paste", "", "paste", "claude", "paste", true},
		{"custom-auto", "auto", "", "custom-agent", "auto", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			rec := launch.Record{External: true, Binary: test.binary, Role: "cto", Handle: "cto", Session: "s", Root: "/mail/s", WakeInjectMode: test.stored, Tmux: &launch.TmuxInfo{PaneID: "%5"}}
			got, err := resolveExternalWakeInjectConfig(wakeInjectConfig{Mode: test.requested}, test.explicit, false, false, rec, nil, test.binary, "cto", "cto", "default", "s", rec.Root, "%5")
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != test.want {
				t.Fatalf("mode = %q, want %q", got.Mode, test.want)
			}
		})
	}
}

func TestResolveExternalWakeInjectConfigInheritsAssociatedInjector(t *testing.T) {
	rec := launch.Record{
		External:       true,
		Role:           "cto",
		Handle:         "cto",
		Session:        "issue-96",
		Root:           "/repo/.agent-mail/issue-96",
		WakeInjectMode: "paste",
		WakeInjectVia:  "/opt/inject",
		WakeInjectArgs: []string{"--pane", "%5"},
		Tmux:           &launch.TmuxInfo{PaneID: "%5"},
	}
	got, err := resolveExternalWakeInjectConfig(wakeInjectConfig{}, false, false, false, rec, nil, rec.Binary, "cto", "cto", "default", "issue-96", rec.Root, "%5")
	if err != nil {
		t.Fatalf("inherit external wake config: %v", err)
	}
	if got.Mode != "paste" || got.Via != "/opt/inject" || strings.Join(got.Args, ",") != "--pane,%5" {
		t.Fatalf("inherited config = %+v", got)
	}
}

func TestStartExternalLeadWakeRequiredTimeoutStopsSpawnedProcess(t *testing.T) {
	harness := installLeadWakeHelper(t, "spawn-child-late-ready")

	_, err := startExternalLeadWake(leadWakeOptions{
		ProjectDir: t.TempDir(),
		Root:       filepath.Join(t.TempDir(), "root"),
		Handle:     "cto",
		Require:    true,
	})
	if err == nil {
		t.Fatal("startExternalLeadWake required timeout err = nil, want error")
	}
	if !strings.Contains(err.Error(), "did not become ready") || !strings.Contains(err.Error(), "stopped spawned wake process") {
		t.Fatalf("timeout error = %v, want stopped-process detail", err)
	}
	harness.assertStoppedAfterCleanup(t)
}

func TestStartExternalLeadWakeDisablesChildUpdateCheck(t *testing.T) {
	t.Setenv("AMQ_NO_UPDATE_CHECK", "0")
	previous := externalLeadWakeCommand
	var captured *exec.Cmd
	externalLeadWakeCommand = func(_ string, _ ...string) *exec.Cmd {
		captured = exec.Command("/bin/sh", "-c", "exit 0")
		return captured
	}
	t.Cleanup(func() { externalLeadWakeCommand = previous })

	if _, err := startExternalLeadWake(leadWakeOptions{
		ProjectDir: t.TempDir(),
		Root:       filepath.Join(t.TempDir(), "root"),
		Handle:     "cto",
		Require:    false,
	}); err != nil {
		t.Fatalf("startExternalLeadWake: %v", err)
	}
	if captured == nil || !envHas(captured.Env, "AMQ_NO_UPDATE_CHECK", "1") {
		t.Fatalf("wake child environment missing AMQ_NO_UPDATE_CHECK=1: %#v", captured)
	}
}

func TestStartExternalLeadWakeUsesCompleteExactRootTuple(t *testing.T) {
	previous := externalLeadWakeCommand
	var captured *exec.Cmd
	externalLeadWakeCommand = func(_ string, _ ...string) *exec.Cmd {
		captured = exec.Command("/bin/sh", "-c", "exit 0")
		return captured
	}
	t.Cleanup(func() { externalLeadWakeCommand = previous })
	root := filepath.Join(t.TempDir(), "with space", ".agent-mail", "review", "issue-96")
	if _, err := startExternalLeadWake(leadWakeOptions{
		ProjectDir: t.TempDir(),
		Profile:    "review",
		Session:    "issue-96",
		Root:       root,
		Handle:     "cto",
		Require:    false,
	}); err != nil {
		t.Fatalf("startExternalLeadWake: %v", err)
	}
	if captured == nil {
		t.Fatal("wake command was not captured")
	}
	for key, want := range map[string]string{"AM_ROOT": root, "AM_BASE_ROOT": root, "AM_ME": "cto"} {
		if !envHas(captured.Env, key, want) {
			t.Fatalf("wake environment missing %s=%q: %#v", key, want, captured.Env)
		}
	}
	if envHasPrefix(captured.Env, "AM_SESSION", "") {
		t.Fatalf("exact-root wake environment must omit AM_SESSION: %#v", captured.Env)
	}
}

func TestStartExternalLeadWakeNonRequiredTimeoutStopsSpawnedProcessAndReportsNoPID(t *testing.T) {
	harness := installLeadWakeHelper(t, "spawn-child-late-ready")

	result, err := startExternalLeadWake(leadWakeOptions{
		ProjectDir: t.TempDir(),
		Root:       filepath.Join(t.TempDir(), "root"),
		Handle:     "cto",
		Require:    false,
	})
	if err != nil {
		t.Fatalf("startExternalLeadWake non-required timeout: %v", err)
	}
	if result.PID != 0 || result.Started {
		t.Fatalf("timeout result = %+v, want no pid and not started", result)
	}
	if !strings.Contains(result.Detail, "did not become ready") || !strings.Contains(result.Detail, "stopped spawned wake process") {
		t.Fatalf("timeout detail = %q, want stopped-process detail", result.Detail)
	}
	harness.assertStoppedAfterCleanup(t)
}

func TestStartExternalLeadWakeRequiredFailureStopsSpawnedProcess(t *testing.T) {
	harness := installLeadWakeHelper(t, "spawn-child-fail")

	_, err := startExternalLeadWake(leadWakeOptions{
		ProjectDir: t.TempDir(),
		Root:       filepath.Join(t.TempDir(), "root"),
		Handle:     "cto",
		Require:    true,
	})
	if err == nil {
		t.Fatal("startExternalLeadWake required failure err = nil, want error")
	}
	if !strings.Contains(err.Error(), "stopped spawned wake process") {
		t.Fatalf("failure error = %v, want stopped-process detail", err)
	}
	harness.assertStoppedAfterCleanup(t)
}

func TestStartExternalLeadWakeNonRequiredFailureStopsSpawnedProcessAndReportsNoPID(t *testing.T) {
	harness := installLeadWakeHelper(t, "spawn-child-fail")

	result, err := startExternalLeadWake(leadWakeOptions{
		ProjectDir: t.TempDir(),
		Root:       filepath.Join(t.TempDir(), "root"),
		Handle:     "cto",
		Require:    false,
	})
	if err != nil {
		t.Fatalf("startExternalLeadWake non-required failure: %v", err)
	}
	if result.PID != 0 || result.Started {
		t.Fatalf("failure result = %+v, want no pid and not started", result)
	}
	hasFailureReason := strings.Contains(result.Detail, "wake not ready") || strings.Contains(result.Detail, "did not become ready")
	if !hasFailureReason || !strings.Contains(result.Detail, "stopped spawned wake process") {
		t.Fatalf("failure detail = %q, want stopped-process detail", result.Detail)
	}
	harness.assertStoppedAfterCleanup(t)
}

func TestLeadRegisterWakeFailureRequiredErrorsBeforeTeamMutation(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "qa",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"},
		},
	})
	prevPane := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prevPane })
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		return leadWakeResult{}, errors.New("wake denied")
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--adopt-project-lead"})
	}); err == nil || !strings.Contains(err.Error(), "start external lead wake") {
		t.Fatalf("lead register wake err = %v", err)
	}
	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatalf("read team: %v", err)
	}
	if !cfg.Orchestrated || cfg.Lead != "qa" {
		t.Fatalf("team config after failed wake = orchestrated:%v lead:%q, want preserved true/qa", cfg.Orchestrated, cfg.Lead)
	}
}

type leadWakeHelperHarness struct {
	mode           string
	markerPath     string
	startedPath    string
	triggerPath    string
	eventsPath     string
	deferredMu     sync.Mutex
	deferredEvents []leadWakeHelperEvent
}

type leadWakeHelperEvent struct {
	Time          string `json:"time"`
	PID           int    `json:"pid"`
	PPID          int    `json:"ppid"`
	PGID          int    `json:"pgid"`
	TargetPID     int    `json:"target_pid,omitempty"`
	TargetPGID    int    `json:"target_pgid,omitempty"`
	Mode          string `json:"mode"`
	Phase         string `json:"phase"`
	ReadyPath     string `json:"ready_path,omitempty"`
	MarkerPath    string `json:"marker_path"`
	ProbeKillZero string `json:"probe_kill_zero,omitempty"`
	ProbeGetPGID  string `json:"probe_getpgid,omitempty"`
	Error         string `json:"error,omitempty"`
}

func installLeadWakeHelper(t *testing.T, mode string) *leadWakeHelperHarness {
	t.Helper()
	dir := t.TempDir()
	harness := &leadWakeHelperHarness{
		mode:        mode,
		markerPath:  filepath.Join(dir, "survived-after-cleanup"),
		startedPath: filepath.Join(dir, "grandchild-started"),
		triggerPath: filepath.Join(dir, "cleanup-returned"),
		eventsPath:  filepath.Join(dir, "helper-events.jsonl"),
	}
	prevCommand := externalLeadWakeCommand
	prevTimeout := externalLeadWakeReadyTimeout
	prevPoll := externalLeadWakePollInterval
	prevStopTimeout := externalLeadWakeStopTimeout
	prevProcessEvent := externalLeadWakeProcessEvent
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("test executable: %v", err)
	}
	externalLeadWakeCommand = func(_ string, args ...string) *exec.Cmd {
		helperArgs := []string{
			"-test.run=TestLeadWakeHelperProcess", "--", "lead-wake-helper", mode,
			harness.markerPath, harness.startedPath, harness.triggerPath, harness.eventsPath,
		}
		helperArgs = append(helperArgs, args...)
		return exec.Command(exe, helperArgs...)
	}
	firstKillAttempt := true
	externalLeadWakeProcessEvent = func(phase string, cmd *exec.Cmd, eventErr error) {
		targetPID := 0
		if cmd != nil && cmd.Process != nil {
			targetPID = cmd.Process.Pid
		}
		if phase == "kill_attempt" && firstKillAttempt {
			firstKillAttempt = false
			_ = appendLeadWakeHelperEvent(harness.eventsPath, mode, "waiting_for_started", "", harness.markerPath, targetPID, nil)
			if err := waitForLeadWakeHelperStarted(harness.startedPath, 2*time.Second); err != nil {
				// Mark the test failed before allowing cleanup to signal the group,
				// but keep running so the failed precondition cannot leak helpers.
				t.Errorf("wake-helper canary precondition: %v", err)
			} else {
				_ = appendLeadWakeHelperEvent(harness.eventsPath, mode, "started_observed", "", harness.markerPath, targetPID, nil)
			}
		}
		_ = appendLeadWakeHelperEvent(harness.eventsPath, mode, phase, "", harness.markerPath, targetPID, eventErr)
		if phase == "kill_result" {
			childPID, err := readLeadWakeHelperStartedPID(harness.startedPath)
			if err != nil {
				_ = appendLeadWakeHelperEvent(harness.eventsPath, mode, "post_kill_probe_unavailable", "", harness.markerPath, 0, err)
				return
			}
			harness.recordImmediateProbe(mode, "post_kill_probe_immediate", childPID)
		}
	}
	externalLeadWakeReadyTimeout = 20 * time.Millisecond
	externalLeadWakePollInterval = 2 * time.Millisecond
	externalLeadWakeStopTimeout = time.Second
	t.Cleanup(func() {
		externalLeadWakeCommand = prevCommand
		externalLeadWakeReadyTimeout = prevTimeout
		externalLeadWakePollInterval = prevPoll
		externalLeadWakeStopTimeout = prevStopTimeout
		externalLeadWakeProcessEvent = prevProcessEvent
		if err := harness.flushDeferredEvents(); err != nil {
			t.Logf("flush deferred lead wake helper events: %v", err)
		}
		if t.Failed() || testing.Verbose() {
			if events, err := os.ReadFile(harness.eventsPath); err == nil {
				t.Logf("lead wake helper events:\n%s", events)
			} else {
				t.Logf("read lead wake helper events: %v", err)
			}
		}
	})
	return harness
}

func waitForLeadWakeHelperStarted(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		if _, err := readLeadWakeHelperStartedPID(path); err == nil {
			return nil
		} else {
			lastErr = err
		}
		if !time.Now().Before(deadline) {
			return fmt.Errorf("grandchild did not reach trigger wait phase within %s: %w", timeout, lastErr)
		}
		time.Sleep(time.Millisecond)
	}
}

func readLeadWakeHelperStartedPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("parse started PID: %w", err)
	}
	if pid <= 0 {
		return 0, fmt.Errorf("started PID must be positive, got %d", pid)
	}
	return pid, nil
}

func TestWaitForLeadWakeHelperStartedFailsClosed(t *testing.T) {
	err := waitForLeadWakeHelperStarted(filepath.Join(t.TempDir(), "missing"), 5*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "did not reach trigger wait phase") {
		t.Fatalf("missing started file err = %v, want bounded fail-closed error", err)
	}
}

func TestWaitExternalLeadWakeProcessGroupGoneResignalsUntilESRCH(t *testing.T) {
	prevSignal := externalLeadWakeProcessGroupSignal
	prevPoll := externalLeadWakePollInterval
	prevTimeout := externalLeadWakeStopTimeout
	t.Cleanup(func() {
		externalLeadWakeProcessGroupSignal = prevSignal
		externalLeadWakePollInterval = prevPoll
		externalLeadWakeStopTimeout = prevTimeout
	})
	probes := 0
	kills := 0
	externalLeadWakeProcessGroupSignal = func(pgid int, signal syscall.Signal) error {
		if pgid != 4242 {
			t.Fatalf("pgid = %d, want 4242", pgid)
		}
		if signal == 0 {
			probes++
			if probes == 1 {
				return nil
			}
			return syscall.ESRCH
		}
		if signal != syscall.SIGKILL {
			t.Fatalf("signal = %v, want SIGKILL", signal)
		}
		kills++
		return nil
	}
	externalLeadWakePollInterval = time.Millisecond
	externalLeadWakeStopTimeout = 50 * time.Millisecond

	cmd := &exec.Cmd{Process: &os.Process{Pid: 4242}}
	if err := waitExternalLeadWakeProcessGroupGone(cmd, true); err != nil {
		t.Fatalf("wait for process group: %v", err)
	}
	if probes != 2 || kills != 1 {
		t.Fatalf("probes/kills = %d/%d, want 2/1", probes, kills)
	}
}

func TestWaitExternalLeadWakeProcessGroupGoneTimesOutWhileLive(t *testing.T) {
	prevSignal := externalLeadWakeProcessGroupSignal
	prevPoll := externalLeadWakePollInterval
	prevTimeout := externalLeadWakeStopTimeout
	t.Cleanup(func() {
		externalLeadWakeProcessGroupSignal = prevSignal
		externalLeadWakePollInterval = prevPoll
		externalLeadWakeStopTimeout = prevTimeout
	})
	externalLeadWakeProcessGroupSignal = func(_ int, _ syscall.Signal) error { return nil }
	externalLeadWakePollInterval = time.Millisecond
	externalLeadWakeStopTimeout = 5 * time.Millisecond

	cmd := &exec.Cmd{Process: &os.Process{Pid: 4242}}
	err := waitExternalLeadWakeProcessGroupGone(cmd, true)
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for spawned wake process group") {
		t.Fatalf("live group err = %v, want bounded timeout", err)
	}
}

func TestWaitExternalLeadWakeProcessGroupGoneScopesDarwinEPERMAfterSuccess(t *testing.T) {
	prevSignal := externalLeadWakeProcessGroupSignal
	t.Cleanup(func() { externalLeadWakeProcessGroupSignal = prevSignal })
	externalLeadWakeProcessGroupSignal = func(_ int, _ syscall.Signal) error { return syscall.EPERM }
	cmd := &exec.Cmd{Process: &os.Process{Pid: 4242}}

	if err := waitExternalLeadWakeProcessGroupGone(cmd, true); err != nil {
		t.Fatalf("post-success EPERM = %v, want accepted quiescence", err)
	}
	if err := waitExternalLeadWakeProcessGroupGone(cmd, false); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("pre-success EPERM = %v, want EPERM failure", err)
	}
}

func (h *leadWakeHelperHarness) assertStoppedAfterCleanup(t *testing.T) {
	t.Helper()
	pgid, _ := syscall.Getpgid(0)
	h.recordDeferredEvent(leadWakeHelperEvent{
		Time:       time.Now().UTC().Format(time.RFC3339Nano),
		PID:        os.Getpid(),
		PPID:       os.Getppid(),
		PGID:       pgid,
		Mode:       h.mode,
		Phase:      "cleanup_returned",
		MarkerPath: h.markerPath,
	})
	if err := os.WriteFile(h.triggerPath, []byte("cleanup returned\n"), 0o644); err != nil {
		t.Fatalf("write cleanup trigger: %v", err)
	}
	if err := appendLeadWakeHelperEvent(h.eventsPath, h.mode, "trigger_written", "", h.markerPath, 0, nil); err != nil {
		t.Fatalf("record cleanup trigger: %v", err)
	}

	deadline := time.Now().Add(250 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(h.markerPath); err == nil {
			t.Fatalf("post-cleanup survival marker exists at %s; spawned wake process survived cleanup", h.markerPath)
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat post-cleanup survival marker: %v", err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	if err := appendLeadWakeHelperEvent(h.eventsPath, h.mode, "marker_absent", "", h.markerPath, 0, nil); err != nil {
		t.Fatalf("record marker absence: %v", err)
	}
}

func appendLeadWakeHelperEvent(path, mode, phase, readyPath, markerPath string, targetPID int, eventErr error) error {
	pgid, _ := syscall.Getpgid(0)
	targetPGID := 0
	if targetPID > 0 {
		targetPGID, _ = syscall.Getpgid(targetPID)
	}
	event := leadWakeHelperEvent{
		Time:       time.Now().UTC().Format(time.RFC3339Nano),
		PID:        os.Getpid(),
		PPID:       os.Getppid(),
		PGID:       pgid,
		TargetPID:  targetPID,
		TargetPGID: targetPGID,
		Mode:       mode,
		Phase:      phase,
		ReadyPath:  readyPath,
		MarkerPath: markerPath,
	}
	if eventErr != nil {
		event.Error = eventErr.Error()
	}
	return writeLeadWakeHelperEvent(path, event)
}

func (h *leadWakeHelperHarness) recordImmediateProbe(mode, phase string, targetPID int) {
	pgid, _ := syscall.Getpgid(0)
	targetPGID, getPGIDErr := syscall.Getpgid(targetPID)
	killZeroErr := syscall.Kill(targetPID, 0)
	event := leadWakeHelperEvent{
		Time:          time.Now().UTC().Format(time.RFC3339Nano),
		PID:           os.Getpid(),
		PPID:          os.Getppid(),
		PGID:          pgid,
		TargetPID:     targetPID,
		TargetPGID:    targetPGID,
		Mode:          mode,
		Phase:         phase,
		MarkerPath:    h.markerPath,
		ProbeKillZero: probeResult(killZeroErr),
		ProbeGetPGID:  probePGIDResult(targetPGID, getPGIDErr),
	}
	h.deferredMu.Lock()
	h.deferredEvents = append(h.deferredEvents, event)
	h.deferredMu.Unlock()
}

func (h *leadWakeHelperHarness) recordDeferredEvent(event leadWakeHelperEvent) {
	h.deferredMu.Lock()
	h.deferredEvents = append(h.deferredEvents, event)
	h.deferredMu.Unlock()
}

func (h *leadWakeHelperHarness) flushDeferredEvents() error {
	h.deferredMu.Lock()
	events := append([]leadWakeHelperEvent(nil), h.deferredEvents...)
	h.deferredEvents = nil
	h.deferredMu.Unlock()
	for _, event := range events {
		if err := writeLeadWakeHelperEvent(h.eventsPath, event); err != nil {
			return err
		}
	}
	return nil
}

func probeResult(err error) string {
	if err == nil {
		return "ok"
	}
	return err.Error()
}

func probePGIDResult(pgid int, err error) string {
	if err == nil {
		return strconv.Itoa(pgid)
	}
	return err.Error()
}

func writeLeadWakeHelperEvent(path string, event leadWakeHelperEvent) error {
	line, err := json.Marshal(event)
	if err != nil {
		return err
	}
	line = append(line, '\n')
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(line)
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func TestLeadWakeHelperProcess(t *testing.T) {
	idx := -1
	for i, arg := range os.Args {
		if arg == "lead-wake-helper" {
			idx = i
			break
		}
	}
	if idx == -1 {
		return
	}
	if len(os.Args) <= idx+6 {
		fmt.Fprintln(os.Stderr, "lead wake helper missing args")
		os.Exit(2)
	}
	mode := os.Args[idx+1]
	marker := os.Args[idx+2]
	startedPath := os.Args[idx+3]
	triggerPath := os.Args[idx+4]
	eventsPath := os.Args[idx+5]
	wakeArgs := os.Args[idx+6:]
	readyPath := ""
	for i := 0; i < len(wakeArgs)-1; i++ {
		if wakeArgs[i] == "--ready-file" {
			readyPath = wakeArgs[i+1]
			break
		}
	}
	if err := appendLeadWakeHelperEvent(eventsPath, mode, "helper_start", readyPath, marker, 0, nil); err != nil {
		fmt.Fprintf(os.Stderr, "record helper start: %v\n", err)
		os.Exit(2)
	}
	switch mode {
	case "spawn-child-late-ready", "spawn-child-fail":
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "test executable: %v\n", err)
			os.Exit(2)
		}
		childArgs := []string{
			"-test.run=TestLeadWakeHelperProcess", "--", "lead-wake-helper", "late-ready",
			marker, startedPath, triggerPath, eventsPath,
		}
		childArgs = append(childArgs, wakeArgs...)
		child := exec.Command(exe, childArgs...)
		child.Stdout = os.Stderr
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			_ = appendLeadWakeHelperEvent(eventsPath, mode, "child_start_failed", readyPath, marker, 0, err)
			fmt.Fprintf(os.Stderr, "start child: %v\n", err)
			os.Exit(2)
		}
		_ = appendLeadWakeHelperEvent(eventsPath, mode, "child_started", readyPath, marker, child.Process.Pid, nil)
		if mode == "spawn-child-fail" {
			_ = appendLeadWakeHelperEvent(eventsPath, mode, "parent_exit", readyPath, marker, child.Process.Pid, errors.New("exit status 42"))
			os.Exit(42)
		}
		_ = appendLeadWakeHelperEvent(eventsPath, mode, "parent_wait", readyPath, marker, child.Process.Pid, nil)
		for {
			time.Sleep(time.Hour)
		}
	case "late-ready":
		if err := os.WriteFile(startedPath, []byte(fmt.Sprintf("%d\n", os.Getpid())), 0o644); err != nil {
			_ = appendLeadWakeHelperEvent(eventsPath, mode, "started_write_failed", readyPath, marker, 0, err)
			fmt.Fprintf(os.Stderr, "write started: %v\n", err)
			os.Exit(2)
		}
		_ = appendLeadWakeHelperEvent(eventsPath, mode, "started_written", readyPath, marker, 0, nil)
		for {
			if _, err := os.Stat(triggerPath); err == nil {
				break
			} else if !errors.Is(err, os.ErrNotExist) {
				_ = appendLeadWakeHelperEvent(eventsPath, mode, "trigger_stat_failed", readyPath, marker, 0, err)
				fmt.Fprintf(os.Stderr, "stat trigger: %v\n", err)
				os.Exit(2)
			}
			time.Sleep(time.Millisecond)
		}
		_ = appendLeadWakeHelperEvent(eventsPath, mode, "trigger_observed", readyPath, marker, 0, nil)
		if err := os.WriteFile(marker, []byte("late-ready"), 0o644); err != nil {
			_ = appendLeadWakeHelperEvent(eventsPath, mode, "marker_write_failed", readyPath, marker, 0, err)
			fmt.Fprintf(os.Stderr, "write marker: %v\n", err)
			os.Exit(2)
		}
		_ = appendLeadWakeHelperEvent(eventsPath, mode, "marker_written", readyPath, marker, 0, nil)
		if readyPath != "" {
			if err := os.WriteFile(readyPath, []byte("ready"), 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "write ready: %v\n", err)
				os.Exit(2)
			}
		}
		os.Exit(0)
	default:
		fmt.Fprintf(os.Stderr, "unknown lead wake helper mode %q\n", mode)
		os.Exit(2)
	}
}

func TestStatusTreatsExternalLeadPaneAsLiveAndActionable(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		External: true,
		Tmux:     &launch.TmuxInfo{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5", Target: "external"},
	})
	prevLister := statusPaneLister
	prevInspector := statusPaneInspector
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) { return nil, nil }
	statusPaneInspector = func(id string) (tmuxpane.TmuxPane, bool) {
		if id == "%5" {
			return tmuxpane.TmuxPane{Session: "tmux-main", PaneID: "%5", WindowID: "@7", WindowName: "lead"}, true
		}
		return tmuxpane.TmuxPane{}, false
	}
	t.Cleanup(func() { statusPaneLister = prevLister; statusPaneInspector = prevInspector })

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Probe:            statusProbe(nil, nil, time.Now()),
		JSON:             true,
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	row := env.Data.Records[0]
	if row.Status != statusStateLive || !strings.Contains(row.Detail, "external pane %5 live") {
		t.Fatalf("external lead status = %s %q, want live external detail", row.Status, row.Detail)
	}
	if row.Tmux == nil || !row.Tmux.PaneAlive {
		t.Fatalf("external lead tmux = %+v, want live pane", row.Tmux)
	}
	var sendAvailable bool
	for _, a := range row.Actions {
		if a.Kind == "send" {
			sendAvailable = a.Available
		}
	}
	if !sendAvailable {
		t.Fatalf("external lead send action should be available: %+v", row.Actions)
	}
}

func TestStatusKeepsActionsAvailableWhenAgentPIDVerifiesButTmuxScanMisses(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Members: []team.Member{{Role: "runtime-dev", Binary: "codex", Handle: "runtime-dev", Session: "v2-5-0"}},
	})
	seedAgentRecord(t, base, "v2-5-0", "runtime-dev", launch.Record{
		Binary:   "codex",
		Handle:   "runtime-dev",
		Role:     "runtime-dev",
		AgentPID: 67026,
		Tmux:     &launch.TmuxInfo{Session: "amq-squad-2-4-0", WindowID: "@99", WindowName: "shell", PaneID: "%112", Target: "current-window"},
	})
	prevLister := statusPaneLister
	prevInspector := statusPaneInspector
	statusPaneLister = func() ([]tmuxpane.TmuxPane, error) { return nil, nil }
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) { return tmuxpane.TmuxPane{}, false }
	t.Cleanup(func() { statusPaneLister = prevLister; statusPaneInspector = prevInspector })

	out, err := runStatusExec(t, statusExecution{
		ProjectDir:       dir,
		RequestedSession: "v2-5-0",
		ExplicitSession:  true,
		Probe:            statusProbe(map[int]bool{67026: true}, map[int]bool{67026: true}, time.Now()),
		JSON:             true,
	})
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[statusEnvelopeData](t, out)
	row := env.Data.Records[0]
	if row.Status != statusStateLive || row.Tmux == nil || !row.Tmux.PaneAlive {
		t.Fatalf("status row = %+v, want live with pane_alive from verified agent pid", row)
	}
	for _, a := range row.Actions {
		if (a.Kind == "focus" || a.Kind == "send") && !a.Available {
			t.Fatalf("%s action should remain available when agent pid verifies recorded pane: %+v", a.Kind, row.Actions)
		}
	}
}

func TestStopDoesNotCloseExternalLeadPane(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		External: true,
		Tmux:     &launch.TmuxInfo{PaneID: "%5"},
	})
	closed := false
	prevCloser := paneCloser
	paneCloser = func(string) error {
		closed = true
		return nil
	}
	t.Cleanup(func() { paneCloser = prevCloser })

	out, err := runDownExec(t, downExecution{
		ProjectDir:       dir,
		RequestedSession: "issue-96",
		ExplicitSession:  true,
		Role:             "cto",
		Profile:          team.DefaultProfile,
		Terminator:       newSignalTerminator(false),
		Probe:            statusProbe(nil, nil, time.Now()),
		ClosePanes:       true,
	})
	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("stop external lead err = %v, want partial maybe-live\n%s", err, out)
	}
	if closed {
		t.Fatal("external lead pane must not be closed by stop --close-panes")
	}
	if !strings.Contains(out, "external/adopted pane %5 is operator-owned") {
		t.Fatalf("stop output missing external safety detail:\n%s", out)
	}
}

func TestResumeDoesNotRestoreDeadExternalLeadRecord(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members:      []team.Member{{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"}},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		CWD:      dir,
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		External: true,
		Tmux:     &launch.TmuxInfo{PaneID: "%5"},
	})
	prevInspector := statusPaneInspector
	statusPaneInspector = func(string) (tmuxpane.TmuxPane, bool) { return tmuxpane.TmuxPane{}, false }
	t.Cleanup(func() { statusPaneInspector = prevInspector })

	out, _, err := captureOutput(t, func() error {
		return runResume([]string{"--session", "issue-96", "--json"})
	})
	if err != nil {
		t.Fatalf("resume: %v\n%s", err, out)
	}
	env := decodeJSONEnvelope[resumeEnvelopeData](t, out)
	if len(env.Data.Plan) != 1 {
		t.Fatalf("resume plan length = %d", len(env.Data.Plan))
	}
	plan := env.Data.Plan[0]
	if plan.Action != string(resumeBlocked) || plan.Command != "" || !strings.Contains(plan.Note, "role boundary violation") || !strings.Contains(plan.Note, "sibling tab") {
		t.Fatalf("external resume plan = %+v, want blocked/no command/boundary repair note", plan)
	}
}

func TestTeamLaunchDryRunSkipsRegisteredExternalLead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"},
		},
	})
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		Session:  "issue-96",
		External: true,
		Tmux:     &launch.TmuxInfo{PaneID: "%5"},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "shell", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })

	stdout, stderr, err := captureOutput(t, func() error {
		return runTeamLaunch([]string{"--session", "issue-96", "--dry-run", "--no-bootstrap"})
	})
	if err != nil {
		t.Fatalf("team launch dry-run: %v\nstderr:\n%s", err, stderr)
	}
	if strings.Contains(stdout, "agent up codex") || strings.Contains(stdout, "--role cto") {
		t.Fatalf("dry-run spawned registered external lead:\n%s", stdout)
	}
	if !strings.Contains(stdout, "agent up claude") || !strings.Contains(stdout, "--role qa") {
		t.Fatalf("dry-run should still launch child qa:\n%s", stdout)
	}
	if !strings.Contains(stderr, "not spawning a duplicate lead") {
		t.Fatalf("stderr missing external lead notice:\n%s", stderr)
	}
}

func TestTeamLaunchAutoRegistersCurrentPaneExternalLead(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"},
		},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "shell", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	t.Setenv("AM_ME", "cto")
	t.Setenv("AM_ROOT", filepath.Join(base, "issue-96"))

	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	filtered, skipped, err := maybeFilterCurrentExternalLead(cfg, "issue-96", team.DefaultProfile, trustModeApproveForMe, nil, nil, true)
	if err != nil {
		t.Fatalf("filter external lead: %v", err)
	}
	if !skipped || len(filtered.Members) != 1 || filtered.Members[0].Role != "qa" {
		t.Fatalf("filtered = skipped:%v members:%+v, want only qa", skipped, filtered.Members)
	}
	rec, err := launch.Read(filepath.Join(base, "issue-96", "agents", "cto"))
	if err != nil {
		t.Fatalf("read external record: %v", err)
	}
	if !rec.External || rec.Tmux == nil || rec.Tmux.PaneID != "%5" || rec.Trust != trustModeApproveForMe {
		t.Fatalf("external record = %+v", rec)
	}
}

func TestTeamLaunchDoesNotAutoRegisterProjectLeadWithoutAMRoot(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	dir := seedTeam(t, team.Team{
		Orchestrated: true,
		Lead:         "cto",
		Members: []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto", Session: "issue-96"},
			{Role: "qa", Binary: "claude", Handle: "qa", Session: "issue-96"},
		},
	})
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "shell", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	t.Setenv("AM_ME", "cto")
	t.Setenv("AM_ROOT", "")

	cfg, err := team.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	filtered, skipped, err := maybeFilterCurrentExternalLead(cfg, "issue-96", team.DefaultProfile, trustModeApproveForMe, nil, nil, true)
	if err != nil {
		t.Fatalf("filter external lead: %v", err)
	}
	if skipped || len(filtered.Members) != 2 {
		t.Fatalf("filtered = skipped:%v members:%+v, want original team", skipped, filtered.Members)
	}
	if _, err := launch.Read(filepath.Join(base, "issue-96", "agents", "cto")); err == nil {
		t.Fatalf("project lead record should not be written without AM_ROOT proof")
	}
}
