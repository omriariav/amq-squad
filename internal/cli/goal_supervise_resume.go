package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/launch"
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/team"
)

// PR5 / #498 U1: the PRODUCTION CALLER.
//
// Round 1 shipped executeSupervisionResume as DEAD CODE -- grep across non-test files found only its
// own definition, so #498's eligible live resume could not occur at all. This is the surface that
// makes it reachable.
//
// WHY A NEW SURFACE RATHER THAN WIRING INTO AN EXISTING ONE: there was nothing to wire into. All
// three production consumers of buildGoalSupervisionAssessment (status_board.go, status.go,
// doctor.go) are READ-ONLY REPORTERS, and no sweep, autonomy loop, or operator loop exists anywhere
// in internal/cli or internal/autonomy. Confirmed by grep before proposing, because "wire it into
// the existing actor" was the obvious answer and it was not available.
//
// NO CONFIRMATION FLAG ON THE DELIVERING PATH (ruled). Policy.Mode == SafeAuto IS the operator's
// consent, recorded once, deliberately. Re-asking per invocation converts automatic resume into
// manual resume and defeats the point of #498. The accidental-invocation concern is answered by the
// command's structure rather than by a prompt: a bare invocation is not a raw action, it is an entry
// into the whole gate chain -- fresh, eligible, policy, action identity, claim-once, revalidation --
// and under manual or notify-only policy its failure mode is a refusal, not a delivery.
//
// --dry-run is the inspection path an operator uses BEFORE enabling SafeAuto, and it must be truly
// side-effect-free: no reservation, no bind, no consume, no directive publication, nothing written
// to disk, no pane input. It reports the decision and the evidence only.

// supervisorIdentityFlagHelp is stated once and used in both the flag help and the refusal, so the
// requirement and its explanation cannot drift apart.
const supervisorIdentityFlagHelp = "REQUIRED explicit supervisor identity recorded in the claim; never inferred from the lead"

// resolveGoalSupervisionAssessment is the SINGLE assembly path for a supervision assessment.
//
// doctor.go had this sequence inline. Extracting it is not tidying: a second copy of "how an
// assessment is built" is a second decider, and the whole failure class this milestone keeps hitting
// is two places computing one thing and disagreeing. The probe defaulting in particular is easy to
// get subtly wrong in a copy -- five nil checks, any of which silently changes what the assessment
// observes.
func resolveGoalSupervisionAssessment(projectDir, profile, session string, probe duplicateLaunchProbe) (team.Team, GoalSupervisionAssessment, error) {
	t, err := team.ReadProfile(projectDir, profile)
	if err != nil {
		return team.Team{}, GoalSupervisionAssessment{}, fmt.Errorf("read team profile: %w", err)
	}
	if probe.PIDAlive == nil {
		probe.PIDAlive = defaultDuplicateLaunchProbe.PIDAlive
	}
	if probe.ProcessMatch == nil {
		probe.ProcessMatch = defaultDuplicateLaunchProbe.ProcessMatch
	}
	if probe.ProcessTTY == nil {
		probe.ProcessTTY = defaultDuplicateLaunchProbe.ProcessTTY
	}
	if probe.ProcessStartTime == nil {
		probe.ProcessStartTime = defaultDuplicateLaunchProbe.ProcessStartTime
	}
	if probe.Now == nil {
		probe.Now = defaultDuplicateLaunchProbe.Now
	}
	rows := buildStatusRows(t, profile, session, probe)
	ctx := newSessionStatusContext(t, profile, session, firstLiveTmuxSession(rows))
	ns := squadnamespace.Resolve(t.Project, ctx.Profile, session)
	invariantErrors := annotateVisibilityInvariants(rows, ctx)
	conflict := namespaceConflictForProfileSession(t.Project, profile, session)
	now := probe.Now().UTC()
	gateObservation := inspectGoalSupervisionGates(t, profile, session, firstStatusRoot(rows), probe, now)
	assessment := buildGoalSupervisionAssessment(
		t, profile, session, ns, rows, gateObservation, invariantErrors,
		conflict, probe, now,
	)
	return t, assessment, nil
}

