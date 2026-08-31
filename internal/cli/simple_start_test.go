package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/drafter"
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

func seedSimpleStartBrief(t *testing.T, f *simpleStartRunFixture) {
	t.Helper()
	path := squadnamespace.BriefPath(f.project, f.profile, f.session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# existing reviewed brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
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

func TestSimpleStartNewSessionAcceptsOwnedLivePaneAfterTitleRewrite(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	const (
		terminalSession = "owned-session"
		paneID          = "%281"
	)
	agentDir := f.seedRecord(t, "dev", "dev", 4281, paneID, true, false)
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.Tmux.Session = terminalSession
	rec.Tmux.Target = "new-session"
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	f.titles[paneID] = "codex: working"
	f.deps.RuntimeProbe.PaneTTY = func(got string) (string, bool) {
		if got != paneID {
			t.Fatalf("PaneTTY(%q), want %q", got, paneID)
		}
		return "/dev/ttys-test", true
	}

	oldExists, oldOutput := tmuxSessionExists, tmuxOutputCommand
	tmuxSessionExists = func(got string) bool { return got == terminalSession }
	tmuxOutputCommand = func(_ string, args ...string) (string, error) {
		joined := strings.Join(args, " ")
		if !strings.Contains(joined, "list-panes -s -t "+terminalSession) {
			t.Fatalf("tmux list-panes args = %v, want all panes in target %s", args, terminalSession)
		}
		return paneID + "\tcodex: working\t\n", nil
	}
	t.Cleanup(func() {
		tmuxSessionExists = oldExists
		tmuxOutputCommand = oldOutput
	})

	plan, err := buildSimpleStartPlan(simpleStartRequest{
		Project: f.project, Profile: f.profile, Session: f.session, SessionExplicit: true,
		Options: teamLaunchOptions{Terminal: "tmux", Target: "new-session", TerminalSession: terminalSession},
	}, f.deps)
	if err != nil {
		t.Fatalf("owned live target rejected after pane title rewrite: %v", err)
	}
	if len(plan.Roles) != 1 || !strings.HasPrefix(plan.Roles[0].State, "live") || len(plan.SpawnTeam.Members) != 0 {
		t.Fatalf("plan roles=%+v spawn=%+v, want one retained live role and no spawn", plan.Roles, plan.SpawnTeam.Members)
	}
}

func TestValidateSimpleStartTmuxTargetRequiresCorroboratedRecordInTargetSession(t *testing.T) {
	const (
		terminalSession = "owned-session"
		paneID          = "%282"
	)
	started := time.Unix(2_000, 0).UTC()
	record := simpleStartRecord{AgentDir: "/root/agents/dev", Record: launch.Record{
		Schema: launch.SchemaVersion, Session: "work", Role: "dev", Handle: "dev", Binary: "codex",
		AgentPID: 4282, AgentTTY: "/dev/ttys-test", StartedAt: started,
		Tmux: &launch.TmuxInfo{Session: terminalSession, PaneID: paneID, Target: "new-session"},
	}}
	probe := launchRuntimeProbe{
		PIDAlive:         func(int) bool { return true },
		ProcessMatch:     func(int, func(string) bool) bool { return true },
		ProcessTTY:       func(int) (string, bool) { return "/dev/ttys-test", true },
		ProcessStartTime: func(int) (time.Time, bool) { return started, true },
		PaneTitle:        func(string) (string, bool) { return "codex: working", true },
		PaneTTY:          func(string) (string, bool) { return "/dev/ttys-test", true },
	}

	oldExists, oldOutput := tmuxSessionExists, tmuxOutputCommand
	tmuxSessionExists = func(string) bool { return true }
	var listOutput string
	tmuxOutputCommand = func(string, ...string) (string, error) { return listOutput, nil }
	t.Cleanup(func() {
		tmuxSessionExists = oldExists
		tmuxOutputCommand = oldOutput
	})

	tests := []struct {
		name   string
		output string
		mutate func(*launch.Record, *launchRuntimeProbe)
	}{
		{name: "recorded pane absent from target", output: "%999\tcodex: working\t\n"},
		{name: "record names another session", output: paneID + "\tcodex: working\t\n", mutate: func(rec *launch.Record, _ *launchRuntimeProbe) {
			rec.Tmux.Session = "other-session"
		}},
		{name: "recorded process is dead", output: paneID + "\tcodex: working\t\n", mutate: func(_ *launch.Record, got *launchRuntimeProbe) {
			got.PIDAlive = func(int) bool { return false }
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotRecord, gotProbe := record, probe
			gotRecord.Record.Tmux = &launch.TmuxInfo{
				Session: record.Record.Tmux.Session, PaneID: record.Record.Tmux.PaneID, Target: record.Record.Tmux.Target,
			}
			if tc.mutate != nil {
				tc.mutate(&gotRecord.Record, &gotProbe)
			}
			listOutput = tc.output
			err := validateSimpleStartTmuxTarget(
				teamLaunchOptions{Target: "new-session", TerminalSession: terminalSession},
				"work", []simpleStartRecord{gotRecord}, gotProbe,
			)
			var conflict *simpleStartConflictError
			if !errors.As(err, &conflict) || conflict.Class != "unmanaged" {
				t.Fatalf("error = %v, want unmanaged conflict", err)
			}
		})
	}

	listOutput = "%999\tcodex: working\tamq:work:dev\n"
	if err := validateSimpleStartTmuxTarget(
		teamLaunchOptions{Target: "new-session", TerminalSession: terminalSession}, "work", nil, probe,
	); err != nil {
		t.Fatalf("durable pane-title fallback rejected: %v", err)
	}
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
	seedSimpleStartBrief(t, f)
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
	seedSimpleStartBrief(t, f)
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

func TestSimpleStartGoalDraftsReviewsAndStagesMissingBriefOnce(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	document := validSimpleStartBriefDraft(f.session, "ship it", f.member)
	draftCalls := 0
	f.deps.ResolveDrafter = func(*drafter.Config) (drafter.Resolution, error) {
		return drafter.Resolution{Config: &drafter.Config{Chain: []string{drafter.BackendYoetz, drafter.BackendClaude}}, Source: drafter.SourceGlobal}, nil
	}
	f.deps.RunDrafter = func(_ context.Context, cfg *drafter.Config, request drafter.Request) (drafter.Result, error) {
		draftCalls++
		if cfg == nil || fmt.Sprint(cfg.EffectiveBackends()) != fmt.Sprint([]string{drafter.BackendYoetz, drafter.BackendClaude}) {
			t.Fatalf("resolved drafter = %+v", cfg)
		}
		for _, want := range []string{"# work brief", "ship it", "## Team shape", "`dev` (`dev`, `codex`)"} {
			if !strings.Contains(request.Prompt, want) {
				t.Fatalf("brief prompt missing %q:\n%s", want, request.Prompt)
			}
		}
		attempts := []drafter.Evidence{
			{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask", ExitCode: 17, Failure: "missing credentials"},
			{Backend: drafter.BackendClaude, CommandDisplay: "claude -p", ExitCode: 0},
		}
		return drafter.Result{Text: document, Evidence: attempts[1], Attempts: attempts}, nil
	}
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		f.seedRecord(t, "dev", "dev", 4510, "%30", true, true)
		return simpleStartLaunchResult("dev", "%30"), nil
	}
	f.deps.DeliverGoal = func(simpleStartPlan, string) error { return nil }
	var out bytes.Buffer
	if err := runStartWithDependencies(f.args("--yes", "--goal", "ship it"), f.deps, strings.NewReader(""), &out); err != nil {
		t.Fatalf("start with drafted brief: %v\n%s", err, out.String())
	}
	if draftCalls != 1 {
		t.Fatalf("drafter calls = %d, want one reviewed draft reused across the locked recheck", draftCalls)
	}
	for _, want := range []string{
		"drafter config source: global", "drafter attempt (yoetz): yoetz ask", "fall-through: missing credentials",
		"drafter attempt (claude): claude -p", "Proposed workstream brief (review before launch):", document, "started work",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("start output missing %q:\n%s", want, out.String())
		}
	}
	path := squadnamespace.BriefPath(f.project, f.profile, f.session)
	got, err := os.ReadFile(path)
	if err != nil || string(got) != document {
		t.Fatalf("staged brief = %q, %v; want reviewed document", got, err)
	}
}

func TestSimpleStartGoalRejectsBriefCreatedAfterReview(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	document := validSimpleStartBriefDraft(f.session, "ship it", f.member)
	reviewed := &simpleStartBriefDraft{Document: []byte(document)}
	path := squadnamespace.BriefPath(f.project, f.profile, f.session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# concurrently changed brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := buildSimpleStartPlan(simpleStartRequest{
		Project: f.project, Profile: f.profile, Session: f.session, SessionExplicit: true,
		Goal: "ship it", ReviewedBrief: reviewed,
		Options: teamLaunchOptions{Terminal: "tmux", Target: "new-window"},
	}, f.deps)
	if err == nil || !strings.Contains(err.Error(), "brief changed after review") {
		t.Fatalf("concurrent brief change error = %v", err)
	}
}

func TestSimpleStartGoalFallbackPrintsPromptAndStopsBeforeMutation(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	launchCalled := false
	f.deps.LookPath = func(string) (string, error) { return "", errors.New("headless: runtime binary unavailable") }
	f.deps.ResolveDrafter = func(*drafter.Config) (drafter.Resolution, error) {
		return drafter.Resolution{Source: drafter.SourceInSession}, nil
	}
	f.deps.RunDrafter = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		return drafter.Result{
			UseInSession: true, Reason: "no external drafter is configured", Remedy: "complete the filled prompt in session",
			Evidence: drafter.Evidence{Backend: drafter.BackendInSession, ExitCode: 0},
		}, nil
	}
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		launchCalled = true
		return teamLaunchResult{}, nil
	}
	var out bytes.Buffer
	if err := runStartWithDependencies(f.args("--yes", "--goal", "ship it"), f.deps, strings.NewReader(""), &out); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"No brief was staged.", "Manual drafting prompt:", "# work brief", "start stopped before mutation"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("manual fallback output missing %q:\n%s", want, out.String())
		}
	}
	if launchCalled {
		t.Fatal("manual brief fallback launched the team")
	}
	if _, err := os.Stat(squadnamespace.BriefPath(f.project, f.profile, f.session)); !os.IsNotExist(err) {
		t.Fatalf("manual fallback staged a brief: %v", err)
	}
	if _, err := os.Stat(simpleStartLockPath(f.project, f.profile, f.session)); !os.IsNotExist(err) {
		t.Fatalf("manual fallback created a launch lock: %v", err)
	}
}

