package cli

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #589: `amq-squad amq send` wrote state=ambiguous_unknown with an empty
// message_id for a send AMQ had refused fail-closed. A send that definitively
// never left must produce a definite failed receipt; ambiguous_unknown is
// reserved for genuinely unknown outcomes, and using it for a provable
// non-delivery makes the receipt trail misleading during outage forensics —
// exactly when it is being read most carefully.
//
// These tests are deliberately BOTH WAYS. A change that classified everything
// as failed would satisfy the first half and would be far more dangerous than
// the defect it replaced.

// exitCodeErr builds an *exec.ExitError carrying a real exit status, so the
// classifier is exercised through the same type production sees rather than
// through a stub that merely reports a number.
func exitCodeErr(t *testing.T, code int) error {
	t.Helper()
	err := exec.Command("sh", "-c", fmt.Sprintf("exit %d", code)).Run()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("building exit-%d error: got %v", code, err)
	}
	if exitErr.ExitCode() != code {
		t.Fatalf("exit code = %d, want %d", exitErr.ExitCode(), code)
	}
	return err
}

func TestFailClosedSendProducesDefiniteFailedReceipt(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
	}{
		// Observed against the pinned AMQ 0.51.1:
		//   5 -> "refusing send: root is not the pinned base root..."
		//   2 -> "--to is required", unknown command, bad flag
		{"explicit refusal", amqExitRefused},
		{"usage rejection", amqExitUsageRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := &deliveryReceiptData{
				AttemptID: "attempt-589",
				Consumers: []deliveryConsumerState{{Consumer: "cto", State: deliveryStateAmbiguousUnknown}},
			}
			sendErr := exitCodeErr(t, tc.code)
			markDeliverySendResult(receipt, []byte("refusing send: root is not the pinned base root\n"), sendErr)

			// The COMPLETE projection is asserted, not just the headline state.
			// A half-updated receipt — definite aggregate state with consumers
			// still reading ambiguous, or a missing evidence source — is its own
			// misleading artifact, which is the defect class #589 is about.
			if receipt.DeliveryState != deliveryStateFailed {
				t.Fatalf("DeliveryState = %q, want %q — a refused send has a definite outcome", receipt.DeliveryState, deliveryStateFailed)
			}
			if receipt.Status != deliveryStateFailed {
				t.Errorf("Status = %q, want %q", receipt.Status, deliveryStateFailed)
			}
			if receipt.EvidenceSource != "amq_refused_send" {
				t.Errorf("EvidenceSource = %q, want %q", receipt.EvidenceSource, "amq_refused_send")
			}
			if receipt.Detail != sendErr.Error() {
				t.Errorf("Detail = %q, want the send error %q", receipt.Detail, sendErr.Error())
			}
			if receipt.FailedAt == nil {
				t.Fatal("FailedAt is nil; a definite failure must carry its timestamp")
			}
			if receipt.MessageID != "" {
				t.Errorf("MessageID = %q, want empty", receipt.MessageID)
			}
			for _, consumer := range receipt.Consumers {
				if consumer.State != deliveryStateFailed {
					t.Errorf("consumer %s state = %q, want %q", consumer.Consumer, consumer.State, deliveryStateFailed)
				}
				if consumer.Stage != deliveryStateFailed {
					t.Errorf("consumer %s stage = %q, want %q", consumer.Consumer, consumer.Stage, deliveryStateFailed)
				}
				if consumer.FailedAt == nil {
					t.Fatalf("consumer %s FailedAt is nil", consumer.Consumer)
				}
				// Aggregate and per-consumer timestamps must be the SAME instant,
				// so a reader cannot infer an ordering that never happened.
				if !consumer.FailedAt.Equal(*receipt.FailedAt) {
					t.Errorf("consumer %s FailedAt = %s, want the aggregate %s", consumer.Consumer, consumer.FailedAt, receipt.FailedAt)
				}
			}
		})
	}
}

