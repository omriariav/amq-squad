package cli

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

const (
	actionDecisionApproved = "approved"
	actionDecisionPending  = "pending"
	actionDecisionDenied   = "denied"
	actionDecisionNoGate   = "no_gate"
	actionDecisionUnbound  = "unbound"
)

type verifyActionExecution struct {
	ProjectDir        string
	Profile           string
	Session           string
	Gate              string
	Action            string
	Target            string
	To                string
	BaseRoot          string
	ResolveBaseRoot   func(projectDir string) (string, error)
	Probe             state.Probe
	Now               func() time.Time
	Out               io.Writer
	JSON              bool
	EmitAuthorization bool
	SigningKeyFile    string
	AuthorizationOut  string
	Capture           *verifyActionResult
}

type verifyActionResult struct {
	Action                    string                                      `json:"action"`
	Target                    string                                      `json:"target"`
	Gate                      string                                      `json:"gate"`
	Decision                  string                                      `json:"decision"`
	AnsweredBy                string                                      `json:"answered_by,omitempty"`
	MessageID                 string                                      `json:"message_id,omitempty"`
	GateKind                  string                                      `json:"gate_kind,omitempty"`
	ApprovalSource            string                                      `json:"approval_source,omitempty"`
	SelfApproved              bool                                        `json:"self_approved"`
	TypedAuthority            bool                                        `json:"typed_authority"`
	TypedEligible             bool                                        `json:"typed_eligible"`
	EnvelopeEligible          bool                                        `json:"envelope_eligible"`
	EnvelopeEligibilityReason string                                      `json:"envelope_eligibility_reason"`
	QuestionMessageID         string                                      `json:"question_message_id,omitempty"`
	AnswerMessageID           string                                      `json:"answer_message_id,omitempty"`
	QuestionCreatedAt         string                                      `json:"question_created_at,omitempty"`
	AnswerCreatedAt           string                                      `json:"answer_created_at,omitempty"`
	AnsweredByRole            string                                      `json:"answered_by_role,omitempty"`
	Namespace                 *operatorauth.NamespaceBinding              `json:"namespace,omitempty"`
	Note                      string                                      `json:"note,omitempty"`
	AuthorizationID           string                                      `json:"authorization_id,omitempty"`
	AuthorizationPath         string                                      `json:"authorization_path,omitempty"`
	AuthorizationKeyID        string                                      `json:"authorization_key_id,omitempty"`
	Compound                  *operatorauth.AuthorizationCompoundEvidence `json:"compound,omitempty"`
	approval                  *operatorauth.ApprovalContext
	Failures                  []verifyMergeFailure `json:"failures,omitempty"`
}

var resolveVerifyActionContext = resolveScopedCommandContext

