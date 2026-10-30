package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

// ownedAMQSendOptions contains the transport controls for one owned send.
type ownedAMQSendOptions struct {
	WaitPosture waitPostureRequest
}

type ownedAMQSendResult struct {
	MessageID string
	Invoked   bool
}

// runOwnedAMQSend is the single amq-squad-owned send boundary: the
// wait-posture guard, one AMQ invocation, and AMQ's stable message id
// (recovered from a committed-delivery report when the send itself failed
// after the commit). It writes no local state. The v2 durable-invocation
// boundary of the release answer path is gone: 3.0 release answers are typed
// gates the core verifies (3-1), and the release flow (3-2) raises gates, not
// mail.
func runOwnedAMQSend(opts ownedAMQSendOptions, req amqCommandRequest) ([]byte, ownedAMQSendResult, error) {
	if err := guardOwnedWait(opts.WaitPosture); err != nil {
		return nil, ownedAMQSendResult{}, err
	}
	out, sendErr := runAMQCommand(req)
	messageID := parseSentMessageID(string(out))
	if evidence, ok := parseCommittedDeliveryEvidence(string(out), sendErr); ok && committedDeliveryEvidenceMatchesRequest(req, evidence) {
		messageID = evidence.MessageID
	}
	if sendErr == nil && messageID == "" {
		sendErr = fmt.Errorf("AMQ exited successfully without a parseable stable message id")
	}
	return out, ownedAMQSendResult{MessageID: messageID, Invoked: true}, sendErr
}

// parseSentMessageID reads AMQ's id from its JSON output, or from a
// "Sent <id>" / "Replied <id>" line.
func parseSentMessageID(out string) string {
	var native struct {
		ID string `json:"id"`
	}
	if i := strings.IndexByte(out, '{'); i >= 0 && json.NewDecoder(strings.NewReader(out[i:])).Decode(&native) == nil && strings.TrimSpace(native.ID) != "" {
		return strings.TrimSpace(native.ID)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		rest, ok := strings.CutPrefix(line, "Sent ")
		if !ok {
			rest, ok = strings.CutPrefix(line, "Replied ")
		}
		if fields := strings.Fields(rest); ok && len(fields) > 0 {
			return fields[0]
		}
	}
	return ""
}

type committedDeliveryEvidence struct {
	MessageID string
	FinalPath string
}

// parseCommittedDeliveryEvidence reads AMQ's "message <id> has a committed
// delivery; ... committed at <path>, but durability is indeterminate:" line
// from a failed send.
func parseCommittedDeliveryEvidence(out string, sendErr error) (committedDeliveryEvidence, bool) {
	if sendErr == nil {
		return committedDeliveryEvidence{}, false
	}
	const idSuffix, pathPrefix, pathSuffix = " has a committed delivery;", " committed at ", ", but durability is indeterminate:"
	for _, line := range strings.Split(out+"\n"+sendErr.Error(), "\n") {
		before, after, ok := strings.Cut(line, idSuffix)
		idStart := strings.LastIndex(before, "message ")
		_, rest, okPath := strings.Cut(after, pathPrefix)
		finalPath, _, okEnd := strings.Cut(rest, pathSuffix)
		if !ok || idStart < 0 || !okPath || !okEnd {
			continue
		}
		id, finalPath := strings.TrimSpace(before[idStart+len("message "):]), strings.TrimSpace(finalPath)
		if safeCommittedName(id) && filepath.IsAbs(finalPath) && filepath.Clean(finalPath) == finalPath {
			return committedDeliveryEvidence{MessageID: id, FinalPath: finalPath}, true
		}
	}
	return committedDeliveryEvidence{}, false
}

// safeCommittedName is one path segment of [A-Za-z0-9._-], not starting with
// a dot and holding no "..".
func safeCommittedName(id string) bool {
	if id == "" || strings.HasPrefix(id, ".") || strings.Contains(id, "..") {
		return false
	}
	return strings.Trim(id, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-") == ""
}

// committedDeliveryEvidenceMatchesRequest binds a committed-delivery report to
// this send: a send (not a reply) to one recipient under a clean absolute
// root, at that recipient's inbox path, and not echoed by the body itself.
func committedDeliveryEvidenceMatchesRequest(req amqCommandRequest, evidence committedDeliveryEvidence) bool {
	if len(req.Arg) == 0 || req.Arg[0] != "send" || !safeCommittedName(evidence.MessageID) {
		return false
	}
	body := amqFlagValue(req.Arg, "body")
	switch {
	case body == "" && req.Stdin != nil, body == "-" || body == "@-" || strings.HasPrefix(body, "@"):
		return false // the transport owns these body sources: fail closed
	case strings.Contains(body, "message "+evidence.MessageID+" has a committed delivery;") && strings.Contains(body, " committed at "+evidence.FinalPath):
		return false
	}
	root, recipient := amqFlagValue(req.Arg, "root"), amqFlagValue(req.Arg, "to")
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root || !safeCommittedName(recipient) {
		return false
	}
	return evidence.FinalPath == filepath.Join(root, "agents", recipient, "inbox", "new", evidence.MessageID+".md")
}
