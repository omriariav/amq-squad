package notifier

import (
	"context"
	"errors"
	"github.com/omriariav/amq-squad/v2/internal/attention"
	"strings"
	"testing"
	"time"
)

type fakeSink struct {
	id  string
	err error
}

func (f fakeSink) ID() string                                     { return f.id }
func (f fakeSink) Deliver(context.Context, attention.Event) error { return f.err }
func TestFanOutIndependent(t *testing.T) {
	r := FanOut(context.Background(), []Sink{fakeSink{"ok", nil}, fakeSink{"bad", errors.New("x")}}, attention.Event{})
	if !r[0].Delivered || r[1].Delivered {
		t.Fatal(r)
	}
}
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
