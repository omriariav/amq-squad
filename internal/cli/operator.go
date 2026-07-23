package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	defaultOperatorPollLeaseTTL  = 2 * time.Minute
	defaultOperatorWatchInterval = 5 * time.Second
)

type operatorExecution struct {
	ProjectDir      string
	Profile         string
	Session         string
	BaseRoot        string
	ReadOnly        bool
	Owner           string
	OwnerID         string
	LeaseTTL        time.Duration
	Force           bool
	ForceReason     string
	JSON            bool
	Out             io.Writer
	ResolveBaseRoot func(projectDir string) (string, error)
	Probe           state.Probe
	Now             func() time.Time
}

type operatorWatchExecution struct {
	operatorExecution
	Interval time.Duration
	Once     bool
	Sleep    func(time.Duration) bool
}

var operatorWatchNotificationPump = deliverOperatorWatchNotifications
var resolveOperatorAnswerContext = resolveScopedCommandContext

type operatorStatusEnvelopeData struct {
	ProjectDir       string                       `json:"project_dir"`
	BaseRoot         string                       `json:"base_root,omitempty"`
	Profile          string                       `json:"profile"`
	Session          string                       `json:"session"`
	Namespace        squadnamespace.Ref           `json:"namespace"`
	ReadOnly         bool                         `json:"readonly"`
	Operator         statusOperatorView           `json:"operator"`
	OperatorDelivery operatorDeliveryData         `json:"operator_delivery"`
	OperatorLoop     operatorLoopStatus           `json:"operator_loop"`
	Attention        []operatorAttention          `json:"attention,omitempty"`
	OperatorGates    bool                         `json:"operator_gates"`
	Claimed          *bool                        `json:"claimed,omitempty"`
	Conflict         *operatorPollConflict        `json:"conflict,omitempty"`
	Watch            *operatorWatchMeta           `json:"watch,omitempty"`
	Message          string                       `json:"message,omitempty"`
	Notifications    *operatorNotificationSummary `json:"notifications,omitempty"`
	operatorCursor   string
}
type operatorNotificationSummary struct {
	Selected   int `json:"selected"`
	Delivered  int `json:"delivered"`
	Failed     int `json:"failed"`
	Suppressed int `json:"suppressed"`
}

type operatorWatchMeta struct {
	Interval string    `json:"interval"`
	Tick     int       `json:"tick"`
	At       time.Time `json:"at"`
}

type operatorPollConflict struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	Owner          string `json:"owner"`
	OwnerID        string `json:"owner_id"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	LastPollAt     string `json:"last_poll_at,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
}

type operatorLoopStatus struct {
	Mode              string `json:"mode"`
	PollRequired      bool   `json:"poll_required"`
	State             string `json:"state"`
	Owner             string `json:"owner"`
	OwnerID           string `json:"owner_id,omitempty"`
	LeaseTTL          string `json:"lease_ttl,omitempty"`
	LeaseExpiresAt    string `json:"lease_expires_at,omitempty"`
	LastPollAt        string `json:"last_poll_at,omitempty"`
	Cursor            string `json:"cursor,omitempty"`
	Backlog           int    `json:"backlog"`
	GatesOpen         int    `json:"gates_open"`
	DirectivesUnacked int    `json:"directives_unacked"`
	DegradedReason    string `json:"degraded_reason,omitempty"`
}

