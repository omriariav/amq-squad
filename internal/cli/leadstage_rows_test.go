package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/piruntime"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// 3.3.1, the lead stages its own launch record. C5a made every trust
// decision read the record the core staged under
// <root>/extensions/amq-squad/launch/<handle>/, but only a roster launch
// staged one: the lead `up` starts (`lead --team`) had none, so task show's
// lead role and the owner's phone (Buzz) refused the real lead. The rows
// drive the REAL producer of the trusted record, `lead --team` (the command
// up's tmux runs) and the start of a confirmed team; none writes a record
// by hand.

// leadTeamOnce runs the real `lead --team` once in the fixture's repository.
func leadTeamOnce(t *testing.T) {
	t.Helper()
	if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err != nil {
		t.Fatal(err)
	}
}

// The failing row first: after `lead --team` the lead's staged record is
// the core's, under the core-owned launch directory (0700): its handle,
// role id, runtime, session and root are squad.yaml's lead's, it is this
// launch's (a launch id, a start, the lead process's pid), and it names no
// seat launch fact (no worktree, nothing uninstall would treat as one).
func TestLeadStageLeadTeamStagesTheLeadsOwnRecord(t *testing.T) {
	_, _, _, root := teamLeadFixture(t)
	before := time.Now().UTC().Add(-time.Second)
	leadTeamOnce(t)
	rec, err := launch.ReadStaged(root, "cto")
	if err != nil {
		t.Fatalf("lead --team staged no record for its lead: %v", err)
	}
	if rec.Handle != "cto" || rec.Role != "cto" || rec.Session != "v31" || rec.Root != root || rec.BaseRoot != filepath.Dir(root) || rec.Runtime != "pi" {
		t.Errorf("the staged lead record %+v", rec)
	}
	if rec.LaunchID == "" || rec.StartedAt.Before(before) || rec.AgentPID != os.Getpid() {
		t.Errorf("the staged lead record is not this launch's: %+v", rec)
	}
	if rec.Worktree != "" || rec.Branch != "" || rec.RequestID != "" || rec.Proof != nil || rec.CWD != "" {
		t.Errorf("the staged lead record names seat launch facts: %+v", rec)
	}
	if st, err := os.Stat(launch.StagedDir(root, "cto")); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("the staged directory %v %v, want 0700", st, err)
	}
}

