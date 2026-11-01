package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/procinfo"
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/routing"
	"github.com/omriariav/amq-squad/v3/internal/rules"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/status"
	"github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
	"github.com/omriariav/amq-squad/v3/internal/usagelimit"
)

const schedulerUsage = `amq-squad scheduler - the squad's scheduler process

Usage:
  amq-squad scheduler run --project DIR [--lead-pool r:m:e]... [--tick 2s] [--max-seats N] [--base-branch B] [--no-open-pr]
  amq-squad scheduler relaunch [--root DIR] [--reason TEXT] [--start STAMP] [--json]
  amq-squad scheduler clear --seat H --warning CODE --launch ID [--root DIR] [--json]

scheduler run is one squad's scheduler process (plan 4.5, 16.5; started and
supervised by the lead's team mode). It ticks the deterministic scheduler
over the squad's task store and wires its hooks: the roster, every seat of
DIR's squad.yaml but its lead, each in a worktree of its own (derived per
handle unless squad.yaml names one, never the repository root), starts at
once through the AMQ launch API in one apply (pi seats too,
through AMQ's pi adapter since v0.116); a seat that holds no lease is parked
and gets the next task of its pool; --max-seats and the grant's max_seats cap
the leases held at once, not the seats up. A seat that is gone is replaced
only by relaunching the roster: AMQ refuses a fresh seat into the session's
present binding, so a task whose pool has no live seat blocks for the lead
(seat_replacement_needs_relaunch). A restarted scheduler adopts the live
seats of the roster. A roster launch that fails at prepare or apply ends the
process with its stage, AMQ's reason and the detail; the doorbell is an AMQ message from
the "scheduler" mailbox, which the seat's AMQ wake delivers; the router is
2b-4's routing table (the default table plus ~/.amq-squad/routing.yaml and
the repository's, over the owner's registry), flushed once per tick so each
ended attempt's outcome row is written; a done report runs the harness commit
step (seat commit); a refusal is recorded, then the lead is told by AMQ
message; the usage-limit detector stops new seats after the first event in
the window. One process per squad: a second one is refused before it
touches anything. An absent AMQ root or session is created first (amq coop
init, amq session create; a session_created decision), as amq-squad up
does. The exit check (the sealed ledger's evidence) runs on
every terminal path of a seat (done, failed, gone, expired); a seat whose claimed
lease expired is closed through the launcher before it is cleaned or
dispatched to again. The lead's widening arrives as the lead's AMQ message
(task widen): the scheduler applies it only from the lead of its launch
record. squad.yaml names the session and the lead, whose handle the
refusals go to and who alone widens.

--lead-pool names the pool of the lead's own seat (repeatable; the lead's
team mode passes its models, and only a process the lead's team mode started
may pass it): a concurrency cap counts the lead's seat when it shares the
pool, as a pi lead on the Ollama GPU does. A routing.yaml that
does not load refuses the start with routing_invalid.

The commit step binds by default under a merge grant: when the squad's
repository has a merge grant, it opens (or finds) each pushed seat branch's
draft PR and binds it to the task, since the merge train refuses an unbound
task (task_unbound). --no-open-pr turns that off and is refused under a
merge grant; without one there is no PR to open, and binding is off.
--open-pr is a no-op kept for compatibility. The start line prints the
effective choice.

Every launch carries the owner's standing answer trust_exact_subject to
AMQ's trust_confirmation (there is no flag for it). --max-seats only
narrows the grant.

scheduler relaunch is the lead's decision to relaunch the roster, sent over
the scheduler's decision socket (proved like task widen): the seats finish
what they hold, every seat closes, the roster launches again in one apply,
and the tasks blocked seat_replacement_needs_relaunch return to pending.
The relaunch reads squad.yaml again (brief 31-2 PR 2): a seat added since
the scheduler started launches with the roster, and its pool is the
router's from then on. The new roster replaces the old one in one step at
the drain's end, after every seat closed; the reload stops no seat itself:
a seat removed from squad.yaml finishes what it holds, closes with the
others and is not launched again, and a changed line (model, effort, role)
applies at that seat's launch in the same apply. A squad.yaml that does not
compile refuses the relaunch squad_invalid before anything drains (as does
one that changes the session or the lead). --start STAMP (RFC3339) is the
key press that asked for it: a seat the reload adds takes it as its
marks.json start, so the launch budget's 10 s ceiling counts from it;
every other seat keeps the squad's start.

scheduler clear is the screen's clear key (brief 31-2): the owner's
acknowledgement of a seat's warning line (its code, the text before the
colon) on the seat's current launch, sent over the same socket and proved
the same way. It is recorded as a warning_cleared decision and in the
scheduler directory's clears.jsonl; status then lists that warning under
the seat's acknowledged_warnings for that launch only. A clear is never a
gate and never changes a seat's state, and it is accepted only for a seat
warning that status shows for that seat on that launch now. Refusals:
clear_not_a_warning (the code is not a seat warning: a refusal is never
cleared), clear_not_showing (status does not show that warning for the
seat now), clear_launch_stale (the seat's current launch is not the one
named), clear_malformed; and the socket's: not_lead (the sender does not
descend from the lead), origin_unknown (the sender's origin cannot be read
or proved).
`

