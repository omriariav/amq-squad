package cli

import (
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/liveidentity"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func TestValidateLiveIdentityTerminalProjectionPreservesNativeTarget(t *testing.T) {
	rec := launch.Record{AgentTTY: "/dev/ttys001", Terminal: &launch.TerminalInfo{
		Backend: "iterm2", Target: "new-window", Session: "session", WindowID: "101", TabID: "tab-1", SessionID: "session-1", TTY: "/dev/ttys001",
	}}
	if err := validateLiveIdentityTerminalProjection(rec); err != nil {
		t.Fatalf("valid native terminal rejected: %v", err)
	}
	want := liveidentity.Terminal{Backend: "iterm2", Target: "new-window", Session: "session", WindowID: "101", TabID: "tab-1", SessionID: "session-1", TTY: "/dev/ttys001"}
	if got := liveIdentityTerminal(rec); got != want {
		t.Fatalf("native terminal projection = %+v, want %+v", got, want)
	}
}

func TestValidateLiveIdentityTerminalProjectionRejectsTmuxNativeContradiction(t *testing.T) {
	rec := launch.Record{
		AgentTTY: "/dev/ttys001",
		Terminal: &launch.TerminalInfo{Backend: "iterm2", Target: "new-window", Session: "session", WindowID: "101", TabID: "tab-1", SessionID: "session-1", TTY: "/dev/ttys001"},
		Tmux:     &launch.TmuxInfo{Target: "new-window", Session: "session", WindowID: "@1", PaneID: "%2"},
	}
	if err := validateLiveIdentityTerminalProjection(rec); err == nil || !strings.Contains(err.Error(), "contradictory tmux projection") {
		t.Fatalf("tmux/native contradiction was not rejected: %v", err)
	}
}

func TestLegacyPreparedIdentityFieldsAreOpaque(t *testing.T) {
	previous := defaultDuplicateLaunchProbe
	t.Cleanup(func() { defaultDuplicateLaunchProbe = previous })
	defaultCalls := 0
	defaultDuplicateLaunchProbe = duplicateLaunchProbe{
		PIDAlive: func(int) bool { defaultCalls++; return false },
	}
	injectedCalls := 0
	injected := duplicateLaunchProbe{
		PIDAlive:         func(pid int) bool { injectedCalls++; return pid == 101 },
		ProcessMatch:     func(pid int, predicate func(string) bool) bool { return pid == 101 && predicate("codex") },
		ProcessTTY:       func(int) (string, bool) { return "", false },
		ProcessStartTime: func(int) (time.Time, bool) { return time.Time{}, false },
	}
	rec := launch.Record{
		Role: "dev", Handle: "dev", Binary: "codex", AgentPID: 101,
		PreparedRunGeneration: "legacy-partial-field",
	}
	result, required, err := verifyRuntimeActionWithRecord("send", t.TempDir(), team.DefaultProfile, "s", "dev", rec, injected)
	if err != nil || !required || result.Verified == nil || injectedCalls == 0 || defaultCalls != 0 {
		t.Fatalf("legacy prepared field or probe injection affected runtime verification: required=%v result=%+v err=%v injected=%d default=%d", required, result, err, injectedCalls, defaultCalls)
	}
}

func TestReadManagedLiveLaunchTypesMissingRosterHandleAsUnmanaged(t *testing.T) {
	project := t.TempDir()
	if err := team.WriteProfile(project, team.DefaultProfile, team.Team{Project: project, Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}}}); err != nil {
		t.Fatal(err)
	}
	_, err := readManagedLiveLaunch(liveIdentityScope{Project: project, Profile: team.DefaultProfile, Session: "s", Handle: "outsider"})
	var unmanaged unmanagedLiveActorError
	if !errors.As(err, &unmanaged) || unmanaged.Handle != "outsider" {
		t.Fatalf("missing roster handle error = %T %v", err, err)
	}
}

func verifiedPaneProcessPIDFromDeliveryPath(t *testing.T) int {
	t.Helper()
	const paneID = "%7"

	previousRun, previousInspect, previousOutput := tmuxRunCommand, inspectPaneExact, tmuxOutputCommand
	t.Cleanup(func() {
		tmuxRunCommand, inspectPaneExact, tmuxOutputCommand = previousRun, previousInspect, previousOutput
	})

	var delivered []string
	tmuxRunCommand = func(name string, args ...string) error {
		delivered = append([]string{name}, args...)
		return nil
	}
	if err := deliverPaneCommand(paneID, "codex --model gpt-5"); err != nil {
		t.Fatalf("deliver pane-process command: %v", err)
	}
	if got, want := strings.Join(delivered, " "), "tmux respawn-pane -k -t %7 codex --model gpt-5"; got != want {
		t.Fatalf("delivery path = %q, want #577 pane-process path %q", got, want)
	}

	inspectPaneExact = func(id string) tmuxpane.PaneInspection {
		return tmuxpane.PaneInspection{
			State: tmuxpane.PaneInspectionFound,
			Pane:  tmuxpane.TmuxPane{Pane: id, PaneID: id, PID: 4242},
		}
	}
	tmuxOutputCommand = func(string, ...string) (string, error) {
		return paneID + "\t0\n", nil
	}
	panePIDText, err := verifyPaneProcessLaunched(paneID)
	if err != nil {
		t.Fatalf("verify pane-process delivery: %v", err)
	}
	panePID, err := strconv.Atoi(panePIDText)
	if err != nil {
		t.Fatalf("parse verified pane pid %q: %v", panePIDText, err)
	}
	return panePID
}

