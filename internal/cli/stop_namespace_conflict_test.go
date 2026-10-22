package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/tmuxpane"
)

const (
	exactStopProfile = "plane-cli"
	exactStopSession = "plane-cli"
)

type legacyTreeEntry struct {
	Mode  os.FileMode
	IsDir bool
	Data  string
}

func exactStopManagedPaneDeps(project string, paneIDs []string, agentPIDs []int) (PaneCleanupDependencies, *[]string) {
	known := make(map[string]bool, len(paneIDs))
	for _, paneID := range paneIDs {
		known[paneID] = true
	}
	closed := []string{}
	return PaneCleanupDependencies{
		Inspect: func(paneID string) tmuxpane.PaneInspection {
			if !known[paneID] {
				return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionGone, Detail: "pane is gone"}
			}
			return tmuxpane.PaneInspection{State: tmuxpane.PaneInspectionFound, Pane: tmuxpane.TmuxPane{
				Session: "tmux-plane", WindowID: "@1", PaneID: paneID, PID: 100, CWD: project,
			}}
		},
		ChildrenIndex: func() (func(int) []int, error) {
			return func(parent int) []int {
				if parent == 100 {
					return agentPIDs
				}
				return nil
			}, nil
		},
		Close: func(paneID string) error {
			closed = append(closed, paneID)
			return nil
		},
	}, &closed
}

func swapTeamMemberStopRunner(t *testing.T, runner func([]string) error) {
	t.Helper()
	old := teamMemberStop
	teamMemberStop = runner
	t.Cleanup(func() { teamMemberStop = old })
}

func seedExactStopProject(t *testing.T, members []team.Member) (projectDir, namedRoot, legacyRoot string) {
	t.Helper()
	setupFakeAMQSessionRoots(t)
	projectDir = canonicalFilesystemPath(t.TempDir())
	resumeChdir(t, projectDir)
	seedProfile(t, projectDir, exactStopProfile, team.Team{
		Project:    projectDir,
		Workstream: exactStopSession,
		Members:    members,
	})
	namedRoot = squadnamespace.AMQRoot(projectDir, exactStopProfile, exactStopSession)
	legacyRoot = squadnamespace.AMQRoot(projectDir, team.DefaultProfile, exactStopSession)

	legacyFiles := map[string]string{
		filepath.Join(legacyRoot, "agents", "legacy", "inbox", "new", "message.md"): "legacy inbox bytes\x00\n",
		filepath.Join(legacyRoot, "agents", "legacy", "presence.json"):              `{"schema":1,"handle":"legacy","status":"active"}`,
		filepath.Join(legacyRoot, "agents", "legacy", ".wake.lock"):                 `{"pid":9191,"root":"legacy-root"}`,
		filepath.Join(legacyRoot, "threads", "legacy.md"):                           "legacy thread\n",
	}
	for path, data := range legacyFiles {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return projectDir, namedRoot, legacyRoot
}

func writeExactStopRecord(t *testing.T, namedRoot string, member team.Member, pid int, paneID string) string {
	t.Helper()
	agentDir := filepath.Join(namedRoot, "agents", member.Handle)
	project := filepath.Dir(filepath.Dir(filepath.Dir(namedRoot)))
	var tmux *launch.TmuxInfo
	if paneID != "" {
		tmux = &launch.TmuxInfo{Session: "tmux-plane", WindowID: "@1", PaneID: paneID, Target: "new-window"}
	}
	rec := fullyManagedLaunchRecord(project, filepath.Dir(namedRoot), namedRoot, exactStopProfile, exactStopSession, member, pid, tmux)
	rec.StartedAt = time.Now().UTC()
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	return agentDir
}

func snapshotLegacyTree(t *testing.T, legacyRoot, namedRoot string) map[string]legacyTreeEntry {
	t.Helper()
	out := map[string]legacyTreeEntry{}
	err := filepath.WalkDir(legacyRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if sameResolvedDir(path, namedRoot) {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(legacyRoot, path)
		if err != nil {
			return err
		}
		item := legacyTreeEntry{Mode: info.Mode(), IsDir: entry.IsDir()}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.Data = string(data)
		}
		out[rel] = item
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertLegacyTreeUnchanged(t *testing.T, before map[string]legacyTreeEntry, legacyRoot, namedRoot string) {
	t.Helper()
	after := snapshotLegacyTree(t, legacyRoot, namedRoot)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("legacy/default state changed\nbefore: %#v\nafter:  %#v", before, after)
	}
}

func exactDownArgs(projectDir string, extra ...string) []string {
	args := []string{"--project", projectDir, "--profile", exactStopProfile, "--session", exactStopSession}
	return append(args, extra...)
}

func TestAllowsExactNamedProfileDownMatrix(t *testing.T) {
	project := t.TempDir()
	root := squadnamespace.AMQRoot(project, exactStopProfile, exactStopSession)
	conflict := &namespaceConflictData{
		Kind:             "legacy_session_root",
		Profile:          exactStopProfile,
		Session:          exactStopSession,
		RequestedAMQRoot: root,
	}
	valid := exactDownNamespaceScope{
		Verb: "down", ProjectDir: project, Profile: exactStopProfile, Session: exactStopSession,
		All: true, ExplicitProject: true, ExplicitProfile: true, ExplicitSession: true,
	}
	if !allowsExactNamedProfileDown(conflict, valid) {
		t.Fatal("fully explicit named legacy-root stop should be allowed")
	}

	tests := map[string]func(*namespaceConflictData, *exactDownNamespaceScope){
		"nil conflict":              func(c *namespaceConflictData, _ *exactDownNamespaceScope) { *c = namespaceConflictData{} },
		"future conflict kind":      func(c *namespaceConflictData, _ *exactDownNamespaceScope) { c.Kind = "future_kind" },
		"creation collision":        func(c *namespaceConflictData, _ *exactDownNamespaceScope) { c.Kind = "profile_session_root_collision" },
		"conflict profile mismatch": func(c *namespaceConflictData, _ *exactDownNamespaceScope) { c.Profile = "other" },
		"conflict session mismatch": func(c *namespaceConflictData, _ *exactDownNamespaceScope) { c.Session = "other" },
		"conflict root mismatch": func(c *namespaceConflictData, _ *exactDownNamespaceScope) {
			c.RequestedAMQRoot = filepath.Join(project, "other")
		},
		"different verb":     func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Verb = "resume" },
		"project inferred":   func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.ExplicitProject = false },
		"profile inferred":   func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.ExplicitProfile = false },
		"session inferred":   func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.ExplicitSession = false },
		"project empty":      func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.ProjectDir = "" },
		"profile default":    func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Profile = team.DefaultProfile },
		"profile empty":      func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Profile = "" },
		"session empty":      func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Session = "" },
		"selector missing":   func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.All = false },
		"selectors both set": func(_ *namespaceConflictData, s *exactDownNamespaceScope) { s.Role = "cto" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			gotConflict := *conflict
			gotScope := valid
			mutate(&gotConflict, &gotScope)
			var candidate *namespaceConflictData = &gotConflict
			if name == "nil conflict" {
				candidate = nil
			}
			if allowsExactNamedProfileDown(candidate, gotScope) {
				t.Fatal("non-exact stop scope unexpectedly allowed")
			}
		})
	}

	roleScope := valid
	roleScope.All = false
	roleScope.Role = "cto"
	if !allowsExactNamedProfileDown(conflict, roleScope) {
		t.Fatal("role-scoped exact down should be allowed")
	}
}
