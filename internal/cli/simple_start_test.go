package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

func TestSimpleStartCommandPinsCanonicalInputsAndExactInstruction(t *testing.T) {
	prompt := "Read .amq-squad/team-rules.md and your brief at /repo/.amq-squad/briefs/work.md."
	command := emitTeamCommand(emitTeamCommandInput{
		CWD:           "/repo/wt",
		SquadBin:      "/bin/amq-squad",
		TeamHome:      "/repo",
		Member:        team.Member{Role: "dev", Handle: "dev", Binary: "codex"},
		NoBootstrap:   true,
		Workstream:    "work",
		TrustMode:     trustModeSandboxed,
		Profile:       team.DefaultProfile,
		SimpleStart:   true,
		CanonicalRoot: "/repo/.agent-mail/work",
		StartupPrompt: prompt,
	})
	for _, want := range []string{
		"--simple-start",
		"--root /repo/.agent-mail/work",
		"--team-profile default",
		"--no-bootstrap",
		prompt,
	} {
		if !strings.Contains(command, want) {
			t.Errorf("simple start command missing %q:\n%s", want, command)
		}
	}
	if strings.Count(command, prompt) != 1 {
		t.Fatalf("startup instruction must occur exactly once:\n%s", command)
	}
}

func TestParseSimpleStartRequestAcceptsOneSessionSurface(t *testing.T) {
	project := t.TempDir()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "flag", args: []string{"--project", project, "--session", "work"}},
		{name: "positional", args: []string{"--project", project, "work"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := parseSimpleStartRequest(tc.args)
			if err != nil {
				t.Fatalf("parseSimpleStartRequest: %v", err)
			}
			if req.Session != "work" || !req.SessionExplicit {
				t.Fatalf("session = %q explicit=%t, want work/true", req.Session, req.SessionExplicit)
			}
		})
	}
	if _, err := parseSimpleStartRequest([]string{"--project", project, "--session", "flag", "positional"}); err == nil {
		t.Fatal("flag and positional session together must be rejected")
	}
}

