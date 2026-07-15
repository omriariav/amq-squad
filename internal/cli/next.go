package cli

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
)

// nextActionData is the kind="next" payload: a single canonical action object
// conforming to docs/action-object-contract.md. id, kind, label, action_kind,
// command, available, and unavailable_reason are the canonical fields. Gate
// topic, operator state, and namespace are additive extensions.
type nextActionData struct {
	ID                string             `json:"id"`
	Kind              string             `json:"kind"`
	Label             string             `json:"label"`
	ActionKind        string             `json:"action_kind"`
	Command           string             `json:"command,omitempty"`
	Available         bool               `json:"available"`
	UnavailableReason string             `json:"unavailable_reason,omitempty"`
	Reason            string             `json:"reason,omitempty"`
	GateTopic         string             `json:"gate_topic,omitempty"`
	OperatorState     string             `json:"operator_state,omitempty"`
	Profile           string             `json:"profile,omitempty"`
	Session           string             `json:"session,omitempty"`
	Namespace         squadnamespace.Ref `json:"namespace"`
}

type nextExecution struct {
	ProjectDir      string
	Profile         string
	Session         string
	BaseRoot        string
	JSON            bool
	Out             io.Writer
	ResolveBaseRoot func(string) (string, error)
	Probe           state.Probe
	Now             func() time.Time
}

