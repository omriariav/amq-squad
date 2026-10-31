package seatlaunch

import (
	"strings"
)

// Failed reports whether the launch did not bring every seat up.
func (r Result) Failed() bool {
	if r.Stage == "apply_failed" || r.Stage == "prepare_refused" {
		return true
	}
	for _, s := range r.Seats {
		if !s.Up {
			return true
		}
	}
	return false
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}
