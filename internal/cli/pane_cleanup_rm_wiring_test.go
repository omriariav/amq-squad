package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/tmuxpane"
)

func completeRmPaneFixture(t *testing.T, session string, pid int) (string, string, team.Team, team.Member, launch.Record, tmuxpane.TmuxPane) {
	t.Helper()
	project := t.TempDir()
	base := t.TempDir()
	cwd := filepath.Join(project, "member")
	if err := os.Mkdir(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	member := team.Member{Role: "cto", Handle: "cto", Binary: "codex", Session: session, CWD: cwd}
	configured := team.Team{Members: []team.Member{member}}
	if err := team.Write(project, configured); err != nil {
		t.Fatal(err)
	}
	configured.Project = project
	root := filepath.Join(base, session)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	tmux := &launch.TmuxInfo{Session: "mux-465", WindowID: "@7", WindowName: "cto", PaneID: "%9", Target: "new-window"}
	record := fullyManagedLaunchRecord(project, base, root, team.DefaultProfile, session, member, pid, tmux)
	pane := tmuxpane.TmuxPane{Session: tmux.Session, WindowID: tmux.WindowID, PaneID: tmux.PaneID, PID: 100, CWD: cwd}
	return project, base, configured, member, record, pane
}

func TestRmKeepPanesStillStopsFreshlyAttestedAgent(t *testing.T) {
	project, base, configured, member, record, _ := completeRmPaneFixture(t, "issue-465", 4242)
	request := paneCleanupRequestForMember(configured, project, team.DefaultProfile, "issue-465", member, member.Handle,
		member.CWD, filepath.Join(base, "issue-465"), base, record, false, PaneCleanupAgentAttestation{})
	work := []rmPaneWork{{Role: member.Role, Handle: member.Handle, Record: record, RecordFound: true,
		Member: member, MemberFound: true, Request: request}}
	term := &recordingTerminator{}
	attestAndStopRmAgents(work, map[string]bool{"cto": true}, true, term,
		rmStateProbe(map[int]bool{4242: true}, map[int]bool{4242: true}), PaneCleanupDependencies{})
	if len(term.calls) != 1 || term.calls[0] != 4242 {
		t.Fatalf("--keep-panes suppressed agent signal: calls=%v", term.calls)
	}
	if work[0].AgentStatus != "stopped" || work[0].Pane.Outcome != PaneCleanupNotRequested {
		t.Fatalf("agent/pane outcomes = %s/%s, want stopped/not_requested", work[0].AgentStatus, work[0].Pane.Outcome)
	}
}

func TestRmStopAgentsRefusesToSignalReusedSameBinaryPID(t *testing.T) {
	project, base, configured, member, record, _ := completeRmPaneFixture(t, "issue-465", 4242)
	record.StartedAt = time.Now().Add(-10 * time.Minute)
	request := paneCleanupRequestForMember(configured, project, team.DefaultProfile, "issue-465", member, member.Handle,
		member.CWD, filepath.Join(base, "issue-465"), base, record, false, PaneCleanupAgentAttestation{})
	work := []rmPaneWork{{Role: member.Role, Handle: member.Handle, Record: record, RecordFound: true,
		Member: member, MemberFound: true, Request: request}}
	probe := rmStateProbe(map[int]bool{4242: true}, map[int]bool{4242: true})
	probe.ProcessStartTime = func(pid int) (time.Time, bool) {
		return record.StartedAt.Add(launchProcessStartSkewEpsilon + time.Nanosecond), pid == 4242
	}
	term := &recordingTerminator{}
	attestAndStopRmAgents(work, map[string]bool{"cto": true}, true, term, probe, PaneCleanupDependencies{})
	if len(term.calls) != 0 {
		t.Fatalf("reused same-binary PID was signaled: %v", term.calls)
	}
	if work[0].AgentStatus != "not_live" || !strings.Contains(work[0].AgentDetail, "runtime identity") {
		t.Fatalf("agent outcome=%s detail=%q, want fail-closed identity refusal", work[0].AgentStatus, work[0].AgentDetail)
	}
}