// goalSuperviseResumeReport is the --dry-run and --json projection. It carries the claim key so an
// operator can find the reservation the delivering path WOULD create, which is the whole point of
// inspecting before enabling SafeAuto.
type goalSuperviseResumeReport struct {
	SchemaVersion          int      `json:"schema_version"`
	DryRun                 bool     `json:"dry_run"`
	Decision               string   `json:"decision"`
	Detail                 string   `json:"detail,omitempty"`
	State                  string   `json:"state"`
	Fresh                  bool     `json:"fresh"`
	Eligible               bool     `json:"eligible"`
	AutomaticResumeAllowed bool     `json:"automatic_resume_allowed"`
	PolicyMode             string   `json:"policy_mode"`
	PolicyRevision         int      `json:"policy_revision"`
	Fingerprint            string   `json:"fingerprint"`
	AttemptID              string   `json:"attempt_id,omitempty"`
	PauseGeneration        string   `json:"pause_generation,omitempty"`
	ClaimKey               string   `json:"claim_key,omitempty"`
	Supervisor             string   `json:"supervisor"`
	Reasons                []string `json:"eligibility_reasons,omitempty"`

	// Per-gate evidence. FailedGate names which gate refused, so a wrapper does not parse prose.
	// SkippedGates and NotEvaluated exist so the report distinguishes three states -- passed, failed,
	// and never-reached -- rather than presenting a prefix as the whole set.
	Gates        []supervisionGateResult `json:"gates,omitempty"`
	FailedGate   string                  `json:"failed_gate,omitempty"`
	SkippedGates []string                `json:"skipped_gates,omitempty"`
	NotEvaluated []string                `json:"delivery_time_gates_not_evaluated,omitempty"`

	// Outcome carries the post-success bookkeeping result: delivered plus the two per-action flags and
	// their error text. Present only on the delivering path.
	Outcome *supervisionResumeOutcome `json:"outcome,omitempty"`

	// PostDeliveryStatus is the captured post-delivery snapshot, embedded as ONE field of this document
	// rather than emitted as a second top-level object.
	PostDeliveryStatus      json.RawMessage `json:"post_delivery_status,omitempty"`
	PostDeliveryStatusError string          `json:"post_delivery_status_error,omitempty"`
}

const goalSuperviseResumeSchemaVersion = 1

