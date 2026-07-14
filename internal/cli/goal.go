package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

var goalGhRun = func(args ...string) ([]byte, error) {
	cmd := exec.Command("gh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

type goalDraftData struct {
	Goal              string `json:"goal"`
	Repo              string `json:"repo,omitempty"`
	Milestone         string `json:"milestone,omitempty"`
	TargetContract    string `json:"target_contract,omitempty"`
	Session           string `json:"session"`
	Profile           string `json:"profile"`
	Lead              string `json:"lead"`
	LeadMode          string `json:"lead_mode"`
	Mode              string `json:"mode"`
	ControlRoot       string `json:"control_root,omitempty"`
	TargetProjectRoot string `json:"target_project_root,omitempty"`
	// TargetProjectRootSource (#290) classifies how target_project_root was
	// determined: provided | resolved_unconfirmed | unresolved | default.
	// resolved_unconfirmed is a proposal, not a confirmation: a global_orchestrator
	// run still needs an explicit/confirmed path before it edits files.
	TargetProjectRootSource     string                 `json:"target_project_root_source,omitempty"`
	TargetProjectRootCandidates []string               `json:"target_project_root_candidates,omitempty"`
	Namespace                   squadnamespace.Ref     `json:"namespace"`
	Execution                   executionModeData      `json:"execution"`
	GoalBinding                 goalBindingData        `json:"goal_binding"`
	Composition                 string                 `json:"composition"`
	Visibility                  string                 `json:"visibility"`
	AutonomousPolicy            *team.AutonomousPolicy `json:"autonomous_policy,omitempty"`
	PreviewOnly                 bool                   `json:"preview_only"`
	CodexOnly                   bool                   `json:"codex_only,omitempty"`
	IssueSources                []goalIssueSource      `json:"issue_sources,omitempty"`
	BriefSkeleton               string                 `json:"brief_skeleton"`
	Roster                      []goalRosterMember     `json:"roster"`
	Tasks                       []goalTaskPlan         `json:"tasks"`
	SpawnGates                  []goalCommandPlan      `json:"spawn_gates"`
	Dispatches                  []goalDispatchPlan     `json:"dispatches"`
	ApplyableMutations          []goalCommandPlan      `json:"applyable_mutations"`
	OrchestratorPrompt          string                 `json:"orchestrator_prompt"`
	SkillInvocation             string                 `json:"skill_invocation,omitempty"`
	// FieldSources (#291) labels each operator-facing Step 1 input as how it was
	// determined: "provided" (set by the operator) or "default" (auto). The
	// target_project_root entry keeps the richer #290 source vocabulary
	// (provided|resolved_unconfirmed|unresolved|default). Additive; clients that
	// ignore it are unaffected.
	FieldSources map[string]string `json:"field_sources,omitempty"`
	// Steps (#291) is the guided operator flow: each step states what just
	// happened, what is about to happen, what the operator approves, and the next
	// gate. Additive and rendered as the markdown Step 1/2/3 sections.
	Steps []goalDraftStep `json:"steps,omitempty"`
	Notes []string        `json:"notes"`
	// codexArgsProvided is true only when the operator explicitly supplied codex
	// args/effort (#291). When false, the seeded reasoning-effort default is a
	// recommendation comment, NOT a live --codex-args flag in any generated or
	// applyable launch command. Internal; not serialized.
	codexArgsProvided bool
}

type goalDraftStep struct {
	Number        int    `json:"number"`
	Title         string `json:"title"`
	JustHappened  string `json:"just_happened,omitempty"`
	AboutToHappen string `json:"about_to_happen,omitempty"`
	Approving     string `json:"approving,omitempty"`
	NextGate      string `json:"next_gate,omitempty"`
}

type goalIssueSource struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	URL    string `json:"url"`
	State  string `json:"state,omitempty"`
}

type goalBindingData struct {
	Mode         string `json:"mode"`
	NativeGoal   bool   `json:"native_goal"`
	Verified     bool   `json:"verified"`
	Source       string `json:"source"`
	Detail       string `json:"detail"`
	BriefPath    string `json:"brief_path,omitempty"`
	TasksPath    string `json:"tasks_path,omitempty"`
	NativeSource string `json:"native_source,omitempty"`
	Command      string `json:"command,omitempty"`
}

type goalRosterMember struct {
	Role      string   `json:"role"`
	Handle    string   `json:"handle"`
	Binary    string   `json:"binary"`
	Reason    string   `json:"reason"`
	CodexArgs []string `json:"codex_args,omitempty"`
}

type goalTaskPlan struct {
	ID        string   `json:"id"`
	Title     string   `json:"title"`
	Assignee  string   `json:"assignee"`
	DependsOn []string `json:"depends_on,omitempty"`
	SourceURL string   `json:"source_url,omitempty"`
}

type goalCommandPlan struct {
	Title   string `json:"title"`
	Command string `json:"command"`
	Reason  string `json:"reason,omitempty"`
}

type goalDispatchPlan struct {
	TaskID  string `json:"task_id"`
	Role    string `json:"role"`
	Thread  string `json:"thread"`
	Subject string `json:"subject"`
	Body    string `json:"body"`
	Command string `json:"command"`
}

type goalStartData struct {
	Command         string               `json:"command"`
	Status          string               `json:"status"`
	DryRun          bool                 `json:"dry_run"`
	Project         string               `json:"project"`
	Profile         string               `json:"profile"`
	Session         string               `json:"session"`
	Mode            string               `json:"mode"`
	Role            string               `json:"role"`
	Handle          string               `json:"handle"`
	Goal            string               `json:"goal"`
	Namespace       squadnamespace.Ref   `json:"namespace"`
	Actions         []mutationAction     `json:"actions,omitempty"`
	DeliverCmd      string               `json:"deliver_command,omitempty"`
	DeliveryReceipt *deliveryReceiptData `json:"delivery_receipt,omitempty"`
}

type goalApplyData struct {
	Command          string                `json:"command"`
	Status           string                `json:"status"`
	Project          string                `json:"project"`
	Profile          string                `json:"profile"`
	Session          string                `json:"session"`
	Mode             string                `json:"mode"`
	Role             string                `json:"role"`
	Handle           string                `json:"handle"`
	GoalID           string                `json:"goal_id,omitempty"`
	Gate             string                `json:"gate"`
	Goal             string                `json:"goal"`
	Namespace        squadnamespace.Ref    `json:"namespace"`
	ApprovalEvidence *goalApprovalEvidence `json:"approval_evidence,omitempty"`
	DeliveryReceipt  *deliveryReceiptData  `json:"delivery_receipt,omitempty"`
}

type goalApprovalEvidence struct {
	MessageID string    `json:"message_id"`
	From      string    `json:"from"`
	To        []string  `json:"to"`
	Thread    string    `json:"thread"`
	Subject   string    `json:"subject"`
	Created   time.Time `json:"created"`
}

type goalDeliveryOptions struct {
	Project   string
	Profile   string
	Session   string
	Role      string
	Goal      string
	AttemptID string
	Team      team.Team
	Member    team.Member
	Namespace squadnamespace.Ref
	Mode      string
	// ResumeTransitionID is an internal, durable compare-and-swap token. It is
	// accepted only from resume after the fresh lead launch has been verified.
	ResumeTransitionID string
}

type goalDeliveryAttemptError struct {
	AttemptID   string
	AttemptPath string
	Sent        bool
	State       string
	Cause       error
}

func (e *goalDeliveryAttemptError) Error() string {
	state := "not sent"
	if e.Sent {
		state = "delivery may have started"
	}
	if e.State != "" {
		state = e.State
	}
	return fmt.Sprintf("goal attempt %s (%s) failed; state=%s: %v", e.AttemptID, e.AttemptPath, state, e.Cause)
}
func (e *goalDeliveryAttemptError) Unwrap() error   { return e.Cause }
func (e *goalDeliveryAttemptError) RetrySafe() bool { return false }

type goalFallbackDelivery struct {
	MessageID string
	Root      string
	Thread    string
}

// goalFallbackDurabilityError preserves the original typed pane-delivery
// outcome when the AMQ fallback itself fails. errors.As must still reach the
// QueuedInputError/SubmitUnconfirmedError so run-start treats this as a soft
// launch-finalization outcome while reporting that durable recovery failed.
type goalFallbackDurabilityError struct {
	DeliveryErr error
	FallbackErr error
}

func (e *goalFallbackDurabilityError) Error() string {
	return fmt.Sprintf("%v; durable goal fallback failed: %v", e.DeliveryErr, e.FallbackErr)
}

func (e *goalFallbackDurabilityError) Unwrap() []error {
	return []error{e.DeliveryErr, e.FallbackErr}
}

// goalFallbackSentReceiptError means AMQ accepted the actionable fallback but
// local receipt persistence failed. Retrying blindly can enqueue the same goal
// again, so callers receive the original delivery error plus the exact durable
// message coordinates and an explicit non-retryable signal.
type goalFallbackSentReceiptError struct {
	MessageID   string
	Root        string
	Thread      string
	DeliveryErr error
	ReceiptErr  error
}

func (e *goalFallbackSentReceiptError) Error() string {
	return fmt.Sprintf("durable goal fallback %s was sent to %s on %s, but its delivery receipt failed: %v; unsafe to blindly retry", strings.TrimSpace(e.MessageID), e.Thread, e.Root, e.ReceiptErr)
}

func (e *goalFallbackSentReceiptError) Unwrap() []error {
	return []error{e.DeliveryErr, e.ReceiptErr}
}

func (e *goalFallbackSentReceiptError) RetrySafe() bool { return false }

type goalPostDeliveryBindingError struct {
	AttemptID string
	Recovery  string
	Cause     error
}

func (e *goalPostDeliveryBindingError) Error() string {
	return fmt.Sprintf("goal attempt %s was delivered but its launch binding could not be marked delivered: %v; do not create a new attempt; inspect the typed attempt and current binding before retrying", e.AttemptID, e.Cause)
}

func (e *goalPostDeliveryBindingError) Unwrap() error   { return e.Cause }
func (e *goalPostDeliveryBindingError) RetrySafe() bool { return false }

// goalFallbackAMQSend is the durable half of ambiguous native goal delivery.
// Tests replace it without needing a real AMQ binary or mailbox tree.
var goalFallbackAMQSend = sendDurableGoalFallback
var goalDeliveryReceiptWrite = writeDeliveryReceipt
var goalLaunchWriteUnderRecordLock = launch.WriteUnderRecordLock
var goalBeforeOrdinaryBindingCAS = func() {}
var goalBeforePostDeliveryBindingCAS = func() {}
var goalBeforeTransitionBindingCAS = func() {}
var goalBeforeTransitionSendCAS = func() {}

const (
	goalDeliveryStateNotSent           = "not_sent"
	goalDeliveryStateNativeQueued      = "native_queued"
	goalDeliveryStateNativeUnconfirmed = "native_unconfirmed"
	goalDeliveryStateFallbackSent      = "fallback_sent"
	goalDeliveryStatePaneFailed        = "pane_failed"
	goalDeliveryStatePaneDelivered     = "pane_delivered"
)

const (
	goalOrchestratorRole          = "orchestrator"
	defaultGoalOrchestratorHandle = "orchestrator"
)

func runGoal(args []string) error {
	return runGoalWithVersion(args, "dev")
}

func runGoalWithVersion(args []string, version string) error {
	if len(args) == 0 {
		printGoalUsage()
		return nil
	}
	switch args[0] {
	case "-h", "--help":
		printGoalUsage()
		return nil
	case "draft":
		return runGoalDraftWithVersion(args[1:], version)
	case "deliver":
		return runGoalDeliver(args[1:])
	case "claim":
		return runGoalClaim(args[1:])
	case "retry-attempt":
		return runGoalRetryAttempt(args[1:])
	case "start":
		return runGoalStart(args[1:])
	case "apply":
		return runGoalApply(args[1:])
	default:
		return usageErrorf("unknown 'goal' subcommand %q. Run 'amq-squad goal --help' for available subcommands.", args[0])
	}
}

func normalizeOptionalStringFlag(args []string, name, defaultValue string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == name {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				out = append(out, name+"="+args[i+1])
				i++
			} else {
				out = append(out, name+"="+defaultValue)
			}
			continue
		}
		out = append(out, arg)
	}
	return out
}

func printGoalUsage() {
	fmt.Fprint(os.Stderr, `amq-squad goal - manage preview-first goal setup plans

Usage:
  amq-squad goal <subcommand> [options]

Subcommands:
  apply     apply an operator-approved visible lead goal
  claim     atomically claim one native/AMQ goal delivery attempt
  deliver   deliver a native /goal to the resolved visible lead
  draft     produce a preview-only goal setup plan from a goal description
  retry-attempt  recover delivery of one already-recorded unclaimed attempt
  start     preview or deliver a goal to the current visible lead

Run 'amq-squad goal <subcommand> --help' for subcommand options and flags.

Examples:
  amq-squad goal draft --goal "fix issue #96" --session issue-96
  amq-squad goal draft --goal "deliver milestone v2.7.0" --repo omriariav/amq-squad --milestone v2.7.0 --session v2-7-0
  amq-squad goal apply --session issue-96 --gate release --yes --json
  amq-squad goal deliver --session issue-96 --goal "fix issue #96" --json
  amq-squad goal start --session issue-96 --goal "fix issue #96" --dry-run --json
`)
}

