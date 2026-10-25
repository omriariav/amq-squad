package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/claudeseat"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
	"github.com/omriariav/amq-squad/v3/internal/seatexec"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Exit codes of the commit step and the widening.
const (
	ExitCommitRefused  = 15 // the core refused the diff or the widening; the task records the reason
	ExitCommitDeferred = 16 // the step could not run to a decision (git, push); nothing was recorded
)

const seatUsage = `amq-squad seat - the harness steps for one seat

Usage:
  amq-squad seat commit --seat H [--root DIR | --project P --session S] [--base SHA] [--open-pr] [--dry-run] [--json]
  amq-squad seat clear --seat H [--root DIR | --project P --session S] [--json]

seat commit is the harness-side commit step (plan 4.3, amendment 21). The
scheduler runs it on a seat's done report; the lead may run it by hand. It
takes the seat worktree's tree, checks every changed path against the
task's declared_files, the grant's protected paths and the size cap, diffs
the owner's global agent config against the preflight copy (amendment 28),
and only then commits under the harness identity and pushes the seat
branch (a fast-forward; never a force push). A refusal blocks the task with
its reason code, routed to the lead (task widen is the lead's path), or to
the owner for a global config change. The seat never commits and never
widens its own set.

--base SHA records the seat's base commit when the core has none (a lead
decision; the launch records it otherwise). --dry-run checks and records
nothing. --open-pr opens (or finds) the pushed seat branch's draft PR with gh
and binds the task to it: pr_number and pr_head in the task record, the
binding 3-2's merge train reads (it never takes a request-supplied task id).
Exit 15: refused; exit 16: deferred (nothing recorded).

A global config change (global_config_changed, routed to the owner) blocks
before the commit step: the seat's tree is kept uncommitted on the commit
ledger with the config diff as evidence, and the seat is quarantined (no new
lease) and closed; a Claude seat whose config dir changed during its run
(claude_config_changed) is quarantined the same way. seat clear is the
owner's clear: it runs only from the owner's own terminal (it refuses when
AM_ME or a seat or lead marker is set, or when the process descends from a
seat pane), the preflight copies are refreshed to the current config, the
clear is written to the ledger signed with the core's issuer key (the commit
step accepts only a clear its trust store verifies), the quarantine is
lifted, and the blocked task returns to the commit step, which commits only
the kept tree. An open PR on the seat branch that is not this task's harness
PR refuses with pr_bound_elsewhere.
`

func runSeat(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(os.Stderr, seatUsage)
		return nil
	}
	switch args[0] {
	case "commit":
		return runSeatCommit(args[1:], os.Stdout)
	case "clear":
		return runSeatClear(args[1:], os.Stdout)
	default:
		return unknownSubcommandError("seat", args[0], "commit", "clear")
	}
}

// seatClearData is the JSON envelope of seat clear.
type seatClearData struct {
	Root      string                       `json:"root"`
	Seat      string                       `json:"seat"`
	Cleared   *task.Quarantine             `json:"cleared,omitempty"`
	Evidence  *seatcommit.LedgerQuarantine `json:"evidence,omitempty"`
	Rearmed   *task.Task                   `json:"rearmed,omitempty"`
	Preflight string                       `json:"preflight"`
}