func runGoalSuperviseResume(args []string) error {
	fs := flag.NewFlagSet("goal supervise-resume", flag.ContinueOnError)
	projectFlag := fs.String("project", "", "project/team-home directory (default: cwd)")
	profileFlag := fs.String("profile", "", "team profile (default: default profile)")
	sessionFlag := fs.String("session", "", "workstream session to assess")
	supervisorFlag := fs.String("supervisor", "", supervisorIdentityFlagHelp)
	attemptFlag := fs.String("attempt-id", "", "bind this invocation to one attempt; refuses if the fresh assessment is about a different one")
	fingerprintFlag := fs.String("assessment-fingerprint", "", "bind this invocation to one assessment; refuses if the situation changed since the action was published")
	dryRun := fs.Bool("dry-run", false, "report the decision, claim key and evidence WITHOUT reserving or delivering")
	jsonOut := fs.Bool("json", false, "emit a schema-versioned decision report")
	registerScopedFlagAliases(fs, projectFlag, sessionFlag, profileFlag)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `amq-squad goal supervise-resume - deliver one audited native /goal resume, claim-once

Usage:
  amq-squad goal supervise-resume --session S --supervisor ID [--profile P] [--project DIR]
                                  [--dry-run] [--json]

CONSENT MODEL. There is deliberately NO confirmation flag. Automatic resume is
authorized by team policy: Policy.Mode == safe_auto is the operator's recorded
consent, given once when the policy is set. Asking again per invocation would
convert automatic resume into manual resume, which is the stall this command
exists to remove. Under manual or notify-only policy this command REFUSES.

A bare invocation is not a raw action. It enters the full gate chain -- fresh
assessment, eligibility, operator policy, action identity, claim-once
reservation, delivery-time revalidation -- and any gate that does not hold
produces a refusal with a recovery step, never a delivery.

--dry-run reports the decision, the claim key that WOULD be reserved, and the
supporting evidence, and performs no reservation, no bind, no consume, no
directive publication and no pane input. Use it to inspect before enabling
safe_auto.

--supervisor is REQUIRED and is never inferred from the lead identity: the
supervising actor is persisted in the claim, and a defaulted value would be
indistinguishable on disk from a deliberate one. The published action renders it
as a shell-quoted placeholder for exactly that reason: it is the one value a
machine must not fill in for you. Pasting the template unedited REFUSES rather
than recording the placeholder as an identity.

--attempt-id and --assessment-fingerprint are optional but VALIDATED. The
published action supplies both, which binds it to the pause it was published
for: if the situation has moved, this command refuses and names which field
drifted rather than acting on a different pause than the one you read about.
Omitting them (a bare invocation) is allowed; the fresh assessment then stands
on its own and the executor's binding checks still govern.

Exit codes:
  0   delivered, or dry-run reported
  11  refused by operator policy (not allowed here) - distinct from failure
  2   refused for any other reason, or delivery is INDETERMINATE
`)
	}
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return usageErrorf("goal supervise-resume takes no positional arguments")
	}

	supervisor := strings.TrimSpace(*supervisorFlag)
	if supervisor == "" {
		// REFUSE, never default. U1: explicit, nonblank, never inferred from lead identity.
		return usageErrorf("goal supervise-resume requires --supervisor: %s", supervisorIdentityFlagHelp)
	}
	// Q2: THE UNFILLED PLACEHOLDER IS NOT AN IDENTITY. Blank was already refused, but the literal
	// placeholder is NONBLANK, so it sailed through and would have been persisted in the claim as
	// though it were an actor -- a durable record naming "<your-identity>" as the authorising
	// supervisor, which is worse than a blank because it looks deliberate.
	//
	// Compared after trimming and against the SAME constant the action renders, so a template pasted
	// without editing refuses instead of resuming.
	if supervisor == supervisorIdentityPlaceholder {
		return usageErrorf("--supervisor is still the placeholder %s: replace it with your own identity. "+
			"The published command renders it as a placeholder because this is the one value that must "+
			"come from a person, and recording the placeholder itself would leave a claim that cannot "+
			"answer who authorised it", supervisorIdentityPlaceholder)
	}
	session := strings.TrimSpace(*sessionFlag)
	if session == "" {
		return usageErrorf("goal supervise-resume requires --session")
	}
	if err := team.ValidateSessionName(session); err != nil {
		return usageErrorf("invalid --session: %v", err)
	}
	// The canonical scope resolver every other scoped command uses, rather than a local
	// project/profile derivation. Scope resolution decides WHICH namespace this claim belongs to,
	// and a second derivation of that is the same two-deciders defect as a second claim key.
	ctx, err := resolveScopedCommandContext(*projectFlag, *profileFlag, session, "", fs)
	if err != nil {
		return err
	}

	_, assessment, err := resolveGoalSupervisionAssessment(ctx.ProjectDir, ctx.Profile, ctx.Session, duplicateLaunchProbe{})
	if err != nil {
		return err
	}

	// P1: BIND THE INVOCATION. Both flags are OPTIONAL but VALIDATED -- the published action always
	// supplies them, a bare human invocation may not.
	//
	// This is what turns a published command from a generic trigger into a bound one. An operator
	// pastes a command from a status board they read some time ago; the situation may have moved.
	// Without this the pasted command would happily act on whatever the CURRENT assessment says,
	// which is a different pause than the one they were looking at when they decided.
	//
	// Refusal names WHICH field drifted, because "the situation changed" without saying what changed
	// leaves the operator re-reading everything.
	if want := strings.TrimSpace(*attemptFlag); want != "" && want != strings.TrimSpace(assessment.Binding.Goal.AttemptID) {
		return fmt.Errorf("the situation changed since this action was published: it was bound to attempt %q "+
			"but the current assessment is about attempt %q. Re-read the assessment before acting",
			want, assessment.Binding.Goal.AttemptID)
	}
	if want := strings.TrimSpace(*fingerprintFlag); want != "" && want != strings.TrimSpace(assessment.Fingerprint) {
		return fmt.Errorf("the situation changed since this action was published: it was bound to assessment "+
			"fingerprint %q but the current fingerprint is %q. Re-read the assessment before acting",
			want, assessment.Fingerprint)
	}

	report := goalSuperviseResumeReport{
		SchemaVersion:          goalSuperviseResumeSchemaVersion,
		DryRun:                 *dryRun,
		State:                  string(assessment.State),
		Fresh:                  assessment.Fresh,
		Eligible:               assessment.Eligible,
		AutomaticResumeAllowed: assessment.AutomaticResumeAllowed,
		PolicyMode:             string(assessment.Policy.Mode),
		PolicyRevision:         assessment.Policy.Revision,
		Fingerprint:            assessment.Fingerprint,
		AttemptID:              assessment.Binding.Goal.AttemptID,
		PauseGeneration:        assessment.Binding.PauseGeneration,
		Supervisor:             supervisor,
	}
	for _, r := range assessment.Reasons {
		if !r.Passed {
			report.Reasons = append(report.Reasons, r.Code)
		}
	}
	// The claim key the delivering path WOULD reserve. Derived for the report only; on the dry-run
	// path nothing is written, so this is the operator's sole preview of the identity at stake.
	if key, keyErr := supervisionClaimKey(assessment.Binding.NamespaceID, assessment.Binding.PauseGeneration,
		assessment.Binding.Goal.AttemptID); keyErr == nil {
		report.ClaimKey = key
	}

	// ONE EVALUATION, consumed by both paths (#498 F5). The dry-run does not re-implement the gates
	// and does not check a subset of them: it runs the SAME evaluator the executor runs, which is what
	// makes "the dry-run predicts the executor" a structural property instead of a claim.
	//
	// It also fixes the overclaim I shipped: the old dry-run reported "would_deliver" after checking
	// AutomaticResumeAllowed ALONE, so an expired assessment or an existing claim would still print
	// would_deliver. The verdict is now named for what was actually established.
	agentDir, agentDirErr := agentDirForSupervisionResume(assessment)
	if agentDirErr != nil {
		// FAIL CLOSED before any evaluation: without a resolved agent directory there is no
		// authoritative payload to authorize, and proceeding would let the loader read whatever a
		// relative path happened to hit.
		return agentDirErr
	}
	loader := launchRecordResumePayloadLoader(agentDir)
	evaluation := evaluateSupervisionPreMutationGates(assessment, assessment.Actions.Resume, supervisor,
		func() time.Time { return time.Now().UTC() }, loader, scanRecoveryTransitionsForPause)

	report.Gates = evaluation.Results
	for _, n := range evaluation.skippedGates() {
		// NOT REACHED is its own state. Short-circuiting means the results are a PREFIX, and printing
		// only the prefix would imply it is the whole set -- the same overclaim-by-omission as a
		// mutation walk that drops an item silently.
		report.SkippedGates = append(report.SkippedGates, string(n))
	}
	if *dryRun {
		// A dry-run can NEVER evaluate these: they need the world at delivery time, after a
		// reservation exists. Named rather than omitted, because "not checked" and "passed" are
		// different facts and only one of them is evidence.
		report.NotEvaluated = supervisionDeliveryTimeGates
	}

	if failed := evaluation.firstFailure(); failed != nil {
		report.Decision = "refused"
		report.FailedGate = string(failed.Name)
		report.Detail = failed.Detail
		if emitErr := emitGoalSuperviseResumeReport(report, *jsonOut); emitErr != nil {
			return emitErr
		}
		if *dryRun {
			// A dry-run REPORTS a refusal; reporting is its job, so this is not a failure exit.
			return nil
		}
		// POLICY refusal keeps its own exit code so a wrapper can tell "not allowed here" from
		// "broke". Keyed on WHICH GATE failed rather than on a re-check of the policy flag -- one
		// evaluation, and the exit code derives from its verdict.
		if failed.Name == gateOperatorPolicy {
			return &ActionDecisionError{Decision: "denied", Message: failed.Detail}
		}
		return evaluation.refusal()
	}

	if *dryRun {
		// NAMED FOR WHAT IT ESTABLISHED. Not "would_deliver": the delivery-time gates listed in
		// NotEvaluated could still refuse, and a verdict implying delivery would be a prediction this
		// command is not entitled to make.
		report.Decision = "pre_mutation_gates_pass"
		report.ClaimKey = evaluation.ClaimKey
		return emitGoalSuperviseResumeReport(report, *jsonOut)
	}

	// The executor re-runs the evaluator itself rather than accepting this evaluation as an argument.
	// That is deliberate: passing a pre-computed verdict in would make the executor trust a caller's
	// claim that the gates passed, and the executor is the durable boundary -- it validates its own
	// preconditions. The cost is one extra read-only evaluation; the alternative is a boundary that
	// can be lied to.
	// The delivery boundary is CONSTRUCTED here with the pane and directive it may use, then handed to
	// the executor as a function of the PAYLOAD ONLY. The executor therefore cannot influence which
	// pane is written to or which directive is recorded -- only what bytes go out. Narrowing what a
	// callee can vary is the same principle as the injected loader: capability is granted, not assumed.
	//
	// THE CLOCK AND THE TWO U5 READERS ARE CONSTRUCTED ONCE, HERE, AND SHARED (codex finding 3). The executor's
	// pre-directive gate and the boundary's immediately-before-input re-check must consult the SAME clock and
	// the SAME readers, or they are two deciders about one world and can disagree about whether delivery is
	// safe. Building a second set inside the gate closure would have been the two-owners defect this milestone
	// has spent the day closing, reintroduced by the fix for it.
	clock := func() time.Time { return time.Now().UTC() }
	readGeneration := newSupervisionGenerationReader()
	readPane := newSupervisionPaneReader()
	// The gate is CAPTURED by the boundary rather than passed to the returned closure, so the closure stays a
	// function of the PAYLOAD ONLY: the executor supplies bytes and cannot choose whether the world is
	// re-checked. Capability is granted, not assumed -- the same principle as the injected loader.
	deliver := newSupervisionResumeDelivery(supervisionProductionDirectivePublisher, assessment, supervisor,
		func() *supervisionResumeRefusal {
			return evaluateSupervisionDeliveryTimeGates(assessment, clock, readGeneration, readPane)
		})
	// THE POLL'S OUTPUT DESTINATION DIFFERS BY MODE, and getting this wrong produced a real bug.
	//
	// I first pointed the poll at os.Stdout in BOTH modes. In --json that made executeStatus write a
	// complete status envelope to stdout and THEN the report write a second object -- two top-level JSON
	// documents, which is not a parseable command response and violates the dedicated
	// post_delivery_status ruling outright. "Render into the command's own output" is not the same as
	// "write to stdout"; in JSON the command's output is ONE document.
	//
	// Text mode renders the labelled snapshot straight to stdout. JSON mode captures it into a
	// command-owned buffer and embeds it as a field.
	var statusBuf bytes.Buffer
	pollTarget := io.Writer(os.Stdout)
	if *jsonOut {
		pollTarget = &statusBuf
	}
	// THE SEAM IS RESTORED ON EXIT. It is package-global, so leaving it set would leak this invocation's
	// writer and json mode into the next in-process command or test -- a cross-test contamination that
	// would be maddening to trace back to here.
	previousPoll := pollSupervisionStatusOnce
	defer func() { pollSupervisionStatusOnce = previousPoll }()
	pollSupervisionStatusOnce = newSupervisionStatusPoll(pollTarget, *jsonOut)
	outcome, err := executeSupervisionResume(assessment, assessment.Actions.Resume, supervisor,
		clock, loader,
		// U5's delivery-time readers, injected as PRODUCTION implementations. They are parameters
		// rather than package globals precisely because they authorize an irreversible delivery: a
		// mutable global that authorizes something can be nil'd into a silent pass.
		//
		// THE SAME VALUES the boundary's re-check captured above. Passing freshly constructed readers here
		// would leave the two gates reading the world through different instances.
		readGeneration, readPane,
		readReservedRecoveryTransition,
		deliver)
	report.Outcome = &outcome
	// EMBED the captured status as one field of the single report document, validating it first: an
	// unparseable capture must not be spliced in raw, or the command's own response becomes invalid JSON
	// because of a dependency's output.
	if *jsonOut && statusBuf.Len() > 0 {
		if json.Valid(statusBuf.Bytes()) {
			report.PostDeliveryStatus = json.RawMessage(statusBuf.Bytes())
		} else {
			report.PostDeliveryStatusError = "post-delivery status output was not valid JSON and was omitted"
		}
	}
	if err != nil {
		// THE DECISION MUST NOT SAY "refused" ABOUT A COMPLETED DELIVERY. I assigned refused to every
		// executor error, but a consume failure returns Delivered=true -- so the report claimed a refusal
		// for an irreversible action that happened. Same return-value-versus-reality class as the
		// Delivered=false bug, one layer up in the reporting.
		report.Decision = supervisionResumeDecision(outcome)
		report.Detail = err.Error()
		// WARNINGS BEFORE THE RETURN. They were rendered only on the success path, so a consume failure --
		// the case that most needs an operator to look -- returned without ever emitting its warning.
		for _, w := range outcome.warnings() {
			fmt.Fprintln(os.Stderr, "warning: "+w)
		}
		_ = emitGoalSuperviseResumeReport(report, *jsonOut)
		// Nonzero for everything here, including indeterminate, so no wrapper reads an indeterminate
		// outcome as success.
		return err
	}
	report.Decision = "delivered"
	// POST-SUCCESS FAILURES ARE VISIBLE, NOT SWALLOWED (#498 U6 ruling 3). Warnings go to STDERR so they
	// cannot be mistaken for the command's data output, and the structured fields travel in --json. The
	// EXIT CODE STAYS SUCCESS: the irreversible delivery happened, and a nonzero exit would invite a
	// manual retry that the consumed claim would refuse -- but which an operator might attempt against a
	// different pause instead.
	for _, w := range outcome.warnings() {
		fmt.Fprintln(os.Stderr, "warning: "+w)
	}
	return emitGoalSuperviseResumeReport(report, *jsonOut)
}

