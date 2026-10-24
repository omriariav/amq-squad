package routing

import (
	"bytes"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Codex pass 5 on f27ae20b: one test row per item.

func noTasks() ([]task.Task, error) { return nil, nil }

// blockThenReset plays the scheduler's block of a held task, the router's
// observation of it (a Flush), and then the lead's reset (L1's two steps).
func blockThenReset(h *fix3Router, r *Router, tk *task.Task) {
	tk.State, tk.BlockReason, tk.UpdatedAt = task.StateBlocked, HistoryTampered, h.clock
	h.cur = []task.Task{*tk}
	h.clock = h.clock.Add(time.Minute)
	r.Flush()
	tk.State, tk.BlockReason, tk.UpdatedAt = task.StatePending, "", h.clock.Add(time.Minute)
	h.clock = h.clock.Add(2 * time.Minute)
	h.cur = []task.Task{*tk}
}

func hasID(ids []string, prefix string) bool {
	return slices.ContainsFunc(ids, func(s string) bool { return strings.HasPrefix(s, prefix) })
}

// (a): truncating the outcome log to a valid prefix (zero rows included)
// across a restart is caught against the head record; the shortfall has no
// task, so the sweep holds the tasks whose rows are gone. Decisions are
// chained too and checked against the head the same way.
func TestFix5TruncationToAValidPrefixAcrossARestart(t *testing.T) {
	for _, keep := range []int{1, 0} {
		h, lines := threeRows(t)
		writeLines(t, h.root, lines[:keep])
		if keep == 0 {
			os.WriteFile(OutcomePath(h.root), nil, 0o600)
		}
		r := h.open("")
		ids := r.Tampered()
		for s := keep + 1; s <= 3; s++ {
			if !slices.Contains(ids, "truncated:"+string(rune('0'+s))) {
				t.Fatalf("keep %d: tampered %v", keep, ids)
			}
		}
		r.Route(newTask("t9", "plan"))
		if !r.held("t3") || (keep == 0 && !r.held("t1")) {
			t.Fatalf("keep %d: holds %v", keep, r.tamperBlockedAt)
		}
	}
	// Decisions carry seq and prev, and a truncated decision log is caught.
	h := newFix3(t, "")
	h.r.Route(newTask("t1", "plan"))
	h.r.Route(newTask("t2", "plan"))
	raw, _ := os.ReadFile(DecisionPath(h.root))
	if !bytes.Contains(raw, []byte(`"seq":2`)) || !bytes.Contains(raw, []byte(`"prev":"`)) {
		t.Fatalf("decision lines are not chained:\n%s", raw)
	}
	first := raw[:bytes.IndexByte(raw, '\n')+1]
	os.WriteFile(DecisionPath(h.root), first, 0o600)
	if ids := h.open("").Tampered(); !slices.Contains(ids, "truncated:decision:2") {
		t.Fatalf("decision truncation: %v", ids)
	}
}

// (a): a head record that is missing or whose tag fails, in a session that
// has routed, is routing_invalid.
func TestFix5HeadMissingOrBrokenIsInvalid(t *testing.T) {
	h, _ := threeRows(t)
	raw, _ := os.ReadFile(HeadPath(h.root))
	os.Remove(HeadPath(h.root))
	if _, err := New(Options{Table: Default(), Root: h.root, Tasks: noTasks}); !IsInvalid(err) || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing head: %v", err)
	}
	os.WriteFile(HeadPath(h.root), bytes.Replace(raw, []byte(`"outcome_seq":3`), []byte(`"outcome_seq":1`), 1), 0o600)
	if _, err := New(Options{Table: Default(), Root: h.root, Tasks: noTasks}); !IsInvalid(err) || !strings.Contains(err.Error(), HistoryTampered) {
		t.Fatalf("edited head: %v", err)
	}
}

