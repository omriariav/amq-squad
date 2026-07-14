package cli

import (
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
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

var highRiskActionAliases = map[string]string{
	"default_branch_push":   "default_branch_push",
	"push_default_branch":   "default_branch_push",
	"protected_branch_push": "protected_branch_push",
	"push_protected_branch": "protected_branch_push",
	"tag":                   "tag",
	"tag_push":              "tag",
	"create_tag":            "tag",
	"push_tag":              "tag",
	"github_release":        "github_release",
	"gh_release":            "github_release",
	"release":               "github_release",
	"external_send":         "external_send",
	"external_message":      "external_send",
}

type verifyActionExecution struct {
	ProjectDir      string
	Profile         string
	Session         string
	Gate            string
	Action          string
	Target          string
	To              string
	BaseRoot        string
	ResolveBaseRoot func(projectDir string) (string, error)
	Probe           state.Probe
	Now             func() time.Time
	Out             io.Writer
	JSON            bool
}

type verifyActionResult struct {
	Action         string               `json:"action"`
	Target         string               `json:"target"`
	Gate           string               `json:"gate"`
	Decision       string               `json:"decision"`
	AnsweredBy     string               `json:"answered_by,omitempty"`
	MessageID      string               `json:"message_id,omitempty"`
	GateKind       string               `json:"gate_kind,omitempty"`
	ApprovalSource string               `json:"approval_source,omitempty"`
	SelfApproved   bool                 `json:"self_approved"`
	Failures       []verifyMergeFailure `json:"failures,omitempty"`
}

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
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad verify action - require a bound operator gate for a high-risk action

Usage:
  amq-squad verify action --project DIR --session S --gate TOPIC --action KIND --target TARGET [--profile P] [--to HANDLE] [--json]

High-risk actions:
  default_branch_push, protected_branch_push, tag, github_release, external_send

Both the gate question and the operator answer must bind to the same action and
target. Put explicit binding text in the gate and answer, for example:
  Action: github_release
  Target: draft v1.41.0 release for omriariav/workspace-cli

Exit codes:
  0 approved
  10 pending
  11 denied
  12 no matching gate
  13 unbound or mismatched operator answer
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("unexpected argument %q", fs.Arg(0))
	}
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	return executeVerifyAction(verifyActionExecution{
		ProjectDir: ctx.ProjectDir,
		Profile:    ctx.Profile,
		Session:    ctx.Session,
		Gate:       *gateFlag,
		Action:     *actionFlag,
		Target:     *targetFlag,
		To:         *toFlag,
		Out:        os.Stdout,
		JSON:       *jsonOut,
	})
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
	target := strings.TrimSpace(x.Target)
	if target == "" {
		return usageErrorf("verify action requires --target")
	}
	gate := normalizeGateTopic(x.Gate)
	if gate == "" {
		return usageErrorf("verify action requires --gate")
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
	result := decideVerifyActionWithPolicy(msgs, gate, action, target, operatorHandle, cfg, session, x.ProjectDir, profile)
	if x.JSON {
		if err := writeJSONEnvelope(out, "verify_action", result); err != nil {
			return err
		}
		return verifyActionErr(result)
	}
	renderVerifyAction(out, result, x.To)
	return verifyActionErr(result)
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
	key := strings.ToLower(strings.TrimSpace(raw))
	key = strings.NewReplacer("-", "_", " ", "_").Replace(key)
	if key == "" {
		return "", usageErrorf("verify action requires --action")
	}
	if canonical, ok := highRiskActionAliases[key]; ok {
		return canonical, nil
	}
	var allowed []string
	seen := map[string]bool{}
	for _, v := range highRiskActionAliases {
		if !seen[v] {
			allowed = append(allowed, v)
			seen[v] = true
		}
	}
	sort.Strings(allowed)
	return "", usageErrorf("unsupported --action %q; use one of: %s", raw, strings.Join(allowed, ", "))
}

