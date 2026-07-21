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
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
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
	Session    string                         `json:"session"`
	Profile    string                         `json:"profile,omitempty"`
	Namespace  squadnamespace.Ref             `json:"namespace"`
	Result     task.ReconcileResult           `json:"result"`
	Completion *taskCompletionEvidencePreview `json:"completion,omitempty"`
	Applied    *task.CompletionEvidenceResult `json:"applied,omitempty"`
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
  amq-squad task event <id> --event ACK|PROGRESS|CHECKPOINT|REVIEW --me <handle> [--body TEXT] [--evidence-kind K --evidence-id ID --evidence-sha256 SHA] --session S [--profile P]
  amq-squad task fail  <id> --me <handle> [--reason R] --session S [--profile P]
  amq-squad task block <id> --me <handle> [--reason R] --session S [--profile P]
  amq-squad task reset <id> --me <handle> [--reason R] --session S [--profile P]
  amq-squad task cancel <id> --me <handle> --reason R [--replacement ID] --session S [--profile P]
  amq-squad task release <id> --me <handle> --reason R --session S [--profile P]
  amq-squad task deliver <id> --intent ID --me <handle> --session S [--profile P]
  amq-squad task retry-delivery <id> --intent ID --me <handle> --reason R [--confirm-not-delivered] --session S [--profile P]
  amq-squad task reconcile [--apply] [--json] --session S [--profile P]
  amq-squad task reconcile <id> --evidence-id ID [--apply --binding-digest SHA --me H] [--json] --session S [--profile P]
  amq-squad task leadership [--json] --session S [--profile P]
  amq-squad task handoff --from H --to H --expected-epoch N --reason R [--evidence E] [--json] --session S [--profile P]

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