// supervisionResumeDecision maps an executor outcome to its reported decision literal.
//
// EXTRACTED FROM runGoalSuperviseResume, and the reason is evidentiary rather than aesthetic. Inline, the
// mapping was only reachable by constructing a full command fixture -- team profile on disk, launch
// record, pane lister, ledger -- so the only test I could write against it inspected the command's SOURCE
// TEXT for the literals. That test asserts strings appear in a file. It does not execute the mapping, so
// it survives SWAPPING the two indeterminate cases, which is precisely the confusion the
// delivery_outcome_unknown rename was ruled to prevent.
//
// Pure function of the outcome, so the four states become four rows with struct literals.
func supervisionResumeDecision(outcome supervisionResumeOutcome) string {
	switch {
	case outcome.DeliveryOutcomeUnknown:
		// U6's unknown pane input: the bytes may or may not have landed. Distinct from a refusal, because
		// a reservation exists and an operator must decide; and distinct from delivered_indeterminate,
		// because delivery is NOT known to have happened.
		//
		// FIRST, and the precedence is load-bearing: a caller that defensively set both this and Delivered
		// would otherwise be reported as a known delivery -- claiming an irreversible action that is not
		// known to have happened, which is the more dangerous of the two errors.
		//
		// The literal is delivery_outcome_unknown, not delivery_indeterminate. My first name sat two
		// characters from the adjacent delivered_indeterminate state, in the same switch, and both appear
		// in operator evidence -- a distinction an operator cannot reliably see is not a distinction,
		// however correct the code beneath it.
		return "delivery_outcome_unknown"
	case outcome.Delivered && !outcome.Consumed:
		// U6's terminal INDETERMINATE state: the resume reached the pane and the claim was not consumed.
		// Not a refusal -- nothing was declined -- and not a success either.
		return "delivered_indeterminate"
	case outcome.Delivered:
		return "delivered_with_error"
	default:
		return "refused"
	}
}