func decideVerifyAction(msgs []state.Message, gate, action, target, operatorHandle string) verifyActionResult {
	result := verifyActionResult{Action: action, Target: target, Gate: gate, Decision: actionDecisionNoGate}
	var matchingQuestion *state.Message
	for i := range msgs {
		m := msgs[i]
		if m.Thread != gate {
			continue
		}
		text := m.Subject + "\n" + m.Body
		if m.Kind == state.KindQuestion && strictBindingMatches(text, action, target) {
			if matchingQuestion == nil || messageAfter(m, *matchingQuestion) {
				mm := m
				matchingQuestion = &mm
			}
		}
	}
	if matchingQuestion == nil {
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "no_matching_gate", Detail: "no gate question on this thread binds the requested action and target"})
		return result
	}
	result.MessageID = matchingQuestion.ID

	var latestAnswer *state.Message
	for i := range msgs {
		m := msgs[i]
		if m.Thread != gate || m.Kind != state.KindAnswer || m.From != operatorHandle {
			continue
		}
		if !messageAfter(m, *matchingQuestion) {
			continue
		}
		if latestAnswer == nil || messageAfter(m, *latestAnswer) {
			mm := m
			latestAnswer = &mm
		}
	}
	if latestAnswer == nil {
		result.Decision = actionDecisionPending
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "gate_pending", Detail: "matching gate exists but has no bound operator answer"})
		return result
	}
	text := latestAnswer.Subject + "\n" + latestAnswer.Body
	decision := classifyDecision(text)
	if !strictBindingMatches(text, action, target) {
		if decision != actionDecisionPending {
			result.Decision = actionDecisionUnbound
			result.AnsweredBy = latestAnswer.From
			result.MessageID = latestAnswer.ID
			result.Failures = append(result.Failures, verifyMergeFailure{Code: "answer_not_bound", Detail: "latest operator answer on the gate does not bind the same action and target"})
			return result
		}
		result.Decision = actionDecisionPending
		result.AnsweredBy = latestAnswer.From
		result.MessageID = latestAnswer.ID
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "gate_pending", Detail: "matching gate exists but has no bound operator answer"})
		return result
	}
	result.AnsweredBy = latestAnswer.From
	result.MessageID = latestAnswer.ID
	switch decision {
	case actionDecisionApproved:
		result.Decision = actionDecisionApproved
	case actionDecisionDenied:
		result.Decision = actionDecisionDenied
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "gate_denied", Detail: "operator denied the bound gate"})
	default:
		result.Decision = actionDecisionPending
		result.Failures = append(result.Failures, verifyMergeFailure{Code: "answer_not_decision", Detail: "bound operator answer is neither APPROVED nor DENIED"})
	}
	return result
}

