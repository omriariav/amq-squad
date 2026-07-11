//go:build darwin

package notifier

import (
	"context"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/attention"
)

func TestDesktopSinkPassesBodyAsOneArgvElement(t *testing.T) {
	var got []string
	sink := DesktopSink{SinkID: "desktop", Run: func(_ context.Context, _ string, args ...string) error {
		got = append([]string(nil), args...)
		return nil
	}}
	if err := sink.Deliver(context.Background(), attention.Event{EventType: "gate", Profile: "default", Session: "s", Summary: "body with quotes ' and spaces"}); err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[len(got)-1] != "gate default/s body with quotes ' and spaces" {
		t.Fatalf("argv=%q", got)
	}
}
