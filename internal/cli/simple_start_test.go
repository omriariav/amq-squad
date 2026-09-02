package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/avivsinai/agent-message-queue/launchapi"

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

// TestFilterSimpleStartRolesBySubsetSpawnsOnlyRequestedRoles is gh#758/t11
// slice B's role-filter seam: filtering must happen on the already-
// reconciled rows, not on team.Members before reconciliation runs, so an
// unselected but LIVE role is never misclassified as "removed from
// roster" -- it is simply outside this invocation's requested subset.
func TestFilterSimpleStartRolesBySubsetSpawnsOnlyRequestedRoles(t *testing.T) {
	tm := team.Team{Project: "/repo", Members: []team.Member{
		{Role: "cto", Handle: "cto", Binary: "codex"},
		{Role: "qa", Handle: "qa", Binary: "codex"},
	}}
	rows := []simpleStartRolePlan{
		{Member: team.Member{Role: "cto", Handle: "cto", Binary: "codex"}, State: "live", Detail: "recorded process or pane verified live; keeping"},
		{Member: team.Member{Role: "qa", Handle: "qa", Binary: "codex"}, State: "stopped", Detail: "recorded process and pane are not live; will respawn"},
	}
	filtered, err := filterSimpleStartRolesBySubset(tm, rows, []string{"qa"})
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Member.Role != "qa" {
		t.Fatalf("filtered = %+v, want exactly the qa row", filtered)
	}
}

