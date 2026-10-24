package routing

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// A task whose own history may have lost a row is held for the lead (lead
// ruling on the fix 3 design call; Codex passes 4 and 5): its remaining
// history may no longer forbid a tier the lost row did. Other tasks keep
// routing; only a torn or non-row tail stops routing for all.
//
// A break the router has not seen before (chain.go, head.go) holds every
// task it may have cost a row: the task a broken line names, the task of a
// row gone from the log under the running router, and, for a break whose
// task is not known (a gap, a truncation, an unreadable line), every task
// with an ended attempt the log does not hold (L2: a running attempt has no
// row due). The hold record names each break: id, what, and the task,
// attempt and seq where known.
//
// A held task is not logged again and not routed: Route answers a block with
// reason history_tampered for the lead, whose detail names the breaks. The
// lead clears it with the task tools on the same path as any block
// (`amq-squad task reset`). Clearance takes two steps (L1, the lead's
// ruling): the router records, signed, when it observes the task blocked
// with history_tampered; only a task pending with an updated_at after that
// observation is cleared. A task leased or running when the break was found
// ends its attempt, is blocked on its next route, and then only a reset
// clears it. The clearance record carries the ids it acknowledges.
// Every record is a signed, chained line of the decision log.

const (
	tamperBlock    = "tamper_block"    // a hold, naming its breaks
	tamperObserved = "tamper_observed" // the router saw the held task blocked with history_tampered
	tamperClear    = "tamper_clear"    // the lead's reset after the observation; the ids acknowledged
	tamperSeen     = "tamper_seen"     // a break that held no task: known, not re-raised
)

// tamperRecord is a signed, chained hold, observation, clearance or seen
// line in the decision log.
type tamperRecord struct {
	Kind     string    `json:"kind"`
	Task     string    `json:"task"`
	Problems []Problem `json:"problems,omitempty"`
	Acked    []string  `json:"acked,omitempty"`
	At       time.Time `json:"at"`
	Seq      int       `json:"seq"`
	Prev     string    `json:"prev"`
	Tag      string    `json:"tag,omitempty"`
}

func (t tamperRecord) sign(key []byte) tamperRecord {
	t.Tag = ""
	t.Tag = hmacTag(key, "amq-squad routing tamper record v2", t)
	return t
}

func (t tamperRecord) verify(key []byte) bool {
	switch t.Kind {
	case tamperBlock, tamperObserved, tamperClear, tamperSeen:
	default:
		return false
	}
	return t.Tag != "" && t.sign(key).Tag == t.Tag && t.Task != ""
}

func (r *Router) applyTamper(rec tamperRecord) {
	for _, p := range rec.Problems {
		r.knownProblems[p.ID] = true
	}
	switch rec.Kind {
	case tamperBlock:
		r.tamperBlockedAt[rec.Task] = rec.At
		r.holdProblems[rec.Task] = append(r.holdProblems[rec.Task], rec.Problems...)
		delete(r.observedAt, rec.Task)
	case tamperObserved:
		r.observedAt[rec.Task] = rec.At
	case tamperClear:
		for _, id := range rec.Acked {
			r.acked[id] = true
		}
		delete(r.tamperBlockedAt, rec.Task)
		delete(r.observedAt, rec.Task)
		delete(r.holdProblems, rec.Task)
	}
}

// writeTamper appends a signed, chained record to the decision log, moves
// the head, and applies it.
func (r *Router) writeTamper(rec tamperRecord) error {
	if r.o.Root != "" {
		rec.Seq, rec.Prev = r.decHead.Seq+1, r.decHead.Tag
		rec = rec.sign(r.key)
		if err := AppendJSONL(DecisionPath(r.o.Root), rec); err != nil {
			return fmt.Errorf("decision log: %w", err)
		}
		r.decHead = chainHead{Seq: rec.Seq, Tag: rec.Tag}
	}
	r.applyTamper(rec)
	return r.saveHead()
}

func taskOfKey(k string) string {
	if i := strings.LastIndex(k, "/"); i > 0 {
		return k[:i]
	}
	return k
}

