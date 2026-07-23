// Package act is the operator's preview-first, receipted control layer.
//
// Actions are inert values until Send. Send executes only first-class
// amq-squad operator commands; those commands re-resolve namespace/authority at
// the invocation boundary and persist durable AMQ delivery receipts. The
// package deliberately has no raw `amq send` escape hatch.
package act

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/amqexec"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// Intent identifies the semantic operator action. Every intent maps to a
// first-class amq-squad command that performs its own context and authority
// revalidation immediately before the durable AMQ send.
type Intent string

const (
	IntentReply     Intent = "reply"
	IntentApprove   Intent = "approve"
	IntentDeny      Intent = "deny"
	IntentMessage   Intent = "message"
	IntentBroadcast Intent = "broadcast"
)

// Context pins an operator action to one project/profile/session tuple.
type Context struct {
	Project string
	Profile string
	Session string
}

// Action is inert, previewable operator intent. Constructing or rendering an
// Action performs no I/O. Send is the sole mutating boundary.
type Action struct {
	Intent  Intent
	Context Context
	To      string
	Subject string
	Body    string
	Thread  string
	Gate    string
	Reason  string
}

// Receipt is the stable subset of the CLI mutation envelope needed by the
// console to show durable send evidence.
type Receipt struct {
	MessageID     string
	Thread        string
	AttemptID     string
	DeliveryState string
	Path          string
}

// Runner is the injectable console boundary. Production uses Send; tests
// provide a recorder and therefore never touch a live AMQ bus.
type Runner func(Action) (Receipt, error)

// Reply builds a first-class operator reply into the selected AMQ thread. The
// configured operator handle is explicit because deployments may replace the
// conventional "user" mailbox.
func Reply(ctx Context, th state.ThreadSummary, operatorHandle, body string) Action {
	return Action{
		Intent: IntentReply, Context: ctx,
		To: strings.Join(nonOperatorParticipants(th, operatorHandle), ","), Subject: replySubject(th.Subject),
		Body: body, Thread: th.ID,
	}
}

// Approve builds a typed/legacy-safe operator answer. The CLI answer
// command re-reads the exact gate at execution time; stale or malformed gates
// fail closed before AMQ is invoked.
func Approve(ctx Context, th state.ThreadSummary) Action {
	return Action{
		Intent: IntentApprove, Context: ctx, To: gateOwner(th),
		Gate: th.ID, Subject: th.Subject,
	}
}

// Deny builds a deny answer carrying an optional reason.
func Deny(ctx Context, th state.ThreadSummary, reason string) Action {
	return Action{
		Intent: IntentDeny, Context: ctx, To: gateOwner(th),
		Gate: th.ID, Subject: th.Subject, Reason: strings.TrimSpace(reason),
	}
}

// Message builds a status message to one configured agent.
func Message(ctx Context, handle, subject, body string) Action {
	return Action{
		Intent: IntentMessage, Context: ctx, To: strings.TrimSpace(handle),
		Subject: subject, Body: body,
	}
}

// Broadcast builds one status message to the configured squad. Recipient
// expansion happens in the CLI under namespace admission, and roster drift
// between preview and send is refused.
func Broadcast(ctx Context, subject, body string) Action {
	return Action{
		Intent: IntentBroadcast, Context: ctx,
		Subject: subject, Body: body, Thread: canonicalBroadcastThread(subject),
	}
}

