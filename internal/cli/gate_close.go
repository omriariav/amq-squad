package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

var gateCloseScanMessages = state.ScanSessionMessages
var gateCloseSend = sendGateClose

type gateCloseSendOptions struct {
	Project, Profile, Session string
	From, To, Thread          string
	ReplyTo                   string
	Subject, Body             string
	Context                   map[string]any
	JSON                      bool
}

// runGateClose terminalizes exactly the currently-open request generation on
// a gate thread. command_registry wiring intentionally lives with the shared
// gate command owner; this function is the stable #464 integration seam.
func runGateClose(args []string) error {
	fs := flag.NewFlagSet("gate close", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session containing the gate")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	gateFlag := fs.String("gate", "", "gate topic, with or without the gate/ prefix")
	meFlag := fs.String("me", "", "requesting roster member handle")
	reasonFlag := fs.String("reason", "", "required close/withdraw reason")
	withdrawn := fs.Bool("withdrawn", false, "withdraw the request instead of closing it")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad gate close - close the current operator gate generation

Usage:
  amq-squad gate close --gate TOPIC --me HANDLE --reason TEXT [--withdrawn]
                       [--project DIR] [--profile NAME] --session S [--json]

Only the roster member that sent the currently-open request may close or
withdraw it. The terminal event is bound to that exact request message ID.

Examples:
  amq-squad gate close --session issue-414 --me cto --gate merge-414 --reason "candidate superseded"
  amq-squad gate close --session issue-414 --me cto --gate merge-414 --reason "request withdrawn" --withdrawn
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("gate close takes no positional arguments; got %d", fs.NArg())
	}
	topic, err := strictGateCloseTopic(*gateFlag)
	if err != nil {
		return err
	}
	actorHandle := strings.TrimSpace(*meFlag)
	if actorHandle == "" {
		return usageErrorf("gate close requires --me <requester-handle>")
	}
	reason := strings.TrimSpace(*reasonFlag)
	if reason == "" {
		return usageErrorf("gate close requires --reason <why>")
	}

	projectDir, profile, cfg, workstream, operatorHandle, err := resolveOperatorCommandContext(*projectFlag, *profileFlag, *sessionFlag, flagWasSet(fs, "project"), flagWasSet(fs, "session"))
	if err != nil {
		return err
	}
	member, ok := gateCloseRosterMember(cfg, actorHandle, workstream)
	if !ok {
		return usageErrorf("gate close requester %q is not a roster member in session %q", actorHandle, workstream)
	}
	if _, err := resolveVerifiedOperatorActor(projectDir, profile, workstream, member.Role, actorHandle); err != nil {
		return fmt.Errorf("gate close requester identity is not verified: %w", err)
	}
	initialIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(projectDir, profile, workstream), gateCloseEndpointHandle(actorHandle, operatorHandle))
	if err != nil {
		return err
	}

	admission, err := acquireNamespaceWriterAdmission(projectDir, profile, workstream)
	if err != nil {
		return err
	}
	defer admission.close()

	currentProject, currentProfile, currentCfg, currentWorkstream, currentOperator, err := resolveOperatorCommandContext(*projectFlag, *profileFlag, *sessionFlag, flagWasSet(fs, "project"), flagWasSet(fs, "session"))
	if err != nil {
		return fmt.Errorf("gate close refused: context re-resolution under admission failed: %w", err)
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(squadnamespace.Resolve(currentProject, currentProfile, currentWorkstream), gateCloseEndpointHandle(actorHandle, currentOperator))
	if err != nil {
		return err
	}
	if err := validateReResolvedEndpointIdentity("gate close", initialIdentity, currentIdentity); err != nil {
		return err
	}
	projectDir, profile, cfg, workstream, operatorHandle = currentProject, currentProfile, currentCfg, currentWorkstream, currentOperator
	if err := ensureNoNamespaceConflict("gate close", projectDir, profile, workstream, flagWasSet(fs, "profile")); err != nil {
		return err
	}
	if err := ensureNoNamespaceMigration("gate close", projectDir, profile, workstream); err != nil {
		return err
	}
	member, ok = gateCloseRosterMember(cfg, actorHandle, workstream)
	if !ok {
		return fmt.Errorf("gate close refused: requester %q left the target roster before admission", actorHandle)
	}
	if _, err := resolveVerifiedOperatorActor(projectDir, profile, workstream, member.Role, actorHandle); err != nil {
		return fmt.Errorf("gate close requester identity changed before admission: %w", err)
	}

	ns := squadnamespace.Resolve(projectDir, profile, workstream)
	messages, warnings := gateCloseScanMessages(ns.AMQRoot, time.Now)
	if len(warnings) > 0 {
		return fmt.Errorf("gate close refused: gate scan is degraded (%d warning(s)); inspect %s before retrying", len(warnings), topic)
	}
	threadMessages := make([]state.Message, 0)
	for _, message := range messages {
		if message.Thread == topic {
			threadMessages = append(threadMessages, message)
		}
	}
	gateState, open := state.ResolveOperatorGate(threadMessages, operatorHandle, time.Now())
	if gateState != state.OperatorGateStateOpen || open == nil {
		if gateState == state.OperatorGateStateUnknown {
			return usageErrorf("gate close requires a currently-open request on %s", topic)
		}
		return usageErrorf("gate %s is already %s", topic, gateState)
	}
	if open.From != actorHandle {
		return usageErrorf("gate %s is owned by requester %s; %s cannot close it", topic, open.From, actorHandle)
	}
	if open.Conflicted {
		return fmt.Errorf("gate close refused: current request %s has conflicting mailbox evidence", open.LatestID)
	}
	if !open.ToPresent || !open.ToArrayValid || len(open.RawTo) != 1 || open.RawTo[0] == "" || open.RawTo[0] != strings.TrimSpace(open.RawTo[0]) || open.RawTo[0] != operatorHandle {
		return fmt.Errorf("gate close refused: current request %s must have exactly one canonical on-disk recipient equal configured operator %q", open.LatestID, operatorHandle)
	}
	if !open.Terminalizable {
		return fmt.Errorf("gate close refused: current request %s lacks exact schema/thread identity", open.LatestID)
	}
	if open.RefsPresent {
		return fmt.Errorf("gate close refused: current request %s already carries refs and cannot safely start another reply chain", open.LatestID)
	}

	terminalState := state.OperatorGateStateClosed
	if *withdrawn {
		terminalState = state.OperatorGateStateWithdrawn
	}
	context := map[string]any{"gate": map[string]any{
		"state":              string(terminalState),
		"request_message_id": open.LatestID,
		"requester":          actorHandle,
		"thread":             topic,
		"actor":              actorHandle,
	}}
	return gateCloseSend(gateCloseSendOptions{
		Project: projectDir, Profile: profile, Session: workstream,
		From: actorHandle, To: operatorHandle, Thread: topic,
		ReplyTo: open.LatestID,
		Subject: strings.ToUpper(string(terminalState)) + ": " + strings.TrimPrefix(topic, "gate/"),
		Body:    reason, Context: context, JSON: *jsonOut,
	})
}

