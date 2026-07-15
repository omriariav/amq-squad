package cli

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
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

func stopRunnerForTest(term processTerminator, probe duplicateLaunchProbe) func([]string) error {
	return func(args []string) error {
		return runStopWithDeps(args, func(bool) processTerminator { return term }, probe)
	}
}

func stopRunnerForPaneTest(term processTerminator, probe duplicateLaunchProbe, paneDeps PaneCleanupDependencies) func([]string) error {
	return func(args []string) error {
		return runStopWithPaneDeps(args, func(bool) processTerminator { return term }, probe, paneDeps)
	}
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
	projectDir = t.TempDir()
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

func exactStopArgs(projectDir string, extra ...string) []string {
	args := []string{"--project", projectDir, "--profile", exactStopProfile, "--session", exactStopSession}
	return append(args, extra...)
}

func TestAllowsExactNamedProfileStopMatrix(t *testing.T) {
	project := t.TempDir()
	root := squadnamespace.AMQRoot(project, exactStopProfile, exactStopSession)
	conflict := &namespaceConflictData{
		Kind:             "legacy_session_root",
		Profile:          exactStopProfile,
		Session:          exactStopSession,
		RequestedAMQRoot: root,
	}
	valid := exactStopNamespaceScope{
		Verb: "stop", ProjectDir: project, Profile: exactStopProfile, Session: exactStopSession,
		All: true, ExplicitProject: true, ExplicitProfile: true, ExplicitSession: true,
	}
	if !allowsExactNamedProfileStop(conflict, valid) {
		t.Fatal("fully explicit named legacy-root stop should be allowed")
	}

	tests := map[string]func(*namespaceConflictData, *exactStopNamespaceScope){
		"nil conflict":              func(c *namespaceConflictData, _ *exactStopNamespaceScope) { *c = namespaceConflictData{} },
		"future conflict kind":      func(c *namespaceConflictData, _ *exactStopNamespaceScope) { c.Kind = "future_kind" },
		"creation collision":        func(c *namespaceConflictData, _ *exactStopNamespaceScope) { c.Kind = "profile_session_root_collision" },
		"conflict profile mismatch": func(c *namespaceConflictData, _ *exactStopNamespaceScope) { c.Profile = "other" },
		"conflict session mismatch": func(c *namespaceConflictData, _ *exactStopNamespaceScope) { c.Session = "other" },
		"conflict root mismatch": func(c *namespaceConflictData, _ *exactStopNamespaceScope) {
			c.RequestedAMQRoot = filepath.Join(project, "other")
		},
		"different verb":     func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.Verb = "resume" },
		"project inferred":   func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.ExplicitProject = false },
		"profile inferred":   func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.ExplicitProfile = false },
		"session inferred":   func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.ExplicitSession = false },
		"project empty":      func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.ProjectDir = "" },
		"profile default":    func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.Profile = team.DefaultProfile },
		"profile empty":      func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.Profile = "" },
		"session empty":      func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.Session = "" },
		"selector missing":   func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.All = false },
		"selectors both set": func(_ *namespaceConflictData, s *exactStopNamespaceScope) { s.Role = "cto" },
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
			if allowsExactNamedProfileStop(candidate, gotScope) {
				t.Fatal("non-exact stop scope unexpectedly allowed")
			}
		})
	}

	roleScope := valid
	roleScope.All = false
	roleScope.Role = "cto"
	if !allowsExactNamedProfileStop(conflict, roleScope) {
		t.Fatal("role-scoped exact stop should be allowed")
	}
}