func runVerifyAction(args []string) error {
	fs := flag.NewFlagSet("verify action", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile namespace (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session containing the gate")
	gateFlag := fs.String("gate", "", "gate topic, with or without gate/ prefix")
	actionFlag := fs.String("action", "", "high-risk action kind")
	targetFlag := fs.String("target", "", "exact target descriptor bound to the gate")
	toFlag := fs.String("to", "", "lead/agent handle that owns the gate (used in guidance)")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned verify_action envelope")
	emitAuthorization := fs.Bool("emit-authorization", false, "persist a signed connector authorization artifact after PASS")
	signingKeyFile := fs.String("signing-key-file", "", "absolute operator-provisioned PKCS#8 Ed25519 private key (0600)")
	authorizationOut := fs.String("authorization-out", "", "optional absolute output path for the immutable signed authorization artifact")
	listKinds := fs.Bool("list-kinds", false, "list the shared action catalog without resolving project context")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `amq-squad verify action - require a bound operator gate for a high-risk action

Usage:
  amq-squad verify action --project DIR --session S --gate TOPIC --action KIND --target TARGET [--profile P] [--to HANDLE] [--json]
  amq-squad verify action ... --emit-authorization --signing-key-file FILE [--authorization-out FILE]
  amq-squad verify action --list-kinds [--json]

High-risk actions:
  %s

Both the gate question and the operator answer must bind to the same action and
target. Put explicit binding text in the gate and answer, for example:
  Action: github_release
  Target: draft v1.41.0 release for omriariav/workspace-cli

--emit-authorization requires a human-approved typed result and an explicit
owner-controlled Ed25519 PKCS#8 private key with mode 0600. Consume the
immutable artifact with 'amq-squad verify authorization', which checks an
explicit trust store and rebinds the current namespace, gate, receipt,
policy/preflight, and compound-release evidence. Neither command performs the
external action.

Exit codes:
  0 approved
  10 pending
  11 denied
  12 no matching gate
  13 unbound or mismatched operator answer
`, strings.Join(catalogActionNames(), ", "))
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	if *listKinds {
		if *emitAuthorization || *signingKeyFile != "" || *authorizationOut != "" {
			return usageErrorf("--list-kinds cannot emit an authorization artifact")
		}
		return printVerifyActionKinds(*jsonOut)
	}
	if !*emitAuthorization && (*signingKeyFile != "" || *authorizationOut != "") {
		return usageErrorf("--signing-key-file and --authorization-out require --emit-authorization")
	}
	if *emitAuthorization && strings.TrimSpace(*signingKeyFile) == "" {
		return usageErrorf("--emit-authorization requires --signing-key-file; unsigned verifier PASS cannot emit a connector artifact")
	}
	canonicalAction, err := normalizeHighRiskAction(*actionFlag)
	if err != nil {
		return err
	}
	if err := operatorauth.ValidateCanonicalSingleLineField("target", *targetFlag, true); err != nil {
		return usageErrorf("verify action: %v", err)
	}
	canonicalGate, err := canonicalGateTopic(*gateFlag)
	if err != nil {
		return usageErrorf("verify action: %v", err)
	}
	resolve := func() (contextResolution, error) {
		return resolveVerifyActionContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	}
	ctx, err := resolve()
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	ctx, admission, err := acquireRevalidatedContextWriter(ctx, false, resolve)
	if err != nil {
		return err
	}
	defer admission.close()
	return executeVerifyActionInSelectedContext(verifyActionExecution{
		ProjectDir:        ctx.ProjectDir,
		Profile:           ctx.Profile,
		Session:           ctx.Session,
		Gate:              canonicalGate,
		Action:            canonicalAction,
		Target:            *targetFlag,
		To:                *toFlag,
		Out:               os.Stdout,
		JSON:              *jsonOut,
		EmitAuthorization: *emitAuthorization,
		SigningKeyFile:    *signingKeyFile,
		AuthorizationOut:  *authorizationOut,
	}, selectedReleaseContext(ctx))
}

func catalogActionNames() []string {
	return operatorauth.SupportedActions()
}

type verifyActionKindsData struct {
	TaxonomyVersion      int      `json:"taxonomy_version"`
	Actions              []string `json:"actions"`
	CustomActionGuidance string   `json:"custom_action_guidance"`
}

const verifyActionCustomGuidance = "Custom actions are not hard-verifier kinds. They require an explicitly bound operator gate with exact Action/Target plus manual verification."

func printVerifyActionKinds(jsonOut bool) error {
	data := verifyActionKindsData{TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Actions: operatorauth.SupportedActions(), CustomActionGuidance: verifyActionCustomGuidance}
	if jsonOut {
		return printJSONEnvelope("verify_action_kinds", data)
	}
	for _, action := range data.Actions {
		fmt.Fprintln(os.Stdout, action)
	}
	fmt.Fprintln(os.Stdout)
	fmt.Fprintln(os.Stdout, data.CustomActionGuidance)
	return nil
}

func executeVerifyAction(x verifyActionExecution) error {
	out := x.Out
	if out == nil {
		out = os.Stdout
	}
	now := x.Now
	if now == nil {
		now = time.Now
	}
	action, err := normalizeHighRiskAction(x.Action)
	if err != nil {
		return err
	}
	target := x.Target
	if err := operatorauth.ValidateCanonicalSingleLineField("target", target, true); err != nil {
		return usageErrorf("verify action: %v", err)
	}
	gate, err := canonicalGateTopic(x.Gate)
	if err != nil {
		return usageErrorf("verify action: %v", err)
	}
	session := strings.TrimSpace(x.Session)
	if session == "" {
		return usageErrorf("verify action requires --session")
	}
	profile := squadnamespace.NormalizeProfile(x.Profile)
	baseRoot := strings.TrimSpace(x.BaseRoot)
	if baseRoot == "" {
		resolve := x.ResolveBaseRoot
		if resolve == nil {
			resolve = scanBaseRootForProject
		}
		baseRoot, err = resolve(x.ProjectDir)
		if err != nil {
			return fmt.Errorf("resolve AMQ base root: %w", err)
		}
	}
	snap, err := state.Build(x.ProjectDir, baseRoot, x.Probe)
	if err != nil {
		return fmt.Errorf("scan AMQ base root: %w", err)
	}
	sess, ok := findThreadsSession(snap.Sessions, profile, session)
	if !ok {
		return fmt.Errorf("session %q for profile %q not found under %s", session, profile, baseRoot)
	}
	cfg, err := team.ReadProfile(x.ProjectDir, profile)
	if err != nil {
		return fmt.Errorf("read team profile: %w", err)
	}
	operatorHandle := team.EffectiveOperator(cfg).Handle
	msgs, warnings := state.ScanSessionMessages(sess.Root, now)
	if len(warnings) > 0 {
		return usageErrorf("verify action message scan degraded; refusing authorization")
	}
	result := decideTypedVerifyActionWithPolicy(msgs, gate, action, target, operatorHandle, cfg, session, x.ProjectDir, profile)
	return emitVerifyActionResult(out, result, x.To, x.JSON)
}

// executeVerifyActionInSelectedContext is the admitted-context execution seam
// used by the CLI. It never rediscovers a conventional AMQ root after context
// admission. Release-owned questions are evaluated only while their immutable
// compound-release claim remains guarded and current.
func executeVerifyActionInSelectedContext(x verifyActionExecution, selected cliReleaseSelectedContext) error {
	out := x.Out
	if out == nil {
		out = os.Stdout
	}
	now := x.Now
	if now == nil {
		now = time.Now
	}
	action, err := normalizeHighRiskAction(x.Action)
	if err != nil {
		return err
	}
	if err := operatorauth.ValidateCanonicalSingleLineField("target", x.Target, true); err != nil {
		return usageErrorf("verify action: %v", err)
	}
	gate, err := canonicalGateTopic(x.Gate)
	if err != nil {
		return usageErrorf("verify action: %v", err)
	}
	if err := validateCLIReleaseSelectedContext(selected); err != nil {
		return usageErrorf("verify action selected context: %v", err)
	}
	if selected.ProjectDir != x.ProjectDir || !squadnamespace.ProfilesEqual(selected.Profile, x.Profile) || selected.Session != x.Session {
		return usageErrorf("verify action selected context does not match admitted command tuple")
	}
	cfg, err := team.ReadProfile(selected.ProjectDir, selected.Profile)
	if err != nil {
		return fmt.Errorf("read team profile: %w", err)
	}
	operatorHandle := team.EffectiveOperator(cfg).Handle
	question, err := latestGateQuestionInSelectedContext(selected, gate, now)
	if err != nil {
		return emitVerifyActionResult(out, verifyActionFailure(action, x.Target, gate, actionDecisionPending, "selected_question_unavailable", err.Error()), x.To, x.JSON)
	}
	classification, err := classifyCLIReleaseQuestion(selected, question)
	if err != nil {
		return emitVerifyActionResult(out, verifyActionFailure(action, x.Target, gate, actionDecisionPending, "compound_release_classification_failed", err.Error()), x.To, x.JSON)
	}

	var result verifyActionResult
	signedInsideGuard := false
	var authorizationErr error
	switch classification.Disposition {
	case cliReleaseDomainOrdinary:
		current, currentErr := latestGateQuestionInSelectedContext(selected, gate, now)
		if currentErr != nil || !securityMessageEqual(current, question) {
			detail := "selected ordinary gate changed before evaluation"
			if currentErr != nil {
				detail = currentErr.Error()
			}
			result = verifyActionFailure(action, x.Target, gate, actionDecisionPending, "selected_question_changed", detail)
			break
		}
		msgs, warnings := state.ScanSessionMessages(selected.SessionRoot, now)
		if len(warnings) != 0 {
			result = verifyActionFailure(action, x.Target, gate, actionDecisionPending, "selected_message_scan_degraded", "selected AMQ message scan is degraded")
			break
		}
		result = decideTypedVerifyActionWithAuthorityPolicy(msgs, gate, action, x.Target, operatorHandle, cfg, selected.Session, selected.ProjectDir, selected.Profile, true)
	case cliReleaseDomainReleaseOwned:
		if !classification.Eligible {
			result = verifyActionFailure(action, x.Target, gate, actionDecisionPending, "compound_release_claim_ineligible", classification.Reason)
			break
		}
		guarded, guardErr := classification.NewGuardedUse()
		if guardErr != nil {
			result = verifyActionFailure(action, x.Target, gate, actionDecisionPending, "compound_release_guard_unavailable", guardErr.Error())
			break
		}
		guardErr = guarded.Run(func(observation cliReleaseGuardObservation) error {
			result = decideTypedVerifyActionWithAuthorityPolicy(observation.Messages(), gate, action, x.Target, operatorHandle, cfg, selected.Session, selected.ProjectDir, selected.Profile, false)
			if result.Decision == actionDecisionApproved && classification.Claim != nil {
				result.Compound = &operatorauth.AuthorizationCompoundEvidence{
					ReleaseID: classification.Marker.ReleaseID, ParentGate: classification.Claim.Scope.ParentGate,
					SeriesID: classification.Claim.SeriesID, GenerationID: classification.Claim.GenerationID,
					PreparedManifestID: classification.Claim.PreparedManifestID, ActiveManifestID: classification.Claim.ActiveManifestID,
					Role: classification.Claim.Role, ManifestSHA256: classification.Claim.ActiveSHA256,
				}
				if x.EmitAuthorization {
					authorizationErr = emitSignedVerifyAuthorization(&result, selected, x.SigningKeyFile, x.AuthorizationOut, now())
					signedInsideGuard = authorizationErr == nil
					return authorizationErr
				}
			}
			return nil
		})
		if authorizationErr != nil {
			result.EnvelopeEligible = false
			result.EnvelopeEligibilityReason = "authorization_emission_failed"
			captureVerifyActionResult(x.Capture, result)
			_ = emitVerifyActionResult(out, result, x.To, x.JSON)
			return authorizationErr
		}
		if guardErr != nil {
			result = verifyActionFailure(action, x.Target, gate, actionDecisionPending, "compound_release_guard_failed", guardErr.Error())
		}
	default:
		result = verifyActionFailure(action, x.Target, gate, actionDecisionPending, "compound_release_domain_unknown", classification.Reason)
	}
	if result.Decision == actionDecisionApproved && x.EmitAuthorization && !signedInsideGuard {
		if err := emitSignedVerifyAuthorization(&result, selected, x.SigningKeyFile, x.AuthorizationOut, now()); err != nil {
			result.EnvelopeEligible = false
			result.EnvelopeEligibilityReason = "authorization_emission_failed"
			captureVerifyActionResult(x.Capture, result)
			_ = emitVerifyActionResult(out, result, x.To, x.JSON)
			return err
		}
	}
	captureVerifyActionResult(x.Capture, result)
	return emitVerifyActionResult(out, result, x.To, x.JSON)
}

func captureVerifyActionResult(dst *verifyActionResult, src verifyActionResult) {
	if dst == nil {
		return
	}
	copyResult := src
	if src.Namespace != nil {
		namespace := *src.Namespace
		copyResult.Namespace = &namespace
	}
	if src.Compound != nil {
		compound := *src.Compound
		copyResult.Compound = &compound
	}
	if src.approval != nil {
		approval := *src.approval
		copyResult.approval = &approval
	}
	copyResult.Failures = append([]verifyMergeFailure(nil), src.Failures...)
	*dst = copyResult
}

func verifyActionFailure(action, target, gate, decision, code, detail string) verifyActionResult {
	if strings.TrimSpace(detail) == "" {
		detail = code
	}
	return verifyActionResult{Action: action, Target: target, Gate: gate, Decision: decision, Failures: []verifyMergeFailure{{Code: code, Detail: detail}}}
}

func emitVerifyActionResult(out io.Writer, result verifyActionResult, to string, jsonOut bool) error {
	annotateVerifyActionEligibility(&result)
	if jsonOut {
		if err := writeJSONEnvelope(out, "verify_action", result); err != nil {
			return err
		}
		return verifyActionErr(result)
	}
	renderVerifyAction(out, result, to)
	return verifyActionErr(result)
}

func annotateVerifyActionEligibility(result *verifyActionResult) {
	if result.EnvelopeEligible && result.AuthorizationID != "" && result.AuthorizationPath != "" {
		result.TypedAuthority = true
		result.TypedEligible = true
		result.EnvelopeEligibilityReason = "signed_authorization_emitted"
		return
	}
	result.EnvelopeEligible = false
	if result.Decision == actionDecisionApproved && (result.ApprovalSource == "human" || result.ApprovalSource == "self_operator") {
		result.TypedAuthority = true
		result.TypedEligible = true
		result.EnvelopeEligibilityReason = "signer_unconfigured"
		return
	}
	result.TypedAuthority = false
	result.TypedEligible = false
	if len(result.Failures) > 0 && result.Failures[0].Code != "" {
		result.EnvelopeEligibilityReason = result.Failures[0].Code
		return
	}
	result.EnvelopeEligibilityReason = "typed_authority_not_approved"
}

func verifyActionOperatorHandle(projectDir, profile string) (string, error) {
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		if os.IsNotExist(err) {
			return team.DefaultOperatorHandle, nil
		}
		return "", fmt.Errorf("read team profile: %w", err)
	}
	op := team.EffectiveOperator(t)
	if !op.Enabled {
		return "", usageErrorf("verify action requires an enabled operator gate for profile %q", profile)
	}
	if strings.TrimSpace(op.Handle) == "" {
		return team.DefaultOperatorHandle, nil
	}
	return strings.TrimSpace(op.Handle), nil
}

