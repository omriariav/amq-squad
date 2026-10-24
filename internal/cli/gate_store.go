package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/buzz"
	"github.com/omriariav/amq-squad/v3/internal/gatestore"
	"github.com/omriariav/amq-squad/v3/internal/gatetarget"
	"github.com/omriariav/amq-squad/v3/internal/gateverify"
	"github.com/omriariav/amq-squad/v3/internal/grants"
	"github.com/omriariav/amq-squad/v3/internal/nostr"
	"github.com/omriariav/amq-squad/v3/internal/operatorauth"
)

// The four typed gates (tag and release, external send, credential change,
// growth beyond the grant; plan 7.1, R30) live in the gate store the core
// writes (internal/gatestore) and are answered only by the owner's signed
// Buzz reply, verified by the core (internal/gateverify, R32). There is no
// AMQ-answer path for them (3-1 ruling P, 2a-2 ruling 6).

// typedGateAction reports whether an operatorauth action is one of the typed
// gates' actions.
func typedGateAction(action string) bool { return gatetarget.Class(action) != "" }

// errTypedOperatorAnswer is the refusal `operator answer` gives a typed gate.
// GateUpgradeNote is the upgrade note of the squad identity (amendment 28).
const GateUpgradeNote = "gates raised before the squad field are closed; raise them again under a new id"

const typedOperatorAnswerRefusal = "typed gates (tag and release, external send, credential change, growth) are answered only by the owner's signed Buzz reply in the gate thread; the core verifies it with 'amq-squad gate answer'. An AMQ operator answer never counts (plan 7.3, R32)."

// gateTransportFunc connects the core to the binding's relay and returns the
// relay, the share body key and its NIP-OA tag for kind 9 posts. The Buzz
// attachment (brief 4-5) provides it: the share the owner script enrolled.
// Without an enrolled share it fails and a raised gate's question waits in
// the store (gate post sends it).
type gateTransportFunc func(ctx context.Context, b gateverify.Binding, pin gateverify.Pin) (gateverify.Relay, nostr.SecretKey, []string, error)

var gateTransport gateTransportFunc = buzz.GateTransport

// gateBindingPath, gateTrustStorePath are the owner files (seams for tests).
var (
	gateBindingPath    = gateverify.DefaultBindingPath
	gateTrustStorePath = gateverify.DefaultTrustStorePath
)

// gateCore builds the core for a session root. withTransport also connects the
// relay; without it the core can raise (record only) and read.
func gateCore(ctx context.Context, sel contextResolution, withTransport bool) (*gateverify.Core, error) {
	store, err := gatestore.Open(sel.Root)
	if err != nil {
		return nil, err
	}
	b, pin, err := gateverify.LoadBinding(gateBindingPath())
	if err != nil {
		return nil, err
	}
	issuer, err := operatorauth.LoadEd25519Signer(b.IssuerKey)
	if err != nil {
		return nil, fmt.Errorf("issuer key: %w", err)
	}
	owner, err := grants.Load("")
	core := &gateverify.Core{
		Store: store, Pin: pin, RelayURL: b.Relay, Issuer: issuer, Carriers: b.Carriers, Version: "3",
		Namespace: gateNamespace(sel.ProjectDir, sel.Profile, sel.Session, sel.NamespaceGeneration),
		Insecure:  err != nil || owner.Insecure,
		Now:       hookNow,
	}
	if withTransport {
		relay, body, grant, err := gateTransport(ctx, b, pin)
		if err != nil {
			return core, err
		}
		core.Relay, core.Body, core.BodyGrant = relay, body, grant
	}
	return core, nil
}