func TestRunStopExactNamedProfileConflictStopsAllAndPreservesLegacyState(t *testing.T) {
	members := []team.Member{
		{Role: "cto", Binary: "codex", Handle: "cto"},
		{Role: "qa", Binary: "codex", Handle: "qa"},
	}
	project, namedRoot, legacyRoot := seedExactStopProject(t, members)
	now := time.Now().UTC()
	alive := map[int]bool{}
	match := map[int]bool{}
	for i, member := range members {
		pid := 1100 + i
		wakePID := 2100 + i
		agentDir := writeExactStopRecord(t, namedRoot, member, pid, "")
		writeWakeLock(t, agentDir, wakeLockFile{PID: wakePID, Root: namedRoot})
		writePresence(t, agentDir, presenceFile{Schema: 1, Handle: member.Handle, Status: "active", LastSeen: now})
		alive[pid], alive[wakePID] = true, true
		match[pid], match[wakePID] = true, true
	}
	legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
	term := &recordingTerminator{}
	stop := stopRunnerForTest(term, downFakeProbe(alive, match))
	swapStatusPaneLister(t, nil, nil)

	stdout, _, err := captureOutput(t, func() error {
		return stop(exactStopArgs(project, "--all"))
	})
	if err != nil {
		t.Fatalf("exact named-profile stop: %v\n%s", err, stdout)
	}
	wantCalls := []int{1100, 2100, 1101, 2101}
	if !reflect.DeepEqual(term.calls, wantCalls) {
		t.Fatalf("terminate calls = %v, want %v", term.calls, wantCalls)
	}
	for _, member := range members {
		agentDir := filepath.Join(namedRoot, "agents", member.Handle)
		if _, err := os.Stat(wakeLockPath(agentDir)); !os.IsNotExist(err) {
			t.Fatalf("named wake lock for %s was not removed: %v", member.Role, err)
		}
		presence, err := readPresenceForEntry(agentDir)
		if err != nil || presence.Status != "offline" {
			t.Fatalf("named presence for %s = %+v / %v, want offline", member.Role, presence, err)
		}
	}
	assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
}

func TestExactNamedProfileStopWakeLockRootValidation(t *testing.T) {
	t.Run("explicit legacy root is removed without touching its wake pid", func(t *testing.T) {
		member := team.Member{Role: "cto", Binary: "codex", Handle: "cto"}
		project, namedRoot, legacyRoot := seedExactStopProject(t, []team.Member{member})
		agentDir := writeExactStopRecord(t, namedRoot, member, 8100, "")
		writeWakeLock(t, agentDir, wakeLockFile{PID: 8200, Root: legacyRoot})
		writePresence(t, agentDir, presenceFile{Schema: 1, Handle: "cto", Status: "active", LastSeen: time.Now()})
		legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
		legacyWakeArgs := "amq wake --me cto --root " + legacyRoot
		if !wakeProcessMatcher("cto", legacyRoot)(legacyWakeArgs) {
			t.Fatal("fixture must describe a matching legacy-root wake")
		}

		var processMatchCalls []int
		probe := duplicateLaunchProbe{
			PIDAlive: func(pid int) bool { return pid == 8100 || pid == 8200 },
			ProcessMatch: func(pid int, predicate func(args string) bool) bool {
				processMatchCalls = append(processMatchCalls, pid)
				switch pid {
				case 8100:
					return predicate("codex --search")
				case 8200:
					return predicate(legacyWakeArgs)
				default:
					return false
				}
			},
			Now: time.Now,
		}
		term := &recordingTerminator{}
		stop := stopRunnerForTest(term, probe)
		swapStatusPaneLister(t, nil, nil)

		stdout, _, err := captureOutput(t, func() error {
			return stop(exactStopArgs(project, "--all"))
		})
		if err != nil {
			t.Fatalf("poisoned wake-lock stop: %v\n%s", err, stdout)
		}
		if !reflect.DeepEqual(processMatchCalls, []int{8100}) {
			t.Fatalf("ProcessMatch calls = %v, poisoned legacy wake pid must not be inspected", processMatchCalls)
		}
		if !reflect.DeepEqual(term.calls, []int{8100}) {
			t.Fatalf("signals = %v, want only named agent pid", term.calls)
		}
		if _, statErr := os.Stat(wakeLockPath(agentDir)); !os.IsNotExist(statErr) {
			t.Fatalf("poisoned named-dir wake lock was not removed: %v", statErr)
		}
		presence, readErr := readPresenceForEntry(agentDir)
		if readErr != nil || presence.Status != "offline" {
			t.Fatalf("named presence = %+v / %v, want offline", presence, readErr)
		}
		assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
	})

	t.Run("empty root falls back to selected named root", func(t *testing.T) {
		member := team.Member{Role: "cto", Binary: "codex", Handle: "cto"}
		project, namedRoot, _ := seedExactStopProject(t, []team.Member{member})
		agentDir := writeExactStopRecord(t, namedRoot, member, 8300, "")
		writeWakeLock(t, agentDir, wakeLockFile{PID: 8301})
		probe := duplicateLaunchProbe{
			PIDAlive: func(pid int) bool { return pid == 8300 || pid == 8301 },
			ProcessMatch: func(pid int, predicate func(args string) bool) bool {
				if pid == 8300 {
					return predicate("codex --search")
				}
				return pid == 8301 && predicate("amq wake --me cto --root "+namedRoot)
			},
			Now: time.Now,
		}
		term := &recordingTerminator{}
		stop := stopRunnerForTest(term, probe)
		swapStatusPaneLister(t, nil, nil)
		if _, _, err := captureOutput(t, func() error { return stop(exactStopArgs(project, "--all")) }); err != nil {
			t.Fatalf("empty-root wake stop: %v", err)
		}
		if !reflect.DeepEqual(term.calls, []int{8300, 8301}) {
			t.Fatalf("signals = %v, want named agent and selected-root wake", term.calls)
		}
		if _, statErr := os.Stat(wakeLockPath(agentDir)); !os.IsNotExist(statErr) {
			t.Fatalf("empty-root wake lock was not removed: %v", statErr)
		}
	})
}