func TestBootstrapContextCanonicalizesRuntimeCoordinates(t *testing.T) {
	realProject := t.TempDir()
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(realProject, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root := filepath.Join(link, ".agent-mail", "work")
	agentDir := filepath.Join(root, "agents", "dev")
	ctx := bootstrapContextFor(launch.Record{
		Role: "dev", Handle: "dev", Binary: "codex", Session: "work",
		TeamHome: link, CWD: link, Root: root, BaseRoot: filepath.Dir(root),
	}, agentDir, link)
	wantProject := canonicalFilesystemPath(realProject)
	wantRoot := filepath.Join(wantProject, ".agent-mail", "work")
	wantAgentDir := filepath.Join(wantRoot, "agents", "dev")
	for name, gotWant := range map[string][2]string{
		"TeamHome":      {ctx.TeamHome, wantProject},
		"CWD":           {ctx.CWD, wantProject},
		"Root":          {ctx.Root, wantRoot},
		"AgentDir":      {ctx.AgentDir, wantAgentDir},
		"TeamRulesPath": {ctx.TeamRulesPath, filepath.Join(wantProject, ".amq-squad", "team-rules.md")},
		"BriefPath":     {ctx.BriefPath, filepath.Join(wantProject, ".amq-squad", "briefs", "work.md")},
		"LaunchPath":    {ctx.LaunchPath, launch.Path(wantAgentDir)},
	} {
		if gotWant[0] != gotWant[1] {
			t.Errorf("%s = %q, want %q", name, gotWant[0], gotWant[1])
		}
	}
}

func TestSelectStatusLaunchRecordRequiresExactHandleIdentity(t *testing.T) {
	tm := team.Team{Project: "/repo"}
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex"}
	entries := []launch.Entry{{
		AgentDir: "/repo/.agent-mail/work/agents/outsider",
		Record: launch.Record{
			TeamHome: "/repo", TeamProfile: team.DefaultProfile, Session: "work",
			Role: "dev", Handle: "outsider", Binary: "codex",
			Root: "/repo/.agent-mail/work",
		},
	}}
	selection := selectStatusLaunchRecord(tm, team.DefaultProfile, member, "work", duplicateLaunchProbe{}, entries)
	if selection.Found || len(selection.DuplicatePaths) != 0 {
		t.Fatalf("wrong-handle same-role record was selected: %+v", selection)
	}
	warnings := statusUnmanagedLaunchRecordWarningsFromEntries(team.Team{Project: "/repo", Members: []team.Member{member}}, team.DefaultProfile, "work", entries)
	if len(warnings) != 1 || warnings[0].Kind != "unmanaged_launch_record" ||
		!strings.Contains(warnings[0].Detail, "outsider") || !strings.Contains(warnings[0].Detail, launch.ExistingPath(entries[0].AgentDir)) {
		t.Fatalf("wrong-handle record was not surfaced as unmanaged: %+v", warnings)
	}
}

func TestReconcileSimpleStartRolesUsesRecordedRuntimeAndClassifiesDrift(t *testing.T) {
	tm := team.Team{
		Project: "/repo",
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex"},
			{Role: "dev", Handle: "dev", Binary: "codex"},
		},
	}
	started := time.Unix(100, 0).UTC()
	records := []simpleStartRecord{
		{AgentDir: "/root/agents/cto", Record: launch.Record{
			Schema: launch.SchemaVersion, CWD: "/repo", TeamHome: "/repo", TeamProfile: team.DefaultProfile,
			Root: "/root", Session: "work", Role: "cto", Handle: "cto", Binary: "codex",
			Trust: trustModeSandboxed, ToolProfile: team.ToolProfileFull, AgentPID: 41, StartedAt: started,
		}},
		{AgentDir: "/root/agents/dev", Record: launch.Record{
			Schema: launch.SchemaVersion, CWD: "/old-worktree", TeamHome: "/repo", TeamProfile: team.DefaultProfile,
			Root: "/root", Session: "work", Role: "dev", Handle: "dev", Binary: "codex",
			Trust: trustModeSandboxed, ToolProfile: team.ToolProfileFull, AgentPID: 42, StartedAt: started,
		}},
	}
	probe := launchRuntimeProbe{
		PIDAlive:         func(pid int) bool { return pid == 42 },
		ProcessMatch:     func(int, func(string) bool) bool { return true },
		ProcessTTY:       func(int) (string, bool) { return "", false },
		ProcessStartTime: func(int) (time.Time, bool) { return started, true },
	}
	rows, _, err := reconcileSimpleStartRoles(tm, team.DefaultProfile, "work", "/root", records, teamLaunchOptions{Trust: trustModeSandboxed}, probe)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, row := range rows {
		states[row.Member.Role] = row.State
	}
	if states["cto"] != "stopped" {
		t.Errorf("dead recorded cto state = %q, want stopped", states["cto"])
	}
	if states["dev"] != "live/config-diverged" {
		t.Errorf("live recorded dev state = %q, want live/config-diverged", states["dev"])
	}
}

func TestReconcileSimpleStartRolesRejectsDuplicateLive(t *testing.T) {
	tm := team.Team{Project: "/repo", Members: []team.Member{{Role: "dev", Handle: "dev", Binary: "codex"}}}
	makeRecord := func(agentDir string, pid int) simpleStartRecord {
		return simpleStartRecord{AgentDir: agentDir, Record: launch.Record{
			Schema: launch.SchemaVersion, CWD: "/repo", TeamHome: "/repo", TeamProfile: team.DefaultProfile,
			Root: "/root", Session: "work", Role: "dev", Handle: "dev", Binary: "codex",
			Trust: trustModeSandboxed, ToolProfile: team.ToolProfileFull, AgentPID: pid,
		}}
	}
	probe := launchRuntimeProbe{
		PIDAlive:     func(int) bool { return true },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		ProcessTTY:   func(int) (string, bool) { return "", false },
	}
	_, _, err := reconcileSimpleStartRoles(tm, team.DefaultProfile, "work", "/root", []simpleStartRecord{makeRecord("/root/agents/dev", 1), makeRecord("/root/agents/legacy-copy", 2)}, teamLaunchOptions{Trust: trustModeSandboxed}, probe)
	var conflict *simpleStartConflictError
	if !errors.As(err, &conflict) || conflict.Class != "duplicate_live" {
		t.Fatalf("error = %v, want duplicate_live conflict", err)
	}
}