// (e): a seed file replaced by another with the same bytes (another inode)
// is the seed refusal.
func TestFix5SeedReplacedIsRefused(t *testing.T) {
	h := newFix3(t, "")
	raw, _ := os.ReadFile(SeedPath(h.root))
	os.Remove(SeedPath(h.root))
	if err := os.WriteFile(SeedPath(h.root), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New(Options{Table: Default(), Root: h.root, Tasks: noTasks})
	if !IsInvalid(err) || !strings.Contains(err.Error(), "device and inode") {
		t.Fatalf("replaced seed: %v", err)
	}
}

// (b) and L1: the hold names each break (id, what, task, attempt, seq); the
// clearance takes the observed block, then the reset, and acknowledges the
// ids; a task running when the break is found ends its attempt and stays
// held until that reset.
func TestFix5HoldNamesBreaksAndClearsOnlyAfterTheObservedBlock(t *testing.T) {
	h := newFix3(t, "")
	a := ended("ta", "implement", "claude:sonnet:medium", task.OutcomeUnclaimed)
	h.cur = []task.Task{a}
	h.r.Route(newTask("tb", "plan"))
	// A's row is edited; A is running its second attempt meanwhile.
	raw, _ := os.ReadFile(OutcomePath(h.root))
	os.WriteFile(OutcomePath(h.root), bytes.Replace(raw, []byte(`"holder":"s1"`), []byte(`"holder":"s2"`), 1), 0o600)
	a.State, a.Attempt = task.StateInProgress, 2
	a.Lease = &task.Lease{Holder: "s1", GrantedAt: h.clock, ExpiresAt: h.clock.Add(time.Hour)}
	h.cur = []task.Task{a}
	h.clock = h.clock.Add(2 * FullCheckInterval)
	if err := h.r.Flush(); err == nil {
		t.Fatal("Flush found a break and reported nothing")
	}
	if !h.r.held("ta") {
		t.Fatal("A not held")
	}
	// The attempt ends: pending again with a later updated_at, no reset.
	a.State, a.Lease = task.StatePending, nil
	a.Attempts = append(a.Attempts, task.Attempt{N: 2, Holder: "s1", Pool: pl("claude:sonnet:medium"), GrantedAt: h.clock, EndedAt: h.clock.Add(time.Minute), Outcome: task.OutcomeUnclaimed})
	a.UpdatedAt = h.clock.Add(time.Minute)
	h.clock = h.clock.Add(2 * time.Minute)
	h.cur = []task.Task{a}
	route := h.r.Route(a)
	if route.Block != HistoryTampered || !strings.Contains(route.Detail, "outcome_line:") || !strings.Contains(route.Detail, "task ta") {
		t.Fatalf("A after its attempt ended (no reset): %+v", route)
	}
	// The scheduler blocks A; the router observes it; the lead resets it.
	a.State, a.BlockReason, a.UpdatedAt = task.StateBlocked, HistoryTampered, h.clock
	h.cur = []task.Task{a}
	h.clock = h.clock.Add(time.Minute)
	h.r.Flush()
	if _, ok := h.r.observedAt["ta"]; !ok {
		t.Fatal("the block was not observed")
	}
	a.State, a.BlockReason, a.UpdatedAt = task.StatePending, "", h.clock.Add(time.Minute)
	h.clock = h.clock.Add(2 * time.Minute)
	h.cur = []task.Task{a}
	if route := h.r.Route(a); route.Pool == nil {
		t.Fatalf("A after the reset: %+v", route)
	}
	dec, _ := os.ReadFile(DecisionPath(h.root))
	for _, want := range []string{`"kind":"tamper_block"`, `"problems":[{"id":"outcome_line:`, `"kind":"tamper_observed"`, `"kind":"tamper_clear"`, `"acked":["outcome_line:`} {
		if !bytes.Contains(dec, []byte(want)) {
			t.Fatalf("decision log lacks %s:\n%s", want, dec)
		}
	}
}

// (c): a torn tail is reported with its byte length (and stops routing); a
// malformed decision line is history_tampered on its task when readable.
func TestFix5TornTailAndBadDecisionAreReported(t *testing.T) {
	h, _ := threeRows(t)
	fh, _ := os.OpenFile(OutcomePath(h.root), os.O_APPEND|os.O_WRONLY, 0o600)
	fh.WriteString(`{"schema":1,"task":"tz"`)
	fh.Close()
	h.clock = h.clock.Add(2 * FullCheckInterval)
	route := h.r.Route(newTask("t9", "plan"))
	if route.Wait != task.WaitOutcomeLogUnwritable || !strings.Contains(route.Detail, "23 bytes") || !slices.Contains(h.r.Tampered(), "torn_tail:23") {
		t.Fatalf("torn tail: %+v %v", route, h.r.Tampered())
	}
	g := newFix3(t, "")
	g.r.Route(newTask("t1", "plan"))
	fh, _ = os.OpenFile(DecisionPath(g.root), os.O_APPEND|os.O_WRONLY, 0o600)
	fh.WriteString(`{"task":"t7","attempt":1,"policy":"table"}` + "\n")
	fh.Close()
	r := g.open("")
	ps := r.Problems()
	if !slices.ContainsFunc(ps, func(p Problem) bool { return strings.HasPrefix(p.ID, "decision_line:") && p.Task == "t7" }) {
		t.Fatalf("bad decision line: %+v", ps)
	}
	t7 := ended("t7", "plan", "claude:opus:high", task.OutcomeUnclaimed)
	g.cur = []task.Task{t7}
	if route := r.Route(t7); route.Block != HistoryTampered {
		t.Fatalf("t7 not held: %+v", route)
	}
}

// (d): Flush returns HoldsCreated when its pass found a break (rows of tasks
// that are not held are still appended), and a rooted router refuses to be
// built without Tasks.
func TestFix5FlushReportsHoldsAndRootedRouterNeedsTasks(t *testing.T) {
	h, lines := threeRows(t)
	writeLines(t, h.root, [][]byte{lines[0], lines[2]}) // t2's row deleted
	h.clock = h.clock.Add(2 * FullCheckInterval)
	t4 := ended("t4", "extract", "claude:haiku:low", task.OutcomeCompleted)
	t4.Attempts[0].EndedAt = h.clock.Add(time.Hour) // ends after the break: not swept... still unlogged now
	h.cur = []task.Task{ended("t1", "extract", "claude:haiku:low", task.OutcomeCompleted), ended("t2", "extract", "claude:haiku:low", task.OutcomeCompleted)}
	err := h.r.Flush()
	var hc *HoldsCreated
	if !errors.As(err, &hc) || len(hc.Problems) == 0 {
		t.Fatalf("Flush: %v", err)
	}
	h.cur = []task.Task{t4}
	if err := h.r.Flush(); err != nil {
		t.Fatalf("a later pass with no new break: %v", err)
	}
	if rows, _, _ := ReadRows(OutcomePath(h.root)); !slices.ContainsFunc(rows, func(r Row) bool { return r.Task == "t4" }) {
		t.Fatal("an unaffected row was not appended after the holds")
	}
	if _, err := New(Options{Table: Default(), Root: t.TempDir()}); err == nil || !strings.Contains(err.Error(), "Options.Tasks") {
		t.Fatalf("rooted router without Tasks: %v", err)
	}
}

// New 1: a gap is attributed to the missing sequence, never to the next
// intact row's task.
func TestFix5GapIsNotTheSuccessors(t *testing.T) {
	h, lines := threeRows(t)
	writeLines(t, h.root, [][]byte{lines[0], lines[2]})
	r := h.open("")
	for _, p := range r.Problems() {
		if strings.HasPrefix(p.ID, "gap:") && (p.Task != "" || p.Seq != 2) {
			t.Fatalf("gap attributed: %+v", p)
		}
	}
	h.cur = []task.Task{ended("t3", "extract", "claude:haiku:low", task.OutcomeCompleted)}
	r.Route(newTask("t9", "plan"))
	if r.held("t3") {
		t.Fatal("the successor's task was held for the gap")
	}
}

// New 2: a completed task's loss stays listed across reloads and restarts
// (the head record keeps it) until acknowledged.
func TestFix5ProblemsPersistUntilAcknowledged(t *testing.T) {
	h, lines := threeRows(t)
	lines[1] = bytes.Replace(lines[1], []byte(`"holder":"s1"`), []byte(`"holder":"s2"`), 1)
	writeLines(t, h.root, lines)
	h.clock = h.clock.Add(2 * FullCheckInterval)
	h.r.Route(newTask("t9", "plan"))
	if !hasID(h.r.Tampered(), "outcome_line:") {
		t.Fatalf("first read: %v", h.r.Tampered())
	}
	// The line is restored by hand; the break stays open.
	writeLines(t, h.root, mustLines(t, h, lines))
	h.clock = h.clock.Add(2 * FullCheckInterval)
	h.r.Route(newTask("t9", "plan"))
	if !hasID(h.r.Tampered(), "outcome_line:") {
		t.Fatalf("after another reload: %v", h.r.Tampered())
	}
	if !hasID(h.open("").Tampered(), "outcome_line:") {
		t.Fatal("lost across a restart")
	}
	// A break that held nobody (a gap, no task to sweep) is kept by the
	// problem list and the head record alone.
	g, glines := threeRows(t)
	writeLines(t, g.root, [][]byte{glines[0], glines[2]})
	g.cur = nil
	gr := g.open("") // a restart: no in-run memory names t2 (no lost: break)
	gr.Route(newTask("t9", "plan"))
	if len(gr.holdProblems) != 0 || !slices.Contains(gr.Tampered(), "gap:2") {
		t.Fatalf("the gap held someone or was not seen: %v %v", gr.holdProblems, gr.Tampered())
	}
	writeLines(t, g.root, glines) // restored by hand
	g.clock = g.clock.Add(2 * FullCheckInterval)
	gr.Route(newTask("t9", "plan"))
	if !slices.Contains(gr.Tampered(), "gap:2") {
		t.Fatalf("a held-nobody break after another reload: %v", gr.Tampered())
	}
	if !slices.Contains(g.open("").Tampered(), "gap:2") {
		t.Fatal("a held-nobody break lost across a restart")
	}
}

func mustLines(t *testing.T, h *fix3Router, lines [][]byte) [][]byte {
	t.Helper()
	out := make([][]byte, len(lines))
	copy(out, lines)
	out[1] = bytes.Replace(out[1], []byte(`"holder":"s2"`), []byte(`"holder":"s1"`), 1)
	return out
}

// L2: the unknown-task sweep holds tasks with an ended attempt the log
// lacks, not a task whose attempt is still running.
func TestFix5SweepSkipsRunningAttempts(t *testing.T) {
	h, lines := threeRows(t)
	writeLines(t, h.root, [][]byte{lines[0], lines[2]})
	h.clock = h.clock.Add(2 * FullCheckInterval)
	run := newTask("tr", "implement")
	run.State, run.Attempt = task.StateInProgress, 1
	run.Lease = &task.Lease{Holder: "s", GrantedAt: t0, ExpiresAt: t0.Add(time.Hour)}
	h.cur = []task.Task{run, ended("t2", "extract", "claude:haiku:low", task.OutcomeCompleted)}
	h.r.Flush()
	if h.r.held("tr") || !h.r.held("t2") {
		t.Fatalf("sweep: tr held %v, t2 held %v", h.r.held("tr"), h.r.held("t2"))
	}
}
