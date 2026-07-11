package notifier

import (
	"context"
	"fmt"

	"github.com/omriariav/amq-squad/v2/internal/attention"
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

func FanOut(ctx context.Context, sinks []Sink, event attention.Event) []Result {
	out := make([]Result, 0, len(sinks))
	for _, s := range sinks {
		err := s.Deliver(ctx, event)
		r := Result{SinkID: s.ID(), Delivered: err == nil}
		if err != nil {
			r.Error = err.Error()
		}
		out = append(out, r)
	}
	return out
}

type UnavailableSink struct {
	SinkID string
	Reason string
}

func (s UnavailableSink) ID() string { return s.SinkID }
func (s UnavailableSink) Deliver(context.Context, attention.Event) error {
	return fmt.Errorf("%s", s.Reason)
}
