// AC9 goal-delivery bodies, written against the step-3 seam
// simpleStartDependencies.DeliverGoal (simple_start.go, called after every role
// verifies live and only when this start actually spawned something).
//
// That seam does not exist on this branch's base (63b9161, step 2); it arrives
// with step 3. The file is therefore gated behind the v228step3 build tag so the
// base stays green.
//
// P3 INTEGRATION STEP: delete the //go:build line above. Nothing else changes.
//
//	go test ./internal/cli/ -tags v228step3 -run TestV228   # pre-merge: does not compile, by design
package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	"github.com/omriariav/amq-squad/v2/internal/rules"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// v228SeedOrchestratedProfile writes a profile with an orchestration lead, which
// is what deliverSimpleStartGoal requires to resolve a goal recipient.
func v228SeedOrchestratedProfile(t *testing.T, projectDir, profile, session, lead string, members []team.Member) {
	t.Helper()
	if err := team.WriteProfile(projectDir, profile, team.Team{
		Project:            projectDir,
		Workstream:         session,
		Members:            members,
		Orchestrated:       true,
		Lead:               lead,
		SharedCwdException: "v2.28 contract fixture: not exercising #497 worktree isolation",
	}); err != nil {
		t.Fatal(err)
	}
}

// v228GoalFixture is an orchestrated project ready for `start --goal`.
type v228GoalFixture struct {
	Project string
	Profile string
	Session string
	Root    string
	Lead    string
	Roles   []string
}