func runGoalApply(args []string) error {
	fs := flag.NewFlagSet("goal apply", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "workstream session of the visible lead")
	roleFlag := fs.String("role", "", "role to apply through (default: configured lead)")
	goalIDFlag := fs.String("goal-id", "", "approved goal identifier (recorded in JSON output)")
	gateFlag := fs.String("gate", "", "gate topic carrying the operator APPROVED answer")
	yes := fs.Bool("yes", false, "confirm apply without an interactive prompt")
	overrideNamespaceConflict := fs.Bool("override-namespace-conflict", false, "acknowledge a collided namespace and continue, writing an audit record")
	overrideNamespaceReason := fs.String("reason", "", "required reason when --override-namespace-conflict is set")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned goal_apply envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad goal apply - apply an operator-approved visible lead goal

Usage:
  amq-squad goal apply [--project DIR] [--profile NAME] [--session S] [--role ROLE] [--goal-id ID] --gate TOPIC --yes [--override-namespace-conflict --reason WHY] [--json]

Verifies that gate/<topic> contains a real operator APPROVED answer to the
resolved visible lead, reads the native goal already recorded on that lead's
launch record, and delivers it through the native /goal control path. This
command is confirm-gated; pass --yes after reviewing the gate and lead state.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("goal apply takes no positional arguments")
	}
	gate := normalizeGateTopic(*gateFlag)
	if gate == "" {
		return usageErrorf("goal apply requires --gate <topic>")
	}
	target, err := resolveGoalTargetOptions(*projectFlag, *profileFlag, *sessionFlag, *roleFlag, flagWasSet(fs, "project"), flagWasSet(fs, "profile"), flagWasSet(fs, "session"), "goal apply", namespaceConflictOverrideOptions{
		Allowed: *overrideNamespaceConflict,
		Reason:  *overrideNamespaceReason,
	})
	if err != nil {
		return err
	}
	goal, err := approvedGoalFromLeadBinding(target)
	if err != nil {
		return err
	}
	evidence, err := verifyGoalApplyApproval(target, gate)
	if err != nil {
		return err
	}
	if !*yes {
		return usageErrorf("goal apply requires --yes after verifying approved gate %s", gate)
	}
	target.Goal = goal
	result, err := executeGoalDelivery(target)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("goal_apply", goalApplyData{
			Command:          "goal apply",
			Status:           result.Status,
			Project:          result.Project,
			Profile:          result.Profile,
			Session:          result.Session,
			Mode:             target.Mode,
			Role:             result.Role,
			Handle:           result.Handle,
			GoalID:           strings.TrimSpace(*goalIDFlag),
			Gate:             gate,
			Goal:             goal,
			Namespace:        result.Namespace,
			ApprovalEvidence: &evidence,
			DeliveryReceipt:  result.DeliveryReceipt,
		})
	}
	fmt.Printf("Applied approved goal on %s for session %s.\n", result.Role, result.Session)
	return nil
}

func runGoalStart(args []string) error {
	args = normalizeOptionalStringFlag(args, "--register-orchestrator", defaultGoalOrchestratorHandle)
	fs := flag.NewFlagSet("goal start", flag.ContinueOnError)
	goalFlag := fs.String("goal", "", "goal text to deliver as a native /goal control command")
	sessionFlag := fs.String("session", "", "workstream session of the visible lead")
	roleFlag := fs.String("role", "", "role to receive the native /goal command (default: configured lead)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	registerOrchestrator := fs.String("register-orchestrator", "", "before delivery, register the current pane as external orchestrator handle (default: orchestrator)")
	wakeInjectMode := fs.String("wake-inject-mode", "", "wake injection mode for --register-orchestrator: auto, raw, paste, or none")
	dryRun := fs.Bool("dry-run", false, "preview the inferred start plan without delivering")
	yes := fs.Bool("yes", false, "confirm delivery without an interactive prompt")
	overrideNamespaceConflict := fs.Bool("override-namespace-conflict", false, "acknowledge a collided namespace and continue, writing an audit record")
	overrideNamespaceReason := fs.String("reason", "", "required reason when --override-namespace-conflict is set")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned goal_start envelope")
	resumeTransition := fs.String("resume-transition", "", "internal durable resume-goal transition token")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad goal start - preview or deliver a goal to the visible lead

Usage:
  amq-squad goal start [--project DIR] [--profile NAME] --session S [--role ROLE] --goal TEXT [--register-orchestrator[=HANDLE] [--wake-inject-mode auto|raw|paste|none]] [--dry-run] [--yes] [--override-namespace-conflict --reason WHY] [--json]

Infers the current team profile, session, execution mode, and visible lead target
from the project. Use --dry-run to inspect the plan. Non-dry-run delivery is
confirm-gated and requires --yes in this first implementation slice.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("goal start takes no positional arguments; use --goal TEXT")
	}
	goal := strings.TrimSpace(*goalFlag)
	if goal == "" {
		return usageErrorf("goal start requires --goal TEXT")
	}
	wakeInjectModeValue, err := normalizeGoalOrchestratorWakeInjectMode(fs, *wakeInjectMode)
	if err != nil {
		return err
	}
	// Dry-run previews the plan without any mutation. --register-orchestrator is
	// intentionally not applied here; the preview resolves against the configured
	// lead, matching delivery without registration.
	if *dryRun {
		opts, err := resolveGoalDeliveryOptions(*projectFlag, *profileFlag, *sessionFlag, *roleFlag, goal, flagWasSet(fs, "project"), flagWasSet(fs, "profile"), flagWasSet(fs, "session"), "goal start", namespaceConflictOverrideOptions{})
		if err != nil {
			return err
		}
		plan := goalStartPlan(opts)
		if *jsonOut {
			return printJSONEnvelope("goal_start", plan)
		}
		writeGoalStartPlan(os.Stdout, plan)
		return nil
	}
	// Non-dry-run delivery is confirm-gated. Defer every mutation (including the
	// --register-orchestrator roster write) until after --yes is confirmed.
	if !*yes {
		return usageErrorf("goal start delivery requires --yes (or run --dry-run to preview first)")
	}
	override := namespaceConflictOverrideOptions{Allowed: *overrideNamespaceConflict, Reason: *overrideNamespaceReason}
	opts, err := resolveGoalDeliveryOptions(*projectFlag, *profileFlag, *sessionFlag, *roleFlag, goal, flagWasSet(fs, "project"), flagWasSet(fs, "profile"), flagWasSet(fs, "session"), "goal start", override)
	if err != nil {
		return err
	}
	opts.ResumeTransitionID = strings.TrimSpace(*resumeTransition)
	if flagWasSet(fs, "register-orchestrator") {
		if err := registerGoalOrchestrator(opts, *registerOrchestrator, wakeInjectModeValue, flagWasSet(fs, "wake-inject-mode")); err != nil {
			return err
		}
	}
	result, err := executeGoalDelivery(opts)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("goal_start", goalStartData{
			Command:         "goal start",
			Status:          result.Status,
			DryRun:          false,
			Project:         result.Project,
			Profile:         result.Profile,
			Session:         result.Session,
			Mode:            effectiveTeamExecutionMode(opts.Team),
			Role:            result.Role,
			Handle:          result.Handle,
			Goal:            opts.Goal,
			Namespace:       result.Namespace,
			DeliveryReceipt: result.DeliveryReceipt,
		})
	}
	if result.Status == "durable_goal_fallback" {
		fmt.Printf("Queued durable goal fallback for %s on session %s.\n", result.Role, result.Session)
	} else if result.Status == "native_goal_queued" {
		fmt.Printf("Queued native goal attempt %s for %s on session %s; no actionable AMQ duplicate was sent.\n", result.DeliveryReceipt.AttemptID, result.Role, result.Session)
	} else {
		fmt.Printf("Started goal on %s for session %s.\n", result.Role, result.Session)
	}
	return nil
}

func runGoalDeliver(args []string) error {
	args = normalizeOptionalStringFlag(args, "--register-orchestrator", defaultGoalOrchestratorHandle)
	fs := flag.NewFlagSet("goal deliver", flag.ContinueOnError)
	goalFlag := fs.String("goal", "", "goal text to deliver as a native /goal control command")
	sessionFlag := fs.String("session", "", "workstream session of the visible lead")
	roleFlag := fs.String("role", "", "role to receive the native /goal command (default: configured lead)")
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	registerOrchestrator := fs.String("register-orchestrator", "", "before delivery, register the current pane as external orchestrator handle (default: orchestrator)")
	wakeInjectMode := fs.String("wake-inject-mode", "", "wake injection mode for --register-orchestrator: auto, raw, paste, or none")
	overrideNamespaceConflict := fs.Bool("override-namespace-conflict", false, "acknowledge a collided namespace and continue, writing an audit record")
	overrideNamespaceReason := fs.String("reason", "", "required reason when --override-namespace-conflict is set")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad goal deliver - deliver native /goal as a control action

Usage:
  amq-squad goal deliver [--project DIR] [--profile NAME] --session S [--role ROLE] --goal TEXT [--register-orchestrator[=HANDLE] [--wake-inject-mode auto|raw|paste|none]] [--override-namespace-conflict --reason WHY] [--json]

Delivers a native Codex /goal command to the visible lead as a first-class
control action. This is not an ordinary prompt send: it preserves the busy guard
for amq-squad send, but /goal delivery may target a busy Codex lead because the
runtime accepts goal control messages safely.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	goal := strings.TrimSpace(*goalFlag)
	if goal == "" {
		return usageErrorf("goal deliver requires --goal TEXT")
	}
	wakeInjectModeValue, err := normalizeGoalOrchestratorWakeInjectMode(fs, *wakeInjectMode)
	if err != nil {
		return err
	}
	override := namespaceConflictOverrideOptions{Allowed: *overrideNamespaceConflict, Reason: *overrideNamespaceReason}
	opts, err := resolveGoalDeliveryOptions(*projectFlag, *profileFlag, *sessionFlag, *roleFlag, goal, flagWasSet(fs, "project"), flagWasSet(fs, "profile"), flagWasSet(fs, "session"), "goal deliver", override)
	if err != nil {
		return err
	}
	if flagWasSet(fs, "register-orchestrator") {
		if err := registerGoalOrchestrator(opts, *registerOrchestrator, wakeInjectModeValue, flagWasSet(fs, "wake-inject-mode")); err != nil {
			return err
		}
	}
	result, err := executeGoalDelivery(opts)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("goal_deliver", result)
	}
	if result.Status == "durable_goal_fallback" {
		fmt.Printf("Queued durable goal fallback for %s (message %s).\n", result.Role, result.MessageID)
	} else if result.Status == "native_goal_queued" {
		fmt.Printf("Queued native /goal attempt %s for %s; no actionable AMQ duplicate was sent.\n", result.DeliveryReceipt.AttemptID, result.Role)
	} else {
		fmt.Printf("Delivered native /goal to %s pane %s (attempt %s).\n", result.Role, result.DeliveryReceipt.PaneID, result.DeliveryReceipt.AttemptID)
	}
	return nil
}

func resolveGoalDeliveryOptions(projectFlag, profileFlag, sessionFlag, roleFlag, goal string, projectSet, profileSet, sessionSet bool, command string, override namespaceConflictOverrideOptions) (goalDeliveryOptions, error) {
	opts, err := resolveGoalTargetOptions(projectFlag, profileFlag, sessionFlag, roleFlag, projectSet, profileSet, sessionSet, command, override)
	if err != nil {
		return goalDeliveryOptions{}, err
	}
	opts.Goal = goal
	return opts, nil
}

func resolveGoalTargetOptions(projectFlag, profileFlag, sessionFlag, roleFlag string, projectSet, profileSet, sessionSet bool, command string, override namespaceConflictOverrideOptions) (goalDeliveryOptions, error) {
	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: projectFlag, ProfileFlag: profileFlag, SessionFlag: sessionFlag,
		ProjectExplicit: projectSet, ProfileExplicit: profileSet, SessionExplicit: sessionSet,
	})
	if err != nil {
		return goalDeliveryOptions{}, err
	}
	emitContextDiagnostics(ctx)
	projectDir, profile := ctx.ProjectDir, ctx.Profile
	if !team.ExistsProfile(projectDir, profile) {
		return goalDeliveryOptions{}, fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return goalDeliveryOptions{}, fmt.Errorf("read team: %w", err)
	}
	workstream, err := resolveTeamWorkstreamName(t, ctx.Session, sessionSet)
	if err != nil {
		return goalDeliveryOptions{}, err
	}
	if err := ensureNoNamespaceConflictWithOverride(command, projectDir, profile, workstream, profileSet, override); err != nil {
		return goalDeliveryOptions{}, err
	}
	role := strings.TrimSpace(roleFlag)
	if role == "" {
		role = strings.TrimSpace(t.Lead)
	}
	if role == "" {
		return goalDeliveryOptions{}, usageErrorf("%s requires --role when the team has no configured lead", command)
	}
	if err := ensureTargetIsNotOperator(t, command, role); err != nil {
		return goalDeliveryOptions{}, err
	}
	member, ok := teamMemberByRole(t, role)
	if !ok {
		return goalDeliveryOptions{}, fmt.Errorf("no team member with role %q in this team", role)
	}
	return goalDeliveryOptions{
		Project:   projectDir,
		Profile:   profile,
		Session:   workstream,
		Role:      role,
		Team:      t,
		Member:    member,
		Namespace: squadnamespace.Resolve(projectDir, profile, workstream),
		Mode:      effectiveTeamExecutionMode(t),
	}, nil
}

func approvedGoalFromLeadBinding(opts goalDeliveryOptions) (string, error) {
	mr, _, err := resolveMemberRuntime(opts.Project, opts.Profile, opts.Session, true, opts.Role)
	if err != nil {
		return "", err
	}
	if mr.ProfileMismatch {
		return "", fmt.Errorf("goal apply refused: launch record for role %q belongs to a different profile", opts.Role)
	}
	if !mr.HasRecord {
		return "", fmt.Errorf("goal apply requires a launch record for visible lead role %q", opts.Role)
	}
	if mr.Record.GoalBinding == nil || !mr.Record.GoalBinding.NativeGoal {
		return "", fmt.Errorf("goal apply requires role %q to have a native goal binding", opts.Role)
	}
	goal, ok := extractNativeGoalText(mr.Record.GoalBinding.Command)
	if !ok || strings.TrimSpace(goal) == "" {
		return "", fmt.Errorf("goal apply could not read native goal text from role %q launch record", opts.Role)
	}
	return strings.TrimSpace(goal), nil
}