func normalizeHighRiskAction(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", usageErrorf("verify action requires --action")
	}
	canonical, err := operatorauth.CanonicalAction(raw)
	if err == nil {
		return canonical, nil
	}
	allowed := operatorauth.SupportedActions()
	return "", usageErrorf("unsupported --action %q; run 'amq-squad verify action --list-kinds' for canonical hard-verifier kinds (%s); custom actions are not mapped to built-in kinds and require an explicitly bound operator gate with exact action/target plus manual verification", raw, strings.Join(allowed, ", "))
}

type typedHumanEvidenceResolution struct {
	Disposition string
	Reason      string
	Request     operatorauth.GateRequestContext
	Binding     operatorauth.Binding
}

func resolveTypedHumanEvidence(projectDir, profile, session, gate, humanHandle string, cfg team.Team, question state.Message, answer *state.Message) typedHumanEvidenceResolution {
	result := typedHumanEvidenceResolution{Disposition: actionDecisionPending}
	if question.Kind != state.KindQuestion {
		result.Reason = "typed_gate_message_kind_invalid"
		return result
	}
	if !question.AuthorizationRequestPresent {
		result.Disposition, result.Reason = actionDecisionUnbound, "legacy_gate_diagnostic_only"
		return result
	}
	if !question.AuthorizationRequestValid || question.AuthorizationRequest == nil {
		result.Reason = "typed_gate_malformed"
		return result
	}
	if err := validateTypedQuestionRouting(cfg, session, humanHandle, question); err != nil {
		result.Reason = "typed_gate_routing_invalid"
		return result
	}
	result.Request = *question.AuthorizationRequest
	if err := validateAuthorizationRequestNamespace(projectDir, profile, session, result.Request); err != nil {
		result.Reason = "typed_gate_namespace_stale"
		return result
	}
	if err := validateTypedAuthorityBody(question, result.Request); err != nil {
		result.Reason = "typed_gate_body_malformed"
		return result
	}
	result.Binding = operatorauth.Binding{GateKind: result.Request.GateKind, Action: result.Request.Action, Target: result.Request.Target}
	if answer == nil {
		result.Reason = "gate_pending"
		return result
	}
	if answer.Thread != gate || answer.From != humanHandle || answer.Kind != state.KindAnswer || len(answer.To) != 1 || answer.To[0] != question.From || !messageAfter(*answer, question) {
		result.Reason = "human_answer_actor_or_order_mismatch"
		return result
	}
	decision := classifyTypedDecision(authoritySubject(*answer), gate)
	if decision != actionDecisionApproved && decision != actionDecisionDenied {
		result.Reason = "answer_not_decision"
		return result
	}
	if err := validateTypedAuthorityBody(*answer, result.Request); err != nil {
		result.Reason = "answer_body_malformed"
		return result
	}
	if !answer.ApprovalPresent || !answer.ApprovalValid || answer.Approval == nil {
		result.Reason = "human_approval_context_missing_or_malformed"
		return result
	}
	if answer.Approval.SchemaVersion != operatorauth.ApprovalSchemaVersion {
		result.Reason = "legacy_approval_v1_diagnostic_only"
		return result
	}
	if !validTypedHumanApproval(*answer, humanHandle, question.ID, result.Binding, result.Request.Action, result.Request.Target) {
		result.Reason = "human_approval_tuple_mismatch"
		return result
	}
	if answer.Approval.Note != result.Request.Note {
		result.Reason = "human_approval_note_mismatch"
		return result
	}
	if err := validateApprovalReceipt(projectDir, profile, session, gate, *answer, *answer.Approval); err != nil {
		result.Reason = "human_receipt_invalid"
		return result
	}
	result.Disposition, result.Reason = decision, decision
	return result
}