// openProblems are the breaks not yet acknowledged, and every break a
// current hold still names: what the head record persists and Tampered and
// the report show.
func (r *Router) openProblems() []Problem {
	var out []Problem
	seen := map[string]bool{}
	add := func(p Problem) {
		if !seen[p.ID] {
			seen[p.ID] = true
			out = append(out, p)
		}
	}
	for _, ps := range r.holdProblems {
		for _, p := range ps {
			add(p)
		}
	}
	for _, p := range r.problems {
		if !r.acked[p.ID] {
			add(p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// holdForProblems holds the tasks breaks not seen before may have cost a
// row, records the observation of held tasks blocked with history_tampered,
// and returns the breaks it held for.
func (r *Router) holdForProblems(list []task.Task, now time.Time) ([]Problem, error) {
	var fresh []Problem
	held := map[string]bool{}
	unknown := false
	for _, p := range r.problems {
		if r.knownProblems[p.ID] || r.acked[p.ID] {
			continue
		}
		fresh = append(fresh, p)
		if p.Task != "" {
			held[p.Task] = true
		}
		if !strings.HasPrefix(p.ID, "lost:") {
			unknown = true // the lost line's task is not known for sure
		}
	}
	if len(fresh) > 0 {
		if unknown {
			for _, t := range list {
				for _, a := range t.Attempts { // ended attempts only (L2)
					if !r.logged[key(t.ID, a.N)] {
						held[t.ID] = true
					}
				}
			}
		}
		var ids []string
		for id := range held {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if err := r.writeTamper(tamperRecord{Kind: tamperBlock, Task: id, Problems: fresh, At: now}); err != nil {
				return nil, err
			}
		}
		if len(ids) == 0 {
			if err := r.writeTamper(tamperRecord{Kind: tamperSeen, Task: "-", Problems: fresh, At: now}); err != nil {
				return nil, err
			}
		}
	}
	// L1, step one: a held task seen blocked with history_tampered.
	for _, t := range list {
		if _, isHeld := r.tamperBlockedAt[t.ID]; !isHeld {
			continue
		}
		if _, seen := r.observedAt[t.ID]; seen || t.State != task.StateBlocked || t.BlockReason != HistoryTampered {
			continue
		}
		if err := r.writeTamper(tamperRecord{Kind: tamperObserved, Task: t.ID, At: now}); err != nil {
			return nil, err
		}
	}
	return fresh, nil
}

// held reports whether the task is held for history_tampered.
func (r *Router) held(id string) bool {
	_, ok := r.tamperBlockedAt[id]
	return ok
}

// holdTampered is the history_tampered hold for t; ok false lets t route.
func (r *Router) holdTampered(t task.Task, now time.Time) (scheduler.Route, bool) {
	if !r.held(t.ID) {
		return scheduler.Route{}, false
	}
	// L1, step two: pending again after the observed block: the lead reset it.
	if seen, ok := r.observedAt[t.ID]; ok && t.State == task.StatePending && t.UpdatedAt.After(seen) {
		var acked []string
		for _, p := range r.holdProblems[t.ID] {
			acked = append(acked, p.ID)
		}
		if err := r.writeTamper(tamperRecord{Kind: tamperClear, Task: t.ID, Acked: acked, At: now}); err != nil {
			return scheduler.Route{Wait: task.WaitOutcomeLogUnwritable, Detail: "routing: " + err.Error()}, true
		}
		return scheduler.Route{}, false
	}
	var named []string
	for _, p := range r.holdProblems[t.ID] {
		named = append(named, p.String())
	}
	return scheduler.Route{Block: HistoryTampered, Detail: fmt.Sprintf(
		"routing: the outcome history of %s may have lost a row: %s. Its remaining history may not forbid a tier the lost row did; review it and `amq-squad task reset %s`",
		t.ID, strings.Join(named, "; "), t.ID)}, true
}

// HoldsCreated is Flush's answer when the pass found new breaks and held
// tasks for them (Codex pass 5, (d)): rows of tasks that are not held were
// still appended.
type HoldsCreated struct{ Problems []Problem }

func (h *HoldsCreated) Error() string {
	var ids []string
	for _, p := range h.Problems {
		ids = append(ids, p.String())
	}
	return HistoryTampered + ": holds created for " + strings.Join(ids, "; ")
}