// storeGateRaise is `gate raise` for a typed action: the core writes the gate
// record (O_EXCL) under the session root, then posts the question when a
// transport is attached.
func storeGateRaise(sel contextResolution, gate, action, target string, window time.Duration, jsonOut bool, out io.Writer) error {
	id := strings.TrimPrefix(gate, "gate/")
	if err := gatetarget.Validate(action, target); err != nil {
		return usageErrorf("gate raise: %v", err)
	}
	ctx := context.Background()
	core, err := gateCore(ctx, sel, true)
	if core == nil {
		return fmt.Errorf("gate raise: %w", err)
	}
	transportErr := err
	spec := gatestore.Spec{GateID: id, Action: action, Target: target, RaisedBy: sel.Handle, Window: window, Squad: core.Store.Squad()}
	var rec gatestore.Record
	var q gatestore.Question
	if transportErr == nil {
		rec, q, err = core.Raise(ctx, spec)
	} else {
		rec, err = gatestore.Render(spec, hookNow(), core.Identity())
		if err == nil {
			err = core.Store.Create(rec)
		}
	}
	if errors.Is(err, os.ErrExist) {
		// 4-5 re-pass ruling: no migration of gates raised before the squad
		// identity; their ids stay taken (records are exclusive)
		if old, lerr := core.Store.Load(id); lerr == nil && old.Squad == "" {
			return fmt.Errorf("gate raise: %w; %s", err, GateUpgradeNote)
		}
	}
	if err != nil && rec.GateID == "" {
		return fmt.Errorf("gate raise: %w", err)
	}
	// question_sent follows the published marker, never the event id: a
	// question can be recorded and still not have reached the relay
	data := gateRaiseData{Gate: rec, QuestionSent: core.Store.QuestionPublished(rec.GateID), QuestionEventID: q.EventID}
	if err != nil {
		data.QuestionError = err.Error()
	} else if transportErr != nil {
		data.QuestionError = transportErr.Error()
	}
	if jsonOut {
		return writeJSONEnvelope(out, "gate_raise", data)
	}
	fmt.Fprintf(out, "gate %s raised: %s %s (digest %s, until %s)\n", rec.GateID, rec.TypedClass, rec.Target, rec.Digest, rec.NotAfter)
	if data.QuestionSent {
		fmt.Fprintf(out, "question posted in the owner's DM thread: %s\n", q.EventID)
	} else {
		fmt.Fprintf(out, "question not sent yet: %s\nsend it with: amq-squad gate post --gate %s\n", data.QuestionError, rec.GateID)
	}
	return nil
}

type gateRaiseData struct {
	Gate            gatestore.Record `json:"gate"`
	QuestionSent    bool             `json:"question_sent"`
	QuestionEventID string           `json:"question_event_id,omitempty"`
	QuestionError   string           `json:"question_error,omitempty"`
}

// gateStoreFlags are the context flags of the store verbs.
type gateStoreFlags struct {
	fs                        *flag.FlagSet
	project, profile, session *string
	me                        *string
	jsonOut                   *bool
	gate                      *string
}

func newGateStoreFlags(name, usage string) gateStoreFlags {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	g := gateStoreFlags{fs: fs,
		project: fs.String("project", "", "project/team-home directory"),
		profile: fs.String("profile", "", "team profile"),
		session: fs.String("session", "", "exact workstream/session"),
		me:      fs.String("me", "", "handle (default: resolved runtime handle)"),
		jsonOut: fs.Bool("json", false, "emit schema-versioned JSON"),
		gate:    fs.String("gate", "", "gate id, with or without gate/ prefix"),
	}
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	return g
}

func (g gateStoreFlags) resolve(args []string) (contextResolution, error) {
	if err := parseFlags(g.fs, args); err != nil {
		return contextResolution{}, err
	}
	if g.fs.NArg() > 0 {
		return contextResolution{}, usageErrorf("%s takes no positional arguments; got %q", g.fs.Name(), g.fs.Arg(0))
	}
	sel, err := resolveGateRaiseContext(*g.project, *g.profile, *g.session, *g.me, g.fs)
	if err != nil {
		return sel, err
	}
	emitContextDiagnostics(sel)
	return sel, nil
}

func (g gateStoreFlags) gateID() (string, error) {
	id := strings.TrimPrefix(strings.TrimSpace(*g.gate), "gate/")
	if err := gatestore.ValidGateID(id); err != nil {
		return "", usageErrorf("%s: %v", g.fs.Name(), err)
	}
	return id, nil
}