func emitGoalSuperviseResumeReport(report goalSuperviseResumeReport, asJSON bool) error {
	if asJSON {
		payload, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(payload))
		return nil
	}
	fmt.Printf("decision=%s state=%s fresh=%t eligible=%t automatic=%t policy=%s@%d\n",
		report.Decision, report.State, report.Fresh, report.Eligible,
		report.AutomaticResumeAllowed, report.PolicyMode, report.PolicyRevision)
	fmt.Printf("supervisor=%s attempt=%s pause_generation=%s\n",
		report.Supervisor, report.AttemptID, report.PauseGeneration)
	if report.ClaimKey != "" {
		fmt.Printf("claim_key=%s\n", report.ClaimKey)
	}
	if report.Detail != "" {
		fmt.Printf("detail=%s\n", report.Detail)
	}
	if len(report.Reasons) > 0 {
		fmt.Printf("failed_eligibility_reasons=%s\n", strings.Join(report.Reasons, ","))
	}
	// The text surface prints the SAME three states as --json. A renderer that showed fewer would make
	// the honest report available only to machines, and the operator inspecting before enabling
	// safe_auto is exactly the human this output exists for.
	for _, g := range report.Gates {
		state := "pass"
		if !g.Passed {
			state = "FAIL"
		}
		fmt.Printf("gate %-40s %s\n", g.Name, state)
	}
	for _, n := range report.SkippedGates {
		fmt.Printf("gate %-40s not reached\n", n)
	}
	for _, n := range report.NotEvaluated {
		fmt.Printf("delivery-time gate NOT EVALUATED: %s\n", n)
	}
	if report.FailedGate != "" {
		fmt.Printf("failed_gate=%s\n", report.FailedGate)
	}
	// THE HUMAN SURFACE CARRIES THE SAME MATERIAL OUTCOME AS --json. It printed neither delivered/consumed
	// nor the directive/status flags, so the structured truth existed only for machines -- and the operator
	// deciding whether to intervene is the reader who needs it most.
	if report.Outcome != nil {
		o := report.Outcome
		fmt.Printf("delivered=%t delivery_outcome_unknown=%t consumed=%t delivered_directive_published=%t status_polled=%t\n",
			o.Delivered, o.DeliveryOutcomeUnknown, o.Consumed, o.DeliveredDirectivePublished, o.StatusPolled)
		// EXPLICIT ORDERED EMISSION, not a map range. My map version made CLI output order
		// NONDETERMINISTIC -- Go randomises map iteration -- so stable evidence and any future golden test
		// would have been flaky by construction. A convenience in the writing became instability in the
		// artifact, which is the same trade I made with the exit-code pipe.
		if o.ConsumeError != "" {
			fmt.Printf("consume_error=%s\n", o.ConsumeError)
		}
		if o.DeliveredDirectiveError != "" {
			fmt.Printf("delivered_directive_error=%s\n", o.DeliveredDirectiveError)
		}
		if o.StatusPollError != "" {
			fmt.Printf("status_poll_error=%s\n", o.StatusPollError)
		}
	}
	return nil
}

