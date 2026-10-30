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
	"github.com/omriariav/amq-squad/v3/internal/registry"
	"github.com/omriariav/amq-squad/v3/internal/routing"
	"github.com/omriariav/amq-squad/v3/internal/schedproc"
	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
	"github.com/omriariav/amq-squad/v3/internal/seatlaunch"
	"github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/usagelimit"
)

const schedulerUsage = `amq-squad scheduler - the squad's scheduler process

Usage:
  amq-squad scheduler run --seats FILE --lead HANDLE [--lead-pool r:m:e]... [--tick 2s] [--trust] [--max-seats N] [--base-branch B]
  amq-squad scheduler relaunch [--root DIR] [--reason TEXT] [--json]

scheduler run is one squad's scheduler process (plan 4.5, 16.5; started and
supervised by the lead's team mode). It ticks the deterministic scheduler
over the squad's task store and wires its hooks: the roster, every seat of
the typed seat list FILE (the launch --seats format) with a worktree of its
own, starts at once through the AMQ launch API in one apply (pi seats too,
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
touches anything. The preflight copies of the owner's global agent config
(the baseline of the exit check) are filled at admission when missing and
never refreshed by the scheduler: the baseline persists across restarts,
and only the owner's seat clear refreshes it. The exit check runs on every
terminal path of a seat (done, failed, gone, expired); a seat whose claimed
lease expired is closed through the launcher before it is cleaned or
dispatched to again. The lead's widening arrives as the lead's AMQ message
(task widen): the scheduler applies it only from the lead of its launch
record.

--lead-pool names the pool of the lead's own seat (repeatable; the lead's
team mode passes its models, and only a process the lead's team mode started
may pass it): a concurrency cap counts the lead's seat when it shares the
pool, as a pi lead on the Ollama GPU does. A routing.yaml that
does not load refuses the start with routing_invalid.

--trust is the owner's standing answer trust_exact_subject to AMQ's
trust_confirmation for this squad's launches; without it a launch that needs
it fails and is reported. --max-seats only narrows the grant.

scheduler relaunch is the lead's decision to relaunch the roster, sent over
the scheduler's decision socket (proved like task widen): the seats finish
what they hold, every seat closes, the roster launches again in one apply,
and the tasks blocked seat_replacement_needs_relaunch return to pending.
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
	default:
		return unknownSubcommandError("scheduler", args[0], "run", "relaunch")
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
	seats := fs.String("seats", "", "the squad's typed seat list (JSON)")
	lead := fs.String("lead", "", "the lead's handle (refusals go to it; only it widens)")
	tick := fs.Duration("tick", schedproc.DefaultTick, "tick interval")
	trust := fs.Bool("trust", false, "the owner's standing trust of this squad's launch subjects")
	maxSeats := fs.Int("max-seats", 0, "narrow the grant's seat cap")
	baseBranch := fs.String("base-branch", "", "branch new seat worktrees start from (default: the grant's, else main)")
	openPR := fs.Bool("open-pr", false, "the commit step opens (or finds) each pushed seat branch's draft PR and binds it to the task")
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
	if *seats == "" || *lead == "" || fs.NArg() > 0 {
		return usageErrorf("scheduler run: --seats FILE and --lead HANDLE are required")
	}
	q, err := readLaunchSeats(*seats)
	if err != nil {
		return err
	}
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
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return fmt.Errorf("scheduler run: session root %s does not exist; create the session first (amq session create %s --root %s --me %s)", root, q.Session, q.BaseRoot, q.Operator)
	}
	g, gerr := grants.Load(q.ProjectRoot)
	if gerr != nil {
		return fmt.Errorf("scheduler run: the grant: %w", gerr)
	}
	seatCap := min(g.MaxSeats, len(q.Seats)) // a lease needs a seat of the roster
	if *maxSeats > 0 && *maxSeats < seatCap {
		seatCap = *maxSeats
	}
	if seatCap <= 0 {
		return fmt.Errorf("scheduler run: the grant admits no seat (max_seats %d%s)", g.MaxSeats, insecureNote(g))
	}
	home, _ := os.UserHomeDir()
	// The launch's record of the owner's grant for every seat's push (R2).
	target, err := seatcommit.LaunchTarget(g, home, seatcommit.ToolsDir(root))
	if err != nil {
		return fmt.Errorf("scheduler run: the push target: %w", err)
	}
	slots := &schedproc.Slots{Squad: q, BaseBranch: firstOf(*baseBranch, target.BaseBranch), Target: target}
	// The roster (3.0.1 t16): every seat that can start launches in one
	// apply; a seat with no worktree of its own never started and is named.
	roster, excluded := slots.Roster()
	for _, why := range excluded {
		fmt.Fprintf(out, "scheduler run: not in the roster: %s\n", why)
	}
	if len(roster) == 0 {
		return fmt.Errorf("scheduler run: the seat list %s has no seat that can start", *seats)
	}
	opts := seatlaunch.Options{KeepPreflight: true, Wait: 90 * time.Second, Target: &target}
	if *trust {
		opts.Standing = map[string]string{"trust_confirmation": "trust_exact_subject"}
	}
	var amqLauncher scheduler.SeatLauncher
	var real *seatlaunch.Launcher
	if schedulerAMQLauncher != nil {
		amqLauncher = schedulerAMQLauncher(slots, root, opts)
	} else {
		real = &seatlaunch.Launcher{Plan: slots.Plan, PlanRoster: slots.PlanRoster, Options: opts, Root: root}
		amqLauncher = real
	}
	byRuntime := map[string]scheduler.SeatLauncher{"claude": amqLauncher, "codex": amqLauncher, "pi": amqLauncher}
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
		Seats: schedulerSeatView(&sched), LeadPools: leadPools})
	if err != nil {
		return fmt.Errorf("scheduler run: the router: %w", err)
	}
	sel := storeSelection{store: store, root: root, release: func() {}}
	if c, err := schedulerContext(q); err == nil {
		sel.ctx = &c
	}
	committer := &schedproc.Committer{Root: root, Step: func(ctx context.Context, handle string, t task.Task) seatcommit.Result {
		return commitStep(ctx, sel, handle, t, false, *openPR)
	}, ExitStep: func(handle, taskID, why string) seatcommit.Result {
		return seatcommit.ExitCheck(seatcommit.Options{Root: root, Handle: handle, Task: task.Task{ID: taskID}, Now: taskNow, Clear: ownerTrust{Root: root}}, why)
	}}
	limits := schedulerLimits
	if limits == nil {
		limits = schedproc.Limits{Home: usagelimit.ForHome(home, os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME")), Root: root}
	}
	fmt.Fprintf(out, "scheduler for %s (session %s, lead %s, roster of %d seats, at most %d leases at once, tick %s); decisions in %s\n", root, q.Session, *lead, len(roster), seatCap, *tick, filepath.Join(schedproc.Dir(root), "decisions.jsonl"))
	err = schedproc.Run(ctx, schedproc.Options{
		Root:        root,
		Session:     q.Session,
		ProjectRoot: q.ProjectRoot,
		// P1 of the milestone B first pass: the baseline after admission, so a
		// rejected duplicate process never touches it; filled when missing,
		// never refreshed (it persists across restarts).
		Admitted: func() error {
			if err := seatexec.WritePreflight(home, false); err != nil {
				return fmt.Errorf("scheduler run: the preflight copies: %w", err)
			}
			return nil
		},
		// R4: the lead's decisions over the scheduler's own socket.
		Decisions: decisionOptions(sel, q.ProjectRoot),
		Exits:     committer,
		Launcher:  &schedproc.Launcher{Slots: slots, ByRuntime: byRuntime},
		Doorbell:  schedproc.Doorbell{Sender: sender},
		Lead:      schedproc.LeadNotice{Sender: sender, Lead: *lead},
		Router:    router, // the process calls its Flush each tick
		Verifiers: store.Verifiers,
		Committer: committer,
		Preparer:  &schedproc.CleanSeats{Root: root, Clear: ownerTrust{Root: root}},
		Limits:    limits,
		Config:    scheduler.Config{MaxSeats: seatCap, Lead: *lead, Roster: roster},
		Tick:      *tick,
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
		return fmt.Errorf("scheduler run: %w (session root %s)", err, root)
	}
	return err
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

// decisionOptions is the decision socket of scheduler run: the widening as
// the lead's decision (R4).
func decisionOptions(sel storeSelection, projectRoot string) *schedproc.DecideOptions {
	d := &schedproc.DecideOptions{Apply: func(lead string, req schedproc.WidenRequest) schedproc.DecideReply {
		return widenDecision(sel, projectRoot, lead, req)
	}}
	if schedulerDecideSeams != nil {
		schedulerDecideSeams(d)
	}
	return d
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
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if f.fs.NArg() > 0 {
		return usageErrorf("scheduler relaunch: takes no arguments")
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
	} else if reply, err = schedulerRelaunchSend(root, rec.DecisionSocket, schedproc.RelaunchRequest{Reason: strings.TrimSpace(*reason)}); err != nil {
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