func validateTypedQuestionRouting(cfg team.Team, session, operatorHandle string, question state.Message) error {
	if len(question.To) != 1 || question.To[0] != operatorHandle {
		return fmt.Errorf("typed question must be addressed only to configured operator")
	}
	for _, member := range cfg.Members {
		if memberHandle(member) == question.From && member.Session == session {
			return nil
		}
	}
	return fmt.Errorf("typed question requester is not a roster member in this session")
}

func decideTypedVerifyActionWithPolicy(msgs []state.Message, gate, action, target, humanHandle string, cfg team.Team, session, projectDir, profile string) verifyActionResult {
	return decideTypedVerifyActionWithAuthorityPolicy(msgs, gate, action, target, humanHandle, cfg, session, projectDir, profile, true)
}

func decideTypedVerifyActionWithAuthorityPolicy(msgs []state.Message, gate, action, target, humanHandle string, cfg team.Team, session, projectDir, profile string, allowSelf bool) verifyActionResult {
	result := verifyActionResult{Action: action, Target: target, Gate: gate, Decision: actionDecisionNoGate}
	var conflict bool
	msgs, conflict = dedupeSecurityMessages(msgs)
	if conflict {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "duplicate_message_conflict", Detail: "conflicting copies share a message id"}}
		return result
	}
	question := latestGateQuestionCandidate(msgs, gate)
	if question == nil {
		result.Failures = []verifyMergeFailure{{Code: "no_matching_gate", Detail: "no gate question exists on this thread"}}
		return result
	}
	result.MessageID = question.ID
	result.QuestionMessageID = question.ID
	result.QuestionCreatedAt = question.Created.UTC().Format(time.RFC3339Nano)
	questionEvidence := resolveTypedHumanEvidence(projectDir, profile, session, gate, humanHandle, cfg, *question, nil)
	if questionEvidence.Reason != "gate_pending" {
		result.Decision = questionEvidence.Disposition
		result.Failures = []verifyMergeFailure{{Code: questionEvidence.Reason, Detail: questionEvidence.Reason}}
		return result
	}
	request := questionEvidence.Request
	requestNamespace := request.Namespace
	result.Namespace = &requestNamespace
	result.Note = request.Note
	result.GateKind = request.GateKind
	capability, err := operatorauth.ValidateGateAction(request.GateKind, request.Action)
	if err != nil || capability.Action != action || request.Target != target {
		result.Decision = actionDecisionUnbound
		result.Failures = []verifyMergeFailure{{Code: "typed_gate_binding_mismatch", Detail: "typed request does not exactly bind the requested action and target"}}
		return result
	}

	facts := make([]operatorauth.MessageFact, 0)
	reasons := make(map[string]string)
	for i := range msgs {
		msg := msgs[i]
		if msg.Thread != gate || msg.From != humanHandle || !messageAfter(msg, *question) {
			continue
		}
		evidence := resolveTypedHumanEvidence(projectDir, profile, session, gate, humanHandle, cfg, *question, &msg)
		decision := evidence.Disposition
		bound := decision == actionDecisionApproved || decision == actionDecisionDenied
		reasons[msg.ID] = evidence.Reason
		facts = append(facts, operatorauth.MessageFact{ID: msg.ID, From: msg.From, Kind: string(msg.Kind), Decision: decision, Bound: bound, After: true, Order: int64(i + 1)})
	}
	precedence := operatorauth.ResolvePrecedence(facts, humanHandle, "")
	if precedence.Barrier {
		result.Decision = actionDecisionPending
		result.AnsweredBy, result.MessageID, result.ApprovalSource = humanHandle, precedence.MessageID, "human"
		reason := reasons[precedence.MessageID]
		if reason == "" {
			reason = "human_intervention_pending"
		}
		result.Failures = []verifyMergeFailure{{Code: reason, Detail: reason}}
		return result
	}
	if precedence.Decision == actionDecisionApproved || precedence.Decision == actionDecisionDenied {
		result.Decision, result.AnsweredBy, result.MessageID, result.ApprovalSource = precedence.Decision, humanHandle, precedence.MessageID, "human"
		result.AnswerMessageID = precedence.MessageID
		result.AnsweredByRole = "operator"
		for i := range msgs {
			if msgs[i].ID == precedence.MessageID {
				result.AnswerCreatedAt = msgs[i].Created.UTC().Format(time.RFC3339Nano)
				if msgs[i].Approval != nil {
					approval := *msgs[i].Approval
					result.approval = &approval
				}
				break
			}
		}
		if precedence.Decision == actionDecisionDenied {
			result.Failures = []verifyMergeFailure{{Code: "gate_denied", Detail: "human operator denied the bound gate"}}
		}
		return result
	}
	if !allowSelf {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "gate_pending", Detail: "release-owned authorization requires a valid human operator answer"}}
		return result
	}

	view := team.EffectiveSelfOperator(cfg, session)
	if !view.Enabled || operatorauth.Evaluate(request.GateKind, request.Action, view.AllowedGateKinds) != nil {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "gate_pending", Detail: "typed gate has no valid human answer and self policy does not authorize it"}}
		return result
	}
	var selfAnswer *state.Message
	for i := range msgs {
		msg := msgs[i]
		if msg.Thread == gate && msg.Kind == state.KindAnswer && msg.From == view.LeadHandle && messageAfter(msg, *question) && (selfAnswer == nil || messageAfter(msg, *selfAnswer)) {
			copy := msg
			selfAnswer = &copy
		}
	}
	if selfAnswer == nil || !validTypedSelfAnswerEnvelope(*selfAnswer, *question, request, gate) {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "self_answer_envelope_invalid", Detail: "self answer has invalid kind, routing, subject, or rendered binding"}}
		return result
	}
	if !selfAnswer.ApprovalPresent || !selfAnswer.ApprovalValid || selfAnswer.Approval == nil {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "self_approval_context_invalid", Detail: "self answer has missing or malformed v2 typed approval context"}}
		return result
	}
	a := *selfAnswer.Approval
	if a.SchemaVersion != operatorauth.ApprovalSchemaVersion || a.TaxonomyVersion != operatorauth.ActionTaxonomyVersion || a.Source != "self_operator" || !a.SelfApproved || a.AnsweredByHandle != view.LeadHandle || a.AnsweredByRole != view.LeadRole || a.QuestionMessageID != question.ID || a.GateKind != request.GateKind || a.Action != request.Action || a.Target != request.Target || a.Note != request.Note {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "self_approval_forged", Detail: "typed self approval does not exactly match actor, request, action, target, and note"}}
		return result
	}
	if a.PolicyRevision != view.PolicyRevision || a.PolicyHash != view.PolicyHash {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "self_policy_revoked", Detail: "current self policy revision/hash differs from the answer"}}
		return result
	}
	if err := validateApprovalReceipt(projectDir, profile, session, gate, *selfAnswer, a); err != nil {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "self_receipt_invalid", Detail: err.Error()}}
		return result
	}
	if request.GateKind != operatorauth.GateMerge {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "self_preflight_unimplemented", Detail: "self preflight is implemented only for merge capabilities"}}
		return result
	}
	if err := revalidateSelfApprovalEvidence(projectDir, a, target); err != nil {
		result.Decision = actionDecisionPending
		result.Failures = []verifyMergeFailure{{Code: "self_preflight_stale", Detail: err.Error()}}
		return result
	}
	actor, err := verifiedCurrentRosterActor(projectDir, profile, session, cfg)
	if err != nil || actor.Handle == view.LeadHandle {
		result.Decision = actionDecisionPending
		detail := "self-approving lead cannot execute the merge"
		if err != nil {
			detail = err.Error()
		}
		result.Failures = []verifyMergeFailure{{Code: "self_merge_actor_conflict", Detail: detail}}
		return result
	}
	approval := a
	return verifyActionResult{Action: action, Target: target, Gate: gate, GateKind: request.GateKind, Decision: actionDecisionApproved, AnsweredBy: view.LeadHandle, MessageID: selfAnswer.ID, ApprovalSource: "self_operator", SelfApproved: true,
		QuestionMessageID: question.ID, AnswerMessageID: selfAnswer.ID, QuestionCreatedAt: question.Created.UTC().Format(time.RFC3339Nano), AnswerCreatedAt: selfAnswer.Created.UTC().Format(time.RFC3339Nano), AnsweredByRole: view.LeadRole,
		Namespace: &requestNamespace, Note: request.Note, approval: &approval}
}

