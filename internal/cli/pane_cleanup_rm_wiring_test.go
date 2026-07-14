package cli

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

type recordingPaneCleanupManifestStore struct {
	prepareCalls  int
	finalizeCalls int
	finalizeErr   error
	finalized     paneCleanupManifest
}

func (s *recordingPaneCleanupManifestStore) Prepare(projectDir string, manifest paneCleanupManifest) (paneCleanupManifestHandle, error) {
	s.prepareCalls++
	return paneCleanupManifestHandle{
		Project: projectDir, Profile: manifest.Profile, Session: manifest.Session, OperationID: manifest.OperationID,
		Operation: manifest.Operation, PreparedSHA256: "prepared-digest", PreparedManifest: manifest,
		Prepared: filepath.Join(projectDir, "prepared.json"), Final: filepath.Join(projectDir, "final.json"),
	}, nil
}

func (s *recordingPaneCleanupManifestStore) Finalize(_ paneCleanupManifestHandle, manifest paneCleanupManifest) error {
	s.finalizeCalls++
	s.finalized = manifest
	return s.finalizeErr
}

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

func rmPaneDeps(pane tmuxpane.TmuxPane, closeCalls *int) PaneCleanupDependencies {
	return PaneCleanupDependencies{
		Inspect: func(string) tmuxpane.PaneInspection {
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: pane}
		},
		Close: func(string) error {
			*closeCalls++
			return nil
		},
		ChildrenIndex: func() (func(int) []int, error) {
			return func(parent int) []int {
				if parent == 100 {
					return []int{200}
				}
				if parent == 200 {
					return []int{4242}
				}
				return nil
			}, nil
		},
	}
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

func TestRmSnapshotEnumerationErrorPreventsManifestAndMutation(t *testing.T) {
	project := t.TempDir()
	base := t.TempDir()
	root := filepath.Join(base, "issue-465")
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := &recordingPaneCleanupManifestStore{}
	snapshotErr := errors.New("agent directory enumeration failed")
	_, err := runRmExec(t, rmExecution{
		ProjectDir: project, Session: "issue-465", Mode: rmModeDelete, Yes: true, BaseRoot: base,
		ManifestStore: store, OperationID: "snapshot-failure",
		SnapshotPaneWork: func(string, team.Team, string, string, string, string, bool) ([]rmPaneWork, error) {
			return nil, snapshotErr
		},
	})
	if !errors.Is(err, snapshotErr) {
		t.Fatalf("err=%v, want snapshot enumeration error", err)
	}
	if store.prepareCalls != 0 || store.finalizeCalls != 0 {
		t.Fatalf("manifest calls=%d/%d, want zero", store.prepareCalls, store.finalizeCalls)
	}
	if _, statErr := os.Stat(root); statErr != nil {
		t.Fatalf("snapshot failure mutated namespace: %v", statErr)
	}
}

func TestRmMutationFailureAfterSignalIsPartialAndPreservesPreparedPane(t *testing.T) {
	project, base, _, _, record, pane := completeRmPaneFixture(t, "issue-465", 4242)
	seedAgentRecord(t, base, "issue-465", "cto", record)
	archiveCollision := filepath.Join(base, archiveDirName, "issue-465")
	if err := os.MkdirAll(archiveCollision, 0o755); err != nil {
		t.Fatal(err)
	}
	store := &recordingPaneCleanupManifestStore{}
	term := &recordingTerminator{}
	closeCalls := 0
	out, err := runRmExec(t, rmExecution{
		ProjectDir: project, Session: "issue-465", Mode: rmModeArchive, Yes: true, StopAgents: true, ClosePanes: true,
		BaseRoot: base, Probe: rmStateProbe(map[int]bool{4242: true}, map[int]bool{4242: true}), Terminator: term,
		PaneDeps: rmPaneDeps(pane, &closeCalls), ManifestStore: store, OperationID: "mutation-failure",
	})
	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("err=%v, want PartialError after signal\n%s", err, out)
	}
	if len(term.calls) != 1 || closeCalls != 0 {
		t.Fatalf("signal/close calls=%v/%d, want one signal and no close", term.calls, closeCalls)
	}
	if store.finalizeCalls != 1 || len(store.finalized.Entries) != 1 || store.finalized.Entries[0].Pane == nil {
		t.Fatalf("final manifest missing explicit pane result: %+v", store.finalized)
	}
	if got := store.finalized.Entries[0].Pane.Outcome; got != PaneCleanupPreservedIdentityUnconfirmed {
		t.Fatalf("pane outcome=%s, want explicit preserved outcome", got)
	}
	if !strings.HasPrefix(store.finalized.NamespaceMutation, "failed:") {
		t.Fatalf("namespace mutation status=%q", store.finalized.NamespaceMutation)
	}
}

