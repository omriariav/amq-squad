package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// PR5 / #498 U6: the OWNED DELIVERY BOUNDARY.
//
// FOUR properties, and each has a named failure mode. Property (1b) was added by codex finding 3: this
// header said "three" while the boundary published a durable directive between the last world-check and
// pane input, which is the U4.2 violation. A property list that omits the ordering requirement is how the
// gap survived a review that read this comment.
//
//  1. THE DIRECTIVE IS DURABLE AND COMES FIRST. An audited automatic action must leave a record of
//     WHY it happened that survives the process, and it must exist BEFORE the pane is touched. After
//     is too late: a crash between input and record produces a delivery nobody can explain, which is
//     the worst outcome for an operator trying to reconstruct what an automatic system did to their
//     agent. Directive failure or unknown result therefore produces ZERO pane input.
//
//  1b. THE DELIVERY-TIME GATES RE-RUN AFTER THE DIRECTIVE, IMMEDIATELY BEFORE INPUT. Property (1) puts
//     an unbounded durable send between the executor's world-check and the pane, so freshness, launch
//     generation and pane state must all be re-established here or the last check is a check about a
//     world that has moved. Ratified text: acceptance line 64 and line 352 say pane INPUT; line 89 says
//     the directive comes before input. Both hold only in this order.
//
//  2. THE INPUT GOES THROUGH THE OWNED ABSTRACTION, never raw send-keys. sendPromptToPane is the
//     package's pane-input seam (runtime_actions.go:247, wrapping tmuxpane.SendPromptToPane) and it
//     is what goal delivery already uses. Raw `tmux send-keys` bypasses the queueing, target
//     validation and error typing that abstraction owns -- and the error typing is load-bearing here,
//     see (3).
//
//  3. THERE ARE THREE OUTCOMES, NOT TWO. Success, failure, and UNKNOWN. tmuxpane.QueuedInputError
//     means the input was queued but its arrival is unconfirmed: the bytes may or may not have
//     reached the pane. Collapsing that into either success or failure is the double-delivery bug --
//     treated as failure a retry could deliver twice, treated as success a lost resume looks
//     completed. It is returned as its own typed condition so the executor leaves the reservation
//     INDETERMINATE and no automatic path retries.

// supervisionDirectivePublisher records the durable audit directive. Injected so the
// directive-failure and directive-unknown falsifiers are expressible; production supplies the real
// publisher.
type supervisionDirectivePublisher func(assessment GoalSupervisionAssessment, supervisor, payload string) error

// supervisionUnknownDeliveryError marks a delivery whose arrival could not be confirmed.
//
// A distinct type rather than a sentinel string because callers must be able to branch on it: the
// executor's handling of unknown differs from its handling of failure, and matching on message text
// would break the moment a message is reworded.
type supervisionUnknownDeliveryError struct {
	Detail string
	Cause  error
}

func (e *supervisionUnknownDeliveryError) Error() string {
	return "native resume delivery result is UNKNOWN: " + e.Detail
}
func (e *supervisionUnknownDeliveryError) Unwrap() error { return e.Cause }