func TestReconcileSimpleStartRolesTreatsSameRoleWrongHandleAsUnmanaged(t *testing.T) {
	tm := team.Team{Project: "/repo", Members: []team.Member{{Role: "dev", Handle: "dev-2", Binary: "codex"}}}
	records := []simpleStartRecord{{AgentDir: "/root/agents/dev-1", Record: launch.Record{
		Schema: launch.SchemaVersion, CWD: "/repo", TeamHome: "/repo", TeamProfile: team.DefaultProfile,
		Root: "/root", Session: "work", Role: "dev", Handle: "dev-1", Binary: "codex",
		Trust: trustModeSandboxed, ToolProfile: team.ToolProfileFull, AgentPID: 42,
	}}}
	probe := launchRuntimeProbe{
		PIDAlive:     func(int) bool { return true },
		ProcessMatch: func(int, func(string) bool) bool { return true },
		ProcessTTY:   func(int) (string, bool) { return "", false },
	}
	rows, removed, err := reconcileSimpleStartRoles(tm, team.DefaultProfile, "work", "/root", records, teamLaunchOptions{Trust: trustModeSandboxed}, probe)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].State != "unmanaged" {
		t.Fatalf("desired rows = %+v, want dev-2 unmanaged so it will spawn", rows)
	}
	if len(removed) != 1 || removed[0].State != "unmanaged" ||
		!strings.Contains(removed[0].Detail, "dev-1") || !strings.Contains(removed[0].Detail, launch.ExistingPath(records[0].AgentDir)) {
		t.Fatalf("foreign same-role record = %+v, want path-bearing unmanaged classification", removed)
	}
}

func TestClassifyRecordlessSimpleStartPaneIsUnmanagedConflict(t *testing.T) {
	plan := simpleStartPlan{
		Session: "work",
		Roles: []simpleStartRolePlan{{
			Member: team.Member{Role: "dev", Handle: "dev", Binary: "codex"},
			State:  "unmanaged",
		}},
	}
	err := classifyRecordlessSimpleStartPanes(plan, []tmuxpane.TmuxPane{{
		Session: "squad", Window: "0", Pane: "1", PaneID: "%17", Title: paneTitleToken("work", "dev"),
	}})
	var conflict *simpleStartConflictError
	if !errors.As(err, &conflict) || conflict.Class != "unmanaged" {
		t.Fatalf("error = %v, want unmanaged conflict", err)
	}
	if !strings.Contains(conflict.Detail, "dev") || !strings.Contains(conflict.Detail, "%17") {
		t.Fatalf("conflict detail = %q, want role and pane identity", conflict.Detail)
	}
}

func TestSimpleStartCheckpointWrapsSentinel(t *testing.T) {
	sentinel := errors.New("crash")
	err := callSimpleStartCheckpoint(func(got simpleStartCheckpoint) error {
		if got != simpleStartCheckpointPaneCreation {
			t.Fatalf("checkpoint = %q", got)
		}
		return sentinel
	}, simpleStartCheckpointPaneCreation)
	var checkpointErr *simpleStartCheckpointError
	if !errors.As(err, &checkpointErr) || !errors.Is(err, sentinel) {
		t.Fatalf("checkpoint error = %v", err)
	}
}

type simpleStartRunFixture struct {
	project string
	profile string
	session string
	root    string
	member  team.Member
	started time.Time
	alive   map[int]bool
	ttys    map[int]string
	titles  map[string]string
	deps    simpleStartDependencies
}

