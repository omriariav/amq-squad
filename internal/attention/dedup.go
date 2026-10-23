package attention

import (
	"strings"
	"time"
)

type State struct {
	Schema int             `json:"schema"`
	Items  map[string]Item `json:"items"`
}
type Item struct {
	Fingerprint  string              `json:"fingerprint"`
	Active       bool                `json:"active"`
	LastObserved time.Time           `json:"last_observed,omitempty"`
	Deliveries   map[string]Delivery `json:"deliveries,omitempty"`
}
type Delivery struct {
	Fingerprint        string    `json:"fingerprint,omitempty"`
	LastNotified       time.Time `json:"last_notified,omitempty"`
	LastEscalation     string    `json:"last_escalation,omitempty"`
	LastAttempt        time.Time `json:"last_attempt,omitempty"`
	LastSuccess        time.Time `json:"last_success,omitempty"`
	LastFailure        time.Time `json:"last_failure,omitempty"`
	FailureCount       int       `json:"failure_count,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
	ReservationToken   string    `json:"reservation_token,omitempty"`
	ReservationExpires time.Time `json:"reservation_expires,omitempty"`
}

const maxDeliveryErrorRunes = 512

// NormalizeDeliveryError makes untrusted sink errors safe for one-line human
// diagnostics and bounds their durable footprint.
func NormalizeDeliveryError(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= maxDeliveryErrorRunes {
		return text
	}
	return string(runes[:maxDeliveryErrorRunes])
}
