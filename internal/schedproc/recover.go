package schedproc

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/seatcommit"
)

// The lead's recovery of a quarantined commits ledger (3.2.0-0c-B rule 3)
// is a decision over the scheduler's socket, proved under R4 like a
// widening: the CLI (scheduler recover) sends it, the scheduler writes the
// new chain (seatcommit.RecoverLedger) with the session key and records one
// ledger_recovered line in the decision log naming the seat, the old seq,
// the quarantine path and the carried entries; the scheduler's next tick
// reads the recovery from the sealed ledger (CleanSeats.LedgerRecovery)
// and releases the seat. The completion is retryable (Codex's P2 on #904):
// the ledger is the source of truth, a decision line that could not be
// appended is answered recover_unrecorded with the chain written, and the
// verb sent again on a recovered ledger records the line and replies OK
// (RecoverResult.AlreadyRecovered) instead of refusing. The CLI never writes the
// chain: a seat that can run the CLI could otherwise recover its own
// tampered ledger. A clear (scheduler clear) is not the recovery: a clear
// naming ledger_tampered is refused (it is no warning), and no clear makes
// a held seat grantable.

// RecoverRequestKind marks the lead's recovery.
const RecoverRequestKind = "amq-squad.ledger-recover.v1"

// DecisionLedgerRecovered is the recovery's decision kind.
const DecisionLedgerRecovered = "ledger_recovered"

// Refusals of a recovery before its apply step.
const (
	RefusedRecoverMalformed   = "recover_malformed"
	RefusedRecoverUnsupported = "recover_unsupported"
	RefusedRecoverFailed      = "recover_failed"
	// RefusedRecoverUnrecorded: the chain is written, the decision line is
	// not; the seat is released from the ledger, the verb sent again
	// records the line.
	RefusedRecoverUnrecorded = "recover_unrecorded"
)

// RecoverRequest is the lead's recovery: the seat, the quarantined head
// seq the slot is named by ("150", or "unknown"), the slot number k of
// commits.<handle>.seq<N>[.k] (0 and 1 are the first), the entries to
// carry as "task:commit", and why.
type RecoverRequest struct {
	Kind   string   `json:"kind"`
	Seat   string   `json:"seat"`
	Seq    string   `json:"seq"`
	Slot   int      `json:"slot,omitempty"`
	Carry  []string `json:"carry,omitempty"`
	Reason string   `json:"reason,omitempty"`
}

var (
	recoverSeatRe  = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	recoverSeqRe   = regexp.MustCompile(`^(?:[1-9][0-9]*|unknown)$`)
	recoverCarryRe = regexp.MustCompile(`^([A-Za-z0-9][A-Za-z0-9._-]{0,63}):([0-9a-f]{40})$`)
)

// ParseCarry reads the carried entries of a request ("task:commit" each).
func ParseCarry(carry []string) ([]seatcommit.CarryEntry, error) {
	var out []seatcommit.CarryEntry
	for _, c := range carry {
		m := recoverCarryRe.FindStringSubmatch(strings.TrimSpace(c))
		if m == nil {
			return nil, fmt.Errorf("a carried entry is TASK:COMMIT with the full commit id, not %q", c)
		}
		out = append(out, seatcommit.CarryEntry{Task: m[1], Commit: m[2]})
	}
	return out, nil
}

// SendRecover is the lead's side: one recovery over the scheduler's socket.
func SendRecover(root, path string, req RecoverRequest) (DecideReply, error) {
	req.Kind = RecoverRequestKind
	return sendDecision(root, path, req)
}

// RecoverApply writes the recovery's chain (Recovery.Apply in the verb).
type RecoverApply func(lead string, req RecoverRequest) (seatcommit.RecoverResult, error)

// Recovery is the scheduler process's chain writer: the session root, the
// owner's grant as the launch records it (the carried entries are checked
// against its push URL) and the git to run.
type Recovery struct {
	Root   string
	Target seatcommit.Target
	Git    string
	Now    func() time.Time
}

