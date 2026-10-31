package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// taskNow is overridable in tests for deterministic timestamps.
var taskNow = func() time.Time { return time.Now().UTC() }

// taskLeaseDuration is the lease length the task verbs grant and renew.
var taskLeaseDuration = task.DefaultLeaseDuration

var taskSubcommands = []string{"add", "list", "show", "claim", "renew", "done", "fail", "block", "release", "reset", "set", "cancel", "widen", "reconcile"}

const taskUsage = `amq-squad task - the squad's task store (leases, pools, file locks)

Usage:
  amq-squad task add --title T --class C [--desc D | --desc-file F] [--depends-on t1,t2]
                     [--files GLOB,...] [--pool runtime:model:effort] [--priority N] [--project-path DIR]
  amq-squad task list [--state S] [--json]
  amq-squad task show ID [--json]
  amq-squad task claim [ID] --me H [--pool runtime:model:effort]
  amq-squad task renew --me H
  amq-squad task done ID --me H [--evidence E] [--final-head SHA]
  amq-squad task fail ID --me H --reason R
  amq-squad task block ID --me H --reason CODE [--detail TEXT] [--route lead|owner]
  amq-squad task release ID --me H --reason R
  amq-squad task reset ID --me H [--pool runtime:model:effort]
  amq-squad task set ID --me H [--priority N] [--pool runtime:model:effort]
  amq-squad task cancel ID --me H --reason R
  amq-squad task widen ID --path PATH [--path PATH]... --reason TEXT   (exact files: glob characters literal, a comma part of the name)
  amq-squad task widen ID --files GLOB,... --reason TEXT   (the lead; no --me: the scheduler proves the lead from the process)
  amq-squad task reconcile [--json]
Store flags (every verb): --root DIR (default AM_ROOT), or --project/--profile/--session.

The store lives under <AMQ session root>/extensions/amq-squad/tasks/. Classes:
extract, summarize, script, implement, plan, review. The scheduler grants
leases; a seat claims its lease (or pulls the next eligible task) and renews
it through evidence ingest. An unclaimed lease returns to the pool when it
expires. A task with --files waits after done for the harness commit step.
See docs/task-store-design.md.

Examples:
  amq-squad task add --title "Fix the parser" --class implement --files internal/parse/ --pool claude:sonnet:medium
  amq-squad task claim --me impl-1
  amq-squad task done t3 --me impl-1 --evidence "go test ./... ok"
`

type taskEnvelope struct {
	Root string    `json:"root"`
	Task task.Task `json:"task"`
}

type taskListEnvelope struct {
	Root  string      `json:"root"`
	Tasks []task.Task `json:"tasks"`
}

type taskReconcileEnvelope struct {
	Root    string      `json:"root"`
	Expired []task.Task `json:"expired_leases"`
}

func runTask(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, taskUsage)
		return nil
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "add":
		return runTaskAdd(rest)
	case "list", "ls":
		return runTaskList(rest)
	case "widen":
		return runTaskWiden(rest, os.Stdout)
	case "reconcile":
		return runTaskReconcile(rest)
	case "show", "claim", "renew", "done", "complete", "fail", "block", "release", "reset", "set", "cancel":
		if verb == "complete" {
			verb = "done"
		}
		return runTaskTransition(verb, rest)
	default:
		return unknownSubcommandError("task", verb, append(append([]string(nil), taskSubcommands...), "ls", "complete")...)
	}
}

// taskFlags are the store-locating flags every task verb takes.
type taskFlags struct {
	fs                              *flag.FlagSet
	root, project, profile, session *string
	me                              *string
	jsonOut                         *bool
}

func newTaskFlags(verb string) taskFlags { return newStoreFlags("task " + verb) }

