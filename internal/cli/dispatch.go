package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

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
// pane that was nudged (empty when none was). Skipped carries a human-readable
// reason the nudge did not happen (no live pane, or busy without --force); a
// skip is NOT an error because the durable task is already queued.
type dispatchOutcome struct {
	PaneID  string
	Skipped string
}

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

// dispatchLinkTask is a seam for partial-failure tests around post-send task
// metadata linkage. Production uses taskstore.LinkDispatch directly.
var dispatchLinkTask = taskstore.LinkDispatch

func runDispatch(args []string) error {
	fs := flag.NewFlagSet("dispatch", flag.ContinueOnError)
	sessionFlag := fs.String("session", "", "workstream session of the team")
	roleFlag := fs.String("role", "", "role of the child agent to dispatch the task to")
	fromFlag := fs.String("from", "", "sender handle (default: the orchestration lead, else AM_ME)")
	threadFlag := fs.String("thread", "", "AMQ thread to send on, e.g. p2p/<lead>__<role> (default: amq's auto thread)")
	kindFlag := fs.String("kind", "todo", "AMQ message kind (todo, question, status, ...)")
	subjectFlag := fs.String("subject", "", "task subject line")
	body := fs.String("body", "", "task body (alternative to --body-file)")
	bodyFile := fs.String("body-file", "", "read the task body from this file ('-' for stdin)")
	priorityFlag := fs.String("priority", "", "message priority: urgent, normal, low")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	forceFlag := fs.Bool("force", false, "nudge the pane even if the agent looks busy (mid-turn)")
	noWakeFlag := fs.Bool("no-wake", false, "queue the durable task without nudging the pane")
	createTaskFlag := fs.Bool("create-task", false, "create and link a native task-store task before dispatch")
	taskIDFlag := fs.String("task", "", "link dispatch metadata to an existing native task id")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad dispatch - queue a durable task for a child and wake it to drain

Usage:
  amq-squad dispatch [--project DIR] [--profile NAME] --session S --role ROLE
                     [--from HANDLE] [--thread THREAD] [--kind todo] --subject SUBJ
                     (--body TEXT | --body-file FILE) [--priority P]
                     [--force] [--no-wake] [--create-task | --task ID] [--json]

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

After dispatch, the lead should collect the child's completion/report message
with the printed root-correct 'amq-squad collect --session ... --me ...'
command. Drain receipts only prove the child saw the task; they do not prove the
task is complete.

Examples:
  amq-squad dispatch --session issue-96 --role qa --subject "Validate PR #64" --body "Run the suite and report risk."
  amq-squad dispatch --session issue-96 --role fullstack --thread p2p/cto__fullstack --subject "Build X" --body-file ./task.md
  amq-squad dispatch --session issue-96 --role cto --kind question --subject "Approve merge?" --body-file ./ask.md
  amq-squad dispatch --session issue-96 --role fullstack --from cto --subject "Build X" --body "..." --force
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
	taskBody, err := readPromptBody(*body, *bodyFile, flagWasSet(fs, "body"), flagWasSet(fs, "body-file"), os.Stdin, stdinIsInteractive())
	if err != nil {
		return err
	}

	projectDir, profile, err := resolveProjectProfile(*projectFlag, *profileFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	if !team.ExistsProfile(projectDir, profile) {
		return fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return fmt.Errorf("read team: %w", err)
	}
	member, ok := teamMemberByRole(t, *roleFlag)
	if !ok {
		return fmt.Errorf("no team member with role %q in this team", *roleFlag)
	}
	workstream, err := resolveTeamWorkstreamName(t, *sessionFlag, flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	from, err := resolveDispatchSender(t, *fromFlag)
	if err != nil {
		return err
	}
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
	ctx, err := resolveAMQContextForProject(cwd, workstream, from)
	if err != nil {
		return fmt.Errorf("resolve amq root for dispatch: %w", err)
	}
	ctx.Me = from

	taskID := strings.TrimSpace(*taskIDFlag)
	if *createTaskFlag {
		created, err := taskstore.Add(projectDir, workstream, taskstore.AddInput{
			Title:       *subjectFlag,
			Description: taskBody,
			AssignTo:    member.Handle,
		}, taskNow())
		if err != nil {
			return fmt.Errorf("create native task for dispatch: %w", err)
		}
		taskID = created.ID
	} else if taskID != "" {
		if _, err := taskstore.Show(projectDir, workstream, taskID); err != nil {
			return fmt.Errorf("link native task for dispatch: %w", err)
		}
	}

	sendCmd := dispatchSendArgs(ctx.Root, from, member.Handle, *threadFlag, *kindFlag, *subjectFlag, taskBody, *priorityFlag)
	out, err := runAMQCommand(amqCommandRequest{Dir: cwd, Env: amqCommandEnv(ctx), Arg: sendCmd})
	if err != nil {
		return fmt.Errorf("dispatch send to %s: %w", *roleFlag, err)
	}
	msgID := parseSentMessageID(string(out))
	if taskID != "" {
		linked, lerr := dispatchLinkTask(projectDir, workstream, taskID, taskstore.Dispatch{
			Assignee:  member.Handle,
			Thread:    *threadFlag,
			Kind:      *kindFlag,
			Subject:   *subjectFlag,
			MessageID: msgID,
		}, taskNow())
		if lerr != nil {
			return fmt.Errorf("link native task %s to dispatch: %w", taskID, lerr)
		}
		taskID = linked.ID
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
			fmt.Printf("Dispatched %s to %s (handle %s) on session %s — msg %s%s (root %s)\n",
				*kindFlag, *roleFlag, member.Handle, workstream, msgID, taskText, ctx.Root)
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
		if *jsonOut {
			return printJSONEnvelope("dispatch", mutationResult{
				Command:   "dispatch",
				Status:    "queued",
				Project:   projectDir,
				Session:   workstream,
				Profile:   profile,
				ID:        taskID,
				TaskID:    taskID,
				Role:      member.Role,
				Assignee:  member.Handle,
				Handle:    member.Handle,
				MessageID: msgID,
				Root:      ctx.Root,
				Actions:   dispatchFollowUpActions(projectDir, profile, workstream, from, msgID),
			})
		}
		quietNotice("Skipped pane nudge (--no-wake); %s drains the task on its next turn.\n", *roleFlag)
		return nil
	}

	outcome, werr := dispatchWakePane(projectDir, profile, *sessionFlag, flagWasSet(fs, "session"), *roleFlag, *forceFlag)
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
				ID:        taskID,
				TaskID:    taskID,
				Role:      member.Role,
				Assignee:  member.Handle,
				Handle:    member.Handle,
				MessageID: msgID,
				Root:      ctx.Root,
				Actions:   dispatchFollowUpActions(projectDir, profile, workstream, from, msgID),
			})
		}
		return nil
	}
	if *jsonOut {
		status := "queued"
		if outcome.PaneID != "" {
			status = "queued_and_nudged"
		}
		return printJSONEnvelope("dispatch", mutationResult{
			Command:   "dispatch",
			Status:    status,
			Project:   projectDir,
			Session:   workstream,
			Profile:   profile,
			ID:        taskID,
			TaskID:    taskID,
			Role:      member.Role,
			Assignee:  member.Handle,
			Handle:    member.Handle,
			MessageID: msgID,
			Root:      ctx.Root,
			Actions:   dispatchFollowUpActions(projectDir, profile, workstream, from, msgID),
		})
	}
	if outcome.PaneID != "" {
		quietNotice("Nudged %s pane %s to drain.\n", *roleFlag, outcome.PaneID)
	} else {
		quietNotice("Task queued; pane not nudged: %s\n", outcome.Skipped)
	}
	return nil
}