// runSeatClear is the owner's clear of a config-change quarantine
// (global_config_changed, claude_config_changed): the owner has inspected
// the config diff (kept on the ledger), so the preflight copies are
// refreshed to the current config, the clear is recorded on the ledger
// signed with the core's issuer key, the seat may get leases again, and the
// blocked task returns to the commit step, which commits only the kept
// tree. It runs only from the owner's terminal (R1): it takes no --me and
// refuses when AM_ME or a seat or lead marker is set, or when the process
// descends from a seat pane.
func runSeatClear(args []string, out io.Writer) error {
	f := newStoreFlags("seat clear")
	f.fs.Usage = func() { fmt.Fprint(os.Stderr, seatUsage) }
	seat := f.fs.String("seat", "", "the quarantined seat")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*seat) == "" || f.fs.NArg() > 0 {
		return usageErrorf("seat clear: --seat HANDLE is required and takes no positional arguments")
	}
	if flagWasSet(f.fs, "me") {
		return usageErrorf("seat clear: takes no --me; the owner is proved by the terminal it runs from, never named")
	}
	if err := requireOwnerTerminal("seat clear", *seat); err != nil {
		return err
	}
	mode, err := clearModeInForce()
	if err != nil {
		return fmt.Errorf("seat clear: %w", err)
	}
	signer, err := operatorauth.LoadEd25519Signer(seatClearIssuerKey())
	if err != nil {
		return fmt.Errorf("seat clear: the clear is signed with the core's issuer key (enroll it with amq-squad remote): %w", err)
	}
	defer signer.Destroy()
	sel, err := f.selection(true)
	if err != nil {
		return err
	}
	defer sel.release()
	home, _ := os.UserHomeDir()
	data := seatClearData{Root: sel.root, Seat: *seat, Preflight: seatexec.PreflightDir(home)}
	// The pty is the request half. In required mode nothing below it runs
	// (not even the preflight refresh) until the owner's signed Buzz answer
	// to the request verifies (clear_unsigned otherwise; the quarantine stays).
	var gate *gatedClear
	if mode == grants.ClearRequired {
		if gate, err = requestClear(context.Background(), sel, f, *seat, out); err != nil {
			return err
		}
	}
	if err := seatexec.WritePreflight(home, true); err != nil {
		return fmt.Errorf("seat clear: refresh the preflight copies: %w", err)
	}
	var authorize func(*seatcommit.LedgerQuarantine) (seatcommit.ClearGate, error)
	if gate != nil {
		authorize = gate.authorize
	}
	var seed *seatcommit.LedgerQuarantine
	if gate != nil {
		seed = gate.id.seed
	}
	if l, err := seatcommit.ClearQuarantineSeeded(sel.root, *seat, signer, taskNow(), seed, authorize); err == nil {
		data.Evidence = l.Quarantine
	} else if errors.Is(err, seatcommit.ErrAlreadyCleared) {
		return fmt.Errorf("seat clear: %w: another clear of seat %s lifted this quarantine first; nothing was done by this run", seatcommit.ErrAlreadyCleared, *seat)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("seat clear: %w", err)
	}
	if data.Cleared, err = sel.store.ClearQuarantine(*seat); err != nil {
		return fmt.Errorf("seat clear: %w", err)
	}
	list, err := sel.store.List()
	if err != nil {
		return err
	}
	for _, t := range list {
		quarantined := t.BlockReason == seatcommit.ReasonGlobalConfig || t.BlockReason == seatcommit.ReasonClaudeConfig || t.BlockReason == claudeseat.HooksSilent || t.BlockReason == seatcommit.ReasonClearUnsigned
		if t.State == task.StateBlocked && t.Report != nil && t.Report.By == *seat && quarantined {
			after, err := sel.store.Rearm(t.ID, taskLeaseDuration, taskNow())
			if err != nil {
				return fmt.Errorf("seat clear: re-arm %s: %w", t.ID, err)
			}
			data.Rearmed = &after
		}
	}
	if *f.jsonOut {
		return writeJSONEnvelope(out, "seat_clear", data)
	}
	fmt.Fprintf(out, "seat %s cleared (signed by %s); preflight copies refreshed under %s\n", *seat, signer.KeyID(), data.Preflight)
	if data.Rearmed != nil {
		fmt.Fprintf(out, "  task %s returns to the commit step (the kept tree only)\n", data.Rearmed.ID)
	}
	return nil
}

// seatCommitData is the JSON envelope of seat commit.
type seatCommitData struct {
	Root   string            `json:"root"`
	Result seatcommit.Result `json:"result"`
	Task   *task.Task        `json:"task,omitempty"`
}

