package notifier

import (
	"context"

	"github.com/omriariav/amq-squad/v3/internal/attention"
)

type Sink interface {
	ID() string
	Deliver(context.Context, attention.Event) error
}
type Result struct {
	SinkID    string `json:"sink_id"`
	Delivered bool   `json:"delivered"`
	Error     string `json:"error,omitempty"`
}

type UnavailableSink struct {
	SinkID string
	Reason string
}
