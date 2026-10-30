// Package scheduler is the deterministic per-squad scheduler of plan 4.5
// (R16 seat side, R17, R18): it grants task leases, sends a task to an idle
// seat of the task's pool or requests a seat inside the grant, returns an
// unclaimed task to the pool on lease expiry, retries through the router
// hook, closes idle seats, and starts no new seat after the first
// usage-limit event in the window (plan 7.2). It does not launch: seats are
// requested through SeatLauncher (2b-2).
package scheduler

import "time"

// UsageLimitEvent is one provider usage-limit (or 429 quota) observation.
// 2b-3's detector produces them from provider session files.
type UsageLimitEvent struct {
	// At is when the provider reported the limit.
	At time.Time `json:"at"`
	// Provider is the runtime family: claude, codex, pi, ...
	Provider string `json:"provider"`
	// Seat is the handle whose session carried the signal, when known.
	Seat string `json:"seat,omitempty"`
	// ResetAt is when the provider says the window resets; zero when it
	// does not say. With a zero ResetAt the stop lasts until the scheduler
	// is restarted or the lead clears it.
	ResetAt time.Time `json:"reset_at,omitempty"`
	// Source names the evidence (file path and line, or a hook event id).
	Source string `json:"source"`
}

// UsageLimitSource is the event source the scheduler polls once per tick.
// It returns every event observed at or after since, oldest first. 2b-3
// implements it; a nil source means no events.
type UsageLimitSource interface {
	UsageLimitEvents(since time.Time) ([]UsageLimitEvent, error)
}

// UsageLimitFunc adapts a function to UsageLimitSource.
type UsageLimitFunc func(since time.Time) ([]UsageLimitEvent, error)

func (f UsageLimitFunc) UsageLimitEvents(since time.Time) ([]UsageLimitEvent, error) {
	return f(since)
}