func TestStopClosePanesAcceptsPaneProcessRecordFromDeliveryPath(t *testing.T) {
	panePID := verifiedPaneProcessPIDFromDeliveryPath(t)
	configured, member, record, pane, project := completeDownPaneFixture(t)
	record.AgentPID = panePID
	if err := launch.Write(filepath.Join(record.Root, "agents", record.Handle), record); err != nil {
		t.Fatalf("write #577 pane-process launch record: %v", err)
	}
	pane.PID = panePID

	var events []string
	deps := PaneCleanupDependencies{
		Inspect: func(string) tmuxpane.PaneInspection {
			events = append(events, "inspect")
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
		},
		ChildrenIndex: func() (func(int) []int, error) {
			t.Fatal("#577 pane-process equality must not require a process snapshot")
			return nil, fmt.Errorf("unreachable")
		},
		Close: func(string) error {
			events = append(events, "close")
			return nil
		},
	}
	report := terminateMember(
		configured, project, team.DefaultProfile, member, "issue-465",
		eventTerminator{events: &events},
		downFakeProbe(map[int]bool{panePID: true}, map[int]bool{panePID: true}),
		nil, true, deps, fakeMissingWakeCheck,
	)
	if report.Status != downStatusStopped || report.Pane.Outcome != PaneCleanupClosed {
		t.Fatalf("stop --close-panes report=%+v, want stopped and pane closed", report)
	}
	if got, want := strings.Join(events, ","), "inspect,signal,inspect,close"; got != want {
		t.Fatalf("stop --close-panes events=%q, want %q", got, want)
	}
}

func TestPaneCleanupRejectsUnrelatedPaneProcessPID(t *testing.T) {
	panePID := verifiedPaneProcessPIDFromDeliveryPath(t)
	configured, member, record, pane, project := completeDownPaneFixture(t)
	record.AgentPID = panePID + 1
	if err := launch.Write(filepath.Join(record.Root, "agents", record.Handle), record); err != nil {
		t.Fatalf("write unrelated agent launch record: %v", err)
	}
	pane.PID = panePID

	closeCalls := 0
	report := terminateMember(
		configured, project, team.DefaultProfile, member, "issue-465",
		eventTerminator{events: &[]string{}},
		downFakeProbe(map[int]bool{record.AgentPID: true}, map[int]bool{record.AgentPID: true}),
		nil, true, PaneCleanupDependencies{
			Inspect: func(string) tmuxpane.PaneInspection {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
			},
			ChildrenIndex: func() (func(int) []int, error) {
				return func(int) []int { return nil }, nil
			},
			Close: func(string) error {
				closeCalls++
				return nil
			},
		}, fakeMissingWakeCheck,
	)
	if report.Status != downStatusStopped || report.Pane.Outcome != PaneCleanupPreservedIdentityUnconfirmed {
		t.Fatalf("unrelated cleanup report=%+v, want signaled with pane preserved", report)
	}
	if closeCalls != 0 {
		t.Fatalf("unrelated pane-process cleanup closed pane %d times", closeCalls)
	}
	if len(report.Pane.Mismatches) != 1 || report.Pane.Mismatches[0].Field != "agent_pid_ancestry" {
		t.Fatalf("unrelated pane-process mismatch=%+v", report.Pane.Mismatches)
	}
}

func TestStrictDescendantRemainsStrictForPaneCleanup(t *testing.T) {
	if strictDescendant(func(int) []int { return nil }, 10, 10) {
		t.Fatal("strictDescendant accepted equality; pane-cleanup semantics must remain strict")
	}
}

func TestPaneProcessOrDescendantRejectsNonPositivePIDs(t *testing.T) {
	for _, tc := range []struct {
		name     string
		panePID  int
		agentPID int
	}{
		{name: "zero equality", panePID: 0, agentPID: 0},
		{name: "negative equality", panePID: -1, agentPID: -1},
		{name: "missing pane pid", panePID: 0, agentPID: 1},
		{name: "missing agent pid", panePID: 1, agentPID: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if paneProcessOrDescendant(nil, tc.panePID, tc.agentPID) {
				t.Fatalf("paneProcessOrDescendant(nil, %d, %d) accepted a non-positive PID", tc.panePID, tc.agentPID)
			}
		})
	}
}

func TestVerifyAgentPaneLineageRejectsUnrelatedAndAcceptsDescendantPID(t *testing.T) {
	tree := map[int][]int{10: {20}, 20: {30}, 40: {101}}
	children := func() (func(int) []int, error) { return func(pid int) []int { return tree[pid] }, nil }
	if err := verifyAgentPaneLineage(10, 101, children); err == nil || !strings.Contains(err.Error(), "neither recorded pane process") {
		t.Fatalf("unexpected lineage result: %v", err)
	}
	if err := verifyAgentPaneLineage(10, 30, children); err != nil {
		t.Fatalf("valid descendant rejected: %v", err)
	}
	if err := verifyAgentPaneLineage(10, 30, func() (func(int) []int, error) { return nil, fmt.Errorf("denied") }); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unavailable lineage did not fail closed: %v", err)
	}
}