func validTypedSelfAnswerEnvelope(answer, question state.Message, request operatorauth.GateRequestContext, gate string) bool {
	return answer.Thread == gate && answer.Kind == state.KindAnswer && len(answer.To) == 1 && answer.To[0] == question.From &&
		classifyTypedDecision(authoritySubject(answer), gate) == actionDecisionApproved &&
		validateTypedAuthorityBody(answer, request) == nil && messageAfter(answer, question)
}

func authoritySubject(msg state.Message) string {
	if msg.AuthorityRaw {
		return msg.RawSubject
	}
	return msg.Subject
}

func authorityBody(msg state.Message) string {
	if msg.AuthorityRaw {
		return msg.RawBody
	}
	return msg.Body
}

func validateTypedAuthorityBody(msg state.Message, request operatorauth.GateRequestContext) error {
	body := authorityBody(msg)
	if strings.TrimSpace(body) != body {
		return fmt.Errorf("typed body has non-canonical outer whitespace")
	}
	return operatorauth.ValidateTypedRenderedBinding(body, request)
}

func latestGateQuestionCandidate(msgs []state.Message, gate string) *state.Message {
	var latest *state.Message
	for i := range msgs {
		msg := msgs[i]
		if msg.Thread == gate && msg.Kind == state.KindQuestion && (latest == nil || messageAfter(msg, *latest)) {
			copy := msg
			latest = &copy
		}
	}
	return latest
}