// TestGenuinelyAmbiguousSendStaysAmbiguous is the half that protects against
// over-claiming. An outcome that is actually unknown must remain unknown: a
// receipt asserting definite failure invites a retry that could duplicate a
// message which did land.
func TestGenuinelyAmbiguousSendStaysAmbiguous(t *testing.T) {
	for _, tc := range []struct {
		name string
		out  string
		err  error
	}{
		{"timeout", "", errors.New("signal: killed")},
		{"unclassified nonzero exit", "", exitCodeErr(t, 1)},
		{"crash mid-commit", "partial output", exitCodeErr(t, 137)},
		{"plain wrapped failure", "", errors.New("amq send: connection reset")},
		// Refusal WORDING without the typed error stays ambiguous by design.
		{"refusal wording without typed error", "refusing send: bad root", errors.New("amq send: refusing send: bad root")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			receipt := &deliveryReceiptData{
				AttemptID: "attempt-589",
				Consumers: []deliveryConsumerState{{Consumer: "cto", State: deliveryStateAmbiguousUnknown}},
			}
			markDeliverySendResult(receipt, []byte(tc.out), tc.err)
			if receipt.DeliveryState != deliveryStateAmbiguousUnknown {
				t.Fatalf("DeliveryState = %q, want %q — this outcome is genuinely unknown and must not be reported as definite", receipt.DeliveryState, deliveryStateAmbiguousUnknown)
			}
			if receipt.FailedAt != nil {
				t.Error("FailedAt was set for an ambiguous outcome")
			}
		})
	}
}

// TestFailClosedClassifierRequiresPositiveEvidence pins the predicate directly.
// Only a typed *exec.ExitError may downgrade an unknown outcome to a definite
// failure: refusal WORDING is not enough, because this predicate narrows
// caution and a false definite failure is the duplicate-send hazard.
func TestFailClosedClassifierRequiresPositiveEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"refusal exit code", exitCodeErr(t, amqExitRefused), true},
		{"usage exit code", exitCodeErr(t, amqExitUsageRejected), true},
		// Typed evidence only. These carry AMQ's refusal wording but lost the
		// *exec.ExitError, so they must NOT downgrade to definite: a false
		// definite failure is the duplicate-send hazard.
		{"refusal wording in a wrapped error", errors.New("amq send: refusing send: root is not pinned"), false},
		{"plain exit-status text", errors.New("exit status 5"), false},
		{"generic failure wording", errors.New("send failed: could not reach the queue"), false},
		{"unrelated nonzero exit", exitCodeErr(t, 1), false},
		{"signal kill", exitCodeErr(t, 137), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := amqRefusedSendFailClosed(tc.err); got != tc.want {
				t.Errorf("amqRefusedSendFailClosed(%v) = %t, want %t", tc.err, got, tc.want)
			}
		})
	}
}

// TestFailClosedReceiptStageExplainsTheDifference: the stage text is what an
// operator reads during an outage. It must say non-delivery is definite and
// point at the refusal cause, not tell them to go confirm delivery.
func TestFailClosedReceiptStageExplainsTheDifference(t *testing.T) {
	receipt := &deliveryReceiptData{AttemptID: "attempt-589"}
	markDeliverySendResult(receipt, nil, exitCodeErr(t, amqExitRefused))
	if len(receipt.Stages) == 0 {
		t.Fatal("no stage recorded for a fail-closed send")
	}
	last := receipt.Stages[len(receipt.Stages)-1]
	if !strings.Contains(last.Detail, "definite") {
		t.Errorf("stage detail does not tell the operator the outcome is definite: %q", last.Detail)
	}
	if strings.Contains(last.Detail, "confirm non-delivery before retry") {
		t.Errorf("fail-closed stage still carries the ambiguous-outcome instruction: %q", last.Detail)
	}
}

