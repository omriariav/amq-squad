// Package statecontract builds the generated state-machine contract fixture.
package statecontract

import (
	"bytes"
	"encoding/json"

	"github.com/omriariav/amq-squad/v3/internal/task"
)

// Contract is the generated state-contract document.
type Contract struct {
	Schema  string          `json:"schema"`
	Task    TaskContract    `json:"task"`
	Reasons ReasonsContract `json:"reasons"`
}

// TaskContract is the task portion of the state-contract document.
type TaskContract struct {
	States      []task.StateContract      `json:"states"`
	Moves       []task.MoveContract       `json:"moves"`
	Outcomes    []string                  `json:"outcomes"`
	Routes      []string                  `json:"routes"`
	BlockGroup  []string                  `json:"block_group"`
	OutcomeSets []task.OutcomeSetContract `json:"outcome_sets"`
}

// ReasonsContract is the reasons portion of the state-contract document.
type ReasonsContract struct {
	Block      ReasonList    `json:"block"`
	Wait       ReasonList    `json:"wait"`
	NotReasons []ReasonEntry `json:"not_reasons"`
}

// Build builds the current state contract from package declarations.
func Build() Contract {
	return Contract{
		Schema: "amq-squad-state-contract/1",
		Task: TaskContract{
			States: task.States(), Moves: task.Moves(), Outcomes: task.Outcomes(),
			Routes: task.Routes(), BlockGroup: task.BlockGroup(), OutcomeSets: task.OutcomeSets(),
		},
		Reasons: ReasonsContract{Block: BlockReasons(), Wait: WaitReasons(), NotReasons: NotReasons()},
	}
}

// JSON returns the indented contract JSON with stable field order and one
// trailing newline.
func JSON() ([]byte, error) {
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(Build()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
