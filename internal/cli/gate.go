package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/operatorauth"
	"github.com/omriariav/amq-squad/v2/internal/state"
	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

type actionKindsData struct {
	TaxonomyVersion int                             `json:"taxonomy_version"`
	Actions         []operatorauth.ActionCapability `json:"actions"`
}

var resolveGateRaiseContext = resolveScopedCommandContext

func printActionKinds(jsonOut bool) error {
	data := actionKindsData{TaxonomyVersion: operatorauth.ActionTaxonomyVersion, Actions: operatorauth.ActionCapabilities()}
	if jsonOut {
		return printJSONEnvelope("authorization_action_kinds", data)
	}
	for _, capability := range data.Actions {
		fmt.Fprintf(os.Stdout, "%s\t%s\thuman_only=%t\tself_eligible=%t\n", capability.GateKind, capability.Action, capability.HumanOnly, capability.SelfEligible)
	}
	return nil
}

func runGate(args []string) error {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, `amq-squad gate - durable atomic authorization requests

Usage:
  amq-squad gate raise [options]
  amq-squad gate close [options]

Subcommands:
  raise  send one typed, exact-target authorization request to the operator
  close  close or withdraw the exact currently-open request generation

Examples:
  amq-squad gate raise --gate merge-414 --kind merge --action protected_branch_push --target "PR #414 head abcdef0 into main"
  amq-squad gate close --session issue-414 --me cto --gate merge-414 --reason "candidate superseded"
`)
		return nil
	}
	switch args[0] {
	case "raise":
		return runGateRaise(args[1:])
	case "close":
		return runGateClose(args[1:])
	default:
		return usageErrorf("unknown 'gate' subcommand %q", args[0])
	}
}

func runGateRaise(args []string) error {
	fs := flag.NewFlagSet("gate raise", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory")
	profileFlag := fs.String("profile", "", "team profile")
	sessionFlag := fs.String("session", "", "exact workstream/session")
	meFlag := fs.String("me", "", "sender handle (default: resolved runtime handle)")
	gateFlag := fs.String("gate", "", "gate topic, with or without gate/ prefix")
	kindFlag := fs.String("kind", "", "catalog gate kind")
	actionFlag := fs.String("action", "", "catalog atomic action")
	targetFlag := fs.String("target", "", "exact case-sensitive target")
	noteFlag := fs.String("note", "", "optional integrity-bearing note")
	taskFlag := fs.String("task", "", "optional canonical task id bound into the typed request")
	toFlag := fs.String("to", "", "operator handle (default: configured operator)")
	listKinds := fs.Bool("list-kinds", false, "list the shared action catalog without resolving project context")
	jsonOut := fs.Bool("json", false, "emit schema-versioned JSON")
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad gate raise - send one typed authorization request

Usage:
  amq-squad gate raise --gate TOPIC --kind KIND --action ACTION --target TARGET [--note TEXT] [--project DIR] [--profile P] [--session S] [--me HANDLE] [--to OPERATOR] [--json]
  amq-squad gate raise --list-kinds [--json]

Examples:
  amq-squad gate raise --session issue-414 --me cto --gate merge-414 --kind merge --action protected_branch_push --target "PR #414 head abcdef0 into main"
  amq-squad gate raise --list-kinds --json
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageErrorf("gate raise takes no positional arguments; got %q", fs.Arg(0))
	}
	if *listKinds {
		return printActionKinds(*jsonOut)
	}
	gate, err := canonicalGateTopic(*gateFlag)
	if err != nil {
		return usageErrorf("gate raise: %v", err)
	}
	capability, err := operatorauth.ValidateGateAction(*kindFlag, *actionFlag)
	if err != nil {
		return usageErrorf("gate raise: %v", err)
	}
	target, note := *targetFlag, *noteFlag
	if err := operatorauth.ValidateCanonicalSingleLineField("target", target, true); err != nil {
		return usageErrorf("gate raise: %v", err)
	}
	if err := operatorauth.ValidateCanonicalSingleLineField("note", note, false); err != nil {
		return usageErrorf("gate raise: %v", err)
	}
	taskID := strings.TrimSpace(*taskFlag)
	if taskID != "" {
		if err := validateTaskIDLeaf(taskID); err != nil {
			return usageErrorf("gate raise: %v", err)
		}
	}

	resolve := func() (contextResolution, error) {
		return resolveGateRaiseContext(*projectFlag, *profileFlag, *sessionFlag, *meFlag, fs)
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
	if err := ensureNoNamespaceMigration("gate raise", ctx.ProjectDir, ctx.Profile, ctx.Session); err != nil {
		return err
	}
	if strings.TrimSpace(ctx.Handle) == "" {
		return usageErrorf("gate raise requires a resolved sender handle; pass --me")
	}
	if taskID != "" {
		if _, err := taskstore.ShowForProfile(ctx.ProjectDir, ctx.Profile, ctx.Session, taskID); err != nil {
			return usageErrorf("gate raise task binding: %v", err)
		}
	}
	cfg, err := team.ReadProfile(ctx.ProjectDir, ctx.Profile)
	if err != nil {
		return fmt.Errorf("read team profile: %w", err)
	}
	operatorHandle := team.EffectiveOperator(cfg).Handle
	to := strings.TrimSpace(*toFlag)
	if to == "" {
		to = operatorHandle
	}
	if to == "" || to != operatorHandle {
		return usageErrorf("gate raise target must be the configured operator handle %q", operatorHandle)
	}
	request := operatorauth.GateRequestContext{
		SchemaVersion: operatorauth.GateRequestSchemaVersion, TaxonomyVersion: operatorauth.ActionTaxonomyVersion,
		Gate: gate, Thread: gate,
		Namespace: operatorauth.NamespaceBinding{ProjectDir: ctx.ProjectDir, Profile: ctx.Profile, Session: ctx.Session, NamespaceID: squadnamespace.ID(ctx.Profile, ctx.Session), Generation: ctx.NamespaceGeneration},
		GateKind:  capability.GateKind, Action: capability.Action, Target: target, Note: note, TaskID: taskID,
	}
	if err := operatorauth.ValidateGateRequest(request); err != nil {
		return usageErrorf("gate raise: %v", err)
	}
	body := fmt.Sprintf("Gate-Kind: %s\nAction: %s\nTarget: %s", request.GateKind, request.Action, request.Target)
	if request.Note != "" {
		body += "\nNote: " + request.Note
	}
	if request.TaskID != "" {
		body += "\nTask: " + request.TaskID
	}
	return sendOperatorAMQ(operatorSendOptions{
		Command: "gate raise", Project: ctx.ProjectDir, Profile: ctx.Profile, Session: ctx.Session,
		From: ctx.Handle, To: to, Thread: gate, Kind: string(state.KindQuestion), Subject: "APPROVAL: " + strings.TrimPrefix(gate, "gate/"), Body: body,
		Context: map[string]any{"authorization_request": request}, JSON: *jsonOut, Out: os.Stdout,
		FollowUp: "amq-squad verify action --project " + shellQuote(ctx.ProjectDir) + operatorProfileArg(ctx.Profile) + " --session " + shellQuote(ctx.Session) + " --gate " + shellQuote(gate) + " --action " + shellQuote(request.Action) + " --target " + shellQuote(request.Target),
	})
}
