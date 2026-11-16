package statecontract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

func taskStoreDesignPath() string { return filepath.Join("..", "..", "docs", "task-store-design.md") }

func TestTaskStoreDesignMachineSectionIsCurrent(t *testing.T) {
	got, err := os.ReadFile(taskStoreDesignPath())
	if err != nil {
		t.Fatal(err)
	}
	want := TaskMachineDocsBegin + "\n" + TaskMachineDocs() + TaskMachineDocsEnd
	start := strings.Index(string(got), TaskMachineDocsBegin)
	end := strings.Index(string(got), TaskMachineDocsEnd)
	if start < 0 || end < start {
		t.Fatalf("%s must contain one task-machine section between %q and %q", taskStoreDesignPath(), TaskMachineDocsBegin, TaskMachineDocsEnd)
	}
	end += len(TaskMachineDocsEnd)
	if section := string(got)[start:end]; section != want {
		t.Fatalf("%s task-machine section is stale; regenerate it from statecontract.TaskMachineDocs", taskStoreDesignPath())
	}
}

func TestTaskMachineWhoLegendCoversMoves(t *testing.T) {
	declared := map[string]bool{}
	for _, move := range task.Moves() {
		for _, who := range move.Who {
			declared[who] = true
			if strings.TrimSpace(WhoLegend[who]) == "" {
				t.Errorf("move %s -> %s uses %q without a who legend", move.From, move.To, who)
			}
		}
	}
	for who := range WhoLegend {
		if !declared[who] {
			t.Errorf("who legend has stale entry %q", who)
		}
	}
}