func runSeatCommit(args []string, out io.Writer) error {
	f := newStoreFlags("seat commit")
	f.fs.Usage = func() { fmt.Fprint(os.Stderr, seatUsage) }
	seat := f.fs.String("seat", "", "the seat handle")
	base := f.fs.String("base", "", "record this commit as the seat's base when none is recorded")
	dryRun := f.fs.Bool("dry-run", false, "check only; commit, push and record nothing")
	openPR := f.fs.Bool("open-pr", false, "after the push, open (or find) the seat branch's draft PR with gh and bind it to the task")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*seat) == "" || f.fs.NArg() > 0 {
		return usageErrorf("seat commit: --seat HANDLE is required and takes no positional arguments")
	}
	sel, err := f.selection(!*dryRun)
	if err != nil {
		return err
	}
	defer sel.release()
	t, err := reportedTask(sel.store, *seat)
	if err != nil {
		return fmt.Errorf("seat commit: %w", err)
	}
	if *base != "" {
		rec, err := seatcommit.StagedSeat(sel.root, *seat)
		if err != nil {
			return fmt.Errorf("seat commit --base: %w", err)
		}
		if _, err := seatcommit.RecordBase(sel.root, *seat, rec.Worktree, rec.Branch, *base, taskNow()); err != nil {
			return fmt.Errorf("seat commit --base: %w", err)
		}
		// With the base, the owner's grant for the push (R2).
		if g, err := grants.Load(t.ProjectPath); err == nil {
			if _, err := seatcommit.RecordTarget(sel.root, *seat, seatcommit.TargetFor(g)); err != nil {
				return fmt.Errorf("seat commit --base: %w", err)
			}
		}
	}
	res := commitStep(sel, *seat, t, *dryRun, *openPR)
	data := seatCommitData{Root: sel.root, Result: res}
	var recErr error
	if !*dryRun && !res.Deferred {
		var after task.Task
		if res.OK {
			// The acceptance and the PR binding are one store transaction (P2).
			after, recErr = sel.store.CompleteBound(t.ID, res.PRRepository, res.PRNumber, res.PRHead, taskNow())
		} else {
			after, recErr = sel.store.RefuseCommit(t.ID, res.ReasonCode, res.Detail, res.Route, taskNow())
			if recErr == nil && res.Quarantine {
				recErr = sel.store.Quarantine(task.Quarantine{Handle: *seat, Reason: res.ReasonCode, Detail: res.Detail, TaskID: t.ID, At: taskNow()})
			}
		}
		if recErr == nil {
			data.Task = &after
		}
	}
	if *f.jsonOut {
		if err := writeJSONEnvelope(out, "seat_commit", data); err != nil {
			return err
		}
	} else {
		printCommitResult(out, res, *dryRun)
	}
	switch {
	case recErr != nil:
		return fmt.Errorf("seat commit: record the result for %s: %w", t.ID, recErr)
	case res.Deferred:
		return &codedExitError{code: ExitCommitDeferred, msg: "seat commit deferred: " + res.Detail}
	case !res.OK:
		return &codedExitError{code: ExitCommitRefused, msg: fmt.Sprintf("seat commit refused %s: %s", res.ReasonCode, res.Detail)}
	}
	return nil
}

func printCommitResult(out io.Writer, res seatcommit.Result, dryRun bool) {
	switch {
	case res.Deferred:
		fmt.Fprintf(out, "commit step for %s (%s) deferred: %s\n", res.Task, res.Seat, res.Detail)
	case !res.OK:
		fmt.Fprintf(out, "commit step for %s (%s) refused: %s, routed to the %s\n  %s\n", res.Task, res.Seat, res.ReasonCode, res.Route, res.Detail)
	case dryRun:
		fmt.Fprintf(out, "commit step for %s (%s) would commit %d files, %d lines (cap %d lines, %d files)\n", res.Task, res.Seat, res.Files, res.Lines, res.CapLines, res.CapFiles)
	default:
		pushed := "not pushed"
		if res.Pushed {
			pushed = "pushed to " + res.Branch
		}
		fmt.Fprintf(out, "commit step for %s (%s): %s, %s (%d files, %d lines)\n", res.Task, res.Seat, orDash(res.Commit), pushed, res.Files, res.Lines)
	}
	for _, n := range res.Notes {
		fmt.Fprintf(out, "  note: %s\n", n)
	}
}

// storeSelection is a resolved store with, when the scoped flags resolved
// it, the context the gate namespace comes from.
type storeSelection struct {
	store   task.Store
	root    string
	ctx     *contextResolution
	release func()
}

// selection resolves the store like the task verbs and keeps the context
// when --project/--profile/--session were given.
func (f taskFlags) selection(mutate bool) (storeSelection, error) {
	s, root, release, err := f.store(mutate)
	if err != nil {
		return storeSelection{release: func() {}}, err
	}
	sel := storeSelection{store: s, root: root, release: release}
	if flagWasSet(f.fs, "session") || flagWasSet(f.fs, "project") || flagWasSet(f.fs, "profile") {
		if ctx, err := resolveScopedCommandContext(*f.project, *f.profile, *f.session, "", f.fs); err == nil {
			sel.ctx = &ctx
		}
	}
	return sel, nil
}

