package routing

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/scheduler"
	"github.com/omriariav/amq-squad/v3/internal/task"
)

// A task whose own history may have lost a row is held for the lead (lead
// ruling on the fix 3 design call; Codex pass 4, N1b): its remaining history
// may no longer forbid a tier the lost row did. Other tasks keep routing;
// only a torn or non-row tail stops routing for all.
//
// When a full read finds a break the router has not seen before (chain.go:
// a line whose tag fails, a gap, a reorder or duplicate, a row gone from the
// log), it holds every task the break may have cost a row: the task a lost
// or broken line names, and, for a break whose task is unknown (a gap, a
// line that does not decode), every task with an ended attempt the log does
// not hold. A held task's attempts are not logged again and the task is not
// routed: Route answers a block with reason history_tampered, routed to the
// lead, so the lead clears it with the task tools on the same path as any
// block (`amq-squad task reset`). The router sees the reset as the task
// pending again with an updated_at after its hold, records the clearance,
// and from then logs its attempts (from the kept decisions; manual where
// none) and routes it. Holds, clearances and the breaks already seen are
// signed records in the decision log, so a restart neither re-holds for a
// known break nor forgets a hold.

const (
	tamperBlock = "tamper_block"
	tamperClear = "tamper_clear"
	tamperSeen  = "tamper_seen" // a break that held no task: known, not re-raised
)

// tamperRecord is a signed hold, clearance or seen line in the decision log.
type tamperRecord struct {
	Kind string    `json:"kind"`
	Task string    `json:"task"`
	Rows []string  `json:"rows"` // the problem ids
	At   time.Time `json:"at"`
	Tag  string    `json:"tag,omitempty"`
}

func (t tamperRecord) sign(key []byte) tamperRecord {
	t.Tag = ""
	t.Tag = hmacTag(key, "amq-squad routing tamper record v1", t)
	return t
}

func (t tamperRecord) verify(key []byte) bool {
	switch t.Kind {
	case tamperBlock, tamperClear, tamperSeen:
	default:
		return false
	}
	return t.Tag != "" && t.sign(key).Tag == t.Tag && t.Task != ""
}

// loadTamperLine applies one decision-log line if it is a tamper record;
// it reports whether the line was one.
func (r *Router) loadTamperLine(line []byte) bool {
	var probe struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(line, &probe) != nil || probe.Kind == "" {
		return false
	}
	var rec tamperRecord
	if json.Unmarshal(line, &rec) != nil || !rec.verify(r.key) {
		r.decTampered = append(r.decTampered, "tamper record "+rec.Task)
		return true
	}
	r.applyTamper(rec)
	return true
}

func (r *Router) applyTamper(rec tamperRecord) {
	for _, id := range rec.Rows {
		r.knownProblems[id] = true
	}
	switch rec.Kind {
	case tamperBlock:
		r.tamperBlockedAt[rec.Task] = rec.At
	case tamperClear:
		delete(r.tamperBlockedAt, rec.Task)
	}
}

func (r *Router) writeTamper(rec tamperRecord) error {
	if r.o.Root != "" {
		if err := AppendJSONL(DecisionPath(r.o.Root), rec.sign(r.key)); err != nil {
			return fmt.Errorf("decision log: %w", err)
		}
	}
	r.applyTamper(rec)
	return nil
}

func taskOfKey(k string) string {
	if i := strings.LastIndex(k, "/"); i > 0 {
		return k[:i]
	}
	return k
}

// holdForProblems holds the tasks a break not seen before may have cost a
// row; the break becomes known whether or not it held a task.
func (r *Router) holdForProblems(list []task.Task, now time.Time) error {
	var fresh []string
	held := map[string]bool{}
	unknown := false
	for _, p := range r.problems {
		if r.knownProblems[p.ID] {
			continue
		}
		fresh = append(fresh, p.ID)
		if p.Task != "" {
			held[p.Task] = true
		}
		if !strings.HasPrefix(p.ID, "lost:") {
			unknown = true // the lost line's task is not known for sure
		}
	}
	if len(fresh) == 0 {
		return nil
	}
	if unknown {
		for _, t := range list {
			for _, a := range t.Attempts {
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
		if _, already := r.tamperBlockedAt[id]; already {
			continue
		}
		if err := r.writeTamper(tamperRecord{Kind: tamperBlock, Task: id, Rows: fresh, At: now}); err != nil {
			return err
		}
	}
	// Known from now on, also when it held nobody.
	return r.writeTamper(tamperRecord{Kind: tamperSeen, Task: "-", Rows: fresh, At: now})
}

// held reports whether the task is held for history_tampered.
func (r *Router) held(id string) bool {
	_, ok := r.tamperBlockedAt[id]
	return ok
}

// holdTampered is the history_tampered hold for t; ok false lets t route.
func (r *Router) holdTampered(t task.Task, now time.Time) (scheduler.Route, bool) {
	at, blocked := r.tamperBlockedAt[t.ID]
	if !blocked {
		return scheduler.Route{}, false
	}
	if t.State == task.StatePending && t.UpdatedAt.After(at) {
		// The lead reset it after the hold: cleared. Its attempts are
		// logged at the next flush and it routes.
		if err := r.writeTamper(tamperRecord{Kind: tamperClear, Task: t.ID, At: now}); err != nil {
			return scheduler.Route{Wait: task.WaitOutcomeLogUnwritable, Detail: "routing: " + err.Error()}, true
		}
		return scheduler.Route{}, false
	}
	return scheduler.Route{Block: HistoryTampered, Detail: fmt.Sprintf(
		"routing: the outcome history of %s may have lost a row (%s); its remaining history may not forbid a tier the lost row did; review it and `amq-squad task reset %s`",
		t.ID, HistoryTampered, t.ID)}, true
}
