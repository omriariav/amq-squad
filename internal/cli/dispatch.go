package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// dispatchNudgePrompt is the drain-only prompt amq-squad injects into a
// worker's pane after queuing a durable task. It deliberately carries NO task
// content: the task body lives only in the durable AMQ message (the single
// source of truth), so the worker reads it with `amq drain` and there is no risk
// of a pane-injected copy diverging from — or double-delivering — the queued
// message. amq-squad nudges through the agent's OWN tmux pane — which it launched
// and tracks by exact pane id — so it has a pane-precise, tmux-native way to poke
// an idle agent into draining, independent of amq's own wake path.
//
// The exact root is explicit because AMQ 0.49.8+ refuses ambiguous repo-cwd
// routing. The target pane's existing AM_ME remains authoritative: adding
// --me here is redundant when healthy and makes a configless-root recovery
// command refuse precisely when the nudge is needed most.
func dispatchNudgePrompt(root string) string {
	return "amq-squad dispatch: a new message is queued in your inbox. " +
		"Run `amq drain --include-body --root " + shellQuote(strings.TrimSpace(root)) +
		"` now and act on the newest item. Do not wait to be polled."
}

// dispatchOutcome reports how the best-effort pane nudge resolved. PaneID is the
// pane that was nudged (empty when none was). SubmitState distinguishes a
// confirmed Enter from a staged-but-unconfirmed prompt. Skipped carries a
// human-readable reason the nudge did not happen (no live pane, or busy without
// --force); a skip is NOT an error because the durable task is already queued.
type dispatchOutcome struct {
	PaneID      string `json:"pane_id,omitempty"`
	Skipped     string `json:"skipped,omitempty"`
	SubmitState string `json:"submit_state,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

const (
	dispatchSubmitConfirmed   = "submit_confirmed"
	dispatchSubmitUnconfirmed = "submit_unconfirmed"
	dispatchSubmitQueued      = "submit_queued"
)

const (
	dispatchNoWait                 = "none"
	dispatchAnswerDefaultWaitFor   = "drained"
	dispatchAnswerDefaultWaitAfter = 60 * time.Second
)

type dispatchEnvelopeData struct {
	Session   string          `json:"session"`
	Role      string          `json:"role"`
	Assignee  string          `json:"assignee"`
	Thread    string          `json:"thread,omitempty"`
	Kind      string          `json:"kind"`
	MessageID string          `json:"message_id,omitempty"`
	TaskID    string          `json:"task_id,omitempty"`
	Root      string          `json:"root"`
	Nudge     dispatchOutcome `json:"nudge"`
}

// dispatchWakePane delivers a rooted dispatchNudgePrompt to a member's live
// pane. dispatchRoot is the already-resolved durable-send root; carrying it
// across the nudge boundary prevents fallback runtime discovery from selecting
// a different mailbox below a worktree cwd. It is a package var so tests can
// drive runDispatch without a tmux server.
var dispatchWakePane = defaultDispatchWakePaneAtRoot

// dispatchRecipientWakeLive reports whether the dispatch recipient currently has
// a positively-live wake sidecar, so dispatch can rely on durable AMQ + wake
// delivery (#289) instead of injecting pane keystrokes. Package var so tests can
// drive the wake-first branch without real liveness probing.
var dispatchRecipientWakeLive = defaultDispatchRecipientWakeLive

// dispatchClaimTask is the simple-mode atomic claim seam. The AMQ message is
// deliberately separate from task-store state.
var dispatchClaimTask = taskstore.ClaimForProfile

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
	forceFlag := fs.Bool("force", false, "nudge the pane even if the agent looks busy (mid-turn)")
	noWakeFlag := fs.Bool("no-wake", false, "queue the durable task without nudging the pane")
	waitForFlag := fs.String("wait-for", "", "AMQ receipt stage to wait for after send (default: drained for --kind answer; use none to disable)")
	waitTimeoutFlag := fs.Duration("wait-timeout", dispatchAnswerDefaultWaitAfter, "maximum time to wait for the AMQ receipt stage")
	overrideWaitPosture := fs.Bool("override-wait-posture", false, "allow a verified own-pane lead wait that would normally park, and write an audit record")
	waitPostureReason := fs.String("wait-posture-reason", "", "distinct required reason when --override-wait-posture is set")
	overrideNamespaceConflict := fs.Bool("override-namespace-conflict", false, "acknowledge a collided namespace and continue, writing an audit record")
	overrideNamespaceReason := fs.String("reason", "", "required reason when --override-namespace-conflict is set")
	createTaskFlag := fs.Bool("create-task", false, "create and link a native task-store task before dispatch")
	taskIDFlag := fs.String("task", "", "link dispatch metadata to an existing native task id")
	taskIntent := fs.String("task-intent", "", "structured created-task intent: implement|review|audit|lifecycle")
	taskArtifact := fs.String("task-artifact", "", "structured created-task artifact")
	taskExpectedBase := fs.String("task-expected-base", "", "structured created-task expected base SHA")
	taskImplementer := fs.String("task-implementer", "", "structured created-task implementer")
	taskReviewer := fs.String("task-reviewer", "", "structured created-task reviewer")
	taskParallelWork := fs.Bool("task-parallel-work", false, "explicitly allow competing implementation for the artifact")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad dispatch - queue a durable task for a child and wake it to drain

Usage:
  amq-squad dispatch [--project DIR] [--profile NAME] --session S --role ROLE
                     [--from HANDLE] [--thread THREAD] [--kind todo] --subject SUBJ
                     (--body TEXT | --body-file FILE) [--priority P]
                     [--force] [--no-wake] [--wait-for STAGE] [--wait-timeout DURATION]
                     [--override-wait-posture --wait-posture-reason WHY]
                     [--override-namespace-conflict --reason WHY]
                     [--create-task | --task ID] [--json]

The deterministic lead-to-child dispatch. It does two things, in order:
  1. Sends a DURABLE AMQ message to the workstream's resolved root (the single
     source of truth), so the task survives even if the child is down. This is
     root-correct for an external lead, exactly like 'amq-squad amq send'.
  2. Nudges the child's exact tmux pane with a FIXED drain-only prompt so an
     idle agent wakes and runs 'amq drain'. The task body is NEVER injected into
     the pane — only the durable message carries it — so there is no double
     delivery. (Because amq-squad launched the agents it knows each one's exact
     tmux pane, so it wakes by pane id — a pane-precise, tmux-native nudge.)

By default the nudge is skipped when the agent looks busy (a prompt pushed over
a working agent is lost); the task stays queued and the agent drains it on its
next turn. Pass --force to nudge anyway, or --no-wake to queue without nudging.
When the recipient launch record uses --wake-inject-mode none, dispatch never
injects pane input. --force cannot override that zero-input contract and is
rejected before anything is queued; re-run without --force for durable-only
delivery.

After dispatch, the lead should read the child's completion/report message with
the printed root-correct 'amq drain --include-body --root ... --me ...' command.
Draining the task message does not prove the task is complete; wait for the
child's explicit report on the durable task thread.

With --create-task or --task ID, dispatch creates (when requested) and atomically
claims the native task before sending AMQ. Task state and message delivery are
separate in simple mode: a failed send leaves the task in_progress, with no
outbox, delivery state, or automatic retry. Inspect the AMQ mailbox before
deciding whether to send again.

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
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
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
	if err := ensureNoNamespaceConflictWithOverride("dispatch", projectDir, profile, workstream, flagWasSet(fs, "profile"), namespaceConflictOverrideOptions{
		Allowed: *overrideNamespaceConflict,
		Reason:  *overrideNamespaceReason,
	}); err != nil {
		return err
	}
	ns := squadnamespace.Resolve(projectDir, profile, workstream)
	from, err := resolveDispatchSender(t, *fromFlag)
	if err != nil {
		return err
	}
	if _, _, err := verifyRuntimeActionByHandle("dispatch authorizer", projectDir, profile, workstream, from); err != nil {
		return err
	}
	executionContract := executionContractForTeam(t, profile, workstream, "", "", "")
	currentActorContract := actorExecutionContractForTeam(t, member.Role, memberHandle(member), executionContract)
	currentActorMode := team.EffectiveActorMode(t, member)
	currentActorImplementationAllowed := currentActorContract.ImplementationAllowedForYou
	// Option 3 (#176): warn when the dispatcher handle differs from the
	// team.json configured lead. Children report to the task's From field
	// (the dispatcher), not the configured lead, so the operator needs to
	// know if they are routing to a different mailbox than they might expect.
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
	recipientWakeInjectMode := dispatchRecipientWakeInjectMode(ctx.Root, member.Handle)
	if *forceFlag && recipientWakeInjectMode == "none" {
		return usageErrorf("refusing --force pane nudge for zero-input worker %s: wake-inject-mode none is active; re-run without --force to queue the task durably", member.Handle)
	}

	taskID := strings.TrimSpace(*taskIDFlag)
	var createInput *taskstore.AddInput
	if *createTaskFlag && dispatchIntentRequiresImplementation(strings.TrimSpace(*taskIntent)) && !currentActorImplementationAllowed {
		return dispatchActorIntentRefusal("create-task", strings.TrimSpace(*taskIntent), currentActorContract, currentActorMode)
	}
	if *createTaskFlag {
		createInput = &taskstore.AddInput{
			Title: *subjectFlag, Description: taskBody, AssignTo: member.Handle,
			Intent: *taskIntent, Artifact: *taskArtifact, ExpectedBaseSHA: *taskExpectedBase,
			Implementer: *taskImplementer, Reviewer: *taskReviewer, ParallelWorkExplicit: *taskParallelWork,
		}
	}
	if taskID != "" {
		currentTask, err := taskstore.ShowForProfile(projectDir, profile, workstream, taskID)
		if err != nil {
			return err
		}
		if err := validateDispatchTask(currentTask, member.Handle, projectDir, profile, workstream); err != nil {
			return err
		}
		if dispatchIntentRequiresImplementation(currentTask.Intent) && !currentActorImplementationAllowed {
			return dispatchActorIntentRefusal("task "+currentTask.ID, currentTask.Intent, currentActorContract, currentActorMode)
		}
	}
	var claimedTask *taskstore.Task
	if taskID != "" || createInput != nil {
		if createInput != nil {
			created, addErr := taskstore.AddForProfile(projectDir, profile, workstream, *createInput, taskNow())
			if addErr != nil {
				return fmt.Errorf("create native task before dispatch: %w", addErr)
			}
			taskID = created.ID
		}
		claimed, _, claimErr := autoClaimDispatchedTask(projectDir, profile, workstream, taskID, member.Handle, taskNow())
		if claimErr != nil {
			return fmt.Errorf("claim native task %s before dispatch: %w", taskID, claimErr)
		}
		claimedTask = &claimed
	}

	sendCmd := dispatchSendArgs(ctx.Root, from, member.Handle, *threadFlag, *kindFlag, *subjectFlag, taskBody, *priorityFlag, waitFor, waitTimeout)
	waitPosture := waitPostureForContext("dispatch", "amq_delivery", ctx, waitTimeout, waitFor != "" && waitTimeout == 0, waitFor != "", *overrideWaitPosture, *waitPostureReason)
	out, sendResult, err := runOwnedAMQSend(ownedAMQSendOptions{
		WaitPosture: waitPosture,
	}, amqCommandRequest{Dir: projectDir, Env: amqCommandEnv(ctx), Arg: sendCmd})
	msgID := sendResult.MessageID
	if err != nil {
		if !dispatchSendWaitTimedOut(out, err, waitFor) {
			if claimedTask != nil {
				return fmt.Errorf("dispatch send to %s failed after task %s was claimed; the task remains in_progress and no automatic retry will occur (inspect AMQ before deciding whether to send again): %w", *roleFlag, taskID, err)
			}
			return fmt.Errorf("dispatch send to %s: %w", *roleFlag, err)
		}
	}
	waitTimedOut := dispatchSendWaitTimedOut(out, err, waitFor)
	// Print our OWN authoritative, session-aware summary rather than echoing
	// `amq send`'s raw line — that line renders an empty "session:" for a
	// --root-only send, which reads like a bug. We know the session, root, and
	// handle here; pull the message id out of amq's output. Fall back to amq's
	// raw line only if the id can't be parsed (so nothing is ever hidden).
	if !*jsonOut {
		if msgID != "" {
			taskText := ""
			if taskID != "" {
				taskText = fmt.Sprintf(" — task %s", taskID)
			}
			waitText := ""
			if waitFor != "" {
				if waitTimedOut {
					waitText = fmt.Sprintf("; queued, %s receipt unconfirmed after %s; do NOT re-send, nudge drain instead", waitFor, waitTimeout)
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

	outcome := dispatchOutcome{}
	if *noWakeFlag {
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
		quietNotice("Skipped pane nudge (--no-wake); %s drains the task on its next turn.\n", *roleFlag)
		return nil
	}

	wakeLive := dispatchRecipientWakeLive(projectDir, profile, *sessionFlag, flagWasSet(fs, "session"), *roleFlag)
	// A recipient launched with wake-inject-mode=none has an explicit zero-input
	// contract. Honor it before wake-first: a live none-mode sidecar emits an
	// attention notice but cannot inject a drain command, so it must not be
	// reported as queued_wake_delivered.
	if recipientWakeInjectMode == "none" {
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command:   "dispatch",
				Status:    "queued_zero_input",
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
		if wakeLive {
			quietNotice("Queued for zero-input worker %s; wake emitted a notice but did not inject a drain command.\n", member.Handle)
		} else {
			quietNotice("Skipped pane nudge for zero-input worker %s; the task is queued durably.\n", member.Handle)
		}
		return nil
	}

	if _, _, identityErr := verifyRuntimeActionByHandle("dispatch wake", projectDir, profile, workstream, member.Handle); identityErr != nil {
		fmt.Fprintf(os.Stderr, "warning: task queued, but recipient wake was refused: %v\n", identityErr)
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command: "dispatch", Status: "queued_wake_refused", Project: projectDir, Session: workstream, Profile: profile,
				Namespace: ns, ID: taskID, TaskID: taskID, Role: member.Role, Assignee: member.Handle, Handle: member.Handle,
				MessageID: msgID, Root: ctx.Root, Actions: dispatchFollowUpActions(projectDir, profile, workstream, ctx.Root, from, member.Handle, msgID),
			})
		}
		return nil
	}

	// Wake-first (#289): if the recipient has a positively-live wake sidecar, the
	// durable AMQ message wakes and drains it (reinforced by the #283/#288
	// drain-on-arrival injection). Normal worker direction must NOT inject pane
	// keystrokes; raw send-keys is last-resort recovery only. --force bypasses
	// this to force an explicit, clearly-marked pane override.
	if !*forceFlag && wakeLive {
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command:   "dispatch",
				Status:    "queued_wake_delivered",
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
		quietNotice("Dispatched to %s via durable AMQ + wake (recipient wake-live; no pane injection).\n", *roleFlag)
		return nil
	}

	outcome, werr := dispatchWakePane(projectDir, profile, *sessionFlag, flagWasSet(fs, "session"), *roleFlag, *forceFlag, ctx.Root)
	if werr != nil {
		// The durable task is already queued; a wake failure is advisory, not a
		// dispatch failure. Surface it (warnings bypass quietNotice) so the
		// operator can nudge or resume manually, but exit 0.
		fmt.Fprintf(os.Stderr, "warning: task queued, but the pane nudge failed: %v\n", werr)
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command:   "dispatch",
				Status:    "queued_nudge_failed",
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
	if outcome.PaneID != "" && outcome.SubmitState == "" {
		outcome.SubmitState = dispatchSubmitConfirmed
	}
	if *jsonOut {
		status := "queued"
		if outcome.PaneID != "" {
			status = "queued_and_nudged"
			switch outcome.SubmitState {
			case dispatchSubmitQueued:
				status = "queued_nudge_submit_queued"
			case dispatchSubmitUnconfirmed:
				status = "queued_nudge_submit_unconfirmed"
			}
		}
		return printJSONEnvelope("dispatch", mutationResult{
			Command:   "dispatch",
			Status:    status,
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
	if outcome.PaneID != "" {
		switch outcome.SubmitState {
		case dispatchSubmitQueued:
			quietNotice("Nudged %s pane %s to drain; the prompt is queued in the pane input and will submit when the agent goes idle.\n", *roleFlag, outcome.PaneID)
		case dispatchSubmitUnconfirmed:
			quietNotice("Nudged %s pane %s to drain, but submit was unconfirmed; durable task remains queued.\n", *roleFlag, outcome.PaneID)
		default:
			quietNotice("Nudged %s pane %s to drain.\n", *roleFlag, outcome.PaneID)
		}
	} else {
		quietNotice("Task queued; pane not nudged: %s\n", outcome.Skipped)
	}
	return nil
}

func dispatchIntentRequiresImplementation(intent string) bool {
	intent = strings.TrimSpace(intent)
	return intent == taskstore.IntentImplement || intent == taskstore.IntentLifecycle
}

func dispatchActorIntentRefusal(subject, intent string, actor actorExecutionData, actorMode string) error {
	return fmt.Errorf("%s dispatch refused: intent %s requires current_actor_implementation_allowed=true; actor %s/%s has EffectiveActorMode=%s and current_actor_implementation_allowed=false; route implementation or lifecycle work to an implementation actor", subject, intent, actor.ActorRole, actor.ActorHandle, actorMode)
}

func dispatchRecipientWakeInjectMode(root, handle string) string {
	rec, err := launch.Read(filepath.Join(root, "agents", strings.TrimSpace(handle)))
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(rec.WakeInjectMode))
}

func validateDispatchTask(t taskstore.Task, assignee, projectDir, profile, session string) error {
	assignee = strings.TrimSpace(assignee)
	if authority := taskstore.AuthorityActor(t); authority != "" && authority != assignee {
		return fmt.Errorf("task %s %s authority actor is %s; dispatch target uses handle %s", t.ID, t.Intent, authority, assignee)
	}
	assigned := strings.TrimSpace(t.AssignedTo)
	if assigned != "" && assigned != assignee {
		return fmt.Errorf("task %s is assigned to %s; dispatch target uses handle %s", t.ID, assigned, assignee)
	}
	switch t.Status {
	case taskstore.StatusPending:
		tasks, err := taskstore.ListForProfile(projectDir, profile, session)
		if err != nil {
			return err
		}
		byID := make(map[string]taskstore.Task, len(tasks))
		for _, task := range tasks {
			byID[task.ID] = task
		}
		for _, dep := range t.DependsOn {
			d, ok := byID[dep]
			if !ok {
				return fmt.Errorf("task %s depends on %s, which does not exist", t.ID, dep)
			}
			if d.Status != taskstore.StatusCompleted {
				return fmt.Errorf("task %s is blocked on %s (%s); complete it before dispatch", t.ID, dep, d.Status)
			}
		}
		return nil
	case taskstore.StatusInProgress:
		return nil
	case taskstore.StatusCompletedPendingReconcile, taskstore.StatusCompleted, taskstore.StatusFailed, taskstore.StatusBlocked, taskstore.StatusCancelled:
		return fmt.Errorf("task %s is %s; dispatch requires pending or in_progress", t.ID, t.Status)
	default:
		return fmt.Errorf("task %s has unknown status %q; dispatch requires pending or in_progress", t.ID, t.Status)
	}
}

func autoClaimDispatchedTask(projectDir, profile, session, taskID, assignee string, now time.Time) (taskstore.Task, bool, error) {
	current, err := taskstore.ShowForProfile(projectDir, profile, session, taskID)
	if err != nil {
		return taskstore.Task{}, false, err
	}
	if err := validateDispatchTask(current, assignee, projectDir, profile, session); err != nil {
		return taskstore.Task{}, false, err
	}
	switch current.Status {
	case taskstore.StatusPending:
		claimed, err := dispatchClaimTask(projectDir, profile, session, taskID, assignee, now)
		return claimed, err == nil, err
	case taskstore.StatusInProgress:
		return current, false, nil
	case taskstore.StatusCompletedPendingReconcile:
		return taskstore.Task{}, false, fmt.Errorf("task %s is completed_pending_reconcile; exact evidence reconciliation is required before dispatch", current.ID)
	default:
		return taskstore.Task{}, false, fmt.Errorf("task %s is %s; dispatch requires pending or in_progress", current.ID, current.Status)
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
	for _, m := range orderedTeamMembers(t.Members) {
		if strings.ToLower(m.Role) == role {
			return m, true
		}
	}
	return team.Member{}, false
}

// defaultDispatchRecipientWakeLive resolves the recipient's launch record and
// reports whether its wake sidecar is verified live (Signals.WakeAlive). Only a
// positively-live wake helper qualifies for wake-first delivery; anything
// uncertain (no record, no root, dead sidecar) returns false so dispatch falls
// back to the explicit last-resort pane nudge. Read-only.
func defaultDispatchRecipientWakeLive(projectDir, profile, session string, explicitSession bool, role string) bool {
	mr, workstream, err := resolveDispatchMemberRuntime(projectDir, profile, session, explicitSession, role)
	if err != nil || !mr.HasRecord {
		return false
	}
	root := strings.TrimSpace(mr.Record.Root)
	if root == "" {
		return false
	}
	live := classifyAgentLiveness(mr.AgentDir, root, mr.Profile, mr.Handle, mr.Member.Role, mr.Member.Binary, workstream, mr.CWD, defaultDuplicateLaunchProbe)
	return live.Signals.WakeAlive
}

// defaultDispatchWakePane is retained for direct runtime-control callers and
// tests. Production dispatch already owns the authoritative durable-send root
// and calls defaultDispatchWakePaneAtRoot so send and nudge cannot diverge.
func defaultDispatchWakePane(projectDir, profile, session string, explicitSession bool, role string, force bool) (dispatchOutcome, error) {
	mr, workstream, err := resolveDispatchMemberRuntime(projectDir, profile, session, explicitSession, role)
	if err != nil {
		return dispatchOutcome{}, err
	}
	ctx, err := resolveAMQContextForNamespace(projectDir, profile, workstream, mr.Handle)
	if err != nil {
		return dispatchOutcome{}, fmt.Errorf("resolve exact AMQ root for dispatch nudge: %w", err)
	}
	return dispatchWakePaneForRuntime(mr, workstream, force, ctx.Root)
}

func defaultDispatchWakePaneAtRoot(projectDir, profile, session string, explicitSession bool, role string, force bool, dispatchRoot string) (dispatchOutcome, error) {
	mr, workstream, err := resolveDispatchMemberRuntime(projectDir, profile, session, explicitSession, role)
	if err != nil {
		return dispatchOutcome{}, err
	}
	return dispatchWakePaneForRuntime(mr, workstream, force, dispatchRoot)
}

func dispatchWakePaneForRuntime(mr memberRuntime, workstream string, force bool, dispatchRoot string) (dispatchOutcome, error) {
	if reason, disabled := mr.nativePromptInjectionDisabledReason(); disabled {
		return dispatchOutcome{Skipped: reason + "; durable AMQ dispatch was queued"}, nil
	}
	panes, err := statusPaneLister()
	if err != nil {
		if tmuxpane.IsPermissionDenied(err) {
			return dispatchOutcome{}, errTmuxAccessDenied()
		}
		// The global `tmux list-panes -a` scan can fail wholesale under iTerm2
		// tmux -CC even when the recorded pane is still directly addressable.
		// Degrade to no scan and let resolveControlTarget address the recorded
		// id directly.
		panes = nil
	}
	paneID, _, ok := resolveControlTarget(mr, workstream, panes)
	if !ok || strings.TrimSpace(paneID) == "" {
		return dispatchOutcome{Skipped: "no live pane (the agent is not running; it will drain the queued task on next start)"}, nil
	}
	if !force {
		// Don't talk over a working agent: a prompt pushed into a busy pane lands
		// in a tool-result buffer and is lost. The durable task is still queued,
		// so skipping is safe — the agent drains it between turns.
		if busy, berr := paneBusyForSend(paneID); berr == nil && busy {
			return dispatchOutcome{Skipped: fmt.Sprintf("pane %s is busy (mid-turn); the agent drains the task when idle, or re-dispatch with --force", paneID)}, nil
		}
	}
	// Canonicalize the authoritative durable-send root (for example /var/... to
	// /private/var/... on Darwin). Never derive it from mr.AgentDir: the no-record
	// fallback may construct AgentDir below a worktree-local .agent-mail even
	// though the durable message was sent to the canonical team-home namespace.
	root := canonicalFilesystemPath(dispatchRoot)
	if strings.TrimSpace(root) == "" || root == "." {
		return dispatchOutcome{}, fmt.Errorf("resolve exact AMQ root for dispatch nudge from durable-send root %q", dispatchRoot)
	}
	err = sendPromptToPane(paneID, dispatchNudgePrompt(root))
	return classifyNudgeResult(paneID, err, paneBusyForSend)
}

// resolveDispatchMemberRuntime selects the recipient launch record through the
// same team-home scan and profile/session/team-home/handle authority used by
// status. Falling back to the legacy runtime resolver preserves support for
// members with no launch record, while recorded worktree-cwd members no longer
// get looked up beneath their worktree-local .agent-mail directory.
func resolveDispatchMemberRuntime(projectDir, profile, session string, explicitSession bool, role string) (memberRuntime, string, error) {
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return memberRuntime{}, "", fmt.Errorf("read team: %w", err)
	}
	workstream, err := resolveTeamWorkstreamName(t, session, explicitSession)
	if err != nil {
		return memberRuntime{}, "", err
	}
	member, ok := teamMemberByRole(t, role)
	if !ok {
		return memberRuntime{}, workstream, fmt.Errorf("no team member with role %q in this team", strings.ToLower(strings.TrimSpace(role)))
	}
	entries, scanErr := launch.ScanEntries(t.Project)
	if scanErr != nil {
		return memberRuntime{}, workstream, fmt.Errorf("scan launch records: %w", scanErr)
	}
	selection := selectStatusLaunchRecord(t, profile, member, workstream, defaultDuplicateLaunchProbe, entries)
	if len(selection.DuplicatePaths) > 0 {
		return memberRuntime{}, workstream, fmt.Errorf("multiple authoritative live launch records for %s: %s", memberHandle(member), strings.Join(selection.DuplicatePaths, ", "))
	}
	if !selection.Found {
		return resolveMemberRuntime(projectDir, profile, session, explicitSession, role)
	}
	rec := selection.Entry.Record
	handle := strings.TrimSpace(rec.Handle)
	if handle == "" {
		handle = memberHandle(member)
	}
	cwd := member.EffectiveCWD(t.Project)
	if strings.TrimSpace(rec.CWD) != "" {
		cwd = rec.CWD
	}
	return memberRuntime{
		Member: member, Profile: profile, Handle: handle, CWD: cwd,
		AgentDir: selection.Entry.AgentDir, HasRecord: true, Record: rec,
	}, workstream, nil
}

// classifyNudgeResult maps a pane-nudge result to a dispatchOutcome. A
// SubmitUnconfirmedError is ambiguous, NOT a failure: the Enter could not be
// confirmed, but often the agent was already woken (the amq wake sidecar drained
// the durable task first) and is now working — which is exactly why its input
// box looked unchanged. So if the pane is now busy, count the wake as delivered;
// otherwise report a soft skip (the durable task is queued and the worker drains
// it on its next turn). Only a hard error (dead pane, bracketed-paste leak,
// tmux denied) is a real failure. paneBusy is injected for testing.
func classifyNudgeResult(paneID string, sendErr error, paneBusy func(string) (bool, error)) (dispatchOutcome, error) {
	if sendErr == nil {
		return dispatchOutcome{PaneID: paneID, SubmitState: dispatchSubmitConfirmed}, nil
	}
	var queued *tmuxpane.QueuedInputError
	if errors.As(sendErr, &queued) {
		return dispatchOutcome{
			PaneID:      paneID,
			SubmitState: dispatchSubmitQueued,
			Detail:      fmt.Sprintf("pane %s has the prompt queued in its input; it will submit when the agent goes idle", paneID),
		}, nil
	}
	var unconfirmed *tmuxpane.SubmitUnconfirmedError
	if errors.As(sendErr, &unconfirmed) {
		detail := fmt.Sprintf("pane %s was staged and Enter was attempted, but submission could not be confirmed after %d attempts", paneID, unconfirmed.Attempts)
		if busy, berr := paneBusy(paneID); berr == nil && busy {
			detail += "; pane is now busy, so the agent may already be processing the durable task"
		} else {
			detail += "; pane may still need a manual Enter or a drain-only re-nudge"
		}
		return dispatchOutcome{PaneID: paneID, SubmitState: dispatchSubmitUnconfirmed, Detail: detail}, nil
	}
	return dispatchOutcome{}, sendErr
}
