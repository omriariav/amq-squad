package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"
)

var deliveryAttemptSequence atomic.Uint64

type durableInvocationDisposition string

const (
	durableInvocationInvoked            durableInvocationDisposition = "invoked"
	durableInvocationReconciledExisting durableInvocationDisposition = "reconciled_existing"
)

type durableInvocationResult struct {
	disposition         durableInvocationDisposition
	reconciledMessageID string
}

func newDurableInvokedResult() durableInvocationResult {
	return durableInvocationResult{disposition: durableInvocationInvoked}
}

func newDurableReconciledExistingResult(messageID string) (durableInvocationResult, error) {
	result := durableInvocationResult{disposition: durableInvocationReconciledExisting, reconciledMessageID: messageID}
	if err := result.validate(); err != nil {
		return durableInvocationResult{}, err
	}
	return result, nil
}

func (r durableInvocationResult) Disposition() durableInvocationDisposition { return r.disposition }
func (r durableInvocationResult) ReconciledMessageID() string               { return r.reconciledMessageID }

func (r durableInvocationResult) validate() error {
	switch r.disposition {
	case durableInvocationInvoked:
		if r.reconciledMessageID != "" {
			return fmt.Errorf("durable invocation result cannot bind a reconciled message id when invoked")
		}
		return nil
	case durableInvocationReconciledExisting:
		if strings.TrimSpace(r.reconciledMessageID) == "" || strings.TrimSpace(r.reconciledMessageID) != r.reconciledMessageID || strings.ContainsAny(r.reconciledMessageID, "\r\n\x00") {
			return fmt.Errorf("durable invocation reconciled message id is required and must be canonical")
		}
		return nil
	default:
		return fmt.Errorf("durable invocation result disposition %q is invalid", r.disposition)
	}
}

type durableInvocationBoundary struct {
	run func(func() error) (durableInvocationResult, error)
}

func newDurableInvocationBoundary(run func(func() error) (durableInvocationResult, error)) (durableInvocationBoundary, error) {
	if run == nil {
		return durableInvocationBoundary{}, fmt.Errorf("durable invocation boundary callback is required")
	}
	return durableInvocationBoundary{run: run}, nil
}

func (b durableInvocationBoundary) Run(invoke func() error) (durableInvocationResult, error) {
	if b.run == nil || invoke == nil {
		return durableInvocationResult{}, fmt.Errorf("durable invocation boundary and callback are required")
	}
	return b.run(invoke)
}

func directDurableInvocationBoundary() durableInvocationBoundary {
	boundary, _ := newDurableInvocationBoundary(func(invoke func() error) (durableInvocationResult, error) {
		if err := invoke(); err != nil {
			return durableInvocationResult{}, err
		}
		return newDurableInvokedResult(), nil
	})
	return boundary
}

// ownedAMQSendOptions contains transport controls plus the recipient resolved
// for an AMQ reply. Sends carry their recipient in --to; replies do not, so the
// caller must supply the sender resolved from the original message.
type ownedAMQSendOptions struct {
	Invocation     durableInvocationBoundary
	WaitPosture    waitPostureRequest
	ReplyRecipient string
}

type ownedAMQSendResult struct {
	MessageID  string
	Invoked    bool
	Reconciled bool
}

// runOwnedAMQSend is the single amq-squad-owned send boundary. It preserves
// the release replay guard and wait-posture guard, captures AMQ output on
// failure, and returns AMQ's stable message id without writing local state.
func runOwnedAMQSend(opts ownedAMQSendOptions, req amqCommandRequest) ([]byte, ownedAMQSendResult, error) {
	boundary := opts.Invocation
	if boundary.run == nil {
		boundary = directDurableInvocationBoundary()
	}

	callbackCount := 0
	invoked := false
	var out []byte
	var sendErr error
	var boundaryPanic any
	boundaryResult, boundaryErr := func() (result durableInvocationResult, err error) {
		defer func() { boundaryPanic = recover() }()
		return boundary.Run(func() error {
			callbackCount++
			if callbackCount != 1 {
				return fmt.Errorf("durable invocation callback is single-use")
			}
			if err := guardOwnedWait(opts.WaitPosture); err != nil {
				return err
			}
			invoked = true
			out, sendErr = runAMQCommand(req)
			return nil
		})
	}()
	if boundaryPanic != nil {
		panic(boundaryPanic)
	}

	if boundaryResult != (durableInvocationResult{}) {
		if err := boundaryResult.validate(); err != nil {
			return out, ownedAMQSendResult{Invoked: invoked}, errors.Join(boundaryErr, err, sendErr)
		}
	}
	switch boundaryResult.Disposition() {
	case durableInvocationReconciledExisting:
		if callbackCount != 0 {
			return out, ownedAMQSendResult{Invoked: invoked}, errors.Join(boundaryErr, fmt.Errorf("reconciled invocation unexpectedly called AMQ"), sendErr)
		}
		return out, ownedAMQSendResult{MessageID: boundaryResult.ReconciledMessageID(), Reconciled: true}, boundaryErr
	case durableInvocationInvoked:
		if callbackCount != 1 || !invoked {
			return out, ownedAMQSendResult{Invoked: invoked}, errors.Join(boundaryErr, fmt.Errorf("invoked result requires exactly one AMQ transport invocation"), sendErr)
		}
	case "":
		if callbackCount == 0 {
			return out, ownedAMQSendResult{}, errors.Join(boundaryErr, fmt.Errorf("durable invocation boundary ended before AMQ invocation"), sendErr)
		}
		if !invoked {
			if combined := errors.Join(boundaryErr, sendErr); combined != nil {
				return out, ownedAMQSendResult{}, combined
			}
			return out, ownedAMQSendResult{}, fmt.Errorf("durable invocation callback ended before AMQ transport invocation")
		}
	default:
		return out, ownedAMQSendResult{Invoked: invoked}, errors.Join(boundaryErr, fmt.Errorf("unsupported durable invocation disposition %q", boundaryResult.Disposition()), sendErr)
	}

	messageID := parseSentMessageID(string(out))
	if evidence, ok := parseCommittedDeliveryEvidence(string(out), sendErr); ok && committedDeliveryEvidenceMatchesRequest(opts, req, evidence) {
		messageID = strings.TrimSpace(evidence.MessageID)
	}
	if sendErr == nil && messageID == "" {
		sendErr = fmt.Errorf("AMQ exited successfully without a parseable stable message id")
	}
	return out, ownedAMQSendResult{MessageID: messageID, Invoked: invoked}, errors.Join(boundaryErr, sendErr)
}

