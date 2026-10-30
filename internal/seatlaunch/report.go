package seatlaunch

import (
	"fmt"
	"io"
	"strings"
)

// Report prints the launch for the operator. R48: the AMQ ApplyResult
// outcome, reason code and failure detail are always shown; a count of
// applied commands is never the only signal. R50: next actions name only
// flags and commands that work on this path.
func Report(w io.Writer, r Result) {
	if p := r.Prepare; p != nil {
		fmt.Fprintf(w, "prepare: outcome %s", p.Outcome)
		if p.Reason != "" {
			fmt.Fprintf(w, ", reason %s", p.Reason)
		}
		fmt.Fprintf(w, " (subject %s, trust %s)\n", p.SubjectDigest, p.TrustDigest)
	}
	if a := r.Apply; a != nil {
		fmt.Fprintf(w, "apply: outcome %s, reason_code %s, disposition %s, backend %s\n", a.Outcome, orNone(a.ReasonCode), orNone(string(a.Disposition)), orNone(a.Backend))
		fmt.Fprintf(w, "apply: failure_detail %s\n", orNone(oneLine(a.FailureDetail)))
		for _, o := range a.Observations {
			if o.Runnable {
				fmt.Fprintf(w, "  seat %s: conversation %s, execution %s, resource %s%s\n", o.Handle, o.Conversation, o.Execution, o.Resource, suffix(o.ReasonCode))
			}
		}
		for _, f := range a.FollowUps {
			fmt.Fprintf(w, "  follow-up: %s (%s) for %s\n", f.Kind, f.ReasonCode, strings.Join(f.Handles, ","))
		}
	} else if r.FailureDetail != "" {
		fmt.Fprintf(w, "amq: %s\n", oneLine(r.FailureDetail))
	}
	for _, a := range r.Pending {
		fmt.Fprintf(w, "pending decision: %s %s (%s) for %s\n", a.ActionID, a.Kind, a.ReasonCode, strings.Join(a.Handles, ","))
	}
	for _, s := range r.Seats {
		fmt.Fprintf(w, "seat %s: %s%s%s\n", s.Handle, s.State, suffix(s.ReasonCode), suffix(s.Detail))
		if wk := s.Wake; wk != nil {
			fmt.Fprintf(w, "  seat %s wake: mode %s, require_wake %t, injector %s (AMQ prepare)\n", s.Handle, wk.Mode, wk.RequireWake, orNone(wk.Injector))
		}
	}
	for _, n := range r.NextActions {
		fmt.Fprintf(w, "next: %s\n", n)
	}
}

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

func suffix(s string) string {
	if s == "" {
		return ""
	}
	return " (" + oneLine(s) + ")"
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
