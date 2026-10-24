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
  amq-squad scheduler run --seats FILE --lead HANDLE [--tick 2s] [--trust] [--max-seats N] [--base-branch B]
  amq-squad scheduler post --root DIR --kind seat_up|seat_gone|seat_activity --handle H [--request-id ID] [--pool r:m:e]

scheduler run is one squad's scheduler process (plan 4.5, 16.5; started and
supervised by the lead's team mode). It ticks the deterministic scheduler
over the squad's task store and wires its hooks: seats start through the AMQ
launch API from the typed seat list FILE (the launch --seats format; each
seat is a slot the scheduler starts on demand for its pool); a seat that AMQ
cannot launch yet (pi on AMQ 0.107.0) goes to an external runner through
request files (the stand-in launcher); the doorbell is an AMQ message from
the "scheduler" mailbox, which the seat's AMQ wake delivers; the router is
the built-in DefaultRouter until 2b-4; a done report runs the harness commit
step (seat commit); a refusal is recorded, then the lead is told by AMQ
message; the usage-limit detector stops new seats after the first event in
the window. The preflight copies of the owner's global agent config are
refreshed once at start, never per seat. One process per squad.

--trust is the owner's standing answer trust_exact_subject to AMQ's
trust_confirmation for this squad's launches; without it a launch that needs
it fails and is reported. --max-seats only narrows the grant.

scheduler post is how an external runner reports a seat it started (seat_up
with the request id from the request file), ended, or saw active.
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
	case "post":
		return runSchedulerPost(args[1:])
	default:
		return unknownSubcommandError("scheduler", args[0], "run", "post")
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
	if err := parseFlags(fs, args); err != nil {
		return err
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
	seatCap := g.MaxSeats
	if *maxSeats > 0 && *maxSeats < seatCap {
		seatCap = *maxSeats
	}
	if seatCap <= 0 {
		return fmt.Errorf("scheduler run: the grant admits no seat (max_seats %d%s)", g.MaxSeats, insecureNote(g))
	}
	home, _ := os.UserHomeDir()
	if err := seatexec.WritePreflight(home, true); err != nil {
		return fmt.Errorf("scheduler run: the preflight copies: %w", err)
	}
	slots := &schedproc.Slots{Squad: q, BaseBranch: firstOf(*baseBranch, firstOf(g.DefaultBranch, "main"))}
	opts := seatlaunch.Options{KeepPreflight: true, Wait: 90 * time.Second}
	if *trust {
		opts.Standing = map[string]string{"trust_confirmation": "trust_exact_subject"}
	}
	var amqLauncher scheduler.SeatLauncher
	var real *seatlaunch.Launcher
	if schedulerAMQLauncher != nil {
		amqLauncher = schedulerAMQLauncher(slots, root, opts)
	} else {
		real = &seatlaunch.Launcher{Plan: slots.Plan, Options: opts, Root: root}
		amqLauncher = real
	}
	byRuntime := map[string]scheduler.SeatLauncher{"claude": amqLauncher, "codex": amqLauncher, "pi": amqLauncher}
	if !seatlaunch.PiAdapterInLibrary && schedulerAMQLauncher == nil {
		byRuntime["pi"] = schedproc.External{Slots: slots, Root: root}
	}
	sender := schedproc.Sender{AMQ: q.AMQPath, Root: root, BaseRoot: q.BaseRoot, Session: q.Session, Run: schedulerSendRun}
	store, err := task.Open(root)
	if err != nil {
		return err
	}
	sel := storeSelection{store: store, root: root, release: func() {}}
	if c, err := schedulerContext(q); err == nil {
		sel.ctx = &c
	}
	committer := &schedproc.Committer{Step: func(handle string, t task.Task) seatcommit.Result {
		return commitStep(sel, handle, t, false)
	}}
	limits := schedulerLimits
	if limits == nil {
		limits = usagelimit.ForHome(home, os.Getenv("CLAUDE_CONFIG_DIR"), os.Getenv("CODEX_HOME"))
	}
	fmt.Fprintf(out, "scheduler for %s (session %s, lead %s, max %d seats, tick %s); decisions in %s\n", root, q.Session, *lead, seatCap, *tick, filepath.Join(schedproc.Dir(root), "decisions.jsonl"))
	err = schedproc.Run(ctx, schedproc.Options{
		Root:      root,
		Launcher:  &schedproc.Launcher{Slots: slots, ByRuntime: byRuntime},
		Doorbell:  schedproc.Doorbell{Sender: sender},
		Lead:      schedproc.LeadNotice{Sender: sender, Lead: *lead},
		Router:    scheduler.DefaultRouter{}, // 2b-4: routing.New(...) here; the process calls its Flush each tick
		Committer: committer,
		Limits:    limits,
		Config:    scheduler.Config{MaxSeats: seatCap, Lead: *lead},
		Tick:      *tick,
		Started: func(s *scheduler.Scheduler) {
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

func runSchedulerPost(args []string) error {
	fs := flag.NewFlagSet("scheduler post", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	root := fs.String("root", "", "the session root (default AM_ROOT)")
	kind := fs.String("kind", "", "seat_up, seat_gone or seat_activity")
	handle := fs.String("handle", "", "the seat")
	reqID := fs.String("request-id", "", "the request id from the request file (seat_up)")
	pool := fs.String("pool", "", "runtime:model:effort (seat_up)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	r := firstOf(strings.TrimSpace(*root), strings.TrimSpace(os.Getenv("AM_ROOT")))
	if r == "" || !filepath.IsAbs(r) {
		return usageErrorf("scheduler post: --root (absolute) or AM_ROOT is required")
	}
	ev := schedproc.Event{Kind: *kind, Handle: *handle, RequestID: *reqID}
	if *pool != "" {
		p, err := task.ParsePool(*pool)
		if err != nil {
			return usageErrorf("scheduler post --pool: %v", err)
		}
		ev.Pool = p
	}
	return schedproc.PostEvent(r, ev)
}