type operatorLoopLeaseFile struct {
	SchemaVersion  int       `json:"schema_version"`
	Profile        string    `json:"profile"`
	Session        string    `json:"session"`
	NamespaceID    string    `json:"namespace_id"`
	Mode           string    `json:"mode"`
	Owner          string    `json:"owner"`
	OwnerID        string    `json:"owner_id"`
	LeaseTTL       string    `json:"lease_ttl"`
	LeaseExpiresAt time.Time `json:"lease_expires_at"`
	LastPollAt     time.Time `json:"last_poll_at"`
	Cursor         string    `json:"cursor,omitempty"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type operatorLoopForceAuditRecord struct {
	At                   time.Time `json:"at"`
	ProjectDir           string    `json:"project_dir"`
	Profile              string    `json:"profile"`
	Session              string    `json:"session"`
	NamespaceID          string    `json:"namespace_id"`
	ActorOwner           string    `json:"actor_owner"`
	ActorOwnerID         string    `json:"actor_owner_id"`
	PreviousOwner        string    `json:"previous_owner"`
	PreviousOwnerID      string    `json:"previous_owner_id"`
	PreviousLeaseExpires time.Time `json:"previous_lease_expires_at"`
	Reason               string    `json:"reason"`
}

type operatorPollLeaseConflictError struct {
	Lease operatorLoopLeaseFile
}

func (e *operatorPollLeaseConflictError) Error() string {
	return fmt.Sprintf("operator poll lease already held by %s until %s; pass --force --reason <why> to steal it", e.Lease.OwnerID, e.Lease.LeaseExpiresAt.UTC().Format(time.RFC3339))
}

func runOperator(args []string) error {
	if len(args) == 0 {
		printOperatorUsage()
		return nil
	}
	switch args[0] {
	case "-h", "--help":
		printOperatorUsage()
		return nil
	case "answer":
		return runOperatorAnswer(args[1:])
	case "self-approve":
		return runOperatorSelfApprove(args[1:])
	case "send":
		return runOperatorSend(args[1:])
	case "directive":
		return runOperatorDirective(args[1:])
	case "poll":
		return runOperatorPoll(args[1:])
	case "status":
		return runOperatorStatus(args[1:])
	case "watch":
		return runOperatorWatch(args[1:])
	default:
		return usageErrorf("unknown 'operator' subcommand %q. Run 'amq-squad operator --help' for available subcommands.", args[0])
	}
}

func printOperatorUsage() {
	fmt.Fprint(os.Stderr, `amq-squad operator - inspect and act as the configured operator participant

Usage:
  amq-squad operator <subcommand> [options]

Subcommands:
  answer    answer an operator gate on gate/<topic>
  send      send a receipted AMQ message to any configured agent
  directive send a DIRECTIVE message to a visible lead
  poll     read the operator polling workload and claim a poll lease
  status   show the operator polling contract and inbox state
  watch    run the reference operator polling loop

Run 'amq-squad operator <subcommand> --help' for subcommand options and flags.

Examples:
  amq-squad operator answer --gate release --approved
  amq-squad operator send --to qa --subject "Review" --body "Please review." --yes
  amq-squad operator directive --to cto --subject "ship it" --body "Proceed after checks."
  amq-squad operator status --json
  amq-squad operator poll --readonly --json
  amq-squad operator watch --once
`)
}

func runOperatorAnswer(args []string) error {
	fs := flag.NewFlagSet("operator answer", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to answer in")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	gateFlag := fs.String("gate", "", "gate topic, with or without the gate/ prefix")
	toFlag := fs.String("to", "", "lead or agent handle that asked the gate")
	approved := fs.Bool("approved", false, "send APPROVED answer")
	denied := fs.Bool("denied", false, "send DENIED answer")
	reasonFlag := fs.String("reason", "", "optional reason to include in the answer body")
	kindFlag := fs.String("kind", "", "structured gate kind for high-risk authorization")
	actionFlag := fs.String("action", "", "structured normalized action")
	targetFlag := fs.String("target", "", "exact case-sensitive target")
	evidenceFlag := fs.String("evidence", "", "optional strict preflight evidence")
	listKinds := fs.Bool("list-kinds", false, "list the shared action catalog without resolving project context")
	overrideNamespaceConflict := fs.Bool("override-namespace-conflict", false, "acknowledge a collided namespace and continue, writing an audit record")
	overrideNamespaceReason := fs.String("namespace-reason", "", "required reason when --override-namespace-conflict is set")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad operator answer - answer an operator gate

Usage:
  amq-squad operator answer [--project DIR] [--profile NAME] [--session S] --gate TOPIC [--to HANDLE] (--approved|--denied) [--reason TEXT] [--override-namespace-conflict --namespace-reason WHY] [--json]

Sends an AMQ answer from the configured operator handle on gate/<topic>. This
first-class command avoids hand-writing the operator protocol. Typed requests
auto-bind the exact gate owner; --to is required only for legacy/raw gates.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("operator answer takes no positional arguments; got %q", fs.Arg(0))
	}
	if *listKinds {
		return printActionKinds(*jsonOut)
	}
	if *approved == *denied {
		return usageErrorf("operator answer requires exactly one of --approved or --denied")
	}
	for name, value := range map[string]string{"kind": *kindFlag, "action": *actionFlag, "target": *targetFlag} {
		if value != "" {
			if err := operatorauth.ValidateCanonicalSingleLineField(name, value, true); err != nil {
				return usageErrorf("operator answer: %v", err)
			}
		}
	}
	if *kindFlag != "" && *actionFlag != "" {
		capability, err := operatorauth.ValidateGateAction(*kindFlag, *actionFlag)
		if err != nil || capability.GateKind != *kindFlag || capability.Action != *actionFlag {
			return usageErrorf("operator answer requires an exact canonical --kind/--action pair")
		}
	}
	topic, err := canonicalGateTopic(*gateFlag)
	if err != nil {
		return usageErrorf("operator answer: %v", err)
	}
	to := strings.TrimSpace(*toFlag)
	resolveAnswerContext := func() (contextResolution, error) {
		return resolveOperatorAnswerContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	}
	initialContext, err := resolveAnswerContext()
	if err != nil {
		return err
	}
	projectDir, profile, t, workstream, operatorHandle, err := resolveOperatorCommandContext(*projectFlag, *profileFlag, *sessionFlag, flagWasSet(fs, "project"), flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	override := namespaceConflictOverrideOptions{
		Allowed: *overrideNamespaceConflict,
		Reason:  *overrideNamespaceReason,
	}
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(projectDir, profile, workstream), operatorHandle)
	if err != nil {
		return err
	}
	admittedContext, admission, err := acquireRevalidatedContextWriter(initialContext, false, resolveAnswerContext)
	if err != nil {
		return err
	}
	defer admission.close()
	currentProject, currentProfile, currentTeam, currentWorkstream, currentOperator, err := resolveOperatorCommandContext(*projectFlag, *profileFlag, *sessionFlag, flagWasSet(fs, "project"), flagWasSet(fs, "session"))
	if err != nil {
		return fmt.Errorf("operator answer refused: context re-resolution under admission failed: %w", err)
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(currentProject, currentProfile, currentWorkstream), currentOperator)
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("operator answer", initialIdentity, currentIdentity); err != nil {
		return err
	}
	projectDir, profile, t, workstream, operatorHandle = currentProject, currentProfile, currentTeam, currentWorkstream, currentOperator
	if err := ensureNoNamespaceConflictWithOverride("operator answer", projectDir, profile, workstream, flagWasSet(fs, "profile"), override); err != nil {
		return err
	}
	decision := "APPROVED"
	if *denied {
		decision = "DENIED"
	}
	subject := decision + ": " + strings.TrimPrefix(topic, "gate/")
	reason := *reasonFlag
	thread := topic
	selected := selectedReleaseContext(admittedContext)
	selectedQuestion, err := latestGateQuestionInSelectedContext(selected, topic, time.Now)
	if err != nil {
		return usageErrorf("operator answer release-domain inspection failed: %v", err)
	}
	classification, err := classifyCLIReleaseQuestion(selected, selectedQuestion)
	if err != nil {
		return usageErrorf("operator answer release-domain inspection failed: %v", err)
	}
	if classification.Disposition == cliReleaseDomainReleaseOwned && !classification.Eligible {
		return usageErrorf("operator answer refused release-owned gate without an exact eligible claim: %s", classification.Reason)
	}
	if classification.Disposition == cliReleaseDomainReleaseOwned {
		if err := validateReleaseOperatorChildIdentity(classification); err != nil {
			return usageErrorf("operator answer refused release-owned gate identity: %v", err)
		}
	}
	if classification.Disposition != cliReleaseDomainOrdinary && classification.Disposition != cliReleaseDomainReleaseOwned {
		return usageErrorf("operator answer refused gate with unknown release-domain ownership")
	}
	if !selectedQuestion.AuthorizationRequestPresent {
		if classification.Disposition != cliReleaseDomainOrdinary {
			return usageErrorf("release-owned gate question is not a valid typed authorization request")
		}
		if flagWasSet(fs, "kind") || flagWasSet(fs, "action") || flagWasSet(fs, "target") || flagWasSet(fs, "evidence") {
			return usageErrorf("legacy/raw gate answers do not accept structured authorization overrides")
		}
		if to == "" {
			return usageErrorf("operator answer requires --to <handle> for a legacy/raw gate")
		}
		if err := ensureOperatorCommandTarget(t, to, "operator answer"); err != nil {
			return err
		}
		return sendOperatorAMQ(operatorSendOptions{
			Command: "operator answer", Project: projectDir, Profile: profile, Session: workstream,
			From: operatorHandle, To: to, Thread: thread, Kind: string(state.KindAnswer), Subject: subject, Body: strings.TrimSpace(reason),
			JSON: *jsonOut, Out: os.Stdout,
			FollowUp: "amq-squad operator status --project " + shellQuote(projectDir) + operatorProfileArg(profile) + " --session " + shellQuote(workstream) + " --json",
		})
	}
	if !selectedQuestion.AuthorizationRequestValid || selectedQuestion.AuthorizationRequest == nil {
		return usageErrorf("latest typed gate request is malformed and blocks legacy fallback: %s", selectedQuestion.AuthorizationRequestError)
	}
	if err := validateTypedAnswerReason(reason); err != nil {
		return usageErrorf("operator answer: %v", err)
	}
	question, err := operatorAnswerQuestionInSelectedContext(selected, selectedQuestion, topic, *kindFlag, *actionFlag, *targetFlag, t, operatorHandle)
	if err != nil {
		return err
	}
	request := *question.AuthorizationRequest
	if to == "" {
		to = question.From
	} else if to != question.From {
		return usageErrorf("operator answer --to %q does not exactly match latest typed gate owner %q", to, question.From)
	}
	if err := ensureOperatorCommandTarget(t, to, "operator answer"); err != nil {
		return err
	}
	approval := operatorauth.ApprovalContext{
		SchemaVersion: operatorauth.ApprovalSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Source: "human", SelfApproved: false, GateKind: request.GateKind, Action: request.Action, Target: request.Target, Note: request.Note,
		QuestionMessageID: question.ID, AnsweredByRole: "operator", AnsweredByHandle: operatorHandle, VerifiedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := operatorauth.ValidateApproval(approval); err != nil {
		return usageErrorf("operator answer: %v", err)
	}
	if strings.TrimSpace(*evidenceFlag) != "" {
		b, err := os.ReadFile(*evidenceFlag)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		approval.PreflightPath = *evidenceFlag
		approval.PreflightSHA256 = fmt.Sprintf("sha256:%x", sum)
		approval.PreflightKind = "provided"
	}
	context := map[string]any{"approval": approval}
	body := fmt.Sprintf("Gate-Kind: %s\nAction: %s\nTarget: %s", approval.GateKind, approval.Action, approval.Target)
	if approval.Note != "" {
		body += "\nNote: " + approval.Note
	}
	if reason != "" {
		body += "\nReason: " + reason
	}
	onSent := func(answerID string) error {
		receipt := operatorauth.Receipt{SchemaVersion: operatorauth.ReceiptSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Gate: topic, GateKind: approval.GateKind, Action: approval.Action, Target: approval.Target, Note: approval.Note, Decision: strings.ToLower(decision), ApprovalSource: "human", QuestionMessageID: question.ID, AnswerMessageID: answerID, AnsweredBy: operatorHandle, Preflight: operatorauth.PreflightReceipt{Kind: approval.PreflightKind, SHA256: approval.PreflightSHA256, Path: approval.PreflightPath, OK: approval.PreflightSHA256 != ""}}
		return writeSelfApprovalReceipt(projectDir, profile, workstream, topic, answerID, receipt)
	}
	sendOptions := operatorSendOptions{
		Command:  "operator answer",
		Project:  projectDir,
		Profile:  profile,
		Session:  workstream,
		From:     operatorHandle,
		To:       to,
		Thread:   thread,
		Kind:     string(state.KindAnswer),
		Subject:  subject,
		Body:     body,
		Context:  context,
		OnSent:   onSent,
		JSON:     *jsonOut,
		Out:      os.Stdout,
		FollowUp: "amq-squad operator status --project " + shellQuote(projectDir) + operatorProfileArg(profile) + " --session " + shellQuote(workstream) + " --json",
	}
	if classification.Disposition == cliReleaseDomainReleaseOwned {
		exactContext, exactErr := exactOperatorAMQContext(admittedContext, operatorHandle)
		if exactErr != nil {
			return usageErrorf("operator answer exact AMQ context: %v", exactErr)
		}
		sendOptions.ExactContext = &exactContext
		frozen, freezeErr := freezeReleaseOperatorAnswer(sendOptions, question, approval)
		if freezeErr != nil {
			return usageErrorf("operator answer frozen release envelope: %v", freezeErr)
		}
		guarded, guardErr := classification.NewGuardedUse()
		if guardErr != nil {
			return usageErrorf("operator answer release guard: %v", guardErr)
		}
		boundary, boundaryErr := releaseOperatorInvocationBoundary(guarded, frozen, func() (releaseOperatorAnswerEnvelope, error) {
			return freezeReleaseOperatorAnswer(sendOptions, question, approval)
		})
		if boundaryErr != nil {
			return usageErrorf("operator answer release invocation boundary: %v", boundaryErr)
		}
		sendOptions.Invocation = boundary
	}
	return sendOperatorAMQ(sendOptions)
}

func runOperatorDirective(args []string) error {
	fs := flag.NewFlagSet("operator directive", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to send in")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	toFlag := fs.String("to", "", "visible lead handle to receive the directive")
	subjectFlag := fs.String("subject", "", "directive subject text, without the DIRECTIVE: prefix")
	bodyFlag := fs.String("body", "", "directive body")
	bodyFileFlag := fs.String("body-file", "", "read directive body from file ('-' for stdin)")
	overrideNamespaceConflict := fs.Bool("override-namespace-conflict", false, "acknowledge a collided namespace and continue, writing an audit record")
	overrideNamespaceReason := fs.String("reason", "", "required reason when --override-namespace-conflict is set")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad operator directive - send a directive to a visible lead

Usage:
  amq-squad operator directive [--project DIR] [--profile NAME] [--session S] --to HANDLE --subject TEXT (--body TEXT | --body-file FILE) [--override-namespace-conflict --reason WHY] [--json]

Sends a DIRECTIVE todo from the configured operator handle on the canonical
p2p/<lead>__<operator> thread. Directives are steering data; they do not answer
or clear gate/<topic> threads.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	to := strings.TrimSpace(*toFlag)
	if to == "" {
		return usageErrorf("operator directive requires --to <lead-handle>")
	}
	subjectText := strings.TrimSpace(*subjectFlag)
	if subjectText == "" {
		return usageErrorf("operator directive requires --subject TEXT")
	}
	body, err := readPromptBody(*bodyFlag, *bodyFileFlag, flagWasSet(fs, "body"), flagWasSet(fs, "body-file"), os.Stdin, stdinIsInteractive())
	if err != nil {
		return err
	}
	projectDir, profile, t, workstream, operatorHandle, err := resolveOperatorCommandContext(*projectFlag, *profileFlag, *sessionFlag, flagWasSet(fs, "project"), flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	override := namespaceConflictOverrideOptions{
		Allowed: *overrideNamespaceConflict,
		Reason:  *overrideNamespaceReason,
	}
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(projectDir, profile, workstream), operatorHandle)
	if err != nil {
		return err
	}
	admission, err := acquireNamespaceWriterAdmission(projectDir, profile, workstream)
	if err != nil {
		return err
	}
	defer admission.close()
	currentProject, currentProfile, currentTeam, currentWorkstream, currentOperator, err := resolveOperatorCommandContext(*projectFlag, *profileFlag, *sessionFlag, flagWasSet(fs, "project"), flagWasSet(fs, "session"))
	if err != nil {
		return fmt.Errorf("operator directive refused: context re-resolution under admission failed: %w", err)
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(currentProject, currentProfile, currentWorkstream), currentOperator)
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("operator directive", initialIdentity, currentIdentity); err != nil {
		return err
	}
	projectDir, profile, t, workstream, operatorHandle = currentProject, currentProfile, currentTeam, currentWorkstream, currentOperator
	if err := ensureNoNamespaceConflictWithOverride("operator directive", projectDir, profile, workstream, flagWasSet(fs, "profile"), override); err != nil {
		return err
	}
	if err := ensureOperatorCommandTarget(t, to, "operator directive"); err != nil {
		return err
	}
	thread := canonicalP2PThread(to, operatorHandle)
	subject := "DIRECTIVE: " + strings.TrimPrefix(subjectText, "DIRECTIVE: ")
	return sendOperatorAMQ(operatorSendOptions{
		Command:  "operator directive",
		Project:  projectDir,
		Profile:  profile,
		Session:  workstream,
		From:     operatorHandle,
		To:       to,
		Thread:   thread,
		Kind:     string(state.KindTodo),
		Subject:  subject,
		Body:     body,
		JSON:     *jsonOut,
		Out:      os.Stdout,
		FollowUp: "amq-squad operator status --project " + shellQuote(projectDir) + operatorProfileArg(profile) + " --session " + shellQuote(workstream) + " --json",
	})
}

type operatorSendOptions struct {
	Command      string
	Project      string
	Profile      string
	Session      string
	From         string
	To           string
	Thread       string
	Kind         string
	Subject      string
	Body         string
	JSON         bool
	Out          io.Writer
	FollowUp     string
	Context      map[string]any
	OnSent       func(messageID string) error
	ExactContext *amqContext
	Invocation   durableInvocationBoundary
}

type releaseOperatorAnswerEnvelope struct {
	Command     string
	Project     string
	Profile     string
	Session     string
	From        string
	To          string
	Thread      string
	Kind        string
	Subject     string
	Body        string
	ContextJSON string
	Question    state.Message
	Approval    operatorauth.ApprovalContext
	AMQRoot     string
	AMQBaseRoot string
	AMQPinMode  amqPinMode
	Generation  string
}

func validateReleaseOperatorChildIdentity(classification cliReleaseDomainClassification) error {
	if !classification.Eligible || classification.Claim == nil || classification.Marker.Role != classification.Claim.Role || classification.Marker.Ordinal != classification.Claim.Ordinal {
		return fmt.Errorf("exact eligible marker and claim identity are required")
	}
	switch classification.Claim.Role {
	case operatorauth.ReleaseChildTag:
		if classification.Claim.Ordinal != 0 || classification.Marker.GateKind != operatorauth.GateTag || classification.Claim.Action != "tag" {
			return fmt.Errorf("tag child identity is not isolated at ordinal zero")
		}
	case operatorauth.ReleaseChildGitHubRelease:
		if classification.Claim.Ordinal != 1 || classification.Marker.GateKind != operatorauth.GateRelease || classification.Claim.Action != "github_release" {
			return fmt.Errorf("github_release child identity is not isolated at ordinal one")
		}
	default:
		return fmt.Errorf("unsupported release child role %q", classification.Claim.Role)
	}
	return nil
}

func operatorAnswerQuestionInSelectedContext(selected cliReleaseSelectedContext, expected state.Message, gate, kind, action, target string, cfg team.Team, operatorHandle string) (state.Message, error) {
	if err := validateCLIReleaseSelectedContext(selected); err != nil {
		return state.Message{}, err
	}
	if expected.Thread != gate || expected.RawThread != gate {
		return state.Message{}, usageErrorf("latest selected gate question does not have the exact raw gate binding")
	}
	if !expected.AuthorizationRequestPresent || !expected.AuthorizationRequestValid || expected.AuthorizationRequest == nil {
		return state.Message{}, usageErrorf("latest selected gate question is not a valid typed authorization request")
	}
	request := *expected.AuthorizationRequest
	if request.Gate != gate || request.Thread != gate {
		return state.Message{}, usageErrorf("latest selected gate request does not have the exact gate owner binding")
	}
	wantNamespace := operatorauth.NamespaceBinding{
		ProjectDir: selected.ProjectDir, Profile: selected.Profile, Session: selected.Session,
		NamespaceID: squadnamespace.ID(selected.Profile, selected.Session), Generation: selected.NamespaceGeneration,
	}
	if request.Namespace != wantNamespace {
		return state.Message{}, usageErrorf("latest selected gate request namespace does not match admitted context")
	}
	if team.EffectiveOperator(cfg).Handle != operatorHandle {
		return state.Message{}, usageErrorf("latest selected gate operator identity changed")
	}
	if err := validateTypedQuestionRouting(cfg, selected.Session, operatorHandle, expected); err != nil {
		return state.Message{}, usageErrorf("latest selected typed gate routing: %v", err)
	}
	if err := validateTypedAuthorityBody(expected, request); err != nil {
		return state.Message{}, usageErrorf("latest selected gate question does not have the exact Gate-Kind/Action/Target binding")
	}
	if kind != "" && kind != request.GateKind || action != "" && action != request.Action || target != "" && target != request.Target {
		return state.Message{}, usageErrorf("gate override does not exactly match latest typed authorization request")
	}
	return cloneReleaseStateMessage(expected), nil
}

func exactOperatorAMQContext(admitted contextResolution, operatorHandle string) (amqContext, error) {
	selected := selectedReleaseContext(admitted)
	if err := validateCLIReleaseSelectedContext(selected); err != nil {
		return amqContext{}, err
	}
	pinMode := amqPinSessionful
	if admitted.PinMode == "exact_root" {
		pinMode = amqPinExactRoot
	}
	env := amqEnv{
		Root: selected.SessionRoot, BaseRoot: selected.BaseRoot, SessionName: selected.Session,
		Me: operatorHandle,
	}
	return amqContext{
		ProjectDir: selected.ProjectDir, Profile: selected.Profile, Env: env,
		Root: selected.SessionRoot, Me: operatorHandle, Session: selected.Session,
		PinMode: pinMode, NamespaceGeneration: selected.NamespaceGeneration,
	}, nil
}

func validateExactOperatorAMQContext(ctx amqContext, o operatorSendOptions) error {
	if ctx.ProjectDir != o.Project || !squadnamespace.ProfilesEqual(ctx.Profile, o.Profile) || ctx.Session != o.Session || ctx.Me != o.From {
		return fmt.Errorf("exact context identity does not match operator send tuple")
	}
	if !filepath.IsAbs(ctx.Root) || filepath.Clean(ctx.Root) != ctx.Root || ctx.Root == string(filepath.Separator) {
		return fmt.Errorf("exact context root must be absolute, clean, and non-root")
	}
	if ctx.Env.Root != ctx.Root || strings.TrimSpace(ctx.NamespaceGeneration) == "" {
		return fmt.Errorf("exact context root or namespace generation is incomplete")
	}
	if ctx.PinMode == amqPinSessionful {
		if !filepath.IsAbs(ctx.Env.BaseRoot) || filepath.Clean(ctx.Env.BaseRoot) != ctx.Env.BaseRoot || ctx.Env.SessionName != ctx.Session {
			return fmt.Errorf("exact sessionful context has inconsistent base root or session")
		}
	} else if ctx.PinMode != amqPinExactRoot {
		return fmt.Errorf("exact context has invalid pin mode")
	}
	return nil
}

func freezeReleaseOperatorAnswer(o operatorSendOptions, question state.Message, approval operatorauth.ApprovalContext) (releaseOperatorAnswerEnvelope, error) {
	if o.ExactContext == nil {
		return releaseOperatorAnswerEnvelope{}, fmt.Errorf("release answer requires an exact admitted AMQ context")
	}
	if o.Kind != string(state.KindAnswer) || o.Thread == "" || o.Thread != question.Thread || question.RawThread != o.Thread || o.From == "" || o.To == "" {
		return releaseOperatorAnswerEnvelope{}, fmt.Errorf("release answer routing is incomplete or not exact")
	}
	if approval.QuestionMessageID != question.ID || approval.AnsweredByHandle != o.From || approval.AnsweredByRole != "operator" {
		return releaseOperatorAnswerEnvelope{}, fmt.Errorf("release answer approval does not match frozen question or operator")
	}
	if err := operatorauth.ValidateApproval(approval); err != nil {
		return releaseOperatorAnswerEnvelope{}, err
	}
	wantContext, err := json.Marshal(map[string]any{"approval": approval})
	if err != nil {
		return releaseOperatorAnswerEnvelope{}, err
	}
	contextJSON, err := json.Marshal(o.Context)
	if err != nil {
		return releaseOperatorAnswerEnvelope{}, err
	}
	if !bytes.Equal(contextJSON, wantContext) {
		return releaseOperatorAnswerEnvelope{}, fmt.Errorf("release answer context is not the complete frozen approval context")
	}
	return releaseOperatorAnswerEnvelope{
		Command: o.Command, Project: o.Project, Profile: o.Profile, Session: o.Session,
		From: o.From, To: o.To, Thread: o.Thread, Kind: o.Kind, Subject: o.Subject, Body: o.Body,
		ContextJSON: string(contextJSON), Question: cloneReleaseStateMessage(question), Approval: approval,
		AMQRoot: o.ExactContext.Root, AMQBaseRoot: o.ExactContext.Env.BaseRoot,
		AMQPinMode: o.ExactContext.PinMode, Generation: o.ExactContext.NamespaceGeneration,
	}, nil
}

func releaseOperatorAnswerEnvelopeEqual(a, b releaseOperatorAnswerEnvelope) bool {
	return a.Command == b.Command && a.Project == b.Project && squadnamespace.ProfilesEqual(a.Profile, b.Profile) &&
		a.Session == b.Session && a.From == b.From && a.To == b.To && a.Thread == b.Thread && a.Kind == b.Kind &&
		a.Subject == b.Subject && a.Body == b.Body && a.ContextJSON == b.ContextJSON && a.Approval == b.Approval &&
		a.AMQRoot == b.AMQRoot && a.AMQBaseRoot == b.AMQBaseRoot && a.AMQPinMode == b.AMQPinMode && a.Generation == b.Generation &&
		securityMessageEqual(a.Question, b.Question)
}

func releaseOperatorInvocationBoundary(guarded *cliReleaseGuardedUse, frozen releaseOperatorAnswerEnvelope, current func() (releaseOperatorAnswerEnvelope, error)) (durableInvocationBoundary, error) {
	if guarded == nil || current == nil {
		return durableInvocationBoundary{}, fmt.Errorf("release guard and frozen-envelope reader are required")
	}
	return newDurableInvocationBoundary(func(invoke func() error) (durableInvocationResult, error) {
		var result durableInvocationResult
		err := guarded.Run(func(observation cliReleaseGuardObservation) error {
			live, err := current()
			if err != nil {
				return err
			}
			if !releaseOperatorAnswerEnvelopeEqual(frozen, live) {
				return fmt.Errorf("release operator answer changed after it was frozen")
			}
			if !securityMessageEqual(observation.Question(), frozen.Question) {
				return fmt.Errorf("release operator answer question changed inside guarded use")
			}
			existingID, reconciled, err := inspectReleaseOperatorAnswerReplay(observation, frozen)
			if err != nil {
				return err
			}
			if reconciled {
				result, err = newDurableReconciledExistingResult(existingID)
				return err
			}
			if err := invoke(); err != nil {
				return err
			}
			result = newDurableInvokedResult()
			return nil
		})
		return result, err
	})
}

func inspectReleaseOperatorAnswerReplay(observation cliReleaseGuardObservation, frozen releaseOperatorAnswerEnvelope) (string, bool, error) {
	messages, conflict := dedupeSecurityMessages(observation.Messages())
	if conflict {
		return "", false, fmt.Errorf("release operator replay inspection found conflicting mailbox copies")
	}
	var matchingID string
	for _, message := range messages {
		if message.Thread != frozen.Thread || message.RawThread != frozen.Thread || message.Kind != state.KindAnswer || !messageAfter(message, frozen.Question) {
			continue
		}
		if !message.ApprovalPresent || message.Approval == nil || message.Approval.QuestionMessageID != frozen.Question.ID {
			if message.From == frozen.From {
				return "", false, fmt.Errorf("release operator replay inspection found an unbound operator answer")
			}
			continue
		}
		if err := validateReleaseOperatorReplayMessage(message, frozen); err != nil {
			return "", false, err
		}
		if matchingID != "" && matchingID != message.ID {
			return "", false, fmt.Errorf("release operator replay inspection found multiple matching answers")
		}
		matchingID = message.ID
	}
	return matchingID, matchingID != "", nil
}

func validateReleaseOperatorReplayMessage(message state.Message, frozen releaseOperatorAnswerEnvelope) error {
	if message.From != frozen.From || len(message.To) != 1 || message.To[0] != frozen.To || !message.ApprovalValid || message.Approval == nil {
		return fmt.Errorf("release operator replay inspection found a conflicting answer identity or route")
	}
	if authoritySubject(message) != frozen.Subject || authorityBody(message) != frozen.Body {
		return fmt.Errorf("release operator replay inspection found an opposite or non-identical answer")
	}
	approval := *message.Approval
	if err := operatorauth.ValidateApproval(approval); err != nil {
		return fmt.Errorf("release operator replay approval is invalid: %w", err)
	}
	expected := frozen.Approval
	expected.VerifiedAt = approval.VerifiedAt
	if approval != expected {
		return fmt.Errorf("release operator replay approval does not equal the frozen semantic context")
	}
	rawApproval, present := message.Context["approval"]
	if !present || len(message.Context) != 1 {
		return fmt.Errorf("release operator replay context is not the complete typed approval context")
	}
	decodedApproval, err := operatorauth.DecodeApproval(rawApproval)
	if err != nil || decodedApproval != approval {
		return fmt.Errorf("release operator replay context does not strictly equal its decoded approval: %v", err)
	}
	return nil
}

func resolveOperatorCommandContext(projectFlag, profileFlag, sessionFlag string, projectSet, sessionSet bool) (string, string, team.Team, string, string, error) {
	ctx, err := resolveCanonicalContext(contextResolveOptions{
		ProjectFlag: projectFlag, ProfileFlag: profileFlag, SessionFlag: sessionFlag,
		ProjectExplicit: projectSet, ProfileExplicit: strings.TrimSpace(profileFlag) != "", SessionExplicit: sessionSet,
	})
	if err != nil {
		return "", "", team.Team{}, "", "", err
	}
	emitContextDiagnostics(ctx)
	projectDir, profile := ctx.ProjectDir, ctx.Profile
	if !team.ExistsProfile(projectDir, profile) {
		return "", "", team.Team{}, "", "", fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return "", "", team.Team{}, "", "", fmt.Errorf("read team: %w", err)
	}
	if !team.SupportsOperatorGates(t) {
		return "", "", team.Team{}, "", "", usageErrorf("operator gates are disabled for profile %q", profile)
	}
	workstream, err := resolveTeamWorkstreamName(t, ctx.Session, sessionSet)
	if err != nil {
		return "", "", team.Team{}, "", "", err
	}
	operator := statusOperatorForTeam(t, squadnamespace.Resolve(projectDir, profile, workstream))
	if !operator.Enabled || strings.TrimSpace(operator.Handle) == "" {
		return "", "", team.Team{}, "", "", usageErrorf("operator handle is not configured for profile %q", profile)
	}
	return projectDir, profile, t, workstream, operator.Handle, nil
}

func ensureOperatorCommandTarget(t team.Team, target, action string) error {
	if err := ensureTargetIsNotOperator(t, action, target); err != nil {
		return err
	}
	return nil
}

func sendOperatorAMQ(o operatorSendOptions) error {
	out := o.Out
	if out == nil {
		out = os.Stdout
	}
	var ctx amqContext
	var err error
	if o.ExactContext != nil {
		ctx = *o.ExactContext
		if err := validateExactOperatorAMQContext(ctx, o); err != nil {
			return fmt.Errorf("validate exact amq context for %s: %w", o.Command, err)
		}
	} else {
		ctx, err = resolveAMQContextForNamespace(o.Project, o.Profile, o.Session, o.From)
		if err != nil {
			return fmt.Errorf("resolve amq root for %s: %w", o.Command, err)
		}
	}
	ctx.Me = o.From
	args := dispatchSendArgs(ctx.Root, o.From, o.To, o.Thread, o.Kind, o.Subject, o.Body, "", "", 0)
	if len(o.Context) > 0 {
		contextJSON, marshalErr := json.Marshal(o.Context)
		if marshalErr != nil {
			return fmt.Errorf("encode %s context: %w", o.Command, marshalErr)
		}
		args = append(args, "--context", string(contextJSON))
	}
	raw, receipt, sendErr := runOwnedDurableSend(durableSendOptions{ProjectDir: o.Project, Profile: o.Profile, Session: o.Session, Kind: "operator_" + o.Command, Invocation: o.Invocation}, amqCommandRequest{Dir: o.Project, Env: amqCommandEnv(ctx), Arg: args})
	var finalPersistErr *durableFinalReceiptPersistError
	if errors.As(sendErr, &finalPersistErr) {
		return fmt.Errorf("%s send to %s: %w", o.Command, o.To, sendErr)
	}
	status := "sent"
	msgID := ""
	if receipt != nil {
		msgID = strings.TrimSpace(receipt.MessageID)
		if msgID == "" && strings.TrimSpace(receipt.ReconciledMessageID) != "" {
			status = deliveryStateReconciledExisting
			msgID = strings.TrimSpace(receipt.ReconciledMessageID)
		}
	}
	var onSentErr error
	if msgID != "" && o.OnSent != nil {
		onSentErr = o.OnSent(msgID)
	}
	if combined := errors.Join(sendErr, onSentErr); combined != nil {
		return fmt.Errorf("%s send to %s: %w", o.Command, o.To, combined)
	}
	if msgID == "" {
		return fmt.Errorf("%s send to %s returned without a stable sent or reconciled message id", o.Command, o.To)
	}
	if o.JSON {
		return printJSONEnvelope("operator_send", mutationResult{
			Command:   o.Command,
			Status:    status,
			Project:   o.Project,
			Session:   o.Session,
			Profile:   o.Profile,
			Namespace: squadnamespace.Resolve(o.Project, o.Profile, o.Session),
			Handle:    o.To,
			MessageID: msgID,
			Thread:    o.Thread,
			Root:      ctx.Root,
			Actions: []mutationAction{
				followUp("status", "show operator status", o.FollowUp),
			},
			DeliveryReceipt: receipt,
		})
	}
	if status == deliveryStateReconciledExisting {
		fmt.Fprintf(out, "Reconciled existing %s for %s on %s: %s (attempt %s, state %s, receipt %s)\n", o.Command, o.To, o.Thread, msgID, receipt.AttemptID, receipt.DeliveryState, receipt.Path)
	} else if msgID != "" {
		fmt.Fprintf(out, "Sent %s to %s on %s: %s (attempt %s, state %s, receipt %s)\n", o.Command, o.To, o.Thread, msgID, receipt.AttemptID, receipt.DeliveryState, receipt.Path)
	} else if msg := strings.TrimSpace(string(raw)); msg != "" {
		fmt.Fprintln(out, msg)
	}
	return nil
}

func canonicalGateTopic(gate string) (string, error) {
	return operatorauth.CanonicalGateThread(gate)
}

func validateTypedAnswerReason(reason string) error {
	if err := operatorauth.ValidateCanonicalSingleLineField("reason", reason, false); err != nil {
		return err
	}
	lower := strings.ToLower(reason)
	for _, reserved := range []string{"action:", "target:", "gate-kind:", "note:", "reason:", "approved:", "denied:"} {
		if strings.HasPrefix(lower, reserved) {
			return fmt.Errorf("reason must not begin with reserved field %q", strings.TrimSuffix(reserved, ":"))
		}
	}
	return nil
}

func operatorProfileArg(profile string) string {
	if squadnamespace.NormalizeProfile(profile) == team.DefaultProfile {
		return ""
	}
	return " --profile " + shellQuote(profile)
}

func runOperatorStatus(args []string) error {
	fs := flag.NewFlagSet("operator status", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to inspect")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned operator status envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad operator status - show the operator polling contract

Usage:
  amq-squad operator status [--project DIR] [--profile NAME] [--session NAME] [--json]

Reports the canonical operator inbox, poll-required state, and read-only
operator attention counts for the resolved workstream. This command does not
claim a poll lease or move mailbox messages.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	return executeOperatorStatus(operatorExecution{
		ProjectDir:      ctx.ProjectDir,
		Profile:         ctx.Profile,
		Session:         ctx.Session,
		BaseRoot:        ctx.BaseRoot,
		JSON:            *jsonOut,
		Out:             os.Stdout,
		ResolveBaseRoot: scanBaseRootForProject,
		Probe:           state.DefaultProbe,
		Now:             time.Now,
	})
}

func executeOperatorStatus(o operatorExecution) error {
	data, err := buildOperatorStatusData(o)
	if err != nil {
		return err
	}
	return writeOrRenderOperatorStatus(o.Out, "operator_status", "operator status", data, o.JSON)
}

func runOperatorPoll(args []string) error {
	fs := flag.NewFlagSet("operator poll", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to inspect")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	readonly := fs.Bool("readonly", false, "read the operator loop state without claiming a poll lease")
	owner := fs.String("owner", "cli", "poll lease owner class (cli, noc, daemon)")
	ownerID := fs.String("owner-id", "", "stable poll lease owner identity (default: cli:<hostname>:<pid>)")
	leaseTTL := fs.Duration("ttl", defaultOperatorPollLeaseTTL, "poll lease duration")
	force := fs.Bool("force", false, "steal an active poll lease from another owner")
	forceReason := fs.String("reason", "", "required reason when --force steals an active lease")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned operator poll envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad operator poll - read the operator polling workload

Usage:
  amq-squad operator poll [--project DIR] [--profile NAME] [--session NAME] [--owner NAME] [--owner-id ID] [--ttl D] [--force --reason WHY] [--json]
  amq-squad operator poll --readonly [--project DIR] [--profile NAME] [--session NAME] [--json]

Reads the canonical operator inbox and operator-loop counters without moving
mailbox messages. Without --readonly, this command claims or refreshes a local
operator-loop lease for the resolved profile/session.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *leaseTTL <= 0 {
		return usageErrorf("--ttl must be > 0")
	}
	if *readonly && *force {
		return usageErrorf("--force cannot be combined with --readonly")
	}
	if *force && strings.TrimSpace(*forceReason) == "" {
		return usageErrorf("operator poll --force requires --reason <why>")
	}
	if err := validateOperatorOwner(*owner); err != nil {
		return err
	}
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	var admission *namespaceAdmissionLocks
	if !*readonly {
		ctx, admission, err = acquireRevalidatedContextWriter(ctx, false, func() (contextResolution, error) {
			return resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
		})
		if err != nil {
			return err
		}
		defer admission.close()
		if err := ensureNoNamespaceMigration("operator poll", ctx.ProjectDir, ctx.Profile, ctx.Session); err != nil {
			return err
		}
	}
	return executeOperatorPoll(operatorExecution{
		ProjectDir:      ctx.ProjectDir,
		Profile:         ctx.Profile,
		Session:         ctx.Session,
		BaseRoot:        ctx.BaseRoot,
		ReadOnly:        *readonly,
		Owner:           *owner,
		OwnerID:         *ownerID,
		LeaseTTL:        *leaseTTL,
		Force:           *force,
		ForceReason:     *forceReason,
		JSON:            *jsonOut,
		Out:             os.Stdout,
		ResolveBaseRoot: scanBaseRootForProject,
		Probe:           state.DefaultProbe,
		Now:             time.Now,
	})
}

func executeOperatorPoll(o operatorExecution) error {
	data, err := buildOperatorPollData(o)
	if err != nil {
		var conflict *operatorPollLeaseConflictError
		if o.JSON && errors.As(err, &conflict) {
			if writeErr := writeOrRenderOperatorStatus(o.Out, "operator_poll", "operator poll", data, o.JSON); writeErr != nil {
				return writeErr
			}
		}
		return err
	}
	return writeOrRenderOperatorStatus(o.Out, "operator_poll", "operator poll", data, o.JSON)
}

func runOperatorWatch(args []string) error {
	fs := flag.NewFlagSet("operator watch", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory to inspect (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile to inspect (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to inspect")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	owner := fs.String("owner", "cli", "poll lease owner class (cli, noc, daemon)")
	ownerID := fs.String("owner-id", "", "stable poll lease owner identity (default: cli:<hostname>:<pid>)")
	leaseTTL := fs.Duration("ttl", defaultOperatorPollLeaseTTL, "poll lease duration")
	interval := fs.Duration("interval", defaultOperatorWatchInterval, "watch refresh interval")
	once := fs.Bool("once", false, "emit one watch tick and exit")
	jsonOut := fs.Bool("json", false, "emit compact NDJSON operator_watch envelopes")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad operator watch - reference operator polling loop

Usage:
  amq-squad operator watch [--project DIR] [--profile NAME] [--session NAME] [--owner NAME] [--owner-id ID] [--ttl D] [--interval D] [--once] [--json]

Recomputes operator state and claims or refreshes the local operator-loop lease
on a cadence. This command does not drain, read, or move AMQ mailbox messages.
When stopped, the lease is not released immediately; it expires after --ttl.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if *leaseTTL <= 0 {
		return usageErrorf("--ttl must be > 0")
	}
	if *interval <= 0 {
		return usageErrorf("--interval must be > 0")
	}
	if *interval > *leaseTTL/2 {
		return usageErrorf("--interval must be <= --ttl/2 so the watch refreshes before lease expiry")
	}
	if err := validateOperatorOwner(*owner); err != nil {
		return err
	}
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	ctx, admission, err := acquireRevalidatedContextWriter(ctx, false, func() (contextResolution, error) {
		return resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	})
	if err != nil {
		return err
	}
	defer admission.close()
	if err := ensureNoNamespaceMigration("operator watch", ctx.ProjectDir, ctx.Profile, ctx.Session); err != nil {
		return err
	}
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)
	sleep := func(d time.Duration) bool {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-timer.C:
			return true
		case <-sigCh:
			return false
		}
	}
	select {
	case <-sigCh:
		return nil
	default:
	}
	return executeOperatorWatch(operatorWatchExecution{
		operatorExecution: operatorExecution{
			ProjectDir:      ctx.ProjectDir,
			Profile:         ctx.Profile,
			Session:         ctx.Session,
			BaseRoot:        ctx.BaseRoot,
			Owner:           *owner,
			OwnerID:         *ownerID,
			LeaseTTL:        *leaseTTL,
			JSON:            *jsonOut,
			Out:             os.Stdout,
			ResolveBaseRoot: scanBaseRootForProject,
			Probe:           state.DefaultProbe,
			Now:             time.Now,
		},
		Interval: *interval,
		Once:     *once,
		Sleep:    sleep,
	})
}

func executeOperatorWatch(w operatorWatchExecution) error {
	if w.Out == nil {
		w.Out = os.Stdout
	}
	interval := w.Interval
	if interval <= 0 {
		interval = defaultOperatorWatchInterval
	}
	sleep := w.Sleep
	if sleep == nil {
		sleep = func(d time.Duration) bool {
			time.Sleep(d)
			return true
		}
	}
	tick := 1
	for {
		now := operatorNow(w.operatorExecution)
		data, err := buildOperatorPollData(w.operatorExecution)
		if err != nil {
			var conflict *operatorPollLeaseConflictError
			if !errors.As(err, &conflict) {
				return err
			}
			data.Watch = &operatorWatchMeta{Interval: interval.String(), Tick: tick, At: now.UTC()}
			if writeErr := writeOperatorWatchTick(w.Out, data, w.JSON); writeErr != nil {
				return writeErr
			}
			if w.Once {
				return err
			}
		} else if w.Once {
			data.Notifications = operatorWatchNotificationPump(w, data, now)
			data.Watch = &operatorWatchMeta{Interval: interval.String(), Tick: tick, At: now.UTC()}
			if writeErr := writeOperatorWatchTick(w.Out, data, w.JSON); writeErr != nil {
				return writeErr
			}
			return nil
		} else {
			data.Notifications = operatorWatchNotificationPump(w, data, now)
			data.Watch = &operatorWatchMeta{Interval: interval.String(), Tick: tick, At: now.UTC()}
			if writeErr := writeOperatorWatchTick(w.Out, data, w.JSON); writeErr != nil {
				return writeErr
			}
		}
		if !sleep(interval) {
			return nil
		}
		tick++
	}
}

func deliverOperatorWatchNotifications(w operatorWatchExecution, data operatorStatusEnvelopeData, now time.Time) *operatorNotificationSummary {
	t, err := team.ReadProfile(data.ProjectDir, data.Profile)
	if err != nil {
		return nil
	}
	policy := team.EffectiveOperatorNotifications(t.Operator)
	if !policy.Enabled {
		return nil
	}
	var out bytes.Buffer
	err = executeNotify(notifyExecution{ProjectDir: data.ProjectDir, Profile: data.Profile, Session: data.Session, BaseRoot: w.BaseRoot, RenotifyAfter: defaultOperatorRenotifyAfter, Deliver: true, JSON: true, Out: &out, Now: func() time.Time { return now }, ResolveBaseRoot: w.ResolveBaseRoot, Probe: w.Probe})
	if err != nil {
		return &operatorNotificationSummary{Failed: 1}
	}
	var env struct {
		Data notifyEnvelopeData `json:"data"`
	}
	if json.Unmarshal(out.Bytes(), &env) != nil {
		return &operatorNotificationSummary{Failed: 1}
	}
	d := env.Data.DeliverySummary
	return &operatorNotificationSummary{Selected: d.Selected, Delivered: d.Delivered, Failed: d.Failed, Suppressed: d.Suppressed}
}

func writeOperatorWatchTick(out io.Writer, data operatorStatusEnvelopeData, jsonOut bool) error {
	if jsonOut {
		return writeCompactJSONEnvelope(out, "operator_watch", data)
	}
	return writeOrRenderOperatorStatus(out, "operator_watch", "operator watch", data, false)
}

func buildOperatorPollData(o operatorExecution) (operatorStatusEnvelopeData, error) {
	data, err := buildOperatorStatusData(o)
	if err != nil {
		return data, err
	}
	data.ReadOnly = o.ReadOnly
	if o.ReadOnly {
		return data, nil
	}
	now := operatorNow(o)
	owner := strings.TrimSpace(o.Owner)
	if owner == "" {
		owner = "cli"
	}
	if err := validateOperatorOwner(owner); err != nil {
		return data, err
	}
	ownerID := strings.TrimSpace(o.OwnerID)
	if ownerID == "" {
		ownerID = defaultOperatorOwnerID(owner)
	}
	ttl := o.LeaseTTL
	if ttl <= 0 {
		ttl = defaultOperatorPollLeaseTTL
	}
	lease, err := claimOperatorLoopLease(data.ProjectDir, data.Profile, data.Session, data.Namespace.ID, owner, ownerID, ttl, data.operatorCursor, now, o.Force, o.ForceReason)
	if err != nil {
		var conflict *operatorPollLeaseConflictError
		if errors.As(err, &conflict) {
			claimed := false
			data.Claimed = &claimed
			applyOperatorLoopLease(&data.OperatorLoop, conflict.Lease, now)
			data.Conflict = operatorPollLeaseConflictData(conflict)
		}
		return data, err
	}
	applyOperatorLoopLease(&data.OperatorLoop, lease, now)
	claimed := true
	data.Claimed = &claimed
	return data, nil
}

func buildOperatorStatusData(o operatorExecution) (operatorStatusEnvelopeData, error) {
	now := time.Now
	if o.Now != nil {
		now = o.Now
	}
	t, err := team.ReadProfile(o.ProjectDir, o.Profile)
	if err != nil {
		return operatorStatusEnvelopeData{}, fmt.Errorf("read team: %w", err)
	}
	workstream, err := resolveTeamWorkstreamName(t, o.Session, strings.TrimSpace(o.Session) != "")
	if err != nil {
		return operatorStatusEnvelopeData{}, err
	}
	ns := squadnamespace.Resolve(t.Project, o.Profile, workstream)
	operator := statusOperatorForTeam(t, ns)
	delivery := operatorDeliveryForTeam(t)
	data := operatorStatusEnvelopeData{
		ProjectDir:       t.Project,
		Profile:          squadnamespace.NormalizeProfile(o.Profile),
		Session:          workstream,
		Namespace:        ns,
		ReadOnly:         true,
		Operator:         operator,
		OperatorDelivery: delivery,
		OperatorGates:    team.SupportsOperatorGates(t),
	}
	if !team.SupportsOperatorGates(t) || !operator.Enabled {
		data.OperatorLoop = operatorLoopStatus{
			Mode:           "disabled",
			State:          "unconfigured",
			Owner:          "none",
			DegradedReason: "operator gates disabled for this profile",
		}
		data.Message = "operator gates disabled"
		return data, nil
	}

	baseRoot := strings.TrimSpace(o.BaseRoot)
	if baseRoot == "" {
		resolve := o.ResolveBaseRoot
		if resolve == nil {
			resolve = scanBaseRootForProject
		}
		baseRoot, err = resolve(o.ProjectDir)
		if err != nil {
			return operatorStatusEnvelopeData{}, fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	snap, err := state.BuildWithThresholds(o.ProjectDir, baseRoot, o.Probe, state.Thresholds{OperatorHandle: operator.Handle})
	if err != nil {
		return operatorStatusEnvelopeData{}, fmt.Errorf("scan AMQ base root: %w", err)
	}
	data.BaseRoot = snap.BaseRoot
	projected, err := collectProjectedOperatorAttention(t, o.ProjectDir, o.Profile, snap, operator.Handle, workstream, now())
	if err != nil {
		return operatorStatusEnvelopeData{}, fmt.Errorf("project compound release attention: %w", err)
	}
	items := projected.Items
	items = activeOperatorAttention(items)
	session, sessionOK := operatorSessionSnapshot(projected.Snapshot, o.Profile, workstream)
	originalSession, originalSessionOK := operatorSessionSnapshot(snap, o.Profile, workstream)
	backlog := 0
	directivesUnacked := 0
	operatorCursor := ""
	if sessionOK {
		backlog = operatorUnreadBacklogWithProjected(session.Coordination.Threads, operator.Handle, items)
		directivesUnacked = operatorDirectivesUnacked(session.Coordination.Threads, operator.Handle, teamLeadHandle(t))
	}
	if originalSessionOK {
		operatorCursor = operatorInboxHighWater(originalSession.Coordination.Threads, operator.Handle)
	}
	gatesOpen := operatorOpenGates(items)
	blockedGoals := blockedNativeGoalsInSnapshot(t, o.Profile, workstream, snap)
	data.Attention = items
	data.operatorCursor = operatorCursor
	data.OperatorLoop = operatorLoopForDelivery(delivery)
	data.OperatorLoop.Backlog = backlog
	data.OperatorLoop.GatesOpen = gatesOpen
	data.OperatorLoop.DirectivesUnacked = directivesUnacked
	if data.Operator.Poll != nil {
		data.Operator.Poll.Unread = backlog
		data.Operator.Poll.OpenGates = gatesOpen
		data.Operator.Poll.OpenBlockers = blockedGoals
	}
	lease, err := readOperatorLoopLease(operatorLoopLeasePath(t.Project, data.Profile, workstream))
	if err != nil {
		return operatorStatusEnvelopeData{}, err
	}
	applyOperatorLoopLease(&data.OperatorLoop, lease, now())
	return data, nil
}

func activeOperatorAttention(items []operatorAttention) []operatorAttention {
	active := make([]operatorAttention, 0, len(items))
	for _, item := range items {
		if !item.Cleared {
			active = append(active, item)
		}
	}
	return active
}

func operatorNow(o operatorExecution) time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

func operatorSessionSnapshot(snap state.Snapshot, profile, session string) (state.Session, bool) {
	profile = squadnamespace.NormalizeProfile(profile)
	for _, candidate := range snap.Sessions {
		if candidate.Name == session && squadnamespace.ProfilesEqual(candidate.TeamProfile, profile) {
			return candidate, true
		}
	}
	return state.Session{}, false
}

func operatorUnreadBacklog(threads []state.ThreadSummary, operatorHandle string) int {
	count := 0
	for _, th := range threads {
		if strings.HasPrefix(th.ID, "gate/") && th.OperatorGateState.IsTerminal() {
			continue
		}
		if notifyUnreadBy(th, operatorHandle) {
			count++
		}
	}
	return count
}

func operatorUnreadBacklogWithProjected(threads []state.ThreadSummary, operatorHandle string, items []operatorAttention) int {
	activeGateThreads := make(map[string]bool)
	for _, item := range items {
		if item.EventType == "gate" && item.Actionable && !item.Cleared {
			activeGateThreads[item.Thread] = true
		}
	}

	unreadThreads := make(map[string]bool)
	for _, thread := range threads {
		if strings.HasPrefix(thread.ID, "gate/") {
			switch thread.OperatorGateState {
			case state.OperatorGateStateClosed, state.OperatorGateStateWithdrawn:
				continue
			case state.OperatorGateStateAnswered:
				if !activeGateThreads[thread.ID] {
					continue
				}
			}
		}
		if notifyUnreadBy(thread, operatorHandle) {
			unreadThreads[thread.ID] = true
		}
	}
	for _, item := range items {
		if item.EventType == "compound_release_child" && item.Actionable && item.Answerable && item.Unread {
			unreadThreads[item.Thread] = true
		}
	}
	return len(unreadThreads)
}

func operatorOpenGates(items []operatorAttention) int {
	count := 0
	for _, item := range items {
		if isOpenGateAttention(item) {
			count++
		}
	}
	return count
}

func isOpenGateAttention(item operatorAttention) bool {
	if !item.Actionable || item.Cleared {
		return false
	}
	return item.EventType == "gate" || item.EventType == "compound_release_child"
}

func operatorDirectivesUnacked(threads []state.ThreadSummary, operatorHandle, leadHandle string) int {
	operatorHandle = strings.TrimSpace(operatorHandle)
	leadHandle = strings.TrimSpace(leadHandle)
	if operatorHandle == "" || leadHandle == "" {
		return 0
	}
	count := 0
	for _, th := range threads {
		if !strings.HasPrefix(strings.TrimSpace(th.Subject), "DIRECTIVE:") || !notifyUnreadBy(th, leadHandle) {
			continue
		}
		a, b, ok := parseP2PThread(th.ID)
		if !ok {
			continue
		}
		if (a == operatorHandle && b == leadHandle) || (a == leadHandle && b == operatorHandle) {
			count++
		}
	}
	return count
}

func operatorLoopState(pollRequired bool) string {
	if pollRequired {
		return "poll_required_unowned"
	}
	return "unconfigured"
}

func operatorLoopForDelivery(delivery operatorDeliveryData) operatorLoopStatus {
	return operatorLoopStatus{
		Mode:         "poll",
		PollRequired: delivery.PollRequired,
		State:        operatorLoopState(delivery.PollRequired),
		Owner:        delivery.PollOwner,
	}
}

func operatorLoopLeasePath(projectDir, profile, session string) string {
	base := filepath.Join(projectDir, team.DirName, "operator-loop")
	profile = squadnamespace.NormalizeProfile(profile)
	if profile != team.DefaultProfile {
		base = filepath.Join(base, profile)
	}
	return filepath.Join(base, session+".json")
}

func operatorLoopLeaseLockPath(projectDir, profile, session string) string {
	return operatorLoopLeasePath(projectDir, profile, session) + ".lock"
}

func readOperatorLoopLease(path string) (operatorLoopLeaseFile, error) {
	var lease operatorLoopLeaseFile
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return lease, nil
		}
		return lease, fmt.Errorf("read operator loop lease: %w", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return lease, nil
	}
	if err := json.Unmarshal(b, &lease); err != nil {
		return lease, fmt.Errorf("parse operator loop lease %s: %w", path, err)
	}
	return lease, nil
}

func claimOperatorLoopLease(projectDir, profile, session, namespaceID, owner, ownerID string, ttl time.Duration, cursor string, now time.Time, force bool, forceReason string) (operatorLoopLeaseFile, error) {
	path := operatorLoopLeasePath(projectDir, profile, session)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return operatorLoopLeaseFile{}, fmt.Errorf("ensure operator loop dir: %w", err)
	}
	var next operatorLoopLeaseFile
	err := flock.WithLock(operatorLoopLeaseLockPath(projectDir, profile, session), func() error {
		current, err := readOperatorLoopLease(path)
		if err != nil {
			return err
		}
		liveForeign := current.OwnerID != "" && current.OwnerID != ownerID && now.Before(current.LeaseExpiresAt)
		if liveForeign && !force {
			return &operatorPollLeaseConflictError{Lease: current}
		}
		if liveForeign && strings.TrimSpace(forceReason) == "" {
			return usageErrorf("operator poll --force requires --reason <why>")
		}
		next = operatorLoopLeaseFile{
			SchemaVersion:  1,
			Profile:        squadnamespace.NormalizeProfile(profile),
			Session:        session,
			NamespaceID:    namespaceID,
			Mode:           "poll",
			Owner:          owner,
			OwnerID:        ownerID,
			LeaseTTL:       ttl.String(),
			LeaseExpiresAt: now.Add(ttl).UTC(),
			LastPollAt:     now.UTC(),
			Cursor:         cursor,
			UpdatedAt:      now.UTC(),
		}
		if liveForeign {
			if err := writeOperatorLoopForceAudit(projectDir, profile, session, namespaceID, owner, ownerID, current, forceReason, now); err != nil {
				return err
			}
		}
		return writeOperatorLoopLease(path, next)
	})
	if err != nil {
		return operatorLoopLeaseFile{}, err
	}
	return next, nil
}

func writeOperatorLoopForceAudit(projectDir, profile, session, namespaceID, owner, ownerID string, previous operatorLoopLeaseFile, reason string, now time.Time) error {
	dir := filepath.Join(projectDir, team.DirName, "operator-loop-audit")
	profile = squadnamespace.NormalizeProfile(profile)
	if profile != team.DefaultProfile {
		dir = filepath.Join(dir, profile)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("ensure operator loop audit dir: %w", err)
	}
	rec := operatorLoopForceAuditRecord{
		At:                   now.UTC(),
		ProjectDir:           projectDir,
		Profile:              profile,
		Session:              session,
		NamespaceID:          namespaceID,
		ActorOwner:           owner,
		ActorOwnerID:         ownerID,
		PreviousOwner:        previous.Owner,
		PreviousOwnerID:      previous.OwnerID,
		PreviousLeaseExpires: previous.LeaseExpiresAt.UTC(),
		Reason:               strings.TrimSpace(reason),
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal operator loop audit: %w", err)
	}
	path := filepath.Join(dir, sanitizeWorkstreamName(session)+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open operator loop audit: %w", err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("write operator loop audit: %w", err)
	}
	return nil
}

func writeOperatorLoopLease(path string, lease operatorLoopLeaseFile) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure operator loop dir: %w", err)
	}
	b, err := json.MarshalIndent(lease, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal operator loop lease: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("write operator loop lease: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename operator loop lease: %w", err)
	}
	return nil
}

func applyOperatorLoopLease(loop *operatorLoopStatus, lease operatorLoopLeaseFile, now time.Time) {
	if loop == nil || strings.TrimSpace(lease.OwnerID) == "" {
		return
	}
	loop.Owner = lease.Owner
	loop.OwnerID = lease.OwnerID
	loop.LeaseTTL = lease.LeaseTTL
	if !lease.LeaseExpiresAt.IsZero() {
		loop.LeaseExpiresAt = lease.LeaseExpiresAt.UTC().Format(time.RFC3339)
	}
	if !lease.LastPollAt.IsZero() {
		loop.LastPollAt = lease.LastPollAt.UTC().Format(time.RFC3339)
	}
	loop.Cursor = lease.Cursor
	if !lease.LeaseExpiresAt.IsZero() && now.Before(lease.LeaseExpiresAt) {
		loop.State = "poller_active"
		return
	}
	loop.State = "poller_stale"
}

func operatorPollLeaseConflictData(conflict *operatorPollLeaseConflictError) *operatorPollConflict {
	if conflict == nil {
		return nil
	}
	lease := conflict.Lease
	out := &operatorPollConflict{
		Code:    "lease_conflict",
		Message: conflict.Error(),
		Owner:   lease.Owner,
		OwnerID: lease.OwnerID,
		Cursor:  lease.Cursor,
	}
	if !lease.LeaseExpiresAt.IsZero() {
		out.LeaseExpiresAt = lease.LeaseExpiresAt.UTC().Format(time.RFC3339)
	}
	if !lease.LastPollAt.IsZero() {
		out.LastPollAt = lease.LastPollAt.UTC().Format(time.RFC3339)
	}
	return out
}

func operatorInboxHighWater(threads []state.ThreadSummary, operatorHandle string) string {
	latest := ""
	for _, th := range threads {
		if !threadParticipant(th, operatorHandle) || th.LatestID == "" {
			continue
		}
		if th.LatestID > latest {
			latest = th.LatestID
		}
	}
	return latest
}

func threadParticipant(th state.ThreadSummary, handle string) bool {
	handle = strings.TrimSpace(handle)
	if handle == "" {
		return false
	}
	for _, p := range th.Participants {
		if p == handle {
			return true
		}
	}
	return false
}

func validateOperatorOwner(owner string) error {
	switch strings.TrimSpace(owner) {
	case "cli", "noc", "daemon":
		return nil
	default:
		return usageErrorf("--owner must be one of cli, noc, or daemon")
	}
}

func defaultOperatorOwnerID(owner string) string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	owner = strings.TrimSpace(owner)
	if owner == "" {
		owner = "cli"
	}
	return fmt.Sprintf("%s:%s:%d", owner, host, os.Getpid())
}

func writeOrRenderOperatorStatus(out io.Writer, kind, label string, data operatorStatusEnvelopeData, jsonOut bool) error {
	if out == nil {
		out = os.Stdout
	}
	if jsonOut {
		return writeJSONEnvelope(out, kind, data)
	}
	inboxRoot := ""
	if data.Operator.CanonicalInbox != nil {
		inboxRoot = data.Operator.CanonicalInbox.Root
	}
	fmt.Fprintf(out, "# %s: %s/%s\n", label, data.Profile, data.Session)
	fmt.Fprintf(out, "# inbox: %s handle=%s\n", inboxRoot, data.Operator.Handle)
	fmt.Fprintf(out, "# loop: %s owner=%s backlog=%d\n\n", data.OperatorLoop.State, data.OperatorLoop.Owner, data.OperatorLoop.Backlog)
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "THREAD\tREASON\tESCALATION\tAGE\tFROM\tSUBJECT")
	for _, item := range data.Attention {
		escalation := item.Escalation
		if escalation == "" {
			escalation = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", item.Thread, item.Reason, escalation, item.Age, item.From, item.Subject)
	}
	return w.Flush()
}