func extractNativeGoalText(command string) (string, bool) {
	command = strings.TrimSpace(command)
	const flagText = "--goal"
	idx := strings.Index(command, flagText)
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimSpace(command[idx+len(flagText):])
	if rest == "" {
		return "", false
	}
	if rest[0] == '"' {
		for end := 1; end < len(rest); end++ {
			if rest[end] != '"' || rest[end-1] == '\\' {
				continue
			}
			candidate := rest[:end+1]
			if parsed, err := strconv.Unquote(candidate); err == nil {
				return parsed, true
			}
		}
		return "", false
	}
	if end := strings.IndexAny(rest, " \t\n"); end >= 0 {
		return rest[:end], true
	}
	return rest, true
}

func verifyGoalApplyApproval(opts goalDeliveryOptions, gate string) (goalApprovalEvidence, error) {
	operator := statusOperatorForTeam(opts.Team, opts.Namespace)
	if !operator.Enabled || strings.TrimSpace(operator.Handle) == "" {
		return goalApprovalEvidence{}, usageErrorf("goal apply requires an enabled operator handle")
	}
	ctx, err := resolveAMQContextForNamespace(opts.Project, opts.Profile, opts.Session, operator.Handle)
	if err != nil {
		return goalApprovalEvidence{}, fmt.Errorf("resolve approval gate state: %w", err)
	}
	msgs, warnings := state.ScanSessionMessages(ctx.Root, time.Now)
	if len(warnings) > 0 {
		return goalApprovalEvidence{}, fmt.Errorf("approval gate state has unreadable messages; inspect %s before applying", gate)
	}
	var latest *state.Message
	for i := range msgs {
		msg := msgs[i]
		if msg.Thread != gate || msg.Kind != state.KindAnswer || msg.From != operator.Handle {
			continue
		}
		if !messageToContains(msg, opts.Member.Handle) {
			continue
		}
		if latest == nil || latest.Created.Before(msg.Created) || (latest.Created.Equal(msg.Created) && latest.ID < msg.ID) {
			latest = &msgs[i]
		}
	}
	if latest == nil {
		return goalApprovalEvidence{}, fmt.Errorf("goal apply requires an operator APPROVED answer on %s to %s", gate, opts.Member.Handle)
	}
	if !strings.HasPrefix(strings.ToUpper(strings.TrimSpace(latest.Subject)), "APPROVED:") {
		return goalApprovalEvidence{}, fmt.Errorf("latest operator answer on %s to %s is not APPROVED: %s", gate, opts.Member.Handle, latest.Subject)
	}
	return goalApprovalEvidence{
		MessageID: latest.ID,
		From:      latest.From,
		To:        append([]string(nil), latest.To...),
		Thread:    latest.Thread,
		Subject:   latest.Subject,
		Created:   latest.Created,
	}, nil
}

func messageToContains(msg state.Message, handle string) bool {
	for _, to := range msg.To {
		if to == handle {
			return true
		}
	}
	return false
}

func goalStartPlan(opts goalDeliveryOptions) goalStartData {
	cmd := goalDeliverCommand(opts, true)
	return goalStartData{
		Command:    "goal start",
		Status:     "planned",
		DryRun:     true,
		Project:    opts.Project,
		Profile:    opts.Profile,
		Session:    opts.Session,
		Mode:       opts.Mode,
		Role:       opts.Role,
		Handle:     opts.Member.Handle,
		Goal:       opts.Goal,
		Namespace:  opts.Namespace,
		DeliverCmd: cmd,
		Actions: []mutationAction{
			followUp("goal_deliver", "deliver goal to visible lead", cmd),
		},
	}
}

func goalDeliverCommand(opts goalDeliveryOptions, jsonOut bool) string {
	parts := []string{
		"amq-squad", "goal", "deliver",
		"--project", shellQuote(opts.Project),
		"--profile", shellQuote(opts.Profile),
		"--session", shellQuote(opts.Session),
		"--role", shellQuote(opts.Role),
		"--goal", shellQuote(opts.Goal),
	}
	if jsonOut {
		parts = append(parts, "--json")
	}
	return strings.Join(parts, " ")
}

func normalizeGoalOrchestratorWakeInjectMode(fs *flag.FlagSet, raw string) (string, error) {
	mode, err := normalizeWakeInjectMode(raw)
	if err != nil {
		return "", err
	}
	if flagWasSet(fs, "wake-inject-mode") && !flagWasSet(fs, "register-orchestrator") {
		return "", usageErrorf("--wake-inject-mode requires --register-orchestrator")
	}
	if err := validateWakeInjectConfig(mode, "", nil, ""); err != nil {
		return "", err
	}
	return mode, nil
}

func registerGoalOrchestrator(opts goalDeliveryOptions, handle, wakeInjectMode string, wakeInjectModeExplicit bool) error {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		handle = defaultGoalOrchestratorHandle
	}
	id, err := currentPaneIdentity()
	if err != nil {
		return err
	}
	if id == nil {
		return fmt.Errorf("goal delivery --register-orchestrator requires a current tmux pane (TMUX/TMUX_PANE unset)")
	}
	lifecycle, err := beginExternalOrchestratorLifecycle(opts, handle, id.PaneID, id.Session, id.WindowID, id.WindowName, currentLaunchTTY(), time.Now().UTC())
	if err != nil {
		return err
	}
	lifecycle, err = ensureExternalOrchestratorMailbox(opts, lifecycle)
	if err != nil {
		return err
	}
	if lifecycle.Registration.State == externalOrchestratorStateRegistered {
		if err := verifyExternalOrchestratorLaunch(lifecycle); err != nil {
			return fmt.Errorf("registered external orchestrator runtime is incomplete: %w", err)
		}
		return nil
	}
	if lifecycle.Registration.State == externalOrchestratorStateRuntimeVerified {
		if err := verifyExternalOrchestratorLaunch(lifecycle); err != nil {
			return fmt.Errorf("runtime-verified external orchestrator is incomplete: %w", err)
		}
		if _, _, err := transitionExternalOrchestratorRegistration(lifecycle.Registration.Identity.Scope, lifecycle.Registration.Generation, externalOrchestratorStateRegistered, externalOrchestratorTransitionEvidence{LaunchPath: filepath.Join(lifecycle.AgentDir, launch.FileName), Outcome: "registered"}, time.Now().UTC()); err != nil {
			return err
		}
		return nil
	}
	handle = lifecycle.Registration.Identity.Scope.Handle
	cwd := opts.Member.EffectiveCWD(opts.Team.Project)
	env, err := resolveAMQEnvForTeamProfile(cwd, opts.Profile, opts.Session, handle)
	if err != nil {
		return fmt.Errorf("resolve orchestrator amq env: %w", err)
	}
	if env.Me != "" {
		handle = env.Me
	}
	root := lifecycle.Root
	agentDir := lifecycle.AgentDir
	existingRec, existingRecErr := launch.Read(agentDir)
	wakeConfig, err := resolveExternalWakeInjectConfig(wakeInjectConfig{Mode: wakeInjectMode}, wakeInjectModeExplicit, false, false, existingRec, existingRecErr, opts.Member.Binary, goalOrchestratorRole, handle, opts.Profile, env.SessionName, root, id.PaneID)
	if err != nil {
		return err
	}
	wakeInjectMode = wakeConfig.Mode
	if wakeInjectMode != "" && !amqSupportsWakeInjectMode(env.AMQVersion) {
		return fmt.Errorf("--wake-inject-mode requires amq %s or newer (found %s)", minWakeInjectModeAMQVersion, versionOrUnknown(env.AMQVersion))
	}
	wakeInjectCmdValue := wakeDrainInject()
	if wakeInjectMode == "none" {
		wakeInjectCmdValue = ""
	}
	wakeResult, err := leadWakeStarter(leadWakeOptions{
		ProjectDir:     cwd,
		Profile:        opts.Profile,
		Session:        env.SessionName,
		Root:           root,
		Handle:         handle,
		Require:        true,
		WakeInjectVia:  wakeConfig.Via,
		WakeInjectArgs: wakeConfig.Args,
		WakeInjectMode: wakeInjectMode,
		WakeInjectCmd:  wakeInjectCmdValue,
	})
	if err != nil {
		return fmt.Errorf("start external orchestrator wake: %w", err)
	}
	wakePID := wakeResult.PID
	if lock, lockErr := readWakeLock(agentDir); lockErr == nil && lock.PID > 0 {
		wakePID = lock.PID
	}
	rec := launch.Record{
		CWD:              cwd,
		Binary:           opts.Member.Binary,
		Session:          env.SessionName,
		SharedWorkstream: true,
		Handle:           handle,
		Role:             goalOrchestratorRole,
		Root:             root,
		BaseRoot:         absoluteAMQRoot(cwd, env.BaseRoot),
		RootSource:       env.RootSource,
		AMQVersion:       env.AMQVersion,
		Model:            strings.TrimSpace(opts.Member.Model),
		Trust:            strings.TrimSpace(opts.Team.Trust),
		External:         true,
		WakeInjectVia:    wakeConfig.Via,
		WakeInjectArgs:   wakeConfig.Args,
		WakeInjectMode:   wakeInjectMode,
		WakeInjectCmd:    wakeInjectCmdValue,
		WakePID:          wakePID,
		AgentTTY:         currentLaunchTTY(),
		StartedAt:        time.Now().UTC(),
		TeamProfile:      opts.Profile,
		Tmux: &launch.TmuxInfo{
			Session:    id.Session,
			WindowID:   id.WindowID,
			WindowName: id.WindowName,
			PaneID:     id.PaneID,
			Target:     "external",
		},
	}
	rec.Terminal = launch.TerminalInfoFromTmux(rec.Tmux)
	if err := launch.Write(agentDir, rec); err != nil {
		return fmt.Errorf("write external orchestrator launch record: %w", err)
	}
	lifecycle.Registration, _, err = transitionExternalOrchestratorRegistration(lifecycle.Registration.Identity.Scope, lifecycle.Registration.Generation, externalOrchestratorStateRuntimeVerified, externalOrchestratorTransitionEvidence{WakePID: wakePID, LaunchPath: filepath.Join(agentDir, launch.FileName), Outcome: "verified"}, time.Now().UTC())
	if err != nil {
		return err
	}
	if _, _, err := transitionExternalOrchestratorRegistration(lifecycle.Registration.Identity.Scope, lifecycle.Registration.Generation, externalOrchestratorStateRegistered, externalOrchestratorTransitionEvidence{WakePID: wakePID, LaunchPath: filepath.Join(agentDir, launch.FileName), Outcome: "registered"}, time.Now().UTC()); err != nil {
		return err
	}
	return nil
}

func writeGoalStartPlan(out *os.File, data goalStartData) {
	fmt.Fprintln(out, "# amq-squad goal start")
	fmt.Fprintln(out, "# dry_run: true")
	fmt.Fprintf(out, "# project: %s\n", data.Project)
	fmt.Fprintf(out, "# profile: %s\n", data.Profile)
	fmt.Fprintf(out, "# session: %s\n", data.Session)
	fmt.Fprintf(out, "# mode: %s\n", data.Mode)
	fmt.Fprintf(out, "# role: %s\n", data.Role)
	fmt.Fprintf(out, "# handle: %s\n", data.Handle)
	fmt.Fprintf(out, "# namespace: %s\n\n", data.Namespace.ID)
	fmt.Fprintf(out, "Goal: %s\n\n", data.Goal)
	fmt.Fprintf(out, "Run: %s\n", data.DeliverCmd)
}

func sendDurableGoalFallback(opts goalDeliveryOptions) (goalFallbackDelivery, error) {
	target := memberHandle(opts.Member)
	if target == "" {
		return goalFallbackDelivery{}, fmt.Errorf("goal fallback target role %q has no handle", opts.Role)
	}
	sender, err := resolveDispatchSender(opts.Team, "")
	if err != nil {
		// A flat/non-orchestrated team can still use goal deliver with an explicit
		// role. A self-addressed durable message is preferable to losing the goal
		// when no separate dispatcher identity exists.
		sender = target
	}
	cwd := opts.Member.EffectiveCWD(opts.Project)
	ctx, err := resolveAMQContextForNamespace(cwd, opts.Profile, opts.Session, sender)
	if err != nil {
		return goalFallbackDelivery{}, fmt.Errorf("resolve amq root for goal fallback: %w", err)
	}
	ctx.Me = sender
	attemptID := strings.TrimSpace(opts.AttemptID)
	if attemptID == "" {
		return goalFallbackDelivery{}, fmt.Errorf("goal fallback requires a shared attempt id")
	}
	thread := "goal/" + opts.Session
	subject := "Claim-once launch goal: " + opts.Session + " (" + attemptID + ")"
	claimCommand := "amq-squad goal claim --project " + shellQuote(opts.Project) +
		" --profile " + shellQuote(opts.Profile) +
		" --session " + shellQuote(opts.Session) +
		" --attempt-id " + shellQuote(attemptID) +
		" --route amq --json"
	body := "Launch goal for session " + opts.Session + ":\n\n" + opts.Goal +
		"\n\nGoal attempt ID: " + attemptID +
		"\n\nThis is the single actionable AMQ path for an unconfirmed native submission. " +
		"Before activating the goal, run:\n\n" + claimCommand +
		"\n\nProceed only when status is claimed. If status is already_claimed, the native path won and this message is a no-op. " +
		"Never reset or retry this attempt to activate it twice."
	args := dispatchSendArgs(ctx.Root, sender, target, thread, "todo", subject, body, "", "", 0)
	out, receipt, err := runOwnedDurableSend(durableSendOptions{ProjectDir: opts.Project, Profile: opts.Profile, Session: opts.Session, Role: opts.Role, Kind: "goal_fallback"}, amqCommandRequest{Dir: cwd, Env: amqCommandEnv(ctx), Arg: args})
	if err != nil {
		return goalFallbackDelivery{}, fmt.Errorf("send durable goal fallback to %s: %w", target, err)
	}
	_ = out
	return goalFallbackDelivery{
		MessageID: receipt.MessageID,
		Root:      ctx.Root,
		Thread:    thread,
	}, nil
}

