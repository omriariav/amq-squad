//go:build !darwin

package notifier

import (
	"context"
	"fmt"
	"github.com/omriariav/amq-squad/v2/internal/attention"
	"time"
)

type DesktopSink struct {
	SinkID  string
	Timeout time.Duration
}

func (s DesktopSink) ID() string { return s.SinkID }
func (s DesktopSink) Deliver(context.Context, attention.Event) error {
	return fmt.Errorf("desktop notifications unavailable on this platform")
}
