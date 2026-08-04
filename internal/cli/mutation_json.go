package cli

import (
	squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"
	"github.com/omriariav/amq-squad/v2/internal/task"
)

type mutationAction struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Command string `json:"command"`
	// Canonical action-object contract fields (v2.12.0).
	ID                string `json:"id,omitempty"`
	ActionKind        string `json:"action_kind,omitempty"`
	Available         bool   `json:"available"`
	UnavailableReason string `json:"unavailable_reason,omitempty"`
}

type mutationResult struct {
	Command         string              `json:"command"`
	Status          string              `json:"status"`
	Project         string              `json:"project,omitempty"`
	Session         string              `json:"session,omitempty"`
	Profile         string              `json:"profile,omitempty"`
	Namespace       squadnamespace.Ref  `json:"namespace,omitempty"`
	ID              string              `json:"id,omitempty"`
	AttemptID       string              `json:"attempt_id,omitempty"`
	TaskID          string              `json:"task_id,omitempty"`
	Role            string              `json:"role,omitempty"`
	Assignee        string              `json:"assignee,omitempty"`
	Handle          string              `json:"handle,omitempty"`
	PaneID          string              `json:"pane_id,omitempty"`
	MessageID       string              `json:"message_id,omitempty"`
	ReleasedTaskIDs []string            `json:"released_task_ids,omitempty"`
	SuccessorTaskID string              `json:"successor_task_id,omitempty"`
	Outbox          []task.OutboxIntent `json:"outbox,omitempty"`
	Thread          string              `json:"thread,omitempty"`
	Root            string              `json:"root,omitempty"`
	Actions         []mutationAction    `json:"actions,omitempty"`
	// Changes carries the field-level before/after diff for preview envelopes
	// that have one (today: team member update --dry-run, #616). It is
	// omitempty, so every other mutation envelope is byte-identical to before.
	Changes []memberFieldChange `json:"changes,omitempty"`
}

func followUp(kind, label, command string) mutationAction {
	actionKind := "run"
	switch kind {
	case "status":
		actionKind = "display"
	}
	return mutationAction{Kind: kind, Label: label, Command: command, ID: kind, ActionKind: actionKind, Available: true}
}