func goalDeliveryLockPath(opts goalDeliveryOptions) string {
	return filepath.Join(goalAttemptDir(opts.Project, opts.Profile, opts.Session), "."+sanitizeWorkstreamName(opts.Role)+".delivery.lock")
}

// withGoalIdentityWriterLocks is entered only while the per-lead goal
// delivery lock is already held. Keep this order stable across transition and
// retry paths: goal delivery -> team profile -> launch record.
func withGoalIdentityWriterLocks(opts goalDeliveryOptions, agentDir string, fn func() error) error {
	return team.WithProfileLock(opts.Project, opts.Profile, func() error {
		return launch.WithRecordLock(agentDir, fn)
	})
}

// withCurrentGoalIdentityWriterLocks locks the current profile first, then
// resolves and locks the current lead record, and finally re-resolves while
// both locks are held. This prevents ordinary goal delivery from writing a
// record snapshot that was superseded by resume/register between its original
// target lookup and its durable binding update.
func withCurrentGoalIdentityWriterLocks(opts goalDeliveryOptions, fn func(memberRuntime, string) error) error {
	return team.WithProfileLock(opts.Project, opts.Profile, func() error {
		locked, workstream, err := resolveMemberRuntime(opts.Project, opts.Profile, opts.Session, true, opts.Role)
		if err != nil {
			return err
		}
		if !locked.HasRecord {
			return fmt.Errorf("goal delivery requires a current launch record for role %q", opts.Role)
		}
		return launch.WithRecordLock(locked.AgentDir, func() error {
			current, currentWorkstream, err := resolveMemberRuntime(opts.Project, opts.Profile, opts.Session, true, opts.Role)
			if err != nil {
				return err
			}
			if !current.HasRecord || current.AgentDir != locked.AgentDir || currentWorkstream != workstream {
				return fmt.Errorf("goal delivery refused: current lead identity changed while acquiring writer locks")
			}
			if memberHandle(current.Member) != memberHandle(opts.Member) || current.Member.Session != opts.Member.Session ||
				current.Member.Binary != opts.Member.Binary || canonicalPath(current.Member.EffectiveCWD(opts.Project)) != canonicalPath(opts.Member.EffectiveCWD(opts.Project)) {
				return fmt.Errorf("goal delivery refused: current lead member identity changed while acquiring writer locks")
			}
			return fn(current, currentWorkstream)
		})
	})
}

// goalDeliveryReservation is the immutable identity evidence produced while
// profile and launch-record writer locks are held. External delivery (tmux,
// AMQ fallback, receipt persistence, and output) must happen only after those
// locks have been released. The per-delivery lock remains responsible for
// serializing attempts for this one role/session.
type goalDeliveryReservation struct {
	Runtime                memberRuntime
	Workstream             string
	Transition             *resumeGoalTransitionRecord
	AttemptPath            string
	TeamDigest             string
	TeamModTime            int64
	TransitionSendSnapshot *resumeGoalSendSnapshot
}

func reserveGoalDeliveryAttempt(opts *goalDeliveryOptions, receipt *deliveryReceiptData, transition *resumeGoalTransitionRecord) (string, error) {
	attemptPath, err := goalAttemptPath(opts.Project, opts.Profile, opts.Session, receipt.AttemptID)
	if err != nil {
		return "", err
	}
	if transition != nil {
		if existing, readErr := readGoalAttempt(attemptPath, receipt.AttemptID); readErr == nil {
			if validateResumeGoalAttempt(existing, opts.Project, opts.Profile, opts.Session, opts.Role, opts.Member.Handle, opts.Goal, receipt.AttemptID, opts.Namespace) != nil {
				return "", fmt.Errorf("preallocated transition attempt is mismatched")
			}
		} else if errors.Is(readErr, os.ErrNotExist) {
			createdPath, createErr := goalAttemptCreate(*opts, receipt.AttemptID, receipt.CreatedAt)
			if createErr != nil {
				return "", fmt.Errorf("create preallocated claim-once goal attempt: %w", createErr)
			}
			attemptPath = createdPath
		} else {
			return "", readErr
		}
	} else {
		createdPath, createErr := goalAttemptCreate(*opts, receipt.AttemptID, receipt.CreatedAt)
		if createErr != nil {
			return "", fmt.Errorf("create claim-once goal attempt: %w", createErr)
		}
		attemptPath = createdPath
	}
	receipt.addStage("attempt_recorded", "claim-once goal attempt recorded at "+attemptPath+"; native and AMQ paths share attempt_id="+receipt.AttemptID)
	return attemptPath, nil
}

// reserveGoalDeliveryIdentity performs the only pre-send profile/record lock
// phase. It rereads the current member while locked, validates any transition,
// and atomically merges the exact binding reservation. It deliberately does
// not discover panes, send input, write receipts, or emit output.
func reserveGoalDeliveryIdentity(opts *goalDeliveryOptions, receipt *deliveryReceiptData, prompt *string, mr memberRuntime, resolvedWorkstream string, transition *resumeGoalTransitionRecord) (goalDeliveryReservation, error) {
	reservation := goalDeliveryReservation{Runtime: mr, Workstream: resolvedWorkstream, Transition: transition}
	if !mr.HasRecord {
		attemptPath, err := reserveGoalDeliveryAttempt(opts, receipt, transition)
		if err != nil {
			return goalDeliveryReservation{}, err
		}
		reservation.AttemptPath = attemptPath
		return reservation, nil
	}
	err := withCurrentGoalIdentityWriterLocks(*opts, func(current memberRuntime, currentWorkstream string) error {
		currentTeam, err := team.ReadProfile(opts.Project, opts.Profile)
		if err != nil {
			return fmt.Errorf("reread current team while reserving goal binding: %w", err)
		}
		opts.Team = currentTeam
		opts.Member = current.Member
		if transition != nil {
			transition, err = validateResumeGoalTransitionForDelivery(*opts, current)
			if err != nil {
				return err
			}
			if transition.NewAttemptID != receipt.AttemptID {
				return fmt.Errorf("resume-goal transition attempt identity changed while reserving binding")
			}
		}
		*prompt = nativeGoalControlPrompt(opts.Goal, currentTeam, opts.Profile, opts.Session, opts.Role, receipt.AttemptID)
		if reason, disabled := current.nativePromptInjectionDisabledReason(); disabled {
			return fmt.Errorf("%s", reason)
		}
		attemptPath, err := reserveGoalDeliveryAttempt(opts, receipt, transition)
		if err != nil {
			return err
		}
		rec := current.Record
		if transition != nil && transition.BindingReserved {
			receipt.addStage("launch_record_reserved", "existing transition launch binding already reserves the exact claim-once attempt")
		} else {
			rec.GoalBinding = &launch.GoalBinding{
				Mode:       "native_goal",
				NativeGoal: true,
				Source:     "goal-control",
				Command:    *prompt,
				Detail:     "native /goal reserved as a claim-once control action",
			}
			if transition != nil {
				goalBeforeTransitionBindingCAS()
			} else {
				goalBeforeOrdinaryBindingCAS()
			}
			if err := goalLaunchWriteUnderRecordLock(current.AgentDir, rec); err != nil {
				return fmt.Errorf("reserve launch goal binding after attempt creation: %w", err)
			}
			receipt.addStage("launch_record_reserved", "launch record goal_binding reserved before native /goal delivery")
		}
		if transition != nil {
			if err := ensureResumeGoalTransitionBinding(*opts, transition, current.AgentDir); err != nil {
				return err
			}
		}
		teamDigest, teamMod, err := readGoalFileGeneration(team.ProfilePath(opts.Project, opts.Profile))
		if err != nil {
			return fmt.Errorf("capture team generation after goal reservation: %w", err)
		}
		reservation = goalDeliveryReservation{
			Runtime:     current,
			Workstream:  currentWorkstream,
			Transition:  transition,
			AttemptPath: attemptPath,
			TeamDigest:  teamDigest,
			TeamModTime: teamMod,
		}
		if transition != nil {
			_, snapshot, err := captureResumeGoalSendSnapshot(*opts, transition, *prompt, receipt.AttemptID)
			if err != nil {
				return err
			}
			reservation.TransitionSendSnapshot = &snapshot
		}
		return nil
	})
	if err != nil {
		return goalDeliveryReservation{}, err
	}
	return reservation, nil
}

// validateTransitionGoalDeliveryBeforeSend takes a final, narrow locked
// snapshot. Pane enumeration and the actual send intentionally happen after
// the locks are released, so concurrent profile/record writers never wait on
// tmux or AMQ I/O.
func validateTransitionGoalDeliveryBeforeSend(opts goalDeliveryOptions, reservation goalDeliveryReservation, prompt string, attemptID string) (memberRuntime, string, error) {
	var runtime memberRuntime
	var workstream string
	err := withCurrentGoalIdentityWriterLocks(opts, func(current memberRuntime, currentWorkstream string) error {
		transition, err := validateResumeGoalTransitionForDelivery(opts, current)
		if err != nil {
			return err
		}
		if reservation.Transition == nil || transition.NewAttemptID != reservation.Transition.NewAttemptID {
			return fmt.Errorf("resume-goal transition identity changed immediately before send")
		}
		validated, err := validateResumeGoalSendSnapshot(opts, transition, prompt, attemptID, *reservation.TransitionSendSnapshot)
		if err != nil {
			return err
		}
		goalBeforeTransitionSendCAS()
		runtime, workstream = validated, currentWorkstream
		return nil
	})
	return runtime, workstream, err
}

// markGoalDeliveryBindingDelivered is the only post-send profile/record lock
// phase. It rereads the current record and merges only the binding detail when
// the expected binding and team generation are still current, preserving
// unrelated concurrent record fields instead of overwriting a stale snapshot.
func markGoalDeliveryBindingDelivered(opts goalDeliveryOptions, reservation goalDeliveryReservation, prompt, attemptID string) error {
	return withCurrentGoalIdentityWriterLocks(opts, func(current memberRuntime, _ string) error {
		teamDigest, teamMod, err := readGoalFileGeneration(team.ProfilePath(opts.Project, opts.Profile))
		if err != nil {
			return fmt.Errorf("capture team generation after pane delivery: %w", err)
		}
		if teamDigest != reservation.TeamDigest || teamMod != reservation.TeamModTime {
			return fmt.Errorf("team generation changed after pane delivery")
		}
		if reason, disabled := current.nativePromptInjectionDisabledReason(); disabled {
			return fmt.Errorf("%s", reason)
		}
		rec := current.Record
		if rec.GoalBinding == nil || rec.GoalBinding.Mode != "native_goal" || !rec.GoalBinding.NativeGoal || rec.GoalBinding.Source != "goal-control" || rec.GoalBinding.Command != prompt {
			return fmt.Errorf("reserved binding changed after pane delivery")
		}
		goalBeforePostDeliveryBindingCAS()
		rec.GoalBinding.Detail = "native /goal delivered as a first-class claim-once control action"
		if err := goalLaunchWriteUnderRecordLock(current.AgentDir, rec); err != nil {
			return err
		}
		return nil
	})
}

func executeGoalDelivery(opts goalDeliveryOptions) (result mutationResult, err error) {
	if err := os.MkdirAll(goalAttemptDir(opts.Project, opts.Profile, opts.Session), 0o755); err != nil {
		return mutationResult{}, fmt.Errorf("ensure goal delivery lock dir: %w", err)
	}
	err = flock.WithLock(goalDeliveryLockPath(opts), func() error {
		var inner error
		result, inner = executeGoalDeliveryLocked(opts)
		return inner
	})
	return result, err
}

func executeGoalDeliveryLocked(opts goalDeliveryOptions) (mutationResult, error) {
	receipt := newDeliveryReceipt(opts.Project, opts.Profile, opts.Session, opts.Role, opts.Member.Handle, opts.Mode, "native_goal")
	opts.AttemptID = receipt.AttemptID
	prompt := nativeGoalControlPrompt(opts.Goal, opts.Team, opts.Profile, opts.Session, opts.Role, receipt.AttemptID)
	receipt.Method = "native_goal_control"
	receipt.addStage("queued", "native /goal control delivery accepted by amq-squad")

	mr, resolvedWorkstream, err := resolveMemberRuntime(opts.Project, opts.Profile, opts.Session, true, opts.Role)
	if err != nil {
		return mutationResult{}, err
	}
	transition, err := validateResumeGoalTransitionForDelivery(opts, mr)
	if err != nil {
		return mutationResult{}, err
	}
	if transition != nil {
		receipt.AttemptID = transition.NewAttemptID
		opts.AttemptID = transition.NewAttemptID
		prompt = nativeGoalControlPrompt(opts.Goal, opts.Team, opts.Profile, opts.Session, opts.Role, receipt.AttemptID)
	}
	return executeGoalDeliveryResolved(opts, receipt, prompt, mr, resolvedWorkstream, transition)
}

