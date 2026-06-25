package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
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
	var wakeOpts []leadWakeOptions
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		wakeOpts = append(wakeOpts, opts)
		return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

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
	if rec.WakePID != 2222 {
		t.Fatalf("WakePID = %d, want 2222", rec.WakePID)
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
	if err := os.WriteFile(filepath.Join(base, "issue-96"), []byte("not a directory"), 0o644); err != nil {
		t.Fatalf("seed blocking session path: %v", err)
	}
	prev := currentPaneIdentity
	currentPaneIdentity = func() (*tmuxpane.PaneIdentity, error) {
		return &tmuxpane.PaneIdentity{Session: "tmux-main", WindowID: "@7", WindowName: "lead", PaneID: "%5"}, nil
	}
	t.Cleanup(func() { currentPaneIdentity = prev })
	prevWake := leadWakeStarter
	leadWakeStarter = func(opts leadWakeOptions) (leadWakeResult, error) {
		return leadWakeResult{PID: 2222, Started: true, Detail: "ready"}, nil
	}
	t.Cleanup(func() { leadWakeStarter = prevWake })

	if _, _, err := captureOutput(t, func() error {
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96"})
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
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--no-wake"})
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
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96", "--no-require-wake", "--wake-inject-via", "/opt/inject", "--wake-inject-arg", "--pane", "--wake-inject-arg", "%5"})
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

func TestStartExternalLeadWakeRequiredTimeoutStopsSpawnedProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "late-ready")
	restore := installLeadWakeHelper(t, "spawn-child-late-ready", marker)
	defer restore()

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
	assertLateReadyMarkerAbsent(t, marker)
}

func TestStartExternalLeadWakeNonRequiredTimeoutStopsSpawnedProcessAndReportsNoPID(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "late-ready")
	restore := installLeadWakeHelper(t, "spawn-child-late-ready", marker)
	defer restore()

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
	assertLateReadyMarkerAbsent(t, marker)
}

func TestStartExternalLeadWakeRequiredFailureStopsSpawnedProcess(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "late-ready")
	restore := installLeadWakeHelper(t, "spawn-child-fail", marker)
	defer restore()

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
	assertLateReadyMarkerAbsent(t, marker)
}

func TestStartExternalLeadWakeNonRequiredFailureStopsSpawnedProcessAndReportsNoPID(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "late-ready")
	restore := installLeadWakeHelper(t, "spawn-child-fail", marker)
	defer restore()

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
	if !strings.Contains(result.Detail, "wake not ready") || !strings.Contains(result.Detail, "stopped spawned wake process") {
		t.Fatalf("failure detail = %q, want stopped-process detail", result.Detail)
	}
	assertLateReadyMarkerAbsent(t, marker)
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
		return runLead([]string{"register", "--role", "cto", "--session", "issue-96"})
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

func installLeadWakeHelper(t *testing.T, mode, marker string) func() {
	t.Helper()
	prevCommand := externalLeadWakeCommand
	prevTimeout := externalLeadWakeReadyTimeout
	prevPoll := externalLeadWakePollInterval
	prevStopTimeout := externalLeadWakeStopTimeout
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("test executable: %v", err)
	}
	externalLeadWakeCommand = func(_ string, args ...string) *exec.Cmd {
		helperArgs := []string{"-test.run=TestLeadWakeHelperProcess", "--", "lead-wake-helper", mode, marker}
		helperArgs = append(helperArgs, args...)
		return exec.Command(exe, helperArgs...)
	}
	externalLeadWakeReadyTimeout = 20 * time.Millisecond
	externalLeadWakePollInterval = 2 * time.Millisecond
	externalLeadWakeStopTimeout = time.Second
	return func() {
		externalLeadWakeCommand = prevCommand
		externalLeadWakeReadyTimeout = prevTimeout
		externalLeadWakePollInterval = prevPoll
		externalLeadWakeStopTimeout = prevStopTimeout
	}
}

func assertLateReadyMarkerAbsent(t *testing.T, marker string) {
	t.Helper()
	time.Sleep(250 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("late-ready helper marker exists at %s; spawned wake process was not stopped", marker)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat late-ready helper marker: %v", err)
	}
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
	if len(os.Args) <= idx+3 {
		fmt.Fprintln(os.Stderr, "lead wake helper missing args")
		os.Exit(2)
	}
	mode := os.Args[idx+1]
	marker := os.Args[idx+2]
	wakeArgs := os.Args[idx+3:]
	readyPath := ""
	for i := 0; i < len(wakeArgs)-1; i++ {
		if wakeArgs[i] == "--ready-file" {
			readyPath = wakeArgs[i+1]
			break
		}
	}
	switch mode {
	case "spawn-child-late-ready", "spawn-child-fail":
		exe, err := os.Executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "test executable: %v\n", err)
			os.Exit(2)
		}
		childArgs := []string{"-test.run=TestLeadWakeHelperProcess", "--", "lead-wake-helper", "late-ready", marker}
		childArgs = append(childArgs, wakeArgs...)
		child := exec.Command(exe, childArgs...)
		child.Stdout = os.Stderr
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			fmt.Fprintf(os.Stderr, "start child: %v\n", err)
			os.Exit(2)
		}
		if mode == "spawn-child-fail" {
			os.Exit(42)
		}
		for {
			time.Sleep(time.Hour)
		}
	case "late-ready":
		time.Sleep(200 * time.Millisecond)
		if err := os.WriteFile(marker, []byte("late-ready"), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "write marker: %v\n", err)
			os.Exit(2)
		}
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
