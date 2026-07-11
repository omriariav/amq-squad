package wizard

import "fmt"

type CapabilityID string

const (
	CapabilitySelfOperator          CapabilityID = "self_operator"
	CapabilityOperatorNotifications CapabilityID = "operator_notifications"
)

type Capability struct {
	ID        CapabilityID
	Available bool
	Issue     int
	ShipsIn   string
	Reason    string
}

type CapabilitySet map[CapabilityID]Capability

type Option struct {
	ID          string
	Label       string
	Consequence string
	Requires    CapabilityID
	Blocked     bool
	BlockReason string
}

func DefaultCapabilities() CapabilitySet {
	return CapabilitySet{
		CapabilitySelfOperator: {
			ID: CapabilitySelfOperator, Available: true, Issue: 391, ShipsIn: "v2.19.0",
		},
		CapabilityOperatorNotifications: {
			ID: CapabilityOperatorNotifications, Available: true, Issue: 390, ShipsIn: "v2.19.0",
		},
	}
}

func OperatorOptions() []Option {
	return []Option{
		{ID: "lead_pane", Label: "Live in the lead pane", Consequence: "Approve by typing in the lead window; the lead mirrors every decision to gate/<topic>."},
		{ID: "separate_terminal", Label: "Separate operator terminal", Consequence: "Poll durable gates and answer with explicit operator AMQ replies."},
		{ID: "noc", Label: "NOC/global board", Consequence: "A global operator board polls and answers this run by explicit namespace."},
		{ID: "self_operator", Label: "Self-operator / delegated approval", Consequence: "The lead may approve only explicitly allowlisted merge gates; spawn, release, tag, publish, external, and destructive gates remain human-only; merges require a second actor.", Requires: CapabilitySelfOperator},
	}
}

func CapabilityAvailable(caps CapabilitySet, id CapabilityID) bool {
	if id == "" {
		return true
	}
	capability, ok := caps[id]
	return ok && capability.Available
}

func OptionAvailabilityLabel(caps CapabilitySet, option Option) string {
	if option.Requires == "" || CapabilityAvailable(caps, option.Requires) {
		return ""
	}
	capability := caps[option.Requires]
	if capability.ShipsIn != "" && capability.Issue > 0 {
		return fmt.Sprintf("ships in %s: #%d", capability.ShipsIn, capability.Issue)
	}
	if capability.Reason != "" {
		return capability.Reason
	}
	return "unavailable"
}

func effectiveCapabilities(caps CapabilitySet) CapabilitySet {
	if caps == nil {
		return DefaultCapabilities()
	}
	return caps
}

func operatorChoices(caps CapabilitySet) []choice {
	caps = effectiveCapabilities(caps)
	options := OperatorOptions()
	choices := make([]choice, 0, len(options))
	for _, option := range options {
		availability := OptionAvailabilityLabel(caps, option)
		label := option.Label + " · " + option.Consequence
		// The ships-in provenance appears only while the capability is
		// genuinely unavailable; a shipped feature must not read as future.
		if availability != "" {
			label += " [" + availability + "]"
		}
		if option.Blocked {
			label += " [" + option.BlockReason + "]"
		}
		choices = append(choices, choice{
			value: option.ID, label: label, disabled: availability != "" || option.Blocked, consequence: option.Consequence, capability: option.Requires != "",
		})
	}
	return choices
}