func TestSimpleStartGoalRejectsInvalidDraftBeforeMutation(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	launchCalled := false
	f.deps.ResolveDrafter = func(*drafter.Config) (drafter.Resolution, error) {
		return drafter.Resolution{Config: &drafter.Config{Backend: drafter.BackendClaude}, Source: drafter.SourceProfile}, nil
	}
	f.deps.RunDrafter = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		attempts := []drafter.Evidence{
			{Backend: drafter.BackendYoetz, CommandDisplay: "yoetz ask", ExitCode: 17, Failure: "missing credentials"},
			{Backend: drafter.BackendClaude, CommandDisplay: "claude -p", ExitCode: 0},
		}
		return drafter.Result{Text: "# work brief\n\n## Goal\nship it\n", Evidence: attempts[1], Attempts: attempts}, nil
	}
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		launchCalled = true
		return teamLaunchResult{}, nil
	}
	err := runStartWithDependencies(f.args("--yes", "--goal", "ship it"), f.deps, strings.NewReader(""), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), `missing heading "## Source"`) || !strings.Contains(err.Error(), "no brief was staged") {
		t.Fatalf("invalid brief error = %v", err)
	}
	for _, want := range []string{
		"drafter config source: profile",
		"attempt[1] backend=yoetz", `command="yoetz ask"`, `fall-through="missing credentials"`,
		"attempt[2] backend=claude", `command="claude -p"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("invalid brief error missing %q: %v", want, err)
		}
	}
	if launchCalled {
		t.Fatal("invalid brief launched the team")
	}
	if _, statErr := os.Stat(squadnamespace.BriefPath(f.project, f.profile, f.session)); !os.IsNotExist(statErr) {
		t.Fatalf("invalid draft staged a brief: %v", statErr)
	}
}

func TestValidateSimpleStartBriefDraftExactShape(t *testing.T) {
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex"}
	valid := validSimpleStartBriefDraft("work", "ship it", member)
	if got, err := validateSimpleStartBriefDraft(valid, "work", "ship it", []team.Member{member}); err != nil || got != valid {
		t.Fatalf("valid brief = %q, %v", got, err)
	}
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "changed goal", body: strings.Replace(valid, "ship it", "ship something else", 1), want: "exact operator goal"},
		{name: "missing source provenance", body: strings.Replace(valid, "the operator goal through the configured drafter", "a template", 1), want: "operator goal and configured drafter"},
		{name: "extra heading", body: strings.Replace(valid, "## Acceptance", "## Risks\n- hidden\n\n## Acceptance", 1), want: "unexpected level-two heading"},
		{name: "changed tuple", body: strings.Replace(valid, "`dev` (`dev`, `codex`)", "`dev` (`other`, `codex`)", 1), want: "missing or changed role"},
		{name: "prose scope", body: strings.Replace(valid, "- Implement", "Implement", 1), want: "only Markdown bullets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateSimpleStartBriefDraft(tt.body, "work", "ship it", []team.Member{member})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("validation error = %v, want %q", err, tt.want)
			}
		})
	}
}

// TestAllMarkdownBulletsAcceptsAsteriskAndPlusMarkers proves gh#760's fix:
// a drafter that emits valid CommonMark "* " or "+ " bullets (e.g. the
// gemini-2.5-flash repro from the issue's 2026-08-30 comment) instead of
// "- " is no longer rejected, in both allMarkdownBullets' generic
// bullet-only sections and validateSimpleStartTeamShape's exact-prefix
// roster match -- the same nonEmptyTrimmedLines normalization feeds both.
func TestAllMarkdownBulletsAcceptsAsteriskAndPlusMarkers(t *testing.T) {
	member := team.Member{Role: "dev", Handle: "dev", Binary: "codex"}
	valid := validSimpleStartBriefDraft("work", "ship it", member)
	body := valid
	body = strings.Replace(body, "- Implement the reviewed change.", "* Implement the reviewed change.", 1)
	body = strings.Replace(body, "- Do not release or send externally.", "+ Do not release or send externally.", 1)
	body = strings.Replace(body, "- Focused and full validation pass.", "* Focused and full validation pass.", 1)
	body = strings.Replace(body, "- `dev` (`dev`, `codex`)", "* `dev` (`dev`, `codex`)", 1)
	if body == valid {
		t.Fatal("test setup did not actually swap any bullet markers")
	}
	got, err := validateSimpleStartBriefDraft(body, "work", "ship it", []team.Member{member})
	if err != nil {
		t.Fatalf("validateSimpleStartBriefDraft with */+ bullets: %v", err)
	}
	if got != body {
		t.Fatalf("validateSimpleStartBriefDraft returned a rewritten document; want the reviewed input returned verbatim")
	}
}

// TestNormalizeMarkdownBulletMarkerLeavesNonBulletProseUntouched proves the
// gh#760 normalization only rewrites an exact "* "/"+ " leading marker, so
// the Goal and Source sections (never bullet-constrained, and not always
// starting with a bullet at all) are byte-identical afterward -- including
// prose that starts with a marker-like character with no following space.
func TestNormalizeMarkdownBulletMarkerLeavesNonBulletProseUntouched(t *testing.T) {
	for _, line := range []string{
		"ship it",
		"Generated from the operator goal through the configured drafter.",
		"**bold** starts the line",
		"+1 to this idea",
		"*emphasis* at the start",
	} {
		if got := normalizeMarkdownBulletMarker(line); got != line {
			t.Fatalf("normalizeMarkdownBulletMarker(%q) = %q, want unchanged", line, got)
		}
	}
	if got := normalizeMarkdownBulletMarker("* bullet"); got != "- bullet" {
		t.Fatalf("normalizeMarkdownBulletMarker(asterisk marker) = %q", got)
	}
	if got := normalizeMarkdownBulletMarker("+ bullet"); got != "- bullet" {
		t.Fatalf("normalizeMarkdownBulletMarker(plus marker) = %q", got)
	}
}

func TestEnsureSimpleStartBriefPublishesWithoutOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "briefs", "work.md")
	const reviewed = "# reviewed brief\n"
	if err := ensureSimpleStartBrief(path, []byte(reviewed)); err != nil {
		t.Fatal(err)
	}
	if err := ensureSimpleStartBrief(path, []byte(reviewed)); err != nil {
		t.Fatalf("identical staged brief should be idempotent: %v", err)
	}
	err := ensureSimpleStartBrief(path, []byte("# changed brief\n"))
	if err == nil || !strings.Contains(err.Error(), "brief changed before staging") {
		t.Fatalf("changed concurrent brief error = %v", err)
	}
	got, readErr := os.ReadFile(path)
	if readErr != nil || string(got) != reviewed {
		t.Fatalf("existing reviewed brief changed: body=%q err=%v", got, readErr)
	}
}

func validSimpleStartBriefDraft(session, goal string, members ...team.Member) string {
	var roster strings.Builder
	for _, member := range members {
		fmt.Fprintf(&roster, "- `%s` (`%s`, `%s`): Own the scoped implementation.\n", member.Role, memberHandle(member), member.Binary)
	}
	return "# " + session + " brief\n\n" +
		"## Goal\n" + goal + "\n\n" +
		"## Source\nGenerated from the operator goal through the configured drafter.\n\n" +
		"## Scope\n- Implement the reviewed change.\n\n" +
		"## Out of scope\n- Do not release or send externally.\n\n" +
		"## Team shape\n" + roster.String() + "\n" +
		"## Acceptance\n- Focused and full validation pass.\n"
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

// TestRunStartDefaultsToLaunchapiOnTmux is gh#757's named acceptance test
// closing the gh#755 gap identified during t1/t6: simpleStartLaunch (start's
// production Launch dependency) previously resolved its backend via a bare
// teamLaunchBackends[opts.Terminal] lookup, bypassing resolveTeamLaunchBackend
// entirely, so plain `amq-squad start` never selected launchapi on tmux
// regardless of gh#755's default flip. It now routes through the same
// selection seam executeTeamLaunch (team launch/up) already uses.
func TestRunStartDefaultsToLaunchapiOnTmux(t *testing.T) {
	legacyFake := &fakeBackend{}
	launchapiFake := &fakeBackend{}
	prevTmux, hadTmux := teamLaunchBackends["tmux"]
	prevLaunchapi, hadLaunchapi := teamLaunchBackends["launchapi"]
	teamLaunchBackends["tmux"] = legacyFake
	teamLaunchBackends["launchapi"] = launchapiFake
	t.Cleanup(func() {
		if hadTmux {
			teamLaunchBackends["tmux"] = prevTmux
		} else {
			delete(teamLaunchBackends, "tmux")
		}
		if hadLaunchapi {
			teamLaunchBackends["launchapi"] = prevLaunchapi
		} else {
			delete(teamLaunchBackends, "launchapi")
		}
	})

	member := team.Team{Members: []team.Member{{Role: "dev", Handle: "dev", Binary: "codex"}}}

	for _, launchVia := range []string{"", "auto", "launchapi"} {
		legacyFake.launches, launchapiFake.launches = nil, nil
		if _, err := simpleStartLaunch(member, teamLaunchOptions{Terminal: "tmux", LaunchVia: launchVia}); err != nil {
			t.Fatalf("LaunchVia=%q: %v", launchVia, err)
		}
		if len(launchapiFake.launches) != 1 || len(legacyFake.launches) != 0 {
			t.Fatalf("LaunchVia=%q: launchapi launches=%d legacy launches=%d, want launchapi selected by default", launchVia, len(launchapiFake.launches), len(legacyFake.launches))
		}
	}

	legacyFake.launches, launchapiFake.launches = nil, nil
	if _, err := simpleStartLaunch(member, teamLaunchOptions{Terminal: "tmux", LaunchVia: "legacy"}); err != nil {
		t.Fatalf("LaunchVia=legacy: %v", err)
	}
	if len(legacyFake.launches) != 1 || len(launchapiFake.launches) != 0 {
		t.Fatalf("LaunchVia=legacy: legacy launches=%d launchapi launches=%d, want the explicit opt-out to select legacy", len(legacyFake.launches), len(launchapiFake.launches))
	}
}
