package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

type operatorDeliveryData struct {
	Enabled         bool   `json:"enabled"`
	Handle          string `json:"handle,omitempty"`
	InteractionMode string `json:"interaction_mode"`
	ApprovalSurface string `json:"approval_surface,omitempty"`
	Contract        string `json:"contract,omitempty"`
	DurableAMQ      bool   `json:"durable_amq"`
	WakeSupported   bool   `json:"wake_supported"`
	PollRequired    bool   `json:"poll_required"`
	PollOwner       string `json:"poll_owner,omitempty"`
	Reason          string `json:"reason,omitempty"`
	Guidance        string `json:"guidance,omitempty"`
}

func operatorDeliveryForTeam(t team.Team) operatorDeliveryData {
	op := team.EffectiveOperator(t)
	if !op.Enabled {
		return operatorDeliveryData{
			InteractionMode: team.OperatorInteractionUnspecified,
			Reason:          "operator gates disabled for this profile",
			Guidance:        "route human-facing decisions through the team lead/CTO rules instead of the virtual operator mailbox",
		}
	}
	handle := strings.TrimSpace(op.Handle)
	if handle == "" {
		handle = team.DefaultOperatorHandle
	}
	data := operatorDeliveryData{
		Enabled:         true,
		Handle:          handle,
		InteractionMode: op.InteractionMode,
		DurableAMQ:      true,
		WakeSupported:   false,
	}
	contract := team.OperatorContractForMode(op.InteractionMode)
	data.InteractionMode = contract.Mode
	data.ApprovalSurface = contract.ApprovalSurface
	data.Contract = contract.Contract
	data.PollRequired = contract.PollRequired
	data.PollOwner = contract.PollOwner
	switch op.InteractionMode {
	case team.OperatorInteractionLeadPane:
		data.Reason = "the human is present in the lead pane; durable gate mirroring remains authoritative"
		data.Guidance = "record every live approval or answer on the matching durable gate thread before acting"
	case team.OperatorInteractionSeparateTerminal:
		data.Reason = fmt.Sprintf("operator handle %q is virtual/non-runnable and is monitored from a separate terminal", handle)
		data.Guidance = "poll with `amq-squad notify` or `amq drain --include-body`; use the scoped answer command printed in bootstrap"
	case team.OperatorInteractionNOC:
		data.Reason = "operator delivery is owned by the NOC/global orchestrator"
		data.Guidance = "poll and answer using the explicit project/profile/session namespace; durable AMQ remains authoritative"
	case team.OperatorInteractionSelfOperator:
		data.Reason = "self_operator is persisted for forward compatibility but has no backing authorization behavior"
		data.Guidance = "continue routing human-only decisions to the operator; the lead cannot approve its own gates"
	default:
		data.Reason = fmt.Sprintf("operator handle %q is virtual/non-runnable; durable AMQ messages have no wakeable agent recipient", handle)
		data.Guidance = "operator or parent orchestrator must poll/drain the operator mailbox, gate threads, and status JSON; durable AMQ remains the source of truth"
	}
	return data
}

func operatorDeliverySummary(d operatorDeliveryData) string {
	if !d.Enabled {
		return "disabled"
	}
	if d.InteractionMode != "" && d.InteractionMode != team.OperatorInteractionUnspecified {
		return fmt.Sprintf("%s (approval_surface=%s, durable_amq=true, wake_supported=%t, poll_required=%t, poll_owner=%s)", d.InteractionMode, d.ApprovalSurface, d.WakeSupported, d.PollRequired, d.PollOwner)
	}
	if d.PollRequired {
		return "poll_required (durable_amq=true, wake_supported=false)"
	}
	if d.WakeSupported {
		return "wake_supported (durable_amq=true)"
	}
	return "durable_amq=true"
}
