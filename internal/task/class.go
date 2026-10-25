package task

import (
	"fmt"
	"strings"
	"time"
)

// The 3.0 task schema vocabulary (docs/task-store-design.md). 2b-2, 2b-4,
// 2b-5, 3-2 and 4-4 code against these names.

// Store states: five, two of them terminal.
const (
	StatePending    = "pending"     // queued: waits for its dependencies, a file lock, a route or a seat (see wait_reason)
	StateInProgress = "in_progress" // a seat holds the lease (claimed or not yet claimed)
	StateBlocked    = "blocked"     // stopped on a decision the lead (or, routed, the owner) owes; no lease
	StateCompleted  = "completed"   // terminal
	StateCancelled  = "cancelled"   // terminal
)

// Classes is the closed class list of plan 6.1, in table order.
var Classes = []string{"extract", "summarize", "script", "implement", "plan", "review"}

// Verifiers of plan 6.1. A verifier is mechanical when a program decides it.
const (
	VerifierSchema   = "schema"
	VerifierExitCode = "exit_code"
	VerifierTests    = "tests"
	VerifierNone     = "none"
)

// classVerifiers is each class's verifier, fixed by plan 6.1.
var classVerifiers = map[string]string{
	"extract": VerifierSchema, "summarize": VerifierNone, "script": VerifierExitCode,
	"implement": VerifierTests, "plan": VerifierNone, "review": VerifierNone,
}

// ClassVerifier is the class's verifier ("" for a class outside the list).
func ClassVerifier(class string) string { return classVerifiers[class] }

// MechanicalVerifier reports whether a program decides the verifier.
func MechanicalVerifier(v string) bool {
	return v == VerifierSchema || v == VerifierExitCode || v == VerifierTests
}

// Verdict results.
const (
	VerdictPass = "pass"
	VerdictFail = "fail"
)

// Block reasons of the completion gate (phase 5 re-pass, ruling 1): a class
// with a mechanical verifier completes only on the harness's recorded pass.
const (
	BlockVerdictMissing = "verdict_missing" // a done report with no harness verdict
	BlockVerdictFailed  = "verdict_failed"  // the harness's verdict was fail
	// BlockVerdictTreeMismatch refuses a verdict whose tree is not the
	// lease's snapshot (v3-5-verifier rule 2).
	BlockVerdictTreeMismatch = "verdict_tree_mismatch"
)

// Verdict is the harness's verdict on one attempt of a class with a
// mechanical verifier: the commit step's test run or the probe's predicate,
// never the seat's own report.
type Verdict struct {
	Verifier string    `json:"verifier"` // the class's verifier (tests, exit_code, schema)
	Result   string    `json:"result"`   // pass | fail
	Source   string    `json:"source"`   // what ran it: the commit step's test run, the probe
	Detail   string    `json:"detail,omitempty"`
	At       time.Time `json:"at"`
	// Tree is the tree the verifier ran on: the commit step's snapshot tree
	// id (the object store's digest of the tree), which must be the lease's
	// Snapshot (verdict_tree_mismatch otherwise); CommandDigest is the
	// sha256 of the command that ran (CommandDigest). Both are inside the
	// signed bytes.
	Tree          string `json:"tree,omitempty"`
	CommandDigest string `json:"command_digest,omitempty"`
	// Tag is the harness's seal (sealchain.Tag under the session seed) over
	// the verdict with its task, attempt and holder; completion verifies it,
	// so a verdict written into the store without the seed is
	// verdict_missing.
	Tag string `json:"tag,omitempty"`
}

// ValidClass reports whether c is in the closed class list.
func ValidClass(c string) bool {
	for _, k := range Classes {
		if c == k {
			return true
		}
	}
	return false
}

// Wait reasons of a pending task (wait_reason). The routing ones come from
// the router hook (2b-4); no_route is also the router's answer when no chain
// entry passed for any other reason.
const (
	WaitDependencies = "dependencies"  // a depends_on task is not completed
	WaitFileLock     = "file_lock"     // declared_files overlap an active task's
	WaitNoRoute      = "no_route"      // no pool: the router found no entry, or the lead must assign one
	WaitSeatStarting = "seat_starting" // a seat for the pool was requested and is not up yet
	WaitSeatCap      = "seat_cap"      // the pool has no idle seat and the grant's max_seats is reached
	WaitUsageLimit   = "usage_limit"   // a usage-limit event stopped new seats in this window

	// The routing reasons (2b-4, internal/routing): no chain entry passed.
	WaitPrivacyClass   = "privacy_class"   // only vendor entries passed the other checks, and a declared path or the grant is local-only
	WaitConcurrencyCap = "concurrency_cap" // every passing entry's concurrency pool is full
	WaitCostCap        = "cost_cap"        // the tier's cost_index cap or the grant's per-day budget stops the entry
	WaitReviewerFamily = "reviewer_family" // a review whose only passing entries share the implementer's family
	// WaitOutcomeLogUnwritable stops every dispatch while an outcome row or
	// a routing decision is not durable (2b-4 Codex pass 1, P1-4).
	WaitOutcomeLogUnwritable = "outcome_log_unwritable"
)

// Attempt outcomes (attempts[].outcome).
const (
	OutcomeCompleted     = "completed"      // the done report committed
	OutcomeUnclaimed     = "unclaimed"      // the lease expired before the seat claimed it (S6 N4)
	OutcomeExpired       = "expired"        // a claimed lease expired without renewal
	OutcomeFailed        = "failed"         // the seat reported failure
	OutcomeReleased      = "released"       // the lead or the seat released the lease
	OutcomeBlocked       = "blocked"        // the seat or the commit step blocked the task
	OutcomeCancelled     = "cancelled"      // the lead cancelled the task during the attempt
	OutcomeCommitRefused = "commit_refused" // the harness commit step refused the diff (2b-2)
)

// Block routes (block_route): who owes the decision.
const (
	RouteLead  = "lead"
	RouteOwner = "owner"
)

// Pool is the runtime x model x effort key (plan 4.5).
type Pool struct {
	Runtime string `json:"runtime"`
	Model   string `json:"model"`
	Effort  string `json:"effort"`
}

// Key is the pool's canonical "runtime:model:effort" string.
func (p Pool) Key() string { return p.Runtime + ":" + p.Model + ":" + p.Effort }

// ParsePool parses "runtime:model:effort". Model and effort may be empty
// ("pi:qwen3.6:27b:low" keeps colons inside the model: the first field is
// the runtime, the last the effort).
func ParsePool(s string) (Pool, error) {
	s = strings.TrimSpace(s)
	first, last := strings.Index(s, ":"), strings.LastIndex(s, ":")
	if first <= 0 || first == last {
		return Pool{}, fmt.Errorf("pool %q: want runtime:model:effort", s)
	}
	p := Pool{Runtime: s[:first], Model: s[first+1 : last], Effort: s[last+1:]}
	if p.Model == "" {
		return Pool{}, fmt.Errorf("pool %q: model is empty", s)
	}
	return p, nil
}
