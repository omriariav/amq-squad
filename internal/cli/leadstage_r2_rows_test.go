package cli

import (
	"context"
	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/launch"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

// 3.3.1, the lead stages its own launch record, round 2 (#1041's PR-level
// Codex pass). Lead authority is the record the lead's start staged
// (SquadLead) for the handle squad.yaml names its lead NOW; a role id
// ("cto", "lead") alone grants nothing, in task show and in the owner's
// phone. The rows drive the real producers: `lead --team` (runLead), the
// start of a confirmed team (leadLauncher), and a roster launch's staging
// (seatlaunch.Stage).

// attachLead is `remote attach --lead h`'s lead check over the squad
// (single-squad mode, the real remoteFlags.mode).
func attachLead(t *testing.T, project, root, h string) error {
	t.Helper()
	f := newRemoteFlags("attach")
	if err := f.fs.Parse([]string{"--lead", h}); err != nil {
		t.Fatal(err)
	}
	_, err := f.mode(contextResolution{Root: root, ProjectDir: project}, "v3")
	return err
}

// showAs is task show --me me's next entries for a fresh task in root.
func showAs(t *testing.T, root, me string) []string {
	t.Helper()
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
	return showNext(t, added.ID, me)
}

// stageSeat stages a roster seat's record as a roster launch does
// (seatlaunch.Stage, the real producer of a seat's record).
func stageSeat(t *testing.T, root, handle, role, launchID string) {
	t.Helper()
	rec := launch.Record{Schema: launch.SchemaVersion, Handle: handle, Root: root, BaseRoot: filepath.Dir(root), Session: filepath.Base(root),
		Runtime: "codex", Role: role, LaunchID: launchID, StartedAt: time.Now().UTC(), Proof: &launch.ProofTicket{Token: "t"},
		Apply: &launch.Apply{Outcome: "applied", At: time.Now().UTC()}}
	if err := seatlaunch.Stage(seatlaunch.Compiled{Records: []launch.Record{rec}, Roles: [][]byte{[]byte("a seat's role\n")}}); err != nil {
		t.Fatal(err)
	}
}

// P2: a role id alone grants nothing. A roster seat whose role is "lead" or
// "cto" (staged by a roster launch) is no lead in task show and is refused
// by the owner's phone, while the squad's real lead (staged by its start,
// squad.yaml's lead) is one.
func TestLeadStageR2ARoleAloneGrantsNoLeadAuthority(t *testing.T) {
	for _, role := range []string{"lead", "cto"} {
		t.Run(role, func(t *testing.T) {
			_, _, project, root := teamLeadFixture(t)
			leadTeamOnce(t)
			stageSeat(t, root, "dev", role, "L-roster")
			if got := showAs(t, root, "dev"); len(got) != 0 {
				t.Errorf("task show --me dev (a %q seat) next = %v, want none", role, got)
			}
			if err := attachLead(t, project, root, "dev"); err == nil {
				t.Errorf("the owner's phone attached to a %q seat as its lead", role)
			}
			if got := showAs(t, root, "cto"); !leadOnly(got) {
				t.Errorf("task show --me cto (the real lead) next = %v", got)
			}
			if err := attachLead(t, project, root, "cto"); err != nil {
				t.Errorf("the owner's phone refused the real lead: %v", err)
			}
		})
	}
}

// P2: lead authority follows squad.yaml's CURRENT lead. squad.yaml's lead
// moves from cto to dev before dev starts: cto's record still marks it the
// lead it was, and grants nothing now.
func TestLeadStageR2LeadAuthorityIsSquadYAMLsCurrentLead(t *testing.T) {
	_, _, project, root := teamLeadFixture(t)
	leadTeamOnce(t)
	mustWrite(t, filepath.Join(project, "squad.yaml"), strings.Replace(teamLeadSquad, "lead: cto", "lead: dev", 1))
	if got := showAs(t, root, "cto"); len(got) != 0 {
		t.Errorf("task show --me cto (no longer squad.yaml's lead) next = %v, want none", got)
	}
	if err := attachLead(t, project, root, "cto"); err == nil {
		t.Error("the owner's phone attached to a lead squad.yaml no longer names")
	}
}

// P2: a former lead an AMQ launch started (its launch's record, marked by
// its start) keeps no lead authority after squad.yaml's lead moves and the
// new lead starts, whatever its role id ("cto", "lead").
func TestLeadStageR2AFormerLaunchedLeadKeepsNoAuthority(t *testing.T) {
	for _, role := range []string{"cto", "lead"} {
		t.Run(role, func(t *testing.T) {
			_, _, project, root := teamLeadFixture(t)
			squad := strings.Replace(teamLeadSquad, "    role: cto\n", "    role: "+role+"\n", 1)
			mustWrite(t, filepath.Join(project, "squad.yaml"), squad)
			stageSeat(t, root, "cto", role, "L-amq")
			t.Setenv("AM_ME", "cto")
			t.Setenv("AM_ROOT", root)
			leadTeamOnce(t)
			t.Setenv("AM_ME", "")
			t.Setenv("AM_ROOT", "")
			mustWrite(t, filepath.Join(project, "squad.yaml"), strings.Replace(strings.Replace(squad, "lead: cto", "lead: dev", 1), "    runtime: codex\n", "    runtime: pi\n", 1))
			leadTeamOnce(t)
			if got := showAs(t, root, "cto"); len(got) != 0 {
				t.Errorf("task show --me cto (a former launched %q lead) next = %v, want none", role, got)
			}
			if err := attachLead(t, project, root, "cto"); err == nil {
				t.Errorf("the owner's phone attached to a former launched %q lead", role)
			}
			if got := showAs(t, root, "dev"); !leadOnly(got) {
				t.Errorf("task show --me dev (the new lead) next = %v", got)
			}
		})
	}
}

// P1 (b): the guard refuses a seat's `lead`, `up` and `down` (the owner's
// verbs that start or stop a lead), as it refuses seat-exec: best effort
// (3.3.1 known limit 9, the shell class); the owner's own `lead --team`
// still stages (TestLeadStageLeadTeamStagesTheLeadsOwnRecord).
func TestLeadStageR2TheGuardRefusesASeatsLeadUpAndDown(t *testing.T) {
	f := newHookFixture(t)
	for _, cmd := range []string{
		"amq-squad lead --team",
		"env -u AM_ME -u AM_ROOT amq-squad lead --team",
		"/usr/local/bin/amq-squad lead",
		"amq-squad up",
		"amq-squad up --project /x",
		"amq-squad down",
	} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: f.repo}, f.now); v.Verdict != "deny" || v.ReasonCode != "owner_only" {
			t.Errorf("%q: %s %s", cmd, v.Verdict, v.ReasonCode)
		}
	}
	for _, cmd := range []string{"amq-squad status --json", "amq-squad task show t1 --json", "amq-squad leads"} {
		if v := evaluateGuard(f.id(), guardCall{Tool: "Bash", Args: map[string]string{"command": cmd}, CWD: f.repo}, f.now); v.Verdict != "allow" {
			t.Errorf("%q: %s %s", cmd, v.Verdict, v.ReasonCode)
		}
	}
}