// reportedTask is the task whose done report the seat filed and that waits
// for the commit step.
func reportedTask(s task.Store, seat string) (task.Task, error) {
	list, err := s.List()
	if err != nil {
		return task.Task{}, err
	}
	t := task.HeldBy(seat, list)
	if t == nil || t.Report == nil || t.Report.By != seat || t.State != task.StateInProgress {
		return task.Task{}, fmt.Errorf("seat %s has no done report awaiting the commit step", seat)
	}
	return *t, nil
}

// seatCommitGh is the gh executable of the commit step's PR (a test seam;
// "" is gh on PATH).
var seatCommitGh = ""

// commitStep runs the commit step with the grant of the task's repository
// and the gate checker of the selection (the scheduler and the verb share
// it).
func commitStep(sel storeSelection, seat string, t task.Task, dryRun, openPR bool) seatcommit.Result {
	g, gerr := grants.Load(t.ProjectPath)
	return seatcommit.Run(seatcommit.Options{Root: sel.root, Handle: seat, Task: t, Grants: g, GrantsErr: gerr,
		Gates: sel.gateChecker(), DryRun: dryRun, OpenPR: openPR, Gh: seatCommitGh, Now: taskNow,
		PRBoundTo: prBoundTo(sel.store), Clear: ownerTrust{Root: sel.root},
		BindPR: func(repository string, n int, head string) error {
			_, err := sel.store.BindPR(t.ID, repository, n, head, taskNow())
			return err
		}})
}

// gateChecker answers growth gates with the full owner-signature check
// (gateverify) in the selection's namespace. Without a resolved context it
// is nil: no gate is accepted (fail closed).
func (sel storeSelection) gateChecker() seatcommit.GateChecker {
	if sel.ctx == nil {
		return nil
	}
	return storeGateChecker{root: sel.root, ns: gateNamespace(sel.ctx.ProjectDir, sel.ctx.Profile, sel.ctx.Session, sel.ctx.NamespaceGeneration)}
}

type storeGateChecker struct {
	root string
	ns   gateverify.Namespace
}

func (c storeGateChecker) Accepted(target string) bool {
	store, err := gatestore.Open(c.root)
	if err != nil {
		return false
	}
	_, pin, err := gateverify.LoadBinding(gateBindingPath())
	if err != nil {
		return false
	}
	trust, err := operatorauth.LoadAuthorizationTrustStore(gateTrustStorePath())
	if err != nil {
		return false
	}
	_, ok := gateverify.Verifier{Trust: trust, Pin: pin, Namespace: c.ns}.AcceptedFor(store, "spawn", target)
	return ok
}

// raiseGrowthGate writes the growth gate record for one crossing (the core
// raises it; the question goes out when a transport is attached). An
// existing record for the same id is the same request. It returns the gate
// id.
func raiseGrowthGate(sel storeSelection, raisedBy, taskID, target string) (string, error) {
	sum := sha256.Sum256([]byte(target))
	id := "widen-" + taskID + "-" + hex.EncodeToString(sum[:])[:12]
	if sel.ctx == nil {
		return id, errors.New("no resolved context: pass --project/--profile/--session to raise the growth gate")
	}
	if rec, err := (gatestore.Store{Root: sel.root}).Load(id); err == nil && rec.GateID == id {
		return id, nil
	}
	ctx := context.Background()
	core, err := gateCore(ctx, *sel.ctx, true)
	if core == nil {
		return id, err
	}
	spec := gatestore.Spec{GateID: id, Action: "spawn", Target: target, RaisedBy: raisedBy, Window: gatestore.DefaultWindow}
	if err == nil {
		_, _, err = core.Raise(ctx, spec)
		return id, err
	}
	rec, rerr := gatestore.Render(spec, hookNow(), core.Identity())
	if rerr == nil {
		rerr = core.Store.Create(rec)
	}
	if rerr != nil {
		return id, rerr
	}
	return id, fmt.Errorf("gate recorded, question not sent: %v", err)
}