func revalidateSelfApprovalEvidence(projectDir string, approval operatorauth.ApprovalContext, target string) error {
	path := strings.TrimSpace(approval.PreflightPath)
	if path == "" {
		return fmt.Errorf("approval omitted preflight path")
	}
	rel, err := filepath.Rel(projectDir, path)
	evidencePrefix := filepath.Join(team.DirName, "evidence") + string(os.PathSeparator)
	if err != nil || !strings.HasPrefix(rel, evidencePrefix) {
		return fmt.Errorf("preflight path is outside project evidence namespace")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(b)
	if fmt.Sprintf("sha256:%x", sum) != approval.PreflightSHA256 {
		return fmt.Errorf("preflight digest changed")
	}
	var evidence verifyMergeEvidence
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&evidence); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("preflight evidence contains trailing JSON values")
	}
	if result := validateVerifyMergeEvidence(evidence); !result.OK || evidence.Base == "" {
		return fmt.Errorf("verify merge evidence is no longer valid")
	}
	parsed, parseErr := operatorauth.ParseMergeTarget(target)
	if parseErr != nil || parsed.Subject != evidence.Subject || parsed.Head != evidence.HeadSHA || parsed.Base != evidence.Base {
		return fmt.Errorf("preflight no longer binds exact PR/head/base")
	}
	return nil
}

