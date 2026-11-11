package task

import (
	"strings"
	"testing"
	"time"
)

func TestSeatHandleRefusesControlsAndInvalidUTF8(t *testing.T) {
	bad := []string{"dev\x1b]0;x\x07", "dev\u009b31m", "dev\x7f", "d\tev", string([]byte{'d', 'e', 'v', 0x9b})}
	for _, in := range bad {
		if _, err := SeatHandle(in); err == nil || !strings.Contains(err.Error(), "is not one plain name") {
			t.Fatalf("SeatHandle(%q) = %v", in, err)
		}
	}
	for in, want := range map[string]string{"dev": "dev", "dév": "dév", " dev\n": "dev"} {
		if got, err := SeatHandle(in); err != nil || got != want {
			t.Fatalf("SeatHandle(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestGrantRefusesControlHandleWithoutChangingTask(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 11, 6, 0, 0, 0, time.UTC)
	created, err := s.Add(AddInput{Title: "control holder", Class: "implement"}, now)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Grant(created.ID, "dev\x1b]0;x\x07", nil, time.Minute, now)
	if err == nil || !strings.Contains(err.Error(), "is not one plain name") {
		t.Fatalf("Grant error = %v", err)
	}
	if strings.IndexFunc(err.Error(), func(r rune) bool { return r < 0x20 && r != '\n' }) >= 0 {
		t.Fatalf("unsafe refusal %q", err)
	}
	got, err := s.Get(created.ID)
	if err != nil || got.State != StatePending || got.Lease != nil {
		t.Fatalf("task after refusal = %+v, %v", got, err)
	}
}