func TestFilterSimpleStartRolesBySubsetRejectsUnknownRole(t *testing.T) {
	tm := team.Team{Project: "/repo", Members: []team.Member{{Role: "cto", Handle: "cto", Binary: "codex"}}}
	rows := []simpleStartRolePlan{{Member: team.Member{Role: "cto", Handle: "cto", Binary: "codex"}, State: "live"}}
	_, err := filterSimpleStartRolesBySubset(tm, rows, []string{"nonexistent"})
	if err == nil || !strings.Contains(err.Error(), "nonexistent") || !strings.Contains(err.Error(), "cto") {
		t.Fatalf("err = %v, want a --role error naming the unknown role and the team's actual roles", err)
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
	// gh#759/t13: start fails closed without a brief now (no more silent
	// stub fallback), so the shared fixture seeds one by default -- the one
	// test that specifically exercises the no-brief refusal
	// (TestStartWithoutBriefFailsClosedNamingBriefCommand) removes it
	// explicitly instead of building its own fixture.
	seedSimpleStartBrief(t, f)
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
	seedBriefAt(t, f.project, f.profile, f.session)
}

// seedBriefAt writes a minimal existing brief for (project, profile,
// session) directly, for fixtures that build their own team/AMQ-root setup
// by hand rather than through newSimpleStartRunFixture (gh#759/t13: start
// and resume --exec both fail closed without one now).
func seedBriefAt(t *testing.T, project, profile, session string) {
	t.Helper()
	path := squadnamespace.BriefPath(project, profile, session)
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
	if err := runStartWithDependencies(f.args("--launch-via", "legacy"), f.deps, strings.NewReader("n\n"), &out); err != nil {
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
	if err := runStartWithDependencies(f.args("--yes", "--launch-via", "legacy"), f.deps, strings.NewReader(""), &out); err != nil {
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
	err := runStartWithDependencies(f.args("--yes", "--launch-via", "legacy"), f.deps, strings.NewReader(""), &out)
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
	if err := runStartWithDependencies(f.args("--yes", "--launch-via", "legacy"), f.deps, strings.NewReader(""), &out); err != nil {
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
	if err := runStartWithDependencies(f.args("--yes", "--launch-via", "legacy"), f.deps, strings.NewReader(""), &out); err != nil {
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

// TestRunStartRejectsRestoreResultThatDropsRecordedConversation exercises
// the legacy composer's own drop-detection (validateSimpleStartRestoreResultCommands),
// so it opts into --launch-via legacy explicitly (gh#757): plain start now
// defaults to the launchapi path, which refuses this same scenario outright
// instead (see TestStartRefusesLegacyMintedRestoreOnLaunchapiPath) since it
// cannot resume a legacy-minted conversation at all yet.
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
	err = runStartWithDependencies(f.args("--yes", "--launch-via", "legacy"), f.deps, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "dispatched child command omits recorded conversation") {
		t.Fatalf("dropped-conversation start error = %v", err)
	}
	if strings.Contains(out.String(), "started ") {
		t.Fatalf("failed restore reported started:\n%s", out.String())
	}
}

// TestStartRefusesLegacyMintedRestoreOnLaunchapiPath is gh#757's named
// acceptance test for the conversation-restore gap found while wiring
// --apply: launchapi has no mechanism to resume a conversation minted by
// the legacy composer (confirmed on task/t7: the launchapi backend never
// writes launch.Record at all, so any recorded Conversation is legacy-
// minted by construction). start refuses closed rather than silently
// falling back to the legacy backend or silently dropping the conversation.
func TestStartRefusesLegacyMintedRestoreOnLaunchapiPath(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	agentDir := f.seedRecord(t, "dev", "dev", 4900, "%40", false, true)
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.Conversation = "conv-legacy-minted"
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	launchCalled := false
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		launchCalled = true
		return teamLaunchResult{}, fmt.Errorf("start must not call Launch when refused")
	}
	var out bytes.Buffer
	err = runStartWithDependencies(f.args("--yes"), f.deps, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "launchapi path cannot resume it yet") || !strings.Contains(err.Error(), "--launch-via legacy") {
		t.Fatalf("start with a legacy-minted restore on the launchapi path = %v, want the refusal naming --launch-via legacy", err)
	}
	if launchCalled {
		t.Fatal("start called Launch despite the refusal")
	}
	if strings.Contains(out.String(), "started ") {
		t.Fatalf("refused start reported started:\n%s", out.String())
	}
}

// simpleStartStubLaunchapiAMQEnv stubs resolveTeamLaunchAMQEnv -- the
// launchapi path's own AMQ env resolution seam (distinct from
// simpleStartDependencies.ResolveAMQEnv, which only feeds start's own
// reconciliation) -- so launchapiTeamLaunchBackend.prepare/launch can run
// end to end against a fixture project without a real amq binary.
func simpleStartStubLaunchapiAMQEnv(t *testing.T, root, session string) {
	t.Helper()
	original := resolveTeamLaunchAMQEnv
	resolveTeamLaunchAMQEnv = func(cwd, profile, sess, handle string) (amqEnv, error) {
		return amqEnv{AMQVersion: doctorMinAMQVersion, Root: root, BaseRoot: filepath.Dir(root), SessionName: session, Me: handle}, nil
	}
	t.Cleanup(func() { resolveTeamLaunchAMQEnv = original })
}

// TestStartApplyRejectsStaleSubjectDigest is gh#757's named acceptance test:
// start --apply <subject_digest> refuses closed when the supplied digest
// does not match a fresh Prepare's, mirroring amq's own
// TestPrepareIsZeroWriteAndApplyRejectsStaleSubject contract.
func TestStartApplyRejectsStaleSubjectDigest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	simpleStartStubLaunchapiAMQEnv(t, f.root, f.session)
	launchCalled := false
	f.deps.Launch = func(team.Team, teamLaunchOptions) (teamLaunchResult, error) {
		launchCalled = true
		return teamLaunchResult{}, fmt.Errorf("start must not call Launch on a stale digest")
	}
	var out bytes.Buffer
	err := runStartWithDependencies(f.args("--apply", "sha256:0000000000000000000000000000000000000000000000000000000000000"), f.deps, strings.NewReader(""), &out)
	if err == nil || !strings.Contains(err.Error(), "does not match the current plan's subject_digest") {
		t.Fatalf("start --apply with a stale digest = %v, want the stale-digest refusal", err)
	}
	if launchCalled {
		t.Fatal("start called Launch despite the stale digest")
	}
}

// TestStartApplyRequiresExactDecisionsForEveryRequiredAction is gh#757's
// named acceptance test for resolveLaunchapiDecisions -- the exact
// mechanism start --apply/--decision depends on and launchapiTeamLaunchBackend.launch
// already calls before ever reaching adoptionseam.Apply. Manufacturing a
// live RequiredActionV1 through a real Prepare call requires elaborate
// trust-store/conversation state this repo's other launchapi tests do not
// attempt either (TestLaunchapiBackendSurfacesRequiredActionsAsOperatorGates
// uses the same launchapiTestRequiredActions() fixture directly for exactly
// this reason) -- so this proves the missing/extra-decision contract
// directly against that fixture: every required action needs an exact
// decision, no partial application, before any roster mutation.
func TestStartApplyRequiresExactDecisionsForEveryRequiredAction(t *testing.T) {
	actions := launchapiTestRequiredActions()

	t.Run("missing decision for one action reports it, not an error", func(t *testing.T) {
		supplied := map[string]string{
			"a1": string(launchapi.DecisionTrustExactSubject),
			"a2": string(launchapi.DecisionFreshOnce),
			"a3": string(launchapi.DecisionCloseOld),
			// a4 deliberately omitted.
		}
		decisions, missing, err := resolveLaunchapiDecisions(actions, supplied)
		if err != nil {
			t.Fatalf("resolveLaunchapiDecisions: %v", err)
		}
		if len(decisions) != 3 {
			t.Fatalf("decisions = %+v, want exactly the 3 supplied", decisions)
		}
		if len(missing) != 1 || missing[0].ActionID != "a4" {
			t.Fatalf("missing = %+v, want exactly a4", missing)
		}
	})

	t.Run("an extra decision for an unknown action_id refuses before any roster mutation", func(t *testing.T) {
		supplied := map[string]string{"a1": string(launchapi.DecisionTrustExactSubject), "stale-action": "fresh_once"}
		if _, _, err := resolveLaunchapiDecisions(actions, supplied); err == nil || !strings.Contains(err.Error(), "stale answer") {
			t.Fatalf("resolveLaunchapiDecisions with an unknown action_id = %v, want a stale-answer refusal", err)
		}
	})

	t.Run("a choice outside the action's allowed set refuses", func(t *testing.T) {
		supplied := map[string]string{"a1": "close_old"} // a1 is RequiredActionTrustConfirmation; close_old is not in its allowed set.
		if _, _, err := resolveLaunchapiDecisions(actions, supplied); err == nil || !strings.Contains(err.Error(), "not in the allowed set") {
			t.Fatalf("resolveLaunchapiDecisions with a disallowed choice = %v, want an allowed-set refusal", err)
		}
	})

	t.Run("every action decided exactly returns all decisions and no missing", func(t *testing.T) {
		supplied := map[string]string{
			"a1": string(launchapi.DecisionTrustExactSubject), "a2": string(launchapi.DecisionFreshOnce),
			"a3": string(launchapi.DecisionCloseOld), "a4": string(launchapi.DecisionAcceptDegraded),
		}
		decisions, missing, err := resolveLaunchapiDecisions(actions, supplied)
		if err != nil {
			t.Fatalf("resolveLaunchapiDecisions: %v", err)
		}
		if len(missing) != 0 {
			t.Fatalf("missing = %+v, want none", missing)
		}
		if len(decisions) != len(actions) {
			t.Fatalf("decisions = %+v, want one per action (%d)", decisions, len(actions))
		}
	})
}

// flipOnFirstReadReader wraps an io.Reader and runs flip() exactly once,
// just before the first byte is ever read from it -- used to simulate an
// external state change (a role coming back live) happening while the
// operator is still at the confirmation prompt.
type flipOnFirstReadReader struct {
	r       io.Reader
	flip    func()
	flipped bool
}

func (f *flipOnFirstReadReader) Read(p []byte) (int, error) {
	if !f.flipped {
		f.flipped = true
		f.flip()
	}
	return f.r.Read(p)
}

// TestStartApplyRefusesWhenLivenessChangedSinceDigest is gh#757's named
// acceptance test for cto's ruling #3 (task/t8): the subject_digest binds
// only the roster reconciliation decided needed launching at print time.
// If liveness changes between the printed digest and the re-locked,
// re-reconciled roster deps.Launch actually receives -- here, a second
// role goes live while the operator is still answering the confirmation
// prompt -- the fresh Prepare inside launchapiTeamLaunchBackend.launch
// computes a different digest for the now-smaller roster and refuses,
// rather than silently applying against a roster that no longer matches
// what was shown.
func TestStartApplyRefusesWhenLivenessChangedSinceDigest(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	project := canonicalFilesystemPath(t.TempDir())
	chdir(t, project)
	const session = "work"
	root := squadnamespace.AMQRoot(project, team.DefaultProfile, session)
	members := []team.Member{
		{Role: "dev", Handle: "dev", Binary: "codex", Session: session},
		{Role: "ops", Handle: "ops", Binary: "codex", Session: session},
	}
	if err := team.Write(project, team.Team{Project: project, SharedCwdException: "digest race fixture", Members: members}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(project, ".amq-squad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".amq-squad", "team-rules.md"), []byte("test rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	briefPath := squadnamespace.BriefPath(project, team.DefaultProfile, session)
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, []byte("# existing reviewed brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	previousBackend, hadBackend := teamLaunchBackends["tmux"]
	teamLaunchBackends["tmux"] = &fakeBackend{}
	t.Cleanup(func() {
		if hadBackend {
			teamLaunchBackends["tmux"] = previousBackend
		} else {
			delete(teamLaunchBackends, "tmux")
		}
	})
	simpleStartStubLaunchapiAMQEnv(t, root, session)

	const opsPID = 5100
	alive := map[int]bool{opsPID: false}
	writeFixtureLaunchRecord := func(role, handle string, pid int, paneID string) {
		agentDir := filepath.Join(root, "agents", handle)
		rec := launch.Record{
			Schema: launch.SchemaVersion, CWD: project, TeamHome: project, TeamProfile: team.DefaultProfile,
			Root: root, BaseRoot: filepath.Dir(root), Session: session,
			Role: role, Handle: handle, Binary: "codex", Trust: trustModeSandboxed,
			ToolProfile: team.ToolProfileFull, AgentPID: pid, AgentTTY: "/dev/ttys-test", StartedAt: time.Unix(1000, 0).UTC(),
			Tmux: &launch.TmuxInfo{Session: "test", WindowID: "@1", PaneID: paneID, Target: "new-window"},
		}
		if err := launch.Write(agentDir, rec); err != nil {
			t.Fatal(err)
		}
	}
	// "ops" has a prior record so it classifies as stopped/live rather
	// than unmanaged (an unmanaged role has no record to go live from).
	writeFixtureLaunchRecord("ops", "ops", opsPID, "%51")

	deps := simpleStartDependencies{
		LookPath: func(name string) (string, error) { return "/test/bin/" + name, nil },
		ResolveAMQEnv: func(proj, r, sess, handle string) (amqEnv, error) {
			return amqEnv{AMQVersion: doctorMinAMQVersion, Root: r, BaseRoot: filepath.Dir(r), SessionName: sess, Me: handle}, nil
		},
		DuplicateProbe: duplicateLaunchProbe{
			PIDAlive:         func(pid int) bool { return alive[pid] },
			ProcessMatch:     func(int, func(string) bool) bool { return true },
			ProcessTTY:       func(pid int) (string, bool) { return "/dev/ttys-test", alive[pid] },
			ProcessStartTime: func(pid int) (time.Time, bool) { return time.Unix(1000, 0).UTC(), alive[pid] },
			Now:              func() time.Time { return time.Unix(1000, 0).UTC() },
		},
		RuntimeProbe: launchRuntimeProbe{
			PIDAlive:         func(pid int) bool { return alive[pid] },
			ProcessMatch:     func(int, func(string) bool) bool { return true },
			ProcessTTY:       func(pid int) (string, bool) { return "/dev/ttys-test", alive[pid] },
			ProcessStartTime: func(pid int) (time.Time, bool) { return time.Unix(1000, 0).UTC(), alive[pid] },
			PaneTitle:        func(string) (string, bool) { return "", false },
		},
		ListPanes:    func() ([]tmuxpane.TmuxPane, error) { return nil, nil },
		StartWatcher: func(team.Team, string, string, string) error { return nil },
	}
	// Deliberately leave deps.Launch unset: this test must exercise the
	// real simpleStartLaunch -> launchapiTeamLaunchBackend.launch path,
	// since the digest gate under test lives inside launch() itself (it
	// re-runs Prepare fresh and compares digests before ever reaching
	// adoptionseam.Apply). A stubbed Launch would bypass the gate entirely
	// and prove nothing.

	in := &flipOnFirstReadReader{
		r:    strings.NewReader("y\n"),
		flip: func() { alive[opsPID] = true },
	}
	var out bytes.Buffer
	err := runStartWithDependencies([]string{"--project", project, "--session", session, "--target", "new-window"}, deps, in, &out)
	if err == nil || !strings.Contains(err.Error(), "stale subject_digest") {
		t.Fatalf("start with liveness changed since the printed digest = %v, want a stale subject_digest refusal", err)
	}
}

// TestStartDeprecatedTrustFlagPrintsEquivalentDecision is gh#757's named,
// table-driven acceptance test: one subtest per flag that still exists on
// start today (--yes, --trust, --launchapi-decision, --force-duplicate,
// per cto's ruling on task/t8 -- --skip-lead-check and --rebind are not
// start flags at all, and --allow-fresh-fallback is unimplemented
// completion-list cruft, so none of the three get a redirect here) and
// has no effect on the resolved launchapi path. Each fires exactly one
// deprecation notice naming its equivalent, and --force-duplicate/--trust
// never leak through to opts (ForceDuplicate is always false, Trust plays
// no role in ExpectedSubjectDigest/LaunchapiDecisions).
func TestStartDeprecatedTrustFlagPrintsEquivalentDecision(t *testing.T) {
	cases := []struct {
		name   string
		flags  []string
		want   string
		notYet string // a substring that must NOT appear (other flags' notices)
	}{
		{
			name:  "--yes",
			flags: []string{"--yes"},
			want:  "deprecated: --yes has no effect on the launchapi path",
		},
		{
			name:  "--trust",
			flags: []string{"--trust", "trusted"},
			want:  "deprecated: --trust has no effect on the launchapi path",
		},
		{
			name:  "--launchapi-decision",
			flags: []string{"--launchapi-decision", "a1=fresh_once"},
			want:  "deprecated: --launchapi-decision is renamed --decision",
		},
		{
			name:  "--force-duplicate",
			flags: []string{"--force-duplicate"},
			want:  "deprecated: --force-duplicate has no effect on the launchapi path",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			req, err := parseSimpleStartRequest(append([]string{"--project", dir}, c.flags...))
			if err != nil {
				t.Fatalf("parseSimpleStartRequest: %v", err)
			}
			if !req.LaunchapiPath {
				t.Fatal("expected the default (tmux, no --launch-via) to resolve to the launchapi path")
			}
			found := false
			for _, notice := range req.DeprecatedFlagNotices {
				if strings.Contains(notice, c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("notices = %+v, want one containing %q", req.DeprecatedFlagNotices, c.want)
			}
			if req.Options.ForceDuplicate {
				t.Fatal("ForceDuplicate leaked through to opts despite the deprecation notice")
			}
		})
	}

	t.Run("--decision and --launchapi-decision merge without duplicate-notice noise", func(t *testing.T) {
		dir := t.TempDir()
		req, err := parseSimpleStartRequest([]string{"--project", dir, "--decision", "a1=fresh_once", "--launchapi-decision", "a2=close_old"})
		if err != nil {
			t.Fatalf("parseSimpleStartRequest: %v", err)
		}
		if req.Options.LaunchapiDecisions["a1"] != "fresh_once" || req.Options.LaunchapiDecisions["a2"] != "close_old" {
			t.Fatalf("LaunchapiDecisions = %+v, want both a1 and a2 merged", req.Options.LaunchapiDecisions)
		}
	})

	t.Run("--apply on the legacy path is a usage error, not a silent no-op", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := parseSimpleStartRequest([]string{"--project", dir, "--launch-via", "legacy", "--apply", "sha256:x"}); err == nil {
			t.Fatal("expected --apply combined with --launch-via legacy to be a usage error")
		}
	})
}

// TestStartHonorsLegacyOptOutForRestore proves the remedy in the refusal
// above actually works: --launch-via legacy still resumes a legacy-minted
// conversation exactly as before gh#757, byte-identical to
// TestRunStartWithDependenciesHoldsExactLockThroughSpawnVerification-style
// launches.
func TestStartHonorsLegacyOptOutForRestore(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	agentDir := f.seedRecord(t, "dev", "dev", 4901, "%41", false, true)
	rec, err := launch.Read(agentDir)
	if err != nil {
		t.Fatal(err)
	}
	rec.Conversation = "conv-legacy-minted"
	if err := launch.Write(agentDir, rec); err != nil {
		t.Fatal(err)
	}
	previousBackend, hadBackend := teamLaunchBackends["tmux"]
	teamLaunchBackends["tmux"] = &fakeBackend{}
	t.Cleanup(func() {
		if hadBackend {
			teamLaunchBackends["tmux"] = previousBackend
		} else {
			delete(teamLaunchBackends, "tmux")
		}
	})
	f.deps.Launch = func(_ team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
		if len(opts.ComposedPanes) != 1 || !strings.Contains(opts.ComposedPanes[0].Command, " --conversation conv-legacy-minted") {
			t.Fatalf("legacy-path composed panes = %+v", opts.ComposedPanes)
		}
		newAgentDir := f.seedRecord(t, "dev", "dev", 4902, "%42", true, true)
		newRec, err := launch.Read(newAgentDir)
		if err != nil {
			t.Fatal(err)
		}
		newRec.Conversation = "conv-legacy-minted"
		if err := launch.Write(newAgentDir, newRec); err != nil {
			t.Fatal(err)
		}
		return teamLaunchResult{Panes: []teamLaunchResultPane{{
			Role: "dev", PaneID: "%42", WindowID: "@2", ChildCommand: opts.ComposedPanes[0].Command,
		}}}, nil
	}
	var out bytes.Buffer
	if err := runStartWithDependencies(f.args("--yes", "--launch-via", "legacy"), f.deps, strings.NewReader(""), &out); err != nil {
		t.Fatalf("runStartWithDependencies with --launch-via legacy: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "started ") {
		t.Fatalf("legacy-path restore did not report started:\n%s", out.String())
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
		if err := runStartWithDependencies(f.args("--yes", "--goal", "ship it", "--launch-via", "legacy"), f.deps, strings.NewReader(""), &out); err != nil {
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
		return runStartWithDependencies(f.args("--yes", "--goal", "ship it", "--launch-via", "legacy"), f.deps, strings.NewReader(""), &out)
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

// gh#759/t13 commit 2: TestSimpleStartGoalDraftsReviewsAndStagesMissingBriefOnce,
// TestSimpleStartGoalRejectsBriefCreatedAfterReview,
// TestSimpleStartGoalFallbackPrintsPromptAndStopsBeforeMutation, and
// TestSimpleStartGoalRejectsInvalidDraftBeforeMutation all deleted. Every
// one exercised start's own inline brief-drafting (draftSimpleStartBrief
// called from buildSimpleStartPlan when no brief exists) and the
// ReviewedBrief round-trip that re-validated a drafted-then-confirmed
// document under the launch lock -- both deleted along with the "no
// brief -> draft or stub" branch itself (cto's ruling, task/t13: launch
// must never depend on an LLM succeeding). The properties they proved
// still exist, just on the new amq-squad brief command instead:
// TestBriefGoalDraftsAndWritesReviewedDocument,
// TestBriefGoalPrintsManualPromptAndRefusesWhenInSessionOnly, and
// TestBriefGoalRejectsInvalidDraftAndDoesNotWrite (brief_test.go) cover the
// draft-writes/manual-fallback/invalid-draft-rejection contracts
// respectively. TestSimpleStartGoalRejectsBriefCreatedAfterReview's own
// concurrent-brief-change race has no equivalent: brief writes are now a
// single O_EXCL-guarded call (writeSeedBriefForProfile), not a two-phase
// draft-then-relock sequence, so there is no window for the brief to
// change between a review and a commit anymore. start's OWN --goal
// coverage (post-launch delivery, unrelated to drafting) is unaffected and
// still lives in TestSimpleStartGoalIsLastAndNeverResentOnSpawnlessRerun/
// TestSimpleStartGoalFailureWarnsAfterSuccessfulLaunch above, both of which
// already seed their own brief via seedSimpleStartBrief.

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

// TestStartWithoutBriefFailsClosedNamingBriefCommand is gh#759/t13's named
// acceptance test for commit 2: start must never draft or stub a brief on
// its own -- with none present it fails closed and names the exact `brief`
// command to run instead.
func TestStartWithoutBriefFailsClosedNamingBriefCommand(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	briefPath := squadnamespace.BriefPath(f.project, f.profile, f.session)
	if err := os.Remove(briefPath); err != nil {
		t.Fatalf("remove seeded brief: %v", err)
	}

	_, err := buildSimpleStartPlan(simpleStartRequest{
		Project: f.project, Profile: f.profile, Session: f.session, SessionExplicit: true,
		Options: teamLaunchOptions{Terminal: "tmux", Target: "new-window"},
	}, f.deps)
	if err == nil {
		t.Fatal("start unexpectedly accepted a session with no brief")
	}
	want := fmt.Sprintf("start refused: no brief for session %q; run 'amq-squad brief --goal TEXT --session %s --project %s' (or --seed-from REF) first", f.session, f.session, f.project)
	if err.Error() != want {
		t.Fatalf("start-without-brief error = %q, want %q", err.Error(), want)
	}
}

// TestStartNeverInvokesDrafter is gh#759/t13's named acceptance test for
// commit 2: even when a brief already exists, start must never reach the
// drafter seam at all -- drafting is `brief`'s job now, not start's.
func TestStartNeverInvokesDrafter(t *testing.T) {
	f := newSimpleStartRunFixture(t, team.Member{Role: "dev", Handle: "dev", Binary: "codex"})
	f.deps.ResolveDrafter = func(*drafter.Config) (drafter.Resolution, error) {
		t.Fatal("start invoked ResolveDrafter; drafting must live only in 'amq-squad brief' now")
		return drafter.Resolution{}, nil
	}
	f.deps.RunDrafter = func(context.Context, *drafter.Config, drafter.Request) (drafter.Result, error) {
		t.Fatal("start invoked RunDrafter; drafting must live only in 'amq-squad brief' now")
		return drafter.Result{}, nil
	}

	if _, err := buildSimpleStartPlan(simpleStartRequest{
		Project: f.project, Profile: f.profile, Session: f.session, SessionExplicit: true,
		Options: teamLaunchOptions{Terminal: "tmux", Target: "new-window"},
	}, f.deps); err != nil {
		t.Fatalf("buildSimpleStartPlan: %v", err)
	}
}