// supervisionDefiniteNonDeliveryError marks a delivery failure PROVEN to have produced ZERO pane input
// (codex finding 5).
//
// WHY IT EXISTS. Every error out of this boundary was reported by the executor as
// DeliveryOutcomeUnknown -- "the bytes may or may not have landed". Three of the boundary's refusals
// happen strictly BEFORE sendPromptToPane is called at all, so no bytes exist to be ambiguous about:
// a blank pane id, a missing directive publisher, and a failed directive publication. Reporting proven
// non-delivery as ambiguous strands the pause in an operator decision that has no question in it, and
// the field's own documentation (goal_supervision_resume.go:91) said it is set "ONLY on the pane-input
// error path" while its single write site set it for all of these.
//
// THE FIX IS REPORT FIDELITY ONLY, ruled and deliberately narrow. This type NEVER causes the
// reservation to be cleared, released or deleted. A claim that removes itself on a proven
// non-delivery would manufacture exactly the orphan-companion state codex finding 2 is about, and
// nothing in this system deletes reservations. What changes is what the operator is TOLD: "no delivery
// occurred" instead of "inspect the pane to find out".
//
// POLARITY IS FAIL-CLOSED, and this is the part to preserve if anyone edits it: UNKNOWN REMAINS THE
// DEFAULT and a definite verdict requires PROOF. The executor marks unknown unless this type is
// present. The inverse -- assume definite, mark unknown -- reads as equivalent and is a fail-open at
// the one seam where a wrong answer costs a double delivery.
type supervisionDefiniteNonDeliveryError struct {
	Detail string
	Cause  error
	// Refusal carries the delivery-time gate verdict when that is what stopped the delivery, so the
	// operator sees the gate's own clause/detail/recovery rather than a paraphrase composed here. Nil
	// for the pre-directive wiring refusals, which have no gate verdict to report.
	Refusal *supervisionResumeRefusal
}

func (e *supervisionDefiniteNonDeliveryError) Error() string {
	return "native resume was NOT delivered and no pane input occurred: " + e.Detail
}
func (e *supervisionDefiniteNonDeliveryError) Unwrap() error { return e.Cause }

// supervisionDeliveryTimeGate re-runs the U5/U4.2 delivery-time gates from INSIDE this boundary.
//
// It is a captured dependency rather than a parameter of the returned closure because the closure must
// stay a function of the PAYLOAD ONLY -- see newSupervisionResumeDelivery. The executor supplies bytes;
// it does not get to choose whether the world is re-checked.
type supervisionDeliveryTimeGate func() *supervisionResumeRefusal

// agentDirForSupervisionResume resolves the agent directory whose launch record holds the goal
// binding. Derived from the assessment's own binding rather than re-resolved from flags, so the
// payload is read from the SAME agent the assessment is about.
func agentDirForSupervisionResume(assessment GoalSupervisionAssessment) (string, error) {
	handle := strings.TrimSpace(assessment.Binding.LeadHandle)
	if handle == "" {
		// RETURNS AN ERROR, NOT AN EMPTY PATH. My first version returned "" and claimed the loader
		// would refuse it. It would not: launch.Read("") resolves a RELATIVE path against the process
		// cwd, so a coincidental record there becomes a wrong-target read of somebody else's binding.
		// An empty string is not an error value -- using absence as a sentinel is how a missing input
		// becomes a silently different input, which is the same class as treating a failed probe as
		// proven vacancy.
		return "", fmt.Errorf("no agent handle in the assessment binding; refusing to resolve an agent directory")
	}
	// CANONICAL, PROFILE-AWARE root. My hand-built <project>/.agent-mail/<session> ignored Profile
	// entirely and would have read the DEFAULT profile's agent for a named-profile workstream --
	// silently the wrong agent, with no error anywhere. squadnamespace.AMQRoot owns this layout; a
	// second hand-rolled copy of a path convention is the same two-deciders defect as a second claim
	// key, and paths fail silently where keys fail loudly.
	root := squadnamespace.AMQRoot(assessment.Binding.Project, assessment.Binding.Profile,
		assessment.Binding.Session)
	dir := filepath.Join(root, "agents", handle)
	if !filepath.IsAbs(dir) {
		// A relative agent dir would be resolved against whatever cwd the process happens to have.
		return "", fmt.Errorf("resolved agent directory %q is not absolute; refusing a cwd-relative read", dir)
	}
	return dir, nil
}