func executeGoalDeliveryResolved(opts goalDeliveryOptions, receipt deliveryReceiptData, prompt string, mr memberRuntime, resolvedWorkstream string, transition *resumeGoalTransitionRecord) (mutationResult, error) {
	var err error
	reservation, err := reserveGoalDeliveryIdentity(&opts, &receipt, &prompt, mr, resolvedWorkstream, transition)
	if err != nil {
		attemptPath, _ := goalAttemptPath(opts.Project, opts.Profile, opts.Session, receipt.AttemptID)
		return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, State: goalDeliveryStateNotSent, Cause: err}
	}
	mr, resolvedWorkstream, transition = reservation.Runtime, reservation.Workstream, reservation.Transition
	attemptPath := reservation.AttemptPath
	if transition != nil {
		mr, resolvedWorkstream, err = validateTransitionGoalDeliveryBeforeSend(opts, reservation, prompt, receipt.AttemptID)
		if err != nil {
			return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, State: goalDeliveryStateNotSent, Cause: err}
		}
		if err := consumeResumeGoalTransition(opts, receipt.AttemptID); err != nil {
			return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, State: goalDeliveryStateNotSent, Cause: err}
		}
	}
	// All pane work occurs outside profile and launch-record writer locks. A
	// concurrent writer may complete here; the post-send phase below will merge
	// independent record changes or refuse a stale identity without overwriting.
	panes, listErr := statusPaneLister()
	if listErr != nil {
		if tmuxpane.IsPermissionDenied(listErr) {
			return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, State: goalDeliveryStateNotSent, Cause: errTmuxAccessDenied()}
		}
		panes = nil
	}
	paneID, _, ok := resolveControlTarget(mr, resolvedWorkstream, panes)
	if !ok || strings.TrimSpace(paneID) == "" {
		return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, State: goalDeliveryStateNotSent, Cause: fmt.Errorf("no live tmux pane found for role %q", opts.Role)}
	}
	receipt.PaneID = paneID
	if transition != nil {
		receipt.addStage("control_delivery_started", "revalidated exact transition-bound pane immediately before native /goal control")
	} else {
		receipt.addStage("control_delivery_started", "resolved exact target pane for native /goal control")
	}
	if err := sendPromptToPane(paneID, prompt); err != nil {
		var queued *tmuxpane.QueuedInputError
		var unconfirmed *tmuxpane.SubmitUnconfirmedError
		if errors.As(err, &queued) {
			receipt.Status = "native_goal_queued"
			receipt.Detail = err.Error()
			receipt.addStage("native_goal_queued", "native goal text is known present in the lead input and will submit when the agent goes idle")
			receipt.addStage("pending_without_amq_action", "durable pending evidence recorded; no actionable AMQ fallback emitted because the native text is known present")
			if writeErr := goalDeliveryReceiptWrite(opts.Project, opts.Profile, opts.Session, &receipt); writeErr != nil {
				return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, Sent: true, State: goalDeliveryStateNativeQueued, Cause: &goalFallbackDurabilityError{DeliveryErr: err, FallbackErr: fmt.Errorf("write queued native-goal receipt: %w", writeErr)}}
			}
			fmt.Fprintf(os.Stderr, "warning: goal queued in the lead's input; it will submit when the agent goes idle. Pending attempt %s was recorded without a second actionable AMQ goal; continuing.\n", receipt.AttemptID)
			return mutationResult{
				Command:         "goal deliver",
				Status:          receipt.Status,
				Project:         opts.Project,
				Session:         opts.Session,
				Profile:         opts.Profile,
				Namespace:       opts.Namespace,
				Role:            opts.Role,
				Handle:          opts.Member.Handle,
				DeliveryReceipt: &receipt,
			}, nil
		}
		if errors.As(err, &unconfirmed) {
			fallback, fallbackErr := goalFallbackAMQSend(opts)
			if fallbackErr != nil {
				receipt.Status = "failed"
				receipt.Detail = fmt.Sprintf("native goal submission was unconfirmed and claim-once AMQ fallback failed: %v", fallbackErr)
				receipt.addStage("failed", receipt.Detail)
				_ = goalDeliveryReceiptWrite(opts.Project, opts.Profile, opts.Session, &receipt)
				return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, Sent: true, State: goalDeliveryStateNativeUnconfirmed, Cause: &goalFallbackDurabilityError{DeliveryErr: err, FallbackErr: fallbackErr}}
			}
			receipt.MessageID = fallback.MessageID
			receipt.Root = fallback.Root
			receipt.Thread = fallback.Thread
			receipt.Fallback = true
			receipt.Method = "durable_amq_goal_fallback"
			receipt.Status = "durable_goal_fallback"
			receipt.Detail = err.Error()
			receipt.addStage("native_goal_unconfirmed", err.Error())
			receipt.addStage("claim_once_contract", "native prompt and AMQ todo share attempt_id="+receipt.AttemptID+" under an at-most-once contract: exactly one route may atomically claim it; a claimant crash before activation is observable but never replayed")
			receipt.addStage("written_to_amq", "single actionable claim-once goal fallback written to the lead inbox")
			if writeErr := goalDeliveryReceiptWrite(opts.Project, opts.Profile, opts.Session, &receipt); writeErr != nil {
				return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, Sent: true, State: goalDeliveryStateFallbackSent, Cause: &goalFallbackSentReceiptError{
					MessageID:   fallback.MessageID,
					Root:        fallback.Root,
					Thread:      fallback.Thread,
					DeliveryErr: err,
					ReceiptErr:  writeErr,
				}}
			}
			messageID := strings.TrimSpace(fallback.MessageID)
			if messageID == "" {
				messageID = "(message id unavailable)"
			}
			fmt.Fprintf(os.Stderr, "warning: native goal submission was not confirmed. Claim-once durable AMQ fallback %s shares attempt %s; continuing.\n", messageID, receipt.AttemptID)
			return mutationResult{
				Command:         "goal deliver",
				Status:          receipt.Status,
				Project:         opts.Project,
				Session:         opts.Session,
				Profile:         opts.Profile,
				Namespace:       opts.Namespace,
				Role:            opts.Role,
				Handle:          opts.Member.Handle,
				MessageID:       fallback.MessageID,
				Thread:          fallback.Thread,
				Root:            fallback.Root,
				DeliveryReceipt: &receipt,
			}, nil
		}
		receipt.Status = "failed"
		receipt.Detail = err.Error()
		receipt.addStage("failed", err.Error())
		_ = goalDeliveryReceiptWrite(opts.Project, opts.Profile, opts.Session, &receipt)
		return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, Sent: true, State: goalDeliveryStatePaneFailed, Cause: err}
	}
	receipt.addStage("pane_settled", "SendPromptToPane waited for target pane output to settle before native /goal control delivery")
	receipt.Status = "native_goal_delivered"
	receipt.addStage("native_goal_delivered", "native /goal command delivered without ordinary prompt busy-guard semantics")
	if mr.HasRecord {
		if err := markGoalDeliveryBindingDelivered(opts, reservation, prompt, receipt.AttemptID); err != nil {
			return mutationResult{}, &goalPostDeliveryBindingError{AttemptID: receipt.AttemptID, Recovery: goalRetryAttemptCommand(opts, receipt.AttemptID), Cause: err}
		}
		receipt.addStage("launch_record_updated", "reserved launch goal_binding marked delivered")
	}
	if err := goalDeliveryReceiptWrite(opts.Project, opts.Profile, opts.Session, &receipt); err != nil {
		return mutationResult{}, &goalDeliveryAttemptError{AttemptID: receipt.AttemptID, AttemptPath: attemptPath, Sent: true, State: goalDeliveryStatePaneDelivered, Cause: err}
	}
	return mutationResult{
		Command:         "goal deliver",
		Status:          receipt.Status,
		Project:         opts.Project,
		Session:         opts.Session,
		Profile:         opts.Profile,
		Namespace:       opts.Namespace,
		Role:            opts.Role,
		Handle:          opts.Member.Handle,
		DeliveryReceipt: &receipt,
	}, nil
}

func goalRetryAttemptCommand(opts goalDeliveryOptions, attemptID string) string {
	args := []string{"amq-squad", "goal", "retry-attempt", "--project", opts.Project, "--profile", opts.Profile, "--session", opts.Session, "--role", opts.Role, "--attempt-id", attemptID, "--yes"}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func nativeGoalControlPrompt(goal string, t team.Team, profile, session, role string, attemptIDs ...string) string {
	args := []string{"/goal", "--goal", strconv.Quote(goal), "--session", session, "--profile", profile, "--mode", effectiveTeamExecutionMode(t)}
	if role != "" && role != "cto" {
		args = append(args, "--lead", role)
	}
	if leadMode := team.EffectiveLeadMode(t); leadMode != team.LeadModeBuilder {
		args = append(args, "--lead-mode", leadMode)
	}
	if target := strings.TrimSpace(t.TargetContract); target != "" {
		args = append(args, "--target-contract", target)
	}
	if len(attemptIDs) > 0 && strings.TrimSpace(attemptIDs[0]) != "" {
		args = append(args, "--attempt-id", strings.TrimSpace(attemptIDs[0]))
	}
	return strings.Join(args, " ")
}

func runGoalDraft(args []string) error {
	return runGoalDraftWithVersion(args, "dev")
}

func runGoalDraftWithVersion(args []string, version string) error {
	fs := flag.NewFlagSet("goal draft", flag.ContinueOnError)
	goalFlag := fs.String("goal", "", "high-level goal to turn into a setup draft")
	repoFlag := fs.String("repo", "", "GitHub repo owner/name for milestone lookup")
	milestoneFlag := fs.String("milestone", "", "GitHub milestone title to include issue titles and URLs")
	targetContractFlag := fs.String("target-contract", "", "target amq-squad contract version for compatibility checks (default: milestone if semver, else 2.10.0)")
	sessionFlag := fs.String("session", "", "AMQ workstream session name")
	profileFlag := fs.String("profile", "", "team profile name for the proposed setup")
	leadFlag := fs.String("lead", "cto", "operator-visible goal lead role")
	leadModeFlag := fs.String("lead-mode", "", "lead implementation posture: builder (default) or planner")
	modeFlag := fs.String("mode", executionModeProjectLead, "execution mode: global_orchestrator, project_lead, project_team, or direct_lead_session")
	controlRootFlag := fs.String("control-root", "", "control-plane root directory (default: cwd)")
	targetProjectRootFlag := fs.String("target-project-root", "", "target project root directory (default: cwd)")
	compositionFlag := fs.String("composition", team.CompositionSeeded, "composition mode: seeded (default) or autonomous")
	maxAgentsFlag := fs.Int("max-agents", 0, "autonomous guardrail: maximum active agents")
	maxTotalSpawnsFlag := fs.Int("max-total-spawns", 0, "autonomous guardrail: maximum total autonomous spawns")
	allowedRolesFlag := fs.String("allowed-roles", "", "autonomous guardrail: comma-separated role allowlist")
	allowedRoleClassesFlag := fs.String("allowed-role-classes", "", "autonomous guardrail: comma-separated role-class allowlist")
	budgetTurnsFlag := fs.Int("budget-turns", 0, "autonomous guardrail: maximum lead turns before operator review")
	idleReapMinutesFlag := fs.Int("idle-reap-minutes", 0, "autonomous guardrail: idle minutes before prune is allowed")
	visibilityFlag := fs.String("visibility", visibilitySiblingTabs, "launch topology: sibling-tabs (default), detached, current, or plan")
	codexArgsRaw := fs.String("codex-args", "", "explicit Codex args for the visible lead launch command, e.g. '-c model_reasoning_effort=high'; when omitted the recommended effort is shown as a comment only, never a live flag")
	codexOnly := fs.Bool("codex-only", false, "propose Codex binaries for every role")
	skillInvocation := fs.Bool("skill-invocation", false, "print a ready-to-paste /amq-squad-orchestrator invocation block")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned goal_draft envelope instead of Markdown")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad goal draft - produce a preview-only setup plan from a goal

Usage:
  amq-squad goal draft --goal TEXT [--repo owner/repo] [--milestone NAME] [--session NAME] [--profile NAME] [--lead ROLE] [--lead-mode builder|planner] [--mode project_lead|project_team|direct_lead_session|global_orchestrator] [--visibility sibling-tabs|detached|current|plan] [--composition seeded|autonomous] [--max-agents N --max-total-spawns N --allowed-roles role,... --budget-turns N] [--codex-args "..."] [--codex-only] [--skill-invocation] [--json]

The draft is read-only. It prints proposed briefs, roster entries, task-store
items, spawn gates, dispatches, and the orchestrator prompt, but it does not
write files, mutate rosters, send AMQ messages, launch agents, or create tasks.

Examples:
  amq-squad goal draft --goal "deliver GitHub milestone v2.7.0" --repo omriariav/amq-squad --milestone v2.7.0 --session v2-7-0 --profile codex-v2-7-0
  amq-squad goal draft --goal "fix issue 96" --session issue-96 --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("goal draft takes no positional arguments; use --goal TEXT")
	}
	leadCodexArgs, err := parseAgentArgs(*codexArgsRaw)
	if err != nil {
		return fmt.Errorf("parse --codex-args: %w", err)
	}
	goal := strings.TrimSpace(*goalFlag)
	if goal == "" {
		return usageErrorf("goal draft requires --goal TEXT")
	}
	if strings.TrimSpace(*milestoneFlag) != "" && strings.TrimSpace(*repoFlag) == "" {
		return usageErrorf("goal draft --milestone requires --repo owner/repo")
	}
	data, err := buildGoalDraft(goalDraftOptions{
		Goal:               goal,
		Repo:               strings.TrimSpace(*repoFlag),
		Milestone:          strings.TrimSpace(*milestoneFlag),
		TargetContract:     strings.TrimSpace(*targetContractFlag),
		Session:            strings.TrimSpace(*sessionFlag),
		Profile:            strings.TrimSpace(*profileFlag),
		Lead:               strings.TrimSpace(*leadFlag),
		LeadMode:           strings.TrimSpace(*leadModeFlag),
		Mode:               strings.TrimSpace(*modeFlag),
		ControlRoot:        strings.TrimSpace(*controlRootFlag),
		TargetProjectRoot:  strings.TrimSpace(*targetProjectRootFlag),
		CodexOnly:          *codexOnly,
		RuntimeVersion:     version,
		Composition:        strings.TrimSpace(*compositionFlag),
		MaxAgents:          *maxAgentsFlag,
		MaxTotalSpawns:     *maxTotalSpawnsFlag,
		AllowedRoles:       strings.TrimSpace(*allowedRolesFlag),
		AllowedRoleClasses: strings.TrimSpace(*allowedRoleClassesFlag),
		BudgetTurns:        *budgetTurnsFlag,
		IdleReapMinutes:    *idleReapMinutesFlag,
		Visibility:         strings.TrimSpace(*visibilityFlag),
		CodexArgs:          leadCodexArgs,
		ProvidedFields:     goalDraftProvidedFields(fs),
	})
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSONEnvelope("goal_draft", data)
	}
	if *skillInvocation {
		fmt.Fprint(os.Stdout, data.SkillInvocation)
		return nil
	}
	writeGoalDraftMarkdown(os.Stdout, data)
	return nil
}