func newSimpleStartRunFixture(t *testing.T, member team.Member) *simpleStartRunFixture {
	t.Helper()
	project := canonicalFilesystemPath(t.TempDir())
	if member.Role == "" {
		member.Role = "dev"
	}
	if member.Handle == "" {
		member.Handle = member.Role
	}
	if member.Binary == "" {
		member.Binary = "codex"
	}
	member.Session = "work"
	if err := team.Write(project, team.Team{
		Project: project, SharedCwdException: "simple start dependency fixture",
		Members: []team.Member{member},
	}); err != nil {
		t.Fatal(err)
	}
	rulesPath := filepath.Join(project, ".amq-squad", "team-rules.md")
	if err := os.MkdirAll(filepath.Dir(rulesPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rulesPath, []byte("test rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	previousBackend, hadBackend := teamLaunchBackends["tmux"]
	teamLaunchBackends["tmux"] = &fakeBackend{}
	t.Cleanup(func() {
		if hadBackend {
			teamLaunchBackends["tmux"] = previousBackend
			return
		}
		delete(teamLaunchBackends, "tmux")
	})

	f := &simpleStartRunFixture{
		project: project,
		profile: team.DefaultProfile,
		session: "work",
		root:    squadnamespace.AMQRoot(project, team.DefaultProfile, "work"),
		member:  member,
		started: time.Unix(1_000, 0).UTC(),
		alive:   map[int]bool{},
		ttys:    map[int]string{},
		titles:  map[string]string{},
	}
	f.deps = simpleStartDependencies{
		LookPath: func(name string) (string, error) { return "/test/bin/" + name, nil },
		ResolveAMQEnv: func(project, root, session, handle string) (amqEnv, error) {
			if project != f.project || root != f.root || session != f.session || handle != memberHandle(f.member) {
				t.Fatalf("ResolveAMQEnv(%q, %q, %q, %q) did not receive canonical inputs", project, root, session, handle)
			}
			return amqEnv{AMQVersion: doctorMinAMQVersion, Root: root, BaseRoot: filepath.Dir(root), SessionName: session, Me: handle}, nil
		},
		DuplicateProbe: duplicateLaunchProbe{
			PIDAlive:         func(pid int) bool { return f.alive[pid] },
			ProcessMatch:     func(int, func(string) bool) bool { return true },
			ProcessTTY:       func(pid int) (string, bool) { tty, ok := f.ttys[pid]; return tty, ok },
			ProcessStartTime: func(pid int) (time.Time, bool) { return f.started, f.alive[pid] },
			Now:              func() time.Time { return f.started },
		},
		RuntimeProbe: launchRuntimeProbe{
			PIDAlive:         func(pid int) bool { return f.alive[pid] },
			ProcessMatch:     func(int, func(string) bool) bool { return true },
			ProcessTTY:       func(pid int) (string, bool) { tty, ok := f.ttys[pid]; return tty, ok },
			ProcessStartTime: func(pid int) (time.Time, bool) { return f.started, f.alive[pid] },
			PaneTitle:        func(paneID string) (string, bool) { title, ok := f.titles[paneID]; return title, ok },
		},
		ListPanes:    func() ([]tmuxpane.TmuxPane, error) { return nil, nil },
		StartWatcher: func(team.Team, string, string, string) error { return nil },
	}
	return f
}

func TestSimpleStartFailsClosedOnSharedImplementationCWD(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "cto", Handle: "cto", Binary: "codex"})
	if err := team.Write(f.project, team.Team{
		Project: f.project,
		Members: []team.Member{
			{Role: "cto", Handle: "cto", Binary: "codex", Session: f.session, ActorMode: team.ActorModeImplementation},
			{Role: "qa", Handle: "qa", Binary: "codex", Session: f.session, ActorMode: team.ActorModeImplementation},
		},
	}); err != nil {
		t.Fatal(err)
	}

	_, err := buildSimpleStartPlan(simpleStartRequest{
		Project: f.project, Profile: f.profile, Session: f.session, SessionExplicit: true,
		Options: teamLaunchOptions{Terminal: "tmux", Target: "new-window"},
	}, f.deps)
	if err == nil {
		t.Fatal("simple start unexpectedly accepted two implementation actors sharing one checkout")
	}
	if !strings.Contains(err.Error(), "worktree isolation blocked") || !strings.Contains(err.Error(), "cto") || !strings.Contains(err.Error(), "qa") {
		t.Fatalf("simple start isolation error = %q, want both colliding roles", err)
	}
}

func (f *simpleStartRunFixture) args(extra ...string) []string {
	args := []string{"--project", f.project, "--session", f.session, "--target", "new-window"}
	return append(args, extra...)
}

func (f *simpleStartRunFixture) seedRecord(t *testing.T, role, handle string, pid int, paneID string, alive, titled bool) string {
	t.Helper()
	agentDir := filepath.Join(f.root, "agents", handle)
	tty := "/dev/ttys-test"
	binary := "codex"
	launcher := ""
	if handle == memberHandle(f.member) {
		binary = f.member.Binary
		launcher = f.member.Launcher
	}
	rec := launch.Record{
		Schema: launch.SchemaVersion, CWD: f.project, TeamHome: f.project, TeamProfile: f.profile,
		Root: f.root, BaseRoot: filepath.Dir(f.root), Session: f.session,
		Role: role, Handle: handle, Binary: binary, Launcher: launcher, Trust: trustModeSandboxed,
		ToolProfile: team.ToolProfileFull, AgentPID: pid, AgentTTY: tty, StartedAt: f.started,
		Tmux: &launch.TmuxInfo{Session: "test", WindowID: "@1", PaneID: paneID, Target: "new-window"},
	}
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	f.alive[pid] = alive
	f.ttys[pid] = tty
	if titled {
		f.titles[paneID] = paneTitleToken(f.session, role)
	}
	return agentDir
}

func simpleStartLaunchResult(role, paneID string) teamLaunchResult {
	return teamLaunchResult{Panes: []teamLaunchResultPane{{Role: role, PaneID: paneID, WindowID: "@1"}}}
}

func TestRunStartWithDependenciesApprovalDefaultsNo(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	launchCalled := false
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		launchCalled = true
		return teamLaunchResult{}, nil
	}
	var out bytes.Buffer
	if err := runStartWithDependencies(f.args(), f.deps, strings.NewReader("n\n"), &out); err != nil {
		t.Fatal(err)
	}
	if launchCalled {
		t.Fatal("default-No approval launched the team")
	}
	if !strings.Contains(out.String(), "Launch now? [y/N]") || !strings.Contains(out.String(), "start cancelled") {
		t.Fatalf("default-No output missing prompt/cancellation:\n%s", out.String())
	}
	if _, err := os.Stat(simpleStartLockPath(f.project, f.profile, f.session)); !os.IsNotExist(err) {
		t.Fatalf("cancelled start created the launch lock: %v", err)
	}
}

