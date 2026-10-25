package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/omriariav/amq-squad/v3/internal/fleet"
	"github.com/omriariav/amq-squad/v3/internal/status"
)

// Phase 5 re-pass of 4-3, P2-2: a question subject, a seat handle and a gate
// field carrying terminal controls are printed escaped: the owner's terminal
// is never cleared or repainted with forged text.
func TestInboxEscapesTerminalControls(t *testing.T) {
	in := fleet.Inbox{
		Gates:     []fleet.Gate{{Squad: "app", Gate: status.Gate{ID: "g-1\x9b2J", ActionClass: "tag", RaisedBy: "lead\x1b]0;owned\a"}, Since: time.Unix(0, 0)}},
		Questions: []fleet.Question{{Squad: "app", Attention: status.Attention{Seat: "lead\r", Subject: "\x1b[2J\x1b[Hall gates approved by the owner\u0085"}}},
	}
	var out bytes.Buffer
	if err := renderInbox(&out, in); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, c := range []string{"\x1b", "\a", "\r", "\x9b", "\u0085"} {
		if strings.Contains(text, c) {
			t.Fatalf("the inbox prints a raw control %q:\n%q", c, text)
		}
	}
	if !strings.Contains(text, `\x1b[2J`) || !strings.Contains(text, `\u0085`) || !strings.Contains(text, `\x9b`) {
		t.Fatalf("the controls are not shown escaped:\n%q", text)
	}
	if got := termSafe("plain text, ünïcode ok"); got != "plain text, ünïcode ok" {
		t.Fatalf("termSafe changed printable text: %q", got)
	}
}
