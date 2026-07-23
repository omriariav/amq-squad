package act

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omriariav/amq-squad/v2/internal/state"
)

func controlContext() Context {
	return Context{Project: "/Code/app", Profile: "review", Session: "issue-515"}
}

func controlThread() state.ThreadSummary {
	return state.ThreadSummary{
		ID: "gate/release", Subject: "APPROVAL: release",
		Participants:      []string{"user", "cto"},
		OperatorGateState: state.OperatorGateStateOpen,
		OperatorGate:      &state.OperatorGateSignal{From: "cto"},
	}
}

func TestControlActionPreviewsAreExactFirstClassCommands(t *testing.T) {
	ctx := controlContext()
	thread := controlThread()
	replyThread := thread
	replyThread.ID = "p2p/cto__user"
	replyThread.OperatorGate = nil
	replyThread.OperatorGateState = state.OperatorGateStateUnknown
	cases := []struct {
		name   string
		action Action
		want   []string
	}{
		{
			name:   "reply",
			action: Reply(ctx, replyThread, state.DefaultOperatorHandle, "looks good"),
			want:   []string{"operator", "send", "--to", "cto", "--thread", "p2p/cto__user", "--kind", "answer", "--yes", "--json"},
		},
		{
			name:   "approve",
			action: Approve(ctx, thread),
			want:   []string{"operator", "answer", "--gate", "gate/release", "--to", "cto", "--approved", "--json"},
		},
		{
			name:   "deny",
			action: Deny(ctx, thread, "unsafe"),
			want:   []string{"operator", "answer", "--gate", "gate/release", "--to", "cto", "--denied", "--reason", "unsafe", "--json"},
		},
		{
			name:   "message",
			action: Message(ctx, "qa", "Review", "please review"),
			want:   []string{"operator", "send", "--to", "qa", "--kind", "status", "--yes", "--json"},
		},
		{
			name:   "broadcast",
			action: Broadcast(ctx, "Stand up", "join"),
			want:   []string{"broadcast", "--thread", "broadcast/stand-up", "--yes", "--json"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argv := tc.action.argv()
			if len(argv) == 0 || argv[0] != "amq-squad" {
				t.Fatalf("argv = %#v, want amq-squad first", argv)
			}
			for _, want := range []string{"--project", "/Code/app", "--profile", "review", "--session", "issue-515"} {
				if !containsArg(argv, want) {
					t.Fatalf("argv missing scope token %q: %#v", want, argv)
				}
			}
			for _, want := range tc.want {
				if !containsArg(argv, want) {
					t.Fatalf("argv missing %q: %#v", want, argv)
				}
			}
			parts := make([]string, len(argv))
			for i, arg := range argv {
				parts[i] = shellQuote(arg)
			}
			if got, want := Preview(tc.action), strings.Join(parts, " "); got != want {
				t.Fatalf("preview mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

func TestReplyExcludesCustomOperatorHandle(t *testing.T) {
	thread := state.ThreadSummary{
		ID: "p2p/cto__ops", Subject: "Question",
		Participants: []string{"ops", "cto"},
	}
	action := Reply(controlContext(), thread, "ops", "answer")
	if action.To != "cto" {
		t.Fatalf("reply recipients = %q, want cto", action.To)
	}
}

func TestSendUsesExactPreviewArgvAndReturnsDurableReceipt(t *testing.T) {
	action := Message(controlContext(), "qa", "Review now", "please review")
	var gotArgs, gotEnv []string
	previous := controlExec
	controlExec = func(args []string, env []string) ([]byte, []byte, error) {
		gotArgs = append([]string(nil), args...)
		gotEnv = append([]string(nil), env...)
		return []byte(`{
  "schema_version": 1,
  "kind": "operator_send",
  "data": {
    "message_id": "msg-515",
    "thread": "p2p/qa__user",
    "delivery_receipt": {
      "attempt_id": "attempt-515",
      "message_id": "msg-515",
      "delivery_state": "sent",
      "path": "/Code/app/.amq-squad/delivery-receipts/default/issue-515/attempt-515.json"
    }
  }
}`), nil, nil
	}
	t.Cleanup(func() { controlExec = previous })
	t.Setenv("AM_ROOT", "/stale")
	t.Setenv("AM_BASE_ROOT", "/stale-base")
	t.Setenv("AM_SESSION", "stale")
	t.Setenv("AM_ME", "stale")

	receipt, err := Send(action)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if receipt.MessageID != "msg-515" || receipt.AttemptID != "attempt-515" || receipt.Path == "" {
		t.Fatalf("receipt = %+v", receipt)
	}
	if want := action.argv()[1:]; !reflect.DeepEqual(gotArgs, want) {
		t.Fatalf("executed args\n got: %#v\nwant: %#v", gotArgs, want)
	}
	for _, entry := range gotEnv {
		for _, key := range []string{"AM_ROOT=", "AM_BASE_ROOT=", "AM_SESSION=", "AM_ME="} {
			if strings.HasPrefix(entry, key) {
				t.Fatalf("stale identity leaked to child: %q", entry)
			}
		}
	}
}

func TestSendFailsClosedBeforeSubprocess(t *testing.T) {
	called := false
	previous := controlExec
	controlExec = func([]string, []string) ([]byte, []byte, error) {
		called = true
		return nil, nil, errors.New("should not run")
	}
	t.Cleanup(func() { controlExec = previous })

	_, err := Send(Action{Intent: IntentApprove, Context: controlContext(), Gate: "p2p/cto__user"})
	if err == nil || !strings.Contains(err.Error(), "gate/<topic>") {
		t.Fatalf("invalid gate error = %v", err)
	}
	if called {
		t.Fatal("invalid action reached subprocess")
	}
}

func TestDecodeControlReceiptRejectsMissingStableEvidence(t *testing.T) {
	_, err := decodeControlReceipt([]byte(`{"kind":"operator_send","data":{"delivery_receipt":{"attempt_id":"a"}}}`))
	if err == nil {
		t.Fatal("missing message id/path should be rejected")
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
