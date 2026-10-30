package registry

import (
	"slices"
	"strings"
	"testing"
)

// 3.0.1 t6 (the reviewer's ruling): the builder writes the Claude seat
// environment fact on every Claude entry and on no other, the file format
// round-trips it, and an entry without one writes no seat_env line.
func TestBuilderEmitsTheClaudeSeatEnvironment(t *testing.T) {
	d := Detection{Tools: []Tool{{Name: "claude", Found: true, Login: "logged_in"}, {Name: "codex", Found: true, Login: "logged_in"}}}
	r := Build(d, Registry{}, BuildOptions{})
	claude, other := 0, 0
	for _, e := range r.Entries {
		switch {
		case e.Runtime == "claude":
			claude++
			if !slices.Equal(e.SeatEnv, ClaudeSeatEnv) {
				t.Fatalf("%s: seat env %v, want %v", e.ID(), e.SeatEnv, ClaudeSeatEnv)
			}
		case len(e.SeatEnv) != 0:
			t.Fatalf("%s carries a seat env %v", e.ID(), e.SeatEnv)
		default:
			other++
		}
	}
	if claude == 0 || other == 0 {
		t.Fatalf("entries: %d claude, %d other", claude, other)
	}
	text := Format(r)
	if !strings.Contains(text, "seat_env:") || strings.Count(text, "seat_env:") != claude {
		t.Fatalf("seat_env lines %d, claude entries %d", strings.Count(text, "seat_env:"), claude)
	}
	back, err := Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	for i, e := range back.Entries {
		if !slices.Equal(e.SeatEnv, r.Entries[i].SeatEnv) {
			t.Fatalf("%s: round trip %v, built %v", e.ID(), e.SeatEnv, r.Entries[i].SeatEnv)
		}
	}
}