func TestRunStartWithDependenciesHoldsExactLockThroughSpawnVerification(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	const (
		pid    = 4101
		paneID = "%1"
	)
	lockPath := simpleStartLockPath(f.project, f.profile, f.session)
	wantLockPath := filepath.Join(f.project, ".amq-squad", "locks", "default.work.launch.lock")
	if lockPath != wantLockPath {
		t.Fatalf("lock path = %q, want %q", lockPath, wantLockPath)
	}
	var events []string
	verified := false
	basePIDAlive := f.deps.RuntimeProbe.PIDAlive
	f.deps.RuntimeProbe.PIDAlive = func(got int) bool {
		live := basePIDAlive(got)
		if got == pid && live && !verified {
			verified = true
			events = append(events, "verify")
		}
		return live
	}
	f.deps.AfterCheckpoint = func(checkpoint simpleStartCheckpoint) error {
		events = append(events, string(checkpoint))
		if checkpoint == simpleStartCheckpointNamespaceCreation {
			lock, acquired, err := flock.TryExclusive(lockPath, false)
			if err != nil {
				t.Fatalf("probe held launch lock: %v", err)
			}
			if acquired {
				if lock != nil {
					_ = lock.Close()
				}
				t.Fatal("launch lock was not held at namespace checkpoint")
			}
		}
		if checkpoint == simpleStartCheckpointLaunchRecordWrite && !verified {
			t.Fatal("launch-record checkpoint ran before PID-backed verification")
		}
		return nil
	}
	f.deps.Launch = func(spawn team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
		if len(spawn.Members) != 1 || memberHandle(spawn.Members[0]) != "dev" {
			t.Fatalf("spawn roster = %+v", spawn.Members)
		}
		if err := callSimpleStartCheckpoint(opts.AfterCheckpoint, simpleStartCheckpointPaneCreation); err != nil {
			return teamLaunchResult{}, err
		}
		if err := callSimpleStartCheckpoint(opts.AfterCheckpoint, simpleStartCheckpointChildDispatch); err != nil {
			return teamLaunchResult{}, err
		}
		f.seedRecord(t, "dev", "dev", pid, paneID, true, true)
		return simpleStartLaunchResult("dev", paneID), nil
	}
	var out bytes.Buffer
	if err := runStartWithDependencies(f.args("--yes"), f.deps, strings.NewReader(""), &out); err != nil {
		t.Fatalf("runStartWithDependencies: %v\n%s", err, out.String())
	}
	wantEvents := []string{"namespace_creation", "pane_creation", "child_dispatch", "verify", "launch_record_write"}
	if strings.Join(events, ",") != strings.Join(wantEvents, ",") {
		t.Fatalf("event order = %v, want %v", events, wantEvents)
	}
	if !strings.Contains(out.String(), "started work") {
		t.Fatalf("successful --yes launch did not report started:\n%s", out.String())
	}
	lock, acquired, err := flock.TryExclusive(lockPath, false)
	if err != nil || !acquired {
		t.Fatalf("launch lock not released after success: acquired=%t err=%v", acquired, err)
	}
	if lock != nil {
		_ = lock.Close()
	}
}

