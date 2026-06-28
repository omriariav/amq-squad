package cli

import squadnamespace "github.com/omriariav/amq-squad/v2/internal/namespace"

type mutationAction struct {
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Command string `json:"command"`
}

type mutationResult struct {
	Command   string             `json:"command"`
	Status    string             `json:"status"`
	Project   string             `json:"project,omitempty"`
	Session   string             `json:"session,omitempty"`
	Profile   string             `json:"profile,omitempty"`
	Namespace squadnamespace.Ref `json:"namespace,omitempty"`
	ID        string             `json:"id,omitempty"`
	TaskID    string             `json:"task_id,omitempty"`
	Role      string             `json:"role,omitempty"`
	Assignee  string             `json:"assignee,omitempty"`
	Handle    string             `json:"handle,omitempty"`
	MessageID string             `json:"message_id,omitempty"`
	Root      string             `json:"root,omitempty"`
	Actions   []mutationAction   `json:"actions,omitempty"`
}

func followUp(kind, label, command string) mutationAction {
	return mutationAction{Kind: kind, Label: label, Command: command}
}