// The lead's own role and rules stay its start's: no role.md and no role
// or rules digest is staged, so `amq-squad context` for the lead has no
// staged role (its pi falls back to AMQ_SQUAD_ROLE_PATH, the team lead's
// role), no rules drift warning and no worktree line; a role.md an
// earlier seat launch of the handle left is removed.
func TestLeadStageTheLeadsContextKeepsItsOwnRole(t *testing.T) {
	_, _, _, root := teamLeadFixture(t)
	if err := os.MkdirAll(launch.StagedDir(root, "cto"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(launch.StagedDir(root, "cto"), launch.RoleFileName), "an earlier seat launch's role\n")
	leadTeamOnce(t)
	if _, err := os.Stat(filepath.Join(launch.StagedDir(root, "cto"), launch.RoleFileName)); !os.IsNotExist(err) {
		t.Errorf("a role.md is staged for the lead: %v", err)
	}
	rec, _ := launch.ReadStaged(root, "cto")
	if rec.RoleSHA256 != "" || rec.RulesPath != "" || rec.RulesSHA256 != "" || rec.TeamHome != "" {
		t.Errorf("the lead record carries a role or rules digest: %+v", rec)
	}
	d := buildHookContext(hookIdentity{Handle: "cto", Root: root}, "", time.Now())
	if d.RoleText != "" || d.Worktree != "" {
		t.Errorf("the lead's context role %q worktree %q", d.RoleText, d.Worktree)
	}
	for _, w := range d.Warnings {
		if strings.Contains(w, "rules changed") {
			t.Errorf("the lead's context warns %q", w)
		}
	}
}

// After `lead --team`, task show resolves the real lead's role: --me the
// lead gets the lead's next entries.
func TestLeadStageTaskShowResolvesTheRealLead(t *testing.T) {
	_, _, _, root := teamLeadFixture(t)
	leadTeamOnce(t)
	t.Setenv("AM_ROOT", root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	s, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := showNext(t, added.ID, "cto"); !leadOnly(got) {
		t.Errorf("task show --me cto (the real lead) next = %v, want the lead's", got)
	}
	if got := showNext(t, added.ID, "dev"); len(got) != 0 {
		t.Errorf("task show --me dev (a seat) next = %v, want none", got)
	}
}

// leadOnly reports next entries that are all, and at least one, the lead's.
func leadOnly(got []string) bool {
	if len(got) == 0 {
		return false
	}
	for _, g := range got {
		if !strings.HasSuffix(g, "/"+task.NextLead) {
			return false
		}
	}
	return true
}

// showNext is task show --json --me me's next entries, as verb/who.
func showNext(t *testing.T, id, me string) []string {
	t.Helper()
	out, err := taskCLI(t, "show", id, "--json", "--me", me)
	if err != nil {
		t.Fatal(err)
	}
	var env struct {
		Data struct {
			Next []task.NextEntry `json:"next"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	var got []string
	for _, n := range env.Data.Next {
		got = append(got, n.Verb+"/"+n.Who)
	}
	return got
}

// After `lead --team`, the owner's phone routes to the real lead: in
// single-squad mode (remote attach --lead: the default lead roles and the
// squad's lead role) and in fleet mode (the registry's lead, re-checked per
// tick against the squad's own lead role only).
func TestLeadStageBuzzRoutesToTheRealLead(t *testing.T) {
	_, _, project, root := teamLeadFixture(t)
	leadTeamOnce(t)
	tm, err := team.ReadFor(project, "")
	if err != nil || strings.TrimSpace(tm.Lead) == "" {
		t.Fatalf("team %+v %v", tm, err)
	}
	// remote attach --lead cto: the real mode's lead check
	f := newRemoteFlags("attach")
	if err := f.fs.Parse([]string{"--lead", "cto"}); err != nil {
		t.Fatal(err)
	}
	if m, err := f.mode(contextResolution{Root: root, ProjectDir: project}, "v3"); err != nil || m.target.Handle != "cto" {
		t.Errorf("remote attach --lead cto (the real lead): %+v %v", m.target, err)
	}
	single := buzz.LaunchRecordLeads{Roles: append(slices.Clone(buzz.DefaultLeadRoles), tm.Lead)}
	if d, err := single.Lead(root, "cto"); err != nil || d.Handle != "cto" {
		t.Errorf("single-squad routing to the real lead: %+v %v", d, err)
	}
	reg := fleet.Registry{Squads: []fleet.Squad{{Name: "app", Root: root, Lead: "cto", ProjectPath: project}}}
	fl := buzz.FleetLeads{Registry: reg, Roles: map[string][]string{"app": {strings.TrimSpace(tm.Lead)}}}
	if d, err := fl.Lead(root, "cto"); err != nil || d.Handle != "cto" {
		t.Errorf("fleet routing to the real lead: %+v %v", d, err)
	}
	if _, err := single.Lead(root, "dev"); err == nil {
		t.Error("a seat with no lead record routed as a lead")
	}
}

// The staged lead record makes the lead no seat: the roster still leaves
// the lead out (squad.yaml's lead), the scheduler's launch ledger lists no
// live seat for it (no proof), and the lead's directory holds only the
// record (no role, proof, wake, marks or exec record).
func TestLeadStageTheLeadIsNoSeat(t *testing.T) {
	_, _, project, root := teamLeadFixture(t)
	leadTeamOnce(t)
	q, lead, err := squadOfProject(project)
	if err != nil {
		t.Fatal(err)
	}
	roster, excluded := (&schedproc.Slots{Squad: q}).Roster(lead)
	for _, r := range roster {
		if r.Handle == "cto" {
			t.Errorf("the roster holds the lead: %+v", roster)
		}
	}
	if len(excluded) != 1 || !strings.Contains(excluded[0], "seat cto is the lead") {
		t.Errorf("excluded %v", excluded)
	}
	for _, s := range schedproc.LiveSeats(root) {
		if s.Handle == "cto" {
			t.Errorf("the launch ledger lists the lead as a live seat: %+v", s)
		}
	}
	names, err := os.ReadDir(launch.StagedDir(root, "cto"))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range names {
		if !strings.HasSuffix(n.Name(), ".lock") {
			got = append(got, n.Name())
		}
	}
	if !slices.Equal(got, []string{launch.FileName}) {
		t.Errorf("the lead's staged directory holds %v, want only the record", got)
	}
	// status: the lead's row joins its role and runtime, and has no launch
	// screen (no roster launch staged it), so the running roster's
	// launch-id fallback never orders the seats by the lead's launch
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o700); err != nil {
		t.Fatal(err)
	}
	in := status.Collect(status.CollectOptions{Root: root, Now: time.Now(), PIDAlive: func(int) bool { return false }})
	found := false
	for _, st := range in.Seats {
		if st.Handle == "cto" {
			found = true
			if st.Launch != nil || st.Role != "cto" || st.Runtime != "pi" || len(st.Warnings) != 0 {
				t.Errorf("status's lead row %+v", st)
			}
		}
	}
	if !found {
		t.Error("status has no row for the lead")
	}
}

// A relaunch restages: a second `lead --team` writes a new launch (another
// launch id, a later start), and a role changed in squad.yaml since is the
// record's role.
func TestLeadStageARelaunchKeepsTheRecordCurrent(t *testing.T) {
	_, _, project, root := teamLeadFixture(t)
	leadTeamOnce(t)
	first, err := launch.ReadStaged(root, "cto")
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(project, "squad.yaml"), strings.Replace(teamLeadSquad, "    role: cto\n", "    role: lead\n", 1))
	time.Sleep(10 * time.Millisecond)
	leadTeamOnce(t)
	second, err := launch.ReadStaged(root, "cto")
	if err != nil {
		t.Fatal(err)
	}
	if second.LaunchID == first.LaunchID || !second.StartedAt.After(first.StartedAt) || second.Role != "lead" {
		t.Errorf("the relaunch did not restage: first %s %s %s, second %s %s %s", first.LaunchID, first.StartedAt, first.Role, second.LaunchID, second.StartedAt, second.Role)
	}
}

// A lead squad.yaml no longer names keeps no lead record: squad.yaml's lead
// moves from cto to dev, dev's start stages dev and removes cto's lead-only
// record (it names no launch), so task show gives cto no lead role and the
// owner's phone does not route to it, even where cto's role id is itself a
// lead role ("lead"); status gives cto no launch screen.
func TestLeadStageAFormerLeadKeepsNoLeadRole(t *testing.T) {
	for _, role := range []string{"cto", "lead"} {
		t.Run(role, func(t *testing.T) {
			_, _, project, root := teamLeadFixture(t)
			squad := strings.Replace(teamLeadSquad, "    role: cto\n", "    role: "+role+"\n", 1)
			mustWrite(t, filepath.Join(project, "squad.yaml"), squad)
			leadTeamOnce(t)
			mustWrite(t, filepath.Join(project, "squad.yaml"), strings.Replace(strings.Replace(squad, "lead: cto", "lead: dev", 1), "    runtime: codex\n", "    runtime: pi\n", 1))
			leadTeamOnce(t)
			if rec, err := launch.ReadStaged(root, "dev"); err != nil || rec.Role != "senior-dev" || !rec.SquadLead {
				t.Fatalf("the new lead's record %+v %v", rec, err)
			}
			if rec, err := launch.ReadStaged(root, "cto"); !os.IsNotExist(err) {
				t.Errorf("the former lead's lead-only record is kept: %+v %v", rec, err)
			}
			t.Setenv("AM_ROOT", root)
			t.Setenv("AM_ME", "")
			now := withFixedTaskNow(t)
			s, err := task.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			added, err := s.Add(task.AddInput{Title: "t", Class: "plan"}, now)
			if err != nil {
				t.Fatal(err)
			}
			if got := showNext(t, added.ID, "dev"); !leadOnly(got) {
				t.Errorf("task show --me dev (the new lead) next = %v", got)
			}
			if got := showNext(t, added.ID, "cto"); len(got) != 0 {
				t.Errorf("task show --me cto (no longer the lead) next = %v, want none", got)
			}
			if _, err := (buzz.LaunchRecordLeads{Roles: append(slices.Clone(buzz.DefaultLeadRoles), role)}).Lead(root, "cto"); err == nil {
				t.Error("the owner's phone still routes to the former lead")
			}
		})
	}
}

// The lead's staging stages the handle the process runs as: a squad.yaml
// whose lead changed after the lead's identity was resolved stages no
// other handle.
func TestLeadStageStagesTheRunningLeadsHandle(t *testing.T) {
	_, _, project, root := teamLeadFixture(t)
	old := leadStageSquad
	t.Cleanup(func() { leadStageSquad = old })
	leadStageSquad = func(p string) (seatlaunch.Squad, string, error) {
		// squad.yaml now names dev the lead
		mustWrite(t, filepath.Join(project, "squad.yaml"), strings.Replace(teamLeadSquad, "lead: cto", "lead: dev", 1))
		return old(p)
	}
	leadTeamOnce(t)
	if rec, err := launch.ReadStaged(root, "cto"); err != nil || !rec.SquadLead {
		t.Errorf("the running lead cto: %+v %v", rec, err)
	}
	if rec, err := launch.ReadStaged(root, "dev"); !os.IsNotExist(err) {
		t.Errorf("a lead this process does not run as was staged: %+v %v", rec, err)
	}
}

// A lead an AMQ launch of its own handle started (AM_ME is the lead) keeps
// its launch's record, as the core staged it for that launch (its launch
// id, proof ticket and role.md), marked SquadLead: no lead-only record
// replaces it, and status keeps its launch screen.
func TestLeadStageALaunchedLeadKeepsItsLaunchRecord(t *testing.T) {
	_, _, _, root := teamLeadFixture(t)
	seatRec := launch.Record{Schema: launch.SchemaVersion, Handle: "cto", Root: root, BaseRoot: filepath.Dir(root), Session: "v31", Runtime: "pi", Role: "cto",
		LaunchID: "L-amq", StartedAt: time.Now().UTC(), Proof: &launch.ProofTicket{Token: "t"}, Apply: &launch.Apply{Outcome: "applied", At: time.Now().UTC()}}
	if err := seatlaunch.Stage(seatlaunch.Compiled{Records: []launch.Record{seatRec}, Roles: [][]byte{[]byte("the launch's role\n")}}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AM_ME", "cto")
	t.Setenv("AM_ROOT", root)
	leadTeamOnce(t)
	rec, err := launch.ReadStaged(root, "cto")
	if err != nil || rec.LaunchID != "L-amq" || rec.Proof == nil || !rec.SquadLead || rec.LeadOnly() {
		t.Fatalf("the launched lead's record %+v %v", rec, err)
	}
	if b, err := os.ReadFile(filepath.Join(launch.StagedDir(root, "cto"), launch.RoleFileName)); err != nil || string(b) != "the launch's role\n" {
		t.Errorf("the launch's role.md %q %v", b, err)
	}
	if err := os.MkdirAll(filepath.Join(root, "agents", "cto"), 0o700); err != nil {
		t.Fatal(err)
	}
	in := status.Collect(status.CollectOptions{Root: root, Now: time.Now(), PIDAlive: func(int) bool { return false }})
	found := false
	for _, st := range in.Seats {
		if st.Handle == "cto" {
			found = true
			if st.Launch == nil || st.Launch.LaunchID != "L-amq" {
				t.Errorf("status's launched lead row lost its launch screen: %+v", st)
			}
		}
	}
	if !found {
		t.Error("status has no row for the launched lead")
	}
	// a handoff: dev leads now; cto's launch record stays its launch's,
	// unmarked, so cto (a cto, not a lead role) has no lead role in task show
	t.Setenv("AM_ME", "")
	t.Setenv("AM_ROOT", "")
	project := filepath.Dir(filepath.Dir(root))
	mustWrite(t, filepath.Join(project, "squad.yaml"), strings.Replace(strings.Replace(teamLeadSquad, "lead: cto", "lead: dev", 1), "    runtime: codex\n", "    runtime: pi\n", 1))
	leadTeamOnce(t)
	if rec, err := launch.ReadStaged(root, "cto"); err != nil || rec.LaunchID != "L-amq" || rec.SquadLead {
		t.Errorf("the former launched lead's record %+v %v", rec, err)
	}
	t.Setenv("AM_ROOT", root)
	now := withFixedTaskNow(t)
	st, err := task.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := st.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := showNext(t, added.ID, "cto"); len(got) != 0 {
		t.Errorf("task show --me cto (a former launched lead) next = %v, want none", got)
	}
}

// A lead start that does not go on takes its staging back: `lead --team`
// whose scheduler does not start, and a confirmed team's start whose
// scheduler or roster fails, leave no lead record (task show and the
// owner's phone grant the handle nothing).
func TestLeadStageAFailedStartLeavesNoLeadRecord(t *testing.T) {
	t.Run("lead --team, the scheduler", func(t *testing.T) {
		_, _, _, root := teamLeadFixture(t)
		startLeadScheduler = func(string, string, []string) (*supervisor, error) { return nil, errors.New("no scheduler") }
		if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err == nil {
			t.Fatal("lead --team started with no scheduler")
		}
		if rec, err := launch.ReadStaged(root, "cto"); !os.IsNotExist(err) {
			t.Errorf("a lead whose scheduler did not start keeps a lead record: %+v %v", rec, err)
		}
	})
	t.Run("lead --team, the pi runtime", func(t *testing.T) {
		_, _, _, root := teamLeadFixture(t)
		piLoad = func() (piruntime.Embedded, error) { return piruntime.Embedded{}, errors.New("no runtime") }
		if _, _, err := captureOutput(t, func() error { return runLead([]string{"--team"}) }); err == nil {
			t.Fatal("lead --team started with no pi runtime")
		}
		if rec, err := launch.ReadStaged(root, "cto"); !os.IsNotExist(err) {
			t.Errorf("a lead whose pi never ran keeps a lead record: %+v %v", rec, err)
		}
	})
	t.Run("lead --team, the pi spawn", func(t *testing.T) {
		_, _, _, root := teamLeadFixture(t)
		piRun = func(string, []string, []string) (int, error) {
			return -1, errors.New("fork: resource temporarily unavailable")
		}
		_, _, _ = captureOutput(t, func() error { return runLead([]string{"--team"}) })
		if rec, err := launch.ReadStaged(root, "cto"); !os.IsNotExist(err) {
			t.Errorf("a lead whose pi did not spawn keeps a lead record: %+v %v", rec, err)
		}
	})
	t.Run("lead --team, a pi that started and failed", func(t *testing.T) {
		_, _, _, root := teamLeadFixture(t)
		piRun = func(string, []string, []string) (int, error) {
			piStartedHook(4242) // runProcess's hook: the child started
			return -1, errors.New("wait: interrupted")
		}
		_, _, _ = captureOutput(t, func() error { return runLead([]string{"--team"}) })
		if _, err := launch.ReadStaged(root, "cto"); err != nil {
			t.Errorf("a lead whose pi ran lost its record: %v", err)
		}
	})
	for _, c := range []struct {
		name  string
		sched func(project, root string, _ []string) (*supervisor, error)
	}{
		{"a confirmed team, the scheduler", func(string, string, []string) (*supervisor, error) { return nil, errors.New("no scheduler") }},
		{"a confirmed team, the roster", func(_, root string, _ []string) (*supervisor, error) {
			_ = schedproc.AppendDecision(root, time.Now(), scheduler.Decision{Kind: "roster_launch_failed", Reason: "prepare_refused: no"})
			return &supervisor{}, nil
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f, s, digest := startWorld(t, autoFleet(0), "owner/app")
			startLeadScheduler = c.sched
			if r := s.start(context.Background(), leadStartRequest{Kind: leadStartKind, Digest: digest, Project: f.proj}); r.Outcome == "started" {
				t.Fatalf("start = %+v", r)
			}
			entries, _ := os.ReadDir(filepath.Join(f.root, "extensions", "amq-squad", "launch"))
			for _, e := range entries {
				if rec, err := launch.ReadStaged(f.root, e.Name()); err == nil && rec.SquadLead {
					t.Errorf("a failed start keeps a lead record: %+v", rec)
				}
			}
		})
	}
}

// The start of a confirmed team (the solo lead's own process, up's shared
// steps) stages the lead's record too: its lead resolves in task show and
// the owner's phone, and the lead step names the staging.
func TestLeadStageAConfirmedTeamsStartStagesItsLead(t *testing.T) {
	f, s, digest := startWorld(t, autoFleet(0), "owner/app")
	r := s.start(context.Background(), leadStartRequest{Kind: leadStartKind, Digest: digest, Project: f.proj})
	if r.Outcome != "started" {
		t.Fatalf("start = %+v", r)
	}
	tm, err := team.ReadFor(f.proj, "")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := launch.ReadStaged(f.root, tm.LeadHandle)
	var leadRuntime string
	for _, m := range tm.Members {
		if m.Handle == tm.LeadHandle {
			leadRuntime = m.Binary
		}
	}
	if err != nil || rec.Role != tm.Lead || rec.Runtime == "" || rec.Runtime != leadRuntime || rec.AgentPID != os.Getpid() || rec.LaunchID == "" || rec.Worktree != "" || !rec.SquadLead {
		t.Fatalf("the confirmed team's lead record %+v %v (runtime want %q)", rec, err, leadRuntime)
	}
	if d, err := (buzz.LaunchRecordLeads{Roles: append(slices.Clone(buzz.DefaultLeadRoles), tm.Lead)}).Lead(f.root, tm.LeadHandle); err != nil || d.Handle != tm.LeadHandle {
		t.Errorf("routing to the confirmed team's lead: %+v %v", d, err)
	}
	t.Setenv("AM_ROOT", f.root)
	t.Setenv("AM_ME", "")
	now := withFixedTaskNow(t)
	st, err := task.Open(f.root)
	if err != nil {
		t.Fatal(err)
	}
	added, err := st.Add(task.AddInput{Title: "t", Class: "plan"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if got := showNext(t, added.ID, tm.LeadHandle); !leadOnly(got) {
		t.Errorf("task show --me %s next = %v", tm.LeadHandle, got)
	}
	for _, st := range r.Steps {
		if st.Name == "lead" && !strings.Contains(st.Detail, launch.StagedPath(f.root, tm.LeadHandle)) {
			t.Errorf("the lead step does not name the staging: %q", st.Detail)
		}
	}
}

// The mayor's close of a fenced launch takes the lead's staging back with
// the lock (leadLauncher.Close).
func TestLeadStageTheMayorsCloseTakesTheStagingBack(t *testing.T) {
	_, s, _ := startWorld(t, autoFleet(0), "owner/app")
	took := 0
	l := &leadLauncher{s: s, unstage: func() { took++ }}
	if err := l.Close(context.Background(), fleet.LaunchRequest{}); err != nil {
		t.Fatal(err)
	}
	if took != 1 || l.unstage != nil {
		t.Errorf("Close took the staging back %d times", took)
	}
}
