//go:build darwin

package notifier

import (
	"context"
	"fmt"
	"github.com/omriariav/amq-squad/v2/internal/attention"
	"os/exec"
	"strings"
)

type DesktopSink struct {
	SinkID string
	Run    func(context.Context, string, ...string) error
}

func (s DesktopSink) ID() string { return s.SinkID }
func (s DesktopSink) Deliver(ctx context.Context, e attention.Event) error {
	p, err := exec.LookPath("osascript")
	if err != nil {
		return fmt.Errorf("desktop degraded: osascript unavailable: %w", err)
	}
	body := e.EventType + " " + e.Profile + "/" + e.Session + " " + e.Summary
	if len(body) > 240 {
		body = body[:240]
	}
	args := []string{"-e", `on run argv`, "-e", `display notification (item 1 of argv) with title "amq-squad: operator attention"`, "-e", `end run`, body}
	if s.Run != nil {
		return s.Run(ctx, p, args...)
	}
	return exec.CommandContext(ctx, p, args...).Run()
}

var _ = strings.TrimSpace