// Preview renders the exact high-level command Send will invoke. It is pure:
// no filesystem scan, subprocess, receipt reservation, or AMQ write occurs.
func Preview(action Action) string {
	args := action.argv()
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

// Send invokes the current amq-squad binary with the exact argv shown by
// Preview. The selected first-class command owns namespace revalidation,
// gate authorization, the durable receipt, and stable message-id checks.
func Send(action Action) (Receipt, error) {
	if err := action.validate(); err != nil {
		return Receipt{}, err
	}
	args := action.argv()
	stdout, stderr, err := controlExec(args[1:], amqexec.NoUpdateCheckEnv(envWithoutAMQIdentity(os.Environ())))
	if err != nil {
		detail := strings.TrimSpace(string(stderr))
		if detail == "" {
			detail = strings.TrimSpace(string(stdout))
		}
		if detail != "" {
			return Receipt{}, fmt.Errorf("act %s: %w: %s", action.Intent, err, detail)
		}
		return Receipt{}, fmt.Errorf("act %s: %w", action.Intent, err)
	}
	receipt, err := decodeControlReceipt(stdout)
	if err != nil {
		return Receipt{}, fmt.Errorf("act %s: %w", action.Intent, err)
	}
	return receipt, nil
}

// controlExecutor is the only execution seam in this package. It deliberately
// executes amq-squad's receipted, authority-aware verbs rather than raw
// `amq send`.
type controlExecutor func(args []string, env []string) (stdout, stderr []byte, err error)

var controlExec controlExecutor = defaultControlExec

func defaultControlExec(args []string, env []string) ([]byte, []byte, error) {
	binary, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve current amq-squad executable: %w", err)
	}
	cmd := exec.Command(binary, args...)
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	stdout, runErr := cmd.Output()
	return stdout, []byte(stderr.String()), runErr
}

func (a Action) argv() []string {
	args := []string{"amq-squad"}
	switch a.Intent {
	case IntentApprove, IntentDeny:
		args = append(args, "operator", "answer")
		args = appendScope(args, a.Context)
		args = append(args, "--gate", a.Gate)
		if strings.TrimSpace(a.To) != "" {
			args = append(args, "--to", a.To)
		}
		if a.Intent == IntentApprove {
			args = append(args, "--approved")
		} else {
			args = append(args, "--denied")
			if strings.TrimSpace(a.Reason) != "" {
				args = append(args, "--reason", a.Reason)
			}
		}
		args = append(args, "--json")
	case IntentBroadcast:
		args = append(args, "broadcast")
		args = appendScope(args, a.Context)
		args = append(args, "--subject", a.Subject, "--body", a.Body)
		if strings.TrimSpace(a.Thread) != "" {
			args = append(args, "--thread", a.Thread)
		}
		args = append(args, "--yes", "--json")
	case IntentReply, IntentMessage:
		args = append(args, "operator", "send")
		args = appendScope(args, a.Context)
		args = append(args, "--to", a.To, "--subject", a.Subject, "--body", a.Body)
		if strings.TrimSpace(a.Thread) != "" {
			args = append(args, "--thread", a.Thread)
		}
		kind := string(state.KindStatus)
		if a.Intent == IntentReply {
			kind = string(state.KindAnswer)
		}
		args = append(args, "--kind", kind, "--yes", "--json")
	}
	return args
}

func appendScope(args []string, ctx Context) []string {
	args = append(args, "--project", ctx.Project)
	if profile := strings.TrimSpace(ctx.Profile); profile != "" && profile != "default" {
		args = append(args, "--profile", profile)
	}
	return append(args, "--session", ctx.Session)
}

func (a Action) validate() error {
	if strings.TrimSpace(a.Context.Project) == "" || strings.TrimSpace(a.Context.Session) == "" {
		return fmt.Errorf("act: project and session are required")
	}
	switch a.Intent {
	case IntentApprove, IntentDeny:
		if strings.TrimSpace(a.Gate) == "" || !strings.HasPrefix(a.Gate, "gate/") {
			return fmt.Errorf("act: approve/deny requires a gate/<topic> thread")
		}
	case IntentReply, IntentMessage:
		if strings.TrimSpace(a.To) == "" {
			return fmt.Errorf("act: reply/message requires a recipient")
		}
		if strings.TrimSpace(a.Subject) == "" {
			return fmt.Errorf("act: reply/message requires a subject")
		}
		if a.Intent == IntentReply && strings.HasPrefix(strings.TrimSpace(a.Thread), "gate/") {
			return fmt.Errorf("act: gate replies require approve/deny so authority is revalidated")
		}
	case IntentBroadcast:
		if strings.TrimSpace(a.Subject) == "" {
			return fmt.Errorf("act: broadcast requires a subject")
		}
	default:
		return fmt.Errorf("act: unknown intent %q", a.Intent)
	}
	return nil
}

