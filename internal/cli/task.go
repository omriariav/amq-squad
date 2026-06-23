package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// taskNow is overridable in tests for deterministic timestamps.
var taskNow = func() time.Time { return time.Now().UTC() }

// tasksEnvelopeData is the `task list --json` payload (typed, matching the
// other JSON envelopes rather than a raw map).
type tasksEnvelopeData struct {
	Session string      `json:"session"`
	Tasks   []task.Task `json:"tasks"`
}

type taskEnvelopeData struct {
	Session string    `json:"session"`
	Task    task.Task `json:"task"`
}

// runTask dispatches `amq-squad task <add|list|show|claim|done|fail|block|reset>`: the
// native pull-based task store. The lead decomposes the goal into tasks; any
// worker (Claude or Codex) claims them and self-schedules around dependencies.
func runTask(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad task - native pull-based task store for a workstream

Usage:
  amq-squad task add --title T [--desc D] [--depends-on id,…] [--assign role] --session S
  amq-squad task list [--status S] [--json] --session S
  amq-squad task show <id> [--json] --session S
  amq-squad task claim <id> --me <handle> --session S
  amq-squad task done  <id> --me <handle> [--evidence E] --session S
  amq-squad task fail  <id> --me <handle> [--reason R] --session S
  amq-squad task block <id> --me <handle> [--reason R] --session S
  amq-squad task reset <id> --me <handle> [--reason R] --session S

Tasks live under .amq-squad/tasks/<session>/. A task is claimable only when all
its --depends-on tasks are completed (dependency gating). All mutations are
atomic and lock-serialized.
`)
		if len(args) == 0 {
			return usageErrorf("task requires a subcommand (add, list, show, claim, done, fail, block, reset)")
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
	case "done", "complete":
		return runTaskTransition(args[1:], "done")
	case "fail":
		return runTaskTransition(args[1:], "fail")
	case "block":
		return runTaskTransition(args[1:], "block")
	case "reset":
		return runTaskTransition(args[1:], "reset")
	default:
		return usageErrorf("unknown 'task' subcommand: %q. Try add, list, show, claim, done, fail, block, or reset.", args[0])
	}
}

// taskSessionProject resolves --session (required) and --project (default cwd).
func taskSessionProject(sessionFlag, projectFlag string, fs *flag.FlagSet) (string, string, error) {
	session := strings.TrimSpace(sessionFlag)
	if session == "" {
		return "", "", usageErrorf("--session is required (tasks are per-workstream)")
	}
	// Validate the session name with the same rules as the rest of the
	// workstream model, so it can't carry path separators or `..` and escape
	// .amq-squad/tasks/<session>/ into an arbitrary directory.
	if err := team.ValidateSessionName(session); err != nil {
		return "", "", usageErrorf("invalid --session: %v", err)
	}
	if fs.NArg() > 0 {
		return "", "", usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return "", "", err
	}
	return session, projectDir, nil
}

func runTaskAdd(args []string) error {
	fs := flag.NewFlagSet("task add", flag.ContinueOnError)
	title := fs.String("title", "", "task title (required)")
	desc := fs.String("desc", "", "task description")
	dependsOn := fs.String("depends-on", "", "comma-separated task ids that must complete first")
	assign := fs.String("assign", "", "pre-assign to a role/handle (optional)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	session, projectDir, err := taskSessionProject(*sessionFlag, *projectFlag, fs)
	if err != nil {
		return err
	}
	t, err := task.Add(projectDir, session, task.AddInput{
		Title:       *title,
		Description: *desc,
		DependsOn:   splitCommaList(*dependsOn),
		AssignTo:    strings.TrimSpace(*assign),
	}, taskNow())
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("task_add", mutationResult{
			Command: "task add",
			Status:  "created",
			Project: projectDir,
			Session: session,
			ID:      t.ID,
			Role:    t.AssignedTo,
			Actions: []mutationAction{
				followUp("list", "list tasks", "amq-squad task list --project "+shellQuote(projectDir)+" --session "+shellQuote(session)),
				followUp("claim", "claim task", "amq-squad task claim "+shellQuote(t.ID)+" --me <handle> --project "+shellQuote(projectDir)+" --session "+shellQuote(session)),
			},
		})
	}
	fmt.Printf("added %s: %s\n", t.ID, t.Title)
	return nil
}

func runTaskList(args []string) error {
	fs := flag.NewFlagSet("task list", flag.ContinueOnError)
	statusFlag := fs.String("status", "", "filter by status (pending|in_progress|completed|failed|blocked)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned tasks envelope")
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	session, projectDir, err := taskSessionProject(*sessionFlag, *projectFlag, fs)
	if err != nil {
		return err
	}
	tasks, err := task.List(projectDir, session)
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
		return printJSONEnvelope("tasks", tasksEnvelopeData{Session: session, Tasks: tasks})
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
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	session, projectDir, err := taskSessionProject(*sessionFlag, *projectFlag, fs)
	if err != nil {
		return err
	}
	t, err := task.Show(projectDir, session, id)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("task", taskEnvelopeData{Session: session, Task: t})
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
	if t.Dispatch != nil {
		fmt.Printf("Dispatch Assignee: %s\n", orDash(t.Dispatch.Assignee))
		fmt.Printf("Dispatch Thread: %s\n", orDash(t.Dispatch.Thread))
		fmt.Printf("Dispatch Message: %s\n", orDash(t.Dispatch.MessageID))
	}
}

// runTaskTransition handles claim/done/fail/block: each takes a positional id.
func runTaskTransition(args []string, verb string) error {
	id, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("task %s requires a task id, e.g. 'task %s t1 --session S'", verb, verb)
	}
	fs := flag.NewFlagSet("task "+verb, flag.ContinueOnError)
	// Register only the flag that applies to this verb, so e.g.
	// `task fail t1 --evidence E` is a clear "flag not defined" error instead
	// of silently dropping --evidence.
	var me, evidence, reason string
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	switch verb {
	case "claim", "done", "fail", "block", "reset":
		fs.StringVar(&me, "me", "", "claiming agent handle (required)")
	}
	switch verb {
	case "done":
		fs.StringVar(&evidence, "evidence", "", "evidence/result note")
	case "fail", "block", "reset":
		fs.StringVar(&reason, "reason", "", "reason")
	}
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	session, projectDir, err := taskSessionProject(*sessionFlag, *projectFlag, fs)
	if err != nil {
		return err
	}
	now := taskNow()
	var t task.Task
	switch verb {
	case "claim":
		t, err = task.Claim(projectDir, session, id, me, now)
	case "done":
		t, err = task.Done(projectDir, session, id, me, evidence, now)
	case "fail":
		t, err = task.Fail(projectDir, session, id, me, reason, now)
	case "block":
		t, err = task.Block(projectDir, session, id, me, reason, now)
	case "reset":
		t, err = task.Reset(projectDir, session, id, me, reason, now)
	}
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("task_"+verb, mutationResult{
			Command: "task " + verb,
			Status:  t.Status,
			Project: projectDir,
			Session: session,
			ID:      t.ID,
			Role:    t.AssignedTo,
			Actions: []mutationAction{
				followUp("show", "show task", "amq-squad task show "+shellQuote(t.ID)+" --project "+shellQuote(projectDir)+" --session "+shellQuote(session)+" --json"),
				followUp("list", "list tasks", "amq-squad task list --project "+shellQuote(projectDir)+" --session "+shellQuote(session)+" --json"),
			},
		})
	}
	fmt.Printf("%s is now %s", t.ID, t.Status)
	if t.AssignedTo != "" {
		fmt.Printf(" (%s)", t.AssignedTo)
	}
	fmt.Println()
	return nil
}