// newSupervisionResumeDelivery builds the delivery boundary. Everything it needs is captured here so
// the executor's deliver signature stays a single-argument function of the PAYLOAD -- the executor
// must not be able to influence which pane or which directive, only what bytes.
func newSupervisionResumeDelivery(
	publishDirective supervisionDirectivePublisher,
	assessment GoalSupervisionAssessment,
	supervisor string,
	// regate is the U4.2 requirement: the gates must be re-checked HERE, after the durable directive and
	// immediately before pane input. See the call site below for why this cannot live in the executor.
	regate supervisionDeliveryTimeGate,
) func(string) error {
	return func(payload string) error {
		paneID := strings.TrimSpace(assessment.Binding.Pane.PaneID)
		if paneID == "" {
			return &supervisionDefiniteNonDeliveryError{
				Detail: "no pane identity in the assessment binding; refusing to deliver to an unresolved target",
			}
		}
		if publishDirective == nil {
			// A missing publisher is a wiring fault, and delivering without an audit record is exactly
			// what property (1) forbids. It refuses rather than degrading to an unaudited delivery.
			return &supervisionDefiniteNonDeliveryError{
				Detail: "no directive publisher supplied; an audited resume is never delivered unaudited",
			}
		}
		if regate == nil {
			// A MISSING GATE IS NOT A PASSING GATE -- the same rule as the nil reserved-claim loader. Without
			// this the whole point of the re-check could be removed by dropping one argument, and every test
			// that supplied a gate would stay green while production re-validated nothing.
			return &supervisionDefiniteNonDeliveryError{
				Detail: "no delivery-time gate was supplied, so the world cannot be re-checked immediately before pane input; refusing rather than typing against unverified state",
			}
		}

		// (1) DIRECTIVE FIRST, durably. Any failure here means zero pane input.
		if err := publishDirective(assessment, supervisor, payload); err != nil {
			return &supervisionDefiniteNonDeliveryError{
				Detail: "durable audit directive failed, so nothing was delivered: " + err.Error(),
				Cause:  err,
			}
		}

		// (1b) THE GATES, RE-RUN AFTER THE DIRECTIVE AND IMMEDIATELY BEFORE INPUT (codex finding 3).
		//
		// THIS ORDER IS FORCED BY THE RATIFIED TEXT, not chosen. Two requirements have to hold at once:
		//   U4 gate 2 (acceptance line 64): "physically after reserve/bind/U2 rescan and immediately before
		//     PANE INPUT"; A-clarification line 352 repeats "immediately before INPUT".
		//   U6 (line 89): "Before input, durably record/send the AMQ directive".
		// The only sequence satisfying both is reserve/bind -> rescan -> DIRECTIVE -> GATES -> pane input.
		//
		// WHAT WAS WRONG BEFORE. The executor's gate call was immediately before this CLOSURE, not before
		// pane input, and publishDirective sits in between -- an `amq send` SUBPROCESS plus a receipt
		// persist, of unbounded duration. In that window FreshUntil can pass, the launch generation can
		// change (a relaunch is the one drift U5 exists to catch), and the pane can go busy or be replaced.
		// The old code then typed into it with no re-check. I coded to U5 line 77's looser phrasing
		// ("immediately before DELIVERY") and wrote the tighter one in my own comment.
		//
		// THE EXECUTOR'S PRE-DIRECTIVE GATE STAYS, and is not redundant with this one: it is what keeps a
		// doomed delivery from publishing a durable directive at all. Both are killable -- one by asserting
		// no directive is published when the world has already drifted, one by asserting a refusal AFTER a
		// published directive -- so neither is decoration.
		//
		// A refusal here is DEFINITE non-delivery: nothing has been typed. The directive already published
		// says "AUTHORIZED and being attempted" and explicitly "does not assert that the resume was
		// delivered" (see supervisionProductionDirectivePublisher), so a published-then-refused record is
		// honest under its existing wording and needs no new record type.
		if refusal := regate(); refusal != nil {
			return &supervisionDefiniteNonDeliveryError{
				Detail:  "the world changed between the audit directive and pane input, so nothing was typed: " + refusal.Detail,
				Refusal: refusal,
			}
		}

		// (2) OWNED ABSTRACTION. Never tmux send-keys.
		err := sendPromptToPane(paneID, payload)
		if err == nil {
			return nil
		}

		// (3) UNKNOWN IS NOT FAILURE, and there are TWO unknown shapes, not one. I mapped only
		// QueuedInputError and wrapped SubmitUnconfirmedError as an ordinary failure -- but tmuxpane
		// documents it as EXPLICITLY AMBIGUOUS (deliver.go:270,289,297): the text was typed and the
		// submit could not be confirmed, so the resume may well be running. Calling that a failure
		// invites a retry that delivers twice, which is the exact harm this whole PR exists to prevent.
		// Both map to unknown; they are reported distinctly so the operator knows WHICH ambiguity.
		var queued *tmuxpane.QueuedInputError
		if errors.As(err, &queued) {
			return &supervisionUnknownDeliveryError{
				Detail: fmt.Sprintf("input to pane %s was queued but arrival is unconfirmed", paneID),
				Cause:  err,
			}
		}
		var unconfirmed *tmuxpane.SubmitUnconfirmedError
		if errors.As(err, &unconfirmed) {
			return &supervisionUnknownDeliveryError{
				Detail: fmt.Sprintf("input to pane %s was typed but submission could not be confirmed", paneID),
				Cause:  err,
			}
		}
		// DELIBERATELY NOT WRAPPED AS DEFINITE NON-DELIVERY, and the restraint is the point. Control has
		// already passed into sendPromptToPane, so this error is reported by the layer that was handling the
		// bytes -- we cannot prove from here that none of them reached the pane. Only the refusals ABOVE this
		// call are provably input-free. Widening the definite class to "any error that is not one of the two
		// typed unknowns" would be the fail-open polarity wearing a tidier shape.
		return fmt.Errorf("deliver native resume to pane %s: %w", paneID, err)
	}
}

