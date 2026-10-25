package cli

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// termSafe is the one escaping of an untrusted field printed to a human
// (phase 5 re-pass of 4-3, P2-2): a subject, a body, a handle or a reason
// that came from a seat, a lead or a squad's files. Every C0 control (the
// escape byte, newline and tab included), DEL, every C1 control (U+0080 to
// U+009F) and every byte that is not valid UTF-8 (a raw 0x9b is an 8-bit
// CSI on some terminals) is shown as a visible \xNN or \u00NN escape, so a
// field can neither clear nor repaint the owner's terminal nor forge a line.
func termSafe(s string) string {
	clean := true
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 || c == 0x7f || c >= 0x80 {
			clean = false
			break
		}
	}
	if clean {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size <= 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r >= 0x80 && r <= 0x9f:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	return b.String()
}
