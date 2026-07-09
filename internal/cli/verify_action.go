package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
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
	Action     string               `json:"action"`
	Target     string               `json:"target"`
	Gate       string               `json:"gate"`
	Decision   string               `json:"decision"`
	AnsweredBy string               `json:"answered_by,omitempty"`
	MessageID  string               `json:"message_id,omitempty"`
	Failures   []verifyMergeFailure `json:"failures,omitempty"`
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
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	projectDir, err := resolveProjectDirFlag(cwd, *projectFlag, flagWasSet(fs, "project"))
	if err != nil {
		return err
	}
	profile, err := resolveProfileFlag(*profileFlag)
	if err != nil {
		return err
	}
	return executeVerifyAction(verifyActionExecution{
		ProjectDir: projectDir,
		Profile:    profile,
		Session:    *sessionFlag,
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
	operatorHandle, err := verifyActionOperatorHandle(x.ProjectDir, profile)
	if err != nil {
		return err
	}
	msgs, warnings := state.ScanSessionMessages(sess.Root, now)
	if len(warnings) > 0 {
		sort.SliceStable(warnings, func(i, j int) bool { return warnings[i].Path < warnings[j].Path })
	}
	result := decideVerifyAction(msgs, gate, action, target, operatorHandle)
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
		switch {
		case hasDecisionPrefixToken(line, "approved"):
			return actionDecisionApproved
		case hasDecisionPrefixToken(line, "denied"):
			return actionDecisionDenied
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
	r := rune(line[len(token)])
	return !(r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
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