func TestRmJSONFinalizeFailureIsTruthful(t *testing.T) {
	project := t.TempDir()
	base := t.TempDir()
	root := filepath.Join(base, "issue-465")
	if err := os.MkdirAll(filepath.Join(root, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := &recordingPaneCleanupManifestStore{finalizeErr: errors.New("directory fsync uncertain")}
	out, err := runRmExec(t, rmExecution{
		ProjectDir: project, Session: "issue-465", Mode: rmModeDelete, Yes: true, JSON: true, BaseRoot: base,
		ManifestStore: store, OperationID: "finalize-failure",
	})
	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("err=%v, want PartialError\n%s", err, out)
	}
	env := decodeJSONEnvelope[rmCleanupEnvelopeData](t, out)
	if env.Data.NamespaceMutation != "succeeded" || !strings.HasPrefix(env.Data.Finalization, "failed:") {
		t.Fatalf("mutation/finalization=%q/%q", env.Data.NamespaceMutation, env.Data.Finalization)
	}
	if env.Data.FinalManifest != "" || env.Data.FinalCandidate == "" || env.Data.PreparedManifest == "" {
		t.Fatalf("truthfulness paths: prepared=%q final=%q candidate=%q", env.Data.PreparedManifest, env.Data.FinalManifest, env.Data.FinalCandidate)
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("namespace mutation did not occur: %v", statErr)
	}
	if store.finalizeCalls != 1 || store.finalized.FinalizedAt.Before(store.finalized.CreatedAt) || time.Since(store.finalized.FinalizedAt) > time.Minute {
		t.Fatalf("finalization attempt not captured: %+v", store.finalized)
	}
}

func TestRmForceExternalAdoptionNoticeNeverOffersManagedPaneRecovery(t *testing.T) {
	project, base, _, _, record, _ := completeRmPaneFixture(t, "issue-465", 4242)
	record.External = false
	record.AdoptionMode = "external_project_lead"
	seedAgentRecord(t, base, "issue-465", "cto", record)
	root := filepath.Join(base, "issue-465")
	out, err := runRmExec(t, rmExecution{
		ProjectDir: project, Session: "issue-465", Mode: rmModeDelete, Yes: true, Force: true, ClosePanes: true,
		BaseRoot: base, Probe: rmStateProbe(map[int]bool{4242: true}, map[int]bool{4242: true}),
	})
	var partial *PartialError
	if !errors.As(err, &partial) {
		t.Fatalf("external requested pane preservation should be partial: %v\n%s", err, out)
	}
	for _, want := range []string{"preserved_external", "external pane(s) are operator-owned", "cto"} {
		if !strings.Contains(out, want) {
			t.Fatalf("external-adoption notice missing %q:\n%s", want, out)
		}
	}
	for _, forbidden := range []string{"unmanaged recorded pane id(s)", "re-attest", "then close only that exact id"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("external-adoption notice offered managed recovery %q:\n%s", forbidden, out)
		}
	}
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("force teardown did not remove external session namespace: %v", statErr)
	}
}
