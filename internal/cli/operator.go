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
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
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

var resolveOperatorAnswerContext = resolveScopedCommandContext

type operatorStatusEnvelopeData struct {
	ProjectDir     string              `json:"project_dir"`
	BaseRoot       string              `json:"base_root,omitempty"`
	Profile        string              `json:"profile"`
	Session        string              `json:"session"`
	Namespace      squadnamespace.Ref  `json:"namespace"`
	ReadOnly       bool                `json:"readonly"`
	Operator       statusOperatorView  `json:"operator"`
	Attention      []operatorAttention `json:"attention,omitempty"`
	OperatorGates  bool                `json:"operator_gates"`
	Message        string              `json:"message,omitempty"`
	sourceWarnings []state.Warning
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
	case "status":
		return runOperatorStatus(args[1:])
	default:
		return unknownSubcommandError("operator", args[0], operatorSubcommands...)
	}
}

// operatorSubcommands is the subcommand list runOperator dispatches, shared by
// the unknown-subcommand hint and the usage-vs-dispatch test. 3.0 (2a-2) keeps
// gate answering and the attention projection (manifest C17). directive, poll
// and watch are cut (seat messages go through dispatch, 2a-1 ruling 8; the NOC
// renders attention), and so is self-approve: only the owner answers a typed
// gate (plan 7.1, 3.5).
var operatorSubcommands = []string{"answer", "status"}

func printOperatorUsage() {
	fmt.Fprint(os.Stderr, `amq-squad operator - inspect and act as the configured operator participant

Usage:
  amq-squad operator <subcommand> [options]

Subcommands:
  answer  answer an operator gate on gate/<topic>
  status  show the operator attention projection and inbox state

Run 'amq-squad operator <subcommand> --help' for subcommand options and flags.

Examples:
  amq-squad operator answer --gate release --approved
  amq-squad operator status --json
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
	// Serialize before release inspection: a sibling answer can retain the
	// release store's exclusive invocation lease, and an uncoordinated inspector
	// would fail busy instead of reconciling that sibling's accepted answer.
	answerLockPath := operatorAnswerLockPath(projectDir, profile, workstream, topic)
	if err := os.MkdirAll(filepath.Dir(answerLockPath), 0o755); err != nil {
		return fmt.Errorf("create operator answer lock directory: %w", err)
	}
	answerLock, err := flock.AcquireExclusive(answerLockPath)
	if err != nil {
		return fmt.Errorf("acquire operator answer gate lock: %w", err)
	}
	defer answerLock.Close()

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

func operatorAnswerLockPath(projectDir, profile, session, gate string) string {
	scope := strings.Join([]string{squadnamespace.NormalizeProfile(profile), strings.TrimSpace(session), gate}, "\x00")
	id := sha256.Sum256([]byte(scope))
	return filepath.Join(projectDir, team.DirName, "locks", fmt.Sprintf("operator-answer-%x.lock", id))
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
	raw, result, sendErr := runOwnedAMQSend(ownedAMQSendOptions{Invocation: o.Invocation}, amqCommandRequest{Dir: o.Project, Env: amqCommandEnv(ctx), Arg: args})
	status := "sent"
	msgID := strings.TrimSpace(result.MessageID)
	if result.Reconciled {
		status = "reconciled_existing"
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
		})
	}
	if result.Reconciled {
		fmt.Fprintf(out, "Reconciled existing %s for %s on %s: %s\n", o.Command, o.To, o.Thread, msgID)
	} else if msgID != "" {
		fmt.Fprintf(out, "Sent %s to %s on %s: %s\n", o.Command, o.To, o.Thread, msgID)
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
	data := operatorStatusEnvelopeData{
		ProjectDir:    t.Project,
		Profile:       squadnamespace.NormalizeProfile(o.Profile),
		Session:       workstream,
		Namespace:     ns,
		ReadOnly:      true,
		Operator:      operator,
		OperatorGates: team.SupportsOperatorGates(t),
	}
	if !team.SupportsOperatorGates(t) || !operator.Enabled {
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
	items := activeOperatorAttention(projected.Items)
	backlog := 0
	if session, ok := operatorSessionSnapshot(projected.Snapshot, o.Profile, workstream); ok {
		backlog = operatorUnreadBacklogWithProjected(session.Coordination.Threads, operator.Handle, items)
		data.sourceWarnings = append([]state.Warning(nil), session.Coordination.Warnings...)
	}
	gatesOpen := operatorOpenGates(items)
	data.Attention = items
	if data.Operator.Poll != nil {
		data.Operator.Poll.Unread = backlog
		data.Operator.Poll.OpenGates = &gatesOpen
	}
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

func operatorSessionSnapshot(snap state.Snapshot, profile, session string) (state.Session, bool) {
	profile = squadnamespace.NormalizeProfile(profile)
	for _, candidate := range snap.Sessions {
		if candidate.Name == session && squadnamespace.ProfilesEqual(candidate.TeamProfile, profile) {
			return candidate, true
		}
	}
	return state.Session{}, false
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
	unread := 0
	if data.Operator.Poll != nil {
		unread = data.Operator.Poll.Unread
	}
	fmt.Fprintf(out, "# unread: %d\n\n", unread)
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
