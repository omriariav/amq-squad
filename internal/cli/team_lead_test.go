package cli

import (
	"errors"
	"path/filepath"
	"strings"
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
	if cfg.Orchestrated || cfg.Lead != "" {
		t.Fatalf("cleared lead config = orchestrated:%v lead:%q, want false/empty", cfg.Orchestrated, cfg.Lead)
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

	out, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96"})
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

func TestRmPaneCollectionSkipsExternalRecords(t *testing.T) {
	base := setupFakeAMQSessionRoots(t)
	seedAgentRecord(t, base, "issue-96", "cto", launch.Record{
		Binary:   "codex",
		Handle:   "cto",
		Role:     "cto",
		External: true,
		Tmux:     &launch.TmuxInfo{PaneID: "%5"},
	})
	panes := collectSessionPaneIDs(filepath.Join(base, "issue-96"), nil)
	if len(panes) != 0 {
		t.Fatalf("external panes must not be collected for rm/archive close: %+v", panes)
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
	if plan.Action != string(resumeBlocked) || plan.Command != "" || !strings.Contains(plan.Note, "lead register") {
		t.Fatalf("external resume plan = %+v, want blocked/no command/re-register note", plan)
	}
}