func TestRunStopExactNamedProfileRoleAndClosePane(t *testing.T) {
	members := []team.Member{
		{Role: "cto", Binary: "codex", Handle: "cto"},
		{Role: "qa", Binary: "codex", Handle: "qa"},
	}
	project, namedRoot, legacyRoot := seedExactStopProject(t, members)
	writeExactStopRecord(t, namedRoot, members[0], 3100, "%31")
	writeExactStopRecord(t, namedRoot, members[1], 3200, "%32")
	legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
	term := &recordingTerminator{}
	paneDeps, closed := exactStopManagedPaneDeps(project, []string{"%31", "%32"}, []int{3100, 3200})
	stop := stopRunnerForPaneTest(term, downFakeProbe(map[int]bool{3100: true, 3200: true}, map[int]bool{3100: true, 3200: true}), paneDeps)
	swapStatusPaneLister(t, nil, nil)

	stdout, _, err := captureOutput(t, func() error {
		return stop(exactStopArgs(project, "--role", "cto", "--close-panes"))
	})
	if err != nil {
		t.Fatalf("role exact stop: %v\n%s", err, stdout)
	}
	if !reflect.DeepEqual(term.calls, []int{3100}) {
		t.Fatalf("terminate calls = %v, want only cto", term.calls)
	}
	if !reflect.DeepEqual(*closed, []string{"%31"}) {
		t.Fatalf("closed panes = %v, want only cto pane", *closed)
	}
	assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
}

func TestRunStopExactNamedProfileRequiresEveryScopeFlag(t *testing.T) {
	for _, omitted := range []string{"project", "profile", "session"} {
		t.Run("omit "+omitted, func(t *testing.T) {
			member := team.Member{Role: "cto", Binary: "codex", Handle: "cto"}
			project, namedRoot, legacyRoot := seedExactStopProject(t, []team.Member{member})
			writeExactStopRecord(t, namedRoot, member, 4100, "")
			if omitted == "profile" {
				seedProfile(t, project, team.DefaultProfile, team.Team{
					Project: project, Workstream: exactStopSession,
					Members: []team.Member{member},
				})
			}
			legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
			term := &recordingTerminator{}
			stop := stopRunnerForTest(term, downFakeProbe(map[int]bool{4100: true}, map[int]bool{4100: true}))
			swapStatusPaneLister(t, nil, nil)

			args := []string{"--project", project, "--profile", exactStopProfile, "--session", exactStopSession, "--all"}
			filtered := make([]string, 0, len(args))
			for i := 0; i < len(args); i++ {
				if args[i] == "--"+omitted {
					i++
					continue
				}
				filtered = append(filtered, args[i])
			}
			_, _, err := captureOutput(t, func() error { return stop(filtered) })
			if err == nil || (!strings.Contains(err.Error(), "legacy/default session root") && !strings.Contains(err.Error(), "default-profile")) {
				t.Fatalf("omitted %s should fail closed, got %v", omitted, err)
			}
			if len(term.calls) != 0 {
				t.Fatalf("omitted %s signaled pids: %v", omitted, term.calls)
			}
			assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
		})
	}
}

