package cli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/flock"
	squadnamespace "github.com/omriariav/amq-squad/v3/internal/namespace"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
	"github.com/omriariav/amq-squad/v3/internal/state"
	"github.com/omriariav/amq-squad/v3/internal/team"
)

var resolveOperatorAnswerContext = resolveScopedCommandContext

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
	case "inbox":
		return runOperatorInbox(args[1:])
	default:
		return unknownSubcommandError("operator", args[0], operatorSubcommands...)
	}
}

// operatorSubcommands is the subcommand list runOperator dispatches, shared by
// the unknown-subcommand hint and the usage-vs-dispatch test.
var operatorSubcommands = []string{"answer", "inbox"}

func printOperatorUsage() {
	fmt.Fprint(os.Stderr, `amq-squad operator - inspect and act as the configured operator participant

Usage:
  amq-squad operator <subcommand> [options]

Subcommands:
  answer  answer an operator gate on gate/<topic>
  inbox   the operator session's view: open gates of every squad in the fleet
          in one list, and the lead questions with their relay argv

Run 'amq-squad operator <subcommand> --help' for subcommand options and flags.

Examples:
  amq-squad operator answer --gate release --approved
  amq-squad operator inbox --json
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
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad operator answer - answer an operator gate

Usage:
  amq-squad operator answer [--project DIR] [--profile NAME] [--session S] --gate TOPIC [--to HANDLE] (--approved|--denied) [--reason TEXT] [--json]

Sends an AMQ answer from the configured operator handle on gate/<topic>. This
first-class command avoids hand-writing the operator protocol. Typed requests
auto-bind the exact gate owner; --to is required only for legacy/raw gates.

It answers merge-kind requests only. The typed gates (tag, github_release,
package_publish, external_send, credential_change, spawn) are answered only by
the owner's signed Buzz reply, verified with 'amq-squad gate answer'.
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
	if action, err := operatorauth.CanonicalAction(*actionFlag); err == nil && typedGateAction(action) {
		return usageErrorf("operator answer refused: %s", typedOperatorAnswerRefusal)
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
	initialRef, initialHandle := squadnamespace.Resolve(projectDir, profile, workstream), operatorHandle
	admittedContext, admission, err := acquireRevalidatedContextWriter(initialContext, resolveAnswerContext)
	if err != nil {
		return err
	}
	defer admission.close()
	currentProject, currentProfile, currentTeam, currentWorkstream, currentOperator, err := resolveOperatorCommandContext(*projectFlag, *profileFlag, *sessionFlag, flagWasSet(fs, "project"), flagWasSet(fs, "session"))
	if err != nil {
		return fmt.Errorf("operator answer refused: context re-resolution under admission failed: %w", err)
	}
	if err := validateReResolvedEndpoint("operator answer", initialRef, squadnamespace.Resolve(currentProject, currentProfile, currentWorkstream), initialHandle, currentOperator); err != nil {
		return err
	}
	projectDir, profile, t, workstream, operatorHandle = currentProject, currentProfile, currentTeam, currentWorkstream, currentOperator
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
	if classification.Disposition == cliReleaseDomainReleaseOwned {
		// release-owned children are tag and github_release gates: typed
		return usageErrorf("operator answer refused: %s", typedOperatorAnswerRefusal)
	}
	if classification.Disposition != cliReleaseDomainOrdinary {
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
			FollowUp: "amq-squad status --project " + shellQuote(projectDir) + operatorProfileArg(profile) + " --session " + shellQuote(workstream) + " --json",
		})
	}
	if !selectedQuestion.AuthorizationRequestValid || selectedQuestion.AuthorizationRequest == nil {
		return usageErrorf("latest typed gate request is malformed and blocks legacy fallback: %s", selectedQuestion.AuthorizationRequestError)
	}
	if typedGateAction(selectedQuestion.AuthorizationRequest.Action) {
		return usageErrorf("operator answer refused: %s", typedOperatorAnswerRefusal)
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
		FollowUp: "amq-squad status --project " + shellQuote(projectDir) + operatorProfileArg(profile) + " --session " + shellQuote(workstream) + " --json",
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
	if !team.ExistsFor(projectDir, profile) {
		return "", "", team.Team{}, "", "", fmt.Errorf("no team configured for profile %q. Run '%s' first.", profile, profileInitCommand(profile))
	}
	t, err := team.ReadFor(projectDir, profile)
	if err != nil {
		return "", "", team.Team{}, "", "", fmt.Errorf("read team: %w", err)
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
				followUp("status", "show the squad status and attention", o.FollowUp),
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
	if squadnamespace.NormalizeProfile(profile) == squadnamespace.DefaultProfile {
		return ""
	}
	return " --profile " + shellQuote(profile)
}