func validateApprovalReceipt(projectDir, profile, session, gate string, answer state.Message, approval operatorauth.ApprovalContext) error {
	path := selfApprovalReceiptPath(projectDir, profile, session, gate, approval.QuestionMessageID, answer.ID)
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	receipt, err := operatorauth.DecodeReceipt(b)
	if err != nil {
		return err
	}
	decision := classifyTypedDecision(authoritySubject(answer), gate)
	if receipt.SchemaVersion != operatorauth.ReceiptSchemaVersion || receipt.TaxonomyVersion != operatorauth.ActionTaxonomyVersion ||
		receipt.Gate != gate || receipt.Decision != decision || receipt.AnswerMessageID != answer.ID ||
		receipt.QuestionMessageID != approval.QuestionMessageID || receipt.AnsweredBy != approval.AnsweredByHandle ||
		receipt.ApprovalSource != approval.Source || receipt.SelfApproved != approval.SelfApproved ||
		receipt.GateKind != approval.GateKind || receipt.Action != approval.Action || receipt.Target != approval.Target || receipt.Note != approval.Note ||
		receipt.PolicyRevision != approval.PolicyRevision || receipt.PolicyHash != approval.PolicyHash ||
		receipt.Preflight.Kind != approval.PreflightKind || receipt.Preflight.Path != approval.PreflightPath || receipt.Preflight.SHA256 != approval.PreflightSHA256 ||
		receipt.Preflight.OK != (approval.PreflightSHA256 != "") {
		return fmt.Errorf("v2 receipt does not match typed approval")
	}
	return nil
}

func validTypedHumanApproval(msg state.Message, humanHandle, questionID string, binding operatorauth.Binding, action, target string) bool {
	if !msg.ApprovalPresent || !msg.ApprovalValid || msg.Approval == nil {
		return false
	}
	a := msg.Approval
	capability, err := operatorauth.ValidateGateAction(a.GateKind, a.Action)
	return err == nil && a.SchemaVersion == operatorauth.ApprovalSchemaVersion && a.TaxonomyVersion == operatorauth.ActionTaxonomyVersion && a.Source == "human" && !a.SelfApproved && a.QuestionMessageID == questionID && a.AnsweredByHandle == humanHandle && a.AnsweredByRole == "operator" && capability.GateKind == binding.GateKind && capability.Action == action && a.Target == target
}

func canonicalGateActionMatches(kind, action, wantKind, wantAction string) bool {
	capability, err := operatorauth.ValidateGateAction(kind, action)
	return err == nil && capability.GateKind == wantKind && capability.Action == wantAction
}

func dedupeSecurityMessages(msgs []state.Message) ([]state.Message, bool) {
	byID := map[string]state.Message{}
	conflict := false
	for _, msg := range msgs {
		if msg.ID == "" {
			continue
		}
		if existing, ok := byID[msg.ID]; ok {
			if !securityMessageEqual(existing, msg) {
				conflict = true
			}
			if msg.Path < existing.Path {
				byID[msg.ID] = msg
			}
			continue
		}
		byID[msg.ID] = msg
	}
	out := make([]state.Message, 0, len(byID))
	for _, msg := range byID {
		out = append(out, msg)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Created.Equal(out[j].Created) {
			return out[i].ID < out[j].ID
		}
		return out[i].Created.Before(out[j].Created)
	})
	return out, conflict
}

