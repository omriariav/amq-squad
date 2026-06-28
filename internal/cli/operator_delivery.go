package cli

import (
	"fmt"
	"strings"

	"github.com/omriariav/amq-squad/v2/internal/team"
)

type operatorDeliveryData struct {
	Enabled       bool   `json:"enabled"`
	Handle        string `json:"handle,omitempty"`
	DurableAMQ    bool   `json:"durable_amq"`
	WakeSupported bool   `json:"wake_supported"`
	PollRequired  bool   `json:"poll_required"`
	Reason        string `json:"reason,omitempty"`
	Guidance      string `json:"guidance,omitempty"`
}

func operatorDeliveryForTeam(t team.Team) operatorDeliveryData {
	op := team.EffectiveOperator(t)
	if !op.Enabled {
		return operatorDeliveryData{
			Reason:   "operator gates disabled for this profile",
			Guidance: "route human-facing decisions through the team lead/CTO rules instead of the virtual operator mailbox",
		}
	}
	handle := strings.TrimSpace(op.Handle)
	if handle == "" {
		handle = team.DefaultOperatorHandle
	}
	data := operatorDeliveryData{
		Enabled:       true,
		Handle:        handle,
		DurableAMQ:    true,
		WakeSupported: false,
		PollRequired:  true,
		Reason:        fmt.Sprintf("operator handle %q is virtual/non-runnable; durable AMQ messages have no wakeable agent recipient", handle),
		Guidance:      "operator or parent orchestrator must poll/drain the operator mailbox, gate threads, and status JSON; durable AMQ remains the source of truth",
	}
	if op.Runnable {
		data.WakeSupported = true
		data.PollRequired = false
		data.Reason = fmt.Sprintf("operator handle %q is runnable", handle)
		data.Guidance = "wakeable runnable operator handles may receive wake delivery; durable AMQ remains the source of truth"
	}
	return data
}

func operatorDeliverySummary(d operatorDeliveryData) string {
	if !d.Enabled {
		return "disabled"
	}
	if d.PollRequired {
		return "poll_required (durable_amq=true, wake_supported=false)"
	}
	if d.WakeSupported {
		return "wake_supported (durable_amq=true)"
	}
	return "durable_amq=true"
}