// supervisionProductionDirectivePublisher is the DURABLE audit directive, on the canonical owned path.
//
// Modelled on sendOperatorAMQ (operator.go:842) rather than invented: dispatchSendArgs composes the
// send, runOwnedDurableSend executes it and persists a receipt under .amq-squad/receipts/ with
// at-most-once semantics. That receipt IS the audit property U6 requires -- a record of why an
// automatic action happened that survives the process.
//
// THE SUPERVISOR IS THE SENDER, NEVER THE OPERATOR. The supervisor is the authorizing identity under
// U1, and sending this as the operator would attribute an automatic action to a human who did not
// take it -- the impersonation amq.go explicitly refuses. So `from` is the supervisor and the thread
// is the canonical p2p pair between the supervisor and the resumed agent, derived through
// receiptCanonicalP2P so the ordering matches every other thread in the system rather than being
// composed by hand here.
//
// A RECEIPT THAT CANNOT PERSIST IS A FAILED DIRECTIVE. durableFinalReceiptPersistError is returned as
// an error, not swallowed: an unpersisted receipt means the audit record does not durably exist, and
// property (1) says no pane input happens without one. This is the branch that makes
// "refuse without a directive" true rather than aspirational -- the send may have gone out, but if
// its record did not land we do not proceed to type into the pane.
func supervisionProductionDirectivePublisher(assessment GoalSupervisionAssessment, supervisor, payload string) error {
	to := strings.TrimSpace(assessment.Binding.LeadHandle)
	if to == "" {
		return fmt.Errorf("no agent handle in the assessment binding; an audit directive with no recipient is not a record")
	}
	ctx, err := resolveAMQContextForNamespace(assessment.Binding.Project, assessment.Binding.Profile,
		assessment.Binding.Session, supervisor)
	if err != nil {
		return fmt.Errorf("resolve amq root for the supervision directive: %w", err)
	}
	ctx.Me = supervisor

	// The body states the DECISION and the EXACT EVIDENCE, so the record answers "why did this happen"
	// without a reader needing to reconstruct the assessment. The payload digest is included rather
	// than the payload: the bytes went to the pane, and the digest is what ties this record to them.
	// THE PRE-INPUT DIRECTIVE SAYS ATTEMPTING, NEVER DELIVERED.
	//
	// My first version's subject and body both said "delivered" -- published BEFORE sendPromptToPane
	// ran. If the input then failed or came back unknown, the durable audit record would assert a
	// completed delivery that never completed. An audit record that can be false is worse than no
	// audit record: it is the artifact an operator trusts when reconstructing what happened, and I
	// built it to be trustworthy and then had it state something not yet true.
	// Only a post-known-success record may claim delivered, and that record is still owed (see U6's
	// remaining work: consume/receipt then one observability-only status poll).
	body := strings.Join([]string{
		"Automatic native /goal resume AUTHORIZED and being attempted by supervision.",
		"This record is written BEFORE pane input. It does not assert that the resume was delivered.",
		"supervisor: " + supervisor,
		"namespace: " + assessment.Binding.NamespaceID,
		"policy: " + string(assessment.Policy.Mode) + " revision " + fmt.Sprint(assessment.Policy.Revision),
		"attempt: " + assessment.Binding.Goal.AttemptID,
		"pause generation: " + assessment.Binding.PauseGeneration,
		"assessment fingerprint: " + assessment.Fingerprint,
		"goal binding digest: " + assessment.Binding.Goal.BindingDigest,
		"goal command digest: " + assessment.Binding.Goal.CommandDigest,
		"authorized payload digest: " + digestGoalSupervisionString(payload),
		"launch record digest: " + assessment.Binding.LaunchRecordDigest,
		"launch record modtime: " + fmt.Sprint(assessment.Binding.LaunchRecordModTime),
		"launch id: " + assessment.Binding.LaunchID,
		"pane: " + assessment.Binding.Pane.PaneID,
	}, "\n")

	// Machine-readable context alongside the readable text, so a reconstruction does not depend on
	// parsing prose. dispatchSendArgs takes it via --context, the same way sendOperatorAMQ does.
	contextJSON, marshalErr := json.Marshal(map[string]any{
		"decision":                  "authorized_attempting",
		"supervisor":                supervisor,
		"namespace_id":              assessment.Binding.NamespaceID,
		"policy_mode":               string(assessment.Policy.Mode),
		"policy_revision":           assessment.Policy.Revision,
		"attempt_id":                assessment.Binding.Goal.AttemptID,
		"pause_generation":          assessment.Binding.PauseGeneration,
		"assessment_fingerprint":    assessment.Fingerprint,
		"goal_binding_digest":       assessment.Binding.Goal.BindingDigest,
		"goal_command_digest":       assessment.Binding.Goal.CommandDigest,
		"authorized_payload_digest": digestGoalSupervisionString(payload),
		"launch_record_digest":      assessment.Binding.LaunchRecordDigest,
		"launch_record_mod_time":    assessment.Binding.LaunchRecordModTime,
		"launch_id":                 assessment.Binding.LaunchID,
		"pane_id":                   assessment.Binding.Pane.PaneID,
	})
	if marshalErr != nil {
		return fmt.Errorf("encode supervision directive context: %w", marshalErr)
	}

	args := dispatchSendArgs(ctx.Root, supervisor, to,
		receiptCanonicalP2P(supervisor, to), "status",
		"Automatic native /goal resume AUTHORIZED (attempting)", body, "", "", 0)
	args = append(args, "--context", string(contextJSON))

	_, _, sendErr := runOwnedDurableSend(
		durableSendOptions{
			ProjectDir: assessment.Binding.Project,
			Profile:    assessment.Binding.Profile,
			Session:    assessment.Binding.Session,
			// Kind identifies the actor in the receipt namespace. The command name lives HERE and
			// nowhere else.
			Kind: "supervision_resume",
			// Invocation is DELIBERATELY UNSET. It is a durableInvocationBoundary -- a struct wrapping
			// a run callback -- not a label, and my first version assigned the string
			// "goal supervise-resume" to it. That could not compile, and dev-2 caught it by READING
			// rather than by building, which is the gate doing precisely what it exists for.
			//
			// Left at its zero value so runOwnedDurableSend uses its own default boundary. Supplying a
			// custom one would mean this publisher wanted to control retry/reconciliation semantics,
			// and it does not: it wants the STANDARD at-most-once receipt behaviour, which is exactly
			// what the default provides. Overloading a typed field to carry a name would have put a
			// label where a behaviour belongs.
		},
		amqCommandRequest{Dir: assessment.Binding.Project, Env: amqCommandEnv(ctx), Arg: args},
	)
	var persistErr *durableFinalReceiptPersistError
	if errors.As(sendErr, &persistErr) {
		return fmt.Errorf("audit directive receipt did not persist, so no delivery proceeds: %w", sendErr)
	}
	if sendErr != nil {
		return fmt.Errorf("publish supervision audit directive: %w", sendErr)
	}
	return nil
}