func decideVerifyActionWithPolicy(msgs []state.Message, gate, action, target, humanHandle string, cfg team.Team, session, projectDir, profile string) verifyActionResult {
	var duplicateConflict bool
	msgs, duplicateConflict = dedupeSecurityMessages(msgs)
	if duplicateConflict {
		return verifyActionResult{Action: action, Target: target, Gate: gate, Decision: actionDecisionPending, Failures: []verifyMergeFailure{{Code: "duplicate_message_conflict", Detail: "conflicting copies share a message id"}}}
	}
	legacy := decideVerifyAction(msgs, gate, action, target, humanHandle)
	view := team.EffectiveSelfOperator(cfg, session)
	if !view.Enabled {
		return legacy
	}
	// Self mode applies full-scan human precedence. Human profiles outside self
	// mode returned above retain the pre-#391 latest-answer behavior unchanged.
	var question *state.Message
	for i := range msgs {
		msg := msgs[i]
		if msg.Thread == gate && msg.Kind == state.KindQuestion && (question == nil || messageAfter(msg, *question)) {
			copy := msg
			question = &copy
		}
	}
	if question != nil {
		if !strictBindingMatches(question.Subject+"\n"+question.Body, action, target) {
			return verifyActionResult{Action: action, Target: target, Gate: gate, Decision: actionDecisionNoGate, MessageID: question.ID, Failures: []verifyMergeFailure{{Code: "latest_gate_binding_mismatch", Detail: "latest gate question does not bind the requested action and target"}}}
		}
		strictHumanBinding, _ := operatorauth.ParseStrictBinding(question.Subject + "\n" + question.Body)
		facts := make([]operatorauth.MessageFact, 0)
		for i := range msgs {
			msg := msgs[i]
			if msg.Thread != gate || msg.From != humanHandle || !messageAfter(msg, *question) {
				continue
			}
			decision, bound := actionDecisionPending, false
			text := msg.Subject + "\n" + msg.Body
			if msg.Kind == state.KindAnswer && strictBindingMatches(text, action, target) {
				decision = classifyDecision(text)
				bound = decision == actionDecisionApproved || decision == actionDecisionDenied
			}
			if msg.ApprovalPresent {
				bound = bound && validTypedHumanApproval(msg, humanHandle, question.ID, strictHumanBinding, action, target)
			}
			facts = append(facts, operatorauth.MessageFact{ID: msg.ID, From: msg.From, Kind: string(msg.Kind), Decision: decision, Bound: bound, After: true, Order: int64(i + 1)})
		}
		precedence := operatorauth.ResolvePrecedence(facts, humanHandle, "")
		switch precedence.Decision {
		case actionDecisionDenied:
			return verifyActionResult{Action: action, Target: target, Gate: gate, Decision: actionDecisionDenied, AnsweredBy: humanHandle, MessageID: precedence.MessageID, ApprovalSource: "human", Failures: []verifyMergeFailure{{Code: "gate_denied", Detail: "human operator denied the bound gate"}}}
		case actionDecisionApproved:
			return verifyActionResult{Action: action, Target: target, Gate: gate, Decision: actionDecisionApproved, AnsweredBy: humanHandle, MessageID: precedence.MessageID, ApprovalSource: "human"}
		case actionDecisionPending:
			if precedence.Barrier {
				return verifyActionResult{Action: action, Target: target, Gate: gate, Decision: actionDecisionPending, AnsweredBy: humanHandle, MessageID: precedence.MessageID, ApprovalSource: "human", Failures: []verifyMergeFailure{{Code: "human_intervention_pending", Detail: "human-authored gate message requires a bound human decision"}}}
			}
		}
	}
	var strictQuestion *state.Message
	var binding operatorauth.Binding
	for i := range msgs {
		msg := msgs[i]
		if msg.Thread != gate || msg.Kind != state.KindQuestion || (strictQuestion != nil && !messageAfter(msg, *strictQuestion)) {
			continue
		}
		parsed, err := operatorauth.ParseStrictBinding(msg.Subject + "\n" + msg.Body)
		if err != nil {
			strictQuestion = &msg
			binding = operatorauth.Binding{}
			continue
		}
		copy := msg
		strictQuestion = &copy
		binding = parsed
	}
	if strictQuestion == nil || !binding.Matches(binding.GateKind, action, target) {
		return legacy
	}
	if err := operatorauth.Evaluate(binding.GateKind, action, view.AllowedGateKinds); err != nil {
		legacy.Failures = append(legacy.Failures, verifyMergeFailure{Code: "self_kind_not_allowed", Detail: err.Error()})
		return legacy
	}
	var selfAnswer *state.Message
	for i := range msgs {
		msg := msgs[i]
		if msg.Thread == gate && msg.Kind == state.KindAnswer && msg.From == view.LeadHandle && messageAfter(msg, *strictQuestion) && (selfAnswer == nil || messageAfter(msg, *selfAnswer)) {
			copy := msg
			selfAnswer = &copy
		}
	}
	if selfAnswer == nil {
		return legacy
	}
	if !selfAnswer.ApprovalPresent || !selfAnswer.ApprovalValid || selfAnswer.Approval == nil {
		legacy.Failures = append(legacy.Failures, verifyMergeFailure{Code: "self_approval_context_invalid", Detail: "self answer has missing or malformed typed approval context"})
		return legacy
	}
	a := *selfAnswer.Approval
	if a.Source != "self_operator" || !a.SelfApproved || a.AnsweredByHandle != view.LeadHandle || a.AnsweredByRole != view.LeadRole || a.QuestionMessageID != strictQuestion.ID || a.GateKind != binding.GateKind || operatorauth.NormalizeAction(a.Action) != action || a.Target != target {
		legacy.Failures = append(legacy.Failures, verifyMergeFailure{Code: "self_approval_forged", Detail: "typed self approval does not match actor/question/action/target"})
		return legacy
	}
	if a.PolicyRevision != view.PolicyRevision || a.PolicyHash != view.PolicyHash {
		legacy.Failures = append(legacy.Failures, verifyMergeFailure{Code: "self_policy_revoked", Detail: "current self policy revision/hash differs from the answer"})
		return legacy
	}
	if err := validateSelfApprovalReceipt(projectDir, profile, session, gate, selfAnswer.ID, a); err != nil {
		legacy.Failures = append(legacy.Failures, verifyMergeFailure{Code: "self_receipt_invalid", Detail: err.Error()})
		return legacy
	}
	if err := revalidateSelfApprovalEvidence(projectDir, a, target); err != nil {
		legacy.Failures = append(legacy.Failures, verifyMergeFailure{Code: "self_preflight_stale", Detail: err.Error()})
		return legacy
	}
	if binding.GateKind == operatorauth.GateMerge {
		actor, err := verifiedCurrentRosterActor(projectDir, profile, session, cfg)
		if err != nil {
			legacy.Failures = append(legacy.Failures, verifyMergeFailure{Code: "executor_identity_unverified", Detail: err.Error()})
			return legacy
		}
		if actor.Handle == view.LeadHandle {
			legacy.Failures = append(legacy.Failures, verifyMergeFailure{Code: "self_merge_actor_conflict", Detail: "self-approving lead cannot execute the merge"})
			return legacy
		}
	}
	return verifyActionResult{Action: action, Target: target, Gate: gate, GateKind: binding.GateKind, Decision: actionDecisionApproved, AnsweredBy: view.LeadHandle, MessageID: selfAnswer.ID, ApprovalSource: "self_operator", SelfApproved: true}
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

func validateSelfApprovalReceipt(projectDir, profile, session, gate, answerID string, approval operatorauth.ApprovalContext) error {
	path := filepath.Join(selfApprovalStoreDir(projectDir, profile, session), safeGateFile(gate)+"-"+safeGateFile(answerID)+".receipt.json")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var receipt operatorauth.Receipt
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&receipt); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		return fmt.Errorf("receipt contains trailing JSON values")
	}
	if receipt.Gate != gate || receipt.Decision != "approved" || receipt.AnswerMessageID != answerID || receipt.QuestionMessageID != approval.QuestionMessageID || receipt.AnsweredBy != approval.AnsweredByHandle || receipt.ApprovalSource != "self_operator" || !receipt.SelfApproved || receipt.GateKind != approval.GateKind || receipt.Action != approval.Action || receipt.Target != approval.Target || receipt.PolicyRevision != approval.PolicyRevision || receipt.PolicyHash != approval.PolicyHash || receipt.Preflight.Kind != approval.PreflightKind || receipt.Preflight.Path != approval.PreflightPath || receipt.Preflight.SHA256 != approval.PreflightSHA256 || !receipt.Preflight.OK {
		return fmt.Errorf("receipt does not match typed self approval")
	}
	return nil
}

