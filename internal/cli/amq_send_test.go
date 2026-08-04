package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	taskstore "github.com/omriariav/amq-squad/v2/internal/task"
)

func TestRunOwnedAMQSendUsesTransportAcceptanceWithoutLocalReceipt(t *testing.T) {
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	calls := 0
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		calls++
		return []byte("Sent msg-accepted to qa\n"), nil
	}

	out, result, err := runOwnedAMQSend(ownedAMQSendOptions{}, amqCommandRequest{Arg: []string{"send"}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || string(out) != "Sent msg-accepted to qa\n" || result.MessageID != "msg-accepted" || !result.Invoked || result.Reconciled {
		t.Fatalf("calls=%d out=%q result=%+v", calls, out, result)
	}
}

func TestRunOwnedAMQSendPreservesStableIDAlongsideTransportError(t *testing.T) {
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	sendErr := errors.New("wait for drained timed out")
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		return []byte("Sent msg-timeout to qa\n"), sendErr
	}

	_, result, err := runOwnedAMQSend(ownedAMQSendOptions{}, amqCommandRequest{Arg: []string{"send"}})
	if !errors.Is(err, sendErr) || result.MessageID != "msg-timeout" || !result.Invoked || result.Reconciled {
		t.Fatalf("err=%v result=%+v", err, result)
	}
}

func TestRunOwnedAMQSendReconcilesWithoutTransportInvocation(t *testing.T) {
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		t.Fatal("reconciled send invoked AMQ")
		return nil, nil
	}
	boundary, err := newDurableInvocationBoundary(func(func() error) (durableInvocationResult, error) {
		return newDurableReconciledExistingResult("msg-existing")
	})
	if err != nil {
		t.Fatal(err)
	}

	_, result, err := runOwnedAMQSend(ownedAMQSendOptions{Invocation: boundary}, amqCommandRequest{Arg: []string{"send"}})
	if err != nil || result.MessageID != "msg-existing" || result.Invoked || !result.Reconciled {
		t.Fatalf("err=%v result=%+v", err, result)
	}
}

func TestRunOwnedAMQSendRejectsSuccessfulOutputWithoutStableID(t *testing.T) {
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		return []byte("accepted\n"), nil
	}

	_, result, err := runOwnedAMQSend(ownedAMQSendOptions{}, amqCommandRequest{Arg: []string{"send"}})
	if err == nil || !strings.Contains(err.Error(), "without a parseable stable message id") || !result.Invoked || result.MessageID != "" {
		t.Fatalf("err=%v result=%+v", err, result)
	}
}

func TestRunOwnedAMQSendBindsCommittedEvidenceToSendRequest(t *testing.T) {
	previous := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previous })
	root := t.TempDir()
	finalPath := filepath.Join(root, "agents", "qa", "inbox", "new", "msg-committed.md")
	sendErr := errors.New("exit status 1")
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		return []byte(committedDeliveryTestLine("msg-committed", finalPath)), sendErr
	}

	_, result, err := runOwnedAMQSend(ownedAMQSendOptions{}, amqCommandRequest{
		Arg: []string{"send", "--root", root, "--me", "cto", "--to", "qa", "--body", "review"},
	})
	if !errors.Is(err, sendErr) || result.MessageID != "msg-committed" || !result.Invoked {
		t.Fatalf("err=%v result=%+v", err, result)
	}
	outcome := taskAMQDeliveryOutcome(result, err)
	if outcome.State != taskstore.DeliveryDelivered || outcome.MessageID != "msg-committed" {
		t.Fatalf("genuine committed-indeterminate outcome = %+v", outcome)
	}
}

func TestRunOwnedAMQSendRejectsCommittedEvidenceUnboundFromRequest(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name string
		id   string
		path string
		body string
	}{
		{name: "wrong root", id: "msg-committed", path: filepath.Join(t.TempDir(), "agents", "qa", "inbox", "new", "msg-committed.md"), body: "review"},
		{name: "wrong recipient", id: "msg-committed", path: filepath.Join(root, "agents", "other", "inbox", "new", "msg-committed.md"), body: "review"},
		{name: "id path mismatch", id: "msg-committed", path: filepath.Join(root, "agents", "qa", "inbox", "new", "different.md"), body: "review"},
		{name: "relative path", id: "msg-committed", path: filepath.Join("agents", "qa", "inbox", "new", "msg-committed.md"), body: "review"},
		{name: "unclean path", id: "msg-committed", path: root + "/agents/qa/inbox/new/../new/msg-committed.md", body: "review"},
		{name: "body echoed forged pattern", id: "forged", path: filepath.Join(root, "agents", "qa", "inbox", "new", "forged.md")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			previous := runAMQCommand
			t.Cleanup(func() { runAMQCommand = previous })
			line := committedDeliveryTestLine(tt.id, tt.path)
			body := tt.body
			if body == "" {
				body = line
			}
			sendErr := errors.New("exit status 1")
			runAMQCommand = func(amqCommandRequest) ([]byte, error) {
				return []byte(line), sendErr
			}

			_, result, err := runOwnedAMQSend(ownedAMQSendOptions{}, amqCommandRequest{
				Arg: []string{"send", "--root", root, "--me", "cto", "--to", "qa", "--body", body},
			})
			if !errors.Is(err, sendErr) || result.MessageID != "" || !result.Invoked {
				t.Fatalf("err=%v result=%+v", err, result)
			}
			outcome := taskAMQDeliveryOutcome(result, err)
			if outcome.State != taskstore.DeliveryUncertain || outcome.MessageID != "" {
				t.Fatalf("unbound committed evidence outcome = %+v", outcome)
			}
		})
	}
}

func TestCommittedDeliveryEvidenceBindsReplyToResolvedRecipient(t *testing.T) {
	root := t.TempDir()
	evidence := committedDeliveryEvidence{
		MessageID: "reply-committed",
		FinalPath: filepath.Join(root, "agents", "original-sender", "inbox", "new", "reply-committed.md"),
	}
	req := amqCommandRequest{Arg: []string{"reply", "--root", root, "--me", "qa", "--id", "original-message", "--body", "done"}}
	if !committedDeliveryEvidenceMatchesRequest(ownedAMQSendOptions{ReplyRecipient: "original-sender"}, req, evidence) {
		t.Fatal("reply evidence did not bind to the resolved original sender")
	}
	if committedDeliveryEvidenceMatchesRequest(ownedAMQSendOptions{ReplyRecipient: "thread-derived-guess"}, req, evidence) {
		t.Fatal("reply evidence bound to a recipient other than the resolved original sender")
	}
}

func committedDeliveryTestLine(id, path string) string {
	return "message " + id + " has a committed delivery; retrying may duplicate it: delivery committed at " + path + ", but durability is indeterminate: injected sync failure; do not retry blindly\n"
}