// PR5 / #498 U6 post-success seams. Package-level function vars, matching sendPromptToPane's shape
// (runtime_actions.go:247), so tests can substitute them and the directive/poll failure paths are
// expressible.
//
// BOTH ARE CALLED ONLY AFTER A KNOWN-SUCCESSFUL DELIVERY AND A COMPLETED CONSUME, and their errors are
// REPORTED IN THE OUTCOME rather than returned. Two corrections are folded in here, both of which I got
// wrong first:
//
//   - REPLAY PROTECTION IS CLAIM-ONCE, NOT THIS RETURN VALUE. The stale version of this comment said
//     surfacing a bookkeeping failure "invites a retry that would deliver a second audited resume". That
//     is false: a consumed claim BLOCKS re-entry at the claim-once gate, so no automatic retry reaches
//     delivery. I corrected this in the executor and left this copy standing -- fixing one instance of a
//     claim and leaving another is its own recurring defect.
//   - THE REAL REASON IS CONFLATION. Callers read a returned error as "the action failed". Here the
//     action SUCCEEDED and only its bookkeeping did not, so an ordinary error would make a completed,
//     irreversible delivery indistinguishable from one that never happened.
//
// Nor are the errors discarded any more: they travel in supervisionResumeOutcome and surface as operator
// warnings. Failure never authorizes replay AND failure evidence never disappears.
var (
	// publishDeliveredDirective records the DELIVERED audit directive -- the counterpart to the
	// pre-input AUTHORIZED one. Only this record may claim delivery, because only here is it true.
	publishDeliveredDirective supervisionDirectivePublisher = supervisionDeliveredDirectivePublisher

	// pollSupervisionStatusOnce performs the ONE fresh post-delivery status read, RENDERED so somebody can
	// see it (#498 U6 ruling 1).
	//
	// The entry point is executeStatus, the COMMAND-LEVEL read that includes rendering -- not
	// buildStatusRows, which is the row builder one layer below. That distinction is the ruling: a read
	// whose output goes nowhere makes nothing observable, so io.Discard would be ceremony rather than
	// observability. cto's first pointer named the lower layer and its "the read itself refreshes
	// observability" rationale was withdrawn; dev-2's identification is the one implemented here.
	//
	// It renders into the INVOKING COMMAND's output, so the invoker -- human or wrapper -- receives it as
	// data. That is why the writer is a parameter rather than a package default.
	pollSupervisionStatusOnce func(GoalSupervisionAssessment) error
)

