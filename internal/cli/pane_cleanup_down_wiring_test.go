package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

type eventTerminator struct {
	events *[]string
	err    error
}

func (t eventTerminator) Terminate(int) error {
	*t.events = append(*t.events, "signal")
	return t.err
}

func (eventTerminator) SignalName() string { return "SIGTERM" }

func completeDownPaneFixture(t *testing.T) (team.Team, team.Member, launch.Record, tmuxpane.TmuxPane, string) {
	t.Helper()
	base := setupFakeAMQSessionRoots(t)
	project := seedTeam(t, team.Team{Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-465"}}})
	member := team.Member{Role: "cto", Handle: "cto", Binary: "codex", Session: "issue-465", CWD: project}
	configured := team.Team{Project: project, Members: []team.Member{member}}
	tmux := &launch.TmuxInfo{Session: "mux-465", WindowID: "@7", WindowName: "cto", PaneID: "%9", Target: "new-window"}
	record := launch.Record{
		CWD: project, Binary: "codex", Session: "issue-465", Handle: "cto", Role: "cto",
		Root: filepath.Join(base, "issue-465"), BaseRoot: base, TeamProfile: team.DefaultProfile, TeamHome: project,
		AdoptionMode: "managed_window", AgentPID: 4242, Tmux: tmux, Terminal: launch.TerminalInfoFromTmux(tmux),
	}
	seedAgentRecord(t, base, "issue-465", "cto", record)
	pane := tmuxpane.TmuxPane{Session: tmux.Session, WindowID: tmux.WindowID, PaneID: tmux.PaneID, PID: 100, CWD: project}
	return configured, member, record, pane, project
}

func TestStopPanePrepareSignalCloseOrdering(t *testing.T) {
	configured, member, _, pane, project := completeDownPaneFixture(t)
	var events []string
	inspections := 0
	deps := PaneCleanupDependencies{
		Inspect: func(string) tmuxpane.PaneInspection {
			inspections++
			events = append(events, "inspect")
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
		},
		ChildrenIndex: func() (func(int) []int, error) {
			events = append(events, "children")
			return func(parent int) []int {
				if parent == 100 {
					return []int{4242}
				}
				return nil
			}, nil
		},
		Close: func(string) error {
			events = append(events, "close")
			return nil
		},
	}
	probe := downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true})
	baseAlive, baseMatch := probe.PIDAlive, probe.ProcessMatch
	probe.PIDAlive = func(pid int) bool {
		events = append(events, "alive")
		return baseAlive(pid)
	}
	probe.ProcessMatch = func(pid int, match func(string) bool) bool {
		events = append(events, "match")
		return baseMatch(pid, match)
	}
	report := terminateMember(configured, project, team.DefaultProfile, member, "issue-465", eventTerminator{events: &events}, probe, nil, true, deps)
	wantPrefix := []string{"alive", "match", "inspect", "children", "signal", "inspect", "close"}
	if len(events) < len(wantPrefix) || !reflect.DeepEqual(events[:len(wantPrefix)], wantPrefix) {
		t.Fatalf("events=%v, want prefix %v", events, wantPrefix)
	}
	if inspections != 2 || report.Status != downStatusStopped || report.Pane.Outcome != PaneCleanupClosed {
		t.Fatalf("report=%+v inspections=%d", report, inspections)
	}
}

func TestStopSignalsWhenPanePreparationRefusesAndReturnsPartial(t *testing.T) {
	configured, member, _, _, project := completeDownPaneFixture(t)
	var events []string
	closeCalls := 0
	report := terminateMember(configured, project, team.DefaultProfile, member, "issue-465", eventTerminator{events: &events},
		downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true}), nil, true, PaneCleanupDependencies{
			Inspect: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionUnavailable, Detail: "tmux unavailable"}
			},
			Close: func(string) error { closeCalls++; return nil },
		})
	if !reflect.DeepEqual(events, []string{"signal"}) || closeCalls != 0 {
		t.Fatalf("signal events=%v close calls=%d", events, closeCalls)
	}
	if report.Status != downStatusStopped || report.Pane.Outcome != PaneCleanupInspectionUnavailable {
		t.Fatalf("report=%+v", report)
	}
	var out bytes.Buffer
	err := renderDownReportsScoped(&out, "stop", project, team.DefaultProfile, "issue-465", []downReport{report}, false)
	var partial *PartialError
	if !errors.As(err, &partial) || !strings.Contains(out.String(), "tmux unavailable") || !strings.Contains(out.String(), "explicit operator review") {
		t.Fatalf("err=%v output=%s", err, out.String())
	}
}

func TestStopPaneFailurePrecedesAgentFailureInJSON(t *testing.T) {
	reports := []downReport{{
		Role: "cto", Handle: "cto", Root: "/tmp/root", Status: downStatusFailed, Detail: "signal failed",
		Pane: PaneCleanupResult{Outcome: PaneCleanupCloseFailed, Detail: "kill-pane failed",
			Mismatches: []PaneCleanupMismatch{{Field: "pane_id", Expected: "%9", Actual: "%10"}},
			Recovery:   &PaneCleanupRecovery{Identity: PaneCleanupIdentity{PaneID: "%9", TmuxSession: "mux", WindowID: "@7"}}},
	}}
	var out bytes.Buffer
	err := renderDownReportsScoped(&out, "stop", "/repo", "release", "issue-465", reports, true)
	var partial *PartialError
	if !errors.As(err, &partial) || !strings.Contains(partial.Message, "pane cleanup") {
		t.Fatalf("err=%v, want pane PartialError precedence", err)
	}
	env := decodeJSONEnvelope[downEnvelopeData](t, out.String())
	if env.Data.Project != "/repo" || env.Data.Profile != "release" || env.Data.Session != "issue-465" || env.Data.Root != "/tmp/root" {
		t.Fatalf("scope metadata=%+v", env.Data)
	}
	if env.Data.Summary.CloseFailed != 1 || env.Data.Reports[0].Agent.Outcome != downStatusFailed || env.Data.Reports[0].Pane.Recovery == nil {
		t.Fatalf("json contract=%+v", env.Data)
	}
}