const gateAnswerUsage = `amq-squad gate answer - verify the owner's signed answer to a typed gate

Usage:
  amq-squad gate answer --event EVENT_ID [--project DIR] [--session S] [--json]
  amq-squad gate answer --from HANDLE --label nostr:EVENT_ID [--label L]... [--json]

The core fetches the owner's event from the relay and verifies it against the
stored gate record: the owner's Buzz key, the DM channel, the answer tuple and
its digest prefix, the window, the gate thread, the question's edit and
deletion history, single use. A verified approve writes answers/<id>.json (an
operatorauth envelope), a verified deny the signed deny record. An answer that
matches an open gate but was not sent in its thread gets one "reply here to
confirm" in the thread and is not accepted. --label is the mailbox path: the
message's nostr:<event id> label, from a carrier handle of the binding.

Exit codes: 0 approved, 11 denied, 10 re-asked (pending), 13 rejected.
`

type gateLabels []string

func (l *gateLabels) String() string     { return strings.Join(*l, ",") }
func (l *gateLabels) Set(v string) error { *l = append(*l, v); return nil }

func runGateAnswer(args []string) error {
	g := newGateStoreFlags("gate answer", gateAnswerUsage)
	event := g.fs.String("event", "", "the owner's event id (64 hex)")
	from := g.fs.String("from", "", "mailbox sender of a labelled message")
	var labels gateLabels
	g.fs.Var(&labels, "label", "AMQ message label (repeatable)")
	sel, err := g.resolve(args)
	if err != nil {
		return err
	}
	if (*event == "") == (len(labels) == 0) {
		return usageErrorf("gate answer takes --event or --label (with --from), not both")
	}
	ctx := context.Background()
	core, err := gateCore(ctx, sel, true)
	if err != nil {
		return fmt.Errorf("gate answer: %w", err)
	}
	var r gateverify.Result
	if *event != "" {
		r = core.HandleEventID(ctx, *event)
	} else {
		r = core.HandleLabel(ctx, gateverify.MailboxMessage{ID: "cli", From: *from, Labels: labels})
	}
	if *g.jsonOut {
		if err := writeJSONEnvelope(os.Stdout, "gate_answer", r); err != nil {
			return err
		}
	} else {
		fmt.Printf("gate %s: %s %s %s\n", r.GateID, r.Outcome, r.Code, r.Detail)
	}
	return gateResultErr(r)
}

func gateResultErr(r gateverify.Result) error {
	switch r.Outcome {
	case operatorauth.DecisionApproved:
		return nil
	case operatorauth.DecisionDenied:
		return &ActionDecisionError{Decision: actionDecisionDenied, Message: "gate denied by the owner"}
	case "reasked":
		return &ActionDecisionError{Decision: actionDecisionPending, Message: "gate re-asked in its thread"}
	}
	return &ActionDecisionError{Decision: actionDecisionUnbound, Message: "gate answer rejected: " + r.Code}
}

const gatePostUsage = `amq-squad gate post - send a raised gate's question (once)

Usage:
  amq-squad gate post --gate ID [--project DIR] [--session S] [--json]
`

func runGatePost(args []string) error {
	g := newGateStoreFlags("gate post", gatePostUsage)
	sel, err := g.resolve(args)
	if err != nil {
		return err
	}
	id, err := g.gateID()
	if err != nil {
		return err
	}
	ctx := context.Background()
	core, err := gateCore(ctx, sel, true)
	if err != nil {
		return fmt.Errorf("gate post: %w", err)
	}
	q, err := core.Store.PostQuestion(id, core.QuestionSender(ctx))
	if err != nil {
		return fmt.Errorf("gate post: %w", err)
	}
	if *g.jsonOut {
		return writeJSONEnvelope(os.Stdout, "gate_post", q)
	}
	fmt.Printf("gate %s question posted: %s\n", id, q.EventID)
	return nil
}

