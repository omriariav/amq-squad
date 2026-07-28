package cli

import (
	"fmt"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/tmuxpane"
)

// #498 U5: THE DELIVERY-TIME GATES.
//
// These are the checks that CANNOT be satisfied before the ledger work, and the reason is not
// fastidiousness. Reserve, bind and the cross-kind rescan all perform filesystem I/O, so real time
// passes and the real world moves between "this assessment was validated" and "these bytes are typed
// into a pane". A pre-mutation gate proves a fact about the past; a delivery-time gate proves it is
// still true at the only moment that matters.
//
// WHY THESE ARE INJECTED PARAMETERS AND NOT PACKAGE GLOBALS. The post-success seams in this package
// (publishDeliveredDirective, pollSupervisionStatusOnce) are package-level vars, and that is
// defensible for them: they perform bookkeeping and observability AFTER an irreversible action, their
// failure is reported rather than authorizing anything, and a nil one simply skips a non-decision.
// These readers are the opposite kind of thing. They AUTHORIZE an irreversible delivery, and a
// mutable package global that authorizes something can be silently replaced or nil'd by any code in
// the package -- turning "the gate did not refuse" into "the gate was not there". An injected
// parameter cannot be bypassed without changing every caller, which is the property an authorization
// seam needs and an observability seam does not.
//
// A NIL READER IS A REFUSAL, NEVER A SKIP, for the same reason: the absence of evidence about a live
// pane is not evidence of a live pane.

// supervisionGenerationReader re-reads the launch generation. It returns the digest and modtime of
// the launch record as it exists NOW, so drift against the bound generation can be detected.
type supervisionGenerationReader func(GoalSupervisionAssessment) (digest string, modTime int64, err error)

// supervisionPaneReader observes the exact recorded pane. It is strictly read-only: it must never
// create, resurrect, or repair a pane, because a gate that fixes what it inspects cannot refuse.
type supervisionPaneReader func(GoalSupervisionAssessment) (supervisionPaneObservation, error)

// supervisionPaneObservation is the pane facts U5 requires, kept as an explicit struct rather than a
// bool so that UNKNOWN is representable. A boolean cannot distinguish "the pane is not idle" from
// "whether the pane is idle could not be determined", and those must both refuse for DIFFERENT
// stated reasons -- collapsing them would report a busy pane when the truth is a failed probe.
type supervisionPaneObservation struct {
	// PaneID is what was actually inspected, so a refusal can name the pane rather than asserting
	// something about a pane the reader may not have looked at.
	PaneID string
	// Managed reports whether this pane is one the supervisor manages. Delivering into an unmanaged
	// pane types bytes into something a human owns.
	Managed bool
	// Found is true ONLY for an affirmative exact-id hit. Gone, Unavailable and Malformed are all
	// false, and State carries which one for the refusal detail.
	Found bool
	State string
	// PID is the pane's foreground process id. Zero means no live process, which is the case a
	// pane-exists check alone would pass: a pane can survive its process.
	PID int
	// IdleKnown separates "not idle" from "idle unknown". See the struct comment.
	IdleKnown bool
	Idle      bool
}