func validTypedHumanApproval(msg state.Message, humanHandle, questionID string, binding operatorauth.Binding, action, target string) bool {
	if !msg.ApprovalPresent || !msg.ApprovalValid || msg.Approval == nil {
		return false
	}
	a := msg.Approval
	return a.Source == "human" && !a.SelfApproved && a.QuestionMessageID == questionID && a.AnsweredByHandle == humanHandle && a.AnsweredByRole == "operator" && a.GateKind == binding.GateKind && operatorauth.NormalizeAction(a.Action) == action && a.Target == target
}

func dedupeSecurityMessages(msgs []state.Message) ([]state.Message, bool) {
	byID := map[string]state.Message{}
	conflict := false
	for _, msg := range msgs {
		if msg.ID == "" {
			continue
		}
		if existing, ok := byID[msg.ID]; ok {
			if existing.From != msg.From || existing.Thread != msg.Thread || existing.Kind != msg.Kind || existing.Subject != msg.Subject || existing.Body != msg.Body || !securityContextEqual(existing, msg) {
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

func securityContextEqual(a, b state.Message) bool {
	aContext, aErr := json.Marshal(a.Context)
	bContext, bErr := json.Marshal(b.Context)
	if aErr != nil || bErr != nil || string(aContext) != string(bContext) {
		return false
	}
	aApproval, aErr := json.Marshal(a.Approval)
	bApproval, bErr := json.Marshal(b.Approval)
	return aErr == nil && bErr == nil && string(aApproval) == string(bApproval) && a.ApprovalPresent == b.ApprovalPresent && a.ApprovalValid == b.ApprovalValid && a.ApprovalError == b.ApprovalError
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