const gateStatusUsage = `amq-squad gate status - typed gates in the session's gate store

Usage:
  amq-squad gate status [--gate ID] [--project DIR] [--session S] [--json]

States: open, accepted (a verified owner approval; final), denied, expired
(not answered before not_after).
`

type gateStatusRow struct {
	gatestore.Record
	State        string `json:"state"`
	QuestionSent bool   `json:"question_sent"`
	Reasked      bool   `json:"reasked"`
}

func runGateStatus(args []string) error {
	g := newGateStoreFlags("gate status", gateStatusUsage)
	sel, err := g.resolve(args)
	if err != nil {
		return err
	}
	store, err := gatestore.Open(sel.Root)
	if err != nil {
		return err
	}
	ids, err := store.IDs()
	if err != nil {
		return err
	}
	if *g.gate != "" {
		id, err := g.gateID()
		if err != nil {
			return err
		}
		ids = []string{id}
	}
	rows := []gateStatusRow{}
	now := hookNow()
	for _, id := range ids {
		rec, err := store.Load(id)
		if err != nil {
			return fmt.Errorf("gate status: %w", err)
		}
		q, _ := store.Question(id)
		re, _ := store.ReadReask(id)
		rows = append(rows, gateStatusRow{Record: rec, State: store.State(rec, now), QuestionSent: q != nil && store.QuestionPublished(id), Reasked: re != nil})
	}
	if *g.jsonOut {
		return writeJSONEnvelope(os.Stdout, "gate_status", rows)
	}
	if len(rows) == 0 {
		fmt.Println("no typed gates")
	}
	for _, r := range rows {
		fmt.Printf("%s\t%s\t%s\t%s\traised_by=%s\n", r.GateID, r.State, r.TypedClass, r.Target, r.RaisedBy)
	}
	return nil
}

// storeVerifyAction is `verify action` for a typed action: approved only when
// the gate store holds an approval for this gate, action and target whose
// envelope verifies against the owner's trust store and whose owner event
// re-verifies (gateverify.VerifyAnswerFile).
func storeVerifyAction(root string, ns gateverify.Namespace, gate, action, target string) verifyActionResult {
	id := strings.TrimPrefix(gate, "gate/")
	result := verifyActionResult{Action: action, Target: target, Gate: "gate/" + id, Decision: actionDecisionNoGate}
	fail := func(decision, code, detail string) verifyActionResult {
		result.Decision = decision
		result.Failures = append(result.Failures, verifyMergeFailure{Code: code, Detail: detail})
		return result
	}
	store, err := gatestore.Open(root)
	if err != nil {
		return fail(actionDecisionNoGate, "gate_store", err.Error())
	}
	rec, err := store.Load(id)
	if err != nil {
		return fail(actionDecisionNoGate, "no_gate_record", err.Error())
	}
	if rec.Class != action || rec.Digest != gatetarget.Digest(action, target) {
		return fail(actionDecisionUnbound, "gate_mismatch", "the gate record binds "+rec.Class+" "+rec.Target)
	}
	switch store.State(rec, hookNow()) {
	case gatestore.StateDenied:
		return fail(actionDecisionDenied, "denied", "the owner denied gate "+id)
	case gatestore.StateOpen, gatestore.StateExpired:
		return fail(actionDecisionPending, "no_verified_answer", "no verified owner answer for gate "+id)
	}
	if err := verifyStoreAnswer(store, rec, ns); err != nil {
		return fail(actionDecisionUnbound, "answer_unverified", err.Error())
	}
	result.Decision, result.AnsweredBy = actionDecisionApproved, "owner"
	if f, _ := store.ReadAnswer(id); f != nil {
		result.MessageID = f.Envelope.Payload.AnswerMessageID
	}
	return result
}

// gateNamespace is the namespace an envelope binds; the core signs and every
// verifier checks the same tuple, so an approval never counts in another
// project, profile, session or generation.
func gateNamespace(project, profile, session, generation string) gateverify.Namespace {
	return gateverify.Namespace{ProjectDir: project, Profile: profile, Session: session, Generation: firstOf(generation, "none")}
}