func TestRunStartWithDependenciesRejectsDeadPIDWithSurvivingTitledPane(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		f.seedRecord(t, "dev", "dev", 4102, "%2", false, true)
		return simpleStartLaunchResult("dev", "%2"), nil
	}
	var out bytes.Buffer
	err := runStartWithDependencies(f.args("--yes"), f.deps, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "does not own the verified live child process") {
		t.Fatalf("dead child with titled pane error = %v", err)
	}
	if strings.Contains(out.String(), "started work") {
		t.Fatalf("dead child was reported started:\n%s", out.String())
	}
}

func TestRunStartWithDependenciesLauncherPIDImageIsAccepted(t *testing.T) {
	project := canonicalFilesystemPath(t.TempDir())
	launcher := filepath.Join(project, "codex-launcher")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex", Launcher: launcher})
	const (
		pid    = 4103
		paneID = "%6"
	)
	matchedLauncher := false
	f.deps.RuntimeProbe.ProcessMatch = func(gotPID int, predicate func(string) bool) bool {
		if gotPID != pid {
			return false
		}
		matchedLauncher = predicate(filepath.Base(launcher) + " --forward codex")
		return matchedLauncher
	}
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		f.seedRecord(t, "dev", "dev", pid, paneID, true, true)
		return simpleStartLaunchResult("dev", paneID), nil
	}
	var out bytes.Buffer
	if err := runStartWithDependencies(f.args("--yes"), f.deps, strings.NewReader(""), &out); err != nil {
		t.Fatalf("launcher-backed start failed: %v\n%s", err, out.String())
	}
	if !matchedLauncher {
		t.Fatal("honest ProcessMatch did not recognize the recorded launcher image")
	}
	if !strings.Contains(out.String(), "started work") {
		t.Fatalf("launcher-backed child was not reported started:\n%s", out.String())
	}
}

