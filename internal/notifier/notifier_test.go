package notifier

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/attention"
)

func TestCommandSinkArgvNoShellAndJSON(t *testing.T) {
	var name string
	var args []string
	var body []byte
	s := CommandSink{SinkID: "h", Argv: []string{"hook", "--stdin-json"}, Timeout: time.Second, Run: func(_ context.Context, n string, a []string, b []byte, _ []string) error {
		name = n
		args = a
		body = b
		return nil
	}}
	if err := s.Deliver(context.Background(), attention.Event{SchemaVersion: 1, EventType: "gate", AttentionOnly: true}); err != nil {
		t.Fatal(err)
	}
	if name != "hook" || strings.Join(args, " ") != "--stdin-json" || !strings.Contains(string(body), `"attention_only":true`) {
		t.Fatal(name, args, string(body))
	}
	if name == "sh" {
		t.Fatal("shell")
	}
}
