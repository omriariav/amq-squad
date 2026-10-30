package routing

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Codex pass 4 on f989e4c7: one test row per item.

// threeRows is a session whose log holds three signed, chained cost rows.
func threeRows(t *testing.T) (*fix3Router, [][]byte) {
	h := newFix3(t, "") // the session seed file, as the report reads it
	cost := func(id string) task.Task { return ended(id, "extract", "claude:haiku:low", task.OutcomeCompleted) }
	h.cur = []task.Task{cost("t1"), cost("t2"), cost("t3")}
	if err := h.r.Flush(); err != nil {
		t.Fatal(err)
	}
	lines := logLines(t, h.root)
	if len(lines) != 3 {
		t.Fatalf("%d rows", len(lines))
	}
	return h, lines
}

func writeLines(t *testing.T, root string, lines [][]byte) {
	if err := os.WriteFile(OutcomePath(root), append(bytes.Join(lines, []byte("\n")), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hasProblem(ps []Problem, prefix string) bool {
	for _, p := range ps {
		if strings.HasPrefix(p.ID, prefix) {
			return true
		}
	}
	return false
}

// N1a: the rows are a hash chain: deleting, reordering or duplicating signed
// lines is history_tampered, a duplicated row is counted once, and the
// session's report sees the same breaks. Each case is read by a fresh
// router (a restart), where the in-run row memory cannot help.
func TestFix4ChainCatchesDeleteReorderDuplicate(t *testing.T) {
	cases := []struct {
		name   string
		edit   func([][]byte) [][]byte
		prefix string
		held   string
	}{
		{"delete the middle row", func(l [][]byte) [][]byte { return [][]byte{l[0], l[2]} }, "gap:2", "t2"},
		{"reorder", func(l [][]byte) [][]byte { return [][]byte{l[0], l[2], l[1]} }, "", ""},
		{"duplicate a cost row", func(l [][]byte) [][]byte { return [][]byte{l[0], l[1], l[2], l[1]} }, "outcome_order:", ""},
	}
	for _, c := range cases {
		h, lines := threeRows(t)
		writeLines(t, h.root, c.edit(lines))
		good, problems, verified, err := VerifySessionLog(h.root)
		if err != nil || !verified || len(problems) == 0 || (c.prefix != "" && !hasProblem(problems, c.prefix)) {
			t.Fatalf("%s: problems %+v (verified %v, err %v)", c.name, problems, verified, err)
		}
		seen := map[string]int{}
		for _, r := range good {
			seen[r.Task]++
		}
		for id, n := range seen {
			if n > 1 {
				t.Fatalf("%s: %s counted %d times", c.name, id, n)
			}
		}
		r := h.open("")
		if len(r.Tampered()) == 0 {
			t.Fatalf("%s: the restarted router saw nothing", c.name)
		}
		if c.held != "" {
			r.Route(newTask("t9", "plan")) // the first route holds for the break
			if !r.held(c.held) {
				t.Fatalf("%s: %s not held", c.name, c.held)
			}
		}
	}
	// A duplicated cost row is not counted twice in the day's spend.
	h, lines := threeRows(t)
	writeLines(t, h.root, [][]byte{lines[0], lines[1], lines[2], lines[2]})
	r := h.open("")
	if n := len(r.logged); n != 3 {
		t.Fatalf("logged %d rows from a log with a duplicate", n)
	}
}

// N1c: a line whose schema field is altered is a break (its tag is checked
// before any field is trusted), reported, and its task is held.
func TestFix4WrongSchemaLineIsReported(t *testing.T) {
	h, lines := threeRows(t)
	lines[1] = bytes.Replace(lines[1], []byte(`"schema":1`), []byte(`"schema":2`), 1)
	writeLines(t, h.root, lines)
	h.clock = h.clock.Add(2 * FullCheckInterval)
	h.r.Route(newTask("t9", "plan"))
	if !hasProblem(h.r.problems, "outcome_line:") || !h.r.held("t2") {
		t.Fatalf("problems %+v, t2 held %v", h.r.problems, h.r.held("t2"))
	}
	if _, problems, _, _ := VerifySessionLog(h.root); !hasProblem(problems, "outcome_line:") {
		t.Fatalf("report problems %+v", problems)
	}
}

// N1b: a break seen once does not hold again on later reads, and a held
// task is cleared only by the lead's reset (see TestHoldTamperedTaskOthersRoute).
func TestFix4KnownBreakHoldsOnce(t *testing.T) {
	h, lines := threeRows(t)
	writeLines(t, h.root, [][]byte{lines[0], lines[2]})
	h.clock = h.clock.Add(2 * FullCheckInterval)
	t2 := ended("t2", "extract", "claude:haiku:low", task.OutcomeUnclaimed)
	h.cur = []task.Task{t2}
	if route := h.r.Route(t2); route.Block != HistoryTampered {
		t.Fatalf("t2: %+v", route)
	}
	blockThenReset(h, h.r, &t2) // the observed block, then the lead's reset
	if route := h.r.Route(t2); route.Pool == nil {
		t.Fatalf("after the reset: %+v", route)
	}
	h.clock = h.clock.Add(2 * FullCheckInterval)
	if route := h.r.Route(t2); route.Pool == nil {
		t.Fatalf("the known gap held t2 again: %+v", route)
	}
	if r := h.open(""); r.Route(t2).Pool == nil {
		t.Fatal("a restart re-held t2 for the known gap")
	}
}