func runNext(args []string) error {
	fs := flag.NewFlagSet("next", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to inspect")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	jsonOut := fs.Bool("json", false, "emit a schema-versioned next envelope with a canonical action object")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad next - get the highest-priority operator action for this session

Usage:
  amq-squad next [--project DIR] [--profile NAME] [--session NAME] [--json]

Returns the single most important action for the operator to take now. Checks,
in order: open operator gates, operator inbox backlog, unacknowledged directives,
and stale operator poll loops. Exits 0 when an action is ready; exits 1 when
the system is idle and no action is pending.

In JSON mode, emits a schema-versioned envelope whose data is a canonical action
object conforming to the action-object contract (docs/action-object-contract.md).

Examples:
  amq-squad next
  amq-squad next --session issue-96 --json
  amq-squad next --project ~/Code/app --profile review --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("next takes no positional arguments")
	}
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	if err != nil {
		return err
	}
	emitContextDiagnostics(ctx)
	return executeNext(nextExecution{
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

func executeNext(ne nextExecution) error {
	out := ne.Out
	if out == nil {
		out = os.Stdout
	}
	data, err := buildOperatorStatusData(operatorExecution{
		ProjectDir:      ne.ProjectDir,
		Profile:         ne.Profile,
		Session:         ne.Session,
		BaseRoot:        ne.BaseRoot,
		ReadOnly:        true,
		ResolveBaseRoot: ne.ResolveBaseRoot,
		Probe:           ne.Probe,
		Now:             ne.Now,
	})
	if err != nil {
		return err
	}
	action, found := deriveNextAction(data, ne.ProjectDir)
	if !found {
		idle := idleNextActionData(data.Profile, data.Session, data.Namespace)
		if ne.JSON {
			_ = writeJSONEnvelope(out, "next", idle)
		} else {
			fmt.Fprintln(out, idle.Label)
		}
		return UsageError(fmt.Sprintf("idle — no action pending for %s/%s", data.Profile, data.Session))
	}
	if ne.JSON {
		return writeJSONEnvelope(out, "next", action)
	}
	if action.Command != "" {
		fmt.Fprintf(out, "%s: %s\n", action.Label, action.Command)
	} else {
		fmt.Fprintln(out, action.Label)
	}
	return nil
}

// deriveNextAction returns the highest-priority operator action from the
// operator status snapshot. Priority: answerable attention → inspect-only
// release recovery → inbox backlog → unacked directives → stale poll loop →
// idle (false).
func deriveNextAction(data operatorStatusEnvelopeData, projectDir string) (nextActionData, bool) {
	profile := data.Profile
	session := data.Session

	// Priority 1: explicit answerable attention. Thread naming is descriptive,
	// not authority for actionability.
	for _, item := range data.Attention {
		if isOpenGateAttention(item) && item.Answerable {
			return nextGateAnswerAction(item, projectDir, profile, session, data.Namespace), true
		}
	}

	// Priority 2: inspect-only open gates, release recovery, or degradation.
	for _, item := range data.Attention {
		inspectOnlyRelease := item.Actionable && !item.Cleared && !item.Answerable && (item.EventType == "compound_release_recovery" || item.EventType == "compound_release_degraded")
		if isOpenGateAttention(item) && !item.Answerable || inspectOnlyRelease {
			return nextInspectAttentionAction(item, profile, session, data.Namespace), true
		}
	}

	// Priority 3: operator inbox backlog.
	if data.OperatorLoop.Backlog > 0 {
		cmd := nextOperatorStatusCmd(projectDir, profile, session)
		a := nextActionData{
			ID:            "operator_status",
			Kind:          "operator_status",
			Label:         fmt.Sprintf("check operator inbox (%d unread)", data.OperatorLoop.Backlog),
			ActionKind:    "display",
			Command:       cmd,
			Available:     true,
			OperatorState: data.OperatorLoop.State,
			Profile:       profile,
			Session:       session,
			Namespace:     data.Namespace,
		}
		return a, true
	}

	// Priority 4: unacknowledged directives.
	if data.OperatorLoop.DirectivesUnacked > 0 {
		cmd := nextOperatorStatusCmd(projectDir, profile, session)
		a := nextActionData{
			ID:            "operator_status",
			Kind:          "operator_status",
			Label:         fmt.Sprintf("check unacknowledged directives (%d)", data.OperatorLoop.DirectivesUnacked),
			ActionKind:    "display",
			Command:       cmd,
			Available:     true,
			OperatorState: data.OperatorLoop.State,
			Profile:       profile,
			Session:       session,
			Namespace:     data.Namespace,
		}
		return a, true
	}

	// Priority 5: stale operator poll loop.
	if data.OperatorLoop.State == "poller_stale" {
		cmd := nextOperatorPollCmd(projectDir, profile, session)
		a := nextActionData{
			ID:            "operator_poll",
			Kind:          "operator_poll",
			Label:         "refresh stale operator poll loop",
			ActionKind:    "run",
			Command:       cmd,
			Available:     true,
			OperatorState: data.OperatorLoop.State,
			Profile:       profile,
			Session:       session,
			Namespace:     data.Namespace,
		}
		return a, true
	}

	return nextActionData{}, false
}

func nextInspectAttentionAction(item operatorAttention, profile, session string, ns squadnamespace.Ref) nextActionData {
	label := item.Subject
	if label == "" {
		label = "inspect compound release recovery"
	}
	return nextActionData{
		ID: item.EventType, Kind: item.EventType, Label: label, ActionKind: "display",
		Command: item.Inspect, Available: item.Inspect != "", Profile: profile, Session: session, Namespace: ns,
	}
}

func nextGateAnswerAction(item operatorAttention, projectDir, profile, session string, ns squadnamespace.Ref) nextActionData {
	topic := strings.TrimPrefix(item.Thread, "gate/")
	cmd := "amq-squad operator answer"
	if projectDir != "" {
		cmd += " --project " + shellQuote(projectDir)
	}
	cmd += operatorProfileArg(profile)
	if session != "" {
		cmd += " --session " + shellQuote(session)
	}
	cmd += " --gate " + shellQuote(topic)
	if item.From != "" {
		cmd += " --to " + shellQuote(item.From)
	}
	cmd += " --approved"
	label := "answer operator gate"
	if item.Subject != "" {
		label = fmt.Sprintf("answer operator gate: %s", item.Subject)
	}
	return nextActionData{
		ID:         "gate_answer",
		Kind:       "gate_answer",
		Label:      label,
		ActionKind: "gate_answer",
		Command:    cmd,
		Available:  true,
		GateTopic:  item.Thread,
		Profile:    profile,
		Session:    session,
		Namespace:  ns,
	}
}

func idleNextActionData(profile, session string, ns squadnamespace.Ref) nextActionData {
	reason := "system is idle — no open gates, backlog, or pending actions"
	return nextActionData{
		ID:                "idle",
		Kind:              "idle",
		Label:             fmt.Sprintf("no action pending for %s/%s", profile, session),
		ActionKind:        "display",
		Available:         false,
		UnavailableReason: reason,
		Reason:            reason,
		Profile:           profile,
		Session:           session,
		Namespace:         ns,
	}
}

func nextOperatorStatusCmd(projectDir, profile, session string) string {
	cmd := "amq-squad operator status"
	cmd += nextCommonArgs(projectDir, profile, session)
	cmd += " --json"
	return cmd
}

func nextOperatorPollCmd(projectDir, profile, session string) string {
	cmd := "amq-squad operator poll --readonly"
	cmd += nextCommonArgs(projectDir, profile, session)
	cmd += " --json"
	return cmd
}

func nextCommonArgs(projectDir, profile, session string) string {
	var b strings.Builder
	if projectDir != "" {
		b.WriteString(" --project ")
		b.WriteString(shellQuote(projectDir))
	}
	b.WriteString(operatorProfileArg(profile))
	if session != "" {
		b.WriteString(" --session ")
		b.WriteString(shellQuote(session))
	}
	return b.String()
}