// newStoreFlags are the store-locating flags under a flag-set name.
func newStoreFlags(name string) taskFlags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stderr, taskUsage) }
	f := taskFlags{
		fs:      fs,
		root:    fs.String("root", "", "AMQ session root of the store (default AM_ROOT)"),
		project: fs.String("project", "", "project/team home, to resolve the session root"),
		profile: fs.String("profile", "", "team profile, to resolve the session root"),
		session: fs.String("session", "", "session, to resolve the session root"),
		me:      fs.String("me", "", "acting handle (default AM_ME)"),
		jsonOut: fs.Bool("json", false, "emit a schema-versioned JSON envelope"),
	}
	registerScopedFlagAliases(fs, f.project, f.session, f.profile)
	return f
}

// actor is --me or AM_ME; the operator mailbox never acts on tasks.
func (f taskFlags) actor(verb string, required bool) (string, error) {
	me := strings.TrimSpace(*f.me)
	if me == "" {
		me = strings.TrimSpace(os.Getenv("AM_ME"))
	}
	if me == "" && required {
		return "", usageErrorf("task %s requires --me HANDLE (or AM_ME)", verb)
	}
	if me != "" {
		cwd, _ := os.Getwd()
		if err := ensureLaunchTargetIsNotOperator(cwd, "", "task "+verb, "", me); err != nil {
			return "", err
		}
	}
	return me, nil
}

// store resolves the session root: --root, else AM_ROOT, else the AMQ root
// of --project/--profile/--session.
// A mutation resolved through --project/--profile/--session holds the
// namespace writer admission (as gate raise does) until release is called,
// so a namespace migration never races a task write.
func (f taskFlags) store(mutate bool) (s task.Store, root string, release func(), err error) {
	release = func() {}
	scoped := flagWasSet(f.fs, "session") || flagWasSet(f.fs, "project") || flagWasSet(f.fs, "profile")
	root = strings.TrimSpace(*f.root)
	if root == "" && !scoped {
		root = strings.TrimSpace(os.Getenv("AM_ROOT"))
	}
	if root == "" {
		if !scoped {
			return task.Store{}, "", release, usageErrorf("%s: no store root; pass --root, set AM_ROOT, or pass --session", f.fs.Name())
		}
		resolve := func() (contextResolution, error) {
			return resolveScopedCommandContext(*f.project, *f.profile, *f.session, "", f.fs)
		}
		ctx, err := resolve()
		if err != nil {
			return task.Store{}, "", release, err
		}
		emitContextDiagnostics(ctx)
		if mutate {
			var admission *namespaceAdmissionLocks
			if ctx, admission, err = acquireRevalidatedContextWriter(ctx, resolve); err != nil {
				return task.Store{}, "", release, err
			}
			release = admission.close
		}
		root = absAMQRoot(ctx.ProjectDir, ctx.Root)
	}
	if s, err = openTaskStore(root); err != nil {
		release()
		return task.Store{}, "", func() {}, usageErrorf("%s: %v", f.fs.Name(), err)
	}
	return s, root, release, nil
}

// absAMQRoot anchors a project-relative AMQ root (amq runs in the project
// directory) so the task store path is absolute.
func absAMQRoot(projectDir, root string) string {
	if root == "" || filepath.IsAbs(root) {
		return filepath.Clean(root)
	}
	return filepath.Join(projectDir, root)
}

func parsePoolFlag(v string) (*task.Pool, error) {
	if strings.TrimSpace(v) == "" {
		return nil, nil
	}
	p, err := task.ParsePool(v)
	if err != nil {
		return nil, usageErrorf("--pool: %v", err)
	}
	return &p, nil
}