func TestRunStartWithDependenciesSpawnsConfiguredHandleBesideForeignSameRoleRecord(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev-2", Binary: "codex"})
	foreignDir := f.seedRecord(t, "dev", "dev-1", 4201, "%3", true, true)
	launchCalls := 0
	f.deps.Launch = func(spawn team.Team, _ teamLaunchOptions) (teamLaunchResult, error) {
		launchCalls++
		if len(spawn.Members) != 1 || memberHandle(spawn.Members[0]) != "dev-2" {
			t.Fatalf("foreign handle suppressed configured spawn: %+v", spawn.Members)
		}
		f.seedRecord(t, "dev", "dev-2", 4202, "%4", true, true)
		return simpleStartLaunchResult("dev", "%4"), nil
	}
	var out bytes.Buffer
	if err := runStartWithDependencies(f.args("--yes"), f.deps, strings.NewReader(""), &out); err != nil {
		t.Fatalf("runStartWithDependencies: %v\n%s", err, out.String())
	}
	if launchCalls != 1 {
		t.Fatalf("launch calls = %d, want 1", launchCalls)
	}
	for _, want := range []string{"unconfigured handle \"dev-1\"", launch.ExistingPath(foreignDir), "started work"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("start output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunStartWithDependenciesClassifiesUnmanagedInvalidAndRemovedRecords(t *testing.T) {
	t.Run("unmanaged default-No", func(t *testing.T) {
		f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
		var out bytes.Buffer
		if err := runStartWithDependencies(f.args(), f.deps, strings.NewReader("n\n"), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "unmanaged") || !strings.Contains(out.String(), "no launch record; will create") {
			t.Fatalf("missing unmanaged classification:\n%s", out.String())
		}
	})

	t.Run("record_invalid", func(t *testing.T) {
		f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
		agentDir := filepath.Join(f.root, "agents", "broken")
		path := launch.Path(agentDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
			t.Fatal(err)
		}
		err := runStartWithDependencies(f.args(), f.deps, strings.NewReader("n\n"), &bytes.Buffer{})
		var conflict *simpleStartConflictError
		if !errors.As(err, &conflict) || conflict.Class != "record_invalid" || !strings.Contains(conflict.Detail, path) {
			t.Fatalf("invalid record error = %v, want path-bearing record_invalid", err)
		}
	})

	t.Run("Removed", func(t *testing.T) {
		f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
		f.seedRecord(t, "ops", "ops", 4301, "%5", true, true)
		var out bytes.Buffer
		if err := runStartWithDependencies(f.args(), f.deps, strings.NewReader("n\n"), &out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), "ops") || !strings.Contains(out.String(), "removed from roster; live recorded runtime retained") {
			t.Fatalf("missing Removed classification:\n%s", out.String())
		}
	})
}

func TestSimpleStartRestoreComposesRecordedConversationWithoutBootstrap(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	agentDir := f.seedRecord(t, "dev", "dev", 4401, "%9", false, true)
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.Conversation = "conv-ac14"
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	plan, err := buildSimpleStartPlan(simpleStartRequest{
		Project: f.project, Profile: f.profile, Session: f.session, SessionExplicit: true,
		Options: teamLaunchOptions{Terminal: "tmux", Target: "new-window"},
	}, f.deps)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.LaunchOptions.ComposedPanes) != 1 {
		t.Fatalf("composed panes = %+v", plan.LaunchOptions.ComposedPanes)
	}
	backend, ok := teamLaunchBackends["tmux"].(*fakeBackend)
	if !ok {
		t.Fatalf("tmux test backend = %T, want fakeBackend", teamLaunchBackends["tmux"])
	}
	result, err := backend.LaunchWithResult(plan.SpawnTeam, plan.LaunchOptions)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Panes) != 1 {
		t.Fatalf("result panes = %+v", result.Panes)
	}
	command := result.Panes[0].ChildCommand
	for _, want := range []string{"--conversation conv-ac14", "--no-bootstrap"} {
		if !strings.Contains(command, want) {
			t.Fatalf("restore command %q missing %q", command, want)
		}
	}
	if strings.Contains(command, "Read .amq-squad/team-rules.md") {
		t.Fatalf("restore command replayed bootstrap: %s", command)
	}
	if err := validateSimpleStartRestoreResultCommands(plan, result); err != nil {
		t.Fatalf("valid result command rejected: %v", err)
	}
	result.Panes[0].ChildCommand = strings.ReplaceAll(command, " --conversation conv-ac14", "")
	if err := validateSimpleStartRestoreResultCommands(plan, result); err == nil || !strings.Contains(err.Error(), "dispatched child command omits recorded conversation") {
		t.Fatalf("missing-conversation validation = %v", err)
	}
}

func TestRunStartRejectsRestoreResultThatDropsRecordedConversation(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	agentDir := f.seedRecord(t, "dev", "dev", 4402, "%19", false, true)
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.Conversation = "conv-dropped"
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	f.deps.Launch = func(_ team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
		if len(opts.ComposedPanes) != 1 {
			t.Fatalf("composed panes = %+v", opts.ComposedPanes)
		}
		command := strings.ReplaceAll(opts.ComposedPanes[0].Command, " --conversation conv-dropped", "")
		return teamLaunchResult{Panes: []teamLaunchResultPane{{
			Role: "dev", PaneID: "%20", WindowID: "@2", ChildCommand: command,
		}}}, nil
	}
	var out bytes.Buffer
	err = runStartWithDependencies(f.args("--yes"), f.deps, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "dispatched child command omits recorded conversation") {
		t.Fatalf("dropped-conversation start error = %v", err)
	}
	if strings.Contains(out.String(), "started ") {
		t.Fatalf("failed restore reported started:\n%s", out.String())
	}
}