// TestSuccessfulSendUnaffected guards the path that must not move at all.
func TestSuccessfulSendUnaffected(t *testing.T) {
	receipt := &deliveryReceiptData{
		AttemptID: "attempt-589",
		Consumers: []deliveryConsumerState{{Consumer: "cto"}},
	}
	markDeliverySendResult(receipt, []byte("Sent 2026-08-02T00-00-00.000Z_pid1_abcdef to cto\n"), nil)
	if receipt.DeliveryState != deliveryStateDeliveredNotDrained {
		t.Fatalf("DeliveryState = %q, want %q", receipt.DeliveryState, deliveryStateDeliveredNotDrained)
	}
	if receipt.MessageID == "" {
		t.Error("MessageID was not parsed from a successful send")
	}
}

// TestPositiveEvidenceOutranksRefusalExit is the precedence guard. The
// classifier lives behind committed-evidence validation and message-id parsing,
// and it must STAY there: if a future AMQ path ever returned 2 or 5 after
// exposing a stable id or a committed path, reordering the classifier ahead of
// that evidence would report a delivered message as definitely failed. That is
// the duplicate-send hazard arriving from the opposite direction.
//
// TestSuccessfulSendUnaffected cannot catch this — it passes a nil error, so a
// reordering would keep it green.
func TestPositiveEvidenceOutranksRefusalExit(t *testing.T) {
	for _, code := range []int{amqExitRefused, amqExitUsageRejected} {
		t.Run(fmt.Sprintf("parseable id survives exit %d", code), func(t *testing.T) {
			receipt := &deliveryReceiptData{
				AttemptID: "attempt-589",
				Consumers: []deliveryConsumerState{{Consumer: "cto"}},
			}
			markDeliverySendResult(receipt,
				[]byte("Sent 2026-08-02T00-00-00.000Z_pid1_abcdef to cto\n"),
				exitCodeErr(t, code))

			if receipt.MessageID != "2026-08-02T00-00-00.000Z_pid1_abcdef" {
				t.Fatalf("MessageID = %q; a parsed id must not be discarded because of the exit code", receipt.MessageID)
			}
			if receipt.DeliveryState != deliveryStateDeliveredNotDrained {
				t.Fatalf("DeliveryState = %q, want %q — a message with a stable id was NOT refused", receipt.DeliveryState, deliveryStateDeliveredNotDrained)
			}
			if receipt.FailedAt != nil {
				t.Error("FailedAt was set for a send that produced a stable message id")
			}
		})
	}
}

// TestCommittedEvidenceOutranksRefusalExit is the same precedence guard for the
// committed-delivery path, which sits even earlier. A message whose commit AMQ
// has already reported must never be downgraded to a definite failure.
func TestCommittedEvidenceOutranksRefusalExit(t *testing.T) {
	for _, code := range []int{amqExitRefused, amqExitUsageRejected} {
		t.Run(fmt.Sprintf("committed evidence survives exit %d", code), func(t *testing.T) {
			const msgID = "2026-08-02T00-00-00.000Z_pid1_abcdef"
			root := t.TempDir()
			receipt := &deliveryReceiptData{
				AttemptID:  "attempt-589",
				Root:       root,
				Recipient:  "cto",
				Recipients: []string{"cto"},
				Target:     deliveryReceiptTarget{Handle: "cto"},
				Consumers:  []deliveryConsumerState{{Consumer: "cto"}},
			}
			finalPath := filepath.Join(root, "agents", "cto", "inbox", "new", msgID+".md")
			committed := fmt.Sprintf("message %s has a committed delivery; committed at %s, but durability is indeterminate: fsync failed", msgID, finalPath)

			// Wrap a REAL *exec.ExitError so the refusal signal and the committed
			// signal are in genuine conflict. Without a typed exit in the chain the
			// test would prove nothing about precedence.
			sendErr := fmt.Errorf("%s: %w", committed, exitCodeErr(t, code))
			markDeliverySendResult(receipt, nil, sendErr)

			if receipt.DeliveryState != deliveryStateCommittedIndeterminate {
				t.Fatalf("DeliveryState = %q, want %q — committed evidence must outrank the exit code", receipt.DeliveryState, deliveryStateCommittedIndeterminate)
			}
			if receipt.MessageID != msgID {
				t.Errorf("MessageID = %q, want %q", receipt.MessageID, msgID)
			}
			if receipt.FailedAt != nil {
				t.Error("FailedAt was set for a committed delivery")
			}
		})
	}
}

