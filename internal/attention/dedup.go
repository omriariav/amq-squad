package attention

import (
	"strings"
	"time"

	"github.com/omriariav/amq-squad/v2/internal/state"
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

func Select(events []Event, prior State, sink string, renotify time.Duration, now time.Time, force bool) ([]Event, State, int) {
	if prior.Items == nil {
		prior.Items = map[string]Item{}
	}
	prior.Schema = 2
	active := map[string]bool{}
	selected := []Event{}
	suppressed := 0
	for _, e := range events {
		active[e.Key] = true
		item := prior.Items[e.Key]
		if e.Cleared {
			item.Active = false
			item.LastObserved = now
			prior.Items[e.Key] = item
			continue
		}
		d := item.Deliveries[sink]
		notify := force || !item.Active || d.Fingerprint != e.Fingerprint || d.LastNotified.IsZero()
		if !notify && escalated(e.Escalation, d.LastEscalation) {
			notify = true
		}
		if !notify && renotify > 0 && now.Sub(d.LastNotified) >= renotify {
			notify = true
		}
		item.Fingerprint = e.Fingerprint
		item.Active = true
		item.LastObserved = now
		if item.Deliveries == nil {
			item.Deliveries = map[string]Delivery{}
		}
		prior.Items[e.Key] = item
		if notify {
			selected = append(selected, e)
		} else {
			suppressed++
		}
	}
	for key, item := range prior.Items {
		if !active[key] && strings.Contains(key, "\x00gate\x00") {
			item.Active = false
			item.LastObserved = now
			prior.Items[key] = item
		}
	}
	return selected, prior, suppressed
}
func Commit(st State, key, sink string, e Event, now time.Time, err error) State {
	item := st.Items[key]
	if item.Deliveries == nil {
		item.Deliveries = map[string]Delivery{}
	}
	d := item.Deliveries[sink]
	d.LastAttempt = now
	if err == nil {
		d.Fingerprint = e.Fingerprint
		d.LastNotified = now
		d.LastSuccess = now
		d.LastEscalation = e.Escalation
		d.LastError = ""
	} else {
		d.LastFailure = now
		d.FailureCount++
		d.LastError = NormalizeDeliveryError(err.Error())
	}
	item.Deliveries[sink] = d
	st.Items[key] = item
	return st
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
func escalated(cur, prev string) bool {
	c := state.OperatorGateEscalation(cur)
	p := state.OperatorGateEscalation(prev)
	return state.OperatorGateEscalationRank(c) >= state.OperatorGateEscalationRank(state.OperatorGateEscalationReminder) && state.OperatorGateEscalationRank(c) > state.OperatorGateEscalationRank(p)
}