func securityMessageEqual(a, b state.Message) bool {
	return a.ID == b.ID && a.From == b.From && slices.Equal(a.To, b.To) &&
		slices.Equal(a.RawTo, b.RawTo) && a.ToPresent == b.ToPresent && a.ToArrayValid == b.ToArrayValid && a.ToRaw == b.ToRaw &&
		a.RawThread == b.RawThread && a.Thread == b.Thread && a.Subject == b.Subject && a.RawSubject == b.RawSubject &&
		a.RawCreated == b.RawCreated && a.Created.Equal(b.Created) && a.Priority == b.Priority &&
		a.Kind == b.Kind && a.ReplyTo == b.ReplyTo && slices.Equal(a.Refs, b.Refs) && a.RefsPresent == b.RefsPresent && a.RefsValid == b.RefsValid && a.RefsRaw == b.RefsRaw && slices.Equal(a.Labels, b.Labels) &&
		a.Orchestrator == b.Orchestrator && a.FromProject == b.FromProject &&
		a.ReplyProject == b.ReplyProject && a.OrchestratorEvent == b.OrchestratorEvent &&
		a.ExternalTaskID == b.ExternalTaskID && a.Body == b.Body && a.RawBody == b.RawBody && a.AuthorityRaw == b.AuthorityRaw && a.SchemaOK == b.SchemaOK &&
		securityContextEqual(a, b)
}

func securityContextEqual(a, b state.Message) bool {
	aContext, aErr := json.Marshal(a.Context)
	bContext, bErr := json.Marshal(b.Context)
	if aErr != nil || bErr != nil || string(aContext) != string(bContext) {
		return false
	}
	aApproval, aErr := json.Marshal(a.Approval)
	bApproval, bErr := json.Marshal(b.Approval)
	if aErr != nil || bErr != nil || string(aApproval) != string(bApproval) || a.ApprovalPresent != b.ApprovalPresent || a.ApprovalValid != b.ApprovalValid || a.ApprovalError != b.ApprovalError {
		return false
	}
	aRequest, aErr := json.Marshal(a.AuthorizationRequest)
	bRequest, bErr := json.Marshal(b.AuthorizationRequest)
	return aErr == nil && bErr == nil && string(aRequest) == string(bRequest) && a.AuthorizationRequestPresent == b.AuthorizationRequestPresent && a.AuthorizationRequestValid == b.AuthorizationRequestValid && a.AuthorizationRequestError == b.AuthorizationRequestError
}

func strictBindingMatches(text, action, target string) bool {
	action = normalizeBindingValue(action)
	target = normalizeBindingValue(target)
	values := bindingFields(text)
	if normalizeBindingValue(values["action"]) == action && normalizeBindingValue(values["target"]) == target {
		return true
	}
	return false
}

func bindingFields(text string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			key, val, ok = strings.Cut(line, "=")
		}
		if !ok {
			continue
		}
		k := strings.ToLower(strings.TrimSpace(key))
		if k == "action" || k == "target" {
			out[k] = strings.TrimSpace(val)
		}
	}
	return out
}

func normalizeBindingValue(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func classifyDecision(text string) string {
	for _, line := range strings.Split(decisionText(text), "\n") {
		line = strings.TrimSpace(line)
		if hasDecisionPrefixToken(line, "denied") {
			return actionDecisionDenied
		}
	}
	for _, line := range strings.Split(decisionText(text), "\n") {
		line = strings.TrimSpace(line)
		if hasDecisionPrefixToken(line, "approved") {
			return actionDecisionApproved
		}
	}
	return actionDecisionPending
}

func classifyTypedDecision(subject, gate string) string {
	if err := operatorauth.ValidateCanonicalSingleLineField("typed answer subject", subject, true); err != nil {
		return actionDecisionPending
	}
	suffix := strings.TrimPrefix(gate, "gate/")
	switch subject {
	case "APPROVED: " + suffix:
		return actionDecisionApproved
	case "DENIED: " + suffix:
		return actionDecisionDenied
	default:
		return actionDecisionPending
	}
}

func hasDecisionPrefixToken(line, token string) bool {
	if !strings.HasPrefix(line, token) {
		return false
	}
	if len(line) == len(token) {
		return true
	}
	return line[len(token)] == ':'
}

func decisionText(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}

func messageAfter(a, b state.Message) bool {
	if !a.Created.Equal(b.Created) {
		return a.Created.After(b.Created)
	}
	return a.ID > b.ID
}

func renderVerifyAction(out io.Writer, result verifyActionResult, to string) {
	fmt.Fprintf(out, "action authorization %s for %s: %s\n", result.Decision, result.Action, result.Target)
	if result.Gate != "" {
		fmt.Fprintf(out, "gate: %s\n", result.Gate)
	}
	if result.AnsweredBy != "" {
		fmt.Fprintf(out, "answered_by: %s\n", result.AnsweredBy)
	}
	if result.MessageID != "" {
		fmt.Fprintf(out, "message_id: %s\n", result.MessageID)
	}
	for _, f := range result.Failures {
		fmt.Fprintf(out, "- %s: %s\n", f.Code, f.Detail)
	}
	if result.Decision != actionDecisionApproved {
		target := strings.TrimSpace(to)
		if target == "" {
			target = "<lead>"
		}
		fmt.Fprintf(out, "Resolve with: amq-squad operator answer --gate %s --to %s --approved --reason \"Action: %s\\nTarget: %s\"\n", strings.TrimPrefix(result.Gate, "gate/"), target, result.Action, result.Target)
	}
}

func verifyActionErr(result verifyActionResult) error {
	switch result.Decision {
	case actionDecisionApproved:
		return nil
	case actionDecisionPending, actionDecisionDenied, actionDecisionNoGate, actionDecisionUnbound:
		return &ActionDecisionError{Decision: result.Decision, Message: "action authorization " + result.Decision}
	default:
		return &ActionDecisionError{Decision: actionDecisionPending, Message: "action authorization " + result.Decision}
	}
}