// Apply is seatcommit.RecoverLedger for a proved request.
func (r Recovery) Apply(lead string, req RecoverRequest) (seatcommit.RecoverResult, error) {
	carry, err := ParseCarry(req.Carry)
	if err != nil {
		return seatcommit.RecoverResult{}, &seatcommit.RecoverRefusal{Code: seatcommit.RecoverEntryUnknown, Detail: err.Error()}
	}
	now := time.Now()
	if r.Now != nil {
		now = r.Now()
	}
	return seatcommit.RecoverLedger(seatcommit.RecoverOptions{Root: r.Root, Handle: req.Seat, Seq: req.Seq, Slot: req.Slot, Carry: carry,
		Target: r.Target, Git: r.Git, Lead: lead, Reason: req.Reason, Now: now})
}

// recoverDecision is the decision socket's apply step for a proved
// recovery: the request's shape, the chain write, the decision line; the
// scheduler releases the seat at its next tick from the ledger.
func recoverDecision(_ *scheduler.Scheduler, root string, apply RecoverApply, now func() time.Time) func(lead string, req RecoverRequest) DecideReply {
	return func(lead string, req RecoverRequest) DecideReply {
		if !recoverSeatRe.MatchString(req.Seat) || !recoverSeqRe.MatchString(req.Seq) || req.Slot < 0 {
			return DecideReply{Code: RefusedRecoverMalformed, Detail: "a recovery names a seat handle, the quarantined head seq (or unknown) and a slot number"}
		}
		if _, err := ParseCarry(req.Carry); err != nil {
			return DecideReply{Code: RefusedRecoverMalformed, Detail: err.Error()}
		}
		res, err := apply(lead, req)
		if err != nil {
			code := RefusedRecoverFailed
			var rr *seatcommit.RecoverRefusal
			if errors.As(err, &rr) {
				code = rr.Code
				return DecideReply{Code: code, Detail: rr.Detail}
			}
			return DecideReply{Code: code, Detail: err.Error()}
		}
		carried := make([]string, 0, len(res.Carried))
		for _, c := range res.Carried {
			carried = append(carried, c.Task+":"+c.Commit)
		}
		verb := "recovered"
		if res.AlreadyRecovered {
			verb = "had recovered (the decision sent again; the sealed ledger records it)"
		}
		if res.Verified {
			verb = "recorded the release of (its ledger verified when the decision came; the record is sealed into the next link, nothing restarted)"
		}
		reason := fmt.Sprintf("the lead %s %s the commits ledger of seat %s from quarantine seq %d (%s): the chain continues at seq %d after the quarantined head's link; carried entries: %s; base %s; reason: %s",
			lead, verb, req.Seat, res.OldSeq, res.Slot, res.NewSeq, firstOrNone(carried), res.Base, firstNonEmptyString(req.Reason, "none given"))
		at := now().UTC()
		if err := AppendDecision(root, at, scheduler.Decision{Kind: DecisionLedgerRecovered, Seat: req.Seat, Reason: reason}); err != nil {
			return DecideReply{Code: RefusedRecoverUnrecorded, Detail: "the chain is written and the scheduler releases the seat from it at its next tick, but the decision line could not be appended (send the decision again to record it): " + err.Error()}
		}
		data, _ := json.Marshal(res)
		detail := fmt.Sprintf("seat %s recovered from quarantine seq %d: the chain continues at seq %d; %d entries carried; the scheduler releases the seat at its next tick", req.Seat, res.OldSeq, res.NewSeq, len(res.Carried))
		if res.AlreadyRecovered {
			detail = fmt.Sprintf("seat %s was already recovered from quarantine seq %d (the chain is at seq %d or past it); the decision is recorded now", req.Seat, res.OldSeq, res.NewSeq)
		}
		return DecideReply{OK: true, Detail: detail, Data: data}
	}
}

func firstOrNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

func firstNonEmptyString(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