func TestSimpleStartGoalIsLastAndNeverResentOnSpawnlessRerun(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "cto", Handle: "cto", Binary: "codex"})
	configured, err := team.Read(f.project)
	if err != nil {
		t.Fatal(err)
	}
	configured.Orchestrated, configured.Lead = true, "cto"
	if err := team.Write(f.project, configured); err != nil {
		t.Fatal(err)
	}
	var events []string
	const (
		pid    = 4501
		paneID = "%10"
	)
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		events = append(events, "launch")
		f.seedRecord(t, "cto", "cto", pid, paneID, true, true)
		return simpleStartLaunchResult("cto", paneID), nil
	}
	f.deps.StartWatcher = func(team.Team, string, string, string) error {
		events = append(events, "notifier")
		return nil
	}
	goalSends := 0
	f.deps.DeliverGoal = func(plan simpleStartPlan, goal string) error {
		goalSends++
		events = append(events, "goal")
		if goal != "ship it" || !strings.HasPrefix(plan.Roles[0].State, "live") {
			t.Fatalf("goal delivery before verified live: goal=%q roles=%+v", goal, plan.Roles)
		}
		return nil
	}
	for i := 0; i < 2; i++ {
		var out bytes.Buffer
		if err := runStartWithDependencies(f.args("--yes", "--goal", "ship it"), f.deps, strings.NewReader(""), &out); err != nil {
			t.Fatalf("start %d: %v\n%s", i, err, out.String())
		}
	}
	if goalSends != 1 {
		t.Fatalf("goal sends = %d, want one across spawn + spawnless rerun", goalSends)
	}
	if got := strings.Join(events[:3], ","); got != "launch,notifier,goal" {
		t.Fatalf("first start order = %s, want launch,notifier,goal", got)
	}
}

func TestSimpleStartGoalFailureWarnsAfterSuccessfulLaunch(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "cto", Handle: "cto", Binary: "codex"})
	configured, err := team.Read(f.project)
	if err != nil {
		t.Fatal(err)
	}
	configured.Orchestrated, configured.Lead = true, "cto"
	if err := team.Write(f.project, configured); err != nil {
		t.Fatal(err)
	}
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		f.seedRecord(t, "cto", "cto", 4502, "%11", true, true)
		return simpleStartLaunchResult("cto", "%11"), nil
	}
	f.deps.DeliverGoal = func(simpleStartPlan, string) error {
		return errors.New("goal mailbox unavailable")
	}
	var out bytes.Buffer
	_, stderr, err := captureOutput(t, func() error {
		return runStartWithDependencies(f.args("--yes", "--goal", "ship it"), f.deps, strings.NewReader(""), &out)
	})
	if err != nil {
		t.Fatalf("start returned goal-delivery failure after launch: %v", err)
	}
	if !strings.Contains(stderr, "WARNING: all agents are live") || !strings.Contains(stderr, "goal mailbox unavailable") {
		t.Fatalf("stderr missing loud goal warning: %q", stderr)
	}
	if !strings.Contains(out.String(), "started ") {
		t.Fatalf("successful launch was not reported: %s", out.String())
	}
}

func TestReadSimpleStartRecordsRejectsMismatchedCanonicalCoordinates(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	agentDir := f.seedRecord(t, "dev", "dev", 4601, "%11", false, true)
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.Root = filepath.Join(f.project, ".agent-mail", "other")
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	_, err = readSimpleStartRecords(f.project, f.root, f.profile, f.session)
	var conflict *simpleStartConflictError
	if !errors.As(err, &conflict) || conflict.Class != "record_invalid" {
		t.Fatalf("mismatched root = %v, want record_invalid", err)
	}
}

func TestVerifySimpleStartRecordsPollsForRecordPublication(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	const (
		pid    = 4701
		paneID = "%12"
	)
	f.alive[pid] = true
	sleeps := 0
	f.deps.Sleep = func(time.Duration) {
		sleeps++
		if sleeps == 1 {
			f.seedRecord(t, "dev", "dev", pid, paneID, true, true)
		}
	}
	plan := simpleStartPlan{Root: f.root, SpawnTeam: team.Team{Project: f.project, Members: []team.Member{f.member}}}
	if err := verifySimpleStartRecords(plan, simpleStartLaunchResult("dev", paneID), normalizeSimpleStartDependencies(f.deps)); err != nil {
		t.Fatal(err)
	}
	if sleeps != 1 {
		t.Fatalf("poll sleeps = %d, want one publication wait", sleeps)
	}
}
