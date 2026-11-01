package piruntime

import "strings"

// The refusal names of the pi runtime (brief 31-1, `amq-squad up`'s
// install step): a build without the payload, and an unpacked runtime that
// does not re-hash against the build's manifest.
const (
	ReasonNotEmbedded = "pi_not_embedded"
	ReasonRejected    = "pi_runtime_rejected"
)

// rejectedMarker is the fixed part of a *Reject's message.
const rejectedMarker = " rejected (run `amq-squad doctor --fix`"

// RefusalCodeText names ReasonNotEmbedded (ErrNotEmbedded) or
// ReasonRejected (a *Reject) from the text another amq-squad binary printed
// (`amq-squad pi verify --json` run as a child, whose error reaches the
// caller as text only); "" for anything else (an absent runtime is not a
// refusal: the first launch unpacks it).
func RefusalCodeText(text string) string {
	switch {
	case strings.Contains(text, ErrNotEmbedded.Error()):
		return ReasonNotEmbedded
	case strings.Contains(text, "pi runtime ") && strings.Contains(text, rejectedMarker):
		return ReasonRejected
	}
	return ""
}