type goalDraftOptions struct {
	Goal               string
	Repo               string
	Milestone          string
	TargetContract     string
	Session            string
	Profile            string
	Lead               string
	LeadMode           string
	Mode               string
	ControlRoot        string
	TargetProjectRoot  string
	CodexOnly          bool
	RuntimeVersion     string
	Composition        string
	MaxAgents          int
	MaxTotalSpawns     int
	AllowedRoles       string
	AllowedRoleClasses string
	BudgetTurns        int
	IdleReapMinutes    int
	Visibility         string
	// CodexArgs are explicit Codex args the operator supplied for the visible
	// lead (#291). When set, they override the seeded reasoning-effort default and
	// flow into the applyable launch command; when empty, the default effort stays
	// an inert recommendation.
	CodexArgs []string
	// ProvidedFields records which operator-facing input flags were explicitly
	// set on the command line (#291), so the preview can label each Step 1 field
	// PROVIDED vs DEFAULT. Keyed by the field name used in field_sources.
	ProvidedFields map[string]bool
}

func buildGoalDraft(opts goalDraftOptions) (goalDraftData, error) {
	session := strings.TrimSpace(opts.Session)
	if session == "" {
		seed := opts.Milestone
		if seed == "" {
			seed = opts.Goal
		}
		session = sanitizeWorkstreamName(seed)
	}
	if err := validateWorkstreamName(session); err != nil {
		return goalDraftData{}, fmt.Errorf("invalid session: %w", err)
	}
	profile := strings.TrimSpace(opts.Profile)
	if profile == "" {
		if opts.CodexOnly {
			profile = "codex-" + session
		} else {
			profile = session
		}
	}
	if err := validateProfileName(profile); err != nil {
		return goalDraftData{}, fmt.Errorf("invalid profile: %w", err)
	}
	lead := strings.TrimSpace(opts.Lead)
	if lead == "" {
		lead = "cto"
	}
	if err := validateProfileName(lead); err != nil {
		return goalDraftData{}, fmt.Errorf("invalid lead: %w", err)
	}
	leadMode, err := normalizeLeadMode(opts.LeadMode)
	if err != nil {
		return goalDraftData{}, err
	}
	mode, err := normalizeExecutionMode(opts.Mode)
	if err != nil {
		return goalDraftData{}, err
	}
	composition := strings.TrimSpace(opts.Composition)
	if composition == "" {
		composition = team.CompositionSeeded
	}
	autonomousPolicy, err := resolveAutonomousPolicy(composition, opts.MaxAgents, opts.MaxTotalSpawns, opts.AllowedRoles, opts.AllowedRoleClasses, opts.BudgetTurns, opts.IdleReapMinutes)
	if err != nil {
		return goalDraftData{}, err
	}
	visibility, err := normalizeLaunchVisibility(opts.Visibility)
	if err != nil {
		return goalDraftData{}, err
	}
	issues, err := resolveGoalMilestoneIssues(opts.Repo, opts.Milestone)
	if err != nil {
		return goalDraftData{}, err
	}
	targetContract := inferGoalTargetContract(opts.TargetContract, opts.Milestone)
	controlRoot := cleanRootOrDefault(opts.ControlRoot, cwdOrEmpty())
	targetRoot, targetRootSource, targetRootCandidates := classifyDraftTargetProjectRoot(mode, controlRoot, opts.TargetProjectRoot, opts.Repo)
	data := goalDraftData{
		Goal:                        opts.Goal,
		Repo:                        opts.Repo,
		Milestone:                   opts.Milestone,
		TargetContract:              targetContract,
		Session:                     session,
		Profile:                     profile,
		Lead:                        lead,
		LeadMode:                    leadMode,
		Mode:                        mode,
		ControlRoot:                 controlRoot,
		TargetProjectRoot:           targetRoot,
		TargetProjectRootSource:     targetRootSource,
		TargetProjectRootCandidates: targetRootCandidates,
		Namespace:                   squadnamespace.Resolve("", profile, session),
		Composition:                 composition,
		Visibility:                  visibility,
		AutonomousPolicy:            autonomousPolicy,
		PreviewOnly:                 true,
		CodexOnly:                   opts.CodexOnly,
		IssueSources:                issues,
		Roster:                      defaultGoalRoster(lead, opts.CodexOnly, len(issues)),
		Notes: []string{
			"Seeded composition remains the default; autonomous composition requires explicit opt-in and policy limits.",
			"This draft is preview-only and does not mutate team.json, briefs, task files, AMQ mailboxes, launch records, wake locks, or panes.",
			"Default visibility is sibling-tabs: launch the visible lead from an existing visible tmux pane with the generated native /goal prompt; workers remain behind spawn gates.",
			"Step 1 / Step 2 / Step 3: preview first, create or register the visible goal lead, then monitor the run through that lead.",
			"Execution mode is explicit: global_orchestrator monitors only; project_lead and project_team mutate through their project-root lead; direct_lead_session is an explicit exception.",
			"The top-level orchestrator dispatches to the visible goal lead; child agents stay implementation details unless an approval gate, blocker, release risk, or final evidence requires surfacing them.",
			"Leads must immediately surface any blocker or approval request to the operator/orchestrator-visible surface; never leave it only in an internal pane or hidden gate.",
			"When wake is unavailable, the parent orchestrator or NOC polls each visible lead's inbox, gates, and status on a cadence; one /goal maps to one visible lead.",
			"Visible lead binding is explicit: launch the visible lead with the generated native /goal prompt when possible; status falls back to AMQ task + active brief + task store until launch evidence exists.",
			"Generated prompts preserve team rules and custom role contracts across profile/session namespaces.",
			"Use --visibility detached only when a separate tmux session is intentional; use --visibility current for split panes in the current window; use --visibility plan when you want commands only.",
			"Merge, push, release, destructive filesystem actions, external communications, and provider side effects remain operator-owned.",
		},
	}
	switch targetRootSource {
	case targetRootSourceResolvedUnconfirmed:
		data.Notes = append(data.Notes, fmt.Sprintf("target_project_root is a PROPOSED single git-remote match (%s), NOT yet confirmed: confirm it or pass --target-project-root before start; team init refuses to start a global_orchestrator run without an explicit --target-project-root.", targetRoot))
	case targetRootSourceUnresolved:
		data.Notes = append(data.Notes, "target_project_root is UNRESOLVED for this global_orchestrator goal (no single git-remote match of the repo under the control root); pass an explicit --target-project-root before start. amq-squad will not guess a project tree from the control root.")
	}
	data.OrchestratorPrompt = renderGoalOrchestratorPrompt(data)
	data.GoalBinding = goalBindingForDraft(data.Namespace, data.OrchestratorPrompt)
	data.Execution = executionContract(mode, controlRoot, targetRoot, profile, session, data.Namespace.ID, lead, data.GoalBinding.Mode, visibility, opts.RuntimeVersion, targetContract, goalVisibleMembers(mode, data.Roster, lead))
	applyLeadModeToDraftContract(&data.Execution, leadMode, lead, data.Roster)
	// For a global_orchestrator goal whose target is only a proposal or
	// unresolved, the execution contract must NOT report a target_project_root
	// (executionContract falls back to cwd). Keep it empty so no surface treats an
	// unconfirmed/guessed path as the place the lead edits (#290); the proposal
	// stays in target_project_root + target_project_root_source + candidates.
	if mode == executionModeGlobalOrchestrator &&
		(targetRootSource == targetRootSourceResolvedUnconfirmed || targetRootSource == targetRootSourceUnresolved) {
		data.Execution.TargetProjectRoot = ""
	}
	data.codexArgsProvided = opts.ProvidedFields["codex_args"]
	// When the operator explicitly supplied --codex-args, override the visible
	// lead's seeded effort default with their value so it flows into the applyable
	// launch command (#291). Without it, the seeded default stays an inert
	// recommendation and is never emitted as a live flag.
	if data.codexArgsProvided && len(opts.CodexArgs) > 0 {
		for i := range data.Roster {
			if data.Roster[i].Role == lead {
				data.Roster[i].CodexArgs = append([]string(nil), opts.CodexArgs...)
				break
			}
		}
	}
	data.FieldSources = goalDraftFieldSources(opts.ProvidedFields, targetRootSource)
	data.BriefSkeleton = renderGoalBriefSkeleton(data)
	data.Tasks = defaultGoalTasks(data)
	data.SpawnGates = defaultGoalSpawnGates(data)
	data.Dispatches = defaultGoalDispatches(data)
	data.ApplyableMutations = defaultGoalMutations(data)
	data.SkillInvocation = renderGoalSkillInvocation(data)
	data.Steps = goalDraftSteps(data)
	return data, nil
}

// goalDraftProvidedFields records which operator-facing input flags were set on
// the command line, so the preview can label each Step 1 field provided/default.
func goalDraftProvidedFields(fs *flag.FlagSet) map[string]bool {
	flagByField := map[string]string{
		"goal": "goal", "repo": "repo", "milestone": "milestone",
		"session": "session", "profile": "profile", "lead": "lead",
		"lead_mode": "lead-mode", "mode": "mode", "visibility": "visibility", "composition": "composition",
		"target_contract": "target-contract", "control_root": "control-root",
		"target_project_root": "target-project-root", "codex_only": "codex-only",
		"codex_args": "codex-args",
	}
	out := make(map[string]bool, len(flagByField))
	for field, flagName := range flagByField {
		if flagWasSet(fs, flagName) {
			out[field] = true
		}
	}
	return out
}

// goalDraftFieldSources labels each operator-facing Step 1 input provided/default
// (#291). target_project_root keeps its richer #290 source vocabulary.
func goalDraftFieldSources(provided map[string]bool, targetRootSource string) map[string]string {
	labeled := []string{"goal", "repo", "milestone", "session", "profile", "lead", "lead_mode", "mode", "visibility", "composition", "target_contract", "control_root", "codex_only"}
	out := make(map[string]string, len(labeled)+1)
	for _, f := range labeled {
		if provided[f] {
			out[f] = targetRootSourceProvided
		} else {
			out[f] = targetRootSourceDefault
		}
	}
	out["target_project_root"] = targetRootSource
	return out
}

// goalDraftSteps builds the guided operator flow (#291): each step states what
// just happened, what is about to happen, what the operator approves, and the
// next gate.
func goalDraftSteps(data goalDraftData) []goalDraftStep {
	register := ""
	if data.Mode == executionModeGlobalOrchestrator {
		register = " The orchestrator registers its own pane via --register-orchestrator."
	}
	return []goalDraftStep{
		{
			Number:        1,
			Title:         "Preview",
			JustHappened:  "amq-squad turned your goal into a preview-only plan (no files, rosters, AMQ, panes, or tasks were touched).",
			AboutToHappen: "Review the labeled plan below: each Step 1 field is marked provided (you set it) or default (auto). Override any default by passing its flag.",
			Approving:     "Nothing yet — this step is read-only.",
			NextGate:      "Approve the plan, then run Step 2 to create/register the visible lead.",
		},
		{
			Number:        2,
			Title:         "Create / launch the visible lead",
			JustHappened:  "You approved the preview.",
			AboutToHappen: "amq-squad will create the profile/session/team and launch or resume a real visible project lead with the generated native /goal prompt. Use lead registration only from an already verified project-lead pane, never to adopt a global orchestrator pane as the project lead." + register,
			Approving:     "Creating durable team config and starting the lead (the first mutating step).",
			NextGate:      "Per-spawn operator approval on gate/spawn-<role> before any worker is brought up.",
		},
		{
			Number:        3,
			Title:         "Monitor through the lead",
			JustHappened:  "The visible lead is running and owns the deliverable.",
			AboutToHappen: "Watch via amq-squad status --json and the lead's reports; only gates, blockers, and DONE are surfaced to you — child detail stays internal unless escalated.",
			Approving:     "Operator gates the lead raises (merge/release/external actions).",
			NextGate:      "Operator approvals on gate/<topic>. With wake limits, poll the lead's inbox/gates/status on a cadence.",
		},
	}
}

