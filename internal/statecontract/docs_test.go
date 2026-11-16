package statecontract

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

var updateDocs = flag.Bool("update-docs", false, "rewrite the generated tables in docs/task-store-design.md")

func designPath() string { return filepath.Join("..", "..", "docs", "task-store-design.md") }

// S3-2: the states and moves tables of docs/task-store-design.md are exactly
// what the task machine's declarations generate. A declaration changed
// without a regeneration, a hand edit between the markers, or a marker
// removed fails here.
func TestTaskStoreDesignTablesAreCurrent(t *testing.T) {
	got, err := os.ReadFile(designPath())
	if err != nil {
		t.Fatal(err)
	}
	want, err := RegenerateTaskStoreDesign(string(got))
	if err != nil {
		t.Fatalf("docs/task-store-design.md: %v", err)
	}
	if *updateDocs {
		if err := os.WriteFile(designPath(), []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		got = []byte(want)
	}
	if string(got) != want {
		t.Fatal("docs/task-store-design.md's generated tables are stale; run: go test ./internal/statecontract/ -run TestTaskStoreDesignTablesAreCurrent -update-docs")
	}
	// one table row per declared state and per declared move
	for _, c := range []struct {
		block string
		rows  int
	}{{"task states", len(task.States())}, {"task moves", len(task.Moves())}} {
		begin := strings.Index(want, BeginMarker(c.block))
		end := strings.Index(want, EndMarker(c.block))
		// the header and the separator row are not table rows
		if n := strings.Count(want[begin:end], "\n| ") - 2; n != c.rows {
			t.Errorf("%s: %d table rows, want %d", c.block, n, c.rows)
		}
	}
}

// The who legend, the hand-written paragraph between "## Moves" and the
// moves block, names every who value the moves table prints (the reviewer's
// ruling on S3-2: the legend stays prose, checked against the declaration).
func TestTaskStoreDesignWhoLegend(t *testing.T) {
	data, err := os.ReadFile(designPath())
	if err != nil {
		t.Fatal(err)
	}
	doc := string(data)
	i := strings.Index(doc, "\n## Moves\n")
	j := strings.Index(doc, BeginMarker("task moves"))
	if i < 0 || j < i {
		t.Fatal("docs/task-store-design.md has no \"## Moves\" section before the moves block")
	}
	legend := doc[i:j]
	seen := map[string]bool{}
	for _, m := range task.Moves() {
		for _, who := range m.Who {
			if !seen[who] && !strings.Contains(legend, "`"+who+"`") {
				t.Errorf("the who legend does not name `%s`", who)
			}
			seen[who] = true
		}
	}
}

// The markers' shape: each marker exactly once in the whole document and a
// line of its own, and the blocks in TaskStoreDesignBlocks order, each begin
// before its end (Codex's pass 1 on the prototype: two whole blocks swapped,
// a duplicate marker not on its own line, and text beside a marker were
// accepted).
func TestTaskStoreDesignMarkers(t *testing.T) {
	blocks := TaskStoreDesignBlocks()
	states, moves := blocks[0].Name, blocks[1].Name
	block := func(name string) string { return BeginMarker(name) + "\nSTALE-ROW\n" + EndMarker(name) + "\n" }
	doc := "## States\n" + block(states) + "## Moves\n" + block(moves) + "more\n"
	got, err := RegenerateTaskStoreDesign(doc)
	if err != nil {
		t.Fatalf("well-formed markers: %v", err)
	}
	if !strings.HasPrefix(got, "## States\n"+BeginMarker(states)+"\n| state |") || !strings.HasSuffix(got, "\n"+EndMarker(moves)+"\nmore\n") || strings.Contains(got, "STALE-ROW") {
		t.Fatalf("the regenerated text outside the tables changed:\n%s", got)
	}
	for name, bad := range map[string]string{
		"a begin marker removed":                   strings.Replace(doc, BeginMarker(moves)+"\n", "", 1),
		"an end marker removed":                    strings.Replace(doc, EndMarker(moves)+"\n", "", 1),
		"a block twice":                            doc + block(moves),
		"a begin and its end reversed":             "## States\n" + block(states) + "## Moves\n" + EndMarker(moves) + "\nSTALE-ROW\n" + BeginMarker(moves) + "\nmore\n",
		"the two blocks swapped":                   "## States\n" + block(moves) + "## Moves\n" + block(states) + "more\n",
		"a duplicate begin marker not on its line": doc + BeginMarker(moves) + " duplicate\n",
		"text before a begin marker on its line":   strings.Replace(doc, BeginMarker(moves), "x "+BeginMarker(moves), 1),
		"text after an end marker on its line":     strings.Replace(doc, EndMarker(states), EndMarker(states)+" x", 1),
	} {
		if _, err := RegenerateTaskStoreDesign(bad); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