// evaluateSupervisionDeliveryTimeGates runs the three owed U5 gates immediately before pane input and
// returns a refusal, or nil to proceed. The payload-drift gate is NOT here: it lives inline in the
// executor because it must consume the AuthorizedDigest baseline established before reserve, and
// moving it would separate that comparison from the baseline it depends on.
//
// ORDER, AND MY FIRST ORDER WAS WRONG. I put the wall clock FIRST, reasoning cheapest-and-most-certain
// first: an expired assessment could then be refused without touching the filesystem or tmux. That is
// a real efficiency argument and it lost to a correctness one, which dev-2 named. The generation read
// and the pane inspection both perform real I/O -- the pane read includes bounded tmux retries -- so a
// freshness check placed BEFORE them is not the "FreshUntil re-checked immediately before pane input"
// the contract requires. An assessment fresh at entry could expire during those reads and be
// delivered anyway, with no clock left to consult.
//
// So the order is now:
//  1. generation -- if the launch record moved, the assessment's pane identity describes a generation
//     that no longer exists, so inspecting that pane would be asking about the wrong program.
//  2. pane -- the most expensive and most failure-prone read.
//  3. wall clock, LAST, sampled after both reads and immediately before the caller types anything.
//
// ONE freshness check, not two. An early cheap check plus a late authoritative one would be gate
// redundancy: deleting the early one would change nothing observable, so no mutation could kill it,
// and a gate no mutation can kill is decoration.
//
// The clock is a supervisionResumeClock rather than a sampled time.Time precisely so the last sample
// happens HERE. A caller-sampled instant cannot express "after the slow reads" no matter where the
// comparison sits.
func evaluateSupervisionDeliveryTimeGates(
	assessment GoalSupervisionAssessment,
	now supervisionResumeClock,
	readGeneration supervisionGenerationReader,
	readPane supervisionPaneReader,
) *supervisionResumeRefusal {
	// GATE 2: LAUNCH GENERATION DRIFT, digest AND modtime.
	//
	// BOTH are compared because each catches what the other misses. A digest change means the content
	// differs. A modtime change with an identical digest means the record was REWRITTEN with the same
	// bytes -- which is a relaunch that happens to produce identical content, and the pane behind it
	// may be entirely different. Comparing only the digest would silently accept that.
	if readGeneration == nil {
		return &supervisionResumeRefusal{
			Clause:   "the same launch generation ... remains current",
			Detail:   "no launch-generation reader was supplied, so generation drift cannot be checked",
			Recovery: "this is a wiring fault: the delivery-time gates must be injected, because a missing check is not a passing check",
		}
	}
	digest, modTime, err := readGeneration(assessment)
	if err != nil {
		// UNKNOWN IS A REFUSAL. A generation that cannot be read is not a generation that has not
		// changed, and the whole point of this gate is that the record may have moved.
		return &supervisionResumeRefusal{
			Clause:   "the same launch generation ... remains current",
			Detail:   "cannot re-read the launch generation immediately before delivery: " + err.Error(),
			Recovery: "the reservation is left unconsumed and INDETERMINATE; inspect the launch record before any manual resume",
		}
	}
	if digest != assessment.Binding.LaunchRecordDigest || modTime != assessment.Binding.LaunchRecordModTime {
		return &supervisionResumeRefusal{
			Clause: "the same launch generation ... remains current",
			Detail: fmt.Sprintf(
				"launch generation changed between reservation and delivery: bound digest %q modtime %d, now digest %q modtime %d",
				assessment.Binding.LaunchRecordDigest, assessment.Binding.LaunchRecordModTime, digest, modTime),
			Recovery: "the reservation is left unconsumed and INDETERMINATE; drift is a refusal, never a re-authorization of whatever the record now says",
		}
	}

	// GATE 3: LIVE PANE REVALIDATION -- exact, managed, present, idle, nonzero PID.
	//
	// Every conjunct is load-bearing and each has its own failure mode:
	//   - MANAGED: an unmanaged pane belongs to a human, and typing a resume into it is input
	//     injection into somebody's terminal.
	//   - FOUND by exact id: a pane matched by anything looser could be a different pane that
	//     happens to fit, and the assessment's authority is over one exact pane.
	//   - NONZERO PID: a pane can OUTLIVE its process. Existence proves a container, not a listener,
	//     and bytes typed into a pane with no process are silently discarded -- a delivery that
	//     reports success and did nothing.
	//   - IDLE, and idle KNOWN: delivering into a busy pane interleaves with whatever is running.
	//     Unknown busyness refuses separately, because a failed probe is not an idle pane.
	if readPane == nil {
		return &supervisionResumeRefusal{
			Clause:   "the same ... pane identity ... remains current",
			Detail:   "no pane reader was supplied, so the live pane cannot be revalidated",
			Recovery: "this is a wiring fault: a missing pane check is not a passing pane check",
		}
	}
	pane, err := readPane(assessment)
	if err != nil {
		return &supervisionResumeRefusal{
			Clause:   "the same ... pane identity ... remains current",
			Detail:   "cannot inspect the recorded pane immediately before delivery: " + err.Error(),
			Recovery: "the reservation is left unconsumed and INDETERMINATE; inspect the pane before any manual resume",
		}
	}
	// The reader must have looked at the pane the ASSESSMENT names. A reader that inspected a
	// different pane would return facts about the wrong target, and those facts passing the gate is
	// worse than the gate failing.
	if want := assessment.Binding.Pane.PaneID; pane.PaneID != want {
		return &supervisionResumeRefusal{
			Clause:   "the same ... pane identity ... remains current",
			Detail:   fmt.Sprintf("pane observation is for %q but the assessment binds pane %q", pane.PaneID, want),
			Recovery: "this is a wiring fault: the observation does not describe the bound pane, so it proves nothing about it",
		}
	}
	if !pane.Managed {
		return &supervisionResumeRefusal{
			Clause:   "the same ... pane identity ... remains current",
			Detail:   fmt.Sprintf("pane %s is not managed at delivery time", pane.PaneID),
			Recovery: "an unmanaged pane belongs to a human; the reservation is left unconsumed and no input is sent",
		}
	}
	if !pane.Found {
		return &supervisionResumeRefusal{
			Clause: "the same ... pane identity ... remains current",
			Detail: fmt.Sprintf("pane %s is not affirmatively present at delivery time (state %q)",
				pane.PaneID, pane.State),
			Recovery: "the reservation is left unconsumed and INDETERMINATE; a pane that is gone or unreadable cannot receive an audited resume",
		}
	}
	if pane.PID <= 0 {
		return &supervisionResumeRefusal{
			Clause:   "the same ... pane identity ... remains current",
			Detail:   fmt.Sprintf("pane %s has no live foreground process (pid %d)", pane.PaneID, pane.PID),
			Recovery: "a pane can outlive its process; bytes sent to a paneless process are discarded, so this refuses rather than reporting a delivery that did nothing",
		}
	}
	if !pane.IdleKnown {
		return &supervisionResumeRefusal{
			Clause:   "the same ... pane identity ... remains current",
			Detail:   fmt.Sprintf("pane %s busy state is UNKNOWN at delivery time", pane.PaneID),
			Recovery: "unknown busyness is not idleness; the reservation is left unconsumed and must be inspected",
		}
	}
	if !pane.Idle {
		return &supervisionResumeRefusal{
			Clause:   "the same ... pane identity ... remains current",
			Detail:   fmt.Sprintf("pane %s is BUSY at delivery time", pane.PaneID),
			Recovery: "delivering into a busy pane interleaves with whatever is running; re-run the sweep once the pane is idle",
		}
	}

	// GATE 3 (LAST): DELIVERY-TIME WALL CLOCK, sampled after every read above.
	//
	// U4 requires a SECOND FreshUntil check, and this is the last point before the caller types. The
	// pre-mutation clock gate ran before reserve/bind/rescan; this one runs after those AND after the
	// generation and pane reads, which is the only placement that makes "immediately before pane
	// input" true.
	//
	// THE BOUNDARY IS !Before, NOT After, and matching it matters more than which one is right. The
	// pre-mutation gate at goal_supervision_gates.go:240 uses !now().Before(FreshUntil), so exact
	// equality is EXPIRED there. My After() treated the same instant as fresh, which means the two
	// gates disagreed about one field at one value -- two deciders for one definition, the defect this
	// milestone keeps producing. Consistency here is not stylistic: a dry-run predicting refusal while
	// the delivering path proceeds is exactly the divergence the shared evaluator exists to prevent.
	if sampled := now(); !assessment.FreshUntil.IsZero() && !sampled.Before(assessment.FreshUntil) {
		return &supervisionResumeRefusal{
			Clause: "the assessment remains fresh at the moment of delivery",
			Detail: fmt.Sprintf(
				"assessment expired before pane input: FreshUntil %s, now %s",
				assessment.FreshUntil.UTC().Format(time.RFC3339Nano),
				sampled.UTC().Format(time.RFC3339Nano)),
			Recovery: "the reservation is left unconsumed and INDETERMINATE; re-run the sweep for a fresh assessment rather than delivering on an expired one",
		}
	}
	return nil
}

