package cli

import (
	"bytes"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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

// TestStopClosePanesAcceptsPriorGenerationBaseRootShape is the #596 effect
// regression. Older AMQ env envelopes omitted base_root, so launch recording
// fell back to root and persisted BaseRoot == Root. A newer binary resolves the
// same namespace as BaseRoot == parent(Root). Teardown must accept that one
// known historical shape without re-validating stale prepared/bootstrap state:
// an in-place upgrade must still be able to stop the agent and close its exact
// pane.
func TestStopClosePanesAcceptsPriorGenerationBaseRootShape(t *testing.T) {
	configured, member, record, pane, project := completeDownPaneFixture(t)
	record.BaseRoot = record.Root
	if err := launch.Write(filepath.Join(record.Root, "agents", record.Handle), record); err != nil {
		t.Fatal(err)
	}

	var events []string
	closeCalls := 0
	report := terminateMember(
		configured, project, team.DefaultProfile, member, record.Session,
		eventTerminator{events: &events},
		downFakeProbe(map[int]bool{record.AgentPID: true}, map[int]bool{record.AgentPID: true}),
		nil, true,
		PaneCleanupDependencies{
			Inspect: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
			},
			ChildrenIndex: func() (func(int) []int, error) {
				return func(parent int) []int {
					if parent == pane.PID {
						return []int{record.AgentPID}
					}
					return nil
				}, nil
			},
			Close: func(string) error { closeCalls++; return nil },
		},
	)

	if !reflect.DeepEqual(events, []string{"signal"}) {
		t.Fatalf("events=%v report=%+v, want exactly one agent signal before pane close", events, report)
	}
	if report.Status != downStatusStopped || report.Pane.Outcome != PaneCleanupClosed || closeCalls != 1 {
		t.Fatalf("report=%+v close calls=%d, want stopped/closed/1", report, closeCalls)
	}
}

// The compatibility rule above is about one launch-record path shape, not a
// license to trust whatever currently occupies the recorded pane id. Even with
// the legacy BaseRoot == Root spelling, a pane from another tmux session must
// remain preserved and never reach the closer.
func TestStopClosePanesPriorGenerationShapeStillRefusesForeignPane(t *testing.T) {
	configured, member, record, pane, project := completeDownPaneFixture(t)
	record.BaseRoot = record.Root
	if err := launch.Write(filepath.Join(record.Root, "agents", record.Handle), record); err != nil {
		t.Fatal(err)
	}
	pane.Session = "foreign-session"

	closeCalls := 0
	report := terminateMember(
		configured, project, team.DefaultProfile, member, record.Session,
		&recordingTerminator{},
		downFakeProbe(map[int]bool{record.AgentPID: true}, map[int]bool{record.AgentPID: true}),
		nil, true,
		PaneCleanupDependencies{
			Inspect: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
			},
			ChildrenIndex: func() (func(int) []int, error) {
				return func(parent int) []int {
					if parent == pane.PID {
						return []int{record.AgentPID}
					}
					return nil
				}, nil
			},
			Close: func(string) error { closeCalls++; return nil },
		},
	)

	if report.Status != downStatusStopped || report.Pane.Outcome != PaneCleanupPreservedIdentityUnconfirmed || closeCalls != 0 {
		t.Fatalf("report=%+v close calls=%d, want stopped/preserved/0", report, closeCalls)
	}
	if len(report.Pane.Mismatches) != 1 || report.Pane.Mismatches[0].Field != "pane.session" {
		t.Fatalf("foreign-pane refusal mismatches=%+v, want pane.session only", report.Pane.Mismatches)
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

func TestStopRefusesToSignalReusedSameBinaryPID(t *testing.T) {
	configured, member, record, _, project := completeDownPaneFixture(t)
	record.StartedAt = time.Now().Add(-10 * time.Minute)
	if err := launch.Write(filepath.Join(record.Root, "agents", record.Handle), record); err != nil {
		t.Fatal(err)
	}
	var events []string
	probe := downFakeProbe(map[int]bool{4242: true}, map[int]bool{4242: true})
	probe.ProcessStartTime = func(pid int) (time.Time, bool) {
		return record.StartedAt.Add(launchProcessStartSkewEpsilon + time.Nanosecond), pid == 4242
	}
	report := terminateMember(
		configured, project, team.DefaultProfile, member, "issue-465",
		eventTerminator{events: &events}, probe, nil, true, PaneCleanupDependencies{},
	)
	if len(events) != 0 {
		t.Fatalf("reused same-binary PID was signaled: %v", events)
	}
	if report.Status != downStatusNotLive || !strings.Contains(report.Detail, "recorded runtime identity") {
		t.Fatalf("report=%+v, want fail-closed not-live identity refusal", report)
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
