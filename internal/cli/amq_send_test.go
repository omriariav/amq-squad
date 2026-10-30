package cli

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
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
	if calls != 1 || string(out) != "Sent msg-accepted to qa\n" || result.MessageID != "msg-accepted" || !result.Invoked {
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
	if !errors.Is(err, sendErr) || result.MessageID != "msg-timeout" || !result.Invoked {
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
		})
	}
}

func committedDeliveryTestLine(id, path string) string {
	return "message " + id + " has a committed delivery; retrying may duplicate it: delivery committed at " + path + ", but durability is indeterminate: injected sync failure; do not retry blindly\n"
}