// newSupervisionGenerationReader is the PRODUCTION generation reader. It consumes the SAME canonical
// snapshot function the assessment used (readGoalSupervisionLaunchSnapshot), so the two sides of the
// comparison are produced by one derivation owner. If this read used a different mechanism, a
// mismatch could mean "the generation changed" OR "the two readers disagree", and a gate that cannot
// tell those apart is a coin flip wearing a refusal message.
func newSupervisionGenerationReader() supervisionGenerationReader {
	return func(assessment GoalSupervisionAssessment) (string, int64, error) {
		agentDir, err := agentDirForSupervisionResume(assessment)
		if err != nil {
			return "", 0, err
		}
		_, digest, modTime, err := readGoalSupervisionLaunchSnapshot(agentDir)
		if err != nil {
			return "", 0, err
		}
		return digest, modTime, nil
	}
}

// newSupervisionPaneReader is the PRODUCTION pane reader, built on the exact-id inspection rather
// than the global pane scan. InspectPaneExactByID reports Gone only from affirmative exact-id
// evidence and stays Unavailable for generic failures, which is the distinction this gate needs: an
// unreadable pane and an absent pane are both refusals here, but they are not the same refusal, and
// the state is carried through so the message can say which.
//
// Managed is taken from the ASSESSMENT, not re-derived: whether a pane is supervisor-managed is a
// fact about how it was launched, which the binding already owns. Re-deciding it here would make
// this a second owner of that classification.
func newSupervisionPaneReader() supervisionPaneReader {
	return func(assessment GoalSupervisionAssessment) (supervisionPaneObservation, error) {
		paneID := assessment.Binding.Pane.PaneID
		observation := supervisionPaneObservation{
			PaneID:  paneID,
			Managed: assessment.Binding.Pane.Managed,
		}
		inspection := tmuxpane.InspectPaneExactByID(paneID)
		observation.State = string(inspection.State)
		if inspection.State == tmuxpane.PaneInspectionFound {
			observation.Found = true
			observation.PID = inspection.Pane.PID
		}
		// BUSY IS PROBED LIVE, not read from the assessment's BusyKnown/Busy. Those were observed
		// during the sweep, and busyness is the single most volatile fact here -- a pane idle at
		// assessment time is routinely busy by delivery time, which is precisely the race this gate
		// exists to catch. Reusing the stale value would make the gate agree with itself.
		if observation.Found {
			busy, err := tmuxpane.PaneBusy(paneID)
			if err == nil {
				observation.IdleKnown = true
				observation.Idle = !busy
			}
			// A probe error leaves IdleKnown false, which the gate refuses on with its own stated
			// reason. The error is deliberately NOT returned: returning it would collapse "cannot
			// determine busyness" into "cannot inspect the pane at all", and those refusals send an
			// operator to different places.
		}
		return observation, nil
	}
}
