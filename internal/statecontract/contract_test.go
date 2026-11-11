package statecontract

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var updateContract = flag.Bool("update-contract", false, "rewrite pi/test/state-contract.json")

func contractPath() string { return filepath.Join("..", "..", "pi", "test", "state-contract.json") }

func TestContractFileIsCurrent(t *testing.T) {
	want, err := JSON()
	if err != nil {
		t.Fatal(err)
	}
	if *updateContract {
		if err := os.WriteFile(contractPath(), want, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(contractPath())
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("pi/test/state-contract.json is stale; run: go test ./internal/statecontract/ -run TestContractFileIsCurrent -update-contract: %v", err)
	}
}

func TestCommittedContractIsSubstantial(t *testing.T) {
	data, err := os.ReadFile(contractPath())
	if err != nil {
		t.Fatal(err)
	}
	var contract Contract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Task.States) != 5 {
		t.Errorf("committed contract has %d states, want 5", len(contract.Task.States))
	}
	pairs := map[string]bool{}
	for _, move := range contract.Task.Moves {
		pairs[move.Writer+"\x00"+move.To] = true
	}
	if len(pairs) != 13 {
		t.Errorf("committed contract has %d distinct (writer,to) pairs, want 13", len(pairs))
	}
}