func runScheduler(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, schedulerUsage)
		return nil
	}
	switch args[0] {
	case "run":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return runSchedulerRun(ctx, args[1:], os.Stdout)
	case "relaunch":
		return runSchedulerRelaunch(args[1:], os.Stdout)
	case "clear":
		return runSchedulerClear(args[1:], os.Stdout)
	default:
		return unknownSubcommandError("scheduler", args[0], "run", "relaunch", "clear")
	}
}

// Seams for tests: the launcher of the AMQ path, the usage-limit source and
// the amq runner of the doorbell. nil uses the real ones.
var (
	schedulerAMQLauncher func(slots *schedproc.Slots, root string, opts seatlaunch.Options) scheduler.SeatLauncher
	schedulerLimits      scheduler.UsageLimitSource
	schedulerSendRun     func(ctx context.Context, amq string, args, env []string) ([]byte, error)
	schedulerStarted     func(*scheduler.Scheduler)
	schedulerOnTick      func([]scheduler.Decision)
)

func runSchedulerRun(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("scheduler run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	project := fs.String("project", "", "the repository whose squad.yaml is the squad")
	tick := fs.Duration("tick", schedproc.DefaultTick, "tick interval")
	maxSeats := fs.Int("max-seats", 0, "narrow the grant's seat cap")
	baseBranch := fs.String("base-branch", "", "branch new seat worktrees start from (default: the grant's, else main)")
	fs.Bool("open-pr", false, "no-op, kept for compatibility: the commit step binds each pushed seat branch's draft PR by default under a merge grant")
	noOpenPR := fs.Bool("no-open-pr", false, "the commit step opens and binds no PR (refused when the repository has a merge grant)")
	var leadPoolFlags multiFlag
	fs.Var(&leadPoolFlags, "lead-pool", "runtime:model:effort of the lead's own seat (repeatable)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if len(leadPoolFlags) > 0 && !startedByLeadTeam() {
		// The lead's team mode is the only starter that passes its own
		// pools (narrow pass, R2 (b)): anyone else could name a pool to
		// fill or free a concurrency cap.
		return usageErrorf("scheduler run --lead-pool: only the lead's team mode (amq-squad lead --team) passes the lead's pools")
	}
	var leadPools []task.Pool
	for _, s := range leadPoolFlags {
		p, err := task.ParsePool(s)
		if err != nil {
			return usageErrorf("scheduler run --lead-pool: %v", err)
		}
		leadPools = append(leadPools, p)
	}
	if *project == "" || fs.NArg() > 0 {
		return usageErrorf("scheduler run: --project DIR is required")
	}
	q, leadHandle, err := squadOfProject(*project)
	if err != nil {
		return fmt.Errorf("scheduler run: %w", err)
	}
	lead := &leadHandle
	if q.Wrapper, err = os.Executable(); err == nil {
		q.Wrapper, err = filepath.EvalSymlinks(q.Wrapper)
	}
	if err != nil {
		return fmt.Errorf("scheduler run: resolve the amq-squad executable: %w", err)
	}
	if q.AMQPath, err = exec.LookPath("amq"); err != nil {
		return fmt.Errorf("scheduler run: the installed amq is required on PATH (R51): %w", err)
	}
	if q, err = q.Normalize(); err != nil {
		return fmt.Errorf("scheduler run: %w", err)
	}
	root := q.SessionRoot()
	// An absent AMQ root or session is created the way `up` creates it
	// (brief 31-1, item 7: the missing-session refusal gave way to it).
	handles := []string{leadHandle}
	for _, s := range q.Seats {
		handles = append(handles, s.Handle)
	}
	if done, err := ensureSquadSession(ctx, q.AMQPath, q, handles); err != nil {
		return fmt.Errorf("scheduler run: create the session: %w", err)
	} else if len(done) > 0 {
		fmt.Fprintf(out, "scheduler run: created the session: %s\n", strings.Join(done, "; "))
		_ = schedproc.AppendDecision(root, time.Now(), scheduler.Decision{Kind: KindSessionCreated, Reason: "scheduler run: " + strings.Join(done, "; ")})
	}
	g, gerr := grants.Load(q.ProjectRoot)
	if gerr != nil {
		return fmt.Errorf("scheduler run: the grant: %w", gerr)
	}
	openPR, binding, err := commitStepOpensPR(g, *noOpenPR)
	if err != nil {
		return err
	}
	seatCap := leaseCap(g.MaxSeats, len(q.Seats), *maxSeats)
	if seatCap <= 0 {
		return fmt.Errorf("scheduler run: the grant admits no seat (max_seats %d%s)", g.MaxSeats, insecureNote(g))
	}
	home, _ := os.UserHomeDir()
	// The launch's record of the owner's grant for every seat's push (R2).
	// It never refuses the start (brief 31-1): the commit step checks the
	// target where the push happens.
	target, terr := seatcommit.LaunchTarget(g, home, seatcommit.ToolsDir(root))
	if terr != nil {
		fmt.Fprintf(out, "scheduler run: the push target: %v; the commit step refuses a push that needs it\n", terr)
	}
	slots := &schedproc.Slots{Squad: q, BaseBranch: firstOf(*baseBranch, target.BaseBranch), Target: target}
	// The roster (3.0.1 t16): every seat that can start launches in one
	// apply; a seat with no worktree of its own never started and is named.
	roster, excluded := slots.Roster(*lead)
	for _, why := range excluded {
		fmt.Fprintf(out, "scheduler run: not in the roster: %s\n", why)
	}
	if len(roster) == 0 {
		return fmt.Errorf("scheduler run: %s has no seat but the lead", team.SquadPath(q.ProjectRoot))
	}
	opts := seatlaunch.Options{Wait: 90 * time.Second, Target: &target, Start: launchStart()}
	var amqLauncher scheduler.SeatLauncher
	var real *seatlaunch.Launcher
	if schedulerAMQLauncher != nil {
		amqLauncher = schedulerAMQLauncher(slots, root, opts)
	} else {
		real = &seatlaunch.Launcher{Plan: slots.Plan, PlanRoster: slots.PlanRoster, Options: opts, Root: root}
		amqLauncher = real
	}
	// Every runtime goes to the AMQ launcher (brief 31-1): which runtimes
	// exist is AMQ's adapters' to say, at Prepare.
	// Every runtime squad.yaml takes, not only the ones the squad starts
	// with: a seat the relaunch's reload adds may bring a new one (brief
	// 31-2 PR 2).
	byRuntime := map[string]scheduler.SeatLauncher{}
	for _, rt := range team.Runtimes {
		byRuntime[rt] = amqLauncher
	}
	for _, s := range q.Seats {
		byRuntime[s.Runtime] = amqLauncher
	}
	sender := schedproc.Sender{AMQ: q.AMQPath, Root: root, BaseRoot: q.BaseRoot, Session: q.Session, Run: schedulerSendRun}
	store, err := openTaskStore(root) // the completion gate reads the grant (phase 5 re-pass)
	if err != nil {
		return err
	}
	// 2b-4's router (the merge checklist: --default-pool is gone), through
	// its start step: a routing.yaml that does not load is routing_invalid
	// (phase 5 re-pass, P2-8). Its concurrency caps count the scheduler's
	// own seat view, which exists once the scheduler does (ruling 2).
	reg, err := registry.Read(registry.Path(home))
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("scheduler run: the registry: %w", err)
	}
	var sched *scheduler.Scheduler
	router, err := routing.Open(routing.StartOptions{Home: home, RepoRoot: q.ProjectRoot, Root: root, Registry: reg, Grants: g.Grants,
		Tasks: func() ([]task.Task, error) { return task.Snapshot(store.Dir) },
		Seats: schedulerSeatView(&sched), LeadPools: leadPools, RosterPools: rosterPools(roster)})
	if err != nil {
		return fmt.Errorf("scheduler run: the router: %w", err)
	}
	sel := storeSelection{store: store, root: root, release: func() {}}
	if c, err := schedulerContext(q); err == nil {
		sel.ctx = &c
	}
	committer := &schedproc.Committer{Root: root, Step: func(ctx context.Context, handle string, t task.Task) seatcommit.Result {
		return commitStep(ctx, sel, handle, t, false, openPR)
	}, ExitStep: func(handle, taskID, why string) seatcommit.Result {
		return seatcommit.ExitCheck(seatcommit.Options{Root: root, Handle: handle, Task: task.Task{ID: taskID}, Now: taskNow}, why)
	}}
	limits := schedulerLimits
	if limits == nil {
		limits = schedproc.Limits{Home: usagelimit.ForHome(home, os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME")), Root: root}
	}
	fmt.Fprintf(out, "scheduler for %s (session %s, lead %s, roster of %d seats, at most %d leases at once, tick %s, PR binding %s); decisions in %s\n", root, q.Session, *lead, len(roster), seatCap, *tick, binding, filepath.Join(schedproc.Dir(root), "decisions.jsonl"))
	// A team-mode start (3.0.1 t16): the lead is this process's parent, its
	// incarnation recorded now; the scheduler exits when it is gone.
	decisions := decisionOptions(sel, q.ProjectRoot)
	// brief 31-2 PR 2: each relaunch compiles squad.yaml again; the swap
	// writes the slots' squad and the router's roster pools with the roster
	decisions.Reload = &schedproc.Reload{Compile: reloadSquad(q, leadHandle), Cap: reloadCap(*maxSeats), Slots: slots, Pools: router, Lead: leadHandle}
	leadGone, err := teamModeLead(decisions)
	if err != nil {
		return err
	}
	err = schedproc.Run(ctx, schedproc.Options{
		Root:        root,
		Session:     q.Session,
		ProjectRoot: q.ProjectRoot,
		Start:       launchStart(),
		// R4: the lead's decisions over the scheduler's own socket.
		Decisions: decisions,
		LeadState: leadGone,
		Exits:     committer,
		// The per-tick liveness of every roster seat from the launch ledger
		// (3.0.1 t17): a seat whose process ended is gone within one tick.
		Liveness: &schedproc.Liveness{Root: root, ReadBound: schedproc.ProofReadBound(*tick, len(roster))},
		Launcher: &schedproc.Launcher{Slots: slots, ByRuntime: byRuntime},
		// 3.0.2 t23: each task on its own branch from the base tip fetched at
		// the grant; the doorbell moves the seat's worktree there first.
		Doorbell:  schedproc.Doorbell{Sender: sender, Start: slots.TaskStart(root, taskNow)},
		Lead:      schedproc.LeadNotice{Sender: sender, Lead: *lead},
		Router:    router, // the process calls its Flush each tick
		Verifiers: store.Verifiers,
		Committer: committer,
		Preparer:  &schedproc.CleanSeats{Root: root},
		Limits:    limits,
		Config: scheduler.Config{MaxSeats: seatCap, Lead: *lead, Roster: roster,
			// the launch budget's second mark (brief 31-1): each launched
			// seat's first AMQ message, from its outbox
			FirstMessage: firstMessageMark(root)},
		Tick: *tick,
		Started: func(s *scheduler.Scheduler) {
			sched = s // before the first tick: the router's seat view
			if real != nil {
				real.Post = s.Post
			}
			if schedulerStarted != nil {
				schedulerStarted(s)
			}
		},
		OnTick: schedulerOnTick,
	})
	if real != nil {
		real.Wait()
	}
	if errors.Is(err, schedproc.ErrRunning) {
		// Another scheduler holds the squad's lock: a refusal, attempted
		// once (brief 31-1), never a crash the lead's supervisor restarts.
		return &codedExitError{code: ExitSchedulerRefused, msg: fmt.Sprintf("scheduler run: %v (session root %s)", err, root)}
	}
	var refused *scheduler.RosterLaunchError
	if errors.As(err, &refused) && refused.Stage != "launch_error" {
		// A refused roster launch is the owner's decision to make, not a
		// crash: a distinct exit, which the lead's supervisor does not
		// restart (3.0.1 t16).
		return &codedExitError{code: ExitSchedulerRefused, msg: rosterRefusal(refused)}
	}
	return err
}

// rosterRefusal is the printed reason of a refused roster launch.
func rosterRefusal(e *scheduler.RosterLaunchError) string {
	return "scheduler run: the roster launch was refused at start: " + e.Error()
}

// rosterPools are the roster seats' pools: the router admits each without
// a registry entry or a table chain (brief 31-1).
func rosterPools(roster []scheduler.RosterSeat) []task.Pool {
	var out []task.Pool
	for _, rs := range roster {
		out = append(out, rs.Pool)
	}
	return out
}

// firstMessageMark finds a launched seat's first AMQ message in its outbox
// and records it in the seat's marks (seatlaunch.RecordFirstMessage), where
// status reads it; the scheduler logs seat_first_message once it is found.
func firstMessageMark(root string) func(string, time.Time) (time.Time, bool) {
	find := scheduler.OutboxFirstMessage(root)
	return func(handle string, since time.Time) (time.Time, bool) {
		at, ok := find(handle, since)
		if ok {
			if err := seatlaunch.RecordFirstMessage(root, handle, at); err != nil {
				return time.Time{}, false // asked again next tick
			}
		}
		return at, ok
	}
}

// leaseCap is the leases held at once: the grant's max_seats, bounded by
// the squad's seat count (a lease needs a seat), narrowed by --max-seats.
func leaseCap(grantMax, seats, narrow int) int {
	c := min(grantMax, seats)
	if narrow > 0 && narrow < c {
		c = narrow
	}
	return c
}

// reloadCap is the reload's lease cap (brief 31-2 PR 2): the grant read
// again (the owner's file narrowed by the squad.yaml just compiled) bounding
// the new squad's seat count, with the start's --max-seats narrowing. The
// owner's grant is never exceeded; a cap of none is squad_invalid.
func reloadCap(narrow int) func(seatlaunch.Squad) (int, error) {
	return func(q seatlaunch.Squad) (int, error) {
		g, err := grants.Load(q.ProjectRoot)
		if err != nil {
			return 0, fmt.Errorf("the grant: %w", err)
		}
		c := leaseCap(g.MaxSeats, len(q.Seats), narrow)
		if c <= 0 {
			return 0, fmt.Errorf("the grant admits no seat (max_seats %d%s)", g.MaxSeats, insecureNote(g))
		}
		return c, nil
	}
}

// reloadSquad is the relaunch's reload seam (brief 31-2 PR 2): squad.yaml
// compiled again exactly as at the start (squadOfProject: team.LoadSquad,
// Squad.LaunchSeats, roleText), with the running squad's wrapper and amq.
// A squad.yaml that names another session or lead is refused: the session
// and the lead are the scheduler's for its life.
func reloadSquad(running seatlaunch.Squad, lead string) func() (seatlaunch.Squad, error) {
	return func() (seatlaunch.Squad, error) {
		q, l, err := squadOfProject(running.ProjectRoot)
		if err != nil {
			return seatlaunch.Squad{}, err
		}
		if q.Session != running.Session || l != lead {
			return seatlaunch.Squad{}, fmt.Errorf("%s names session %s and lead %s; the running scheduler's are %s and %s (a relaunch keeps both: restart the squad to change them)",
				team.SquadPath(running.ProjectRoot), q.Session, l, running.Session, lead)
		}
		q.Wrapper, q.AMQPath = running.Wrapper, running.AMQPath
		return q.Normalize()
	}
}

// launchStart is the start of this squad's launch from AMQ_SQUAD_START
// (brief 31-1), zero when it is unset or malformed.
func launchStart() time.Time {
	t, err := time.Parse(time.RFC3339Nano, os.Getenv(leadStartEnv))
	if err != nil {
		return time.Time{}
	}
	return t
}

// squadOfProject is the squad of a repository's squad.yaml (brief 31-1: the
// one source): its seats as the typed seat list (team.Squad.LaunchSeats,
// each role's text through roleText), its session, the repository's team
// rules when present, and its lead's handle.
func squadOfProject(project string) (seatlaunch.Squad, string, error) {
	dir, err := filepath.Abs(project)
	if err != nil {
		return seatlaunch.Squad{}, "", err
	}
	s, err := team.LoadSquad(dir)
	if err != nil {
		return seatlaunch.Squad{}, "", err
	}
	src := team.SquadPath(dir)
	if strings.TrimSpace(s.Lead) == "" {
		return seatlaunch.Squad{}, "", fmt.Errorf("%s names no lead (lead: <seat handle>)", src)
	}
	if s.Session == "" {
		return seatlaunch.Squad{}, "", fmt.Errorf("%s names no session (session: <name>)", src)
	}
	seats, err := s.LaunchSeats(dir, func(id string) (string, error) { return roleText(dir, id) })
	if err != nil {
		return seatlaunch.Squad{}, "", err
	}
	q := seatlaunch.Squad{ProjectRoot: dir, Session: s.Session, Seats: seats}
	if p := rules.Path(dir); p != "" {
		if _, err := os.Stat(p); err == nil {
			q.RulesPath = p
		}
	}
	return q, s.Lead, nil
}

// schedulerSeatView is the router's seat view in scheduler run: the
// scheduler's Occupancy once it exists (bound at Started, before the first
// tick), nothing before.
func schedulerSeatView(sched **scheduler.Scheduler) func() []scheduler.SeatView {
	return func() []scheduler.SeatView {
		if *sched == nil {
			return nil
		}
		return (*sched).Occupancy()
	}
}

// commitStepOpensPR is the commit step's PR choice (3.0.1 t14): with a
// merge grant for the squad's repository (the push target the launch
// records, seatcommit.TargetFor) the step opens (or finds) each pushed seat
// branch's draft PR and binds it to the task through task.Store.BindPR, by
// default, because the merge train refuses an unbound task (task_unbound).
// --no-open-pr is the off-switch only without a merge grant. It also
// returns the choice as the start line prints it.
func commitStepOpensPR(g grants.Resolved, off bool) (bool, string, error) {
	repo := seatcommit.TargetFor(g).Repository
	switch {
	case repo != "" && off:
		return false, "", usageErrorf("scheduler run --no-open-pr: refused: %s has a merge grant, and the merge train needs each completed task bound to its PR", repo)
	case repo != "":
		return true, "on (merge grant " + repo + ")", nil
	case off:
		return false, "off (--no-open-pr)", nil
	}
	return false, "off (no merge grant)", nil
}

func insecureNote(g grants.Resolved) string {
	if g.Insecure {
		return "; the owner grants file is insecure: " + g.InsecureWhy
	}
	return ""
}

// schedulerContext resolves the squad's context (the gate namespace of the
// commit step's growth gates); without it no growth gate is accepted.
func schedulerContext(q seatlaunch.Squad) (contextResolution, error) {
	return resolveCanonicalContext(contextResolveOptions{ProjectFlag: q.ProjectRoot, ProfileFlag: q.Profile, SessionFlag: q.Session,
		ProjectExplicit: true, ProfileExplicit: q.Profile != "", SessionExplicit: true})
}

// schedulerDecideSeams adjusts the decision socket's options (a test seam:
// the pane listing and the chain walk).
var schedulerDecideSeams func(*schedproc.DecideOptions)

// schedulerLeadParent is the scheduler's parent process and its raw start
// stamp (a test seam).
var schedulerLeadParent = func() (int, string, bool) {
	pid := os.Getppid()
	start, ok := procinfo.StartToken(pid)
	return pid, start, ok
}

// decisionOptions is the decision socket of scheduler run: the widening as
// the lead's decision (R4). Its lead is the one the launch ledger recorded;
// runSchedulerRun replaces it with the scheduler's own parent for a
// team-mode start (schedproc.SupervisingLead, 3.0.1 t16).
func decisionOptions(sel storeSelection, projectRoot string) *schedproc.DecideOptions {
	d := &schedproc.DecideOptions{Apply: func(lead string, req schedproc.WidenRequest) schedproc.DecideReply {
		return widenDecision(sel, projectRoot, lead, req)
	}, Warnings: status.CurrentSeatWarnings} // brief 31-2: a clear names a warning status shows
	if schedulerDecideSeams != nil {
		schedulerDecideSeams(d)
	}
	return d
}

// teamModeLead wires a team-mode start's lead (3.0.1 t16): when the lead's
// team mode started this scheduler, the decision socket's lead is this
// process's parent, its incarnation (pid and raw start stamp) recorded now,
// and the returned check reports it gone, which ends the scheduler. Any
// other start keeps the launch ledger's lead and no check (nil).
func teamModeLead(d *schedproc.DecideOptions) (func() (schedproc.LeadStatus, string), error) {
	if !startedByLeadTeam() {
		return nil, nil
	}
	pid, start, ok := schedulerLeadParent()
	if !ok {
		return nil, fmt.Errorf("scheduler run: the start stamp of the team-mode lead (pid %d) cannot be read; the lead's decisions could not be proved", pid)
	}
	d.LeadPane = schedproc.SupervisingLead(pid, start, procinfo.StartToken, procinfo.Alive)
	return func() (schedproc.LeadStatus, string) {
		return schedproc.LeadState(pid, start, procinfo.StartToken, procinfo.Alive)
	}, nil
}

// schedulerRelaunchSend sends the relaunch over the decision socket (a test
// seam).
var schedulerRelaunchSend = schedproc.SendRelaunch

// runSchedulerRelaunch is the lead's relaunch of the roster (3.0.1 t16): a
// replacement for a gone seat, since AMQ refuses a fresh seat into the
// session's present binding until it can join one (ask 20).
func runSchedulerRelaunch(args []string, out io.Writer) error {
	f := newStoreFlags("scheduler relaunch")
	f.fs.Usage = func() { fmt.Fprint(os.Stderr, schedulerUsage) }
	reason := f.fs.String("reason", "", "why the lead relaunches the roster")
	start := f.fs.String("start", "", "the key press that asked for the relaunch (RFC3339): a seat the reload adds counts its launch budget from it")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if f.fs.NArg() > 0 {
		return usageErrorf("scheduler relaunch: takes no arguments")
	}
	stamp := strings.TrimSpace(*start)
	if stamp != "" {
		t, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil {
			return usageErrorf("scheduler relaunch --start: %q is not an RFC3339 stamp", stamp)
		}
		stamp = t.UTC().Format(time.RFC3339Nano)
	}
	if flagWasSet(f.fs, "me") {
		return usageErrorf("scheduler relaunch: takes no --me; the scheduler proves the lead from the process, never from a flag")
	}
	_, root, release, err := f.store(false)
	if err != nil {
		return err
	}
	release()
	rec, err := schedproc.ReadLaunchRecord(root)
	if err != nil {
		return fmt.Errorf("scheduler relaunch: no running scheduler for %s (the relaunch is the scheduler's to run): %v", root, err)
	}
	var reply schedproc.DecideReply
	if rec.DecisionSocket == "" {
		reply = schedproc.DecideReply{Code: schedproc.RefusedStarting, Detail: "the scheduler is starting: its decision socket opens once the exits ledger is loaded and every seat of the previous incarnation is closed and confirmed gone"}
	} else if reply, err = schedulerRelaunchSend(root, rec.DecisionSocket, schedproc.RelaunchRequest{Reason: strings.TrimSpace(*reason), Start: stamp}); err != nil {
		return fmt.Errorf("scheduler relaunch: the scheduler's decision socket: %w", err)
	}
	if *f.jsonOut {
		if err := writeJSONEnvelope(out, "scheduler_relaunch", reply); err != nil {
			return err
		}
	} else if reply.OK {
		fmt.Fprintf(out, "scheduler relaunch: %s\n", reply.Detail)
	}
	if !reply.OK {
		return &codedExitError{code: ExitCommitRefused, msg: fmt.Sprintf("scheduler relaunch refused (%s): %s", reply.Code, reply.Detail)}
	}
	return nil
}
