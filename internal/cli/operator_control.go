package cli

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/act"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/state"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// operatorControlContext is the exact, admitted endpoint used by the
// first-class operator message and broadcast surfaces.
type operatorControlContext struct {
	Resolution contextResolution
	Project    string
	Profile    string
	Team       team.Team
	Session    string
	Operator   string
}

type operatorControlPreview struct {
	Command              string   `json:"command"`
	Project              string   `json:"project"`
	Profile              string   `json:"profile"`
	Session              string   `json:"session"`
	Recipients           []string `json:"recipients"`
	Thread               string   `json:"thread"`
	Kind                 string   `json:"message_kind"`
	Subject              string   `json:"subject"`
	Preview              string   `json:"preview"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
}

func runOperatorSend(args []string) error {
	fs := flag.NewFlagSet("operator send", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to send in")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	toFlag := fs.String("to", "", "configured agent handle to receive the message")
	subjectFlag := fs.String("subject", "", "message subject")
	bodyFlag := fs.String("body", "", "message body")
	bodyFileFlag := fs.String("body-file", "", "read message body from file ('-' for stdin)")
	threadFlag := fs.String("thread", "", "AMQ thread (default: canonical operator/agent p2p thread)")
	kindFlag := fs.String("kind", string(state.KindStatus), "AMQ kind: status or answer")
	yes := fs.Bool("yes", false, "confirm and send (without this flag, print an exact preview only)")
	overrideNamespaceConflict := fs.Bool("override-namespace-conflict", false, "acknowledge a collided namespace and continue, writing an audit record")
	overrideNamespaceReason := fs.String("reason", "", "required reason when --override-namespace-conflict is set")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned preview or mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad operator send - send an operator-authored AMQ message to any configured agent

Usage:
  amq-squad operator send [--project DIR] [--profile NAME] [--session S]
      --to HANDLE --subject TEXT (--body TEXT | --body-file FILE)
      [--thread THREAD] [--kind status|answer] [--yes] [--json]

The default posture is preview-only. Pass --yes only after reviewing the exact
recipient, thread, kind, subject, and command. Successful sends write a durable
delivery receipt and report the stable AMQ message id.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("operator send takes no positional arguments; got %q", fs.Arg(0))
	}
	to := strings.TrimSpace(*toFlag)
	recipients := splitOperatorRecipients(to)
	if len(recipients) == 0 {
		return usageErrorf("operator send requires --to <agent-handle>")
	}
	to = strings.Join(recipients, ",")
	subject := strings.TrimSpace(*subjectFlag)
	if subject == "" {
		return usageErrorf("operator send requires --subject TEXT")
	}
	body, err := readPromptBody(*bodyFlag, *bodyFileFlag, flagWasSet(fs, "body"), flagWasSet(fs, "body-file"), os.Stdin, stdinIsInteractive())
	if err != nil {
		return err
	}
	kind := strings.TrimSpace(*kindFlag)
	if kind != string(state.KindStatus) && kind != string(state.KindAnswer) {
		return usageErrorf("operator send --kind must be status or answer")
	}

	resolve := func() (contextResolution, error) {
		return resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	}
	initialResolution, err := resolve()
	if err != nil {
		return err
	}
	initial, err := resolveOperatorControlContext(*projectFlag, *profileFlag, *sessionFlag, fs)
	if err != nil {
		return err
	}
	if err := ensureConfiguredOperatorTargets(initial.Team, recipients, "operator send"); err != nil {
		return err
	}
	thread := strings.TrimSpace(*threadFlag)
	if thread == "" {
		if len(recipients) != 1 {
			return usageErrorf("operator send requires --thread when --to names multiple agents")
		}
		thread = canonicalP2PThread(recipients[0], initial.Operator)
	}
	if strings.HasPrefix(thread, "gate/") {
		return usageErrorf("operator send cannot write to gate/<topic>; use 'amq-squad operator answer' so gate authority is validated")
	}
	previewAction := act.Message(operatorActContext(initial), to, subject, body)
	previewAction.Thread = thread
	if kind == string(state.KindAnswer) {
		previewAction.Intent = act.IntentReply
	}
	if !*yes {
		return printOperatorControlPreview(*jsonOut, operatorControlPreview{
			Command: "operator send", Project: initial.Project, Profile: initial.Profile, Session: initial.Session,
			Recipients: recipients, Thread: thread, Kind: kind, Subject: subject,
			Preview: act.Preview(previewAction), RequiresConfirmation: true,
		})
	}

	override := namespaceConflictOverrideOptions{Allowed: *overrideNamespaceConflict, Reason: *overrideNamespaceReason}
	current, admission, err := admitOperatorControl("operator send", initialResolution, resolve, *projectFlag, *profileFlag, *sessionFlag, fs, override)
	if err != nil {
		return err
	}
	defer admission.close()
	if err := ensureConfiguredOperatorTargets(current.Team, recipients, "operator send"); err != nil {
		return err
	}
	if current.Operator != initial.Operator || current.Session != initial.Session || current.Profile != initial.Profile || current.Project != initial.Project {
		return fmt.Errorf("operator send refused: operator endpoint changed after confirmation")
	}
	if strings.TrimSpace(*threadFlag) == "" {
		thread = canonicalP2PThread(recipients[0], current.Operator)
	}
	return sendOperatorAMQ(operatorSendOptions{
		Command: "message", Project: current.Project, Profile: current.Profile, Session: current.Session,
		From: current.Operator, To: to, Thread: thread, Kind: kind, Subject: subject, Body: body,
		JSON: *jsonOut, Out: os.Stdout,
		FollowUp: "amq-squad thread --project " + shellQuote(current.Project) + operatorProfileArg(current.Profile) +
			" --session " + shellQuote(current.Session) + " --id " + shellQuote(thread),
	})
}

func runBroadcast(args []string) error {
	fs := flag.NewFlagSet("broadcast", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "AMQ workstream/session to broadcast in")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	subjectFlag := fs.String("subject", "", "broadcast subject")
	bodyFlag := fs.String("body", "", "broadcast body")
	bodyFileFlag := fs.String("body-file", "", "read broadcast body from file ('-' for stdin)")
	threadFlag := fs.String("thread", "", "AMQ thread (default: broadcast/<subject-slug>)")
	yes := fs.Bool("yes", false, "confirm and broadcast (without this flag, print an exact preview only)")
	overrideNamespaceConflict := fs.Bool("override-namespace-conflict", false, "acknowledge a collided namespace and continue, writing an audit record")
	overrideNamespaceReason := fs.String("reason", "", "required reason when --override-namespace-conflict is set")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned preview or mutation result envelope")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad broadcast - send one operator-authored AMQ message to the squad

Usage:
  amq-squad broadcast [--project DIR] [--profile NAME] [--session S]
      --subject TEXT (--body TEXT | --body-file FILE)
      [--thread THREAD] [--yes] [--json]

Recipients are the configured squad handles, excluding the operator mailbox.
They are sorted and de-duplicated before the exact preview is shown. The default
posture is preview-only; --yes performs the receipted send.
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("broadcast takes no positional arguments; got %q", fs.Arg(0))
	}
	subject := strings.TrimSpace(*subjectFlag)
	if subject == "" {
		return usageErrorf("broadcast requires --subject TEXT")
	}
	body, err := readPromptBody(*bodyFlag, *bodyFileFlag, flagWasSet(fs, "body"), flagWasSet(fs, "body-file"), os.Stdin, stdinIsInteractive())
	if err != nil {
		return err
	}
	resolve := func() (contextResolution, error) {
		return resolveScopedCommandContext(*projectFlag, *profileFlag, *sessionFlag, "", fs)
	}
	initialResolution, err := resolve()
	if err != nil {
		return err
	}
	initial, err := resolveOperatorControlContext(*projectFlag, *profileFlag, *sessionFlag, fs)
	if err != nil {
		return err
	}
	recipients := operatorBroadcastRecipients(initial.Team, initial.Operator)
	if len(recipients) == 0 {
		return usageErrorf("broadcast has no configured non-operator recipients")
	}
	thread := strings.TrimSpace(*threadFlag)
	if thread == "" {
		thread = canonicalBroadcastThread(subject)
	}
	if strings.HasPrefix(thread, "gate/") {
		return usageErrorf("broadcast cannot write to gate/<topic>")
	}
	previewAction := act.Broadcast(operatorActContext(initial), subject, body)
	previewAction.Thread = thread
	if !*yes {
		return printOperatorControlPreview(*jsonOut, operatorControlPreview{
			Command: "broadcast", Project: initial.Project, Profile: initial.Profile, Session: initial.Session,
			Recipients: recipients, Thread: thread, Kind: string(state.KindStatus), Subject: subject,
			Preview: act.Preview(previewAction), RequiresConfirmation: true,
		})
	}

	override := namespaceConflictOverrideOptions{Allowed: *overrideNamespaceConflict, Reason: *overrideNamespaceReason}
	current, admission, err := admitOperatorControl("broadcast", initialResolution, resolve, *projectFlag, *profileFlag, *sessionFlag, fs, override)
	if err != nil {
		return err
	}
	defer admission.close()
	currentRecipients := operatorBroadcastRecipients(current.Team, current.Operator)
	if !equalStringSet(recipients, currentRecipients) {
		return fmt.Errorf("broadcast refused: configured recipient roster changed after confirmation (was %s, now %s)",
			strings.Join(recipients, ","), strings.Join(currentRecipients, ","))
	}
	if current.Operator != initial.Operator || current.Session != initial.Session || current.Profile != initial.Profile || current.Project != initial.Project {
		return fmt.Errorf("broadcast refused: operator endpoint changed after confirmation")
	}
	if strings.TrimSpace(*threadFlag) == "" {
		thread = canonicalBroadcastThread(subject)
	}
	return sendOperatorAMQ(operatorSendOptions{
		Command: "broadcast", Project: current.Project, Profile: current.Profile, Session: current.Session,
		From: current.Operator, To: strings.Join(currentRecipients, ","), Thread: thread,
		Kind: string(state.KindStatus), Subject: subject, Body: body,
		JSON: *jsonOut, Out: os.Stdout,
		FollowUp: "amq-squad thread --project " + shellQuote(current.Project) + operatorProfileArg(current.Profile) +
			" --session " + shellQuote(current.Session) + " --id " + shellQuote(thread),
	})
}

func resolveOperatorControlContext(projectFlag, profileFlag, sessionFlag string, fs *flag.FlagSet) (operatorControlContext, error) {
	projectDir, profile, cfg, session, operatorHandle, err := resolveOperatorCommandContext(
		projectFlag, profileFlag, sessionFlag, flagWasSet(fs, "project"), flagWasSet(fs, "session"))
	if err != nil {
		return operatorControlContext{}, err
	}
	resolution, err := resolveScopedCommandContext(projectFlag, profileFlag, sessionFlag, "", fs)
	if err != nil {
		return operatorControlContext{}, err
	}
	return operatorControlContext{
		Resolution: resolution, Project: projectDir, Profile: profile, Team: cfg,
		Session: session, Operator: operatorHandle,
	}, nil
}

func admitOperatorControl(command string, initial contextResolution, resolve func() (contextResolution, error), projectFlag, profileFlag, sessionFlag string, fs *flag.FlagSet, override namespaceConflictOverrideOptions) (operatorControlContext, *namespaceAdmissionLocks, error) {
	initialControl, err := resolveOperatorControlContext(projectFlag, profileFlag, sessionFlag, fs)
	if err != nil {
		return operatorControlContext{}, nil, err
	}
	initialIdentity, err := captureNamespaceEndpointIdentity(
		squadnamespace.Resolve(initialControl.Project, initialControl.Profile, initialControl.Session),
		initialControl.Operator,
	)
	if err != nil {
		return operatorControlContext{}, nil, err
	}
	admittedResolution, admission, err := acquireRevalidatedContextWriter(initial, false, resolve)
	if err != nil {
		return operatorControlContext{}, nil, err
	}
	current, err := resolveOperatorControlContext(projectFlag, profileFlag, sessionFlag, fs)
	if err != nil {
		admission.close()
		return operatorControlContext{}, nil, fmt.Errorf("%s refused: context re-resolution under admission failed: %w", command, err)
	}
	currentIdentity, err := captureNamespaceEndpointIdentity(
		squadnamespace.Resolve(current.Project, current.Profile, current.Session),
		current.Operator,
	)
	if err != nil {
		admission.close()
		return operatorControlContext{}, nil, err
	}
	if err := validateReResolvedEndpointIdentity(command, initialIdentity, currentIdentity); err != nil {
		admission.close()
		return operatorControlContext{}, nil, err
	}
	if err := validateReResolvedContext(admittedResolution, current.Resolution, false); err != nil {
		admission.close()
		return operatorControlContext{}, nil, err
	}
	if err := ensureNoNamespaceConflictWithOverride(command, current.Project, current.Profile, current.Session, flagWasSet(fs, "profile"), override); err != nil {
		admission.close()
		return operatorControlContext{}, nil, err
	}
	return current, admission, nil
}

func ensureConfiguredOperatorTargets(cfg team.Team, targets []string, command string) error {
	for _, target := range targets {
		if err := ensureOperatorCommandTarget(cfg, target, command); err != nil {
			return err
		}
		found := false
		for _, member := range cfg.Members {
			if memberHandle(member) == target {
				found = true
				break
			}
		}
		if !found {
			return usageErrorf("%s target %q is not a configured agent handle", command, target)
		}
	}
	return nil
}

func splitOperatorRecipients(raw string) []string {
	seen := map[string]bool{}
	var out []string
	for _, target := range strings.Split(raw, ",") {
		target = strings.TrimSpace(target)
		if target == "" || seen[target] {
			continue
		}
		seen[target] = true
		out = append(out, target)
	}
	sort.Strings(out)
	return out
}

func operatorBroadcastRecipients(cfg team.Team, operatorHandle string) []string {
	seen := map[string]bool{}
	var recipients []string
	for _, member := range cfg.Members {
		handle := strings.TrimSpace(memberHandle(member))
		if handle == "" || handle == operatorHandle || seen[handle] {
			continue
		}
		seen[handle] = true
		recipients = append(recipients, handle)
	}
	sort.Strings(recipients)
	return recipients
}

func canonicalBroadcastThread(subject string) string {
	return "broadcast/" + sanitizeWorkstreamName(subject)
}

func operatorActContext(ctx operatorControlContext) act.Context {
	return act.Context{Project: ctx.Project, Profile: ctx.Profile, Session: ctx.Session}
}

func printOperatorControlPreview(jsonOut bool, preview operatorControlPreview) error {
	if jsonOut {
		return printJSONEnvelope("operator_control_preview", preview)
	}
	fmt.Printf("Preview only; no message was sent.\nRecipients: %s\nThread: %s\nKind: %s\nCommand: %s\nRe-run with --yes to confirm.\n",
		strings.Join(preview.Recipients, ","), preview.Thread, preview.Kind, preview.Preview)
	return nil
}

func equalStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