func renderGoalSkillInvocation(data goalDraftData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "/amq-squad-orchestrator --goal %s --session %s --profile %s --mode %s --lead %s",
		quoteSkillInvocationArg(data.Goal),
		quoteSkillInvocationArg(data.Session),
		quoteSkillInvocationArg(data.Profile),
		quoteSkillInvocationArg(data.Mode),
		quoteSkillInvocationArg(data.Lead),
	)
	if data.Repo != "" {
		fmt.Fprintf(&b, " --repo %s", quoteSkillInvocationArg(data.Repo))
	}
	if data.Milestone != "" {
		fmt.Fprintf(&b, " --milestone %s", quoteSkillInvocationArg(data.Milestone))
	}
	if data.TargetContract != "" {
		fmt.Fprintf(&b, " --target-contract %s", quoteSkillInvocationArg(data.TargetContract))
	}
	if data.LeadMode != "" && data.LeadMode != team.LeadModeBuilder {
		fmt.Fprintf(&b, " --lead-mode %s", quoteSkillInvocationArg(data.LeadMode))
	}
	if data.Composition != "" {
		fmt.Fprintf(&b, " --composition %s", quoteSkillInvocationArg(data.Composition))
	}
	if data.Visibility != "" {
		fmt.Fprintf(&b, " --visibility %s", quoteSkillInvocationArg(data.Visibility))
	}
	if data.CodexOnly {
		b.WriteString(" --codex-only")
	}
	// #291: a global_orchestrator run should register its own control pane.
	if data.Mode == executionModeGlobalOrchestrator {
		b.WriteString(" --register-orchestrator")
	}
	// #290/#291: carry target_project_root into the invocation ONLY when the
	// operator explicitly provided it; never emit a resolved_unconfirmed or
	// unresolved path as an executable flag.
	if data.FieldSources["target_project_root"] == targetRootSourceProvided && data.TargetProjectRoot != "" {
		fmt.Fprintf(&b, " --target-project-root %s", quoteSkillInvocationArg(data.TargetProjectRoot))
	}
	b.WriteString("\n")
	// Recommendations and required-but-unprovided inputs are rendered as clearly
	// marked comments, NOT executable flags, so the pasted command stays safe and
	// does not silently change runtime assumptions (#291).
	for _, rec := range goalSkillInvocationRecommendations(data) {
		b.WriteString(rec)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(data.OrchestratorPrompt)
	if !strings.HasSuffix(data.OrchestratorPrompt, "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

// goalSkillInvocationRecommendations returns clearly-marked comment lines (each
// starting with "# ") for high-value inputs the operator has not provided. They
// are advisory, never executable flags (#291): a Codex lead's reasoning effort
// is recommended, not silently injected, and an unconfirmed global_orchestrator
// target is flagged as required-before-start rather than smuggled in.
func goalSkillInvocationRecommendations(data goalDraftData) []string {
	var recs []string
	if data.Mode == executionModeGlobalOrchestrator {
		recs = append(recs, `# recommended for a Codex lead: --codex-args "-c model_reasoning_effort=high"`)
		if data.FieldSources["target_project_root"] != targetRootSourceProvided {
			recs = append(recs, "# REQUIRED before start: --target-project-root <confirmed local checkout> (a global_orchestrator run will not begin without an explicit, confirmed project path)")
		}
		recs = append(recs, "# multi-workstream board: if more than one run is active or recently active in this conversation, maintain an in-conversation board with name/repo/profile/session/lead/pane, state, last checked, next poll source, gate/blocker, last action, next action, polling commands, and closed-run demotion")
	}
	return recs
}

func quoteSkillInvocationArg(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return `""`
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n', '\r', '\t':
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func goalBindingForDraft(ns squadnamespace.Ref, command string) goalBindingData {
	binding := goalBindingForNamespace(ns)
	binding.Mode = "native_goal_pending"
	binding.NativeGoal = true
	binding.Verified = false
	binding.Source = "orchestrator-prompt"
	binding.NativeSource = "generated-/goal"
	binding.Command = command
	binding.Detail = "The generated visible-lead prompt is a native /goal command; status reports native_goal only after the lead launch record records that command, otherwise AMQ task + brief fallback remains explicit."
	return binding
}

func inferGoalTargetContract(explicit, milestone string) string {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimPrefix(strings.TrimSpace(explicit), "v")
	}
	if _, ok := parseSemverParts(milestone); ok {
		return strings.TrimPrefix(strings.TrimSpace(milestone), "v")
	}
	return "2.10.0"
}

func goalVisibleMembers(mode string, roster []goalRosterMember, lead string) []string {
	switch mode {
	case executionModeProjectTeam:
		out := make([]string, 0, len(roster))
		for _, member := range roster {
			if strings.TrimSpace(member.Role) != "" {
				out = append(out, member.Role)
			}
		}
		return out
	default:
		if strings.TrimSpace(lead) == "" {
			return nil
		}
		return []string{lead}
	}
}

func validateProfileName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("invalid profile name %q: use lowercase a-z, 0-9, - or _", name)
	}
	return nil
}

func resolveGoalMilestoneIssues(repo, milestone string) ([]goalIssueSource, error) {
	repo = strings.TrimSpace(repo)
	milestone = strings.TrimSpace(milestone)
	if milestone == "" {
		return nil, nil
	}
	out, err := goalGhRun("issue", "list", "--repo", repo, "--milestone", milestone, "--state", "all", "--limit", "200", "--json", "number,title,url,state")
	if err != nil {
		return nil, fmt.Errorf("goal draft milestone %q in %s: gh: %w", milestone, repo, err)
	}
	var issues []goalIssueSource
	if err := json.Unmarshal(out, &issues); err != nil {
		return nil, fmt.Errorf("goal draft milestone %q in %s: parse gh output: %w", milestone, repo, err)
	}
	sort.SliceStable(issues, func(i, j int) bool { return issues[i].Number < issues[j].Number })
	return issues, nil
}

func defaultGoalRoster(lead string, codexOnly bool, issueCount int) []goalRosterMember {
	binary := func(defaultBinary string) string {
		if codexOnly {
			return "codex"
		}
		return defaultBinary
	}
	leadReason := "Visible goal lead: owns Step 1 preview, Step 2 setup/register, Step 3 monitoring, final evidence, and operator escalation."
	if lead == "cto" {
		leadReason = "Lead orchestration, scope control, architecture, final sign-off, and operator escalation."
	}
	roster := []goalRosterMember{{
		Role:      lead,
		Handle:    lead,
		Binary:    "codex",
		Reason:    leadReason,
		CodexArgs: []string{"-c", "model_reasoning_effort=high"},
	}}
	appendWorker := func(member goalRosterMember) {
		for _, existing := range roster {
			if existing.Role == member.Role {
				return
			}
		}
		roster = append(roster, member)
	}
	appendWorker(goalRosterMember{
		Role:   "fullstack",
		Handle: "fullstack",
		Binary: binary("claude"),
		Reason: "Primary implementation owner for the drafted task plan.",
	})
	appendWorker(goalRosterMember{
		Role:      "senior-dev",
		Handle:    "senior-dev",
		Binary:    "codex",
		Reason:    "Independent implementation-shape and risk review before merge-ready claims.",
		CodexArgs: []string{"-c", "model_reasoning_effort=high"},
	})
	if issueCount > 3 {
		appendWorker(goalRosterMember{
			Role:   "qa",
			Handle: "qa",
			Binary: binary("claude"),
			Reason: "Milestone-sized work benefits from explicit regression and release-risk coverage.",
		})
	}
	for i := range roster {
		if roster[i].Binary == "codex" && len(roster[i].CodexArgs) == 0 {
			roster[i].CodexArgs = []string{"-c", "model_reasoning_effort=medium"}
		}
	}
	return roster
}

func renderGoalBriefSkeleton(data goalDraftData) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", data.Session)
	fmt.Fprintf(&b, "## Goal\n%s\n\n", data.Goal)
	if data.Repo != "" || data.Milestone != "" {
		b.WriteString("## Source\n")
		if data.Repo != "" {
			fmt.Fprintf(&b, "- Repo: %s\n", data.Repo)
		}
		if data.Milestone != "" {
			fmt.Fprintf(&b, "- Milestone: %s\n", data.Milestone)
		}
		for _, issue := range data.IssueSources {
			fmt.Fprintf(&b, "- #%d %s - %s\n", issue.Number, issue.Title, issue.URL)
		}
		b.WriteString("\n")
	}
	b.WriteString("## Scope\n- Deliver the goal through amq-squad orchestration.\n- Keep AMQ, the task store, and the workstream brief as durable coordination records.\n")
	fmt.Fprintf(&b, "- Execution mode: %s. Mutable actor: %s. Implementation allowed: %t.\n", data.Execution.Mode, data.Execution.MutableActor, data.Execution.ImplementationAllowed)
	fmt.Fprintf(&b, "- Visible lead binding: %s (%s).\n", data.GoalBinding.Mode, data.GoalBinding.Source)
	fmt.Fprintf(&b, "- Composition mode: %s.\n\n", data.Composition)
	fmt.Fprintf(&b, "- Visibility: %s.\n\n", data.Visibility)
	if data.Execution.Mode == executionModeGlobalOrchestrator {
		b.WriteString("- Global orchestrator board: when this conversation owns more than one active or recently active workstream, maintain an in-conversation board with run name, repo, profile/session, lead/pane, state, last checked, next poll source, current gate/blocker, last action, next action, polling commands, and closed-run demotion.\n\n")
	}
	if data.AutonomousPolicy != nil {
		b.WriteString("## Autonomous policy\n")
		fmt.Fprintf(&b, "- Max active agents: %d\n", data.AutonomousPolicy.MaxActiveAgents)
		fmt.Fprintf(&b, "- Max total spawns: %d\n", data.AutonomousPolicy.MaxTotalSpawns)
		fmt.Fprintf(&b, "- Allowed roles: %s\n", strings.Join(data.AutonomousPolicy.AllowedRoles, ", "))
		fmt.Fprintf(&b, "- Allowed role classes: %s\n", strings.Join(data.AutonomousPolicy.AllowedRoleClasses, ", "))
		fmt.Fprintf(&b, "- Budget turns: %d\n\n", data.AutonomousPolicy.BudgetTurns)
	}
	b.WriteString("## Out of scope\n- No autonomous action outside the declared policy envelope.\n- No child-authored spawn or prune authority.\n- No merge, release, destructive filesystem action, external communication, or provider side effect without operator approval.\n\n")
	b.WriteString("## Acceptance\n- Preview is reviewed before any setup mutation.\n- Spawn gates are explicit and durable.\n- Visible lead binding is declared as native /goal when available, otherwise AMQ task + active brief + task store.\n- Tasks, dispatches, review evidence, and final verification are recorded before merge-ready claims.\n")
	return b.String()
}

func defaultGoalTasks(data goalDraftData) []goalTaskPlan {
	if len(data.IssueSources) == 0 {
		return []goalTaskPlan{
			{ID: "t1", Title: "Confirm scope and acceptance from the goal", Assignee: data.Lead},
			{ID: "t2", Title: "Implement the goal against the agreed scope", Assignee: "fullstack", DependsOn: []string{"t1"}},
			{ID: "t3", Title: "Review implementation and test evidence", Assignee: "senior-dev", DependsOn: []string{"t2"}},
		}
	}
	tasks := make([]goalTaskPlan, 0, len(data.IssueSources)+1)
	for i, issue := range data.IssueSources {
		tasks = append(tasks, goalTaskPlan{
			ID:        "t" + strconv.Itoa(i+1),
			Title:     fmt.Sprintf("Resolve #%d: %s", issue.Number, issue.Title),
			Assignee:  "fullstack",
			SourceURL: issue.URL,
		})
	}
	deps := make([]string, 0, len(tasks))
	for _, task := range tasks {
		deps = append(deps, task.ID)
	}
	tasks = append(tasks, goalTaskPlan{
		ID:        "t" + strconv.Itoa(len(tasks)+1),
		Title:     "Milestone integration review and merge-gate evidence",
		Assignee:  "senior-dev",
		DependsOn: deps,
	})
	return tasks
}

func defaultGoalSpawnGates(data goalDraftData) []goalCommandPlan {
	gates := make([]goalCommandPlan, 0, len(data.Roster))
	for _, member := range data.Roster {
		if member.Role == data.Lead {
			continue
		}
		gates = append(gates, goalCommandPlan{
			Title:   "spawn " + member.Role,
			Command: fmt.Sprintf("amq send --to user --thread gate/spawn-%s --kind question --subject %q --body %q", member.Role, "APPROVAL: spawn "+member.Role+" ("+member.Binary+")", "The goal needs "+member.Role+" to "+member.Reason+" Approve?"),
			Reason:  member.Reason,
		})
	}
	return gates
}

func defaultGoalDispatches(data goalDraftData) []goalDispatchPlan {
	dispatches := make([]goalDispatchPlan, 0, len(data.Tasks))
	for _, task := range data.Tasks {
		if task.Assignee == data.Lead {
			continue
		}
		thread := canonicalP2PThread(data.Lead, task.Assignee)
		subject := "Task: " + task.Title
		body := task.Title + "\n\nPush progress, blockers, review requests, and DONE reports to " + data.Lead + " over AMQ. Treat this durable AMQ task as the source of truth."
		if task.SourceURL != "" {
			body += "\n\nSource: " + task.SourceURL
		}
		dispatches = append(dispatches, goalDispatchPlan{
			TaskID:  task.ID,
			Role:    task.Assignee,
			Thread:  thread,
			Subject: subject,
			Body:    body,
			Command: fmt.Sprintf("amq-squad dispatch --profile %s --session %s --role %s --thread %s --kind todo --subject %q --body %q", data.Profile, data.Session, task.Assignee, thread, subject, body),
		})
	}
	return dispatches
}