func TestRunStopImplicitDefaultRefusesMultipleNamedOwners(t *testing.T) {
	setupFakeAMQSessionRoots(t)
	project := t.TempDir()
	resumeChdir(t, project)
	member := team.Member{Role: "cto", Binary: "codex", Handle: "cto", Session: exactStopSession}
	seedProfile(t, project, team.DefaultProfile, team.Team{
		Project: project, Workstream: exactStopSession, Members: []team.Member{member},
	})
	type sentinel struct {
		path string
		data string
	}
	var sentinels []sentinel
	for _, profile := range []string{"product", "release"} {
		seedProfile(t, project, profile, team.Team{
			Project: project, Workstream: exactStopSession, Members: []team.Member{member},
		})
		path := filepath.Join(squadnamespace.AMQRoot(project, profile, exactStopSession), "agents", "cto", "inbox.md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		data := "named owner " + profile + "\n"
		if err := os.WriteFile(path, []byte(data), 0o640); err != nil {
			t.Fatal(err)
		}
		sentinels = append(sentinels, sentinel{path: path, data: data})
	}
	term := &recordingTerminator{}
	stop := stopRunnerForTest(term, downFakeProbe(nil, nil))
	swapStatusPaneLister(t, nil, nil)

	_, _, err := captureOutput(t, func() error {
		return stop([]string{"--project", project, "--session", exactStopSession, "--all"})
	})
	if err == nil || !strings.Contains(err.Error(), "multiple named profiles") {
		t.Fatalf("implicit default stop should refuse multiple named owners, got %v", err)
	}
	if len(term.calls) != 0 {
		t.Fatalf("implicit default stop signaled pids: %v", term.calls)
	}
	for _, item := range sentinels {
		got, readErr := os.ReadFile(item.path)
		if readErr != nil || string(got) != item.data {
			t.Fatalf("named owner sentinel changed at %s: %q / %v", item.path, got, readErr)
		}
	}
	if _, statErr := os.Stat(squadnamespace.AMQRoot(project, team.DefaultProfile, exactStopSession)); !os.IsNotExist(statErr) {
		t.Fatalf("refused implicit-default stop created legacy root: %v", statErr)
	}
}

func TestExactNamedProfileStopLaunchIdentityMismatchFailsBeforeMutation(t *testing.T) {
	tests := map[string]func(*launch.Record, string){
		"profile": func(rec *launch.Record, _ string) { rec.TeamProfile = "other" },
		"session": func(rec *launch.Record, _ string) { rec.Session = "other" },
		"root":    func(rec *launch.Record, project string) { rec.Root = filepath.Join(project, "other") },
		"role":    func(rec *launch.Record, _ string) { rec.Role = "other" },
		"handle":  func(rec *launch.Record, _ string) { rec.Handle = "other" },
	}
	for field, mutate := range tests {
		t.Run(field, func(t *testing.T) {
			member := team.Member{Role: "cto", Binary: "codex", Handle: "cto"}
			project, namedRoot, legacyRoot := seedExactStopProject(t, []team.Member{member})
			agentDir := writeExactStopRecord(t, namedRoot, member, 5100, "%51")
			rec, err := launch.Read(agentDir)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&rec, project)
			if err := launch.Write(agentDir, rec); err != nil {
				t.Fatal(err)
			}
			writeWakeLock(t, agentDir, wakeLockFile{PID: 5101, Root: namedRoot})
			writePresence(t, agentDir, presenceFile{Schema: 1, Handle: "cto", Status: "active", LastSeen: time.Now()})
			legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
			term := &recordingTerminator{}
			stop := stopRunnerForTest(term, downFakeProbe(map[int]bool{5100: true, 5101: true}, map[int]bool{5100: true, 5101: true}))
			swapStatusPaneLister(t, nil, nil)
			closed := swapPaneCloser(t)

			_, _, err = captureOutput(t, func() error {
				return stop(exactStopArgs(project, "--all", "--close-panes"))
			})
			if err == nil || !strings.Contains(err.Error(), "identity validation") {
				t.Fatalf("%s mismatch should fail identity validation, got %v", field, err)
			}
			if len(term.calls) != 0 || len(*closed) != 0 {
				t.Fatalf("%s mismatch mutated runtime: signals=%v panes=%v", field, term.calls, *closed)
			}
			if _, statErr := os.Stat(wakeLockPath(agentDir)); statErr != nil {
				t.Fatalf("%s mismatch removed wake lock: %v", field, statErr)
			}
			presence, readErr := readPresenceForEntry(agentDir)
			if readErr != nil || presence.Status != "active" {
				t.Fatalf("%s mismatch changed presence: %+v / %v", field, presence, readErr)
			}
			assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
		})
	}
}

