package cli

import "time"

// goalDeliveryAttempt is temporary step-6 state for the legacy goal delivery
// machinery. It is intentionally in-memory only and never creates a local
// delivery artifact.
type goalDeliveryAttempt struct {
	AttemptID  string
	CreatedAt  time.Time
	Status     string
	Detail     string
	MessageID  string
	Method     string
	PaneID     string
	Root       string
	Thread     string
	Fallback   bool
	AMQInvoked bool
}

func newGoalDeliveryAttempt(kind, role, handle string) goalDeliveryAttempt {
	now := time.Now().UTC()
	return goalDeliveryAttempt{
		AttemptID: deliveryAttemptID(now, kind, role, handle),
		CreatedAt: now,
		Status:    "queued",
	}
}

func (*goalDeliveryAttempt) addStage(string, string) {}