func dispatchCollectCommand(projectDir, session, me string) string {
	return "amq-squad collect --project " + shellQuote(projectDir) +
		" --session " + shellQuote(session) +
		" --me " + shellQuote(me) +
		" --timeout 120s --include-body"
}

func dispatchFollowUpActions(projectDir, profile, session, from, msgID string) []mutationAction {
	actions := []mutationAction{
		followUp("collect", "collect child report", dispatchCollectCommand(projectDir, session, from)),
	}
	if strings.TrimSpace(msgID) != "" {
		actions = append(actions, followUp("receipts", "wait for drain receipt", "amq-squad amq receipts wait --project "+shellQuote(projectDir)+" --session "+shellQuote(session)+" --me "+shellQuote(from)+" --msg-id "+shellQuote(msgID)+" --stage drained"))
	}
	actions = append(actions, followUp("status", "show recipient status", "amq-squad status --project "+shellQuote(projectDir)+" --profile "+shellQuote(profile)+" --session "+shellQuote(session)+" --json"))
	return actions
}

// parseSentMessageID extracts the message id from `amq send`'s text output,
// whose confirmation line reads "Sent <id> to <handle> (...)". Returns "" when
// no such line is found, so the caller can fall back to echoing amq's raw output
// rather than hiding it.
func parseSentMessageID(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Sent "); ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

// dispatchSendArgs builds the `amq send` argv for a dispatch: a durable message
// to the resolved root from the lead handle to the child handle. The body is
// always passed (it is required and validated upstream). Pure + table-testable.
func dispatchSendArgs(root, from, to, thread, kind, subject, body, priority string) []string {
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
	return args
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

func defaultDispatchWakePane(projectDir, profile, session string, explicitSession bool, role string, force bool) (dispatchOutcome, error) {
	mr, workstream, err := resolveMemberRuntime(projectDir, profile, session, explicitSession, role)
	if err != nil {
		return dispatchOutcome{}, err
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
		return dispatchOutcome{PaneID: paneID}, nil
	}
	var unconfirmed *tmuxpane.SubmitUnconfirmedError
	if errors.As(sendErr, &unconfirmed) {
		if busy, berr := paneBusy(paneID); berr == nil && busy {
			return dispatchOutcome{PaneID: paneID}, nil
		}
		return dispatchOutcome{Skipped: fmt.Sprintf("pane %s nudged but submission unconfirmed; the durable task is queued and the worker drains it on its next turn", paneID)}, nil
	}
	return dispatchOutcome{}, sendErr
}
