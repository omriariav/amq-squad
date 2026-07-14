package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/activity"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// taskNow is overridable in tests for deterministic timestamps.
var taskNow = func() time.Time { return time.Now().UTC() }

// tasksEnvelopeData is the `task list --json` payload (typed, matching the
// other JSON envelopes rather than a raw map).
type tasksEnvelopeData struct {
	Session   string             `json:"session"`
	Profile   string             `json:"profile,omitempty"`
	Namespace squadnamespace.Ref `json:"namespace"`
	Tasks     []task.Task        `json:"tasks"`
}

type taskEnvelopeData struct {
	Session   string             `json:"session"`
	Profile   string             `json:"profile,omitempty"`
	Namespace squadnamespace.Ref `json:"namespace"`
	Task      task.Task          `json:"task"`
}

type taskReconcileEnvelopeData struct {
	Session   string               `json:"session"`
	Profile   string               `json:"profile,omitempty"`
	Namespace squadnamespace.Ref   `json:"namespace"`
	Result    task.ReconcileResult `json:"result"`
}

// runTask dispatches the native task lifecycle commands: the
// native pull-based task store. The lead decomposes the goal into tasks; any
// worker (Claude or Codex) claims them and self-schedules around dependencies.
func runTask(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad task - native pull-based task store for a workstream

Usage:
  amq-squad task add --title T [--desc D] [--depends-on id,…] [--assign role] --session S [--profile P]
  amq-squad task list [--status S] [--json] --session S [--profile P]
  amq-squad task show <id> [--json] --session S [--profile P]
  amq-squad task claim <id> --me <handle> [--lease 2h] [--override-dependencies --reason WHY] --session S [--profile P]
  amq-squad task renew <id> --me <handle> [--lease 2h] --session S [--profile P]
  amq-squad task done  <id> --me <handle> [--evidence E] [--final-head SHA] [--dispatch-next ID] [--no-notify] --session S [--profile P]
  amq-squad task fail  <id> --me <handle> [--reason R] --session S [--profile P]
  amq-squad task block <id> --me <handle> [--reason R] --session S [--profile P]
  amq-squad task reset <id> --me <handle> [--reason R] --session S [--profile P]
  amq-squad task cancel <id> --me <handle> --reason R [--replacement ID] --session S [--profile P]
  amq-squad task release <id> --me <handle> --reason R --session S [--profile P]
  amq-squad task deliver <id> --intent ID --me <handle> --session S [--profile P]
  amq-squad task retry-delivery <id> --intent ID --me <handle> --reason R [--confirm-not-delivered] --session S [--profile P]
  amq-squad task reconcile [--apply] [--json] --session S [--profile P]

Tasks live under .amq-squad/tasks/<session>/ for the default profile, or
.amq-squad/tasks/<profile>/<session>/ for named profiles. A task is claimable
only when all its --depends-on tasks are completed (dependency gating), unless
an explicit audited override with a reason is recorded. Claims carry a 2h lease
by default; expiry is reported but never silently unclaims a worker.

task done atomically closes the predecessor, releases newly-ready dependents,
and may claim plus queue a successor with --dispatch-next. When the completed
task has dispatch metadata, it sends the canonical completion signal by default:
AMQ kind status with subject "DONE: <task title>". Use --no-notify to suppress
that signal explicitly; there is no AMQ kind named done.

All multi-task mutations use a durably-synced transaction journal. task reconcile
replays committed journals, reports stale/legacy leases, lifecycle-link problems,
and pending/failed/uncertain delivery intents without auto-resending them.
`)
		if len(args) == 0 {
			return usageErrorf("task requires a subcommand (add, list, show, claim, renew, done, fail, block, reset, cancel, release, deliver, retry-delivery, reconcile)")
		}
		return nil
	}
	switch args[0] {
	case "add":
		return runTaskAdd(args[1:])
	case "list", "ls":
		return runTaskList(args[1:])
	case "show":
		return runTaskShow(args[1:])
	case "claim":
		return runTaskTransition(args[1:], "claim")
	case "renew":
		return runTaskTransition(args[1:], "renew")
	case "done", "complete":
		return runTaskTransition(args[1:], "done")
	case "fail":
		return runTaskTransition(args[1:], "fail")
	case "block":
		return runTaskTransition(args[1:], "block")
	case "reset":
		return runTaskTransition(args[1:], "reset")
	case "cancel":
		return runTaskTransition(args[1:], "cancel")
	case "release":
		return runTaskTransition(args[1:], "release")
	case "deliver":
		return runTaskTransition(args[1:], "deliver")
	case "retry-delivery":
		return runTaskTransition(args[1:], "retry-delivery")
	case "reconcile":
		return runTaskReconcile(args[1:])
	default:
		return usageErrorf("unknown 'task' subcommand: %q. Try add, list, show, claim, renew, done, fail, block, reset, cancel, release, deliver, retry-delivery, or reconcile.", args[0])
	}
}

// taskNamespace resolves --session (required), --project (default cwd), and
// --profile (default profile). Storage is profile/session-scoped for named
// profiles and legacy session-scoped for the default profile.
func taskNamespace(sessionFlag, projectFlag, profileFlag string, fs *flag.FlagSet) (string, string, string, squadnamespace.Ref, error) {
	session := strings.TrimSpace(sessionFlag)
	if session == "" {
		return "", "", "", squadnamespace.Ref{}, usageErrorf("--session is required (tasks are per-workstream)")
	}
	// Validate the session name with the same rules as the rest of the
	// workstream model, so it can't carry path separators or `..` and escape
	// the resolved task namespace into an arbitrary directory.
	if err := team.ValidateSessionName(session); err != nil {
		return "", "", "", squadnamespace.Ref{}, usageErrorf("invalid --session: %v", err)
	}
	if fs.NArg() > 0 {
		return "", "", "", squadnamespace.Ref{}, usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	ctx, err := resolveScopedCommandContext(projectFlag, profileFlag, session, "", fs)
	if err != nil {
		return "", "", "", squadnamespace.Ref{}, err
	}
	emitContextDiagnostics(ctx)
	return ctx.Session, ctx.ProjectDir, ctx.Profile, squadnamespace.Resolve(ctx.ProjectDir, ctx.Profile, ctx.Session), nil
}

func taskScope(projectDir, profile, session string) string {
	scope := " --project " + shellQuote(projectDir)
	if profile != "" && profile != team.DefaultProfile {
		scope += " --profile " + shellQuote(profile)
	}
	scope += " --session " + shellQuote(session)
	return scope
}

func runTaskAdd(args []string) error {
	fs := flag.NewFlagSet("task add", flag.ContinueOnError)
	title := fs.String("title", "", "task title (required)")
	desc := fs.String("desc", "", "task description")
	dependsOn := fs.String("depends-on", "", "comma-separated task ids that must complete first")
	assign := fs.String("assign", "", "pre-assign to a role/handle (optional)")
	reviewOf := fs.String("review-of", "", "link this task as a review iteration of an existing task")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	profileFlag := fs.String("profile", "", "team profile namespace (default: default profile)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	session, projectDir, profile, ns, err := taskNamespace(*sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	if err := ensureNoNamespaceConflict("task add", projectDir, profile, session, flagWasSet(fs, "profile")); err != nil {
		return err
	}
	// The operator is a non-runnable, non-assignable mailbox participant, so
	// it must never be pre-assigned work in the pull queue. Refuse before the
	// store is written.
	if err := ensureLaunchTargetIsNotOperator(projectDir, profile, "task add", strings.TrimSpace(*assign), ""); err != nil {
		return err
	}
	t, err := task.AddForProfile(projectDir, profile, session, task.AddInput{
		Title:       *title,
		Description: *desc,
		DependsOn:   splitCommaList(*dependsOn),
		AssignTo:    strings.TrimSpace(*assign),
		ReviewOf:    strings.TrimSpace(*reviewOf),
	}, taskNow())
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("task_add", mutationResult{
			Command:   "task add",
			Status:    "created",
			Project:   projectDir,
			Session:   session,
			Profile:   profile,
			Namespace: ns,
			ID:        t.ID,
			Role:      t.AssignedTo,
			Actions: []mutationAction{
				followUp("list", "list tasks", "amq-squad task list"+taskScope(projectDir, profile, session)),
				followUp("claim", "claim task", "amq-squad task claim "+shellQuote(t.ID)+" --me <handle>"+taskScope(projectDir, profile, session)),
			},
		})
	}
	fmt.Printf("added %s: %s\n", t.ID, t.Title)
	return nil
}

func runTaskList(args []string) error {
	fs := flag.NewFlagSet("task list", flag.ContinueOnError)
	statusFlag := fs.String("status", "", "filter by status (pending|in_progress|completed|failed|blocked|cancelled)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned tasks envelope")
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	profileFlag := fs.String("profile", "", "team profile namespace (default: default profile)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	session, projectDir, profile, ns, err := taskNamespace(*sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	tasks, err := task.ListForProfile(projectDir, profile, session)
	if err != nil {
		return err
	}
	if s := strings.TrimSpace(*statusFlag); s != "" {
		filtered := tasks[:0:0]
		for _, t := range tasks {
			if t.Status == s {
				filtered = append(filtered, t)
			}
		}
		tasks = filtered
	}
	if *jsonOut {
		return printJSONEnvelope("tasks", tasksEnvelopeData{Session: session, Profile: profile, Namespace: ns, Tasks: tasks})
	}
	if len(tasks) == 0 {
		fmt.Println("(no tasks)")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tASSIGNED\tDEPENDS\tTITLE")
	for _, t := range tasks {
		deps := strings.Join(t.DependsOn, ",")
		if deps == "" {
			deps = "-"
		}
		assigned := t.AssignedTo
		if assigned == "" {
			assigned = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", t.ID, t.Status, assigned, deps, t.Title)
	}
	return w.Flush()
}

func runTaskShow(args []string) error {
	id, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("task show requires a task id, e.g. 'task show t1 --session S'")
	}
	fs := flag.NewFlagSet("task show", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned task envelope")
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	profileFlag := fs.String("profile", "", "team profile namespace (default: default profile)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	session, projectDir, profile, ns, err := taskNamespace(*sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	t, err := task.ShowForProfile(projectDir, profile, session, id)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("task", taskEnvelopeData{Session: session, Profile: profile, Namespace: ns, Task: t})
	}
	printTaskDetails(t)
	return nil
}

func printTaskDetails(t task.Task) {
	fmt.Printf("ID: %s\n", t.ID)
	fmt.Printf("Title: %s\n", t.Title)
	fmt.Printf("Status: %s\n", t.Status)
	fmt.Printf("Assigned: %s\n", orDash(t.AssignedTo))
	fmt.Printf("Depends: %s\n", orDash(strings.Join(t.DependsOn, ",")))
	if t.Description != "" {
		fmt.Printf("Description: %s\n", t.Description)
	}
	if t.Evidence != "" {
		fmt.Printf("Evidence: %s\n", t.Evidence)
	}
	if t.FailureReason != "" {
		fmt.Printf("Failure: %s\n", t.FailureReason)
	}
	if t.BlockReason != "" {
		fmt.Printf("Block: %s\n", t.BlockReason)
	}
	if t.ResetReason != "" {
		fmt.Printf("Reset: %s\n", t.ResetReason)
	}
	if t.CancelReason != "" {
		fmt.Printf("Cancelled: %s\n", t.CancelReason)
	}
	if t.ReadyAt != nil {
		fmt.Printf("Ready At: %s\n", t.ReadyAt.UTC().Format(time.RFC3339Nano))
	}
	if t.Lease != nil {
		fmt.Printf("Lease: %s until %s\n", t.Lease.Owner, t.Lease.ExpiresAt.UTC().Format(time.RFC3339Nano))
	}
	if t.Replaces != "" {
		fmt.Printf("Replaces: %s\n", t.Replaces)
	}
	if t.ReplacedBy != "" {
		fmt.Printf("Replaced By: %s\n", t.ReplacedBy)
	}
	if t.ReviewOf != "" {
		fmt.Printf("Review Of: %s\n", t.ReviewOf)
	}
	if len(t.ReviewTasks) > 0 {
		fmt.Printf("Review Tasks: %s\n", strings.Join(t.ReviewTasks, ","))
	}
	if t.FinalHead != "" {
		fmt.Printf("Final Head: %s\n", t.FinalHead)
	}
	if t.NotificationSuppression != nil {
		fmt.Printf("Completion Notification: suppressed by %s (%s)\n", t.NotificationSuppression.Actor, t.NotificationSuppression.Reason)
	}
	for _, intent := range t.Outbox {
		fmt.Printf("Outbox: %s %s %s -> %s", intent.ID, intent.State, intent.From, intent.To)
		if intent.MessageID != "" {
			fmt.Printf(" message=%s", intent.MessageID)
		}
		if intent.LastError != "" {
			fmt.Printf(" error=%s", intent.LastError)
		}
		fmt.Println()
	}
	if t.Dispatch != nil {
		fmt.Printf("Dispatch Assignee: %s\n", orDash(t.Dispatch.Assignee))
		fmt.Printf("Dispatch Thread: %s\n", orDash(t.Dispatch.Thread))
		fmt.Printf("Dispatch Message: %s\n", orDash(t.Dispatch.MessageID))
	}
}

// runTaskTransition handles task-id lifecycle commands.
func runTaskTransition(args []string, verb string) error {
	id, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("task %s requires a task id, e.g. 'task %s t1 --session S'", verb, verb)
	}
	fs := flag.NewFlagSet("task "+verb, flag.ContinueOnError)
	// Register only the flag that applies to this verb, so e.g.
	// `task fail t1 --evidence E` is a clear "flag not defined" error instead
	// of silently dropping --evidence.
	var me, evidence, reason, finalHead, dispatchNext, replacement, intentID string
	var lease time.Duration
	var overrideDependencies, noNotify, confirmNotDelivered bool
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	switch verb {
	case "claim", "renew", "done", "fail", "block", "reset", "cancel", "release", "deliver", "retry-delivery":
		fs.StringVar(&me, "me", "", "claiming agent handle (required)")
	}
	switch verb {
	case "claim":
		fs.DurationVar(&lease, "lease", task.DefaultLeaseDuration, "claim lease duration")
		fs.BoolVar(&overrideDependencies, "override-dependencies", false, "claim despite unmet dependencies and record an audit")
		fs.StringVar(&reason, "reason", "", "required audit reason with --override-dependencies")
	case "renew":
		fs.DurationVar(&lease, "lease", task.DefaultLeaseDuration, "renewed lease duration")
	case "done":
		fs.StringVar(&evidence, "evidence", "", "evidence/result note")
		fs.StringVar(&finalHead, "final-head", "", "accepted immutable head/commit linked to completion")
		fs.StringVar(&dispatchNext, "dispatch-next", "", "dependent task to claim and dispatch after the atomic commit")
		fs.DurationVar(&lease, "lease", task.DefaultLeaseDuration, "successor claim lease duration")
		fs.BoolVar(&noNotify, "no-notify", false, "explicitly suppress the default canonical DONE: status notification")
	case "fail", "block", "reset", "release":
		fs.StringVar(&reason, "reason", "", "reason")
	case "cancel":
		fs.StringVar(&reason, "reason", "", "cancellation reason (required)")
		fs.StringVar(&replacement, "replacement", "", "replacement task id for a bidirectional supersession link")
	case "retry-delivery":
		fs.StringVar(&reason, "reason", "", "audited retry reason (required)")
		fs.StringVar(&intentID, "intent", "", "outbox intent id (required)")
		fs.BoolVar(&confirmNotDelivered, "confirm-not-delivered", false, "confirm an uncertain send did not arrive before retrying")
	case "deliver":
		fs.StringVar(&intentID, "intent", "", "pending outbox intent id (required)")
	}
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	profileFlag := fs.String("profile", "", "team profile namespace (default: default profile)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	session, projectDir, profile, ns, err := taskNamespace(*sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	if err := ensureNoNamespaceConflict("task "+verb, projectDir, profile, session, flagWasSet(fs, "profile")); err != nil {
		return err
	}
	// The operator is a non-runnable mailbox participant and never acts as a
	// task agent, so it cannot claim or transition queued work. Refuse before
	// the store is mutated.
	if err := ensureLaunchTargetIsNotOperator(projectDir, profile, "task "+verb, "", me); err != nil {
		return err
	}
	now := taskNow()
	var t task.Task
	var released []string
	var successorID string
	var outbox []task.OutboxIntent
	var deliveryErr error
	switch verb {
	case "claim":
		overrideReason := ""
		if overrideDependencies {
			if strings.TrimSpace(reason) == "" {
				return usageErrorf("--override-dependencies requires --reason")
			}
			overrideReason = reason
		} else if strings.TrimSpace(reason) != "" {
			return usageErrorf("--reason requires --override-dependencies for task claim")
		}
		t, err = task.ClaimWithOptionsForProfile(projectDir, profile, session, id, task.ClaimOptions{Actor: me, LeaseDuration: lease, OverrideReason: overrideReason, Now: now})
	case "renew":
		t, err = task.RenewLeaseForProfile(projectDir, profile, session, id, me, lease, now)
	case "done":
		var result task.DoneResult
		result, err = task.DoneAtomicForProfile(projectDir, profile, session, id, task.DoneOptions{
			Actor: me, Evidence: evidence, FinalHead: finalHead, DispatchNextID: dispatchNext,
			LeaseDuration: lease, Notify: !noNotify, Now: now,
		})
		if err == nil {
			t, released, outbox = result.Task, result.ReleasedTaskIDs, result.Outbox
			if result.Successor != nil {
				successorID = result.Successor.ID
			}
			outbox, deliveryErr = deliverTaskOutbox(projectDir, profile, session, outbox, now)
		}
	case "fail":
		t, err = task.FailForProfile(projectDir, profile, session, id, me, reason, now)
	case "block":
		t, err = task.BlockForProfile(projectDir, profile, session, id, me, reason, now)
	case "reset":
		t, err = task.ResetForProfile(projectDir, profile, session, id, me, reason, now)
	case "cancel":
		t, err = task.CancelForProfile(projectDir, profile, session, id, me, reason, replacement, now)
	case "release":
		t, err = task.ReleaseForProfile(projectDir, profile, session, id, me, reason, now)
	case "deliver":
		if strings.TrimSpace(intentID) == "" {
			return usageErrorf("task deliver requires --intent ID")
		}
		var intent task.OutboxIntent
		intent, err = task.PendingOutboxIntentForProfile(projectDir, profile, session, id, intentID)
		if err == nil {
			outbox, deliveryErr = deliverTaskOutbox(projectDir, profile, session, []task.OutboxIntent{intent}, now)
			t, err = task.ShowForProfile(projectDir, profile, session, id)
		}
	case "retry-delivery":
		if strings.TrimSpace(intentID) == "" {
			return usageErrorf("task retry-delivery requires --intent ID")
		}
		var intent task.OutboxIntent
		intent, err = task.PrepareOutboxRetryForProfile(projectDir, profile, session, id, intentID, me, reason, confirmNotDelivered, now)
		if err == nil {
			outbox, deliveryErr = deliverTaskOutbox(projectDir, profile, session, []task.OutboxIntent{intent}, now)
			t, err = task.ShowForProfile(projectDir, profile, session, id)
		}
	}
	if err != nil {
		return err
	}
	if deliveryErr != nil {
		fmt.Fprintf(os.Stderr, "warning: task transition committed, but delivery needs reconciliation: %v\n", deliveryErr)
	}
	stampTaskActivity(projectDir, profile, session, me, verb, t, now)
	if *jsonOut {
		status := t.Status
		if deliveryErr != nil {
			status += "_delivery_attention"
		}
		return printJSONEnvelope("task_"+verb, mutationResult{
			Command:         "task " + verb,
			Status:          status,
			Project:         projectDir,
			Session:         session,
			Profile:         profile,
			Namespace:       ns,
			ID:              t.ID,
			Role:            t.AssignedTo,
			ReleasedTaskIDs: released,
			SuccessorTaskID: successorID,
			Outbox:          outbox,
			Actions: []mutationAction{
				followUp("show", "show task", "amq-squad task show "+shellQuote(t.ID)+taskScope(projectDir, profile, session)+" --json"),
				followUp("list", "list tasks", "amq-squad task list"+taskScope(projectDir, profile, session)+" --json"),
			},
		})
	}
	fmt.Printf("%s is now %s", t.ID, t.Status)
	if t.AssignedTo != "" {
		fmt.Printf(" (%s)", t.AssignedTo)
	}
	fmt.Println()
	if len(released) > 0 {
		fmt.Printf("released dependents: %s\n", strings.Join(released, ","))
	}
	if successorID != "" {
		fmt.Printf("successor %s claimed and dispatch intent committed before delivery\n", successorID)
	}
	for _, intent := range outbox {
		fmt.Printf("outbox %s: %s", intent.ID, intent.State)
		if intent.MessageID != "" {
			fmt.Printf(" (%s)", intent.MessageID)
		}
		fmt.Println()
	}
	return nil
}

func deliverTaskOutbox(projectDir, profile, session string, intents []task.OutboxIntent, now time.Time) ([]task.OutboxIntent, error) {
	updated := make([]task.OutboxIntent, 0, len(intents))
	var deliveryErrs []error
	for i, intent := range intents {
		startedAt := now.Add(time.Duration(i+1) * time.Nanosecond)
		started, err := task.BeginOutboxDeliveryForProfile(projectDir, profile, session, intent.TaskID, intent.ID, startedAt)
		if err != nil {
			deliveryErrs = append(deliveryErrs, err)
			continue
		}
		ctx, resolveErr := resolveAMQContextForNamespace(projectDir, profile, session, started.From)
		if resolveErr != nil {
			finished, finishErr := task.FinishOutboxDeliveryForProfile(projectDir, profile, session, started.TaskID, started.ID, task.DeliveryOutcome{State: task.DeliveryFailedBeforeInvoke, Error: resolveErr.Error()}, startedAt.Add(time.Nanosecond))
			if finishErr != nil {
				deliveryErrs = append(deliveryErrs, finishErr)
			} else {
				updated = append(updated, finished)
			}
			deliveryErrs = append(deliveryErrs, resolveErr)
			continue
		}
		ctx.Me = started.From
		args := dispatchSendArgs(ctx.Root, started.From, started.To, started.Thread, started.Kind, started.Subject, started.Body, "", "", 0)
		receipt := newDeliveryReceipt(projectDir, profile, session, started.To, started.To, "task_outbox", "task_outbox")
		receipt.Sender, receipt.Recipient = started.From, started.To
		receipt.Recipients = []string{started.To}
		receipt.Consumers = []deliveryConsumerState{{Consumer: started.To, State: deliveryStateAmbiguousUnknown}}
		receipt.Root, receipt.Thread = ctx.Root, started.Thread
		if receipt.Thread == "" {
			receipt.Thread = receiptCanonicalP2P(started.From, started.To)
		}
		receipt.TaskID, receipt.OutboxIntentID = started.TaskID, started.ID
		receipt.addStage(deliveryStateAmbiguousUnknown, "receipt reserved before task outbox link and AMQ send")
		if receiptErr := writeDeliveryReceipt(projectDir, profile, session, &receipt); receiptErr != nil {
			if finished, finishErr := task.FinishOutboxDeliveryForProfile(projectDir, profile, session, started.TaskID, started.ID, task.DeliveryOutcome{State: task.DeliveryFailedBeforeInvoke, Error: receiptErr.Error()}, startedAt.Add(time.Nanosecond)); finishErr == nil {
				updated = append(updated, finished)
			} else {
				deliveryErrs = append(deliveryErrs, finishErr)
			}
			deliveryErrs = append(deliveryErrs, receiptErr)
			continue
		}
		linked, linkErr := task.AttachOutboxReceiptForProfile(projectDir, profile, session, started.TaskID, started.ID, receipt.AttemptID, receipt.Path, startedAt.Add(time.Nanosecond))
		if linkErr != nil {
			markDeliveryFailedBeforeID(projectDir, profile, session, &receipt, linkErr)
			if finished, finishErr := task.FinishOutboxDeliveryForProfile(projectDir, profile, session, started.TaskID, started.ID, task.DeliveryOutcome{State: task.DeliveryFailedBeforeInvoke, Error: linkErr.Error()}, startedAt.Add(2*time.Nanosecond)); finishErr == nil {
				updated = append(updated, finished)
			} else {
				deliveryErrs = append(deliveryErrs, finishErr)
			}
			deliveryErrs = append(deliveryErrs, linkErr)
			continue
		}
		started = linked
		out, sendReceipt, sendErr := runOwnedDurableSend(durableSendOptions{ProjectDir: projectDir, Profile: profile, Session: session, Kind: "task_outbox", TaskID: started.TaskID, OutboxIntentID: started.ID, Receipt: &receipt}, amqCommandRequest{Dir: projectDir, Env: amqCommandEnv(ctx), Arg: args})
		_ = out
		messageID := sendReceipt.MessageID
		finished, finishErr := task.FinishOutboxDeliveryForProfile(projectDir, profile, session, started.TaskID, started.ID, taskDeliveryOutcome(sendReceipt, sendErr), startedAt.Add(time.Nanosecond))
		if finishErr != nil {
			deliveryErrs = append(deliveryErrs, finishErr)
		} else {
			updated = append(updated, finished)
		}
		if sendErr != nil && messageID == "" {
			deliveryErrs = append(deliveryErrs, sendErr)
		}
	}
	return updated, errors.Join(deliveryErrs...)
}

func runTaskReconcile(args []string) error {
	fs := flag.NewFlagSet("task reconcile", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "apply deterministic internal repairs; never resends external delivery")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned reconciliation envelope")
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	profileFlag := fs.String("profile", "", "team profile namespace (default: default profile)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	session, projectDir, profile, ns, err := taskNamespace(*sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	if err := ensureNoNamespaceConflict("task reconcile", projectDir, profile, session, flagWasSet(fs, "profile")); err != nil {
		return err
	}
	result, err := task.ReconcileForProfile(projectDir, profile, session, task.ReconcileOptions{Apply: *apply, Now: taskNow()})
	if err != nil {
		return err
	}
	for i := range result.Findings {
		if strings.HasPrefix(result.Findings[i].Guidance, "task ") {
			result.Findings[i].Guidance = "amq-squad " + result.Findings[i].Guidance + taskScope(projectDir, profile, session)
		}
	}
	if *jsonOut {
		return printJSONEnvelope("task_reconcile", taskReconcileEnvelopeData{Session: session, Profile: profile, Namespace: ns, Result: result})
	}
	if result.RecoveredTransactionID != "" {
		fmt.Printf("replayed committed transaction %s\n", result.RecoveredTransactionID)
	}
	if len(result.Findings) == 0 {
		fmt.Println("task store is consistent")
		return nil
	}
	for _, finding := range result.Findings {
		label := finding.Kind
		if finding.TaskID != "" {
			label += " " + finding.TaskID
		}
		if finding.IntentID != "" {
			label += " intent=" + finding.IntentID
		}
		fmt.Printf("%s: %s\n", label, finding.Detail)
		if finding.Guidance != "" {
			fmt.Printf("  next: %s\n", finding.Guidance)
		}
	}
	if len(result.ChangedTaskIDs) > 0 {
		fmt.Printf("updated: %s\n", strings.Join(result.ChangedTaskIDs, ","))
	}
	return nil
}

func stampTaskActivity(projectDir, profile, session, actor, verb string, t task.Task, now time.Time) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return
	}
	ctx, err := resolveAMQContextForNamespace(projectDir, profile, session, actor)
	if err != nil {
		return
	}
	phase := "task_" + verb
	switch verb {
	case "claim":
		phase = "task_claimed"
	case "done":
		phase = "task_done"
	case "fail":
		phase = "task_failed"
	case "block":
		phase = "task_blocked"
	case "reset":
		phase = "task_reset"
	}
	detail := strings.TrimSpace(t.Title)
	if detail == "" {
		detail = "task " + t.ID
	}
	_ = activity.Write(filepath.Join(ctx.Root, "agents", ctx.Me), activity.File{
		Handle:    ctx.Me,
		TaskID:    t.ID,
		Phase:     phase,
		Detail:    detail,
		WrittenAt: now,
	})
}