// verifyStoreAnswer runs the full consumer check on a gate's approval: the
// recovered decision is accepted, and the approval passes VerifyAnswerFile
// against the owner's binding, trust store and the caller's namespace.
func verifyStoreAnswer(store gatestore.Store, rec gatestore.Record, ns gateverify.Namespace) error {
	_, pin, err := gateverify.LoadBinding(gateBindingPath())
	if err != nil {
		return err
	}
	trust, err := operatorauth.LoadAuthorizationTrustStore(gateTrustStorePath())
	if err != nil {
		return err
	}
	return gateverify.Verifier{Trust: trust, Pin: pin, Namespace: ns}.Accepted(store, rec)
}

const verifyStoreAuthorizationUsage = `amq-squad verify authorization --gate - verify a typed gate's authorization

Usage:
  amq-squad verify authorization --gate ID --action TYPED_ACTION --target TARGET [--project DIR] [--session S] [--trust-store FILE] [--json]

The authorization is the session gate store's answers/<id>.json: the envelope
is checked against the owner's trust store (default
~/.amq-squad/authorization-trust.json) and the issuer the gate record names,
every bound field against the immutable record, and the owner's signed Buzz
event is re-verified. It never performs the action.
`

// runVerifyStoreAuthorization is `verify authorization --gate`: the full
// consumer check (gateverify.VerifyAnswerFile) on answers/<id>.json, plus the
// caller's exact action and target against the immutable record.
func runVerifyStoreAuthorization(args []string) error {
	g := newGateStoreFlags("verify authorization", verifyStoreAuthorizationUsage)
	action := g.fs.String("action", "", "typed action expected by the caller")
	target := g.fs.String("target", "", "exact target string expected by the caller")
	trustPath := g.fs.String("trust-store", "", "owner trust store (default ~/.amq-squad/authorization-trust.json)")
	sel, err := g.resolve(args)
	if err != nil {
		return err
	}
	id, err := g.gateID()
	if err != nil {
		return err
	}
	canonical, err := operatorauth.CanonicalAction(*action)
	if err != nil || !typedGateAction(canonical) {
		return usageErrorf("verify authorization --gate needs a typed --action (%s)", strings.Join(gatetarget.Actions(), ", "))
	}
	if err := operatorauth.ValidateCanonicalSingleLineField("target", *target, true); err != nil {
		return usageErrorf("verify authorization: %v", err)
	}
	store, err := gatestore.Open(sel.Root)
	if err != nil {
		return err
	}
	rec, err := store.Load(id)
	if err != nil {
		return err
	}
	if rec.Class != canonical || rec.Target != *target {
		return usageErrorf("gate %s binds %s %q, not %s %q", id, rec.Class, rec.Target, canonical, *target)
	}
	if *trustPath != "" {
		prev := gateTrustStorePath
		gateTrustStorePath = func() string { return *trustPath }
		defer func() { gateTrustStorePath = prev }()
	}
	if err := verifyStoreAnswer(store, rec, gateNamespace(sel.ProjectDir, sel.Profile, sel.Session, sel.NamespaceGeneration)); err != nil {
		return fmt.Errorf("gate %s authorization does not verify: %w", id, err)
	}
	f, _ := store.ReadAnswer(id)
	result := verifyAuthorizationResult{OK: true, AuthorizationID: f.Envelope.Payload.AuthorizationID, Action: rec.Class, Target: rec.Target, Gate: "gate/" + id, KeyID: f.Envelope.Signature.KeyID}
	if *g.jsonOut {
		return printJSONEnvelope("verify_authorization", result)
	}
	fmt.Printf("signed authorization verified: %s\naction: %s\ntarget: %s\ngate: %s\nkey_id: %s\n", result.AuthorizationID, result.Action, result.Target, result.Gate, result.KeyID)
	return nil
}