// supervisionDeliveredDirectivePublisher publishes the post-success DELIVERED record on the same
// canonical owned path as the authorized one, differing only in what it asserts -- which is the entire
// point of having two records instead of one optimistic record written early.
func supervisionDeliveredDirectivePublisher(assessment GoalSupervisionAssessment, supervisor, payload string) error {
	to := strings.TrimSpace(assessment.Binding.LeadHandle)
	if to == "" {
		return fmt.Errorf("no agent handle in the assessment binding")
	}
	ctx, err := resolveAMQContextForNamespace(assessment.Binding.Project, assessment.Binding.Profile,
		assessment.Binding.Session, supervisor)
	if err != nil {
		return fmt.Errorf("resolve amq root for the delivered directive: %w", err)
	}
	ctx.Me = supervisor

	body := strings.Join([]string{
		"Automatic native /goal resume DELIVERED and the claim is consumed.",
		"This record is written AFTER known-successful pane input and a completed consume.",
		"supervisor: " + supervisor,
		"attempt: " + assessment.Binding.Goal.AttemptID,
		"pause generation: " + assessment.Binding.PauseGeneration,
		"delivered payload digest: " + digestGoalSupervisionString(payload),
		"pane: " + assessment.Binding.Pane.PaneID,
	}, "\n")

	contextJSON, marshalErr := json.Marshal(map[string]any{
		"decision":                 "delivered_and_consumed",
		"supervisor":               supervisor,
		"attempt_id":               assessment.Binding.Goal.AttemptID,
		"pause_generation":         assessment.Binding.PauseGeneration,
		"delivered_payload_digest": digestGoalSupervisionString(payload),
		"pane_id":                  assessment.Binding.Pane.PaneID,
	})
	if marshalErr != nil {
		return fmt.Errorf("encode delivered directive context: %w", marshalErr)
	}

	args := dispatchSendArgs(ctx.Root, supervisor, to,
		receiptCanonicalP2P(supervisor, to), "status",
		"Automatic native /goal resume DELIVERED", body, "", "", 0)
	args = append(args, "--context", string(contextJSON))

	_, _, sendErr := runOwnedDurableSend(
		durableSendOptions{
			ProjectDir: assessment.Binding.Project,
			Profile:    assessment.Binding.Profile,
			Session:    assessment.Binding.Session,
			Kind:       "supervision_resume_delivered",
		},
		amqCommandRequest{Dir: assessment.Binding.Project, Env: amqCommandEnv(ctx), Arg: args},
	)
	if sendErr != nil {
		return fmt.Errorf("publish delivered directive: %w", sendErr)
	}
	return nil
}