func deliveryAttemptID(now time.Time, kind, role, handle string) string {
	seed := sanitizeWorkstreamName(strings.Join([]string{kind, role, handle}, "-"))
	if seed == "" {
		seed = "delivery"
	}
	return fmt.Sprintf("%s-%s-p%d-%016x", now.Format("20060102T150405.000000000Z"), seed, os.Getpid(), deliveryAttemptSequence.Add(1))
}

func parseSentMessageID(out string) string {
	var native struct {
		ID string `json:"id"`
	}
	if payload := firstJSONObject([]byte(out)); len(payload) > 0 && json.Unmarshal(payload, &native) == nil && strings.TrimSpace(native.ID) != "" {
		return strings.TrimSpace(native.ID)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "Sent ")
		if !ok {
			rest, ok = strings.CutPrefix(line, "Replied ")
		}
		if ok {
			if fields := strings.Fields(rest); len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}

type committedDeliveryEvidence struct {
	MessageID string
	FinalPath string
}

func parseCommittedDeliveryEvidence(out string, sendErr error) (committedDeliveryEvidence, bool) {
	if sendErr == nil {
		return committedDeliveryEvidence{}, false
	}
	text := out + "\n" + sendErr.Error()
	const idSuffix = " has a committed delivery;"
	const pathPrefix = " committed at "
	const pathSuffix = ", but durability is indeterminate:"
	for _, line := range strings.Split(text, "\n") {
		idEnd := strings.Index(line, idSuffix)
		if idEnd < 0 {
			continue
		}
		idStart := strings.LastIndex(line[:idEnd], "message ")
		if idStart < 0 {
			continue
		}
		messageID := strings.TrimSpace(line[idStart+len("message ") : idEnd])
		pathStart := strings.Index(line[idEnd+len(idSuffix):], pathPrefix)
		if !safeCommittedMessageID(messageID) || pathStart < 0 {
			continue
		}
		pathStart += idEnd + len(idSuffix) + len(pathPrefix)
		pathEnd := strings.Index(line[pathStart:], pathSuffix)
		if pathEnd < 0 {
			continue
		}
		finalPath := strings.TrimSpace(line[pathStart : pathStart+pathEnd])
		if !filepath.IsAbs(finalPath) || filepath.Clean(finalPath) != finalPath {
			continue
		}
		return committedDeliveryEvidence{MessageID: messageID, FinalPath: finalPath}, true
	}
	return committedDeliveryEvidence{}, false
}

func safeCommittedMessageID(id string) bool {
	if id == "" || id == "." || id == ".." || strings.HasPrefix(id, ".") || strings.Contains(id, "..") || filepath.Base(id) != id {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

func committedDeliveryEvidenceMatchesRequest(opts ownedAMQSendOptions, req amqCommandRequest, evidence committedDeliveryEvidence) bool {
	if len(req.Arg) == 0 || !safeCommittedMessageID(evidence.MessageID) {
		return false
	}
	if committedDeliveryEvidenceEchoedByRequestBody(req, evidence) {
		return false
	}
	root := amqFlagValue(req.Arg, "root")
	if root == "" || strings.TrimSpace(root) != root || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return false
	}

	var recipient string
	switch req.Arg[0] {
	case "send":
		recipient = amqFlagValue(req.Arg, "to")
	case "reply":
		recipient = opts.ReplyRecipient
	default:
		return false
	}
	if !safeCommittedRecipient(recipient) {
		return false
	}

	expected := filepath.Join(root, "agents", recipient, "inbox", "new", evidence.MessageID+".md")
	return evidence.FinalPath == expected && filepath.Clean(evidence.FinalPath) == expected
}

func safeCommittedRecipient(recipient string) bool {
	return recipient != "" && strings.TrimSpace(recipient) == recipient && recipient != "." && recipient != ".." && filepath.Base(recipient) == recipient
}

func committedDeliveryEvidenceEchoedByRequestBody(req amqCommandRequest, evidence committedDeliveryEvidence) bool {
	body := amqFlagValue(req.Arg, "body")
	if body == "" {
		return req.Stdin != nil
	}
	if body == "-" || body == "@-" || strings.HasPrefix(body, "@") {
		// The transport owns these body sources. Without independently trusted
		// provenance for an error line, fail closed instead of treating body
		// content as proof that delivery committed.
		return true
	}
	return strings.Contains(body, "message "+evidence.MessageID+" has a committed delivery;") &&
		strings.Contains(body, " committed at "+evidence.FinalPath+", but durability is indeterminate:")
}

func firstJSONObject(data []byte) []byte {
	start := bytes.IndexByte(data, '{')
	if start < 0 {
		return nil
	}
	depth, inString, escaped := 0, false, false
	for i := start; i < len(data); i++ {
		c := data[i]
		if inString {
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		switch c {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return data[start : i+1]
			}
		}
	}
	return nil
}
