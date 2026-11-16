package statecontract

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

const (
	// TaskMachineDocsBegin and TaskMachineDocsEnd delimit the generated task
	// machine section in docs/task-store-design.md.
	TaskMachineDocsBegin = "<!-- task-machine:begin -->"
	TaskMachineDocsEnd   = "<!-- task-machine:end -->"
)

// WhoLegend explains every actor token that task.Moves may declare. Keep the
// wording here so the generated document and its contract have one source.
var WhoLegend = map[string]string{
	task.WhoLiveHolder:     "the current holder of a live, claimed lease",
	task.WhoCreator:        "the task creator",
	task.WhoProvenLead:     "a lead whose authority has been proved by the caller",
	task.WhoScheduler:      "the scheduler",
	task.WhoHarnessShell:   "the harness shell",
	task.WhoAssertedHandle: "a caller whose handle is asserted by the command boundary",
}

// TaskMachineDocs renders the task-machine declaration as the Markdown kept
// between TaskMachineDocsBegin and TaskMachineDocsEnd.
func TaskMachineDocs() string {
	var b strings.Builder
	b.WriteString("## States\n\n")
	b.WriteString("| state | terminal |\n| --- | --- |\n")
	for _, state := range task.States() {
		fmt.Fprintf(&b, "| `%s` | %t |\n", state.Name, state.Terminal)
	}

	b.WriteString("\n## Moves\n\n")
	b.WriteString("| from | via | writer | to | who | needs | clears block group |\n")
	b.WriteString("| --- | --- | --- | --- | --- | --- | --- |\n")
	for _, move := range task.Moves() {
		from := "(new task)"
		if move.From != "" {
			from = "`" + move.From + "`"
		}
		fmt.Fprintf(&b, "| %s | %s | `%s` | `%s` | %s | %s | %t |\n",
			from, codeList(move.Via), move.Writer, move.To, codeList(move.Who), cell(move.Needs), move.ClearsBlockGroup)
	}

	b.WriteString("\n## Block group\n\n")
	b.WriteString("| field |\n| --- |\n")
	for _, field := range task.BlockGroup() {
		fmt.Fprintf(&b, "| `%s` |\n", field)
	}

	b.WriteString("\n## Who legend\n\n")
	b.WriteString("| who | meaning |\n| --- | --- |\n")
	for _, who := range declaredWhos() {
		fmt.Fprintf(&b, "| `%s` | %s |\n", who, WhoLegend[who])
	}
	return b.String()
}

func declaredWhos() []string {
	seen := make(map[string]bool)
	var whos []string
	for _, move := range task.Moves() {
		for _, who := range move.Who {
			if !seen[who] {
				seen[who] = true
				whos = append(whos, who)
			}
		}
	}
	return whos
}

func codeList(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = "`" + value + "`"
	}
	return strings.Join(quoted, ", ")
}

func cell(value string) string {
	if value == "" {
		return ""
	}
	return strings.ReplaceAll(value, "|", "\\|")
}
