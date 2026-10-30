package seatlaunch

import (
	"strings"

	"github.com/omriariav/amq-squad/v3/internal/redact"
)

// amqDetail is the one chokepoint through which AMQ's output becomes a
// reason or a detail (3.0.1 t17, Codex finding 1 and the reviewer's shape):
// it trims the raw text and redacts it as one block before anything
// prefixes, wraps or flattens it. redact.Block finds a YAML key that owns
// the quoted value on the next line only at a line's start, so a message
// prefixed to AMQ's stderr, or oneLine's flattening, would bury the key and
// keep the value.
//
// Every carrier goes through it: prepareCLI's and applyCLI's stderr (their
// error wrapping and the detail they return), every write and read of a
// FailureDetail (the Result's, the ApplyResult's, the staged record's
// apply), the launch and roster failures handed to the scheduler, and the
// operator's report. TestAMQOutputHasOneChokepoint fails when a new caller
// reads RunAMQ's stderr or a FailureDetail around it. It is idempotent: an
// already redacted text comes back the same.
func amqDetail(raw string) string { return redact.Block(strings.TrimSpace(raw)) }