func splitList(v string) []string {
	var out []string
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// openTaskStore opens a session's task store with the production verifier
// source: its completion gate reads the owner grant and the task's
// squad.yaml (grants.TaskVerifier; phase 5 re-pass, ruling on ruling 1).
// The task verbs and scheduler run open their stores here.
func openTaskStore(root string) (task.Store, error) {
	s, err := task.Open(root)
	if err != nil {
		return s, err
	}
	s.Verifiers = grants.TaskVerifier
	return s, nil
}

func runTaskAdd(args []string) error {
	f := newTaskFlags("add")
	title := f.fs.String("title", "", "one-line title (required)")
	class := f.fs.String("class", "", "class: "+strings.Join(task.Classes, ", ")+" (required)")
	desc := f.fs.String("desc", "", "description")
	descFile := f.fs.String("desc-file", "", "read the description from FILE ('-' for stdin)")
	deps := f.fs.String("depends-on", "", "comma-separated task ids that must complete first")
	files := f.fs.String("files", "", "comma-separated declared file globs, repo-relative")
	poolFlag := f.fs.String("pool", "", "runtime:model:effort")
	priority := f.fs.Int("priority", 0, "higher dispatches first")
	projectPath := f.fs.String("project-path", "", "repository root the task works in (default: cwd)")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	if f.fs.NArg() > 0 {
		return usageErrorf("task add takes no positional arguments; got %q", f.fs.Arg(0))
	}
	me, err := f.actor("add", false)
	if err != nil {
		return err
	}
	description := *desc
	if strings.TrimSpace(*descFile) != "" {
		if description, err = readPromptBody("", *descFile, false, true, os.Stdin, stdinIsInteractive()); err != nil {
			return err
		}
	}
	pool, err := parsePoolFlag(*poolFlag)
	if err != nil {
		return err
	}
	path := strings.TrimSpace(*projectPath)
	if path == "" {
		path, _ = os.Getwd()
	}
	s, root, release, err := f.store(true)
	if err != nil {
		return err
	}
	defer release()
	t, err := s.Add(task.AddInput{
		Title: *title, Description: description, Class: *class, DependsOn: splitList(*deps),
		DeclaredFiles: splitList(*files), Pool: pool, Priority: *priority, ProjectPath: path, CreatedBy: me,
	}, taskNow())
	if err != nil {
		return usageErrorf("task add: %v", err)
	}
	return printTaskResult("task_add", root, t, *f.jsonOut, "Added")
}

func runTaskList(args []string) error {
	f := newTaskFlags("list")
	stateFlag := f.fs.String("state", "", "only tasks in this state")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	s, root, release, err := f.store(false)
	if err != nil {
		return err
	}
	defer release()
	all, err := s.List()
	if err != nil {
		return err
	}
	list := []task.Task{}
	for _, t := range all {
		if *stateFlag == "" || t.State == *stateFlag {
			list = append(list, t)
		}
	}
	if *f.jsonOut {
		return printJSONEnvelope("tasks", taskListEnvelope{Root: root, Tasks: list})
	}
	if len(list) == 0 {
		fmt.Println("No tasks.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATE\tCLASS\tPRIO\tHOLDER\tPOOL\tWAIT/BLOCK\tTITLE")
	for _, t := range list {
		holder, pool := "-", "-"
		if t.Lease != nil {
			holder = t.Lease.Holder
			if t.Lease.ClaimedAt == nil {
				holder += " (unclaimed)"
			}
		}
		if t.Pool != nil {
			pool = t.Pool.Key()
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\t%s\t%s\n", t.ID, t.State, t.Class, t.Priority, holder, pool, orDash(t.WaitReason+t.BlockReason), t.Title)
	}
	return w.Flush()
}

func runTaskReconcile(args []string) error {
	f := newTaskFlags("reconcile")
	if err := parseFlags(f.fs, args); err != nil {
		return err
	}
	s, root, release, err := f.store(false)
	if err != nil {
		return err
	}
	defer release()
	all, err := s.List() // replays a committed journal under the lock
	if err != nil {
		return err
	}
	now := taskNow()
	expired := []task.Task{}
	for _, t := range all {
		if t.State == task.StateInProgress && t.Lease != nil && t.Report == nil && !now.Before(t.Lease.ExpiresAt) {
			expired = append(expired, t)
		}
	}
	if *f.jsonOut {
		return printJSONEnvelope("task_reconcile", taskReconcileEnvelope{Root: root, Expired: expired})
	}
	fmt.Printf("Task store %s: %d tasks, %d expired leases (the scheduler returns them to the pool).\n", task.DirFor(root), len(all), len(expired))
	for _, t := range expired {
		fmt.Printf("  %s held by %s expired %s\n", t.ID, t.Lease.Holder, t.Lease.ExpiresAt.Format(time.RFC3339))
	}
	return nil
}

func runTaskTransition(verb string, args []string) error {
	id, rest, _ := peelPositional(args)
	f := newTaskFlags(verb)
	reason := f.fs.String("reason", "", "reason")
	evidence := f.fs.String("evidence", "", "completion evidence (done)")
	finalHead := f.fs.String("final-head", "", "final commit (done)")
	detail := f.fs.String("detail", "", "block detail")
	route := f.fs.String("route", task.RouteLead, "who owes the decision: lead or owner (block)")
	poolFlag := f.fs.String("pool", "", "runtime:model:effort (claim, reset, set)")
	priority := f.fs.String("priority", "", "new priority (set)")
	if err := parseFlags(f.fs, rest); err != nil {
		return err
	}
	if f.fs.NArg() > 0 {
		return usageErrorf("task %s: unexpected argument %q", verb, f.fs.Arg(0))
	}
	if id == "" && verb != "claim" && verb != "renew" {
		return usageErrorf("task %s requires a task id", verb)
	}
	if err := taskRejectInapplicableFlags(verb, f.fs); err != nil {
		return err
	}
	me, err := f.actor(verb, verb != "show")
	if err != nil {
		return err
	}
	pool, err := parsePoolFlag(*poolFlag)
	if err != nil {
		return err
	}
	s, root, release, err := f.store(verb != "show")
	if err != nil {
		return err
	}
	defer release()
	now := taskNow()
	var t task.Task
	switch verb {
	case "show":
		t, err = s.Get(id)
	case "claim":
		t, err = s.Claim(id, me, pool, taskLeaseDuration, now)
	case "renew":
		t, _, err = s.Renew(me, taskLeaseDuration, 0, now)
	case "done":
		t, err = s.Done(id, me, *evidence, *finalHead, now)
	case "fail":
		t, err = s.Fail(id, me, *reason, now)
	case "block":
		t, err = s.Block(id, me, *reason, *detail, *route, now)
	case "release":
		t, err = s.Release(id, me, *reason, now)
	case "reset":
		t, err = s.ResetBy(id, me, pool, now)
	case "set":
		var prio *int
		if strings.TrimSpace(*priority) != "" {
			n, convErr := strconv.Atoi(strings.TrimSpace(*priority))
			if convErr != nil {
				return usageErrorf("task set --priority: %v", convErr)
			}
			prio = &n
		}
		if prio == nil && pool == nil {
			return usageErrorf("task set needs --priority or --pool")
		}
		if cur, gerr := s.Get(id); pool != nil && gerr == nil && cur.Attempt > 0 {
			return fmt.Errorf("task set: task %s has started attempt %d; pool cannot be changed after the first claim; use task reset --pool", id, cur.Attempt)
		}
		t, err = s.Set(id, prio, pool, now)
	case "cancel":
		t, err = s.Cancel(id, me, *reason, now)
	}
	if err != nil {
		return fmt.Errorf("task %s: %w", verb, err)
	}
	if verb == "show" {
		if *f.jsonOut {
			return printJSONEnvelope("task", taskEnvelope{Root: root, Task: t})
		}
		printTaskDetails(t)
		return nil
	}
	return printTaskResult("task_"+verb, root, t, *f.jsonOut, strings.ToUpper(verb[:1])+verb[1:])
}

// taskRejectInapplicableFlags refuses a flag that the verb ignores.
func taskRejectInapplicableFlags(verb string, fs *flag.FlagSet) error {
	allowed := map[string][]string{
		"show": {}, "claim": {"pool"}, "renew": {}, "done": {"evidence", "final-head"},
		"fail": {"reason"}, "block": {"reason", "detail", "route"}, "release": {"reason"},
		"reset": {"pool"}, "set": {"priority", "pool"}, "cancel": {"reason"},
	}[verb]
	for _, name := range []string{"reason", "evidence", "final-head", "detail", "route", "pool", "priority"} {
		if !flagWasSet(fs, name) {
			continue
		}
		ok := false
		for _, a := range allowed {
			ok = ok || a == name
		}
		if !ok {
			return usageErrorf("task %s does not take --%s", verb, name)
		}
	}
	if (verb == "fail" || verb == "block" || verb == "release" || verb == "cancel") && !flagWasSet(fs, "reason") {
		return usageErrorf("task %s requires --reason", verb)
	}
	return nil
}

func printTaskResult(kind, root string, t task.Task, jsonOut bool, verbText string) error {
	if jsonOut {
		return printJSONEnvelope(kind, taskEnvelope{Root: root, Task: t})
	}
	holder := ""
	if t.Lease != nil {
		holder = " lease " + t.Lease.Holder + " until " + t.Lease.ExpiresAt.Format(time.RFC3339)
	}
	fmt.Printf("%s %s %q: %s%s\n", verbText, t.ID, t.Title, t.State, holder)
	return nil
}

func printTaskDetails(t task.Task) {
	fmt.Printf("%s  %s\n", t.ID, t.Title)
	fmt.Printf("  state:    %s\n  class:    %s\n  priority: %d\n", t.State, t.Class, t.Priority)
	if t.Pool != nil {
		fmt.Printf("  pool:     %s\n", t.Pool.Key())
	}
	if len(t.DependsOn) > 0 {
		fmt.Printf("  depends:  %s\n", strings.Join(t.DependsOn, ", "))
	}
	if len(t.DeclaredFiles) > 0 {
		fmt.Printf("  files:    %s\n", strings.Join(t.DeclaredFiles, ", "))
	}
	if t.Lease != nil {
		claimed := "unclaimed"
		if t.Lease.ClaimedAt != nil {
			claimed = "claimed " + t.Lease.ClaimedAt.Format(time.RFC3339)
		}
		fmt.Printf("  lease:    %s, %s, expires %s, %d renewals\n", t.Lease.Holder, claimed, t.Lease.ExpiresAt.Format(time.RFC3339), t.Lease.Renewals)
	}
	fmt.Printf("  attempt:  %d\n", t.Attempt)
	for _, a := range t.Attempts {
		fmt.Printf("    #%d %s %s %s\n", a.N, a.Holder, a.Outcome, a.Detail)
	}
	for _, kv := range [][2]string{{"wait", t.WaitReason}, {"block", strings.TrimSpace(t.BlockReason + " " + t.BlockRoute + " " + t.BlockDetail)}, {"evidence", t.Evidence}, {"cancel", t.CancelReason}} {
		if kv[1] != "" {
			fmt.Printf("  %-9s %s\n", kv[0]+":", kv[1])
		}
	}
	if t.Report != nil {
		fmt.Printf("  report:   by %s at %s, awaiting the commit step\n", t.Report.By, t.Report.At.Format(time.RFC3339))
	}
	if t.Description != "" {
		fmt.Printf("\n%s\n", t.Description)
	}
}

// validateTaskIDLeaf refuses anything but a canonical t<N> task id.
func validateTaskIDLeaf(id string) error {
	if err := task.ValidID(strings.TrimSpace(id)); err != nil {
		return usageErrorf("%v", err)
	}
	return nil
}

// taskExistsAtRoot checks that a task id exists in the store of an AMQ
// session root (gate raise --task binds a gate to a task).
func taskExistsAtRoot(root, id string) error {
	s, err := task.Open(root)
	if err != nil {
		return err
	}
	_, err = s.Get(id)
	return err
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

// peelPositional splits a leading positional argument from the remaining flag
// args (Go's flag parser stops at the first non-flag, so a positional that
// precedes flags must be peeled first).
func peelPositional(args []string) (val string, rest []string, ok bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", args, false
	}
	return args[0], args[1:], true
}
