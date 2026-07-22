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

// dispatchNudgePrompt is the FIXED, drain-only prompt amq-squad injects into a
// worker's pane after queuing a durable task. It deliberately carries NO task
// content: the task body lives only in the durable AMQ message (the single
// source of truth), so the worker reads it with `amq drain` and there is no risk
// of a pane-injected copy diverging from — or double-delivering — the queued
// message. amq-squad nudges through the agent's OWN tmux pane — which it launched
// and tracks by exact pane id — so it has a pane-precise, tmux-native way to poke
// an idle agent into draining, independent of amq's own wake path.
const dispatchNudgePrompt = "amq-squad dispatch: a new message is queued in your inbox. " +
	"Run `amq drain --include-body` now and act on the newest item. Do not wait to be polled."

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

// dispatchWakePane delivers dispatchNudgePrompt to a member's live pane. It is a
// package var so tests can drive runDispatch without a tmux server.
var dispatchWakePane = defaultDispatchWakePane

// dispatchRecipientWakeLive reports whether the dispatch recipient currently has
// a positively-live wake sidecar, so dispatch can rely on durable AMQ + wake
// delivery (#289) instead of injecting pane keystrokes. Package var so tests can
// drive the wake-first branch without real liveness probing.
var dispatchRecipientWakeLive = defaultDispatchRecipientWakeLive

// dispatchLinkTask and dispatchClaimTask retain the legacy auto-claim helper's
// test seam. Production task-backed dispatch uses the transaction/outbox seams
// below so no AMQ announcement can precede the durable claim and intent.
var dispatchLinkTask = taskstore.LinkDispatchForProfile
var dispatchClaimTask = taskstore.ClaimForProfile

// Task-backed dispatch commits claim + outbox intent before AMQ send. These
// seams let crash/failure tests prove that no announcement precedes commit.
var dispatchPrepareTask = taskstore.PrepareDispatchForProfile
var dispatchBeginTaskDelivery = taskstore.BeginOutboxDeliveryForProfile
var dispatchFinishTask = taskstore.FinishDispatchForProfile

// dispatchAfterLeadershipRead is a deterministic race seam. Production is a
// no-op; tests commit a leadership handoff after the advisory outer read and
// prove the task-store transaction rejects the stale sender/epoch atomically.
var dispatchAfterLeadershipRead = func(projectDir, profile, session string, state taskstore.LeadershipState) error {
	return nil
}

// dispatchAfterGenerationRead is a deterministic race seam. Production is a
// no-op. Tests use it after CURRENT and both actor launch records agree, while
// the namespace -> prepared reader admission is still held, to prove a new
// preparation cannot overtake the authoritative dispatch transaction.
var dispatchAfterGenerationRead = func(projectDir, profile, session string, ref *taskstore.GenerationRef) error {
	return nil
}

func runDispatch(args []string) error {
	fs := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "workstream session of the team")
	roleFlag := fs.String("role", "", "role of the child agent to dispatch the task to")
	fromFlag := fs.String("from", "", "sender handle (default: the orchestration lead, else AM_ME)")
	leadershipEpoch := fs.Uint64("leadership-epoch", 0, "required current leadership epoch after a durable handoff")
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

After dispatch, the lead should collect the child's completion/report message
with the printed root-correct 'amq-squad collect --session ... --me ...'
command. Drain receipts only prove the child saw the task; they do not prove the
task is complete.

