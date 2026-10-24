package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	taskstore "github.com/omriariav/amq-squad/v3/internal/task"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

const (
	dispatchNoWait                 = "none"
	dispatchAnswerDefaultWaitFor   = "drained"
	dispatchAnswerDefaultWaitAfter = 60 * time.Second
)

func runDispatch(args []string) error {
	fs := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "workstream session of the team")
	roleFlag := fs.String("role", "", "role of the child agent to dispatch the task to")
	fromFlag := fs.String("from", "", "sender handle (default: the orchestration lead, else AM_ME)")
	fs.Uint64("leadership-epoch", 0, "legacy option; unavailable in simple task mode")
	threadFlag := fs.String("thread", "", "AMQ thread to send on, e.g. p2p/<lead>__<role> (default: amq's auto thread)")
	kindFlag := fs.String("kind", "todo", "AMQ message kind (todo, question, status, ...)")
	subjectFlag := fs.String("subject", "", "task subject line")
	body := fs.String("body", "", "task body (alternative to --body-file)")
	bodyFile := fs.String("body-file", "", "read the task body from this file ('-' for stdin)")
	priorityFlag := fs.String("priority", "", "message priority: urgent, normal, low")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	waitForFlag := fs.String("wait-for", "", "AMQ receipt stage to wait for after send (default: drained for --kind answer; use none to disable)")
	waitTimeoutFlag := fs.Duration("wait-timeout", dispatchAnswerDefaultWaitAfter, "maximum time to wait for the AMQ receipt stage")
	overrideWaitPosture := fs.Bool("override-wait-posture", false, "allow a verified own-pane lead wait that would normally park, and write an audit record")
	waitPostureReason := fs.String("wait-posture-reason", "", "distinct required reason when --override-wait-posture is set")
	createTaskFlag := fs.Bool("create-task", false, "create a task in the squad's task store and lease it to the child before dispatch")
	taskIDFlag := fs.String("task", "", "lease an existing pending task (or confirm the child's lease) before dispatch")
	taskClass := fs.String("task-class", "", "class of the created task (required with --create-task): "+strings.Join(taskstore.Classes, ", "))
	taskFiles := fs.String("task-files", "", "comma-separated declared file globs of the created task")
	taskPool := fs.String("task-pool", "", "runtime:model:effort of the created task")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad dispatch - queue a durable task for a child

Usage:
  amq-squad dispatch [--project DIR] [--profile NAME] --session S --role ROLE
                     [--from HANDLE] [--thread THREAD] [--kind todo] --subject SUBJ
                     (--body TEXT | --body-file FILE) [--priority P]
                     [--wait-for STAGE] [--wait-timeout DURATION]
                     [--override-wait-posture --wait-posture-reason WHY]
                     [--create-task --task-class C [--task-files G,...] [--task-pool P] | --task ID] [--json]

The deterministic lead-to-child dispatch: it sends a DURABLE AMQ message to
the workstream's resolved root (the single source of truth), so the task
survives even if the child is down. The root is resolved from the team home, so
it is correct for an external lead too. The child picks it up through AMQ (its
next drain); dispatch never types into a terminal.

After dispatch, the lead should read the child's completion/report message with
the printed root-correct 'amq drain --include-body --root ... --me ...' command.
Draining the task message does not prove the task is complete; wait for the
child's explicit report on the durable task thread.

With --create-task or --task ID, dispatch creates (when requested) the task in
the squad's task store and grants its lease to the child before sending AMQ.
Dispatch is tracked by the lease, not by the mail: the message is a doorbell.
The child claims the lease (amq-squad task claim); if it never does, the lease
expires and the task returns to the pool for another seat. A failed send
leaves the lease in place; it expires the same way.

Use --body-file FILE or --body-file - (stdin) for task bodies containing code,
commands, backticks, or $() syntax. Inline --body is suitable only for short
plain prose: the caller's shell expands inline text before amq-squad receives
argv, so no literal flag can recover text the shell already substituted.