func defaultGoalMutations(data goalDraftData) []goalCommandPlan {
	roles := make([]string, 0, len(data.Roster))
	binaries := make([]string, 0, len(data.Roster))
	for _, member := range data.Roster {
		roles = append(roles, member.Role)
		binaries = append(binaries, member.Role+"="+member.Binary)
	}
	compositionArgs := ""
	if data.Composition == team.CompositionAutonomous && data.AutonomousPolicy != nil {
		compositionArgs = fmt.Sprintf(" --composition autonomous --max-agents %d --max-total-spawns %d --allowed-roles %s --budget-turns %d",
			data.AutonomousPolicy.MaxActiveAgents,
			data.AutonomousPolicy.MaxTotalSpawns,
			strings.Join(data.AutonomousPolicy.AllowedRoles, ","),
			data.AutonomousPolicy.BudgetTurns,
		)
		if len(data.AutonomousPolicy.AllowedRoleClasses) > 0 {
			compositionArgs += " --allowed-role-classes " + strings.Join(data.AutonomousPolicy.AllowedRoleClasses, ",")
		}
		if data.AutonomousPolicy.IdleReapMinutes > 0 {
			compositionArgs += fmt.Sprintf(" --idle-reap-minutes %d", data.AutonomousPolicy.IdleReapMinutes)
		}
	}
	executionArgs := fmt.Sprintf(" --mode %s", data.Mode)
	if data.ControlRoot != "" {
		executionArgs += " --control-root " + shellQuote(data.ControlRoot)
	}
	// Only emit --target-project-root in the generated start command when it was
	// explicitly PROVIDED (#290). A resolved_unconfirmed proposal or an unresolved
	// target must NOT be carried into actionable start surfaces; omitting it makes
	// the generated global_orchestrator team init fail closed until the operator
	// supplies a confirmed path. default (non-global) omits too and cwd-defaults.
	if data.TargetProjectRootSource == targetRootSourceProvided && data.TargetProjectRoot != "" {
		executionArgs += " --target-project-root " + shellQuote(data.TargetProjectRoot)
	}
	if data.TargetContract != "" {
		executionArgs += " --target-contract " + shellQuote(data.TargetContract)
	}
	leadModeArgs := ""
	if data.LeadMode != "" && data.LeadMode != team.LeadModeBuilder {
		leadModeArgs = " --lead-mode " + data.LeadMode
	}
	mutations := []goalCommandPlan{
		{
			Title:   "initialize profile",
			Command: fmt.Sprintf("amq-squad team init --profile %s --session %s --roles %s --binary %s --orchestrated --lead %s%s%s%s --dry-run", data.Profile, data.Session, strings.Join(roles, ","), strings.Join(binaries, ","), data.Lead, leadModeArgs, executionArgs, compositionArgs),
			Reason:  "Preview the proposed roster and orchestration metadata before writing team config.",
		},
		{
			Title:   "write brief",
			Command: fmt.Sprintf("amq-squad brief seed --profile %s --session %s --seed-from file:<approved-brief.md> --dry-run", data.Profile, data.Session),
			Reason:  "Preview the workstream brief before writing .amq-squad/briefs.",
		},
	}
	for _, task := range data.Tasks {
		cmd := fmt.Sprintf("amq-squad task add --profile %s --session %s --title %q --assign %s", data.Profile, data.Session, task.Title, task.Assignee)
		if len(task.DependsOn) > 0 {
			cmd += " --depends-on " + strings.Join(task.DependsOn, ",")
		}
		mutations = append(mutations, goalCommandPlan{Title: "add " + task.ID, Command: cmd, Reason: "Create the native task-store item after preview approval."})
	}
	mutations = append(mutations, goalVisibilityMutation(data))
	return mutations
}

func goalVisibilityMutation(data goalDraftData) goalCommandPlan {
	command := visibleLeadLaunchCommand(data, false)
	var plan goalCommandPlan
	switch data.Visibility {
	case visibilityDetached:
		plan = goalCommandPlan{
			Title:   "launch detached visible lead",
			Command: command,
			Reason:  "Start the operator-visible lead with the native /goal prompt, then attach/open its pane deliberately before treating the run as observable.",
		}
	case visibilityCurrent:
		plan = goalCommandPlan{
			Title:   "launch visible lead in current pane",
			Command: command,
			Reason:  "Start the visible goal lead from the current operator pane with the native /goal prompt; workers remain gated/internal.",
		}
	case visibilityPlan:
		plan = goalCommandPlan{
			Title:   "preview visible lead launch",
			Command: visibleLeadLaunchCommand(data, true),
			Reason:  "Preview the native /goal lead launch command only; do not open a pane until the operator approves a concrete visibility mode.",
		}
	default:
		plan = goalCommandPlan{
			Title:   "launch visible lead",
			Command: command,
			Reason:  "Run from a visible tmux pane so the lead receives the native /goal prompt; workers are launched later only after their spawn gates are approved.",
		}
	}
	// #291: surface the lead reasoning-effort default as an inert recommendation,
	// since it is intentionally NOT baked into the launch command unless the
	// operator explicitly provided codex args.
	if rec := goalLeadEffortRecommendation(data); rec != "" {
		plan.Reason += " " + rec
	}
	return plan
}

// goalLeadEffortRecommendation returns an inert note recommending the lead's
// reasoning effort when it was seeded as a default (not operator-provided) for a
// Codex lead. Empty when codex args were explicitly provided or the lead is not
// Codex.
func goalLeadEffortRecommendation(data goalDraftData) string {
	if data.codexArgsProvided {
		return ""
	}
	lead := data.Roster[0]
	for _, member := range data.Roster {
		if member.Role == data.Lead {
			lead = member
			break
		}
	}
	if normalizedAgentBinary(lead.Binary) != "codex" || len(lead.CodexArgs) == 0 {
		return ""
	}
	return "Recommended (not applied): add --codex-args=" + joinedAgentArgs(lead.CodexArgs) + " to run the Codex lead at the recommended reasoning effort."
}

func visibleLeadLaunchCommand(data goalDraftData, dryRun bool) string {
	lead := data.Roster[0]
	for _, member := range data.Roster {
		if member.Role == data.Lead {
			lead = member
			break
		}
	}
	args := []string{
		"agent", "up", lead.Binary,
		"--role", lead.Role,
		"--session", data.Session,
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	if root := launchRootForProfile(".", data.Profile, data.Session); root != "" {
		args = append(args, "--root", root)
	}
	args = append(args, "--team-workstream", "--team-home", ".")
	if data.Profile != "" && data.Profile != team.DefaultProfile {
		args = append(args, "--team-profile", data.Profile)
	}
	if lead.Handle != "" {
		args = append(args, "--me", lead.Handle)
	}
	// #291: do NOT bake the seeded default reasoning effort into the actionable
	// launch command. Emit --codex-args only when the operator explicitly provided
	// codex args; otherwise effort stays a recommendation comment so the generated
	// command never silently changes runtime assumptions.
	if data.codexArgsProvided && len(lead.CodexArgs) > 0 && normalizedAgentBinary(lead.Binary) == "codex" {
		args = append(args, "--codex-args="+joinedAgentArgs(lead.CodexArgs))
	}
	if data.OrchestratorPrompt != "" {
		args = append(args, "--", data.OrchestratorPrompt)
	}
	quoted := make([]string, 0, len(args)+1)
	quoted = append(quoted, "amq-squad")
	for _, arg := range args {
		quoted = append(quoted, shellQuote(arg))
	}
	return strings.Join(quoted, " ")
}

func renderGoalOrchestratorPrompt(data goalDraftData) string {
	args := []string{"/goal", "--goal", strconv.Quote(data.Goal), "--session", data.Session, "--profile", data.Profile, "--mode", data.Mode}
	if data.ControlRoot != "" {
		args = append(args, "--control-root", data.ControlRoot)
	}
	// Only carry an explicitly PROVIDED target into the visible-lead /goal prompt
	// (#290); a resolved_unconfirmed/unresolved target must not appear as a real
	// flag in an actionable start surface.
	if data.TargetProjectRootSource == targetRootSourceProvided && data.TargetProjectRoot != "" {
		args = append(args, "--target-project-root", data.TargetProjectRoot)
	}
	if data.TargetContract != "" {
		args = append(args, "--target-contract", data.TargetContract)
	}
	if data.Lead != "" && data.Lead != "cto" {
		args = append(args, "--lead", data.Lead)
	}
	if data.LeadMode != "" && data.LeadMode != team.LeadModeBuilder {
		args = append(args, "--lead-mode", data.LeadMode)
	}
	if data.Repo != "" {
		args = append(args, "--repo", data.Repo)
	}
	if data.Milestone != "" {
		args = append(args, "--milestone", data.Milestone)
	}
	if data.CodexOnly {
		args = append(args, "--codex-only")
	}
	if data.Composition == team.CompositionAutonomous && data.AutonomousPolicy != nil {
		args = append(args, "--composition", "autonomous", "--max-agents", strconv.Itoa(data.AutonomousPolicy.MaxActiveAgents), "--max-total-spawns", strconv.Itoa(data.AutonomousPolicy.MaxTotalSpawns), "--allowed-roles", strings.Join(data.AutonomousPolicy.AllowedRoles, ","), "--budget-turns", strconv.Itoa(data.AutonomousPolicy.BudgetTurns))
	}
	return strings.Join(args, " ")
}

// goalFieldSourceLabel renders the #291 provided/default (or richer #290 target)
// label for a Step 1 field, e.g. " (provided)". Empty when the field is not
// labeled.
func goalFieldSourceLabel(data goalDraftData, field string) string {
	if src, ok := data.FieldSources[field]; ok && src != "" {
		return " (" + src + ")"
	}
	return ""
}

func writeGoalDraftMarkdown(out *os.File, data goalDraftData) {
	fmt.Fprintln(out, "# amq-squad goal draft")
	fmt.Fprintf(out, "# preview_only: %t\n", data.PreviewOnly)
	fmt.Fprintf(out, "# composition: %s%s\n", data.Composition, goalFieldSourceLabel(data, "composition"))
	fmt.Fprintf(out, "# mode: %s%s\n", data.Mode, goalFieldSourceLabel(data, "mode"))
	fmt.Fprintf(out, "# visibility: %s%s\n", data.Visibility, goalFieldSourceLabel(data, "visibility"))
	fmt.Fprintf(out, "# session: %s%s\n", data.Session, goalFieldSourceLabel(data, "session"))
	fmt.Fprintf(out, "# profile: %s%s\n", data.Profile, goalFieldSourceLabel(data, "profile"))
	fmt.Fprintf(out, "# lead: %s%s\n", data.Lead, goalFieldSourceLabel(data, "lead"))
	fmt.Fprintf(out, "# lead_mode: %s%s\n", data.LeadMode, goalFieldSourceLabel(data, "lead_mode"))
	fmt.Fprintf(out, "# namespace: %s\n", data.Namespace.ID)
	if data.ControlRoot != "" {
		fmt.Fprintf(out, "# control_root: %s%s\n", data.ControlRoot, goalFieldSourceLabel(data, "control_root"))
	}
	fmt.Fprintf(out, "# target_project_root: %s\n", goalTargetProjectRootLine(data))
	if data.Execution.MutableActor != "" {
		fmt.Fprintf(out, "# mutable_actor: %s\n", data.Execution.MutableActor)
	}
	fmt.Fprintf(out, "# implementation_allowed: %t\n", data.Execution.ImplementationAllowed)
	if data.Execution.ModeError != "" {
		fmt.Fprintf(out, "# mode_error: %s\n", data.Execution.ModeError)
	}
	if data.Execution.VersionCompatibility.Detail != "" {
		fmt.Fprintf(out, "# version_compatibility: %s\n", data.Execution.VersionCompatibility.Detail)
	}
	if data.Repo != "" {
		fmt.Fprintf(out, "# repo: %s\n", data.Repo)
	}
	if data.Milestone != "" {
		fmt.Fprintf(out, "# milestone: %s\n", data.Milestone)
	}
	if data.AutonomousPolicy != nil {
		fmt.Fprintf(out, "# autonomous.max_active_agents: %d\n", data.AutonomousPolicy.MaxActiveAgents)
		fmt.Fprintf(out, "# autonomous.max_total_spawns: %d\n", data.AutonomousPolicy.MaxTotalSpawns)
		fmt.Fprintf(out, "# autonomous.budget_turns: %d\n", data.AutonomousPolicy.BudgetTurns)
	}
	fmt.Fprintln(out)
	if len(data.Steps) > 0 {
		fmt.Fprintln(out, "## Operator Steps")
		for _, s := range data.Steps {
			fmt.Fprintf(out, "### Step %d — %s\n", s.Number, s.Title)
			if s.JustHappened != "" {
				fmt.Fprintf(out, "- Just happened: %s\n", s.JustHappened)
			}
			if s.AboutToHappen != "" {
				fmt.Fprintf(out, "- About to happen: %s\n", s.AboutToHappen)
			}
			if s.Approving != "" {
				fmt.Fprintf(out, "- You are approving: %s\n", s.Approving)
			}
			if s.NextGate != "" {
				fmt.Fprintf(out, "- Next gate: %s\n", s.NextGate)
			}
		}
		fmt.Fprintln(out)
	}
	fmt.Fprintln(out, "## Brief Skeleton")
	fmt.Fprintln(out)
	fmt.Fprint(out, data.BriefSkeleton)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Roster")
	for _, member := range data.Roster {
		fmt.Fprintf(out, "- %s (%s): %s\n", member.Role, member.Binary, member.Reason)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Execution Boundary")
	fmt.Fprintf(out, "- Mode: %s\n", data.Execution.Mode)
	fmt.Fprintf(out, "- Control root: %s\n", data.Execution.ControlRoot)
	fmt.Fprintf(out, "- Target project root: %s\n", goalTargetProjectRootLine(data))
	fmt.Fprintf(out, "- Visible lead: %s\n", data.Execution.VisibleLead)
	fmt.Fprintf(out, "- Lead mode: %s\n", data.Execution.LeadMode)
	fmt.Fprintf(out, "- Visible team members: %s\n", strings.Join(data.Execution.VisibleTeamMembers, ", "))
	fmt.Fprintf(out, "- Mutable actor: %s\n", data.Execution.MutableActor)
	fmt.Fprintf(out, "- Implementation allowed: %t\n", data.Execution.ImplementationAllowed)
	fmt.Fprintf(out, "- Boundary: %s\n", data.Execution.Boundary)
	if data.Execution.ModeError != "" {
		fmt.Fprintf(out, "- Mode error: %s\n", data.Execution.ModeError)
	}
	fmt.Fprintf(out, "- Version compatibility: %s\n", data.Execution.VersionCompatibility.Detail)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Task Plan")
	for _, task := range data.Tasks {
		deps := ""
		if len(task.DependsOn) > 0 {
			deps = " after " + strings.Join(task.DependsOn, ",")
		}
		fmt.Fprintf(out, "- %s [%s%s]: %s\n", task.ID, task.Assignee, deps, task.Title)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Spawn Gates")
	for _, gate := range data.SpawnGates {
		fmt.Fprintf(out, "- `%s`\n", gate.Command)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Proposed Mutations")
	for _, mutation := range data.ApplyableMutations {
		fmt.Fprintf(out, "- `%s`\n", mutation.Command)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Dispatches")
	for _, dispatch := range data.Dispatches {
		fmt.Fprintf(out, "- `%s`\n", dispatch.Command)
	}
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Orchestrator Prompt")
	fmt.Fprintf(out, "`%s`\n", data.OrchestratorPrompt)
	fmt.Fprintln(out)
	fmt.Fprintln(out, "## Notes")
	for _, note := range data.Notes {
		fmt.Fprintf(out, "- %s\n", note)
	}
}