// newSupervisionStatusPoll builds the post-delivery status poll, rendering into the supplied writer.
//
// ONE read, not a wait loop, and its result is never consulted for a decision: it runs AFTER consume, so
// nothing downstream depends on it. A poll whose output influenced control flow would be a post-consume
// decider, which the no-post-claim-reassessment rule forbids -- the output is for the INVOKER, not for
// this code.
func newSupervisionStatusPoll(out io.Writer, asJSON bool) func(GoalSupervisionAssessment) error {
	return func(assessment GoalSupervisionAssessment) error {
		if out == nil {
			return fmt.Errorf("no writer for the post-delivery status snapshot; an unrendered poll observes nothing")
		}
		if !asJSON {
			// LABELLED, so a reader can tell this snapshot is post-delivery rather than the pre-delivery
			// state they were looking at when they invoked.
			fmt.Fprintln(out, "--- post-delivery status snapshot ---")
		}
		// FIELD NAMES READ FROM status.go:349, not guessed. My first version wrote Session; the field is
		// RequestedSession with an ExplicitSession companion, and ExplicitSession=true matters -- the
		// assessment is ABOUT one exact session, so the poll must not fall back to inferring a different
		// one. Third time today I typed a field name from expectation instead of from the declaration.
		return executeStatus(statusExecution{
			ProjectDir:       assessment.Binding.Project,
			Profile:          assessment.Binding.Profile,
			RequestedSession: assessment.Binding.Session,
			ExplicitSession:  true,
			Probe:            defaultDuplicateLaunchProbe,
			JSON:             asJSON,
			Out:              out,
		})
	}
}