func gateOwner(th state.ThreadSummary) string {
	if th.OperatorGate != nil && strings.TrimSpace(th.OperatorGate.From) != "" {
		return strings.TrimSpace(th.OperatorGate.From)
	}
	participants := nonOperatorParticipants(th, state.DefaultOperatorHandle)
	if len(participants) == 0 {
		return ""
	}
	return participants[0]
}

func canonicalBroadcastThread(subject string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(subject)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "message"
	}
	return "broadcast/" + slug
}

func decodeControlReceipt(raw []byte) (Receipt, error) {
	var env struct {
		Kind string `json:"kind"`
		Data struct {
			MessageID       string `json:"message_id"`
			Thread          string `json:"thread"`
			DeliveryReceipt *struct {
				AttemptID         string `json:"attempt_id"`
				DeliveryState     string `json:"delivery_state"`
				Path              string `json:"path"`
				MessageID         string `json:"message_id"`
				ReconciledMessage string `json:"reconciled_message_id"`
			} `json:"delivery_receipt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return Receipt{}, fmt.Errorf("decode receipt envelope: %w", err)
	}
	if env.Kind != "operator_send" || env.Data.DeliveryReceipt == nil {
		return Receipt{}, fmt.Errorf("unexpected mutation envelope kind %q or missing delivery receipt", env.Kind)
	}
	delivery := env.Data.DeliveryReceipt
	messageID := strings.TrimSpace(env.Data.MessageID)
	if messageID == "" {
		messageID = strings.TrimSpace(delivery.MessageID)
	}
	if messageID == "" {
		messageID = strings.TrimSpace(delivery.ReconciledMessage)
	}
	if messageID == "" || strings.TrimSpace(env.Data.Thread) == "" ||
		strings.TrimSpace(delivery.AttemptID) == "" ||
		strings.TrimSpace(delivery.DeliveryState) == "" ||
		strings.TrimSpace(delivery.Path) == "" {
		return Receipt{}, fmt.Errorf("mutation envelope lacks stable message/thread/attempt/state/path evidence")
	}
	return Receipt{
		MessageID: messageID, Thread: env.Data.Thread, AttemptID: delivery.AttemptID,
		DeliveryState: delivery.DeliveryState, Path: delivery.Path,
	}, nil
}

func envWithoutAMQIdentity(env []string) []string {
	remove := map[string]bool{
		"AM_ROOT":         true,
		"AM_BASE_ROOT":    true,
		"AM_SESSION":      true,
		"AM_ME":           true,
		"AMQ_GLOBAL_ROOT": true,
	}
	out := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if !ok || !remove[key] {
			out = append(out, entry)
		}
	}
	return out
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if isShellSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("-_./@:,=+", r):
		default:
			return false
		}
	}
	return true
}

func replySubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "Re:"
	}
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}

func nonOperatorParticipants(th state.ThreadSummary, operatorHandle string) []string {
	operatorHandle = strings.TrimSpace(operatorHandle)
	if operatorHandle == "" {
		operatorHandle = state.DefaultOperatorHandle
	}
	seen := map[string]bool{}
	var participants []string
	for _, participant := range th.Participants {
		participant = strings.TrimSpace(participant)
		if participant == "" || participant == operatorHandle || seen[participant] {
			continue
		}
		seen[participant] = true
		participants = append(participants, participant)
	}
	sort.Strings(participants)
	return participants
}
