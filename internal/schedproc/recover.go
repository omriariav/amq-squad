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
	// RefusedRecoverAlarmNotHeld: a decision by alarm names an alarm that is
	// not the one the seat is held by now (another alarm, an earlier one
	// already released, or a seat no alarm holds).
	RefusedRecoverAlarmNotHeld = "recover_alarm_not_held"
)

// RecoverRequest is the lead's recovery: the seat, the quarantined head
// seq the slot is named by ("150", or "unknown"), the slot number k of
// commits.<handle>.seq<N>[.k] (0 and 1 are the first), the entries to
// carry as "task:commit", and why. A decision by alarm names the seat and
// the hold's alarm id instead, and no seq, slot or carried entry.
type RecoverRequest struct {
	Kind   string   `json:"kind"`
	Seat   string   `json:"seat"`
	Seq    string   `json:"seq,omitempty"`
	Slot   int      `json:"slot,omitempty"`
	Carry  []string `json:"carry,omitempty"`
	Alarm  string   `json:"alarm,omitempty"`
	Reason string   `json:"reason,omitempty"`
	// guard is the apply step's check, run inside the ledger lock right
	// before the seal (seatcommit.RecoverOptions.Guard); never sent.
	guard func() error
}

// recoverAfterHeldCheck is a test seam between the socket's held-alarm
// check and the apply step (Codex on 254f2c2c: a request that passed the
// check can wait there while its hold is released and another recovered).
var recoverAfterHeldCheck = func() {}

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

// NewRecovery is the scheduler process's chain writer as scheduler run
// builds it: the session root and the launch's grant, Git, env and clock
// left to RecoverLedger's defaults.
func NewRecovery(root string, target seatcommit.Target) Recovery {
	return Recovery{Root: root, Target: target}
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
		Target: r.Target, Git: r.Git, Lead: lead, Reason: req.Reason, Now: now, Alarm: req.Alarm, Guard: req.guard})
}

// recoverDecision is the decision socket's apply step for a proved
// recovery: the request's shape, the chain write, the decision line; the
// scheduler releases the seat at its next tick from the ledger. A decision
// by alarm is refused unless the seat is held by that alarm now
// (Scheduler.HeldAlarm), so an earlier alarm's id never reaches the ledger
// for a newer hold; the ledger half refuses it unless the ledger verifies.
func recoverDecision(s *scheduler.Scheduler, root string, apply RecoverApply, now func() time.Time) func(lead string, req RecoverRequest) DecideReply {
	return func(lead string, req RecoverRequest) DecideReply {
		if req.Alarm != "" {
			if !recoverSeatRe.MatchString(req.Seat) || !seatcommit.AlarmIDRe.MatchString(req.Alarm) || req.Seq != "" || req.Slot != 0 || len(req.Carry) > 0 {
				return DecideReply{Code: RefusedRecoverMalformed, Detail: "a decision by alarm names a seat handle and an alarm id (eight hex characters), and no seq, slot or carried entry"}
			}
			held, ok := "", false
			if s != nil {
				held, ok = s.HeldAlarm(req.Seat)
			}
			if !ok {
				return DecideReply{Code: RefusedRecoverAlarmNotHeld, Detail: fmt.Sprintf("seat %s is not held by a tamper alarm now; alarm %s is not the seat's", req.Seat, req.Alarm)}
			}
			if held != req.Alarm {
				return DecideReply{Code: RefusedRecoverAlarmNotHeld, Detail: fmt.Sprintf("seat %s is held by alarm %s, not %s; the decision names the alarm its stop line shows", req.Seat, held, req.Alarm)}
			}
			recoverAfterHeldCheck()
			// the same check again, inside the ledger lock, right before the
			// seal: a request that waited here never seals over a newer
			// hold's recovery (Codex on 254f2c2c)
			seat, alarm := req.Seat, req.Alarm
			req.guard = func() error {
				if held, ok := s.HeldAlarm(seat); !ok || held != alarm {
					return &seatcommit.RecoverRefusal{Code: RefusedRecoverAlarmNotHeld, Detail: fmt.Sprintf("seat %s is no longer held by alarm %s (its hold was released, or another alarm holds the seat); nothing was written", seat, alarm)}
				}
				return nil
			}
		} else if !recoverSeatRe.MatchString(req.Seat) || !recoverSeqRe.MatchString(req.Seq) || req.Slot < 0 {
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
		reason := fmt.Sprintf("the lead %s %s the commits ledger of seat %s from quarantine seq %d (%s): the chain continues at seq %d (sequence chosen from %s); carried entries: %s; base %s; reason: %s",
			lead, verb, req.Seat, res.OldSeq, res.Slot, res.NewSeq, firstNonEmptyString(res.SeqFrom, "the verified head"), firstOrNone(carried), res.Base, firstNonEmptyString(req.Reason, "none given"))
		if req.Alarm != "" {
			verb = "released"
			if res.AlreadyRecovered {
				verb = "had released (the decision sent again; its hold release file has the line)"
			}
			reason = fmt.Sprintf("the lead %s %s alarm %s of seat %s by decision: its commits ledger verified when the decision came; the decision is a sealed line of %s, the ledger unchanged, nothing carried; reason: %s",
				lead, verb, req.Alarm, req.Seat, seatcommit.AlarmsPath(root, req.Seat), firstNonEmptyString(req.Reason, "none given"))
		}
		at := now().UTC()
		if err := AppendDecision(root, at, scheduler.Decision{Kind: DecisionLedgerRecovered, Seat: req.Seat, Reason: reason}); err != nil {
			return DecideReply{Code: RefusedRecoverUnrecorded, Detail: "the chain is written and the scheduler releases the seat from it at its next tick, but the decision line could not be appended (send the decision again to record it): " + err.Error()}
		}
		data, _ := json.Marshal(res)
		detail := fmt.Sprintf("seat %s recovered from quarantine seq %d: the chain continues at seq %d; %d entries carried; the scheduler releases the seat at its next tick", req.Seat, res.OldSeq, res.NewSeq, len(res.Carried))
		if res.AlreadyRecovered {
			detail = fmt.Sprintf("seat %s was already recovered from quarantine seq %d (the chain is at seq %d or past it); the decision is recorded now", req.Seat, res.OldSeq, res.NewSeq)
		}
		if req.Alarm != "" {
			detail = fmt.Sprintf("seat %s: alarm %s released by decision, a sealed line of its hold release file; the scheduler releases the seat at its next tick", req.Seat, req.Alarm)
			if res.AlreadyRecovered {
				detail = fmt.Sprintf("seat %s: alarm %s was already released by decision (its hold release file has the line); the decision is recorded now", req.Seat, req.Alarm)
			}
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