// launchRecordResumePayloadLoader is the PRODUCTION payload loader: read-only, reads the agent's
// launch record, and returns the exact native resume payload with its digest.
//
// It returns the digest computed from the bytes it just read, NOT a digest carried alongside them.
// That is deliberate and it is what makes the executor's two equalities independent: this loader can
// only ever produce a self-consistent pair, so the FIRST equality is a corruption check on this
// path, and the SECOND -- against assessment.Binding.Goal.CommandDigest, which PR4 captured
// separately -- is the one that catches drift or substitution. A loader that returned a digest from
// some other source would collapse both checks into one.
//
// Read-only by construction: launch.Read and a digest, nothing written, nothing signalled. The
// executor calls it twice (pre-reserve and again at U5's gate), so it must be side-effect-free or
// calling it twice would itself be a mutation.
func launchRecordResumePayloadLoader(agentDir string) supervisionResumePayloadLoader {
	return func(assessment GoalSupervisionAssessment) (string, string, error) {
		rec, err := launch.Read(agentDir)
		if err != nil {
			return "", "", fmt.Errorf("read launch record: %w", err)
		}
		// launch.Read returns launch.Record BY VALUE, so there is no nil record to test -- my
		// `rec == nil` was written for a pointer API I assumed instead of read. The fail-closed check
		// that matters survives: no goal binding means nothing was authorized.
		if rec.GoalBinding == nil {
			// No binding means nothing was authorized. Refusing here rather than returning empty
			// keeps "unauthorized" distinct from "authorized to send nothing".
			return "", "", fmt.Errorf("launch record carries no goal binding, so no native resume payload is authorized")
		}
		payload := rec.GoalBinding.Command
		return payload, digestGoalSupervisionString(payload), nil
	}
}