func TestExactNamedProfileStopSafetyRegressions(t *testing.T) {
	t.Run("reused pid is not signaled", func(t *testing.T) {
		member := team.Member{Role: "cto", Binary: "codex", Handle: "cto"}
		project, namedRoot, legacyRoot := seedExactStopProject(t, []team.Member{member})
		writeExactStopRecord(t, namedRoot, member, 6100, "")
		legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
		term := &recordingTerminator{}
		stop := stopRunnerForTest(term, downFakeProbe(map[int]bool{6100: true}, map[int]bool{6100: false}))
		swapStatusPaneLister(t, nil, nil)
		stdout, _, err := captureOutput(t, func() error { return stop(exactStopArgs(project, "--all")) })
		if err != nil {
			t.Fatalf("reused pid stop: %v\n%s", err, stdout)
		}
		if len(term.calls) != 0 || !strings.Contains(stdout, "PID reuse") {
			t.Fatalf("reused pid result signals=%v output=%s", term.calls, stdout)
		}
		assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
	})

	t.Run("dead agent cleanup stays in named root", func(t *testing.T) {
		member := team.Member{Role: "cto", Binary: "codex", Handle: "cto"}
		project, namedRoot, legacyRoot := seedExactStopProject(t, []team.Member{member})
		agentDir := writeExactStopRecord(t, namedRoot, member, 6200, "")
		writeWakeLock(t, agentDir, wakeLockFile{PID: 6201, Root: namedRoot})
		writePresence(t, agentDir, presenceFile{Schema: 1, Handle: "cto", Status: "active", LastSeen: time.Now()})
		legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
		term := &recordingTerminator{}
		stop := stopRunnerForTest(term, downFakeProbe(map[int]bool{6200: false, 6201: true}, map[int]bool{6201: true}))
		swapStatusPaneLister(t, nil, nil)
		stdout, _, err := captureOutput(t, func() error { return stop(exactStopArgs(project, "--all")) })
		if err != nil {
			t.Fatalf("dead cleanup: %v\n%s", err, stdout)
		}
		if !reflect.DeepEqual(term.calls, []int{6201}) || !strings.Contains(stdout, "cleaned") {
			t.Fatalf("dead cleanup signals=%v output=%s", term.calls, stdout)
		}
		assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
	})

	t.Run("mixed failure is partial and failed pane stays open", func(t *testing.T) {
		members := []team.Member{
			{Role: "cto", Binary: "codex", Handle: "cto"},
			{Role: "qa", Binary: "codex", Handle: "qa"},
		}
		project, namedRoot, legacyRoot := seedExactStopProject(t, members)
		writeExactStopRecord(t, namedRoot, members[0], 6300, "%63")
		writeExactStopRecord(t, namedRoot, members[1], 6301, "%64")
		legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
		term := &recordingTerminator{failOn: map[int]error{6301: errors.New("denied")}}
		paneDeps, closed := exactStopManagedPaneDeps(project, []string{"%63", "%64"}, []int{6300, 6301})
		stop := stopRunnerForPaneTest(term, downFakeProbe(map[int]bool{6300: true, 6301: true}, map[int]bool{6300: true, 6301: true}), paneDeps)
		swapStatusPaneLister(t, nil, nil)
		stdout, _, err := captureOutput(t, func() error {
			return stop(exactStopArgs(project, "--all", "--close-panes"))
		})
		var partial *PartialError
		if !errors.As(err, &partial) {
			t.Fatalf("mixed stop error = %T %v, want PartialError\n%s", err, err, stdout)
		}
		if !reflect.DeepEqual(*closed, []string{"%63"}) {
			t.Fatalf("closed panes = %v, failed qa pane must remain", *closed)
		}
		assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
	})
}