func v228NewGoalFixture(t *testing.T, session, lead string, roles []string) v228GoalFixture {
	t.Helper()
	project := canonicalFilesystemPath(t.TempDir())
	const profile = "v228"
	v228SeedOrchestratedProfile(t, project, profile, session, lead, v228StartMembers(session, roles...))
	if err := os.MkdirAll(filepath.Dir(rules.Path(project)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rules.Path(project), []byte("# team rules\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	briefPath := briefPathForProfile(project, profile, session)
	if err := os.MkdirAll(filepath.Dir(briefPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(briefPath, []byte("# existing reviewed brief\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return v228GoalFixture{
		Project: project, Profile: profile, Session: session,
		Root: v228CanonicalRoot(project, profile, session), Lead: lead, Roles: roles,
	}
}

// v228GoalRun records what one `start` did, in order. Ordering is the whole
// point of AC9: the goal is the last act of a successful launch.
type v228GoalRun struct {
	Err          error
	Output       string
	Events       []string // "spawn:<role>", then "goal:<text>"
	GoalPlans    []simpleStartPlan
	GoalMessages []string
}

// v228RunGoalStart drives the real start command with a fake tmux backend and a
// capturing DeliverGoal. skipRoles are dispatched but never record a launch, so
// they stay non-live — that is the "last role fails to come up" case.
func v228RunGoalStart(t *testing.T, fixture v228GoalFixture, goal string, alive map[int]bool, pidFor func(string) int, skipRoles ...string) v228GoalRun {
	t.Helper()
	run := v228GoalRun{}
	skip := map[string]bool{}
	for _, role := range skipRoles {
		skip[role] = true
	}
	probeAlive := func(pid int) bool { return alive[pid] }

	var out bytes.Buffer
	deps := simpleStartDependencies{
		LookPath: func(name string) (string, error) { return filepath.Join("/usr/bin", name), nil },
		ResolveAMQEnv: func(string, string, string, string) (amqEnv, error) {
			return amqEnv{Root: fixture.Root, BaseRoot: filepath.Dir(fixture.Root), AMQVersion: doctorMinAMQVersion}, nil
		},
		DuplicateProbe: duplicateLaunchProbe{
			PIDAlive:     probeAlive,
			ProcessMatch: func(pid int, _ func(string) bool) bool { return probeAlive(pid) },
			ProcessTTY:   func(int) (string, bool) { return "", false },
			Now:          func() time.Time { return v228Now },
		},
		RuntimeProbe: launchRuntimeProbe{
			PIDAlive:     probeAlive,
			ProcessMatch: func(int, func(string) bool) bool { return true },
			ProcessTTY:   func(int) (string, bool) { return "", false },
		},
		Launch: func(spawn team.Team, opts teamLaunchOptions) (teamLaunchResult, error) {
			result := teamLaunchResult{}
			for i, member := range spawn.Members {
				pid := pidFor(member.Role)
				tmuxInfo := &launch.TmuxInfo{
					Session: fixture.Session, WindowID: fmt.Sprintf("@%d", 600+i),
					PaneID: fmt.Sprintf("%%%d", 700+pid%100), Target: opts.Target,
				}
				result.Panes = append(result.Panes, teamLaunchResultPane{
					Role: member.Role, PaneID: tmuxInfo.PaneID, WindowID: tmuxInfo.WindowID,
				})
				if skip[member.Role] {
					// Dispatched, then died before recording anything.
					continue
				}
				agentDir := filepath.Join(fixture.Root, "agents", member.Handle)
				if err := os.MkdirAll(agentDir, 0o755); err != nil {
					return teamLaunchResult{}, err
				}
				if err := launch.Write(agentDir, launch.Record{
					Schema: launch.SchemaVersion, Binary: member.Binary,
					Role: member.Role, Handle: member.Handle, Session: fixture.Session,
					TeamProfile: fixture.Profile, TeamHome: fixture.Project, CWD: fixture.Project,
					Root: fixture.Root, BaseRoot: filepath.Dir(fixture.Root),
					Trust: opts.Trust, ToolProfile: team.ToolProfileFull,
					AgentPID: pid, StartedAt: v228Now,
					Tmux: tmuxInfo, Terminal: launch.TerminalInfoFromTmux(tmuxInfo),
				}); err != nil {
					return teamLaunchResult{}, err
				}
				alive[pid] = true
				run.Events = append(run.Events, "spawn:"+member.Role)
			}
			return result, nil
		},
		StartWatcher: func(team.Team, string, string, string) error { return nil },
		DeliverGoal: func(plan simpleStartPlan, delivered string) error {
			run.Events = append(run.Events, "goal:"+delivered)
			run.GoalPlans = append(run.GoalPlans, plan)
			run.GoalMessages = append(run.GoalMessages, delivered)
			return nil
		},
		Sleep: func(time.Duration) {},
	}

	args := []string{
		"--project", fixture.Project, "--profile", fixture.Profile, "--session", fixture.Session,
		"--terminal", "tmux", "--yes",
	}
	if strings.TrimSpace(goal) != "" {
		args = append(args, "--goal", goal)
	}
	run.Err = runStartWithDependencies(args, deps, strings.NewReader(""), &out)
	run.Output = out.String()
	return run
}

func TestV228ContractWizardGoalDeliveredOnlyAfterAllAgentsLive(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)
	const goal = "ship the simple launcher"

	// Clause 1: every role live -> exactly one goal send, issued LAST.
	happy := v228NewGoalFixture(t, "ac9", "cto", []string{"cto", "dev"})
	pids := map[string]int{"cto": 6001, "dev": 6002}
	run := v228RunGoalStart(t, happy, goal, map[int]bool{}, func(role string) int { return pids[role] })
	if run.Err != nil {
		t.Fatalf("start --goal: %v\n%s", run.Err, run.Output)
	}
	if len(run.GoalMessages) != 1 || run.GoalMessages[0] != goal {
		t.Fatalf("goal deliveries = %v, want exactly one %q", run.GoalMessages, goal)
	}
	if len(run.Events) == 0 || run.Events[len(run.Events)-1] != "goal:"+goal {
		t.Fatalf("event order = %v, want the goal send last", run.Events)
	}
	for _, event := range run.Events[:len(run.Events)-1] {
		if strings.HasPrefix(event, "goal:") {
			t.Fatalf("goal was sent before every role spawned: %v", run.Events)
		}
	}
	// The plan handed to delivery is the VERIFIED one: every role live.
	if len(run.GoalPlans) != 1 {
		t.Fatalf("goal plans = %d, want 1", len(run.GoalPlans))
	}
	for _, role := range run.GoalPlans[0].Roles {
		if role.State != "live" && role.State != "live/config-diverged" {
			t.Errorf("goal delivered with %s in state %q; delivery must follow full verification", role.Member.Role, role.State)
		}
	}

	// Clause 2: the last role never comes up -> start fails and NOTHING is sent.
	broken := v228NewGoalFixture(t, "ac9", "cto", []string{"cto", "dev"})
	brokenPIDs := map[string]int{"cto": 6011, "dev": 6012}
	failed := v228RunGoalStart(t, broken, goal, map[int]bool{}, func(role string) int { return brokenPIDs[role] }, "dev")
	if failed.Err == nil {
		t.Fatalf("start succeeded with a role that never came up:\n%s", failed.Output)
	}
	if len(failed.GoalMessages) != 0 {
		t.Errorf("goal delivered for a squad that never assembled: %v", failed.GoalMessages)
	}
}

func TestV228ContractSkippedGoalCanBeAssignedLater(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)

	fixture := v228NewGoalFixture(t, "ac9", "cto", []string{"cto", "dev"})
	pids := map[string]int{"cto": 6021, "dev": 6022}
	alive := map[int]bool{}

	// Skipped in the wizard: start succeeds and sends nothing.
	run := v228RunGoalStart(t, fixture, "", alive, func(role string) int { return pids[role] })
	if run.Err != nil {
		t.Fatalf("goal-less start: %v\n%s", run.Err, run.Output)
	}
	if len(run.GoalMessages) != 0 {
		t.Fatalf("goal-less start delivered %v", run.GoalMessages)
	}

	// Assigned later: the send targets the lead recorded in the roster, at the
	// canonical root, as one ordinary message.
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	var sent [][]string
	runAMQCommand = func(req amqCommandRequest) ([]byte, error) {
		sent = append(sent, req.Arg)
		return []byte(`{"id":"goal-1"}`), nil
	}
	tm, err := team.ReadProfile(fixture.Project, fixture.Profile)
	if err != nil {
		t.Fatal(err)
	}
	plan := simpleStartPlan{Project: fixture.Project, Profile: fixture.Profile, Session: fixture.Session, Root: fixture.Root, Team: tm}
	if err := deliverSimpleStartGoal(plan, "late goal"); err != nil {
		t.Fatalf("later goal assignment: %v", err)
	}
	if len(sent) != 1 {
		t.Fatalf("later goal produced %d transport calls, want 1", len(sent))
	}
	args := strings.Join(sent[0], " ")
	for _, want := range []string{"send", "--root " + fixture.Root, "--to " + fixture.Lead, "late goal"} {
		if !strings.Contains(args, want) {
			t.Errorf("later goal send missing %q: %s", want, args)
		}
	}
}

func TestV228ContractStartRerunNeverResendsGoal(t *testing.T) {
	requireV228Contract(t)
	setupFakeAMQSessionRoots(t)
	swapStatusPaneLister(t, nil, nil)
	const goal = "one goal only"

	fixture := v228NewGoalFixture(t, "ac9", "cto", []string{"cto", "dev"})
	pids := map[string]int{"cto": 6031, "dev": 6032}
	alive := map[int]bool{}
	pidFor := func(role string) int { return pids[role] }

	first := v228RunGoalStart(t, fixture, goal, alive, pidFor)
	if first.Err != nil {
		t.Fatalf("first start --goal: %v\n%s", first.Err, first.Output)
	}
	if len(first.GoalMessages) != 1 {
		t.Fatalf("first start delivered %v, want exactly one goal", first.GoalMessages)
	}
	afterFirst := v228InventoryPaths(t, filepath.Join(fixture.Project, team.DirName))

	// Same command again: every role is live, so nothing spawns and no goal is
	// re-sent. The mechanism must be "a spawnless rerun issues no send", not a
	// persisted delivered-marker.
	second := v228RunGoalStart(t, fixture, goal, alive, pidFor)
	if second.Err != nil {
		t.Fatalf("rerun start --goal: %v\n%s", second.Err, second.Output)
	}
	if len(second.GoalMessages) != 0 {
		t.Errorf("rerun re-sent the goal: %v", second.GoalMessages)
	}
	if len(second.Events) != 0 {
		t.Errorf("rerun did work: %v, want no spawn and no send", second.Events)
	}
	if !strings.Contains(second.Output, "already started") {
		t.Errorf("rerun did not report an already-live squad:\n%s", second.Output)
	}

	// No new state was written to make "do not re-send" true: a marker would be
	// exactly the second owned representation the governing rule forbids.
	afterSecond := v228InventoryPaths(t, filepath.Join(fixture.Project, team.DirName))
	for path := range afterSecond {
		if !afterFirst[path] {
			t.Errorf("rerun persisted new state under %s: %s (goal delivery must keep no dedup record)", team.DirName, path)
		}
	}
}