Examples:
  amq-squad dispatch --session issue-96 --role fullstack --thread p2p/cto__fullstack --subject "Build X" --body-file ./task.md
  cat task.md | amq-squad dispatch --session issue-96 --role qa --subject "Validate PR #64" --body-file -
  amq-squad dispatch --session issue-96 --role cto --kind question --subject "Approve merge?" --body "Plain-prose question."
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if strings.TrimSpace(*roleFlag) == "" {
		return usageErrorf("dispatch requires --role")
	}
	if strings.TrimSpace(*subjectFlag) == "" {
		return usageErrorf("dispatch requires --subject")
	}
	if *createTaskFlag && strings.TrimSpace(*taskIDFlag) != "" {
		return usageErrorf("--create-task and --task are mutually exclusive")
	}
	if flagWasSet(fs, "leadership-epoch") {
		return usageErrorf("dispatch --leadership-epoch is unavailable in simple task mode; sender identity is carried by the AMQ message")
	}
	waitFor := dispatchReceiptWaitFor(*kindFlag, *waitForFlag)
	waitTimeout := *waitTimeoutFlag
	if waitFor != "" && waitTimeout < 0 {
		return usageErrorf("dispatch --wait-timeout must be non-negative when receipt waiting is enabled")
	}
	taskBody, err := readPromptBody(*body, *bodyFile, flagWasSet(fs, "body"), flagWasSet(fs, "body-file"), os.Stdin, stdinIsInteractive())
	if err != nil {
		return err
	}
	warnSuspiciousInlineBody("dispatch", taskBody, flagWasSet(fs, "body"), os.Stderr)

	resolvedContext, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, *fromFlag, fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(resolvedContext)
	projectDir, profile := resolvedContext.ProjectDir, resolvedContext.Profile
	if !team.ExistsFor(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadFor(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	workstream, err := resolveTeamWorkstreamName(t, resolvedContext.Session, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	if err := ensureTargetIsNotOperator(t, "dispatch", *roleFlag); err != nil {
		return err
	}
	member, ok := teamMemberByRole(t, *roleFlag)
	if !ok {
		return fmt.Errorf("no team member with role %q in this team", *roleFlag)
	}
	ns := squadnamespace.Resolve(projectDir, profile, workstream)
	from, err := resolveDispatchSender(t, *fromFlag)
	if err != nil {
		return err
	}
	executionContract := executionContractForTeam(t, profile, workstream, "", "")
	currentActorContract := actorExecutionContractForTeam(t, member.Role, memberHandle(member), executionContract)
	currentActorMode := team.EffectiveActorMode(t, member)
	currentActorImplementationAllowed := currentActorContract.ImplementationAllowedForYou
	// #176: children report to the dispatcher (the task's From), so warn when
	// it differs from the configured lead.
	if t.Orchestrated && strings.TrimSpace(t.Lead) != "" {
		if configuredLead, ok := teamMemberByRole(t, t.Lead); ok {
			if ch := memberHandle(configuredLead); ch != "" && ch != from {
				fmt.Fprintf(os.Stderr, "notice: dispatch --from %q differs from configured lead %q; "+
					"children will report to the dispatcher (%q), not the team lead.\n", from, ch, from)
			}
		}
	}

	// Resolve the workstream root from the canonical TEAM HOME, not the
	// recipient's execution cwd. Implementation members commonly run from an
	// isolated worktree whose project-local .agent-mail is not the squad's
	// durable namespace. Route and status both treat projectDir/t.Project as the
	// namespace authority; dispatch must do the same.
	ctx, err := resolveAMQContextForNamespace(projectDir, profile, workstream, from)
	if err != nil {
		return fmt.Errorf("resolve amq root for dispatch: %w", err)
	}
	ctx.Me = from

	taskID := strings.TrimSpace(*taskIDFlag)
	if *createTaskFlag && !taskstore.ValidClass(strings.TrimSpace(*taskClass)) {
		return usageErrorf("dispatch --create-task requires --task-class (one of %s)", strings.Join(taskstore.Classes, ", "))
	}
	if *createTaskFlag && dispatchClassRequiresImplementation(strings.TrimSpace(*taskClass)) && !currentActorImplementationAllowed {
		return dispatchActorIntentRefusal("create-task", strings.TrimSpace(*taskClass), currentActorContract, currentActorMode)
	}
	pool, err := parsePoolFlag(*taskPool)
	if err != nil {
		return err
	}
	var leased *taskstore.Task
	if taskID != "" || *createTaskFlag {
		store, err := taskstore.Open(absAMQRoot(projectDir, ctx.Root))
		if err != nil {
			return err
		}
		if *createTaskFlag {
			created, addErr := store.Add(taskstore.AddInput{
				Title: *subjectFlag, Description: taskBody, Class: strings.TrimSpace(*taskClass),
				DeclaredFiles: splitList(*taskFiles), Pool: pool, ProjectPath: projectDir, CreatedBy: from,
			}, taskNow())
			if addErr != nil {
				return fmt.Errorf("create task before dispatch: %w", addErr)
			}
			taskID = created.ID
		} else if current, getErr := store.Get(taskID); getErr != nil {
			return getErr
		} else if dispatchClassRequiresImplementation(current.Class) && !currentActorImplementationAllowed {
			return dispatchActorIntentRefusal("task "+current.ID, current.Class, currentActorContract, currentActorMode)
		}
		got, leaseErr := leaseDispatchedTask(store, taskID, member.Handle, taskNow())
		if leaseErr != nil {
			return fmt.Errorf("lease task %s before dispatch: %w", taskID, leaseErr)
		}
		leased = &got
	}

	sendCmd := dispatchSendArgs(ctx.Root, from, member.Handle, *threadFlag, *kindFlag, *subjectFlag, taskBody, *priorityFlag, waitFor, waitTimeout)
	waitPosture := waitPostureForContext("dispatch", "amq_delivery", ctx, waitTimeout, waitFor != "" && waitTimeout == 0, waitFor != "", *overrideWaitPosture, *waitPostureReason)
	out, sendResult, err := runOwnedAMQSend(ownedAMQSendOptions{
		WaitPosture: waitPosture,
	}, amqCommandRequest{Dir: projectDir, Env: amqCommandEnv(ctx), Arg: sendCmd})
	msgID := sendResult.MessageID
	if err != nil {
		if !dispatchSendWaitTimedOut(out, err, waitFor) {
			if leased != nil {
				return fmt.Errorf("dispatch send to %s failed after task %s was leased to %s; the lease stays and returns the task to the pool if it is never claimed: %w", *roleFlag, taskID, member.Handle, err)
			}
			return fmt.Errorf("dispatch send to %s: %w", *roleFlag, err)
		}
	}
	waitTimedOut := dispatchSendWaitTimedOut(out, err, waitFor)
	// Our own session-aware summary (amq's raw line renders an empty "session:"
	// for a --root-only send); amq's raw line only when no message id parses.
	if !*jsonOut {
		if msgID != "" {
			taskText := ""
			if taskID != "" {
				taskText = fmt.Sprintf(" — task %s", taskID)
			}
			waitText := ""
			if waitFor != "" {
				if waitTimedOut {
					waitText = fmt.Sprintf("; queued, %s receipt unconfirmed after %s; do NOT re-send", waitFor, waitTimeout)
				} else {
					waitText = fmt.Sprintf("; waited for %s receipt up to %s", waitFor, waitTimeout)
				}
			}
			fmt.Printf("Dispatched %s to %s (handle %s) on session %s — msg %s%s (root %s%s)\n",
				*kindFlag, *roleFlag, member.Handle, workstream, msgID, taskText, ctx.Root, waitText)
		} else {
			if msg := strings.TrimSpace(string(out)); msg != "" {
				fmt.Println(msg)
			}
			quietNotice("Queued %s task for %s (handle %s) at %s.\n", *kindFlag, *roleFlag, member.Handle, ctx.Root)
		}
		fmt.Printf("Next: drain the child report with `%s`\n", dispatchDrainCommand(ctx.Root, from))
	}

	if *jsonOut {
		return printJSONEnvelope("dispatch", mutationResult{
			Command:   "dispatch",
			Status:    "queued",
			Project:   projectDir,
			Session:   workstream,
			Profile:   profile,
			Namespace: ns,
			ID:        taskID,
			TaskID:    taskID,
			Role:      member.Role,
			Assignee:  member.Handle,
			Handle:    member.Handle,
			MessageID: msgID,
			Root:      ctx.Root,
			Actions:   dispatchFollowUpActions(projectDir, profile, workstream, ctx.Root, from, member.Handle, msgID),
		})
	}
	return nil
}

func dispatchClassRequiresImplementation(class string) bool {
	return strings.TrimSpace(class) == "implement"
}

func dispatchActorIntentRefusal(subject, class string, actor actorExecutionData, actorMode string) error {
	return fmt.Errorf("%s dispatch refused: class %s requires current_actor_implementation_allowed=true; actor %s/%s has EffectiveActorMode=%s and current_actor_implementation_allowed=false; route implementation work to an implementation actor", subject, class, actor.ActorRole, actor.ActorHandle, actorMode)
}

// leaseDispatchedTask grants a pending task's lease to the assignee, or
// accepts a lease the assignee already holds. Any other state refuses.
func leaseDispatchedTask(store taskstore.Store, id, assignee string, now time.Time) (taskstore.Task, error) {
	current, err := store.Get(id)
	if err != nil {
		return taskstore.Task{}, err
	}
	switch {
	case current.State == taskstore.StatePending:
		return store.Grant(id, assignee, nil, taskLeaseDuration, now)
	case current.State == taskstore.StateInProgress && current.Lease != nil && current.Lease.Holder == assignee:
		return current, nil
	case current.State == taskstore.StateInProgress && current.Lease != nil:
		return taskstore.Task{}, fmt.Errorf("task %s is leased to %s, not %s", id, current.Lease.Holder, assignee)
	default:
		return taskstore.Task{}, fmt.Errorf("task %s is %s; dispatch requires pending, or a lease the target already holds", id, current.State)
	}
}

func dispatchDrainCommand(root, me string) string {
	return "amq drain --include-body --root " + shellQuote(root) + " --me " + shellQuote(me)
}

func dispatchFollowUpActions(projectDir, profile, session, root, from, recipient, msgID string) []mutationAction {
	actions := []mutationAction{
		followUp("drain", "drain child report", dispatchDrainCommand(root, from)),
	}
	actions = append(actions, followUp("status", "show recipient status", "amq-squad status --project "+shellQuote(projectDir)+" --profile "+shellQuote(profile)+" --session "+shellQuote(session)+" --json"))
	return actions
}

// dispatchSendArgs builds the `amq send` argv for a dispatch: a durable message
// to the resolved root from the lead handle to the child handle. The body is
// always passed (it is required and validated upstream). Pure + table-testable.
func dispatchSendArgs(root, from, to, thread, kind, subject, body, priority, waitFor string, waitTimeout time.Duration) []string {
	args := []string{"send", "--root", root, "--me", from, "--to", to}
	if th := strings.TrimSpace(thread); th != "" {
		args = append(args, "--thread", th)
	}
	if k := strings.TrimSpace(kind); k != "" {
		args = append(args, "--kind", k)
	}
	if s := strings.TrimSpace(subject); s != "" {
		args = append(args, "--subject", s)
	}
	args = append(args, "--body", body)
	if p := strings.TrimSpace(priority); p != "" {
		args = append(args, "--priority", p)
	}
	if w := strings.TrimSpace(waitFor); w != "" {
		args = append(args, "--wait-for", w)
		if waitTimeout > 0 {
			args = append(args, "--wait-timeout", waitTimeout.String())
		}
	}
	return args
}

func dispatchReceiptWaitFor(kind, explicit string) string {
	explicit = strings.TrimSpace(explicit)
	if strings.EqualFold(explicit, dispatchNoWait) {
		return ""
	}
	if explicit != "" {
		return explicit
	}
	if strings.EqualFold(strings.TrimSpace(kind), "answer") {
		return dispatchAnswerDefaultWaitFor
	}
	return ""
}

func dispatchSendWaitTimedOut(out []byte, err error, waitFor string) bool {
	if err == nil || strings.TrimSpace(waitFor) == "" {
		return false
	}
	if parseSentMessageID(string(out)) == "" {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "timed out waiting")
}

// resolveDispatchSender picks the AMQ handle the dispatched task is sent FROM.
// An explicit --from wins. Otherwise, for an orchestrated team it defaults to
// the lead role's handle (the lead is the one dispatching to children); failing
// that it uses AM_ME from the environment (a bootstrapped lead). It errors only
// when none of those is available, so an external lead on a non-orchestrated
// team is told to pass --from rather than silently sending from an empty handle.
func resolveDispatchSender(t team.Team, fromFlag string) (string, error) {
	if f := strings.TrimSpace(fromFlag); f != "" {
		return f, nil
	}
	if t.Orchestrated && strings.TrimSpace(t.Lead) != "" {
		if m, ok := teamMemberByRole(t, t.Lead); ok && strings.TrimSpace(m.Handle) != "" {
			return m.Handle, nil
		}
		return strings.TrimSpace(t.Lead), nil
	}
	if me := strings.TrimSpace(os.Getenv("AM_ME")); me != "" {
		return me, nil
	}
	return "", usageErrorf("dispatch requires --from <sender-handle>: the team is not orchestrated (no lead to default to) and AM_ME is unset")
}

// teamMemberByRole returns the member whose role matches (case-insensitively),
// honoring the canonical member ordering.
func teamMemberByRole(t team.Team, role string) (team.Member, bool) {
	role = strings.ToLower(strings.TrimSpace(role))
	for _, m := range t.Members {
		if strings.ToLower(m.Role) == role {
			return m, true
		}
	}
	return team.Member{}, false
}