// TestOwnedDurableSendPersistsDefiniteFailure closes the gap the peer review
// named: every test above calls markDeliverySendResult directly, so all of them
// could pass while the durable boundary or persistence wiring regressed. This
// one drives runOwnedDurableSend — the single amq-squad-owned send boundary —
// with a production-shaped wrapped *exec.ExitError, then reads the receipt back
// FROM DISK.
func TestOwnedDurableSendPersistsDefiniteFailure(t *testing.T) {
	project := t.TempDir()
	root := filepath.Join(project, ".agent-mail", "v2-27-0")

	previousRun := runAMQCommand
	t.Cleanup(func() { runAMQCommand = previousRun })
	runAMQCommand = func(amqCommandRequest) ([]byte, error) {
		// Shaped like the real failure: AMQ's refusal text on the output, and a
		// wrapped *exec.ExitError carrying the refusal code.
		return []byte("refusing send: root is not the pinned base root\n"),
			fmt.Errorf("amq send: %w", exitCodeErr(t, amqExitRefused))
	}

	_, receipt, err := runOwnedDurableSend(
		durableSendOptions{ProjectDir: project, Profile: "squad", Session: "v2-27-0", Kind: "amq_send"},
		amqCommandRequest{Dir: project, Arg: []string{
			"send", "--root", root, "--me", "amq-dev-1", "--to", "cto",
			"--thread", "p2p/amq-dev-1__cto", "--kind", "status", "--subject", "s", "--body", "b",
		}},
	)
	if err == nil {
		t.Fatal("runOwnedDurableSend returned nil error for a refused send")
	}
	if receipt == nil {
		t.Fatal("runOwnedDurableSend returned no receipt")
	}
	if !receipt.AMQInvoked {
		t.Error("AMQInvoked = false; AMQ was invoked and refused, which is different from never invoking it")
	}
	if receipt.DeliveryState != deliveryStateFailed {
		t.Fatalf("in-memory DeliveryState = %q, want %q", receipt.DeliveryState, deliveryStateFailed)
	}

	// The persisted artifact is what an operator reads during forensics, and is
	// the thing #589 was actually about.
	path := filepath.Join(deliveryReceiptDir(project, "squad", "v2-27-0"), receipt.AttemptID+".json")
	persisted, readErr := readDeliveryReceipt(path)
	if readErr != nil {
		t.Fatalf("reading persisted receipt at %s: %v", path, readErr)
	}
	if persisted.DeliveryState != deliveryStateFailed {
		t.Fatalf("persisted DeliveryState = %q, want %q", persisted.DeliveryState, deliveryStateFailed)
	}
	if persisted.Status != deliveryStateFailed {
		t.Errorf("persisted Status = %q, want %q", persisted.Status, deliveryStateFailed)
	}
	if persisted.EvidenceSource != "amq_refused_send" {
		t.Errorf("persisted EvidenceSource = %q, want %q", persisted.EvidenceSource, "amq_refused_send")
	}
	if persisted.MessageID != "" {
		t.Errorf("persisted MessageID = %q, want empty", persisted.MessageID)
	}
	if persisted.FailedAt == nil {
		t.Fatal("persisted FailedAt is nil")
	}
	if !persisted.AMQInvoked {
		t.Error("persisted AMQInvoked = false")
	}
	for _, consumer := range persisted.Consumers {
		if consumer.State != deliveryStateFailed {
			t.Errorf("persisted consumer %s state = %q, want %q", consumer.Consumer, consumer.State, deliveryStateFailed)
		}
		if consumer.FailedAt == nil {
			t.Errorf("persisted consumer %s FailedAt is nil", consumer.Consumer)
		}
	}
}