With --create-task or --task ID, dispatch first commits the native task claim
and a pending delivery intent, then marks that intent sending, and only then
sends AMQ. A failed send remains an explicit failed outbox entry; a crash after
send but before finalization remains delivery-uncertain and is never retried
automatically.

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
	if err := ensureTargetIsNotOperator(t, "dispatch", *roleFlag); err != nil {
		return err
	}
	member, ok := teamMemberByRole(t, *roleFlag)
	if !ok {
		return fmt.Errorf("no team member with role %q in this team", *roleFlag)
	}
	workstream, err := resolveTeamWorkstreamName(t, resolvedContext.Session, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(projectDir, profile, workstream), memberHandle(member))
	if err != nil {
		return err
	}
	admission, err := acquirePreparedTaskMutationAdmission(projectDir, profile, workstream)
	if err != nil {
		return err
	}
	defer admission.close()
	currentContext, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, *fromFlag, fs)
	if err != nil {
		return fmt.Errorf("dispatch refused: context re-resolution under admission failed: %w", err)
	}
	if err := validateReResolvedContext(resolvedContext, currentContext, false); err != nil {
		return err
	}
	currentTeam, err := team.ReadProfile(currentContext.ProjectDir, currentContext.Profile)
	if err != nil {
		return fmt.Errorf("dispatch refused: reread team under admission: %w", err)
	}
	currentWorkstream, err := resolveTeamWorkstreamName(currentTeam, currentContext.Session, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	currentMember, ok := teamMemberByRole(currentTeam, *roleFlag)
	if !ok {
		return fmt.Errorf("dispatch refused: target role %q changed before admission", *roleFlag)
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(currentContext.ProjectDir, currentContext.Profile, currentWorkstream), memberHandle(currentMember))
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("dispatch", initialIdentity, currentIdentity); err != nil {
		return err
	}
	if member.Binary != currentMember.Binary || member.Session != currentMember.Session || canonicalPath(member.EffectiveCWD(t.Project)) != canonicalPath(currentMember.EffectiveCWD(currentTeam.Project)) {
		return fmt.Errorf("dispatch refused: target role %q identity changed before admission; retry", *roleFlag)
	}
	resolvedContext, projectDir, profile, t, workstream, member = currentContext, currentContext.ProjectDir, currentContext.Profile, currentTeam, currentWorkstream, currentMember
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
	leadership, err := taskstore.ReadLeadershipForProfile(projectDir, profile, workstream)
	if err != nil {
		return fmt.Errorf("read leadership authority before dispatch: %w", err)
	}
	if leadership.Epoch > 0 {
		if !flagWasSet(fs, "leadership-epoch") || *leadershipEpoch != leadership.Epoch {
			return fmt.Errorf("dispatch refused: leadership epoch is %d; pass --leadership-epoch %d after recovering the current record", leadership.Epoch, leadership.Epoch)
		}
		if from != leadership.CurrentLead {
			return fmt.Errorf("dispatch refused: sender %q is stale at leadership epoch %d; current lead is %q", from, leadership.Epoch, leadership.CurrentLead)
		}
	} else if flagWasSet(fs, "leadership-epoch") && *leadershipEpoch != 0 {
		return fmt.Errorf("dispatch refused: no durable leadership handoff exists; expected backward-compatible epoch 0")
	}
	if err := dispatchAfterLeadershipRead(projectDir, profile, workstream, leadership); err != nil {
		return fmt.Errorf("dispatch leadership race seam: %w", err)
	}
	receipt := newDeliveryReceipt(projectDir, profile, workstream, member.Role, member.Handle, effectiveTeamExecutionMode(t), "dispatch")
	// Reserve the deterministic path in memory so a task outbox can link it in
	// the atomic authority transaction. The file itself is not created until
	// after that transaction succeeds, so an epoch refusal leaves no receipt.
	receipt.Path = filepath.Join(deliveryReceiptDir(projectDir, profile, workstream), receipt.AttemptID+".json")
	executionContract := executionContractForTeam(t, profile, workstream, "", "", "")
	currentActorContract := actorExecutionContractForTeam(t, member.Role, memberHandle(member), executionContract)
	currentActorMode := team.EffectiveActorMode(t, member)
	leadActorContract := actorExecutionData{}
	if leadMember, ok := teamMemberByRole(t, t.Lead); ok {
		leadActorContract = actorExecutionContractForTeam(t, leadMember.Role, memberHandle(leadMember), executionContract)
	}
	leadImplementationAllowed := leadActorContract.ImplementationAllowedForYou
	currentActorImplementationAllowed := currentActorContract.ImplementationAllowedForYou
	receipt.LeadImplementationAllowed = &leadImplementationAllowed
	receipt.CurrentActorImplementationAllowed = &currentActorImplementationAllowed
	receipt.Method = "durable_amq"
	receipt.addStage("queued_amq", "dispatch accepted by amq-squad")
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

	// Resolve the workstream root for the SENDER. The durable message lands in
	// .agent-mail/<workstream> regardless of which session the lead runs from,
	// so an external lead (no AM_ROOT injected) reaches the child's real mailbox
	// instead of the default .agent-mail (#152's misroute, the root cause #153
	// builds on).
	cwd := member.EffectiveCWD(t.Project)
	ctx, err := resolveAMQContextForNamespace(cwd, profile, workstream, from)
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
	receipt.Sender = from
	receipt.Recipient = member.Handle
	receipt.Recipients = []string{member.Handle}
	receipt.Consumers = []deliveryConsumerState{{Consumer: member.Handle, State: deliveryStateAmbiguousUnknown}}
	receipt.Root = ctx.Root
	receipt.Thread = strings.TrimSpace(*threadFlag)
	if receipt.Thread == "" {
		receipt.Thread = receiptCanonicalP2P(from, member.Handle)
	}
	receipt.EvidenceSource = "amq_send_output"
	receipt.addStage(deliveryStateAmbiguousUnknown, "receipt reserved before task link and AMQ send; no blind retry if interrupted")
	var prepared *taskstore.DispatchPrepareResult
	if taskID != "" || createInput != nil {
		generationRef, err := dispatchGenerationRef(projectDir, profile, workstream, ctx.Root, from, member.Handle)
		if err != nil {
			return fmt.Errorf("resolve native task dispatch generation: %w", err)
		}
		if err := dispatchAfterGenerationRead(projectDir, profile, workstream, generationRef); err != nil {
			return fmt.Errorf("dispatch generation race seam: %w", err)
		}
		preparedAt := taskNow()
		p, err := dispatchPrepareTask(projectDir, profile, workstream, taskID, taskstore.DispatchIntentOptions{
			From: from, Assignee: member.Handle, Thread: receipt.Thread, Kind: *kindFlag,
			Subject: *subjectFlag, Body: taskBody, ReceiptAttemptID: receipt.AttemptID, ReceiptPath: receipt.Path,
			LeaseDuration: taskstore.DefaultLeaseDuration, Now: preparedAt,
			Create:        createInput,
			GenerationRef: generationRef,
			Leadership: taskstore.LeadershipExpectation{
				Sender: from, ExpectedEpoch: *leadershipEpoch, EpochSpecified: flagWasSet(fs, "leadership-epoch"),
			},
		})
		if err != nil {
			return fmt.Errorf("prepare native task %s dispatch transaction: %w", taskID, err)
		}
		taskID = p.Task.ID
		receipt.TaskID = taskID
		receipt.OutboxIntentID = p.Intent.ID
		if p.LeadershipEpoch != nil {
			epoch := *p.LeadershipEpoch
			receipt.LeadershipEpoch = &epoch
		}
		started, err := dispatchBeginTaskDelivery(projectDir, profile, workstream, taskID, p.Intent.ID, preparedAt.Add(time.Nanosecond))
		if err != nil {
			markDeliveryFailedBeforeID(projectDir, profile, workstream, &receipt, err)
			return fmt.Errorf("begin native task %s delivery: %w", taskID, err)
		}
		p.Intent = started
		if err := persistDeliveryReceipt(projectDir, profile, workstream, &receipt); err != nil {
			cause := fmt.Errorf("link dispatch receipt %s to task outbox %s (send not attempted): %w", receipt.AttemptID, p.Intent.ID, err)
			finished, finishedIntent, finishErr := dispatchFinishTask(projectDir, profile, workstream, taskID, p.Intent.ID, taskstore.Dispatch{
				Sender: from, Assignee: member.Handle, Thread: receipt.Thread, Kind: *kindFlag,
				Subject: *subjectFlag, ReceiptAttemptID: receipt.AttemptID, ReceiptPath: receipt.Path,
			}, taskstore.DeliveryOutcome{State: taskstore.DeliveryFailedBeforeInvoke, Error: cause.Error()}, taskNow())
			if finishErr != nil {
				return fmt.Errorf("%v; finalize proven pre-invocation failure: %w", cause, finishErr)
			}
			p.Task, p.Intent = finished, finishedIntent
			markDeliveryFailedBeforeID(projectDir, profile, workstream, &receipt, cause)
			return cause
		}
		prepared = &p
	} else if err := persistDeliveryReceipt(projectDir, profile, workstream, &receipt); err != nil {
		return fmt.Errorf("reserve dispatch receipt: %w", err)
	}

	sendCmd := dispatchSendArgs(ctx.Root, from, member.Handle, *threadFlag, *kindFlag, *subjectFlag, taskBody, *priorityFlag, waitFor, waitTimeout)
	waitPosture := waitPostureForContext("dispatch", "delivery_receipt", ctx, waitTimeout, waitFor != "" && waitTimeout == 0, waitFor != "", *overrideWaitPosture, *waitPostureReason)
	out, sendReceipt, err := runOwnedDurableSend(durableSendOptions{
		ProjectDir: projectDir, Profile: profile, Session: workstream, Role: member.Role,
		ExecutionMode: effectiveTeamExecutionMode(t), Kind: "dispatch", TaskID: taskID, Receipt: &receipt,
		WaitPosture: waitPosture,
	}, amqCommandRequest{Dir: cwd, Env: amqCommandEnv(ctx), Arg: sendCmd})
	receipt = *sendReceipt
	msgID := receipt.MessageID
	if prepared != nil {
		finished, finishedIntent, finishErr := dispatchFinishTask(projectDir, profile, workstream, taskID, prepared.Intent.ID, taskstore.Dispatch{
			Sender: from, Assignee: member.Handle, Thread: receipt.Thread, Kind: *kindFlag,
			Subject: *subjectFlag, MessageID: msgID, ReceiptAttemptID: receipt.AttemptID, ReceiptPath: receipt.Path,
		}, taskDeliveryOutcome(&receipt, err), taskNow())
		if finishErr != nil {
			return fmt.Errorf("finalize native task %s dispatch outcome (delivery may be uncertain): %w", taskID, finishErr)
		}
		prepared.Task, prepared.Intent = finished, finishedIntent
	}
	if err != nil {
		if !dispatchSendWaitTimedOut(out, err, waitFor) {
			return fmt.Errorf("dispatch send to %s: %w", *roleFlag, err)
		}
	}
	receipt.MessageID = msgID
	receipt.Root = ctx.Root
	if thread := strings.TrimSpace(*threadFlag); thread != "" {
		receipt.Thread = thread
	}
	receipt.Status = "written_to_amq"
	receipt.addStage("written_to_amq", "durable AMQ message written to recipient inbox")
	waitTimedOut := dispatchSendWaitTimedOut(out, err, waitFor)
	if waitTimedOut {
		receipt.addStage("amq_wait_timeout", fmt.Sprintf("durable message queued, but %s receipt was not observed before timeout %s; do not re-send", waitFor, waitTimeout))
	} else if waitFor != "" {
		receipt.addStage("amq_wait_"+waitFor, fmt.Sprintf("amq send waited for %s receipt with timeout %s", waitFor, waitTimeout))
	}
	if prepared != nil {
		if prepared.DidClaim {
			receipt.addStage("task_claimed", fmt.Sprintf("native task %s marked in_progress for %s", taskID, member.Handle))
		} else if prepared.Task.Status == taskstore.StatusInProgress {
			receipt.addStage("task_already_in_progress", fmt.Sprintf("native task %s already in_progress for %s", taskID, member.Handle))
		}
	}
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
		fmt.Printf("Next: collect the child report with `%s`\n", dispatchCollectCommand(projectDir, workstream, from))
	}

	outcome := dispatchOutcome{}
	if *noWakeFlag {
		receipt.TaskID = taskID
		receipt.Method = "durable_amq_only"
		receipt.addStage("wake_skipped", "--no-wake requested; recipient must drain without pane nudge")
		if err := writeDeliveryReceipt(projectDir, profile, workstream, &receipt); err != nil {
			return err
		}
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command:         "dispatch",
				Status:          "queued",
				Project:         projectDir,
				Session:         workstream,
				Profile:         profile,
				Namespace:       ns,
				ID:              taskID,
				TaskID:          taskID,
				Role:            member.Role,
				Assignee:        member.Handle,
				Handle:          member.Handle,
				MessageID:       msgID,
				Root:            ctx.Root,
				Actions:         dispatchFollowUpActions(projectDir, profile, workstream, from, member.Handle, msgID),
				DeliveryReceipt: &receipt,
			})
		}
		quietNotice("Skipped pane nudge (--no-wake); %s drains the task on its next turn.\n", *roleFlag)
		return nil
	}

	if _, _, identityErr := verifyRuntimeActionByHandle("dispatch wake", projectDir, profile, workstream, member.Handle); identityErr != nil {
		receipt.TaskID = taskID
		receipt.Status = "wake_failed"
		receipt.Method = "durable_amq_wake_refused"
		receipt.Detail = identityErr.Error()
		receipt.addStage("wake_identity_refused", identityErr.Error())
		if err := writeDeliveryReceipt(projectDir, profile, workstream, &receipt); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "warning: task queued, but recipient wake was refused: %v\n", identityErr)
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command: "dispatch", Status: "queued_wake_refused", Project: projectDir, Session: workstream, Profile: profile,
				Namespace: ns, ID: taskID, TaskID: taskID, Role: member.Role, Assignee: member.Handle, Handle: member.Handle,
				MessageID: msgID, Root: ctx.Root, Actions: dispatchFollowUpActions(projectDir, profile, workstream, from, member.Handle, msgID), DeliveryReceipt: &receipt,
			})
		}
		return nil
	}

	wakeLive := dispatchRecipientWakeLive(projectDir, profile, *sessionFlag, flagWasSet(fs, "session"), *roleFlag)
	// A recipient launched with wake-inject-mode=none has an explicit zero-input
	// contract. Honor it before wake-first: a live none-mode sidecar emits an
	// attention notice but cannot inject a drain command, so it must not be
	// reported as queued_wake_delivered.
	if recipientWakeInjectMode == "none" {
		receipt.TaskID = taskID
		receipt.Status = "queued_zero_input"
		stage := "wake_skipped_zero_input"
		detail := "recipient launch record requires wake-inject-mode none; durable task queued with zero pane input"
		if wakeLive {
			receipt.Method = "durable_amq+wake_notice"
			stage = "wake_notice_zero_input"
			detail = "recipient wake sidecar is live in none mode; durable task queued and wake notice emitted, but no drain input was injected"
		} else {
			receipt.Method = "durable_amq_only"
		}
		receipt.addStage(stage, detail)
		if err := writeDeliveryReceipt(projectDir, profile, workstream, &receipt); err != nil {
			return err
		}
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command:         "dispatch",
				Status:          "queued_zero_input",
				Project:         projectDir,
				Session:         workstream,
				Profile:         profile,
				Namespace:       ns,
				ID:              taskID,
				TaskID:          taskID,
				Role:            member.Role,
				Assignee:        member.Handle,
				Handle:          member.Handle,
				MessageID:       msgID,
				Root:            ctx.Root,
				Actions:         dispatchFollowUpActions(projectDir, profile, workstream, from, member.Handle, msgID),
				DeliveryReceipt: &receipt,
			})
		}
		if wakeLive {
			quietNotice("Queued for zero-input worker %s; wake emitted a notice but did not inject a drain command.\n", member.Handle)
		} else {
			quietNotice("Skipped pane nudge for zero-input worker %s; the task is queued durably.\n", member.Handle)
		}
		return nil
	}

	// Wake-first (#289): if the recipient has a positively-live wake sidecar, the
	// durable AMQ message wakes and drains it (reinforced by the #283/#288
	// drain-on-arrival injection). Normal worker direction must NOT inject pane
	// keystrokes; raw send-keys is last-resort recovery only. --force bypasses
	// this to force an explicit, clearly-marked pane override.
	if !*forceFlag && wakeLive {
		receipt.TaskID = taskID
		receipt.Method = "durable_amq+wake"
		receipt.Status = "queued_wake_delivered"
		receipt.addStage("wake_delivered", "recipient is wake-live; the durable AMQ message wakes and drains it (no pane injection)")
		if err := writeDeliveryReceipt(projectDir, profile, workstream, &receipt); err != nil {
			return err
		}
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command:         "dispatch",
				Status:          "queued_wake_delivered",
				Project:         projectDir,
				Session:         workstream,
				Profile:         profile,
				Namespace:       ns,
				ID:              taskID,
				TaskID:          taskID,
				Role:            member.Role,
				Assignee:        member.Handle,
				Handle:          member.Handle,
				MessageID:       msgID,
				Root:            ctx.Root,
				Actions:         dispatchFollowUpActions(projectDir, profile, workstream, from, member.Handle, msgID),
				DeliveryReceipt: &receipt,
			})
		}
		quietNotice("Dispatched to %s via durable AMQ + wake (recipient wake-live; no pane injection).\n", *roleFlag)
		return nil
	}

	if *forceFlag {
		receipt.addStage("nudge_requested", "explicit --force pane nudge override requested (bypasses wake-first)")
	} else {
		receipt.addStage("nudge_requested", "recipient not confidently wake-live; requesting LAST-RESORT pane prompt nudge")
	}
	outcome, werr := dispatchWakePane(projectDir, profile, *sessionFlag, flagWasSet(fs, "session"), *roleFlag, *forceFlag)
	if werr != nil {
		receipt.TaskID = taskID
		receipt.Status = "wake_failed"
		receipt.Method = "durable_amq_wake_failed"
		receipt.Detail = werr.Error()
		var leak *tmuxpane.BracketedPasteLeakError
		var unavailable *tmuxpane.BracketedPasteCheckUnavailableError
		switch {
		case errors.As(werr, &leak):
			receipt.Status = "bracketed_paste_leak"
			receipt.Method = "durable_amq_plus_prompt_fallback"
			receipt.PaneID = leak.PaneID
			receipt.Fallback = true
			receipt.addStage("bracketed_paste_leak", "pane nudge stopped before Enter after bracketed-paste markers leaked: "+werr.Error())
		case errors.As(werr, &unavailable):
			receipt.Status = "bracketed_paste_check_unavailable"
			receipt.Method = "durable_amq_plus_prompt_fallback"
			receipt.PaneID = unavailable.PaneID
			receipt.Fallback = true
			receipt.addStage("bracketed_paste_check_unavailable", "pane nudge stopped before Enter because bracketed-paste inspection was unavailable: "+werr.Error())
		default:
			receipt.addStage("failed", "pane nudge failed after durable AMQ write: "+werr.Error())
		}
		if err := writeDeliveryReceipt(projectDir, profile, workstream, &receipt); err != nil {
			return err
		}
		// The durable task is already queued; a wake failure is advisory, not a
		// dispatch failure. Surface it (warnings bypass quietNotice) so the
		// operator can nudge or resume manually, but exit 0.
		fmt.Fprintf(os.Stderr, "warning: task queued, but the pane nudge failed: %v\n", werr)
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command:         "dispatch",
				Status:          "queued_nudge_failed",
				Project:         projectDir,
				Session:         workstream,
				Profile:         profile,
				Namespace:       ns,
				ID:              taskID,
				TaskID:          taskID,
				Role:            member.Role,
				Assignee:        member.Handle,
				Handle:          member.Handle,
				MessageID:       msgID,
				Root:            ctx.Root,
				Actions:         dispatchFollowUpActions(projectDir, profile, workstream, from, member.Handle, msgID),
				DeliveryReceipt: &receipt,
			})
		}
		return nil
	}
	receipt.TaskID = taskID
	if outcome.PaneID != "" {
		receipt.PaneID = outcome.PaneID
		receipt.Fallback = true
		// Preserve the legacy method + prompt_staged stage for existing pane-fallback
		// consumers; mark the #289 last-resort / --force semantics ADDITIVELY with an
		// extra recovery stage so nothing existing is renamed.
		receipt.Method = "durable_amq_plus_prompt_fallback"
		receipt.addStage("prompt_staged", "fixed drain-only pane prompt staged; this is fallback delivery, not an AMQ acknowledgement")
		if *forceFlag {
			receipt.addStage("forced_pane_injection", "explicit --force pane override (bypasses wake-first); pane injection, not an AMQ acknowledgement")
		} else {
			receipt.addStage("last_resort_pane_injection", "LAST-RESORT pane injection: recipient not wake-live, so the durable task got a best-effort pane nudge")
		}
		receipt.addStage("submit_attempted", "attempted to submit the staged drain-only prompt")
		switch outcome.SubmitState {
		case dispatchSubmitQueued:
			receipt.Status = dispatchSubmitQueued
			receipt.Detail = outcome.Detail
			receipt.addStage(dispatchSubmitQueued, outcome.Detail)
		case dispatchSubmitUnconfirmed:
			receipt.Status = dispatchSubmitUnconfirmed
			receipt.Detail = outcome.Detail
			receipt.addStage(dispatchSubmitUnconfirmed, outcome.Detail)
		default:
			receipt.Status = dispatchSubmitConfirmed
			receipt.Acknowledged = true
			receipt.addStage(dispatchSubmitConfirmed, "Enter submission confirmed by observed input-region change")
			outcome.SubmitState = dispatchSubmitConfirmed
		}
	} else {
		receipt.Status = "wake_pending"
		receipt.Detail = outcome.Skipped
		receipt.addStage("wake_pending", "pane nudge skipped: "+outcome.Skipped)
	}
	if err := writeDeliveryReceipt(projectDir, profile, workstream, &receipt); err != nil {
		return err
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
			Command:         "dispatch",
			Status:          status,
			Project:         projectDir,
			Session:         workstream,
			Profile:         profile,
			Namespace:       ns,
			ID:              taskID,
			TaskID:          taskID,
			Role:            member.Role,
			Assignee:        member.Handle,
			Handle:          member.Handle,
			MessageID:       msgID,
			Root:            ctx.Root,
			Actions:         dispatchFollowUpActions(projectDir, profile, workstream, from, member.Handle, msgID),
			DeliveryReceipt: &receipt,
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

func dispatchGenerationRef(project, profile, session, root, sender, assignee string) (*taskstore.GenerationRef, error) {
	type observed struct {
		handle string
		ref    taskstore.GenerationRef
		any    bool
		err    error
	}
	read := func(handle string) observed {
		rec, err := launch.Read(filepath.Join(root, "agents", strings.TrimSpace(handle)))
		if err != nil {
			return observed{handle: handle, err: err}
		}
		ref := taskstore.GenerationRef{Generation: rec.PreparedRunGeneration, ManifestDigest: rec.PreparedRunDigest, GoalNamespace: rec.PreparedRunGoalNamespace, GoalDigest: rec.PreparedRunGoalDigest}
		return observed{handle: handle, ref: ref, any: ref.Generation != "" || ref.ManifestDigest != "" || ref.GoalNamespace != "" || ref.GoalDigest != ""}
	}
	left, right := read(sender), read(assignee)
	prepared, preparedErr := currentPreparedGenerationRef(project, profile, session)
	if preparedErr != nil {
		return nil, preparedErr
	}
	if prepared == nil {
		if !left.any && !right.any && (left.err == nil || errors.Is(left.err, os.ErrNotExist)) && (right.err == nil || errors.Is(right.err, os.ErrNotExist)) {
			return nil, nil
		}
		return nil, fmt.Errorf("launch records carry prepared identity but the namespace has no accepted prepared artifact")
	}
	for _, item := range []observed{left, right} {
		if item.err != nil {
			return nil, fmt.Errorf("managed generation requires launch record for %s: %w", item.handle, item.err)
		}
		if err := taskstore.ValidateGenerationRef(item.ref); err != nil {
			return nil, fmt.Errorf("managed generation for %s is incomplete: %w", item.handle, err)
		}
	}
	if left.ref != right.ref {
		return nil, fmt.Errorf("sender %s and assignee %s launch generation_ref values disagree", sender, assignee)
	}
	if left.ref != *prepared {
		return nil, fmt.Errorf("launch generation_ref does not match the current accepted prepared artifact")
	}
	ref := left.ref
	return &ref, nil
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

func dispatchCollectCommand(projectDir, session, me string) string {
	return "amq-squad collect --project " + shellQuote(projectDir) +
		" --session " + shellQuote(session) +
		" --me " + shellQuote(me) +
		" --timeout 120s --include-body"
}

func dispatchFollowUpActions(projectDir, profile, session, from, recipient, msgID string) []mutationAction {
	actions := []mutationAction{
		followUp("collect", "collect child report", dispatchCollectCommand(projectDir, session, from)),
	}
	if strings.TrimSpace(msgID) != "" {
		actions = append(actions, followUp("receipts", "wait for drain receipt", "amq-squad amq receipts wait --project "+shellQuote(projectDir)+" --session "+shellQuote(session)+" --me "+shellQuote(recipient)+" --msg-id "+shellQuote(msgID)+" --stage drained"))
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
	mr, workstream, err := resolveMemberRuntime(projectDir, profile, session, explicitSession, role)
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

func defaultDispatchWakePane(projectDir, profile, session string, explicitSession bool, role string, force bool) (dispatchOutcome, error) {
	mr, workstream, err := resolveMemberRuntime(projectDir, profile, session, explicitSession, role)
	if err != nil {
		return dispatchOutcome{}, err
	}
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
		if busy, berr := tmuxpane.PaneBusy(paneID); berr == nil && busy {
			return dispatchOutcome{Skipped: fmt.Sprintf("pane %s is busy (mid-turn); the agent drains the task when idle, or re-dispatch with --force", paneID)}, nil
		}
	}
	err = tmuxpane.SendPromptToPane(paneID, dispatchNudgePrompt)
	return classifyNudgeResult(paneID, err, tmuxpane.PaneBusy)
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