func TestTeamMemberRmStopExactNamedConflictAndMissingSession(t *testing.T) {
	t.Run("explicit member session stops then removes", func(t *testing.T) {
		member := team.Member{Role: "qa", Binary: "codex", Handle: "qa", Session: exactStopSession}
		project, namedRoot, legacyRoot := seedExactStopProject(t, []team.Member{member})
		writeExactStopRecord(t, namedRoot, member, 7100, "")
		legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
		term := &recordingTerminator{}
		swapTeamMemberStopRunner(t, stopRunnerForTest(term, downFakeProbe(map[int]bool{7100: true}, map[int]bool{7100: true})))
		swapStatusPaneLister(t, nil, nil)
		_, _, err := captureOutput(t, func() error {
			return runTeamMember([]string{"rm", "qa", "--project", project, "--profile", exactStopProfile, "--stop"})
		})
		if err != nil {
			t.Fatalf("team member rm --stop: %v", err)
		}
		if !reflect.DeepEqual(term.calls, []int{7100}) {
			t.Fatalf("signals = %v, want qa pid", term.calls)
		}
		cfg, err := team.ReadProfile(project, exactStopProfile)
		if err != nil || len(cfg.Members) != 0 {
			t.Fatalf("roster after remove = %+v / %v", cfg.Members, err)
		}
		assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
	})

	t.Run("missing member session refuses and preserves roster", func(t *testing.T) {
		member := team.Member{Role: "qa", Binary: "codex", Handle: "qa"}
		project, namedRoot, legacyRoot := seedExactStopProject(t, []team.Member{member})
		writeExactStopRecord(t, namedRoot, member, 7200, "")
		legacyBefore := snapshotLegacyTree(t, legacyRoot, namedRoot)
		term := &recordingTerminator{}
		swapTeamMemberStopRunner(t, stopRunnerForTest(term, downFakeProbe(map[int]bool{7200: true}, map[int]bool{7200: true})))
		swapStatusPaneLister(t, nil, nil)
		_, _, err := captureOutput(t, func() error {
			return runTeamMember([]string{"rm", "qa", "--project", project, "--profile", exactStopProfile, "--stop"})
		})
		if err == nil || !strings.Contains(err.Error(), "legacy/default session root") {
			t.Fatalf("missing session should fail closed, got %v", err)
		}
		if len(term.calls) != 0 {
			t.Fatalf("missing session signaled pids: %v", term.calls)
		}
		cfg, readErr := team.ReadProfile(project, exactStopProfile)
		if readErr != nil || len(cfg.Members) != 1 || cfg.Members[0].Role != "qa" {
			t.Fatalf("roster changed after refused stop: %+v / %v", cfg.Members, readErr)
		}
		assertLegacyTreeUnchanged(t, legacyBefore, legacyRoot, namedRoot)
	})
}