All multi-task mutations use a durably-synced transaction journal. Store-wide
task reconcile replays committed journals and reports lease/link/delivery drift.
Task-scoped reconcile previews one exact raw DONE evidence ID without writing;
explicit apply consumes the preview binding and audited actor. Exact evidence
completes atomically without a completion send. Mismatch evidence may enter
completed_pending_reconcile, retaining assignee/lease and satisfying no dependency.
Reconcile never deletes/moves mailbox evidence or auto-resends delivery.
`)
		if len(args) == 0 {
			return usageErrorf("task requires a subcommand (add, list, show, claim, renew, event, done, fail, block, reset, cancel, release, deliver, retry-delivery, reconcile)")
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
	case "event":
		return runTaskEvent(args[1:])
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
	case "leadership":
		return runTaskLeadership(args[1:])
	case "handoff":
		return runTaskHandoff(args[1:])
	default:
		return usageErrorf("unknown 'task' subcommand: %q. Try add, list, show, claim, renew, event, done, fail, block, reset, cancel, release, deliver, retry-delivery, or reconcile.", args[0])
	}
}

func runTaskEvent(args []string) error {
	id, rest, ok := peelPositional(args)
	if !ok {
		return usageErrorf("task event requires a task id")
	}
	fs := flag.NewFlagSet("task event", flag.ContinueOnError)
	me := fs.String("me", "", "emitting task actor")
	eventRaw := fs.String("event", "", "ACK|PROGRESS|CHECKPOINT|REVIEW")
	body := fs.String("body", "", "display-only event body")
	evidenceKind := fs.String("evidence-kind", "", "typed evidence kind")
	evidenceID := fs.String("evidence-id", "", "typed evidence id")
	evidenceSHA := fs.String("evidence-sha256", "", "typed evidence digest")
	runGeneration := fs.String("run-generation", "", "prepared run generation override")
	manifestDigest := fs.String("manifest-digest", "", "prepared manifest digest override")
	goalNamespace := fs.String("goal-namespace", "", "prepared goal namespace override")
	goalDigest := fs.String("goal-digest", "", "prepared goal digest override")
	jsonOut := fs.Bool("json", false, "emit schema-versioned mutation output")
	sessionFlag := fs.String("session", "", "AMQ workstream session")
	profileFlag := fs.String("profile", "", "team profile namespace")
	projectFlag := fs.String("project", "", "project/team-home directory")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	event, err := task.ParseLifecycleEvent(*eventRaw)
	if err != nil {
		return usageErrorf("%v", err)
	}
	if event == task.LifecycleDone || event == task.LifecycleBlock || event == task.LifecycleCancel {
		return usageErrorf("task event %s must use the dedicated task %s transition", event, strings.ToLower(string(event)))
	}
	selected, err := selectTaskForMutation(id, *sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	if err := ensureLaunchTargetIsNotOperator(selected.ProjectDir, selected.Profile, "task event", "", *me); err != nil {
		return err
	}
	admission, err := acquireNamespaceWriterAdmission(selected.ProjectDir, selected.Profile, selected.Session)
	if err != nil {
		return err
	}
	defer admission.close()
	current, err := revalidateTaskSelection(selected)
	if err != nil {
		return fmt.Errorf("task event refused: task revalidation under admission failed: %w", err)
	}
	ref, err := taskLifecycleGenerationRef(current.Namespace, *me, task.GenerationRef{
		Generation: *runGeneration, ManifestDigest: *manifestDigest, GoalNamespace: *goalNamespace, GoalDigest: *goalDigest,
	})
	if err != nil {
		return err
	}
	var evidence *task.EvidenceRef
	if *evidenceKind != "" || *evidenceID != "" || *evidenceSHA != "" {
		evidence = &task.EvidenceRef{Kind: *evidenceKind, ID: *evidenceID, SHA256: *evidenceSHA}
	}
	now := taskNow()
	result, err := task.RecordLifecycleEventForProfile(current.ProjectDir, current.Profile, current.Session, id, task.LifecycleEventOptions{
		Event: event, Actor: *me, GenerationRef: ref, EvidenceRef: evidence, Body: *body, Now: now,
	})
	if err != nil {
		return err
	}
	updated, deliveryErr := deliverTaskOutbox(current.ProjectDir, current.Profile, current.Session, []task.OutboxIntent{result.Intent}, now)
	if deliveryErr != nil {
		fmt.Fprintf(os.Stderr, "warning: lifecycle event committed, but delivery needs reconciliation: %v\n", deliveryErr)
	}
	stampTaskActivity(current.ProjectDir, current.Profile, current.Session, *me, strings.ToLower(string(event)), result.Task, now)
	if *jsonOut {
		return printJSONEnvelope("task_event", mutationResult{Command: "task event", Status: result.Task.Status, Project: current.ProjectDir,
			Profile: current.Profile, Session: current.Session, Namespace: current.Namespace, ID: result.Task.ID, Outbox: updated})
	}
	fmt.Printf("%s lifecycle %s committed as %s\n", result.Task.ID, event, result.Intent.Lifecycle.EventID)
	for _, intent := range updated {
		fmt.Printf("outbox %s: %s\n", intent.ID, intent.State)
	}
	return deliveryErr
}

func taskLifecycleGenerationRef(ns squadnamespace.Ref, actor string, explicit task.GenerationRef) (task.GenerationRef, error) {
	provided := explicit.Generation != "" || explicit.ManifestDigest != "" || explicit.GoalNamespace != "" || explicit.GoalDigest != ""
	rec, err := launch.Read(filepath.Join(ns.AMQRoot, "agents", strings.TrimSpace(actor)))
	if err != nil {
		return task.GenerationRef{}, fmt.Errorf("resolve task lifecycle GenerationRef from actor launch record: %w", err)
	}
	ref := task.GenerationRef{Generation: rec.PreparedRunGeneration, ManifestDigest: rec.PreparedRunDigest,
		GoalNamespace: rec.PreparedRunGoalNamespace, GoalDigest: rec.PreparedRunGoalDigest}
	if err := task.ValidateGenerationRef(ref); err != nil {
		return task.GenerationRef{}, fmt.Errorf("actor launch record has incomplete task lifecycle GenerationRef: %w", err)
	}
	prepared, err := currentPreparedGenerationRef(ns.TeamHome, ns.Profile, ns.Session)
	if err != nil {
		return task.GenerationRef{}, err
	}
	if prepared == nil {
		return task.GenerationRef{}, fmt.Errorf("task lifecycle requires the current accepted prepared artifact")
	}
	if ref != *prepared {
		return task.GenerationRef{}, fmt.Errorf("actor launch generation_ref does not match the current accepted prepared artifact")
	}
	if provided {
		if err := task.ValidateGenerationRef(explicit); err != nil {
			return task.GenerationRef{}, err
		}
		if explicit != ref {
			return task.GenerationRef{}, fmt.Errorf("explicit generation_ref does not match the current managed actor launch record")
		}
	}
	return ref, nil
}

func currentPreparedGenerationRef(project, profile, session string) (*task.GenerationRef, error) {
	manifest, digest, err := readPreparedRunManifestSnapshot(project, profile, session)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read current accepted prepared artifact: %w", err)
	}
	ref := task.GenerationRef{Generation: manifest.Generation, ManifestDigest: digest, GoalNamespace: manifest.GoalNamespace, GoalDigest: manifest.GoalDigest}
	if err := task.ValidateGenerationRef(ref); err != nil {
		return nil, fmt.Errorf("current accepted prepared artifact is incomplete: %w", err)
	}
	return &ref, nil
}

func taskLifecycleEvidenceRef(t task.Task, attemptID string) (task.EvidenceRef, error) {
	attemptID = strings.TrimSpace(attemptID)
	for _, link := range t.CommandEvidence {
		if link.AttemptID == attemptID {
			return task.LifecycleCommandEvidenceRef(link)
		}
	}
	return task.EvidenceRef{}, fmt.Errorf("task %s has no immutable command evidence attempt %q", t.ID, attemptID)
}

func runStructuredTerminalTask(projectDir, profile, session string, ns squadnamespace.Ref, current task.Task, actor, reason, replacement, evidenceID string, explicit task.GenerationRef, event task.LifecycleEvent, now time.Time) (task.Task, []task.OutboxIntent, error) {
	refProvided := explicit.Generation != "" || explicit.ManifestDigest != "" || explicit.GoalNamespace != "" || explicit.GoalDigest != ""
	ref, refErr := taskLifecycleGenerationRef(ns, actor, explicit)
	if refErr != nil && !refProvided && current.LifecycleGenerationRef == nil {
		// Legacy/queued namespaces without prepared identity keep the old local
		// transition. They never produce structured evidence or warnings.
		if event == task.LifecycleBlock {
			t, err := task.BlockForProfile(projectDir, profile, session, current.ID, actor, reason, now)
			return t, nil, err
		}
		t, err := task.CancelForProfile(projectDir, profile, session, current.ID, actor, reason, replacement, now)
		return t, nil, err
	}
	if refErr != nil {
		return task.Task{}, nil, refErr
	}
	evidence, err := taskLifecycleEvidenceRef(current, evidenceID)
	if err != nil {
		return task.Task{}, nil, err
	}
	opts := task.TerminalLifecycleOptions{Actor: actor, Reason: reason, ReplacementID: replacement, GenerationRef: ref, EvidenceRef: &evidence, Now: now}
	var result task.TerminalLifecycleResult
	if event == task.LifecycleBlock {
		result, err = task.BlockAtomicLifecycleForProfile(projectDir, profile, session, current.ID, opts)
	} else {
		result, err = task.CancelAtomicLifecycleForProfile(projectDir, profile, session, current.ID, opts)
	}
	return result.Task, result.Outbox, err
}

func runTaskLeadership(args []string) error {
	fs := flag.NewFlagSet("task leadership", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "emit the durable leadership epoch record")
	sessionFlag := fs.String("session", "", "AMQ workstream session")
	profileFlag := fs.String("profile", "", "team profile namespace")
	projectFlag := fs.String("project", "", "project/team-home directory")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	session, projectDir, profile, _, err := taskNamespace(*sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	state, err := task.ReadLeadershipForProfile(projectDir, profile, session)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("task_leadership", state)
	}
	fmt.Printf("Leadership epoch: %d\nCurrent lead: %s\n", state.Epoch, orDash(state.CurrentLead))
	for _, handoff := range state.Handoffs {
		fmt.Printf("Epoch %d: %s -> %s (%s)\n", handoff.Epoch, handoff.From, handoff.To, handoff.Reason)
	}
	return nil
}

func runTaskHandoff(args []string) error {
	fs := flag.NewFlagSet("task handoff", flag.ContinueOnError)
	from := fs.String("from", "", "current lead handle")
	to := fs.String("to", "", "recovery/new lead handle")
	expectedEpoch := fs.Uint64("expected-epoch", 0, "last observed leadership epoch (CAS guard)")
	reason := fs.String("reason", "", "handoff/recovery reason")
	evidence := fs.String("evidence", "", "durable thread or artifact evidence")
	jsonOut := fs.Bool("json", false, "emit the updated leadership epoch record")
	sessionFlag := fs.String("session", "", "AMQ workstream session")
	profileFlag := fs.String("profile", "", "team profile namespace")
	projectFlag := fs.String("project", "", "project/team-home directory")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	session, projectDir, profile, _, err := taskNamespace(*sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	state, err := task.HandoffLeadershipForProfile(projectDir, profile, session, task.LeadershipHandoffInput{
		ExpectedEpoch: *expectedEpoch, From: *from, To: *to, Reason: *reason, Evidence: *evidence, Now: taskNow(),
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("task_handoff", state)
	}
	fmt.Printf("leadership epoch %d: %s -> %s\n", state.Epoch, strings.TrimSpace(*from), state.CurrentLead)
	return nil
}

// taskNamespace resolves --session, --project (default cwd), and --profile
// (default profile). A session is normally explicit. The one exception is a
// launched agent carrying the exact named-profile root pin: AM_ROOT and
// AM_BASE_ROOT are the same exact root, AM_SESSION is omitted, and the launch
// record under that root proves the full namespace. Storage is
// profile/session-scoped for named profiles and legacy session-scoped for the
// default profile.
func taskNamespace(sessionFlag, projectFlag, profileFlag string, fs *flag.FlagSet) (string, string, string, squadnamespace.Ref, error) {
	session := strings.TrimSpace(sessionFlag)
	if session == "" {
		if inferred, ok := taskSessionFromExactLaunch(); ok {
			session = inferred
		} else {
			return "", "", "", squadnamespace.Ref{}, usageErrorf("--session is required (tasks are per-workstream)")
		}
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

// taskSessionFromExactLaunch recognizes only the healthy sessionless identity
// injected into a launched named-profile agent. It deliberately refuses a
// merely discoverable team/profile default: a human shell without a matching
// launch record must still pass --session explicitly.
func taskSessionFromExactLaunch() (string, bool) {
	ns, ok := taskNamespaceFromExactLaunch()
	return ns.Session, ok
}

// taskNamespaceFromExactLaunch returns the full namespace proven by the exact
// sessionless named-profile identity. Returning only the session would allow
// subsequent context discovery to drift back to an ambient/default profile.
func taskNamespaceFromExactLaunch() (exactTaskLaunchNamespace, bool) {
	root, rootOK := os.LookupEnv("AM_ROOT")
	baseRoot, baseRootOK := os.LookupEnv("AM_BASE_ROOT")
	handle, handleOK := os.LookupEnv("AM_ME")
	_, sessionPresent := os.LookupEnv("AM_SESSION")
	root = strings.TrimSpace(root)
	baseRoot = strings.TrimSpace(baseRoot)
	handle = strings.TrimSpace(handle)
	if !rootOK || !baseRootOK || !handleOK || sessionPresent || root == "" || baseRoot == "" || handle == "" ||
		!filepath.IsAbs(root) || !filepath.IsAbs(baseRoot) || !rootsMatch(root, baseRoot) {
		return exactTaskLaunchNamespace{}, false
	}

	rec, err := launch.Read(filepath.Join(filepath.Clean(root), "agents", handle))
	if err != nil || strings.TrimSpace(rec.Handle) != handle || strings.TrimSpace(rec.Session) == "" ||
		!rootsMatch(rec.Root, root) || squadnamespace.NormalizeProfile(rec.TeamProfile) == team.DefaultProfile {
		return exactTaskLaunchNamespace{}, false
	}
	teamHome := strings.TrimSpace(rec.TeamHome)
	if teamHome == "" {
		teamHome = strings.TrimSpace(rec.CWD)
	}
	if teamHome == "" || !rootsMatch(squadnamespace.AMQRoot(teamHome, rec.TeamProfile, rec.Session), root) {
		return exactTaskLaunchNamespace{}, false
	}
	teamHome, err = filepath.Abs(filepath.Clean(teamHome))
	if err != nil {
		return exactTaskLaunchNamespace{}, false
	}
	return exactTaskLaunchNamespace{
		ProjectDir: teamHome,
		Profile:    squadnamespace.NormalizeProfile(rec.TeamProfile),
		Session:    strings.TrimSpace(rec.Session),
	}, true
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
	intent := fs.String("intent", "", "structured task intent: implement|review|audit|lifecycle")
	artifact := fs.String("artifact", "", "artifact or mutation scope governed by this task")
	expectedBase := fs.String("expected-base", "", "expected base commit SHA for the artifact")
	implementer := fs.String("implementer", "", "actor allowed to implement the artifact")
	reviewer := fs.String("reviewer", "", "distinct actor responsible for review/audit")
	parallelWork := fs.Bool("parallel-work", false, "explicitly allow a competing implementation for the same artifact")
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
	initialIdentity, err := captureNamespaceEndpointIdentity(ns, "")
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWriterAdmission(projectDir, profile, session)
	if err != nil {
		return err
	}
	defer admission.close()
	currentSession, currentProject, currentProfile, currentNS, err := taskNamespace(*sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return fmt.Errorf("task add refused: context re-resolution under admission failed: %w", err)
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(currentNS, "")
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("task add", initialIdentity, currentIdentity); err != nil {
		return err
	}
	session, projectDir, profile, ns = currentSession, currentProject, currentProfile, currentNS
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
		Title: *title, Description: *desc, Intent: *intent, Artifact: *artifact,
		ExpectedBaseSHA: *expectedBase, Implementer: *implementer, Reviewer: *reviewer,
		ParallelWorkExplicit: *parallelWork, DependsOn: splitCommaList(*dependsOn),
		AssignTo: strings.TrimSpace(*assign), ReviewOf: strings.TrimSpace(*reviewOf),
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
	statusFlag := fs.String("status", "", "filter by status (pending|in_progress|completed_pending_reconcile|completed|failed|blocked|cancelled)")
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
	if t.Intent != "" {
		fmt.Printf("Intent: %s\n", t.Intent)
		fmt.Printf("Artifact: %s\n", t.Artifact)
		fmt.Printf("Expected Base: %s\n", t.ExpectedBaseSHA)
		fmt.Printf("Implementer: %s\n", t.Implementer)
		fmt.Printf("Reviewer: %s\n", t.Reviewer)
		fmt.Printf("Parallel Work: %t\n", t.ParallelWorkExplicit)
	}
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
	var lifecycleEvidenceID string
	var completionGeneration, gateThread, gateRequestID string
	var lifecycleRunGeneration, lifecycleManifestDigest, lifecycleGoalNamespace, lifecycleGoalDigest string
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
		fs.StringVar(&completionGeneration, "completion-generation", "", "exact idempotence generation (generated and recorded when omitted)")
		fs.StringVar(&gateThread, "gate", "", "optional exactly task-scoped gate topic to correlate")
		fs.StringVar(&gateRequestID, "gate-request-id", "", "exact task-scoped gate request message id (requires --gate)")
		fs.StringVar(&lifecycleRunGeneration, "run-generation", "", "prepared run generation override for structured DONE")
		fs.StringVar(&lifecycleManifestDigest, "manifest-digest", "", "prepared manifest digest override for structured DONE")
		fs.StringVar(&lifecycleGoalNamespace, "goal-namespace", "", "prepared goal namespace override for structured DONE")
		fs.StringVar(&lifecycleGoalDigest, "goal-digest", "", "prepared goal digest override for structured DONE")
		fs.StringVar(&lifecycleEvidenceID, "evidence-id", "", "linked immutable command-evidence attempt for structured DONE")
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
	if verb == "block" || verb == "cancel" {
		fs.StringVar(&lifecycleRunGeneration, "run-generation", "", "prepared run generation override for structured terminal event")
		fs.StringVar(&lifecycleManifestDigest, "manifest-digest", "", "prepared manifest digest override for structured terminal event")
		fs.StringVar(&lifecycleGoalNamespace, "goal-namespace", "", "prepared goal namespace override for structured terminal event")
		fs.StringVar(&lifecycleGoalDigest, "goal-digest", "", "prepared goal digest override for structured terminal event")
		fs.StringVar(&lifecycleEvidenceID, "evidence-id", "", "linked immutable command-evidence attempt for structured terminal event")
	}
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	profileFlag := fs.String("profile", "", "team profile namespace (default: default profile)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, rest); err != nil {
		return err
	}
	// Refuse reserved operator actors before task-file discovery. This is
	// read-only and preserves the mailbox-only guard even when the supplied task
	// does not exist; the selected namespace is checked again below.
	guardProject := *projectFlag
	if !flagWasSet(fs, "project") {
		guardProject, _ = os.Getwd()
	} else if resolved, resolveErr := resolveProjectDirFlag("", *projectFlag, true); resolveErr == nil {
		guardProject = resolved
	}
	guardProfile := squadnamespace.NormalizeProfile(*profileFlag)
	if err := ensureLaunchTargetIsNotOperator(guardProject, guardProfile, "task "+verb, "", me); err != nil {
		return err
	}
	selected, err := selectTaskForMutation(id, *sessionFlag, *projectFlag, *profileFlag, fs)
	if err != nil {
		return err
	}
	session, projectDir, profile, ns := selected.Session, selected.ProjectDir, selected.Profile, selected.Namespace
	initialIdentity, err := captureNamespaceEndpointIdentity(ns, "")
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWriterAdmission(projectDir, profile, session)
	if err != nil {
		return err
	}
	defer admission.close()
	current, err := revalidateTaskSelection(selected)
	if err != nil {
		return fmt.Errorf("task %s refused: task revalidation under admission failed: %w", verb, err)
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(current.Namespace, "")
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("task "+verb, initialIdentity, currentIdentity); err != nil {
		return err
	}
	session, projectDir, profile, ns = current.Session, current.ProjectDir, current.Profile, current.Namespace
	if err := validateTaskSelectionNamespace(current); err != nil {
		return err
	}
	// The operator is a non-runnable mailbox participant and never acts as a
	// task agent, so it cannot claim or transition queued work. Refuse before
	// the store is mutated.
	if err := ensureLaunchTargetIsNotOperator(projectDir, profile, "task "+verb, "", me); err != nil {
		return err
	}
	now := taskNow()
	if verb == "done" {
		if (strings.TrimSpace(gateThread) == "") != (strings.TrimSpace(gateRequestID) == "") {
			return usageErrorf("task done requires --gate and --gate-request-id together")
		}
		if completionGeneration != strings.TrimSpace(completionGeneration) || strings.ContainsAny(completionGeneration, "\r\n\x00") {
			return usageErrorf("--completion-generation must be one trim-canonical single-line value")
		}
	}
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
		var gateCorrelation *task.CompletionGateCorrelation
		if strings.TrimSpace(gateThread) != "" {
			gateCorrelation, err = assessTaskCompletionGateCorrelation(projectDir, profile, session, id, gateThread, gateRequestID, now)
			if err != nil {
				break
			}
		}
		var result task.DoneResult
		var generationRef *task.GenerationRef
		explicitRef := task.GenerationRef{Generation: lifecycleRunGeneration, ManifestDigest: lifecycleManifestDigest, GoalNamespace: lifecycleGoalNamespace, GoalDigest: lifecycleGoalDigest}
		refProvided := explicitRef.Generation != "" || explicitRef.ManifestDigest != "" || explicitRef.GoalNamespace != "" || explicitRef.GoalDigest != ""
		if ref, refErr := taskLifecycleGenerationRef(ns, me, explicitRef); refErr == nil {
			generationRef = &ref
		} else if refProvided || current.Task.LifecycleGenerationRef != nil {
			err = refErr
			break
		}
		var evidenceRef *task.EvidenceRef
		if strings.TrimSpace(lifecycleEvidenceID) != "" {
			ref, refErr := taskLifecycleEvidenceRef(current.Task, lifecycleEvidenceID)
			if refErr != nil {
				err = refErr
				break
			}
			evidenceRef = &ref
		}
		result, err = task.DoneAtomicForProfile(projectDir, profile, session, id, task.DoneOptions{
			Actor: me, Evidence: evidence, FinalHead: finalHead, DispatchNextID: dispatchNext,
			CompletionGeneration: completionGeneration, GateCorrelation: gateCorrelation,
			LeaseDuration: lease, Notify: !noNotify, GenerationRef: generationRef, EvidenceRef: evidenceRef, Now: now,
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
		t, outbox, err = runStructuredTerminalTask(projectDir, profile, session, ns, current.Task, me, reason, "", lifecycleEvidenceID,
			task.GenerationRef{Generation: lifecycleRunGeneration, ManifestDigest: lifecycleManifestDigest, GoalNamespace: lifecycleGoalNamespace, GoalDigest: lifecycleGoalDigest}, task.LifecycleBlock, now)
		if err == nil {
			outbox, deliveryErr = deliverTaskOutbox(projectDir, profile, session, outbox, now)
		}
	case "reset":
		t, err = task.ResetForProfile(projectDir, profile, session, id, me, reason, now)
	case "cancel":
		t, outbox, err = runStructuredTerminalTask(projectDir, profile, session, ns, current.Task, me, reason, replacement, lifecycleEvidenceID,
			task.GenerationRef{Generation: lifecycleRunGeneration, ManifestDigest: lifecycleManifestDigest, GoalNamespace: lifecycleGoalNamespace, GoalDigest: lifecycleGoalDigest}, task.LifecycleCancel, now)
		if err == nil {
			outbox, deliveryErr = deliverTaskOutbox(projectDir, profile, session, outbox, now)
		}
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
	if t.CompletionLifecycle != nil {
		fmt.Printf("completion generation: %s\n", t.CompletionLifecycle.Generation)
		if gate := t.CompletionLifecycle.Gate; gate != nil {
			fmt.Printf("completion gate: %s request=%s state=%s suppressed=%t\n", gate.Thread, gate.RequestMessageID, gate.State, gate.Suppressed)
		}
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

func assessTaskCompletionGateCorrelation(projectDir, profile, session, taskID, gate, requestID string, now time.Time) (*task.CompletionGateCorrelation, error) {
	topic, err := strictGateCloseTopic(gate)
	if err != nil {
		return nil, err
	}
	requestID = strings.TrimSpace(requestID)
	if requestID == "" || strings.ContainsAny(requestID, "\r\n\x00") {
		return nil, usageErrorf("--gate-request-id requires one exact AMQ message id")
	}
	cfg, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return nil, err
	}
	operatorHandle := team.EffectiveOperator(cfg).Handle
	ns := squadnamespace.Resolve(projectDir, profile, session)
	messages, warnings := state.ScanSessionMessages(ns.AMQRoot, func() time.Time { return now })
	if len(warnings) > 0 {
		return nil, fmt.Errorf("task completion gate correlation refused: message scan degraded with %d warning(s)", len(warnings))
	}
	var matches []state.Message
	var threadMessages []state.Message
	for _, message := range messages {
		if message.Thread == topic {
			threadMessages = append(threadMessages, message)
		}
		if strings.TrimSpace(message.ID) == requestID && message.Thread == topic {
			matches = append(matches, message)
		}
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("task completion gate request %s not found on %s", requestID, topic)
	}
	digests := map[string]state.Message{}
	for _, match := range matches {
		digest, err := canonicalTaskMessageSHA256(match)
		if err != nil {
			return nil, err
		}
		digests[digest] = match
	}
	if len(digests) != 1 {
		return nil, fmt.Errorf("task completion gate request %s has conflicting replicas", requestID)
	}
	var request state.Message
	var requestSHA string
	for digest, match := range digests {
		requestSHA, request = digest, match
	}
	if request.Kind != state.KindQuestion || !request.SchemaOK || !request.AuthorityRaw || request.RawThread != topic ||
		!request.AuthorizationRequestPresent || !request.AuthorizationRequestValid || request.AuthorizationRequest == nil {
		return nil, fmt.Errorf("task completion gate request %s is not exact typed raw authority", requestID)
	}
	generation, err := namespaceEndpointGeneration(projectDir, profile, session)
	if err != nil {
		return nil, err
	}
	if !taskCompletionGateRequestMatches(request, topic, projectDir, profile, session, generation, taskID) {
		return nil, fmt.Errorf("task completion gate request %s does not match exact task namespace binding", requestID)
	}

	stateName := ""
	suppressed := false
	reason := ""
	var nextQuestion *state.Message
	for i := range threadMessages {
		candidate := threadMessages[i]
		if candidate.Kind != state.KindQuestion || candidate.ID == requestID || !messageAfter(candidate, request) {
			continue
		}
		if !taskCompletionGateRequestMatches(candidate, topic, projectDir, profile, session, generation, taskID) {
			return nil, fmt.Errorf("task completion gate request %s has newer non-authoritative or mismatched question %s; refusing supersession", requestID, candidate.ID)
		}
		if candidate.AuthorizationRequestPresent {
			if nextQuestion == nil || messageAfter(*nextQuestion, candidate) {
				copy := candidate
				nextQuestion = &copy
			}
		}
	}
	if nextQuestion != nil {
		stateName, suppressed = "superseded", true
		reason = "newer durable typed request supersedes the exact task-scoped generation"
	} else {
		gateState, resolved := state.ResolveOperatorGate(threadMessages, operatorHandle, now)
		if gateState == state.OperatorGateStateOpen && (resolved == nil || resolved.LatestID != requestID) {
			return nil, fmt.Errorf("task completion gate request %s is not the current resolvable generation", requestID)
		}
		switch gateState {
		case state.OperatorGateStateOpen:
			stateName, suppressed = "open_preserved", false
			reason = "unresolved human decision preserved; task completion creates no answer or close"
		case state.OperatorGateStateAnswered:
			stateName, suppressed = "answered", true
			reason = "exact task-scoped request already has a durable decision"
		case state.OperatorGateStateClosed:
			stateName, suppressed = "closed", true
			reason = "exact task-scoped request already has a durable close"
		case state.OperatorGateStateWithdrawn:
			stateName, suppressed = "withdrawn", true
			reason = "exact task-scoped request already has a durable withdrawal"
		default:
			return nil, fmt.Errorf("task completion gate request %s has unresolved degraded lifecycle %s", requestID, gateState)
		}
	}
	return &task.CompletionGateCorrelation{
		TaskID: taskID, Profile: squadnamespace.NormalizeProfile(profile), Session: session,
		NamespaceID: squadnamespace.ID(profile, session), NamespaceGeneration: generation,
		Thread: topic, RequestMessageID: requestID, RequestSHA256: requestSHA,
		State: stateName, Suppressed: suppressed, Reason: reason, ObservedAt: now,
	}, nil
}

func taskCompletionGateRequestMatches(message state.Message, topic, projectDir, profile, session, generation, taskID string) bool {
	if message.Kind != state.KindQuestion || !message.SchemaOK || !message.AuthorityRaw || message.Thread != topic || message.RawThread != topic ||
		!message.AuthorizationRequestPresent || !message.AuthorizationRequestValid || message.AuthorizationRequest == nil {
		return false
	}
	typed := message.AuthorizationRequest
	return typed.TaskID == taskID && typed.Gate == topic && typed.Thread == topic &&
		canonicalPath(typed.Namespace.ProjectDir) == canonicalPath(projectDir) && squadnamespace.ProfilesEqual(typed.Namespace.Profile, profile) &&
		typed.Namespace.Session == session && typed.Namespace.NamespaceID == squadnamespace.ID(profile, session) && typed.Namespace.Generation == generation
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
		if started.Lifecycle != nil {
			contextJSON, contextErr := task.LifecycleContextJSON(*started.Lifecycle)
			if contextErr != nil {
				finished, finishErr := task.FinishOutboxDeliveryForProfile(projectDir, profile, session, started.TaskID, started.ID, task.DeliveryOutcome{State: task.DeliveryFailedBeforeInvoke, Error: contextErr.Error()}, startedAt.Add(time.Nanosecond))
				if finishErr != nil {
					deliveryErrs = append(deliveryErrs, finishErr)
				} else {
					updated = append(updated, finished)
				}
				deliveryErrs = append(deliveryErrs, contextErr)
				continue
			}
			args = append(args, "--context", contextJSON)
		}
		receipt := newDeliveryReceipt(projectDir, profile, session, started.To, started.To, "task_outbox", "task_outbox")
		receipt.Sender, receipt.Recipient = started.From, started.To
		receipt.Recipients = []string{started.To}
		receipt.Consumers = []deliveryConsumerState{{Consumer: started.To, State: deliveryStateAmbiguousUnknown}}
		receipt.Root, receipt.Thread = ctx.Root, started.Thread
		if receipt.Thread == "" {
			receipt.Thread = receiptCanonicalP2P(started.From, started.To)
		}
		receipt.TaskID, receipt.OutboxIntentID = started.TaskID, started.ID
		if lifecycle := started.Lifecycle; lifecycle != nil {
			receipt.LifecycleEventID = lifecycle.EventID
			receipt.LifecycleEvent = string(lifecycle.Event)
			receipt.LifecycleTaskGeneration = lifecycle.TaskGeneration
			receipt.PreparedRunGeneration = lifecycle.GenerationRef.Generation
			receipt.PreparedRunDigest = lifecycle.GenerationRef.ManifestDigest
			receipt.PreparedRunGoalNamespace = lifecycle.GenerationRef.GoalNamespace
			receipt.PreparedRunGoalDigest = lifecycle.GenerationRef.GoalDigest
			if actorRecord, recordErr := launch.Read(filepath.Join(ctx.Root, "agents", started.From)); recordErr == nil {
				receipt.PreparedRunLaunchAttempt = reflectedStringField(actorRecord, "PreparedRunLaunchAttempt", "LaunchAttempt")
			}
		}
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
	var taskID string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		taskID, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("task reconcile", flag.ContinueOnError)
	apply := fs.Bool("apply", false, "apply deterministic internal repairs; never resends external delivery")
	me := fs.String("me", "", "audited actor applying completion evidence")
	evidenceID := fs.String("evidence-id", "", "exact AMQ completion evidence message id")
	bindingDigest := fs.String("binding-digest", "", "accepted preview binding digest (required with evidence --apply)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned reconciliation envelope")
	sessionFlag := fs.String("session", "", "AMQ workstream session (required)")
	profileFlag := fs.String("profile", "", "team profile namespace (default: default profile)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	completionMode := strings.TrimSpace(taskID) != "" || strings.TrimSpace(*evidenceID) != ""
	if completionMode {
		if strings.TrimSpace(taskID) == "" || strings.TrimSpace(*evidenceID) == "" {
			return usageErrorf("task reconcile completion mode requires both <task-id> and --evidence-id")
		}
		return runTaskCompletionReconcile(taskID, *evidenceID, *bindingDigest, *me, *apply, *jsonOut, *sessionFlag, *projectFlag, *profileFlag, fs)
	}
	if strings.TrimSpace(*bindingDigest) != "" {
		return usageErrorf("--binding-digest requires <task-id> --evidence-id and --apply")
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

func runTaskCompletionReconcile(taskID, evidenceID, bindingDigest, actor string, apply, jsonOut bool, sessionFlag, projectFlag, profileFlag string, fs *flag.FlagSet) error {
	selected, err := selectTaskForMutation(taskID, sessionFlag, projectFlag, profileFlag, fs)
	if err != nil {
		return err
	}
	if err := validateTaskSelectionNamespace(selected); err != nil {
		return err
	}
	now := taskNow()
	preview, err := assessTaskCompletionEvidence(selected, evidenceID, now)
	if err != nil {
		return err
	}
	if !apply {
		if strings.TrimSpace(bindingDigest) != "" {
			return usageErrorf("--binding-digest is only valid with --apply")
		}
		if jsonOut {
			return printJSONEnvelope("task_reconcile", taskReconcileEnvelopeData{
				Session: selected.Session, Profile: selected.Profile, Namespace: selected.Namespace, Completion: &preview,
			})
		}
		printTaskCompletionPreview(preview)
		return nil
	}
	if strings.TrimSpace(bindingDigest) == "" {
		return usageErrorf("completion evidence --apply requires --binding-digest from the accepted preview")
	}
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return usageErrorf("completion evidence --apply requires --me for the durable audit")
	}
	if err := ensureLaunchTargetIsNotOperator(selected.ProjectDir, selected.Profile, "task reconcile --apply", "", actor); err != nil {
		return err
	}
	if strings.TrimSpace(bindingDigest) != preview.BindingSHA256 {
		return fmt.Errorf("completion evidence binding digest changed or was not accepted: preview=%s supplied=%s", preview.BindingSHA256, strings.TrimSpace(bindingDigest))
	}
	if err := validateCompletionApplyPreview(preview); err != nil {
		return err
	}
	admission, err := acquireNamespaceWriterAdmission(selected.ProjectDir, selected.Profile, selected.Session)
	if err != nil {
		return err
	}
	defer admission.close()
	selected, err = revalidateTaskSelection(selected)
	if err != nil {
		return fmt.Errorf("task reconcile refused: task revalidation under admission failed: %w", err)
	}
	if err := validateTaskSelectionNamespace(selected); err != nil {
		return err
	}
	current, err := assessTaskCompletionEvidence(selected, evidenceID, now)
	if err != nil {
		return err
	}
	if current.BindingSHA256 != strings.TrimSpace(bindingDigest) {
		return fmt.Errorf("completion evidence changed after preview: accepted binding %s, current binding %s", strings.TrimSpace(bindingDigest), current.BindingSHA256)
	}
	if err := validateCompletionApplyPreview(current); err != nil {
		return err
	}
	applied, err := task.ApplyCompletionEvidenceForProfile(selected.ProjectDir, selected.Profile, selected.Session, selected.Task.ID, task.CompletionEvidenceApply{
		ExpectedTaskSHA256: selected.FileSHA256,
		Evidence:           completionEvidenceRecord(current, actor, now),
		Exact:              current.Exact,
		Actor:              actor,
		Now:                now,
	})
	if err != nil {
		return err
	}
	if jsonOut {
		return printJSONEnvelope("task_reconcile", taskReconcileEnvelopeData{
			Session: selected.Session, Profile: selected.Profile, Namespace: selected.Namespace,
			Completion: &current, Applied: &applied,
		})
	}
	printTaskCompletionPreview(current)
	if applied.Changed {
		fmt.Printf("applied: %s is now %s\n", applied.Task.ID, applied.Task.Status)
	} else {
		fmt.Printf("already applied: %s remains %s\n", applied.Task.ID, applied.Task.Status)
	}
	if len(applied.ReleasedTaskIDs) > 0 {
		fmt.Printf("released dependents: %s\n", strings.Join(applied.ReleasedTaskIDs, ","))
	}
	return nil
}

func printTaskCompletionPreview(preview taskCompletionEvidencePreview) {
	fmt.Printf("task: %s (%s)\n", preview.TaskPath, preview.TaskFileSHA256)
	fmt.Printf("namespace: %v\n", preview.TaskNamespace)
	fmt.Printf("evidence: %s first=%s current=%s digest=%s\n", preview.MessageID, orDash(preview.FirstPath), orDash(preview.CurrentPath), orDash(preview.ContentSHA256))
	fmt.Printf("observed: from=%s to=%s owner=%s thread=%s\n", orDash(preview.From), strings.Join(preview.To, ","), orDash(preview.Owner), orDash(preview.CanonicalThread))
	fmt.Printf("expected: assignee=%s sender=%s to=%s thread=%s\n", orDash(preview.Expected.Assignee), orDash(preview.Expected.Sender), orDash(preview.Expected.To), orDash(preview.Expected.Thread))
	fmt.Printf("proposed: %s\n", preview.ProposedState)
	if len(preview.Blockers) > 0 {
		fmt.Printf("blockers: %s\n", strings.Join(preview.Blockers, ","))
	}
	if preview.BindingSHA256 != "" {
		fmt.Printf("binding: %s\n", preview.BindingSHA256)
	}
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
