package statecontract

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// The task machine's tables in docs/task-store-design.md are generated from
// the same declarations as the contract (S3-2): the states table and the
// moves table, each between its two markers. Regenerate them with
// `go test ./internal/statecontract/ -run TestTaskStoreDesignTablesAreCurrent -update-docs`.

// stateWords are the states table's words that the declarations do not hold:
// whether a lease is held, and what the state means. The rows, their order,
// and "terminal; " come from task.States; a state without words here fails
// TaskStoreStatesTable.
var stateWords = map[string][2]string{
	task.StatePending:    {"none", "queued; `wait_reason` names why it is not dispatched yet"},
	task.StateInProgress: {"held", "a seat holds the lease, claimed or not yet claimed"},
	task.StateBlocked:    {"none", "stopped on a decision the lead owes (`block_route: lead`) or, routed, the owner (`block_route: owner`)"},
	task.StateCompleted:  {"none", "the done report passed the commit step"},
	task.StateCancelled:  {"none", "the lead cancelled it"},
}

// TaskStoreStatesTable is the generated states table.
func TaskStoreStatesTable() (string, error) {
	var b strings.Builder
	b.WriteString("| state | lease | meaning |\n| --- | --- | --- |\n")
	seen := map[string]bool{}
	for _, s := range task.States() {
		w, ok := stateWords[s.Name]
		if !ok {
			return "", fmt.Errorf("state %q has no words in stateWords", s.Name)
		}
		seen[s.Name] = true
		meaning := w[1]
		if s.Terminal {
			meaning = "terminal; " + meaning
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", s.Name, w[0], meaning)
	}
	for name := range stateWords {
		if !seen[name] {
			return "", fmt.Errorf("stateWords names %q, which task.States does not declare", name)
		}
	}
	return b.String(), nil
}

// TaskStoreMovesTable is the generated moves table, one row per declared
// move in task.Moves order.
func TaskStoreMovesTable() string {
	var b strings.Builder
	b.WriteString("| from | via | writer | to | who | clears the block group | needs |\n| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, m := range task.Moves() {
		from := "(a new task)"
		if m.From != "" {
			from = "`" + m.From + "`"
		}
		// the declaration's own value, as the contract writes it
		// (clears_block_group); no answer word
		fmt.Fprintf(&b, "| %s | %s | `%s` | `%s` | %s | `%t` | %s |\n", from, codes(m.Via), m.Writer, m.To, codes(m.Who), m.ClearsBlockGroup, m.Needs)
	}
	return b.String()
}

// TaskStoreMovesBlock is the moves block: the moves table, then the block
// group's field names as task.BlockGroup reflects them from Task's JSON tags.
func TaskStoreMovesBlock() string {
	return TaskStoreMovesTable() + "\nThe block group: " + codes(task.BlockGroup()) + ".\n"
}

func codes(names []string) string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "`" + n + "`"
	}
	return strings.Join(out, ", ")
}

// DocsBlock names one generated block of docs/task-store-design.md: the text
// between its begin and end markers is exactly the generated table.
type DocsBlock struct {
	Name  string
	Table func() (string, error)
}

// TaskStoreDesignBlocks are the generated blocks, in document order.
func TaskStoreDesignBlocks() []DocsBlock {
	return []DocsBlock{
		{Name: "task states", Table: TaskStoreStatesTable},
		{Name: "task moves", Table: func() (string, error) { return TaskStoreMovesBlock(), nil }},
	}
}

// BeginMarker and EndMarker delimit a generated block.
func BeginMarker(name string) string {
	return "<!-- BEGIN generated: " + name + " (go test ./internal/statecontract/ -run TestTaskStoreDesignTablesAreCurrent -update-docs) -->"
}

func EndMarker(name string) string { return "<!-- END generated: " + name + " -->" }

// RegenerateTaskStoreDesign returns doc with every generated block replaced
// by its table. Each marker must occur exactly once in the whole document and
// be a line of its own, and the blocks must come in TaskStoreDesignBlocks
// order, each begin before its end; anything else is an error.
func RegenerateTaskStoreDesign(doc string) (string, error) {
	lines := strings.SplitAfter(doc, "\n")
	blocks := TaskStoreDesignBlocks()
	type span struct{ begin, end int }
	spans := make([]span, len(blocks))
	prev := -1
	for k, blk := range blocks {
		begin, end := BeginMarker(blk.Name), EndMarker(blk.Name)
		if strings.Count(doc, begin) != 1 || strings.Count(doc, end) != 1 {
			return "", fmt.Errorf("the %q markers must each occur exactly once", blk.Name)
		}
		b, e := -1, -1
		for i, l := range lines {
			switch strings.TrimSuffix(l, "\n") {
			case begin:
				b = i
			case end:
				e = i
			}
		}
		if b < 0 || e < 0 {
			return "", fmt.Errorf("the %q markers must each be a line of their own", blk.Name)
		}
		if b <= prev || e <= b {
			return "", fmt.Errorf("the %q markers are out of order", blk.Name)
		}
		spans[k], prev = span{b, e}, e
	}
	var out strings.Builder
	next := 0
	for k, blk := range blocks {
		table, err := blk.Table()
		if err != nil {
			return "", err
		}
		// the lines up to and including the begin marker, then the table,
		// then (at the next block or the end) the end marker onwards
		for _, l := range lines[next : spans[k].begin+1] {
			out.WriteString(l)
		}
		out.WriteString(table)
		next = spans[k].end
	}
	for _, l := range lines[next:] {
		out.WriteString(l)
	}
	return out.String(), nil
}