func strictGateCloseTopic(raw string) (string, error) {
	if raw == "" {
		return "", usageErrorf("gate close requires --gate <topic>")
	}
	if !utf8.ValidString(raw) || raw != strings.TrimSpace(raw) {
		return "", usageErrorf("gate close requires one trim-canonical single-line gate topic; got %q", raw)
	}
	for _, r := range raw {
		if r == '\\' || r == '\u2028' || r == '\u2029' || unicode.IsControl(r) || unicode.IsSpace(r) {
			return "", usageErrorf("gate close requires one trim-canonical single-line gate topic; got %q", raw)
		}
	}
	topic := raw
	if strings.HasPrefix(topic, "gate/") {
		topic = strings.TrimPrefix(topic, "gate/")
	}
	if topic == "" {
		return "", usageErrorf("gate close requires one canonical gate topic (for example release or gate/release); got %q", raw)
	}
	for _, segment := range strings.Split(topic, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", usageErrorf("gate close requires nonempty canonical gate segments; got %q", raw)
		}
	}
	return "gate/" + topic, nil
}

func gateCloseEndpointHandle(actor, operator string) string {
	return strings.TrimSpace(actor) + "->" + strings.TrimSpace(operator)
}

func gateCloseRosterMember(cfg team.Team, handle, session string) (team.Member, bool) {
	for _, member := range cfg.Members {
		if memberHandle(member) == handle && (strings.TrimSpace(member.Session) == "" || member.Session == session) {
			return member, true
		}
	}
	return team.Member{}, false
}

func sendGateClose(o gateCloseSendOptions) error {
	ctx, err := resolveAMQContextForNamespace(o.Project, o.Profile, o.Session, o.From)
	if err != nil {
		return fmt.Errorf("resolve amq root for gate close: %w", err)
	}
	ctx.Me = o.From
	contextJSON, err := json.Marshal(o.Context)
	if err != nil {
		return fmt.Errorf("encode gate close context: %w", err)
	}
	args := []string{
		"reply", "--root", ctx.Root, "--me", o.From, "--id", o.ReplyTo,
		"--kind", string(state.KindStatus), "--subject", o.Subject, "--body", o.Body,
		"--context", string(contextJSON),
	}
	durableReceipt := newDeliveryReceipt(o.Project, o.Profile, o.Session, "", o.To, "", "gate_close")
	durableReceipt.Sender = o.From
	durableReceipt.Recipients = []string{o.To}
	durableReceipt.Consumers = []deliveryConsumerState{{Consumer: o.To, State: deliveryStateAmbiguousUnknown}}
	durableReceipt.Thread = o.Thread
	raw, receipt, err := runOwnedDurableSend(durableSendOptions{ProjectDir: o.Project, Profile: o.Profile, Session: o.Session, Kind: "gate_close", Receipt: &durableReceipt}, amqCommandRequest{Dir: o.Project, Env: amqCommandEnv(ctx), Arg: args})
	if err != nil {
		return fmt.Errorf("gate close send to %s: %w", o.To, err)
	}
	if o.JSON {
		return printJSONEnvelope("gate_close", mutationResult{
			Command: "gate close", Status: "sent", Project: o.Project, Profile: o.Profile, Session: o.Session,
			Namespace: squadnamespace.Resolve(o.Project, o.Profile, o.Session), Handle: o.To,
			MessageID: receipt.MessageID, Thread: o.Thread, Root: ctx.Root, DeliveryReceipt: receipt,
			Actions: []mutationAction{followUp("status", "show operator status", "amq-squad operator status --project "+shellQuote(o.Project)+operatorProfileArg(o.Profile)+" --session "+shellQuote(o.Session)+" --json")},
		})
	}
	if receipt.MessageID != "" {
		fmt.Printf("Sent gate close on %s: %s (attempt %s, state %s, receipt %s)\n", o.Thread, receipt.MessageID, receipt.AttemptID, receipt.DeliveryState, receipt.Path)
	} else if msg := strings.TrimSpace(string(raw)); msg != "" {
		fmt.Println(msg)
	}
	return nil
}