// Codex's test note on #1041: the mayor's close of a started team removes
// the REAL record the start staged (the real leadLauncher, its real steps).
func TestLeadStageR2TheMayorsCloseRemovesTheRealRecord(t *testing.T) {
	f, s, _ := startWorld(t, autoFleet(0), "owner/app")
	mustWrite(t, team.SquadPath(f.proj), team.RenderPlan(soloTeamPlan()))
	l := &leadLauncher{s: s, at: time.Now()}
	if err := l.Launch(context.Background(), fleet.LaunchRequest{}); err != nil {
		t.Fatalf("launch: %v %+v", err, s.steps)
	}
	tm, err := team.ReadFor(f.proj, "")
	if err != nil {
		t.Fatal(err)
	}
	if rec, err := launch.ReadStaged(f.root, tm.LeadHandle); err != nil || !rec.SquadLead {
		t.Fatalf("the started team's lead record %+v %v", rec, err)
	}
	if err := l.Close(context.Background(), fleet.LaunchRequest{}); err != nil {
		t.Fatal(err)
	}
	if rec, err := launch.ReadStaged(f.root, tm.LeadHandle); !os.IsNotExist(err) {
		t.Errorf("the mayor's close kept the lead record: %+v %v", rec, err)
	}
}

// stageLeadFor makes handle root's squad lead through the real producer
// (seatlaunch.StageLead), from a repository whose squad.yaml names it the
// lead: the lead authority task show and the owner's phone read (3.3.1).
func stageLeadFor(t *testing.T, root, handle string) string {
	t.Helper()
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "squad.yaml"), "schema: 1\nsession: "+filepath.Base(root)+"\nlead: "+handle+"\nseats:\n  - handle: "+handle+"\n    role: cto\n    runtime: pi\n")
	q := seatlaunch.Squad{ProjectRoot: project, BaseRoot: filepath.Dir(root), Session: filepath.Base(root),
		Seats: []seatlaunch.Seat{{Handle: handle, Runtime: "pi", RoleName: "cto"}}}
	rec, err := seatlaunch.StageLead(q, handle, os.Getpid(), time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := seatlaunch.PromoteLead(root, handle, rec.LaunchID); err != nil {
		t.Fatal(err)
	}
	return project
}

// markLead makes handle (a record already staged in root, as a fixture's
// launch left it) root's squad lead through the real producer: the lead's
// start under an AMQ launch of its own handle (seatlaunch.StageLead,
// launched), from a repository whose squad.yaml names it the lead.
func markLead(t *testing.T, root, handle string) string {
	t.Helper()
	project := t.TempDir()
	mustWrite(t, filepath.Join(project, "squad.yaml"), "schema: 1\nsession: "+filepath.Base(root)+"\nlead: "+handle+"\nseats:\n  - handle: "+handle+"\n    role: cto\n    runtime: pi\n")
	q := seatlaunch.Squad{ProjectRoot: project, BaseRoot: filepath.Dir(root), Session: filepath.Base(root),
		Seats: []seatlaunch.Seat{{Handle: handle, Runtime: "pi", RoleName: "cto"}}}
	// a launch always has its launch id (a fixture's seeded record may not)
	if err := launch.UpdateStaged(root, handle, func(r *launch.Record) error {
		if r.LaunchID == "" {
			r.LaunchID = "L-" + handle
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rec, err := seatlaunch.StageLead(q, handle, os.Getpid(), time.Now(), true)
	if err != nil {
		t.Fatalf("mark %s the lead: %+v %v", handle, rec, err)
	}
	if err := seatlaunch.PromoteLead(root, handle, rec.LaunchID); err != nil {
		t.Fatalf("promote %s: %v", handle, err)
	}
	return project
}

// Codex r2 pass 1: a start killed after staging and before it went on
// leaves no lead authority: while `lead --team` starts its scheduler (the
// window before its pi spawns) and while a confirmed team's start runs its
// scheduler, the staged record is pending; task show and the owner's phone
// grant nothing until the start promotes it.
func TestLeadStageR2AStartThatHasNotGoneOnGrantsNoAuthority(t *testing.T) {
	t.Run("lead --team", func(t *testing.T) {
		_, _, project, root := teamLeadFixture(t)
		var during launch.Record
		var duringErr error
		startLeadScheduler = func(string, string, []string) (*supervisor, error) {
			during, duringErr = launch.ReadStaged(root, "cto")
			return &supervisor{}, nil
		}
		leadTeamOnce(t)
		if duringErr != nil || during.SquadLead || during.SquadProject != project {
			t.Errorf("the record before the pi ran: %+v %v", during, duringErr)
		}
		if cur, _ := squadLeadNow(project); during.HasLeadAuthority(cur) {
			t.Error("a start that had not gone on had lead authority")
		}
		if rec, err := launch.ReadStaged(root, "cto"); err != nil || !rec.SquadLead {
			t.Errorf("the record after the pi ran: %+v %v", rec, err)
		}
	})
	t.Run("a confirmed team", func(t *testing.T) {
		f, s, digest := startWorld(t, autoFleet(0), "owner/app")
		// observed at the scheduler's start and again while the start waits
		// for the roster's outcome (the roster comes up only afterwards)
		var mu sync.Mutex
		var during []launch.Record
		observe := func(root string) {
			entries, _ := os.ReadDir(filepath.Join(root, "extensions", "amq-squad", "launch"))
			mu.Lock()
			defer mu.Unlock()
			for _, e := range entries {
				if rec, err := launch.ReadStaged(root, e.Name()); err == nil {
					during = append(during, rec)
				}
			}
		}
		startLeadScheduler = func(_, root string, _ []string) (*supervisor, error) {
			observe(root)
			go func() {
				time.Sleep(300 * time.Millisecond) // the start waits for the roster
				observe(root)
				_ = schedproc.AppendDecision(root, time.Now(), scheduler.Decision{Kind: "roster_launched", Reason: "launch L1: 2 seats in one apply"})
			}()
			return &supervisor{}, nil
		}
		if r := s.start(context.Background(), leadStartRequest{Kind: leadStartKind, Digest: digest, Project: f.proj}); r.Outcome != "started" {
			t.Fatalf("start = %+v", r)
		}
		mu.Lock()
		defer mu.Unlock()
		if len(during) < 2 {
			t.Fatalf("staged records observed during the start: %d", len(during))
		}
		for _, rec := range during {
			if rec.SquadLead {
				t.Errorf("a record had lead authority before the team was up: %+v", rec)
			}
		}
		tm, _ := team.ReadFor(f.proj, "")
		if rec, err := launch.ReadStaged(f.root, tm.LeadHandle); err != nil || !rec.SquadLead {
			t.Errorf("the record after the team was up: %+v %v", rec, err)
		}
	})
}

// Codex r2 pass 1 (test strength): the fleet attach re-checks lead
// authority every tick: squad.yaml's lead moving away from the registry's
// lead while the attachment runs stops the routing to it.
func TestLeadStageR2TheFleetAttachRechecksTheCurrentLeadEveryTick(t *testing.T) {
	f, file, _, ep, mailer := operatorFixture(t)
	project := markLead(t, f.root, "cto")
	out, stop := attachAsync(t, []string{"--fleet-file", file, "--project", f.project, "--session", "s", "--me", "cto", "--interval", "50ms", "--json"})
	eventually(t, "the endpoint to start", func() bool { _, starts, _ := ep.counts(); return starts == 1 })
	f.answer(t, "@app/cto first", "")
	eventually(t, "the message routed to app's lead", func() bool { return mailer.to("cto") })
	mustWrite(t, filepath.Join(project, "squad.yaml"), "schema: 1\nsession: s\nlead: dev\nseats:\n  - handle: cto\n    role: cto\n    runtime: pi\n  - handle: dev\n    role: cto\n    runtime: pi\n")
	time.Sleep(200 * time.Millisecond) // ticks read squad.yaml again
	before := mailer.count("cto")
	f.answer(t, "@app/cto second", "")
	time.Sleep(400 * time.Millisecond)
	stop()
	if after := mailer.count("cto"); after != before {
		t.Errorf("a message routed to a lead squad.yaml no longer names (%d -> %d)\n%s", before, after, out.String())
	}
}

// Codex r2 pass 2 (test strength): lead authority fails closed when the
// repository's squad.yaml cannot be read: task show and the owner's phone
// grant the genuine lead's record nothing while squad.yaml is gone.
func TestLeadStageR2AnUnreadableSquadYAMLGrantsNothing(t *testing.T) {
	_, _, project, root := teamLeadFixture(t)
	leadTeamOnce(t)
	if err := os.Rename(filepath.Join(project, "squad.yaml"), filepath.Join(project, "squad.yaml.away")); err != nil {
		t.Fatal(err)
	}
	if got := showAs(t, root, "cto"); len(got) != 0 {
		t.Errorf("task show --me cto with no squad.yaml next = %v, want none", got)
	}
	if _, err := (buzz.LaunchRecordLeads{Current: squadLeadNow}).Lead(root, "cto"); err == nil {
		t.Error("the owner's phone routed with no squad.yaml")
	}
}
